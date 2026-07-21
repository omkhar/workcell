// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/omkhar/workcell/internal/metadatautil"
)

func writeJSONFile(path string, value any) error {
	content, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	content = append(content, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, content, 0o644)
}

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

func writePinnedInputsFixture(tb testing.TB) metadatautil.PinnedInputsConfig {
	tb.Helper()

	srcRoot := metadatautilRepoRoot(tb)
	dstRoot := tb.TempDir()
	for _, relativePath := range []string{
		"go.mod",
		".github/CODEOWNERS",
		".github/workflows/ci.yml",
		".github/workflows/docs.yml",
		".github/workflows/release.yml",
		".github/workflows/hosted-controls.yml",
		".github/workflows/security.yml",
		".github/workflows/pin-hygiene.yml",
		".github/workflows/upstream-refresh.yml",
		"adapters/codex/requirements.toml",
		"adapters/codex/mcp/config.toml",
		"policy/github-hosted-controls.toml",
		"policy/provider-bumps.toml",
		"policy/allowed-actions.toml",
		"policy/tool-pins.toml",
		"runtime/container/Dockerfile",
		"runtime/container/debian-bootstrap.env",
		"runtime/container/providers/package.json",
		"runtime/container/providers/package-lock.json",
		"runtime/container/rust/Cargo.toml",
		"runtime/container/rust/rust-toolchain.toml",
		"scripts/ci/build-validator-image.sh",
		"scripts/ci/job-pin-hygiene.sh",
		"scripts/ci/job-validate.sh",
		"scripts/install-dev-tools.sh",
		"scripts/verify-github-hosted-controls.sh",
		"tools/markdownlint/package.json",
		"tools/markdownlint/package-lock.json",
		"tools/validator/Dockerfile",
	} {
		copyFixtureFile(tb, srcRoot, dstRoot, relativePath)
	}

	return metadatautil.PinnedInputsConfig{
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
		MaxDebianSnapshotAgeDays: 60,
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

func replaceFirstMatch(tb testing.TB, content string, pattern *regexp.Regexp, replacement string) string {
	tb.Helper()
	match := pattern.FindStringIndex(content)
	if match == nil {
		tb.Fatalf("fixture does not contain pattern %q", pattern.String())
	}
	return content[:match[0]] + replacement + content[match[1]:]
}

func replaceAllMatches(tb testing.TB, content string, pattern *regexp.Regexp, replacement string) string {
	tb.Helper()
	if !pattern.MatchString(content) {
		tb.Fatalf("fixture does not contain pattern %q", pattern.String())
	}
	return pattern.ReplaceAllString(content, replacement)
}

func rewritePinnedInputsFixtureFile(tb testing.TB, relativePath string, rewrite func(string) string) metadatautil.PinnedInputsConfig {
	tb.Helper()
	cfg := writePinnedInputsFixture(tb)
	fixtureRoot := filepath.Join(filepath.Dir(cfg.RuntimeDockerfilePath), "..", "..")
	rewriteFile(tb, filepath.Join(fixtureRoot, filepath.FromSlash(relativePath)), rewrite)
	return cfg
}

func rewriteInstallDevToolsFixture(tb testing.TB, rewrite func(string) string) metadatautil.PinnedInputsConfig {
	tb.Helper()
	return rewritePinnedInputsFixtureFile(tb, "scripts/install-dev-tools.sh", rewrite)
}

func requirePinnedInputsErrorContains(tb testing.TB, cfg metadatautil.PinnedInputsConfig, want string) {
	tb.Helper()
	err := metadatautil.CheckPinnedInputs(cfg)
	if err == nil {
		tb.Fatalf("metadatautil.CheckPinnedInputs() unexpectedly accepted fixture; want error containing %q", want)
	}
	if !strings.Contains(err.Error(), want) {
		tb.Fatalf("metadatautil.CheckPinnedInputs() error = %v, want %q", err, want)
	}
}

func TestCheckPinnedInputsRejectsCommentedDebianBootstrapGuard(t *testing.T) {
	cfg := rewritePinnedInputsFixtureFile(t, "runtime/container/Dockerfile", func(content string) string {
		return strings.Replace(content, `  && [[ "${#debian_bootstrap_pins[@]}" -eq 7 ]]`, `  # && [[ "${#debian_bootstrap_pins[@]}" -eq 7 ]]`, 1)
	})
	requirePinnedInputsErrorContains(t, cfg, "must use reviewed Debian bootstrap pin")
}

func writeHostedControlsFixture(tb testing.TB, branchMode, releaseMode string, directCollaborators []map[string]any) (string, string) {
	tb.Helper()

	root := tb.TempDir()
	policyPath := filepath.Join(root, "policy.toml")
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
		"[actions_policy]",
		"allow_only_pinned_verified_or_explicitly_trusted_actions = true",
		`default_workflow_token_permissions = "read"`,
		"",
		"[release_assets]",
		"immutable_github_releases = true",
		"",
		"[repository_variables]",
		`WORKCELL_RELEASE_NO_ATTEST = "false"`,
		`WORKCELL_ENABLE_PRIVATE_GITHUB_ATTESTATIONS = "false"`,
		"",
		"[workflow_environment.hosted-controls-audit]",
		`required_secrets = ["WORKCELL_HOSTED_CONTROLS_TOKEN"]`,
		"allow_admin_bypass = false",
		`deployment_branches = ["main"]`,
		`deployment_tags = ["v*"]`,
		"",
		"[workflow_environment.upstream-refresh]",
		"allow_admin_bypass = false",
		`deployment_branches = ["main"]`,
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
		"allowed_actions":      "selected",
		"sha_pinning_required": true,
	}
	selectedActions := map[string]any{
		"github_owned_allowed": true,
		"verified_allowed":     true,
		"patterns_allowed":     []any{},
	}
	workflowPermissions := map[string]any{
		"default_workflow_permissions":     "read",
		"can_approve_pull_request_reviews": false,
	}
	immutableReleases := map[string]any{
		"enabled":           true,
		"enforced_by_owner": false,
	}
	actionsVariables := map[string]any{
		"variables": []map[string]any{
			{
				"name":  "WORKCELL_RELEASE_NO_ATTEST",
				"value": "false",
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
		"deployment_branch_policy": map[string]any{
			"custom_branch_policies": true,
			"protected_branches":     false,
		},
		"can_admins_bypass": false,
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
		"deployment_branch_policy": map[string]any{
			"custom_branch_policies": true,
			"protected_branches":     false,
		},
		"can_admins_bypass": false,
	}
	upstreamRefreshVariables := map[string]any{
		"variables": []map[string]any{},
	}
	upstreamRefreshSecrets := map[string]any{
		"secrets": []map[string]any{},
	}
	hostedControlsAuditDeploymentBranches := map[string]any{
		"branch_policies": []map[string]any{
			{"name": "main", "type": "branch"},
			{"name": "v*", "type": "tag"},
		},
	}
	upstreamRefreshDeploymentBranches := map[string]any{
		"branch_policies": []map[string]any{
			{"name": "main", "type": "branch"},
		},
	}

	for _, fixture := range []struct {
		name  string
		value any
	}{
		{"repo.json", repoJSON},
		{"actions-permissions.json", actionsPermissions},
		{"actions-selected-actions.json", selectedActions},
		{"actions-workflow-permissions.json", workflowPermissions},
		{"immutable-releases.json", immutableReleases},
		{"actions-variables.json", actionsVariables},
		{"environments.json", environmentsIndex},
		{"collaborators-direct.json", directCollaborators},
		{"rulesets.json", rulesets},
		{"environment-hosted-controls-audit.json", hostedControlsAuditEnv},
		{"environment-hosted-controls-audit-deployment-branch-policies.json", hostedControlsAuditDeploymentBranches},
		{"environment-hosted-controls-audit-variables.json", hostedControlsAuditVariables},
		{"environment-hosted-controls-audit-secrets.json", hostedControlsAuditSecrets},
		{"environment-release.json", releaseEnv},
		{"environment-upstream-refresh.json", upstreamRefreshEnv},
		{"environment-upstream-refresh-deployment-branch-policies.json", upstreamRefreshDeploymentBranches},
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

	err := metadatautil.CheckPinnedInputs(cfg)
	if err == nil {
		t.Fatal("metadatautil.CheckPinnedInputs() unexpectedly accepted a non-release rustup version")
	}
	if !strings.Contains(err.Error(), "RUSTUP_VERSION") {
		t.Fatalf("metadatautil.CheckPinnedInputs() error = %v, want rustup version rejection", err)
	}
}

func TestCheckPinnedInputsRejectsInvalidRustupDigest(t *testing.T) {
	t.Parallel()

	cfg := writePinnedInputsFixture(t)
	rewriteFile(t, cfg.ValidatorDockerfilePath, func(content string) string {
		content = strings.Replace(content, "ARG RUSTUP_INIT_LINUX_X86_64_SHA256=", "ARG RUSTUP_INIT_LINUX_X86_64_SHA256=deadbeef", 1)
		return strings.Replace(content, "ARG RUSTUP_INIT_LINUX_ARM64_SHA256=", "ARG RUSTUP_INIT_LINUX_ARM64_SHA256=feedface", 1)
	})

	err := metadatautil.CheckPinnedInputs(cfg)
	if err == nil {
		t.Fatal("metadatautil.CheckPinnedInputs() unexpectedly accepted a non-sha256 rustup digest")
	}
	if !strings.Contains(err.Error(), "RUSTUP_INIT_LINUX") {
		t.Fatalf("metadatautil.CheckPinnedInputs() error = %v, want rustup digest rejection", err)
	}
}

func TestCheckPinnedInputsRejectsDocsBuildxVersionDrift(t *testing.T) {
	t.Parallel()

	cfg := writePinnedInputsFixture(t)
	rewriteFile(t, filepath.Join(cfg.WorkflowsDir, "docs.yml"), func(content string) string {
		return strings.Replace(content, "  WORKCELL_BUILDX_VERSION: v", "  WORKCELL_BUILDX_VERSION: v9.", 1)
	})

	err := metadatautil.CheckPinnedInputs(cfg)
	if err == nil {
		t.Fatal("metadatautil.CheckPinnedInputs() unexpectedly accepted a drifted buildx version in docs.yml")
	}
	if !strings.Contains(err.Error(), "WORKCELL_BUILDX_VERSION") || !strings.Contains(err.Error(), "docs.yml") {
		t.Fatalf("metadatautil.CheckPinnedInputs() error = %v, want docs.yml buildx drift rejection", err)
	}
}

func TestCheckPinnedInputsRejectsOffAllowlistAction(t *testing.T) {
	t.Parallel()

	cfg := writePinnedInputsFixture(t)
	// Use a QUOTED step key (`- "uses":`), which GitHub treats as the `uses` key
	// but a raw-line scan would miss. Parsing the YAML must still catch it.
	// SHA-shaped so it passes the pin check and reaches the allowlist check.
	rewriteFile(t, filepath.Join(cfg.WorkflowsDir, "ci.yml"), func(content string) string {
		return strings.Replace(content,
			"- uses: actions/checkout@9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0",
			`- "uses": evilorg/evil-action@0000000000000000000000000000000000000000`,
			1)
	})

	err := metadatautil.CheckPinnedInputs(cfg)
	if err == nil {
		t.Fatal("metadatautil.CheckPinnedInputs() unexpectedly accepted a dash-less off-allowlist action")
	}
	if !strings.Contains(err.Error(), "allowlist") || !strings.Contains(err.Error(), "evilorg/evil-action") {
		t.Fatalf("metadatautil.CheckPinnedInputs() error = %v, want off-allowlist rejection", err)
	}
}

func TestCheckPinnedInputsRejectsToolPinPolicyDrift(t *testing.T) {
	t.Parallel()

	// Cover a plain version pin, an image pin (special chars), and a
	// regex-extracted pin. The mutation is derived from whatever value the
	// fixture holds, so a routine pin bump does not require editing this test.
	for _, key := range []string{"cosign", "buildkit", "zizmor_sha256"} {
		key := key
		t.Run(key, func(t *testing.T) {
			t.Parallel()
			cfg := writePinnedInputsFixture(t)
			toolPinsPath := filepath.Join(filepath.Dir(cfg.ProviderBumpPolicyPath), "tool-pins.toml")
			pattern := regexp.MustCompile(`(?m)^(` + regexp.QuoteMeta(key) + ` = )"[^"]*"`)
			rewriteFile(t, toolPinsPath, func(content string) string {
				updated := pattern.ReplaceAllString(content, `${1}"policy-drift-sentinel"`)
				if updated == content {
					t.Fatalf("fixture policy/tool-pins.toml has no %q pin to mutate", key)
				}
				return updated
			})
			err := metadatautil.CheckPinnedInputs(cfg)
			if err == nil || !strings.Contains(err.Error(), "does not match policy/tool-pins.toml") {
				t.Fatalf("metadatautil.CheckPinnedInputs() error = %v, want tool-pin policy drift rejection", err)
			}
		})
	}
}

func TestCheckPinnedInputsRejectsUnknownToolPinKey(t *testing.T) {
	t.Parallel()

	cfg := writePinnedInputsFixture(t)
	toolPinsPath := filepath.Join(filepath.Dir(cfg.ProviderBumpPolicyPath), "tool-pins.toml")
	rewriteFile(t, toolPinsPath, func(content string) string {
		return strings.Replace(content, "[tool_pins]", "[tool_pins]\nbogus = \"x\"", 1)
	})
	err := metadatautil.CheckPinnedInputs(cfg)
	if err == nil || !strings.Contains(err.Error(), "unknown key") {
		t.Fatalf("metadatautil.CheckPinnedInputs() error = %v, want unknown-key rejection", err)
	}
}

func TestCheckPinnedInputsRejectsUnsupportedUsesForm(t *testing.T) {
	t.Parallel()

	cfg := writePinnedInputsFixture(t)
	rewriteFile(t, filepath.Join(cfg.WorkflowsDir, "ci.yml"), func(content string) string {
		return strings.Replace(content, "- uses: actions/checkout@9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0", "- uses: docker://ghcr.io/evil/image:latest", 1)
	})

	err := metadatautil.CheckPinnedInputs(cfg)
	if err == nil || !strings.Contains(err.Error(), "unsupported uses:") {
		t.Fatalf("metadatautil.CheckPinnedInputs() error = %v, want unsupported-uses rejection", err)
	}
}

func TestCheckPinnedInputsRejectsDocsBuildkitImageDrift(t *testing.T) {
	t.Parallel()

	cfg := writePinnedInputsFixture(t)
	rewriteFile(t, filepath.Join(cfg.WorkflowsDir, "docs.yml"), func(content string) string {
		return strings.Replace(content, "  WORKCELL_BUILDKIT_IMAGE: moby/buildkit:buildx-stable-1@sha256:", "  WORKCELL_BUILDKIT_IMAGE: moby/buildkit:buildx-stable-1@sha256:00000000", 1)
	})

	err := metadatautil.CheckPinnedInputs(cfg)
	if err == nil {
		t.Fatal("metadatautil.CheckPinnedInputs() unexpectedly accepted a drifted BuildKit image in docs.yml")
	}
	if !strings.Contains(err.Error(), "WORKCELL_BUILDKIT_IMAGE") || !strings.Contains(err.Error(), "docs.yml") {
		t.Fatalf("metadatautil.CheckPinnedInputs() error = %v, want docs.yml BuildKit drift rejection", err)
	}
}

func TestCheckPinnedInputsRejectsValidatorImageScriptFallbackDrift(t *testing.T) {
	t.Parallel()

	cfg := writePinnedInputsFixture(t)
	// CheckPinnedInputs derives this path from the repo root implied by
	// RuntimeDockerfilePath; mirror that derivation instead of guessing.
	fixtureRoot := filepath.Clean(filepath.Join(filepath.Dir(cfg.RuntimeDockerfilePath), "..", ".."))
	scriptPath := filepath.Join(fixtureRoot, "scripts", "ci", "build-validator-image.sh")
	rewriteFile(t, scriptPath, func(content string) string {
		return strings.Replace(content, `BUILDKIT_IMAGE="${WORKCELL_BUILDKIT_IMAGE:-moby/buildkit:buildx-stable-1@sha256:`, `BUILDKIT_IMAGE="${WORKCELL_BUILDKIT_IMAGE:-moby/buildkit:buildx-stable-1@sha256:00000000`, 1)
	})

	err := metadatautil.CheckPinnedInputs(cfg)
	if err == nil {
		t.Fatal("metadatautil.CheckPinnedInputs() unexpectedly accepted a drifted BuildKit fallback in build-validator-image.sh")
	}
	if !strings.Contains(err.Error(), "build-validator-image.sh") {
		t.Fatalf("metadatautil.CheckPinnedInputs() error = %v, want validator image fallback drift rejection", err)
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

	err := metadatautil.CheckPinnedInputs(cfg)
	if err == nil {
		t.Fatal("metadatautil.CheckPinnedInputs() unexpectedly accepted a non-sha256 validator tool digest")
	}
	if !strings.Contains(err.Error(), "_SHA256") {
		t.Fatalf("metadatautil.CheckPinnedInputs() error = %v, want validator tool digest rejection", err)
	}
}

func TestCheckPinnedInputsRejectsMarkdownlintPinDrift(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name         string
		relativePath string
		old          string
		new          string
		want         string
	}{
		{
			name:         "package-json",
			relativePath: "tools/markdownlint/package.json",
			old:          `"markdownlint-cli": "0.49.0"`,
			new:          `"markdownlint-cli": "0.48.0"`,
			want:         "markdownlint-cli version must match",
		},
		{
			name:         "package-lock-dependency",
			relativePath: "tools/markdownlint/package-lock.json",
			old:          `"markdownlint-cli": "0.49.0"`,
			new:          `"markdownlint-cli": "0.48.0"`,
			want:         "markdownlint-cli version must match",
		},
		{
			name:         "package-lock-entry",
			relativePath: "tools/markdownlint/package-lock.json",
			old: `"node_modules/markdownlint-cli": {
      "version": "0.49.0"`,
			new: `"node_modules/markdownlint-cli": {
      "version": "0.48.0"`,
			want: "locked markdownlint-cli package version",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg := rewritePinnedInputsFixtureFile(t, tc.relativePath, func(content string) string {
				return strings.Replace(content, tc.old, tc.new, 1)
			})
			requirePinnedInputsErrorContains(t, cfg, tc.want)
		})
	}
}

func TestCheckPinnedInputsRejectsMarkdownlintInstallerNodeFloorDrift(t *testing.T) {
	t.Parallel()

	cfg := rewriteInstallDevToolsFixture(t, func(content string) string {
		return strings.Replace(content, `readonly MARKDOWNLINT_NODE_VERSION_MINIMUM="22.12.0"`, `readonly MARKDOWNLINT_NODE_VERSION_MINIMUM="22.0.0"`, 1)
	})
	requirePinnedInputsErrorContains(t, cfg, "MARKDOWNLINT_NODE_VERSION_MINIMUM")
}

func TestCheckPinnedInputsRejectsMarkdownlintInstallerMissingNodeCheck(t *testing.T) {
	t.Parallel()

	cfg := rewriteInstallDevToolsFixture(t, func(content string) string {
		return strings.Replace(content, "if markdownlint_needs_install; then\n  require_markdownlint_node\n  require_markdownlint_npm\n  echo", "if markdownlint_needs_install; then\n  echo", 1)
	})
	requirePinnedInputsErrorContains(t, cfg, "immediately before installing markdownlint-cli")
}

func TestCheckPinnedInputsRejectsMarkdownlintInstallerMissingNPMInstall(t *testing.T) {
	t.Parallel()

	cfg := rewriteInstallDevToolsFixture(t, func(content string) string {
		return strings.Replace(content, "  npm install -g \"markdownlint-cli@${MARKDOWNLINT_VERSION}\"\n", "", 1)
	})
	requirePinnedInputsErrorContains(t, cfg, "immediately before installing markdownlint-cli")
}

func TestCheckPinnedInputsRejectsMarkdownlintInstallerMissingLinuxEarlyNodeCheck(t *testing.T) {
	t.Parallel()

	cfg := rewriteInstallDevToolsFixture(t, func(content string) string {
		return strings.Replace(content, "if [[ \"${host_os}\" == \"Linux\" ]] && markdownlint_needs_install; then\n  require_markdownlint_node\n  require_markdownlint_npm\nfi\n\n", "", 1)
	})
	requirePinnedInputsErrorContains(t, cfg, "before apt installs")
}

func TestCheckPinnedInputsRejectsMarkdownlintInstallerAptNodeBootstrap(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		old  string
		new  string
	}{
		{
			name: "reordered-append",
			old:  "    Linux) ;;",
			new:  "    Linux)\n      append_unique_apt npm nodejs\n      ;;",
		},
		{
			name: "direct-multiline-apt-install",
			old:  `sudo apt-get update -qq && sudo apt-get install -y "${apt_missing[@]}"`,
			new:  "sudo apt-get update -qq && sudo apt-get install -y \\\n        nodejs=18.19.1 \\\n        npm/stable \\\n        \"${apt_missing[@]}\"",
		},
		{
			name: "qualified-apt-specs",
			old:  "    Linux) ;;",
			new:  "    Linux)\n      apt_missing+=(nodejs=18.19.1 npm/stable)\n      append_unique_apt nodejs:amd64\n      ;;",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg := rewriteInstallDevToolsFixture(t, func(content string) string {
				return strings.Replace(content, tc.old, tc.new, 1)
			})
			requirePinnedInputsErrorContains(t, cfg, "must not add")
		})
	}
}

func TestCheckPinnedInputsRejectsMarkdownlintInstallerMissingNodeInstructions(t *testing.T) {
	t.Parallel()

	cfg := rewriteInstallDevToolsFixture(t, func(content string) string {
		return strings.Replace(content, "Ubuntu 24.04's nodejs/npm apt packages are too old for this markdownlint release.", "Ubuntu apt packages are fine.", 1)
	})
	requirePinnedInputsErrorContains(t, cfg, "manual Node.js upgrade instructions")
}

func TestCheckPinnedInputsRejectsMarkdownlintInstallerMissingNodeHintCall(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		old  string
	}{
		{
			name: "missing-node",
			old:  "no usable node binary was found.\" >&2\n    markdownlint_node_install_hint\n    exit 1",
		},
		{
			name: "too-old-node",
			old:  "found ${version}.\" >&2\n    markdownlint_node_install_hint\n    exit 1",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg := rewriteInstallDevToolsFixture(t, func(content string) string {
				return strings.Replace(content, tc.old, strings.Replace(tc.old, "\n    markdownlint_node_install_hint", "", 1), 1)
			})
			requirePinnedInputsErrorContains(t, cfg, "every markdownlint-cli Node.js floor failure path")
		})
	}
}

