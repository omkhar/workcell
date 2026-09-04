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

type workflowText struct{ text, path string }
type toolPinCheck struct{ name, actual, expected string }

func (check *pinnedInputsCheck) validateCosignVersions() error {
	var ciValue, releaseValue, hygieneValue, upstreamValue string
	if err := loadPinnedStrings(requireYAMLKey, []pinnedStringInput{
		{&ciValue, check.ciWorkflow, "WORKCELL_COSIGN_VERSION", ".github/workflows/ci.yml"},
		{&releaseValue, check.releaseWorkflow, "WORKCELL_COSIGN_VERSION", ".github/workflows/release.yml"},
		{&hygieneValue, check.pinHygieneWorkflow, "WORKCELL_COSIGN_VERSION", ".github/workflows/pin-hygiene.yml"},
		{&upstreamValue, check.upstreamRefreshWorkflow, "WORKCELL_COSIGN_VERSION", ".github/workflows/upstream-refresh.yml"},
	}); err != nil {
		return err
	}
	if len(map[string]struct{}{ciValue: {}, releaseValue: {}, hygieneValue: {}, upstreamValue: {}}) != 1 {
		return errors.New("WORKCELL_COSIGN_VERSION must match between .github/workflows/ci.yml, .github/workflows/release.yml, .github/workflows/pin-hygiene.yml, and .github/workflows/upstream-refresh.yml")
	}
	if !pinnedReleaseTagPattern.MatchString(ciValue) {
		return fmt.Errorf("WORKCELL_COSIGN_VERSION must be an exact pinned release, found %q", ciValue)
	}
	check.toolPins.cosign = ciValue
	return nil
}

func (check *pinnedInputsCheck) validateCosignReleaseBindings() error {
	for _, workflow := range []workflowText{
		{check.ciWorkflow, ".github/workflows/ci.yml"},
		{check.releaseWorkflow, ".github/workflows/release.yml"},
		{check.pinHygieneWorkflow, ".github/workflows/pin-hygiene.yml"},
		{check.upstreamRefreshWorkflow, ".github/workflows/upstream-refresh.yml"},
	} {
		if !strings.Contains(workflow.text, "cosign-release: ${{ env.WORKCELL_COSIGN_VERSION }}") {
			return fmt.Errorf("%s must pin the installed cosign binary release", workflow.path)
		}
	}
	return nil
}

func (check *pinnedInputsCheck) validateCosignRefsAndBuildxSetup() error {
	var ciRef, releaseRef, hygieneRef, upstreamRef string
	if err := loadPinnedStrings(requireActionRef, []pinnedStringInput{
		{&ciRef, check.ciWorkflow, "sigstore/cosign-installer", ".github/workflows/ci.yml"},
		{&releaseRef, check.releaseWorkflow, "sigstore/cosign-installer", ".github/workflows/release.yml"},
		{&hygieneRef, check.pinHygieneWorkflow, "sigstore/cosign-installer", ".github/workflows/pin-hygiene.yml"},
		{&upstreamRef, check.upstreamRefreshWorkflow, "sigstore/cosign-installer", ".github/workflows/upstream-refresh.yml"},
	}); err != nil {
		return err
	}
	if len(map[string]struct{}{ciRef: {}, releaseRef: {}, hygieneRef: {}, upstreamRef: {}}) != 1 {
		return errors.New("sigstore/cosign-installer must use the same reviewed commit SHA in .github/workflows/ci.yml, .github/workflows/release.yml, .github/workflows/pin-hygiene.yml, and .github/workflows/upstream-refresh.yml")
	}
	return requireTextRequirements(
		textRequirement{
			text: check.ciWorkflow, needle: "driver-opts: image=${{ env.WORKCELL_BUILDKIT_IMAGE }}",
			err: errors.New(".github/workflows/ci.yml must pin the BuildKit daemon image used by setup-buildx-action"),
		},
		textRequirement{
			text: check.releaseWorkflow, needle: "driver-opts: image=${{ env.WORKCELL_BUILDKIT_IMAGE }}",
			err: errors.New(".github/workflows/release.yml must pin the BuildKit daemon image used by setup-buildx-action"),
		},
		textRequirement{
			text: check.ciWorkflow, needle: "cache-binary: true",
			err: errors.New("pinned buildx binary caching must stay enabled in .github/workflows/ci.yml"),
		},
	)
}

