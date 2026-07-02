// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package authresolve

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	"github.com/omkhar/workcell/internal/host/authstate"
	"github.com/omkhar/workcell/internal/secretfile"
)

func validateSourcePath(raw any, label, base string) (string, error) {
	str, ok := raw.(string)
	if !ok || str == "" {
		return "", fmt.Errorf("%s must be a non-empty string path", label)
	}
	source := expandHostPath(str, base)
	if _, err := os.Stat(source); err != nil {
		return "", fmt.Errorf("%s does not exist: %s", label, source)
	}
	if err := requireNoSymlinkInPathChain(source, label); err != nil {
		return "", err
	}
	if err := authstate.RejectCredentialSource(source, label); err != nil {
		return "", err
	}
	return source, nil
}

func requirePathWithin(root, candidate, label string) error {
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		resolvedRoot, err = filepath.Abs(root)
		if err != nil {
			return err
		}
	}
	resolvedCandidate, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		resolvedCandidate, err = filepath.Abs(candidate)
		if err != nil {
			return err
		}
	}
	rel, err := filepath.Rel(resolvedRoot, resolvedCandidate)
	if err != nil {
		return err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%s must stay within %s: %s", label, resolvedRoot, resolvedCandidate)
	}
	return nil
}

func requireNoSymlinkInPathChain(path, label string) error {
	current := path
	for {
		fi, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			if _, allowed := systemSymlinkAllowlist[current]; !allowed {
				return fmt.Errorf("%s must not be a symlink: %s", label, current)
			}
		}
		parent := filepath.Dir(current)
		if parent == current {
			return nil
		}
		current = parent
	}
}

func requireSecretFile(source, label string) (string, error) {
	handle, err := secretfile.Open(source, label, os.Getuid())
	if err != nil {
		return "", err
	}
	defer handle.Close()
	return source, nil
}

func expandHostPath(raw, base string) string {
	expanded := raw
	if strings.HasPrefix(raw, "~") {
		if home, err := expandUser(raw); err == nil {
			expanded = home
		}
	}
	if !filepath.IsAbs(expanded) {
		expanded = filepath.Join(base, expanded)
	}
	abs, err := filepath.Abs(expanded)
	if err != nil {
		return filepath.Clean(expanded)
	}
	return abs
}

func expandUser(raw string) (string, error) {
	if raw == "~" || strings.HasPrefix(raw, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if raw == "~" {
			return home, nil
		}
		return filepath.Join(home, raw[2:]), nil
	}
	if !strings.HasPrefix(raw, "~") {
		return raw, nil
	}
	slash := strings.IndexByte(raw, '/')
	userName := raw[1:]
	remainder := ""
	if slash >= 0 {
		userName = raw[1:slash]
		remainder = raw[slash+1:]
	}
	var home string
	if userName == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		home = h
	} else {
		usr, err := user.Lookup(userName)
		if err != nil {
			return "", err
		}
		home = usr.HomeDir
	}
	if remainder == "" {
		return home, nil
	}
	return filepath.Join(home, remainder), nil
}

func resolvePath(raw string) (string, error) {
	expanded := raw
	if strings.HasPrefix(raw, "~") {
		userExpanded, err := expandUser(raw)
		if err == nil {
			expanded = userExpanded
		}
	}
	abs, err := filepath.Abs(expanded)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved, nil
	}
	parent := filepath.Dir(abs)
	resolvedParent, err := filepath.EvalSymlinks(parent)
	if err == nil {
		return filepath.Join(resolvedParent, filepath.Base(abs)), nil
	}
	return abs, nil
}

func cwd() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}
