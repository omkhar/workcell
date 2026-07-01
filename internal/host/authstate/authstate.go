// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package authstate

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

var forbiddenCredentialSourceRoots = []string{
	".codex",
	".claude",
	".claude.json",
	".copilot",
	".gemini",
	".config/claude-code",
	".config/gh",
	".config/gcloud",
	".config/git",
	".config/github-copilot",
	".config/op",
	".cache/github-copilot",
	".ssh",
	".aws",
	".docker",
	".kube",
	".gnupg",
	".git-credentials",
	".netrc",
	"Library/Keychains",
}

func RejectCredentialSource(source string, label string) error {
	if root, ok := ForbiddenCredentialSourceRoot(source); ok {
		return fmt.Errorf("%s must not point inside host provider/auth state: %s", label, root)
	}
	return nil
}

func ForbiddenCredentialSourceRoot(source string) (string, bool) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", false
	}
	return forbiddenCredentialSourceRoot(source, home, hostPathComparisonCaseInsensitive())
}

func forbiddenCredentialSourceRoot(source, home string, caseInsensitive bool) (string, bool) {
	var err error

	home, err = filepath.Abs(home)
	if err != nil {
		return "", false
	}
	source, err = filepath.Abs(source)
	if err != nil {
		return "", false
	}
	source = filepath.Clean(source)

	for _, rel := range forbiddenCredentialSourceRoots {
		root := filepath.Clean(filepath.Join(home, filepath.FromSlash(rel)))
		if pathWithinRoot(root, source, caseInsensitive) {
			return root, true
		}
	}
	return "", false
}

func hostPathComparisonCaseInsensitive() bool {
	switch runtime.GOOS {
	case "darwin", "windows":
		return true
	default:
		return false
	}
}

func pathWithinRoot(root, candidate string, caseInsensitive bool) bool {
	root = filepath.Clean(root)
	candidate = filepath.Clean(candidate)
	if caseInsensitive {
		root = strings.ToLower(root)
		candidate = strings.ToLower(candidate)
	}
	if candidate == root {
		return true
	}
	rel, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !filepath.IsAbs(rel) && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
