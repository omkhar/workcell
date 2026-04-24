// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package authpolicy

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type runResult struct {
	code   int
	stdout string
	stderr string
}

func runAuthPolicy(args ...string) runResult {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run("workcell", args, &stdout, &stderr)
	return runResult{code: code, stdout: stdout.String(), stderr: stderr.String()}
}

func writeFile(tb testing.TB, path, content string, mode os.FileMode) {
	tb.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		tb.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		tb.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		tb.Fatal(err)
	}
}

func readFile(tb testing.TB, path string) string {
	tb.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		tb.Fatal(err)
	}
	return string(data)
}

func assertMode(tb testing.TB, path string, want os.FileMode) {
	tb.Helper()
	info, err := os.Stat(path)
	if err != nil {
		tb.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		tb.Fatalf("mode mismatch for %s: got %04o want %04o", path, got, want)
	}
}

func mustContain(tb testing.TB, text, want string) {
	tb.Helper()
	if !strings.Contains(text, want) {
		tb.Fatalf("%q does not contain %q", text, want)
	}
}

func mustNotContain(tb testing.TB, text, want string) {
	tb.Helper()
	if strings.Contains(text, want) {
		tb.Fatalf("%q unexpectedly contains %q", text, want)
	}
}

func pathVariants(path string) []string {
	var variants []string
	seen := map[string]struct{}{}
	add := func(candidate string) {
		if candidate == "" {
			return
		}
		if _, ok := seen[candidate]; ok {
			return
		}
		seen[candidate] = struct{}{}
		variants = append(variants, candidate)
	}

	add(filepath.Clean(path))
	if abs, err := filepath.Abs(path); err == nil {
		add(filepath.Clean(abs))
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		add(filepath.Clean(resolved))
	}
	if abs, err := filepath.Abs(path); err == nil {
		if resolved, err := filepath.EvalSymlinks(abs); err == nil {
			add(filepath.Clean(resolved))
		}
	}
	return variants
}

func mustContainAny(tb testing.TB, text string, candidates ...string) {
	tb.Helper()
	for _, candidate := range candidates {
		if strings.Contains(text, candidate) {
			return
		}
	}
	tb.Fatalf("%q does not contain any of %q", text, candidates)
}

func mustContainAnyPath(tb testing.TB, text, prefix string, candidates ...string) {
	tb.Helper()
	parts := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		parts = append(parts, prefix+candidate)
	}
	mustContainAny(tb, text, parts...)
}

func mustContainAnyWrapped(tb testing.TB, text, prefix, suffix string, candidates ...string) {
	tb.Helper()
	parts := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		parts = append(parts, prefix+candidate+suffix)
	}
	mustContainAny(tb, text, parts...)
}

func existingPath(tb testing.TB, candidates ...string) string {
	tb.Helper()
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	tb.Fatalf("none of the candidate paths exist: %q", candidates)
	return ""
}

func removeExistingPath(tb testing.TB, candidates ...string) {
	tb.Helper()
	path := existingPath(tb, candidates...)
	if err := os.Remove(path); err != nil {
		tb.Fatal(err)
	}
}

func TestStatusWithoutPolicyReportsNone(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	policyPath := filepath.Join(root, "missing-policy.toml")

	got := runAuthPolicy("status", "--policy", policyPath, "--agent", "codex")
	if got.code != 0 {
		t.Fatalf("Run(status) = %d stdout=%q stderr=%q", got.code, got.stdout, got.stderr)
	}
	mustContain(t, got.stdout, "injection_policy=none")
	mustContain(t, got.stdout, "credential_resolution_states=none")
	mustContain(t, got.stdout, "provider_bootstrap_state=not-configured")
	mustContain(t, got.stdout, "provider_bootstrap_path=direct-staged")
}

