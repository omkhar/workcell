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
	base, tree, kind                 string
}

// candidateDir is the directory of the one candidate the fixture wrote.
func (f publishFixture) candidateDir() string { return filepath.Join(f.candidate, f.kind) }

func newPublishFixture(t *testing.T, mutate func(checkout string), editMetadata func(m map[string]any)) publishFixture {
	t.Helper()
	return newPublishFixtureOfKind(t, "provider", mutate, editMetadata)
}

// newPublishFixtureOfKind writes one candidate of the given kind.
func newPublishFixtureOfKind(t *testing.T, kind string, mutate func(checkout string), editMetadata func(m map[string]any)) publishFixture {
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

	candidate := filepath.Join(root, "candidate")
	tree := writePublishCandidate(t, root, checkout, base, kind, mutate, editMetadata)
	bin := writePublishFakeGH(t, root)
	return publishFixture{checkout: checkout, candidate: candidate, audit: filepath.Join(root, "audit.md"), path: bin, base: base, tree: tree, kind: kind}
}

// writePublishCandidate writes a candidate of kind under root/candidate from
// mutate's change to checkout, puts checkout back at base, and returns the
// candidate tree.
func writePublishCandidate(t *testing.T, root, checkout, base, kind string, mutate func(checkout string), editMetadata func(m map[string]any)) string {
	t.Helper()
	mutate(checkout)
	candidate := filepath.Join(root, "candidate")
	mustMkdir(t, filepath.Join(candidate, kind))
	publishGit(t, checkout, nil, "add", "-A")
	patch := publishGitRaw(t, checkout, "diff", "--cached", "--binary", "--full-index", "--patch", "--no-ext-diff", "--no-color")
	index := filepath.Join(root, "index-"+kind)
	treeEnv := []string{"GIT_INDEX_FILE=" + index}
	publishGit(t, checkout, treeEnv, "read-tree", "HEAD")
	publishGit(t, checkout, treeEnv, "add", "-A")
	tree := publishGit(t, checkout, treeEnv, "write-tree")
	mustWrite(t, filepath.Join(candidate, kind, "patch"), patch, 0o644)
	mustWrite(t, filepath.Join(candidate, kind, "diffstat"), []byte(" stat\n"), 0o644)
	digest := sha256.Sum256(patch)
	metadata := map[string]any{
		"version": 1, "repository": "o/r", "workflow": "upstream-refresh", "run_id": 42,
		"run_url": "https://example.invalid/run", "artifact_name": "upstream-refresh-candidate", "candidate": kind,
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
	mustWrite(t, filepath.Join(candidate, kind, "metadata.json"), raw, 0o644)
	// Return the checkout to the base state, as a fresh CI checkout is.
	publishGit(t, checkout, nil, "reset", "-q", "--hard")
	publishGit(t, checkout, nil, "clean", "-fdq")
	return tree
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
	scope, toolchain, rederive := "passed", "failed", "failed"
	for _, e := range env {
		if v, ok := strings.CutPrefix(e, "SCOPE="); ok {
			scope = v
		}
		if v, ok := strings.CutPrefix(e, "TOOLCHAIN="); ok {
			toolchain = v
		}
		if v, ok := strings.CutPrefix(e, "REDERIVE="); ok {
			rederive = v
		}
	}
	cmd := exec.Command(filepath.Join(repoRoot(t), "scripts", "ci", "upstream-refresh-publish.sh"), f.candidate, scope, toolchain, rederive, f.audit)
	cmd.Dir = f.checkout
	cmd.Env = append(append(os.Environ(),
		"PATH="+f.path+string(os.PathListSeparator)+os.Getenv("PATH"),
		"GH_TOKEN=unused", "GITHUB_SERVER_URL=https://github.com", "GITHUB_REPOSITORY=o/r", "GITHUB_RUN_ID=42", "FAKE_MAIN_SHA="+mainSHA, "FAKE_PARENT="+f.base,
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

	const prFmt = `[{"title":"Refresh pinned upstreams (provider)","url":"https://example.invalid/pr/9","headRefName":"codex/upstream-refresh-provider-%s","headRefOid":"%s","isCrossRepository":false}]`
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

// TestUpstreamRefreshPublishToolchainCandidate proves the App publishes a
// toolchain candidate only under a passed toolchain guard verdict and a
// rederive verdict, each for that candidate's base and tree. It labels the PR
// for a person, never arms auto-merge, and lists the pin lines in the PR body
// and the audit.
func TestUpstreamRefreshPublishToolchainCandidate(t *testing.T) {
	t.Parallel()

	commit := strings.Repeat("c", 40)
	other := strings.Repeat("e", 40)
	editPin := func(c string) {
		if err := os.WriteFile(filepath.Join(c, "plain.txt"), []byte("two\n"), 0o644); err != nil {
			panic(err)
		}
	}
	addWorkflow := func(c string) {
		editPin(c)
		if err := os.MkdirAll(filepath.Join(c, ".github", "workflows"), 0o755); err != nil {
			panic(err)
		}
		if err := os.WriteFile(filepath.Join(c, ".github", "workflows", "x.yml"), []byte("on: push\n"), 0o644); err != nil {
			panic(err)
		}
	}
	cases := []struct {
		name     string
		mutate   func(string)
		verdict  func(f publishFixture) string
		rederive func(f publishFixture) string
		wantErr  string
		audit    string
		noGH     bool
	}{
		{name: "failed guard holds before any GitHub call", mutate: editPin, verdict: func(publishFixture) string { return "failed" },
			audit: "the toolchain scope guard did not pass", noGH: true},
		{name: "empty verdict holds", mutate: editPin, verdict: func(publishFixture) string { return "" },
			audit: "the toolchain scope guard did not pass", noGH: true},
		{name: "bare passed holds", mutate: editPin, verdict: func(publishFixture) string { return "passed" },
			audit: "the toolchain scope guard did not pass", noGH: true},
		{name: "uppercase verdict holds", mutate: editPin, verdict: func(f publishFixture) string { return "passed:" + strings.ToUpper(f.base) + ":" + f.tree },
			audit: "the toolchain scope guard did not pass", noGH: true},
		{name: "verdict for another base dies", mutate: editPin, verdict: func(f publishFixture) string { return "passed:" + other + ":" + f.tree },
			wantErr: "the toolchain scope guard checked base " + other, noGH: true},
		{name: "verdict for another tree dies", mutate: editPin, verdict: func(f publishFixture) string { return "passed:" + f.base + ":" + other },
			wantErr: "the toolchain scope guard checked tree " + other, noGH: true},
		{name: "workflow change holds even with a verdict", mutate: addWorkflow, verdict: func(f publishFixture) string { return "passed:" + f.base + ":" + f.tree },
			audit: "not published, it changes .github/workflows/x.yml. Publish it from a host: `./scripts/publish-upstream-refresh-pr.sh --run-id 42 --candidate toolchain`"},
		{name: "failed rederive holds before any GitHub call", mutate: editPin, rederive: func(publishFixture) string { return "failed" },
			audit: "the rederive job did not get the same tree from the base", noGH: true},
		{name: "bare rederived holds", mutate: editPin, rederive: func(publishFixture) string { return "rederived" },
			audit: "the rederive job did not get the same tree from the base", noGH: true},
		{name: "guard verdict in the rederive slot holds", mutate: editPin, rederive: func(f publishFixture) string { return "passed:" + f.base + ":" + f.tree },
			audit: "the rederive job did not get the same tree from the base", noGH: true},
		{name: "rederive verdict for another base dies", mutate: editPin, rederive: func(f publishFixture) string { return "rederived:" + other + ":" + f.tree },
			wantErr: "the rederive job used base " + other, noGH: true},
		{name: "rederive verdict for another tree dies", mutate: editPin, rederive: func(f publishFixture) string { return "rederived:" + f.base + ":" + other },
			wantErr: "the rederive job got tree " + other, noGH: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newPublishFixtureOfKind(t, "toolchain", tc.mutate, nil)
			verdict, rederive := "passed:"+f.base+":"+f.tree, "rederived:"+f.base+":"+f.tree
			if tc.verdict != nil {
				verdict = tc.verdict(f)
			}
			if tc.rederive != nil {
				rederive = tc.rederive(f)
			}
			out, err := f.run(t, f.base, "FAKE_ALLOW_WRITES=1", "FAKE_COMMIT="+commit, "TOOLCHAIN="+verdict, "REDERIVE="+rederive)
			if (err != nil) != (tc.wantErr != "") || !strings.Contains(out, tc.wantErr) {
				t.Fatalf("publish error = %v, want %q\n%s", err, tc.wantErr, out)
			}
			log, _ := os.ReadFile(filepath.Join(filepath.Dir(f.path), "gh.log"))
			if tc.noGH && len(log) != 0 {
				t.Fatalf("publish called gh before the verdict check:\n%s", log)
			}
			for _, w := range []string{"pr create", "git/refs", "graphql", "pr merge", "pr edit"} {
				if strings.Contains(string(log), w) {
					t.Fatalf("gh calls = %q, must not contain %q", log, w)
				}
			}
			audit, _ := os.ReadFile(f.audit)
			if !strings.Contains(string(audit), tc.audit) {
				t.Fatalf("audit = %q, want %q", audit, tc.audit)
			}
		})
	}

	t.Run("passed guard publishes for a person", func(t *testing.T) {
		t.Parallel()
		f := newPublishFixtureOfKind(t, "toolchain", editPin, nil)
		out, err := f.run(t, f.base, "FAKE_ALLOW_WRITES=1", "FAKE_COMMIT="+commit, "TOOLCHAIN=passed:"+f.base+":"+f.tree, "REDERIVE=rederived:"+f.base+":"+f.tree)
		if err != nil {
			t.Fatalf("publish failed: %v\n%s", err, out)
		}
		logPath := filepath.Join(filepath.Dir(f.path), "gh.log")
		log, _ := os.ReadFile(logPath)
		for _, w := range []string{"pr create --repo o/r --base main --head codex/upstream-refresh-toolchain-42 --title Refresh pinned upstreams (toolchain)", "pr edit https://example.invalid/pr/1 --repo o/r --add-label needs-human-review"} {
			if !strings.Contains(string(log), w) {
				t.Fatalf("gh calls = %q, want %q", log, w)
			}
		}
		if strings.Contains(string(log), "pr merge") {
			t.Fatalf("publish armed auto-merge on a toolchain PR:\n%s", log)
		}
		pins := "```diff\nplain.txt\n-one\n+two\n```"
		body, _ := os.ReadFile(logPath + ".bodies")
		for _, w := range []string{"- scope guard: passed on tree " + f.tree, "- re-derived: the rederive job got tree " + f.tree + " from base " + f.base, "## Pins\n\n" + pins + "\n## Diffstat"} {
			if !strings.Contains(string(body), w) {
				t.Fatalf("PR body = %q, want %q", body, w)
			}
		}
		audit, _ := os.ReadFile(f.audit)
		for _, w := range []string{"labeled needs-human-review, no auto-merge (toolchain scope guard passed on this tree)", "- pins, each old line then its new line:\n\n" + pins} {
			if !strings.Contains(string(audit), w) {
				t.Fatalf("audit = %q, want %q", audit, w)
			}
		}
	})
}

// TestUpstreamRefreshPublishBodyIgnoresArtifactText proves the PR body takes
// its diffstat from the applied tree and its run URL from the workflow
// context, not from artifact text that no check covers.
func TestUpstreamRefreshPublishBodyIgnoresArtifactText(t *testing.T) {
	t.Parallel()
	commit := strings.Repeat("c", 40)
	for _, kind := range []string{"provider", "toolchain"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			f := newPublishFixtureOfKind(t, kind, func(c string) {
				if err := os.WriteFile(filepath.Join(c, "plain.txt"), []byte("two\n"), 0o644); err != nil {
					panic(err)
				}
			}, func(m map[string]any) { m["run_url"] = "https://evil.invalid/run" })
			mustWrite(t, filepath.Join(f.candidateDir(), "diffstat"), []byte("```\n## evil\n[click](https://evil.invalid)\n"), 0o644)
			out, err := f.run(t, f.base, "FAKE_ALLOW_WRITES=1", "FAKE_COMMIT="+commit, "TOOLCHAIN=passed:"+f.base+":"+f.tree, "REDERIVE=rederived:"+f.base+":"+f.tree)
			if err != nil {
				t.Fatalf("publish failed: %v\n%s", err, out)
			}
			body, _ := os.ReadFile(filepath.Join(filepath.Dir(f.path), "gh.log.bodies"))
			if strings.Contains(string(body), "evil") {
				t.Fatalf("PR body took artifact text:\n%s", body)
			}
			for _, w := range []string{"- run: https://github.com/o/r/actions/runs/42\n", "## Diffstat\n\n```\n plain.txt | 2 +-\n 1 file changed, 1 insertion(+), 1 deletion(-)\n```"} {
				if !strings.Contains(string(body), w) {
					t.Fatalf("PR body = %q, want %q", body, w)
				}
			}
		})
	}
}

// TestUpstreamRefreshPublishFailedRederiveHoldsOnlyToolchain proves that a
// rederive job that failed, which the workflow passes as 'failed', holds the
// toolchain candidate and still publishes the provider candidate.
func TestUpstreamRefreshPublishFailedRederiveHoldsOnlyToolchain(t *testing.T) {
	t.Parallel()
	f := newPublishFixture(t, func(c string) {
		if err := os.WriteFile(filepath.Join(c, "plain.txt"), []byte("two\n"), 0o644); err != nil {
			panic(err)
		}
	}, nil)
	toolchainTree := writePublishCandidate(t, filepath.Dir(f.candidate), f.checkout, f.base, "toolchain", func(c string) {
		if err := os.WriteFile(filepath.Join(c, "tool.sh"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			panic(err)
		}
	}, nil)
	out, err := f.run(t, f.base, "FAKE_ALLOW_WRITES=1", "FAKE_COMMIT="+strings.Repeat("c", 40),
		"TOOLCHAIN=passed:"+f.base+":"+toolchainTree, "REDERIVE=failed")
	if err != nil {
		t.Fatalf("publish failed: %v\n%s", err, out)
	}
	log, _ := os.ReadFile(filepath.Join(filepath.Dir(f.path), "gh.log"))
	for _, w := range []string{"--head codex/upstream-refresh-provider-42", "pr merge --repo o/r --auto"} {
		if !strings.Contains(string(log), w) {
			t.Fatalf("gh calls = %q, want %q", log, w)
		}
	}
	if strings.Contains(string(log), "upstream-refresh-toolchain") {
		t.Fatalf("publish published the toolchain candidate without a rederive verdict:\n%s", log)
	}
	audit, _ := os.ReadFile(f.audit)
	if !strings.Contains(string(audit), "- toolchain: not published by the App, the rederive job did not get the same tree") {
		t.Fatalf("audit = %q, want the toolchain hold", audit)
	}
}
