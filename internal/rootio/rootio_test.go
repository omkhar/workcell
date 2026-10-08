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

// A directory swapped in at the name during the recursion survives, and the
// traversed tree goes. Not parallel: the test sets the package hook.
func TestRemoveAllAtNoFollowRemovesTheTraversedTree(t *testing.T) {
	base := t.TempDir()
	tree := filepath.Join(base, "tree")
	if err := os.MkdirAll(filepath.Join(tree, "a"), 0o700); err != nil {
		t.Fatal(err)
	}
	removeAllHook = func() {
		removeAllHook = func() {}
		_ = os.Rename(tree, filepath.Join(base, "moved")) // the name is already staged after the fix
		if err := os.Mkdir(tree, 0o700); err != nil {
			t.Error(err)
		}
	}
	defer func() { removeAllHook = func() {} }()
	parent, err := os.Open(base)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	if err := RemoveAllAtNoFollow(parent, "tree"); err != nil {
		t.Fatalf("RemoveAllAtNoFollow() error = %v", err)
	}
	entries, err := os.ReadDir(base)
	if err != nil || len(entries) != 1 || entries[0].Name() != "tree" {
		t.Fatalf("expected only the replacement to remain, found %v, %v", entries, err)
	}
}

// A replacement placed at the staged name after the traversal is kept, and
// the call names the swap. Not parallel: the test sets the package hook.
func TestRemoveAllAtNoFollowRefusesAStagedNameSwap(t *testing.T) {
	base := t.TempDir()
	if err := os.Mkdir(filepath.Join(base, "tree"), 0o700); err != nil {
		t.Fatal(err)
	}
	var staged string
	removeAllHook = func() {
		removeAllHook = func() {}
		matches, _ := filepath.Glob(filepath.Join(base, ".workcell-rm-*"))
		if len(matches) != 1 {
			t.Errorf("expected one staged entry, found %v", matches)
			return
		}
		staged = matches[0]
		if err := os.Rename(staged, filepath.Join(base, "moved")); err != nil {
			t.Error(err)
		}
		if err := os.Mkdir(staged, 0o700); err != nil {
			t.Error(err)
		}
	}
	defer func() { removeAllHook = func() {} }()
	parent, err := os.Open(base)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	if err := RemoveAllAtNoFollow(parent, "tree"); err == nil || !strings.Contains(err.Error(), "swapped") {
		t.Fatalf("RemoveAllAtNoFollow() error = %v, want a swap error", err)
	}
	for _, kept := range []string{staged, filepath.Join(base, "moved")} {
		if _, err := os.Lstat(kept); err != nil {
			t.Fatalf("expected %s to remain, found %v", kept, err)
		}
	}
}