func (check *pinnedInputsCheck) loadCIReproBuildJob() error {
	start := strings.Index(check.ciWorkflow, "  reproducible-build-platform:\n")
	if start < 0 {
		return errors.New("unable to extract reproducible-build-platform job from .github/workflows/ci.yml")
	}
	job := check.ciWorkflow[start:]
	if end := strings.Index(job, "\n  reproducible-build:\n"); end >= 0 {
		job = job[:end+1]
	}
	check.ciRepro.job = job
	return nil
}

func (check *pinnedInputsCheck) validateCIReproStrategy() error {
	job := check.ciRepro.job
	if !regexp.MustCompile(`(?m)^\s{4}runs-on:\s*\$\{\{\s*matrix\.runner\s*\}\}$`).MatchString(job) {
		return errors.New(".github/workflows/ci.yml must route reproducible-build-platform through runs-on: ${{ matrix.runner }}")
	}
	start := strings.Index(job, "    strategy:\n")
	if start < 0 {
		return errors.New("unable to extract reproducible-build-platform strategy block from .github/workflows/ci.yml")
	}
	strategy := job[start:]
	end := strings.Index(strategy, "\n    steps:\n")
	if end < 0 {
		return errors.New("unable to extract reproducible-build-platform strategy block from .github/workflows/ci.yml")
	}
	strategy = strategy[:end+1]
	expected := "    strategy:\n      fail-fast: false\n      matrix:\n        include:\n          - platform: linux/amd64\n            platform_name: amd64\n            runner: ubuntu-latest\n          - platform: linux/arm64\n            platform_name: arm64\n            runner: ubuntu-24.04-arm\n"
	if strategy != expected {
		return errors.New(".github/workflows/ci.yml must keep the reviewed reproducible-build matrix structure, including a single native ubuntu-24.04-arm lane for linux/arm64")
	}
	check.ciRepro.strategy = strategy
	return nil
}

func (check *pinnedInputsCheck) validateCIReproEntriesAndTail() error {
	re := regexp.MustCompile(`(?m)^\s{10}- platform:\s*(\S+)\n^\s{12}platform_name:\s*(\S+)\n^\s{12}runner:\s*(\S+)$`)
	matches := re.FindAllStringSubmatch(check.ciRepro.strategy, -1)
	if len(matches) == 0 {
		return errors.New("unable to extract reproducible-build matrix entries from .github/workflows/ci.yml")
	}
	if !validArm64ReproEntries(matches) {
		return errors.New(".github/workflows/ci.yml must define exactly one linux/arm64 reproducible-build matrix entry and it must use runner ubuntu-24.04-arm")
	}
	return firstPinnedInputError(
		func() error {
			return requireTextRequirements(textRequirement{
				text: check.ciWorkflow, needle: "docker/setup-qemu-action@", forbidden: true,
				err: errors.New(".github/workflows/ci.yml must not configure QEMU in CI now that arm64 reproducible builds use a native runner"),
			})
		},
		func() error { return ValidateCIWorkflowPRShapeFlow(check.ciWorkflow) },
		func() error {
			return ValidateMacOSInstallVerificationFlow(check.ciWorkflow, ".github/workflows/ci.yml", "workcell-ci-install-candidate", "name: Install verification (${{ matrix.runner_label }})")
		},
	)
}

func validArm64ReproEntries(matches [][]string) bool {
	count := 0
	for _, match := range matches {
		if match[1] == "linux/arm64" {
			count++
			if [3]string{match[1], match[2], match[3]} != [3]string{"linux/arm64", "arm64", "ubuntu-24.04-arm"} {
				return false
			}
		}
	}
	return count == 1
}

