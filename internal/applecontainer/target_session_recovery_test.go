// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package applecontainer

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omkhar/workcell/internal/host/sessions"
)

// TestStartSessionRejectsAuditLogOutsideTargetRoot: an AuditLogPath outside the
// target root is rejected, so the log cannot be written outside the state tree.
func TestStartSessionRejectsAuditLogOutsideTargetRoot(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	target, err := NewAppleContainerTarget(Contract{})
	mustNil(t, err)
	source := writeSampleWorkspace(t)
	root := t.TempDir()
	mat, err := target.MaterializeWorkspace(ctx, MaterializeRequest{StateRoot: root, TargetID: "tid", MaterializationID: "mid", SourceWorkspace: source})
	mustNil(t, err)
	boot, err := target.BootstrapTarget(ctx, BootstrapRequest{StateRoot: root, TargetID: "tid", BootstrapID: "bid", ImageRef: "img:1"})
	mustNil(t, err)
	boot.AuditLogPath = filepath.Join(t.TempDir(), "outside.log") // outside TargetRoot
	if _, e := target.StartSession(ctx, StartSessionRequest{SessionID: "sid", Agent: "codex", Mode: "strict", StartedAt: "2026", Materialization: mat, Bootstrap: boot}); e == nil {
		t.Fatalf("start accepted an audit log outside the target root")
	}
}

// TestStartSessionRejectsAuditLogAtRecordPath: an AuditLogPath aimed at the
// session record (under the target root, so containment passes) is rejected and
// the record is not created or corrupted.
func TestStartSessionRejectsAuditLogAtRecordPath(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	target, err := NewAppleContainerTarget(Contract{})
	mustNil(t, err)
	source := writeSampleWorkspace(t)
	root := t.TempDir()
	mat, err := target.MaterializeWorkspace(ctx, MaterializeRequest{StateRoot: root, TargetID: "tid", MaterializationID: "mid", SourceWorkspace: source})
	mustNil(t, err)
	boot, err := target.BootstrapTarget(ctx, BootstrapRequest{StateRoot: root, TargetID: "tid", BootstrapID: "bid", ImageRef: "img:1"})
	mustNil(t, err)

	recordPath := filepath.Join(boot.TargetRoot, "sessions", "sid.json")
	boot.AuditLogPath = recordPath // a target-managed file, but not the constructed log
	if _, e := target.StartSession(ctx, StartSessionRequest{SessionID: "sid", Agent: "codex", Mode: "strict", StartedAt: "2026", Materialization: mat, Bootstrap: boot}); e == nil {
		t.Fatalf("start accepted an audit log pointed at the session record")
	}
	if _, err := os.Stat(recordPath); !os.IsNotExist(err) {
		t.Fatalf("session record was created/corrupted despite the rejection")
	}
}

// startedForFinishTest runs a full StartSession and returns the target and the
// SessionResult for exercising FinishSession's trust-of-persisted-fields.
func startedForFinishTest(t *testing.T) (AppleContainerTarget, SessionResult) {
	t.Helper()
	ctx := context.Background()
	target, err := NewAppleContainerTarget(Contract{})
	mustNil(t, err)
	state := t.TempDir()
	mat, err := target.MaterializeWorkspace(ctx, MaterializeRequest{StateRoot: state, TargetID: "tid", MaterializationID: "mid", SourceWorkspace: writeSampleWorkspace(t)})
	mustNil(t, err)
	boot, err := target.BootstrapTarget(ctx, BootstrapRequest{StateRoot: state, TargetID: "tid", BootstrapID: "bid", ImageRef: "img:1"})
	mustNil(t, err)
	started, err := target.StartSession(ctx, StartSessionRequest{SessionID: "sid", Agent: "codex", Mode: "strict", StartedAt: "2026", Materialization: mat, Bootstrap: boot})
	mustNil(t, err)
	return target, started
}

// tamperRecordField rewrites one field of the persisted record in place (same
// inode) to simulate an attacker editing mutable stored state between start and
// finish.
func tamperRecordField(t *testing.T, recordPath, field, value string) {
	t.Helper()
	tamperRecordFields(t, recordPath, map[string]string{field: value})
}

