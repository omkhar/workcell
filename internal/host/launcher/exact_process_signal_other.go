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

// Hosts other than Darwin and Linux have no exact handle, so the reaper keeps
// the generation-guarded bare-PID kill it used before exact handles existed.
// Every caller treats a reaper failure as fatal and runs before teardown, so a
// non-signalling path would take availability away from these hosts.
func reapColimaProfileProcessesForHost(ctx context.Context, profile string, deps colimaProcessReaperDependencies) error {
	deps.openSignal = openLegacyPIDSignalHandle
	return reapColimaProfileProcesses(ctx, profile, deps)
}
