// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

const (
	defaultProviderBumpCodexRegistryURL   = "https://registry.npmjs.org/@openai%2fcodex"
	defaultProviderBumpCodexReleaseAPIURL = "https://api.github.com/repos/openai/codex/releases/tags/rust-v%s"
	defaultProviderBumpGeminiRegistryURL  = "https://registry.npmjs.org/@google%2fgemini-cli"
	defaultProviderBumpClaudeBucketURL    = "https://storage.googleapis.com/claude-code-dist-86c565f3-f756-42ad-8dfa-d59b1c096819?prefix=claude-code-releases/&delimiter=/"
	defaultProviderBumpClaudeReleaseRoot  = "https://storage.googleapis.com/claude-code-dist-86c565f3-f756-42ad-8dfa-d59b1c096819/claude-code-releases"
	providerBumpUserAgent                 = "workcell-provider-bump/1.0"
)

var (
	errReleaseAssetNotFound       = errors.New("release asset not found")
	stableVersionPattern          = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
	geminiDependencyPattern       = regexp.MustCompile(`(?m)("(@google/gemini-cli)"\s*:\s*")[^"]+(")`)
	claudeVersionLinePattern      = regexp.MustCompile(`(?m)^ARG CLAUDE_VERSION=.*$`)
	codexVersionLinePattern       = regexp.MustCompile(`(?m)^ARG CODEX_VERSION=.*$`)
	claudeArmChecksumLinePattern  = regexp.MustCompile(`(?ms)(arm64\)\s+\\\s*CLAUDE_PLATFORM="linux-arm64";\s+\\\s*CLAUDE_SHA256=")[0-9a-f]{64}(";)`)
	claudeAmdChecksumLinePattern  = regexp.MustCompile(`(?ms)(amd64\)\s+\\\s*CLAUDE_PLATFORM="linux-x64";\s+\\\s*CLAUDE_SHA256=")[0-9a-f]{64}(";)`)
	codexArmChecksumLinePattern   = regexp.MustCompile(`(?ms)(arm64\)\s+\\\s*CODEX_ARCH="aarch64-unknown-linux-gnu";\s+\\\s*CODEX_SHA256=")[0-9a-f]{64}(";)`)
	codexAmdChecksumLinePattern   = regexp.MustCompile(`(?ms)(amd64\)\s+\\\s*CODEX_ARCH="x86_64-unknown-linux-gnu";\s+\\\s*CODEX_SHA256=")[0-9a-f]{64}(";)`)
	claudeBucketVersionDirPattern = regexp.MustCompile(`^claude-code-releases/([0-9]+\.[0-9]+\.[0-9]+)/$`)
)

type ProviderBumpPolicy struct {
	Version      int                            `toml:"version"`
	CooloffHours int                            `toml:"cooloff_hours"`
	Providers    map[string]ProviderBumpChannel `toml:"provider"`
}

type ProviderBumpChannel struct {
	Channel         string `toml:"channel"`
	MaxVersion      string `toml:"max_version"`
	ApprovedVersion string `toml:"approved_version"`
}

type ProviderBumpPlan struct {
	GeneratedAt  string                           `json:"generated_at"`
	Cutoff       string                           `json:"cutoff"`
	CooloffHours int                              `json:"cooloff_hours"`
	HasChanges   bool                             `json:"has_changes"`
	Providers    map[string]ProviderBumpSelection `json:"providers"`
}

type ProviderBumpSelection struct {
	Channel         string                       `json:"channel"`
	CurrentVersion  string                       `json:"current_version"`
	TargetVersion   string                       `json:"target_version"`
	PublishedAt     string                       `json:"published_at,omitempty"`
	Changed         bool                         `json:"changed"`
	Checksums       map[string]string            `json:"checksums,omitempty"`
	SkippedReleases []ProviderBumpSkippedRelease `json:"skipped_releases,omitempty"`
}

type ProviderBumpSkippedRelease struct {
	Version     string `json:"version"`
	PublishedAt string `json:"published_at,omitempty"`
	Reason      string `json:"reason"`
}

type ProviderBumpSources struct {
	CodexRegistryURL      string
	CodexReleaseAPIURLFmt string
	GeminiRegistryURL     string
	ClaudeBucketURL       string
	ClaudeReleaseRootURL  string
}

