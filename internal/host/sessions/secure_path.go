// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package sessions

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

type descriptorFS struct {
	openat   func(int, string, int, uint32) (int, error)
	fstatat  func(int, string, *unix.Stat_t, int) error
	mkdirat  func(int, string, uint32) error
	linkat   func(int, string, int, string, int) error
	unlinkat func(int, string, int) error
	fchmod   func(int, uint32) error
	fstat    func(int, *unix.Stat_t) error
	fsync    func(int) error
	flock    func(int, int) error
	read     func(int, []byte) (int, error)
	close    func(int) error
}

func defaultDescriptorFS() descriptorFS {
	return descriptorFS{
		openat: unix.Openat, fstatat: unix.Fstatat, mkdirat: unix.Mkdirat, linkat: unix.Linkat,
		unlinkat: unix.Unlinkat, fchmod: unix.Fchmod, fstat: unix.Fstat,
		fsync: unix.Fsync, flock: unix.Flock, read: unix.Read, close: unix.Close,
	}
}

// ownedDescriptor records exactly one close obligation. close consumes that
// obligation before invoking the seam, so even a reported close failure is
// never retried against a descriptor number the kernel may already have reused.
type ownedDescriptor struct {
	fs descriptorFS
	fd int
}

func ownDescriptor(fs descriptorFS, fd int) *ownedDescriptor {
	return &ownedDescriptor{fs: fs, fd: fd}
}

func (descriptor *ownedDescriptor) close() error {
	if descriptor == nil || descriptor.fd < 0 {
		return nil
	}
	fd := descriptor.fd
	descriptor.fd = -1
	return descriptor.fs.close(fd)
}

func (descriptor *ownedDescriptor) release() int {
	fd := descriptor.fd
	descriptor.fd = -1
	return fd
}

func closeOwnedDescriptors(cause error, descriptors ...*ownedDescriptor) error {
	result := cause
	for _, descriptor := range descriptors {
		result = errors.Join(result, descriptor.close())
	}
	return result
}

type directoryPolicy func(string, unix.Stat_t) error

// absoluteDirectoryComponents deliberately rejects non-canonical raw input
// instead of normalizing it. The accepted spelling has one leading separator,
// no trailing or duplicate separators, no dot components, and no component
// whose leading or trailing whitespace would be changed by trimming.
func absoluteDirectoryComponents(path string) ([]string, error) {
	separator := string(filepath.Separator)
	if path == "" || path != strings.TrimSpace(path) || !filepath.IsAbs(path) || path == separator || strings.ContainsRune(path, 0) {
		return nil, errors.New("secure directory walk requires a canonical absolute non-root path")
	}
	components := strings.Split(strings.TrimPrefix(path, separator), separator)
	for _, component := range components {
		if component == "" || component == "." || component == ".." || component != strings.TrimSpace(component) {
			return nil, errors.New("secure directory walk contains an unsafe raw component")
		}
	}
	return components, nil
}

func ownedByCurrentUser(stat unix.Stat_t) bool {
	return ownedByUser(stat, uint32(os.Geteuid()))
}

func ownedByUser(stat unix.Stat_t, euid uint32) bool {
	return stat.Uid == euid
}

func ownerControlsDirectory(stat unix.Stat_t) bool {
	return ownerControlsDirectoryForUser(stat, uint32(os.Geteuid()))
}

func ownerControlsDirectoryForUser(stat unix.Stat_t, euid uint32) bool {
	return stat.Mode&unix.S_IFMT == unix.S_IFDIR && stat.Ino != 0 && ownedByUser(stat, euid) && stat.Mode&0o022 == 0
}

// validateWalkDirectory permits only root-owned non-writable system ancestors
// (plus root-owned sticky transit directories such as /tmp) until it reaches a
// directory controlled by the current user. Below that anchor, every component
// must remain controlled by the current user. This constrains replacement races
// to the same-UID actor that already controls the selected state tree.
func validateWalkDirectory(path string, stat unix.Stat_t, anchored bool) (bool, error) {
	return validateWalkDirectoryForUser(path, stat, anchored, uint32(os.Geteuid()))
}

func validateWalkDirectoryForUser(path string, stat unix.Stat_t, anchored bool, euid uint32) (bool, error) {
	if stat.Mode&unix.S_IFMT != unix.S_IFDIR || stat.Ino == 0 {
		return false, fmt.Errorf("%s: path contains a non-directory component", path)
	}
	ownedAndControlled := ownerControlsDirectoryForUser(stat, euid)
	if anchored {
		if !ownedAndControlled {
			return false, fmt.Errorf("%s: directory below the owner-controlled anchor is unsafe", path)
		}
		return true, nil
	}
	if stat.Uid == 0 && (stat.Mode&0o022 == 0 || stat.Mode&unix.S_ISVTX != 0) {
		return euid == 0 && stat.Mode&0o077 == 0 && stat.Mode&unix.S_ISVTX == 0, nil
	}
	if ownedAndControlled {
		return true, nil
	}
	return false, fmt.Errorf("%s: directory ancestor is not trusted", path)
}

