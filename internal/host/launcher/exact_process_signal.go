// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package launcher

import "syscall"

type exactProcessSignalHandle interface {
	Signal(syscall.Signal) error
	Close() error
}
