// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package launcher

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"syscall"
	"time"
)

const (
	processGroupTerminationGrace = 5 * time.Second
	processGroupProofLimit       = 5 * time.Second
	processGroupPollInterval     = 25 * time.Millisecond
)

type processGroupRunResult struct {
	runErr           error
	cleanupErr       error
	cause            error
	cleanupRan       bool
	preStartCanceled bool
}

type processGroupSupervisor struct {
	signalGroup func(int, syscall.Signal) error
	waitAbsent  func(int, time.Duration) (bool, error)
	termGrace   time.Duration
	proofLimit  time.Duration
}

func defaultProcessGroupSupervisor() processGroupSupervisor {
	return processGroupSupervisor{
		signalGroup: signalProcessGroup,
		waitAbsent:  waitForProcessGroupExit,
		termGrace:   processGroupTerminationGrace,
		proofLimit:  processGroupProofLimit,
	}
}

func (s processGroupSupervisor) run(ctx context.Context, cmd *exec.Cmd) processGroupRunResult {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	cleanupResult := make(chan error, 1)
	cmd.Cancel = func() error {
		err := s.terminate(cmd)
		cleanupResult <- err
		return err
	}

	runErr := cmd.Run()
	result := processGroupRunResult{
		runErr:           runErr,
		cause:            context.Cause(ctx),
		preStartCanceled: cmd.Process == nil && ctx.Err() != nil && errors.Is(runErr, ctx.Err()),
	}
	select {
	case cleanupErr := <-cleanupResult:
		result.cleanupErr = cleanupErr
		result.cleanupRan = true
	default:
	}
	return result
}

func (s processGroupSupervisor) terminate(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	groupID := cmd.Process.Pid
	if err := s.signalGroup(groupID, syscall.SIGTERM); err != nil {
		return fmt.Errorf("signal process group %d with SIGTERM: %w", groupID, err)
	}
	absent, err := s.waitAbsent(groupID, s.termGrace)
	if err != nil {
		return fmt.Errorf("poll process group %d after SIGTERM: %w", groupID, err)
	}
	if absent {
		return nil
	}
	if err := s.signalGroup(groupID, syscall.SIGKILL); err != nil {
		return fmt.Errorf("signal process group %d with SIGKILL: %w", groupID, err)
	}
	absent, err = s.waitAbsent(groupID, s.proofLimit)
	if err != nil {
		return fmt.Errorf("poll process group %d after SIGKILL: %w", groupID, err)
	}
	if !absent {
		return fmt.Errorf("process group %d remains after SIGKILL", groupID)
	}
	return nil
}

func signalProcessGroup(groupID int, signal syscall.Signal) error {
	if err := syscall.Kill(-groupID, signal); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}

func waitForProcessGroupExit(groupID int, limit time.Duration) (bool, error) {
	return waitForProcessGroupExitWithProbe(groupID, limit, syscall.Kill, time.Now, time.Sleep)
}

func waitForProcessGroupExitWithProbe(
	groupID int,
	limit time.Duration,
	probe func(int, syscall.Signal) error,
	now func() time.Time,
	sleep func(time.Duration),
) (bool, error) {
	deadline := now().Add(limit)
	for {
		err := probe(-groupID, 0)
		if errors.Is(err, syscall.ESRCH) {
			return true, nil
		}
		if err != nil && !errors.Is(err, syscall.EPERM) {
			return false, err
		}
		if !now().Before(deadline) {
			return false, nil
		}
		sleep(processGroupPollInterval)
	}
}
