package policybundle

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestStripCommentAndParseValue(t *testing.T) {
	t.Parallel()

	if got := StripComment(`value = "keep # hash" # remove`); got != `value = "keep # hash"` {
		t.Fatalf("StripComment = %q", got)
	}
	if got := StripComment(`value = 'keep # hash' # remove`); got != `value = 'keep # hash'` {
		t.Fatalf("StripComment = %q", got)
	}
	if got := StripComment(`value = "escaped \"#\" hash" # remove`); got != `value = "escaped \"#\" hash"` {
		t.Fatalf("StripComment = %q", got)
	}

	policyPath := "/tmp/policy.toml"
	cases := []struct {
		raw     string
		want    any
		wantErr bool
		lineNo  int
	}{
		{raw: "true", want: true, lineNo: 1},
		{raw: "false", want: false, lineNo: 2},
		{raw: `"text"`, want: "text", lineNo: 3},
		{raw: `["a", "b"]`, want: []string{"a", "b"}, lineNo: 4},
		{raw: "42", want: 42, lineNo: 5},
		{raw: "1.5", wantErr: true, lineNo: 6},
		{raw: "[1, 2]", wantErr: true, lineNo: 7},
		{raw: "", wantErr: true, lineNo: 8},
	}
	for _, tc := range cases {
		got, err := ParseValue(tc.raw, policyPath, tc.lineNo)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("ParseValue(%q) expected error", tc.raw)
			}
			continue
		}
		if err != nil {
			t.Fatalf("ParseValue(%q) error: %v", tc.raw, err)
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("ParseValue(%q) = %#v, want %#v", tc.raw, got, tc.want)
		}
	}
}

func TestParseRenderAndReparsePolicySubset(t *testing.T) {
	t.Parallel()

	rendered, err := RenderPolicyTOML(map[string]any{
		"version":  1,
		"includes": []string{"shared.toml"},
		"documents": map[string]any{
			"common": "/tmp/common.md",
		},
		"credentials": map[string]any{
			"codex_auth": "/tmp/auth.json",
			"github_hosts": map[string]any{
				"source":    "/tmp/hosts.yml",
				"providers": []string{"codex"},
			},
		},
		"ssh": map[string]any{
			"enabled":     true,
			"config":      "/tmp/config",
			"known_hosts": "/tmp/known_hosts",
			"identities":  []string{"/tmp/id_key"},
			"proxy_jump":  "bastion",
		},
		"copies": []map[string]any{
			{
				"source":         "/tmp/source.txt",
				"target":         "/state/injected/source.txt",
				"classification": "public",
				"note":           "extra",
			},
		},
	})
	if err != nil {
		t.Fatalf("RenderPolicyTOML error: %v", err)
	}

	want := strings.Join([]string{
		"version = 1",
		`includes = ["shared.toml"]`,
		"",
		"[documents]",
		`common = "/tmp/common.md"`,
		"",
		"[credentials]",
		`codex_auth = "/tmp/auth.json"`,
		"",
		"[credentials.github_hosts]",
		`providers = ["codex"]`,
		`source = "/tmp/hosts.yml"`,
		"",
		"[ssh]",
		`enabled = true`,
		`config = "/tmp/config"`,
		`known_hosts = "/tmp/known_hosts"`,
		`identities = ["/tmp/id_key"]`,
		`proxy_jump = "bastion"`,
		"",
		"[[copies]]",
		`source = "/tmp/source.txt"`,
		`target = "/state/injected/source.txt"`,
		`classification = "public"`,
		`note = "extra"`,
		"",
	}, "\n")
	if rendered != want {
		t.Fatalf("RenderPolicyTOML mismatch\nwant:\n%s\ngot:\n%s", want, rendered)
	}

	reparsed, err := ParseTOMLSubset(rendered, "/tmp/policy.toml")
	if err != nil {
		t.Fatalf("ParseTOMLSubset error: %v", err)
	}
	if reparsed["version"] != 1 {
		t.Fatalf("version = %#v", reparsed["version"])
	}
	if reparsed["includes"].([]string)[0] != "shared.toml" {
		t.Fatalf("includes = %#v", reparsed["includes"])
	}
	if reparsed["documents"].(map[string]any)["common"] != "/tmp/common.md" {
		t.Fatalf("documents = %#v", reparsed["documents"])
	}
	if reparsed["credentials"].(map[string]any)["codex_auth"] != "/tmp/auth.json" {
		t.Fatalf("credentials = %#v", reparsed["credentials"])
	}
}

