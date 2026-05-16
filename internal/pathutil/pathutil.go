// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package pathutil

import (
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"strings"
)

// ErrEmptyPath is the sentinel returned by
// ExpandUserPathStrictRequireNonEmpty for empty inputs (and reused by
// the package's other empty-input gates, including
// CanonicalizePath via canonicalize.go). Callers can match it via
// errors.Is to detect the missing-input case and stamp a
// domain-specific message.
var ErrEmptyPath = errors.New("pathutil: path is empty")

// ExpandUserPathBestEffort expands `~`, `~/...`, and `~user`/`~user/...`
// references via os.UserHomeDir or user.Lookup.  When a `~user` lookup
// fails (unknown user or empty home), it returns the input verbatim
// rather than an error.  Use this when callers can tolerate an
// unexpanded fallback (e.g. logging or display paths).
func ExpandUserPathBestEffort(raw string) (string, error) {
	return expandUserPath(raw, false)
}

// ExpandUserPathStrict behaves like ExpandUserPathBestEffort but
// surfaces user.Lookup failures (or empty home directories) as errors
// instead of returning the input verbatim.  Use this when an
// unexpanded path would be a correctness bug.
func ExpandUserPathStrict(raw string) (string, error) {
	return expandUserPath(raw, true)
}

// ExpandUserPathStrictRequireNonEmpty rejects an empty raw input with
// ErrEmptyPath, then delegates to ExpandUserPathStrict.  The two
// near-identical private wrappers in internal/authpolicy and
// internal/injection used to inline this check; consolidating here
// keeps the empty-input contract in a single place.
//
// The function name is intentionally verbose — Strict and BestEffort
// describe the error-handling axis, RequireNonEmpty is the empty-input
// axis. If a 5th expansion variant arrives (e.g., a Strict + NonEmpty +
// no-`~user`-lookup combination) the package should be reshaped into a
// pathutil.ExpandUserPath(raw, ExpandOpts{...}) options-style API so
// the variant count stays manageable.
func ExpandUserPathStrictRequireNonEmpty(raw string) (string, error) {
	if raw == "" {
		return "", ErrEmptyPath
	}
	return ExpandUserPathStrict(raw)
}

// ExpandUserPathHomeOnly expands `~` and `~/...` to the current user's
// home directory and returns any other input verbatim — it never
// consults user.Lookup.  This is the launcher-side variant: pointer
// files read under WORKCELL_STATE_ROOT may legitimately reference paths
// like `~/Library/...` but a `~someotheruser` reference would force a
// host-level lookup, which the launcher deliberately avoids so a
// hostile (or just non-existent) pointer cannot poke at the OS user
// database.  Errors from os.UserHomeDir are surfaced as the
// unmodified raw input rather than a returned error so the call site
// can keep its best-effort fallback shape.
func ExpandUserPathHomeOnly(raw string) string {
	if raw == "" {
		return ""
	}
	if raw == "~" || strings.HasPrefix(raw, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return raw
		}
		if raw == "~" {
			return home
		}
		return filepath.Join(home, raw[2:])
	}
	return raw
}

// CanonicalizeExpandedPath returns an absolute, symlink-resolved form
// of the input.  Relative inputs are first made absolute via filepath.Abs
// against the process CWD; the result is then passed through
// ResolveBestEffort so missing trailing components do not cause failure.
func CanonicalizeExpandedPath(path string) (string, error) {
	if !filepath.IsAbs(path) {
		abs, err := filepath.Abs(path)
		if err != nil {
			return "", err
		}
		path = abs
	}
	return ResolveBestEffort(filepath.Clean(path))
}

// ResolveBestEffort walks up the path until it finds an existing
// ancestor, evaluates its symlinks, then re-joins the missing suffix.
// This matches the "canonicalize what exists, accept the rest verbatim"
// shape that scripts/workcell uses when staging not-yet-created bundle
// directories.
func ResolveBestEffort(path string) (string, error) {
	if path == string(filepath.Separator) {
		return path, nil
	}

	existing := path
	suffix := make([]string, 0)
	for {
		if _, err := os.Lstat(existing); err == nil {
			break
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return path, nil
		}
		suffix = append([]string{filepath.Base(existing)}, suffix...)
		existing = parent
	}

	resolvedExisting, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return "", err
	}
	if len(suffix) == 0 {
		return filepath.Clean(resolvedExisting), nil
	}
	parts := append([]string{resolvedExisting}, suffix...)
	return filepath.Clean(filepath.Join(parts...)), nil
}

func expandUserPath(raw string, strict bool) (string, error) {
	switch {
	case raw == "~":
		return os.UserHomeDir()
	case strings.HasPrefix(raw, "~/"):
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, raw[2:]), nil
	case !strings.HasPrefix(raw, "~"):
		return raw, nil
	}

	slash := strings.IndexByte(raw, '/')
	userName := raw[1:]
	remainder := ""
	if slash >= 0 {
		userName = raw[1:slash]
		remainder = raw[slash+1:]
	}
	if userName == "" {
		return os.UserHomeDir()
	}

	lookup, err := user.Lookup(userName)
	if err != nil || lookup.HomeDir == "" {
		if strict {
			if err != nil {
				return "", err
			}
			return "", os.ErrNotExist
		}
		return raw, nil
	}
	if remainder == "" {
		return lookup.HomeDir, nil
	}
	return filepath.Join(lookup.HomeDir, remainder), nil
}