func tamperRecordFields(t *testing.T, recordPath string, fields map[string]string) {
	t.Helper()
	data, err := os.ReadFile(recordPath)
	mustNil(t, err)
	var m map[string]any
	mustNil(t, json.Unmarshal(data, &m))
	for k, v := range fields {
		m[k] = v
	}
	out, err := json.Marshal(m)
	mustNil(t, err)
	mustNil(t, os.WriteFile(recordPath, out, 0o600))
}

// TestFinishSessionRederivesCanonicalAuditLog: a tampered persisted audit_log_path
// must NOT redirect the finish event — it is re-derived from TargetRoot, so the
// finish lands in the canonical log and the tampered target is untouched.
func TestFinishSessionRederivesCanonicalAuditLog(t *testing.T) {
	t.Parallel()
	target, started := startedForFinishTest(t)
	targetRoot := filepath.Dir(filepath.Dir(started.RecordPath))
	canonical := filepath.Join(targetRoot, "workcell.audit.log")
	// A target-managed decoy log seeded with this session's start events, so even a
	// trust-the-field finalize would pass the start-events check and append there.
	decoy := filepath.Join(targetRoot, "decoy.audit.log")
	content, err := os.ReadFile(canonical)
	mustNil(t, err)
	mustNil(t, os.WriteFile(decoy, content, 0o600))
	tamperRecordField(t, started.RecordPath, "audit_log_path", decoy)

	if _, e := target.FinishSession(context.Background(), FinishSessionRequest{Started: started, FinishedAt: "2027", ExitStatus: "0"}); e != nil {
		t.Fatalf("FinishSession failed: %v", e)
	}
	if cd, _ := os.ReadFile(canonical); !strings.Contains(string(cd), "event=session_finished") {
		t.Fatalf("finish event not written to the canonical log (audit_log_path trusted?)")
	}
	if dd, _ := os.ReadFile(decoy); strings.Contains(string(dd), "event=session_finished") {
		t.Fatalf("finish event redirected to the tampered audit_log_path")
	}
}

// TestFinishSessionHealsTamperedAuditLogPath: a tampered persisted audit_log_path
// is HEALED to the canonical path in the FINALIZED record, so the exported record
// no longer carries the tampered value.
func TestFinishSessionHealsTamperedAuditLogPath(t *testing.T) {
	t.Parallel()
	target, started := startedForFinishTest(t)
	targetRoot := filepath.Dir(filepath.Dir(started.RecordPath))
	canonical := filepath.Join(targetRoot, "workcell.audit.log")
	decoy := filepath.Join(targetRoot, "decoy.audit.log")
	content, err := os.ReadFile(canonical)
	mustNil(t, err)
	mustNil(t, os.WriteFile(decoy, content, 0o600)) // seed so the finalize can proceed
	tamperRecordField(t, started.RecordPath, "audit_log_path", decoy)

	if _, e := target.FinishSession(context.Background(), FinishSessionRequest{Started: started, FinishedAt: "2027", ExitStatus: "0"}); e != nil {
		t.Fatalf("FinishSession failed: %v", e)
	}
	rec, err := sessions.ReadSessionRecord(started.RecordPath)
	mustNil(t, err)
	if rec.AuditLogPath != canonical {
		t.Fatalf("finalized record audit_log_path = %q, want canonical %q (not healed?)", rec.AuditLogPath, canonical)
	}
}

// TestFinishSessionRejectsTamperedSessionID: a persisted session_id that no longer
// matches the record's own path is refused, not finalized.
func TestFinishSessionRejectsTamperedSessionID(t *testing.T) {
	t.Parallel()
	target, started := startedForFinishTest(t)
	tamperRecordField(t, started.RecordPath, "session_id", "other")
	if _, e := target.FinishSession(context.Background(), FinishSessionRequest{Started: started, FinishedAt: "2027", ExitStatus: "0"}); e == nil {
		t.Fatalf("FinishSession finalized a record whose session_id was tampered")
	}
}

