// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
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
	check := newPinnedInputsCheck(cfg)
	if err := check.load(); err != nil {
		return err
	}

	repoRoot := check.repoRoot
	pins := check.pins
	cargoManifestPath := check.paths.cargoManifest
	installDevToolsScriptPath := check.paths.installDevTools
	validateRepoScriptPath := check.paths.validateRepo
	markdownlintPackageJSONPath := check.paths.markdownlintJSON
	markdownlintPackageLockPath := check.paths.markdownlintLock
	rustToolchainPath := check.paths.rustToolchain
	debianBootstrapManifestPath := check.paths.debianBootstrap
	runtimeDockerfile := check.runtimeDockerfile
	validatorDockerfile := check.validatorDockerfile
	debianBootstrapManifest := check.debianBootstrapManifest
	providersPackageJSON := check.providersPackageJSON
	providersPackageLock := check.providersPackageLock
	markdownlintPackageJSON := check.markdownlintPackageJSON
	markdownlintPackageLock := check.markdownlintPackageLock
	installDevToolsScript := check.installDevToolsScript
	validateRepoScript := check.validateRepoScript
	ciWorkflow := check.ciWorkflow
	releaseWorkflow := check.releaseWorkflow
	pinHygieneWorkflow := check.pinHygieneWorkflow
	upstreamRefreshWorkflow := check.upstreamRefreshWorkflow
	validatorImageScript := check.validatorImageScript
	codexRequirementsText := check.codexRequirementsText
	codexMCPConfigText := check.codexMCPConfigText
	goModText := check.goModText
	cargoManifestText := check.cargoManifestText
	rustToolchainText := check.rustToolchainText

	// requirePolicyPin binds a workflow's canonical tool pin to policy/tool-pins.toml
	// so bumping a tool is a reviewed change to that one file. The existing
	// cross-file asserts then keep every other workflow copy in lockstep.
	requirePolicyPin := func(name, actual, policyValue string) error {
		if actual != policyValue {
			return fmt.Errorf("%s pin %q does not match policy/tool-pins.toml %q; the workflow and the policy must stay in lockstep (scripts/update-upstream-pins.sh rewrites both)", name, actual, policyValue)
		}
		return nil
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
		validateRepoScript,
		validateRepoScriptPath,
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
	expectedRustToolchainTrack := fmt.Sprintf("rust:%s-slim-trixie@", runtimeRustVersion)
	if err := firstPinnedInputError(
		func() error {
			return requireEqual("RUST_VERSION", runtimeRustVersion, cfg.RuntimeDockerfilePath, validatorRustVersion, cfg.ValidatorDockerfilePath)
		},
		func() error {
			return requireEqual("Rust toolchain channel", rustToolchainVersion, rustToolchainPath, runtimeRustVersion, cfg.RuntimeDockerfilePath)
		},
		func() error {
			return requirePinnedBaseImage(runtimeRustToolchainImage, "RUST_TOOLCHAIN_IMAGE", cfg.RuntimeDockerfilePath)
		},
		func() error {
			return requireTextRequirements(textRequirement{
				text: runtimeRustToolchainImage, needle: expectedRustToolchainTrack,
				err: fmt.Errorf("RUST_TOOLCHAIN_IMAGE in %s must pin the official rust:%s-slim-trixie image, found %q", cfg.RuntimeDockerfilePath, runtimeRustVersion, runtimeRustToolchainImage),
			})
		},
	); err != nil {
		return err
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
	if err := validateWorkflowBuilderPins(cfg.WorkflowsDir, ciBuildxVersion, ciBuildkitImage); err != nil {
		return err
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
	if err := requireTextRequirements(
		textRequirement{text: ciWorkflow, needle: "driver-opts: image=${{ env.WORKCELL_BUILDKIT_IMAGE }}", err: errors.New(".github/workflows/ci.yml must pin the BuildKit daemon image used by setup-buildx-action")},
		textRequirement{text: releaseWorkflow, needle: "driver-opts: image=${{ env.WORKCELL_BUILDKIT_IMAGE }}", err: errors.New(".github/workflows/release.yml must pin the BuildKit daemon image used by setup-buildx-action")},
		textRequirement{text: ciWorkflow, needle: "cache-binary: true", err: errors.New("pinned buildx binary caching must stay enabled in .github/workflows/ci.yml")},
	); err != nil {
		return err
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
	securityActionlintVersion, err := validateActionlintVersions(securityWorkflow, releaseWorkflow)
	if err != nil {
		return err
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
		if err := requireCappedReleaseDownloads(workflow.text, workflow.path, []releaseDownload{
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
	return firstPinnedInputError(
		check.validateReleaseManifestAndRuntimeSources,
		check.validateRuntimeCargoBuild,
		check.validateReleaseRequiredSteps,
		check.validateReleaseArtifactFlows,
		check.validateReleaseLegacyReferences,
		check.validateReleaseHostedControls,
		check.validateHostedControlsWorkflow,
		check.validateReleaseVerificationJobs,
		check.validateReleasePublishingInputs,
		check.validateWorkflowPolicies,
		check.validateCodeownersAndHostedPolicy,
	)
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
