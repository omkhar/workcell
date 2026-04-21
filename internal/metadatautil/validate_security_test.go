// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func metadatautilRepoRoot(tb testing.TB) string {
	tb.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		tb.Fatal("unable to determine repo root")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func copyFixtureFile(tb testing.TB, srcRoot, dstRoot, relativePath string) {
	tb.Helper()
	sourcePath := filepath.Join(srcRoot, filepath.FromSlash(relativePath))
	data, err := os.ReadFile(sourcePath)
	if err != nil {
		tb.Fatal(err)
	}
	targetPath := filepath.Join(dstRoot, filepath.FromSlash(relativePath))
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		tb.Fatal(err)
	}
	if err := os.WriteFile(targetPath, data, 0o644); err != nil {
		tb.Fatal(err)
	}
}

func writePinnedInputsFixture(tb testing.TB) PinnedInputsConfig {
	tb.Helper()

	srcRoot := metadatautilRepoRoot(tb)
	dstRoot := tb.TempDir()
	for _, relativePath := range []string{
		"go.mod",
		".github/CODEOWNERS",
		".github/workflows/ci.yml",
		".github/workflows/release.yml",
		".github/workflows/hosted-controls.yml",
		".github/workflows/security.yml",
		".github/workflows/pin-hygiene.yml",
		".github/workflows/upstream-refresh.yml",
		"adapters/codex/requirements.toml",
		"adapters/codex/mcp/config.toml",
		"policy/github-hosted-controls.toml",
		"policy/provider-bumps.toml",
		"runtime/container/Dockerfile",
		"runtime/container/providers/package.json",
		"runtime/container/providers/package-lock.json",
		"runtime/container/rust/Cargo.toml",
		"runtime/container/rust/rust-toolchain.toml",
		"scripts/verify-github-hosted-controls.sh",
		"tools/validator/Dockerfile",
	} {
		copyFixtureFile(tb, srcRoot, dstRoot, relativePath)
	}

	return PinnedInputsConfig{
		RuntimeDockerfilePath:    filepath.Join(dstRoot, "runtime", "container", "Dockerfile"),
		ValidatorDockerfilePath:  filepath.Join(dstRoot, "tools", "validator", "Dockerfile"),
		ProvidersPackageJSONPath: filepath.Join(dstRoot, "runtime", "container", "providers", "package.json"),
		ProvidersPackageLockPath: filepath.Join(dstRoot, "runtime", "container", "providers", "package-lock.json"),
		WorkflowsDir:             filepath.Join(dstRoot, ".github", "workflows"),
		CIWorkflowPath:           filepath.Join(dstRoot, ".github", "workflows", "ci.yml"),
		ReleaseWorkflowPath:      filepath.Join(dstRoot, ".github", "workflows", "release.yml"),
		PinHygieneWorkflowPath:   filepath.Join(dstRoot, ".github", "workflows", "pin-hygiene.yml"),
		CodeownersPath:           filepath.Join(dstRoot, ".github", "CODEOWNERS"),
		CodexRequirementsPath:    filepath.Join(dstRoot, "adapters", "codex", "requirements.toml"),
		CodexMCPConfigPath:       filepath.Join(dstRoot, "adapters", "codex", "mcp", "config.toml"),
		HostedControlsPolicyPath: filepath.Join(dstRoot, "policy", "github-hosted-controls.toml"),
		HostedControlsScriptPath: filepath.Join(dstRoot, "scripts", "verify-github-hosted-controls.sh"),
		ProviderBumpPolicyPath:   filepath.Join(dstRoot, "policy", "provider-bumps.toml"),
		MaxDebianSnapshotAgeDays: 3650,
	}
}

func rewriteFile(tb testing.TB, path string, rewrite func(string) string) {
	tb.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		tb.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(rewrite(string(content))), 0o644); err != nil {
		tb.Fatal(err)
	}
}