type npmRegistryMetadata struct {
	DistTags map[string]string `json:"dist-tags"`
	Time     map[string]string `json:"time"`
}

type codexReleaseMetadata struct {
	TagName    string `json:"tag_name"`
	Prerelease bool   `json:"prerelease"`
	Assets     []struct {
		Name   string `json:"name"`
		Digest string `json:"digest"`
	} `json:"assets"`
}

type claudeBucketListing struct {
	CommonPrefixes []struct {
		Prefix string `xml:"Prefix"`
	} `xml:"CommonPrefixes"`
}

type claudeManifest struct {
	Version   string `json:"version"`
	BuildDate string `json:"buildDate"`
	Platforms map[string]struct {
		Checksum string `json:"checksum"`
	} `json:"platforms"`
}

type providersPackageJSON struct {
	Dependencies map[string]string `json:"dependencies"`
}

type stableVersion struct {
	Raw    string
	Major  int
	Minor  int
	Patch  int
	Source time.Time
}

func DefaultProviderBumpSources() ProviderBumpSources {
	return ProviderBumpSources{
		CodexRegistryURL:      defaultProviderBumpCodexRegistryURL,
		CodexReleaseAPIURLFmt: defaultProviderBumpCodexReleaseAPIURL,
		GeminiRegistryURL:     defaultProviderBumpGeminiRegistryURL,
		ClaudeBucketURL:       defaultProviderBumpClaudeBucketURL,
		ClaudeReleaseRootURL:  defaultProviderBumpClaudeReleaseRoot,
	}
}

func LoadProviderBumpPolicy(policyPath string) (ProviderBumpPolicy, error) {
	var policy ProviderBumpPolicy
	if _, err := toml.DecodeFile(policyPath, &policy); err != nil {
		return ProviderBumpPolicy{}, err
	}
	if policy.Version != 1 {
		return ProviderBumpPolicy{}, fmt.Errorf("%s must set version = 1", policyPath)
	}
	if policy.CooloffHours <= 0 {
		return ProviderBumpPolicy{}, fmt.Errorf("%s must set a positive cooloff_hours", policyPath)
	}
	requiredProviders := []string{"claude", "codex", "gemini"}
	for _, provider := range requiredProviders {
		spec, ok := policy.Providers[provider]
		if !ok {
			return ProviderBumpPolicy{}, fmt.Errorf("%s must define [provider.%s]", policyPath, provider)
		}
		if spec.Channel != "stable" {
			return ProviderBumpPolicy{}, fmt.Errorf("%s must pin provider.%s.channel to \"stable\"", policyPath, provider)
		}
		if spec.MaxVersion != "" {
			if _, ok := parseStableVersion(spec.MaxVersion); !ok {
				return ProviderBumpPolicy{}, fmt.Errorf("%s must pin provider.%s.max_version to an exact stable version", policyPath, provider)
			}
		}
		if spec.ApprovedVersion != "" {
			approved, ok := parseStableVersion(spec.ApprovedVersion)
			if !ok {
				return ProviderBumpPolicy{}, fmt.Errorf("%s must pin provider.%s.approved_version to an exact stable version", policyPath, provider)
			}
			if provider != "claude" {
				return ProviderBumpPolicy{}, fmt.Errorf("%s only supports provider.claude.approved_version today", policyPath)
			}
			if spec.MaxVersion != "" {
				maxAllowed, ok := parseStableVersion(spec.MaxVersion)
				if !ok {
					return ProviderBumpPolicy{}, fmt.Errorf("%s must pin provider.%s.max_version to an exact stable version", policyPath, provider)
				}
				if compareStableVersions(approved, maxAllowed) > 0 {
					return ProviderBumpPolicy{}, fmt.Errorf("%s requires provider.%s.approved_version <= %s", policyPath, provider, spec.MaxVersion)
				}
			}
		}
	}
	if len(policy.Providers) != len(requiredProviders) {
		return ProviderBumpPolicy{}, fmt.Errorf("%s must define exactly %d providers", policyPath, len(requiredProviders))
	}
	return policy, nil
}

