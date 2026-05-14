// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package launcher

import (
	"os"

	"github.com/omkhar/workcell/internal/pathutil"
)

// CanonicalizePath is a thin wrapper around pathutil.CanonicalizePath
// in best-effort mode (the launcher historically used the lenient
// ~user fallback).  New code should call pathutil.CanonicalizePath
// directly.
func CanonicalizePath(path string) (string, error) {
	return CanonicalizePathFrom(path, "")
}

// CanonicalizePathFrom is a thin wrapper around pathutil.CanonicalizePath
// that anchors relative inputs against base (falling back to the
// process cwd when base is empty).
func CanonicalizePathFrom(path, base string) (string, error) {
	return pathutil.CanonicalizePath(path, pathutil.Options{Base: base})
}

func RealHome() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return CanonicalizePath(home)
}

func CanonicalizeToolPath(candidate string) (string, error) {
	if candidate == "" {
		return "", nil
	}
	return CanonicalizePath(candidate)
}
