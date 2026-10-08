// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package rootio

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"golang.org/x/sys/unix"
)

const (
	// MaxManifestBytes is the maximum accepted size for one runtime or injection
	// manifest. Callers use it for metadata JSON that describes the same bundle.
	MaxManifestBytes int64 = 16 * 1024 * 1024
	// MaxDirectMountSpecBytes is the maximum accepted size for one direct-mount
	// specification.
	MaxDirectMountSpecBytes int64 = 1 * 1024 * 1024
)

// OpenParentDirectoryNoFollow uses descriptors for the parent and rejects parent symlinks.
func OpenParentDirectoryNoFollow(path string) (*os.File, string, error) {
	cleaned, err := filepath.Abs(path)
	if err != nil {
		return nil, "", err
	}
	cleaned = canonicalizeSystemPath(filepath.Clean(cleaned))
	parent := filepath.Dir(cleaned)
	components := []string{}
	if parent != string(filepath.Separator) {
		components = strings.Split(strings.TrimPrefix(parent, string(filepath.Separator)), string(filepath.Separator))
	}
	fd, err := unix.Open(string(filepath.Separator), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, "", err
	}
	for _, component := range components {
		nextFD, openErr := unix.Openat(fd, component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		_ = unix.Close(fd)
		if openErr != nil {
			return nil, "", openErr
		}
		fd = nextFD
	}
	parentFile := os.NewFile(uintptr(fd), parent)
	if parentFile == nil {
		_ = unix.Close(fd)
		return nil, "", fmt.Errorf("open parent directory: %s", parent)
	}
	return parentFile, cleaned, nil
}

// ReadFileNoFollow reads one regular file through a descriptor-relative,
// no-follow traversal. It accepts files up to limit bytes.
func ReadFileNoFollow(path, label string, limit int64) ([]byte, error) {
	parent, cleaned, err := OpenParentDirectoryNoFollow(path)
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	return ReadFileAtNoFollow(parent, filepath.Base(cleaned), label, limit)
}

// OpenRegularFileAtNoFollow opens one regular leaf from an already trusted
// parent without following a symlink. The caller closes the file.
func OpenRegularFileAtNoFollow(parent *os.File, name, label string) (*os.File, error) {
	if err := validateLeafName(name); err != nil {
		return nil, err
	}
	fd, err := unix.Openat(int(parent.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), filepath.Join(parent.Name(), name))
	if file == nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("open %s: %s", label, name)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, fmt.Errorf("%s must be a regular file: %s", label, name)
	}
	return file, nil
}

// ReadFileAtNoFollow reads one regular leaf from an already trusted parent.
func ReadFileAtNoFollow(parent *os.File, name, label string, limit int64) ([]byte, error) {
	if limit < 0 {
		return nil, fmt.Errorf("%s has an invalid byte limit", label)
	}
	file, err := OpenRegularFileAtNoFollow(parent, name, label)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) == limit && limit < math.MaxInt64 {
		var extra [1]byte
		n, readErr := file.Read(extra[:])
		if readErr != nil && readErr != io.EOF {
			return nil, readErr
		}
		if n != 0 {
			return nil, fmt.Errorf("%s exceeds the byte limit of %d: %s", label, limit, name)
		}
	}
	return data, nil
}

// SameFileAtNoFollow compares existing leaf inodes without following symlinks.
func SameFileAtNoFollow(firstParent *os.File, firstName string, secondParent *os.File, secondName string) (bool, error) {
	if err := validateLeafName(firstName); err != nil {
		return false, err
	}
	if err := validateLeafName(secondName); err != nil {
		return false, err
	}
	var first, second unix.Stat_t
	if err := unix.Fstatat(int(firstParent.Fd()), firstName, &first, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return false, err
	}
	if err := unix.Fstatat(int(secondParent.Fd()), secondName, &second, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		if errors.Is(err, unix.ENOENT) {
			return false, nil
		}
		return false, err
	}
	return first.Dev == second.Dev && first.Ino == second.Ino, nil
}

// LstatAtNoFollow returns the status of relative under parent without
// following a symlink in any component. A symlink before the leaf fails with
// ELOOP, and a symlink leaf reports itself.
func LstatAtNoFollow(parent *os.File, relative string) (unix.Stat_t, error) {
	var info unix.Stat_t
	components, err := relativeComponents(relative)
	if err != nil {
		return info, err
	}
	current, err := openDirsNoFollow(parent, components[:len(components)-1])
	if err != nil {
		return info, err
	}
	defer func() { _ = unix.Close(current) }()
	return info, unix.Fstatat(current, components[len(components)-1], &info, unix.AT_SYMLINK_NOFOLLOW)
}

// OpenDirAtNoFollow opens the directory at relative under parent without
// following a symlink in any component. The caller closes it.
func OpenDirAtNoFollow(parent *os.File, relative string) (*os.File, error) {
	components, err := relativeComponents(relative)
	if err != nil {
		return nil, err
	}
	current, err := openDirsNoFollow(parent, components)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(current), filepath.Join(parent.Name(), relative)), nil
}

// openDirsNoFollow opens each directory of components from the descriptor of
// the one before it, so a component swapped mid-walk fails with ELOOP.
func openDirsNoFollow(parent *os.File, components []string) (int, error) {
	current, err := unix.FcntlInt(parent.Fd(), unix.F_DUPFD_CLOEXEC, 0)
	if err != nil {
		return -1, err
	}
	for _, component := range components {
		// Linux reports ENOTDIR, not ELOOP, for a symlink opened with
		// O_DIRECTORY|O_NOFOLLOW, so stat it first to name the cause.
		var info unix.Stat_t
		err := unix.Fstatat(current, component, &info, unix.AT_SYMLINK_NOFOLLOW)
		if err == nil && info.Mode&unix.S_IFMT == unix.S_IFLNK {
			err = fmt.Errorf("%s: %w", component, unix.ELOOP)
		}
		next := -1
		if err == nil {
			next, err = unix.Openat(current, component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		}
		_ = unix.Close(current)
		if err != nil {
			return -1, err
		}
		current = next
	}
	return current, nil
}

// MarshalCompactJSON returns compact newline-terminated JSON when it fits limit.
func MarshalCompactJSON(value any, label string, limit int64) ([]byte, error) {
	if limit < 0 {
		return nil, fmt.Errorf("%s has an invalid byte limit", label)
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) >= limit {
		return nil, fmt.Errorf("%s exceeds the byte limit of %d", label, limit)
	}
	return append(data, '\n'), nil
}

// WriteFileAtomicAtNoFollow replaces name from an already trusted parent.
//
// It is StageAndPublishAt under its original name: one staged sibling created
// with O_EXCL under a random name, its mode set through the descriptor, its
// contents synced, published with renameat, and the parent directory synced.
func WriteFileAtomicAtNoFollow(parent *os.File, name string, data []byte, mode os.FileMode, tempPrefix string) error {
	return StageAndPublishAt(parent, name, data, mode, tempPrefix)
}

func validateLeafName(name string) error {
	if name == "" || name == "." || name == ".." || name == string(filepath.Separator) || name != filepath.Base(name) {
		return fmt.Errorf("path must name one file within the opened parent: %s", name)
	}
	return nil
}

func canonicalizeSystemPath(path string) string {
	if runtime.GOOS != "darwin" {
		return path
	}
	for _, prefix := range []string{"/var", "/etc", "/tmp"} {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return filepath.Join("/private", strings.TrimPrefix(path, string(filepath.Separator)))
		}
	}
	return path
}
