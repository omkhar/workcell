package pathutil

import (
	"os"
	"os/user"
	"path/filepath"
	"strings"
)

func ExpandUserPathBestEffort(raw string) (string, error) {
	return expandUserPath(raw, false)
}

func ExpandUserPathStrict(raw string) (string, error) {
	return expandUserPath(raw, true)
}

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