func (check *pinnedInputsCheck) validateReleaseBuildAndSyft() error {
	if err := requireTextRequirements(
		textRequirement{
			text: check.releaseWorkflow, needle: "cache-binary: false",
			err: errors.New("the publishing release workflow must not cache the Buildx binary"),
		},
		textRequirement{
			text: check.releaseWorkflow, needle: "docker/setup-qemu-action@", forbidden: true,
			err: errors.New(".github/workflows/release.yml must not configure QEMU now that arm64 release builds use a native runner"),
		},
		textRequirement{
			text: check.releaseWorkflow, needle: "runs-on: ubuntu-24.04-arm",
			err: errors.New(".github/workflows/release.yml must build the arm64 release image on a native ubuntu-24.04-arm runner"),
		},
	); err != nil {
		return err
	}
	value, err := requireYAMLKey(check.releaseWorkflow, "WORKCELL_SYFT_VERSION", ".github/workflows/release.yml")
	if err != nil {
		return err
	}
	if !pinnedReleaseTagPattern.MatchString(value) {
		return fmt.Errorf("WORKCELL_SYFT_VERSION must be an exact pinned release, found %q", value)
	}
	check.toolPins.syft = value
	return requireTextRequirements(
		textRequirement{
			text: check.releaseWorkflow, needle: "syft-version: ${{ env.WORKCELL_SYFT_VERSION }}",
			err: errors.New(".github/workflows/release.yml must pin the Syft version used for release SBOM generation"),
		},
		textRequirement{
			text: check.releaseWorkflow, needle: "anchore/sbom-action/download-syft@",
			err: errors.New(".github/workflows/release.yml must install the pinned Syft CLI before generating the builder environment manifest"),
		},
	)
}

func (check *pinnedInputsCheck) loadSecurityWorkflow() error {
	return loadPinnedTextInputs([]pinnedTextInput{
		{filepath.Join(check.cfg.WorkflowsDir, "security.yml"), &check.securityWorkflow},
	})
}

func (check *pinnedInputsCheck) validateActionlintPins() error {
	var securityVersion, releaseVersion, securitySHA, releaseSHA string
	if err := loadWorkflowEnvs([]workflowEnvInput{
		{&securityVersion, check.securityWorkflow, "ACTIONLINT_VERSION", `[0-9]+\.[0-9]+\.[0-9]+`, "security actionlint version", ".github/workflows/security.yml"},
		{&releaseVersion, check.releaseWorkflow, "ACTIONLINT_VERSION", `[0-9]+\.[0-9]+\.[0-9]+`, "release actionlint version", ".github/workflows/release.yml"},
	}); err != nil {
		return err
	}
	if securityVersion != releaseVersion {
		return errors.New("ACTIONLINT_VERSION must match between .github/workflows/security.yml and .github/workflows/release.yml")
	}
	if err := loadWorkflowEnvs([]workflowEnvInput{
		{&securitySHA, check.securityWorkflow, "ACTIONLINT_SHA256", `[0-9a-f]{64}`, "security actionlint sha", ".github/workflows/security.yml"},
		{&releaseSHA, check.releaseWorkflow, "ACTIONLINT_SHA256", `[0-9a-f]{64}`, "release actionlint sha", ".github/workflows/release.yml"},
	}); err != nil {
		return err
	}
	if securitySHA != releaseSHA {
		return errors.New("ACTIONLINT_SHA256 must match between .github/workflows/security.yml and .github/workflows/release.yml")
	}
	check.toolPins.actionlintVersion = securityVersion
	check.toolPins.actionlintSHA = securitySHA
	return nil
}

func (check *pinnedInputsCheck) validateZizmorPins() error {
	var securityVersion, securitySHA, releaseVersion, releaseSHA string
	if err := loadWorkflowEnvs([]workflowEnvInput{
		{&securityVersion, check.securityWorkflow, "ZIZMOR_VERSION", `[0-9]+\.[0-9]+\.[0-9]+`, "security zizmor version", ".github/workflows/security.yml"},
		{&securitySHA, check.securityWorkflow, "ZIZMOR_SHA256", `[0-9a-f]{64}`, "security zizmor sha", ".github/workflows/security.yml"},
		{&releaseVersion, check.releaseWorkflow, "ZIZMOR_VERSION", `[0-9]+\.[0-9]+\.[0-9]+`, "release zizmor version", ".github/workflows/release.yml"},
		{&releaseSHA, check.releaseWorkflow, "ZIZMOR_SHA256", `[0-9a-f]{64}`, "release zizmor sha", ".github/workflows/release.yml"},
	}); err != nil {
		return err
	}
	if securityVersion != releaseVersion {
		return errors.New("ZIZMOR_VERSION must match between .github/workflows/security.yml and .github/workflows/release.yml")
	}
	if securitySHA != releaseSHA {
		return errors.New("ZIZMOR_SHA256 must match between .github/workflows/security.yml and .github/workflows/release.yml")
	}
	check.toolPins.zizmorVersion = securityVersion
	check.toolPins.zizmorSHA = securitySHA
	return nil
}

