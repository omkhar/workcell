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

func validateActionlintVersions(securityWorkflow, releaseWorkflow string) (string, error) {
	securityVersion, err := requireUniformWorkflowEnv(securityWorkflow, "ACTIONLINT_VERSION", `[0-9]+\.[0-9]+\.[0-9]+`, "security actionlint version", ".github/workflows/security.yml")
	if err != nil {
		return "", err
	}
	releaseVersion, err := requireUniformWorkflowEnv(releaseWorkflow, "ACTIONLINT_VERSION", `[0-9]+\.[0-9]+\.[0-9]+`, "release actionlint version", ".github/workflows/release.yml")
	if err != nil {
		return "", err
	}
	if securityVersion != releaseVersion {
		return "", errors.New("ACTIONLINT_VERSION must match between .github/workflows/security.yml and .github/workflows/release.yml")
	}
	return securityVersion, nil
}

func validateWorkflowBuilderPins(workflowsDir, buildx, buildkit string) error {
	specs := [][3]string{
		{"WORKCELL_BUILDX_VERSION", buildx, ""},
		{"WORKCELL_BUILDKIT_IMAGE", buildkit, "driver-opts: image=${{ env.WORKCELL_BUILDKIT_IMAGE }}"},
	}
	for _, workflowPath := range workflowYAMLFiles(workflowsDir) {
		text, err := readText(workflowPath)
		if err != nil {
			return err
		}
		path := ".github/workflows/" + filepath.Base(workflowPath)
		for _, spec := range specs {
			if err := validateOptionalWorkflowPin(text, path, spec); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateOptionalWorkflowPin(text, path string, spec [3]string) error {
	name, expected, needle := spec[0], spec[1], spec[2]
	present := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(name) + `:`).MatchString(text)
	if !present {
		return nil
	}
	value, err := requireYAMLKey(text, name, path)
	if err != nil {
		return err
	}
	if err := requireEqual(name, expected, ".github/workflows/ci.yml", value, path); err != nil {
		return err
	}
	if needle == "" {
		return nil
	}
	return requireTextRequirements(textRequirement{
		text: text, needle: needle,
		err: fmt.Errorf("%s must pin the BuildKit daemon image used by setup-buildx-action", path),
	})
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