func TestPolicyInspectionCommandsShowValidateAndDiff(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	policyPath := filepath.Join(root, "policy.toml")
	sourcePath := filepath.Join(root, "auth.json")
	writeFile(t, sourcePath, "super-secret-token\n", 0o600)
	writeFile(t, policyPath, strings.Join([]string{
		"version = 1",
		"# local note",
		"[credentials.codex_auth]",
		`source = "` + sourcePath + `"`,
		`providers = ["codex"]`,
		`modes = ["strict"]`,
	}, "\n")+"\n", 0o600)

	got := runAuthPolicy("show", "--policy", policyPath)
	if got.code != 0 {
		t.Fatalf("Run(show) = %d stdout=%q stderr=%q", got.code, got.stdout, got.stderr)
	}
	if !(strings.Index(got.stdout, `modes = ["strict"]`) < strings.Index(got.stdout, `providers = ["codex"]`) && strings.Index(got.stdout, `providers = ["codex"]`) < strings.Index(got.stdout, `source = "`+sourcePath+`"`)) {
		t.Fatalf("show output not canonicalized as expected: %q", got.stdout)
	}
	mustNotContain(t, got.stdout, "local note")

	got = runAuthPolicy("validate", "--policy", policyPath)
	if got.code != 0 {
		t.Fatalf("Run(validate) = %d stdout=%q stderr=%q", got.code, got.stdout, got.stderr)
	}
	mustContain(t, got.stdout, "policy_valid=1")
	mustContain(t, got.stdout, "resolver_readiness=not-applicable")

	got = runAuthPolicy("diff", "--policy", policyPath)
	if got.code != 0 {
		t.Fatalf("Run(diff) = %d stdout=%q stderr=%q", got.code, got.stdout, got.stderr)
	}
	mustContain(t, got.stdout, "diff_status=changed")
	mustContain(t, got.stdout, "--- current")
	mustContain(t, got.stdout, "+++ canonical")
	mustContain(t, got.stdout, "-# local note")
}

func TestPolicyWhyExplainsSelectionAndHidesSecrets(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	policyPath := filepath.Join(root, "policy.toml")
	sourcePath := filepath.Join(root, "auth.json")
	writeFile(t, sourcePath, "super-secret-token\n", 0o600)
	writeFile(t, policyPath, strings.Join([]string{
		"version = 1",
		"[credentials.codex_auth]",
		`source = "` + sourcePath + `"`,
		`providers = ["codex"]`,
		`modes = ["strict"]`,
	}, "\n")+"\n", 0o600)

	got := runAuthPolicy(
		"why",
		"--policy", policyPath,
		"--credential", "codex_auth",
		"--agent", "codex",
		"--mode", "strict",
	)
	if got.code != 0 {
		t.Fatalf("Run(why) = %d stdout=%q stderr=%q", got.code, got.stdout, got.stderr)
	}
	mustContain(t, got.stdout, "selected=1")
	mustContain(t, got.stdout, "selection_reason=agent matches providers; mode matches modes")
	mustContain(t, got.stdout, "credential_readiness=ready")
	mustContain(t, got.stdout, "credential_input_kind=source")
	mustContain(t, got.stdout, "credential_providers=codex")
	mustContain(t, got.stdout, "credential_modes=strict")
	mustContain(t, got.stdout, "bootstrap_path=direct-staged")
	mustContain(t, got.stdout, "bootstrap_support=repo-required")
	mustNotContain(t, got.stdout, "super-secret-token")
}

func TestPolicyWhyExplainsResolverBackedSelection(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	policyPath := filepath.Join(root, "policy.toml")
	writeFile(t, policyPath, strings.Join([]string{
		"version = 1",
		"[credentials.claude_auth]",
		`resolver = "claude-macos-keychain"`,
		`materialization = "ephemeral"`,
		`providers = ["claude"]`,
		`modes = ["strict"]`,
	}, "\n")+"\n", 0o600)

	got := runAuthPolicy(
		"why",
		"--policy", policyPath,
		"--credential", "claude_auth",
		"--agent", "claude",
		"--mode", "strict",
	)
	if got.code != 0 {
		t.Fatalf("Run(why) = %d stdout=%q stderr=%q", got.code, got.stdout, got.stderr)
	}
	mustContain(t, got.stdout, "selected=1")
	mustContain(t, got.stdout, "credential_readiness=configured-only")
	mustContain(t, got.stdout, "credential_input_kind=resolver")
	mustContain(t, got.stdout, "credential_resolver=claude-macos-keychain")
	mustContain(t, got.stdout, "selection_reason=agent matches providers; mode matches modes")
	mustContain(t, got.stdout, "bootstrap_path=host-export-scaffold")
	mustContain(t, got.stdout, "bootstrap_support=manual")
}