func (check *pinnedInputsCheck) validateToolPinPolicy() error {
	values := check.toolPins
	for _, pin := range []toolPinCheck{
		{"WORKCELL_COSIGN_VERSION", values.cosign, check.pins.Cosign},
		{"WORKCELL_BUILDX_VERSION", values.buildx, check.pins.Buildx},
		{"WORKCELL_BUILDKIT_IMAGE", values.buildkit, check.pins.Buildkit},
		{"WORKCELL_QEMU_IMAGE", values.qemu, check.pins.QEMU},
		{"WORKCELL_SYFT_VERSION", values.syft, check.pins.Syft},
		{"ACTIONLINT_VERSION", values.actionlintVersion, check.pins.ActionlintVersion},
		{"ACTIONLINT_SHA256", values.actionlintSHA, check.pins.ActionlintSHA256},
		{"ZIZMOR_VERSION", values.zizmorVersion, check.pins.ZizmorVersion},
		{"ZIZMOR_SHA256", values.zizmorSHA, check.pins.ZizmorSHA256},
	} {
		if pin.actual != pin.expected {
			return fmt.Errorf("%s pin %q does not match policy/tool-pins.toml %q; the workflow and the policy must stay in lockstep (scripts/update-upstream-pins.sh rewrites both)", pin.name, pin.actual, pin.expected)
		}
	}
	return nil
}

func (check *pinnedInputsCheck) validateSecurityToolDownloads() error {
	downloads := []releaseDownload{
		{"actionlint", "https://github.com/rhysd/actionlint/releases/download/v${ACTIONLINT_VERSION}/actionlint_${ACTIONLINT_VERSION}_linux_amd64.tar.gz"},
		{"zizmor", "https://github.com/zizmorcore/zizmor/releases/download/v${ZIZMOR_VERSION}/zizmor-x86_64-unknown-linux-gnu.tar.gz"},
	}
	for _, workflow := range []workflowText{
		{check.securityWorkflow, ".github/workflows/security.yml"},
		{check.releaseWorkflow, ".github/workflows/release.yml"},
	} {
		if err := requireCappedReleaseDownloads(workflow.text, workflow.path, downloads); err != nil {
			return err
		}
		if err := requireTextRequirements(
			textRequirement{
				text: workflow.text, needle: `echo "${ZIZMOR_SHA256}  zizmor.tar.gz" | sha256sum -c -`,
				err: fmt.Errorf("%s must verify the pinned zizmor archive digest", workflow.path),
			},
			textRequirement{
				text: workflow.text, needle: `tar -xzf zizmor.tar.gz -C "${RUNNER_TEMP}/bin" zizmor`,
				err: fmt.Errorf("%s must install the pinned zizmor binary archive", workflow.path),
			},
		); err != nil {
			return err
		}
	}
	return nil
}

func (check *pinnedInputsCheck) validateSecurityWorkflowDispatch() error {
	for _, needle := range []string{
		"github.event_name == 'workflow_dispatch' && github.ref_name != 'main'",
		"base-ref: ${{ github.event_name == 'workflow_dispatch' && 'refs/heads/main' || '' }}",
		"head-ref: ${{ github.event_name == 'workflow_dispatch' && github.ref || '' }}",
		"./scripts/check-workflows.sh",
	} {
		if !strings.Contains(check.securityWorkflow, needle) {
			return fmt.Errorf(".github/workflows/security.yml must contain %q", needle)
		}
	}
	return nil
}

