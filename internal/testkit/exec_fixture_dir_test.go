// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package testkit

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// A non-ETXTBSY error (here, a missing executable) must return on the first
// attempt: retrying it would just be a 20x slower version of the same failure.
func TestExecRetryETXTBSYReturnsImmediatelyOnOtherErrors(t *testing.T) {
	calls := 0
	_, err := execRetryETXTBSY(func() *exec.Cmd {
		calls++
		return exec.Command(filepath.Join(t.TempDir(), "does-not-exist"))
	})
	if err == nil {
		t.Fatal("expected an error for a missing executable")
	}
	if calls != 1 {
		t.Fatalf("newCmd calls = %d, want 1 (no retry on a non-ETXTBSY error)", calls)
	}
}

// Reproduces the golang/go#22315 shape directly: a file held open for write is
// ETXTBSY to exec on Linux, and closing it while a retry loop is in flight
// must let the very next attempt succeed.
func TestExecRetryETXTBSYRetriesThroughTheTransientRace(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("ETXTBSY on a file open for write is Linux-specific (golang/go#22315)")
	}
	path := filepath.Join(t.TempDir(), "busy.sh")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("#!/bin/sh\nexit 0\n"); err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(30 * time.Millisecond)
		f.Close()
	}()
	if _, err := execRetryETXTBSY(func() *exec.Cmd { return exec.Command(path) }); err != nil {
		t.Fatalf("execRetryETXTBSY did not recover from the transient ETXTBSY race: %v", err)
	}
}
