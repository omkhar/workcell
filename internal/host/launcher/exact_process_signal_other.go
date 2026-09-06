// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

//go:build !darwin && !linux

package launcher

import (
	"context"
	"fmt"
)

func openExactProcessSignalHandle(pid int) (exactProcessSignalHandle, error) {
	return nil, fmt.Errorf("host does not provide an exact signal handle for process %d", pid)
}

func reapColimaProfileProcessesForHost(ctx context.Context, profile string, deps colimaProcessReaperDependencies) error {
	return passivelyReapColimaProfileProcesses(ctx, profile, deps)
}
