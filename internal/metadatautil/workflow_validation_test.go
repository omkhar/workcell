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
      - uses: actions/attest@59d89421af93a897026c735860bf21b6eb4f7b26 # v4.1.0
        if: env.ENABLE_GITHUB_ATTESTATIONS == 'true'
      - uses: actions/attest@59d89421af93a897026c735860bf21b6eb4f7b26 # v4.1.0
        if: env.ENABLE_GITHUB_ATTESTATIONS == 'true' && env.ENABLE_GITHUB_ATTESTATIONS_SUPPORTED == 'true'
      - uses: actions/attest@59d89421af93a897026c735860bf21b6eb4f7b26 # v4.1.0
        if: env.ENABLE_GITHUB_ATTESTATIONS == 'true' && env.ENABLE_GITHUB_ATTESTATIONS_SUPPORTED == 'true'
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
      - uses: actions/attest@59d89421af93a897026c735860bf21b6eb4f7b26 # v4.1.0
        if: env.ENABLE_GITHUB_ATTESTATIONS == 'true' && env.ENABLE_GITHUB_ATTESTATIONS_SUPPORTED == 'true'
      - uses: actions/attest@59d89421af93a897026c735860bf21b6eb4f7b26 # v4.1.0
        if: env.ENABLE_GITHUB_ATTESTATIONS == 'true' && env.ENABLE_GITHUB_ATTESTATIONS_SUPPORTED == 'true'
      - uses: actions/attest@59d89421af93a897026c735860bf21b6eb4f7b26 # v4.1.0
        if: env.ENABLE_GITHUB_ATTESTATIONS == 'true' && env.ENABLE_GITHUB_ATTESTATIONS_SUPPORTED == 'true'
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