func TestPolicyWhyExplainsWhenCredentialIsNotSelected(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	policyPath := filepath.Join(root, "policy.toml")
	writeFile(t, policyPath, strings.Join([]string{
		"version = 1",
		"[credentials.codex_auth]",
		`source = "/tmp/auth.json"`,
		`providers = ["claude"]`,
		`modes = ["build"]`,
	}, "\n")+"\n", 0o600)

	got := runAuthPolicy(
		"why",
		"--policy", policyPath,
		"--credential", "codex_auth",
		"--agent", "codex",
		"--mode", "strict",
	)
	if got.code != 0 {
		t.Fatalf("Run(why) = %d stdout=%q stderr=%q", got.code, got.stdout, got.stderr)
	}
	mustContain(t, got.stdout, "selected=0")
	mustContain(t, got.stdout, "selection_reason=agent does not match providers; mode does not match modes")
	mustContain(t, got.stdout, "credential_readiness=filtered-provider")
	mustContain(t, got.stdout, "credential_input_kind=source")
}

func TestPolicyWhyTreatsOutOfScopeCredentialAsNotSelected(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	policyPath := filepath.Join(root, "policy.toml")
	writeFile(t, policyPath, strings.Join([]string{
		"version = 1",
		"[credentials.claude_api_key]",
		`source = "/no/such/file"`,
	}, "\n")+"\n", 0o600)

	got := runAuthPolicy(
		"why",
		"--policy", policyPath,
		"--credential", "claude_api_key",
		"--agent", "codex",
		"--mode", "strict",
	)
	if got.code != 0 {
		t.Fatalf("Run(why) = %d stdout=%q stderr=%q", got.code, got.stdout, got.stderr)
	}
	mustContain(t, got.stdout, "selected=0")
	mustContain(t, got.stdout, "selection_reason=credential is not in scope for agent codex")
	mustContain(t, got.stdout, "credential_readiness=out-of-scope")
	mustContain(t, got.stdout, "credential_input_kind=source")
}

func TestPolicyInspectionCommandsFailClosedOnMissingPolicy(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	policyPath := filepath.Join(root, "missing.toml")
	cases := []struct {
		name string
		args []string
	}{
		{name: "show", args: []string{"show", "--policy", policyPath}},
		{name: "validate", args: []string{"validate", "--policy", policyPath}},
		{name: "diff", args: []string{"diff", "--policy", policyPath}},
		{name: "why", args: []string{"why", "--policy", policyPath, "--credential", "codex_auth", "--agent", "codex", "--mode", "strict"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := runAuthPolicy(tc.args...)
			if got.code != 1 {
				t.Fatalf("Run(%s) = %d stdout=%q stderr=%q", tc.name, got.code, got.stdout, got.stderr)
			}
			mustContainAny(t, got.stderr, "does not exist", "no such file or directory")
		})
	}
}

func TestValidateRejectsInvalidSelectors(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	policyPath := filepath.Join(root, "policy.toml")
	sourcePath := filepath.Join(root, "auth.json")
	writeFile(t, sourcePath, "{}\n", 0o600)
	writeFile(t, policyPath, strings.Join([]string{
		"version = 1",
		"[credentials.codex_auth]",
		`source = "` + sourcePath + `"`,
		`providers = ["bogus"]`,
	}, "\n")+"\n", 0o600)

	got := runAuthPolicy("validate", "--policy", policyPath)
	if got.code != 1 {
		t.Fatalf("Run(validate) = %d stdout=%q stderr=%q", got.code, got.stdout, got.stderr)
	}
	mustContain(t, got.stderr, "credentials.codex_auth.providers contains unsupported value: bogus")
}

