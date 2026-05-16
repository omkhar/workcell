// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package pathutil

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCanonicalizePathStrictRejectsEmpty(t *testing.T) {
	t.Parallel()

	_, err := CanonicalizePath("", Options{Strict: true})
	if err == nil {
		t.Fatal("expected error for empty strict path")
	}
	if !errors.Is(err, ErrEmptyPath) {
		t.Fatalf("error %q is not ErrEmptyPath", err.Error())
	}
}

func TestCanonicalizePathBestEffortAcceptsEmpty(t *testing.T) {
	t.Parallel()

	// BestEffort mirrors the legacy metadatautil semantics, which
	// historically accepted the empty string by treating it as the
	// current working directory.  The result is the absolute form of
	// "." — checking only that the call does not error.
	if _, err := CanonicalizePath("", Options{}); err != nil {
		t.Fatalf("unexpected error for empty best-effort path: %v", err)
	}
}

func TestCanonicalizePathBestEffortAbsolutePassThrough(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	got, err := CanonicalizePath(tmp, Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resolved, _ := filepath.EvalSymlinks(tmp)
	if resolved == "" {
		resolved = filepath.Clean(tmp)
	}
	if got != resolved {
		t.Fatalf("CanonicalizePath = %q, want %q", got, resolved)
	}
}

func TestCanonicalizePathStrictAbsoluteWithMissingLeaf(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	missing := filepath.Join(tmp, "does-not-exist", "leaf")
	got, err := CanonicalizePath(missing, Options{Strict: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// ResolveBestEffort returns the path with the existing prefix
	// canonicalised; the missing suffix is appended verbatim.
	resolvedTmp, _ := filepath.EvalSymlinks(tmp)
	if resolvedTmp == "" {
		resolvedTmp = filepath.Clean(tmp)
	}
	want := filepath.Join(resolvedTmp, "does-not-exist", "leaf")
	if got != want {
		t.Fatalf("CanonicalizePath = %q, want %q", got, want)
	}
}

func TestCanonicalizePathRelativeAnchorsAgainstBase(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	got, err := CanonicalizePath("nested/leaf", Options{Base: base})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resolvedBase, _ := filepath.EvalSymlinks(base)
	if resolvedBase == "" {
		resolvedBase = filepath.Clean(base)
	}
	want := filepath.Join(resolvedBase, "nested", "leaf")
	if got != want {
		t.Fatalf("CanonicalizePath = %q, want %q", got, want)
	}
}

func TestCanonicalizePathRelativeNoBaseUsesCwd(t *testing.T) {
	t.Parallel()

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	got, err := CanonicalizePath("relative-target", Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resolvedCwd, _ := filepath.EvalSymlinks(cwd)
	if resolvedCwd == "" {
		resolvedCwd = filepath.Clean(cwd)
	}
	want := filepath.Join(resolvedCwd, "relative-target")
	if got != want {
		t.Fatalf("CanonicalizePath = %q, want %q", got, want)
	}
}

func TestCanonicalizePathStrictUnknownUserRejected(t *testing.T) {
	t.Parallel()

	// Use a username that is extremely unlikely to exist in CI.
	_, err := CanonicalizePath("~this-user-should-not-exist-anywhere-1234567/foo", Options{Strict: true})
	if err == nil {
		t.Fatal("expected error for unknown ~user in strict mode")
	}
}

func TestCanonicalizePathBestEffortUnknownUserKept(t *testing.T) {
	t.Parallel()

	// Best-effort returns the raw path when ~user lookup fails; the
	// resulting path is relative (starts with ~) so it gets anchored
	// against cwd or Base.  We only check that no error is returned —
	// the exact resolved form depends on cwd.
	if _, err := CanonicalizePath("~this-user-should-not-exist-anywhere-1234567/foo", Options{}); err != nil {
		t.Fatalf("unexpected error for unknown ~user in best-effort mode: %v", err)
	}
}
