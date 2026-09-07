// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

//go:build linux

package launcher

import (
	"context"
	"errors"
	"fmt"
	"syscall"

	"golang.org/x/sys/unix"
)

type pidfdSignalHandle struct {
	fd  int
	pid int
}

func openExactProcessSignalHandle(pid int) (exactProcessSignalHandle, error) {
	fd, err := unix.PidfdOpen(pid, 0)
	if errors.Is(err, syscall.ESRCH) {
		return nil, processGoneErr{pid: pid}
	}
	if err != nil {
		return nil, fmt.Errorf("open process %d pidfd: %w", pid, err)
	}
	return &pidfdSignalHandle{fd: fd, pid: pid}, nil
}

func (h *pidfdSignalHandle) Signal(signal syscall.Signal) error {
	if err := unix.PidfdSendSignal(h.fd, unix.Signal(signal), nil, 0); err != nil {
		return fmt.Errorf("signal process %d through pidfd: %w", h.pid, err)
	}
	return nil
}

func (h *pidfdSignalHandle) Close() error {
	if err := unix.Close(h.fd); err != nil {
		return fmt.Errorf("close process %d pidfd: %w", h.pid, err)
	}
	return nil
}

func reapColimaProfileProcessesForHost(ctx context.Context, profile string, deps colimaProcessReaperDependencies) error {
	return reapColimaProfileProcesses(ctx, profile, deps)
}