func TestValidateRejectsMissingCredentialSource(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	policyPath := filepath.Join(root, "policy.toml")
	writeFile(t, policyPath, strings.Join([]string{
		"version = 1",
		"[credentials.codex_auth]",
		`source = "/no/such/file"`,
	}, "\n")+"\n", 0o600)

	got := runAuthPolicy("validate", "--policy", policyPath)
	if got.code != 1 {
		t.Fatalf("Run(validate) = %d stdout=%q stderr=%q", got.code, got.stdout, got.stderr)
	}
	mustContainAny(t, got.stderr, "does not exist", "no such file or directory")
}

func TestValidateReportsResolverReadinessAsDeferred(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	policyPath := filepath.Join(root, "policy.toml")
	writeFile(t, policyPath, strings.Join([]string{
		"version = 1",
		"[credentials.claude_auth]",
		`resolver = "claude-macos-keychain"`,
		`materialization = "ephemeral"`,
	}, "\n")+"\n", 0o600)

	got := runAuthPolicy("validate", "--policy", policyPath)
	if got.code != 0 {
		t.Fatalf("Run(validate) = %d stdout=%q stderr=%q", got.code, got.stdout, got.stderr)
	}
	mustContain(t, got.stdout, "policy_valid=1")
	mustContain(t, got.stdout, "resolver_readiness=deferred-to-launch")
}

func TestInitSetStatusUnsetRoundTrip(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	policyPath := filepath.Join(root, "injection-policy.toml")
	managedRoot := filepath.Join(root, "credentials")
	sourcePath := filepath.Join(root, "auth.json")
	writeFile(t, sourcePath, "{}\n", 0o600)

	got := runAuthPolicy("init", "--policy", policyPath, "--managed-root", managedRoot)
	if got.code != 0 {
		t.Fatalf("Run(init) = %d stdout=%q stderr=%q", got.code, got.stdout, got.stderr)
	}
	mustContainAnyPath(t, got.stdout, "policy_path=", pathVariants(policyPath)...)
	mustContainAnyPath(t, got.stdout, "managed_root=", pathVariants(managedRoot)...)
	if readFile(t, policyPath) != "version = 1\n" {
		t.Fatalf("unexpected init policy content: %q", readFile(t, policyPath))
	}
	for _, dir := range []string{"codex", "claude", "gemini", "shared"} {
		if info, err := os.Stat(filepath.Join(managedRoot, dir)); err != nil || !info.IsDir() {
			t.Fatalf("expected managed root directory %s to exist: %v", dir, err)
		}
	}

	got = runAuthPolicy(
		"set",
		"--policy", policyPath,
		"--managed-root", managedRoot,
		"--agent", "codex",
		"--credential", "codex_auth",
		"--source", sourcePath,
	)
	if got.code != 0 {
		t.Fatalf("Run(set source) = %d stdout=%q stderr=%q", got.code, got.stdout, got.stderr)
	}
	expectedManaged := filepath.Join(managedRoot, "codex", "auth.json")
	managedVariants := pathVariants(expectedManaged)
	mustContainAnyWrapped(t, readFile(t, policyPath), `source = "`, `"`, managedVariants...)
	managedCopy := existingPath(t, managedVariants...)
	assertMode(t, managedCopy, 0o600)
	if readFile(t, managedCopy) != "{}\n" {
		t.Fatalf("unexpected managed copy content: %q", readFile(t, managedCopy))
	}

	got = runAuthPolicy("status", "--policy", policyPath, "--agent", "codex")
	if got.code != 0 {
		t.Fatalf("Run(status) = %d stdout=%q stderr=%q", got.code, got.stdout, got.stderr)
	}
	mustContain(t, got.stdout, "credential_keys=codex_auth")
	mustContain(t, got.stdout, "credential_input_kinds=codex_auth:source")
	mustContain(t, got.stdout, "provider_auth_ready_states=codex_auth:ready")
	mustContain(t, got.stdout, "shared_auth_ready_states=none")
	mustContain(t, got.stdout, "provider_auth_mode=codex_auth")
	mustContain(t, got.stdout, "shared_auth_modes=none")
	mustContain(t, got.stdout, "provider_bootstrap_state=ready")
	mustContain(t, got.stdout, "provider_bootstrap_path=direct-staged")
	mustContain(t, got.stdout, "provider_bootstrap_support=repo-required")

	got = runAuthPolicy("unset", "--policy", policyPath, "--managed-root", managedRoot, "--credential", "codex_auth")
	if got.code != 0 {
		t.Fatalf("Run(unset) = %d stdout=%q stderr=%q", got.code, got.stdout, got.stderr)
	}
	mustContain(t, got.stdout, "removed=1")
	for _, candidate := range managedVariants {
		if _, err := os.Stat(candidate); !os.IsNotExist(err) {
			t.Fatalf("expected managed copy to be removed, got err=%v for %s", err, candidate)
		}
	}
}

