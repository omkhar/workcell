// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckWorkflowsRecognizesRequiredJobNames(t *testing.T) {
	root := t.TempDir()
	workflowDir := filepath.Join(root, ".github", "workflows")
	if err := os.MkdirAll(workflowDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(workflowDir, "ci.yml"), []byte(`name: CI

on:
  workflow_dispatch:

jobs:
  validate:
    name: Validate repository
    runs-on: ubuntu-latest
    steps:
      - run: true

  container-smoke:
    name: Container smoke
    runs-on: ubuntu-latest
    steps:
      - run: true

  reproducible-build:
    name: Reproducible build
    runs-on: ubuntu-latest
    steps:
      - run: true
`), 0o644); err != nil {
		t.Fatalf("WriteFile(ci.yml) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(workflowDir, "security.yml"), []byte(`name: Security

on:
  workflow_dispatch:

jobs:
  actionlint:
    name: GitHub Actions lint
    runs-on: ubuntu-latest
    steps:
      - run: true

  zizmor:
    name: GitHub Actions security analysis
    runs-on: ubuntu-latest
    steps:
      - run: true
`), 0o644); err != nil {
		t.Fatalf("WriteFile(security.yml) error = %v", err)
	}

	policyPath := filepath.Join(root, "policy.toml")
	if err := os.WriteFile(policyPath, []byte(`[required_status_checks]
contexts = [
  "Validate repository",
  "Container smoke",
  "Reproducible build",
  "GitHub Actions lint",
  "GitHub Actions security analysis",
]
`), 0o644); err != nil {
		t.Fatalf("WriteFile(policy.toml) error = %v", err)
	}

	if err := CheckWorkflows(root, policyPath); err != nil {
		t.Fatalf("CheckWorkflows() error = %v", err)
	}
}

func TestCheckWorkflowsRejectsMultilineSpoofedName(t *testing.T) {
	root := t.TempDir()
	workflowDir := filepath.Join(root, ".github", "workflows")
	if err := os.MkdirAll(workflowDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(workflowDir, "ci.yml"), []byte(`name: CI

on:
  workflow_dispatch:

env:
  SPOOFED_NAME: |
    name: Validate repository

jobs:
  validate:
    runs-on: ubuntu-latest
    steps:
      - run: true
`), 0o644); err != nil {
		t.Fatalf("WriteFile(ci.yml) error = %v", err)
	}

	policyPath := filepath.Join(root, "policy.toml")
	if err := os.WriteFile(policyPath, []byte(`[required_status_checks]
contexts = [
  "Validate repository",
]
`), 0o644); err != nil {
		t.Fatalf("WriteFile(policy.toml) error = %v", err)
	}

	err := CheckWorkflows(root, policyPath)
	if err == nil {
		t.Fatal("CheckWorkflows() unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "Validate repository") {
		t.Fatalf("CheckWorkflows() error = %v, want missing Validate repository", err)
	}
}

func TestValidateReleaseWorkflowControlPlaneFlowRejectsMissingCanonicalArtifact(t *testing.T) {
	releaseWorkflow := `      - name: Generate preflight control-plane manifest
        run: |
          mkdir -p dist
          ./scripts/generate-control-plane-manifest.sh dist/workcell-control-plane-preflight.json

      - name: Regenerate control-plane manifest from archived source tree
        env:
          WORKCELL_CONTROL_PLANE_ROOT: ${{ github.workspace }}/dist/release-source
        run: ./scripts/generate-control-plane-manifest.sh dist/workcell-control-plane-archived.json

      - name: Verify control-plane manifest matches preflight
        run: |
          cmp -s \
            dist/workcell-control-plane-archived.json \
            dist/preflight/workcell-control-plane-preflight.json
`

	err := validateReleaseWorkflowControlPlaneFlow(releaseWorkflow)
	if err == nil {
		t.Fatal("validateReleaseWorkflowControlPlaneFlow() unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "dist/workcell-control-plane.json") {
		t.Fatalf("validateReleaseWorkflowControlPlaneFlow() error = %v, want canonical control-plane artifact path", err)
	}
}

func TestValidateReleaseWorkflowControlPlaneFlowAcceptsCanonicalArtifact(t *testing.T) {
	releaseWorkflow := `      - name: Generate preflight control-plane manifest
        run: |
          mkdir -p dist
          ./scripts/generate-control-plane-manifest.sh dist/workcell-control-plane-preflight.json

      - name: Regenerate control-plane manifest from archived source tree
        env:
          WORKCELL_CONTROL_PLANE_ROOT: ${{ github.workspace }}/dist/release-source
        run: ./scripts/generate-control-plane-manifest.sh dist/workcell-control-plane.json

      - name: Verify control-plane manifest matches preflight
        run: |
          cmp -s \
            dist/workcell-control-plane.json \
            dist/preflight/workcell-control-plane-preflight.json
`

	if err := validateReleaseWorkflowControlPlaneFlow(releaseWorkflow); err != nil {
		t.Fatalf("validateReleaseWorkflowControlPlaneFlow() error = %v", err)
	}
}

func TestValidateMacOSInstallVerificationFlowRejectsMissingBundleUninstall(t *testing.T) {
	workflow := `      - name: Upload CI release install artifacts
        uses: actions/upload-artifact@b7c566a772e6b6bfb58ed0dc250532a479d7789f # v6.0.0
        with:
          name: workcell-ci-install-candidate

      - name: Download CI release install artifacts
        uses: actions/download-artifact@018cc2cf5baa6db3ef3c5f8a56943fffe632ef53 # v6.0.0
        with:
          name: workcell-ci-install-candidate
          path: dist/install

  install-verification:
    name: Install verification (${{ matrix.runner_label }})
    strategy:
      matrix:
        include:
          - runner: macos-26
            runner_label: macos-26
          - runner: macos-15
            runner_label: macos-15
    steps:
      - run: |
          bundle_path="$(find dist/install -maxdepth 1 -type f -name 'workcell-*.tar.gz' -print -quit)"
          "${bundle_dir}/scripts/install.sh"
          brew tap-new "${tap_name}" --no-git
          brew --repo "${tap_name}"
          brew install "${tap_name}/workcell"
          brew uninstall --force "${tap_name}/workcell"
          brew list --versions workcell
`

	err := validateMacOSInstallVerificationFlow(workflow, ".github/workflows/ci.yml", "workcell-ci-install-candidate", "name: Install verification (${{ matrix.runner_label }})")
	if err == nil {
		t.Fatal("validateMacOSInstallVerificationFlow() unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "scripts/uninstall.sh") {
		t.Fatalf("validateMacOSInstallVerificationFlow() error = %v, want missing bundle uninstall check", err)
	}
}

func TestValidateMacOSInstallVerificationFlowAcceptsCanonicalFlow(t *testing.T) {
	workflow := `      - name: Upload CI release install artifacts
        uses: actions/upload-artifact@b7c566a772e6b6bfb58ed0dc250532a479d7789f # v6.0.0
        with:
          name: workcell-ci-install-candidate

  install-verification:
    name: Install verification (${{ matrix.runner_label }})
    strategy:
      matrix:
        include:
          - runner: macos-26
            runner_label: macos-26
          - runner: macos-15
            runner_label: macos-15
    steps:
      - uses: actions/download-artifact@018cc2cf5baa6db3ef3c5f8a56943fffe632ef53 # v6.0.0
      - run: |
          bundle_path="$(find dist/install -maxdepth 1 -type f -name 'workcell-*.tar.gz' -print -quit)"
          "${bundle_dir}/scripts/install.sh"
          "${bundle_dir}/scripts/uninstall.sh"
          brew tap-new "${tap_name}" --no-git
          brew --repo "${tap_name}"
          brew install "${tap_name}/workcell"
          brew uninstall --force "${tap_name}/workcell"
          brew list --versions workcell
`

	if err := validateMacOSInstallVerificationFlow(workflow, ".github/workflows/ci.yml", "workcell-ci-install-candidate", "name: Install verification (${{ matrix.runner_label }})"); err != nil {
		t.Fatalf("validateMacOSInstallVerificationFlow() error = %v", err)
	}
}

func TestValidateUpstreamRefreshWorkflowRejectsMissingDispatches(t *testing.T) {
	workflow := `name: Upstream refresh

on:
  workflow_dispatch:

jobs:
  refresh:
    permissions:
      actions: write
      contents: write
      pull-requests: write
    steps:
      - uses: actions/checkout@de0fac2e4500dabe0009e67214ff5f5447ce83dd # v6.0.2
        with:
          fetch-depth: 0
          persist-credentials: false
      - run: ./scripts/update-upstream-pins.sh --apply
      - run: ./scripts/update-upstream-pins.sh --check
      - run: ./scripts/check-pinned-inputs.sh
      - env:
          WORKCELL_UPSTREAM_REFRESH_GPG_PRIVATE_KEY: ${{ secrets.WORKCELL_UPSTREAM_REFRESH_GPG_PRIVATE_KEY }}
          WORKCELL_UPSTREAM_REFRESH_GPG_KEY_ID: ${{ secrets.WORKCELL_UPSTREAM_REFRESH_GPG_KEY_ID }}
      - run: |
          git commit -S -F "${commit_file}"
          gh pr create --draft
`

	err := validateUpstreamRefreshWorkflow(workflow)
	if err == nil {
		t.Fatal("validateUpstreamRefreshWorkflow() unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "gh workflow run") {
		t.Fatalf("validateUpstreamRefreshWorkflow() error = %v, want missing workflow dispatch guard", err)
	}
}

func TestValidateUpstreamRefreshWorkflowAcceptsCanonicalFlow(t *testing.T) {
	workflow := `name: Upstream refresh

on:
  workflow_dispatch:

jobs:
  refresh:
    permissions:
      actions: write
      contents: write
      pull-requests: write
    steps:
      - uses: actions/checkout@de0fac2e4500dabe0009e67214ff5f5447ce83dd # v6.0.2
        with:
          fetch-depth: 0
          persist-credentials: false
      - run: ./scripts/update-upstream-pins.sh --apply
      - run: |
          ./scripts/update-upstream-pins.sh --check
          ./scripts/check-pinned-inputs.sh
      - env:
          WORKCELL_UPSTREAM_REFRESH_GPG_PRIVATE_KEY: ${{ secrets.WORKCELL_UPSTREAM_REFRESH_GPG_PRIVATE_KEY }}
          WORKCELL_UPSTREAM_REFRESH_GPG_KEY_ID: ${{ secrets.WORKCELL_UPSTREAM_REFRESH_GPG_KEY_ID }}
        run: |
          git commit -S -F "${commit_file}"
          gh pr create --draft
          gh workflow run "ci.yml" --ref "$branch"
          gh workflow run "docs.yml" --ref "$branch"
          gh workflow run "security.yml" --ref "$branch"
          gh workflow run "codeql.yml" --ref "$branch"
`

	if err := validateUpstreamRefreshWorkflow(workflow); err != nil {
		t.Fatalf("validateUpstreamRefreshWorkflow() error = %v", err)
	}
}

func TestValidateReleaseWorkflowGitHubAttestationFlowRejectsMissingSupportGuard(t *testing.T) {
	releaseWorkflow := `env:
  ENABLE_GITHUB_ATTESTATIONS: ${{ vars.WORKCELL_ENABLE_GITHUB_ATTESTATIONS || 'false' }}
  ENABLE_GITHUB_ATTESTATIONS_SUPPORTED: ${{ !github.event.repository.private || github.event.repository.owner.type != 'User' }}

      - uses: actions/attest@59d89421af93a897026c735860bf21b6eb4f7b26 # v4.1.0
        if: env.ENABLE_GITHUB_ATTESTATIONS == 'true'
      - uses: actions/attest@59d89421af93a897026c735860bf21b6eb4f7b26 # v4.1.0
        if: env.ENABLE_GITHUB_ATTESTATIONS == 'true'
      - uses: actions/attest@59d89421af93a897026c735860bf21b6eb4f7b26 # v4.1.0
        if: env.ENABLE_GITHUB_ATTESTATIONS == 'true'
      - uses: actions/attest@59d89421af93a897026c735860bf21b6eb4f7b26 # v4.1.0
        if: env.ENABLE_GITHUB_ATTESTATIONS == 'true'
        with:
          subject-name: ${{ env.IMAGE_NAME }}
`

	err := validateReleaseWorkflowGitHubAttestationFlow(releaseWorkflow)
	if err == nil {
		t.Fatal("validateReleaseWorkflowGitHubAttestationFlow() unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "public visibility or an explicit private-repo capability flag") {
		t.Fatalf("validateReleaseWorkflowGitHubAttestationFlow() error = %v, want support guard failure", err)
	}
}

func TestValidateReleaseWorkflowGitHubAttestationFlowRejectsUnguardedAttestStep(t *testing.T) {
	releaseWorkflow := `env:
  ENABLE_GITHUB_ATTESTATIONS: ${{ vars.WORKCELL_ENABLE_GITHUB_ATTESTATIONS || 'false' }}
  ENABLE_GITHUB_ATTESTATIONS_SUPPORTED: ${{ github.event.repository.visibility == 'public' || vars.WORKCELL_ENABLE_PRIVATE_GITHUB_ATTESTATIONS == 'true' }}

      - uses: actions/attest@59d89421af93a897026c735860bf21b6eb4f7b26 # v4.1.0
        if: env.ENABLE_GITHUB_ATTESTATIONS == 'true' && env.ENABLE_GITHUB_ATTESTATIONS_SUPPORTED == 'true'
        with:
          subject-name: ${{ env.IMAGE_NAME }}
      - uses: actions/attest@59d89421af93a897026c735860bf21b6eb4f7b26 # v4.1.0
        if: env.ENABLE_GITHUB_ATTESTATIONS == 'true'
        with:
          sbom-path: dist/workcell-image.spdx.json
          subject-name: ${{ env.IMAGE_NAME }}
      - uses: actions/attest@59d89421af93a897026c735860bf21b6eb4f7b26 # v4.1.0
        if: env.ENABLE_GITHUB_ATTESTATIONS == 'true' && env.ENABLE_GITHUB_ATTESTATIONS_SUPPORTED == 'true'
        with:
          subject-path: dist/${{ env.BUNDLE_NAME }}
      - uses: actions/attest@59d89421af93a897026c735860bf21b6eb4f7b26 # v4.1.0
        if: env.ENABLE_GITHUB_ATTESTATIONS == 'true' && env.ENABLE_GITHUB_ATTESTATIONS_SUPPORTED == 'true'
        with:
          sbom-path: dist/workcell-source.spdx.json
          subject-path: dist/${{ env.BUNDLE_NAME }}
      - uses: actions/attest@59d89421af93a897026c735860bf21b6eb4f7b26 # v4.1.0
        if: env.ENABLE_GITHUB_ATTESTATIONS == 'true' && env.ENABLE_GITHUB_ATTESTATIONS_SUPPORTED == 'true'
        with:
          subject-path: dist/workcell.rb
      - uses: actions/attest@59d89421af93a897026c735860bf21b6eb4f7b26 # v4.1.0
        if: env.ENABLE_GITHUB_ATTESTATIONS == 'true' && env.ENABLE_GITHUB_ATTESTATIONS_SUPPORTED == 'true'
        with:
          subject-path: dist/workcell-image.digest
      - uses: actions/attest@59d89421af93a897026c735860bf21b6eb4f7b26 # v4.1.0
        if: env.ENABLE_GITHUB_ATTESTATIONS == 'true' && env.ENABLE_GITHUB_ATTESTATIONS_SUPPORTED == 'true'
        with:
          subject-path: dist/workcell-build-inputs.json
      - uses: actions/attest@59d89421af93a897026c735860bf21b6eb4f7b26 # v4.1.0
        if: env.ENABLE_GITHUB_ATTESTATIONS == 'true' && env.ENABLE_GITHUB_ATTESTATIONS_SUPPORTED == 'true'
        with:
          subject-path: dist/workcell-control-plane.json
      - uses: actions/attest@59d89421af93a897026c735860bf21b6eb4f7b26 # v4.1.0
        if: env.ENABLE_GITHUB_ATTESTATIONS == 'true' && env.ENABLE_GITHUB_ATTESTATIONS_SUPPORTED == 'true'
        with:
          subject-path: dist/workcell-builder-environment.json
      - uses: actions/attest@59d89421af93a897026c735860bf21b6eb4f7b26 # v4.1.0
        if: env.ENABLE_GITHUB_ATTESTATIONS == 'true' && env.ENABLE_GITHUB_ATTESTATIONS_SUPPORTED == 'true'
        with:
          subject-path: dist/SHA256SUMS
`

	err := validateReleaseWorkflowGitHubAttestationFlow(releaseWorkflow)
	if err == nil {
		t.Fatal("validateReleaseWorkflowGitHubAttestationFlow() unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "guard every actions/attest step") {
		t.Fatalf("validateReleaseWorkflowGitHubAttestationFlow() error = %v, want unguarded attestation failure", err)
	}
}

func TestValidateReleaseWorkflowGitHubAttestationFlowAcceptsSupportGuard(t *testing.T) {
	releaseWorkflow := `env:
  ENABLE_GITHUB_ATTESTATIONS: ${{ vars.WORKCELL_ENABLE_GITHUB_ATTESTATIONS || 'false' }}
  ENABLE_GITHUB_ATTESTATIONS_SUPPORTED: ${{ github.event.repository.visibility == 'public' || vars.WORKCELL_ENABLE_PRIVATE_GITHUB_ATTESTATIONS == 'true' }}

      - uses: actions/attest@59d89421af93a897026c735860bf21b6eb4f7b26 # v4.1.0
        if: env.ENABLE_GITHUB_ATTESTATIONS == 'true' && env.ENABLE_GITHUB_ATTESTATIONS_SUPPORTED == 'true'
        with:
          subject-name: ${{ env.IMAGE_NAME }}
      - uses: actions/attest@59d89421af93a897026c735860bf21b6eb4f7b26 # v4.1.0
        if: env.ENABLE_GITHUB_ATTESTATIONS == 'true' && env.ENABLE_GITHUB_ATTESTATIONS_SUPPORTED == 'true'
        with:
          sbom-path: dist/workcell-image.spdx.json
          subject-name: ${{ env.IMAGE_NAME }}
      - uses: actions/attest@59d89421af93a897026c735860bf21b6eb4f7b26 # v4.1.0
        if: env.ENABLE_GITHUB_ATTESTATIONS == 'true' && env.ENABLE_GITHUB_ATTESTATIONS_SUPPORTED == 'true'
        with:
          subject-path: dist/${{ env.BUNDLE_NAME }}
      - uses: actions/attest@59d89421af93a897026c735860bf21b6eb4f7b26 # v4.1.0
        if: env.ENABLE_GITHUB_ATTESTATIONS == 'true' && env.ENABLE_GITHUB_ATTESTATIONS_SUPPORTED == 'true'
        with:
          sbom-path: dist/workcell-source.spdx.json
          subject-path: dist/${{ env.BUNDLE_NAME }}
      - uses: actions/attest@59d89421af93a897026c735860bf21b6eb4f7b26 # v4.1.0
        if: env.ENABLE_GITHUB_ATTESTATIONS == 'true' && env.ENABLE_GITHUB_ATTESTATIONS_SUPPORTED == 'true'
        with:
          subject-path: dist/workcell.rb
      - uses: actions/attest@59d89421af93a897026c735860bf21b6eb4f7b26 # v4.1.0
        if: env.ENABLE_GITHUB_ATTESTATIONS == 'true' && env.ENABLE_GITHUB_ATTESTATIONS_SUPPORTED == 'true'
        with:
          subject-path: dist/workcell-image.digest
      - uses: actions/attest@59d89421af93a897026c735860bf21b6eb4f7b26 # v4.1.0
        if: env.ENABLE_GITHUB_ATTESTATIONS == 'true' && env.ENABLE_GITHUB_ATTESTATIONS_SUPPORTED == 'true'
        with:
          subject-path: dist/workcell-build-inputs.json
      - uses: actions/attest@59d89421af93a897026c735860bf21b6eb4f7b26 # v4.1.0
        if: env.ENABLE_GITHUB_ATTESTATIONS == 'true' && env.ENABLE_GITHUB_ATTESTATIONS_SUPPORTED == 'true'
        with:
          subject-path: dist/workcell-control-plane.json
      - uses: actions/attest@59d89421af93a897026c735860bf21b6eb4f7b26 # v4.1.0
        if: env.ENABLE_GITHUB_ATTESTATIONS == 'true' && env.ENABLE_GITHUB_ATTESTATIONS_SUPPORTED == 'true'
        with:
          subject-path: dist/workcell-builder-environment.json
      - uses: actions/attest@59d89421af93a897026c735860bf21b6eb4f7b26 # v4.1.0
        if: env.ENABLE_GITHUB_ATTESTATIONS == 'true' && env.ENABLE_GITHUB_ATTESTATIONS_SUPPORTED == 'true'
        with:
          subject-path: dist/SHA256SUMS
`

	if err := validateReleaseWorkflowGitHubAttestationFlow(releaseWorkflow); err != nil {
		t.Fatalf("validateReleaseWorkflowGitHubAttestationFlow() error = %v", err)
	}
}

func TestValidateCanonicalHostedControlsRepositoryVariablesRejectsMissingPrivateAttestationFlag(t *testing.T) {
	policy := map[string]any{
		"repository_variables": map[string]any{
			"WORKCELL_ENABLE_GITHUB_ATTESTATIONS": "true",
		},
	}

	err := validateCanonicalHostedControlsRepositoryVariables(policy, "policy/github-hosted-controls.toml")
	if err == nil {
		t.Fatal("validateCanonicalHostedControlsRepositoryVariables() unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "WORKCELL_ENABLE_PRIVATE_GITHUB_ATTESTATIONS") {
		t.Fatalf("validateCanonicalHostedControlsRepositoryVariables() error = %v, want missing private attestation flag", err)
	}
}

func TestValidateCanonicalHostedControlsRepositoryVariablesRejectsWrongPrivateAttestationFlag(t *testing.T) {
	policy := map[string]any{
		"repository_variables": map[string]any{
			"WORKCELL_ENABLE_GITHUB_ATTESTATIONS":         "true",
			"WORKCELL_ENABLE_PRIVATE_GITHUB_ATTESTATIONS": "true",
		},
	}

	err := validateCanonicalHostedControlsRepositoryVariables(policy, "policy/github-hosted-controls.toml")
	if err == nil {
		t.Fatal("validateCanonicalHostedControlsRepositoryVariables() unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), `WORKCELL_ENABLE_PRIVATE_GITHUB_ATTESTATIONS = "false"`) {
		t.Fatalf("validateCanonicalHostedControlsRepositoryVariables() error = %v, want private attestation value failure", err)
	}
}

func TestValidateCanonicalHostedControlsRepositoryVariablesAcceptsCanonicalValues(t *testing.T) {
	policy := map[string]any{
		"repository_variables": map[string]any{
			"WORKCELL_ENABLE_GITHUB_ATTESTATIONS":         "true",
			"WORKCELL_ENABLE_PRIVATE_GITHUB_ATTESTATIONS": "false",
		},
	}

	if err := validateCanonicalHostedControlsRepositoryVariables(policy, "policy/github-hosted-controls.toml"); err != nil {
		t.Fatalf("validateCanonicalHostedControlsRepositoryVariables() error = %v", err)
	}
}