// TestFinishSessionRejectsTamperedTargetID: a persisted target_id that no longer
// matches the canonical TargetRoot leaf is refused.
func TestFinishSessionRejectsTamperedTargetID(t *testing.T) {
	t.Parallel()
	target, started := startedForFinishTest(t)
	tamperRecordField(t, started.RecordPath, "target_id", "other")
	if _, e := target.FinishSession(context.Background(), FinishSessionRequest{Started: started, FinishedAt: "2027", ExitStatus: "0"}); e == nil {
		t.Fatalf("FinishSession finalized a record whose target_id was tampered")
	}
}

// TestFinishSessionRejectsForeignTarget: a persisted record whose target
// kind/provider belong to ANOTHER target is refused — an apple-container
// FinishSession must not finalize a foreign target's record.
func TestFinishSessionRejectsForeignTarget(t *testing.T) {
	t.Parallel()
	target, started := startedForFinishTest(t)
	tamperRecordFields(t, started.RecordPath, map[string]string{
		"target_kind":     "remote_vm",
		"target_provider": "fake-remote",
	})
	if _, e := target.FinishSession(context.Background(), FinishSessionRequest{Started: started, FinishedAt: "2027", ExitStatus: "0"}); e == nil {
		t.Fatalf("FinishSession finalized a record from another target kind/provider")
	}
}

// TestFinishSessionRejectsTerminalNonExitedStatus: a record already in a terminal
// status other than the final "exited" (e.g. "failed") must not be re-finalized —
// the exactly-once guard checks the full terminal set, not just FinalStatus. The
// tampered record is a VALID terminal "failed" record (finished_at/exit_status/
// final_assurance set) so it passes the read; only the guard can reject it.
func TestFinishSessionRejectsTerminalNonExitedStatus(t *testing.T) {
	t.Parallel()
	target, started := startedForFinishTest(t)
	// A VALID terminal "failed" record whose exit_status MATCHES the retry, so only
	// the status-equality gate (failed != contract's exited) can reject it — not the
	// exit_status check.
	tamperRecordFields(t, started.RecordPath, map[string]string{
		"status":          "failed",
		"finished_at":     "2026",
		"exit_status":     "0",
		"final_assurance": DefaultContract().Session.Assurance,
	})
	_, e := target.FinishSession(context.Background(), FinishSessionRequest{Started: started, FinishedAt: "2027", ExitStatus: "0"})
	if e == nil || !strings.Contains(e.Error(), "different terminal status") {
		t.Fatalf("expected different-terminal-status rejection for a failed record, got: %v", e)
	}
}

// TestStartSessionRejectsTamperedManifestContractField: a self-consistent manifest
// pair (persisted AND Result both edited to agree) with a wrong contract-sourced
// field passes the byte-compare but is refused by the contract-field pin.
func TestStartSessionRejectsTamperedManifestContractField(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	target, err := NewAppleContainerTarget(Contract{})
	mustNil(t, err)
	state := t.TempDir()
	mat, err := target.MaterializeWorkspace(ctx, MaterializeRequest{StateRoot: state, TargetID: "tid", MaterializationID: "mid", SourceWorkspace: writeSampleWorkspace(t)})
	mustNil(t, err)
	boot, err := target.BootstrapTarget(ctx, BootstrapRequest{StateRoot: state, TargetID: "tid", BootstrapID: "bid", ImageRef: "img:1"})
	mustNil(t, err)

	// Tamper the materialization manifest's target_provider in BOTH the Result and
	// on disk (writeJSON format = MarshalIndent + "\n") so the byte-compare matches.
	mat.Manifest.TargetProvider = "evil-provider"
	data, err := json.MarshalIndent(mat.Manifest, "", "  ")
	mustNil(t, err)
	mustNil(t, os.WriteFile(mat.ManifestPath, append(data, '\n'), 0o600))

	if _, e := target.StartSession(ctx, StartSessionRequest{SessionID: "sid", Agent: "codex", Mode: "strict", StartedAt: "2026", Materialization: mat, Bootstrap: boot}); e == nil {
		t.Fatalf("StartSession certified a materialization manifest with a tampered contract field")
	}
}

