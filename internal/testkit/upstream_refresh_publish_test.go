// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package testkit

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func publishGit(t *testing.T, dir string, env []string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), append([]string{"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null"}, env...)...)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

// publishFixture builds a checkout at base, a candidate directory that mutate
// produced, and a fake gh that answers only the two read-only queries.
type publishFixture struct {
	checkout, candidate, audit, path string
	base                             string
}

func newPublishFixture(t *testing.T, mutate func(checkout string), editMetadata func(m map[string]any)) publishFixture {
	t.Helper()
	for _, tool := range []string{"jq", "shasum", "base64"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed", tool)
		}
	}
	root := t.TempDir()
	checkout := filepath.Join(root, "checkout")
	if err := os.MkdirAll(checkout, 0o755); err != nil {
		t.Fatal(err)
	}
	publishGit(t, checkout, nil, "init", "-q")
	if err := os.WriteFile(filepath.Join(checkout, "plain.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(checkout, "tool.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	ident := []string{"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com"}
	publishGit(t, checkout, nil, "add", "-A")
	publishGit(t, checkout, ident, "-c", "commit.gpgsign=false", "commit", "-q", "-m", "base")
	base := publishGit(t, checkout, nil, "rev-parse", "HEAD")

	mutate(checkout)
	candidate := filepath.Join(root, "candidate")
	if err := os.MkdirAll(candidate, 0o755); err != nil {
		t.Fatal(err)
	}
	publishGit(t, checkout, nil, "add", "-A")
	patch := publishGitRaw(t, checkout, "diff", "--cached", "--binary", "--full-index", "--patch", "--no-ext-diff", "--no-color")
	index := filepath.Join(root, "index")
	treeEnv := []string{"GIT_INDEX_FILE=" + index}
	publishGit(t, checkout, treeEnv, "read-tree", "HEAD")
	publishGit(t, checkout, treeEnv, "add", "-A")
	tree := publishGit(t, checkout, treeEnv, "write-tree")
	if err := os.WriteFile(filepath.Join(candidate, "patch"), patch, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(candidate, "diffstat"), []byte(" stat\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(patch)
	metadata := map[string]any{
		"version": 1, "repository": "o/r", "workflow": "upstream-refresh", "run_id": 42,
		"run_url": "https://example.invalid/run", "artifact_name": "upstream-refresh-candidate",
		"base_ref": "refs/heads/main", "base_sha": base, "patch_sha256": hex.EncodeToString(digest[:]),
		"tree_oid": tree, "changed_files": []string{},
	}
	if editMetadata != nil {
		editMetadata(metadata)
	}
	raw, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(candidate, "metadata.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	// Return the checkout to the base state, as a fresh CI checkout is.
	publishGit(t, checkout, nil, "reset", "-q", "--hard")
	publishGit(t, checkout, nil, "clean", "-fdq")

	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	fakeGH := "#!/bin/sh\n" +
		"case \"$*\" in\n" +
		"  *git/ref/heads/main*) echo \"${FAKE_MAIN_SHA}\" ;;\n" +
		"  \"pr list\"*) echo \"\" ;;\n" +
		"  *) echo \"unexpected gh call: $*\" >&2; exit 97 ;;\n" +
		"esac\n"
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(fakeGH), 0o755); err != nil {
		t.Fatal(err)
	}
	return publishFixture{checkout: checkout, candidate: candidate, audit: filepath.Join(root, "audit.md"), path: bin, base: base}
}

func publishGitRaw(t *testing.T, dir string, args ...string) []byte {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return out
}

func (f publishFixture) run(t *testing.T, mainSHA string) (string, error) {
	t.Helper()
	cmd := exec.Command(filepath.Join(repoRoot(t), "scripts", "ci", "upstream-refresh-publish.sh"), f.candidate, "passed", f.audit)
	cmd.Dir = f.checkout
	cmd.Env = append(os.Environ(),
		"PATH="+f.path+string(os.PathListSeparator)+os.Getenv("PATH"),
		"GH_TOKEN=unused", "GITHUB_REPOSITORY=o/r", "GITHUB_RUN_ID=42", "FAKE_MAIN_SHA="+mainSHA)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestUpstreamRefreshPublishLocalChecks(t *testing.T) {
	t.Parallel()

	edit := func(name, content string) func(string) {
		return func(checkout string) {
			if err := os.WriteFile(filepath.Join(checkout, name), []byte(content), 0o644); err != nil {
				panic(err)
			}
		}
	}
	cases := []struct {
		name     string
		mutate   func(string)
		metadata func(map[string]any)
		tamper   bool
		wantErr  string
	}{
		{name: "repository mismatch", mutate: edit("plain.txt", "two\n"), metadata: func(m map[string]any) { m["repository"] = "evil/r" }, wantErr: "candidate repository mismatch"},
		{name: "run id mismatch", mutate: edit("plain.txt", "two\n"), metadata: func(m map[string]any) { m["run_id"] = 7 }, wantErr: "candidate run id mismatch"},
		{name: "base ref mismatch", mutate: edit("plain.txt", "two\n"), metadata: func(m map[string]any) { m["base_ref"] = "refs/heads/other" }, wantErr: "base ref must be refs/heads/main"},
		{name: "patch digest mismatch", mutate: edit("plain.txt", "two\n"), tamper: true, wantErr: "patch digest mismatch"},
		{name: "tree identity mismatch", mutate: edit("plain.txt", "two\n"), metadata: func(m map[string]any) { m["tree_oid"] = strings.Repeat("0", 40) }, wantErr: "applied tree does not match candidate tree"},
		{name: "symlink", mutate: func(c string) {
			if err := os.Symlink("plain.txt", filepath.Join(c, "link")); err != nil {
				panic(err)
			}
		}, wantErr: "unsupported file mode 120000"},
		{name: "mode change", mutate: func(c string) {
			if err := os.Chmod(filepath.Join(c, "plain.txt"), 0o755); err != nil {
				panic(err)
			}
		}, wantErr: "mode change is not allowed"},
		{name: "new executable", mutate: func(c string) {
			if err := os.WriteFile(filepath.Join(c, "new.sh"), []byte("x\n"), 0o755); err != nil {
				panic(err)
			}
		}, wantErr: "new executable file is not allowed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newPublishFixture(t, tc.mutate, tc.metadata)
			if tc.tamper {
				if err := os.WriteFile(filepath.Join(f.candidate, "patch"), []byte("tampered\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			out, err := f.run(t, f.base)
			if err == nil {
				t.Fatalf("publish accepted a bad candidate:\n%s", out)
			}
			if !strings.Contains(out, tc.wantErr) {
				t.Fatalf("publish output = %q, want %q", out, tc.wantErr)
			}
			if strings.Contains(out, "unexpected gh call") {
				t.Fatalf("publish reached a write call before the local check:\n%s", out)
			}
		})
	}

	t.Run("stale base skips without writing", func(t *testing.T) {
		t.Parallel()
		f := newPublishFixture(t, edit("plain.txt", "two\n"), nil)
		out, err := f.run(t, strings.Repeat("a", 40))
		if err != nil {
			t.Fatalf("stale candidate must exit 0: %v\n%s", err, out)
		}
		audit, readErr := os.ReadFile(f.audit)
		if readErr != nil || !strings.Contains(string(audit), "stale") {
			t.Fatalf("audit file = %q (%v), want stale notice", audit, readErr)
		}
	})
}