func TestSetResolverAndStatus(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	policyPath := filepath.Join(root, "injection-policy.toml")
	managedRoot := filepath.Join(root, "credentials")
	got := runAuthPolicy("init", "--policy", policyPath, "--managed-root", managedRoot)
	if got.code != 0 {
		t.Fatalf("Run(init) = %d stdout=%q stderr=%q", got.code, got.stdout, got.stderr)
	}

	got = runAuthPolicy(
		"set",
		"--policy", policyPath,
		"--managed-root", managedRoot,
		"--agent", "claude",
		"--credential", "claude_auth",
		"--resolver", "claude-macos-keychain",
		"--ack-host-resolver",
	)
	if got.code != 0 {
		t.Fatalf("Run(set resolver) = %d stdout=%q stderr=%q", got.code, got.stdout, got.stderr)
	}
	rendered := readFile(t, policyPath)
	mustContain(t, rendered, `[credentials.claude_auth]`)
	mustContain(t, rendered, `resolver = "claude-macos-keychain"`)
	mustContain(t, rendered, `materialization = "ephemeral"`)

	got = runAuthPolicy("status", "--policy", policyPath, "--agent", "claude")
	if got.code != 0 {
		t.Fatalf("Run(status) = %d stdout=%q stderr=%q", got.code, got.stdout, got.stderr)
	}
	mustContain(t, got.stdout, "credential_input_kinds=claude_auth:resolver")
	mustContain(t, got.stdout, "credential_resolvers=claude_auth:claude-macos-keychain")
	mustContain(t, got.stdout, "credential_materialization=claude_auth:ephemeral")
	mustContain(t, got.stdout, "credential_resolution_states=claude_auth:configured-only")
	mustContain(t, got.stdout, "provider_auth_ready_states=claude_auth:configured-only")
	mustContain(t, got.stdout, "shared_auth_ready_states=none")
	mustContain(t, got.stdout, "provider_auth_mode=none")
	mustContain(t, got.stdout, "shared_auth_modes=none")
	mustContain(t, got.stdout, "provider_bootstrap_state=configured-only")
	mustContain(t, got.stdout, "provider_bootstrap_path=host-export-scaffold")
	mustContain(t, got.stdout, "provider_bootstrap_support=manual")
}

func TestSetCodexResolverAndStatusWhenHostCacheExists(t *testing.T) {
	root := t.TempDir()
	policyPath := filepath.Join(root, "injection-policy.toml")
	managedRoot := filepath.Join(root, "credentials")
	codexAuthPath := filepath.Join(root, ".codex", "auth.json")
	if err := os.MkdirAll(filepath.Dir(codexAuthPath), 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, codexAuthPath, "{\"token\":\"codex\"}\n", 0o600)
	t.Setenv("HOME", root)

	got := runAuthPolicy("init", "--policy", policyPath, "--managed-root", managedRoot)
	if got.code != 0 {
		t.Fatalf("Run(init) = %d stdout=%q stderr=%q", got.code, got.stdout, got.stderr)
	}

	got = runAuthPolicy(
		"set",
		"--policy", policyPath,
		"--managed-root", managedRoot,
		"--agent", "codex",
		"--credential", "codex_auth",
		"--resolver", "codex-home-auth-file",
		"--ack-host-resolver",
	)
	if got.code != 0 {
		t.Fatalf("Run(set codex resolver) = %d stdout=%q stderr=%q", got.code, got.stdout, got.stderr)
	}
	mustContain(t, got.stdout, "resolver_status=configured-launch-ready")

	got = runAuthPolicy("status", "--policy", policyPath, "--agent", "codex")
	if got.code != 0 {
		t.Fatalf("Run(status) = %d stdout=%q stderr=%q", got.code, got.stdout, got.stderr)
	}
	mustContain(t, got.stdout, "credential_input_kinds=codex_auth:resolver")
	mustContain(t, got.stdout, "credential_resolvers=codex_auth:codex-home-auth-file")
	mustContain(t, got.stdout, "credential_resolution_states=codex_auth:host-source")
	mustContain(t, got.stdout, "provider_auth_ready_states=codex_auth:ready")
	mustContain(t, got.stdout, "provider_auth_mode=codex_auth")
	mustContain(t, got.stdout, "provider_bootstrap_path=host-resolver")
	mustContain(t, got.stdout, "provider_bootstrap_support=repo-required")
}

