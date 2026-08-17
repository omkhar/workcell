// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package launcher

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
)

const (
	processGroupTerminationGrace = 5 * time.Second
	processGroupProofLimit       = 5 * time.Second
	processGroupWaitDelay        = 5 * time.Second
	processGroupPollInterval     = 25 * time.Millisecond
)

type ownedProcessGroupID int

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
	killProcess func(*os.Process) error
	termGrace   time.Duration
	proofLimit  time.Duration
	waitDelay   time.Duration
}

func defaultProcessGroupSupervisor() processGroupSupervisor {
	return processGroupSupervisor{
		signalGroup: signalProcessGroup,
		waitAbsent:  waitForProcessGroupExit,
		killProcess: killExactProcess,
		termGrace:   processGroupTerminationGrace,
		proofLimit:  processGroupProofLimit,
		waitDelay:   processGroupWaitDelay,
	}
}

func (s processGroupSupervisor) run(ctx context.Context, cmd *exec.Cmd) processGroupRunResult {
	if err := validateProcessGroupCommand(cmd); err != nil {
		return processGroupRunResult{runErr: err, cause: context.Cause(ctx)}
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	if s.waitDelay <= 0 {
		s.waitDelay = processGroupWaitDelay
	}
	cmd.WaitDelay = s.waitDelay
	cleanupResult := make(chan error, 1)
	cmd.Cancel = func() error {
		groupID, absent, err := ownedProcessGroupForProcess(cmd.Process)
		if err != nil {
			err = errors.Join(err, s.killLeader(cmd.Process))
		} else if !absent {
			err = s.terminate(groupID, cmd.Process)
		}
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

func validateProcessGroupCommand(cmd *exec.Cmd) error {
	if cmd == nil {
		return errors.New("process-group command is required")
	}
	if cmd.SysProcAttr != nil && (cmd.SysProcAttr.Pgid != 0 || cmd.SysProcAttr.Foreground) {
		return errors.New("process-group command must not select a pre-existing or foreground process group")
	}
	return nil
}

func ownedProcessGroupForProcess(process *os.Process) (ownedProcessGroupID, bool, error) {
	if process == nil {
		return 0, false, errors.New("process-group leader is required")
	}
	groupID := process.Pid
	if err := validateSafeProcessGroupID(groupID); err != nil {
		return 0, false, err
	}
	actualGroupID, err := syscall.Getpgid(process.Pid)
	if errors.Is(err, syscall.ESRCH) {
		absent, probeErr := waitForProcessGroupExit(groupID, 0)
		return ownedProcessGroupID(groupID), absent, probeErr
	}
	if err != nil {
		return 0, false, fmt.Errorf("resolve process group for leader %d: %w", process.Pid, err)
	}
	if actualGroupID != groupID {
		return 0, false, fmt.Errorf("process %d belongs to group %d, not its dedicated group", process.Pid, actualGroupID)
	}
	return ownedProcessGroupID(groupID), false, nil
}

func killExactProcess(process *os.Process) error {
	if process == nil {
		return nil
	}
	if err := process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("kill process-group leader %d: %w", process.Pid, err)
	}
	return nil
}

func (s processGroupSupervisor) terminate(groupID ownedProcessGroupID, process *os.Process) error {
	var cleanupErrors []error
	if err := s.signalGroup(int(groupID), syscall.SIGTERM); err != nil {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("signal process group %d with SIGTERM: %w", groupID, err))
	}
	absent, err := s.waitAbsent(int(groupID), s.termGrace)
	if err != nil {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("poll process group %d after SIGTERM: %w", groupID, err))
	}
	if absent && err == nil {
		return errors.Join(cleanupErrors...)
	}
	if err := s.signalGroup(int(groupID), syscall.SIGKILL); err != nil {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("signal process group %d with SIGKILL: %w", groupID, err))
		if fallbackErr := s.killLeader(process); fallbackErr != nil {
			cleanupErrors = append(cleanupErrors, fallbackErr)
		}
	}
	absent, err = s.waitAbsent(int(groupID), s.proofLimit)
	if err != nil {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("poll process group %d after SIGKILL: %w", groupID, err))
	}
	if !absent || err != nil {
		if err == nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("process group %d remains after SIGKILL", groupID))
		}
		if fallbackErr := s.killLeader(process); fallbackErr != nil {
			cleanupErrors = append(cleanupErrors, fallbackErr)
		}
	}
	return errors.Join(cleanupErrors...)
}

func (s processGroupSupervisor) killLeader(process *os.Process) error {
	if s.killProcess != nil {
		return s.killProcess(process)
	}
	return killExactProcess(process)
}

func signalProcessGroup(groupID int, signal syscall.Signal) error {
	return signalProcessGroupWithKill(groupID, signal, syscall.Kill)
}

func signalProcessGroupWithKill(groupID int, signal syscall.Signal, kill func(int, syscall.Signal) error) error {
	if err := validateSafeProcessGroupID(groupID); err != nil {
		return err
	}
	if err := kill(-groupID, signal); err != nil && !errors.Is(err, syscall.ESRCH) {
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
	if err := validateSafeProcessGroupID(groupID); err != nil {
		return false, err
	}
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

func validateSafeProcessGroupID(groupID int) error {
	if groupID <= 1 {
		return fmt.Errorf("process group ID %d is not safe", groupID)
	}
	if groupID == syscall.Getpgrp() {
		return fmt.Errorf("process group ID %d belongs to the caller", groupID)
	}
	return nil
}
