// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package applecontainer

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// TestAppendAuditLineHealsTornBoundary (FIX A): a torn append leaves a newline-less
// fragment at EOF; the next clean append must land on its OWN line (a separator is
// prepended) rather than being glued onto the fragment into one merged physical line.
// Neutralize (drop the newline-heal) → the lines merge → the clean line is not on its
// own line → FAIL.
func TestAppendAuditLineHealsTornBoundary(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	logPath := filepath.Join(root, "workcell.audit.log")
	// A prior torn append: a complete-looking line with NO trailing newline.
	mustNil(t, os.WriteFile(logPath, []byte("ts=1 session_id=sid event=session_started workspace_control_plane=cp"), 0o600))
	clean := "ts=2 session_id=sid event=session_finished target_kind=k exit_status=0"
	mustNil(t, appendAuditLine(root, logPath, clean))
	data, err := os.ReadFile(logPath)
	mustNil(t, err)
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if lines[len(lines)-1] != clean {
		t.Fatalf("clean append did not land on its own line:\n%s", data)
	}
}

// TestAppendAuditLineHealsMidValueTear (FIX A variant): a fragment torn mid-value (no
// trailing newline) is likewise separated from the next clean append.
func TestAppendAuditLineHealsMidValueTear(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	logPath := filepath.Join(root, "workcell.audit.log")
	mustNil(t, os.WriteFile(logPath, []byte("ts=1 session_id=sid event=bootstrap_ready image_ref=img:"), 0o600))
	clean := "ts=2 session_id=sid event=session_started workspace_control_plane=cp"
	mustNil(t, appendAuditLine(root, logPath, clean))
	data, _ := os.ReadFile(logPath)
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if lines[len(lines)-1] != clean {
		t.Fatalf("clean append merged into a mid-value fragment:\n%s", data)
	}
	// A newline-terminated tail must NOT get an extra blank line prepended.
	mustNil(t, appendAuditLine(root, logPath, "ts=3 session_id=sid event=x k=v"))
	data2, _ := os.ReadFile(logPath)
	if strings.Contains(string(data2), "\n\n") {
		t.Fatalf("append after a clean newline inserted a spurious blank line:\n%s", data2)
	}
}

// TestAppendAuditLineConcurrentIntegrity: N goroutines append distinct complete lines to
// the SAME shared log concurrently. The flock serialization must make each tail-check +
// append atomic, so every input lands intact on its OWN physical line — no merged,
// interleaved, or lost lines. A pre-planted torn fragment (no trailing newline) must also
// be healed onto its own line. Neutralize (remove the Flock) → under -race a check/write
// interleave merges two inputs onto one line → the recovered set != inputs → FAIL.
func TestAppendAuditLineConcurrentIntegrity(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	logPath := filepath.Join(root, "workcell.audit.log")
	// Pre-plant a torn, newline-less fragment at EOF.
	mustNil(t, os.WriteFile(logPath, []byte("ts=0 session_id=sid event=session_started torn_fragment_no_newline"), 0o600))

	const n = 48
	inputs := make([]string, n)
	for i := range inputs {
		inputs[i] = fmt.Sprintf("ts=%d session_id=sid event=session_started target_id=tid workspace_control_plane=cp%03d", i+1, i+1)
	}
	var wg sync.WaitGroup
	errs := make([]error, n)
	start := make(chan struct{})
	wg.Add(n)
	for i := range inputs {
		go func(i int) {
			defer wg.Done()
			<-start // release all goroutines together to maximize contention on the torn tail
			errs[i] = appendAuditLine(root, logPath, inputs[i])
		}(i)
	}
	close(start)
	wg.Wait()
	for _, e := range errs {
		mustNil(t, e)
	}

	data, err := os.ReadFile(logPath)
	mustNil(t, err)
	// Split on '\n' and drop only the single trailing empty (from the final newline). The
	// result must be EXACTLY the healed fragment + the N inputs, in some order — no merged
	// lines (fewer), no lost lines, and no spurious BLANK lines. Under the flock, exactly
	// one appender heals the torn tail; without it, several concurrently see the torn tail
	// and each prepends a separator, injecting blank lines (and the check/write critical
	// section is no longer atomic). Any blank line here is a raced, unserialized heal.
	got := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	want := append([]string{"ts=0 session_id=sid event=session_started torn_fragment_no_newline"}, inputs...)
	sort.Strings(got)
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("got %d physical lines, want %d — a merge, loss, or raced blank-line heal occurred:\n%s", len(got), len(want), data)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("line %d = %q, want %q (merged/interleaved/blank append):\n%s", i, got[i], want[i], data)
		}
	}
}

