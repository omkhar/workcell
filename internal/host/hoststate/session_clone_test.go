// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package hoststate

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

// makeRepoAt stands in for git clone: it makes repo in the pinned session
// directory through its descriptor, as a clone that fchdir put there does.
func makeRepoAt(dir *os.File) error {
	return unix.Mkdirat(int(dir.Fd()), SessionCloneRepoName, 0o700)
}

func TestCloneSessionWorkspaceMakesTheCloneInThePinnedDirectory(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "workcell-sessions")
	if err := CloneSessionWorkspace(parent, "s1", makeRepoAt); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]os.FileMode{parent: 0o700, filepath.Join(parent, "s1"): 0o700} {
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() || info.Mode().Perm() != want {
			t.Fatalf("%s = %v, %v; want a %o directory", path, info, err, want)
		}
	}
	if info, err := os.Lstat(filepath.Join(parent, "s1", SessionCloneRepoName)); err != nil || !info.IsDir() {
		t.Fatalf("clone = %v, %v", info, err)
	}
	// A second clone with the same name is refused and leaves the first.
	err := CloneSessionWorkspace(parent, "s1", makeRepoAt)
	if err == nil || !strings.Contains(err.Error(), "refusing to reuse") {
		t.Fatalf("reuse error = %v", err)
	}
	if _, err := os.Lstat(filepath.Join(parent, "s1", SessionCloneRepoName)); err != nil {
		t.Fatalf("the refused reuse removed the first clone: %v", err)
	}
}

func TestCloneSessionWorkspaceRefusesBadNamesAndLinkedParents(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "workcell-sessions")
	for _, name := range []string{"", ".", "..", "a/b", "-x", ".hidden", "a\nb", "../s"} {
		if err := CloneSessionWorkspace(parent, name, makeRepoAt); err == nil {
			t.Errorf("session name %q was accepted", name)
		}
	}
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(root, "linked")
	if err := os.Symlink(target, linked); err != nil {
		t.Fatal(err)
	}
	if err := CloneSessionWorkspace(linked, "s1", makeRepoAt); err == nil {
		t.Fatal("a symlinked session clone directory was accepted")
	}
	if entries, _ := os.ReadDir(target); len(entries) != 0 {
		t.Fatalf("the link target was written: %v", entries)
	}
}

// TestCloneSessionWorkspaceSurvivesAndDetectsASwap swaps a symlink in at the
// session clone directory after the check, as a concurrent process could. The
// clone still lands in the pinned directory and never under the link target,
// and the changed path fails the call and removes the session directory.
func TestCloneSessionWorkspaceSurvivesAndDetectsASwap(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "workcell-sessions")
	moved := filepath.Join(root, "moved")
	attacker := filepath.Join(root, "attacker")
	if err := os.MkdirAll(filepath.Join(attacker, "s1"), 0o700); err != nil {
		t.Fatal(err)
	}
	err := CloneSessionWorkspace(parent, "s1", func(dir *os.File) error {
		if err := os.Rename(parent, moved); err != nil {
			return err
		}
		if err := os.Symlink(attacker, parent); err != nil {
			return err
		}
		return makeRepoAt(dir)
	})
	if err == nil || !strings.Contains(err.Error(), "changed during the clone") {
		t.Fatalf("swap error = %v", err)
	}
	if _, err := os.Lstat(filepath.Join(attacker, "s1", SessionCloneRepoName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the clone landed under the link target: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(moved, "s1")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the failed clone left its session directory: %v", err)
	}
}

func TestCloneSessionWorkspaceRemovesTheSessionDirectoryOnFailure(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "workcell-sessions")
	failure := errors.New("clone failed")
	err := CloneSessionWorkspace(parent, "s1", func(dir *os.File) error {
		if err := makeRepoAt(dir); err != nil {
			return err
		}
		return failure
	})
	if !errors.Is(err, failure) {
		t.Fatalf("error = %v, want the clone failure", err)
	}
	if _, err := os.Lstat(filepath.Join(parent, "s1")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the session directory stayed: %v", err)
	}
	// A clone that makes no repo directory fails too.
	if err := CloneSessionWorkspace(parent, "s2", func(*os.File) error { return nil }); err == nil {
		t.Fatal("a clone with no repo directory was accepted")
	}
}

// swapParentForRealDirectory moves the session clone directory aside and puts
// a plain directory, not a link, at its path, as a concurrent process could.
// A walk of the path with no symlink then succeeds, so only the descriptor
// pins and the device and inode check tell the two apart.
func swapParentForRealDirectory(t *testing.T, parent, moved string, sessions ...string) {
	t.Helper()
	if err := os.Rename(parent, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, session := range sessions {
		if err := os.MkdirAll(filepath.Join(parent, session, SessionCloneRepoName), 0o700); err != nil {
			t.Fatal(err)
		}
	}
}

// TestCloneSessionWorkspaceDetectsARealDirectorySwap swaps a plain directory
// in at each step: before the session directory is made, before it is
// opened, and during the clone. The session directory and the clone stay in
// the pinned directory, and the call fails on the changed path.
func TestCloneSessionWorkspaceDetectsARealDirectorySwap(t *testing.T) {
	for _, step := range []string{"mkdir", "open", "clone"} {
		t.Run(step, func(t *testing.T) {
			root := t.TempDir()
			parent := filepath.Join(root, "workcell-sessions")
			moved := filepath.Join(root, "moved")
			// Before the mkdir the swapped-in directory is empty, so a
			// path-based mkdir makes s1 in it; later it holds an empty s1 that
			// a path-based open would clone into.
			swapPoint = func(at string) {
				switch {
				case at == step && step == "mkdir":
					swapParentForRealDirectory(t, parent, moved)
				case at == step:
					swapParentForRealDirectory(t, parent, moved, "s1")
					if err := os.Remove(filepath.Join(parent, "s1", SessionCloneRepoName)); err != nil {
						t.Fatal(err)
					}
				}
			}
			defer func() { swapPoint = nil }()
			err := CloneSessionWorkspace(parent, "s1", func(dir *os.File) error {
				if step == "clone" {
					swapParentForRealDirectory(t, parent, moved, "s1")
				}
				return makeRepoAt(dir)
			})
			if err == nil || !strings.Contains(err.Error(), "changed during the clone") {
				t.Fatalf("swap error = %v", err)
			}
			if step != "clone" {
				if _, err := os.Lstat(filepath.Join(parent, "s1", SessionCloneRepoName)); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("the clone landed in the swapped-in directory: %v", err)
				}
			}
			if step == "mkdir" {
				if _, err := os.Lstat(filepath.Join(parent, "s1")); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("the session directory was made in the swapped-in directory: %v", err)
				}
			}
			if _, err := os.Lstat(filepath.Join(moved, "s1")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("the failed clone left its session directory: %v", err)
			}
		})
	}
}
