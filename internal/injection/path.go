// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package injection

import (
	"os"
	"path/filepath"
)

// Path is a string-typed wrapper for filesystem paths used inside the
// injection package.  It is a thin convenience type — every call site
// at the os/filepath boundary must still bounce through String().  The
// type stays here, at the top of its own file, so a reader skimming
// the injection sources sees the abstraction before the consumers
// instead of meeting it at the bottom of a 1900-line monolith.
type Path string

// String returns the underlying filesystem path string.
func (p Path) String() string {
	return string(p)
}

// Parent returns the parent directory of p.
func (p Path) Parent() Path {
	return Path(filepath.Dir(string(p)))
}

// Join appends rel to p.
func (p Path) Join(rel string) Path {
	return Path(filepath.Join(string(p), rel))
}

// Base returns the last element of p.
func (p Path) Base() string {
	return filepath.Base(string(p))
}

// IsDir reports whether p exists and is a directory.  Errors (ENOENT,
// EACCES, ELOOP) are folded into a single `false` result; callers
// that need to distinguish should Stat directly.
func (p Path) IsDir() bool {
	info, err := os.Stat(string(p))
	return err == nil && info.IsDir()
}
