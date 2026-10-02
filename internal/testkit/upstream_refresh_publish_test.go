// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package testkit

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func mustWrite(t *testing.T, path string, data []byte, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatal(err)
	}
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

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

// publishFixture builds a checkout, a candidate root with one provider
// candidate, and a fake gh that logs each call to FAKE_GH_LOG. The fake answers
// write calls only when FAKE_ALLOW_WRITES=1.
type publishFixture struct {
	checkout, candidate, audit, path string
	base, tree                       string
}

// candidateDir is the directory of the one candidate the fixture wrote.
func (f publishFixture) candidateDir() string { return filepath.Join(f.candidate, "provider") }

func newPublishFixture(t *testing.T, mutate func(checkout string), editMetadata func(m map[string]any)) publishFixture {
	t.Helper()
	for _, tool := range []string{"jq", "shasum", "base64"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed", tool)
		}
	}
	root := t.TempDir()
	checkout := filepath.Join(root, "checkout")
	mustMkdir(t, checkout)
	publishGit(t, checkout, nil, "init", "-q")
	mustWrite(t, filepath.Join(checkout, "plain.txt"), []byte("one\n"), 0o644)
	mustWrite(t, filepath.Join(checkout, "tool.sh"), []byte("#!/bin/sh\n"), 0o755)
	mustMkdir(t, filepath.Join(checkout, "policy"))
	mustWrite(t, filepath.Join(checkout, "policy", "provider-bumps.toml"), []byte("cooloff_hours = 48\n"), 0o644)
	ident := []string{"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com"}
	publishGit(t, checkout, nil, "add", "-A")
	publishGit(t, checkout, ident, "-c", "commit.gpgsign=false", "commit", "-q", "-m", "base")
	base := publishGit(t, checkout, nil, "rev-parse", "HEAD")

	mutate(checkout)
	candidate := filepath.Join(root, "candidate")
	mustMkdir(t, filepath.Join(candidate, "provider"))
	publishGit(t, checkout, nil, "add", "-A")
	patch := publishGitRaw(t, checkout, "diff", "--cached", "--binary", "--full-index", "--patch", "--no-ext-diff", "--no-color")
	index := filepath.Join(root, "index")
	treeEnv := []string{"GIT_INDEX_FILE=" + index}
	publishGit(t, checkout, treeEnv, "read-tree", "HEAD")
	publishGit(t, checkout, treeEnv, "add", "-A")
	tree := publishGit(t, checkout, treeEnv, "write-tree")
	mustWrite(t, filepath.Join(candidate, "provider", "patch"), patch, 0o644)
	mustWrite(t, filepath.Join(candidate, "provider", "diffstat"), []byte(" stat\n"), 0o644)
	digest := sha256.Sum256(patch)
	metadata := map[string]any{
		"version": 1, "repository": "o/r", "workflow": "upstream-refresh", "run_id": 42,
		"run_url": "https://example.invalid/run", "artifact_name": "upstream-refresh-candidate", "candidate": "provider",
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
	mustWrite(t, filepath.Join(candidate, "provider", "metadata.json"), raw, 0o644)
	// Return the checkout to the base state, as a fresh CI checkout is.
	publishGit(t, checkout, nil, "reset", "-q", "--hard")
	publishGit(t, checkout, nil, "clean", "-fdq")

	bin := writePublishFakeGH(t, root)
	return publishFixture{checkout: checkout, candidate: candidate, audit: filepath.Join(root, "audit.md"), path: bin, base: base, tree: tree}
}