// TestStartSessionRejectsTamperedBootstrapContractField: same for the bootstrap
// manifest's access_model.
func TestStartSessionRejectsTamperedBootstrapContractField(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	target, err := NewAppleContainerTarget(Contract{})
	mustNil(t, err)
	state := t.TempDir()
	mat, err := target.MaterializeWorkspace(ctx, MaterializeRequest{StateRoot: state, TargetID: "tid", MaterializationID: "mid", SourceWorkspace: writeSampleWorkspace(t)})
	mustNil(t, err)
	boot, err := target.BootstrapTarget(ctx, BootstrapRequest{StateRoot: state, TargetID: "tid", BootstrapID: "bid", ImageRef: "img:1"})
	mustNil(t, err)

	boot.Manifest.AccessModel = "evil-access"
	data, err := json.MarshalIndent(boot.Manifest, "", "  ")
	mustNil(t, err)
	mustNil(t, os.WriteFile(boot.ManifestPath, append(data, '\n'), 0o600))

	if _, e := target.StartSession(ctx, StartSessionRequest{SessionID: "sid", Agent: "codex", Mode: "strict", StartedAt: "2026", Materialization: mat, Bootstrap: boot}); e == nil {
		t.Fatalf("StartSession certified a bootstrap manifest with a tampered contract field")
	}
}

// TestFinishSessionRejectsNonCanonicalRecordPath: a RecordPath whose derived
// targetRoot is not the canonical targets/<kind>/<provider>/<id> layout is
// rejected BEFORE any read (the canonical check StartSession also applies).
func TestFinishSessionRejectsNonCanonicalRecordPath(t *testing.T) {
	t.Parallel()
	target, started := startedForFinishTest(t)
	bad := started
	bad.RecordPath = filepath.Join(t.TempDir(), "evil", "sessions", "sid.json")
	_, e := target.FinishSession(context.Background(), FinishSessionRequest{Started: bad, FinishedAt: "2027", ExitStatus: "0"})
	if e == nil || !strings.Contains(e.Error(), "is not the canonical") {
		t.Fatalf("expected canonical-layout rejection, got: %v", e)
	}
}

// TestFinishSessionRejectsWhitespaceSessionID: a session id derived from a record
// filename containing whitespace is token-validated (rejected) before it could
// enter the finish audit line.
func TestFinishSessionRejectsWhitespaceSessionID(t *testing.T) {
	t.Parallel()
	target, started := startedForFinishTest(t)
	targetRoot := filepath.Dir(filepath.Dir(started.RecordPath))
	data, err := os.ReadFile(started.RecordPath)
	mustNil(t, err)
	var m map[string]any
	mustNil(t, json.Unmarshal(data, &m))
	m["session_id"] = "s id" // a space, so it matches the whitespace filename below
	out, err := json.Marshal(m)
	mustNil(t, err)
	badPath := filepath.Join(targetRoot, "sessions", "s id.json")
	mustNil(t, os.WriteFile(badPath, out, 0o600))
	bad := started
	bad.RecordPath = badPath
	_, e := target.FinishSession(context.Background(), FinishSessionRequest{Started: bad, FinishedAt: "2027", ExitStatus: "0"})
	if e == nil || !strings.Contains(e.Error(), "session id must not contain whitespace") {
		t.Fatalf("expected session-id whitespace rejection, got: %v", e)
	}
}

