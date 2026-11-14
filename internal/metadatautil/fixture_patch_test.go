// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func fixturePatchRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	repo := t.TempDir()
	writePatchFixture(t, repo, "tests/fixtures/actions/inputs.tsv", "a\tb\n")
	writePatchFixture(t, repo, "tests/fixtures/flags/codex.txt", "old\n")
	writePatchFixture(t, repo, "README.md", "readme\n")
	runGitFixture(t, repo, "init", "-q")
	commitPatchFixture(t, repo)
	return repo
}

func writePatchFixture(t *testing.T, repo, rel, body string) {
	t.Helper()
	path := filepath.Join(repo, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func commitPatchFixture(t *testing.T, repo string) {
	t.Helper()
	runGitFixture(t, repo, "add", "-A")
	runGitFixture(t, repo, "-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "-q", "-m", "fixture")
}

// patchNumstat returns what git apply --numstat prints for patch: one line
// of added and removed line counts and the path for each file it touches.
func patchNumstat(t *testing.T, repo string, patch []byte) string {
	t.Helper()
	cmd := exec.Command("git", "-C", repo, "apply", "--numstat", "-")
	cmd.Stdin = strings.NewReader(string(patch))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git apply --numstat: %v\n%s", err, out)
	}
	return string(out)
}

func applyFixturePatch(t *testing.T, repo string, patch []byte) string {
	t.Helper()
	clean := t.TempDir()
	runGitFixture(t, clean, "clone", "-q", repo, "bump")
	bump := filepath.Join(clean, "bump")
	cmd := exec.Command("git", "-C", bump, "apply", "-")
	cmd.Stdin = strings.NewReader(string(patch))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git apply: %v\n%s", err, out)
	}
	return bump
}

// The patch carries the changed table and nothing else, so applying it on a
// clean checkout of the bump reproduces the regenerated table only.
func TestFixturePatchCarriesOnlyTheInputsTable(t *testing.T) {
	repo := fixturePatchRepo(t)
	for rel, body := range map[string]string{
		"tests/fixtures/actions/inputs.tsv": "a\tb\nc\td\n",
		"tests/fixtures/flags/codex.txt":    "new\n",
		"tests/fixtures/actions/extra.tsv":  "x\n",
		"README.md":                         "not a fixture\n",
	} {
		writePatchFixture(t, repo, rel, body)
	}
	patch, err := FixturePatch(repo)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := patchNumstat(t, repo, patch), "1\t0\ttests/fixtures/actions/inputs.tsv\n"; got != want {
		t.Fatalf("patch numstat = %q, want %q: the patch must touch the inputs table only\n%s", got, want, patch)
	}
	bump := applyFixturePatch(t, repo, patch)
	for rel, want := range map[string]string{"tests/fixtures/actions/inputs.tsv": "a\tb\nc\td\n", "tests/fixtures/flags/codex.txt": "old\n", "README.md": "readme\n"} {
		got, err := os.ReadFile(filepath.Join(bump, rel))
		if err != nil || string(got) != want {
			t.Errorf("%s after apply = %q, %v; want %q", rel, got, err, want)
		}
	}
}

// A bump tree with no table gets the regenerated table as a new file, not an
// empty patch.
func TestFixturePatchIncludesANewTable(t *testing.T) {
	repo := fixturePatchRepo(t)
	runGitFixture(t, repo, "rm", "-q", "tests/fixtures/actions/inputs.tsv")
	commitPatchFixture(t, repo)
	writePatchFixture(t, repo, "tests/fixtures/actions/inputs.tsv", "n\tm\n")
	patch, err := FixturePatch(repo)
	if err != nil {
		t.Fatal(err)
	}
	bump := applyFixturePatch(t, repo, patch)
	if got, err := os.ReadFile(filepath.Join(bump, "tests/fixtures/actions/inputs.tsv")); err != nil || string(got) != "n\tm\n" {
		t.Fatalf("new table after apply = %q, %v", got, err)
	}
}

// A .gitattributes in the bump tree cannot turn the patch into a binary
// patch, run a filter, or change line endings: the patch stays a text diff
// of the rows.
func TestFixturePatchIgnoresBumpTreeAttributes(t *testing.T) {
	for _, attrs := range []string{"*.tsv binary\n", "*.tsv -diff\n", "*.tsv filter=evil\n", "*.tsv text eol=crlf\n"} {
		repo := fixturePatchRepo(t)
		writePatchFixture(t, repo, ".gitattributes", attrs)
		commitPatchFixture(t, repo)
		runGitFixture(t, repo, "config", "filter.evil.clean", "sed s/c/EVIL/")
		runGitFixture(t, repo, "config", "filter.evil.required", "true")
		writePatchFixture(t, repo, "tests/fixtures/actions/inputs.tsv", "a\tb\nc\td\n")
		patch, err := FixturePatch(repo)
		if err != nil {
			t.Fatalf("%q: %v", attrs, err)
		}
		// A binary patch counts "-\t-"; a filter would change the counts.
		if got, want := patchNumstat(t, repo, patch), "1\t0\ttests/fixtures/actions/inputs.tsv\n"; got != want {
			t.Errorf("%q: patch numstat = %q, want the text row count %q:\n%s", attrs, got, want, patch)
		}
	}
}

func TestFixturePatchIsEmptyForACleanTree(t *testing.T) {
	repo := fixturePatchRepo(t)
	patch, err := FixturePatch(repo)
	if err != nil || len(patch) != 0 {
		t.Fatalf("FixturePatch = %q, %v; want empty", patch, err)
	}
}

// A changed fixture that the diff cannot carry must fail, not pass as clean.
func TestFixturePatchRefusesAChangeTheDiffMisses(t *testing.T) {
	repo := fixturePatchRepo(t)
	// A mode-only change with core.fileMode off is a change status sees
	// nowhere in the diff; any such gap must fail.
	runGitFixture(t, repo, "config", "core.fileMode", "false")
	runGitFixture(t, repo, "update-index", "--chmod=+x", "tests/fixtures/actions/inputs.tsv")
	if _, err := FixturePatch(repo); err == nil {
		t.Fatal("FixturePatch passed a tree whose fixtures differ from the index but whose diff is empty")
	}
}
