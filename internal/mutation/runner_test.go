// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package mutation

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCopyTreeSkipsNamedDirectories(t *testing.T) {
	source := t.TempDir()
	destination := filepath.Join(t.TempDir(), "copy")
	for _, dir := range []string{"src", "target/debug", "fuzz/target", "fuzz/artifacts"} {
		if err := os.MkdirAll(filepath.Join(source, dir), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	for _, file := range []string{"Cargo.toml", "src/lib.rs", "target/debug/artifact.bin", "fuzz/target/artifact.bin", "fuzz/artifacts/crash-1"} {
		if err := os.WriteFile(filepath.Join(source, file), []byte("content\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", file, err)
		}
	}

	if err := copyTree(source, destination, "target", "artifacts"); err != nil {
		t.Fatalf("copyTree: %v", err)
	}

	for _, file := range []string{"Cargo.toml", "src/lib.rs"} {
		if _, err := os.Stat(filepath.Join(destination, file)); err != nil {
			t.Errorf("expected %s in the copy: %v", file, err)
		}
	}
	for _, path := range []string{"target", "fuzz/target", "fuzz/artifacts"} {
		if _, err := os.Stat(filepath.Join(destination, path)); !os.IsNotExist(err) {
			t.Errorf("expected %s to be skipped, stat err = %v", path, err)
		}
	}
}

func TestCopyTreeWithoutSkipsCopiesEverything(t *testing.T) {
	source := t.TempDir()
	destination := filepath.Join(t.TempDir(), "copy")
	if err := os.MkdirAll(filepath.Join(source, "target"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(source, "target", "keep.txt"), []byte("keep\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := copyTree(source, destination); err != nil {
		t.Fatalf("copyTree: %v", err)
	}
	if _, err := os.Stat(filepath.Join(destination, "target", "keep.txt")); err != nil {
		t.Errorf("expected unskipped copy to keep target/keep.txt: %v", err)
	}
}