func TestCheckPinnedInputsRejectsMarkdownlintInstallerMissingNPMHintCall(t *testing.T) {
	t.Parallel()

	cfg := rewriteInstallDevToolsFixture(t, func(content string) string {
		return strings.Replace(content, "requires npm from a Node.js ${MARKDOWNLINT_NODE_VERSION_MINIMUM} or newer installation.\" >&2\n  markdownlint_node_install_hint\n  exit 1", "requires npm from a Node.js ${MARKDOWNLINT_NODE_VERSION_MINIMUM} or newer installation.\" >&2\n  exit 1", 1)
	})
	requirePinnedInputsErrorContains(t, cfg, "markdownlint-cli npm failure path")
}

func TestCheckPinnedInputsRejectsInvalidZizmorDigest(t *testing.T) {
	t.Parallel()

	cfg := writePinnedInputsFixture(t)
	securityWorkflowPath := filepath.Join(cfg.WorkflowsDir, "security.yml")
	rewriteFile(t, securityWorkflowPath, func(content string) string {
		return replaceFirstMatch(t, content, regexp.MustCompile(`ZIZMOR_SHA256: [0-9a-f]{64}`), "ZIZMOR_SHA256: deadbeef")
	})

	err := metadatautil.CheckPinnedInputs(cfg)
	if err == nil {
		t.Fatal("metadatautil.CheckPinnedInputs() unexpectedly accepted a non-sha256 zizmor digest")
	}
	if !strings.Contains(err.Error(), "security zizmor sha") {
		t.Fatalf("metadatautil.CheckPinnedInputs() error = %v, want zizmor digest rejection", err)
	}
}