func writeHostedControlsFixture(tb testing.TB, branchMode, releaseMode string, directCollaborators []map[string]any) (string, string) {
	tb.Helper()

	root := tb.TempDir()
	policyPath := filepath.Join(root, "policy.toml")
	const (
		upstreamRefreshName        = "Omkhar Arasaratnam"
		upstreamRefreshEmail       = "omkhar@gmail.com"
		upstreamRefreshFingerprint = "90554248C4F7CC086BB745D0DA5A8E9F536C42FD"
	)
	policy := strings.Join([]string{
		"[branch_integrity]",
		"require_signed_commits = true",
		"block_force_pushes = true",
		"block_deletions = true",
		"",
		"[branch_review]",
		`mode = "` + branchMode + `"`,
		"",
		"[release_environment]",
		`mode = "` + releaseMode + `"`,
		"",
		"[required_status_checks]",
		`contexts = ["Allowed PR base", "Validate repository"]`,
		"",
		"[repository_variables]",
		`WORKCELL_ENABLE_GITHUB_ATTESTATIONS = "true"`,
		`WORKCELL_ENABLE_PRIVATE_GITHUB_ATTESTATIONS = "false"`,
		"",
		"[workflow_environment.hosted-controls-audit]",
		`required_secrets = ["WORKCELL_HOSTED_CONTROLS_TOKEN"]`,
		"",
		"[workflow_environment.upstream-refresh]",
		`required_secrets = ["WORKCELL_UPSTREAM_REFRESH_GPG_PRIVATE_KEY"]`,
		"",
		"[workflow_environment.upstream-refresh.variables]",
		`WORKCELL_UPSTREAM_REFRESH_GIT_NAME = "` + upstreamRefreshName + `"`,
		`WORKCELL_UPSTREAM_REFRESH_GIT_EMAIL = "` + upstreamRefreshEmail + `"`,
		`WORKCELL_UPSTREAM_REFRESH_GPG_FINGERPRINT = "` + upstreamRefreshFingerprint + `"`,
		"",
	}, "\n")
	if err := os.WriteFile(policyPath, []byte(policy), 0o644); err != nil {
		tb.Fatal(err)
	}

	repoJSON := map[string]any{
		"private": false,
		"owner": map[string]any{
			"login": "omkhar",
			"type":  "User",
		},
	}
	actionsPermissions := map[string]any{
		"enabled":              true,
		"sha_pinning_required": true,
	}
	actionsVariables := map[string]any{
		"variables": []map[string]any{
			{
				"name":  "WORKCELL_ENABLE_GITHUB_ATTESTATIONS",
				"value": "true",
			},
			{
				"name":  "WORKCELL_ENABLE_PRIVATE_GITHUB_ATTESTATIONS",
				"value": "false",
			},
		},
	}
	environmentsIndex := map[string]any{
		"environments": []map[string]any{
			{"name": "hosted-controls-audit"},
			{"name": "release"},
			{"name": "upstream-refresh"},
		},
	}
	branchReviewParameters := map[string]any{
		"required_approving_review_count":   float64(0),
		"require_code_owner_review":         false,
		"require_last_push_approval":        false,
		"required_review_thread_resolution": true,
	}
	if branchMode == "review-gated" {
		branchReviewParameters = map[string]any{
			"required_approving_review_count":   float64(1),
			"require_code_owner_review":         true,
			"required_review_thread_resolution": true,
		}
	}
	rulesets := []map[string]any{
		{
			"name":        "default-branch-integrity",
			"enforcement": "active",
			"target":      "branch",
			"conditions": map[string]any{
				"ref_name": map[string]any{
					"include": []any{"~DEFAULT_BRANCH"},
				},
			},
			"rules": []map[string]any{
				{"type": "required_signatures"},
				{"type": "non_fast_forward"},
				{"type": "deletion"},
			},
			"bypass_actors": []any{},
		},
		{
			"name":        "default-branch-review",
			"enforcement": "active",
			"target":      "branch",
			"conditions": map[string]any{
				"ref_name": map[string]any{
					"include": []any{"~DEFAULT_BRANCH"},
				},
			},
			"rules": []map[string]any{
				{
					"type":       "pull_request",
					"parameters": branchReviewParameters,
				},
			},
			"bypass_actors": []map[string]any{
				{
					"actor_type":  "RepositoryRole",
					"bypass_mode": "pull_request",
				},
			},
		},
		{
			"name":        "default-branch-status",
			"enforcement": "active",
			"target":      "branch",
			"conditions": map[string]any{
				"ref_name": map[string]any{
					"include": []any{"~DEFAULT_BRANCH"},
				},
			},
			"rules": []map[string]any{
				{
					"type": "required_status_checks",
					"parameters": map[string]any{
						"strict_required_status_checks_policy": true,
						"required_status_checks": []map[string]any{
							{"context": "Allowed PR base"},
							{"context": "Validate repository"},
						},
					},
				},
			},
			"bypass_actors": []any{},
		},
		{
			"name":        "release-tags",
			"enforcement": "active",
			"target":      "tag",
			"conditions": map[string]any{
				"ref_name": map[string]any{
					"include": []any{"refs/tags/v*"},
				},
			},
			"rules": []map[string]any{
				{"type": "creation"},
				{"type": "update"},
				{"type": "deletion"},
			},
			"bypass_actors": []map[string]any{
				{
					"actor_type":  "RepositoryRole",
					"bypass_mode": "always",
				},
			},
		},
	}

	releaseReviewRule := map[string]any{
		"type": "required_reviewers",
		"reviewers": []map[string]any{
			{"type": "User", "id": float64(1)},
		},
		"prevent_self_review": false,
	}
	if releaseMode == "review-gated" {
		releaseReviewRule["prevent_self_review"] = true
	}
	releaseEnv := map[string]any{
		"name": "release",
		"protection_rules": []map[string]any{
			releaseReviewRule,
			{
				"type":    "admin_bypass",
				"enabled": false,
			},
		},
		"can_admins_bypass": false,
	}
	hostedControlsAuditEnv := map[string]any{
		"name": "hosted-controls-audit",
	}
	hostedControlsAuditVariables := map[string]any{
		"variables": []map[string]any{},
	}
	hostedControlsAuditSecrets := map[string]any{
		"secrets": []map[string]any{
			{"name": "WORKCELL_HOSTED_CONTROLS_TOKEN"},
		},
	}
	upstreamRefreshEnv := map[string]any{
		"name": "upstream-refresh",
	}
	upstreamRefreshVariables := map[string]any{
		"variables": []map[string]any{
			{
				"name":  "WORKCELL_UPSTREAM_REFRESH_GIT_NAME",
				"value": upstreamRefreshName,
			},
			{
				"name":  "WORKCELL_UPSTREAM_REFRESH_GIT_EMAIL",
				"value": upstreamRefreshEmail,
			},
			{
				"name":  "WORKCELL_UPSTREAM_REFRESH_GPG_FINGERPRINT",
				"value": upstreamRefreshFingerprint,
			},
		},
	}
	upstreamRefreshSecrets := map[string]any{
		"secrets": []map[string]any{
			{"name": "WORKCELL_UPSTREAM_REFRESH_GPG_PRIVATE_KEY"},
		},
	}

	for _, fixture := range []struct {
		name  string
		value any
	}{
		{"repo.json", repoJSON},
		{"actions-permissions.json", actionsPermissions},
		{"actions-variables.json", actionsVariables},
		{"environments.json", environmentsIndex},
		{"collaborators-direct.json", directCollaborators},
		{"rulesets.json", rulesets},
		{"environment-hosted-controls-audit.json", hostedControlsAuditEnv},
		{"environment-hosted-controls-audit-variables.json", hostedControlsAuditVariables},
		{"environment-hosted-controls-audit-secrets.json", hostedControlsAuditSecrets},
		{"environment-release.json", releaseEnv},
		{"environment-upstream-refresh.json", upstreamRefreshEnv},
		{"environment-upstream-refresh-variables.json", upstreamRefreshVariables},
		{"environment-upstream-refresh-secrets.json", upstreamRefreshSecrets},
	} {
		if err := writeJSONFile(filepath.Join(root, fixture.name), fixture.value); err != nil {
			tb.Fatal(err)
		}
	}

	return root, policyPath
}

