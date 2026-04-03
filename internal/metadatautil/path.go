package metadatautil

import (
	"errors"
	"path/filepath"
	"strings"

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

func canonicalizeToShellPath(raw string) string {
	canonical, err := CanonicalizePath(raw)
	if err != nil {
		return raw
	}
	return strings.TrimSuffix(canonical, string(filepath.Separator))
}