func TestCheckPinnedInputsRejectsUnboundedWorkflowDownloadSize(t *testing.T) {
	t.Parallel()

	cfg := writePinnedInputsFixture(t)
	securityWorkflowPath := filepath.Join(cfg.WorkflowsDir, "security.yml")
	rewriteFile(t, securityWorkflowPath, func(content string) string {
		return strings.Replace(content, "            --max-filesize 209715200 \\\n", "", 1)
	})

	err := metadatautil.CheckPinnedInputs(cfg)
	if err == nil {
		t.Fatal("metadatautil.CheckPinnedInputs() unexpectedly accepted an unbounded workflow download")
	}
	if !strings.Contains(err.Error(), "must bound actionlint downloads") {
		t.Fatalf("metadatautil.CheckPinnedInputs() error = %v, want download size cap rejection", err)
	}
}

func TestCheckPinnedInputsRejectsZizmorVersionMismatch(t *testing.T) {
	t.Parallel()

	cfg := writePinnedInputsFixture(t)
	securityWorkflowPath := filepath.Join(cfg.WorkflowsDir, "security.yml")
	rewriteFile(t, securityWorkflowPath, func(content string) string {
		return replaceFirstMatch(t, content, regexp.MustCompile(`ZIZMOR_VERSION: [0-9]+\.[0-9]+\.[0-9]+`), "ZIZMOR_VERSION: 0.0.0")
	})

	err := metadatautil.CheckPinnedInputs(cfg)
	if err == nil {
		t.Fatal("metadatautil.CheckPinnedInputs() unexpectedly accepted mismatched zizmor versions")
	}
	if !strings.Contains(err.Error(), "one reviewed value for ZIZMOR_VERSION") {
		t.Fatalf("metadatautil.CheckPinnedInputs() error = %v, want zizmor version mismatch rejection", err)
	}
}

