// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

//go:build darwin || linux

package launcher

import (
	"errors"
	"os/exec"
	"testing"
	"time"
)

func startWatchedTestCommand(t *testing.T, cmd *exec.Cmd) processExitWatcher {
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	watch, err := startProcessExitWatch(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	return watch
}
func awaitTestExitWatch(t *testing.T, watch processExitWatcher) {
	t.Helper()
	select {
	case err := <-watch.done():
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("exit watch did not report")
	}
}
func TestProcessExitWatchDoesNotReap(t *testing.T) {
	for _, tc := range []struct {
		name, script string
		code         int
	}{{"zero", "exit 0", 0}, {"nonzero", "exit 7", 7}} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command("/bin/sh", "-c", tc.script)
			watch := startWatchedTestCommand(t, cmd)
			awaitTestExitWatch(t, watch)
			if cmd.ProcessState != nil {
				t.Fatal("exit watch reaped the child")
			}
			waitErr := cmd.Wait()
			var exitErr *exec.ExitError
			if tc.code == 0 && waitErr != nil ||
				tc.code != 0 && (!errors.As(waitErr, &exitErr) || exitErr.ExitCode() != tc.code) {
				t.Fatalf("Wait error = %v, want exit %d", waitErr, tc.code)
			}
		})
	}
}
func TestProcessExitWatchCloseDrainsWithoutReap(t *testing.T) {
	cmd := exec.Command("/bin/sleep", ".2")
	watch := startWatchedTestCommand(t, cmd)
	select {
	case err := <-watch.done():
		t.Fatalf("live watch completed: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	if err := watch.close(); err != nil {
		t.Fatal(err)
	}
	awaitTestExitWatch(t, watch)
	if cmd.ProcessState != nil {
		t.Fatal("closing the watch reaped the child")
	}
	_ = cmd.Wait()
}
