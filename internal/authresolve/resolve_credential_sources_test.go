package authresolve

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fileSnapshot struct {
	data []byte
	mode os.FileMode
}

func runResolveCredentialSources(tb testing.TB, args []string, env map[string]string) (int, string, string) {
	tb.Helper()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if env != nil {
		for key, value := range env {
			tb.Setenv(key, value)
		}
	}
	code := Run(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func snapshotFile(tb testing.TB, path string) fileSnapshot {
	tb.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		tb.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		tb.Fatal(err)
	}
	return fileSnapshot{data: data, mode: info.Mode().Perm()}
}

func assertSnapshotEqual(tb testing.TB, path string, want fileSnapshot) {
	tb.Helper()
	got := snapshotFile(tb, path)
	if !bytes.Equal(got.data, want.data) {
		tb.Fatalf("content mismatch for %s\nGO:\n%s\nWANT:\n%s", path, got.data, want.data)
	}
	if got.mode != want.mode {
		tb.Fatalf("mode mismatch for %s: got %v want %v", path, got.mode, want.mode)
	}
}

func writePolicy(tb testing.TB, path, content string) {
	tb.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		tb.Fatal(err)
	}
}

func readJSON(tb testing.TB, path string) map[string]any {
	tb.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		tb.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil {
		tb.Fatal(err)
	}
	return value
}