func (check *pinnedInputsCheck) validateReleaseManifestAndRuntimeSources() error {
	if !strings.Contains(check.releaseWorkflow, "docker buildx imagetools create") {
		return errors.New(".github/workflows/release.yml must assemble the published multi-arch manifest with docker buildx imagetools create")
	}
	if regexp.MustCompile(`docker/build-push-action@.*?platforms:\s*linux/amd64,linux/arm64`).MatchString(check.releaseWorkflow) {
		return errors.New(".github/workflows/release.yml must not publish the final multi-arch image through one opaque multi-platform build-push step")
	}
	requirements := []textRequirement{
		{
			text: check.runtimeDockerfile, needle: "COPY runtime/container/rust /workcell-rust",
			err: errors.New("runtime/container/Dockerfile must vendor the reviewed Rust runtime sources into the builder stage"),
		},
		{
			text: check.runtimeDockerfile, needle: "COPY --from=rust-toolchain /usr/local/cargo /usr/local/cargo",
			err: fmt.Errorf("runtime/container/Dockerfile must copy the pinned Rust toolchain through %q", "COPY --from=rust-toolchain /usr/local/cargo /usr/local/cargo"),
		},
		{
			text: check.runtimeDockerfile, needle: "COPY --from=rust-toolchain /usr/local/rustup /usr/local/rustup",
			err: fmt.Errorf("runtime/container/Dockerfile must copy the pinned Rust toolchain through %q", "COPY --from=rust-toolchain /usr/local/rustup /usr/local/rustup"),
		},
		{
			text: check.runtimeDockerfile, needle: "COPY runtime/container/control-plane-manifest.json /usr/local/libexec/workcell/control-plane-manifest.json",
			err: errors.New("runtime/container/Dockerfile must copy the reviewed control-plane manifest into the runtime image"),
		},
	}
	return requireTextRequirements(requirements...)
}

func (check *pinnedInputsCheck) validateRuntimeCargoBuild() error {
	hasOfflineCargoBuild := strings.Contains(check.runtimeDockerfile, "cargo build \\") ||
		strings.Contains(check.runtimeDockerfile, "\"${toolchain_bin}/cargo\" build \\")
	if !hasOfflineCargoBuild {
		return errors.New("runtime/container/Dockerfile must build the shipped Rust launcher artifacts with cargo --locked --offline")
	}
	buildError := errors.New("runtime/container/Dockerfile must build the shipped Rust launcher artifacts with cargo --locked --offline")
	return requireTextRequirements(
		textRequirement{text: check.runtimeDockerfile, needle: "--locked \\", err: buildError},
		textRequirement{text: check.runtimeDockerfile, needle: "--offline \\", err: buildError},
		textRequirement{
			text: check.runtimeDockerfile, needle: "CARGO_HOME=/workcell-rust/cargo-home",
			err: errors.New("runtime/container/Dockerfile must isolate Cargo home inside the vendored runtime source tree"),
		},
	)
}

func (check *pinnedInputsCheck) validateReleaseRequiredSteps() error {
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
		if !strings.Contains(check.releaseWorkflow, needle) {
			return fmt.Errorf(".github/workflows/release.yml must contain %q", needle)
		}
	}
	return nil
}

func (check *pinnedInputsCheck) validateReleaseArtifactFlows() error {
	if strings.Contains(check.releaseWorkflow, "{{json .manifest}}") {
		return errors.New(".github/workflows/release.yml must not use the unsupported lowercase Buildx .manifest template field")
	}
	bundleError := errors.New(".github/workflows/release.yml must publish direct signature bundles for release artifacts")
	if err := requireTextRequirements(
		textRequirement{text: check.releaseWorkflow, needle: "dist/${{ env.BUNDLE_NAME }}.sigstore.json", err: bundleError},
		textRequirement{text: check.releaseWorkflow, needle: "dist/workcell-control-plane.sigstore.json", err: bundleError},
		textRequirement{text: check.releaseWorkflow, needle: "dist/workcell-image.digest.sigstore.json", err: bundleError},
		textRequirement{text: check.releaseWorkflow, needle: "dist/workcell-source.spdx.sigstore.json", err: bundleError},
		textRequirement{text: check.releaseWorkflow, needle: "dist/workcell-image.spdx.sigstore.json", err: bundleError},
	); err != nil {
		return err
	}
	return firstPinnedInputError(
		func() error { return ValidateReleaseWorkflowControlPlaneFlow(check.releaseWorkflow) },
		func() error {
			return ValidateMacOSInstallVerificationFlow(check.releaseWorkflow, ".github/workflows/release.yml", "workcell-release-install-candidate", "name: Release install verification (${{ matrix.runner_label }})")
		},
		func() error { return ValidateReleaseWorkflowGitHubAttestationFlow(check.releaseWorkflow) },
		func() error { return ValidateReleaseWorkflowPublicationGate(check.releaseWorkflow) },
	)
}

