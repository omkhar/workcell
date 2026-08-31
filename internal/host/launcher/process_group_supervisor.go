// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package launcher

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

const (
	processGroupTerminationGrace = 5 * time.Second
	processGroupProofLimit       = 5 * time.Second
	processGroupPollInterval     = 25 * time.Millisecond
	processGroupWatchDrainLimit  = 5 * time.Second
)

type processGroupOwner struct {
	pid        int
	generation string
}

// Receiving from done means the watcher stopped and closed its descriptor.
type processExitWatcher interface {
	done() <-chan error
	close() error
}

type completedExitWatcher <-chan error

func (w completedExitWatcher) done() <-chan error { return w }
func (completedExitWatcher) close() error         { return nil }

func newCompletedExitWatcher(err error) processExitWatcher {
	result := make(chan error, 1)
	result <- err
	return completedExitWatcher(result)
}

type processGroupRunResult struct {
	runErr           error
	cleanupErr       error
	cause            error
	cleanupRan       bool
	preStartCanceled bool
}

type processGroupSupervisor struct {
	afterFunc         func(context.Context, func()) func() bool
	captureGeneration func(int) (string, error)
	observeGeneration func(int, string) (string, error)
	processGroupID    func(int) (int, error)
	watchExit         func(int) (processExitWatcher, error)
	signalGroup       func(int, syscall.Signal) error
	waitAbsent        func(int, time.Duration) (bool, error)
	killProcess       func(*os.Process) error
	termGrace         time.Duration
	proofLimit        time.Duration
}

func defaultProcessGroupSupervisor() processGroupSupervisor {
	return processGroupSupervisor{
		afterFunc:         context.AfterFunc,
		captureGeneration: processGeneration,
		observeGeneration: ObserveProcessGeneration,
		processGroupID:    syscall.Getpgid,
		watchExit:         startProcessExitWatch,
		signalGroup:       signalProcessGroup,
		waitAbsent:        waitForProcessGroupExit,
		killProcess:       killExactProcess,
		termGrace:         processGroupTerminationGrace,
		proofLimit:        processGroupProofLimit,
	}
}

