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
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/omkhar/workcell/internal/providerid"
	"github.com/omkhar/workcell/internal/rootio"
)

const (
	defaultProviderBumpCodexRegistryURL     = "https://registry.npmjs.org/@openai%2fcodex"
	defaultProviderBumpCodexReleaseAPIURL   = "https://api.github.com/repos/openai/codex/releases/tags/rust-v%s"
	defaultProviderBumpCopilotReleaseAPIURL = "https://api.github.com/repos/github/copilot-cli/releases"
	defaultProviderBumpGeminiRegistryURL    = "https://registry.npmjs.org/@google%2fgemini-cli"
	defaultProviderBumpClaudeRegistryURL    = "https://registry.npmjs.org/@anthropic-ai%2fclaude-code"
	defaultProviderBumpNPMRegistryURL       = "https://registry.npmjs.org"
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
	// providerBumpMaxNPMStreamBytes caps the bytes read from an npm packument.
	// The packument is streamed, but the token walk still holds each scalar
	// literal whole, so this cap also bounds the largest literal held. The
	// full @openai/codex document was 17.5 MB in October 2026 and still grows.
	providerBumpMaxNPMStreamBytes = 64 << 20 // 64 MiB
	copilotReleaseMaxPages        = 10
)

// buildStampedVersionExpr is the 0.0.<build>-g<commit> version form.
const buildStampedVersionExpr = `0\.0\.[0-9]+-g[0-9a-f]+`

