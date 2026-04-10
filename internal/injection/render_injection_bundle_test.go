// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package injection

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseTOMLSubsetAndHelperErrors(t *testing.T) {
	t.Parallel()

	policyPath := Path("/tmp/policy.toml")
	parsed, err := parseTOMLSubset(strings.Join([]string{
		"version = 1",
		`includes = ["shared.toml"]`,
		"[documents]",
		`common = "common.md"`,
		"[credentials]",
		`codex_auth = "auth.json"`,
		"[ssh]",
		`enabled = true`,
		`identities = ["id_ed25519"]`,
		"[[copies]]",
		`source = "file.txt"`,
		`target = "/state/injected/file.txt"`,
		`classification = "public"`,
	}, "\n"), policyPath)
	if err != nil {
		t.Fatalf("parseTOMLSubset error: %v", err)
	}
	if parsed["version"] != 1 {
		t.Fatalf("version = %#v", parsed["version"])
	}
	if got := parsed["includes"].([]any); len(got) != 1 || got[0] != "shared.toml" {
		t.Fatalf("includes = %#v", parsed["includes"])
	}
	if got := parsed["documents"].(map[string]any)["common"]; got != "common.md" {
		t.Fatalf("documents.common = %#v", got)
	}
	if got := parsed["credentials"].(map[string]any)["codex_auth"]; got != "auth.json" {
		t.Fatalf("credentials.codex_auth = %#v", got)
	}
	if got := parsed["ssh"].(map[string]any)["enabled"]; got != true {
		t.Fatalf("ssh.enabled = %#v", got)
	}
	if got := parsed["copies"].([]any); len(got) != 1 {
		t.Fatalf("copies = %#v", parsed["copies"])
	}

	scoped, err := parseTOMLSubset(strings.Join([]string{
		"[credentials.codex_auth]",
		`source = "auth.json"`,
		`providers = ["codex"]`,
		`modes = ["strict"]`,
	}, "\n"), policyPath)
	if err != nil {
		t.Fatalf("parseTOMLSubset scoped error: %v", err)
	}
	if got := scoped["credentials"].(map[string]any)["codex_auth"].(map[string]any)["providers"]; !reflect.DeepEqual(got, []any{"codex"}) {
		t.Fatalf("scoped providers = %#v", got)
	}

	for _, content := range []string{
		"[[ssh]]\nfoo = \"bar\"\n",
		"= \"bar\"\n",
		"nope\n",
		`credentials.gemini_env = "gemini.env"` + "\n",
		"[documents]\n[documents]\n",
	} {
		content := content
		t.Run(strings.ReplaceAll(strings.TrimSpace(content), "\n", " "), func(t *testing.T) {
			t.Parallel()
			if _, err := parseTOMLSubset(content, policyPath); err == nil {
				t.Fatalf("expected parseTOMLSubset to fail for %q", content)
			}
		})
	}
}