// writePublishFakeGH writes a fake gh under root/bin and returns that
// directory. Without FAKE_TREE, the commit lookup reports the tree that the
// publish script staged when it asked for the commit, as GitHub would.
func writePublishFakeGH(t *testing.T, root string) string {
	t.Helper()
	bin := filepath.Join(root, "bin")
	mustMkdir(t, bin)
	fakeGH := "#!/bin/sh\n" +
		"echo \"$*\" >>\"${FAKE_GH_LOG}\"\n" +
		"case \"$*\" in\n" +
		"  *git/ref/heads/main*) echo \"${FAKE_MAIN_SHA}\"; exit 0 ;;\n" +
		"  \"pr list\"*) echo \"${FAKE_EXISTING_PR}\"; exit 0 ;;\n" +
		"esac\n" +
		"[ \"${FAKE_ALLOW_WRITES}\" = 1 ] || { echo \"unexpected gh call: $*\" >&2; exit 97; }\n" +
		"case \"$*\" in\n" +
		"  *git/refs\\ *) ;;\n" +
		"  \"api graphql\"*) git write-tree >\"${FAKE_GH_LOG}.tree\"; echo \"${FAKE_COMMIT}\" ;;\n" +
		"  *git/commits/*) printf '{\"tree\":{\"sha\":\"%s\"},\"parents\":[{\"sha\":\"%s\"}],\"verification\":{\"verified\":true}}\\n' \"${FAKE_TREE:-$(cat \"${FAKE_GH_LOG}.tree\")}\" \"${FAKE_PARENT}\" ;;\n" +
		"  \"pr create\"*) [ -z \"${FAKE_PR_CREATE_FAIL}\" ] || exit 1; for a; do [ \"${p}\" = --body-file ] && cat \"${a}\" >>\"${FAKE_GH_LOG}.bodies\"; p=\"${a}\"; done; echo https://example.invalid/pr/1 ;;\n" +
		"  \"api -X DELETE\"*) ;;\n" +
		"  *git/ref/heads/codex*) [ -n \"${FAKE_ORPHAN}\" ] || { echo \"${FAKE_REF_ERROR:-gh: Not Found (HTTP 404)}\" >&2; exit 1; } ;;\n" +
		"  \"pr merge\"* | \"pr edit\"*) ;;\n" +
		"  *) echo \"unexpected gh call: $*\" >&2; exit 97 ;;\n" +
		"esac\n"
	mustWrite(t, filepath.Join(bin, "gh"), []byte(fakeGH), 0o755)
	return bin
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

func (f publishFixture) run(t *testing.T, mainSHA string, env ...string) (string, error) {
	t.Helper()
	scope := "passed"
	for _, e := range env {
		if v, ok := strings.CutPrefix(e, "SCOPE="); ok {
			scope = v
		}
	}
	cmd := exec.Command(filepath.Join(repoRoot(t), "scripts", "ci", "upstream-refresh-publish.sh"), f.candidate, scope, f.audit)
	cmd.Dir = f.checkout
	cmd.Env = append(append(os.Environ(),
		"PATH="+f.path+string(os.PathListSeparator)+os.Getenv("PATH"),
		"GH_TOKEN=unused", "GITHUB_REPOSITORY=o/r", "GITHUB_RUN_ID=42", "FAKE_MAIN_SHA="+mainSHA, "FAKE_PARENT="+f.base,
		"FAKE_GH_LOG="+filepath.Join(filepath.Dir(f.path), "gh.log")), env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestUpstreamRefreshPublishLocalChecks(t *testing.T) {
	t.Parallel()

	must := func(err error) {
		if err != nil {
			panic(err)
		}
	}
	edit := func(name, content string) func(string) {
		return func(checkout string) { must(os.WriteFile(filepath.Join(checkout, name), []byte(content), 0o644)) }
	}
	cases := []struct {
		name     string
		mutate   func(string)
		metadata func(map[string]any)
		tamper   bool
		wantErr  string
	}{
		{name: "repository mismatch", mutate: edit("plain.txt", "two\n"), metadata: func(m map[string]any) { m["repository"] = "evil/r" }, wantErr: "candidate repository mismatch"},
		{name: "candidate kind mismatch", mutate: edit("plain.txt", "two\n"), metadata: func(m map[string]any) { m["candidate"] = "toolchain" }, wantErr: "candidate kind mismatch"},
		{name: "run id mismatch", mutate: edit("plain.txt", "two\n"), metadata: func(m map[string]any) { m["run_id"] = 7 }, wantErr: "candidate run id mismatch"},
		{name: "base ref mismatch", mutate: edit("plain.txt", "two\n"), metadata: func(m map[string]any) { m["base_ref"] = "refs/heads/other" }, wantErr: "base ref must be refs/heads/main"},
		{name: "patch digest mismatch", mutate: edit("plain.txt", "two\n"), tamper: true, wantErr: "patch digest mismatch"},
		{name: "tree identity mismatch", mutate: edit("plain.txt", "two\n"), metadata: func(m map[string]any) { m["tree_oid"] = strings.Repeat("0", 40) }, wantErr: "applied tree does not match candidate tree"},
		{name: "symlink", mutate: func(c string) { must(os.Symlink("plain.txt", filepath.Join(c, "link"))) }, wantErr: "unsupported file mode 120000"},
		{name: "mode change", mutate: func(c string) { must(os.Chmod(filepath.Join(c, "plain.txt"), 0o755)) }, wantErr: "mode change is not allowed"},
		{name: "new executable", mutate: func(c string) { must(os.WriteFile(filepath.Join(c, "new.sh"), []byte("x\n"), 0o755)) }, wantErr: "new executable file is not allowed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newPublishFixture(t, tc.mutate, tc.metadata)
			if tc.tamper {
				mustWrite(t, filepath.Join(f.candidateDir(), "patch"), []byte("tampered\n"), 0o644)
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

	const prFmt = `{"title":"Refresh pinned upstreams (provider)","url":"https://example.invalid/pr/9","headRefName":"codex/upstream-refresh-provider-%s","headRefOid":"%s"}`
	commit, resumed := strings.Repeat("c", 40), strings.Repeat("d", 40)
	disposition := []struct {
		name    string
		moved   bool
		env     []string
		want    []string
		forbid  []string
		wantErr bool
	}{
		{name: "stale base skips without writing", moved: true, forbid: []string{"git/refs", "pr create"}},
		{name: "passed guard pins auto-merge to the published commit", env: []string{"FAKE_ALLOW_WRITES=1", "FAKE_COMMIT=" + commit},
			want: []string{"pr merge --repo o/r --auto --merge --match-head-commit " + commit + " "}},
		{name: "rerun resumes the PR this run opened even when main moved", moved: true, env: []string{"FAKE_ALLOW_WRITES=1", "FAKE_EXISTING_PR=" + fmt.Sprintf(prFmt, "42", resumed)},
			want: []string{"--match-head-commit " + resumed + " https://example.invalid/pr/9"}, forbid: []string{"pr create", "git/refs", "graphql"}},
		{name: "resumed PR on another parent fails closed", moved: true, env: []string{"FAKE_ALLOW_WRITES=1", "FAKE_PARENT=" + resumed, "FAKE_EXISTING_PR=" + fmt.Sprintf(prFmt, "42", resumed)}, wantErr: true, forbid: []string{"pr merge"}},
		{name: "PR from another run skips", env: []string{"FAKE_EXISTING_PR=" + fmt.Sprintf(prFmt, "7", resumed)}, forbid: []string{"pr merge", "pr create"}},
		{name: "orphan branch from a killed attempt is replaced", env: []string{"FAKE_ALLOW_WRITES=1", "FAKE_COMMIT=" + commit, "FAKE_ORPHAN=1"},
			want: []string{"api -X DELETE repos/o/r/git/refs/heads/codex/upstream-refresh-provider-42", "pr merge"}},
		{name: "ref lookup error fails closed", env: []string{"FAKE_ALLOW_WRITES=1", "FAKE_COMMIT=" + commit, "FAKE_REF_ERROR=gh: HTTP 502"}, wantErr: true, forbid: []string{"git/refs ", "pr create"}},
		{name: "failed guard labels without creating the label", env: []string{"SCOPE=failed", "FAKE_ALLOW_WRITES=1", "FAKE_COMMIT=" + commit},
			want: []string{"--add-label needs-human-review"}, forbid: []string{"label create", "pr merge"}},
		{name: "failed PR creation deletes the branch", env: []string{"FAKE_ALLOW_WRITES=1", "FAKE_COMMIT=" + commit, "FAKE_PR_CREATE_FAIL=1"}, wantErr: true,
			want: []string{"api -X DELETE repos/o/r/git/refs/heads/codex/upstream-refresh-provider-42"}},
	}
	for _, tc := range disposition {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newPublishFixture(t, edit("plain.txt", "two\n"), nil)
			main := f.base
			if tc.moved {
				main = strings.Repeat("a", 40)
			}
			out, err := f.run(t, main, append([]string{"FAKE_TREE=" + f.tree}, tc.env...)...)
			if (err != nil) != tc.wantErr {
				t.Fatalf("publish error = %v, want error %v\n%s", err, tc.wantErr, out)
			}
			log, _ := os.ReadFile(filepath.Join(filepath.Dir(f.path), "gh.log"))
			for _, w := range tc.want {
				if !strings.Contains(string(log), w) {
					t.Fatalf("gh calls = %q, want %q", log, w)
				}
			}
			for _, w := range tc.forbid {
				if strings.Contains(string(log), w) {
					t.Fatalf("gh calls = %q, must not contain %q", log, w)
				}
			}
		})
	}
}
