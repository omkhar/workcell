// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

//go:build !darwin && !linux

package applecontainer

import "golang.org/x/sys/unix"

const workspaceCopySupported = false

func workspaceReadlink(_ int, _ string, _ []byte) (int, error) { return 0, unix.ENOTSUP }

func workspaceSnapshotFromUnix(stat unix.Stat_t) workspaceSnapshot {
	return workspaceSnapshot{uint32(stat.Mode), stat.Size, uint64(stat.Nlink), stat.Uid, stat.Gid, uint64(stat.Dev), stat.Ino, stat.Mtim.Sec, int64(stat.Mtim.Nsec), stat.Ctim.Sec, int64(stat.Ctim.Nsec)}
}