func TestLoadPolicyBundleMergesIncludesRebasesAndRejectsBadIncludes(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	fragments := filepath.Join(root, "fragments")
	if err := os.MkdirAll(fragments, 0o755); err != nil {
		t.Fatal(err)
	}
	writeText(t, filepath.Join(fragments, "common.md"), "common\n", 0o600)
	writeText(t, filepath.Join(fragments, "copy.txt"), "copy\n", 0o600)
	writeText(t, filepath.Join(fragments, "auth.json"), "{}\n", 0o600)
	writeText(t, filepath.Join(fragments, "ssh_config"), "Host github.com\n", 0o600)
	writeText(t, filepath.Join(fragments, "id_workcell"), "private\n", 0o600)
	writeText(t, filepath.Join(fragments, "fragment.toml"), strings.Join([]string{
		"[documents]",
		`common = "common.md"`,
		"[[copies]]",
		`source = "copy.txt"`,
		`target = "/state/injected/copy.txt"`,
		`classification = "public"`,
		"[ssh]",
		`enabled = true`,
		`config = "ssh_config"`,
		`identities = ["id_workcell"]`,
		"[credentials]",
		`codex_auth = "auth.json"`,
	}, "\n"), 0o600)
	policyPath := filepath.Join(root, "policy.toml")
	writeText(t, policyPath, strings.Join([]string{
		"version = 1",
		`includes = ["fragments/fragment.toml"]`,
		"[credentials]",
		`github_hosts = "/tmp/hosts.yml"`,
	}, "\n"), 0o600)

	merged, sources, err := loadPolicyBundle(Path(policyPath))
	if err != nil {
		t.Fatalf("loadPolicyBundle error: %v", err)
	}
	wantDoc, err := filepath.EvalSymlinks(filepath.Join(fragments, "common.md"))
	if err != nil {
		t.Fatal(err)
	}
	if got := merged["documents"].(map[string]any)["common"]; got != wantDoc {
		t.Fatalf("documents.common = %v want %v", got, wantDoc)
	}
	wantCopy, err := filepath.EvalSymlinks(filepath.Join(fragments, "copy.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if got := merged["copies"].([]any)[0].(map[string]any)["source"]; got != wantCopy {
		t.Fatalf("copies[0].source = %v want %v", got, wantCopy)
	}
	wantConfig, err := filepath.EvalSymlinks(filepath.Join(fragments, "ssh_config"))
	if err != nil {
		t.Fatal(err)
	}
	if got := merged["ssh"].(map[string]any)["config"]; got != wantConfig {
		t.Fatalf("ssh.config = %v want %v", got, wantConfig)
	}
	resolvedAuth, err := filepath.EvalSymlinks(filepath.Join(fragments, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got := merged["credentials"].(map[string]any)["codex_auth"]; got != resolvedAuth {
		t.Fatalf("credentials.codex_auth = %v", got)
	}
	if got := []string{sources[0].Path, sources[1].Path}; !reflect.DeepEqual(got, []string{"fragments/fragment.toml", "policy.toml"}) {
		t.Fatalf("policy sources = %#v", got)
	}
	if !strings.HasPrefix(compositePolicySHA256(sources), "sha256:") {
		t.Fatalf("compositePolicySHA256 missing hash prefix")
	}

	overridePath := filepath.Join(root, "policy-metadata.json")
	writeText(t, overridePath, strings.Join([]string{
		`{"policy_entrypoint":"policy.toml","policy_sources":[`,
		`{"path":"policy.toml","sha256":"sha256:original"}`,
		`]}`,
	}, ""), 0o600)
	entrypoint, overrideSources, err := loadPolicyMetadataOverride(overridePath)
	if err != nil {
		t.Fatalf("loadPolicyMetadataOverride error: %v", err)
	}
	if entrypoint != "policy.toml" || len(overrideSources) != 1 || overrideSources[0].Path != "policy.toml" {
		t.Fatalf("unexpected override metadata: %#v %#v", entrypoint, overrideSources)
	}

	writeText(t, filepath.Join(root, "shared.toml"), "version = 1\n", 0o600)
	outside := filepath.Join(filepath.Dir(root), "escape.toml")
	writeText(t, outside, "version = 1\n", 0o600)

	badCases := []struct {
		name   string
		policy string
		want   string
	}{
		{
			name: "non-list includes",
			policy: strings.Join([]string{
				"version = 1",
				`includes = "shared.toml"`,
			}, "\n"),
			want: "includes must be an array of strings",
		},
		{
			name: "duplicate include file",
			policy: strings.Join([]string{
				"version = 1",
				`includes = ["shared.toml", "shared.toml"]`,
			}, "\n"),
			want: "same file more than once",
		},
		{
			name: "escape include",
			policy: strings.Join([]string{
				"version = 1",
				`includes = ["../escape.toml"]`,
			}, "\n"),
			want: "must stay within",
		},
	}
	for _, tc := range badCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(root, tc.name+".toml")
			writeText(t, p, tc.policy, 0o600)
			if _, _, err := loadPolicyBundle(Path(p)); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %q error, got %v", tc.want, err)
			}
		})
	}
}