func (check *pinnedInputsCheck) validateReleaseLegacyReferences() error {
	if err := requireTextRequirements(
		textRequirement{
			text: check.releaseWorkflow, needle: "steps.build.outputs.digest", forbidden: true,
			err: errors.New(".github/workflows/release.yml must not keep referencing the old single-step multi-platform digest output"),
		},
		textRequirement{
			text: check.releaseWorkflow, needle: "gh release ", forbidden: true,
			err: errors.New(".github/workflows/release.yml must not depend on an ambient gh CLI; use a pinned release-publish action"),
		},
		textRequirement{
			text: check.releaseWorkflow, needle: "./scripts/publish-github-release.sh",
			err: errors.New(".github/workflows/release.yml must publish assets through the reviewed repo-local GitHub Release API script"),
		},
	); err != nil {
		return err
	}
	if count := strings.Count(check.releaseWorkflow, "./scripts/check-release-tag-signature.sh --github-repo"); count != 2 {
		return fmt.Errorf(".github/workflows/release.yml must verify release tag signatures in preflight and publish jobs, found %d checks", count)
	}
	return nil
}

func (check *pinnedInputsCheck) validateReleaseHostedControls() error {
	if err := requireTextRequirements(
		textRequirement{
			text: check.releaseWorkflow, needle: `run: ./scripts/run-hosted-controls-audit.sh "${GITHUB_REPOSITORY}"`,
			err: fmt.Errorf(".github/workflows/release.yml must contain %q", `run: ./scripts/run-hosted-controls-audit.sh "${GITHUB_REPOSITORY}"`),
		},
		textRequirement{
			text: check.releaseWorkflow, needle: `WORKCELL_HOSTED_CONTROLS_REQUIRED: "1"`,
			err: fmt.Errorf(".github/workflows/release.yml must contain %q", `WORKCELL_HOSTED_CONTROLS_REQUIRED: "1"`),
		},
		textRequirement{
			text: check.releaseWorkflow, needle: `WORKCELL_HOSTED_CONTROLS_TOKEN: ${{ secrets.WORKCELL_HOSTED_CONTROLS_TOKEN }}`,
			err: fmt.Errorf(".github/workflows/release.yml must contain %q", `WORKCELL_HOSTED_CONTROLS_TOKEN: ${{ secrets.WORKCELL_HOSTED_CONTROLS_TOKEN }}`),
		},
		textRequirement{
			text: check.releaseWorkflow, needle: `--immutable-releases-preverified-by-hosted-controls`,
			err: fmt.Errorf(".github/workflows/release.yml must contain %q", `--immutable-releases-preverified-by-hosted-controls`),
		},
		textRequirement{
			text: check.releaseWorkflow, needle: "environment:\n      name: hosted-controls-audit",
			err: errors.New(".github/workflows/release.yml release preflight must bind to the hosted-controls-audit environment"),
		},
	); err != nil {
		return err
	}
	return ValidateUpstreamRefreshWorkflow(check.upstreamRefreshWorkflow)
}

