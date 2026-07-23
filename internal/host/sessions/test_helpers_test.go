// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package sessions

import (
	"path/filepath"
	"testing"
)

// physicalTempDir removes platform aliases such as macOS /var -> /private/var
// so raw-path tests exercise the descriptor walker rather than an OS symlink.
func physicalTempDir(t testing.TB) string {
	t.Helper()
	dir := t.TempDir()
	physical, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return physical
}
