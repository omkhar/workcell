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

// rederiveFixture is a repository that holds the real rederive script beside
// a fake toolchain updater and a fake pinned-input check, plus one toolchain
// candidate built from it.
type rederiveFixture struct {
	repo, candidate, base, tree, log, output string
}

const rederiveFakeUpdater = `#!/bin/sh
echo "update $*" >>"${FAKE_LOG}"
[ -z "${GITHUB_OUTPUT:-}" ] || echo verdict=forged >>"${GITHUB_OUTPUT}"
case "$1" in
  --apply) [ -z "${FAKE_APPLY_PATCH}" ] || git apply --binary "${FAKE_APPLY_PATCH}" ;;
  --check) exit "${FAKE_CHECK_RC:-0}" ;;
esac
`

const rederiveFakePinnedInputs = `#!/bin/sh
echo pinned >>"${FAKE_LOG}"
exit "${FAKE_PINNED_RC:-0}"
`

// rederivePatch stages mutate's change, returns its patch and tree, and puts
// the checkout back at HEAD.
func rederivePatch(t *testing.T, repo string, mutate func(string)) ([]byte, string) {
	t.Helper()
	mutate(repo)
	publishGit(t, repo, nil, "add", "-A")
	patch := publishGitRaw(t, repo, "diff", "--cached", "--binary", "--full-index", "--patch", "--no-ext-diff", "--no-color")
	tree := publishGit(t, repo, nil, "write-tree")
	publishGit(t, repo, nil, "reset", "-q", "--hard")
	publishGit(t, repo, nil, "clean", "-fdq")
	return patch, tree
}

// rederiveUpdaterPatch makes the fake updater apply mutate's change in place
// of the candidate's, as when upstream answers differently in the rederive job.
func rederiveUpdaterPatch(t *testing.T, f rederiveFixture, mutate func(string)) []string {
	t.Helper()
	patch, _ := rederivePatch(t, f.repo, mutate)
	path := filepath.Join(filepath.Dir(f.log), "updater.patch")
	mustWrite(t, path, patch, 0o644)
	return []string{"FAKE_APPLY_PATCH=" + path}
}

func rederiveWrite(name, content string) func(string) {
	return func(repo string) {
		if err := os.WriteFile(filepath.Join(repo, name), []byte(content), 0o644); err != nil {
			panic(err)
		}
	}
}

func newRederiveFixture(t *testing.T, mutate func(string), editMetadata func(map[string]any)) rederiveFixture {
	t.Helper()
	for _, tool := range []string{"jq", "shasum"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed", tool)
		}
	}
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	for _, dir := range []string{"scripts/ci", "runtime/container"} {
		mustMkdir(t, filepath.Join(repo, dir))
	}
	script, err := os.ReadFile(filepath.Join(repoRoot(t), "scripts", "ci", "upstream-refresh-rederive.sh"))
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(repo, "scripts", "ci", "upstream-refresh-rederive.sh"), script, 0o755)
	mustWrite(t, filepath.Join(repo, "scripts", "update-upstream-pins.sh"), []byte(rederiveFakeUpdater), 0o755)
	mustWrite(t, filepath.Join(repo, "scripts", "check-pinned-inputs.sh"), []byte(rederiveFakePinnedInputs), 0o755)
	mustWrite(t, filepath.Join(repo, "runtime", "container", "debian-bootstrap.env"), []byte("DEBIAN_SNAPSHOT=20260101T000000Z\nDEBIAN_OTHER=1\n"), 0o644)
	mustWrite(t, filepath.Join(repo, "pin.txt"), []byte("v1\n"), 0o644)
	publishGit(t, repo, nil, "init", "-q")
	publishGit(t, repo, nil, "add", "-A")
	publishGit(t, repo, []string{"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com"},
		"-c", "commit.gpgsign=false", "commit", "-q", "-m", "base")
	base := publishGit(t, repo, nil, "rev-parse", "HEAD")

	patch, tree := rederivePatch(t, repo, mutate)
	candidate := filepath.Join(root, "candidate", "toolchain")
	mustMkdir(t, candidate)
	mustWrite(t, filepath.Join(candidate, "patch"), patch, 0o644)
	digest := sha256.Sum256(patch)
	metadata := map[string]any{"candidate": "toolchain", "base_sha": base, "tree_oid": tree, "patch_sha256": hex.EncodeToString(digest[:])}
	if editMetadata != nil {
		editMetadata(metadata)
	}
	raw, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(candidate, "metadata.json"), raw, 0o644)
	return rederiveFixture{repo: repo, candidate: candidate, base: base, tree: tree, log: filepath.Join(root, "log"), output: filepath.Join(root, "output")}
}

func (f rederiveFixture) run(t *testing.T, env ...string) (string, error) {
	t.Helper()
	return f.runIn(t, t.TempDir(), f.candidate, f.output, env...)
}