func TestCheckPinnedInputsRejectsReleaseZizmorVersionMismatch(t *testing.T) {
	t.Parallel()

	cfg := writePinnedInputsFixture(t)
	releaseWorkflowPath := filepath.Join(cfg.WorkflowsDir, "release.yml")
	rewriteFile(t, releaseWorkflowPath, func(content string) string {
		return replaceFirstMatch(t, content, regexp.MustCompile(`ZIZMOR_VERSION: [0-9]+\.[0-9]+\.[0-9]+`), "ZIZMOR_VERSION: 0.0.0")
	})

	err := metadatautil.CheckPinnedInputs(cfg)
	if err == nil {
		t.Fatal("metadatautil.CheckPinnedInputs() unexpectedly accepted a release workflow zizmor version mismatch")
	}
	if !strings.Contains(err.Error(), "ZIZMOR_VERSION must match") {
		t.Fatalf("metadatautil.CheckPinnedInputs() error = %v, want release zizmor version mismatch rejection", err)
	}
}

func TestCheckPinnedInputsRejectsReleaseZizmorDigestMismatch(t *testing.T) {
	t.Parallel()

	cfg := writePinnedInputsFixture(t)
	releaseWorkflowPath := filepath.Join(cfg.WorkflowsDir, "release.yml")
	rewriteFile(t, releaseWorkflowPath, func(content string) string {
		return replaceFirstMatch(t, content, regexp.MustCompile(`ZIZMOR_SHA256: [0-9a-f]{64}`), "ZIZMOR_SHA256: deadbeef")
	})

	err := metadatautil.CheckPinnedInputs(cfg)
	if err == nil {
		t.Fatal("metadatautil.CheckPinnedInputs() unexpectedly accepted a release workflow zizmor digest mismatch")
	}
	if !strings.Contains(err.Error(), "release zizmor sha") {
		t.Fatalf("metadatautil.CheckPinnedInputs() error = %v, want release zizmor digest rejection", err)
	}
}

