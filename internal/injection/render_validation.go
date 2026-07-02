// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package injection

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/omkhar/workcell/internal/host/authstate"
	"github.com/omkhar/workcell/internal/pathutil"
)

func validateAllowedKeys(table map[string]any, allowed map[string]struct{}, label string) error {
	unknown := make([]string, 0)
	for key := range table {
		if _, ok := allowed[key]; !ok {
			unknown = append(unknown, key)
		}
	}
	slices.Sort(unknown)
	if len(unknown) > 0 {
		return fmt.Errorf("%s contains unsupported keys: %s", label, strings.Join(unknown, ", "))
	}
	return nil
}

func selectedFor(values any, current, label string, allowed map[string]struct{}) (bool, error) {
	if values == nil {
		return true, nil
	}
	items, err := stringSlice(values, label)
	if err != nil {
		return false, err
	}
	if len(items) == 0 {
		return false, fmt.Errorf("%s must be a non-empty array when specified", label)
	}
	for _, s := range items {
		if _, ok := allowed[s]; !ok {
			return false, fmt.Errorf("%s contains unsupported value: %s", label, s)
		}
		if s == current {
			return true, nil
		}
	}
	return false, nil
}

func stringSlice(values any, label string) ([]string, error) {
	switch typed := values.(type) {
	case nil:
		return nil, nil
	case []string:
		return append([]string(nil), typed...), nil
	case []any:
		items := make([]string, 0, len(typed))
		for _, item := range typed {
			value, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("%s values must be strings", label)
			}
			items = append(items, value)
		}
		return items, nil
	default:
		return nil, fmt.Errorf("%s must be a non-empty array when specified", label)
	}
}

func anySlice(values any, label string) ([]any, error) {
	switch typed := values.(type) {
	case nil:
		return nil, nil
	case []any:
		return append([]any(nil), typed...), nil
	case []string:
		items := make([]any, 0, len(typed))
		for _, item := range typed {
			items = append(items, item)
		}
		return items, nil
	default:
		return nil, fmt.Errorf("%s must be an array of strings when specified", label)
	}
}

func ensureNoSymlinksWithin(root Path) error {
	return filepath.WalkDir(root.String(), func(current string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if current == root.String() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("directory injections must not contain symlinks: %s", current)
		}
		return nil
	})
}

func directMountEntry(source Path, mountPath string) map[string]string {
	return map[string]string{
		"source":     source.String(),
		"mount_path": mountPath,
	}
}

func validateSourcePath(raw any, label string, base Path) (Path, error) {
	rawStr, ok := raw.(string)
	if !ok || rawStr == "" {
		return Path(""), fmt.Errorf("%s must be a non-empty string path", label)
	}
	source, err := expandHostPath(rawStr, base)
	if err != nil {
		return Path(""), err
	}
	info, err := os.Stat(source.String())
	if err != nil {
		return Path(""), fmt.Errorf("%s does not exist: %s", label, source)
	}
	offender, err := findUnsafeSymlinkInPathChain(source.String())
	if err != nil {
		return Path(""), err
	}
	if offender != "" {
		return Path(""), fmt.Errorf("%s must not be a symlink: %s", label, offender)
	}
	if err := authstate.RejectCredentialSource(source.String(), label); err != nil {
		return Path(""), err
	}
	if info.IsDir() {
		if err := authstate.RejectCredentialDirectorySource(source.String(), label); err != nil {
			return Path(""), err
		}
	}
	return source, nil
}

func expandHostPath(raw string, base Path) (Path, error) {
	expanded, err := pathutil.ExpandUserPathStrictRequireNonEmpty(raw)
	if err != nil {
		return Path(""), err
	}
	if !filepath.IsAbs(expanded) {
		expanded = filepath.Join(base.String(), expanded)
	}
	abs, err := filepath.Abs(expanded)
	if err != nil {
		return Path(""), err
	}
	return Path(abs), nil
}

func requirePathWithin(root, candidate Path, label string) error {
	resolvedRoot, err := filepath.EvalSymlinks(root.String())
	if err != nil {
		return err
	}
	resolvedCandidate, err := filepath.EvalSymlinks(candidate.String())
	if err != nil {
		return err
	}
	if resolvedCandidate != resolvedRoot && !strings.HasPrefix(resolvedCandidate, resolvedRoot+string(filepath.Separator)) {
		return fmt.Errorf("%s must stay within %s: %s", label, resolvedRoot, resolvedCandidate)
	}
	return nil
}

func requireNoSymlink(path Path, label string) error {
	if info, err := os.Lstat(path.String()); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s must not be a symlink: %s", label, path)
	}
	return nil
}

func requireSecretOwnerOnly(path Path, label string) error {
	info, err := os.Lstat(path.String())
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return errors.New("unsupported file stat type")
	}
	if int(stat.Uid) != os.Getuid() {
		return fmt.Errorf("%s must be owned by uid %d: %s", label, os.Getuid(), path)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%s must not be group/world-accessible: %s", label, path)
	}
	return nil
}

func validateSecretFile(source Path, label string) (Path, error) {
	if err := requireNoSymlink(source, label); err != nil {
		return Path(""), err
	}
	info, err := os.Stat(source.String())
	if err != nil {
		return Path(""), err
	}
	if !info.Mode().IsRegular() {
		return Path(""), fmt.Errorf("%s must point at a file: %s", label, source)
	}
	if err := requireSecretOwnerOnly(source, label); err != nil {
		return Path(""), err
	}
	return source, nil
}

func validateSecretTree(source Path, label string) error {
	if err := requireNoSymlink(source, label); err != nil {
		return err
	}
	info, err := os.Stat(source.String())
	if err != nil {
		return err
	}
	if info.Mode().IsRegular() {
		_, err = validateSecretFile(source, label)
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s must point at a file or directory: %s", label, source)
	}
	if err := requireSecretOwnerOnly(source, label); err != nil {
		return err
	}
	if err := ensureNoSymlinksWithin(source); err != nil {
		return err
	}
	return filepath.WalkDir(source.String(), func(current string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if current == source.String() {
			return nil
		}
		child := Path(current)
		if err := requireNoSymlink(child, label); err != nil {
			return err
		}
		return requireSecretOwnerOnly(child, label)
	})
}

func ensureIsFile(source Path, label string) error {
	info, err := os.Stat(source.String())
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s must point at a file: %s", label, source)
	}
	return nil
}