// openAbsoluteDirectory walks an already-canonical absolute path from a
// descriptor for /. Each component is opened with O_DIRECTORY|O_NOFOLLOW. The
// returned descriptor belongs to the caller. The walk prevents symlink
// redirection and rejects writable or foreign-owned ancestors according to
// validateWalkDirectory; it does not defend against a same-UID actor that can
// mutate the anchored tree concurrently.
func openAbsoluteDirectory(path string, create bool, fs descriptorFS, policy directoryPolicy) (int, error) {
	components, err := absoluteDirectoryComponents(path)
	if err != nil {
		return -1, err
	}
	rootFD, err := fs.openat(unix.AT_FDCWD, string(filepath.Separator), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		if rootFD >= 0 {
			err = closeOwnedDescriptors(err, ownDescriptor(fs, rootFD))
		}
		return -1, err
	}
	if rootFD < 0 {
		return -1, errors.New("root open returned an invalid descriptor")
	}
	current := ownDescriptor(fs, rootFD)
	var rootStat unix.Stat_t
	if err := fs.fstat(current.fd, &rootStat); err != nil {
		return -1, closeOwnedDescriptors(err, current)
	}
	anchored, err := validateWalkDirectory(string(filepath.Separator), rootStat, false)
	if err != nil {
		return -1, closeOwnedDescriptors(err, current)
	}

	for index, component := range components {
		nextFD, openErr := fs.openat(current.fd, component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if openErr != nil && nextFD >= 0 {
			return -1, closeOwnedDescriptors(openErr, ownDescriptor(fs, nextFD), current)
		}
		created := false
		if errors.Is(openErr, unix.ENOENT) && create {
			mkdirErr := fs.mkdirat(current.fd, component, 0o700)
			if mkdirErr != nil && !errors.Is(mkdirErr, unix.EEXIST) {
				return -1, closeOwnedDescriptors(mkdirErr, current)
			}
			created = mkdirErr == nil
			nextFD, openErr = fs.openat(current.fd, component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
			if openErr != nil && nextFD >= 0 {
				return -1, closeOwnedDescriptors(openErr, ownDescriptor(fs, nextFD), current)
			}
		}
		if openErr != nil {
			if errors.Is(openErr, unix.ELOOP) || errors.Is(openErr, unix.ENOTDIR) {
				openErr = fmt.Errorf("%s: directory path contains a symlink or non-directory component: %w", path, openErr)
			}
			return -1, closeOwnedDescriptors(openErr, current)
		}
		if nextFD < 0 {
			return -1, closeOwnedDescriptors(errors.New("component open returned an invalid descriptor"), current)
		}
		next := ownDescriptor(fs, nextFD)
		if created {
			if err := fs.fchmod(next.fd, 0o700); err != nil {
				return -1, closeOwnedDescriptors(err, next, current)
			}
		}
		var stat unix.Stat_t
		if err := fs.fstat(next.fd, &stat); err != nil {
			return -1, closeOwnedDescriptors(err, next, current)
		}
		nextAnchored, err := validateWalkDirectory(path, stat, anchored)
		if err != nil {
			return -1, closeOwnedDescriptors(err, next, current)
		}
		final := index == len(components)-1
		if final && policy != nil {
			if err := policy(path, stat); err != nil {
				return -1, closeOwnedDescriptors(err, next, current)
			}
		}
		// A retry after an ambiguous mkdir/fsync result must resync the
		// containing directory even when this invocation found the child.
		if create && (created || ownerControlsDirectory(stat)) {
			if err := fs.fsync(current.fd); err != nil {
				return -1, closeOwnedDescriptors(err, next, current)
			}
		}
		if err := current.close(); err != nil {
			return -1, closeOwnedDescriptors(err, next, current)
		}
		current = next
		anchored = nextAnchored
	}
	return current.release(), nil
}

func requireOwnerOnlyDirectory(path string, stat unix.Stat_t) error {
	if !ownerControlsDirectory(stat) || stat.Mode&0o077 != 0 {
		return fmt.Errorf("%s: directory must be owned by the current user and owner-only", path)
	}
	return nil
}

func requireOwnerControlledDirectory(path string, stat unix.Stat_t) error {
	if !ownerControlsDirectory(stat) {
		return fmt.Errorf("%s: directory must be owner-controlled", path)
	}
	return nil
}