func TestRebasePolicyFragmentAndValidationHelpers(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	fragmentDir := filepath.Join(root, "fragment")
	if err := os.MkdirAll(fragmentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeText(t, filepath.Join(fragmentDir, "common.md"), "common\n", 0o600)
	writeText(t, filepath.Join(fragmentDir, "copy.txt"), "copy\n", 0o600)
	writeText(t, filepath.Join(fragmentDir, "ssh_config"), "Host github.com\n", 0o600)
	writeText(t, filepath.Join(fragmentDir, "known_hosts"), "github.com ssh-ed25519 AAAA\n", 0o600)
	writeText(t, filepath.Join(fragmentDir, "id_workcell"), "private\n", 0o600)
	writeText(t, filepath.Join(fragmentDir, "auth.json"), "{}\n", 0o600)

	rebased := rebasePolicyFragment(map[string]any{
		"documents": map[string]any{"common": "common.md"},
		"copies": []any{
			map[string]any{"source": "copy.txt", "target": "/state/injected/copy.txt"},
			"literal",
		},
		"ssh": map[string]any{
			"config":      "ssh_config",
			"known_hosts": "known_hosts",
			"identities":  []any{"id_workcell"},
		},
		"credentials": map[string]any{
			"codex_auth": "auth.json",
			"github_hosts": map[string]any{
				"source":    "hosts.yml",
				"providers": []any{"codex"},
			},
		},
	}, Path(fragmentDir))

	want := func(relative string) string {
		return filepath.Join(fragmentDir, relative)
	}
	if got := rebased["documents"].(map[string]any)["common"]; got != want("common.md") {
		t.Fatalf("documents.common = %v", got)
	}
	if got := rebased["copies"].([]any)[0].(map[string]any)["source"]; got != want("copy.txt") {
		t.Fatalf("copies[0].source = %v", got)
	}
	if got := rebased["copies"].([]any)[1]; got != "literal" {
		t.Fatalf("copies[1] = %v", got)
	}
	if got := rebased["ssh"].(map[string]any)["config"]; got != want("ssh_config") {
		t.Fatalf("ssh.config = %v", got)
	}
	if got := rebased["ssh"].(map[string]any)["known_hosts"]; got != want("known_hosts") {
		t.Fatalf("ssh.known_hosts = %v", got)
	}
	if got := rebased["ssh"].(map[string]any)["identities"].([]any)[0]; got != want("id_workcell") {
		t.Fatalf("ssh.identities[0] = %v", got)
	}
	if got := rebased["credentials"].(map[string]any)["codex_auth"]; got != want("auth.json") {
		t.Fatalf("credentials.codex_auth = %v", got)
	}
	if got := rebased["credentials"].(map[string]any)["github_hosts"].(map[string]any)["source"]; got != want("hosts.yml") {
		t.Fatalf("credentials.github_hosts.source = %v", got)
	}

	if ok, err := selectedFor(nil, "codex", "providers", supportedAgents); err != nil || !ok {
		t.Fatalf("selectedFor(nil) = %v, %v", ok, err)
	}
	ok, err := selectedFor([]any{"claude"}, "codex", "providers", supportedAgents)
	if err != nil {
		t.Fatalf("selectedFor filter error: %v", err)
	}
	if ok {
		t.Fatal("selectedFor filter unexpectedly matched")
	}
	if _, err := selectedFor([]any{"codex", 1}, "codex", "providers", supportedAgents); err == nil {
		t.Fatal("selectedFor accepted non-string value")
	}
	if err := validateAllowedKeys(map[string]any{"a": 1}, map[string]struct{}{"a": {}}, "table"); err != nil {
		t.Fatalf("validateAllowedKeys error: %v", err)
	}
	if err := validateAllowedKeys(map[string]any{"a": 1, "b": 2}, map[string]struct{}{"a": {}}, "table"); err == nil {
		t.Fatal("validateAllowedKeys accepted unsupported key")
	}

	target := normalizeContainerTarget("~/notes.txt")
	if target != "/state/agent-home/notes.txt" {
		t.Fatalf("normalizeContainerTarget = %s", target)
	}
	if got := normalizeContainerTarget("~/../injected/notes.txt"); got != "/state/agent-home/../injected/notes.txt" {
		t.Fatalf("normalizeContainerTarget home traversal = %s", got)
	}
	if got := normalizeContainerTarget("/state/injected/../agent-home/notes.txt"); got != "/state/injected/../agent-home/notes.txt" {
		t.Fatalf("normalizeContainerTarget traversal = %s", got)
	}
	if got, err := validateContainerTarget("/state/injected/notes.txt"); err != nil || got != "/state/injected/notes.txt" {
		t.Fatalf("validateContainerTarget = %q, %v", got, err)
	}
	for _, bad := range []string{"relative.txt", "/tmp/outside", "/state/agent-home/.mcp.json", "/state/injected/../agent-home/notes.txt", "/state/agent-home/../injected/notes.txt"} {
		if _, err := validateContainerTarget(bad); err == nil {
			t.Fatalf("validateContainerTarget accepted %q", bad)
		}
	}
}

func TestRenderDocumentsRejectsUnknownKeys(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	output := filepath.Join(root, "bundle")
	if err := os.MkdirAll(output, 0o700); err != nil {
		t.Fatal(err)
	}
	writeText(t, filepath.Join(root, "common.md"), "common\n", 0o600)

	_, err := renderDocuments(map[string]any{
		"documents": map[string]any{
			"common": "common.md",
			"extra":  "common.md",
		},
	}, Path(output), Path(root))
	if err == nil {
		t.Fatal("renderDocuments() unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "unsupported keys: extra") {
		t.Fatalf("renderDocuments() error = %v, want unknown key failure", err)
	}
}

func TestRenderHelpersAndManifestParity(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	output := filepath.Join(root, "bundle")
	if err := os.MkdirAll(output, 0o700); err != nil {
		t.Fatal(err)
	}

	writeText(t, filepath.Join(root, "common.md"), "common\n", 0o600)
	writeText(t, filepath.Join(root, "codex.md"), "codex\n", 0o600)
	writeText(t, filepath.Join(root, "public.txt"), "public\n", 0o600)
	writeText(t, filepath.Join(root, "secret.txt"), "secret\n", 0o600)
	writeText(t, filepath.Join(root, "public-dir", "note.txt"), "note\n", 0o600)
	writeText(t, filepath.Join(root, "secret-dir", "token.txt"), "token\n", 0o600)
	if err := os.Chmod(filepath.Join(root, "public-dir"), 0o755); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(root, "secret-dir"), 0o700); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	writeText(t, filepath.Join(root, "ssh-config"), "Host github.com\n  IdentityFile ~/.ssh/id_ed25519\n", 0o600)
	writeText(t, filepath.Join(root, "unsafe-ssh-config"), "Host *\nProxyCommand nc %h %p\n", 0o600)
	writeText(t, filepath.Join(root, "forwardagent-ssh-config"), "Host *\nForwardAgent yes\n", 0o600)
	writeText(t, filepath.Join(root, "sendenv-ssh-config"), "Host *\nSendEnv FOO\n", 0o600)
	writeText(t, filepath.Join(root, "known_hosts"), "github.com ssh-ed25519 AAAA\n", 0o644)
	writeText(t, filepath.Join(root, "id_a"), "key-a\n", 0o600)
	writeText(t, filepath.Join(root, "id_b"), "key-b\n", 0o600)
	writeText(t, filepath.Join(root, "claude-auth.json"), `{"token":"claude"}`+"\n", 0o600)
	writeText(t, filepath.Join(root, "world-readable-auth.json"), `{"token":"public"}`+"\n", 0o644)
	writeText(t, filepath.Join(root, "claude-mcp.json"), `{"mcpServers":{}}`+"\n", 0o600)
	writeText(t, filepath.Join(root, "gemini-projects.json"), `{"projects":{}}`+"\n", 0o600)
	writeText(t, filepath.Join(root, "gemini.env"), "GOOGLE_GENAI_USE_GCA=true\n", 0o600)
	writeText(t, filepath.Join(root, "gemini-invalid.env"), "GOOGLE_API_KEY=test\n", 0o600)

	documents, err := renderDocuments(map[string]any{
		"documents": map[string]any{
			"common": "common.md",
			"codex":  "codex.md",
		},
	}, Path(output), Path(root))
	if err != nil {
		t.Fatalf("renderDocuments error: %v", err)
	}
	if documents["common"] != "documents/common.md" || documents["codex"] != "documents/codex.md" {
		t.Fatalf("renderDocuments = %#v", documents)
	}
	if got := readText(t, filepath.Join(output, "documents", "common.md")); got != "common\n" {
		t.Fatalf("staged document content = %q", got)
	}
	assertRenderFileMode(t, filepath.Join(output, "documents", "common.md"), 0o600)

	copies, err := renderCopies(map[string]any{
		"copies": []any{
			map[string]any{
				"source":         "public.txt",
				"target":         "/state/injected/public.txt",
				"classification": "public",
			},
			map[string]any{
				"source":         "secret.txt",
				"target":         "~/.config/workcell/token.txt",
				"classification": "secret",
			},
			map[string]any{
				"source":         "public-dir",
				"target":         "/state/injected/public-dir",
				"classification": "public",
			},
			map[string]any{
				"source":         "secret-dir",
				"target":         "~/.config/workcell/secrets",
				"classification": "secret",
			},
			map[string]any{
				"source":         "secret.txt",
				"target":         "/state/injected/skipped.txt",
				"classification": "public",
				"providers":      []any{"claude"},
			},
		},
	}, Path(output), Path(root), "codex", "strict")
	if err != nil {
		t.Fatalf("renderCopies error: %v", err)
	}
	if len(copies) != 4 {
		t.Fatalf("renderCopies len = %d", len(copies))
	}
	if copies[0]["source"] != "copies/0" {
		t.Fatalf("renderCopies public source = %#v", copies[0]["source"])
	}
	if got := copies[1]["source"].(map[string]string)["mount_path"]; got != "/opt/workcell/host-inputs/copies/1" {
		t.Fatalf("renderCopies secret mount_path = %q", got)
	}
	if kind := copies[2]["kind"]; kind != "dir" {
		t.Fatalf("renderCopies public dir kind = %#v", kind)
	}
	if kind := copies[3]["kind"]; kind != "dir" {
		t.Fatalf("renderCopies secret dir kind = %#v", kind)
	}
	assertRenderFileMode(t, filepath.Join(output, "copies", "0"), 0o600)
	assertRenderFileMode(t, filepath.Join(output, "copies", "2"), 0o700)
	if got := readText(t, filepath.Join(output, "copies", "2", "note.txt")); got != "note\n" {
		t.Fatalf("staged directory content = %q", got)
	}

	sshRendered, err := renderSSH(map[string]any{
		"ssh": map[string]any{
			"enabled":             true,
			"config":              "ssh-config",
			"known_hosts":         "known_hosts",
			"identities":          []any{"id_a", "id_b"},
			"allow_unsafe_config": true,
		},
	}, Path(output), Path(root), "codex", "strict")
	if err != nil {
		t.Fatalf("renderSSH error: %v", err)
	}
	if sshRendered["config_assurance"] != "lower-assurance-unsafe-config" {
		t.Fatalf("renderSSH config_assurance = %#v", sshRendered["config_assurance"])
	}
	if len(sshRendered["identities"].([]map[string]any)) != 2 {
		t.Fatalf("renderSSH identities = %#v", sshRendered["identities"])
	}
	if _, err := renderSSH(map[string]any{
		"ssh": map[string]any{
			"enabled": true,
			"config":  "unsafe-ssh-config",
		},
	}, Path(output), Path(root), "codex", "strict"); err == nil || !strings.Contains(err.Error(), "unsafe directive") {
		t.Fatalf("renderSSH unsafe config error = %v", err)
	}
	for _, fileName := range []string{"forwardagent-ssh-config", "sendenv-ssh-config"} {
		if _, err := renderSSH(map[string]any{
			"ssh": map[string]any{
				"enabled": true,
				"config":  fileName,
			},
		}, Path(output), Path(root), "codex", "strict"); err == nil || !strings.Contains(err.Error(), "unsafe directive") {
			t.Fatalf("renderSSH %s error = %v", fileName, err)
		}
	}

	claudeCredentials, err := renderCredentials(map[string]any{
		"credentials": map[string]any{
			"claude_auth": "claude-auth.json",
			"claude_mcp":  "claude-mcp.json",
			"github_hosts": map[string]any{
				"source":    "claude-auth.json",
				"providers": []any{"claude"},
			},
		},
	}, Path(root), "claude", "strict")
	if err != nil {
		t.Fatalf("renderCredentials claude error: %v", err)
	}
	if _, ok := claudeCredentials["claude_auth"]; !ok {
		t.Fatalf("renderCredentials missing claude_auth: %#v", claudeCredentials)
	}
	if _, ok := claudeCredentials["claude_mcp"]; !ok {
		t.Fatalf("renderCredentials missing claude_mcp: %#v", claudeCredentials)
	}
	if _, ok := claudeCredentials["github_hosts"]; !ok {
		t.Fatalf("renderCredentials missing github_hosts: %#v", claudeCredentials)
	}

	geminiCredentials, err := renderCredentials(map[string]any{
		"credentials": map[string]any{
			"gemini_env":      "gemini.env",
			"gemini_projects": "gemini-projects.json",
		},
	}, Path(root), "gemini", "strict")
	if err != nil {
		t.Fatalf("renderCredentials gemini error: %v", err)
	}
	if _, ok := geminiCredentials["gemini_env"]; !ok {
		t.Fatalf("renderCredentials missing gemini_env: %#v", geminiCredentials)
	}
	if _, ok := geminiCredentials["gemini_projects"]; !ok {
		t.Fatalf("renderCredentials missing gemini_projects: %#v", geminiCredentials)
	}
	if got := deriveCredentialExtraEndpoints(geminiCredentials); !reflect.DeepEqual(got, []string{"accounts.google.com:443", "oauth2.googleapis.com:443", "sts.googleapis.com:443"}) {
		t.Fatalf("deriveCredentialExtraEndpoints = %#v", got)
	}

	if _, err := renderCredentials(map[string]any{
		"credentials": map[string]any{
			"gemini_env": "gemini-invalid.env",
		},
	}, Path(root), "gemini", "strict"); err == nil {
		t.Fatal("renderCredentials accepted invalid gemini env unexpectedly")
	}
	if _, err := renderCredentials(map[string]any{
		"credentials": map[string]any{
			"codex_auth": "world-readable-auth.json",
		},
	}, Path(root), "codex", "strict"); err == nil || !strings.Contains(err.Error(), "group/world-accessible") {
		t.Fatalf("renderCredentials world-readable secret error = %v", err)
	}
}

func TestRunRenderInjectionBundleWritesManifestAndTracksMaterialChanges(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	policyPath := filepath.Join(root, "policy.toml")
	outputA := filepath.Join(root, "bundle-a")
	outputB := filepath.Join(root, "bundle-b")
	writeText(t, filepath.Join(root, "common.md"), "common\n", 0o600)
	credentialPath := filepath.Join(root, "auth.json")
	writeText(t, credentialPath, `{"token":"one"}`+"\n", 0o600)
	writeText(t, policyPath, strings.Join([]string{
		"version = 1",
		"[documents]",
		`common = "common.md"`,
		"[credentials]",
		`codex_auth = "auth.json"`,
	}, "\n"), 0o600)

	if err := RunRenderInjectionBundle(policyPath, "codex", "strict", outputA, ""); err != nil {
		t.Fatalf("RunRenderInjectionBundle outputA error: %v", err)
	}
	manifestA := readManifest(t, filepath.Join(outputA, "manifest.json"))

	writeText(t, credentialPath, `{"token":"two"}`+"\n", 0o600)
	if err := RunRenderInjectionBundle(policyPath, "codex", "strict", outputB, ""); err != nil {
		t.Fatalf("RunRenderInjectionBundle outputB error: %v", err)
	}
	manifestB := readManifest(t, filepath.Join(outputB, "manifest.json"))

	metaA := manifestA["metadata"].(map[string]any)
	metaB := manifestB["metadata"].(map[string]any)
	if metaA["policy_sha256"] == metaB["policy_sha256"] {
		t.Fatalf("policy hash did not change after credential mutation: %v", metaA["policy_sha256"])
	}
	if metaA["ssh_enabled"] != false || metaA["ssh_config_assurance"] != "off" {
		t.Fatalf("unexpected ssh metadata: %#v", metaA)
	}

	overridePath := filepath.Join(root, "policy-metadata.json")
	writeText(t, overridePath, strings.Join([]string{
		`{"policy_entrypoint":"policy.toml","policy_sources":[`,
		`{"path":"policy.toml","sha256":"sha256:original"}`,
		`]}`,
	}, ""), 0o600)
	outputC := filepath.Join(root, "bundle-c")
	if err := RunRenderInjectionBundle(policyPath, "codex", "strict", outputC, overridePath); err != nil {
		t.Fatalf("RunRenderInjectionBundle override error: %v", err)
	}
	manifestC := readManifest(t, filepath.Join(outputC, "manifest.json"))
	metaC := manifestC["metadata"].(map[string]any)
	if metaC["policy_entrypoint"] != "policy.toml" {
		t.Fatalf("policy_entrypoint = %#v", metaC["policy_entrypoint"])
	}
	sources := metaC["policy_sources"].([]any)
	if len(sources) != 1 || sources[0].(map[string]any)["path"] != "policy.toml" {
		t.Fatalf("policy_sources = %#v", metaC["policy_sources"])
	}
}

func writeText(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func readText(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func assertRenderFileMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("mode mismatch for %s: got %04o want %04o", path, got, want)
	}
}

func readManifest(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	return manifest
}
