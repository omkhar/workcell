// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

//go:build darwin

package launcher

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	darwinProcInfoCallSignalAuditToken = 0x11
	darwinAuditTokenSignalMajor        = 23
	darwinAuditTokenSignalMinor        = 2
)

type darwinAuditTokenSignalHandle struct {
	token [8]uint32
	pid   int
}

func openExactProcessSignalHandle(pid int) (exactProcessSignalHandle, error) {
	identity, err := darwinProcessIdentity(pid)
	if err != nil {
		return nil, err
	}
	handle := &darwinAuditTokenSignalHandle{pid: pid}
	handle.token[5] = uint32(pid)
	handle.token[7] = identity.idVersion
	return handle, nil
}

func (h *darwinAuditTokenSignalHandle) Signal(signal syscall.Signal) error {
	_, _, errno := unix.Syscall6(
		unix.SYS_PROC_INFO,
		darwinProcInfoCallSignalAuditToken, 0, uintptr(signal), 0,
		uintptr(unsafe.Pointer(&h.token)), unsafe.Sizeof(h.token),
	)
	if errno != 0 {
		return fmt.Errorf("signal process %d through Darwin audit token: %w", h.pid, errno)
	}
	return nil
}

func (*darwinAuditTokenSignalHandle) Close() error { return nil }

func reapColimaProfileProcessesForHost(ctx context.Context, profile string, deps colimaProcessReaperDependencies) error {
	if !darwinAuditTokenSignalAvailable() {
		return passivelyReapColimaProfileProcesses(ctx, profile, deps)
	}
	return reapColimaProfileProcesses(ctx, profile, deps)
}

func darwinAuditTokenSignalAvailable() bool {
	release, err := unix.Sysctl("kern.osrelease")
	return err == nil && darwinReleaseSupportsAuditTokenSignal(release)
}

func darwinReleaseSupportsAuditTokenSignal(release string) bool {
	parts := strings.SplitN(release, ".", 3)
	if len(parts) < 2 {
		return false
	}
	major, majorErr := strconv.Atoi(parts[0])
	minor, minorErr := strconv.Atoi(parts[1])
	if majorErr != nil || minorErr != nil || major < 0 || minor < 0 {
		return false
	}
	return major > darwinAuditTokenSignalMajor || major == darwinAuditTokenSignalMajor && minor >= darwinAuditTokenSignalMinor
}