func TestCheckPinnedInputsRejectsCrossWorkflowZizmorDigestMismatch(t *testing.T) {
	t.Parallel()

	cfg := writePinnedInputsFixture(t)
	securityWorkflowPath := filepath.Join(cfg.WorkflowsDir, "security.yml")
	rewriteFile(t, securityWorkflowPath, func(content string) string {
		return replaceAllMatches(
			t,
			content,
			regexp.MustCompile(`ZIZMOR_SHA256: [0-9a-f]{64}`),
			"ZIZMOR_SHA256: 0000000000000000000000000000000000000000000000000000000000000000",
		)
	})

	err := metadatautil.CheckPinnedInputs(cfg)
	if err == nil {
		t.Fatal("metadatautil.CheckPinnedInputs() unexpectedly accepted mismatched workflow zizmor digests")
	}
	if !strings.Contains(err.Error(), "ZIZMOR_SHA256 must match") {
		t.Fatalf("metadatautil.CheckPinnedInputs() error = %v, want cross-workflow zizmor digest mismatch rejection", err)
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

	err := metadatautil.VerifyGitHubHostedControls(tmpDir, "omkhar/workcell", policyPath)
	if err == nil {
		t.Fatal("metadatautil.VerifyGitHubHostedControls() unexpectedly accepted extra collaborators in single-owner-public-pr mode")
	}
	if !strings.Contains(err.Error(), "requires exactly one direct collaborator") {
		t.Fatalf("metadatautil.VerifyGitHubHostedControls() error = %v, want collaborator-count rejection", err)
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

	err := metadatautil.VerifyGitHubHostedControls(tmpDir, "omkhar/workcell", policyPath)
	if err == nil {
		t.Fatal("metadatautil.VerifyGitHubHostedControls() unexpectedly accepted approval-gated mode")
	}
	if !strings.Contains(err.Error(), "must set branch_review.mode to 'review-gated', 'single-owner-public-pr', or 'single-owner-private-pr'") {
		t.Fatalf("metadatautil.VerifyGitHubHostedControls() error = %v, want unsupported-mode rejection", err)
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

	err := metadatautil.VerifyGitHubHostedControls(tmpDir, "omkhar/workcell", policyPath)
	if err == nil {
		t.Fatal("metadatautil.VerifyGitHubHostedControls() unexpectedly accepted review-gated rules without code owner review")
	}
	if !strings.Contains(err.Error(), "must require code owner review") {
		t.Fatalf("metadatautil.VerifyGitHubHostedControls() error = %v, want code-owner rejection", err)
	}
}

func TestVerifyGitHubHostedControlsRejectsAllActionsAllowed(t *testing.T) {
	t.Parallel()

	tmpDir, policyPath := writeHostedControlsFixture(t, "review-gated", "review-gated", []map[string]any{
		{
			"login": "omkhar",
			"permissions": map[string]any{
				"admin": true,
			},
		},
	})

	rewriteFile(t, filepath.Join(tmpDir, "actions-permissions.json"), func(content string) string {
		return strings.Replace(content, `"allowed_actions": "selected"`, `"allowed_actions": "all"`, 1)
	})

	err := metadatautil.VerifyGitHubHostedControls(tmpDir, "omkhar/workcell", policyPath)
	if err == nil {
		t.Fatal("metadatautil.VerifyGitHubHostedControls() unexpectedly accepted all Actions")
	}
	if !strings.Contains(err.Error(), "must restrict allowed_actions to selected") {
		t.Fatalf("metadatautil.VerifyGitHubHostedControls() error = %v, want selected-actions rejection", err)
	}
}

func TestVerifyGitHubHostedControlsRejectsPermissiveWorkflowToken(t *testing.T) {
	t.Parallel()

	tmpDir, policyPath := writeHostedControlsFixture(t, "review-gated", "review-gated", []map[string]any{
		{
			"login": "omkhar",
			"permissions": map[string]any{
				"admin": true,
			},
		},
	})

	rewriteFile(t, filepath.Join(tmpDir, "actions-workflow-permissions.json"), func(content string) string {
		return strings.Replace(content, `"default_workflow_permissions": "read"`, `"default_workflow_permissions": "write"`, 1)
	})

	err := metadatautil.VerifyGitHubHostedControls(tmpDir, "omkhar/workcell", policyPath)
	if err == nil {
		t.Fatal("metadatautil.VerifyGitHubHostedControls() unexpectedly accepted write workflow token permissions")
	}
	if !strings.Contains(err.Error(), `must be "read"`) {
		t.Fatalf("metadatautil.VerifyGitHubHostedControls() error = %v, want workflow-token rejection", err)
	}
}

func TestVerifyGitHubHostedControlsRejectsWorkflowTokenPRApproval(t *testing.T) {
	t.Parallel()

	tmpDir, policyPath := writeHostedControlsFixture(t, "review-gated", "review-gated", []map[string]any{
		{
			"login": "omkhar",
			"permissions": map[string]any{
				"admin": true,
			},
		},
	})

	rewriteFile(t, filepath.Join(tmpDir, "actions-workflow-permissions.json"), func(content string) string {
		return strings.Replace(content, `"can_approve_pull_request_reviews": false`, `"can_approve_pull_request_reviews": true`, 1)
	})

	err := metadatautil.VerifyGitHubHostedControls(tmpDir, "omkhar/workcell", policyPath)
	if err == nil {
		t.Fatal("metadatautil.VerifyGitHubHostedControls() unexpectedly accepted workflow token PR approval")
	}
	if !strings.Contains(err.Error(), "must not be allowed to approve pull requests") {
		t.Fatalf("metadatautil.VerifyGitHubHostedControls() error = %v, want workflow-token approval rejection", err)
	}
}

func TestVerifyGitHubHostedControlsRejectsMutableGitHubReleases(t *testing.T) {
	t.Parallel()

	tmpDir, policyPath := writeHostedControlsFixture(t, "review-gated", "review-gated", []map[string]any{
		{
			"login": "omkhar",
			"permissions": map[string]any{
				"admin": true,
			},
		},
	})

	rewriteFile(t, filepath.Join(tmpDir, "immutable-releases.json"), func(content string) string {
		return strings.Replace(content, `"enabled": true`, `"enabled": false`, 1)
	})

	err := metadatautil.VerifyGitHubHostedControls(tmpDir, "omkhar/workcell", policyPath)
	if err == nil {
		t.Fatal("metadatautil.VerifyGitHubHostedControls() unexpectedly accepted mutable GitHub releases")
	}
	if !strings.Contains(err.Error(), "immutable GitHub releases must be enabled") {
		t.Fatalf("metadatautil.VerifyGitHubHostedControls() error = %v, want immutable-release rejection", err)
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

	err := metadatautil.VerifyGitHubHostedControls(tmpDir, "omkhar/workcell", policyPath)
	if err == nil {
		t.Fatal("metadatautil.VerifyGitHubHostedControls() unexpectedly accepted extra collaborators in single-owner-public release mode")
	}
	if !strings.Contains(err.Error(), "requires exactly one direct collaborator") {
		t.Fatalf("metadatautil.VerifyGitHubHostedControls() error = %v, want collaborator-count rejection", err)
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

	err := metadatautil.VerifyGitHubHostedControls(tmpDir, "omkhar/workcell", policyPath)
	if err == nil {
		t.Fatal("metadatautil.VerifyGitHubHostedControls() unexpectedly accepted a non-owner collaborator in single-owner-public-pr mode")
	}
	if !strings.Contains(err.Error(), "requires the owner to be the only direct collaborator") {
		t.Fatalf("metadatautil.VerifyGitHubHostedControls() error = %v, want owner-only collaborator rejection", err)
	}
}

func TestVerifyGitHubHostedControlsRejectsUnexpectedUpstreamRefreshSecret(t *testing.T) {
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
		return strings.Replace(content, `"secrets": []`, `"secrets": [{"name":"WORKCELL_UPSTREAM_REFRESH_GPG_PRIVATE_KEY"}]`, 1)
	})

	err := metadatautil.VerifyGitHubHostedControls(tmpDir, "omkhar/workcell", policyPath)
	if err == nil {
		t.Fatal("metadatautil.VerifyGitHubHostedControls() unexpectedly accepted an unexpected upstream-refresh secret")
	}
	if !strings.Contains(err.Error(), "workflow environment secrets on omkhar/workcell/upstream-refresh include unexpected entries") {
		t.Fatalf("metadatautil.VerifyGitHubHostedControls() error = %v, want unexpected upstream-refresh secret rejection", err)
	}
}

func TestVerifyGitHubHostedControlsRejectsUnexpectedUpstreamRefreshVariable(t *testing.T) {
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
		return strings.Replace(content, `"variables": []`, `"variables": [{"name":"WORKCELL_UPSTREAM_REFRESH_GIT_EMAIL","value":"omkhar@gmail.com"}]`, 1)
	})

	err := metadatautil.VerifyGitHubHostedControls(tmpDir, "omkhar/workcell", policyPath)
	if err == nil {
		t.Fatal("metadatautil.VerifyGitHubHostedControls() unexpectedly accepted an unexpected upstream-refresh variable")
	}
	if !strings.Contains(err.Error(), "workflow environment variables on omkhar/workcell/upstream-refresh include unexpected entries") {
		t.Fatalf("metadatautil.VerifyGitHubHostedControls() error = %v, want unexpected upstream-refresh variable rejection", err)
	}
}

func TestVerifyGitHubHostedControlsRejectsEnvironmentAdminBypass(t *testing.T) {
	t.Parallel()

	tmpDir, policyPath := writeHostedControlsFixture(t, "review-gated", "review-gated", []map[string]any{
		{
			"login": "omkhar",
			"permissions": map[string]any{
				"admin": true,
			},
		},
	})

	rewriteFile(t, filepath.Join(tmpDir, "environment-upstream-refresh.json"), func(content string) string {
		return strings.Replace(content, `"can_admins_bypass": false`, `"can_admins_bypass": true`, 1)
	})

	err := metadatautil.VerifyGitHubHostedControls(tmpDir, "omkhar/workcell", policyPath)
	if err == nil {
		t.Fatal("metadatautil.VerifyGitHubHostedControls() unexpectedly accepted administrator bypass on upstream-refresh")
	}
	if !strings.Contains(err.Error(), "must set can_admins_bypass=false") {
		t.Fatalf("metadatautil.VerifyGitHubHostedControls() error = %v, want admin-bypass rejection", err)
	}
}

func TestVerifyGitHubHostedControlsRejectsUnexpectedEnvironmentBranchPolicy(t *testing.T) {
	t.Parallel()

	tmpDir, policyPath := writeHostedControlsFixture(t, "review-gated", "review-gated", []map[string]any{
		{
			"login": "omkhar",
			"permissions": map[string]any{
				"admin": true,
			},
		},
	})

	rewriteFile(t, filepath.Join(tmpDir, "environment-upstream-refresh-deployment-branch-policies.json"), func(content string) string {
		return strings.Replace(content, `"main"`, `"develop"`, 1)
	})

	err := metadatautil.VerifyGitHubHostedControls(tmpDir, "omkhar/workcell", policyPath)
	if err == nil {
		t.Fatal("metadatautil.VerifyGitHubHostedControls() unexpectedly accepted the wrong deployment branch policy")
	}
	if !strings.Contains(err.Error(), "must restrict deployment branches to main") {
		t.Fatalf("metadatautil.VerifyGitHubHostedControls() error = %v, want deployment-branch rejection", err)
	}
}
