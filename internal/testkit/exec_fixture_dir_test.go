// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package testkit

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
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

// Reproduces the golang/go#22315 shape directly and deterministically: a file
// held open for write is ETXTBSY to exec on Linux, with no timing dependence
// (the kernel checks this unconditionally, not as a race), so gating the
// close on newCmd's own call count - rather than a sleep - guarantees the
// first attempt sees ETXTBSY and the second sees a closed, executable file.
// A goroutine + sleep would not: under a loaded scheduler the closer could
// run before the first attempt ever executes, and the test would pass
// without ever exercising the retry path.
func TestExecRetryETXTBSYRetriesThroughTheTransientRace(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("ETXTBSY on a file open for write is Linux-specific (golang/go#22315)")
	}
	// ExecFixtureDir, not t.TempDir(): TMPDIR can be a noexec mount (a layout
	// this package's own hostile-env axis exercises elsewhere), which would
	// fail this exec with EACCES instead of proving ETXTBSY recovery.
	path := filepath.Join(ExecFixtureDir(t), "busy.sh")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }() // safety net if attempts never reaches 2 below
	if _, err := f.WriteString("#!/bin/sh\nexit 0\n"); err != nil {
		t.Fatal(err)
	}
	attempts := 0
	_, err = execRetryETXTBSY(func() *exec.Cmd {
		attempts++
		// Close only once the first attempt's exec has already been issued
		// against this exact newCmd call - the loop always calls newCmd
		// immediately before running it, so by the second call the first
		// attempt is guaranteed to have already seen the file open.
		if attempts == 2 {
			if closeErr := f.Close(); closeErr != nil {
				t.Fatal(closeErr)
			}
		}
		return exec.Command(path)
	})
	if err != nil {
		t.Fatalf("execRetryETXTBSY did not recover from the transient ETXTBSY race: %v", err)
	}
	if attempts < 2 {
		t.Fatalf("attempts = %d, want >1: the file was never held open through a real attempt, so this proves nothing about the retry path", attempts)
	}
}

// execRetryETXTBSYOrNestedBusy's extra shape (exit 126 with "text file busy"
// on stderr, the way bash reports a nested exec hitting the same
// golang/go#22315 race) is deterministic to reproduce without a real kernel
// race: a fixture script that reports that exact shape on its first run and
// succeeds afterward.
func TestExecRetryETXTBSYOrNestedBusyRetriesOnlyOnTheBusyMessage(t *testing.T) {
	dir := ExecFixtureDir(t)

	busyThenOK := filepath.Join(dir, "busy-then-ok.sh")
	writeExecFile(t, busyThenOK, []byte(`#!/bin/sh
if [ ! -e "$1" ]; then
  : >"$1"
  echo "bash: ./verify-github-hosted-controls.sh: Text file busy" >&2
  exit 126
fi
exit 0
`), 0o755)
	marker := filepath.Join(t.TempDir(), "ran-once")
	if _, err := execRetryETXTBSYOrNestedBusy(func() *exec.Cmd { return exec.Command(busyThenOK, marker) }); err != nil {
		t.Fatalf("execRetryETXTBSYOrNestedBusy did not retry past the nested-busy shape: %v", err)
	}

	unrelated126 := filepath.Join(dir, "unrelated-126.sh")
	writeExecFile(t, unrelated126, []byte("#!/bin/sh\necho permission denied >&2\nexit 126\n"), 0o755)
	calls := 0
	_, err := execRetryETXTBSYOrNestedBusy(func() *exec.Cmd {
		calls++
		return exec.Command(unrelated126)
	})
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 126 {
		t.Fatalf("expected an unretried exit 126, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1: an exit 126 without the busy message must not be retried", calls)
	}
}