// TestStartSessionRejectsTamperedManifestExcludedPaths: a self-consistent
// workspace-manifest pair (persisted AND Result both edited to agree) with a
// tampered excluded_paths passes the byte-compare but is refused by the completed
// contract-field pin.
func TestStartSessionRejectsTamperedManifestExcludedPaths(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	target, err := NewAppleContainerTarget(Contract{})
	mustNil(t, err)
	state := t.TempDir()
	mat, err := target.MaterializeWorkspace(ctx, MaterializeRequest{StateRoot: state, TargetID: "tid", MaterializationID: "mid", SourceWorkspace: writeSampleWorkspace(t)})
	mustNil(t, err)
	boot, err := target.BootstrapTarget(ctx, BootstrapRequest{StateRoot: state, TargetID: "tid", BootstrapID: "bid", ImageRef: "img:1"})
	mustNil(t, err)

	mat.Manifest.ExcludedPaths = []string{"evil"}
	data, err := json.MarshalIndent(mat.Manifest, "", "  ")
	mustNil(t, err)
	mustNil(t, os.WriteFile(mat.ManifestPath, append(data, '\n'), 0o600))

	if _, e := target.StartSession(ctx, StartSessionRequest{SessionID: "sid", Agent: "codex", Mode: "strict", StartedAt: "2026", Materialization: mat, Bootstrap: boot}); e == nil {
		t.Fatalf("StartSession certified a manifest with tampered excluded_paths")
	}
}

// TestStartSessionAcceptsTrailingSlashTargetRoot: a canonical TargetRoot differing
// only by a trailing slash (here on the bootstrap Result) is still accepted — both
// TargetRoots are Cleaned before comparison/derivation.
func TestStartSessionAcceptsTrailingSlashTargetRoot(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	target, err := NewAppleContainerTarget(Contract{})
	mustNil(t, err)
	state := t.TempDir()
	mat, err := target.MaterializeWorkspace(ctx, MaterializeRequest{StateRoot: state, TargetID: "tid", MaterializationID: "mid", SourceWorkspace: writeSampleWorkspace(t)})
	mustNil(t, err)
	boot, err := target.BootstrapTarget(ctx, BootstrapRequest{StateRoot: state, TargetID: "tid", BootstrapID: "bid", ImageRef: "img:1"})
	mustNil(t, err)
	boot.TargetRoot += "/" // trailing slash on one Result only
	if _, e := target.StartSession(ctx, StartSessionRequest{SessionID: "sid", Agent: "codex", Mode: "strict", StartedAt: "2026", Materialization: mat, Bootstrap: boot}); e != nil {
		t.Fatalf("StartSession rejected a canonical target root with a trailing slash: %v", e)
	}
}

// TestStartSessionRejectsDotDotTargetRoot: a `..`-bearing TargetRoot that Cleans to
// a non-canonical layout is still rejected — normalization does not create a bypass.
func TestStartSessionRejectsDotDotTargetRoot(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	target, err := NewAppleContainerTarget(Contract{})
	mustNil(t, err)
	state := t.TempDir()
	mat, err := target.MaterializeWorkspace(ctx, MaterializeRequest{StateRoot: state, TargetID: "tid", MaterializationID: "mid", SourceWorkspace: writeSampleWorkspace(t)})
	mustNil(t, err)
	boot, err := target.BootstrapTarget(ctx, BootstrapRequest{StateRoot: state, TargetID: "tid", BootstrapID: "bid", ImageRef: "img:1"})
	mustNil(t, err)
	mat.TargetRoot += "/../evil"
	boot.TargetRoot += "/../evil"
	if _, e := target.StartSession(ctx, StartSessionRequest{SessionID: "sid", Agent: "codex", Mode: "strict", StartedAt: "2026", Materialization: mat, Bootstrap: boot}); e == nil {
		t.Fatalf("StartSession accepted a ..-bearing (non-canonical after Clean) target root")
	}
}

// TestFinishSessionRejectsRecordPathOutsideSessions: a RecordPath under the SAME
// canonical target root but OUTSIDE sessions/ (here a sibling dir) is rejected —
// the record path is pinned to exactly <targetRoot>/sessions/<id>.json.
func TestFinishSessionRejectsRecordPathOutsideSessions(t *testing.T) {
	t.Parallel()
	target, started := startedForFinishTest(t)
	targetRoot := filepath.Dir(filepath.Dir(started.RecordPath))
	// A valid record copied to a sibling of sessions/, so the target-root canonical
	// check passes and only the sessions/ pin can reject it.
	data, err := os.ReadFile(started.RecordPath)
	mustNil(t, err)
	outside := filepath.Join(targetRoot, "notsessions")
	mustNil(t, os.MkdirAll(outside, 0o755))
	badPath := filepath.Join(outside, "sid.json")
	mustNil(t, os.WriteFile(badPath, data, 0o600))
	bad := started
	bad.RecordPath = badPath
	if _, e := target.FinishSession(context.Background(), FinishSessionRequest{Started: bad, FinishedAt: "2027", ExitStatus: "0"}); e == nil {
		t.Fatalf("FinishSession finalized a record outside the sessions directory")
	}
}