func TestCheckPinnedInputsRejectsUnpinnedRustupVersion(t *testing.T) {
	t.Parallel()

	cfg := writePinnedInputsFixture(t)
	rewriteFile(t, cfg.ValidatorDockerfilePath, func(content string) string {
		return strings.Replace(content, "ARG RUSTUP_VERSION=", "ARG RUSTUP_VERSION=stable-", 1)
	})

	err := CheckPinnedInputs(cfg)
	if err == nil {
		t.Fatal("CheckPinnedInputs() unexpectedly accepted a non-release rustup version")
	}
	if !strings.Contains(err.Error(), "RUSTUP_VERSION") {
		t.Fatalf("CheckPinnedInputs() error = %v, want rustup version rejection", err)
	}
}

func TestCheckPinnedInputsRejectsInvalidRustupDigest(t *testing.T) {
	t.Parallel()

	cfg := writePinnedInputsFixture(t)
	rewriteFile(t, cfg.ValidatorDockerfilePath, func(content string) string {
		content = strings.Replace(content, "ARG RUSTUP_INIT_LINUX_X86_64_SHA256=", "ARG RUSTUP_INIT_LINUX_X86_64_SHA256=deadbeef", 1)
		return strings.Replace(content, "ARG RUSTUP_INIT_LINUX_ARM64_SHA256=", "ARG RUSTUP_INIT_LINUX_ARM64_SHA256=feedface", 1)
	})

	err := CheckPinnedInputs(cfg)
	if err == nil {
		t.Fatal("CheckPinnedInputs() unexpectedly accepted a non-sha256 rustup digest")
	}
	if !strings.Contains(err.Error(), "RUSTUP_INIT_LINUX") {
		t.Fatalf("CheckPinnedInputs() error = %v, want rustup digest rejection", err)
	}
}