func TestSharedCredentialsAreScopedToRequestedAgent(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	policyPath := filepath.Join(root, "injection-policy.toml")
	hostsPath := filepath.Join(root, "hosts.yml")
	writeFile(t, hostsPath, "github.com:\n", 0o600)
	writeFile(t, policyPath, strings.Join([]string{
		"version = 1",
		"[credentials.github_hosts]",
		`source = "` + hostsPath + `"`,
		`providers = ["codex"]`,
	}, "\n")+"\n", 0o600)

	codexStatus := runAuthPolicy("status", "--policy", policyPath, "--agent", "codex")
	if codexStatus.code != 0 {
		t.Fatalf("Run(status codex) = %d stdout=%q stderr=%q", codexStatus.code, codexStatus.stdout, codexStatus.stderr)
	}
	mustContain(t, codexStatus.stdout, "provider_auth_ready_states=none")
	mustContain(t, codexStatus.stdout, "shared_auth_ready_states=github_hosts:ready")
	mustContain(t, codexStatus.stdout, "shared_auth_modes=github_hosts")

	claudeStatus := runAuthPolicy("status", "--policy", policyPath, "--agent", "claude")
	if claudeStatus.code != 0 {
		t.Fatalf("Run(status claude) = %d stdout=%q stderr=%q", claudeStatus.code, claudeStatus.stdout, claudeStatus.stderr)
	}
	mustContain(t, claudeStatus.stdout, "credential_keys=none")
	mustContain(t, claudeStatus.stdout, "shared_auth_ready_states=github_hosts:filtered-provider")
	mustContain(t, claudeStatus.stdout, "shared_auth_modes=none")
}