// TestStartSessionAcceptsDotAuditLogPath: a canonical audit-log path spelled with a
// trailing "/." normalizes and is accepted (the guard Cleans before comparing).
func TestStartSessionAcceptsDotAuditLogPath(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	target, err := NewAppleContainerTarget(Contract{})
	mustNil(t, err)
	state := t.TempDir()
	mat, err := target.MaterializeWorkspace(ctx, MaterializeRequest{StateRoot: state, TargetID: "tid", MaterializationID: "mid", SourceWorkspace: writeSampleWorkspace(t)})
	mustNil(t, err)
	boot, err := target.BootstrapTarget(ctx, BootstrapRequest{StateRoot: state, TargetID: "tid", BootstrapID: "bid", ImageRef: "img:1"})
	mustNil(t, err)
	boot.AuditLogPath += "/." // same canonical path, different spelling
	if _, e := target.StartSession(ctx, StartSessionRequest{SessionID: "sid", Agent: "codex", Mode: "strict", StartedAt: "2026", Materialization: mat, Bootstrap: boot}); e != nil {
		t.Fatalf("StartSession rejected a canonical audit log path spelled with /.: %v", e)
	}
}

// TestStartSessionAcceptsDotManifestWorkspace: the manifest's materialized_workspace
// spelled with a trailing "/." (canonical after Clean, on-disk rewritten to match)
// is accepted — the field is Cleaned and pinned to the canonical workspace.
func TestStartSessionAcceptsDotManifestWorkspace(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	target, err := NewAppleContainerTarget(Contract{})
	mustNil(t, err)
	state := t.TempDir()
	mat, err := target.MaterializeWorkspace(ctx, MaterializeRequest{StateRoot: state, TargetID: "tid", MaterializationID: "mid", SourceWorkspace: writeSampleWorkspace(t)})
	mustNil(t, err)
	boot, err := target.BootstrapTarget(ctx, BootstrapRequest{StateRoot: state, TargetID: "tid", BootstrapID: "bid", ImageRef: "img:1"})
	mustNil(t, err)
	mat.Manifest.MaterializedWorkspace += "/."
	data, err := json.MarshalIndent(mat.Manifest, "", "  ")
	mustNil(t, err)
	mustNil(t, os.WriteFile(mat.ManifestPath, append(data, '\n'), 0o600))
	if _, e := target.StartSession(ctx, StartSessionRequest{SessionID: "sid", Agent: "codex", Mode: "strict", StartedAt: "2026", Materialization: mat, Bootstrap: boot}); e != nil {
		t.Fatalf("StartSession rejected a canonical manifest workspace spelled with /.: %v", e)
	}
}

// TestFinishSessionRejectsNonSegmentSessionID: a record filename yielding a
// degenerate session id (".", from sessions/..json) or one containing a path
// separator ("\") is rejected as a non-segment — statePathSegment is stricter than
// the audit-token check. Asserts the segment error to isolate this guard.
func TestFinishSessionRejectsNonSegmentSessionID(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"..json", `a\b.json`} {
		name := name
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			target, started := startedForFinishTest(t)
			targetRoot := filepath.Dir(filepath.Dir(started.RecordPath))
			bad := started
			bad.RecordPath = filepath.Join(targetRoot, "sessions", name)
			_, e := target.FinishSession(context.Background(), FinishSessionRequest{Started: bad, FinishedAt: "2027", ExitStatus: "0"})
			if e == nil || !strings.Contains(e.Error(), "must be a single path segment") {
				t.Fatalf("expected single-path-segment rejection for %q, got: %v", name, e)
			}
		})
	}
}
