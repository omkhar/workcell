// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package rootio

import (
	"bytes"
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

// ReadFileAtNoFollow reads one regular leaf from an already trusted parent.
func ReadFileAtNoFollow(parent *os.File, name, label string, limit int64) ([]byte, error) {
	if err := validateLeafName(name); err != nil {
		return nil, err
	}
	if limit < 0 {
		return nil, fmt.Errorf("%s has an invalid byte limit", label)
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
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s must be a regular file: %s", label, name)
	}
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
// It writes a unique sibling with O_EXCL, sets mode before publication, then
// uses renameat. renameat replaces a swapped leaf symlink instead of following it.
func WriteFileAtomicAtNoFollow(parent *os.File, name string, data []byte, mode os.FileMode, tempPrefix string) error {
	if err := validateLeafName(name); err != nil {
		return err
	}
	if tempPrefix == "" {
		tempPrefix = ".workcell-tmp-"
	}
	if strings.Contains(tempPrefix, string(filepath.Separator)) {
		return fmt.Errorf("temporary-file prefix must not contain a path separator: %s", tempPrefix)
	}
	parentFD := int(parent.Fd())
	for attempt := 0; attempt < 32; attempt++ {
		suffix, err := randomSuffix()
		if err != nil {
			return err
		}
		temporaryName := tempPrefix + suffix + ".tmp"
		fd, err := unix.Openat(parentFD, temporaryName, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, uint32(mode.Perm()))
		if errors.Is(err, unix.EEXIST) {
			continue
		}
		if err != nil {
			return err
		}
		file := os.NewFile(uintptr(fd), filepath.Join(parent.Name(), temporaryName))
		if file == nil {
			_ = unix.Close(fd)
			_ = unix.Unlinkat(parentFD, temporaryName, 0)
			return fmt.Errorf("create temporary file for %s", name)
		}
		if _, err := io.Copy(file, bytes.NewReader(data)); err != nil {
			_ = file.Close()
			_ = unix.Unlinkat(parentFD, temporaryName, 0)
			return err
		}
		if err := unix.Fchmod(fd, uint32(mode.Perm())); err != nil {
			_ = file.Close()
			_ = unix.Unlinkat(parentFD, temporaryName, 0)
			return err
		}
		if err := file.Close(); err != nil {
			_ = unix.Unlinkat(parentFD, temporaryName, 0)
			return err
		}
		if err := unix.Renameat(parentFD, temporaryName, parentFD, name); err != nil {
			_ = unix.Unlinkat(parentFD, temporaryName, 0)
			return err
		}
		return nil
	}
	return fmt.Errorf("unable to allocate temporary file under %s", parent.Name())
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