func TestLoadPolicyBundleMergesIncludesAndRebasesPaths(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	fragmentDir := filepath.Join(root, "fragment")
	if err := os.MkdirAll(fragmentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, filepath.Join(fragmentDir, "doc.md"), "doc\n", 0o600)
	mustWriteFile(t, filepath.Join(fragmentDir, "auth.json"), "{}\n", 0o600)
	mustWriteFile(t, filepath.Join(fragmentDir, "shared.toml"), strings.Join([]string{
		"[documents]",
		`common = "doc.md"`,
		"",
		"[credentials.codex_auth]",
		`source = "auth.json"`,
		`providers = ["codex"]`,
		`modes = ["strict"]`,
		"",
	}, "\n"), 0o600)
	mustWriteFile(t, filepath.Join(root, "policy.toml"), strings.Join([]string{
		"version = 1",
		`includes = ["fragment/shared.toml"]`,
		"",
		"[credentials]",
		`github_hosts = "/tmp/hosts.yml"`,
		"",
	}, "\n"), 0o600)

	merged, sources, err := LoadPolicyBundle(filepath.Join(root, "policy.toml"))
	if err != nil {
		t.Fatalf("LoadPolicyBundle error: %v", err)
	}
	resolvedDoc, err := filepath.EvalSymlinks(filepath.Join(fragmentDir, "doc.md"))
	if err != nil {
		t.Fatal(err)
	}
	if got := merged["documents"].(map[string]any)["common"]; got != resolvedDoc {
		t.Fatalf("documents.common = %v", got)
	}
	resolvedAuth, err := filepath.EvalSymlinks(filepath.Join(fragmentDir, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got := merged["credentials"].(map[string]any)["codex_auth"].(map[string]any)["source"]; got != resolvedAuth {
		t.Fatalf("credentials.codex_auth.source = %v", got)
	}
	if got := merged["credentials"].(map[string]any)["github_hosts"]; got != "/tmp/hosts.yml" {
		t.Fatalf("credentials.github_hosts = %v", got)
	}
	if got := []string{sources[0].Path, sources[1].Path}; !reflect.DeepEqual(got, []string{"fragment/shared.toml", "policy.toml"}) {
		t.Fatalf("policy sources = %#v", got)
	}
	if !strings.HasPrefix(sources[0].SHA256, "sha256:") || !strings.HasPrefix(sources[1].SHA256, "sha256:") {
		t.Fatalf("unexpected source hashes: %#v", sources)
	}
}

func TestLoadPolicyBundleRejectsCyclesAndDuplicateIncludes(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "invalid.toml"), "version = 1\nincludes = \"shared.toml\"\n", 0o600)
	if _, _, err := LoadPolicyBundle(filepath.Join(root, "invalid.toml")); err == nil || !strings.Contains(err.Error(), "includes must be an array of strings") {
		t.Fatalf("expected invalid includes error, got %v", err)
	}

	mustWriteFile(t, filepath.Join(root, "a.toml"), "version = 1\nincludes = [\"b.toml\"]\n", 0o600)
	mustWriteFile(t, filepath.Join(root, "b.toml"), "version = 1\nincludes = [\"a.toml\"]\n", 0o600)
	if _, _, err := LoadPolicyBundle(filepath.Join(root, "a.toml")); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected cycle error, got %v", err)
	}

	mustWriteFile(t, filepath.Join(root, "shared.toml"), "version = 1\n", 0o600)
	mustWriteFile(t, filepath.Join(root, "dup.toml"), "version = 1\nincludes = [\"shared.toml\", \"shared.toml\"]\n", 0o600)
	if _, _, err := LoadPolicyBundle(filepath.Join(root, "dup.toml")); err == nil || !strings.Contains(err.Error(), "more than once") {
		t.Fatalf("expected duplicate include error, got %v", err)
	}

	outside := filepath.Join(filepath.Dir(root), filepath.Base(root)+"-escape.toml")
	mustWriteFile(t, outside, "version = 1\n", 0o600)
	mustWriteFile(t, filepath.Join(root, "policy.toml"), "version = 1\nincludes = [\"../"+filepath.Base(outside)+"\"]\n", 0o600)
	if _, _, err := LoadPolicyBundle(filepath.Join(root, "policy.toml")); err == nil || !strings.Contains(err.Error(), "stay within") {
		t.Fatalf("expected include boundary error, got %v", err)
	}
}

