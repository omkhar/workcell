// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

//go:build linux

package aptbroker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const helperTerminationGrace = 2 * time.Second
const helperOutputDrainLimit = 2 * time.Second

var (
	errHelperOutputLimit = errors.New("helper output exceeds limit")
	errHelperOutputDrain = errors.New("helper output drain exceeded limit")
	errHelperTimeout     = errors.New("helper timeout")
)

type helperOutput struct {
	stdout   *limitedBuffer
	stderr   *limitedBuffer
	readers  []io.ReadCloser
	done     chan struct{}
	overflow chan struct{}
}

type startedHelper struct {
	command *exec.Cmd
	output  *helperOutput
	owner   helperOwner
	watcher *helperWatcher
}

func runOwnedHelper(ctx context.Context, connection *net.UnixConn, request Request, config helperConfig) (Response, error) {
	return runOwnedHelperWithSystem(ctx, connection, request, config, defaultHelperSystem())
}

func runOwnedHelperWithSystem(ctx context.Context, connection *net.UnixConn, request Request, config helperConfig, system helperSystem) (Response, error) {
	if err := ctx.Err(); err != nil {
		return Response{}, err
	}
	helper, err := startOwnedHelper(request, config, system)
	if err != nil {
		return Response{}, err
	}
	defer helper.watcher.close()
	cause := arbitrateHelper(ctx, connection, helper.watcher, helper.output, config.timeout, system.afterFunc)
	cleanupErr := stopOwnedHelper(helper.command, helper.owner, helper.output, helper.watcher, cause, system)
	return helperResponse(helper.command, helper.output, cause, cleanupErr, config.report)
}

func startOwnedHelper(request Request, config helperConfig, system helperSystem) (*startedHelper, error) {
	command := exec.Command(config.path, request.Args...)
	command.Env = fixedEnvironment(request.Env)
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	output, err := startHelperOutput(command)
	if err != nil {
		return nil, err
	}
	if err := system.start(command); err != nil {
		output.close()
		return nil, err
	}
	return superviseHelperStart(command, output, system)
}

func superviseHelperStart(command *exec.Cmd, output *helperOutput, system helperSystem) (*startedHelper, error) {
	owner, err := captureHelperOwner(command.Process.Pid, system)
	if err != nil {
		return nil, abortUnprovedHelper(command, output, err, system)
	}
	watcher, err := system.openWatcher(owner.pid)
	if err != nil {
		cleanupErr := stopOwnedHelper(command, owner, output, nil, err, system)
		return nil, errors.Join(err, cleanupErr)
	}
	return &startedHelper{command: command, output: output, owner: owner, watcher: watcher}, nil
}

func helperResponse(command *exec.Cmd, output *helperOutput, cause, cleanupErr error, report func(error)) (Response, error) {
	if _, committed := committedHelperStatus(cause); !committed {
		if err := errors.Join(cause, cleanupErr); err != nil {
			return Response{}, err
		}
	}
	if cause == nil {
		cause = output.limitError()
	}
	status, committed := committedHelperStatus(cause)
	response := Response{Status: exitStatus(command.ProcessState), Stdout: output.stdout.bytes(), Stderr: output.stderr.bytes()}
	if committed {
		report(cleanupErr)
		response.Status = status
	}
	return response, nil
}

func committedHelperStatus(cause error) (int, bool) {
	if errors.Is(cause, errHelperOutputLimit) {
		return 1, true
	}
	if errors.Is(cause, errHelperTimeout) {
		return 124, true
	}
	return 0, false
}

func (output *helperOutput) limitError() error {
	if output.stdout.overflow.Load() {
		return errHelperOutputLimit
	}
	if output.stderr.overflow.Load() {
		return errHelperOutputLimit
	}
	return nil
}

func startHelperOutput(command *exec.Cmd) (*helperOutput, error) {
	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		_ = stdout.Close()
		return nil, err
	}
	output := &helperOutput{
		stdout:   newLimitedBuffer(),
		stderr:   newLimitedBuffer(),
		readers:  []io.ReadCloser{stdout, stderr},
		done:     make(chan struct{}),
		overflow: make(chan struct{}, 1),
	}
	go output.copy()
	return output, nil
}

