// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/omkhar/workcell/internal/providerid"
)

const (
	defaultProviderBumpCodexRegistryURL     = "https://registry.npmjs.org/@openai%2fcodex"
	defaultProviderBumpCodexReleaseAPIURL   = "https://api.github.com/repos/openai/codex/releases/tags/rust-v%s"
	defaultProviderBumpCopilotReleaseAPIURL = "https://api.github.com/repos/github/copilot-cli/releases"
	defaultProviderBumpGeminiRegistryURL    = "https://registry.npmjs.org/@google%2fgemini-cli"
	defaultProviderBumpClaudeRegistryURL    = "https://registry.npmjs.org/@anthropic-ai%2fclaude-code"
	defaultProviderBumpClaudeReleaseRoot    = "https://storage.googleapis.com/claude-code-dist-86c565f3-f756-42ad-8dfa-d59b1c096819/claude-code-releases"
	providerBumpUserAgent                   = "workcell-provider-bump/1.0"
	// providerBumpHTTPTimeout caps every upstream metadata fetch (npm
	// registry, GitHub release API, GCS release manifest) end-to-end.
	providerBumpHTTPTimeout = 30 * time.Second
	// providerBumpMaxJSONBytes caps the success-path response body so a
	// misbehaving or hostile upstream cannot OOM the caller (upstream-refresh,
	// hosted-controls audit, release preflight). The error path already wraps
	// in LimitReader(4096); this mirrors that pattern for the success path.
	//
	// 16 MiB headroom on JSON because the npm registry response for
	// `@openai/codex` crossed 7.3 MiB in May 2026 (full version metadata
	// accumulates per-release tarball entries; the bound grows over
	// time). At 4 MiB the LimitReader truncated mid-stream and decode
	// failed with "unexpected EOF", silently breaking the scheduled
	// upstream-refresh workflow. The new ceiling still bounds memory
	// for hostile/misbehaving upstreams and tracks the projected growth
	// curve for the next ~5 years of npm metadata.
	providerBumpMaxJSONBytes = 16 << 20 // 16 MiB
	copilotReleaseMaxPages   = 10
)

var (
	errReleaseAssetNotFound       = errors.New("release asset not found")
	stableVersionPattern          = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
	geminiDependencyPattern       = regexp.MustCompile(`(?m)("(@google/gemini-cli)"\s*:\s*")[^"]+(")`)
	claudeVersionLinePattern      = regexp.MustCompile(`(?m)^ARG CLAUDE_VERSION=.*$`)
	codexVersionLinePattern       = regexp.MustCompile(`(?m)^ARG CODEX_VERSION=.*$`)
	copilotVersionLinePattern     = regexp.MustCompile(`(?m)^ARG COPILOT_VERSION=.*$`)
	claudeArmChecksumLinePattern  = regexp.MustCompile(`(?ms)(arm64\)\s+\\\s*CLAUDE_PLATFORM="linux-arm64";\s+\\\s*CLAUDE_SHA256=")[0-9a-f]{64}(";)`)
	claudeAmdChecksumLinePattern  = regexp.MustCompile(`(?ms)(amd64\)\s+\\\s*CLAUDE_PLATFORM="linux-x64";\s+\\\s*CLAUDE_SHA256=")[0-9a-f]{64}(";)`)
	codexArmChecksumLinePattern   = regexp.MustCompile(`(?ms)(arm64\)\s+\\\s*CODEX_ARCH="aarch64-unknown-linux-musl";\s+\\\s*CODEX_SHA256=")[0-9a-f]{64}(";)`)
	codexAmdChecksumLinePattern   = regexp.MustCompile(`(?ms)(amd64\)\s+\\\s*CODEX_ARCH="x86_64-unknown-linux-musl";\s+\\\s*CODEX_SHA256=")[0-9a-f]{64}(";)`)
	copilotArmChecksumLinePattern = regexp.MustCompile(`(?ms)(arm64\)\s+\\\s*COPILOT_PLATFORM="linux-arm64";\s+\\\s*COPILOT_SHA256=")[0-9a-f]{64}(";)`)
	copilotAmdChecksumLinePattern = regexp.MustCompile(`(?ms)(amd64\)\s+\\\s*COPILOT_PLATFORM="linux-x64";\s+\\\s*COPILOT_SHA256=")[0-9a-f]{64}(";)`)
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
	CopilotReleaseAPIURL  string
	GeminiRegistryURL     string
	ClaudeRegistryURL     string
	ClaudeReleaseRootURL  string
}

