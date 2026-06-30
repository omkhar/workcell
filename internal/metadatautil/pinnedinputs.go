// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/omkhar/workcell/internal/tomlsubset"
	"gopkg.in/yaml.v3"
)

var (
	workflowPermissionsRE = regexp.MustCompile(`(?m)^permissions:\s+\{\}$`)
	aptInstallPattern     = regexp.MustCompile(`apt-get install -y --no-install-recommends(?s:(.*?))&&`)
	// pinnedReleaseTagPattern matches an exact vMAJOR.MINOR.PATCH release tag.
	pinnedReleaseTagPattern = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)
	// workflowUsesPattern and commitShaPattern scan workflow `uses:` refs for a
	// pinned 40-hex commit SHA; both run inside the per-workflow scan loop.
	workflowUsesPattern = regexp.MustCompile(`(?m)^\s*-\s+uses:\s+([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)@([^\s#]+)`)
	commitShaPattern    = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

type PinnedInputsConfig struct {
	RuntimeDockerfilePath    string
	ValidatorDockerfilePath  string
	ProvidersPackageJSONPath string
	ProvidersPackageLockPath string
	WorkflowsDir             string
	CIWorkflowPath           string
	ReleaseWorkflowPath      string
	PinHygieneWorkflowPath   string
	CodeownersPath           string
	CodexRequirementsPath    string
	CodexMCPConfigPath       string
	HostedControlsPolicyPath string
	HostedControlsScriptPath string
	ProviderBumpPolicyPath   string
	MaxDebianSnapshotAgeDays int
}

// readText, isHexDigest, hexDigestPattern live in core.go.
// requireStringSliceTable lives in hostedcontrols.go
// (canonical post-collapse; same package-internal symbols all consumers share).

