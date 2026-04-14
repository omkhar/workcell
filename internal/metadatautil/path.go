// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"errors"

	"github.com/omkhar/workcell/internal/pathutil"
)

func expandUserPath(raw string) (string, error) {
	if raw == "" {
		return "", errors.New("empty path")
	}
	return pathutil.ExpandUserPathBestEffort(raw)
}

func CanonicalizePath(raw string) (string, error) {
	expanded, err := expandUserPath(raw)
	if err != nil {
		return "", err
	}
	return pathutil.CanonicalizeExpandedPath(expanded)
}