type npmRegistryMetadata struct {
	DistTags map[string]string `json:"dist-tags"`
	Time     map[string]string `json:"time"`
}

type githubReleaseAsset struct {
	Name   string `json:"name"`
	Digest string `json:"digest"`
}

type codexReleaseMetadata struct {
	TagName    string               `json:"tag_name"`
	Prerelease bool                 `json:"prerelease"`
	Assets     []githubReleaseAsset `json:"assets"`
}

type copilotReleaseMetadata struct {
	TagName     string               `json:"tag_name"`
	Prerelease  bool                 `json:"prerelease"`
	PublishedAt string               `json:"published_at"`
	Assets      []githubReleaseAsset `json:"assets"`
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
		CopilotReleaseAPIURL:  defaultProviderBumpCopilotReleaseAPIURL,
		GeminiRegistryURL:     defaultProviderBumpGeminiRegistryURL,
		ClaudeRegistryURL:     defaultProviderBumpClaudeRegistryURL,
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
	requiredProviders := []string{providerid.Claude, providerid.Codex, providerid.Copilot, providerid.Gemini}
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
			if provider != providerid.Claude {
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
	copilotVersion, err := ExtractDockerfileArg(dockerfilePath, "COPILOT_VERSION")
	if err != nil {
		return err
	}
	geminiVersion, err := extractGeminiVersion(providersPackageJSONPath)
	if err != nil {
		return err
	}
	if policy.Providers[providerid.Codex].Channel == "stable" && !stableVersionPattern.MatchString(codexVersion) {
		return fmt.Errorf("%s requires a stable Codex pin, found %q in %s", policyPath, codexVersion, dockerfilePath)
	}
	if policy.Providers[providerid.Claude].Channel == "stable" && !stableVersionPattern.MatchString(claudeVersion) {
		return fmt.Errorf("%s requires a stable Claude pin, found %q in %s", policyPath, claudeVersion, dockerfilePath)
	}
	if policy.Providers[providerid.Copilot].Channel == "stable" && !stableVersionPattern.MatchString(copilotVersion) {
		return fmt.Errorf("%s requires a stable Copilot pin, found %q in %s", policyPath, copilotVersion, dockerfilePath)
	}
	if policy.Providers[providerid.Gemini].Channel == "stable" && !stableVersionPattern.MatchString(geminiVersion) {
		return fmt.Errorf("%s requires a stable Gemini pin, found %q in %s", policyPath, geminiVersion, providersPackageJSONPath)
	}
	if err := enforceProviderMaxVersion(policyPath, providerid.Codex, "Codex", codexVersion, policy.Providers[providerid.Codex].MaxVersion, dockerfilePath); err != nil {
		return err
	}
	if err := enforceProviderMaxVersion(policyPath, providerid.Claude, "Claude", claudeVersion, policy.Providers[providerid.Claude].MaxVersion, dockerfilePath); err != nil {
		return err
	}
	if err := enforceProviderMaxVersion(policyPath, providerid.Copilot, "Copilot", copilotVersion, policy.Providers[providerid.Copilot].MaxVersion, dockerfilePath); err != nil {
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
		client = &http.Client{Timeout: providerBumpHTTPTimeout}
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
	copilotCurrent, err := ExtractDockerfileArg(dockerfilePath, "COPILOT_VERSION")
	if err != nil {
		return nil, err
	}
	geminiCurrent, err := extractGeminiVersion(providersPackageJSONPath)
	if err != nil {
		return nil, err
	}

	codexSelection, err := selectCodexStable(codexCurrent, cutoff, policy.Providers[providerid.Codex].MaxVersion, sources, client)
	if err != nil {
		return nil, err
	}
	claudeSelection, err := selectClaudeStable(
		claudeCurrent,
		cutoff,
		policy.Providers[providerid.Claude].MaxVersion,
		policy.Providers[providerid.Claude].ApprovedVersion,
		sources,
		client,
	)
	if err != nil {
		return nil, err
	}
	copilotSelection, err := selectCopilotStable(copilotCurrent, cutoff, policy.Providers[providerid.Copilot].MaxVersion, sources, client)
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
		HasChanges:   codexSelection.Changed || claudeSelection.Changed || copilotSelection.Changed || geminiSelection.Changed,
		Providers: map[string]ProviderBumpSelection{
			providerid.Codex:   codexSelection,
			providerid.Claude:  claudeSelection,
			providerid.Copilot: copilotSelection,
			providerid.Gemini:  geminiSelection,
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
	codexPlan, ok := plan.Providers[providerid.Codex]
	if !ok {
		return fmt.Errorf("%s does not contain a codex provider plan", planPath)
	}
	claudePlan, ok := plan.Providers[providerid.Claude]
	if !ok {
		return fmt.Errorf("%s does not contain a claude provider plan", planPath)
	}
	copilotPlan, ok := plan.Providers[providerid.Copilot]
	if !ok {
		return fmt.Errorf("%s does not contain a copilot provider plan", planPath)
	}
	geminiPlan, ok := plan.Providers[providerid.Gemini]
	if !ok {
		return fmt.Errorf("%s does not contain a gemini provider plan", planPath)
	}
	if !stableVersionPattern.MatchString(claudePlan.TargetVersion) {
		return fmt.Errorf("%s contains a non-stable Claude target version %q", planPath, claudePlan.TargetVersion)
	}
	if !stableVersionPattern.MatchString(codexPlan.TargetVersion) {
		return fmt.Errorf("%s contains a non-stable Codex target version %q", planPath, codexPlan.TargetVersion)
	}
	if !stableVersionPattern.MatchString(copilotPlan.TargetVersion) {
		return fmt.Errorf("%s contains a non-stable Copilot target version %q", planPath, copilotPlan.TargetVersion)
	}
	if !stableVersionPattern.MatchString(geminiPlan.TargetVersion) {
		return fmt.Errorf("%s contains a non-stable Gemini target version %q", planPath, geminiPlan.TargetVersion)
	}
	currentClaudeVersion, err := ExtractDockerfileArg(dockerfilePath, "CLAUDE_VERSION")
	if err != nil {
		return err
	}
	currentCodexVersion, err := ExtractDockerfileArg(dockerfilePath, "CODEX_VERSION")
	if err != nil {
		return err
	}
	currentCopilotVersion, err := ExtractDockerfileArg(dockerfilePath, "COPILOT_VERSION")
	if err != nil {
		return err
	}
	nativePlans := []nativeDockerfilePlan{
		{Provider: providerid.Claude, Label: "Claude", ArgName: "CLAUDE_VERSION", SHAName: "CLAUDE_SHA256", CurrentVersion: currentClaudeVersion, Selection: claudePlan},
		{Provider: providerid.Codex, Label: "Codex", ArgName: "CODEX_VERSION", SHAName: "CODEX_SHA256", CurrentVersion: currentCodexVersion, Selection: codexPlan},
		{Provider: providerid.Copilot, Label: "Copilot", ArgName: "COPILOT_VERSION", SHAName: "COPILOT_SHA256", CurrentVersion: currentCopilotVersion, Selection: copilotPlan},
	}
	if policyPath != "" {
		policy, err := LoadProviderBumpPolicy(policyPath)
		if err != nil {
			return err
		}
		if err := enforceProviderMaxVersion(policyPath, providerid.Codex, "Codex", codexPlan.TargetVersion, policy.Providers[providerid.Codex].MaxVersion, planPath); err != nil {
			return err
		}
		if err := enforceProviderMaxVersion(policyPath, providerid.Claude, "Claude", claudePlan.TargetVersion, policy.Providers[providerid.Claude].MaxVersion, planPath); err != nil {
			return err
		}
		if err := enforceProviderMaxVersion(policyPath, providerid.Copilot, "Copilot", copilotPlan.TargetVersion, policy.Providers[providerid.Copilot].MaxVersion, planPath); err != nil {
			return err
		}
	}
	for _, nativePlan := range nativePlans {
		if nativePlan.VersionChanged() {
			if err := requireProviderChecksums(planPath, nativePlan.Provider, nativePlan.Selection); err != nil {
				return err
			}
		} else if err := validateSuppliedProviderChecksums(planPath, nativePlan.Provider, nativePlan.Selection); err != nil {
			return err
		}
	}

	updatedDockerfile := dockerfileText
	updatedDockerfile = claudeVersionLinePattern.ReplaceAllString(updatedDockerfile, fmt.Sprintf("ARG CLAUDE_VERSION=%s", claudePlan.TargetVersion))
	updatedDockerfile = codexVersionLinePattern.ReplaceAllString(updatedDockerfile, fmt.Sprintf("ARG CODEX_VERSION=%s", codexPlan.TargetVersion))
	updatedDockerfile = copilotVersionLinePattern.ReplaceAllString(updatedDockerfile, fmt.Sprintf("ARG COPILOT_VERSION=%s", copilotPlan.TargetVersion))
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
	if checksum := copilotPlan.Checksums["arm64"]; checksum != "" {
		updatedDockerfile = copilotArmChecksumLinePattern.ReplaceAllString(updatedDockerfile, fmt.Sprintf(`${1}%s${2}`, checksum))
	}
	if checksum := copilotPlan.Checksums["amd64"]; checksum != "" {
		updatedDockerfile = copilotAmdChecksumLinePattern.ReplaceAllString(updatedDockerfile, fmt.Sprintf(`${1}%s${2}`, checksum))
	}
	for _, nativePlan := range nativePlans {
		if nativePlan.VersionChanged() {
			if err := verifyNativeDockerfilePlanApplied(updatedDockerfile, dockerfilePath, nativePlan); err != nil {
				return err
			}
		}
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

type nativeDockerfilePlan struct {
	Provider       string
	Label          string
	ArgName        string
	SHAName        string
	CurrentVersion string
	Selection      ProviderBumpSelection
}

func (p nativeDockerfilePlan) VersionChanged() bool {
	return p.Selection.TargetVersion != p.CurrentVersion
}

func verifyNativeDockerfilePlanApplied(updatedDockerfile, dockerfilePath string, plan nativeDockerfilePlan) error {
	if !strings.Contains(updatedDockerfile, fmt.Sprintf("ARG %s=%s", plan.ArgName, plan.Selection.TargetVersion)) {
		return fmt.Errorf("failed to update %s to %s in %s", plan.ArgName, plan.Selection.TargetVersion, dockerfilePath)
	}
	for _, arch := range []string{"arm64", "amd64"} {
		checksum := plan.Selection.Checksums[arch]
		checksumPattern := regexp.MustCompile(fmt.Sprintf(
			`(?ms)^\s*%s\)\s+\\\s*[A-Z_]+="[^"]+";\s+\\\s*%s="%s";`,
			regexp.QuoteMeta(arch),
			regexp.QuoteMeta(plan.SHAName),
			regexp.QuoteMeta(checksum),
		))
		if !checksumPattern.MatchString(updatedDockerfile) {
			return fmt.Errorf("failed to update %s %s checksum in %s", plan.Label, arch, dockerfilePath)
		}
	}
	return nil
}

func requireProviderChecksums(planPath, provider string, selection ProviderBumpSelection) error {
	for _, arch := range []string{"arm64", "amd64"} {
		checksum := selection.Checksums[arch]
		if !isHexDigest(checksum) {
			return fmt.Errorf("%s provider %s target version %s requires a valid %s sha256 checksum", planPath, provider, selection.TargetVersion, arch)
		}
	}
	return nil
}

func validateSuppliedProviderChecksums(planPath, provider string, selection ProviderBumpSelection) error {
	for _, arch := range []string{"arm64", "amd64"} {
		checksum := selection.Checksums[arch]
		if checksum != "" && !isHexDigest(checksum) {
			return fmt.Errorf("%s provider %s target version %s contains an invalid %s sha256 checksum", planPath, provider, selection.TargetVersion, arch)
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
		armDigest, armErr := releaseAssetDigest(release.Assets, "codex-aarch64-unknown-linux-musl.tar.gz")
		amdDigest, amdErr := releaseAssetDigest(release.Assets, "codex-x86_64-unknown-linux-musl.tar.gz")
		if armErr != nil || amdErr != nil {
			reasons := make([]string, 0, 2)
			if armErr != nil {
				reasons = append(reasons, armErr.Error())
			}
			if amdErr != nil {
				reasons = append(reasons, amdErr.Error())
			}
			if (armErr == nil || errors.Is(armErr, errReleaseAssetNotFound)) && (amdErr == nil || errors.Is(amdErr, errReleaseAssetNotFound)) {
				skipped = appendSkippedProviderRelease(skipped, candidate, "missing supported musl Linux release assets: "+strings.Join(reasons, "; "))
				continue
			}
			return ProviderBumpSelection{}, fmt.Errorf("codex release %s has invalid musl Linux asset metadata: %s", candidate.Raw, strings.Join(reasons, "; "))
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

func selectCopilotStable(currentVersion string, cutoff time.Time, maxVersion string, sources ProviderBumpSources, client *http.Client) (ProviderBumpSelection, error) {
	current, hasCurrentVersion := parseStableVersion(currentVersion)
	maxAllowed, hasMaxVersion := parseStableVersion(maxVersion)
	if maxVersion != "" && !hasMaxVersion {
		return ProviderBumpSelection{}, fmt.Errorf("provider.copilot.max_version must be an exact stable version, found %q", maxVersion)
	}
	if hasCurrentVersion && hasMaxVersion && compareStableVersions(current, maxAllowed) > 0 {
		return ProviderBumpSelection{}, fmt.Errorf("current Copilot version %s exceeds provider.copilot.max_version %s", currentVersion, maxVersion)
	}
	if sources.CopilotReleaseAPIURL == "" {
		return ProviderBumpSelection{
			Channel:        "stable",
			CurrentVersion: currentVersion,
			TargetVersion:  currentVersion,
		}, nil
	}
	releases, err := fetchCopilotReleases(client, sources.CopilotReleaseAPIURL)
	if err != nil {
		return ProviderBumpSelection{}, err
	}

	type releaseCandidate struct {
		version stableVersion
		release copilotReleaseMetadata
	}
	candidates := make([]releaseCandidate, 0, len(releases))
	skipped := make([]ProviderBumpSkippedRelease, 0)
	for _, release := range releases {
		rawVersion := strings.TrimPrefix(release.TagName, "v")
		version, ok := parseStableVersion(rawVersion)
		if !ok {
			continue
		}
		publishedAt, err := time.Parse(time.RFC3339, release.PublishedAt)
		if err != nil {
			return ProviderBumpSelection{}, fmt.Errorf("parse GitHub Copilot CLI release publish time for %s: %w", release.TagName, err)
		}
		version.Source = publishedAt.UTC()
		if release.Prerelease {
			skipped = appendSkippedProviderRelease(skipped, version, "GitHub release is marked prerelease")
			continue
		}
		if hasMaxVersion && compareStableVersions(version, maxAllowed) > 0 {
			skipped = appendSkippedProviderRelease(skipped, version, fmt.Sprintf("exceeds configured max_version %s", maxVersion))
			continue
		}
		if !cutoff.IsZero() && version.Source.After(cutoff) {
			continue
		}
		candidates = append(candidates, releaseCandidate{version: version, release: release})
	}
	sort.Slice(candidates, func(i, j int) bool {
		return compareStableVersions(candidates[i].version, candidates[j].version) > 0
	})
	sort.Slice(skipped, func(i, j int) bool {
		left, leftOK := parseStableVersion(skipped[i].Version)
		right, rightOK := parseStableVersion(skipped[j].Version)
		if leftOK && rightOK {
			return compareStableVersions(left, right) > 0
		}
		return skipped[i].Version > skipped[j].Version
	})

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
		if hasCurrentVersion && compareStableVersions(candidate.version, current) < 0 {
			break
		}
		if candidate.version.Raw == currentVersion {
			return currentSelection(candidate.version.Source), nil
		}
		armDigest, armErr := releaseAssetDigest(candidate.release.Assets, "copilot-linux-arm64.tar.gz")
		amdDigest, amdErr := releaseAssetDigest(candidate.release.Assets, "copilot-linux-x64.tar.gz")
		if armErr != nil || amdErr != nil {
			reasons := make([]string, 0, 2)
			if armErr != nil {
				reasons = append(reasons, armErr.Error())
			}
			if amdErr != nil {
				reasons = append(reasons, amdErr.Error())
			}
			if (armErr == nil || errors.Is(armErr, errReleaseAssetNotFound)) && (amdErr == nil || errors.Is(amdErr, errReleaseAssetNotFound)) {
				skipped = appendSkippedProviderRelease(skipped, candidate.version, "missing supported Linux release assets: "+strings.Join(reasons, "; "))
				continue
			}
			return ProviderBumpSelection{}, fmt.Errorf("copilot release %s has invalid Linux asset metadata: %s", candidate.version.Raw, strings.Join(reasons, "; "))
		}
		return ProviderBumpSelection{
			Channel:         "stable",
			CurrentVersion:  currentVersion,
			TargetVersion:   candidate.version.Raw,
			PublishedAt:     candidate.version.Source.Format(time.RFC3339),
			Changed:         candidate.version.Raw != currentVersion,
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

// claudeSelectionForCandidate fetches and validates the Claude release manifest
// for a candidate version and assembles its ProviderBumpSelection, returning the
// parsed build time so callers can apply the cool-off cutoff. Shared by the
// approved-version and newest-stable resolution paths in selectClaudeStable.
func claudeSelectionForCandidate(candidate stableVersion, currentVersion string, sources ProviderBumpSources, client *http.Client) (ProviderBumpSelection, time.Time, error) {
	manifestURL := fmt.Sprintf("%s/%s/manifest.json", sources.ClaudeReleaseRootURL, candidate.Raw)
	var manifest claudeManifest
	if err := fetchJSON(client, manifestURL, &manifest); err != nil {
		return ProviderBumpSelection{}, time.Time{}, err
	}
	if manifest.Version != candidate.Raw {
		return ProviderBumpSelection{}, time.Time{}, fmt.Errorf("claude manifest for %s reports version %q", candidate.Raw, manifest.Version)
	}
	buildTime, err := time.Parse(time.RFC3339, manifest.BuildDate)
	if err != nil {
		return ProviderBumpSelection{}, time.Time{}, fmt.Errorf("parse Claude manifest buildDate for %s: %w", candidate.Raw, err)
	}
	armChecksum := manifest.Platforms["linux-arm64"].Checksum
	amdChecksum := manifest.Platforms["linux-x64"].Checksum
	if armChecksum == "" || amdChecksum == "" {
		return ProviderBumpSelection{}, time.Time{}, fmt.Errorf("claude manifest for %s is missing Linux checksums", candidate.Raw)
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
	}, buildTime, nil
}

func selectClaudeStable(currentVersion string, cutoff time.Time, maxVersion string, approvedVersion string, sources ProviderBumpSources, client *http.Client) (ProviderBumpSelection, error) {
	candidates, _, err := stableCandidatesFromRegistry(sources.ClaudeRegistryURL, time.Time{}, maxVersion, client)
	if err != nil {
		return ProviderBumpSelection{}, err
	}
	maxAllowed, hasMaxVersion := parseStableVersion(maxVersion)
	current, hasCurrentVersion := parseStableVersion(currentVersion)
	approved, hasApprovedVersion := parseStableVersion(approvedVersion)
	currentPresentInCandidates := false
	for _, candidate := range candidates {
		if hasCurrentVersion && compareStableVersions(candidate, current) == 0 {
			currentPresentInCandidates = true
		}
	}
	if hasApprovedVersion && hasCurrentVersion && compareStableVersions(approved, current) <= 0 {
		hasApprovedVersion = false
	}
	if hasApprovedVersion {
		for _, candidate := range candidates {
			if compareStableVersions(candidate, approved) != 0 {
				continue
			}
			selection, _, err := claudeSelectionForCandidate(candidate, currentVersion, sources, client)
			if err != nil {
				return ProviderBumpSelection{}, err
			}
			return selection, nil
		}
		return ProviderBumpSelection{}, fmt.Errorf("claude approved_version %s is not present in the registry metadata", approvedVersion)
	}
	var selected *ProviderBumpSelection
	var selectedVersion stableVersion
	for _, candidate := range candidates {
		selection, buildTime, err := claudeSelectionForCandidate(candidate, currentVersion, sources, client)
		if err != nil {
			return ProviderBumpSelection{}, err
		}
		if buildTime.After(cutoff) {
			continue
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
				return ProviderBumpSelection{}, fmt.Errorf("current Claude version %s is not present in the registry metadata", currentVersion)
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
		return ProviderBumpSelection{}, fmt.Errorf("current Claude version %s is not present in the registry metadata", currentVersion)
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
		if !cutoff.IsZero() && publishedAt.After(cutoff) {
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

func releaseAssetDigest(assets []githubReleaseAsset, assetName string) (string, error) {
	for _, asset := range assets {
		if asset.Name != assetName {
			continue
		}
		digest := strings.TrimPrefix(asset.Digest, "sha256:")
		if !isHexDigest(digest) {
			return "", fmt.Errorf("release asset %s is missing a valid sha256 digest", assetName)
		}
		return digest, nil
	}
	return "", fmt.Errorf("%w: %s", errReleaseAssetNotFound, assetName)
}

func fetchCopilotReleases(client *http.Client, releaseAPIURL string) ([]copilotReleaseMetadata, error) {
	var releases []copilotReleaseMetadata
	nextURL, err := paginatedCopilotReleaseURL(releaseAPIURL, 1)
	if err != nil {
		return nil, err
	}

	for page := 1; page <= copilotReleaseMaxPages && nextURL != ""; page++ {
		var pageReleases []copilotReleaseMetadata
		header, err := fetchJSONResponse(client, nextURL, &pageReleases)
		if err != nil {
			return nil, err
		}
		releases = append(releases, pageReleases...)

		if linkNext := nextLinkURL(header.Get("Link")); linkNext != "" {
			nextURL = linkNext
			continue
		}
		if len(pageReleases) == 100 {
			nextURL, err = paginatedCopilotReleaseURL(releaseAPIURL, page+1)
			if err != nil {
				return nil, err
			}
			continue
		}
		nextURL = ""
	}

	if nextURL != "" {
		return nil, fmt.Errorf("fetch %s: exceeded Copilot release pagination cap of %d pages", releaseAPIURL, copilotReleaseMaxPages)
	}
	return releases, nil
}

func paginatedCopilotReleaseURL(releaseAPIURL string, page int) (string, error) {
	parsed, err := url.Parse(releaseAPIURL)
	if err != nil {
		return "", fmt.Errorf("parse Copilot release API URL %s: %w", releaseAPIURL, err)
	}
	values := parsed.Query()
	if values.Get("per_page") == "" {
		values.Set("per_page", "100")
	}
	values.Set("page", strconv.Itoa(page))
	parsed.RawQuery = values.Encode()
	return parsed.String(), nil
}

func nextLinkURL(linkHeader string) string {
	for _, part := range strings.Split(linkHeader, ",") {
		part = strings.TrimSpace(part)
		if !strings.Contains(part, `rel="next"`) && !strings.Contains(part, `rel=next`) {
			continue
		}
		start := strings.Index(part, "<")
		end := strings.Index(part, ">")
		if start >= 0 && end > start {
			return strings.TrimSpace(part[start+1 : end])
		}
	}
	return ""
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
	_, err := fetchJSONResponse(client, targetURL, target)
	return err
}

func fetchJSONResponse(client *http.Client, targetURL string, target any) (http.Header, error) {
	ctx, cancel := context.WithTimeout(context.Background(), providerBumpHTTPTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", targetURL, err)
	}
	req.Header.Set("User-Agent", providerBumpUserAgent)
	token, err := githubBearerTokenForURL(targetURL)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", targetURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("fetch %s: unexpected status %d: %s", targetURL, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, providerBumpMaxJSONBytes)).Decode(target); err != nil {
		return nil, fmt.Errorf("decode %s: %w", targetURL, err)
	}
	return resp.Header.Clone(), nil
}

func githubBearerTokenForURL(targetURL string) (string, error) {
	parsed, err := url.Parse(targetURL)
	if err != nil || parsed.Hostname() != "api.github.com" {
		return "", nil
	}
	if tokenFile := os.Getenv("WORKCELL_GITHUB_API_TOKEN_FILE"); tokenFile != "" {
		contents, err := os.ReadFile(tokenFile)
		if err != nil {
			return "", fmt.Errorf("read WORKCELL_GITHUB_API_TOKEN_FILE: %w", err)
		}
		token := strings.TrimSpace(string(contents))
		if token == "" {
			return "", fmt.Errorf("WORKCELL_GITHUB_API_TOKEN_FILE is empty")
		}
		return token, nil
	}
	for _, name := range []string{"WORKCELL_GITHUB_API_TOKEN", "GITHUB_TOKEN", "GH_TOKEN"} {
		if token := os.Getenv(name); token != "" {
			return token, nil
		}
	}
	return "", nil
}
