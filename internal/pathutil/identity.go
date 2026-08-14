// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package pathutil

import (
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

// CollisionKey returns a Unicode-normalized, case-insensitive path identity.
func CollisionKey(path string) (string, error) {
	normalized, err := normalizePath(path, true)
	if err != nil {
		return "", err
	}
	return filepath.ToSlash(normalized), nil
}

// WithinOrEqual reports if candidate is root or is below root.
func WithinOrEqual(root, candidate string, caseInsensitive bool) (bool, error) {
	root, err := normalizePath(root, caseInsensitive)
	if err != nil {
		return false, err
	}
	candidate, err = normalizePath(candidate, caseInsensitive)
	if err != nil {
		return false, err
	}
	rel, err := filepath.Rel(root, candidate)
	if err != nil {
		return false, err
	}
	return rel == "." || (rel != ".." && !filepath.IsAbs(rel) && !strings.HasPrefix(rel, ".."+string(filepath.Separator))), nil
}

func normalizePath(path string, caseInsensitive bool) (string, error) {
	if path == "" {
		return "", ErrEmptyPath
	}
	if !utf8.ValidString(path) {
		return "", ErrInvalidUTF8Path
	}
	for _, runeValue := range path {
		if unicode.IsControl(runeValue) || unicode.Is(unicode.Zl, runeValue) || unicode.Is(unicode.Zp, runeValue) {
			return "", ErrUnsafePathControl
		}
	}
	normalized := norm.NFC.String(filepath.Clean(path))
	if caseInsensitive {
		normalized = norm.NFC.String(cases.Fold().String(normalized))
	}
	return normalized, nil
}