var (
	errReleaseAssetNotFound = errors.New("release asset not found")
	stableVersionPattern    = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
	// buildStampedVersionPattern is the npm version form of a CLI that
	// publishes each build as 0.0.<build>-g<commit>, for example @ampcode/cli.
	// Its dist-tags.latest has the same form, and the build number orders
	// the releases.
	buildStampedVersionPattern              = regexp.MustCompile(`^` + buildStampedVersionExpr + `$`)
	geminiDependencyPattern                 = regexp.MustCompile(`(?m)("(@google/gemini-cli)"\s*:\s*")[^"]+(")`)
	claudeVersionLinePattern                = regexp.MustCompile(`(?m)^ARG CLAUDE_VERSION=.*$`)
	codexVersionLinePattern                 = regexp.MustCompile(`(?m)^ARG CODEX_VERSION=.*$`)
	copilotVersionLinePattern               = regexp.MustCompile(`(?m)^ARG COPILOT_VERSION=.*$`)
	claudeArmChecksumLinePattern            = regexp.MustCompile(`(?ms)(arm64\)\s+\\\s*CLAUDE_PLATFORM="linux-arm64";\s+\\\s*CLAUDE_SHA256=")[0-9a-f]{64}(";)`)
	claudeAmdChecksumLinePattern            = regexp.MustCompile(`(?ms)(amd64\)\s+\\\s*CLAUDE_PLATFORM="linux-x64";\s+\\\s*CLAUDE_SHA256=")[0-9a-f]{64}(";)`)
	codexArmChecksumLinePattern             = regexp.MustCompile(`(?ms)(arm64\)\s+\\\s*CODEX_ARCH="aarch64-unknown-linux-musl";\s+\\\s*CODEX_SHA256=")[0-9a-f]{64}(";)`)
	codexAmdChecksumLinePattern             = regexp.MustCompile(`(?ms)(amd64\)\s+\\\s*CODEX_ARCH="x86_64-unknown-linux-musl";\s+\\\s*CODEX_SHA256=")[0-9a-f]{64}(";)`)
	codexCodeModeHostArmChecksumLinePattern = regexp.MustCompile(`(?ms)(arm64\)\s+\\\s*CODEX_ARCH="aarch64-unknown-linux-musl";\s+\\\s*CODEX_SHA256="[0-9a-f]{64}";\s+\\\s*CODEX_CODE_MODE_HOST_SHA256=")[0-9a-f]{64}(";)`)
	codexCodeModeHostAmdChecksumLinePattern = regexp.MustCompile(`(?ms)(amd64\)\s+\\\s*CODEX_ARCH="x86_64-unknown-linux-musl";\s+\\\s*CODEX_SHA256="[0-9a-f]{64}";\s+\\\s*CODEX_CODE_MODE_HOST_SHA256=")[0-9a-f]{64}(";)`)
	copilotArmChecksumLinePattern           = regexp.MustCompile(`(?ms)(arm64\)\s+\\\s*COPILOT_PLATFORM="linux-arm64";\s+\\\s*COPILOT_SHA256=")[0-9a-f]{64}(";)`)
	copilotAmdChecksumLinePattern           = regexp.MustCompile(`(?ms)(amd64\)\s+\\\s*COPILOT_PLATFORM="linux-x64";\s+\\\s*COPILOT_SHA256=")[0-9a-f]{64}(";)`)
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
	// Uncertified holds the npm pins of the uncertified provider packages,
	// keyed by package name. Only the runtime-uncertified image installs them.
	Uncertified map[string]ProviderBumpSelection `json:"uncertified,omitempty"`
	// UncertifiedReleases holds the GitHub release pins of the uncertified
	// provider CLIs in the Dockerfile, keyed by adapter id.
	UncertifiedReleases map[string]ProviderBumpSelection `json:"uncertified_releases,omitempty"`
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

type providerChecksumRequirement struct {
	key      string
	variable string
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
	// CodexCLISourceURLFmt and CodexSubcommandFixturePath enable the
	// plan-time Codex command namespace check. An empty fixture path skips
	// it; prepare-codex-subcommand-fixture still enforces it at apply time.
	CodexCLISourceURLFmt       string
	CodexSubcommandFixturePath string
	// UncertifiedPackageJSONPath enables the plan of every dependency of the
	// uncertified provider package.json, read from NPMRegistryURL. An empty
	// path skips it.
	UncertifiedPackageJSONPath string
	NPMRegistryURL             string
	// GooseReleaseAPIURL enables the plan of the goose release pin in the
	// Dockerfile, read from one page of the GitHub release list. An empty URL
	// skips it.
	GooseReleaseAPIURL string
}

type npmRegistryMetadata struct {
	DistTags map[string]string `json:"dist-tags"`
	Time     map[string]string `json:"time"`
}

// githubReleaseMetadata is the part of a GitHub release document that the
// provider bumps read.
type githubReleaseMetadata struct {
	TagName     string `json:"tag_name"`
	Prerelease  bool   `json:"prerelease"`
	PublishedAt string `json:"published_at"`
	Assets      []struct {
		Name   string `json:"name"`
		Digest string `json:"digest"`
	} `json:"assets"`
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
		CodexCLISourceURLFmt:  defaultCodexCLISourceAPIURLFormat,
		NPMRegistryURL:        defaultProviderBumpNPMRegistryURL,
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
	if codexSelection.Changed && sources.CodexSubcommandFixturePath != "" {
		codexSelection, err = holdCodexOnUnclassifiedCLISurface(codexSelection, sources, client)
		if err != nil {
			return nil, err
		}
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
	geminiSelection, err := selectNPMStable(sources.GeminiRegistryURL, geminiCurrent, cutoff, parseStableVersion, client)
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
	if sources.GooseReleaseAPIURL != "" {
		selection, err := selectGooseStable(dockerfilePath, cutoff, sources.GooseReleaseAPIURL, client)
		if err != nil {
			return nil, err
		}
		plan.UncertifiedReleases = map[string]ProviderBumpSelection{gooseReleaseCLI.id: selection}
		plan.HasChanges = plan.HasChanges || selection.Changed
	}
	if sources.UncertifiedPackageJSONPath != "" {
		plan.Uncertified, err = planUncertifiedNPMBumps(sources.UncertifiedPackageJSONPath, cutoff, sources.NPMRegistryURL, client)
		if err != nil {
			return nil, err
		}
		for _, selection := range plan.Uncertified {
			plan.HasChanges = plan.HasChanges || selection.Changed
		}
	}
	return plan, nil
}

// planUncertifiedNPMBumps selects the cool-off target of each dependency
// that packageJSONPath pins, in the exact version form of its pin.
func planUncertifiedNPMBumps(packageJSONPath string, cutoff time.Time, registryURL string, client *http.Client) (map[string]ProviderBumpSelection, error) {
	var pkg providersPackageJSON
	if err := readJSONFile(packageJSONPath, &pkg); err != nil {
		return nil, err
	}
	if len(pkg.Dependencies) == 0 {
		return nil, fmt.Errorf("%s must pin at least one dependency", packageJSONPath)
	}
	plan := map[string]ProviderBumpSelection{}
	for name, current := range pkg.Dependencies {
		parse := uncertifiedVersionParser(current)
		if parse == nil {
			return nil, fmt.Errorf("%s requires an exact stable or build-stamped %s pin, found %q", packageJSONPath, name, current)
		}
		// A scoped name keeps its @ and escapes its slash, as the registry expects.
		selection, err := selectNPMStable(registryURL+"/"+strings.Replace(name, "/", "%2f", 1), current, cutoff, parse, client)
		if err != nil {
			return nil, err
		}
		plan[name] = selection
	}
	return plan, nil
}

// ApplyUncertifiedProviderBumpPlan writes each plan.Uncertified target
// version to its dependency in packageJSONPath.
func ApplyUncertifiedProviderBumpPlan(planPath, packageJSONPath string) error {
	content, err := rootio.ReadFileNoFollow(planPath, "provider bump plan", providerBumpMaxJSONBytes)
	if err != nil {
		return err
	}
	var plan ProviderBumpPlan
	if err := json.Unmarshal(content, &plan); err != nil {
		return err
	}
	// One no-follow parent descriptor serves the read and the atomic write, so
	// the file that is validated is the file that is replaced.
	parent, path, err := rootio.OpenParentDirectoryNoFollow(packageJSONPath)
	if err != nil {
		return err
	}
	defer parent.Close()
	raw, err := rootio.ReadFileAtNoFollow(parent, filepath.Base(path), "uncertified provider package", providerBumpMaxJSONBytes)
	if err != nil {
		return err
	}
	var pkg providersPackageJSON
	if err := json.Unmarshal(raw, &pkg); err != nil {
		return fmt.Errorf("%s: %w", packageJSONPath, err)
	}
	text := string(raw)
	updated := text
	for name, selection := range plan.Uncertified {
		if uncertifiedVersionParser(selection.TargetVersion) == nil {
			return fmt.Errorf("%s contains a non-exact %s target version %q", planPath, name, selection.TargetVersion)
		}
		if pkg.Dependencies[name] == "" {
			return fmt.Errorf("%s plans %s, which %s does not pin", planPath, name, packageJSONPath)
		}
		dependency := regexp.MustCompile(`("` + regexp.QuoteMeta(name) + `"\s*:\s*")[^"]+(")`)
		updated = dependency.ReplaceAllString(updated, fmt.Sprintf(`${1}%s${2}`, selection.TargetVersion))
	}
	if updated == text {
		return nil
	}
	return rootio.WriteFileAtomicAtNoFollow(parent, filepath.Base(path), []byte(updated), 0o644, ".uncertified-bump-")
}

// GooseReleaseTagAPIURLFormat is the GitHub API URL of one goose release,
// formatted with its X.Y.Z version.
const GooseReleaseTagAPIURLFormat = "https://api.github.com/repos/aaif-goose/goose/releases/tags/v%s"

// gooseReleaseCLI is the goose CLI of the runtime-uncertified image: the
// static musl archive of each architecture.
var gooseReleaseCLI = githubReleaseCLI{
	id:    "goose",
	label: "goose",
	title: "goose CLI",
	assets: []releaseAsset{
		{"arm64", "goose-aarch64-unknown-linux-musl.tar.gz"},
		{"amd64", "goose-x86_64-unknown-linux-musl.tar.gz"},
	},
}

var gooseVersionLinePattern = regexp.MustCompile(`(?m)^ARG GOOSE_VERSION=(.*)$`)

// gooseChecksumLinePattern matches the Dockerfile case arm of asset: its
// GOOSE_ARCH, the archive name without the goose- prefix and the .tar.gz
// suffix, and its GOOSE_SHA256 value in group 2.
func gooseChecksumLinePattern(asset releaseAsset) *regexp.Regexp {
	arch := strings.TrimSuffix(strings.TrimPrefix(asset.name, "goose-"), ".tar.gz")
	return regexp.MustCompile(`(?ms)(` + asset.key + `\)\s+\\\s*GOOSE_ARCH="` + regexp.QuoteMeta(arch) + `";\s+\\\s*GOOSE_SHA256=")([0-9a-f]{64})(";)`)
}

// gooseDockerfilePin returns the one stable GOOSE_VERSION of dockerfile and
// the one GOOSE_SHA256 of each goose asset, keyed by architecture.
func gooseDockerfilePin(dockerfile string) (string, map[string]string, error) {
	versions := gooseVersionLinePattern.FindAllStringSubmatch(dockerfile, -1)
	if len(versions) != 1 || !stableVersionPattern.MatchString(versions[0][1]) {
		return "", nil, errors.New("the Dockerfile must pin one stable X.Y.Z ARG GOOSE_VERSION")
	}
	checksums := map[string]string{}
	for _, asset := range gooseReleaseCLI.assets {
		matches := gooseChecksumLinePattern(asset).FindAllStringSubmatch(dockerfile, -1)
		if len(matches) != 1 {
			return "", nil, fmt.Errorf("the Dockerfile must pin one %s GOOSE_SHA256 for %s", asset.key, asset.name)
		}
		checksums[asset.key] = matches[0][2]
	}
	return versions[0][1], checksums, nil
}

func readGooseDockerfilePin(dockerfilePath string) (string, map[string]string, error) {
	dockerfile, err := rootio.ReadFileNoFollow(dockerfilePath, "Dockerfile", providerBumpMaxJSONBytes)
	if err != nil {
		return "", nil, err
	}
	return gooseDockerfilePin(string(dockerfile))
}

// selectGooseStable reads one page of the goose release list at releasesURL.
// goose ships about one release a week, so the newest page holds the releases
// that the cool-off can select, and the plan spends one GitHub API request.
func selectGooseStable(dockerfilePath string, cutoff time.Time, releasesURL string, client *http.Client) (ProviderBumpSelection, error) {
	current, _, err := readGooseDockerfilePin(dockerfilePath)
	if err != nil {
		return ProviderBumpSelection{}, err
	}
	var releases []githubReleaseMetadata
	if _, err := fetchJSONResponse(client, releasesURL, &releases); err != nil {
		return ProviderBumpSelection{}, err
	}
	return selectGitHubReleaseStable(gooseReleaseCLI, releases, current, cutoff, "")
}

// ApplyUncertifiedReleaseBumpPlan writes each changed plan.UncertifiedReleases
// target version and its checksums to dockerfilePath.
func ApplyUncertifiedReleaseBumpPlan(planPath, dockerfilePath string) error {
	content, err := rootio.ReadFileNoFollow(planPath, "provider bump plan", providerBumpMaxJSONBytes)
	if err != nil {
		return err
	}
	var plan ProviderBumpPlan
	if err := json.Unmarshal(content, &plan); err != nil {
		return err
	}
	for id := range plan.UncertifiedReleases {
		if id != gooseReleaseCLI.id {
			return fmt.Errorf("%s plans the %s release, which no Dockerfile pin owns", planPath, id)
		}
	}
	selection := plan.UncertifiedReleases[gooseReleaseCLI.id]
	if !selection.Changed {
		return nil
	}
	// One no-follow parent descriptor serves the read and the atomic write, so
	// the file that is validated is the file that is replaced.
	parent, path, err := rootio.OpenParentDirectoryNoFollow(dockerfilePath)
	if err != nil {
		return err
	}
	defer parent.Close()
	raw, err := rootio.ReadFileAtNoFollow(parent, filepath.Base(path), "Dockerfile", providerBumpMaxJSONBytes)
	if err != nil {
		return err
	}
	updated, err := applyGoosePin(string(raw), selection)
	if err != nil {
		return fmt.Errorf("%s: %w", planPath, err)
	}
	return rootio.WriteFileAtomicAtNoFollow(parent, filepath.Base(path), []byte(updated), 0o644, ".goose-bump-")
}

func applyGoosePin(dockerfile string, selection ProviderBumpSelection) (string, error) {
	if !stableVersionPattern.MatchString(selection.TargetVersion) {
		return "", fmt.Errorf("the goose target version %q is not a stable X.Y.Z version", selection.TargetVersion)
	}
	if _, _, err := gooseDockerfilePin(dockerfile); err != nil {
		return "", err
	}
	updated := gooseVersionLinePattern.ReplaceAllLiteralString(dockerfile, "ARG GOOSE_VERSION="+selection.TargetVersion)
	for _, asset := range gooseReleaseCLI.assets {
		checksum := selection.Checksums[asset.key]
		if !isHexDigest(checksum) {
			return "", fmt.Errorf("the goose target version %s needs a valid %s sha256 checksum", selection.TargetVersion, asset.key)
		}
		updated = gooseChecksumLinePattern(asset).ReplaceAllString(updated, "${1}"+checksum+"${3}")
	}
	return updated, nil
}

// VerifyGooseReleasePin fails unless the goose pin of dockerfilePath names a
// stable GitHub release whose archive digests equal the pinned checksums.
// releaseURLFormat is GooseReleaseTagAPIURLFormat outside tests.
func VerifyGooseReleasePin(dockerfilePath, releaseURLFormat string, client *http.Client) error {
	version, pinned, err := readGooseDockerfilePin(dockerfilePath)
	if err != nil {
		return err
	}
	if client == nil {
		client = &http.Client{Timeout: providerBumpHTTPTimeout}
	}
	var release githubReleaseMetadata
	if _, err := fetchJSONResponse(client, fmt.Sprintf(releaseURLFormat, version), &release); err != nil {
		return err
	}
	if release.TagName != "v"+version {
		return fmt.Errorf("the goose release metadata names %q, want v%s", release.TagName, version)
	}
	if release.Prerelease {
		return fmt.Errorf("goose v%s is a prerelease", version)
	}
	released, reasons, _ := releaseAssetChecksums(release, gooseReleaseCLI.assets)
	if len(reasons) != 0 {
		return fmt.Errorf("goose v%s release: %s", version, strings.Join(reasons, "; "))
	}
	for _, asset := range gooseReleaseCLI.assets {
		if pinned[asset.key] != released[asset.key] {
			return fmt.Errorf("the pinned %s goose sha256 %s differs from the v%s release digest %s", asset.key, pinned[asset.key], version, released[asset.key])
		}
	}
	return nil
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
	currentCodexVersion, err := ExtractDockerfileArg(dockerfilePath, "CODEX_VERSION")
	if err != nil {
		return err
	}
	currentCopilotVersion, err := ExtractDockerfileArg(dockerfilePath, "COPILOT_VERSION")
	if err != nil {
		return err
	}
	codexVersionChanged := codexPlan.TargetVersion != currentCodexVersion
	copilotVersionChanged := copilotPlan.TargetVersion != currentCopilotVersion
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

	updatedDockerfile := dockerfileText
	updatedDockerfile = claudeVersionLinePattern.ReplaceAllString(updatedDockerfile, fmt.Sprintf("ARG CLAUDE_VERSION=%s", claudePlan.TargetVersion))
	updatedDockerfile = codexVersionLinePattern.ReplaceAllString(updatedDockerfile, fmt.Sprintf("ARG CODEX_VERSION=%s", codexPlan.TargetVersion))
	updatedDockerfile = copilotVersionLinePattern.ReplaceAllString(updatedDockerfile, fmt.Sprintf("ARG COPILOT_VERSION=%s", copilotPlan.TargetVersion))
	for _, update := range []struct {
		pattern  *regexp.Regexp
		checksum string
	}{
		{claudeArmChecksumLinePattern, claudePlan.Checksums["arm64"]},
		{claudeAmdChecksumLinePattern, claudePlan.Checksums["amd64"]},
		{codexArmChecksumLinePattern, codexPlan.Checksums["arm64"]},
		{codexAmdChecksumLinePattern, codexPlan.Checksums["amd64"]},
		{codexCodeModeHostArmChecksumLinePattern, codexPlan.Checksums["code_mode_host_arm64"]},
		{codexCodeModeHostAmdChecksumLinePattern, codexPlan.Checksums["code_mode_host_amd64"]},
		{copilotArmChecksumLinePattern, copilotPlan.Checksums["arm64"]},
		{copilotAmdChecksumLinePattern, copilotPlan.Checksums["amd64"]},
	} {
		if update.checksum != "" {
			updatedDockerfile = update.pattern.ReplaceAllString(updatedDockerfile, fmt.Sprintf(`${1}%s${2}`, update.checksum))
		}
	}
	if codexVersionChanged {
		requirements := []providerChecksumRequirement{
			{"arm64", "CODEX_SHA256"},
			{"amd64", "CODEX_SHA256"},
			{"code_mode_host_arm64", "CODEX_CODE_MODE_HOST_SHA256"},
			{"code_mode_host_amd64", "CODEX_CODE_MODE_HOST_SHA256"},
		}
		if err := validateProviderChecksumUpdate(planPath, dockerfilePath, providerid.Codex, updatedDockerfile, codexPlan, requirements); err != nil {
			return err
		}
	}
	if copilotVersionChanged {
		requirements := []providerChecksumRequirement{
			{"arm64", "COPILOT_SHA256"},
			{"amd64", "COPILOT_SHA256"},
		}
		if err := validateProviderChecksumUpdate(planPath, dockerfilePath, providerid.Copilot, updatedDockerfile, copilotPlan, requirements); err != nil {
			return err
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

func validateProviderChecksumUpdate(planPath, dockerfilePath, provider, dockerfile string, selection ProviderBumpSelection, requirements []providerChecksumRequirement) error {
	for _, requirement := range requirements {
		checksum := selection.Checksums[requirement.key]
		if !isHexDigest(checksum) {
			return fmt.Errorf("%s provider %s target version %s requires a valid %s sha256 checksum", planPath, provider, selection.TargetVersion, requirement.key)
		}
		if !strings.Contains(dockerfile, fmt.Sprintf(`%s="%s"`, requirement.variable, checksum)) {
			return fmt.Errorf("failed to update %s %s checksum in %s", provider, requirement.key, dockerfilePath)
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
	candidates, skipped, err := stableCandidatesFromRegistry(sources.CodexRegistryURL, cutoff, maxVersion, parseStableVersion, client)
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
			return keepNewerCurrentPin(currentVersion, candidate, skipped), nil
		}
		if candidate.Raw == currentVersion {
			return currentSelection(candidate.Source), nil
		}
		releaseURL := fmt.Sprintf(sources.CodexReleaseAPIURLFmt, candidate.Raw)
		var release githubReleaseMetadata
		if err := fetchJSON(client, releaseURL, &release); err != nil {
			return ProviderBumpSelection{}, err
		}
		if release.Prerelease {
			skipped = appendSkippedProviderRelease(skipped, candidate, "GitHub release is marked prerelease")
			continue
		}
		checksums, reasons, invalidMetadata := releaseAssetChecksums(release, codexReleaseAssets)
		if len(reasons) != 0 {
			details := strings.Join(reasons, "; ")
			if invalidMetadata {
				return ProviderBumpSelection{}, fmt.Errorf("codex release %s has invalid musl Linux asset metadata: %s", candidate.Raw, details)
			}
			skipped = appendSkippedProviderRelease(skipped, candidate, "missing supported musl Linux release assets: "+details)
			continue
		}
		return ProviderBumpSelection{
			Channel:         "stable",
			CurrentVersion:  currentVersion,
			TargetVersion:   candidate.Raw,
			PublishedAt:     candidate.Source.Format(time.RFC3339),
			Changed:         candidate.Raw != currentVersion,
			SkippedReleases: skipped,
			Checksums:       checksums,
		}, nil
	}
	return currentSelection(time.Time{}), nil
}

// holdCodexOnUnclassifiedCLISurface keeps the current Codex pin when the
// target release changes the classified command namespace. Other providers and
// upstream pins still refresh. Fetch, provenance, and fixture errors stay fatal.
func holdCodexOnUnclassifiedCLISurface(selection ProviderBumpSelection, sources ProviderBumpSources, client *http.Client) (ProviderBumpSelection, error) {
	subcommands, err := fetchCodexSubcommands(selection.TargetVersion, fmt.Sprintf(sources.CodexCLISourceURLFmt, selection.TargetVersion), client)
	if err != nil {
		return ProviderBumpSelection{}, err
	}
	fixture, err := rootio.ReadFileNoFollow(sources.CodexSubcommandFixturePath, "Codex subcommand fixture", 1<<20)
	if err != nil {
		return ProviderBumpSelection{}, fmt.Errorf("read Codex subcommand fixture: %w", err)
	}
	_, err = updateCodexSubcommandFixture(selection.TargetVersion, fixture, subcommands)
	var namespaceErr *codexNamespaceChangeError
	if !errors.As(err, &namespaceErr) {
		return selection, err
	}
	held := ProviderBumpSkippedRelease{
		Version:     selection.TargetVersion,
		PublishedAt: selection.PublishedAt,
		Reason:      fmt.Sprintf("unclassified-cli-surface added=%v removed=%v", namespaceErr.added, namespaceErr.removed),
	}
	return ProviderBumpSelection{
		Channel:         selection.Channel,
		CurrentVersion:  selection.CurrentVersion,
		TargetVersion:   selection.CurrentVersion,
		SkippedReleases: append(selection.SkippedReleases, held),
	}, nil
}

// releaseAsset is one release archive that a provider pin checksums, keyed
// by its plan checksum key.
type releaseAsset struct {
	key  string
	name string
}

var codexReleaseAssets = []releaseAsset{
	{"arm64", "codex-aarch64-unknown-linux-musl.tar.gz"},
	{"amd64", "codex-x86_64-unknown-linux-musl.tar.gz"},
	{"code_mode_host_arm64", "codex-code-mode-host-aarch64-unknown-linux-musl.tar.gz"},
	{"code_mode_host_amd64", "codex-code-mode-host-x86_64-unknown-linux-musl.tar.gz"},
}

// releaseAssetChecksums returns the sha256 digest of each asset in release.
// The reasons name each asset that is missing or has a bad digest, and the
// flag is true when a digest is bad rather than missing.
func releaseAssetChecksums(release githubReleaseMetadata, assets []releaseAsset) (map[string]string, []string, bool) {
	checksums := make(map[string]string, len(assets))
	reasons := make([]string, 0, len(assets))
	invalidMetadata := false
	for _, asset := range assets {
		digest, err := githubReleaseAssetDigest(release, asset.name)
		if err == nil {
			checksums[asset.key] = digest
			continue
		}
		reasons = append(reasons, err.Error())
		if !errors.Is(err, errReleaseAssetNotFound) {
			invalidMetadata = true
		}
	}
	return checksums, reasons, invalidMetadata
}

// githubReleaseCLI names a CLI that a GitHub release ships as one Linux
// archive per Docker architecture, with the names its messages use.
type githubReleaseCLI struct {
	id     string // the policy and plan key, for example copilot
	label  string // Copilot
	title  string // GitHub Copilot CLI
	assets []releaseAsset
}

var copilotReleaseCLI = githubReleaseCLI{
	id:    providerid.Copilot,
	label: "Copilot",
	title: "GitHub Copilot CLI",
	assets: []releaseAsset{
		{"arm64", "copilot-linux-arm64.tar.gz"},
		{"amd64", "copilot-linux-x64.tar.gz"},
	},
}

func selectCopilotStable(currentVersion string, cutoff time.Time, maxVersion string, sources ProviderBumpSources, client *http.Client) (ProviderBumpSelection, error) {
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
	return selectGitHubReleaseStable(copilotReleaseCLI, releases, currentVersion, cutoff, maxVersion)
}

// selectGitHubReleaseStable selects the newest stable vX.Y.Z release of cli
// that is older than cutoff, is not above maxVersion, and ships every asset.
func selectGitHubReleaseStable(cli githubReleaseCLI, releases []githubReleaseMetadata, currentVersion string, cutoff time.Time, maxVersion string) (ProviderBumpSelection, error) {
	current, hasCurrentVersion := parseStableVersion(currentVersion)
	maxAllowed, hasMaxVersion := parseStableVersion(maxVersion)
	if maxVersion != "" && !hasMaxVersion {
		return ProviderBumpSelection{}, fmt.Errorf("provider.%s.max_version must be an exact stable version, found %q", cli.id, maxVersion)
	}
	if hasCurrentVersion && hasMaxVersion && compareStableVersions(current, maxAllowed) > 0 {
		return ProviderBumpSelection{}, fmt.Errorf("current %s version %s exceeds provider.%s.max_version %s", cli.label, currentVersion, cli.id, maxVersion)
	}

	type releaseCandidate struct {
		version stableVersion
		release githubReleaseMetadata
	}
	candidates := make([]releaseCandidate, 0, len(releases))
	skipped := make([]ProviderBumpSkippedRelease, 0)
	for _, release := range releases {
		if !strings.HasPrefix(release.TagName, "v") {
			continue
		}
		rawVersion := strings.TrimPrefix(release.TagName, "v")
		version, ok := parseStableVersion(rawVersion)
		if !ok {
			continue
		}
		if release.Prerelease {
			skipped = appendSkippedProviderRelease(skipped, version, "GitHub release is marked prerelease")
			continue
		}
		if hasMaxVersion && compareStableVersions(version, maxAllowed) > 0 {
			skipped = appendSkippedProviderRelease(skipped, version, fmt.Sprintf("exceeds configured max_version %s", maxVersion))
			continue
		}
		publishedAt, err := time.Parse(time.RFC3339, release.PublishedAt)
		if err != nil {
			return ProviderBumpSelection{}, fmt.Errorf("parse %s release publish time for %s: %w", cli.title, release.TagName, err)
		}
		version.Source = publishedAt.UTC()
		if !cutoff.IsZero() && version.Source.After(cutoff) {
			skipped = appendSkippedProviderRelease(skipped, version, "within configured cool-off window")
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
			return keepNewerCurrentPin(currentVersion, candidate.version, skipped), nil
		}
		if candidate.version.Raw == currentVersion {
			return currentSelection(candidate.version.Source), nil
		}
		checksums, reasons, invalidMetadata := releaseAssetChecksums(candidate.release, cli.assets)
		if len(reasons) != 0 {
			if !invalidMetadata {
				skipped = appendSkippedProviderRelease(skipped, candidate.version, "missing supported Linux release assets: "+strings.Join(reasons, "; "))
				continue
			}
			return ProviderBumpSelection{}, fmt.Errorf("%s release %s has invalid Linux asset metadata: %s", cli.id, candidate.version.Raw, strings.Join(reasons, "; "))
		}
		return ProviderBumpSelection{
			Channel:         "stable",
			CurrentVersion:  currentVersion,
			TargetVersion:   candidate.version.Raw,
			PublishedAt:     candidate.version.Source.Format(time.RFC3339),
			Changed:         candidate.version.Raw != currentVersion,
			SkippedReleases: skipped,
			Checksums:       checksums,
		}, nil
	}
	return currentSelection(time.Time{}), nil
}

// selectNPMStable selects the newest stable release of the npm package at
// registryURL that is older than cutoff.
func selectNPMStable(registryURL, currentVersion string, cutoff time.Time, parse versionParser, client *http.Client) (ProviderBumpSelection, error) {
	candidates, _, err := stableCandidatesFromRegistry(registryURL, cutoff, "", parse, client)
	if err != nil {
		return ProviderBumpSelection{}, err
	}
	if len(candidates) == 0 {
		return ProviderBumpSelection{
			Channel:        "stable",
			CurrentVersion: currentVersion,
			TargetVersion:  currentVersion,
		}, nil
	}
	newest := candidates[0]
	if current, ok := parse(currentVersion); ok && compareStableVersions(newest, current) < 0 {
		return keepNewerCurrentPin(currentVersion, newest, nil), nil
	}
	return ProviderBumpSelection{
		Channel:        "stable",
		CurrentVersion: currentVersion,
		TargetVersion:  newest.Raw,
		PublishedAt:    newest.Source.Format(time.RFC3339),
		Changed:        newest.Raw != currentVersion,
	}, nil
}

// keepNewerCurrentPin holds the current pin when the newest cooled-off
// candidate is older than it. The cool-off never moves a pin backwards; a
// pin adopted under a shorter cool-off stays until a newer release cools off.
// Every provider resolver routes this case through here.
func keepNewerCurrentPin(currentVersion string, cooled stableVersion, skipped []ProviderBumpSkippedRelease) ProviderBumpSelection {
	return ProviderBumpSelection{
		Channel:         "stable",
		CurrentVersion:  currentVersion,
		TargetVersion:   currentVersion,
		SkippedReleases: appendSkippedProviderRelease(skipped, cooled, "current-newer-than-cooled-candidate"),
	}
}

// claudeSelectionForCandidate fetches and validates the Claude release manifest
// for a candidate version and assembles its ProviderBumpSelection. It returns
// the later of the registry publication and native build times so a delayed
// stable-channel publication cannot shorten the configured cool-off. Shared by
// the approved-version and newest-stable resolution paths in selectClaudeStable.
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
	availableAt := buildTime.UTC()
	if candidate.Source.After(availableAt) {
		availableAt = candidate.Source.UTC()
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
		PublishedAt:    availableAt.Format(time.RFC3339),
		Changed:        candidate.Raw != currentVersion,
		Checksums: map[string]string{
			"arm64": armChecksum,
			"amd64": amdChecksum,
		},
	}, availableAt, nil
}

func selectClaudeStable(currentVersion string, cutoff time.Time, maxVersion string, approvedVersion string, sources ProviderBumpSources, client *http.Client) (ProviderBumpSelection, error) {
	candidates, _, err := stableCandidatesFromRegistry(sources.ClaudeRegistryURL, time.Time{}, maxVersion, parseStableVersion, client)
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
			return keepNewerCurrentPin(currentVersion, selectedVersion, nil), nil
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

// stableCandidatesFromRegistry lists the registry releases that parse names
// as exact versions, up to dist-tags.latest and published by cutoff.
func stableCandidatesFromRegistry(registryURL string, cutoff time.Time, maxVersion string, parse versionParser, client *http.Client) ([]stableVersion, []ProviderBumpSkippedRelease, error) {
	var metadata npmRegistryMetadata
	if _, err := fetchDecoded(client, registryURL, providerBumpMaxNPMStreamBytes, func(body io.Reader) error { return decodeNPMRegistryMetadata(body, &metadata) }); err != nil {
		return nil, nil, err
	}
	latestRaw := metadata.DistTags["latest"]
	latestVersion, ok := parse(latestRaw)
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
		version, ok := parse(rawVersion)
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

func githubReleaseAssetDigest(release githubReleaseMetadata, assetName string) (string, error) {
	for _, asset := range release.Assets {
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

func fetchCopilotReleases(client *http.Client, releaseAPIURL string) ([]githubReleaseMetadata, error) {
	var releases []githubReleaseMetadata
	pageSize, err := copilotReleasePageSize(releaseAPIURL)
	if err != nil {
		return nil, err
	}
	nextURL, err := paginatedCopilotReleaseURL(releaseAPIURL, 1)
	if err != nil {
		return nil, err
	}

	for page := 1; page <= copilotReleaseMaxPages && nextURL != ""; page++ {
		var pageReleases []githubReleaseMetadata
		header, err := fetchJSONResponse(client, nextURL, &pageReleases)
		if err != nil {
			return nil, err
		}
		releases = append(releases, pageReleases...)

		if linkNext := nextLinkURL(header.Get("Link")); linkNext != "" {
			nextURL, err = validateCopilotReleaseNextURL(releaseAPIURL, linkNext)
			if err != nil {
				return nil, err
			}
			continue
		}
		if len(pageReleases) == pageSize {
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

func copilotReleasePageSize(releaseAPIURL string) (int, error) {
	parsed, err := url.Parse(releaseAPIURL)
	if err != nil {
		return 0, fmt.Errorf("parse Copilot release API URL %s: %w", releaseAPIURL, err)
	}
	rawPageSize := parsed.Query().Get("per_page")
	if rawPageSize == "" {
		return 100, nil
	}
	pageSize, err := strconv.Atoi(rawPageSize)
	if err != nil || pageSize <= 0 {
		return 0, fmt.Errorf("parse Copilot release API per_page %q: expected positive integer", rawPageSize)
	}
	return pageSize, nil
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

func validateCopilotReleaseNextURL(releaseAPIURL string, nextURL string) (string, error) {
	base, err := url.Parse(releaseAPIURL)
	if err != nil {
		return "", fmt.Errorf("parse Copilot release API URL %s: %w", releaseAPIURL, err)
	}
	next, err := url.Parse(nextURL)
	if err != nil {
		return "", fmt.Errorf("parse Copilot release next URL %s: %w", nextURL, err)
	}
	if !next.IsAbs() {
		next = base.ResolveReference(next)
	}
	if !strings.EqualFold(next.Scheme, base.Scheme) || !strings.EqualFold(next.Host, base.Host) {
		return "", fmt.Errorf("reject Copilot release pagination URL outside configured origin: %s", next.String())
	}
	return next.String(), nil
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

// versionParser parses one exact version form of a provider pin.
type versionParser func(raw string) (stableVersion, bool)

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

// parseBuildStampedVersion parses a 0.0.<build>-g<commit> version. The build
// number is the patch number, so the stable comparison orders the builds.
func parseBuildStampedVersion(raw string) (stableVersion, bool) {
	if !buildStampedVersionPattern.MatchString(raw) {
		return stableVersion{}, false
	}
	version, ok := parseStableVersion(raw[:strings.IndexByte(raw, '-')])
	version.Raw = raw
	return version, ok
}

// uncertifiedVersionParser returns the parser of the version form of an
// uncertified npm pin, or nil when the pin is not an exact version.
func uncertifiedVersionParser(pin string) versionParser {
	for _, parse := range []versionParser{parseStableVersion, parseBuildStampedVersion} {
		if _, ok := parse(pin); ok {
			return parse
		}
	}
	return nil
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

// decodeNPMRegistryMetadata reads only dist-tags and time from an npm packument and skips every other value token by token, so the versions object is never held in memory.
func decodeNPMRegistryMetadata(body io.Reader, metadata *npmRegistryMetadata) error {
	dec := json.NewDecoder(body)
	// UseNumber keeps skipped numbers such as 1e1000000 as text, not float64.
	dec.UseNumber()
	if token, err := dec.Token(); err != nil || token != json.Delim('{') {
		return fmt.Errorf("npm packument is not a JSON object: %v", err)
	}
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			return err
		}
		switch key {
		case "dist-tags":
			err = dec.Decode(&metadata.DistTags)
		case "time":
			err = dec.Decode(&metadata.Time)
		default:
			err = skipJSONValue(dec)
		}
		if err != nil {
			return err
		}
	}
	_, err := dec.Token()
	return err
}

// skipJSONValue consumes one JSON value, however deep, one token at a time.
func skipJSONValue(dec *json.Decoder) error {
	for depth := 0; ; {
		token, err := dec.Token()
		if err != nil {
			return err
		}
		switch token {
		case json.Delim('{'), json.Delim('['):
			depth++
		case json.Delim('}'), json.Delim(']'):
			depth--
		}
		if depth == 0 {
			return nil
		}
	}
}

func fetchJSON(client *http.Client, targetURL string, target any) error {
	_, err := fetchJSONResponse(client, targetURL, target)
	return err
}

func fetchJSONResponse(client *http.Client, targetURL string, target any) (http.Header, error) {
	return fetchDecoded(client, targetURL, providerBumpMaxJSONBytes, func(body io.Reader) error { return json.NewDecoder(body).Decode(target) })
}

// fetchDecoded owns the provider-bump HTTP request: the timeout, the GitHub API auth, the status check, and a cap of limit bytes on the body that decode reads.
func fetchDecoded(client *http.Client, targetURL string, limit int64, decode func(io.Reader) error) (http.Header, error) {
	ctx, cancel := context.WithTimeout(context.Background(), providerBumpHTTPTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", targetURL, err)
	}
	req.Header.Set("User-Agent", providerBumpUserAgent)
	if providerBumpIsGitHubAPIURL(targetURL) {
		token, err := providerBumpGitHubAPIToken()
		if err != nil {
			return nil, fmt.Errorf("prepare GitHub API auth for %s: %w", targetURL, err)
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", targetURL, err)
	}
	defer resp.Body.Close()
	if err := githubRateLimitError(resp); err != nil {
		return nil, fmt.Errorf("fetch %s: %w", targetURL, err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("fetch %s: unexpected status %d: %s", targetURL, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if err := decode(io.LimitReader(resp.Body, limit)); err != nil {
		return nil, fmt.Errorf("decode %s: %w", targetURL, err)
	}
	return resp.Header.Clone(), nil
}

func providerBumpIsGitHubAPIURL(targetURL string) bool {
	parsed, err := url.Parse(targetURL)
	if err != nil {
		return false
	}
	return strings.EqualFold(parsed.Scheme, "https") && strings.EqualFold(parsed.Hostname(), "api.github.com")
}

func providerBumpGitHubAPIToken() (string, error) {
	raw := os.Getenv("WORKCELL_GITHUB_API_TOKEN")
	if tokenFile := os.Getenv("WORKCELL_GITHUB_API_TOKEN_FILE"); tokenFile != "" {
		content, err := os.ReadFile(tokenFile)
		if err != nil {
			return "", fmt.Errorf("read WORKCELL_GITHUB_API_TOKEN_FILE: %w", err)
		}
		raw = string(content)
	}
	token := strings.TrimRight(raw, "\r\n")
	if token == "" {
		return "", nil
	}
	if strings.ContainsAny(token, "\r\n") {
		return "", errors.New("WORKCELL_GITHUB_API_TOKEN_FILE and WORKCELL_GITHUB_API_TOKEN must contain exactly one token line")
	}
	return token, nil
}