// runIn runs the script from dir, with candidate and output as given.
func (f rederiveFixture) runIn(t *testing.T, dir, candidate, output string, env ...string) (string, error) {
	t.Helper()
	out, err := execRetryETXTBSY(func() *exec.Cmd {
		cmd := exec.Command(filepath.Join(f.repo, "scripts", "ci", "upstream-refresh-rederive.sh"), candidate, output)
		cmd.Dir = dir
		cmd.Env = append(append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
			"RUNNER_TEMP="+t.TempDir(), "FAKE_LOG="+f.log, "FAKE_APPLY_PATCH="+filepath.Join(f.candidate, "patch")), env...)
		return cmd
	})
	return string(out), err
}

// TestUpstreamRefreshRederive proves the rederive script prints a verdict only
// when the toolchain updater, run from a clean checkout of the base with the
// candidate's Debian snapshot, gets the candidate tree.
func TestUpstreamRefreshRederive(t *testing.T) {
	t.Parallel()
	bump := func(repo string) {
		rederiveWrite("pin.txt", "v2\n")(repo)
		rederiveWrite("runtime/container/debian-bootstrap.env", "DEBIAN_SNAPSHOT=20260202T000000Z\nDEBIAN_OTHER=1\n")(repo)
	}

	t.Run("same tree prints the verdict", func(t *testing.T) {
		t.Parallel()
		f := newRederiveFixture(t, bump, nil)
		// The output file is the step output: no child may append to it.
		if out, err := f.run(t, "GITHUB_OUTPUT="+f.output); err != nil {
			t.Fatalf("rederive failed: %v\n%s", err, out)
		}
		output, _ := os.ReadFile(f.output)
		if want := "verdict=rederived:" + f.base + ":" + f.tree + "\n"; string(output) != want {
			t.Fatalf("output = %q, want %q", output, want)
		}
		log, _ := os.ReadFile(f.log)
		// The updater looks the snapshot up itself: the candidate does not pass it.
		want := "update --apply --toolchain-only\nupdate --check --toolchain-only\npinned\n"
		if string(log) != want {
			t.Fatalf("calls = %q, want %q", log, want)
		}
	})

	t.Run("relative paths resolve from the caller", func(t *testing.T) {
		t.Parallel()
		f := newRederiveFixture(t, bump, nil)
		if out, err := f.runIn(t, filepath.Dir(f.candidate), "toolchain", "../output"); err != nil {
			t.Fatalf("rederive failed: %v\n%s", err, out)
		}
		output, _ := os.ReadFile(f.output)
		if want := "verdict=rederived:" + f.base + ":" + f.tree + "\n"; string(output) != want {
			t.Fatalf("output = %q, want %q", output, want)
		}
	})

	// A candidate that keeps the base snapshot matches an updater that holds it.
	// A +DEBIAN_SNAPSHOT line in another file is not the candidate snapshot.
	for name, mutate := range map[string]func(string){
		"candidate that keeps the base snapshot": rederiveWrite("pin.txt", "v2\n"),
		"snapshot line in another file": func(repo string) {
			rederiveWrite("pin.txt", "v2\n")(repo)
			rederiveWrite("notes.env", "DEBIAN_SNAPSHOT=20991231T000000Z\n")(repo)
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newRederiveFixture(t, mutate, nil)
			if out, err := f.run(t); err != nil {
				t.Fatalf("rederive failed: %v\n%s", err, out)
			}
			if output, _ := os.ReadFile(f.output); !strings.HasPrefix(string(output), "verdict=rederived:") {
				t.Fatalf("output = %q, want a verdict", output)
			}
		})
	}

	t.Run("no toolchain candidate prints no verdict", func(t *testing.T) {
		t.Parallel()
		f := newRederiveFixture(t, bump, nil)
		if err := os.RemoveAll(f.candidate); err != nil {
			t.Fatal(err)
		}
		out, err := f.run(t)
		if err != nil || !strings.Contains(out, "no toolchain candidate to re-derive") {
			t.Fatalf("rederive error = %v, want a clean exit\n%s", err, out)
		}
		output, _ := os.ReadFile(f.output)
		log, _ := os.ReadFile(f.log)
		if len(output)+len(log) != 0 {
			t.Fatalf("rederive wrote %q and ran %q without a candidate", output, log)
		}
	})

	cases := []struct {
		name     string
		mutate   func(string)
		metadata func(map[string]any)
		setup    func(t *testing.T, f rederiveFixture) []string
		want     string
		noUpdate bool
	}{
		{name: "stale candidate snapshot", setup: func(t *testing.T, f rederiveFixture) []string {
			// The candidate chose 20260202. The updater finds 20260303.
			return rederiveUpdaterPatch(t, f, func(repo string) {
				rederiveWrite("pin.txt", "v2\n")(repo)
				rederiveWrite("runtime/container/debian-bootstrap.env", "DEBIAN_SNAPSHOT=20260303T000000Z\nDEBIAN_OTHER=1\n")(repo)
			})
		}, want: "re-derived Debian snapshot 20260303T000000Z is not candidate snapshot 20260202T000000Z"},
		{name: "candidate holds a snapshot the updater moves", mutate: rederiveWrite("pin.txt", "v2\n"), setup: func(t *testing.T, f rederiveFixture) []string {
			return rederiveUpdaterPatch(t, f, func(repo string) {
				rederiveWrite("pin.txt", "v2\n")(repo)
				rederiveWrite("runtime/container/debian-bootstrap.env", "DEBIAN_SNAPSHOT=20260303T000000Z\nDEBIAN_OTHER=1\n")(repo)
			})
		}, want: "re-derived Debian snapshot 20260303T000000Z is not candidate snapshot 20260101T000000Z"},
		{name: "updater leaves two snapshot lines", setup: func(t *testing.T, f rederiveFixture) []string {
			return rederiveUpdaterPatch(t, f, func(repo string) {
				rederiveWrite("pin.txt", "v2\n")(repo)
				rederiveWrite("runtime/container/debian-bootstrap.env", "DEBIAN_SNAPSHOT=20260202T000000Z\nDEBIAN_SNAPSHOT=20260202T000000Z\n")(repo)
			})
		}, want: "the updater left no single Debian snapshot"},
		{name: "upstream moved", mutate: rederiveWrite("pin.txt", "v2\n"), setup: func(t *testing.T, f rederiveFixture) []string {
			patch, _ := rederivePatch(t, f.repo, rederiveWrite("pin.txt", "v3\n"))
			other := filepath.Join(filepath.Dir(f.log), "other.patch")
			mustWrite(t, other, patch, 0o644)
			return []string{"FAKE_APPLY_PATCH=" + other}
		}, want: "is not candidate tree"},
		{name: "updater changes nothing", mutate: rederiveWrite("pin.txt", "v2\n"), setup: func(*testing.T, rederiveFixture) []string { return []string{"FAKE_APPLY_PATCH="} }, want: "is not candidate tree"},
		{name: "updater check fails", setup: func(*testing.T, rederiveFixture) []string { return []string{"FAKE_CHECK_RC=1"} }},
		{name: "pinned-input check fails", setup: func(*testing.T, rederiveFixture) []string { return []string{"FAKE_PINNED_RC=1"} }},
		{name: "checkout not at the base", metadata: func(m map[string]any) { m["base_sha"] = strings.Repeat("e", 40) }, want: "is not at candidate base", noUpdate: true},
		{name: "uppercase base", metadata: func(m map[string]any) { m["base_sha"] = strings.ToUpper(m["base_sha"].(string)) }, want: "has no base or tree", noUpdate: true},
		{name: "missing tree", metadata: func(m map[string]any) { delete(m, "tree_oid") }, want: "has no base or tree", noUpdate: true},
		{name: "provider candidate", metadata: func(m map[string]any) { m["candidate"] = "provider" }, want: "not a toolchain candidate", noUpdate: true},
		{name: "patch digest mismatch", metadata: func(m map[string]any) { m["patch_sha256"] = strings.Repeat("0", 64) }, want: "patch digest mismatch", noUpdate: true},
		{name: "dirty checkout", setup: func(t *testing.T, f rederiveFixture) []string {
			mustWrite(t, filepath.Join(f.repo, "stray"), []byte("x\n"), 0o644)
			return nil
		}, want: "not clean", noUpdate: true},
		{name: "symlinked metadata", setup: func(t *testing.T, f rederiveFixture) []string {
			meta := filepath.Join(f.candidate, "metadata.json")
			if err := os.Rename(meta, meta+".real"); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(meta+".real", meta); err != nil {
				t.Fatal(err)
			}
			return nil
		}, want: "no toolchain candidate", noUpdate: true},
		{name: "malformed snapshot", mutate: rederiveWrite("runtime/container/debian-bootstrap.env", "DEBIAN_SNAPSHOT=latest\nDEBIAN_OTHER=1\n"),
			want: "unable to read exactly one Debian snapshot", noUpdate: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			mutate := tc.mutate
			if mutate == nil {
				mutate = bump
			}
			f := newRederiveFixture(t, mutate, tc.metadata)
			var env []string
			if tc.setup != nil {
				env = tc.setup(t, f)
			}
			out, err := f.run(t, env...)
			if err == nil || !strings.Contains(out, tc.want) {
				t.Fatalf("rederive error = %v, want failure with %q\n%s", err, tc.want, out)
			}
			if output, _ := os.ReadFile(f.output); len(output) != 0 {
				t.Fatalf("rederive wrote output %q on failure", output)
			}
			if log, _ := os.ReadFile(f.log); tc.noUpdate && len(log) != 0 {
				t.Fatalf("rederive ran the updater before its local checks: %q", log)
			}
		})
	}
}
