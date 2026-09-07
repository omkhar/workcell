// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package launcher

import "syscall"

type exactProcessSignalHandle interface {
	Signal(syscall.Signal) error
	Close() error
}

// legacyPIDSignalHandle signals a bare PID, the status quo every host shipped
// before exact handles existed. It is not bound to the kernel object, so a PID
// recycled between the reaper's generation revalidation and the signal could
// receive it. Hosts without an exact handle keep this path rather than losing
// the ability to signal at all.
type legacyPIDSignalHandle struct{ pid int }

func openLegacyPIDSignalHandle(pid int) (exactProcessSignalHandle, error) {
	return legacyPIDSignalHandle{pid: pid}, nil
}

func (h legacyPIDSignalHandle) Signal(signal syscall.Signal) error {
	return syscall.Kill(h.pid, signal)
}

func (legacyPIDSignalHandle) Close() error { return nil }