func TestRunRejectsInvalidConfigurations(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		policy       string
		agent        string
		credential   string
		command      string
		extraArgs    []string
		needsInit    bool
		wantContains string
	}{
		{
			name: "conflicting-source-and-resolver",
			policy: strings.Join([]string{
				"version = 1",
				"[credentials.claude_auth]",
				`source = "/tmp/auth.json"`,
				`resolver = "claude-macos-keychain"`,
				`materialization = "ephemeral"`,
			}, "\n") + "\n",
			command:      "status",
			agent:        "claude",
			needsInit:    false,
			wantContains: "credentials.claude_auth must not declare both source and resolver",
		},
		{
			name: "unsupported-resolver",
			policy: strings.Join([]string{
				"version = 1",
				"[credentials.claude_auth]",
				`resolver = "bogus"`,
				`materialization = "ephemeral"`,
			}, "\n") + "\n",
			command:      "status",
			agent:        "claude",
			needsInit:    false,
			wantContains: "credentials.claude_auth.resolver is unsupported: bogus",
		},
		{
			name: "invalid-selector-values",
			policy: strings.Join([]string{
				"version = 1",
				"[credentials.codex_auth]",
				`source = "/tmp/auth.json"`,
				`providers = ["bogus"]`,
			}, "\n") + "\n",
			command:      "status",
			agent:        "codex",
			needsInit:    false,
			wantContains: "credentials.codex_auth.providers contains unsupported value: bogus",
		},
		{
			name: "shared-github-without-providers",
			policy: strings.Join([]string{
				"version = 1",
				"[credentials.github_hosts]",
				`source = "/tmp/hosts.yml"`,
			}, "\n") + "\n",
			command:      "status",
			agent:        "codex",
			needsInit:    false,
			wantContains: "credentials.github_hosts.providers is required so shared GitHub credentials stay least-privilege",
		},
		{
			name: "shared-github-string-form",
			policy: strings.Join([]string{
				"version = 1",
				"[credentials]",
				`github_hosts = "/tmp/hosts.yml"`,
			}, "\n") + "\n",
			command:      "status",
			agent:        "codex",
			needsInit:    false,
			wantContains: "credentials.github_hosts.providers is required so shared GitHub credentials stay least-privilege",
		},
		{
			name: "set-rejects-included-credential",
			policy: strings.Join([]string{
				"version = 1",
				`includes = ["fragment.toml"]`,
			}, "\n") + "\n",
			command:      "set",
			agent:        "codex",
			credential:   "codex_auth",
			extraArgs:    []string{"--source", "/tmp/auth.json"},
			needsInit:    false,
			wantContains: "declared by an included policy fragment",
		},
		{
			name: "unset-rejects-included-credential",
			policy: strings.Join([]string{
				"version = 1",
				`includes = ["fragment.toml"]`,
			}, "\n") + "\n",
			command:      "unset",
			credential:   "codex_auth",
			needsInit:    false,
			wantContains: "declared by an included policy fragment",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			policyPath := filepath.Join(root, "injection-policy.toml")
			writeFile(t, policyPath, tc.policy, 0o600)
			if strings.Contains(tc.name, "included-credential") {
				writeFile(t, filepath.Join(root, "fragment.toml"), strings.Join([]string{
					"version = 1",
					"[credentials.codex_auth]",
					`source = "/tmp/original.json"`,
				}, "\n")+"\n", 0o600)
			}
			managedRoot := filepath.Join(root, "credentials")
			if tc.needsInit {
				if got := runAuthPolicy("init", "--policy", policyPath, "--managed-root", managedRoot); got.code != 0 {
					t.Fatalf("Run(init) = %d stdout=%q stderr=%q", got.code, got.stdout, got.stderr)
				}
			}

			args := []string{tc.command, "--policy", policyPath}
			if tc.command != "status" {
				args = append(args, "--managed-root", managedRoot)
			}
			if tc.agent != "" {
				args = append(args, "--agent", tc.agent)
			}
			if tc.credential != "" {
				args = append(args, "--credential", tc.credential)
			}
			args = append(args, tc.extraArgs...)

			got := runAuthPolicy(args...)
			if got.code != 1 {
				t.Fatalf("Run(%s) = %d stdout=%q stderr=%q", tc.command, got.code, got.stdout, got.stderr)
			}
			mustContain(t, got.stderr, tc.wantContains)
		})
	}
}

func TestSetRollsBackManagedCopyWhenPolicyWriteFails(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	policyPath := filepath.Join(root, "injection-policy.toml")
	managedRoot := filepath.Join(root, "credentials")
	sourcePath := filepath.Join(root, "auth.json")
	writeFile(t, sourcePath, "{\"token\":\"next\"}\n", 0o600)
	writeFile(t, policyPath, strings.Join([]string{
		"version = 1",
		`unexpected = "value"`,
		"[credentials.codex_auth]",
		`source = "/tmp/original.json"`,
	}, "\n")+"\n", 0o600)

	got := runAuthPolicy(
		"set",
		"--policy", policyPath,
		"--managed-root", managedRoot,
		"--agent", "codex",
		"--credential", "codex_auth",
		"--source", sourcePath,
	)
	if got.code != 1 {
		t.Fatalf("Run(set) = %d stdout=%q stderr=%q", got.code, got.stdout, got.stderr)
	}
	candidates := append([]string{}, pathVariants(filepath.Join(managedRoot, "codex", "auth.json"))...)
	candidates = append(candidates, pathVariants(filepath.Join(managedRoot, ".workcell-managed-root"))...)
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			t.Fatalf("expected rollback to remove %s", candidate)
		}
	}
}

