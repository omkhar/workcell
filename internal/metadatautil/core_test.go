// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"os"
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
