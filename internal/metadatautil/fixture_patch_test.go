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
	write := func(rel, body string) {
		t.Helper()
		path := filepath.Join(repo, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("tests/fixtures/actions/inputs.tsv", "a\tb\n")
	write("tests/fixtures/flags/codex.txt", "old\n")
	write("tests/fixtures/flags/gone.txt", "gone\n")
	write("README.md", "readme\n")
	runGitFixture(t, repo, "init", "-q")
	runGitFixture(t, repo, "add", ".")
	runGitFixture(t, repo, "-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "-q", "-m", "base")
	return repo
}

// The patch carries new, changed and deleted fixtures, so applying it on a
// clean checkout of the bump reproduces the regenerated tree.
func TestFixturePatchIncludesUntrackedChangedAndDeletedFixtures(t *testing.T) {
	repo := fixturePatchRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "tests/fixtures/flags/codex.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(repo, "tests/fixtures/flags/gone.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "tests/fixtures/flags/claude.txt"), []byte("added\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("not a fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	patch, err := FixturePatch(repo)
	if err != nil {
		t.Fatal(err)
	}
	clean := t.TempDir()
	runGitFixture(t, clean, "clone", "-q", repo, "bump")
	bump := filepath.Join(clean, "bump")
	cmd := exec.Command("git", "-C", bump, "apply", "-")
	cmd.Stdin = strings.NewReader(string(patch))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git apply: %v\n%s", err, out)
	}
	// README.md is outside the fixtures, so the patch leaves it as committed.
	for rel, want := range map[string]string{"tests/fixtures/flags/codex.txt": "new\n", "tests/fixtures/flags/claude.txt": "added\n", "README.md": "readme\n"} {
		got, err := os.ReadFile(filepath.Join(bump, rel))
		if err != nil || string(got) != want {
			t.Errorf("%s after apply = %q, %v; want %q", rel, got, err, want)
		}
	}
	if _, err := os.Stat(filepath.Join(bump, "tests/fixtures/flags/gone.txt")); !os.IsNotExist(err) {
		t.Errorf("gone.txt survived the patch: %v", err)
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
	runGitFixture(t, repo, "update-index", "--chmod=+x", "tests/fixtures/flags/codex.txt")
	if _, err := FixturePatch(repo); err == nil {
		t.Fatal("FixturePatch passed a tree whose fixtures differ from the index but whose diff is empty")
	}
}
