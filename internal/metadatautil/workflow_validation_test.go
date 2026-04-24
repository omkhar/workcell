// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func installWorkflowToolStubs(t *testing.T, root string) {
	t.Helper()

	binDir := filepath.Join(root, "test-bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(test-bin) error = %v", err)
	}
	for _, name := range []string{"actionlint", "zizmor"} {
		path := filepath.Join(binDir, name)
		if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", path, err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, ".github"), 0o755); err != nil {
		t.Fatalf("MkdirAll(.github) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".github", "zizmor.yml"), []byte("rules: []\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(zizmor.yml) error = %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func writeWorkflowValidationFixtures(t *testing.T, workflowDir string) {
	t.Helper()

	if err := os.WriteFile(filepath.Join(workflowDir, "codeql.yml"), []byte(`name: CodeQL

on:
  workflow_dispatch:

jobs:
  analyze:
    strategy:
      matrix:
        include:
          - language: rust
            build-mode: none
          - language: javascript-typescript
            build-mode: none
          - language: go
            build-mode: autobuild
    steps:
      - uses: github/codeql-action/init@deadbeef
      - uses: github/codeql-action/autobuild@deadbeef
      - uses: github/codeql-action/analyze@deadbeef
`), 0o644); err != nil {
		t.Fatalf("WriteFile(codeql.yml) error = %v", err)
	}

	if err := os.WriteFile(filepath.Join(workflowDir, "release.yml"), []byte(`name: Release

on:
  workflow_dispatch:

jobs:
  codeql-preflight:
    name: Release CodeQL (${{ matrix.language }})
    strategy:
      matrix:
        include:
          - language: rust
            build-mode: none
          - language: javascript-typescript
            build-mode: none
          - language: go
            build-mode: autobuild
    steps:
      - uses: github/codeql-action/init@deadbeef
      - uses: github/codeql-action/autobuild@deadbeef
      - uses: github/codeql-action/analyze@deadbeef

  preflight:
    name: Release preflight
    runs-on: ubuntu-latest
    steps:
      - run: true

  install-verification:
    name: Release install verification
    runs-on: ubuntu-latest
    steps:
      - run: true

  release:
    name: Publish release artifacts
    needs:
      - codeql-preflight
      - preflight
      - install-verification
    runs-on: ubuntu-latest
    steps:
      - run: true
`), 0o644); err != nil {
		t.Fatalf("WriteFile(release.yml) error = %v", err)
	}
}

func TestCheckWorkflowsRecognizesRequiredJobNames(t *testing.T) {
	root := t.TempDir()
	installWorkflowToolStubs(t, root)
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
	if err := os.WriteFile(filepath.Join(workflowDir, "pr-base-policy.yml"), []byte(`name: PR base policy

on:
  pull_request_target:
    types:
      - opened

permissions: {}

jobs:
  pr-base-policy:
    name: Allowed PR base
    runs-on: ubuntu-latest
    steps:
      - run: true
`), 0o644); err != nil {
		t.Fatalf("WriteFile(pr-base-policy.yml) error = %v", err)
	}
	writeWorkflowValidationFixtures(t, workflowDir)

	policyPath := filepath.Join(root, "policy.toml")
	if err := os.WriteFile(policyPath, []byte(`[required_status_checks]
contexts = [
  "Allowed PR base",
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
	installWorkflowToolStubs(t, root)
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
	writeWorkflowValidationFixtures(t, workflowDir)

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

func TestSafePullRequestTargetWorkflowAllowsTrustedPRBasePolicy(t *testing.T) {
	t.Parallel()

	workflow := `name: PR base policy

on:
  # kusari-inspector suppress: trusted metadata-only pull_request_target exception.
  pull_request_target:
    types:
      - opened

permissions: {}

jobs:
  pr-base-policy:
    name: Allowed PR base
    runs-on: ubuntu-latest
    steps:
      - run: true
`
	if err := isSafePullRequestTargetWorkflow(workflow, ".github/workflows/pr-base-policy.yml"); err != nil {
		t.Fatalf("isSafePullRequestTargetWorkflow() error = %v", err)
	}
}

func TestSafePullRequestTargetWorkflowRejectsCheckout(t *testing.T) {
	t.Parallel()

	workflow := `name: PR base policy

on:
  # kusari-inspector suppress: trusted metadata-only pull_request_target exception.
  pull_request_target:
    types:
      - opened

permissions: {}

jobs:
  pr-base-policy:
    name: Allowed PR base
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@de0fac2e4500dabe0009e67214ff5f5447ce83dd
`
	err := isSafePullRequestTargetWorkflow(workflow, ".github/workflows/pr-base-policy.yml")
	if err == nil {
		t.Fatal("isSafePullRequestTargetWorkflow() unexpectedly accepted checkout under pull_request_target")
	}
	if !strings.Contains(err.Error(), "must not checkout repository contents") {
		t.Fatalf("isSafePullRequestTargetWorkflow() error = %v, want checkout rejection", err)
	}
}

func TestSafePullRequestTargetWorkflowRejectsJobLevelPermissions(t *testing.T) {
	t.Parallel()

	workflow := `name: PR base policy

on:
  # kusari-inspector suppress: trusted metadata-only pull_request_target exception.
  pull_request_target:
    types:
      - opened

permissions: {}

jobs:
  pr-base-policy:
    name: Allowed PR base
    runs-on: ubuntu-latest
    permissions:
      contents: read
    steps:
      - run: true
`
	err := isSafePullRequestTargetWorkflow(workflow, ".github/workflows/pr-base-policy.yml")
	if err == nil {
		t.Fatal("isSafePullRequestTargetWorkflow() unexpectedly accepted job-level permissions under pull_request_target")
	}
	if !strings.Contains(err.Error(), "must not grant job-level permissions") {
		t.Fatalf("isSafePullRequestTargetWorkflow() error = %v, want job-level permissions rejection", err)
	}
}

func TestSafePullRequestTargetWorkflowRejectsInlineJobLevelPermissions(t *testing.T) {
	t.Parallel()

	workflow := `name: PR base policy

on:
  # kusari-inspector suppress: trusted metadata-only pull_request_target exception.
  pull_request_target:
    types:
      - opened

permissions: {}

jobs: { pr-base-policy: { name: Allowed PR base, runs-on: ubuntu-latest, permissions: { contents: write }, steps: [ { run: true } ] } }
`
	err := isSafePullRequestTargetWorkflow(workflow, ".github/workflows/pr-base-policy.yml")
	if err == nil {
		t.Fatal("isSafePullRequestTargetWorkflow() unexpectedly accepted inline job-level permissions under pull_request_target")
	}
	if !strings.Contains(err.Error(), "must not grant job-level permissions") {
		t.Fatalf("isSafePullRequestTargetWorkflow() error = %v, want job-level permissions rejection", err)
	}
}

func TestSafePullRequestTargetWorkflowRejectsReusableWorkflowCalls(t *testing.T) {
	t.Parallel()

	workflow := `name: PR base policy

on:
  # kusari-inspector suppress: trusted metadata-only pull_request_target exception.
  pull_request_target:
    types:
      - opened

permissions: {}

jobs:
  pr-base-policy:
    uses: ./.github/workflows/reusable.yml
`
	err := isSafePullRequestTargetWorkflow(workflow, ".github/workflows/pr-base-policy.yml")
	if err == nil {
		t.Fatal("isSafePullRequestTargetWorkflow() unexpectedly accepted reusable workflow invocation under pull_request_target")
	}
	if !strings.Contains(err.Error(), "must not call reusable workflows") {
		t.Fatalf("isSafePullRequestTargetWorkflow() error = %v, want reusable workflow rejection", err)
	}
}

func TestSafePullRequestTargetWorkflowRejectsInlineReusableWorkflowCalls(t *testing.T) {
	t.Parallel()

	workflow := `name: PR base policy

on:
  # kusari-inspector suppress: trusted metadata-only pull_request_target exception.
  pull_request_target:
    types:
      - opened

permissions: {}

jobs: { pr-base-policy: { uses: ./.github/workflows/reusable.yml } }
`
	err := isSafePullRequestTargetWorkflow(workflow, ".github/workflows/pr-base-policy.yml")
	if err == nil {
		t.Fatal("isSafePullRequestTargetWorkflow() unexpectedly accepted inline reusable workflow invocation under pull_request_target")
	}
	if !strings.Contains(err.Error(), "must not call reusable workflows") {
		t.Fatalf("isSafePullRequestTargetWorkflow() error = %v, want reusable workflow rejection", err)
	}
}

func TestSafePullRequestTargetWorkflowRejectsInlineStepUses(t *testing.T) {
	t.Parallel()

	workflow := `name: PR base policy

on:
  # kusari-inspector suppress: trusted metadata-only pull_request_target exception.
  pull_request_target:
    types:
      - opened

permissions: {}

jobs: { pr-base-policy: { name: Allowed PR base, runs-on: ubuntu-latest, steps: [ { uses: evil/action@0123456789012345678901234567890123456789 } ] } }
`
	err := isSafePullRequestTargetWorkflow(workflow, ".github/workflows/pr-base-policy.yml")
	if err == nil {
		t.Fatal("isSafePullRequestTargetWorkflow() unexpectedly accepted inline external action under pull_request_target")
	}
	if !strings.Contains(err.Error(), "must not use external actions") {
		t.Fatalf("isSafePullRequestTargetWorkflow() error = %v, want external action rejection", err)
	}
}

func TestSafePullRequestTargetWorkflowRejectsInlineCheckout(t *testing.T) {
	t.Parallel()

	workflow := `name: PR base policy

on:
  # kusari-inspector suppress: trusted metadata-only pull_request_target exception.
  pull_request_target:
    types:
      - opened

permissions: {}

jobs: { pr-base-policy: { name: Allowed PR base, runs-on: ubuntu-latest, steps: [ { uses: actions/checkout@de0fac2e4500dabe0009e67214ff5f5447ce83dd } ] } }
`
	err := isSafePullRequestTargetWorkflow(workflow, ".github/workflows/pr-base-policy.yml")
	if err == nil {
		t.Fatal("isSafePullRequestTargetWorkflow() unexpectedly accepted inline checkout under pull_request_target")
	}
	if !strings.Contains(err.Error(), "must not checkout repository contents") {
		t.Fatalf("isSafePullRequestTargetWorkflow() error = %v, want checkout rejection", err)
	}
}

func TestSafePullRequestTargetWorkflowRequiresSuppressionComment(t *testing.T) {
	t.Parallel()

	workflow := `name: PR base policy

on:
  pull_request_target:
    types:
      - opened

permissions: {}

jobs:
  pr-base-policy:
    name: Allowed PR base
    runs-on: ubuntu-latest
    steps:
      - run: true
`
	err := isSafePullRequestTargetWorkflow(workflow, ".github/workflows/pr-base-policy.yml")
	if err == nil {
		t.Fatal("isSafePullRequestTargetWorkflow() unexpectedly accepted a pull_request_target workflow without a Kusari suppression comment")
	}
	if !strings.Contains(err.Error(), "must document the reviewed Kusari suppression") {
		t.Fatalf("isSafePullRequestTargetWorkflow() error = %v, want Kusari suppression rejection", err)
	}
}

func TestValidateReleaseWorkflowControlPlaneFlowRejectsMissingCanonicalArtifact(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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

func TestValidateCodeQLWorkflowRejectsGoBuildlessMode(t *testing.T) {
	t.Parallel()
	workflow := `jobs:
  analyze:
    strategy:
      matrix:
        include:
          - language: rust
            build-mode: none
          - language: javascript-typescript
            build-mode: none
          - language: go
            build-mode: none
    steps:
      - uses: github/codeql-action/init@deadbeef
      - uses: github/codeql-action/analyze@deadbeef
`

	err := validateCodeQLWorkflow(workflow, ".github/workflows/codeql.yml")
	if err == nil {
		t.Fatal("validateCodeQLWorkflow() unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "build-mode: none") {
		t.Fatalf("validateCodeQLWorkflow() error = %v, want go build-mode rejection", err)
	}
}

func TestValidateCodeQLWorkflowAcceptsGoAutobuild(t *testing.T) {
	t.Parallel()
	workflow := `jobs:
  analyze:
    strategy:
      matrix:
        include:
          - language: rust
            build-mode: none
          - language: javascript-typescript
            build-mode: none
          - language: go
            build-mode: autobuild
    steps:
      - uses: github/codeql-action/init@deadbeef
      - uses: github/codeql-action/autobuild@deadbeef
      - uses: github/codeql-action/analyze@deadbeef
`

	if err := validateCodeQLWorkflow(workflow, ".github/workflows/codeql.yml"); err != nil {
		t.Fatalf("validateCodeQLWorkflow() error = %v", err)
	}
}

func TestValidateReleaseWorkflowCodeQLFlowRejectsMissingGoAutobuild(t *testing.T) {
	t.Parallel()
	workflow := `jobs:
  preflight:
    steps:
      - uses: github/codeql-action/init@deadbeef
      - uses: github/codeql-action/analyze@deadbeef

  release:
    needs:
      - preflight
`

	err := validateReleaseWorkflowCodeQLFlow(workflow)
	if err == nil {
		t.Fatal("validateReleaseWorkflowCodeQLFlow() unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "Release CodeQL") {
		t.Fatalf("validateReleaseWorkflowCodeQLFlow() error = %v, want missing release CodeQL job", err)
	}
}

func TestValidateReleaseWorkflowCodeQLFlowAcceptsMatrixJob(t *testing.T) {
	t.Parallel()
	workflow := `jobs:
  codeql-preflight:
    name: Release CodeQL (${{ matrix.language }})
    strategy:
      matrix:
        include:
          - language: rust
            build-mode: none
          - language: javascript-typescript
            build-mode: none
          - language: go
            build-mode: autobuild
    steps:
      - uses: github/codeql-action/init@deadbeef
      - uses: github/codeql-action/autobuild@deadbeef
      - uses: github/codeql-action/analyze@deadbeef

  release:
    needs:
      - codeql-preflight
      - preflight
      - install-verification
`

	if err := validateReleaseWorkflowCodeQLFlow(workflow); err != nil {
		t.Fatalf("validateReleaseWorkflowCodeQLFlow() error = %v", err)
	}
}

func TestValidateCIWorkflowPRShapeFlowRejectsLegacyInlineShapeGate(t *testing.T) {
	t.Parallel()
	workflow := `jobs:
  pr-shape:
    name: Pull request shape
    steps:
      - uses: actions/checkout@deadbeef
        with:
          fetch-depth: 0
      - name: Check pull request shape
        if: ${{ github.event_name == 'pull_request' }}
        env:
          WORKCELL_PR_BASE_REF: ${{ github.event.pull_request.base.ref }}
        run: |
          git fetch --no-tags --prune origin "${WORKCELL_PR_BASE_REF}"
          ./scripts/check-pr-shape.sh --base-ref "origin/${WORKCELL_PR_BASE_REF}" --head-ref HEAD --max-files 25 --max-lines 1200 --max-areas 8 --max-binaries 0
      - name: Skip outside pull requests
        if: ${{ github.event_name != 'pull_request' }}
        run: echo "PR shape gate applies only to pull requests."
`

	err := validateCIWorkflowPRShapeFlow(workflow)
	if err == nil {
		t.Fatal("validateCIWorkflowPRShapeFlow() unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "./scripts/ci/job-pr-shape.sh --base") {
		t.Fatalf("validateCIWorkflowPRShapeFlow() error = %v, want shared job gate", err)
	}
}

func TestValidateCIWorkflowPRShapeFlowAcceptsSharedJobGate(t *testing.T) {
	t.Parallel()
	workflow := `jobs:
  pr-shape:
    name: Pull request shape
    steps:
      - uses: actions/checkout@deadbeef
        with:
          fetch-depth: 0
      - name: Check pull request shape
        if: ${{ github.event_name == 'pull_request' }}
        env:
          WORKCELL_PR_BASE_REF: ${{ github.event.pull_request.base.ref }}
        run: ./scripts/ci/job-pr-shape.sh --base "${WORKCELL_PR_BASE_REF}"
      - name: Skip outside pull requests
        if: ${{ github.event_name != 'pull_request' }}
        run: echo "PR shape gate applies only to pull requests."
`

	if err := validateCIWorkflowPRShapeFlow(workflow); err != nil {
		t.Fatalf("validateCIWorkflowPRShapeFlow() error = %v", err)
	}
}

func TestValidateUpstreamRefreshWorkflowRejectsGitHubSidePRPublication(t *testing.T) {
	t.Parallel()
	workflow := `name: Upstream refresh

on:
  workflow_dispatch:

jobs:
  refresh:
    environment:
      name: upstream-refresh
    permissions:
      contents: read
      issues: write
      pull-requests: read
    steps:
      - uses: actions/checkout@de0fac2e4500dabe0009e67214ff5f5447ce83dd # v6.0.2
        with:
          fetch-depth: 0
          persist-credentials: false
      - run: ./scripts/update-upstream-pins.sh --apply
      - run: |
          ./scripts/update-upstream-pins.sh --check
          ./scripts/check-pinned-inputs.sh
      - run: |
          jq -n '{version:1}' > metadata.json
      - uses: actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a # v7.0.1
        with:
          name: upstream-refresh-candidate
          path: metadata.json
      - env:
          GH_TOKEN: ${{ github.token }}
        run: |
          gh issue edit 1 --body "candidate"
          gh pr create --draft
`

	err := validateUpstreamRefreshWorkflow(workflow)
	if err == nil {
		t.Fatal("validateUpstreamRefreshWorkflow() unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), `gh pr create`) {
		t.Fatalf("validateUpstreamRefreshWorkflow() error = %v, want GitHub-side PR publication rejection", err)
	}
}

func TestValidateUpstreamRefreshWorkflowAcceptsCanonicalFlow(t *testing.T) {
	t.Parallel()
	workflow := `name: Upstream refresh

on:
  workflow_dispatch:

jobs:
  refresh:
    environment:
      name: upstream-refresh
    permissions:
      contents: read
      issues: write
      pull-requests: read
    steps:
      - uses: actions/checkout@de0fac2e4500dabe0009e67214ff5f5447ce83dd # v6.0.2
        with:
          fetch-depth: 0
          persist-credentials: false
      - run: ./scripts/update-upstream-pins.sh --apply
      - run: |
          ./scripts/update-upstream-pins.sh --check
          ./scripts/check-pinned-inputs.sh
      - run: |
          jq -n '{version:1}' > metadata.json
      - uses: actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a # v7.0.1
        with:
          name: upstream-refresh-candidate
          path: metadata.json
      - env:
          GH_TOKEN: ${{ github.token }}
        run: |
          gh issue create --title "Upstream refresh candidate" --body "metadata.json"
`

	if err := validateUpstreamRefreshWorkflow(workflow); err != nil {
		t.Fatalf("validateUpstreamRefreshWorkflow() error = %v", err)
	}
}

func TestValidateUpstreamRefreshWorkflowRejectsHostedSigningInputs(t *testing.T) {
	t.Parallel()
	workflow := `name: Upstream refresh

on:
  workflow_dispatch:

jobs:
  refresh:
    environment:
      name: upstream-refresh
    permissions:
      contents: read
      issues: write
      pull-requests: read
    steps:
      - uses: actions/checkout@de0fac2e4500dabe0009e67214ff5f5447ce83dd # v6.0.2
        with:
          fetch-depth: 0
          persist-credentials: false
      - run: ./scripts/update-upstream-pins.sh --apply
      - run: |
          ./scripts/update-upstream-pins.sh --check
          ./scripts/check-pinned-inputs.sh
      - run: |
          jq -n '{version:1}' > metadata.json
      - uses: actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a # v7.0.1
        with:
          name: upstream-refresh-candidate
          path: metadata.json
      - env:
          WORKCELL_UPSTREAM_REFRESH_GPG_PRIVATE_KEY: ${{ secrets.WORKCELL_UPSTREAM_REFRESH_GPG_PRIVATE_KEY }}
        run: |
          gh issue create --title "Upstream refresh candidate" --body "metadata.json"
`

	err := validateUpstreamRefreshWorkflow(workflow)
	if err == nil {
		t.Fatal("validateUpstreamRefreshWorkflow() unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "WORKCELL_UPSTREAM_REFRESH_GPG_PRIVATE_KEY") {
		t.Fatalf("validateUpstreamRefreshWorkflow() error = %v, want hosted signing input rejection", err)
	}
}

func TestValidateUpstreamRefreshWorkflowRejectsMissingEnvironmentBinding(t *testing.T) {
	t.Parallel()
	workflow := `name: Upstream refresh

on:
  workflow_dispatch:

jobs:
  refresh:
    permissions:
      contents: read
      issues: write
      pull-requests: read
    steps:
      - uses: actions/checkout@de0fac2e4500dabe0009e67214ff5f5447ce83dd # v6.0.2
        with:
          fetch-depth: 0
          persist-credentials: false
      - run: ./scripts/update-upstream-pins.sh --apply
      - run: |
          ./scripts/update-upstream-pins.sh --check
          ./scripts/check-pinned-inputs.sh
      - run: |
          jq -n '{version:1}' > metadata.json
      - uses: actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a # v7.0.1
        with:
          name: upstream-refresh-candidate
          path: metadata.json
      - env:
          GH_TOKEN: ${{ github.token }}
        run: |
          gh issue create --title "Upstream refresh candidate" --body "metadata.json"
`

	err := validateUpstreamRefreshWorkflow(workflow)
	if err == nil {
		t.Fatal("validateUpstreamRefreshWorkflow() unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), `environment:\n      name: upstream-refresh`) {
		t.Fatalf("validateUpstreamRefreshWorkflow() error = %v, want upstream-refresh environment binding rejection", err)
	}
}

func TestValidateReleaseWorkflowGitHubAttestationFlowRejectsMissingSupportGuard(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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

func TestValidateCanonicalHostedControlsWorkflowEnvironmentsRejectsMissingHostedControlsAudit(t *testing.T) {
	t.Parallel()
	policy := map[string]any{
		"workflow_environment": map[string]any{
			"upstream-refresh": map[string]any{
				"allow_admin_bypass":  false,
				"deployment_branches": []any{"main"},
			},
		},
	}

	err := validateCanonicalHostedControlsWorkflowEnvironments(policy, "policy/github-hosted-controls.toml")
	if err == nil {
		t.Fatal("validateCanonicalHostedControlsWorkflowEnvironments() unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "workflow_environment.hosted-controls-audit") {
		t.Fatalf("validateCanonicalHostedControlsWorkflowEnvironments() error = %v, want hosted-controls-audit rejection", err)
	}
}

func TestValidateCanonicalHostedControlsWorkflowEnvironmentsRejectsUnexpectedUpstreamRefreshSecrets(t *testing.T) {
	t.Parallel()
	policy := map[string]any{
		"workflow_environment": map[string]any{
			"hosted-controls-audit": map[string]any{
				"required_secrets":    []any{"WORKCELL_HOSTED_CONTROLS_TOKEN"},
				"allow_admin_bypass":  false,
				"deployment_branches": []any{"main"},
			},
			"upstream-refresh": map[string]any{
				"required_secrets":    []any{"WORKCELL_UPSTREAM_REFRESH_GPG_PRIVATE_KEY"},
				"allow_admin_bypass":  false,
				"deployment_branches": []any{"main"},
			},
		},
	}

	err := validateCanonicalHostedControlsWorkflowEnvironments(policy, "policy/github-hosted-controls.toml")
	if err == nil {
		t.Fatal("validateCanonicalHostedControlsWorkflowEnvironments() unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "must not declare secrets") {
		t.Fatalf("validateCanonicalHostedControlsWorkflowEnvironments() error = %v, want upstream-refresh secret rejection", err)
	}
}

func TestValidateCanonicalHostedControlsWorkflowEnvironmentsAcceptsCanonicalValues(t *testing.T) {
	t.Parallel()
	policy := map[string]any{
		"workflow_environment": map[string]any{
			"hosted-controls-audit": map[string]any{
				"required_secrets":    []any{"WORKCELL_HOSTED_CONTROLS_TOKEN"},
				"allow_admin_bypass":  false,
				"deployment_branches": []any{"main"},
			},
			"upstream-refresh": map[string]any{
				"allow_admin_bypass":  false,
				"deployment_branches": []any{"main"},
			},
		},
	}

	if err := validateCanonicalHostedControlsWorkflowEnvironments(policy, "policy/github-hosted-controls.toml"); err != nil {
		t.Fatalf("validateCanonicalHostedControlsWorkflowEnvironments() error = %v", err)
	}
}

func TestHostedControlsEnvironmentArtifactNameEscapesSlashes(t *testing.T) {
	t.Parallel()

	if got := hostedControlsEnvironmentArtifactName("prod/us west"); got != "prod%2Fus%20west" {
		t.Fatalf("hostedControlsEnvironmentArtifactName() = %q, want %q", got, "prod%2Fus%20west")
	}
}

func TestHostedControlsEnvironmentArtifactNameEscapesReservedCharacters(t *testing.T) {
	t.Parallel()

	if got := hostedControlsEnvironmentArtifactName("prod+east:blue&green=1"); got != "prod%2Beast%3Ablue%26green%3D1" {
		t.Fatalf("hostedControlsEnvironmentArtifactName() = %q, want %q", got, "prod%2Beast%3Ablue%26green%3D1")
	}
}
