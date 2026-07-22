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

	"github.com/omkhar/workcell/internal/tomlsubset"
)

var (
	// pinnedReleaseTagPattern matches an exact vMAJOR.MINOR.PATCH release tag.
	pinnedReleaseTagPattern = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)
	// workflowPermissionsRE, actionRefPattern, commitShaPattern, and the
	// workflow-parsing helpers live in pinnedinputs_workflows.go.
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

type markdownlintPackageJSON struct {
	Dependencies map[string]string `json:"dependencies"`
}

type markdownlintPackageLock struct {
	Packages map[string]markdownlintPackageLockEntry `json:"packages"`
}

type markdownlintPackageLockEntry struct {
	Version      string            `json:"version"`
	Dependencies map[string]string `json:"dependencies"`
	Engines      map[string]string `json:"engines"`
}

// readText, isHexDigest, hexDigestPattern live in core.go.
// requireStringSliceTable lives in hostedcontrols.go
// (canonical post-collapse; same package-internal symbols all consumers share).
// The GitHub Actions workflow format — uses-scan types, extractWorkflowUses,
// toolPins/loadToolPins/parseToolPins, loadAllowedActions, and the
// pull_request_target and YAML helpers — lives in pinnedinputs_workflows.go.

func CheckPinnedInputs(cfg PinnedInputsConfig) error {
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(cfg.RuntimeDockerfilePath), "..", ".."))
	allowedActions, err := loadAllowedActions(filepath.Join(repoRoot, "policy", "allowed-actions.toml"))
	if err != nil {
		return err
	}
	pins, err := loadToolPins(filepath.Join(repoRoot, "policy", "tool-pins.toml"))
	if err != nil {
		return err
	}
	// requirePolicyPin binds a workflow's canonical tool pin to policy/tool-pins.toml
	// so bumping a tool is a reviewed change to that one file. The existing
	// cross-file asserts then keep every other workflow copy in lockstep.
	requirePolicyPin := func(name, actual, policyValue string) error {
		if actual != policyValue {
			return fmt.Errorf("%s pin %q does not match policy/tool-pins.toml %q; the workflow and the policy must stay in lockstep (scripts/update-upstream-pins.sh rewrites both)", name, actual, policyValue)
		}
		return nil
	}
	goModPath := filepath.Join(repoRoot, "go.mod")
	cargoManifestPath := filepath.Join(repoRoot, "runtime", "container", "rust", "Cargo.toml")
	installDevToolsScriptPath := filepath.Join(repoRoot, "scripts", "install-dev-tools.sh")
	markdownlintPackageJSONPath := filepath.Join(repoRoot, "tools", "markdownlint", "package.json")
	markdownlintPackageLockPath := filepath.Join(repoRoot, "tools", "markdownlint", "package-lock.json")
	rustToolchainPath := filepath.Join(repoRoot, "runtime", "container", "rust", "rust-toolchain.toml")
	debianBootstrapManifestPath := filepath.Join(repoRoot, filepath.FromSlash(DebianBootstrapManifestRelPath))

	runtimeDockerfile, err := readText(cfg.RuntimeDockerfilePath)
	if err != nil {
		return err
	}
	validatorDockerfile, err := readText(cfg.ValidatorDockerfilePath)
	if err != nil {
		return err
	}
	debianBootstrapManifest, err := ReadDebianBootstrapManifest(debianBootstrapManifestPath)
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
	var markdownlintPackageJSON markdownlintPackageJSON
	if err := json.Unmarshal([]byte(markdownlintPackageJSONText), &markdownlintPackageJSON); err != nil {
		return err
	}
	var markdownlintPackageLock markdownlintPackageLock
	if err := json.Unmarshal([]byte(markdownlintPackageLockText), &markdownlintPackageLock); err != nil {
		return err
	}
	hostedControlsPolicy, err := tomlsubset.Parse(hostedControlsPolicyText, cfg.HostedControlsPolicyPath)
	if err != nil {
		return err
	}

	requireYAMLKey := func(text, name, path string) (string, error) {
		match := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(name) + `:\s*(.+)$`).FindStringSubmatch(text)
		if match == nil {
			return "", fmt.Errorf("unable to extract %s from %s", name, path)
		}
		return strings.TrimSpace(match[1]), nil
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
	requireTOMLString := func(text, key, path string) (string, error) {
		match := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(key) + `\s*=\s*"([^"]+)"\s*$`).FindStringSubmatch(text)
		if match == nil {
			return "", fmt.Errorf("unable to extract %s from %s", key, path)
		}
		return match[1], nil
	}
	majorMinor := func(version, path string) (string, error) {
		match := regexp.MustCompile(`^([0-9]+\.[0-9]+)\.[0-9]+$`).FindStringSubmatch(version)
		if match == nil {
			return "", fmt.Errorf("expected a semantic version in %s, found %q", path, version)
		}
		return match[1], nil
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
	if err := validateDockerPinnedInputs(cfg, repoRoot, runtimeDockerfile, validatorDockerfile, debianBootstrapManifest, debianBootstrapManifestPath, goModText, codexRequirementsText, codexMCPConfigText); err != nil {
		return err
	}
	if err := validateNodeMarkdownlintPinnedInputs(
		cfg,
		validatorDockerfile,
		installDevToolsScript,
		markdownlintPackageJSON,
		markdownlintPackageJSONPath,
		markdownlintPackageLock,
		markdownlintPackageLockPath,
		installDevToolsScriptPath,
	); err != nil {
		return err
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

	if err := validateNodeProviderLock(providersPackageJSON, providersPackageLock); err != nil {
		return err
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
	for _, workflowPath := range workflowYAMLFiles(cfg.WorkflowsDir) {
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
	for _, needle := range []string{
		`DEBIAN_BOOTSTRAP_MANIFEST="${ROOT_DIR}/runtime/container/debian-bootstrap.env"`,
		`DEBIAN_BOOTSTRAP_CKSUM="$(cksum "${DEBIAN_BOOTSTRAP_MANIFEST}" | awk '{print $1}')"`,
		`VALIDATOR_IMAGE_DEFAULT_TAG="workcell-validator:local-${VALIDATOR_DOCKERFILE_CKSUM}-${DEBIAN_BOOTSTRAP_CKSUM}"`,
	} {
		if !strings.Contains(validatorImageScript, needle) {
			return fmt.Errorf("scripts/ci/build-validator-image.sh must include the Debian bootstrap manifest in validator image identity: missing %s", needle)
		}
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
	securityActionlintVersion, err := requireUniformWorkflowEnv(securityWorkflow, "ACTIONLINT_VERSION", `[0-9]+\.[0-9]+\.[0-9]+`, "security actionlint version", ".github/workflows/security.yml")
	if err != nil {
		return err
	}
	releaseActionlintVersion, err := requireUniformWorkflowEnv(releaseWorkflow, "ACTIONLINT_VERSION", `[0-9]+\.[0-9]+\.[0-9]+`, "release actionlint version", ".github/workflows/release.yml")
	if err != nil {
		return err
	}
	if securityActionlintVersion != releaseActionlintVersion {
		return errors.New("ACTIONLINT_VERSION must match between .github/workflows/security.yml and .github/workflows/release.yml")
	}
	securityActionlintSHA, err := requireUniformWorkflowEnv(securityWorkflow, "ACTIONLINT_SHA256", `[0-9a-f]{64}`, "security actionlint sha", ".github/workflows/security.yml")
	if err != nil {
		return err
	}
	releaseActionlintSHA, err := requireUniformWorkflowEnv(releaseWorkflow, "ACTIONLINT_SHA256", `[0-9a-f]{64}`, "release actionlint sha", ".github/workflows/release.yml")
	if err != nil {
		return err
	}
	if securityActionlintSHA != releaseActionlintSHA {
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
	// Bind each tool's canonical workflow value to policy/tool-pins.toml. Run
	// after the cross-file asserts above so a workflow-vs-workflow mismatch is
	// reported as such; this catches the case where every workflow copy agrees
	// but drifts from the reviewed policy.
	for _, check := range []struct {
		name        string
		actual      string
		policyValue string
	}{
		{"WORKCELL_COSIGN_VERSION", ciCosignVersion, pins.Cosign},
		{"WORKCELL_BUILDX_VERSION", ciBuildxVersion, pins.Buildx},
		{"WORKCELL_BUILDKIT_IMAGE", ciBuildkitImage, pins.Buildkit},
		{"WORKCELL_QEMU_IMAGE", ciQEMUImage, pins.QEMU},
		{"WORKCELL_SYFT_VERSION", releaseSyftVersion, pins.Syft},
		{"ACTIONLINT_VERSION", securityActionlintVersion, pins.ActionlintVersion},
		{"ACTIONLINT_SHA256", securityActionlintSHA, pins.ActionlintSHA256},
		{"ZIZMOR_VERSION", securityZizmorVersion, pins.ZizmorVersion},
		{"ZIZMOR_SHA256", securityZizmorSHA, pins.ZizmorSHA256},
	} {
		if err := requirePolicyPin(check.name, check.actual, check.policyValue); err != nil {
			return err
		}
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
	validateJob, err := readText(filepath.Join(repoRoot, "scripts", "ci", "job-validate.sh"))
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
		`WORKCELL_COPILOT_RELEASE_HELP_MODE=checksum "${ROOT_DIR}/scripts/verify-upstream-copilot-release.sh"`,
		"unset WORKCELL_GITHUB_API_TOKEN GITHUB_TOKEN GH_TOKEN",
	} {
		if !strings.Contains(validateJob, needle) {
			return fmt.Errorf("scripts/ci/job-validate.sh must contain %q", needle)
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
	for _, workflowPath := range workflowYAMLFiles(cfg.WorkflowsDir) {
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
		usesRefs, err := extractWorkflowUses(workflowText)
		if err != nil {
			return fmt.Errorf("%s: %w", workflowPath, err)
		}
		for _, ref := range usesRefs {
			action := actionRefPattern.FindStringSubmatch(ref)
			if action == nil {
				return fmt.Errorf("%s has an unsupported uses: reference %q; only pinned owner/repo actions are permitted (no docker:// or local ./ actions)", workflowPath, ref)
			}
			if !commitShaPattern.MatchString(action[2]) {
				return fmt.Errorf("%s must pin GitHub Actions by full commit SHA; found %s@%s", workflowPath, action[1], action[2])
			}
			segments := strings.SplitN(action[1], "/", 3)
			ownerRepo := segments[0] + "/" + segments[1]
			if !allowedActions[ownerRepo] {
				return fmt.Errorf("%s uses action %q which is not in the reviewed allowlist policy/allowed-actions.toml", workflowPath, ownerRepo)
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

func requireArg(text, name, path string) (string, error) {
	match := regexp.MustCompile(`(?m)^ARG ` + regexp.QuoteMeta(name) + `=(.+)$`).FindStringSubmatch(text)
	if match == nil {
		return "", fmt.Errorf("unable to extract %s from %s", name, path)
	}
	return strings.TrimSpace(match[1]), nil
}

func requirePinnedBaseImage(image, label, path string) error {
	if !regexp.MustCompile(`^[^@]+@sha256:[0-9a-f]{64}$`).MatchString(image) {
		return fmt.Errorf("%s in %s must be pinned by immutable digest, found %q", label, path, image)
	}
	return nil
}

func requireRegex(text, pattern, label, path string) (*regexp.Regexp, []string, error) {
	re := regexp.MustCompile(pattern)
	match := re.FindStringSubmatch(text)
	if match == nil {
		return nil, nil, fmt.Errorf("%s in %s must match %q", label, path, pattern)
	}
	return re, match, nil
}

func requireEqual(label, left, leftPath, right, rightPath string) error {
	if left != right {
		return fmt.Errorf("%s must match between %s (%q) and %s (%q)", label, leftPath, left, rightPath, right)
	}
	return nil
}

func requireDelimitedText(text, start, end, label, path string) (string, error) {
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

func requireOrderedText(text, label, path string, needles []string) error {
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
