// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package injection

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// isSafeRelativeSymlinkTarget returns true when target is a relative
// path with no `..` segments and no leading `/`. This is the shape macOS
// uses for `/var -> private/var`, `/etc -> private/etc`,
// `/tmp -> private/tmp` and is *not* the shape an attacker would plant
// to escape a managed staging tree (those almost always use an absolute
// target or a `..`-laden relative target).
func isSafeRelativeSymlinkTarget(target string) bool {
	if target == "" {
		return false
	}
	if filepath.IsAbs(target) {
		return false
	}
	for _, seg := range strings.Split(target, "/") {
		if seg == ".." {
			return false
		}
	}
	return true
}

// findUnsafeSymlinkInPathChain walks every component of cleanedPath
// from the filesystem root down to (and including) the leaf, Lstat'ing
// each step. It returns the first component path that is a symbolic
// link whose target fails isSafeRelativeSymlinkTarget. Returns "" with
// a nil error when no offending symlink is found.
//
// The safe-shape rule accepts macOS system symlinks
// (`/var -> private/var`, `/etc -> private/etc`, `/tmp -> private/tmp`),
// which production paths and `t.TempDir()` traversals rely on. It
// rejects:
//   - any link with an absolute target (the canonical attack pattern
//     `parent-link -> /etc`),
//   - any link whose target contains a `..` segment (the relative
//     sideways/upward escape `parent-link -> ../../etc`).
//
// Errors other than os.ErrNotExist abort the walk: a transient I/O
// failure on Lstat must not silently skip the symlink check. ENOENT
// on an intermediate component returns ("" , nil) so the caller's
// subsequent os.Stat can surface its canonical "missing" diagnostic.
//
// TOCTOU note: this leaves a window between the walk completing and
// any subsequent Stat/Open on cleanedPath. A complete TOCTOU-free
// defense would hold an open file descriptor on each directory and
// stage from there, which would require restructuring the callers.
// The current walk closes the *static* symlink-planting hole; an
// attacker would still need to win a sub-millisecond race to swap a
// component after the check but before the consuming syscall, and
// would already need write access to a path component — already a
// privileged position.
func findUnsafeSymlinkInPathChain(cleanedPath string) (string, error) {
	parts := strings.Split(cleanedPath, string(filepath.Separator))
	current := string(filepath.Separator)
	for _, part := range parts {
		if part == "" {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return "", nil
			}
			return "", err
		}
		if info.Mode()&fs.ModeSymlink == 0 {
			continue
		}
		target, rerr := os.Readlink(current)
		if rerr != nil {
			return "", rerr
		}
		if !isSafeRelativeSymlinkTarget(target) {
			return current, nil
		}
	}
	return "", nil
}
