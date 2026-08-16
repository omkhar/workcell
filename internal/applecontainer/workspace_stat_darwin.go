// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

//go:build darwin

package applecontainer

import (
	"unsafe"

	"golang.org/x/sys/unix"
)

const workspaceCopySupported = true
const workspaceSymlinkOpenFlags = unix.O_SYMLINK | unix.O_CLOEXEC
const workspaceFreadlinkTrap uintptr = 551 // SYS_freadlink, available since macOS 13.

func workspaceReadlink(fd int, name string, buffer []byte) (int, error) {
	if name != "" {
		return unix.Readlinkat(fd, name, buffer)
	}
	if len(buffer) == 0 {
		return 0, nil
	}
	count, _, errno := unix.Syscall(workspaceFreadlinkTrap, uintptr(fd), uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)))
	if errno != 0 {
		return 0, errno
	}
	return int(count), nil
}

func workspaceSnapshotFromUnix(stat unix.Stat_t) workspaceSnapshot {
	return workspaceSnapshot{uint32(stat.Mode), stat.Size, uint64(stat.Nlink), stat.Uid, stat.Gid, uint64(stat.Dev), stat.Ino, stat.Mtim.Sec, int64(stat.Mtim.Nsec), stat.Ctim.Sec, int64(stat.Ctim.Nsec)}
}