func TestRunMetadataModeWritesPlaceholderAndMetadata(t *testing.T) {
	root := t.TempDir()
	policyPath := filepath.Join(root, "policy.toml")
	writePolicy(t, policyPath, strings.Join([]string{
		"version = 1",
		"[credentials.claude_auth]",
		`resolver = "claude-macos-keychain"`,
		`materialization = "ephemeral"`,
	}, "\n")+"\n")

	outputRoot := filepath.Join(root, "out")
	if err := os.MkdirAll(outputRoot, 0o700); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runResolveCredentialSources(t, []string{
		"--policy", policyPath,
		"--agent", "claude",
		"--mode", "strict",
		"--resolution-mode", "metadata",
		"--output-policy", filepath.Join(outputRoot, "resolved-policy.toml"),
		"--output-metadata", filepath.Join(outputRoot, "resolver-metadata.json"),
		"--output-root", outputRoot,
	}, nil)
	if code != 0 {
		t.Fatalf("Run() = %d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if stdout != "" || stderr != "" {
		t.Fatalf("unexpected output stdout=%q stderr=%q", stdout, stderr)
	}

	resolvedOutputRoot, err := filepath.EvalSymlinks(outputRoot)
	if err != nil {
		t.Fatal(err)
	}
	resolvedPolicy := snapshotFile(t, filepath.Join(resolvedOutputRoot, "resolved-policy.toml"))
	if !bytes.Contains(resolvedPolicy.data, []byte(`source = "`+filepath.Join(resolvedOutputRoot, "resolved", "credentials", "claude_auth.json")+`"`)) {
		t.Fatalf("resolved policy missing rewritten source path:\n%s", resolvedPolicy.data)
	}
	metadata := readJSON(t, filepath.Join(resolvedOutputRoot, "resolver-metadata.json"))
	if got := metadata["credential_resolvers"].(map[string]any)["claude_auth"]; got != "claude-macos-keychain" {
		t.Fatalf("credential_resolvers.claude_auth = %v", got)
	}
	if got := metadata["credential_resolution_states"].(map[string]any)["claude_auth"]; got != "configured-only" {
		t.Fatalf("credential_resolution_states.claude_auth = %v", got)
	}
	if got := metadata["credential_input_kinds"].(map[string]any)["claude_auth"]; got != "resolver" {
		t.Fatalf("credential_input_kinds.claude_auth = %v", got)
	}
	assertSnapshotEqual(t, filepath.Join(resolvedOutputRoot, "resolved", "credentials", "claude_auth.json"), fileSnapshot{
		data: []byte("{\"resolver\": \"claude-macos-keychain\", \"workcell\": \"metadata-only\"}\n"),
		mode: 0o600,
	})
}

func TestRunLaunchModeFailsClosedWithoutSupportedExportPath(t *testing.T) {
	root := t.TempDir()
	policyPath := filepath.Join(root, "policy.toml")
	writePolicy(t, policyPath, strings.Join([]string{
		"version = 1",
		"[credentials.claude_auth]",
		`resolver = "claude-macos-keychain"`,
		`materialization = "ephemeral"`,
	}, "\n")+"\n")

	code, stdout, stderr := runResolveCredentialSources(t, []string{
		"--policy", policyPath,
		"--agent", "claude",
		"--mode", "strict",
		"--resolution-mode", "launch",
		"--output-policy", filepath.Join(root, "resolved-policy.toml"),
		"--output-metadata", filepath.Join(root, "resolver-metadata.json"),
		"--output-root", root,
	}, nil)
	if code != 1 {
		t.Fatalf("Run() = %d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "Claude macOS login reuse is configured") {
		t.Fatalf("stderr %q missing launch-mode failure", stderr)
	}
}

func TestRunLaunchModeAcceptsTestExportFile(t *testing.T) {
	root := t.TempDir()
	policyPath := filepath.Join(root, "policy.toml")
	exportPath := filepath.Join(root, "claude-export.json")
	writePolicy(t, exportPath, "{\"token\":\"claude\"}\n")
	if err := os.Chmod(exportPath, 0o600); err != nil {
		t.Fatal(err)
	}
	writePolicy(t, policyPath, strings.Join([]string{
		"version = 1",
		"[credentials.claude_auth]",
		`resolver = "claude-macos-keychain"`,
		`materialization = "ephemeral"`,
	}, "\n")+"\n")

	outputRoot := filepath.Join(root, "out")
	if err := os.MkdirAll(outputRoot, 0o700); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runResolveCredentialSources(t, []string{
		"--policy", policyPath,
		"--agent", "claude",
		"--mode", "strict",
		"--resolution-mode", "launch",
		"--output-policy", filepath.Join(outputRoot, "resolved-policy.toml"),
		"--output-metadata", filepath.Join(outputRoot, "resolver-metadata.json"),
		"--output-root", outputRoot,
	}, map[string]string{testClaudeExportEnv: exportPath})
	if code != 0 {
		t.Fatalf("Run() = %d stdout=%q stderr=%q", code, stdout, stderr)
	}

	resolvedOutputRoot, err := filepath.EvalSymlinks(outputRoot)
	if err != nil {
		t.Fatal(err)
	}
	metadata := readJSON(t, filepath.Join(resolvedOutputRoot, "resolver-metadata.json"))
	if got := metadata["credential_resolution_states"].(map[string]any)["claude_auth"]; got != "resolved" {
		t.Fatalf("credential_resolution_states.claude_auth = %v", got)
	}
	resolvedPolicy := snapshotFile(t, filepath.Join(resolvedOutputRoot, "resolved-policy.toml"))
	if !bytes.Contains(resolvedPolicy.data, []byte("resolved/credentials/claude_auth.json")) {
		t.Fatalf("resolved policy missing resolved credential path:\n%s", resolvedPolicy.data)
	}
	assertSnapshotEqual(t, filepath.Join(resolvedOutputRoot, "resolved", "credentials", "claude_auth.json"), fileSnapshot{
		data: []byte("{\"token\":\"claude\"}\n"),
		mode: 0o600,
	})
}

func TestRunRejectsOutputPolicyOutsideOutputRoot(t *testing.T) {
	root := t.TempDir()
	policyPath := filepath.Join(root, "policy.toml")
	writePolicy(t, policyPath, strings.Join([]string{
		"version = 1",
		"[credentials.claude_auth]",
		`resolver = "claude-macos-keychain"`,
		`materialization = "ephemeral"`,
	}, "\n")+"\n")

	outputRoot := filepath.Join(root, "out")
	if err := os.MkdirAll(outputRoot, 0o700); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runResolveCredentialSources(t, []string{
		"--policy", policyPath,
		"--agent", "claude",
		"--mode", "strict",
		"--resolution-mode", "metadata",
		"--output-policy", filepath.Join(root, "resolved-policy.toml"),
		"--output-metadata", filepath.Join(outputRoot, "resolver-metadata.json"),
		"--output-root", outputRoot,
	}, nil)
	if code != 1 {
		t.Fatalf("Run() = %d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "--output-policy must stay within") {
		t.Fatalf("stderr %q missing output-root containment failure", stderr)
	}
}

func TestRunMetadataModeRejectsResolvedCredentialSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	policyPath := filepath.Join(root, "policy.toml")
	writePolicy(t, policyPath, strings.Join([]string{
		"version = 1",
		"[credentials.claude_auth]",
		`resolver = "claude-macos-keychain"`,
		`materialization = "ephemeral"`,
	}, "\n")+"\n")

	outputRoot := filepath.Join(root, "out")
	if err := os.MkdirAll(outputRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	escapeRoot := filepath.Join(root, "escape")
	if err := os.MkdirAll(escapeRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(escapeRoot, filepath.Join(outputRoot, "resolved")); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runResolveCredentialSources(t, []string{
		"--policy", policyPath,
		"--agent", "claude",
		"--mode", "strict",
		"--resolution-mode", "metadata",
		"--output-policy", filepath.Join(outputRoot, "resolved-policy.toml"),
		"--output-metadata", filepath.Join(outputRoot, "resolver-metadata.json"),
		"--output-root", outputRoot,
	}, nil)
	if code != 1 {
		t.Fatalf("Run() = %d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("unexpected stdout %q", stdout)
	}
	if _, err := os.Stat(filepath.Join(escapeRoot, "credentials", "claude_auth.json")); !os.IsNotExist(err) {
		t.Fatalf("credential file unexpectedly materialized outside output root: %v", err)
	}
}

func TestRunDropsProviderFilteredResolverEntries(t *testing.T) {
	root := t.TempDir()
	policyPath := filepath.Join(root, "policy.toml")
	writePolicy(t, policyPath, strings.Join([]string{
		"version = 1",
		"[credentials.claude_auth]",
		`resolver = "claude-macos-keychain"`,
		`materialization = "ephemeral"`,
		`providers = ["codex"]`,
	}, "\n")+"\n")

	outputRoot := filepath.Join(root, "out")
	if err := os.MkdirAll(outputRoot, 0o700); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runResolveCredentialSources(t, []string{
		"--policy", policyPath,
		"--agent", "claude",
		"--mode", "strict",
		"--resolution-mode", "metadata",
		"--output-policy", filepath.Join(outputRoot, "resolved-policy.toml"),
		"--output-metadata", filepath.Join(outputRoot, "resolver-metadata.json"),
		"--output-root", outputRoot,
	}, nil)
	if code != 0 {
		t.Fatalf("Run() = %d stdout=%q stderr=%q", code, stdout, stderr)
	}

	resolvedOutputRoot, err := filepath.EvalSymlinks(outputRoot)
	if err != nil {
		t.Fatal(err)
	}
	resolvedPolicy := snapshotFile(t, filepath.Join(resolvedOutputRoot, "resolved-policy.toml"))
	if string(resolvedPolicy.data) != "version = 1\n" {
		t.Fatalf("unexpected resolved policy:\n%s", resolvedPolicy.data)
	}
	metadata := readJSON(t, filepath.Join(resolvedOutputRoot, "resolver-metadata.json"))
	if got := metadata["credential_input_kinds"].(map[string]any); len(got) != 0 {
		t.Fatalf("expected filtered metadata input kinds to be empty, got %#v", got)
	}
}

func TestRunRejectsInvalidConfigurations(t *testing.T) {
	cases := []struct {
		name         string
		policy       string
		agent        string
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
			agent:        "claude",
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
			agent:        "claude",
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
			agent:        "codex",
			wantContains: "credentials.codex_auth.providers contains unsupported value: bogus",
		},
		{
			name: "resolver-materialization-must-stay-ephemeral",
			policy: strings.Join([]string{
				"version = 1",
				"[credentials.claude_auth]",
				`resolver = "claude-macos-keychain"`,
				`materialization = "persistent"`,
			}, "\n") + "\n",
			agent:        "claude",
			wantContains: "credentials.claude_auth.materialization must stay ephemeral for resolver-backed auth",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			policyPath := filepath.Join(root, "policy.toml")
			writePolicy(t, policyPath, tc.policy)
			outputRoot := filepath.Join(root, "out")
			if err := os.MkdirAll(outputRoot, 0o700); err != nil {
				t.Fatal(err)
			}

			code, stdout, stderr := runResolveCredentialSources(t, []string{
				"--policy", policyPath,
				"--agent", tc.agent,
				"--mode", "strict",
				"--resolution-mode", "metadata",
				"--output-policy", filepath.Join(outputRoot, "resolved-policy.toml"),
				"--output-metadata", filepath.Join(outputRoot, "resolver-metadata.json"),
				"--output-root", outputRoot,
			}, nil)
			if code != 1 {
				t.Fatalf("Run() = %d stdout=%q stderr=%q", code, stdout, stderr)
			}
			if !strings.Contains(stderr, tc.wantContains) {
				t.Fatalf("stderr %q does not contain %q", stderr, tc.wantContains)
			}
		})
	}
}
