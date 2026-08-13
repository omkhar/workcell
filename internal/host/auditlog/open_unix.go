//go:build unix

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package auditlog

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// openRegularSingleLink opens an audit log without following its leaf path.
// The nonblocking flag prevents a swapped FIFO from stalling the host command.
func openRegularSingleLink(path string) (*os.File, error) {
	parentFD, leaf, err := openAuditLogParent(path)
	if err != nil {
		return nil, err
	}
	defer unix.Close(parentFD)
	fd, err := unix.Openat(parentFD, leaf, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		if errors.Is(err, unix.ELOOP) {
			return nil, fmt.Errorf("auditlog: %s must not be a symlink: %w", path, err)
		}
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("auditlog: %s could not be opened", path)
	}

	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		_ = file.Close()
		return nil, &os.PathError{Op: "stat", Path: path, Err: err}
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG {
		_ = file.Close()
		return nil, fmt.Errorf("auditlog: %s is not a regular file", path)
	}
	if stat.Nlink != 1 {
		_ = file.Close()
		return nil, fmt.Errorf("auditlog: %s has %d links; expected one", path, stat.Nlink)
	}
	if stat.Uid != uint32(os.Geteuid()) || stat.Mode&0o077 != 0 {
		_ = file.Close()
		return nil, fmt.Errorf("auditlog: %s must be owned by the current user and owner-only", path)
	}
	return file, nil
}

func openAuditLogParent(path string) (int, string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return -1, "", err
	}
	clean := canonicalSystemPath(filepath.Clean(abs))
	if clean == string(filepath.Separator) {
		return -1, "", fmt.Errorf("auditlog: %s is not a file", path)
	}
	parent := filepath.Dir(clean)
	leaf := filepath.Base(clean)
	fd, err := unix.Open(string(filepath.Separator), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, "", err
	}
	var rootStat unix.Stat_t
	if err := unix.Fstat(fd, &rootStat); err != nil {
		_ = unix.Close(fd)
		return -1, "", err
	}
	anchored, err := validateAuditDirectory(string(filepath.Separator), rootStat, false)
	if err != nil {
		_ = unix.Close(fd)
		return -1, "", err
	}
	currentPath := string(filepath.Separator)
	for _, component := range strings.Split(strings.TrimPrefix(parent, string(filepath.Separator)), string(filepath.Separator)) {
		if component == "" {
			continue
		}
		next, openErr := unix.Openat(fd, component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if openErr != nil {
			_ = unix.Close(fd)
			return -1, "", openErr
		}
		var stat unix.Stat_t
		if statErr := unix.Fstat(next, &stat); statErr != nil {
			_ = unix.Close(next)
			_ = unix.Close(fd)
			return -1, "", statErr
		}
		currentPath = filepath.Join(currentPath, component)
		nextAnchored, validateErr := validateAuditDirectory(currentPath, stat, anchored)
		if validateErr != nil {
			_ = unix.Close(next)
			_ = unix.Close(fd)
			return -1, "", validateErr
		}
		_ = unix.Close(fd)
		fd = next
		anchored = nextAnchored
	}
	return fd, leaf, nil
}

// validateAuditDirectory uses the host state-path policy. Before the walk
// reaches a current-user directory, only root-owned non-writable ancestors and
// sticky transit directories are trusted. Each directory below that anchor must
// be controlled by the current user.
func validateAuditDirectory(path string, stat unix.Stat_t, anchored bool) (bool, error) {
	if stat.Mode&unix.S_IFMT != unix.S_IFDIR || stat.Ino == 0 {
		return false, fmt.Errorf("auditlog: %s is not a directory", path)
	}
	euid := uint32(os.Geteuid())
	ownerControlled := stat.Uid == euid && stat.Mode&0o022 == 0
	if anchored {
		if !ownerControlled {
			return false, fmt.Errorf("auditlog: %s is unsafe below the owner-controlled directory", path)
		}
		return true, nil
	}
	if stat.Uid == 0 && (stat.Mode&0o022 == 0 || stat.Mode&unix.S_ISVTX != 0) {
		return euid == 0 && stat.Mode&0o077 == 0 && stat.Mode&unix.S_ISVTX == 0, nil
	}
	if ownerControlled {
		return true, nil
	}
	return false, fmt.Errorf("auditlog: %s is not a trusted directory ancestor", path)
}

// canonicalSystemPath removes platform aliases such as macOS /var and /tmp.
// It does not resolve arbitrary descendants, so a mutable state-directory
// symlink remains visible to the no-follow descriptor walk below.
func canonicalSystemPath(path string) string {
	for _, alias := range []string{"/var", "/tmp"} {
		physical, err := filepath.EvalSymlinks(alias)
		if err != nil || physical == alias || (path != alias && !strings.HasPrefix(path, alias+string(filepath.Separator))) {
			continue
		}
		return physical + strings.TrimPrefix(path, alias)
	}
	return path
}
