// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package rootio

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
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

func TestMarshalCompactJSONBoundsAndPreservesFormat(t *testing.T) {
	value := map[string]any{"items": []any{"quote \"[]{}:,\\\\", map[string]any{"name": "two"}}}
	want, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	want = append(want, '\n')
	got, err := MarshalCompactJSON(value, "test JSON", int64(len(want)))
	if err != nil {
		t.Fatalf("MarshalCompactJSON exact limit: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("MarshalCompactJSON changed output:\n got %q\nwant %q", got, want)
	}
	if _, err := MarshalCompactJSON(value, "test JSON", int64(len(want)-1)); err == nil {
		t.Fatal("MarshalCompactJSON accepted output over the byte limit")
	}
}

func TestMarshalCompactJSONAvoidsPrettyPrintAmplification(t *testing.T) {
	const depth = 3000
	compact := []byte(strings.Repeat("[", depth) + "null" + strings.Repeat("]", depth))
	var value any
	if err := json.Unmarshal(compact, &value); err != nil {
		t.Fatal(err)
	}
	compact, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(compact)) >= MaxManifestBytes {
		t.Fatalf("compact fixture size = %d, want less than %d", len(compact), MaxManifestBytes)
	}
	indented, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(indented)+1) <= MaxManifestBytes {
		t.Fatalf("indented fixture does not exceed %d bytes", MaxManifestBytes)
	}
	if got, err := MarshalCompactJSON(value, "injection manifest", MaxManifestBytes); err != nil || int64(len(got)) >= MaxManifestBytes {
		t.Fatalf("MarshalCompactJSON amplification result = %d bytes, %v", len(got), err)
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

func TestRemoveAllAtNoFollowStaysInsideTheTree(t *testing.T) {
	base := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "keep"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	tree := filepath.Join(base, "tree")
	if err := os.MkdirAll(filepath.Join(tree, "a", "b"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree, "a", "b", "file"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(tree, "a", "link")); err != nil {
		t.Fatal(err)
	}
	parent, leaf, err := OpenParentDirectoryNoFollow(tree)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	if err := RemoveAllAtNoFollow(parent, filepath.Base(leaf)); err != nil {
		t.Fatalf("RemoveAllAtNoFollow error = %v", err)
	}
	if _, err := os.Lstat(tree); !os.IsNotExist(err) {
		t.Fatalf("tree remains: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "keep")); err != nil {
		t.Fatalf("a link inside the tree was followed: %v", err)
	}
	// A missing name is not an error, and a name with a separator is refused.
	if err := RemoveAllAtNoFollow(parent, "absent"); err != nil {
		t.Fatalf("missing name error = %v", err)
	}
	if err := RemoveAllAtNoFollow(parent, "a/b"); err == nil {
		t.Fatal("RemoveAllAtNoFollow accepted a path with a separator")
	}
	// A symlinked ancestor is refused before anything is removed.
	link := filepath.Join(base, "ancestor-link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if _, _, err := OpenParentDirectoryNoFollow(filepath.Join(link, "keep")); err == nil {
		t.Fatal("OpenParentDirectoryNoFollow followed a symlinked ancestor")
	}
}

func TestRemoveAllAtTreatsAnObservedVanishedEntryAsFailure(t *testing.T) {
	parent, err := os.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	fd := int(parent.Fd())
	if err := removeAllAt(fd, "absent", false); err != nil {
		t.Fatalf("a named absent entry error = %v", err)
	}
	if err := removeAllAt(fd, "absent", true); err == nil {
		t.Fatal("an entry seen in a directory read vanished without an error")
	}
}

func TestRemoveAllAtNoFollowRemovesAnEmptyUnreadableDirectoryOnly(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root reads every directory")
	}
	root := t.TempDir()
	parent, err := os.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	empty := filepath.Join(root, "empty")
	if err := os.Mkdir(empty, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(empty, 0); err != nil {
		t.Fatal(err)
	}
	if err := RemoveAllAtNoFollow(parent, "empty"); err != nil {
		t.Fatalf("empty unreadable directory not removed: %v", err)
	}
	full := filepath.Join(root, "full")
	if err := os.MkdirAll(filepath.Join(full, "child"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(full, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(full, 0o700) })
	if err := RemoveAllAtNoFollow(parent, "full"); err == nil {
		t.Fatal("non-empty unreadable directory reported removed")
	}
	if _, err := os.Lstat(full); err != nil {
		t.Fatalf("non-empty unreadable directory vanished: %v", err)
	}
}
