// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"github.com/omkhar/workcell/internal/pathutil"
)

// CanonicalizePath is a thin wrapper around pathutil.CanonicalizePath
// using best-effort semantics, preserving the metadatautil contract:
// the empty input is rejected explicitly here (the legacy helper did
// this before delegating to pathutil); ~user lookups that fail return
// the raw input unchanged.  New code should call
// pathutil.CanonicalizePath directly.
func CanonicalizePath(raw string) (string, error) {
	if raw == "" {
		return "", pathutil.ErrEmptyPath
	}
	return pathutil.CanonicalizePath(raw, pathutil.Options{})
}
