// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package hostutil

import (
	"os"
	"path/filepath"

	"github.com/omkhar/workcell/internal/pathutil"
)

func CanonicalizePath(path string) (string, error) {
	return CanonicalizePathFrom(path, "")
}

func CanonicalizePathFrom(path, base string) (string, error) {
	expanded, err := expandUser(path)
	if err != nil {
		return "", err
	}

	if !filepath.IsAbs(expanded) {
		switch {
		case base == "":
			base, err = os.Getwd()
			if err != nil {
				return "", err
			}
		case !filepath.IsAbs(base):
			base, err = filepath.Abs(base)
			if err != nil {
				return "", err
			}
		}
		expanded = filepath.Join(base, expanded)
	}

	return pathutil.ResolveBestEffort(filepath.Clean(expanded))
}

func RealHome() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return CanonicalizePath(home)
}

func expandUser(path string) (string, error) {
	return pathutil.ExpandUserPathBestEffort(path)
}
