// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package pathutil

import (
	"errors"
	"os"
	"path/filepath"
)

// Options control how CanonicalizePath resolves a raw path.
//
// The zero value matches the legacy "best-effort, no base" behaviour
// previously implemented in metadatautil and the host launcher: ~ is
// expanded best-effort, relative paths are anchored against
// os.Getwd, and ResolveBestEffort is used for the symlink walk so
// non-existent leaf components are tolerated.
//
// Strict=true mirrors the runtimeutil path canonicalizer used by
// container injection: ExpandUserPathStrict is used (unknown ~user
// fails), the empty input is rejected, and the result still flows
// through ResolveBestEffort so callers can canonicalize paths whose
// leaf has not been created yet (e.g. injection bundle outputs).
//
// Base, when non-empty, is the directory used to anchor relative
// inputs.  When Base is empty the legacy fallback (os.Getwd) is
// applied.  A relative Base is itself resolved with filepath.Abs so
// the final join is always absolute.
type Options struct {
	Strict bool
	Base   string
}

// CanonicalizePath is the canonical replacement for the three
// CanonicalizePath helpers that previously lived in
// runtimeutil/metadatautil/host launcher.  Each of those packages
// keeps a thin wrapper so existing call sites continue to compile;
// new code should call CanonicalizePath directly.
func CanonicalizePath(path string, opts Options) (string, error) {
	if opts.Strict && path == "" {
		return "", errors.New("empty path")
	}

	expanded, err := expandUserPath(path, opts.Strict)
	if err != nil {
		return "", err
	}

	if !filepath.IsAbs(expanded) {
		base := opts.Base
		if base == "" {
			base, err = os.Getwd()
			if err != nil {
				return "", err
			}
		} else if !filepath.IsAbs(base) {
			base, err = filepath.Abs(base)
			if err != nil {
				return "", err
			}
		}
		expanded = filepath.Join(base, expanded)
	}

	return ResolveBestEffort(filepath.Clean(expanded))
}
