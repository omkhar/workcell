// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package injection

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// isAllowedSystemSymlink returns true only for the small set of
// platform bootstrap links that Workcell must traverse on macOS.
// Operator-controlled direct-mount sources remain symlink-free.
//
// The allowlist intentionally fires only on darwin: on Linux these
// link targets (`/var -> private/var`, etc.) have no legitimate
// meaning, so a match there is evidence of an attacker planting the
// same name to bypass the check. Mirrors the GOOS == "darwin" gate
// in internal/secretfile/open_unix.go::canonicalizeSystemPath.
func isAllowedSystemSymlink(linkPath, target string) bool {
	if runtime.GOOS != "darwin" {
		return false
	}
	switch filepath.Clean(linkPath) {
	case "/var":
		return target == "private/var"
	case "/etc":
		return target == "private/etc"
	case "/tmp":
		return target == "private/tmp"
	default:
		return false
	}
}

func canonicalizeAllowedSystemPath(path string) string {
	if runtime.GOOS != "darwin" {
		return path
	}
	for _, prefix := range []string{"/var", "/etc", "/tmp"} {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return filepath.Join("/private", strings.TrimPrefix(path, string(filepath.Separator)))
		}
	}
	return path
}

// findUnsafeSymlinkInPathChain walks every component of cleanedPath
// from the filesystem root down to (and including) the leaf, Lstat'ing
// each step. It returns the first component path that is a symbolic
// link that is not a reviewed system symlink. Returns "" with
// a nil error when no offending symlink is found.
//
// The allowlist accepts only macOS system symlinks (`/var ->
// private/var`, `/etc -> private/etc`, `/tmp -> private/tmp`), which
// production paths and `t.TempDir()` traversals rely on. Every
// operator-controlled symlink in the direct-mount source chain is
// rejected, including relative targets that do not contain `..`.
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
		if !isAllowedSystemSymlink(current, target) {
			return current, nil
		}
	}
	return "", nil
}
