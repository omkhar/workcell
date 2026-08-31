// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package launcher

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

type supervisorTestWatcher struct {
	result  chan error
	closeFn func() error
}

func newSupervisorTestWatcher() *supervisorTestWatcher {
	return &supervisorTestWatcher{result: make(chan error, 1)}
}
func (w *supervisorTestWatcher) done() <-chan error { return w.result }
func (w *supervisorTestWatcher) signal(err error) {
	select {
	case w.result <- err:
	default:
	}
}
func (w *supervisorTestWatcher) close() error {
	w.signal(nil)
	if w.closeFn != nil {
		return w.closeFn()
	}
	return nil
}
func fixturePID(t *testing.T, path string) int {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		data, err := os.ReadFile(path)
		pid, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
		if err == nil && parseErr == nil && pid > 1 {
			return pid
		}
	}
	t.Fatalf("fixture PID was not ready: %s", path)
	return 0
}
func startSupervisorTestCommand(t *testing.T, cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
}

func TestProcessGroupSupervisorLeaderExitCancellation(t *testing.T) {
	cause := errors.New("leader exit cancellation")
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(context.Canceled)
	dir := t.TempDir()
	childPIDPath := filepath.Join(dir, "child-pid")
	releasePath := filepath.Join(dir, "release")
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", `
/bin/sh -c 'trap "" TERM; printf "%s\n" "$$" >"$1"; sleep 5' fixture-child "$1" &
while [ ! -f "$2" ]; do sleep .01; done
exit 0
`, "fixture", childPIDPath, releasePath)

	watchInstalled := make(chan struct{})
	childReady := make(chan int, 1)
	var signals []syscall.Signal
	var signaledAfterReap, termSurvived, watchClosedBeforeWait bool
	s := defaultProcessGroupSupervisor()
	s.signalGroup = func(groupID int, signal syscall.Signal) error {
		signaledAfterReap = signaledAfterReap || cmd.ProcessState != nil
		signals = append(signals, signal)
		err := signalProcessGroup(groupID, signal)
		if signal == syscall.SIGTERM {
			termSurvived = syscall.Kill(<-childReady, 0) == nil
		}
		return err
	}
	s.afterFunc = func(_ context.Context, callback func()) func() bool {
		return func() bool { cancel(cause); callback(); return false }
	}
	s.watchExit = func(pid int) (processExitWatcher, error) {
		inner, err := startProcessExitWatch(pid)
		if err != nil {
			return nil, err
		}
		watch := newSupervisorTestWatcher()
		watch.closeFn = func() error {
			watchClosedBeforeWait = cmd.ProcessState == nil
			return inner.close()
		}
		go func() {
			watch.signal(<-inner.done())
		}()
		close(watchInstalled)
		return watch, nil
	}

	results := make(chan processGroupRunResult, 1)
	go func() { results <- s.run(ctx, cmd) }()
	select {
	case <-watchInstalled:
	case <-time.After(5 * time.Second):
		t.Fatal("exit watcher was not installed")
	}
	childReady <- fixturePID(t, childPIDPath)
	if err := os.WriteFile(releasePath, []byte("release\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var result processGroupRunResult
	select {
	case result = <-results:
	case <-time.After(10 * time.Second):
		if cmd.Process != nil && cmd.ProcessState == nil {
			_ = signalProcessGroup(cmd.Process.Pid, syscall.SIGKILL)
		}
		t.Fatal("leader-exit cleanup did not finish")
	}
	if result.runErr != nil || result.cleanupErr != nil || !result.cleanupRan || !errors.Is(result.cause, cause) {
		t.Fatalf("run result = %+v", result)
	}
	if len(signals) != 2 || signals[0] != syscall.SIGTERM || signals[1] != syscall.SIGKILL {
		t.Fatalf("signals = %v, want TERM then KILL", signals)
	}
	if signaledAfterReap || !termSurvived || !watchClosedBeforeWait || cmd.ProcessState == nil {
		t.Fatalf("ordering: after-reap=%v term-survived=%v close-before-wait=%v state=%v", signaledAfterReap, termSurvived, watchClosedBeforeWait, cmd.ProcessState)
	}
	if err := syscall.Kill(-cmd.Process.Pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("process group %d remains after cleanup: %v", cmd.Process.Pid, err)
	}
}
func TestProcessGroupSupervisorOrdinaryExit(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-c", "exit 7")
	watch := newSupervisorTestWatcher()
	watch.signal(nil)
	closedBeforeWait := false
	watch.closeFn = func() error { closedBeforeWait = cmd.ProcessState == nil; return nil }
	s := defaultProcessGroupSupervisor()
	s.watchExit = func(int) (processExitWatcher, error) { return watch, nil }
	result := s.run(context.Background(), cmd)
	var exitErr *exec.ExitError
	if result.cleanupRan || !closedBeforeWait || cmd.ProcessState == nil ||
		!errors.As(result.runErr, &exitErr) || exitErr.ExitCode() != 7 {
		t.Fatalf("run result = %+v, want ordinary exit 7", result)
	}
}
func TestProcessGroupSupervisorWatchFailures(t *testing.T) {
	sentinel := errors.New("supervision failed")
	tests := []struct {
		name      string
		configure func(*processGroupSupervisor, *supervisorTestWatcher)
		wantErr   error
		wantText  string
	}{
		{"capture generation", func(s *processGroupSupervisor, _ *supervisorTestWatcher) {
			s.captureGeneration = func(int) (string, error) { return "", sentinel }
		}, sentinel, ""},
		{"unsupported generation", func(s *processGroupSupervisor, _ *supervisorTestWatcher) {
			s.captureGeneration = func(int) (string, error) { return "legacy start time", nil }
		}, nil, "unsupported generation"},
		{"second generation observation", func(s *processGroupSupervisor, _ *supervisorTestWatcher) {
			calls := 0
			s.observeGeneration = func(_ int, recorded string) (string, error) {
				calls++
				return []string{recorded, "darwin:reused"}[calls-1], nil
			}
		}, nil, "generation changed"},
		{"watch setup", func(s *processGroupSupervisor, _ *supervisorTestWatcher) {
			s.watchExit = func(int) (processExitWatcher, error) { return nil, sentinel }
		}, sentinel, ""},
		{"watch runtime", func(s *processGroupSupervisor, w *supervisorTestWatcher) {
			s.watchExit = func(int) (processExitWatcher, error) { w.signal(sentinel); return w, nil }
		}, sentinel, ""},
		{"nil watcher", func(s *processGroupSupervisor, _ *supervisorTestWatcher) {
			s.watchExit = func(int) (processExitWatcher, error) { return nil, nil }
		}, nil, "exit watch"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command("/bin/sleep", "2")
			watch := newSupervisorTestWatcher()
			var signals []syscall.Signal
			signaledAfterReap := false
			s := defaultProcessGroupSupervisor()
			tc.configure(&s, watch)
			s.signalGroup = func(groupID int, signal syscall.Signal) error {
				signaledAfterReap = signaledAfterReap || cmd.ProcessState != nil
				signals = append(signals, signal)
				return signalProcessGroup(groupID, signal)
			}
			result := s.run(context.Background(), cmd)
			unreaped := cmd.ProcessState == nil
			if unreaped {
				_ = cmd.Wait()
			}
			if result.cleanupErr != nil || !unreaped || cmd.ProcessState == nil || signaledAfterReap ||
				len(signals) != 1 || signals[0] != syscall.SIGKILL {
				t.Fatalf("run result=%+v signals=%v after-reap=%v state=%v", result, signals, signaledAfterReap, cmd.ProcessState)
			}
			if !strings.Contains(result.runErr.Error(), "wait skipped") || tc.wantErr != nil && !errors.Is(result.runErr, tc.wantErr) ||
				tc.wantText != "" && !strings.Contains(result.runErr.Error(), tc.wantText) {
				t.Fatalf("run error = %v, want %v %q", result.runErr, tc.wantErr, tc.wantText)
			}
			if code, err := colimaRunResult(result.runErr); code != 0 || err == nil {
				t.Fatalf("colima result = %d, %v; want surfaced supervision error", code, err)
			}
		})
	}
}
func TestProcessGroupSupervisorSkipsWaitWithoutObservedExit(t *testing.T) {
	for _, directErr := range []error{errors.New("direct kill failed"), nil} {
		for _, groupErr := range []error{errors.New("group signal failed"), nil} {
			cmd := exec.Command("/bin/sleep", "30")
			startSupervisorTestCommand(t, cmd)
			t.Cleanup(func() {
				if cmd.ProcessState == nil {
					_ = cmd.Process.Kill()
					_ = cmd.Wait()
				}
			})
			s := defaultProcessGroupSupervisor()
			owner, err := s.captureOwner(cmd.Process)
			if err != nil {
				t.Fatal(err)
			}
			s.termGrace, s.proofLimit = 10*time.Millisecond, 10*time.Millisecond
			s.signalGroup = func(int, syscall.Signal) error { return groupErr }
			s.killProcess = func(*os.Process) error { return directErr }
			fallbackDone := make(chan struct{})
			killFallback := time.AfterFunc(2*time.Second, func() { _ = cmd.Process.Kill(); close(fallbackDone) })
			waitErr, cleanupErr := s.stopStartedCommand(
				cmd, owner, newSupervisorTestWatcher(), false, nil, true)
			stopped := killFallback.Stop()
			if !stopped {
				<-fallbackDone
			}
			if !stopped || waitErr != nil || cmd.ProcessState != nil {
				t.Fatalf("unconfirmed cleanup was not bounded: wait=%v state=%v", waitErr, cmd.ProcessState)
			}
			if directErr != nil && !errors.Is(cleanupErr, directErr) || !strings.Contains(cleanupErr.Error(), "wait skipped") ||
				!strings.Contains(cleanupErr.Error(), "exit watch did not signal") ||
				groupErr != nil && !errors.Is(cleanupErr, groupErr) {
				t.Fatalf("cleanup error = %v, group error = %v", cleanupErr, groupErr)
			}
		}
	}
}
func TestProcessGroupSupervisorSignalEPERMRequiresProof(t *testing.T) {
	proofErr := errors.New("absence proof failed")
	tests := []struct {
		signal    syscall.Signal
		absent    bool
		proofErr  error
		reuse     bool
		wantError bool
	}{
		{syscall.SIGTERM, true, nil, false, false},
		{syscall.SIGTERM, false, nil, false, true},
		{syscall.SIGKILL, true, nil, false, false},
		{syscall.SIGKILL, false, proofErr, false, true},
		{syscall.SIGKILL, false, nil, true, true},
	}
	for _, tc := range tests {
		cmd := exec.Command("/bin/sh", "-c", "while :; do sleep 1; done")
		startSupervisorTestCommand(t, cmd)
		owner := processGroupOwner{pid: cmd.Process.Pid, generation: "darwin:test"}
		watch := newSupervisorTestWatcher()
		watch.signal(nil)
		signaledAfterReap := false
		s := defaultProcessGroupSupervisor()
		s.signalGroup = func(_ int, signal syscall.Signal) error {
			signaledAfterReap = signaledAfterReap || cmd.ProcessState != nil
			if signal == tc.signal {
				return syscall.EPERM
			}
			return nil
		}
		s.killProcess = func(process *os.Process) error { return process.Kill() }
		s.observeGeneration = func(pid int, _ string) (string, error) {
			if tc.reuse {
				return "darwin:reused", nil
			}
			return "", processGoneErr{pid: pid}
		}
		s.waitAbsent = func(int, time.Duration) (bool, error) {
			return tc.absent, tc.proofErr
		}
		_, cleanupErr := s.stopStartedCommand(cmd, owner, watch, true, nil, true)
		if cmd.ProcessState == nil || signaledAfterReap {
			t.Fatalf("ordering: state=%v after-reap=%v", cmd.ProcessState, signaledAfterReap)
		}
		if (cleanupErr != nil) != tc.wantError || tc.wantError && !errors.Is(cleanupErr, syscall.EPERM) ||
			tc.proofErr != nil && !errors.Is(cleanupErr, tc.proofErr) ||
			tc.reuse && !strings.Contains(cleanupErr.Error(), "generation changed after wait") {
			t.Fatalf("cleanup error = %v for signal=%s proof=%v reuse=%v", cleanupErr, tc.signal, tc.proofErr, tc.reuse)
		}
	}
}
func TestColimaCancellationResultsPreserveFailures(t *testing.T) {
	runErr, cleanupErr, cause := errors.New("run failed"), errors.New("cleanup failed"), context.DeadlineExceeded
	if code, err := colimaCancellationResult(runErr, cleanupErr, cause); code != 0 ||
		!errors.Is(err, runErr) || !errors.Is(err, cleanupErr) || !errors.Is(err, cause) {
		t.Fatalf("cleanup failure result = %d, %v", code, err)
	}
	if code, err := colimaCancellationResult(runErr, nil, cause); code != 0 || !errors.Is(err, runErr) {
		t.Fatalf("unexpected wait failure result = %d, %v", code, err)
	}
}
func TestColimaCancellationResultPreservesTimeout(t *testing.T) {
	runErr := exec.Command("/bin/sh", "-c", "exit 11").Run()
	var exitErr *exec.ExitError
	if code, err := colimaCancellationResult(runErr, nil, context.DeadlineExceeded); !errors.As(runErr, &exitErr) || code != ColimaTimeoutExitCode || err != nil {
		t.Fatalf("graceful nonzero timeout result = %d, %v", code, err)
	}
}
func TestWaitStartedCommandWaitDelayIsSynchronous(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-c", "sleep .2 & exit 0")
	cmd.Stdout = &bytes.Buffer{}
	startSupervisorTestCommand(t, cmd)
	if err := waitStartedCommand(cmd, 20*time.Millisecond); !errors.Is(err, exec.ErrWaitDelay) {
		t.Fatalf("wait error = %v, want ErrWaitDelay", err)
	}
	if cmd.ProcessState == nil {
		t.Fatal("Wait returned before recording process state")
	}
	if absent, err := waitForProcessGroupExit(cmd.Process.Pid, 2*time.Second); err != nil || !absent {
		t.Fatalf("held-pipe process group remains: absent=%v err=%v", absent, err)
	}
}
func TestProcessGroupSupervisorRejectsPresetGroup(t *testing.T) {
	for _, attr := range []syscall.SysProcAttr{{Pgid: 42}, {Foreground: true}} {
		cmd := exec.Command("/usr/bin/true")
		cmd.SysProcAttr = &attr
		result := defaultProcessGroupSupervisor().run(context.Background(), cmd)
		if result.runErr == nil || !strings.Contains(result.runErr.Error(), "must not select") || cmd.Process != nil {
			t.Fatalf("preset group result=%+v process=%v", result, cmd.Process)
		}
	}
}

func TestProcessGroupHelpersRejectUnsafeIDsAndTreatEPERMAsPresent(t *testing.T) {
	for _, groupID := range []int{-1, 0, 1, syscall.Getpgrp()} {
		called := false
		if err := signalProcessGroupWithKill(groupID, syscall.SIGTERM, func(int, syscall.Signal) error { called = true; return nil }); err == nil || called {
			t.Fatalf("unsafe signal group %d: err=%v called=%v", groupID, err, called)
		}
		if absent, err := waitForProcessGroupExitWithProbe(groupID, 0,
			func(int, syscall.Signal) error { called = true; return nil },
			time.Now, time.Sleep); err == nil || absent || called {
			t.Fatalf("unsafe probe group %d: absent=%v err=%v called=%v", groupID, absent, err, called)
		}
	}
	absent, err := waitForProcessGroupExitWithProbe(42, 0,
		func(pid int, signal syscall.Signal) error {
			if pid != -42 || signal != 0 {
				t.Fatalf("probe = (%d, %d)", pid, signal)
			}
			return syscall.EPERM
		},
		func() time.Time { return time.Unix(1, 0) },
		func(time.Duration) { t.Fatal("unexpected sleep") })
	if err != nil || absent {
		t.Fatalf("EPERM probe = absent=%v err=%v, want present", absent, err)
	}
}
