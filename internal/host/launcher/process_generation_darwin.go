// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

//go:build darwin

package launcher

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

const darwinProcPIDUniqueIdentifierInfo = 17

type darwinUniqueIdentifierInfo struct {
	uuid                    [16]byte
	uniqueID                uint64
	parentUniqueID          uint64
	idVersion               uint32
	originalParentIDVersion uint32
	reserved                [2]uint64
}

// processGeneration combines Darwin's persistent process start time with its
// boot-scoped unique identifier. The identifier stays stable across exec.
func processGeneration(pid int) (string, error) {
	before, err := darwinProcessIdentity(pid)
	if err != nil {
		return "", err
	}
	started, err := legacyDarwinProcessGeneration(pid)
	if err != nil {
		return "", err
	}
	after, err := darwinProcessIdentity(pid)
	if err != nil {
		return "", err
	}
	return formatDarwinProcessGeneration(pid, started, before.uniqueID, after.uniqueID)
}

func formatDarwinProcessGeneration(pid int, started string, before, after uint64) (string, error) {
	if before != after {
		return "", fmt.Errorf("read process %d Darwin identity: process generation changed", pid)
	}
	return started + ":" + strconv.FormatUint(after, 10), nil
}

func observeValidDarwinProcessGeneration(pid int, recorded string) (string, error) {
	if !strings.Contains(strings.TrimPrefix(recorded, "darwin:"), ":") {
		return legacyDarwinProcessGeneration(pid)
	}
	return processGeneration(pid)
}

func legacyDarwinProcessGeneration(pid int) (string, error) {
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

func darwinProcessIdentity(pid int) (darwinUniqueIdentifierInfo, error) {
	var info darwinUniqueIdentifierInfo
	size := unsafe.Sizeof(info)
	result, _, errno := unix.Syscall6(
		unix.SYS_PROC_INFO,
		2, uintptr(pid), darwinProcPIDUniqueIdentifierInfo, 0,
		uintptr(unsafe.Pointer(&info)), size,
	)
	if errors.Is(errno, syscall.ESRCH) {
		return info, processGoneErr{pid: pid}
	}
	if errno != 0 {
		return info, fmt.Errorf("read process %d Darwin identity: %w", pid, errno)
	}
	if result != size {
		return info, fmt.Errorf("read process %d Darwin identity: got %d bytes, want %d", pid, result, size)
	}
	return info, nil
}