func CheckProviderBumpPolicy(policyPath, dockerfilePath, providersPackageJSONPath string) error {
	policy, err := LoadProviderBumpPolicy(policyPath)
	if err != nil {
		return err
	}
	codexVersion, err := ExtractDockerfileArg(dockerfilePath, "CODEX_VERSION")
	if err != nil {
		return err
	}
	claudeVersion, err := ExtractDockerfileArg(dockerfilePath, "CLAUDE_VERSION")
	if err != nil {
		return err
	}
	geminiVersion, err := extractGeminiVersion(providersPackageJSONPath)
	if err != nil {
		return err
	}
	if policy.Providers["codex"].Channel == "stable" && !stableVersionPattern.MatchString(codexVersion) {
		return fmt.Errorf("%s requires a stable Codex pin, found %q in %s", policyPath, codexVersion, dockerfilePath)
	}
	if policy.Providers["claude"].Channel == "stable" && !stableVersionPattern.MatchString(claudeVersion) {
		return fmt.Errorf("%s requires a stable Claude pin, found %q in %s", policyPath, claudeVersion, dockerfilePath)
	}
	if policy.Providers["gemini"].Channel == "stable" && !stableVersionPattern.MatchString(geminiVersion) {
		return fmt.Errorf("%s requires a stable Gemini pin, found %q in %s", policyPath, geminiVersion, providersPackageJSONPath)
	}
	if err := enforceProviderMaxVersion(policyPath, "codex", "Codex", codexVersion, policy.Providers["codex"].MaxVersion, dockerfilePath); err != nil {
		return err
	}
	if err := enforceProviderMaxVersion(policyPath, "claude", "Claude", claudeVersion, policy.Providers["claude"].MaxVersion, dockerfilePath); err != nil {
		return err
	}
	return nil
}

func PlanProviderBumps(policyPath, dockerfilePath, providersPackageJSONPath string, now time.Time, sources ProviderBumpSources, client *http.Client) (*ProviderBumpPlan, error) {
	policy, err := LoadProviderBumpPolicy(policyPath)
	if err != nil {
		return nil, err
	}
	if client == nil {
		client = http.DefaultClient
	}
	cutoff := now.UTC().Add(-time.Duration(policy.CooloffHours) * time.Hour)

	codexCurrent, err := ExtractDockerfileArg(dockerfilePath, "CODEX_VERSION")
	if err != nil {
		return nil, err
	}
	claudeCurrent, err := ExtractDockerfileArg(dockerfilePath, "CLAUDE_VERSION")
	if err != nil {
		return nil, err
	}
	geminiCurrent, err := extractGeminiVersion(providersPackageJSONPath)
	if err != nil {
		return nil, err
	}

	codexSelection, err := selectCodexStable(codexCurrent, cutoff, policy.Providers["codex"].MaxVersion, sources, client)
	if err != nil {
		return nil, err
	}
	claudeSelection, err := selectClaudeStable(
		claudeCurrent,
		cutoff,
		policy.Providers["claude"].MaxVersion,
		policy.Providers["claude"].ApprovedVersion,
		sources,
		client,
	)
	if err != nil {
		return nil, err
	}
	geminiSelection, err := selectGeminiStable(geminiCurrent, cutoff, sources, client)
	if err != nil {
		return nil, err
	}

	plan := &ProviderBumpPlan{
		GeneratedAt:  now.UTC().Format(time.RFC3339),
		Cutoff:       cutoff.Format(time.RFC3339),
		CooloffHours: policy.CooloffHours,
		HasChanges:   codexSelection.Changed || claudeSelection.Changed || geminiSelection.Changed,
		Providers: map[string]ProviderBumpSelection{
			"codex":  codexSelection,
			"claude": claudeSelection,
			"gemini": geminiSelection,
		},
	}
	return plan, nil
}

