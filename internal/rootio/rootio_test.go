// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package rootio

import (
	"bytes"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestReadFileNoFollowMaxIntLimit(t *testing.T) {
	rootDir := t.TempDir()
	path := filepath.Join(rootDir, "manifest.json")
	want := []byte(`{"version":1}`)
	if err := os.WriteFile(path, want, 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := ReadFileNoFollow(path, "manifest", math.MaxInt64)
	if err != nil {
		t.Fatalf("ReadFileNoFollow returned error: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("content mismatch: got %q, want %q", got, want)
	}
}

func TestWriteFileAtomicWritesContentAndMode(t *testing.T) {
	rootDir := t.TempDir()
	root, err := os.OpenRoot(rootDir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	if err := WriteFileAtomic(root, filepath.Join("resolved", "credentials", "token.json"), []byte("{\"token\":\"x\"}\n"), 0o600, ".test-"); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(rootDir, "resolved", "credentials", "token.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, []byte("{\"token\":\"x\"}\n")) {
		t.Fatalf("content mismatch: got %q", data)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("mode = %v, want %v", got, os.FileMode(0o600))
	}
}

func TestWriteFileAtomicRejectsSymlinkEscape(t *testing.T) {
	rootDir := t.TempDir()
	escapeDir := filepath.Join(t.TempDir(), "escape")
	if err := os.MkdirAll(escapeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(escapeDir, filepath.Join(rootDir, "resolved")); err != nil {
		t.Fatal(err)
	}

	root, err := os.OpenRoot(rootDir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	err = WriteFileAtomic(root, filepath.Join("resolved", "credentials", "token.json"), []byte("secret\n"), 0o600, ".test-")
	if err == nil {
		t.Fatal("WriteFileAtomic unexpectedly succeeded through escaping symlink")
	}
	if _, statErr := os.Stat(filepath.Join(escapeDir, "credentials", "token.json")); !os.IsNotExist(statErr) {
		t.Fatalf("escaped write unexpectedly materialized: %v", statErr)
	}
}

func TestRelativePathWithinRejectsOutsideRoot(t *testing.T) {
	rootDir := t.TempDir()
	outside := filepath.Join(filepath.Dir(rootDir), "outside.txt")
	if _, err := RelativePathWithin(rootDir, outside, "test"); err == nil {
		t.Fatal("RelativePathWithin unexpectedly accepted a path outside the root")
	}
}
