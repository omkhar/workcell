//go:build unix

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package secretfile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"golang.org/x/sys/unix"
)

// Open securely opens a secret file without following symlinks.
// The caller must close the returned file.
func Open(path, label string, uid int) (*os.File, error) {
	cleanPath := canonicalizeSystemPath(filepath.Clean(path))
	if !filepath.IsAbs(cleanPath) {
		return nil, fmt.Errorf("%s must point at a file: %s", label, path)
	}

	if info, err := os.Lstat(cleanPath); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%s must not be a symlink: %s", label, path)
	}

	parentFD, err := openParent(cleanPath)
	if err != nil {
		return nil, wrapOpenError(label, path, err)
	}
	defer unix.Close(parentFD)

	fd, err := unix.Openat(parentFD, filepath.Base(cleanPath), unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, wrapOpenError(label, path, err)
	}

	file := os.NewFile(uintptr(fd), cleanPath)
	if file == nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("%s must point at a file: %s", label, path)
	}

	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		_ = file.Close()
		return nil, wrapOpenError(label, path, err)
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG {
		_ = file.Close()
		return nil, fmt.Errorf("%s must point at a file: %s", label, path)
	}
	if int(stat.Uid) != uid {
		_ = file.Close()
		return nil, fmt.Errorf("%s must be owned by uid %d: %s", label, uid, path)
	}
	if stat.Mode&0o077 != 0 {
		_ = file.Close()
		return nil, fmt.Errorf("%s must not be group/world-accessible: %s", label, path)
	}
	return file, nil
}

func openParent(path string) (int, error) {
	fd, err := unix.Open(string(filepath.Separator), unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY, 0)
	if err != nil {
		return -1, err
	}
	parent := filepath.Dir(path)
	if parent == string(filepath.Separator) {
		return fd, nil
	}

	components := strings.Split(strings.TrimPrefix(parent, string(filepath.Separator)), string(filepath.Separator))
	for _, component := range components {
		nextFD, err := unix.Openat(fd, component, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
		_ = unix.Close(fd)
		if err != nil {
			return -1, err
		}
		fd = nextFD
	}
	return fd, nil
}

func wrapOpenError(label, path string, err error) error {
	switch {
	case errors.Is(err, unix.ELOOP):
		return fmt.Errorf("%s must not be a symlink: %s", label, path)
	case errors.Is(err, unix.ENOTDIR), errors.Is(err, unix.ENOENT):
		return fmt.Errorf("%s must point at a file: %s", label, path)
	default:
		return &os.PathError{Op: "open", Path: path, Err: err}
	}
}

func canonicalizeSystemPath(path string) string {
	if runtime.GOOS != "darwin" {
		return path
	}
	if path == "/var" || strings.HasPrefix(path, "/var/") {
		return filepath.Join("/private", strings.TrimPrefix(path, string(filepath.Separator)))
	}
	if path == "/tmp" || strings.HasPrefix(path, "/tmp/") {
		return filepath.Join("/private", strings.TrimPrefix(path, string(filepath.Separator)))
	}
	return path
}
