// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package applecontainer

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/omkhar/workcell/internal/host/sessions"
)

// TestRecoveryAuditLineFieldValue: exact-token parsing of an event line's fields.
func TestRecoveryAuditLineFieldValue(t *testing.T) {
	t.Parallel()
	lines := []string{
		"ts=1 session_id=s event=workspace_materialized",
		"ts=1 session_id=s event=bootstrap_ready bootstrap_id=bid image_ref=img:1",
		"ts=1 session_id=s event=session_started",
	}
	if got := auditLineFieldValue(lines, "bootstrap_ready", "bootstrap_id"); got != "bid" {
		t.Fatalf("bootstrap_id = %q, want bid", got)
	}
	if got := auditLineFieldValue(lines, "bootstrap_ready", "image_ref"); got != "img:1" {
		t.Fatalf("image_ref = %q, want img:1", got)
	}
	if got := auditLineFieldValue(lines, "bootstrap_ready", "nope"); got != "" {
		t.Fatalf("missing field should be empty, got %q", got)
	}
}

// TestRecoveryStartTriplet: the triplet renders three ts-stamped start events.
func TestRecoveryStartTriplet(t *testing.T) {
	t.Parallel()
	target, err := NewAppleContainerTarget(Contract{})
	mustNil(t, err)
	req := StartSessionRequest{
		Materialization: MaterializeResult{Manifest: WorkspaceManifest{MaterializationID: "mid", SourceWorkspace: "/src"}},
		Bootstrap:       BootstrapResult{Manifest: BootstrapManifest{BootstrapID: "bid", ImageRef: "img:1"}},
	}
	lines := strings.Split(target.startTriplet("2026", "sid", "tid", "/ws", req), "\n")
	if len(lines) != 3 {
		t.Fatalf("want 3 lines, got %d", len(lines))
	}
	for _, l := range lines {
		if !strings.HasPrefix(l, "ts=2026 session_id=sid ") {
			t.Fatalf("line missing ts/session prefix: %q", l)
		}
	}
	if auditLineFieldValue(lines, "bootstrap_ready", "bootstrap_id") != "bid" {
		t.Fatalf("triplet bootstrap_id mismatch")
	}
}

// TestRecoveryStartEventLines: the per-event renderer yields the three start events
// in canonical order, each name matching the exact event= token on its line.
func TestRecoveryStartEventLines(t *testing.T) {
	t.Parallel()
	target, err := NewAppleContainerTarget(Contract{})
	mustNil(t, err)
	events := target.startEventLines("2026", "sid", "tid", "/ws", StartSessionRequest{})
	want := []string{"workspace_materialized", "bootstrap_ready", "session_started"}
	if len(events) != len(want) {
		t.Fatalf("want %d events, got %d", len(want), len(events))
	}
	for i, e := range events {
		if e.name != want[i] {
			t.Fatalf("event %d = %q, want %q", i, e.name, want[i])
		}
		if !auditHasEvents([]string{e.line}, e.name) {
			t.Fatalf("line for %q does not carry the exact event token: %q", e.name, e.line)
		}
	}
}

// TestRecoveryStartedRecordMatchesRequest: a record equal to what the request would
// write matches; a divergent field does not.
func TestRecoveryStartedRecordMatchesRequest(t *testing.T) {
	t.Parallel()
	target, err := NewAppleContainerTarget(Contract{})
	mustNil(t, err)
	c := target.Contract
	req := StartSessionRequest{Agent: "codex", Mode: "strict",
		Materialization: MaterializeResult{Manifest: WorkspaceManifest{SourceWorkspace: "/src"}}}
	rec := sessions.SessionRecord{
		Version:   1,      // every persisted record carries version 1; the exhaustive match requires it
		StartedAt: "2026", // required, non-empty; excluded from the match but must decode
		SessionID: "sid", Profile: "tid", TargetKind: c.TargetKind, TargetProvider: c.TargetProvider,
		TargetID: "tid", TargetAssuranceClass: c.TargetAssuranceClass, RuntimeAPI: c.RuntimeAPI,
		WorkspaceTransport: c.WorkspaceTransport, Agent: "codex", Mode: "strict", Status: c.Session.StartStatus,
		Workspace: "/ws", WorkspaceOrigin: "/src", WorkspaceRoot: "/ws", WorktreePath: "/ws",
		AuditLogPath: "/log", InitialAssurance: c.Session.Assurance, CurrentAssurance: c.Session.Assurance,
		WorkspaceControlPlane: c.Session.WorkspaceControlPlane,
	}
	if !target.startedRecordMatchesRequest(rec, req, "sid", "tid", "/ws", "/log") {
		t.Fatalf("matching record reported as divergent")
	}
	rec.Agent = "other"
	if target.startedRecordMatchesRequest(rec, req, "sid", "tid", "/ws", "/log") {
		t.Fatalf("divergent agent reported as matching")
	}
	// Exhaustiveness: a record matching every enumerated field but carrying an EXTRA
	// populated field the request never sets (here container_name) is NOT a match.
	rec.Agent = "codex"
	rec.ContainerName = "unexpected"
	if target.startedRecordMatchesRequest(rec, req, "sid", "tid", "/ws", "/log") {
		t.Fatalf("record with an extra populated field reported as matching")
	}
}

// TestRecoveryLockSession: acquire, release, and re-acquire the per-session lock.
func TestRecoveryLockSession(t *testing.T) {
	t.Parallel()
	stateRoot := t.TempDir()
	recordPath := filepath.Join(stateRoot, "targets", "local_vm", "apple-container", "tid", "sessions", "sid.json")
	unlock, err := lockSession(stateRoot, recordPath)
	mustNil(t, err)
	unlock()
	unlock2, err := lockSession(stateRoot, recordPath)
	mustNil(t, err)
	unlock2()
}

// TestRecoveryLockSessionModeUmaskIndependent: the per-session .lock file lands at
// 0o600 even under a restrictive umask that would mask O_CREAT's mode. Non-parallel
// (process-global umask), defer-restored; the sessions dir is created under the
// normal umask first (like the io staged-record umask test) so only the lock file's
// fresh create is subject to the restrictive umask. Neutralize (drop the Fchmod) →
// the lock file is created with masked (mode-0) bits → FAIL.
func TestRecoveryLockSessionModeUmaskIndependent(t *testing.T) {
	stateRoot := t.TempDir()
	recordPath := filepath.Join(stateRoot, "targets", "local_vm", "apple-container", "tid", "sessions", "sid.json")
	mustNil(t, os.MkdirAll(filepath.Dir(recordPath), 0o700)) // dirs under the normal umask
	old := syscall.Umask(0o777)                              // would mask the lock create to mode 0
	defer syscall.Umask(old)
	unlock, err := lockSession(stateRoot, recordPath)
	mustNil(t, err)
	defer unlock()
	lockPath := filepath.Join(filepath.Dir(recordPath), "sid.lock")
	fi, err := os.Stat(lockPath)
	mustNil(t, err)
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Fatalf("session lock mode = %o, want 600 (umask leaked into the lock file)", perm)
	}
}