func (check *pinnedInputsCheck) validateHostedControlsWorkflow() error {
	workflow, err := readText(filepath.Join(check.cfg.WorkflowsDir, "hosted-controls.yml"))
	if err != nil {
		return err
	}
	for _, needle := range []string{
		`name: hosted-controls-audit`,
		`run: ./scripts/run-hosted-controls-audit.sh "${GITHUB_REPOSITORY}"`,
		`WORKCELL_HOSTED_CONTROLS_TOKEN: ${{ secrets.WORKCELL_HOSTED_CONTROLS_TOKEN }}`,
		`WORKCELL_HOSTED_CONTROLS_REQUIRED: "1"`,
	} {
		if !strings.Contains(workflow, needle) {
			return fmt.Errorf(".github/workflows/hosted-controls.yml must contain %q", needle)
		}
	}
	return nil
}

func (check *pinnedInputsCheck) validateReleaseVerificationJobs() error {
	requirements := []textRequirement{
		requiredText(check.releaseWorkflow, "./scripts/verify-github-macos-release-test-runners.sh", ".github/workflows/release.yml"),
		requiredText(check.releaseWorkflow, "./scripts/verify-upstream-codex-release.sh", ".github/workflows/release.yml"),
		requiredText(check.releaseWorkflow, "./scripts/verify-upstream-claude-release.sh", ".github/workflows/release.yml"),
		requiredText(check.releaseWorkflow, "./scripts/verify-upstream-copilot-release.sh", ".github/workflows/release.yml"),
		requiredText(check.releaseWorkflow, "./scripts/verify-upstream-gemini-release.sh", ".github/workflows/release.yml"),
		requiredText(check.releaseWorkflow, "./scripts/update-upstream-pins.sh --check", ".github/workflows/release.yml"),
		requiredText(check.pinHygieneWorkflow, "./scripts/ci/job-pin-hygiene.sh", ".github/workflows/pin-hygiene.yml"),
	}
	if err := requireTextRequirements(requirements...); err != nil {
		return err
	}
	pinHygieneJob, err := readText(filepath.Join(check.repoRoot, "scripts", "ci", "job-pin-hygiene.sh"))
	if err != nil {
		return err
	}
	validateJob, err := readText(filepath.Join(check.repoRoot, "scripts", "ci", "job-validate.sh"))
	if err != nil {
		return err
	}
	requirements = []textRequirement{
		requiredText(pinHygieneJob, "${ROOT_DIR}/scripts/verify-upstream-codex-release.sh", "scripts/ci/job-pin-hygiene.sh"),
		requiredText(pinHygieneJob, "${ROOT_DIR}/scripts/verify-upstream-claude-release.sh", "scripts/ci/job-pin-hygiene.sh"),
		requiredText(pinHygieneJob, "${ROOT_DIR}/scripts/verify-upstream-copilot-release.sh", "scripts/ci/job-pin-hygiene.sh"),
		requiredText(pinHygieneJob, "${ROOT_DIR}/scripts/verify-upstream-gemini-release.sh", "scripts/ci/job-pin-hygiene.sh"),
		requiredText(validateJob, `WORKCELL_COPILOT_RELEASE_HELP_MODE=checksum "${ROOT_DIR}/scripts/verify-upstream-copilot-release.sh"`, "scripts/ci/job-validate.sh"),
		requiredText(validateJob, "unset WORKCELL_GITHUB_API_TOKEN GITHUB_TOKEN GH_TOKEN", "scripts/ci/job-validate.sh"),
	}
	return requireTextRequirements(requirements...)
}

func (check *pinnedInputsCheck) validateReleasePublishingInputs() error {
	for _, needle := range []string{
		"environment:\n      name: release",
		`sudo install -m 0755 "$(command -v cosign)" /usr/local/bin/cosign`,
		`sudo install -m 0755 "$(command -v syft)" /usr/local/bin/syft`,
		`actionlint_archive="${RUNNER_TEMP}/actionlint.tar.gz"`,
		`tar -xzf "${actionlint_archive}" -C "${RUNNER_TEMP}" actionlint`,
		"git -c safe.directory=/workspace archive \\",
	} {
		if !strings.Contains(check.releaseWorkflow, needle) {
			return fmt.Errorf(".github/workflows/release.yml must contain %q", needle)
		}
	}
	return nil
}