func (output *helperOutput) copy() {
	var readers sync.WaitGroup
	readers.Add(2)
	go output.copyStream(&readers, output.stdout, output.readers[0])
	go output.copyStream(&readers, output.stderr, output.readers[1])
	readers.Wait()
	close(output.done)
}

func (output *helperOutput) copyStream(readers *sync.WaitGroup, destination *limitedBuffer, source io.Reader) {
	defer readers.Done()
	_, _ = io.Copy(destination, source)
	if destination.overflow.Load() {
		select {
		case output.overflow <- struct{}{}:
		default:
		}
	}
}

func (output *helperOutput) close() {
	output.closeReaders()
	<-output.done
}

func (output *helperOutput) finish(limit time.Duration) error {
	timer := time.NewTimer(limit)
	defer timer.Stop()
	select {
	case <-output.done:
		return nil
	case <-timer.C:
		output.closeReaders()
		<-output.done
		return errHelperOutputDrain
	}
}

func (output *helperOutput) closeReaders() {
	for _, reader := range output.readers {
		_ = reader.Close()
	}
}

type helperOwner struct{ pid int }

type helperSystem struct {
	start          func(*exec.Cmd) error
	processGroupID func(int) (int, error)
	openWatcher    func(int) (*helperWatcher, error)
	signalGroup    func(int, syscall.Signal) error
	killExact      func(*exec.Cmd) error
	wait           func(*exec.Cmd) error
	waitWatcher    func(*helperWatcher, time.Duration)
	afterFunc      helperAfterFunc
	drainLimit     time.Duration
}

func defaultHelperSystem() helperSystem {
	return helperSystem{
		start:          func(command *exec.Cmd) error { return command.Start() },
		processGroupID: syscall.Getpgid,
		openWatcher:    openHelperWatcher,
		signalGroup:    func(pid int, signal syscall.Signal) error { return syscall.Kill(-pid, signal) },
		killExact:      killExactHelper,
		wait:           func(command *exec.Cmd) error { return command.Wait() },
		waitWatcher:    waitWatcher,
		afterFunc:      context.AfterFunc,
		drainLimit:     helperOutputDrainLimit,
	}
}

func captureHelperOwner(pid int, system helperSystem) (helperOwner, error) {
	if pid <= 1 {
		return helperOwner{}, fmt.Errorf("unsafe helper pid")
	}
	group, err := system.processGroupID(pid)
	if err != nil || group != pid {
		return helperOwner{}, fmt.Errorf("helper does not own its process group")
	}
	return helperOwner{pid: pid}, nil
}

type helperWatcher struct {
	fd   int
	done chan struct{}
	err  error
}

func openHelperWatcher(pid int) (*helperWatcher, error) {
	fd, err := unix.PidfdOpen(pid, 0)
	if err != nil {
		return nil, fmt.Errorf("open helper exit watcher: %w", err)
	}
	watcher := &helperWatcher{fd: fd, done: make(chan struct{})}
	go watcher.watch()
	return watcher, nil
}

func (watcher *helperWatcher) close() {
	_ = unix.Close(watcher.fd)
}

func (watcher *helperWatcher) watch() {
	watcher.watchWith(unix.Poll)
}

func (watcher *helperWatcher) watchWith(poll func([]unix.PollFd, int) (int, error)) {
	descriptors := []unix.PollFd{{Fd: int32(watcher.fd), Events: unix.POLLIN}}
	for {
		ready, err := poll(descriptors, 100)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			watcher.err = err
			close(watcher.done)
			return
		}
		if ready == 1 {
			close(watcher.done)
			return
		}
	}
}

func arbitrateHelper(ctx context.Context, connection *net.UnixConn, watcher *helperWatcher, output *helperOutput, timeout time.Duration, afterFunc helperAfterFunc) error {
	runContext, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	timer := time.AfterFunc(timeout, func() { cancel(errHelperTimeout) })
	defer timer.Stop()
	go watchDisconnect(runContext, connection, cancel)
	go watchOutputOverflow(runContext, output.overflow, cancel)
	return committedHelperCause(runContext, watcher, afterFunc)
}