func TestParseTOMLSubsetRejectsInvalidStructures(t *testing.T) {
	t.Parallel()

	policyPath := "/tmp/policy.toml"
	for _, content := range []string{
		"[[unknown]]\n",
		"[documents]\n[documents]\n",
		"[credentials.unknown]\nsource = \"/tmp/x\"\n",
		"[unknown]\nvalue = 1\n",
		"invalid-line\n",
		" = \"value\"\n",
		"a.b = \"value\"\n",
		"value = \"one\"\nvalue = \"two\"\n",
		"[[credentials.codex_auth]]\nsource = \"/tmp/x\"\n",
	} {
		content := content
		t.Run(strings.ReplaceAll(strings.TrimSpace(content), "\n", " "), func(t *testing.T) {
			t.Parallel()
			if _, err := ParseTOMLSubset(content, policyPath); err == nil {
				t.Fatalf("expected ParseTOMLSubset to fail for %q", content)
			}
		})
	}
}

func TestRebasePolicyFragmentRebasesAllSupportedPathFields(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	fragmentDir := filepath.Join(root, "fragment")
	if err := os.MkdirAll(fragmentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, filepath.Join(fragmentDir, "common.md"), "common\n", 0o600)
	mustWriteFile(t, filepath.Join(fragmentDir, "copy.txt"), "copy\n", 0o600)
	mustWriteFile(t, filepath.Join(fragmentDir, "ssh_config"), "Host github.com\n", 0o600)
	mustWriteFile(t, filepath.Join(fragmentDir, "known_hosts"), "github.com ssh-ed25519 AAAA\n", 0o600)
	mustWriteFile(t, filepath.Join(fragmentDir, "id_workcell"), "private\n", 0o600)
	mustWriteFile(t, filepath.Join(fragmentDir, "auth.json"), "{}\n", 0o600)

	rebased := RebasePolicyFragment(map[string]any{
		"documents": map[string]any{"common": "common.md"},
		"copies": []map[string]any{
			{"source": "copy.txt", "target": "/state/injected/copy.txt"},
		},
		"ssh": map[string]any{
			"config":      "ssh_config",
			"known_hosts": "known_hosts",
			"identities":  []string{"id_workcell"},
		},
		"credentials": map[string]any{
			"codex_auth": "auth.json",
			"github_hosts": map[string]any{
				"source":    "hosts.yml",
				"providers": []string{"codex"},
			},
		},
	}, fragmentDir)

	if got := rebased["documents"].(map[string]any)["common"]; got != filepath.Join(fragmentDir, "common.md") {
		t.Fatalf("documents.common = %v", got)
	}
	if got := rebased["copies"].([]map[string]any)[0]["source"]; got != filepath.Join(fragmentDir, "copy.txt") {
		t.Fatalf("copies[0].source = %v", got)
	}
	if got := rebased["ssh"].(map[string]any)["config"]; got != filepath.Join(fragmentDir, "ssh_config") {
		t.Fatalf("ssh.config = %v", got)
	}
	if got := rebased["ssh"].(map[string]any)["known_hosts"]; got != filepath.Join(fragmentDir, "known_hosts") {
		t.Fatalf("ssh.known_hosts = %v", got)
	}
	if got := rebased["ssh"].(map[string]any)["identities"].([]string)[0]; got != filepath.Join(fragmentDir, "id_workcell") {
		t.Fatalf("ssh.identities[0] = %v", got)
	}
	if got := rebased["credentials"].(map[string]any)["codex_auth"]; got != filepath.Join(fragmentDir, "auth.json") {
		t.Fatalf("credentials.codex_auth = %v", got)
	}
	if got := rebased["credentials"].(map[string]any)["github_hosts"].(map[string]any)["source"]; got != filepath.Join(fragmentDir, "hosts.yml") {
		t.Fatalf("credentials.github_hosts.source = %v", got)
	}
}

