// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

//go:build darwin || linux

package release

import "golang.org/x/sys/unix"

func assetSourceStatFromUnix(stat unix.Stat_t) assetSourceStat {
	return assetSourceStat{
		Mode:           uint32(stat.Mode),
		Size:           stat.Size,
		Nlink:          uint64(stat.Nlink),
		UID:            stat.Uid,
		GID:            stat.Gid,
		Dev:            uint64(stat.Dev),
		Ino:            stat.Ino,
		ModTimeSec:     stat.Mtim.Sec,
		ModTimeNsec:    stat.Mtim.Nsec,
		ChangeTimeSec:  stat.Ctim.Sec,
		ChangeTimeNsec: stat.Ctim.Nsec,
	}
}
