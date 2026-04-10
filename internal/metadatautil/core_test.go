// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestWalkFilesSkipsExcludedPaths(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "adapters", "keep"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "adapters", "node_modules", "skip"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "adapters", "keep", "a.txt"), []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "adapters", "node_modules", "skip", "b.txt"), []byte("b"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := walkFiles(root, "adapters", "node_modules", "target")
	if err != nil {
		t.Fatalf("walkFiles() error = %v", err)
	}
	want := []string{filepath.Join("adapters", "keep", "a.txt")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("walkFiles() = %#v, want %#v", got, want)
	}
}

func TestWalkRepoFilesSkipsExcludedPaths(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{".git", "dist", "tmp", "node_modules", "target", "pkg"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "pkg", "keep.txt"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "dist", "ignore.txt"), []byte("ignore"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "target", "ignore.txt"), []byte("ignore"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "pkg", "skip.pyc"), []byte("skip"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := walkRepoFiles(root)
	if err != nil {
		t.Fatalf("walkRepoFiles() error = %v", err)
	}
	want := []string{filepath.Join("pkg", "keep.txt")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("walkRepoFiles() = %#v, want %#v", got, want)
	}
}

func TestGitTrackedFilesExcludesUntrackedFiles(t *testing.T) {
	root := t.TempDir()
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("EvalSymlinks(%s) error = %v", root, err)
	}

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%v failed: %v\n%s", args, err, output)
		}
	}

	run("git", "init", "-q", canonicalRoot)
	run("git", "-C", canonicalRoot, "config", "user.name", "Workcell Tests")
	run("git", "-C", canonicalRoot, "config", "user.email", "workcell-tests@example.com")
	run("git", "-C", canonicalRoot, "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(canonicalRoot, "tracked.txt"), []byte("tracked\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("git", "-C", canonicalRoot, "add", "tracked.txt")
	run("git", "-C", canonicalRoot, "commit", "-q", "-m", "init")
	if err := os.WriteFile(filepath.Join(canonicalRoot, "tracked.txt"), []byte("modified\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(canonicalRoot, "scratch.txt"), []byte("untracked\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, tracked, err := gitTrackedFiles(canonicalRoot)
	if err != nil {
		t.Fatalf("gitTrackedFiles() error = %v", err)
	}
	if !tracked {
		t.Fatal("gitTrackedFiles() should report tracked repository context")
	}
	want := []string{"tracked.txt"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("gitTrackedFiles() = %#v, want %#v", got, want)
	}
}
