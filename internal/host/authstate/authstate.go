// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package authstate

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/omkhar/workcell/internal/pathutil"
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
	".mcp.json",
	".netrc",
	"Library/Keychains",
}

func RejectCredentialSource(source string, label string) error {
	if root, ok, err := ForbiddenCredentialSourceRoot(source); err != nil {
		return fmt.Errorf("cannot check host provider/auth state: %w", err)
	} else if ok {
		return fmt.Errorf("%s must not point inside host provider/auth state: %s", label, root)
	}
	return nil
}

func RejectCredentialDirectorySource(source string, label string) error {
	if root, ok, err := ForbiddenCredentialDirectorySourceRoot(source); err != nil {
		return fmt.Errorf("cannot check host provider/auth state: %w", err)
	} else if ok {
		return fmt.Errorf("%s must not include host provider/auth state: %s", label, root)
	}
	return nil
}

func ForbiddenCredentialSourceRoot(source string) (string, bool, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		if err != nil {
			return "", false, fmt.Errorf("get user home: %w", err)
		}
		return "", false, fmt.Errorf("get user home: empty path")
	}
	return forbiddenCredentialSourceRoot(source, home, hostPathComparisonCaseInsensitive())
}

func ForbiddenCredentialDirectorySourceRoot(source string) (string, bool, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		if err != nil {
			return "", false, fmt.Errorf("get user home: %w", err)
		}
		return "", false, fmt.Errorf("get user home: empty path")
	}
	return forbiddenCredentialDirectorySourceRoot(source, home, hostPathComparisonCaseInsensitive())
}

func forbiddenCredentialSourceRoot(source, home string, caseInsensitive bool) (string, bool, error) {
	var err error

	home, err = filepath.Abs(home)
	if err != nil {
		return "", false, fmt.Errorf("resolve user home: %w", err)
	}
	source, err = filepath.Abs(source)
	if err != nil {
		return "", false, fmt.Errorf("resolve source path: %w", err)
	}
	source = filepath.Clean(source)

	for _, rel := range forbiddenCredentialSourceRoots {
		root := filepath.Clean(filepath.Join(home, filepath.FromSlash(rel)))
		inside, err := pathutil.WithinOrEqual(root, source, caseInsensitive)
		if err != nil {
			return "", false, err
		}
		if inside {
			return root, true, nil
		}
	}
	return "", false, nil
}

func forbiddenCredentialDirectorySourceRoot(source, home string, caseInsensitive bool) (string, bool, error) {
	var err error

	home, err = filepath.Abs(home)
	if err != nil {
		return "", false, fmt.Errorf("resolve user home: %w", err)
	}
	source, err = filepath.Abs(source)
	if err != nil {
		return "", false, fmt.Errorf("resolve source path: %w", err)
	}
	source = filepath.Clean(source)

	for _, rel := range forbiddenCredentialSourceRoots {
		root := filepath.Clean(filepath.Join(home, filepath.FromSlash(rel)))
		inside, err := pathutil.WithinOrEqual(source, root, caseInsensitive)
		if err != nil {
			return "", false, err
		}
		if inside {
			return root, true, nil
		}
	}
	return "", false, nil
}

func hostPathComparisonCaseInsensitive() bool {
	switch runtime.GOOS {
	case "darwin", "windows":
		return true
	default:
		return false
	}
}