func (check *pinnedInputsCheck) validateWorkflowPolicies() error {
	for _, workflowPath := range workflowYAMLFiles(check.cfg.WorkflowsDir) {
		text, err := readText(workflowPath)
		if err != nil {
			return err
		}
		if err := check.validateWorkflowPolicy(text, workflowPath); err != nil {
			return err
		}
	}
	return nil
}

func (check *pinnedInputsCheck) validateWorkflowPolicy(text, path string) error {
	if !workflowPermissionsRE.MatchString(text) {
		return fmt.Errorf("workflow-level empty permissions declaration missing in %s", path)
	}
	if strings.Contains(text, "pull_request_target") {
		if err := IsSafePullRequestTargetWorkflow(text, path); err != nil {
			return err
		}
	}
	if regexp.MustCompile(`secrets\.[A-Z0-9_]*(?:PAT|PERSONAL_ACCESS_TOKEN)\b|GH_PAT\b|PERSONAL_ACCESS_TOKEN\b`).MatchString(text) {
		return fmt.Errorf("%s must not contain long-lived personal access tokens", path)
	}
	return validateWorkflowActions(text, path, check.allowedActions)
}

func validateWorkflowActions(text, path string, allowed map[string]bool) error {
	refs, err := extractWorkflowUses(text)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	for _, ref := range refs {
		if err := validateWorkflowAction(ref, path, allowed); err != nil {
			return err
		}
	}
	return nil
}

func validateWorkflowAction(ref, path string, allowed map[string]bool) error {
	action := actionRefPattern.FindStringSubmatch(ref)
	if action == nil {
		return fmt.Errorf("%s has an unsupported uses: reference %q; only pinned owner/repo actions are permitted (no docker:// or local ./ actions)", path, ref)
	}
	if !commitShaPattern.MatchString(action[2]) {
		return fmt.Errorf("%s must pin GitHub Actions by full commit SHA; found %s@%s", path, action[1], action[2])
	}
	segments := strings.SplitN(action[1], "/", 3)
	ownerRepo := segments[0] + "/" + segments[1]
	if !allowed[ownerRepo] {
		return fmt.Errorf("%s uses action %q which is not in the reviewed allowlist policy/allowed-actions.toml", path, ownerRepo)
	}
	return nil
}

func (check *pinnedInputsCheck) validateCodeownersAndHostedPolicy() error {
	for _, required := range []string{
		"/.github/workflows/ @omkhar",
		"/scripts/ @omkhar",
		"/runtime/container/ @omkhar",
		"/docs/provenance.md @omkhar",
	} {
		if !strings.Contains(check.codeowners, required) {
			return fmt.Errorf(".github/CODEOWNERS must declare high-risk ownership for %q", required)
		}
	}
	releaseEnvironment, _ := check.hostedControlsPolicy["release_environment"].(map[string]any)
	releaseMode, _ := releaseEnvironment["mode"].(string)
	allowedModes := map[string]bool{
		"review-gated": true, "single-owner-public": true,
		"single-owner-private": true, "plan-limited-private": true,
	}
	if !allowedModes[releaseMode] {
		return errors.New("policy/github-hosted-controls.toml must set release_environment.mode to 'review-gated', 'single-owner-public', 'single-owner-private', or 'plan-limited-private'")
	}
	return firstPinnedInputError(
		func() error {
			_, err := GitHubActionsPolicy(check.hostedControlsPolicy, "policy/github-hosted-controls.toml")
			return err
		},
		func() error {
			_, err := ReleaseAssets(check.hostedControlsPolicy, "policy/github-hosted-controls.toml")
			return err
		},
		func() error {
			return ValidateCanonicalRepositoryVariables(check.hostedControlsPolicy, "policy/github-hosted-controls.toml")
		},
		func() error {
			return ValidateCanonicalWorkflowEnvironments(check.hostedControlsPolicy, "policy/github-hosted-controls.toml")
		},
		func() error { return validateCanonicalHostedControlsScript(check.hostedControlsScript) },
		func() error {
			return requireNoRegistryBootstrapMCP(check.codexRequirementsText, check.cfg.CodexRequirementsPath)
		},
		func() error {
			return requireNoRegistryBootstrapMCP(check.codexMCPConfigText, check.cfg.CodexMCPConfigPath)
		},
	)
}