func ApplyProviderBumpPlan(planPath, policyPath, dockerfilePath, providersPackageJSONPath string) error {
	content, err := os.ReadFile(planPath)
	if err != nil {
		return err
	}
	var plan ProviderBumpPlan
	if err := json.Unmarshal(content, &plan); err != nil {
		return err
	}

	dockerfileText, err := readText(dockerfilePath)
	if err != nil {
		return err
	}
	codexPlan, ok := plan.Providers["codex"]
	if !ok {
		return fmt.Errorf("%s does not contain a codex provider plan", planPath)
	}
	claudePlan, ok := plan.Providers["claude"]
	if !ok {
		return fmt.Errorf("%s does not contain a claude provider plan", planPath)
	}
	geminiPlan, ok := plan.Providers["gemini"]
	if !ok {
		return fmt.Errorf("%s does not contain a gemini provider plan", planPath)
	}
	if !stableVersionPattern.MatchString(claudePlan.TargetVersion) {
		return fmt.Errorf("%s contains a non-stable Claude target version %q", planPath, claudePlan.TargetVersion)
	}
	if !stableVersionPattern.MatchString(codexPlan.TargetVersion) {
		return fmt.Errorf("%s contains a non-stable Codex target version %q", planPath, codexPlan.TargetVersion)
	}
	if !stableVersionPattern.MatchString(geminiPlan.TargetVersion) {
		return fmt.Errorf("%s contains a non-stable Gemini target version %q", planPath, geminiPlan.TargetVersion)
	}
	if policyPath != "" {
		policy, err := LoadProviderBumpPolicy(policyPath)
		if err != nil {
			return err
		}
		if err := enforceProviderMaxVersion(policyPath, "codex", "Codex", codexPlan.TargetVersion, policy.Providers["codex"].MaxVersion, planPath); err != nil {
			return err
		}
		if err := enforceProviderMaxVersion(policyPath, "claude", "Claude", claudePlan.TargetVersion, policy.Providers["claude"].MaxVersion, planPath); err != nil {
			return err
		}
	}

	updatedDockerfile := dockerfileText
	updatedDockerfile = claudeVersionLinePattern.ReplaceAllString(updatedDockerfile, fmt.Sprintf("ARG CLAUDE_VERSION=%s", claudePlan.TargetVersion))
	updatedDockerfile = codexVersionLinePattern.ReplaceAllString(updatedDockerfile, fmt.Sprintf("ARG CODEX_VERSION=%s", codexPlan.TargetVersion))
	if checksum := claudePlan.Checksums["arm64"]; checksum != "" {
		updatedDockerfile = claudeArmChecksumLinePattern.ReplaceAllString(updatedDockerfile, fmt.Sprintf(`${1}%s${2}`, checksum))
	}
	if checksum := claudePlan.Checksums["amd64"]; checksum != "" {
		updatedDockerfile = claudeAmdChecksumLinePattern.ReplaceAllString(updatedDockerfile, fmt.Sprintf(`${1}%s${2}`, checksum))
	}
	if checksum := codexPlan.Checksums["arm64"]; checksum != "" {
		updatedDockerfile = codexArmChecksumLinePattern.ReplaceAllString(updatedDockerfile, fmt.Sprintf(`${1}%s${2}`, checksum))
	}
	if checksum := codexPlan.Checksums["amd64"]; checksum != "" {
		updatedDockerfile = codexAmdChecksumLinePattern.ReplaceAllString(updatedDockerfile, fmt.Sprintf(`${1}%s${2}`, checksum))
	}
	if updatedDockerfile != dockerfileText {
		if err := os.WriteFile(dockerfilePath, []byte(updatedDockerfile), 0o644); err != nil {
			return err
		}
	}

	packageJSONText, err := readText(providersPackageJSONPath)
	if err != nil {
		return err
	}
	updatedPackageJSON := geminiDependencyPattern.ReplaceAllString(packageJSONText, fmt.Sprintf(`${1}%s${3}`, geminiPlan.TargetVersion))
	if updatedPackageJSON != packageJSONText {
		if err := os.WriteFile(providersPackageJSONPath, []byte(updatedPackageJSON), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func extractGeminiVersion(providersPackageJSONPath string) (string, error) {
	var pkg providersPackageJSON
	if err := readJSONFile(providersPackageJSONPath, &pkg); err != nil {
		return "", err
	}
	version := pkg.Dependencies["@google/gemini-cli"]
	if version == "" {
		return "", fmt.Errorf("%s must pin @google/gemini-cli in dependencies", providersPackageJSONPath)
	}
	return version, nil
}

func selectCodexStable(currentVersion string, cutoff time.Time, maxVersion string, sources ProviderBumpSources, client *http.Client) (ProviderBumpSelection, error) {
	candidates, skipped, err := stableCandidatesFromRegistry(sources.CodexRegistryURL, cutoff, maxVersion, client)
	if err != nil {
		return ProviderBumpSelection{}, err
	}
	current, hasCurrentVersion := parseStableVersion(currentVersion)
	maxAllowed, hasMaxVersion := parseStableVersion(maxVersion)
	if maxVersion != "" && !hasMaxVersion {
		return ProviderBumpSelection{}, fmt.Errorf("provider.codex.max_version must be an exact stable version, found %q", maxVersion)
	}
	if hasCurrentVersion && hasMaxVersion && compareStableVersions(current, maxAllowed) > 0 {
		return ProviderBumpSelection{}, fmt.Errorf("current Codex version %s exceeds provider.codex.max_version %s", currentVersion, maxVersion)
	}
	currentSelection := func(publishedAt time.Time) ProviderBumpSelection {
		selection := ProviderBumpSelection{
			Channel:         "stable",
			CurrentVersion:  currentVersion,
			TargetVersion:   currentVersion,
			SkippedReleases: skipped,
		}
		if !publishedAt.IsZero() {
			selection.PublishedAt = publishedAt.Format(time.RFC3339)
		}
		return selection
	}
	if len(candidates) == 0 {
		return currentSelection(time.Time{}), nil
	}
	for _, candidate := range candidates {
		if hasCurrentVersion && compareStableVersions(candidate, current) < 0 {
			break
		}
		if candidate.Raw == currentVersion {
			return currentSelection(candidate.Source), nil
		}
		releaseURL := fmt.Sprintf(sources.CodexReleaseAPIURLFmt, candidate.Raw)
		var release codexReleaseMetadata
		if err := fetchJSON(client, releaseURL, &release); err != nil {
			return ProviderBumpSelection{}, err
		}
		if release.Prerelease {
			skipped = appendSkippedProviderRelease(skipped, candidate, "GitHub release is marked prerelease")
			continue
		}
		armDigest, armErr := releaseAssetDigest(release, "codex-aarch64-unknown-linux-gnu.tar.gz")
		amdDigest, amdErr := releaseAssetDigest(release, "codex-x86_64-unknown-linux-gnu.tar.gz")
		if armErr != nil || amdErr != nil {
			if (armErr == nil || errors.Is(armErr, errReleaseAssetNotFound)) && (amdErr == nil || errors.Is(amdErr, errReleaseAssetNotFound)) {
				reasons := make([]string, 0, 2)
				if armErr != nil {
					reasons = append(reasons, armErr.Error())
				}
				if amdErr != nil {
					reasons = append(reasons, amdErr.Error())
				}
				skipped = appendSkippedProviderRelease(skipped, candidate, "missing supported GNU Linux release assets: "+strings.Join(reasons, "; "))
				continue
			}
			reasons := make([]string, 0, 2)
			if armErr != nil {
				reasons = append(reasons, armErr.Error())
			}
			if amdErr != nil {
				reasons = append(reasons, amdErr.Error())
			}
			return ProviderBumpSelection{}, fmt.Errorf("codex release %s has invalid GNU Linux asset metadata: %s", candidate.Raw, strings.Join(reasons, "; "))
		}
		return ProviderBumpSelection{
			Channel:         "stable",
			CurrentVersion:  currentVersion,
			TargetVersion:   candidate.Raw,
			PublishedAt:     candidate.Source.Format(time.RFC3339),
			Changed:         candidate.Raw != currentVersion,
			SkippedReleases: skipped,
			Checksums: map[string]string{
				"arm64": armDigest,
				"amd64": amdDigest,
			},
		}, nil
	}
	return currentSelection(time.Time{}), nil
}

func selectGeminiStable(currentVersion string, cutoff time.Time, sources ProviderBumpSources, client *http.Client) (ProviderBumpSelection, error) {
	version, publishedAt, err := selectNewestStableFromRegistry(sources.GeminiRegistryURL, cutoff, client)
	if err != nil {
		return ProviderBumpSelection{}, err
	}
	if version == "" {
		return ProviderBumpSelection{
			Channel:        "stable",
			CurrentVersion: currentVersion,
			TargetVersion:  currentVersion,
		}, nil
	}
	return ProviderBumpSelection{
		Channel:        "stable",
		CurrentVersion: currentVersion,
		TargetVersion:  version,
		PublishedAt:    publishedAt.Format(time.RFC3339),
		Changed:        version != currentVersion,
	}, nil
}

func selectClaudeStable(currentVersion string, cutoff time.Time, maxVersion string, approvedVersion string, sources ProviderBumpSources, client *http.Client) (ProviderBumpSelection, error) {
	var listing claudeBucketListing
	if err := fetchXML(client, sources.ClaudeBucketURL, &listing); err != nil {
		return ProviderBumpSelection{}, err
	}
	maxAllowed, hasMaxVersion := parseStableVersion(maxVersion)
	current, hasCurrentVersion := parseStableVersion(currentVersion)
	approved, hasApprovedVersion := parseStableVersion(approvedVersion)
	candidates := make([]stableVersion, 0, len(listing.CommonPrefixes))
	currentPresentInCandidates := false
	for _, prefix := range listing.CommonPrefixes {
		match := claudeBucketVersionDirPattern.FindStringSubmatch(prefix.Prefix)
		if match == nil {
			continue
		}
		version, ok := parseStableVersion(match[1])
		if !ok {
			continue
		}
		if hasMaxVersion && compareStableVersions(version, maxAllowed) > 0 {
			continue
		}
		if hasCurrentVersion && compareStableVersions(version, current) == 0 {
			currentPresentInCandidates = true
		}
		candidates = append(candidates, version)
	}
	sortStableVersionsDesc(candidates)
	if hasApprovedVersion && hasCurrentVersion && compareStableVersions(approved, current) <= 0 {
		hasApprovedVersion = false
	}
	if hasApprovedVersion {
		for _, candidate := range candidates {
			if compareStableVersions(candidate, approved) != 0 {
				continue
			}
			manifestURL := fmt.Sprintf("%s/%s/manifest.json", sources.ClaudeReleaseRootURL, candidate.Raw)
			var manifest claudeManifest
			if err := fetchJSON(client, manifestURL, &manifest); err != nil {
				return ProviderBumpSelection{}, err
			}
			buildTime, err := time.Parse(time.RFC3339, manifest.BuildDate)
			if err != nil {
				return ProviderBumpSelection{}, fmt.Errorf("parse Claude manifest buildDate for %s: %w", candidate.Raw, err)
			}
			armChecksum := manifest.Platforms["linux-arm64"].Checksum
			amdChecksum := manifest.Platforms["linux-x64"].Checksum
			if armChecksum == "" || amdChecksum == "" {
				return ProviderBumpSelection{}, fmt.Errorf("claude manifest for %s is missing Linux checksums", candidate.Raw)
			}
			return ProviderBumpSelection{
				Channel:        "stable",
				CurrentVersion: currentVersion,
				TargetVersion:  candidate.Raw,
				PublishedAt:    buildTime.UTC().Format(time.RFC3339),
				Changed:        candidate.Raw != currentVersion,
				Checksums: map[string]string{
					"arm64": armChecksum,
					"amd64": amdChecksum,
				},
			}, nil
		}
		return ProviderBumpSelection{}, fmt.Errorf("claude approved_version %s is not present in the release bucket listing", approvedVersion)
	}
	var selected *ProviderBumpSelection
	var selectedVersion stableVersion
	for _, candidate := range candidates {
		manifestURL := fmt.Sprintf("%s/%s/manifest.json", sources.ClaudeReleaseRootURL, candidate.Raw)
		var manifest claudeManifest
		if err := fetchJSON(client, manifestURL, &manifest); err != nil {
			return ProviderBumpSelection{}, err
		}
		buildTime, err := time.Parse(time.RFC3339, manifest.BuildDate)
		if err != nil {
			return ProviderBumpSelection{}, fmt.Errorf("parse Claude manifest buildDate for %s: %w", candidate.Raw, err)
		}
		if buildTime.After(cutoff) {
			continue
		}
		armChecksum := manifest.Platforms["linux-arm64"].Checksum
		amdChecksum := manifest.Platforms["linux-x64"].Checksum
		if armChecksum == "" || amdChecksum == "" {
			return ProviderBumpSelection{}, fmt.Errorf("claude manifest for %s is missing Linux checksums", candidate.Raw)
		}
		selection := ProviderBumpSelection{
			Channel:        "stable",
			CurrentVersion: currentVersion,
			TargetVersion:  candidate.Raw,
			PublishedAt:    buildTime.UTC().Format(time.RFC3339),
			Changed:        candidate.Raw != currentVersion,
			Checksums: map[string]string{
				"arm64": armChecksum,
				"amd64": amdChecksum,
			},
		}
		selected = &selection
		selectedVersion = candidate
		break
	}
	if selected != nil {
		if hasCurrentVersion && compareStableVersions(selectedVersion, current) < 0 {
			if hasMaxVersion && compareStableVersions(current, maxAllowed) > 0 {
				return ProviderBumpSelection{}, fmt.Errorf("current Claude version %s exceeds provider.claude.max_version %s", currentVersion, maxVersion)
			}
			if !currentPresentInCandidates {
				return ProviderBumpSelection{}, fmt.Errorf("current Claude version %s is not present in the release bucket listing", currentVersion)
			}
			return ProviderBumpSelection{
				Channel:        "stable",
				CurrentVersion: currentVersion,
				TargetVersion:  currentVersion,
			}, nil
		}
		return *selected, nil
	}
	if hasCurrentVersion && hasMaxVersion && compareStableVersions(current, maxAllowed) > 0 {
		return ProviderBumpSelection{}, fmt.Errorf("current Claude version %s exceeds provider.claude.max_version %s", currentVersion, maxVersion)
	}
	if hasCurrentVersion && len(candidates) > 0 && !currentPresentInCandidates && compareStableVersions(current, candidates[0]) > 0 {
		return ProviderBumpSelection{}, fmt.Errorf("current Claude version %s is not present in the release bucket listing", currentVersion)
	}
	return ProviderBumpSelection{
		Channel:        "stable",
		CurrentVersion: currentVersion,
		TargetVersion:  currentVersion,
	}, nil
}

func selectNewestStableFromRegistry(registryURL string, cutoff time.Time, client *http.Client) (string, time.Time, error) {
	candidates, _, err := stableCandidatesFromRegistry(registryURL, cutoff, "", client)
	if err != nil {
		return "", time.Time{}, err
	}
	if len(candidates) == 0 {
		return "", time.Time{}, nil
	}
	return candidates[0].Raw, candidates[0].Source, nil
}

func stableCandidatesFromRegistry(registryURL string, cutoff time.Time, maxVersion string, client *http.Client) ([]stableVersion, []ProviderBumpSkippedRelease, error) {
	var metadata npmRegistryMetadata
	if err := fetchJSON(client, registryURL, &metadata); err != nil {
		return nil, nil, err
	}
	latestRaw := metadata.DistTags["latest"]
	latestVersion, ok := parseStableVersion(latestRaw)
	if !ok {
		return nil, nil, fmt.Errorf("%s dist-tags.latest must resolve to a stable version, found %q", registryURL, latestRaw)
	}
	maxAllowed, hasMaxVersion := parseStableVersion(maxVersion)
	if maxVersion != "" && !hasMaxVersion {
		return nil, nil, fmt.Errorf("max_version must be an exact stable version, found %q", maxVersion)
	}
	candidates := make([]stableVersion, 0, len(metadata.Time))
	skipped := make([]ProviderBumpSkippedRelease, 0)
	for rawVersion, publishedRaw := range metadata.Time {
		version, ok := parseStableVersion(rawVersion)
		if !ok {
			continue
		}
		if compareStableVersions(version, latestVersion) > 0 {
			continue
		}
		publishedAt, err := time.Parse(time.RFC3339, publishedRaw)
		if err != nil {
			return nil, nil, fmt.Errorf("parse publish time for %s: %w", rawVersion, err)
		}
		version.Source = publishedAt.UTC()
		if hasMaxVersion && compareStableVersions(version, maxAllowed) > 0 {
			skipped = appendSkippedProviderRelease(skipped, version, fmt.Sprintf("exceeds configured max_version %s", maxVersion))
			continue
		}
		if publishedAt.After(cutoff) {
			continue
		}
		candidates = append(candidates, version)
	}
	sortStableVersionsDesc(candidates)
	sort.Slice(skipped, func(i, j int) bool {
		left, leftOK := parseStableVersion(skipped[i].Version)
		right, rightOK := parseStableVersion(skipped[j].Version)
		if leftOK && rightOK {
			return compareStableVersions(left, right) > 0
		}
		return skipped[i].Version > skipped[j].Version
	})
	return candidates, skipped, nil
}

func appendSkippedProviderRelease(skipped []ProviderBumpSkippedRelease, version stableVersion, reason string) []ProviderBumpSkippedRelease {
	entry := ProviderBumpSkippedRelease{
		Version: version.Raw,
		Reason:  reason,
	}
	if !version.Source.IsZero() {
		entry.PublishedAt = version.Source.Format(time.RFC3339)
	}
	return append(skipped, entry)
}

func releaseAssetDigest(release codexReleaseMetadata, assetName string) (string, error) {
	for _, asset := range release.Assets {
		if asset.Name != assetName {
			continue
		}
		digest := strings.TrimPrefix(asset.Digest, "sha256:")
		if !hexDigestPattern.MatchString(digest) {
			return "", fmt.Errorf("release asset %s is missing a valid sha256 digest", assetName)
		}
		return digest, nil
	}
	return "", fmt.Errorf("%w: %s", errReleaseAssetNotFound, assetName)
}

func parseStableVersion(raw string) (stableVersion, bool) {
	if !stableVersionPattern.MatchString(raw) {
		return stableVersion{}, false
	}
	var version stableVersion
	if _, err := fmt.Sscanf(raw, "%d.%d.%d", &version.Major, &version.Minor, &version.Patch); err != nil {
		return stableVersion{}, false
	}
	version.Raw = raw
	return version, true
}

func sortStableVersionsDesc(versions []stableVersion) {
	sort.Slice(versions, func(i, j int) bool {
		return compareStableVersions(versions[i], versions[j]) > 0
	})
}

func enforceProviderMaxVersion(policyPath, providerID, displayName, version, maxVersion, sourcePath string) error {
	if maxVersion == "" {
		return nil
	}
	current, ok := parseStableVersion(version)
	if !ok {
		return fmt.Errorf("%s requires a stable %s pin, found %q in %s", policyPath, displayName, version, sourcePath)
	}
	maxAllowed, ok := parseStableVersion(maxVersion)
	if !ok {
		return fmt.Errorf("%s must pin provider.%s.max_version to an exact stable version", policyPath, providerID)
	}
	if compareStableVersions(current, maxAllowed) > 0 {
		return fmt.Errorf("%s requires %s <= %s, found %q in %s", policyPath, displayName, maxVersion, version, sourcePath)
	}
	return nil
}

func compareStableVersions(left, right stableVersion) int {
	if left.Major != right.Major {
		return left.Major - right.Major
	}
	if left.Minor != right.Minor {
		return left.Minor - right.Minor
	}
	return left.Patch - right.Patch
}

func fetchJSON(client *http.Client, targetURL string, target any) error {
	req, err := http.NewRequest(http.MethodGet, targetURL, nil)
	if err != nil {
		return fmt.Errorf("fetch %s: %w", targetURL, err)
	}
	req.Header.Set("User-Agent", providerBumpUserAgent)
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("fetch %s: %w", targetURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("fetch %s: unexpected status %d: %s", targetURL, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
		return fmt.Errorf("decode %s: %w", targetURL, err)
	}
	return nil
}

func fetchXML(client *http.Client, targetURL string, target any) error {
	req, err := http.NewRequest(http.MethodGet, targetURL, nil)
	if err != nil {
		return fmt.Errorf("fetch %s: %w", targetURL, err)
	}
	req.Header.Set("User-Agent", providerBumpUserAgent)
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("fetch %s: %w", targetURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("fetch %s: unexpected status %d: %s", targetURL, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if err := xml.NewDecoder(resp.Body).Decode(target); err != nil {
		return fmt.Errorf("decode %s: %w", targetURL, err)
	}
	return nil
}
