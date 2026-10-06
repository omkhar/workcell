// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"os"
	"path/filepath"
	"testing"
)

// The read must not trust the walk entry. A rename after the walk can leave a
// symlink under the walked name, and os.Root follows one whose target stays
// inside the root, so the scan would read bytes other than the checked source.
func TestScanHardenedGoSourcesRefusesASwappedLeaf(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatalf("create the fixture directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "real.go"), []byte("package sub\n"), 0o600); err != nil {
		t.Fatalf("write the fixture: %v", err)
	}
	if err := os.Symlink("real.go", filepath.Join(dir, "sub", "swapped.go")); err != nil {
		t.Fatalf("create the swapped leaf: %v", err)
	}
	var visited []string
	err := scanHardenedGoSources(dir, "sub", func(rel string, content []byte) error {
		if string(content) != "package sub\n" {
			t.Fatalf("read %s = %q", rel, content)
		}
		visited = append(visited, rel)
		return nil
	})
	if err == nil {
		t.Fatal("expected a symlink under the walked name to be refused")
	}
	if len(visited) != 1 || visited[0] != "sub/real.go" {
		t.Fatalf("expected the regular source to be read first, found %v", visited)
	}
}

// A directory swapped for an in-root symlink after the walk listed its parent
// must be refused. os.Root follows that symlink, so the scan would read the
// decoy's sources under the swapped directory's name and report them clean.
func TestScanHardenedGoSourcesRefusesASwappedDirectory(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for _, name := range []string{"a/a.go", "b/b.go", "decoy/b.go"} {
		if err := os.MkdirAll(filepath.Join(dir, "pkg", filepath.Dir(name)), 0o755); err != nil {
			t.Fatalf("create the fixture directory: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "pkg", name), []byte("package x\n"), 0o600); err != nil {
			t.Fatalf("write the fixture: %v", err)
		}
	}
	swapped := false
	err := scanHardenedGoSources(dir, "pkg", func(rel string, _ []byte) error {
		if swapped {
			return nil
		}
		// The walk has listed pkg and visited a/a.go; b is not opened yet.
		swapped = true
		if err := os.Rename(filepath.Join(dir, "pkg", "b"), filepath.Join(dir, "b-real")); err != nil {
			return err
		}
		return os.Symlink("decoy", filepath.Join(dir, "pkg", "b"))
	})
	if !swapped {
		t.Fatal("the scan visited no source")
	}
	if err == nil {
		t.Fatal("expected a directory swapped for a symlink to be refused")
	}
}