func (s processGroupSupervisor) run(ctx context.Context, cmd *exec.Cmd) processGroupRunResult {
	if err := validateProcessGroupCommand(cmd); err != nil {
		return processGroupRunResult{runErr: err, cause: context.Cause(ctx)}
	}
	if err := validateProcessExitWatchSupport(); err != nil {
		return processGroupRunResult{runErr: err, cause: context.Cause(ctx)}
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	// Disable os/exec cancellation so it cannot reap or signal the child.
	cmd.Cancel = nil
	cmd.WaitDelay = 0

	runErr := cmd.Start()
	if runErr != nil {
		return processGroupRunResult{
			runErr:           runErr,
			cause:            context.Cause(ctx),
			preStartCanceled: cmd.Process == nil && ctx.Err() != nil && errors.Is(runErr, ctx.Err()),
		}
	}
	owner, err := s.captureOwner(cmd.Process)
	if err != nil {
		return s.abortStartedCommand(ctx, cmd, processGroupOwner{pid: cmd.Process.Pid}, fmt.Errorf("capture process-group owner: %w", err))
	}
	exitWatch, err := s.startExitWatch(owner.pid)
	if err != nil {
		return s.abortStartedCommand(ctx, cmd, owner, fmt.Errorf("start process-group leader exit watch: %w", err))
	}
	cancelRequest := make(chan error, 1)
	stopCancel := s.afterFunc(ctx, func() {
		cause := context.Cause(ctx)
		if cause == nil {
			cause = ctx.Err()
		}
		cancelRequest <- cause
	})
	// The cancellation callback is the arbitration point. If it has started,
	// cancellation wins; if Stop succeeds, the observed exit commits first.
	select {
	case watchErr := <-exitWatch.done():
		if stopCancel() {
			if watchErr != nil {
				return s.abortWatchedCommand(cmd, owner, exitWatch, watchErr)
			}
			closeErr := closeAndDrainExitWatch(exitWatch, true, watchErr, processGroupWatchDrainLimit)
			waitErr := waitStartedCommand(cmd, processGroupProofLimit)
			return processGroupRunResult{runErr: errors.Join(waitErr, closeErr)}
		}
		cause := <-cancelRequest
		return s.cancelStartedCommand(cmd, owner, exitWatch, true, watchErr, cause)
	case cause := <-cancelRequest:
		return s.cancelStartedCommand(cmd, owner, exitWatch, false, nil, cause)
	}
}

func (s processGroupSupervisor) startExitWatch(pid int) (processExitWatcher, error) {
	watch, err := s.watchExit(pid)
	if err == nil && watch == nil {
		err = errors.New("process-group exit watch is required")
	}
	return watch, err
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

func (s processGroupSupervisor) captureOwner(process *os.Process) (processGroupOwner, error) {
	if process == nil {
		return processGroupOwner{}, errors.New("process-group leader is required")
	}
	owner := processGroupOwner{pid: process.Pid}
	if err := validateSafeProcessGroupID(owner.pid); err != nil {
		return processGroupOwner{}, err
	}
	generation, err := s.captureGeneration(owner.pid)
	if err != nil {
		return processGroupOwner{}, fmt.Errorf("capture process-group leader %d generation: %w", owner.pid, err)
	}
	if generation == "" {
		return processGroupOwner{}, fmt.Errorf("capture process-group leader %d generation: empty generation", owner.pid)
	}
	if !strings.HasPrefix(generation, "darwin:") && !strings.HasPrefix(generation, "linux:") {
		return processGroupOwner{}, fmt.Errorf("capture process-group leader %d generation: unsupported generation", owner.pid)
	}
	owner.generation = generation
	if _, err := s.currentOwnedGroup(owner); err != nil {
		return processGroupOwner{}, fmt.Errorf("capture process-group leader %d ownership: %w", owner.pid, err)
	}
	return owner, nil
}

func (s processGroupSupervisor) currentOwnedGroup(owner processGroupOwner) (int, error) {
	if err := validateSafeProcessGroupID(owner.pid); err != nil {
		return 0, err
	}
	observed, err := s.observeGeneration(owner.pid, owner.generation)
	if err != nil {
		return 0, fmt.Errorf("observe process-group leader %d generation: %w", owner.pid, err)
	}
	if observed != owner.generation {
		return 0, fmt.Errorf("process-group leader %d generation changed", owner.pid)
	}
	groupID, err := s.processGroupID(owner.pid)
	if err != nil {
		return 0, fmt.Errorf("resolve process group for leader %d: %w", owner.pid, err)
	}
	if groupID != owner.pid {
		return 0, fmt.Errorf("process %d belongs to group %d, not its dedicated group", owner.pid, groupID)
	}
	observed, err = s.observeGeneration(owner.pid, owner.generation)
	if err != nil {
		return 0, fmt.Errorf("revalidate process-group leader %d generation: %w", owner.pid, err)
	}
	if observed != owner.generation {
		return 0, fmt.Errorf("process-group leader %d generation changed", owner.pid)
	}
	return groupID, nil
}

func (s processGroupSupervisor) cancelStartedCommand(cmd *exec.Cmd, owner processGroupOwner, exitWatch processExitWatcher, watchReady bool, watchErr, cause error) processGroupRunResult {
	waitErr, cleanupErr := s.stopStartedCommand(cmd, owner, exitWatch, watchReady, watchErr, true)
	return processGroupRunResult{
		runErr:     waitErr,
		cleanupErr: cleanupErr,
		cause:      cause,
		cleanupRan: true,
	}
}

func (s processGroupSupervisor) abortStartedCommand(ctx context.Context, cmd *exec.Cmd, owner processGroupOwner, startErr error) processGroupRunResult {
	waitErr, cleanupErr := s.stopStartedCommand(cmd, owner, nil, false, nil, false)
	return processGroupAbortResultWithCause(context.Cause(ctx), startErr, waitErr, cleanupErr)
}

func (s processGroupSupervisor) abortWatchedCommand(cmd *exec.Cmd, owner processGroupOwner, exitWatch processExitWatcher, watchErr error) processGroupRunResult {
	waitErr, cleanupErr := s.stopStartedCommand(cmd, owner, exitWatch, true, watchErr, false)
	return processGroupAbortResultWithCause(nil, nil, waitErr, cleanupErr)
}

func processGroupAbortResultWithCause(cause, supervisionErr, waitErr, cleanupErr error) processGroupRunResult {
	if cause == nil {
		return processGroupRunResult{
			runErr: errors.Join(supervisionErr, nonExitWaitFailure(waitErr), cleanupErr),
		}
	}
	return processGroupRunResult{
		runErr:     waitErr,
		cleanupErr: errors.Join(supervisionErr, cleanupErr),
		cause:      cause,
		cleanupRan: true,
	}
}

func nonExitWaitFailure(waitErr error) error {
	var exitErr *exec.ExitError
	if waitErr == nil || errors.As(waitErr, &exitErr) {
		return nil
	}
	return waitErr
}

// stopStartedCommand keeps the child unreaped until all numeric signals finish.
func (s processGroupSupervisor) stopStartedCommand(cmd *exec.Cmd, owner processGroupOwner, exitWatch processExitWatcher, watchReady bool, watchErr error, sendTerm bool) (error, error) {
	if cmd == nil || cmd.Process == nil {
		return nil, errors.New("started process-group command is required")
	}
	var sigtermErr error
	if sendTerm {
		sigtermErr = s.signalOwnedGroup(owner, syscall.SIGTERM)
		if !watchReady {
			watchReady, watchErr = awaitExitWatch(exitWatch, s.termGrace)
		}
	}
	sigkillErr := s.signalOwnedGroup(owner, syscall.SIGKILL)
	leaderKillErr := s.killProcess(cmd.Process)
	var watchTimeoutErr error
	if !watchReady && exitWatch != nil {
		observed, postKillErr := awaitExitWatch(exitWatch, s.proofLimit)
		if observed {
			watchReady = true
			watchErr = errors.Join(watchErr, postKillErr)
		} else {
			watchTimeoutErr = fmt.Errorf("process-group leader %d exit watch did not signal after SIGKILL", owner.pid)
		}
	}
	cleanupErr := errors.Join(leaderKillErr, watchTimeoutErr,
		closeAndDrainExitWatch(exitWatch, watchReady, watchErr, processGroupWatchDrainLimit))
	if !(watchReady && watchErr == nil) {
		return nil, errors.Join(cleanupErr,
			processGroupSignalError(owner.pid, "SIGTERM", sigtermErr, false),
			processGroupSignalError(owner.pid, "SIGKILL", sigkillErr, false),
			fmt.Errorf("process-group leader %d exit was not observed; command wait skipped", owner.pid))
	}
	waitErr := waitStartedCommand(cmd, s.proofLimit)
	proofErr := s.proveOwnedGroupAbsent(cmd, owner)
	// Darwin can report EPERM for a zombie-only group after the leader exits.
	proofSucceeded := proofErr == nil
	return waitErr, errors.Join(cleanupErr,
		processGroupSignalError(owner.pid, "SIGTERM", sigtermErr, proofSucceeded),
		processGroupSignalError(owner.pid, "SIGKILL", sigkillErr, proofSucceeded),
		proofErr)
}

func processGroupSignalError(pid int, signal string, err error, suppressEPERM bool) error {
	if err == nil || (suppressEPERM && errors.Is(err, syscall.EPERM)) {
		return nil
	}
	return fmt.Errorf("signal process group %d with %s: %w", pid, signal, err)
}

func (s processGroupSupervisor) signalOwnedGroup(owner processGroupOwner, signal syscall.Signal) error {
	if err := validateSafeProcessGroupID(owner.pid); err != nil {
		return err
	}
	return s.signalGroup(owner.pid, signal)
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

func awaitExitWatch(exitWatch processExitWatcher, limit time.Duration) (bool, error) {
	if exitWatch == nil {
		return false, nil
	}
	if limit <= 0 {
		limit = processGroupTerminationGrace
	}
	timer := time.NewTimer(limit)
	defer timer.Stop()
	select {
	case err := <-exitWatch.done():
		return true, err
	case <-timer.C:
		return false, nil
	}
}

func closeAndDrainExitWatch(exitWatch processExitWatcher, ready bool, priorErr error, limit time.Duration) error {
	if exitWatch == nil {
		return priorErr
	}
	closeErr := exitWatch.close()
	if ready {
		return errors.Join(priorErr, closeErr)
	}
	if limit <= 0 {
		limit = processGroupWatchDrainLimit
	}
	ready, err := awaitExitWatch(exitWatch, limit)
	if !ready {
		err = errors.New("process-group exit watch did not drain")
	}
	return errors.Join(priorErr, closeErr, err)
}

func waitStartedCommand(cmd *exec.Cmd, limit time.Duration) error {
	if cmd == nil {
		return errors.New("started process-group command is required")
	}
	if limit <= 0 {
		limit = processGroupProofLimit
	}
	// WaitDelay bounds copy goroutines after all process-group signals finish.
	cmd.WaitDelay = limit
	return cmd.Wait()
}

func (s processGroupSupervisor) proveOwnedGroupAbsent(cmd *exec.Cmd, owner processGroupOwner) error {
	if cmd == nil || cmd.ProcessState == nil {
		return errors.New("cannot prove process-group absence before the leader is reaped")
	}
	if owner.pid <= 0 {
		return fmt.Errorf("cannot prove process-group absence for invalid leader %d", owner.pid)
	}
	if owner.generation == "" {
		return fmt.Errorf("cannot prove process-group %d absence without leader generation", owner.pid)
	}
	observed, err := s.observeGeneration(owner.pid, owner.generation)
	if err == nil {
		if observed != owner.generation {
			return fmt.Errorf("process-group leader %d generation changed after wait", owner.pid)
		}
		return fmt.Errorf("process-group leader %d remains after wait", owner.pid)
	}
	if !IsProcessGone(err) {
		return fmt.Errorf("prove process-group leader %d is gone: %w", owner.pid, err)
	}
	absent, err := s.waitForOwnedGroupAbsence(owner.pid)
	if err != nil {
		return fmt.Errorf("poll process group %d after wait: %w", owner.pid, err)
	}
	if !absent {
		return fmt.Errorf("process group %d remains after wait", owner.pid)
	}
	return nil
}

func (s processGroupSupervisor) waitForOwnedGroupAbsence(groupID int) (bool, error) {
	if err := validateSafeProcessGroupID(groupID); err != nil {
		return false, err
	}
	return s.waitAbsent(groupID, s.proofLimit)
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
