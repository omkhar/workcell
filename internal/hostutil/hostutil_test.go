// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package hostutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCanonicalizePathResolvesHomeAndSymlinks(t *testing.T) {
	tmp := t.TempDir()
	realHome := filepath.Join(tmp, "real-home")
	linkHome := filepath.Join(tmp, "link-home")
	if err := os.MkdirAll(realHome, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realHome, linkHome); err != nil {
		t.Fatal(err)
	}

	got, err := canonicalizeForTest(t, "~/debug/workcell.log", linkHome)
	if err != nil {
		t.Fatal(err)
	}

	canonicalHome, err := filepath.EvalSymlinks(realHome)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(canonicalHome, "debug", "workcell.log")
	if got != want {
		t.Fatalf("canonicalizeForTest() = %q, want %q", got, want)
	}
}

func TestCanonicalizePathResolvesMissingSuffixBehindSymlink(t *testing.T) {
	tmp := t.TempDir()
	realRoot := filepath.Join(tmp, "real-root")
	linkRoot := filepath.Join(tmp, "link-root")
	if err := os.MkdirAll(realRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realRoot, linkRoot); err != nil {
		t.Fatal(err)
	}

	got, err := canonicalizeForTest(t, filepath.Join(linkRoot, "missing", "child"), realRoot)
	if err != nil {
		t.Fatal(err)
	}

	canonicalRoot, err := filepath.EvalSymlinks(realRoot)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(canonicalRoot, "missing", "child")
	if got != want {
		t.Fatalf("canonicalizeForTest() = %q, want %q", got, want)
	}
}

func TestCanonicalizePathFromUsesExplicitBaseForRelativePaths(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	base := filepath.Join(tmp, "workspace")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	canonicalBase, err := filepath.EvalSymlinks(base)
	if err != nil {
		t.Fatal(err)
	}

	got, err := CanonicalizePathFrom(filepath.Join("configs", "policy.toml"), base)
	if err != nil {
		t.Fatal(err)
	}

	want := filepath.Join(canonicalBase, "configs", "policy.toml")
	if got != want {
		t.Fatalf("CanonicalizePathFrom() = %q, want %q", got, want)
	}
}

func canonicalizeForTest(t *testing.T, path, home string) (string, error) {
	t.Helper()
	t.Setenv("HOME", home)
	return CanonicalizePath(path)
}
