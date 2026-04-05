// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package paritytree

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompareDirectoryTreesDetectsMismatch(t *testing.T) {
	t.Parallel()

	left := t.TempDir()
	right := t.TempDir()
	if err := os.WriteFile(filepath.Join(left, "value.txt"), []byte("left"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(right, "value.txt"), []byte("right"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := CompareDirectoryTrees(left, right)
	if err == nil {
		t.Fatal("CompareDirectoryTrees returned nil error")
	}
	if !strings.Contains(err.Error(), "tree mismatch") {
		t.Fatalf("error %q does not mention tree mismatch", err)
	}
	if !strings.Contains(strings.ToLower(err.Error()), "sha256") {
		t.Fatalf("error %q does not mention sha256 mismatch", err)
	}
}

func TestSnapshotCapturesSymlinks(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "dir"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "dir", "file.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("dir", "file.txt"), filepath.Join(root, "link.txt")); err != nil {
		t.Fatal(err)
	}

	entries, err := Snapshot(root)
	if err != nil {
		t.Fatalf("Snapshot returned error: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("len(entries) = %d, want 3", len(entries))
	}
	if entries[0].Path != "dir" || entries[0].Kind != "dir" || !entries[0].Mode.IsDir() {
		t.Fatalf("dir entry = %#v", entries[0])
	}
	wantHash := sha256.Sum256([]byte("hello"))
	if entries[1].Path != filepath.ToSlash(filepath.Join("dir", "file.txt")) || entries[1].Kind != "file" || !entries[1].Mode.IsRegular() || entries[1].SHA256 != hex.EncodeToString(wantHash[:]) {
		t.Fatalf("file entry = %#v", entries[1])
	}
	if entries[2].Path != "link.txt" || entries[2].Kind != "symlink" || entries[2].Mode&os.ModeSymlink == 0 || entries[2].LinkTarget != filepath.Join("dir", "file.txt") {
		t.Fatalf("symlink entry = %#v", entries[2])
	}
}