func TestCheckPinnedInputsRejectsInvalidValidatorToolDigests(t *testing.T) {
	t.Parallel()

	cfg := writePinnedInputsFixture(t)
	rewriteFile(t, cfg.ValidatorDockerfilePath, func(content string) string {
		content = strings.Replace(content, "ARG GO_LINUX_X86_64_SHA256=", "ARG GO_LINUX_X86_64_SHA256=deadbeef", 1)
		content = strings.Replace(content, "ARG GO_LINUX_ARM64_SHA256=", "ARG GO_LINUX_ARM64_SHA256=feedface", 1)
		content = strings.Replace(content, "ARG HADOLINT_LINUX_X86_64_SHA256=", "ARG HADOLINT_LINUX_X86_64_SHA256=deadbeef", 1)
		return strings.Replace(content, "ARG HADOLINT_LINUX_ARM64_SHA256=", "ARG HADOLINT_LINUX_ARM64_SHA256=feedface", 1)
	})

	err := CheckPinnedInputs(cfg)
	if err == nil {
		t.Fatal("CheckPinnedInputs() unexpectedly accepted a non-sha256 validator tool digest")
	}
	if !strings.Contains(err.Error(), "_SHA256") {
		t.Fatalf("CheckPinnedInputs() error = %v, want validator tool digest rejection", err)
	}
}

func TestVerifyGitHubHostedControlsRejectsExtraPublicCollaboratorsForBranchReview(t *testing.T) {
	t.Parallel()

	tmpDir, policyPath := writeHostedControlsFixture(t, "single-owner-public-pr", "single-owner-public", []map[string]any{
		{
			"login": "omkhar",
			"permissions": map[string]any{
				"admin": true,
			},
		},
		{
			"login": "extra-maintainer",
			"permissions": map[string]any{
				"admin": false,
			},
		},
	})

	err := VerifyGitHubHostedControls(tmpDir, "omkhar/workcell", policyPath)
	if err == nil {
		t.Fatal("VerifyGitHubHostedControls() unexpectedly accepted extra collaborators in single-owner-public-pr mode")
	}
	if !strings.Contains(err.Error(), "requires exactly one direct collaborator") {
		t.Fatalf("VerifyGitHubHostedControls() error = %v, want collaborator-count rejection", err)
	}
}

func TestVerifyGitHubHostedControlsRejectsApprovalGatedMode(t *testing.T) {
	t.Parallel()

	tmpDir, policyPath := writeHostedControlsFixture(t, "approval-gated", "review-gated", []map[string]any{
		{
			"login": "omkhar",
			"permissions": map[string]any{
				"admin": true,
			},
		},
		{
			"login": "extra-maintainer",
			"permissions": map[string]any{
				"admin": false,
			},
		},
	})

	err := VerifyGitHubHostedControls(tmpDir, "omkhar/workcell", policyPath)
	if err == nil {
		t.Fatal("VerifyGitHubHostedControls() unexpectedly accepted approval-gated mode")
	}
	if !strings.Contains(err.Error(), "must set branch_review.mode to 'review-gated', 'single-owner-public-pr', or 'single-owner-private-pr'") {
		t.Fatalf("VerifyGitHubHostedControls() error = %v, want unsupported-mode rejection", err)
	}
}

func TestVerifyGitHubHostedControlsRejectsReviewGatedRulesetWithoutCodeOwnerReview(t *testing.T) {
	t.Parallel()

	tmpDir, policyPath := writeHostedControlsFixture(t, "review-gated", "review-gated", []map[string]any{
		{
			"login": "omkhar",
			"permissions": map[string]any{
				"admin": true,
			},
		},
	})

	rewriteFile(t, filepath.Join(tmpDir, "rulesets.json"), func(content string) string {
		content = strings.Replace(content, `"require_code_owner_review": true`, `"require_code_owner_review": false`, 1)
		return strings.Replace(content, `"require_code_owner_review":true`, `"require_code_owner_review":false`, 1)
	})

	err := VerifyGitHubHostedControls(tmpDir, "omkhar/workcell", policyPath)
	if err == nil {
		t.Fatal("VerifyGitHubHostedControls() unexpectedly accepted review-gated rules without code owner review")
	}
	if !strings.Contains(err.Error(), "must require code owner review") {
		t.Fatalf("VerifyGitHubHostedControls() error = %v, want code-owner rejection", err)
	}
}

