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
func TestHardenedFSReadSourceRefusesASwappedLeaf(t *testing.T) {
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
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatalf("open the root: %v", err)
	}
	defer root.Close() //nolint:errcheck // read-only handle
	if content, err := hardenedFSReadSource(root, "sub/real.go", "real.go"); err != nil || string(content) != "package sub\n" {
		t.Fatalf("read a regular source = %q, %v", content, err)
	}
	if _, err := hardenedFSReadSource(root, "sub/swapped.go", "swapped.go"); err == nil {
		t.Fatal("expected a symlink under the walked name to be refused")
	}
}