func TestStatusRejectsMissingManagedSourceFile(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	policyPath := filepath.Join(root, "injection-policy.toml")
	managedRoot := filepath.Join(root, "credentials")
	sourcePath := filepath.Join(root, "auth.json")
	writeFile(t, sourcePath, "{}\n", 0o600)
	if got := runAuthPolicy("init", "--policy", policyPath, "--managed-root", managedRoot); got.code != 0 {
		t.Fatalf("Run(init) = %d stdout=%q stderr=%q", got.code, got.stdout, got.stderr)
	}
	if got := runAuthPolicy(
		"set",
		"--policy", policyPath,
		"--managed-root", managedRoot,
		"--agent", "codex",
		"--credential", "codex_auth",
		"--source", sourcePath,
	); got.code != 0 {
		t.Fatalf("Run(set) = %d stdout=%q stderr=%q", got.code, got.stdout, got.stderr)
	}
	policyText := readFile(t, policyPath)
	prefix := `source = "`
	start := strings.Index(policyText, prefix)
	if start < 0 {
		t.Fatalf("could not locate rewritten source path in policy: %q", policyText)
	}
	start += len(prefix)
	end := strings.Index(policyText[start:], `"`)
	if end < 0 {
		t.Fatalf("could not parse rewritten source path in policy: %q", policyText)
	}
	managedSourcePath := policyText[start : start+end]
	removeExistingPath(t, pathVariants(managedSourcePath)...)

	got := runAuthPolicy("status", "--policy", policyPath, "--agent", "codex")
	if got.code != 1 {
		t.Fatalf("Run(status) = %d stdout=%q stderr=%q", got.code, got.stdout, got.stderr)
	}
	mustContain(t, got.stderr, "does not exist")
}

func TestSetRejectsSymlinkedManagedRootDestination(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	policyPath := filepath.Join(root, "injection-policy.toml")
	managedRoot := filepath.Join(root, "credentials")
	escapeRoot := filepath.Join(root, "escape")
	sourcePath := filepath.Join(root, "auth.json")
	writeFile(t, sourcePath, "{}\n", 0o600)
	if got := runAuthPolicy("init", "--policy", policyPath, "--managed-root", managedRoot); got.code != 0 {
		t.Fatalf("Run(init) = %d stdout=%q stderr=%q", got.code, got.stdout, got.stderr)
	}
	if err := os.RemoveAll(filepath.Join(managedRoot, "codex")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(escapeRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(escapeRoot, filepath.Join(managedRoot, "codex")); err != nil {
		t.Fatal(err)
	}
	got := runAuthPolicy(
		"set",
		"--policy", policyPath,
		"--managed-root", managedRoot,
		"--agent", "codex",
		"--credential", "codex_auth",
		"--source", sourcePath,
	)
	if got.code != 1 {
		t.Fatalf("Run(set) = %d stdout=%q stderr=%q", got.code, got.stdout, got.stderr)
	}
	mustContain(t, got.stderr, "must not be a symlink")
}

func TestWriteSourceFileRejectsValidatedSourceSwappedToSymlink(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	managedRoot := filepath.Join(root, "managed")
	if err := os.MkdirAll(managedRoot, 0o700); err != nil {
		t.Fatal(err)
	}

	sourcePath := filepath.Join(root, "auth.json")
	writeFile(t, sourcePath, "{\"token\":\"safe\"}\n", 0o600)
	unsafeTarget := filepath.Join(root, "unsafe.json")
	writeFile(t, unsafeTarget, "{\"token\":\"unsafe\"}\n", 0o644)

	validatedSource, err := requireSecretFile(sourcePath, "credentials.codex_auth")
	if err != nil {
		t.Fatalf("requireSecretFile() error: %v", err)
	}

	if err := os.Remove(sourcePath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(unsafeTarget, sourcePath); err != nil {
		t.Fatal(err)
	}

	managedRootFS, err := os.OpenRoot(managedRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer managedRootFS.Close()

	err = writeSourceFile(managedRootFS, validatedSource, filepath.Join("codex", "auth.json"))
	if err == nil {
		got := readFile(t, filepath.Join(managedRoot, "codex", "auth.json"))
		t.Fatalf("writeSourceFile() unexpectedly accepted swapped symlink source and copied %q", got)
	}
}
