// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package applecontainer

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReadAuditLogHealsWriteOnly: a 0o200 (write-only) audit log is unreadable via the raw
// hardened reader, but readAuditLog transparently heals the mode to 0o600 and reads it.
// Skipped under root, which bypasses the mode bits so the unreadable precondition does not
// hold. Neutralize (readAuditLog = plain readFileSafe, no heal) → EACCES → FAIL.
func TestReadAuditLogHealsWriteOnly(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("write-only unreadable precondition does not hold for root")
	}
	root := t.TempDir()
	logPath := filepath.Join(root, "workcell.audit.log")
	mustNil(t, os.WriteFile(logPath, []byte("ts=1 session_id=sid event=session_started k=v\n"), 0o600))
	mustNil(t, os.Chmod(logPath, 0o200)) // write-only: read bit masked

	// The raw hardened reader fails on a write-only log.
	if _, err := readFileSafe(root, logPath, "audit log"); err == nil {
		t.Fatal("readFileSafe unexpectedly succeeded on a 0o200 log")
	}
	// readAuditLog heals the mode and reads.
	data, err := readAuditLog(root, logPath)
	mustNil(t, err)
	if !strings.Contains(string(data), "event=session_started") {
		t.Fatalf("healed read missing content:\n%s", data)
	}
	fi, err := os.Stat(logPath)
	mustNil(t, err)
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Fatalf("log mode = %o after heal, want 600", perm)
	}
}

// TestReadAuditLogPassesThroughNotExist: a missing log returns its error unchanged (not
// misclassified as a permission heal).
func TestReadAuditLogPassesThroughNotExist(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if _, err := readAuditLog(root, filepath.Join(root, "workcell.audit.log")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("want not-exist error, got: %v", err)
	}
}

// TestHealAuditLogModeDoesNotCreateParent (FIX 2): the read-path heal must NEVER create
// state — a missing parent component is an error, not a mkdir. Neutralize (use the
// create-parent opener) → the missing dir is created → FAIL.
func TestHealAuditLogModeDoesNotCreateParent(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	missing := filepath.Join(root, "missingdir")
	logPath := filepath.Join(missing, "workcell.audit.log")
	if err := healAuditLogModeForRead(root, logPath); err == nil {
		t.Fatal("heal succeeded with a missing parent component")
	}
	if _, serr := os.Stat(missing); !os.IsNotExist(serr) {
		t.Fatalf("read-path heal created the missing parent %q", missing)
	}
}
