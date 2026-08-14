// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

//go:build darwin

package injectionpolicy

import "golang.org/x/sys/unix"

func policyFileTimes(stat unix.Stat_t) (int64, int64) {
	return stat.Mtim.Sec*1_000_000_000 + stat.Mtim.Nsec, stat.Ctim.Sec*1_000_000_000 + stat.Ctim.Nsec
}
