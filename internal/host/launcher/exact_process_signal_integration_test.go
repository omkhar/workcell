// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

//go:build darwin || linux

package launcher

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

const exactProcessSignalIntegrationEnvironment = "WORKCELL_RUN_EXACT_PROCESS_SIGNAL_INTEGRATION"

func TestExactProcessSignalHandleOwnedChildIntegration(t *testing.T) {
	if os.Getenv(exactProcessSignalIntegrationEnvironment) != "1" {
		t.Skipf("set %s=1 to run owned-child exact-signal integration", exactProcessSignalIntegrationEnvironment)
	}
	if reason := exactProcessSignalIntegrationUnavailableReason(); reason != "" {
		t.Skip(reason)
	}
	for _, signal := range []syscall.Signal{syscall.SIGTERM, syscall.SIGKILL} {
		t.Run(signal.String(), func(t *testing.T) { testExactProcessSignalHandleOwnedChild(t, signal) })
	}
}

func testExactProcessSignalHandleOwnedChild(t *testing.T, signal syscall.Signal) {
	t.Helper()
	target := startExactSignalIntegrationChild(t)
	control := startExactSignalIntegrationChild(t)
	handle, err := openExactProcessSignalHandle(target.Process.Pid)
	if err != nil {
		t.Fatalf("open exact signal handle for owned child: %v", err)
	}
	if err := handle.Signal(signal); err != nil {
		_ = handle.Close()
		t.Fatalf("signal %v through exact handle: %v", signal, err)
	}
	if err := handle.Close(); err != nil {
		t.Fatalf("close exact signal handle: %v", err)
	}

	assertOwnedChildTerminated(t, target, signal)
	assertOwnedChildRunning(t, control)
}

func startExactSignalIntegrationChild(t *testing.T) *exec.Cmd {
	t.Helper()
	cmd := exec.Command("/bin/sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	return cmd
}

func assertOwnedChildRunning(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	state, err := processState(cmd.Process.Pid)
	if err != nil {
		t.Fatalf("read control child state: %v", err)
	}
	if isZombieProcessState(state) {
		t.Fatalf("control child state = %q, want running", state)
	}
}

func assertOwnedChildTerminated(t *testing.T, cmd *exec.Cmd, signal syscall.Signal) {
	t.Helper()
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	select {
	case err := <-waited:
		assertTerminatedBySignal(t, err, signal)
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		<-waited
		t.Fatalf("owned child did not stop after %v within 5s", signal)
	}
}

func assertTerminatedBySignal(t *testing.T, err error, signal syscall.Signal) {
	t.Helper()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("owned child wait error = %v, want signal exit", err)
	}
	status, ok := exitErr.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() || status.Signal() != signal {
		t.Fatalf("owned child status = %v, want signal %v", exitErr.ProcessState, signal)
	}
}
