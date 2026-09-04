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

type leaderExitSupervisorFixture struct {
	t                     *testing.T
	cause                 error
	cancel                context.CancelCauseFunc
	cmd                   *exec.Cmd
	childPIDPath          string
	releasePath           string
	watchInstalled        chan struct{}
	childReady            chan int
	signals               []syscall.Signal
	signaledAfterReap     bool
	termSurvived          bool
	watchClosedBeforeWait bool
}

func newLeaderExitSupervisorFixture(t *testing.T) (*leaderExitSupervisorFixture, context.Context) {
	t.Helper()
	cause := errors.New("leader exit cancellation")
	ctx, cancel := context.WithCancelCause(context.Background())
	t.Cleanup(func() { cancel(context.Canceled) })
	dir := t.TempDir()
	childPIDPath := filepath.Join(dir, "child-pid")
	releasePath := filepath.Join(dir, "release")
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", `
/bin/sh -c 'trap "" TERM; printf "%s\n" "$$" >"$1"; sleep 5' fixture-child "$1" &
while [ ! -f "$2" ]; do sleep .01; done
exit 0
`, "fixture", childPIDPath, releasePath)
	return &leaderExitSupervisorFixture{
		t:              t,
		cause:          cause,
		cancel:         cancel,
		cmd:            cmd,
		childPIDPath:   childPIDPath,
		releasePath:    releasePath,
		watchInstalled: make(chan struct{}),
		childReady:     make(chan int, 1),
	}, ctx
}

func (f *leaderExitSupervisorFixture) signalGroup(groupID int, signal syscall.Signal) error {
	f.signaledAfterReap = f.signaledAfterReap || f.cmd.ProcessState != nil
	f.signals = append(f.signals, signal)
	err := signalProcessGroup(groupID, signal)
	if signal == syscall.SIGTERM {
		f.termSurvived = syscall.Kill(<-f.childReady, 0) == nil
	}
	return err
}

func (f *leaderExitSupervisorFixture) afterFunc(_ context.Context, callback func()) func() bool {
	return func() bool {
		f.cancel(f.cause)
		callback()
		return false
	}
}

func (f *leaderExitSupervisorFixture) watchExit(pid int) (processExitWatcher, error) {
	inner, err := startProcessExitWatch(pid)
	if err != nil {
		return nil, err
	}
	watch := newSupervisorTestWatcher()
	watch.closeFn = func() error {
		f.watchClosedBeforeWait = f.cmd.ProcessState == nil
		return inner.close()
	}
	go func() { watch.signal(<-inner.done()) }()
	close(f.watchInstalled)
	return watch, nil
}

func (f *leaderExitSupervisorFixture) admitLeaderExit() {
	f.t.Helper()
	select {
	case <-f.watchInstalled:
	case <-time.After(5 * time.Second):
		f.t.Fatal("exit watcher was not installed")
	}
	f.childReady <- fixturePID(f.t, f.childPIDPath)
	if err := os.WriteFile(f.releasePath, []byte("release\n"), 0o600); err != nil {
		f.t.Fatal(err)
	}
}

func (f *leaderExitSupervisorFixture) awaitResult(results <-chan processGroupRunResult) processGroupRunResult {
	f.t.Helper()
	select {
	case result := <-results:
		return result
	case <-time.After(10 * time.Second):
		killRunningSupervisorTestCommand(f.cmd)
		f.t.Fatal("leader-exit cleanup did not finish")
		return processGroupRunResult{}
	}
}

func killRunningSupervisorTestCommand(cmd *exec.Cmd) {
	if cmd.Process != nil && cmd.ProcessState == nil {
		_ = signalProcessGroup(cmd.Process.Pid, syscall.SIGKILL)
	}
}

func (f *leaderExitSupervisorFixture) assertResult(result processGroupRunResult) {
	f.t.Helper()
	if result.runErr != nil || result.cleanupErr != nil || !result.cleanupRan {
		f.t.Fatalf("run result = %+v", result)
	}
	if !errors.Is(result.cause, f.cause) {
		f.t.Fatalf("run result = %+v, want cause %v", result, f.cause)
	}
}

func (f *leaderExitSupervisorFixture) assertSignalOrder() {
	f.t.Helper()
	if len(f.signals) != 2 {
		f.t.Fatalf("signals = %v, want TERM then KILL", f.signals)
	}
	if f.signals[0] != syscall.SIGTERM || f.signals[1] != syscall.SIGKILL {
		f.t.Fatalf("signals = %v, want TERM then KILL", f.signals)
	}
}

func (f *leaderExitSupervisorFixture) assertCleanupOrder() {
	f.t.Helper()
	if f.signaledAfterReap || !f.termSurvived || !f.watchClosedBeforeWait {
		f.t.Fatalf("ordering: after-reap=%v term-survived=%v close-before-wait=%v state=%v", f.signaledAfterReap, f.termSurvived, f.watchClosedBeforeWait, f.cmd.ProcessState)
	}
	if f.cmd.ProcessState == nil {
		f.t.Fatal("leader was not reaped after cleanup")
	}
}

func (f *leaderExitSupervisorFixture) assertGroupAbsent() {
	f.t.Helper()
	if err := syscall.Kill(-f.cmd.Process.Pid, 0); !errors.Is(err, syscall.ESRCH) {
		f.t.Fatalf("process group %d remains after cleanup: %v", f.cmd.Process.Pid, err)
	}
}

func TestSignalOwnedGroupRejectsReapedLeader(t *testing.T) {
	cmd := exec.Command("/usr/bin/true")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	signaled := false
	s := defaultProcessGroupSupervisor()
	s.signalGroup = func(int, syscall.Signal) error {
		signaled = true
		return nil
	}
	err := s.signalOwnedGroup(cmd, processGroupOwner{pid: cmd.Process.Pid}, syscall.SIGKILL)
	if err == nil || !strings.Contains(err.Error(), "reaped before signaling") || signaled {
		t.Fatalf("reaped leader signal = %v, signaled=%t", err, signaled)
	}
}

func TestProcessGroupSupervisorLeaderExitCancellation(t *testing.T) {
	fixture, ctx := newLeaderExitSupervisorFixture(t)
	s := defaultProcessGroupSupervisor()
	s.signalGroup = fixture.signalGroup
	s.afterFunc = fixture.afterFunc
	s.watchExit = fixture.watchExit

	results := make(chan processGroupRunResult, 1)
	go func() { results <- s.run(ctx, fixture.cmd) }()
	fixture.admitLeaderExit()
	result := fixture.awaitResult(results)
	fixture.assertResult(result)
	fixture.assertSignalOrder()
	fixture.assertCleanupOrder()
	fixture.assertGroupAbsent()
}
func TestProcessGroupSupervisorOrdinaryExit(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-c", "exit 7")
	result := defaultProcessGroupSupervisor().run(context.Background(), cmd)
	assertNoProcessGroupCleanup(t, cmd, result, 7)
	assertProcessGroupExitCode(t, result, 7)
}

func TestProcessGroupSupervisorAfterStartLinearizesOwnership(t *testing.T) {
	cmd := exec.Command("/bin/sleep", "1")
	calls := 0
	result := defaultProcessGroupSupervisor().runAfterStart(context.Background(), cmd, func() {
		calls++
		assertStartedProcess(t, cmd)
	})
	if calls != 1 {
		t.Fatalf("after-start calls = %d, want 1", calls)
	}
	if result.runErr != nil || result.cleanupErr != nil {
		t.Fatalf("after-start result = %+v", result)
	}
}

func assertStartedProcess(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	if cmd.Process == nil || cmd.ProcessState != nil {
		t.Fatalf("after-start process state = %v, %v", cmd.Process, cmd.ProcessState)
	}
}

func TestProcessGroupSupervisorStartFailureSkipsAfterStart(t *testing.T) {
	calls := 0
	cmd := exec.Command(filepath.Join(t.TempDir(), "missing"))
	result := defaultProcessGroupSupervisor().runAfterStart(context.Background(), cmd, func() { calls++ })
	if result.runErr == nil || calls != 0 {
		t.Fatalf("start failure result = %+v, calls = %d", result, calls)
	}
}

func TestProcessGroupSupervisorOrdinaryExitSurfacesCleanupFailure(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-c", "exit 7")
	proofErr := errors.New("absence proof failed")
	s := defaultProcessGroupSupervisor()
	s.waitAbsent = func(int, time.Duration) (bool, error) { return false, proofErr }
	result := s.run(context.Background(), cmd)
	if !errors.Is(result.cleanupErr, proofErr) || !errors.Is(result.runErr, proofErr) {
		t.Fatalf("ordinary exit result = %+v, want surfaced cleanup failure", result)
	}
	assertColimaOrdinaryFailure(t, result, proofErr)
	if !strings.Contains(result.runErr.Error(), "exit status 7") {
		t.Fatalf("ordinary exit error = %v, want exit status", result.runErr)
	}
}

func assertNoProcessGroupCleanup(t *testing.T, cmd *exec.Cmd, result processGroupRunResult, wantCode int) {
	t.Helper()
	if result.cleanupRan || result.cleanupErr != nil || cmd.ProcessState == nil {
		t.Fatalf("run result = %+v, want ordinary exit %d", result, wantCode)
	}
}

func assertProcessGroupExitCode(t *testing.T, result processGroupRunResult, wantCode int) {
	t.Helper()
	var exitErr *exec.ExitError
	if !errors.As(result.runErr, &exitErr) || exitErr.ExitCode() != wantCode {
		t.Fatalf("run result = %+v, want ordinary exit %d", result, wantCode)
	}
}

func TestProcessGroupSupervisorOrdinaryExitDrainsPersistentDescendant(t *testing.T) {
	dir := t.TempDir()
	childPIDPath := filepath.Join(dir, "child-pid")
	script := `
/bin/sh -c 'trap "" TERM; printf "%s\n" "$$" >"$1"; while :; do sleep 1; done' fixture-child "$1" &
while [ ! -s "$1" ]; do sleep .01; done
exit 0
`
	cmd := exec.Command("/bin/sh", "-c", script, "fixture", childPIDPath)
	results := make(chan processGroupRunResult, 1)
	go func() { results <- defaultProcessGroupSupervisor().run(context.Background(), cmd) }()
	_ = fixturePID(t, childPIDPath)
	result := awaitOrdinaryExitDrainResult(t, cmd, results)
	assertOrdinaryExitDrainResult(t, cmd, result)
	assertProcessGroupAbsentAfterDrain(t, cmd.Process.Pid)
}

func awaitOrdinaryExitDrainResult(t *testing.T, cmd *exec.Cmd, results <-chan processGroupRunResult) processGroupRunResult {
	t.Helper()
	select {
	case result := <-results:
		return result
	case <-time.After(10 * time.Second):
		killRunningSupervisorTestCommand(cmd)
		t.Fatal("ordinary-exit process-group cleanup did not finish")
		return processGroupRunResult{}
	}
}

func assertOrdinaryExitDrainResult(t *testing.T, cmd *exec.Cmd, result processGroupRunResult) {
	t.Helper()
	if result.runErr != nil || result.cleanupErr != nil || result.cleanupRan || cmd.ProcessState == nil {
		t.Fatalf("ordinary exit result = %+v state=%v", result, cmd.ProcessState)
	}
}

func assertProcessGroupAbsentAfterDrain(t *testing.T, groupID int) {
	t.Helper()
	if absent, err := waitForProcessGroupExit(groupID, 2*time.Second); err != nil || !absent {
		t.Fatalf("ordinary-exit process group remained: absent=%t err=%v", absent, err)
	}
}

type supervisorWatchFailureCase struct {
	name      string
	configure func(*processGroupSupervisor, *supervisorTestWatcher)
	wantErr   error
	wantText  string
}

func TestProcessGroupSupervisorWatchFailures(t *testing.T) {
	sentinel := errors.New("supervision failed")
	tests := []supervisorWatchFailureCase{
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
		t.Run(tc.name, func(t *testing.T) { runSupervisorWatchFailureCase(t, tc) })
	}
}

func TestIsExactProcessGenerationCurrentFormats(t *testing.T) {
	tests := map[string]bool{
		"darwin:1.000001": true,
		"linux:1":         true,
		"1":               false,
		"darwin:1":        false,
		"linux:0":         false,
	}
	for generation, want := range tests {
		if got := IsExactProcessGeneration(generation); got != want {
			t.Errorf("IsExactProcessGeneration(%q) = %t, want %t", generation, got, want)
		}
	}
}

type supervisorSignalRecorder struct {
	cmd               *exec.Cmd
	signals           []syscall.Signal
	signaledAfterReap bool
}

func (r *supervisorSignalRecorder) signalGroup(groupID int, signal syscall.Signal) error {
	r.signaledAfterReap = r.signaledAfterReap || r.cmd.ProcessState != nil
	r.signals = append(r.signals, signal)
	return signalProcessGroup(groupID, signal)
}

func runSupervisorWatchFailureCase(t *testing.T, tc supervisorWatchFailureCase) {
	t.Helper()
	cmd := exec.Command("/bin/sleep", "2")
	watch := newSupervisorTestWatcher()
	s := defaultProcessGroupSupervisor()
	tc.configure(&s, watch)
	recorder := supervisorSignalRecorder{cmd: cmd}
	s.signalGroup = recorder.signalGroup
	result := s.run(context.Background(), cmd)
	unreaped := cmd.ProcessState == nil
	if unreaped {
		_ = cmd.Wait()
	}
	assertWatchFailureCleanupState(t, cmd, result, unreaped)
	assertWatchFailureSignals(t, result, recorder)
	combinedErr := errors.Join(result.runErr, result.cleanupErr)
	assertCleanupSkippedWait(t, result.cleanupErr)
	assertOptionalError(t, combinedErr, tc.wantErr)
	assertOptionalErrorText(t, combinedErr, tc.wantText)
	assertColimaCleanupFailure(t, result)
}

func assertWatchFailureCleanupState(t *testing.T, cmd *exec.Cmd, result processGroupRunResult, unreaped bool) {
	t.Helper()
	if result.cleanupErr == nil || !unreaped || cmd.ProcessState == nil {
		t.Fatalf("run result=%+v state=%v unreaped=%v", result, cmd.ProcessState, unreaped)
	}
}

func assertWatchFailureSignals(t *testing.T, result processGroupRunResult, recorder supervisorSignalRecorder) {
	t.Helper()
	if recorder.signaledAfterReap || len(recorder.signals) != 1 {
		t.Fatalf("run result=%+v signals=%v after-reap=%v", result, recorder.signals, recorder.signaledAfterReap)
	}
	if recorder.signals[0] != syscall.SIGKILL {
		t.Fatalf("run result=%+v signals=%v, want KILL", result, recorder.signals)
	}
}

func assertCleanupSkippedWait(t *testing.T, cleanupErr error) {
	t.Helper()
	if !strings.Contains(cleanupErr.Error(), "wait skipped") {
		t.Fatalf("cleanup error = %v, want wait skipped", cleanupErr)
	}
}

func assertOptionalError(t *testing.T, err, want error) {
	t.Helper()
	if want != nil && !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}

func assertOptionalErrorText(t *testing.T, err error, want string) {
	t.Helper()
	if want != "" && !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %v, want %q", err, want)
	}
}

func TestProcessGroupAbortClassificationRequiresUnprovedCleanup(t *testing.T) {
	supervisionErr := errors.New("supervision failed")
	cleanupErr := errors.New("absence proof failed")
	assertColimaOrdinaryFailure(t, processGroupAbortResultWithCause(nil, supervisionErr, nil, nil), supervisionErr)
	cleanupResult := processGroupAbortResultWithCause(nil, supervisionErr, nil, cleanupErr)
	assertColimaOrdinaryFailure(t, cleanupResult, supervisionErr)
	assertColimaOrdinaryFailure(t, cleanupResult, cleanupErr)
	assertColimaCleanupFailure(t, cleanupResult, supervisionErr, cleanupErr)
}

func assertColimaOrdinaryFailure(t *testing.T, result processGroupRunResult, want error) {
	t.Helper()
	code, err := colimaRunResult(result.runErr)
	if code != 0 || !errors.Is(err, want) {
		t.Fatalf("ordinary Colima result = %d, %v", code, err)
	}
}

func assertColimaCleanupFailure(t *testing.T, result processGroupRunResult, wants ...error) {
	t.Helper()
	code, err := colimaCancellationResult(result.runErr, result.cleanupErr, result.cause)
	if code != 0 || err == nil {
		t.Fatalf("cleanup-failed Colima result = %d, %v", code, err)
	}
	for _, want := range wants {
		if !errors.Is(err, want) {
			t.Fatalf("cleanup-failed Colima result = %v, want %v", err, want)
		}
	}
}

type skippedWaitSupervisorCase struct {
	name      string
	directErr error
	groupErr  error
}

func TestProcessGroupSupervisorSkipsWaitWithoutObservedExit(t *testing.T) {
	directErr := errors.New("direct kill failed")
	groupErr := errors.New("group signal failed")
	tests := []skippedWaitSupervisorCase{
		{name: "both signals fail", directErr: directErr, groupErr: groupErr},
		{name: "direct signal fails", directErr: directErr},
		{name: "group signal fails", groupErr: groupErr},
		{name: "signals succeed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) { runSkippedWaitSupervisorCase(t, test) })
	}
}

func runSkippedWaitSupervisorCase(t *testing.T, test skippedWaitSupervisorCase) {
	t.Helper()
	cmd := exec.Command("/bin/sleep", "30")
	startSupervisorTestCommand(t, cmd)
	cleanupSupervisorTestCommand(t, cmd)
	s := defaultProcessGroupSupervisor()
	owner := mustCaptureSupervisorOwner(t, &s, cmd.Process)
	s.termGrace, s.proofLimit = 10*time.Millisecond, 10*time.Millisecond
	s.signalGroup = func(int, syscall.Signal) error { return test.groupErr }
	s.killProcess = func(*os.Process) error { return test.directErr }
	fallbackDone := make(chan struct{})
	killFallback := time.AfterFunc(2*time.Second, func() {
		_ = cmd.Process.Kill()
		close(fallbackDone)
	})
	waitErr, cleanupErr := s.stopStartedCommand(cmd, owner, newSupervisorTestWatcher(), false, nil, true)
	stopped := killFallback.Stop()
	waitForStoppedFallback(stopped, fallbackDone)
	assertUnconfirmedCleanupBounded(t, cmd, stopped, waitErr)
	assertOptionalError(t, cleanupErr, test.directErr)
	assertCleanupErrorText(t, cleanupErr, "wait skipped")
	assertCleanupErrorText(t, cleanupErr, "exit watch did not signal")
	assertOptionalError(t, cleanupErr, test.groupErr)
}

func cleanupSupervisorTestCommand(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
}

func mustCaptureSupervisorOwner(t *testing.T, s *processGroupSupervisor, process *os.Process) processGroupOwner {
	t.Helper()
	owner, err := s.captureOwner(process)
	if err != nil {
		t.Fatal(err)
	}
	return owner
}

func waitForStoppedFallback(stopped bool, fallbackDone <-chan struct{}) {
	if !stopped {
		<-fallbackDone
	}
}

func assertUnconfirmedCleanupBounded(t *testing.T, cmd *exec.Cmd, stopped bool, waitErr error) {
	t.Helper()
	if !stopped || waitErr != nil || cmd.ProcessState != nil {
		t.Fatalf("unconfirmed cleanup was not bounded: wait=%v state=%v", waitErr, cmd.ProcessState)
	}
}

func assertCleanupErrorText(t *testing.T, cleanupErr error, want string) {
	t.Helper()
	if cleanupErr == nil || !strings.Contains(cleanupErr.Error(), want) {
		t.Fatalf("cleanup error = %v, want %q", cleanupErr, want)
	}
}

type supervisorEPERMCase struct {
	name      string
	signal    syscall.Signal
	absent    bool
	proofErr  error
	reuse     bool
	wantError bool
}

type supervisorEPERMFixture struct {
	cmd               *exec.Cmd
	test              supervisorEPERMCase
	signaledAfterReap bool
}

func (f *supervisorEPERMFixture) signalGroup(_ int, signal syscall.Signal) error {
	f.signaledAfterReap = f.signaledAfterReap || f.cmd.ProcessState != nil
	if signal == f.test.signal {
		return syscall.EPERM
	}
	return nil
}

func (f *supervisorEPERMFixture) observeGeneration(pid int, _ string) (string, error) {
	if f.test.reuse {
		return "darwin:reused", nil
	}
	return "", processGoneErr{pid: pid}
}

func (f *supervisorEPERMFixture) waitAbsent(int, time.Duration) (bool, error) {
	return f.test.absent, f.test.proofErr
}

func TestProcessGroupSupervisorSignalEPERMRequiresProof(t *testing.T) {
	proofErr := errors.New("absence proof failed")
	tests := []supervisorEPERMCase{
		{name: "TERM with absence proof", signal: syscall.SIGTERM, absent: true},
		{name: "TERM without absence proof", signal: syscall.SIGTERM, wantError: true},
		{name: "KILL with absence proof", signal: syscall.SIGKILL, absent: true},
		{name: "KILL with proof error", signal: syscall.SIGKILL, proofErr: proofErr, wantError: true},
		{name: "KILL with reused generation", signal: syscall.SIGKILL, reuse: true, wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) { runSupervisorEPERMCase(t, test) })
	}
}

func runSupervisorEPERMCase(t *testing.T, test supervisorEPERMCase) {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", "while :; do sleep 1; done")
	startSupervisorTestCommand(t, cmd)
	owner := processGroupOwner{pid: cmd.Process.Pid, generation: "darwin:test"}
	watch := newSupervisorTestWatcher()
	watch.signal(nil)
	fixture := supervisorEPERMFixture{cmd: cmd, test: test}
	s := defaultProcessGroupSupervisor()
	s.signalGroup = fixture.signalGroup
	s.killProcess = func(process *os.Process) error { return process.Kill() }
	s.observeGeneration = fixture.observeGeneration
	s.waitAbsent = fixture.waitAbsent
	_, cleanupErr := s.stopStartedCommand(cmd, owner, watch, true, nil, true)
	assertSupervisorStopOrdering(t, cmd, fixture.signaledAfterReap)
	assertExpectedCleanupError(t, cleanupErr, test.wantError, syscall.EPERM)
	assertOptionalError(t, cleanupErr, test.proofErr)
	assertReuseErrorText(t, cleanupErr, test.reuse)
}

func assertSupervisorStopOrdering(t *testing.T, cmd *exec.Cmd, signaledAfterReap bool) {
	t.Helper()
	if cmd.ProcessState == nil || signaledAfterReap {
		t.Fatalf("ordering: state=%v after-reap=%v", cmd.ProcessState, signaledAfterReap)
	}
}

func assertExpectedCleanupError(t *testing.T, err error, wantError bool, want error) {
	t.Helper()
	if (err != nil) != wantError {
		t.Fatalf("cleanup error = %v, want error=%v", err, wantError)
	}
	if !wantError {
		return
	}
	if !errors.Is(err, want) {
		t.Fatalf("cleanup error = %v, want %v", err, want)
	}
}

func assertReuseErrorText(t *testing.T, err error, reuse bool) {
	t.Helper()
	if reuse && !strings.Contains(err.Error(), "generation changed after wait") {
		t.Fatalf("cleanup error = %v, want generation change", err)
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

func TestProcessGroupSupervisorClassifiesPreStartCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/true")
	result := defaultProcessGroupSupervisor().runAfterStart(ctx, cmd, func() {
		t.Fatal("after-start hook ran after pre-start cancellation")
	})
	if !result.preStartCanceled || !errors.Is(result.runErr, context.Canceled) || !errors.Is(result.cause, context.Canceled) {
		t.Fatalf("pre-start cancellation result = %+v", result)
	}
}

func TestProcessGroupHelpersRejectUnsafeIDs(t *testing.T) {
	for _, groupID := range []int{-1, 0, 1, syscall.Getpgrp()} {
		assertUnsafeSignalGroupRejected(t, groupID)
		assertUnsafeProbeGroupRejected(t, groupID)
	}
}

func assertUnsafeSignalGroupRejected(t *testing.T, groupID int) {
	t.Helper()
	called := false
	err := signalProcessGroupWithKill(groupID, syscall.SIGTERM, func(int, syscall.Signal) error {
		called = true
		return nil
	})
	if err == nil || called {
		t.Fatalf("unsafe signal group %d: err=%v called=%v", groupID, err, called)
	}
}

func assertUnsafeProbeGroupRejected(t *testing.T, groupID int) {
	t.Helper()
	called := false
	absent, err := waitForProcessGroupExitWithProbe(groupID, 0,
		func(int, syscall.Signal) error {
			called = true
			return nil
		},
		time.Now, time.Sleep)
	if err == nil || absent || called {
		t.Fatalf("unsafe probe group %d: absent=%v err=%v called=%v", groupID, absent, err, called)
	}
}

func TestProcessGroupProbeTreatsEPERMAsPresent(t *testing.T) {
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