func TestLoadRawWriteAndSecretGuards(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if got, err := LoadRawPolicy(filepath.Join(root, "missing.toml")); err != nil || got["version"] != 1 {
		t.Fatalf("LoadRawPolicy missing = %#v, %v", got, err)
	}

	rawPath := filepath.Join(root, "raw.toml")
	mustWriteFile(t, rawPath, "[documents]\ncommon = \"doc.md\"\n", 0o600)
	loaded, err := LoadRawPolicy(rawPath)
	if err != nil {
		t.Fatalf("LoadRawPolicy error: %v", err)
	}
	if loaded["version"] != 1 {
		t.Fatalf("LoadRawPolicy version = %#v", loaded["version"])
	}

	policyPath := filepath.Join(root, "policy.toml")
	if err := WritePolicyFile(policyPath, map[string]any{"version": 1}); err != nil {
		t.Fatalf("WritePolicyFile error: %v", err)
	}
	if got := mustReadFile(t, policyPath); got != "version = 1\n" {
		t.Fatalf("WritePolicyFile content = %q", got)
	}
	assertMode(t, policyPath, 0o600)

	directory := filepath.Join(root, "directory")
	if err := os.Mkdir(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := RequireSecretFile(directory, "directory"); err == nil || !strings.Contains(err.Error(), "must point at a file") {
		t.Fatalf("expected directory rejection, got %v", err)
	}

	secret := filepath.Join(root, "secret.txt")
	mustWriteFile(t, secret, "secret\n", 0o600)
	link := filepath.Join(root, "link.txt")
	if err := os.Symlink(secret, link); err != nil {
		t.Fatal(err)
	}
	if _, err := RequireSecretFile(link, "link"); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected symlink rejection, got %v", err)
	}

	oldGetUID := getUID
	getUID = func() int { return oldGetUID() + 1 }
	defer func() { getUID = oldGetUID }()
	if _, err := RequireSecretFile(secret, "secret"); err == nil || !strings.Contains(err.Error(), "owned by uid") {
		t.Fatalf("expected owner rejection, got %v", err)
	}

	getUID = oldGetUID
	worldReadable := filepath.Join(root, "world.txt")
	mustWriteFile(t, worldReadable, "secret\n", 0o644)
	if _, err := RequireSecretFile(worldReadable, "world"); err == nil || !strings.Contains(err.Error(), "group/world-accessible") {
		t.Fatalf("expected permission rejection, got %v", err)
	}
}

func TestCompositePolicySHA256IsOrderIndependent(t *testing.T) {
	t.Parallel()

	sources := []PolicySource{
		{Path: "b.toml", SHA256: "sha256:b"},
		{Path: "a.toml", SHA256: "sha256:a"},
	}
	got := CompositePolicySHA256(sources)
	wantCanonical := "[{'path': 'a.toml', 'sha256': 'sha256:a'}, {'path': 'b.toml', 'sha256': 'sha256:b'}]"
	sum := sha256.Sum256([]byte(wantCanonical))
	want := "sha256:" + hex.EncodeToString(sum[:])
	if got != want {
		t.Fatalf("CompositePolicySHA256 = %s, want %s", got, want)
	}
}

func TestSelectedForAndValidateAllowedKeys(t *testing.T) {
	t.Parallel()

	if ok, err := SelectedFor(nil, "codex", "providers", SupportedAgents); err != nil || !ok {
		t.Fatalf("SelectedFor nil = %v, %v", ok, err)
	}
	if ok, err := SelectedFor([]string{"claude"}, "codex", "providers", SupportedAgents); err != nil || ok {
		t.Fatalf("SelectedFor filter = %v, %v", ok, err)
	}
	if _, err := SelectedFor([]any{"codex", 1}, "codex", "providers", SupportedAgents); err == nil || !strings.Contains(err.Error(), "values must be strings") {
		t.Fatalf("expected string list error, got %v", err)
	}

	if err := ValidateAllowedKeys(map[string]any{"version": 1}, map[string]struct{}{"version": {}}, "policy"); err != nil {
		t.Fatalf("ValidateAllowedKeys error: %v", err)
	}
	if err := ValidateAllowedKeys(map[string]any{"unexpected": true}, map[string]struct{}{"version": {}}, "policy"); err == nil || !strings.Contains(err.Error(), "unsupported keys") {
		t.Fatalf("expected unsupported key error, got %v", err)
	}
}

func mustWriteFile(t *testing.T, path string, content string, mode os.FileMode) {
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

func mustReadFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("mode mismatch for %s: got %04o want %04o", path, got, want)
	}
}
