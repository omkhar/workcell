// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package sessions

import (
	"errors"
	"fmt"
	"io"
	"math"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

type ownerFile struct {
	data []byte
	stat unix.Stat_t
}

func safeDescriptorBasename(name string) error {
	if name == "" || name == "." || name == ".." || filepath.IsAbs(name) || strings.ContainsRune(name, filepath.Separator) || strings.ContainsRune(name, 0) || name != strings.TrimSpace(name) {
		return errors.New("descriptor-relative file name must be one safe basename")
	}
	return nil
}

type descriptorReader struct {
	fs descriptorFS
	fd int
}

func (reader descriptorReader) Read(buffer []byte) (int, error) {
	for {
		count, err := reader.fs.read(reader.fd, buffer)
		if count > 0 {
			return count, nil
		}
		if err == nil {
			return 0, io.EOF
		}
		if !errors.Is(err, unix.EINTR) {
			return count, err
		}
	}
}

// readOwnerFileAt verifies and closes one owner-only regular file beneath
// dirFD. The caller must obtain dirFD from a trusted descriptor walk. A caller
// that correlates this snapshot with another file or later mutates state based
// on it must also hold the enclosing state lock through that decision. Because
// the file descriptor is not retained, same-UID mutations that preserve the
// checked identity and metadata remain outside this helper's guarantee.
func readOwnerFileAt(dirFD int, name, path, subject string, limit int64, writable bool, allowedLinks ...uint64) (ownerFile, error) {
	return readOwnerFileAtFS(defaultDescriptorFS(), dirFD, name, path, subject, limit, writable, allowedLinks...)
}

func readOwnerFileAtFS(fs descriptorFS, dirFD int, name, path, subject string, limit int64, writable bool, allowedLinks ...uint64) (result ownerFile, err error) {
	if err := safeDescriptorBasename(name); err != nil {
		return ownerFile{}, err
	}
	if limit <= 0 || limit > math.MaxInt64-1 {
		return ownerFile{}, errors.New("owner-file read limit must be positive and permit a one-byte overflow probe")
	}
	var expected unix.Stat_t
	if err := fs.fstatat(dirFD, name, &expected, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return ownerFile{}, err
	}
	if expected.Mode&unix.S_IFMT != unix.S_IFREG || expected.Ino == 0 {
		return ownerFile{}, fmt.Errorf("%s: %s is not a regular file", path, subject)
	}
	flags := unix.O_RDONLY
	if writable {
		flags = unix.O_RDWR
	}
	fd, err := fs.openat(dirFD, name, flags|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		if fd >= 0 {
			err = closeOwnedDescriptors(err, ownDescriptor(fs, fd))
		}
		if errors.Is(err, unix.ELOOP) {
			return ownerFile{}, fmt.Errorf("%s: %s is not a regular file: %w", path, subject, err)
		}
		return ownerFile{}, err
	}
	if fd < 0 {
		return ownerFile{}, errors.New("owner-file open returned an invalid descriptor")
	}
	owned := ownDescriptor(fs, fd)
	defer func() {
		err = errors.Join(err, owned.close())
		if err != nil {
			result = ownerFile{}
		}
	}()

	var before unix.Stat_t
	if err := fs.fstat(fd, &before); err != nil {
		return ownerFile{}, err
	}
	if !sameFileIdentity(expected, before) || before.Mode&unix.S_IFMT != unix.S_IFREG {
		return ownerFile{}, fmt.Errorf("%s: %s changed between inspection and open", path, subject)
	}
	if err := validateOwnerFileStat(path, subject, before, limit, allowedLinks...); err != nil {
		return ownerFile{}, err
	}
	data, err := io.ReadAll(io.LimitReader(descriptorReader{fs: fs, fd: fd}, limit+1))
	if err != nil {
		return ownerFile{}, err
	}
	var after unix.Stat_t
	if err := fs.fstat(fd, &after); err != nil {
		return ownerFile{}, err
	}
	if err := validateOwnerFileStat(path, subject, after, limit, allowedLinks...); err != nil {
		return ownerFile{}, err
	}
	if int64(len(data)) > limit {
		return ownerFile{}, fmt.Errorf("%s: %s exceeds %d bytes", path, subject, limit)
	}
	if !sameFileIdentity(before, after) || before.Uid != after.Uid || before.Gid != after.Gid || before.Mode != after.Mode || before.Nlink != after.Nlink || before.Size != after.Size || int64(len(data)) != after.Size {
		return ownerFile{}, fmt.Errorf("%s: %s changed while it was read", path, subject)
	}
	return ownerFile{data: data, stat: after}, nil
}

func validateOwnerFileStat(path, subject string, stat unix.Stat_t, limit int64, allowedLinks ...uint64) error {
	if stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Ino == 0 {
		return fmt.Errorf("%s: %s is not a regular file", path, subject)
	}
	if !ownedByCurrentUser(stat) || stat.Mode&0o077 != 0 {
		return fmt.Errorf("%s: %s must be owned by the current user and owner-only", path, subject)
	}
	linkAllowed := false
	for _, count := range allowedLinks {
		if count > 0 && uint64(stat.Nlink) == count {
			linkAllowed = true
		}
	}
	if !linkAllowed {
		return fmt.Errorf("%s: %s link count is unsafe", path, subject)
	}
	if stat.Size < 0 || stat.Size > limit {
		return fmt.Errorf("%s: %s exceeds %d bytes", path, subject, limit)
	}
	return nil
}

func sameFileIdentity(left, right unix.Stat_t) bool {
	return left.Dev == right.Dev && left.Ino == right.Ino
}