// TestAppendAuditLineRecoversWriteOnlyLog: an existing log with owner-write but NO
// owner-read (mode 0o200 — a crash after O_CREAT under a read-masking umask, before the
// Fchmod heal) must still be appendable: appendAuditLine opens O_WRONLY first, heals the
// mode to 0o600, then re-opens O_RDWR. Neutralize (revert to the plain O_RDWR-first open)
// → EACCES on the missing read bit → the append fails and the log is unrecoverable → FAIL.
func TestAppendAuditLineRecoversWriteOnlyLog(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	logPath := filepath.Join(root, "workcell.audit.log")
	mustNil(t, os.WriteFile(logPath, []byte("ts=0 session_id=sid event=session_started k=v\n"), 0o600))
	mustNil(t, os.Chmod(logPath, 0o200)) // write-only, no owner read
	line := "ts=1 session_id=sid event=session_finished exit_status=0"
	if err := appendAuditLine(root, logPath, line); err != nil {
		t.Fatalf("append to a write-only (0o200) log failed: %v", err)
	}
	// The mode is healed to 0o600 and the line landed.
	fi, err := os.Stat(logPath)
	mustNil(t, err)
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Fatalf("write-only log mode = %o, want 600 (not healed)", perm)
	}
	data, err := os.ReadFile(logPath)
	mustNil(t, err)
	if !strings.Contains(string(data), line) {
		t.Fatalf("appended line did not land:\n%s", data)
	}
}

// TestAppendAuditLineDoesNotHangOnContendedLock: the append lock is NON-blocking with a
// bounded retry, so a foreign holder of the advisory lock on the shared log cannot hang
// appendAuditLine (a DoS). With an external LOCK_EX held, the call fails closed
// ("contended") within its bounded window; once released, a later append succeeds.
// Neutralize (blocking LOCK_EX) → the call hangs past the select timeout → FAIL.
func TestAppendAuditLineDoesNotHangOnContendedLock(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	logPath := filepath.Join(root, "workcell.audit.log")
	mustNil(t, os.WriteFile(logPath, []byte("ts=0 session_id=sid event=x k=v\n"), 0o600))
	// Hold an exclusive advisory lock on the log, as a foreign same-UID process would.
	holder, err := unix.Open(logPath, unix.O_RDWR, 0)
	mustNil(t, err)
	mustNil(t, unix.Flock(holder, unix.LOCK_EX))

	done := make(chan error, 1)
	go func() { done <- appendAuditLine(root, logPath, "ts=1 session_id=sid event=y k=v") }()
	select {
	case e := <-done:
		if e == nil || !strings.Contains(e.Error(), "contended") {
			t.Fatalf("expected contended-lock error, got: %v", e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("appendAuditLine hung on a foreign-held advisory lock (blocking flock?)")
	}

	// Release the foreign lock; a subsequent append must now succeed.
	mustNil(t, unix.Flock(holder, unix.LOCK_UN))
	mustNil(t, unix.Close(holder))
	if e := appendAuditLine(root, logPath, "ts=2 session_id=sid event=z k=v"); e != nil {
		t.Fatalf("append after lock release failed: %v", e)
	}
}