type helperAfterFunc func(context.Context, func()) func() bool

func committedHelperCause(ctx context.Context, watcher *helperWatcher, afterFunc helperAfterFunc) error {
	cancelRequest := make(chan error, 1)
	stopCancel := afterFunc(ctx, func() { cancelRequest <- context.Cause(ctx) })
	select {
	case <-watcher.done:
		if stopCancel() {
			return watcher.err
		}
		return <-cancelRequest
	case cause := <-cancelRequest:
		return cause
	}
}

func watchDisconnect(ctx context.Context, connection *net.UnixConn, cancel context.CancelCauseFunc) {
	buffer := make([]byte, 1)
	for {
		_ = connection.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		_, err := connection.Read(buffer)
		if retryDisconnectRead(ctx, err) {
			continue
		}
		cancel(disconnectCause(err))
		return
	}
}

func retryDisconnectRead(ctx context.Context, err error) bool {
	timeout, ok := err.(net.Error)
	return ok && timeout.Timeout() && ctx.Err() == nil
}

func disconnectCause(err error) error {
	if err == nil {
		return net.ErrClosed
	}
	return err
}

func watchOutputOverflow(ctx context.Context, overflow <-chan struct{}, cancel context.CancelCauseFunc) {
	select {
	case <-overflow:
		cancel(errHelperOutputLimit)
	case <-ctx.Done():
	}
}

func stopOwnedHelper(command *exec.Cmd, owner helperOwner, output *helperOutput, watcher *helperWatcher, cause error, system helperSystem) error {
	cleanupErr := terminateOwnedHelper(command, owner, watcher, cause != nil, system)
	drainErr := output.finish(system.drainLimit)
	waitErr := system.wait(command)
	if cause == nil && cleanupErr == nil && drainErr == nil {
		return nonExitError(waitErr)
	}
	return errors.Join(cleanupErr, drainErr, nonExitError(waitErr))
}

func terminateOwnedHelper(command *exec.Cmd, owner helperOwner, watcher *helperWatcher, sendTerm bool, system helperSystem) error {
	var cleanupErr error
	if sendTerm || !ownedHelperExited(watcher) {
		cleanupErr = signalOwnedHelper(owner, syscall.SIGTERM, system)
		system.waitWatcher(watcher, helperTerminationGrace)
	}
	cleanupErr = errors.Join(cleanupErr, signalOwnedHelper(owner, syscall.SIGKILL, system), system.killExact(command))
	system.waitWatcher(watcher, helperTerminationGrace)
	return cleanupErr
}

func killExactHelper(command *exec.Cmd) error {
	err := command.Process.Kill()
	if errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}

func ownedHelperExited(watcher *helperWatcher) bool {
	if watcher == nil {
		return false
	}
	select {
	case <-watcher.done:
		return true
	default:
		return false
	}
}

func signalOwnedHelper(owner helperOwner, signal syscall.Signal, system helperSystem) error {
	group, err := system.processGroupID(owner.pid)
	if err != nil || group != owner.pid {
		return fmt.Errorf("helper process-group ownership is unavailable")
	}
	if err := system.signalGroup(owner.pid, signal); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}

func waitWatcher(watcher *helperWatcher, limit time.Duration) {
	if watcher == nil {
		return
	}
	timer := time.NewTimer(limit)
	defer timer.Stop()
	select {
	case <-watcher.done:
	case <-timer.C:
	}
}

func abortUnprovedHelper(command *exec.Cmd, output *helperOutput, proofErr error, system helperSystem) error {
	killErr := system.killExact(command)
	drainErr := output.finish(system.drainLimit)
	waitErr := system.wait(command)
	return errors.Join(proofErr, killErr, drainErr, nonExitError(waitErr))
}

func nonExitError(err error) error {
	var exitErr *exec.ExitError
	if err == nil || errors.As(err, &exitErr) {
		return nil
	}
	return err
}

func exitStatus(state *os.ProcessState) int {
	if wait, ok := state.Sys().(syscall.WaitStatus); ok && wait.Signaled() {
		return 128 + int(wait.Signal())
	}
	status := state.ExitCode()
	if status < 0 {
		return 1
	}
	return status
}
