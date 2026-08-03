// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

//go:build darwin

package launcher

import (
	"errors"
	"fmt"
	"syscall"

	"golang.org/x/sys/unix"
)

// processGeneration returns the kernel-recorded process start time at
// microsecond resolution. Unlike ps lstart, it does not collapse every process
// started in the same second to the same identity.
func processGeneration(pid int) (string, error) {
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		if killErr := syscall.Kill(pid, 0); errors.Is(killErr, syscall.ESRCH) {
			return "", processGoneErr{pid: pid}
		}
		return "", fmt.Errorf("read process %d kernel identity: %w", pid, err)
	}
	if int(info.Proc.P_pid) != pid {
		return "", fmt.Errorf("read process %d kernel identity: returned pid %d", pid, info.Proc.P_pid)
	}
	started := info.Proc.P_starttime
	if started.Sec <= 0 || started.Usec < 0 || started.Usec >= 1_000_000 {
		return "", fmt.Errorf("read process %d kernel identity: invalid start time", pid)
	}
	return fmt.Sprintf("darwin:%d.%06d", started.Sec, started.Usec), nil
}
