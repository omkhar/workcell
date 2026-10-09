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
	fd, err = openDirectoryChain(fd, components)
	if err != nil {
		return nil, "", err
	}
	parentFile := os.NewFile(uintptr(fd), parent)
	if parentFile == nil {
		_ = unix.Close(fd)
		return nil, "", fmt.Errorf("open parent directory: %s", parent)
	}
	return parentFile, cleaned, nil
}

// OpenDirectoryAtNoFollow opens the directory at relative under parent. Each
// component is opened relative to the previous descriptor with O_NOFOLLOW, so
// a symlink anywhere on the path is refused even when its target stays under
// parent. The caller closes the directory.
func OpenDirectoryAtNoFollow(parent *os.File, relative string) (*os.File, error) {
	components, err := relativeComponents(relative)
	if err != nil {
		return nil, err
	}
	// F_DUPFD_CLOEXEC rather than dup, for the reason MkdirAllSyncedAt gives.
	fd, err := unix.FcntlInt(parent.Fd(), unix.F_DUPFD_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	fd, err = openDirectoryChain(fd, components)
	if err != nil {
		return nil, err
	}
	name := filepath.Join(parent.Name(), filepath.Join(components...))
	file := os.NewFile(uintptr(fd), name)
	if file == nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("open directory: %s", name)
	}
	return file, nil
}

// openDirectoryChain opens each component below fd as a directory without
// following a symlink. It always consumes fd: it returns the last descriptor,
// or closes every descriptor it holds and returns the error.
func openDirectoryChain(fd int, components []string) (int, error) {
	for _, component := range components {
		nextFD, err := unix.Openat(fd, component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		_ = unix.Close(fd)
		if err != nil {
			return -1, err
		}
		fd = nextFD
	}
	return fd, nil
}

// RemoveAllAtNoFollow removes name under parent and every entry below it,
// like os.RemoveAll. Each directory opens relative to its parent's descriptor
// with O_NOFOLLOW, so a symlink is unlinked as a leaf and its target is kept.
// A missing name is not an error.
//
// Each entry is renamed to a random staged name first, and the walk and the
// unlink use that name, so a directory swapped in at the original name is
// kept. Before each unlink, the entry at the staged name must match the
// directory descriptor that was traversed, or the regular file descriptor
// that was opened, by device, inode and mode; a mismatch is an error, and
// both entries stay.
//
// Residual risk, accepted: the check and the unlink address the entry by name
// under the parent descriptor, and POSIX has no unlink by inode, so a process
// that can write that directory could swap the entry between the two calls.
// That process is the same user or root, which already controls the tree; the
// check exists to keep this call from removing an entry it did not traverse,
// not to defend against a hostile same-uid process, which is outside the
// threat model.
func RemoveAllAtNoFollow(parent *os.File, name string) error {
	if err := validateLeafName(name); err != nil {
		return err
	}
	return removeAllAt(int(parent.Fd()), name)
}

func removeAllAt(parentFD int, name string) error {
	suffix, err := randomSuffix()
	if err != nil {
		return err
	}
	staged := ".workcell-rm-" + suffix
	// renameNoReplaceAt refuses a staged name that already exists.
	if err := renameNoReplaceAt(parentFD, name, staged); errors.Is(err, unix.ENOENT) {
		return nil
	} else if err != nil {
		return err
	}
	fd, err := unix.Openat(parentFD, staged, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if errors.Is(err, unix.ENOTDIR) || errors.Is(err, unix.ELOOP) {
		// A file or a symlink: remove the entry itself, never its target.
		return unlinkLeafAt(parentFD, staged)
	}
	if err != nil {
		return err
	}
	dir := os.NewFile(uintptr(fd), staged)
	names, err := dir.Readdirnames(-1)
	for _, child := range names {
		if err == nil {
			err = removeAllAt(fd, child)
		}
	}
	var traversed unix.Stat_t
	if err == nil {
		err = unix.Fstat(fd, &traversed)
	}
	if closeErr := dir.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	removeAllHook()
	if err := requireStagedEntry(parentFD, staged, &traversed); err != nil {
		return err
	}
	return unix.Unlinkat(parentFD, staged, unix.AT_REMOVEDIR)
}

// unlinkLeafAt removes one staged non-directory. A regular file is opened
// without following a symlink and bound by its descriptor; other entries, and
// a regular file this user cannot open, are bound by the Fstatat taken here.
func unlinkLeafAt(parentFD int, staged string) error {
	var held unix.Stat_t
	if err := unix.Fstatat(parentFD, staged, &held, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return err
	}
	if held.Mode&unix.S_IFMT == unix.S_IFREG {
		fd, err := unix.Openat(parentFD, staged, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
		if err == nil {
			err = unix.Fstat(fd, &held)
			_ = unix.Close(fd)
		} else if errors.Is(err, unix.EACCES) {
			err = nil
		}
		if err != nil {
			return err
		}
	}
	if err := requireStagedEntry(parentFD, staged, &held); err != nil {
		return err
	}
	return unix.Unlinkat(parentFD, staged, 0)
}

// requireStagedEntry fails when the entry at staged is not the held inode.
func requireStagedEntry(parentFD int, staged string, held *unix.Stat_t) error {
	var current unix.Stat_t
	if err := unix.Fstatat(parentFD, staged, &current, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return err
	}
	if current.Dev != held.Dev || current.Ino != held.Ino || current.Mode != held.Mode {
		return fmt.Errorf("refusing to remove %s: the staged entry was swapped after it was traversed", staged)
	}
	return nil
}

// removeAllHook lets a test rename entries before a directory is removed.
var removeAllHook = func() {}

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