func TestVerifyGitHubHostedControlsRejectsExtraPublicCollaboratorsForReleaseEnv(t *testing.T) {
	t.Parallel()

	tmpDir, policyPath := writeHostedControlsFixture(t, "review-gated", "single-owner-public", []map[string]any{
		{
			"login": "omkhar",
			"permissions": map[string]any{
				"admin": true,
			},
		},
		{
			"login": "extra-maintainer",
			"permissions": map[string]any{
				"admin": false,
			},
		},
	})

	err := VerifyGitHubHostedControls(tmpDir, "omkhar/workcell", policyPath)
	if err == nil {
		t.Fatal("VerifyGitHubHostedControls() unexpectedly accepted extra collaborators in single-owner-public release mode")
	}
	if !strings.Contains(err.Error(), "requires exactly one direct collaborator") {
		t.Fatalf("VerifyGitHubHostedControls() error = %v, want collaborator-count rejection", err)
	}
}

func TestVerifyGitHubHostedControlsRejectsNonOwnerPublicCollaboratorForBranchReview(t *testing.T) {
	t.Parallel()

	tmpDir, policyPath := writeHostedControlsFixture(t, "single-owner-public-pr", "single-owner-public", []map[string]any{
		{
			"login": "not-the-owner",
			"permissions": map[string]any{
				"admin": true,
			},
		},
	})

	err := VerifyGitHubHostedControls(tmpDir, "omkhar/workcell", policyPath)
	if err == nil {
		t.Fatal("VerifyGitHubHostedControls() unexpectedly accepted a non-owner collaborator in single-owner-public-pr mode")
	}
	if !strings.Contains(err.Error(), "requires the owner to be the only direct collaborator") {
		t.Fatalf("VerifyGitHubHostedControls() error = %v, want owner-only collaborator rejection", err)
	}
}

func TestVerifyGitHubHostedControlsRejectsMissingUpstreamRefreshSecret(t *testing.T) {
	t.Parallel()

	tmpDir, policyPath := writeHostedControlsFixture(t, "review-gated", "review-gated", []map[string]any{
		{
			"login": "omkhar",
			"permissions": map[string]any{
				"admin": true,
			},
		},
	})

	rewriteFile(t, filepath.Join(tmpDir, "environment-upstream-refresh-secrets.json"), func(content string) string {
		return strings.Replace(content, `"WORKCELL_UPSTREAM_REFRESH_GPG_PRIVATE_KEY"`, `"WORKCELL_UPSTREAM_REFRESH_GPG_PRIVATE_KEY_REMOVED"`, 1)
	})

	err := VerifyGitHubHostedControls(tmpDir, "omkhar/workcell", policyPath)
	if err == nil {
		t.Fatal("VerifyGitHubHostedControls() unexpectedly accepted a missing upstream-refresh private-key secret")
	}
	if !strings.Contains(err.Error(), "workflow environment secrets missing on omkhar/workcell/upstream-refresh") {
		t.Fatalf("VerifyGitHubHostedControls() error = %v, want upstream-refresh secret rejection", err)
	}
}

func TestVerifyGitHubHostedControlsRejectsWrongUpstreamRefreshFingerprint(t *testing.T) {
	t.Parallel()

	tmpDir, policyPath := writeHostedControlsFixture(t, "review-gated", "review-gated", []map[string]any{
		{
			"login": "omkhar",
			"permissions": map[string]any{
				"admin": true,
			},
		},
	})

	rewriteFile(t, filepath.Join(tmpDir, "environment-upstream-refresh-variables.json"), func(content string) string {
		return strings.Replace(content, "90554248C4F7CC086BB745D0DA5A8E9F536C42FD", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", 1)
	})

	err := VerifyGitHubHostedControls(tmpDir, "omkhar/workcell", policyPath)
	if err == nil {
		t.Fatal("VerifyGitHubHostedControls() unexpectedly accepted a mismatched upstream-refresh fingerprint")
	}
	if !strings.Contains(err.Error(), "workflow environment variables on omkhar/workcell/upstream-refresh do not match policy") {
		t.Fatalf("VerifyGitHubHostedControls() error = %v, want upstream-refresh fingerprint rejection", err)
	}
}