func CheckPinnedInputs(cfg PinnedInputsConfig) error {
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(cfg.RuntimeDockerfilePath), "..", ".."))
	goModPath := filepath.Join(repoRoot, "go.mod")
	cargoManifestPath := filepath.Join(repoRoot, "runtime", "container", "rust", "Cargo.toml")
	installDevToolsScriptPath := filepath.Join(repoRoot, "scripts", "install-dev-tools.sh")
	markdownlintPackageJSONPath := filepath.Join(repoRoot, "tools", "markdownlint", "package.json")
	markdownlintPackageLockPath := filepath.Join(repoRoot, "tools", "markdownlint", "package-lock.json")
	rustToolchainPath := filepath.Join(repoRoot, "runtime", "container", "rust", "rust-toolchain.toml")

	runtimeDockerfile, err := readText(cfg.RuntimeDockerfilePath)
	if err != nil {
		return err
	}
	validatorDockerfile, err := readText(cfg.ValidatorDockerfilePath)
	if err != nil {
		return err
	}
	providersPackageJSONText, err := readText(cfg.ProvidersPackageJSONPath)
	if err != nil {
		return err
	}
	providersPackageLockText, err := readText(cfg.ProvidersPackageLockPath)
	if err != nil {
		return err
	}
	markdownlintPackageJSONText, err := readText(markdownlintPackageJSONPath)
	if err != nil {
		return err
	}
	markdownlintPackageLockText, err := readText(markdownlintPackageLockPath)
	if err != nil {
		return err
	}
	installDevToolsScript, err := readText(installDevToolsScriptPath)
	if err != nil {
		return err
	}
	ciWorkflow, err := readText(cfg.CIWorkflowPath)
	if err != nil {
		return err
	}
	releaseWorkflow, err := readText(cfg.ReleaseWorkflowPath)
	if err != nil {
		return err
	}
	pinHygieneWorkflow, err := readText(cfg.PinHygieneWorkflowPath)
	if err != nil {
		return err
	}
	upstreamRefreshWorkflow, err := readText(filepath.Join(cfg.WorkflowsDir, "upstream-refresh.yml"))
	if err != nil {
		return err
	}
	validatorImageScript, err := readText(filepath.Join(repoRoot, "scripts", "ci", "build-validator-image.sh"))
	if err != nil {
		return err
	}
	codeowners, err := readText(cfg.CodeownersPath)
	if err != nil {
		return err
	}
	hostedControlsPolicyText, err := readText(cfg.HostedControlsPolicyPath)
	if err != nil {
		return err
	}
	hostedControlsScript, err := readText(cfg.HostedControlsScriptPath)
	if err != nil {
		return err
	}
	codexRequirementsText, err := readText(cfg.CodexRequirementsPath)
	if err != nil {
		return err
	}
	codexMCPConfigText, err := readText(cfg.CodexMCPConfigPath)
	if err != nil {
		return err
	}
	goModText, err := readText(goModPath)
	if err != nil {
		return err
	}
	cargoManifestText, err := readText(cargoManifestPath)
	if err != nil {
		return err
	}
	rustToolchainText, err := readText(rustToolchainPath)
	if err != nil {
		return err
	}

	var providersPackageJSON map[string]any
	if err := json.Unmarshal([]byte(providersPackageJSONText), &providersPackageJSON); err != nil {
		return err
	}
	var providersPackageLock map[string]any
	if err := json.Unmarshal([]byte(providersPackageLockText), &providersPackageLock); err != nil {
		return err
	}
	var markdownlintPackageJSON struct {
		Dependencies map[string]string `json:"dependencies"`
	}
	if err := json.Unmarshal([]byte(markdownlintPackageJSONText), &markdownlintPackageJSON); err != nil {
		return err
	}
	var markdownlintPackageLock struct {
		Packages map[string]struct {
			Version      string            `json:"version"`
			Dependencies map[string]string `json:"dependencies"`
		} `json:"packages"`
	}
	if err := json.Unmarshal([]byte(markdownlintPackageLockText), &markdownlintPackageLock); err != nil {
		return err
	}
	hostedControlsPolicy, err := tomlsubset.Parse(hostedControlsPolicyText, cfg.HostedControlsPolicyPath)
	if err != nil {
		return err
	}

	requireArg := func(text, name, path string) (string, error) {
		match := regexp.MustCompile(`(?m)^ARG ` + regexp.QuoteMeta(name) + `=(.+)$`).FindStringSubmatch(text)
		if match == nil {
			return "", fmt.Errorf("unable to extract %s from %s", name, path)
		}
		return strings.TrimSpace(match[1]), nil
	}
	requireYAMLKey := func(text, name, path string) (string, error) {
		match := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(name) + `:\s*(.+)$`).FindStringSubmatch(text)
		if match == nil {
			return "", fmt.Errorf("unable to extract %s from %s", name, path)
		}
		return strings.TrimSpace(match[1]), nil
	}
	requirePinnedBaseImage := func(image, label, path string) error {
		if !regexp.MustCompile(`^[^@]+@sha256:[0-9a-f]{64}$`).MatchString(image) {
			return fmt.Errorf("%s in %s must be pinned by immutable digest, found %q", label, path, image)
		}
		return nil
	}
	verifySnapshotFreshness := func(snapshot, path string) error {
		ts, err := time.Parse("20060102T150405Z", snapshot)
		if err != nil {
			return fmt.Errorf("debian snapshot %s in %s is not valid", snapshot, path)
		}
		now := time.Now().UTC()
		ageDays := int(now.Sub(ts).Hours() / 24)
		if ageDays > cfg.MaxDebianSnapshotAgeDays {
			return fmt.Errorf(
				"debian snapshot %s in %s is %d days old; refresh it or raise WORKCELL_MAX_DEBIAN_SNAPSHOT_AGE_DAYS",
				snapshot,
				path,
				ageDays,
			)
		}
		return nil
	}
	extractInstallBlocks := func(text, path string) ([][]string, error) {
		matches := aptInstallPattern.FindAllStringSubmatch(text, -1)
		if len(matches) == 0 {
			return nil, fmt.Errorf("unable to find apt install blocks in %s", path)
		}
		blocks := make([][]string, 0, len(matches))
		for _, match := range matches {
			body := strings.ReplaceAll(match[1], "\\", " ")
			fields := strings.Fields(body)
			if len(fields) == 0 {
				return nil, fmt.Errorf("unable to extract package list from install block in %s", path)
			}
			blocks = append(blocks, fields)
		}
		return blocks, nil
	}
	requireExactPackages := func(actual, expected []string, label, path string) error {
		if len(actual) != len(expected) {
			return fmt.Errorf("%s package set in %s changed.\nexpected: %v\nactual:   %v", label, path, expected, actual)
		}
		for i := range actual {
			if actual[i] != expected[i] {
				return fmt.Errorf("%s package set in %s changed.\nexpected: %v\nactual:   %v", label, path, expected, actual)
			}
		}
		return nil
	}
	requireRegex := func(text, pattern, label, path string) (*regexp.Regexp, []string, error) {
		re := regexp.MustCompile(pattern)
		match := re.FindStringSubmatch(text)
		if match == nil {
			return nil, nil, fmt.Errorf("%s in %s must match %q", label, path, pattern)
		}
		return re, match, nil
	}
	requireDelimitedText := func(text, start, end, label, path string) (string, error) {
		startIndex := strings.Index(text, start)
		if startIndex < 0 {
			return "", fmt.Errorf("%s in %s must start with %q", label, path, start)
		}
		bodyStart := startIndex + len(start)
		endIndex := strings.Index(text[bodyStart:], end)
		if endIndex < 0 {
			return "", fmt.Errorf("%s in %s must end with %q", label, path, end)
		}
		return text[bodyStart : bodyStart+endIndex], nil
	}
	requireOrderedText := func(text, label, path string, needles []string) error {
		offset := 0
		for _, needle := range needles {
			index := strings.Index(text[offset:], needle)
			if index < 0 {
				return fmt.Errorf("%s in %s must contain %q after the previous required step", label, path, needle)
			}
			offset += index + len(needle)
		}
		return nil
	}
	rejectInstallScriptAptPackages := func(text, path string, disallowedPackages ...string) error {
		scanText := strings.ReplaceAll(text, "\\\n", " ")
		disallowed := map[string]struct{}{}
		for _, pkg := range disallowedPackages {
			disallowed[pkg] = struct{}{}
		}
		tokenREs := []struct {
			label string
			re    *regexp.Regexp
		}{
			{
				label: "append_unique_apt",
				re:    regexp.MustCompile(`(?m)^\s*append_unique_apt\s+([^\n#]+)`),
			},
			{
				label: "apt_missing",
				re:    regexp.MustCompile(`(?m)^\s*apt_missing\+\=\(([^)]*)\)`),
			},
			{
				label: "apt-get install",
				re:    regexp.MustCompile(`(?m)(?:^|&&)\s*(?:sudo\s+)?apt-get\s+install(?:\s+-[A-Za-z0-9-]+)*\s+([^\n#]+)`),
			},
		}
		for _, tokenRE := range tokenREs {
			for _, match := range tokenRE.re.FindAllStringSubmatch(scanText, -1) {
				for _, field := range strings.Fields(match[1]) {
					token := strings.Trim(field, `"'`)
					for _, separator := range []string{"=", ":", "/"} {
						if index := strings.Index(token, separator); index >= 0 {
							token = token[:index]
						}
					}
					if _, ok := disallowed[token]; ok {
						return fmt.Errorf("%s must not add %s to the Linux apt package set through %s", path, token, tokenRE.label)
					}
				}
			}
		}
		return nil
	}
	requireUniformWorkflowEnv := func(text, key, valuePattern, label, path string) (string, error) {
		lineRE := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(key) + `:\s*(\S+)\s*$`)
		valueRE := regexp.MustCompile(`^` + valuePattern + `$`)
		matches := lineRE.FindAllStringSubmatch(text, -1)
		if len(matches) == 0 {
			return "", fmt.Errorf("%s in %s must define %s", label, path, key)
		}
		value := matches[0][1]
		for _, match := range matches {
			if !valueRE.MatchString(match[1]) {
				return "", fmt.Errorf("%s in %s must match %q", label, path, valuePattern)
			}
			if match[1] != value {
				return "", fmt.Errorf("%s in %s must use one reviewed value for %s", label, path, key)
			}
		}
		return value, nil
	}
	requireCappedReleaseDownloads := func(text, path string, downloads []struct {
		label string
		url   string
	}) error {
		for _, download := range downloads {
			offset := 0
			found := false
			for {
				relativeURLIndex := strings.Index(text[offset:], download.url)
				if relativeURLIndex < 0 {
					break
				}
				found = true
				urlIndex := offset + relativeURLIndex
				curlIndex := strings.LastIndex(text[:urlIndex], "curl -fsSL")
				if curlIndex < 0 {
					return fmt.Errorf("%s must download %s with curl -fsSL", path, download.label)
				}
				block := text[curlIndex : urlIndex+len(download.url)]
				for _, needle := range []string{"--max-time 60", "--connect-timeout 15", "--max-filesize 209715200"} {
					if !strings.Contains(block, needle) {
						return fmt.Errorf("%s must bound %s downloads with %s", path, download.label, needle)
					}
				}
				offset = urlIndex + len(download.url)
			}
			if !found {
				return fmt.Errorf("%s must derive the %s archive URL from its pinned version", path, download.label)
			}
		}
		return nil
	}
	requireActionRef := func(text, action, path string) (string, error) {
		re := regexp.MustCompile(regexp.QuoteMeta(action) + `@([0-9a-f]{40})`)
		matches := re.FindAllStringSubmatch(text, -1)
		if len(matches) == 0 {
			return "", fmt.Errorf("%s must pin %s to an immutable commit SHA", path, action)
		}
		refs := map[string]struct{}{}
		for _, match := range matches {
			refs[match[1]] = struct{}{}
		}
		if len(refs) != 1 {
			return "", fmt.Errorf("%s must use a single reviewed ref for %s", path, action)
		}
		for ref := range refs {
			return ref, nil
		}
		return "", nil
	}
	requireGoDirective := func(text, directive, path string) (string, error) {
		match := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(directive) + ` ([0-9]+\.[0-9]+\.[0-9]+)$`).FindStringSubmatch(text)
		if match == nil {
			return "", fmt.Errorf("unable to extract %s from %s", directive, path)
		}
		return match[1], nil
	}
	requireToolchainDirective := func(text, path string) (string, error) {
		match := regexp.MustCompile(`(?m)^toolchain go([0-9]+\.[0-9]+\.[0-9]+)$`).FindStringSubmatch(text)
		if match == nil {
			return "", fmt.Errorf("unable to extract toolchain from %s", path)
		}
		return match[1], nil
	}
	requireTOMLString := func(text, key, path string) (string, error) {
		match := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(key) + `\s*=\s*"([^"]+)"\s*$`).FindStringSubmatch(text)
		if match == nil {
			return "", fmt.Errorf("unable to extract %s from %s", key, path)
		}
		return match[1], nil
	}
	requireEqual := func(label, left, leftPath, right, rightPath string) error {
		if left != right {
			return fmt.Errorf("%s must match between %s (%q) and %s (%q)", label, leftPath, left, rightPath, right)
		}
		return nil
	}
	majorMinor := func(version, path string) (string, error) {
		match := regexp.MustCompile(`^([0-9]+\.[0-9]+)\.[0-9]+$`).FindStringSubmatch(version)
		if match == nil {
			return "", fmt.Errorf("expected a semantic version in %s, found %q", path, version)
		}
		return match[1], nil
	}
	goLanguageVersionFromToolchain := func(version, path string) (string, error) {
		match := regexp.MustCompile(`^([0-9]+\.[0-9]+)\.[0-9]+$`).FindStringSubmatch(version)
		if match == nil {
			return "", fmt.Errorf("expected a semantic Go toolchain version in %s, found %q", path, version)
		}
		return match[1] + ".0", nil
	}
	extractReproMatrixEntries := func(strategyBlock, path string) ([][3]string, error) {
		re := regexp.MustCompile(`(?m)^\s{10}- platform:\s*(\S+)\n^\s{12}platform_name:\s*(\S+)\n^\s{12}runner:\s*(\S+)$`)
		matches := re.FindAllStringSubmatch(strategyBlock, -1)
		if len(matches) == 0 {
			return nil, fmt.Errorf("unable to extract reproducible-build matrix entries from %s", path)
		}
		result := make([][3]string, 0, len(matches))
		for _, match := range matches {
			result = append(result, [3]string{match[1], match[2], match[3]})
		}
		return result, nil
	}
	requireNoRegistryBootstrapMCP := func(text, path string) error {
		disallowedFragments := []string{
			"npx",
			"npm exec",
			"pnpm dlx",
			"yarn dlx",
			"bunx",
			"@upstash/context7-mcp",
			"exa-mcp-server",
		}
		lines := strings.Split(text, "\n")
		for _, line := range lines {
			line = tomlsubset.StripComment(line)
			lower := strings.ToLower(line)
			for _, fragment := range disallowedFragments {
				if strings.Contains(lower, fragment) {
					return fmt.Errorf("%s must not seed mutable registry-backed MCP commands; found %q", path, line)
				}
			}
		}
		return nil
	}

	runtimeBaseImage, err := requireArg(runtimeDockerfile, "NODE_BASE_IMAGE", cfg.RuntimeDockerfilePath)
	if err != nil {
		return err
	}
	validatorBaseImage, err := requireArg(validatorDockerfile, "VALIDATOR_BASE_IMAGE", cfg.ValidatorDockerfilePath)
	if err != nil {
		return err
	}
	runtimeSnapshot, err := requireArg(runtimeDockerfile, "DEBIAN_SNAPSHOT", cfg.RuntimeDockerfilePath)
	if err != nil {
		return err
	}
	validatorSnapshot, err := requireArg(validatorDockerfile, "DEBIAN_SNAPSHOT", cfg.ValidatorDockerfilePath)
	if err != nil {
		return err
	}
	codexVersion, err := requireArg(runtimeDockerfile, "CODEX_VERSION", cfg.RuntimeDockerfilePath)
	if err != nil {
		return err
	}
	claudeVersion, err := requireArg(runtimeDockerfile, "CLAUDE_VERSION", cfg.RuntimeDockerfilePath)
	if err != nil {
		return err
	}
	copilotVersion, err := requireArg(runtimeDockerfile, "COPILOT_VERSION", cfg.RuntimeDockerfilePath)
	if err != nil {
		return err
	}

	runtimeInstallBlocks, err := extractInstallBlocks(runtimeDockerfile, cfg.RuntimeDockerfilePath)
	if err != nil {
		return err
	}
	validatorInstallBlocks, err := extractInstallBlocks(validatorDockerfile, cfg.ValidatorDockerfilePath)
	if err != nil {
		return err
	}

	if err := requirePinnedBaseImage(runtimeBaseImage, "NODE_BASE_IMAGE", cfg.RuntimeDockerfilePath); err != nil {
		return err
	}
	if err := requirePinnedBaseImage(validatorBaseImage, "VALIDATOR_BASE_IMAGE", cfg.ValidatorDockerfilePath); err != nil {
		return err
	}
	if err := verifySnapshotFreshness(runtimeSnapshot, cfg.RuntimeDockerfilePath); err != nil {
		return err
	}
	if err := verifySnapshotFreshness(validatorSnapshot, cfg.ValidatorDockerfilePath); err != nil {
		return err
	}
	if err := requireNoRegistryBootstrapMCP(codexRequirementsText, cfg.CodexRequirementsPath); err != nil {
		return err
	}
	if err := requireNoRegistryBootstrapMCP(codexMCPConfigText, cfg.CodexMCPConfigPath); err != nil {
		return err
	}
	if err := CheckProviderBumpPolicy(cfg.ProviderBumpPolicyPath, cfg.RuntimeDockerfilePath, cfg.ProvidersPackageJSONPath); err != nil {
		return err
	}
	if _, _, err := requireRegex(runtimeDockerfile, `curl --ipv4 -fsSL "https://storage\.googleapis\.com/claude-code-dist-86c565f3-f756-42ad-8dfa-d59b1c096819/claude-code-releases/\$\{CLAUDE_VERSION\}/\$\{CLAUDE_PLATFORM\}/claude"`, "Claude native release download URL", cfg.RuntimeDockerfilePath); err != nil {
		return err
	}
	if _, _, err := requireRegex(runtimeDockerfile, `curl --ipv4 -fsSL "https://github\.com/github/copilot-cli/releases/download/v\$\{COPILOT_VERSION\}/copilot-\$\{COPILOT_PLATFORM\}\.tar\.gz"`, "Copilot native release download URL", cfg.RuntimeDockerfilePath); err != nil {
		return err
	}
	if _, _, err := requireRegex(runtimeDockerfile, `install -m 0644 /tmp/copilot /usr/local/libexec/workcell/real/copilot`, "non-executable Copilot provenance artifact install", cfg.RuntimeDockerfilePath); err != nil {
		return err
	}
	if _, match, err := requireRegex(runtimeDockerfile, `(?m)^\s*arm64\)\s+\\\s*CLAUDE_PLATFORM="([^"]+)";\s+\\\s*CLAUDE_SHA256="([0-9a-f]{64})";`, "arm64 Claude mapping", cfg.RuntimeDockerfilePath); err != nil {
		return err
	} else if match[1] != "linux-arm64" {
		return fmt.Errorf("arm64 Claude mapping in %s must use linux-arm64", cfg.RuntimeDockerfilePath)
	}
	if _, match, err := requireRegex(runtimeDockerfile, `(?m)^\s*amd64\)\s+\\\s*CLAUDE_PLATFORM="([^"]+)";\s+\\\s*CLAUDE_SHA256="([0-9a-f]{64})";`, "amd64 Claude mapping", cfg.RuntimeDockerfilePath); err != nil {
		return err
	} else if match[1] != "linux-x64" {
		return fmt.Errorf("amd64 Claude mapping in %s must use linux-x64", cfg.RuntimeDockerfilePath)
	}
	if !regexp.MustCompile(`^0\.[0-9]+\.[0-9]+(?:-[A-Za-z0-9.-]+)?$`).MatchString(codexVersion) {
		return fmt.Errorf("runtime/container/Dockerfile CODEX_VERSION must stay pinned to an explicit release, found %q", codexVersion)
	}
	if !regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[A-Za-z0-9.-]+)?$`).MatchString(claudeVersion) {
		return fmt.Errorf("runtime/container/Dockerfile CLAUDE_VERSION must stay pinned to an explicit release, found %q", claudeVersion)
	}
	if !regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(copilotVersion) {
		return fmt.Errorf("runtime/container/Dockerfile COPILOT_VERSION must stay pinned to a non-prerelease GitHub Copilot CLI release, found %q", copilotVersion)
	}
	if _, match, err := requireRegex(runtimeDockerfile, `(?m)^\s*arm64\)\s+\\(?:\s*CLAUDE_[A-Z0-9_]+="[^"]+";\s+\\)*\s*CODEX_ARCH="([^"]+)";\s+\\\s*CODEX_SHA256="([0-9a-f]{64})";`, "arm64 Codex mapping", cfg.RuntimeDockerfilePath); err != nil {
		return err
	} else if match[1] != "aarch64-unknown-linux-musl" {
		return fmt.Errorf("arm64 Codex mapping in %s must use aarch64-unknown-linux-musl", cfg.RuntimeDockerfilePath)
	}
	if _, match, err := requireRegex(runtimeDockerfile, `(?m)^\s*amd64\)\s+\\(?:\s*CLAUDE_[A-Z0-9_]+="[^"]+";\s+\\)*\s*CODEX_ARCH="([^"]+)";\s+\\\s*CODEX_SHA256="([0-9a-f]{64})";`, "amd64 Codex mapping", cfg.RuntimeDockerfilePath); err != nil {
		return err
	} else if match[1] != "x86_64-unknown-linux-musl" {
		return fmt.Errorf("amd64 Codex mapping in %s must use x86_64-unknown-linux-musl", cfg.RuntimeDockerfilePath)
	}
	if _, match, err := requireRegex(runtimeDockerfile, `(?m)^\s*arm64\)\s+\\\s*COPILOT_PLATFORM="([^"]+)";\s+\\\s*COPILOT_SHA256="([0-9a-f]{64})";`, "arm64 Copilot mapping", cfg.RuntimeDockerfilePath); err != nil {
		return err
	} else if match[1] != "linux-arm64" {
		return fmt.Errorf("arm64 Copilot mapping in %s must use linux-arm64", cfg.RuntimeDockerfilePath)
	}
	if _, match, err := requireRegex(runtimeDockerfile, `(?m)^\s*amd64\)\s+\\\s*COPILOT_PLATFORM="([^"]+)";\s+\\\s*COPILOT_SHA256="([0-9a-f]{64})";`, "amd64 Copilot mapping", cfg.RuntimeDockerfilePath); err != nil {
		return err
	} else if match[1] != "linux-x64" {
		return fmt.Errorf("amd64 Copilot mapping in %s must use linux-x64", cfg.RuntimeDockerfilePath)
	}
	if len(runtimeInstallBlocks) != 2 {
		return fmt.Errorf("runtime/container/Dockerfile must contain exactly two apt install blocks (runtime base and runtime builder)")
	}
	if len(validatorInstallBlocks) != 1 {
		return errors.New("tools/validator/Dockerfile must contain exactly one apt install block")
	}
	if err := requireExactPackages(runtimeInstallBlocks[0], []string{"bash", "bubblewrap", "ca-certificates", "curl", "fd-find", "git", "jq", "less", "openssh-client", "passwd", "procps", "ripgrep", "sudo", "unzip", "util-linux", "xz-utils"}, "Runtime base", cfg.RuntimeDockerfilePath); err != nil {
		return err
	}
	if err := requireExactPackages(runtimeInstallBlocks[1], []string{"gcc", "libc6-dev"}, "Runtime builder", cfg.RuntimeDockerfilePath); err != nil {
		return err
	}
	if err := requireExactPackages(validatorInstallBlocks[0], []string{"ca-certificates", "codespell", "curl", "gcc", "git", "groff-base", "jq", "libc6-dev", "llvm", "mandoc", "openssh-client", "procps", "shellcheck", "shfmt", "yamllint"}, "Validator", cfg.ValidatorDockerfilePath); err != nil {
		return err
	}
	goLanguageVersion, err := requireGoDirective(goModText, "go", goModPath)
	if err != nil {
		return err
	}
	goToolchainVersion, err := requireToolchainDirective(goModText, goModPath)
	if err != nil {
		return err
	}
	validatorGoVersion, err := requireArg(validatorDockerfile, "GO_VERSION", cfg.ValidatorDockerfilePath)
	if err != nil {
		return err
	}
	if err := requireEqual("Go toolchain version", goToolchainVersion, goModPath, validatorGoVersion, cfg.ValidatorDockerfilePath); err != nil {
		return err
	}
	expectedGoLanguageVersion, err := goLanguageVersionFromToolchain(goToolchainVersion, goModPath)
	if err != nil {
		return err
	}
	if goLanguageVersion != expectedGoLanguageVersion {
		return fmt.Errorf("go language version in %s must match the toolchain major/minor at patch zero, expected %q, found %q", goModPath, expectedGoLanguageVersion, goLanguageVersion)
	}
	validatorGoSHAx86_64, err := requireArg(validatorDockerfile, "GO_LINUX_X86_64_SHA256", cfg.ValidatorDockerfilePath)
	if err != nil {
		return err
	}
	if !isHexDigest(validatorGoSHAx86_64) {
		return fmt.Errorf("GO_LINUX_X86_64_SHA256 in %s must be a full SHA256 digest, found %q", cfg.ValidatorDockerfilePath, validatorGoSHAx86_64)
	}
	validatorGoSHAArm64, err := requireArg(validatorDockerfile, "GO_LINUX_ARM64_SHA256", cfg.ValidatorDockerfilePath)
	if err != nil {
		return err
	}
	if !isHexDigest(validatorGoSHAArm64) {
		return fmt.Errorf("GO_LINUX_ARM64_SHA256 in %s must be a full SHA256 digest, found %q", cfg.ValidatorDockerfilePath, validatorGoSHAArm64)
	}
	validatorHadolintVersion, err := requireArg(validatorDockerfile, "HADOLINT_VERSION", cfg.ValidatorDockerfilePath)
	if err != nil {
		return err
	}
	if !pinnedReleaseTagPattern.MatchString(validatorHadolintVersion) {
		return fmt.Errorf("HADOLINT_VERSION must be an exact pinned release, found %q", validatorHadolintVersion)
	}
	validatorHadolintSHAx86_64, err := requireArg(validatorDockerfile, "HADOLINT_LINUX_X86_64_SHA256", cfg.ValidatorDockerfilePath)
	if err != nil {
		return err
	}
	if !isHexDigest(validatorHadolintSHAx86_64) {
		return fmt.Errorf("HADOLINT_LINUX_X86_64_SHA256 in %s must be a full SHA256 digest, found %q", cfg.ValidatorDockerfilePath, validatorHadolintSHAx86_64)
	}
	validatorHadolintSHAArm64, err := requireArg(validatorDockerfile, "HADOLINT_LINUX_ARM64_SHA256", cfg.ValidatorDockerfilePath)
	if err != nil {
		return err
	}
	if !isHexDigest(validatorHadolintSHAArm64) {
		return fmt.Errorf("HADOLINT_LINUX_ARM64_SHA256 in %s must be a full SHA256 digest, found %q", cfg.ValidatorDockerfilePath, validatorHadolintSHAArm64)
	}
	validatorDeadcodeVersion, err := requireArg(validatorDockerfile, "DEADCODE_VERSION", cfg.ValidatorDockerfilePath)
	if err != nil {
		return err
	}
	if !pinnedReleaseTagPattern.MatchString(validatorDeadcodeVersion) {
		return fmt.Errorf("DEADCODE_VERSION must be an exact pinned release, found %q", validatorDeadcodeVersion)
	}
	validatorMarkdownlintVersion, err := requireArg(validatorDockerfile, "MARKDOWNLINT_VERSION", cfg.ValidatorDockerfilePath)
	if err != nil {
		return err
	}
	if !regexp.MustCompile(`^0\.\d+\.\d+$`).MatchString(validatorMarkdownlintVersion) {
		return fmt.Errorf("MARKDOWNLINT_VERSION must be an exact pinned release, found %q", validatorMarkdownlintVersion)
	}
	markdownlintDependency, ok := markdownlintPackageJSON.Dependencies["markdownlint-cli"]
	if !ok {
		return fmt.Errorf("%s must depend on markdownlint-cli", markdownlintPackageJSONPath)
	}
	if markdownlintDependency != validatorMarkdownlintVersion {
		return fmt.Errorf("markdownlint-cli version must match between %s and %s; found %q and %q", markdownlintPackageJSONPath, cfg.ValidatorDockerfilePath, markdownlintDependency, validatorMarkdownlintVersion)
	}
	markdownlintLockRoot, ok := markdownlintPackageLock.Packages[""]
	if !ok {
		return fmt.Errorf("%s must include the root package lock entry", markdownlintPackageLockPath)
	}
	if markdownlintLockRoot.Dependencies["markdownlint-cli"] != validatorMarkdownlintVersion {
		return fmt.Errorf("markdownlint-cli version must match between %s and %s; found %q and %q", markdownlintPackageLockPath, cfg.ValidatorDockerfilePath, markdownlintLockRoot.Dependencies["markdownlint-cli"], validatorMarkdownlintVersion)
	}
	markdownlintLockPackage, ok := markdownlintPackageLock.Packages["node_modules/markdownlint-cli"]
	if !ok {
		return fmt.Errorf("%s must lock node_modules/markdownlint-cli", markdownlintPackageLockPath)
	}
	if markdownlintLockPackage.Version != validatorMarkdownlintVersion {
		return fmt.Errorf("locked markdownlint-cli package version must match %s; found %q and %q", cfg.ValidatorDockerfilePath, markdownlintLockPackage.Version, validatorMarkdownlintVersion)
	}
	_, installMarkdownlintVersionMatch, err := requireRegex(installDevToolsScript, `(?m)^readonly MARKDOWNLINT_VERSION="([0-9]+\.[0-9]+\.[0-9]+)"$`, "install-dev-tools markdownlint version", installDevToolsScriptPath)
	if err != nil {
		return err
	}
	if installMarkdownlintVersionMatch[1] != validatorMarkdownlintVersion {
		return fmt.Errorf("MARKDOWNLINT_VERSION must match between %s and %s; found %q and %q", installDevToolsScriptPath, cfg.ValidatorDockerfilePath, installMarkdownlintVersionMatch[1], validatorMarkdownlintVersion)
	}
	_, markdownlintNodeFloorMatch, err := requireRegex(installDevToolsScript, `(?m)^readonly MARKDOWNLINT_NODE_VERSION_MINIMUM="([0-9]+\.[0-9]+\.[0-9]+)"$`, "install-dev-tools markdownlint Node floor", installDevToolsScriptPath)
	if err != nil {
		return err
	}
	if markdownlintNodeFloorMatch[1] != "22.12.0" {
		return fmt.Errorf("MARKDOWNLINT_NODE_VERSION_MINIMUM in %s must be 22.12.0 for markdownlint-cli@%s, found %q", installDevToolsScriptPath, validatorMarkdownlintVersion, markdownlintNodeFloorMatch[1])
	}
	if err := rejectInstallScriptAptPackages(installDevToolsScript, installDevToolsScriptPath, "nodejs", "npm"); err != nil {
		return err
	}
	if !strings.Contains(installDevToolsScript, "if [[ \"${host_os}\" == \"Linux\" ]] && markdownlint_needs_install; then\n  require_markdownlint_node\n  require_markdownlint_npm\nfi\n\nif [[ ${#missing[@]} -gt 0 ]]; then") {
		return fmt.Errorf("%s must validate Linux Node.js/npm compatibility before apt installs when markdownlint-cli needs installation", installDevToolsScriptPath)
	}
	markdownlintInstallBody, err := requireDelimitedText(
		installDevToolsScript,
		"if markdownlint_needs_install; then\n",
		"\nfi\n\necho \"Done.\"",
		"markdownlint install block",
		installDevToolsScriptPath,
	)
	if err != nil {
		return err
	}
	if err := requireOrderedText(markdownlintInstallBody, "markdownlint install block", installDevToolsScriptPath, []string{
		"require_markdownlint_node",
		"require_markdownlint_npm",
		"npm install -g \"markdownlint-cli@${MARKDOWNLINT_VERSION}\"",
	}); err != nil {
		return fmt.Errorf("%s must validate the Node.js floor and npm immediately before installing markdownlint-cli: %w", installDevToolsScriptPath, err)
	}
	markdownlintNodeHintBody, err := requireDelimitedText(
		installDevToolsScript,
		"markdownlint_node_install_hint() {\n",
		"\n}\n\nrequire_markdownlint_node()",
		"markdownlint Node.js upgrade hint",
		installDevToolsScriptPath,
	)
	if err != nil {
		return err
	}
	for _, needle := range []string{
		"Install Node.js ${MARKDOWNLINT_NODE_VERSION_MINIMUM} or newer before installing markdownlint-cli@${MARKDOWNLINT_VERSION}.",
		"Homebrew's node package",
		"NodeSource",
		"nvm",
		"asdf",
		"Ubuntu 24.04's nodejs/npm apt packages are too old for this markdownlint release.",
		"Then rerun scripts/install-dev-tools.sh.",
	} {
		if !strings.Contains(markdownlintNodeHintBody, needle) {
			return fmt.Errorf("%s must print manual Node.js upgrade instructions before failing markdownlint-cli installation", installDevToolsScriptPath)
		}
	}
	requireMarkdownlintNodeBody, err := requireDelimitedText(
		installDevToolsScript,
		"require_markdownlint_node() {\n",
		"\n}\n\nrequire_markdownlint_npm()",
		"markdownlint Node.js floor check",
		installDevToolsScriptPath,
	)
	if err != nil {
		return err
	}
	for _, needle := range []string{
		"no usable node binary was found.\" >&2\n    markdownlint_node_install_hint\n    exit 1",
		"found ${version}.\" >&2\n    markdownlint_node_install_hint\n    exit 1",
	} {
		if !strings.Contains(requireMarkdownlintNodeBody, needle) {
			return fmt.Errorf("%s must print Node.js upgrade instructions before exiting every markdownlint-cli Node.js floor failure path", installDevToolsScriptPath)
		}
	}
	requireMarkdownlintNPMBody, err := requireDelimitedText(
		installDevToolsScript,
		"require_markdownlint_npm() {\n",
		"\n}\n\nmarkdownlint_needs_install()",
		"markdownlint npm check",
		installDevToolsScriptPath,
	)
	if err != nil {
		return err
	}
	for _, needle := range []string{
		"command -v npm &>/dev/null",
		"requires npm from a Node.js ${MARKDOWNLINT_NODE_VERSION_MINIMUM} or newer installation.\" >&2\n  markdownlint_node_install_hint\n  exit 1",
	} {
		if !strings.Contains(requireMarkdownlintNPMBody, needle) {
			return fmt.Errorf("%s must print Node.js upgrade instructions before exiting the markdownlint-cli npm failure path", installDevToolsScriptPath)
		}
	}
	for _, needle := range []string{
		`GOBIN=/usr/local/bin go install "golang.org/x/tools/cmd/deadcode@${DEADCODE_VERSION}"`,
		`COPY tools/markdownlint/package.json tools/markdownlint/package-lock.json /usr/local/lib/workcell-markdownlint/`,
		`deadcode -h >/dev/null`,
		`npm ci --prefix /usr/local/lib/workcell-markdownlint --ignore-scripts --omit=dev`,
		`ln -sf /usr/local/lib/workcell-markdownlint/node_modules/.bin/markdownlint /usr/local/bin/markdownlint`,
		`markdownlint --version | grep -F "${MARKDOWNLINT_VERSION}" >/dev/null`,
	} {
		if !strings.Contains(validatorDockerfile, needle) {
			return fmt.Errorf("%s must contain %q", cfg.ValidatorDockerfilePath, needle)
		}
	}
	cargoEdition, err := requireTOMLString(cargoManifestText, "edition", cargoManifestPath)
	if err != nil {
		return err
	}
	if cargoEdition != "2024" {
		return fmt.Errorf("%s must use edition 2024, found %q", cargoManifestPath, cargoEdition)
	}
	cargoRustVersion, err := requireTOMLString(cargoManifestText, "rust-version", cargoManifestPath)
	if err != nil {
		return err
	}
	rustToolchainVersion, err := requireTOMLString(rustToolchainText, "channel", rustToolchainPath)
	if err != nil {
		return err
	}
	runtimeRustVersion, err := requireArg(runtimeDockerfile, "RUST_VERSION", cfg.RuntimeDockerfilePath)
	if err != nil {
		return err
	}
	runtimeRustToolchainImage, err := requireArg(runtimeDockerfile, "RUST_TOOLCHAIN_IMAGE", cfg.RuntimeDockerfilePath)
	if err != nil {
		return err
	}
	validatorRustVersion, err := requireArg(validatorDockerfile, "RUST_VERSION", cfg.ValidatorDockerfilePath)
	if err != nil {
		return err
	}
	if err := requireEqual("RUST_VERSION", runtimeRustVersion, cfg.RuntimeDockerfilePath, validatorRustVersion, cfg.ValidatorDockerfilePath); err != nil {
		return err
	}
	if err := requireEqual("Rust toolchain channel", rustToolchainVersion, rustToolchainPath, runtimeRustVersion, cfg.RuntimeDockerfilePath); err != nil {
		return err
	}
	if err := requirePinnedBaseImage(runtimeRustToolchainImage, "RUST_TOOLCHAIN_IMAGE", cfg.RuntimeDockerfilePath); err != nil {
		return err
	}
	expectedRustToolchainTrack := fmt.Sprintf("rust:%s-slim-trixie@", runtimeRustVersion)
	if !strings.Contains(runtimeRustToolchainImage, expectedRustToolchainTrack) {
		return fmt.Errorf("RUST_TOOLCHAIN_IMAGE in %s must pin the official rust:%s-slim-trixie image, found %q", cfg.RuntimeDockerfilePath, runtimeRustVersion, runtimeRustToolchainImage)
	}
	expectedCargoRustVersion, err := majorMinor(rustToolchainVersion, rustToolchainPath)
	if err != nil {
		return err
	}
	if cargoRustVersion != expectedCargoRustVersion {
		return fmt.Errorf("rust-version in %s must match the pinned toolchain major/minor, expected %q, found %q", cargoManifestPath, expectedCargoRustVersion, cargoRustVersion)
	}
	validatorRustupVersion, err := requireArg(validatorDockerfile, "RUSTUP_VERSION", cfg.ValidatorDockerfilePath)
	if err != nil {
		return err
	}
	if !regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[A-Za-z0-9.-]+)?$`).MatchString(validatorRustupVersion) {
		return fmt.Errorf("RUSTUP_VERSION must be an exact pinned release, found %q", validatorRustupVersion)
	}
	validatorRustupSHAx86_64, err := requireArg(validatorDockerfile, "RUSTUP_INIT_LINUX_X86_64_SHA256", cfg.ValidatorDockerfilePath)
	if err != nil {
		return err
	}
	if !isHexDigest(validatorRustupSHAx86_64) {
		return fmt.Errorf("RUSTUP_INIT_LINUX_X86_64_SHA256 in %s must be a full SHA256 digest, found %q", cfg.ValidatorDockerfilePath, validatorRustupSHAx86_64)
	}
	validatorRustupSHAArm64, err := requireArg(validatorDockerfile, "RUSTUP_INIT_LINUX_ARM64_SHA256", cfg.ValidatorDockerfilePath)
	if err != nil {
		return err
	}
	if !isHexDigest(validatorRustupSHAArm64) {
		return fmt.Errorf("RUSTUP_INIT_LINUX_ARM64_SHA256 in %s must be a full SHA256 digest, found %q", cfg.ValidatorDockerfilePath, validatorRustupSHAArm64)
	}

	rootPackage, _ := providersPackageLock["packages"].(map[string]any)
	rootDependencies, _ := rootPackage[""].(map[string]any)
	expectedDependencies, _ := providersPackageJSON["dependencies"].(map[string]any)
	actualDependencies, _ := rootDependencies["dependencies"].(map[string]any)
	if len(actualDependencies) != len(expectedDependencies) {
		return errors.New("runtime/container/providers/package-lock.json root dependencies do not match package.json")
	}
	for name, expected := range expectedDependencies {
		if actualDependencies[name] != expected {
			return errors.New("runtime/container/providers/package-lock.json root dependencies do not match package.json")
		}
	}
	for packageName, expectedVersionAny := range expectedDependencies {
		expectedVersion, _ := expectedVersionAny.(string)
		pkgEntry, ok := rootPackage["node_modules/"+packageName].(map[string]any)
		if !ok {
			return fmt.Errorf("missing pinned provider package entry for %s", packageName)
		}
		if version, _ := pkgEntry["version"].(string); version != expectedVersion {
			return fmt.Errorf("pinned provider package %s is %s, expected %s", packageName, version, expectedVersion)
		}
		if integrity, _ := pkgEntry["integrity"].(string); integrity == "" {
			return fmt.Errorf("pinned provider package %s is missing an integrity hash", packageName)
		}
		if resolved, _ := pkgEntry["resolved"].(string); !strings.HasPrefix(resolved, "https://registry.npmjs.org/") {
			return fmt.Errorf("pinned provider package %s uses an unexpected source: %q", packageName, resolved)
		}
	}
	for packagePath, rawEntry := range rootPackage {
		if packagePath == "" {
			continue
		}
		entry, _ := rawEntry.(map[string]any)
		if link, _ := entry["link"].(bool); link {
			return fmt.Errorf("linked npm dependencies are not allowed in the provider lockfile: %s", packagePath)
		}
		if integrity, _ := entry["integrity"].(string); integrity == "" {
			return fmt.Errorf("provider lockfile entry is missing integrity data: %s", packagePath)
		}
		if resolved, _ := entry["resolved"].(string); !strings.HasPrefix(resolved, "https://registry.npmjs.org/") {
			return fmt.Errorf("provider lockfile entry uses an unexpected source (%s): %q", packagePath, resolved)
		}
	}

	ciBuildxVersion, err := requireYAMLKey(ciWorkflow, "WORKCELL_BUILDX_VERSION", ".github/workflows/ci.yml")
	if err != nil {
		return err
	}
	releaseBuildxVersion, err := requireYAMLKey(releaseWorkflow, "WORKCELL_BUILDX_VERSION", ".github/workflows/release.yml")
	if err != nil {
		return err
	}
	if ciBuildxVersion != releaseBuildxVersion {
		return errors.New("WORKCELL_BUILDX_VERSION must match between .github/workflows/ci.yml and .github/workflows/release.yml")
	}
	if !pinnedReleaseTagPattern.MatchString(ciBuildxVersion) {
		return fmt.Errorf("WORKCELL_BUILDX_VERSION must be an exact pinned release (for example v0.32.1), found %q", ciBuildxVersion)
	}

	ciQEMUImage, err := requireYAMLKey(ciWorkflow, "WORKCELL_QEMU_IMAGE", ".github/workflows/ci.yml")
	if err != nil {
		return err
	}
	releaseQEMUImage, err := requireYAMLKey(releaseWorkflow, "WORKCELL_QEMU_IMAGE", ".github/workflows/release.yml")
	if err != nil {
		return err
	}
	if ciQEMUImage != releaseQEMUImage {
		return errors.New("WORKCELL_QEMU_IMAGE must match between .github/workflows/ci.yml and .github/workflows/release.yml")
	}
	if err := requirePinnedBaseImage(ciQEMUImage, "WORKCELL_QEMU_IMAGE", ".github/workflows/ci.yml"); err != nil {
		return err
	}
	ciBuildkitImage, err := requireYAMLKey(ciWorkflow, "WORKCELL_BUILDKIT_IMAGE", ".github/workflows/ci.yml")
	if err != nil {
		return err
	}
	releaseBuildkitImage, err := requireYAMLKey(releaseWorkflow, "WORKCELL_BUILDKIT_IMAGE", ".github/workflows/release.yml")
	if err != nil {
		return err
	}
	if ciBuildkitImage != releaseBuildkitImage {
		return errors.New("WORKCELL_BUILDKIT_IMAGE must match between .github/workflows/ci.yml and .github/workflows/release.yml")
	}
	if err := requirePinnedBaseImage(ciBuildkitImage, "WORKCELL_BUILDKIT_IMAGE", ".github/workflows/ci.yml"); err != nil {
		return err
	}
	for _, workflowPath := range mustGlob(filepath.Join(cfg.WorkflowsDir, "*.yml")) {
		workflowText, err := readText(workflowPath)
		if err != nil {
			return err
		}
		workflowName := ".github/workflows/" + filepath.Base(workflowPath)
		if regexp.MustCompile(`(?m)^\s*WORKCELL_BUILDX_VERSION:`).MatchString(workflowText) {
			workflowBuildxVersion, err := requireYAMLKey(workflowText, "WORKCELL_BUILDX_VERSION", workflowName)
			if err != nil {
				return err
			}
			if err := requireEqual("WORKCELL_BUILDX_VERSION", ciBuildxVersion, ".github/workflows/ci.yml", workflowBuildxVersion, workflowName); err != nil {
				return err
			}
		}
		if regexp.MustCompile(`(?m)^\s*WORKCELL_BUILDKIT_IMAGE:`).MatchString(workflowText) {
			workflowBuildkitImage, err := requireYAMLKey(workflowText, "WORKCELL_BUILDKIT_IMAGE", workflowName)
			if err != nil {
				return err
			}
			if err := requireEqual("WORKCELL_BUILDKIT_IMAGE", ciBuildkitImage, ".github/workflows/ci.yml", workflowBuildkitImage, workflowName); err != nil {
				return err
			}
			if !strings.Contains(workflowText, "driver-opts: image=${{ env.WORKCELL_BUILDKIT_IMAGE }}") {
				return fmt.Errorf("%s must pin the BuildKit daemon image used by setup-buildx-action", workflowName)
			}
		}
	}
	validatorImageFallback := regexp.MustCompile(`(?m)^BUILDKIT_IMAGE="\$\{WORKCELL_BUILDKIT_IMAGE:-([^}]+)\}"$`).FindStringSubmatch(validatorImageScript)
	if validatorImageFallback == nil {
		return errors.New("scripts/ci/build-validator-image.sh must default BUILDKIT_IMAGE from WORKCELL_BUILDKIT_IMAGE with a pinned fallback")
	}
	if err := requireEqual("WORKCELL_BUILDKIT_IMAGE", ciBuildkitImage, ".github/workflows/ci.yml", validatorImageFallback[1], "scripts/ci/build-validator-image.sh"); err != nil {
		return err
	}

	ciCosignVersion, err := requireYAMLKey(ciWorkflow, "WORKCELL_COSIGN_VERSION", ".github/workflows/ci.yml")
	if err != nil {
		return err
	}
	releaseCosignVersion, err := requireYAMLKey(releaseWorkflow, "WORKCELL_COSIGN_VERSION", ".github/workflows/release.yml")
	if err != nil {
		return err
	}
	pinHygieneCosignVersion, err := requireYAMLKey(pinHygieneWorkflow, "WORKCELL_COSIGN_VERSION", ".github/workflows/pin-hygiene.yml")
	if err != nil {
		return err
	}
	upstreamRefreshCosignVersion, err := requireYAMLKey(upstreamRefreshWorkflow, "WORKCELL_COSIGN_VERSION", ".github/workflows/upstream-refresh.yml")
	if err != nil {
		return err
	}
	if len(map[string]struct{}{ciCosignVersion: {}, releaseCosignVersion: {}, pinHygieneCosignVersion: {}, upstreamRefreshCosignVersion: {}}) != 1 {
		return errors.New("WORKCELL_COSIGN_VERSION must match between .github/workflows/ci.yml, .github/workflows/release.yml, .github/workflows/pin-hygiene.yml, and .github/workflows/upstream-refresh.yml")
	}
	if !pinnedReleaseTagPattern.MatchString(ciCosignVersion) {
		return fmt.Errorf("WORKCELL_COSIGN_VERSION must be an exact pinned release, found %q", ciCosignVersion)
	}
	for _, workflow := range []struct {
		text string
		path string
	}{{ciWorkflow, ".github/workflows/ci.yml"}, {releaseWorkflow, ".github/workflows/release.yml"}, {pinHygieneWorkflow, ".github/workflows/pin-hygiene.yml"}, {upstreamRefreshWorkflow, ".github/workflows/upstream-refresh.yml"}} {
		if !strings.Contains(workflow.text, "cosign-release: ${{ env.WORKCELL_COSIGN_VERSION }}") {
			return fmt.Errorf("%s must pin the installed cosign binary release", workflow.path)
		}
	}
	ciCosignInstallerRef, err := requireActionRef(ciWorkflow, "sigstore/cosign-installer", ".github/workflows/ci.yml")
	if err != nil {
		return err
	}
	releaseCosignInstallerRef, err := requireActionRef(releaseWorkflow, "sigstore/cosign-installer", ".github/workflows/release.yml")
	if err != nil {
		return err
	}
	pinHygieneCosignInstallerRef, err := requireActionRef(pinHygieneWorkflow, "sigstore/cosign-installer", ".github/workflows/pin-hygiene.yml")
	if err != nil {
		return err
	}
	upstreamRefreshCosignInstallerRef, err := requireActionRef(upstreamRefreshWorkflow, "sigstore/cosign-installer", ".github/workflows/upstream-refresh.yml")
	if err != nil {
		return err
	}
	if len(map[string]struct{}{ciCosignInstallerRef: {}, releaseCosignInstallerRef: {}, pinHygieneCosignInstallerRef: {}, upstreamRefreshCosignInstallerRef: {}}) != 1 {
		return errors.New("sigstore/cosign-installer must use the same reviewed commit SHA in .github/workflows/ci.yml, .github/workflows/release.yml, .github/workflows/pin-hygiene.yml, and .github/workflows/upstream-refresh.yml")
	}
	if !strings.Contains(ciWorkflow, "driver-opts: image=${{ env.WORKCELL_BUILDKIT_IMAGE }}") {
		return errors.New(".github/workflows/ci.yml must pin the BuildKit daemon image used by setup-buildx-action")
	}
	if !strings.Contains(releaseWorkflow, "driver-opts: image=${{ env.WORKCELL_BUILDKIT_IMAGE }}") {
		return errors.New(".github/workflows/release.yml must pin the BuildKit daemon image used by setup-buildx-action")
	}
	if !strings.Contains(ciWorkflow, "cache-binary: true") {
		return errors.New("pinned buildx binary caching must stay enabled in .github/workflows/ci.yml")
	}
	extractBetween := func(text, startMarker, endMarker, label string) (string, error) {
		start := strings.Index(text, startMarker)
		if start < 0 {
			return "", fmt.Errorf("unable to extract %s from .github/workflows/ci.yml", label)
		}
		remaining := text[start:]
		end := strings.Index(remaining, endMarker)
		if end < 0 {
			return "", fmt.Errorf("unable to extract %s from .github/workflows/ci.yml", label)
		}
		return remaining[:end+1], nil
	}
	ciReproBuildJob := ""
	if start := strings.Index(ciWorkflow, "  reproducible-build-platform:\n"); start >= 0 {
		remaining := ciWorkflow[start:]
		if end := strings.Index(remaining, "\n  reproducible-build:\n"); end >= 0 {
			ciReproBuildJob = remaining[:end+1]
		} else {
			ciReproBuildJob = remaining
		}
	}
	if ciReproBuildJob == "" {
		return errors.New("unable to extract reproducible-build-platform job from .github/workflows/ci.yml")
	}
	if !regexp.MustCompile(`(?m)^\s{4}runs-on:\s*\$\{\{\s*matrix\.runner\s*\}\}$`).MatchString(ciReproBuildJob) {
		return errors.New(".github/workflows/ci.yml must route reproducible-build-platform through runs-on: ${{ matrix.runner }}")
	}
	ciReproStrategyBlock, err := extractBetween(ciReproBuildJob, "    strategy:\n", "\n    steps:\n", "reproducible-build-platform strategy block")
	if err != nil {
		return errors.New("unable to extract reproducible-build-platform strategy block from .github/workflows/ci.yml")
	}
	expectedCiReproStrategyBlock := "    strategy:\n" +
		"      fail-fast: false\n" +
		"      matrix:\n" +
		"        include:\n" +
		"          - platform: linux/amd64\n" +
		"            platform_name: amd64\n" +
		"            runner: ubuntu-latest\n" +
		"          - platform: linux/arm64\n" +
		"            platform_name: arm64\n" +
		"            runner: ubuntu-24.04-arm\n"
	if ciReproStrategyBlock != expectedCiReproStrategyBlock {
		return errors.New(".github/workflows/ci.yml must keep the reviewed reproducible-build matrix structure, including a single native ubuntu-24.04-arm lane for linux/arm64")
	}
	entries, err := extractReproMatrixEntries(ciReproStrategyBlock, ".github/workflows/ci.yml")
	if err != nil {
		return err
	}
	arm64Entries := make([][3]string, 0)
	for _, entry := range entries {
		if entry[0] == "linux/arm64" {
			arm64Entries = append(arm64Entries, entry)
		}
	}
	if len(arm64Entries) != 1 || arm64Entries[0] != [3]string{"linux/arm64", "arm64", "ubuntu-24.04-arm"} {
		return errors.New(".github/workflows/ci.yml must define exactly one linux/arm64 reproducible-build matrix entry and it must use runner ubuntu-24.04-arm")
	}
	if strings.Contains(ciWorkflow, "docker/setup-qemu-action@") {
		return errors.New(".github/workflows/ci.yml must not configure QEMU in CI now that arm64 reproducible builds use a native runner")
	}
	if err := ValidateCIWorkflowPRShapeFlow(ciWorkflow); err != nil {
		return err
	}
	if err := ValidateMacOSInstallVerificationFlow(ciWorkflow, ".github/workflows/ci.yml", "workcell-ci-install-candidate", "name: Install verification (${{ matrix.runner_label }})"); err != nil {
		return err
	}
	if !strings.Contains(releaseWorkflow, "cache-binary: false") {
		return errors.New("the publishing release workflow must not cache the Buildx binary")
	}
	if strings.Contains(releaseWorkflow, "docker/setup-qemu-action@") {
		return errors.New(".github/workflows/release.yml must not configure QEMU now that arm64 release builds use a native runner")
	}
	if !strings.Contains(releaseWorkflow, "runs-on: ubuntu-24.04-arm") {
		return errors.New(".github/workflows/release.yml must build the arm64 release image on a native ubuntu-24.04-arm runner")
	}
	releaseSyftVersion, err := requireYAMLKey(releaseWorkflow, "WORKCELL_SYFT_VERSION", ".github/workflows/release.yml")
	if err != nil {
		return err
	}
	if !pinnedReleaseTagPattern.MatchString(releaseSyftVersion) {
		return fmt.Errorf("WORKCELL_SYFT_VERSION must be an exact pinned release, found %q", releaseSyftVersion)
	}
	if !strings.Contains(releaseWorkflow, "syft-version: ${{ env.WORKCELL_SYFT_VERSION }}") {
		return errors.New(".github/workflows/release.yml must pin the Syft version used for release SBOM generation")
	}
	if !strings.Contains(releaseWorkflow, "anchore/sbom-action/download-syft@") {
		return errors.New(".github/workflows/release.yml must install the pinned Syft CLI before generating the builder environment manifest")
	}
	securityWorkflow, err := readText(filepath.Join(cfg.WorkflowsDir, "security.yml"))
	if err != nil {
		return err
	}
	_, securityActionlintVersionMatch, err := requireRegex(securityWorkflow, `(?m)^\s*ACTIONLINT_VERSION:\s*([0-9]+\.[0-9]+\.[0-9]+)\s*$`, "security actionlint version", ".github/workflows/security.yml")
	if err != nil {
		return err
	}
	_, releaseActionlintVersionMatch, err := requireRegex(releaseWorkflow, `(?m)^\s*ACTIONLINT_VERSION:\s*([0-9]+\.[0-9]+\.[0-9]+)\s*$`, "release actionlint version", ".github/workflows/release.yml")
	if err != nil {
		return err
	}
	if securityActionlintVersionMatch[1] != releaseActionlintVersionMatch[1] {
		return errors.New("ACTIONLINT_VERSION must match between .github/workflows/security.yml and .github/workflows/release.yml")
	}
	_, securityActionlintSHAMatch, err := requireRegex(securityWorkflow, `(?m)^\s*ACTIONLINT_SHA256:\s*([0-9a-f]{64})\s*$`, "security actionlint sha", ".github/workflows/security.yml")
	if err != nil {
		return err
	}
	_, releaseActionlintSHAMatch, err := requireRegex(releaseWorkflow, `(?m)^\s*ACTIONLINT_SHA256:\s*([0-9a-f]{64})\s*$`, "release actionlint sha", ".github/workflows/release.yml")
	if err != nil {
		return err
	}
	if securityActionlintSHAMatch[1] != releaseActionlintSHAMatch[1] {
		return errors.New("ACTIONLINT_SHA256 must match between .github/workflows/security.yml and .github/workflows/release.yml")
	}
	securityZizmorVersion, err := requireUniformWorkflowEnv(securityWorkflow, "ZIZMOR_VERSION", `[0-9]+\.[0-9]+\.[0-9]+`, "security zizmor version", ".github/workflows/security.yml")
	if err != nil {
		return err
	}
	securityZizmorSHA, err := requireUniformWorkflowEnv(securityWorkflow, "ZIZMOR_SHA256", `[0-9a-f]{64}`, "security zizmor sha", ".github/workflows/security.yml")
	if err != nil {
		return err
	}
	releaseZizmorVersion, err := requireUniformWorkflowEnv(releaseWorkflow, "ZIZMOR_VERSION", `[0-9]+\.[0-9]+\.[0-9]+`, "release zizmor version", ".github/workflows/release.yml")
	if err != nil {
		return err
	}
	releaseZizmorSHA, err := requireUniformWorkflowEnv(releaseWorkflow, "ZIZMOR_SHA256", `[0-9a-f]{64}`, "release zizmor sha", ".github/workflows/release.yml")
	if err != nil {
		return err
	}
	if securityZizmorVersion != releaseZizmorVersion {
		return errors.New("ZIZMOR_VERSION must match between .github/workflows/security.yml and .github/workflows/release.yml")
	}
	if securityZizmorSHA != releaseZizmorSHA {
		return errors.New("ZIZMOR_SHA256 must match between .github/workflows/security.yml and .github/workflows/release.yml")
	}
	for _, workflow := range []struct {
		text string
		path string
	}{
		{text: securityWorkflow, path: ".github/workflows/security.yml"},
		{text: releaseWorkflow, path: ".github/workflows/release.yml"},
	} {
		if err := requireCappedReleaseDownloads(workflow.text, workflow.path, []struct {
			label string
			url   string
		}{
			{
				label: "actionlint",
				url:   "https://github.com/rhysd/actionlint/releases/download/v${ACTIONLINT_VERSION}/actionlint_${ACTIONLINT_VERSION}_linux_amd64.tar.gz",
			},
			{
				label: "zizmor",
				url:   "https://github.com/zizmorcore/zizmor/releases/download/v${ZIZMOR_VERSION}/zizmor-x86_64-unknown-linux-gnu.tar.gz",
			},
		}); err != nil {
			return err
		}
		if !strings.Contains(workflow.text, `echo "${ZIZMOR_SHA256}  zizmor.tar.gz" | sha256sum -c -`) {
			return fmt.Errorf("%s must verify the pinned zizmor archive digest", workflow.path)
		}
		if !strings.Contains(workflow.text, `tar -xzf zizmor.tar.gz -C "${RUNNER_TEMP}/bin" zizmor`) {
			return fmt.Errorf("%s must install the pinned zizmor binary archive", workflow.path)
		}
	}
	for _, needle := range []string{
		"github.event_name == 'workflow_dispatch' && github.ref_name != 'main'",
		"base-ref: ${{ github.event_name == 'workflow_dispatch' && 'refs/heads/main' || '' }}",
		"head-ref: ${{ github.event_name == 'workflow_dispatch' && github.ref || '' }}",
		"./scripts/check-workflows.sh",
	} {
		if !strings.Contains(securityWorkflow, needle) {
			return fmt.Errorf(".github/workflows/security.yml must contain %q", needle)
		}
	}
	if !strings.Contains(releaseWorkflow, "docker buildx imagetools create") {
		return errors.New(".github/workflows/release.yml must assemble the published multi-arch manifest with docker buildx imagetools create")
	}
	if regexp.MustCompile(`docker/build-push-action@.*?platforms:\s*linux/amd64,linux/arm64`).MatchString(releaseWorkflow) {
		return errors.New(".github/workflows/release.yml must not publish the final multi-arch image through one opaque multi-platform build-push step")
	}
	if !strings.Contains(runtimeDockerfile, "COPY runtime/container/rust /workcell-rust") {
		return errors.New("runtime/container/Dockerfile must vendor the reviewed Rust runtime sources into the builder stage")
	}
	for _, needle := range []string{
		"COPY --from=rust-toolchain /usr/local/cargo /usr/local/cargo",
		"COPY --from=rust-toolchain /usr/local/rustup /usr/local/rustup",
	} {
		if !strings.Contains(runtimeDockerfile, needle) {
			return fmt.Errorf("runtime/container/Dockerfile must copy the pinned Rust toolchain through %q", needle)
		}
	}
	if !strings.Contains(runtimeDockerfile, "COPY runtime/container/control-plane-manifest.json /usr/local/libexec/workcell/control-plane-manifest.json") {
		return errors.New("runtime/container/Dockerfile must copy the reviewed control-plane manifest into the runtime image")
	}
	hasOfflineCargoBuild := strings.Contains(runtimeDockerfile, "cargo build \\") ||
		strings.Contains(runtimeDockerfile, "\"${toolchain_bin}/cargo\" build \\")
	if !hasOfflineCargoBuild || !strings.Contains(runtimeDockerfile, "--locked \\") || !strings.Contains(runtimeDockerfile, "--offline \\") {
		return errors.New("runtime/container/Dockerfile must build the shipped Rust launcher artifacts with cargo --locked --offline")
	}
	if !strings.Contains(runtimeDockerfile, "CARGO_HOME=/workcell-rust/cargo-home") {
		return errors.New("runtime/container/Dockerfile must isolate Cargo home inside the vendored runtime source tree")
	}
	for _, needle := range []string{
		"name: workcell-release-preflight",
		"name: workcell-release-install-candidate",
		"name: Release install verification (${{ matrix.runner_label }})",
		"brew tap-new",
		"brew --repo",
		"brew install \"${tap_name}/workcell\"",
		"macos-26",
		"macos-15",
		"actions/download-artifact@",
		"context: dist/release-source",
		"name: Re-verify pinned upstreams from archived source tree",
		"name: Verify GitHub macOS release test runners",
		"working-directory: dist/release-source",
		"WORKCELL_BUILD_INPUT_ROOT: ${{ github.workspace }}/dist/release-source",
		"WORKCELL_CONTROL_PLANE_ROOT: ${{ github.workspace }}/dist/release-source",
		"Verify published platform digests match preflight",
		"docker buildx imagetools inspect --raw",
		"{{json .Manifest}}",
		"vnd.docker.reference.type",
		"RELEASE_NO_ATTEST: ${{ vars.WORKCELL_RELEASE_NO_ATTEST || 'false' }}",
		"actions/attest@",
		"Verify release bundle matches preflight",
		"Verify control-plane manifest matches preflight",
		"github/codeql-action/init@",
		"github/codeql-action/analyze@",
		"./scripts/publish-github-release.sh",
	} {
		if !strings.Contains(releaseWorkflow, needle) {
			return fmt.Errorf(".github/workflows/release.yml must contain %q", needle)
		}
	}
	if strings.Contains(releaseWorkflow, "{{json .manifest}}") {
		return errors.New(".github/workflows/release.yml must not use the unsupported lowercase Buildx .manifest template field")
	}
	if !strings.Contains(releaseWorkflow, "dist/${{ env.BUNDLE_NAME }}.sigstore.json") ||
		!strings.Contains(releaseWorkflow, "dist/workcell-control-plane.sigstore.json") ||
		!strings.Contains(releaseWorkflow, "dist/workcell-image.digest.sigstore.json") ||
		!strings.Contains(releaseWorkflow, "dist/workcell-source.spdx.sigstore.json") ||
		!strings.Contains(releaseWorkflow, "dist/workcell-image.spdx.sigstore.json") {
		return errors.New(".github/workflows/release.yml must publish direct signature bundles for release artifacts")
	}
	if err := ValidateReleaseWorkflowControlPlaneFlow(releaseWorkflow); err != nil {
		return err
	}
	if err := ValidateMacOSInstallVerificationFlow(releaseWorkflow, ".github/workflows/release.yml", "workcell-release-install-candidate", "name: Release install verification (${{ matrix.runner_label }})"); err != nil {
		return err
	}
	if err := ValidateReleaseWorkflowGitHubAttestationFlow(releaseWorkflow); err != nil {
		return err
	}
	if strings.Contains(releaseWorkflow, "steps.build.outputs.digest") {
		return errors.New(".github/workflows/release.yml must not keep referencing the old single-step multi-platform digest output")
	}
	if strings.Contains(releaseWorkflow, "gh release ") {
		return errors.New(".github/workflows/release.yml must not depend on an ambient gh CLI; use a pinned release-publish action")
	}
	if !strings.Contains(releaseWorkflow, "./scripts/publish-github-release.sh") {
		return errors.New(".github/workflows/release.yml must publish assets through the reviewed repo-local GitHub Release API script")
	}
	if count := strings.Count(releaseWorkflow, "./scripts/check-release-tag-signature.sh --github-repo"); count != 2 {
		return fmt.Errorf(".github/workflows/release.yml must verify release tag signatures in preflight and publish jobs, found %d checks", count)
	}
	for _, needle := range []string{
		`run: ./scripts/run-hosted-controls-audit.sh "${GITHUB_REPOSITORY}"`,
		`WORKCELL_HOSTED_CONTROLS_REQUIRED: "1"`,
		`WORKCELL_HOSTED_CONTROLS_TOKEN: ${{ secrets.WORKCELL_HOSTED_CONTROLS_TOKEN }}`,
	} {
		if !strings.Contains(releaseWorkflow, needle) {
			return fmt.Errorf(".github/workflows/release.yml must contain %q", needle)
		}
	}
	if !strings.Contains(releaseWorkflow, "environment:\n      name: hosted-controls-audit") {
		return errors.New(".github/workflows/release.yml release preflight must bind to the hosted-controls-audit environment")
	}
	if err := ValidateUpstreamRefreshWorkflow(upstreamRefreshWorkflow); err != nil {
		return err
	}
	hostedControlsWorkflow, err := readText(filepath.Join(cfg.WorkflowsDir, "hosted-controls.yml"))
	if err != nil {
		return err
	}
	for _, needle := range []string{
		`name: hosted-controls-audit`,
		`run: ./scripts/run-hosted-controls-audit.sh "${GITHUB_REPOSITORY}"`,
		`WORKCELL_HOSTED_CONTROLS_TOKEN: ${{ secrets.WORKCELL_HOSTED_CONTROLS_TOKEN }}`,
		`WORKCELL_HOSTED_CONTROLS_REQUIRED: "1"`,
	} {
		if !strings.Contains(hostedControlsWorkflow, needle) {
			return fmt.Errorf(".github/workflows/hosted-controls.yml must contain %q", needle)
		}
	}
	for _, needle := range []string{
		"./scripts/verify-github-macos-release-test-runners.sh",
		"./scripts/verify-upstream-codex-release.sh",
		"./scripts/verify-upstream-claude-release.sh",
		"./scripts/verify-upstream-copilot-release.sh",
		"./scripts/verify-upstream-gemini-release.sh",
		"./scripts/update-upstream-pins.sh --check",
	} {
		if !strings.Contains(releaseWorkflow, needle) {
			return fmt.Errorf(".github/workflows/release.yml must contain %q", needle)
		}
	}
	for _, needle := range []string{
		"./scripts/ci/job-pin-hygiene.sh",
	} {
		if !strings.Contains(pinHygieneWorkflow, needle) {
			return fmt.Errorf(".github/workflows/pin-hygiene.yml must contain %q", needle)
		}
	}
	pinHygieneJob, err := readText(filepath.Join(repoRoot, "scripts", "ci", "job-pin-hygiene.sh"))
	if err != nil {
		return err
	}
	for _, needle := range []string{
		"${ROOT_DIR}/scripts/verify-upstream-codex-release.sh",
		"${ROOT_DIR}/scripts/verify-upstream-claude-release.sh",
		"${ROOT_DIR}/scripts/verify-upstream-copilot-release.sh",
		"${ROOT_DIR}/scripts/verify-upstream-gemini-release.sh",
	} {
		if !strings.Contains(pinHygieneJob, needle) {
			return fmt.Errorf("scripts/ci/job-pin-hygiene.sh must contain %q", needle)
		}
	}
	for _, needle := range []string{
		"./scripts/update-upstream-pins.sh --check",
	} {
		if !strings.Contains(releaseWorkflow, needle) {
			return fmt.Errorf(".github/workflows/release.yml must contain %q", needle)
		}
	}
	for _, needle := range []string{
		"environment:\n      name: release",
		`sudo install -m 0755 "$(command -v cosign)" /usr/local/bin/cosign`,
		`sudo install -m 0755 "$(command -v syft)" /usr/local/bin/syft`,
		`actionlint_archive="${RUNNER_TEMP}/actionlint.tar.gz"`,
		`tar -xzf "${actionlint_archive}" -C "${RUNNER_TEMP}" actionlint`,
		"git -c safe.directory=/workspace archive \\",
	} {
		if !strings.Contains(releaseWorkflow, needle) {
			return fmt.Errorf(".github/workflows/release.yml must contain %q", needle)
		}
	}
	for _, workflowPath := range mustGlob(filepath.Join(cfg.WorkflowsDir, "*.yml")) {
		workflowText, err := readText(workflowPath)
		if err != nil {
			return err
		}
		if !workflowPermissionsRE.MatchString(workflowText) {
			return fmt.Errorf("workflow-level empty permissions declaration missing in %s", workflowPath)
		}
		if strings.Contains(workflowText, "pull_request_target") {
			if err := IsSafePullRequestTargetWorkflow(workflowText, workflowPath); err != nil {
				return err
			}
		}
		if regexp.MustCompile(`secrets\.[A-Z0-9_]*(?:PAT|PERSONAL_ACCESS_TOKEN)\b|GH_PAT\b|PERSONAL_ACCESS_TOKEN\b`).MatchString(workflowText) {
			return fmt.Errorf("%s must not contain long-lived personal access tokens", workflowPath)
		}
		for _, match := range workflowUsesPattern.FindAllStringSubmatch(workflowText, -1) {
			if !commitShaPattern.MatchString(match[2]) {
				return fmt.Errorf("%s must pin GitHub Actions by full commit SHA; found %s@%s", workflowPath, match[1], match[2])
			}
		}
	}
	for _, required := range []string{
		"/.github/workflows/ @omkhar",
		"/scripts/ @omkhar",
		"/runtime/container/ @omkhar",
		"/docs/provenance.md @omkhar",
	} {
		if !strings.Contains(codeowners, required) {
			return fmt.Errorf(".github/CODEOWNERS must declare high-risk ownership for %q", required)
		}
	}
	releaseEnvironment, _ := hostedControlsPolicy["release_environment"].(map[string]any)
	releaseMode, _ := releaseEnvironment["mode"].(string)
	if releaseMode != "review-gated" && releaseMode != "single-owner-public" && releaseMode != "single-owner-private" && releaseMode != "plan-limited-private" {
		return errors.New("policy/github-hosted-controls.toml must set release_environment.mode to 'review-gated', 'single-owner-public', 'single-owner-private', or 'plan-limited-private'")
	}
	if _, err := GitHubActionsPolicy(hostedControlsPolicy, "policy/github-hosted-controls.toml"); err != nil {
		return err
	}
	if _, err := ReleaseAssets(hostedControlsPolicy, "policy/github-hosted-controls.toml"); err != nil {
		return err
	}
	if err := ValidateCanonicalRepositoryVariables(hostedControlsPolicy, "policy/github-hosted-controls.toml"); err != nil {
		return err
	}
	if err := ValidateCanonicalWorkflowEnvironments(hostedControlsPolicy, "policy/github-hosted-controls.toml"); err != nil {
		return err
	}
	for _, needle := range []string{
		"gh api --paginate \"repos/${REPO}/actions/variables?per_page=100\"",
		"repos/${REPO}/actions/permissions/selected-actions",
		"repos/${REPO}/actions/permissions/workflow",
		"repos/${REPO}/immutable-releases",
		"jq -s '{total_count: (map(.total_count // 0) | max // 0), variables: (map(.variables // []) | add)}'",
		"gh api --paginate \"repos/${REPO}/environments?per_page=100\"",
		`list-hosted-control-environments "${POLICY_PATH}"`,
		"safe_environment_name=\"${encoded_environment_name}\"",
		"environment-${safe_environment_name}.json",
		"repos/${REPO}/environments/${encoded_environment_name}/variables?per_page=100",
		"repos/${REPO}/environments/${encoded_environment_name}/secrets?per_page=100",
		`verify-github-hosted-controls "${TMP_DIR}" "${REPO}" "${POLICY_PATH}"`,
	} {
		if !strings.Contains(hostedControlsScript, needle) {
			return fmt.Errorf("scripts/verify-github-hosted-controls.sh must contain %q", needle)
		}
	}
	if err := requireNoRegistryBootstrapMCP(codexRequirementsText, cfg.CodexRequirementsPath); err != nil {
		return err
	}
	if err := requireNoRegistryBootstrapMCP(codexMCPConfigText, cfg.CodexMCPConfigPath); err != nil {
		return err
	}
	return nil
}

func IsSafePullRequestTargetWorkflow(workflowText, workflowPath string) error {
	if filepath.Base(workflowPath) != "pr-base-policy.yml" {
		return fmt.Errorf("%s must not contain pull_request_target triggers", workflowPath)
	}
	if !strings.Contains(workflowText, "kusari-inspector suppress") {
		return fmt.Errorf("%s must document the reviewed Kusari suppression for pull_request_target", workflowPath)
	}
	root, err := parseWorkflowRoot(workflowText, workflowPath)
	if err != nil {
		return err
	}
	permissionsNodes := yamlMappingValues(root, "permissions")
	if len(permissionsNodes) != 1 || permissionsNodes[0].Kind != yaml.MappingNode || len(permissionsNodes[0].Content) != 0 {
		return fmt.Errorf("%s must keep top-level permissions: {}", workflowPath)
	}
	jobsNodes := yamlMappingValues(root, "jobs")
	if len(jobsNodes) != 1 || jobsNodes[0].Kind != yaml.MappingNode {
		return fmt.Errorf("%s must define pull_request_target jobs as a mapping", workflowPath)
	}
	for i := 1; i < len(jobsNodes[0].Content); i += 2 {
		job := jobsNodes[0].Content[i]
		if job.Kind != yaml.MappingNode {
			return fmt.Errorf("%s must define pull_request_target jobs as mapping nodes", workflowPath)
		}
		if len(yamlMappingValues(job, "permissions")) > 0 {
			return fmt.Errorf("%s must not grant job-level permissions under pull_request_target", workflowPath)
		}
		if len(yamlMappingValues(job, "uses")) > 0 {
			return fmt.Errorf("%s must not call reusable workflows under pull_request_target", workflowPath)
		}
		for _, steps := range yamlMappingValues(job, "steps") {
			if steps.Kind != yaml.SequenceNode {
				return fmt.Errorf("%s must define pull_request_target steps as a sequence", workflowPath)
			}
			for _, step := range steps.Content {
				if step.Kind != yaml.MappingNode {
					return fmt.Errorf("%s must define pull_request_target steps as mapping nodes", workflowPath)
				}
				for _, uses := range yamlMappingValues(step, "uses") {
					if strings.HasPrefix(yamlScalarValue(uses), "actions/checkout@") {
						return fmt.Errorf("%s must not checkout repository contents when using pull_request_target", workflowPath)
					}
					return fmt.Errorf("%s must not use external actions when using pull_request_target", workflowPath)
				}
			}
		}
	}
	return nil
}

func parseWorkflowRoot(workflowText, workflowPath string) (*yaml.Node, error) {
	var document yaml.Node
	if err := yaml.Unmarshal([]byte(workflowText), &document); err != nil {
		return nil, fmt.Errorf("%s: parse workflow YAML: %w", workflowPath, err)
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%s must be a YAML mapping", workflowPath)
	}
	return document.Content[0], nil
}

func yamlMappingValues(mapping *yaml.Node, key string) []*yaml.Node {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	values := []*yaml.Node{}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Kind == yaml.ScalarNode && mapping.Content[i].Value == key {
			values = append(values, mapping.Content[i+1])
		}
	}
	return values
}

func yamlScalarValue(node *yaml.Node) string {
	if node == nil || node.Kind != yaml.ScalarNode {
		return ""
	}
	return strings.TrimSpace(node.Value)
}

func mustGlob(pattern string) []string {
	matches, err := filepath.Glob(pattern)
	if err != nil {
		panic(err)
	}
	return matches
}
