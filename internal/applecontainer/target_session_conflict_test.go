// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package applecontainer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omkhar/workcell/internal/host/sessions"
)

// startForRecovery runs a full StartSession and returns the target, the exact
// request, and the Started result — for exercising recovery-aware idempotency.
func startForRecovery(t *testing.T) (AppleContainerTarget, StartSessionRequest, SessionResult) {
	t.Helper()
	target, mat, boot := newSessionFixture(t)
	req := startReq("sid", mat, boot)
	started, err := target.StartSession(context.Background(), req)
	mustNil(t, err)
	return target, req, started
}

// appendRawLine appends line (plus a trailing newline) to path, creating it if
// absent — used to inject a crafted, conflicting, or torn audit line directly on
// disk.
func appendRawLine(t *testing.T, path, line string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	mustNil(t, err)
	_, werr := f.WriteString(line + "\n")
	mustNil(t, werr)
	mustNil(t, f.Close())
}

// replaceAuditLine rewrites path, replacing every line containing needle with
// newLine — used to simulate a torn or tampered audit line in place.
func replaceAuditLine(t *testing.T, path, needle, newLine string) {
	t.Helper()
	data, err := os.ReadFile(path)
	mustNil(t, err)
	var out []string
	for _, ln := range strings.Split(string(data), "\n") {
		if strings.Contains(ln, needle) {
			ln = newLine
		}
		out = append(out, ln)
	}
	mustNil(t, os.WriteFile(path, []byte(strings.Join(out, "\n")), 0o600))
}

// finishOnce finalizes started once (asserting success) and returns the finish
// request so callers can drive a follow-up retry or divergence.
func finishOnce(t *testing.T, target AppleContainerTarget, started SessionResult) FinishSessionRequest {
	t.Helper()
	fin := FinishSessionRequest{Started: started, FinishedAt: "2027", ExitStatus: "0"}
	if _, e := target.FinishSession(context.Background(), fin); e != nil {
		t.Fatalf("finish failed: %v", e)
	}
	return fin
}

// TestStartSessionRecoversPartialStart: a crash between the record write and the
// audit append (simulated by removing the audit log) leaves a record with no start
// events. A retry re-appends the triplet and succeeds, and FinishSession then works
// — no dead-end. Neutralize (reject-on-exists) → the retry dead-ends → FAIL.
func TestStartSessionRecoversPartialStart(t *testing.T) {
	t.Parallel()
	target, req, started := startForRecovery(t)
	mustNil(t, os.Remove(started.AuditLogPath)) // crash: start events never landed
	if _, e := target.StartSession(context.Background(), req); e != nil {
		t.Fatalf("recovery retry of a partial start failed: %v", e)
	}
	if _, e := target.FinishSession(context.Background(), FinishSessionRequest{Started: started, FinishedAt: "2027", ExitStatus: "0"}); e != nil {
		t.Fatalf("FinishSession after recovery failed (dead session?): %v", e)
	}
	if data, _ := os.ReadFile(started.AuditLogPath); strings.Count(string(data), "event=session_started ") != 1 {
		t.Fatalf("recovery duplicated start events:\n%s", data)
	}
}

// TestStartSessionIdempotentWhenFullyStarted: a retry of a fully-started session
// returns success WITHOUT duplicating any start event.
func TestStartSessionIdempotentWhenFullyStarted(t *testing.T) {
	t.Parallel()
	target, req, started := startForRecovery(t)
	if _, e := target.StartSession(context.Background(), req); e != nil {
		t.Fatalf("idempotent retry of a completed start failed: %v", e)
	}
	data, _ := os.ReadFile(started.AuditLogPath)
	for _, ev := range []string{"event=workspace_materialized", "event=bootstrap_ready", "event=session_started "} {
		if n := strings.Count(string(data), ev); n != 1 {
			t.Fatalf("idempotent retry duplicated %q (count=%d):\n%s", ev, n, data)
		}
	}
}

// TestStartSessionDoesNotRecoverForeignRecord: recovery must not weaken the
// identity gate — an existing record whose session_id no longer matches is
// rejected, not recovered. Neutralize (drop the identity gate) → recovers → FAIL.
func TestStartSessionDoesNotRecoverForeignRecord(t *testing.T) {
	t.Parallel()
	target, req, started := startForRecovery(t)
	tamperRecordField(t, started.RecordPath, "session_id", "other")
	if _, e := target.StartSession(context.Background(), req); e == nil {
		t.Fatalf("StartSession recovered a record with a mismatched identity")
	}
}

// removeAuditEvent rewrites the audit log with every line containing needle
// removed — used to simulate a crash that dropped a specific audit event.
func removeAuditEvent(t *testing.T, path, needle string) {
	t.Helper()
	data, err := os.ReadFile(path)
	mustNil(t, err)
	var kept []string
	for _, ln := range strings.Split(string(data), "\n") {
		if !strings.Contains(ln, needle) {
			kept = append(kept, ln)
		}
	}
	mustNil(t, os.WriteFile(path, []byte(strings.Join(kept, "\n")), 0o600))
}

// TestFinishSessionRecoversPartialFinish: a crash after the terminal record rewrite
// but before the session_finished append (simulated by dropping that line) leaves a
// finished record with no finish event. A retry re-appends it (exactly one) and
// returns success. Neutralize (reject-on-terminal) → the retry dead-ends → FAIL.
func TestFinishSessionRecoversPartialFinish(t *testing.T) {
	t.Parallel()
	target, _, started := startForRecovery(t)
	fin := finishOnce(t, target, started)
	removeAuditEvent(t, started.AuditLogPath, "event=session_finished") // crash: event lost
	if _, e := target.FinishSession(context.Background(), fin); e != nil {
		t.Fatalf("recovery retry of a partial finish failed: %v", e)
	}
	if data, _ := os.ReadFile(started.AuditLogPath); strings.Count(string(data), "event=session_finished") != 1 {
		t.Fatalf("recovery missed/duplicated the finish event:\n%s", data)
	}
}

// TestFinishSessionRejectsDivergentExitStatus: a retry of a finished session with a
// DIFFERENT exit_status is a different finish, not a retry — rejected, not recovered.
func TestFinishSessionRejectsDivergentExitStatus(t *testing.T) {
	t.Parallel()
	target, _, started := startForRecovery(t)
	finishOnce(t, target, started)
	if _, e := target.FinishSession(context.Background(), FinishSessionRequest{Started: started, FinishedAt: "2027", ExitStatus: "1"}); e == nil {
		t.Fatalf("FinishSession accepted a divergent exit_status retry")
	}
}

// TestStartSessionRejectsDivergentRecovery: a partial start recovered by a retry
// whose request DIFFERS (here a different agent) is a distinct start, not a retry —
// rejected. Neutralize (identity-only gate) → recovers → FAIL.
func TestStartSessionRejectsDivergentRecovery(t *testing.T) {
	t.Parallel()
	target, req, started := startForRecovery(t)
	mustNil(t, os.Remove(started.AuditLogPath)) // partial start: events gone
	diverged := req
	diverged.Agent = "other-agent"
	if _, e := target.StartSession(context.Background(), diverged); e == nil {
		t.Fatalf("StartSession recovered a record for a divergent request")
	}
}

// TestStartSessionRejectsRecordWithExtraField (exhaustive match): a persisted record
// matching every field StartSession writes BUT also carrying an extra populated
// optional field the request never sets (here container_name) is a distinct record,
// not an idempotent retry — the exhaustive whole-struct compare rejects it. Neutralize
// (revert to an enumerated field compare that ignores container_name) → accepted → FAIL.
func TestStartSessionRejectsRecordWithExtraField(t *testing.T) {
	t.Parallel()
	target, req, started := startForRecovery(t)
	tamperRecordField(t, started.RecordPath, "container_name", "unexpected")
	if _, e := target.StartSession(context.Background(), req); e == nil {
		t.Fatalf("StartSession accepted a record carrying an unexpected extra populated field")
	}
}

// TestStartSessionRecoversGenuineRetryTimestampOnly: a partial start recovered by a
// retry with the SAME request except a re-stamped started_at still recovers (only
// timestamps are excluded from the record match).
func TestStartSessionRecoversGenuineRetryTimestampOnly(t *testing.T) {
	t.Parallel()
	target, req, started := startForRecovery(t)
	mustNil(t, os.Remove(started.AuditLogPath))
	retry := req
	retry.StartedAt = "2099" // only the timestamp differs
	if _, e := target.StartSession(context.Background(), retry); e != nil {
		t.Fatalf("genuine retry (timestamp-only difference) was rejected: %v", e)
	}
}

// TestFinishSessionRefusesWhenAuditLogDeleted: a terminal record whose audit log is
// DELETED entirely is evidence loss (a genuine crash-partial keeps the start
// events), so FinishSession refuses and does NOT fabricate a finish-only log.
// Neutralize (treat ErrNotExist as recoverable) → it fabricates a log → FAIL.
func TestFinishSessionRefusesWhenAuditLogDeleted(t *testing.T) {
	t.Parallel()
	target, _, started := startForRecovery(t)
	fin := finishOnce(t, target, started)
	mustNil(t, os.Remove(started.AuditLogPath)) // evidence gone, not just the finish line
	if _, e := target.FinishSession(context.Background(), fin); e == nil {
		t.Fatalf("FinishSession finalized despite the audit log being gone")
	}
	if _, err := os.Lstat(started.AuditLogPath); !os.IsNotExist(err) {
		t.Fatalf("FinishSession fabricated an audit log after evidence loss: %v", err)
	}
}

// TestFinishSessionRefusesWhenStartEventsGone: a terminal record whose audit log
// exists but no longer carries this session's start events is also evidence loss —
// FinishSession refuses rather than re-append a provenance-less finish event.
func TestFinishSessionRefusesWhenStartEventsGone(t *testing.T) {
	t.Parallel()
	target, _, started := startForRecovery(t)
	fin := finishOnce(t, target, started)
	// Strip every line for this session (start triplet + finish), leaving the log file
	// present but without this session's provenance.
	removeAuditEvent(t, started.AuditLogPath, "session_id=sid")
	if _, e := target.FinishSession(context.Background(), fin); e == nil {
		t.Fatalf("FinishSession finalized despite the start events being gone")
	}
}

// TestStartSessionRecoveryUsesPersistedTimestamp: recovering a crash-partial
// re-appends the start triplet stamped with the PERSISTED started_at (the first
// attempt's time), not the retry's — so the recovered audit line is consistent with
// the committed record. Neutralize (use req's time) → the line carries the retry ts
// → FAIL.
func TestStartSessionRecoveryUsesPersistedTimestamp(t *testing.T) {
	t.Parallel()
	target, req, started := startForRecovery(t) // record started_at = "2026"
	mustNil(t, os.Remove(started.AuditLogPath)) // partial start: triplet gone
	retry := req
	retry.StartedAt = "2099" // the retry re-stamps
	if _, e := target.StartSession(context.Background(), retry); e != nil {
		t.Fatalf("recovery retry failed: %v", e)
	}
	data, _ := os.ReadFile(started.AuditLogPath)
	if !strings.Contains(string(data), "ts=2026 session_id=sid event=session_started") {
		t.Fatalf("recovered triplet did not carry the persisted started_at (2026):\n%s", data)
	}
	if strings.Contains(string(data), "ts=2099") {
		t.Fatalf("recovered triplet used the retry timestamp (2099):\n%s", data)
	}
}

// TestStartSessionRejectsDivergentBootstrapOnRetry: a fully-started session retried
// with a DIFFERENT bootstrap (bootstrap_id/image_ref, which live only in the audit
// line, not the record) is not an idempotent retry — rejected. A genuine retry with
// the same bootstrap is idempotent. Neutralize (record-only match) → accepted → FAIL.
func TestStartSessionRejectsDivergentBootstrapOnRetry(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	target, mat, boot1 := newSessionFixture(t)
	req := startReq("sid", mat, boot1)
	_, err := target.StartSession(ctx, req)
	mustNil(t, err) // fully started: record + audit (bootstrap_ready bid/img:1)

	// A second, different bootstrap for the same target; retry StartSession with it.
	boot2, err := target.BootstrapTarget(ctx, BootstrapRequest{StateRoot: stateRootFor(boot1.TargetRoot), TargetID: "tid", BootstrapID: "bid2", ImageRef: "img:2"})
	mustNil(t, err)
	req2 := req
	req2.Bootstrap = boot2
	if _, e := target.StartSession(ctx, req2); e == nil {
		t.Fatalf("StartSession accepted a retry with divergent bootstrap evidence as idempotent")
	}
	// The genuine retry (same bootstrap) is idempotent.
	if _, e := target.StartSession(ctx, req); e != nil {
		t.Fatalf("genuine idempotent retry rejected: %v", e)
	}
}

// TestStartSessionRejectsTamperedStartedAtOnRecovery (Fix 1): the persisted
// started_at is excluded from the record-match gate, so an attacker can tamper it
// without failing the retry's identity check. Before StartSession recovery renders
// it into a re-appended start line it must be validated as an audit token; a value
// with whitespace is refused. Neutralize (drop the validate) → the malformed value
// is written and StartSession succeeds → FAIL.
func TestStartSessionRejectsTamperedStartedAtOnRecovery(t *testing.T) {
	t.Parallel()
	target, req, started := startForRecovery(t)
	mustNil(t, os.Remove(started.AuditLogPath)) // partial start → recovery re-append path
	tamperRecordField(t, started.RecordPath, "started_at", "2026 evil")
	_, e := target.StartSession(context.Background(), req)
	if e == nil || !strings.Contains(e.Error(), "invalid started_at") {
		t.Fatalf("expected invalid-started_at rejection on recovery, got: %v", e)
	}
}

// TestFinishSessionRejectsTamperedFinishedAtOnRecovery (Fix 2): the persisted
// finished_at is a timestamp (not covered by the terminal-status/exit_status gates),
// so it reaches the recovery re-append. It must be validated before being rendered
// into the session_finished line. Neutralize (drop the validate) → the malformed
// value is written and the finish succeeds → FAIL.
func TestFinishSessionRejectsTamperedFinishedAtOnRecovery(t *testing.T) {
	t.Parallel()
	target, _, started := startForRecovery(t)
	fin := finishOnce(t, target, started)
	removeAuditEvent(t, started.AuditLogPath, "event=session_finished") // partial finish
	tamperRecordField(t, started.RecordPath, "finished_at", "2027 evil")
	_, e := target.FinishSession(context.Background(), fin)
	if e == nil || !strings.Contains(e.Error(), "invalid persisted finished at") {
		t.Fatalf("expected invalid-finished_at rejection on recovery, got: %v", e)
	}
}

// TestStartSessionRecoversOnlyMissingStartEvents (Fix 3): a crash after 2 of 3 start
// events (here session_started is lost) is healed by appending ONLY the missing
// event — every event ends up present exactly once. Neutralize (re-append the whole
// triplet) → workspace_materialized/bootstrap_ready duplicate → FAIL.
func TestStartSessionRecoversOnlyMissingStartEvents(t *testing.T) {
	t.Parallel()
	target, req, started := startForRecovery(t)
	removeAuditEvent(t, started.AuditLogPath, "event=session_started") // crash after 2 of 3
	if _, e := target.StartSession(context.Background(), req); e != nil {
		t.Fatalf("recovery of a partial triplet failed: %v", e)
	}
	data, _ := os.ReadFile(started.AuditLogPath)
	for _, ev := range []string{"event=workspace_materialized", "event=bootstrap_ready", "event=session_started "} {
		if n := strings.Count(string(data), ev); n != 1 {
			t.Fatalf("partial-triplet recovery left %q count=%d (want 1):\n%s", ev, n, data)
		}
	}
}

// TestFinishSessionRejectsDifferentCompleteFinishLine (Fix 3, L417): a DIFFERENT but
// COMPLETE session_finished line (its trailing exit_status field intact) is a distinct
// finish — appending ours would leave two disagreeing complete finishes, so reject.
// Neutralize (drop the conflicting-evidence guard) → a duplicate is appended → FAIL.
func TestFinishSessionRejectsDifferentCompleteFinishLine(t *testing.T) {
	t.Parallel()
	target, _, started := startForRecovery(t)
	fin := finishOnce(t, target, started)
	// Tamper the finish line to a DIFFERENT but still complete finish (exit_status 0→1).
	data, err := os.ReadFile(started.AuditLogPath)
	mustNil(t, err)
	tampered := strings.Replace(string(data), "exit_status=0", "exit_status=1", 1)
	mustNil(t, os.WriteFile(started.AuditLogPath, []byte(tampered), 0o600))
	_, e := target.FinishSession(context.Background(), fin)
	if e == nil || !strings.Contains(e.Error(), "conflicting finish evidence") {
		t.Fatalf("expected conflicting-finish-evidence rejection, got: %v", e)
	}
	data2, _ := os.ReadFile(started.AuditLogPath)
	if n := strings.Count(string(data2), "event=session_finished"); n != 1 {
		t.Fatalf("conflicting finish must not append a duplicate (count=%d):\n%s", n, data2)
	}
}

// TestFinishSessionHealsTornFinishFragment (Fix 3, L417): a finish line torn after its
// event= token (trailing status/exit_status lost) is NOT a complete finish — it is
// inert garbage treated as "no finish present", so a retry HEALS by appending the
// complete line rather than being falsely idempotent OR rejected as conflicting.
// Neutralize (token-only conflict guard) → the torn fragment is treated as conflicting
// → the retry is rejected and never heals → FAIL.
func TestFinishSessionHealsTornFinishFragment(t *testing.T) {
	t.Parallel()
	target, _, started := startForRecovery(t)
	fin := finishOnce(t, target, started)
	// torn: trailing status/exit_status lost
	replaceAuditLine(t, started.AuditLogPath, "event=session_finished", "ts=2027 session_id=sid event=session_finished")
	if _, e := target.FinishSession(context.Background(), fin); e != nil {
		t.Fatalf("torn finish fragment must heal, got: %v", e)
	}
	data2, _ := os.ReadFile(started.AuditLogPath)
	if n := strings.Count(string(data2), "event=session_finished target_kind="); n != 1 {
		t.Fatalf("torn fragment must be healed with exactly one complete finish line (count=%d):\n%s", n, data2)
	}
}

// TestStartSessionRejectsDivergentBootstrapRecord (Fix A, L224): a crash before ANY
// audit line lands, then a retry naming a DIFFERENT bootstrap (id/image). With
// bootstrap_id/image_ref persisted into the record, the exhaustive record match
// catches the divergence even though no bootstrap_ready line exists to compare.
// Neutralize (drop bootstrap from the record fields) → the partial path re-appends and
// the retry is accepted → FAIL.
func TestStartSessionRejectsDivergentBootstrapRecord(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	target, mat, boot1 := newSessionFixture(t)
	boot2, err := target.BootstrapTarget(ctx, BootstrapRequest{StateRoot: stateRootFor(boot1.TargetRoot), TargetID: "tid", BootstrapID: "bid2", ImageRef: "img:2"})
	mustNil(t, err)
	req1 := startReq("sid", mat, boot1)
	started, err := target.StartSession(ctx, req1)
	mustNil(t, err)
	mustNil(t, os.Remove(started.AuditLogPath)) // crash: no audit line landed at all
	req2 := req1
	req2.Bootstrap = boot2 // a valid, on-disk, but DIFFERENT bootstrap
	if _, e := target.StartSession(ctx, req2); e == nil {
		t.Fatalf("StartSession recovered a record for a request naming a different bootstrap")
	}
}

// TestStartSessionRejectsPartialWithDivergentBootstrapAudit (Fix A belt-and-suspenders,
// L226): a partial start whose surviving bootstrap_ready audit line names a DIFFERENT
// bootstrap than the retry is drift — recovery must reject rather than skip the line.
// Neutralize (drop the partial-path bootstrap-audit check) → the line is skipped and the
// retry recovers → FAIL.
func TestStartSessionRejectsPartialWithDivergentBootstrapAudit(t *testing.T) {
	t.Parallel()
	target, req, started := startForRecovery(t)
	removeAuditEvent(t, started.AuditLogPath, "event=session_started") // partial: only 2 of 3
	data, err := os.ReadFile(started.AuditLogPath)
	mustNil(t, err)
	tampered := strings.Replace(string(data), "bootstrap_id=bid image_ref", "bootstrap_id=other image_ref", 1)
	mustNil(t, os.WriteFile(started.AuditLogPath, []byte(tampered), 0o600))
	if _, e := target.StartSession(context.Background(), req); e == nil {
		t.Fatalf("StartSession recovered a partial start whose audit names a different bootstrap")
	}
}

// TestSessionRecordOmitsBootstrapWhenUnset: a record that does not set
// bootstrap_id/image_ref (every remotevm record) serializes WITHOUT those keys, so
// adding the fields keeps such records byte-identical to before.
func TestSessionRecordOmitsBootstrapWhenUnset(t *testing.T) {
	t.Parallel()
	b, err := sessions.EncodeSessionRecordFrom(nil, map[string]string{
		"session_id": "s", "profile": "t", "agent": "a", "mode": "m",
		"status": "running", "workspace": "/ws", "started_at": "2026",
	})
	mustNil(t, err)
	if strings.Contains(string(b), "bootstrap_id") || strings.Contains(string(b), "image_ref") {
		t.Fatalf("record with no bootstrap set leaked bootstrap keys:\n%s", b)
	}
}

// TestStartSessionRewritesTruncatedStartLine (Fix 1, L204): a start event line truncated
// to just its event= token is NOT the exact complete expected line, so recovery treats it
// as absent and re-appends the COMPLETE line (token-only matching would wrongly skip it).
// Neutralize (token-only presence) → the complete line is never written → FAIL.
func TestStartSessionRewritesTruncatedStartLine(t *testing.T) {
	t.Parallel()
	target, req, started := startForRecovery(t)
	// Truncate the workspace_materialized line to just its event token (trailing fields lost).
	replaceAuditLine(t, started.AuditLogPath, "event=workspace_materialized", "ts=2026 session_id=sid event=workspace_materialized")
	if _, e := target.StartSession(context.Background(), req); e != nil {
		t.Fatalf("recovery of a truncated start line failed: %v", e)
	}
	data2, _ := os.ReadFile(started.AuditLogPath)
	if !strings.Contains(string(data2), "event=workspace_materialized target_kind=") {
		t.Fatalf("truncated start line was not rewritten with a complete line:\n%s", data2)
	}
	// The session must still finalize (start provenance intact after healing).
	if _, e := target.FinishSession(context.Background(), FinishSessionRequest{Started: started, FinishedAt: "2027", ExitStatus: "0"}); e != nil {
		t.Fatalf("FinishSession after start-line heal failed: %v", e)
	}
}

// TestFinishSessionHealsTornFragmentAfterRollback (Fix 3b, L477): the fresh-finish
// rollback restores a NON-terminal record while a torn session_finished fragment (from
// the failed append) lingers. A subsequent FinishSession takes the fresh path and heals
// (writes the complete line + terminal record), neither falsely idempotent nor rejected.
func TestFinishSessionHealsTornFragmentAfterRollback(t *testing.T) {
	t.Parallel()
	target, _, started := startForRecovery(t) // non-terminal, start events present
	// Simulate a prior finish that tore mid-append: a fragment lingers, record still non-terminal.
	appendRawLine(t, started.AuditLogPath, "ts=2027 session_id=sid event=session_finished") // torn fragment
	if _, e := target.FinishSession(context.Background(), FinishSessionRequest{Started: started, FinishedAt: "2027", ExitStatus: "0"}); e != nil {
		t.Fatalf("finish over a torn fragment must heal, got: %v", e)
	}
	data, _ := os.ReadFile(started.AuditLogPath)
	if n := strings.Count(string(data), "event=session_finished target_kind="); n != 1 {
		t.Fatalf("expected exactly one complete finish line after healing (count=%d):\n%s", n, data)
	}
}

// TestStartTripletIsSingleBuffer (Fix 2): the fresh-start triplet is rendered as ONE
// newline-joined buffer (3 lines, 2 internal newlines) so StartSession writes it in a
// single appendAuditLine call — no inter-line partial window on the shared target log.
func TestStartTripletIsSingleBuffer(t *testing.T) {
	t.Parallel()
	target, err := NewAppleContainerTarget(Contract{})
	mustNil(t, err)
	tri := target.startTriplet("2026", "sid", "tid", "/ws", StartSessionRequest{})
	if got := strings.Count(tri, "\n"); got != 2 {
		t.Fatalf("triplet is not a single 3-line buffer (internal newlines=%d):\n%s", got, tri)
	}
	for _, ev := range []string{"event=workspace_materialized", "event=bootstrap_ready", "event=session_started"} {
		if !strings.Contains(tri, ev) {
			t.Fatalf("triplet buffer missing %q:\n%s", ev, tri)
		}
	}
}

// withAuditAppendFailpoint installs a failpoint on the audit-append seam that writes
// only the first `landLines` complete lines of the buffer (via the real appender) and
// then returns an error, simulating a short/ENOSPC write. It restores the seam on
// cleanup so a subsequent retry uses the real appender.
func withAuditAppendFailpoint(t *testing.T, landLines int) {
	t.Helper()
	orig := appendAudit
	t.Cleanup(func() { appendAudit = orig })
	appendAudit = func(stateRoot, path, buf string) error {
		lines := strings.Split(buf, "\n")
		if landLines > 0 {
			_ = orig(stateRoot, path, strings.Join(lines[:landLines], "\n")) // land the first N complete lines
		}
		return errors.New("simulated short audit write")
	}
}

// TestStartSessionKeepsPartialAndRecovers (Fix 1, L257): a start append that lands the
// FIRST complete start line then fails must leave the record AND that line in place; the
// retry recovers by appending only the 2 missing events → exactly one of each, no
// duplicate of the already-written line. Neutralize (remove the record on failure) →
// the retry fresh-creates and re-appends all three → duplicate first line → FAIL.
func TestStartSessionKeepsPartialAndRecovers(t *testing.T) {
	target, mat, boot := newSessionFixture(t)
	req := startReq("sid", mat, boot)
	recordPath := filepath.Join(boot.TargetRoot, "sessions", "sid.json")

	withAuditAppendFailpoint(t, 1) // land workspace_materialized, then fail
	if _, e := target.StartSession(context.Background(), req); e == nil {
		t.Fatal("StartSession should surface the audit-append failure")
	}
	if _, err := os.Stat(recordPath); err != nil {
		t.Fatalf("record must be kept after a partial start append: %v", err)
	}
	// Retry with the seam restored (Cleanup also restores it at test end).
	appendAudit = appendAuditLine
	if _, e := target.StartSession(context.Background(), req); e != nil {
		t.Fatalf("recovery retry after partial start failed: %v", e)
	}
	data, _ := os.ReadFile(boot.AuditLogPath)
	for _, ev := range []string{"event=workspace_materialized", "event=bootstrap_ready", "event=session_started "} {
		if n := strings.Count(string(data), ev); n != 1 {
			t.Fatalf("partial-start recovery left %q count=%d (want 1):\n%s", ev, n, data)
		}
	}
}

// TestFinishSessionStaysTerminalAndIdempotent (Fix 2, L477): a finish append that lands
// the complete session_finished line then fails must leave the record TERMINAL; the
// retry sees the complete line → idempotent no-op (no second finish line). Neutralize
// (roll back to non-terminal) → the retry finalizes again → two finish lines → FAIL.
func TestFinishSessionStaysTerminalAndIdempotent(t *testing.T) {
	target, _, started := startForRecovery(t)
	fin := FinishSessionRequest{Started: started, FinishedAt: "2027", ExitStatus: "0"}
	withAuditAppendFailpoint(t, 1) // land the complete finish line, then fail
	if _, e := target.FinishSession(context.Background(), fin); e == nil {
		t.Fatal("FinishSession should surface the audit-append failure")
	}
	appendAudit = appendAuditLine
	if _, e := target.FinishSession(context.Background(), fin); e != nil {
		t.Fatalf("idempotent finish retry after terminal commit failed: %v", e)
	}
	data, _ := os.ReadFile(started.AuditLogPath)
	if n := strings.Count(string(data), "event=session_finished"); n != 1 {
		t.Fatalf("finish must stay terminal and not double-finish (count=%d):\n%s", n, data)
	}
}

// TestFinishSessionStaysTerminalRecoversWhenNoLine (Fix 2, no-line case): a finish
// append that writes NOTHING then fails still leaves the record TERMINAL; the retry
// re-appends exactly one finish line via the terminal-recovery path.
func TestFinishSessionStaysTerminalRecoversWhenNoLine(t *testing.T) {
	target, _, started := startForRecovery(t)
	fin := FinishSessionRequest{Started: started, FinishedAt: "2027", ExitStatus: "0"}
	withAuditAppendFailpoint(t, 0) // no line lands, then fail
	if _, e := target.FinishSession(context.Background(), fin); e == nil {
		t.Fatal("FinishSession should surface the audit-append failure")
	}
	appendAudit = appendAuditLine
	if _, e := target.FinishSession(context.Background(), fin); e != nil {
		t.Fatalf("terminal recovery retry (no finish line) failed: %v", e)
	}
	data, _ := os.ReadFile(started.AuditLogPath)
	if n := strings.Count(string(data), "event=session_finished"); n != 1 {
		t.Fatalf("expected exactly one finish line after recovery (count=%d):\n%s", n, data)
	}
}

// TestFinishSessionRefusesTornStartLine (L459/L441): a start line truncated after its
// event= token is not the exact complete start line, so FinishSession must NOT treat it
// as valid start provenance — it refuses to finalize. Neutralize (token matching) →
// FinishSession finalizes on the torn start line → FAIL.
func TestFinishSessionRefusesTornStartLine(t *testing.T) {
	t.Parallel()
	target, _, started := startForRecovery(t)
	// Truncate the session_started line to just its event token (trailing fields lost).
	replaceAuditLine(t, started.AuditLogPath, "event=session_started", "ts=2026 session_id=sid event=session_started")
	_, e := target.FinishSession(context.Background(), FinishSessionRequest{Started: started, FinishedAt: "2027", ExitStatus: "0"})
	if e == nil || !strings.Contains(e.Error(), "incomplete start events") {
		t.Fatalf("expected incomplete-start-events refusal, got: %v", e)
	}
}

// TestStartSessionRejectsConflictingCompleteStartLine (Fix 1, L229): a fully-started
// record whose audit log holds the EXPECTED session_started line AND a second, DIFFERENT
// complete session_started line must NOT be idempotent-accepted — the conflict is flagged
// before the idempotent return. Neutralize (idempotent-return before the scan) → the
// retry accepts the conflicting log → FAIL.
func TestStartSessionRejectsConflictingCompleteStartLine(t *testing.T) {
	t.Parallel()
	target, req, started := startForRecovery(t)
	// Append a second, DIFFERENT complete session_started line for the same session.
	ss := auditLineContaining(t, started.AuditLogPath, "event=session_started ")
	conflict := strings.Replace(ss, "workspace_control_plane=", "workspace_control_plane=evil_", 1)
	appendRawLine(t, started.AuditLogPath, conflict)
	_, e := target.StartSession(context.Background(), req)
	if e == nil || !strings.Contains(e.Error(), "conflicting complete start evidence") {
		t.Fatalf("expected conflicting-complete-start rejection, got: %v", e)
	}
}

// TestFinishSessionRejectsConflictingCompleteFinishOnIdempotentRetry (Fix 2, L456): a
// terminal record whose audit log holds the EXPECTED session_finished line AND a second,
// DIFFERENT complete session_finished line must NOT be idempotent-accepted — the conflict
// runs before the idempotent return. Neutralize (idempotent-return first) → accepts → FAIL.
func TestFinishSessionRejectsConflictingCompleteFinishOnIdempotentRetry(t *testing.T) {
	t.Parallel()
	target, _, started := startForRecovery(t)
	fin := finishOnce(t, target, started)
	// Append a second, DIFFERENT complete session_finished line (different finished_at ts).
	line := auditLineContaining(t, started.AuditLogPath, "event=session_finished target_kind=")
	conflict := strings.Replace(line, "ts=2027", "ts=2099", 1)
	appendRawLine(t, started.AuditLogPath, conflict)
	_, e := target.FinishSession(context.Background(), fin)
	if e == nil || !strings.Contains(e.Error(), "conflicting finish evidence") {
		t.Fatalf("expected conflicting-finish rejection on idempotent retry, got: %v", e)
	}
}

// TestFreshStartRejectsPrePlantedConflictingLine (FIX B): a fresh StartSession whose
// shared log already holds a COMPLETE divergent session_started line for this session_id
// must refuse up front (fail closed) rather than append a triplet that would leave two
// disagreeing complete lines and permanently fail-close later retries. Neutralize (drop
// the fresh-start scan) → the start succeeds and plants the dead-end → FAIL.
func TestFreshStartRejectsPrePlantedConflictingLine(t *testing.T) {
	t.Parallel()
	target, mat, boot := newSessionFixture(t)
	req := startReq("sid", mat, boot)
	// Pre-plant a COMPLETE but divergent session_started line for this session_id.
	mustNil(t, os.MkdirAll(filepath.Dir(boot.AuditLogPath), 0o755))
	appendRawLine(t, boot.AuditLogPath, "ts=2026 session_id=sid event=session_started target_kind= target_provider= target_id=tid status=running workspace_control_plane=evil v=1")
	_, e := target.StartSession(context.Background(), req)
	if e == nil || !strings.Contains(e.Error(), "conflicting complete start evidence") {
		t.Fatalf("expected fresh-start conflicting-evidence refusal, got: %v", e)
	}
}

// TestFreshFinishRejectsPreExistingCompleteFinish (FIX B): a fresh (non-terminal)
// FinishSession whose log already holds a different COMPLETE session_finished line must
// refuse rather than append a second disagreeing finish. Neutralize (drop the fresh
// finish scan) → double-finish → FAIL.
func TestFreshFinishRejectsPreExistingCompleteFinish(t *testing.T) {
	t.Parallel()
	_, _, started := startForRecovery(t)
	target, err := NewAppleContainerTarget(Contract{})
	mustNil(t, err)
	// Pre-plant a different COMPLETE session_finished line (different exit_status).
	appendRawLine(t, started.AuditLogPath, "ts=2027 session_id=sid event=session_finished target_kind= target_provider= target_id=tid status=exited exit_status=9 v=1")
	_, e := target.FinishSession(context.Background(), FinishSessionRequest{Started: started, FinishedAt: "2027", ExitStatus: "0"})
	if e == nil || !strings.Contains(e.Error(), "complete session_finished line") {
		t.Fatalf("expected fresh-finish conflicting-finish refusal, got: %v", e)
	}
}

// TestFinishSessionRejectsContractDrift (FIX C): a record whose kind/provider/id place it
// under the canonical target path but whose other contract-identity fields have DRIFTED
// (here runtime_api) must be rejected, not finalized. Neutralize (only kind/provider
// checked) → finalizes → FAIL.
func TestFinishSessionRejectsContractDrift(t *testing.T) {
	t.Parallel()
	target, started := startedForFinishTest(t)
	tamperRecordField(t, started.RecordPath, "runtime_api", "drifted-api")
	_, e := target.FinishSession(context.Background(), FinishSessionRequest{Started: started, FinishedAt: "2027", ExitStatus: "0"})
	if e == nil || !strings.Contains(e.Error(), "contract fields do not match") {
		t.Fatalf("expected contract-drift rejection, got: %v", e)
	}
}

// auditLineContaining returns the audit line in logPath containing needle, failing the
// test if none is present. Each caller crafts a scenario with exactly one matching line.
func auditLineContaining(t *testing.T, logPath, needle string) string {
	t.Helper()
	data, err := os.ReadFile(logPath)
	mustNil(t, err)
	for _, ln := range strings.Split(string(data), "\n") {
		if strings.Contains(ln, needle) {
			return ln
		}
	}
	t.Fatalf("no audit line containing %q found", needle)
	return ""
}

// TestStartSessionIgnoresHealedTornPrefixFragment (L333): a healed torn fragment that is
// a strict PREFIX of the expected line (a short/ENOSPC write cut mid-value of the trailing
// field) must NOT be treated as a conflicting complete line — it heals via re-append. A
// StartSession retry with such a fragment present succeeds (idempotent). Neutralize (drop
// the prefix exclusion) → the fragment is a false conflict → the retry is refused → FAIL.
func TestStartSessionIgnoresHealedTornPrefixFragment(t *testing.T) {
	t.Parallel()
	target, req, started := startForRecovery(t)
	ss := auditLineContaining(t, started.AuditLogPath, "event=session_started ")
	frag := ss[:len(ss)-1] // strict prefix: trailing field truncated mid-value
	appendRawLine(t, started.AuditLogPath, frag)
	if _, e := target.StartSession(context.Background(), req); e != nil {
		t.Fatalf("healed torn prefix fragment wrongly treated as conflict: %v", e)
	}
}

// TestStartSessionRejectsDivergentTrailingValue (L333 regression): a complete line that
// shares a long prefix with the expected line but DIVERGES in the trailing value (not a
// prefix relation) is still a genuine conflict and must be rejected — the prefix
// exclusion must not swallow real divergence.
func TestStartSessionRejectsDivergentTrailingValue(t *testing.T) {
	t.Parallel()
	target, req, started := startForRecovery(t)
	ss := auditLineContaining(t, started.AuditLogPath, "event=session_started ")
	repl := byte('X')
	if ss[len(ss)-1] == repl {
		repl = 'Y'
	}
	divergent := ss[:len(ss)-1] + string(repl) // same length, diverges at the last char
	appendRawLine(t, started.AuditLogPath, divergent)
	_, e := target.StartSession(context.Background(), req)
	if e == nil || !strings.Contains(e.Error(), "conflicting complete start evidence") {
		t.Fatalf("expected divergent-trailing-value rejection, got: %v", e)
	}
}

// TestStartSessionRecoversWriteOnlyLog (FIX A, L208): a crash-created 0o200 write-only
// audit log + a present record — StartSession recovery must heal the log mode, read it,
// and recover (append-only-missing). Neutralize (no read-heal) → the recovery read
// EACCES's → recovery fails → FAIL.
func TestStartSessionRecoversWriteOnlyLog(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("write-only unreadable precondition does not hold for root")
	}
	target, req, started := startForRecovery(t)
	// Drop one start event so recovery must re-append it, and mask the log read bit.
	removeAuditEvent(t, started.AuditLogPath, "event=session_started")
	mustNil(t, os.Chmod(started.AuditLogPath, 0o200))
	if _, e := target.StartSession(context.Background(), req); e != nil {
		t.Fatalf("recovery over a write-only log failed: %v", e)
	}
	fi, err := os.Stat(started.AuditLogPath)
	mustNil(t, err)
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Fatalf("log mode = %o after recovery, want 600 (not healed)", perm)
	}
	data, _ := os.ReadFile(started.AuditLogPath)
	if n := strings.Count(string(data), "event=session_started "); n != 1 {
		t.Fatalf("recovery did not restore exactly one session_started (count=%d):\n%s", n, data)
	}
}

// TestFreshStartDoesNotDuplicateExactTriplet (FIX B, L260): record absent but the shared
// log already holds this session's EXACT start triplet (record deleted while the append-
// only log kept the events, or pre-planted) — a fresh StartSession must append-only-MISSING
// (nothing here) and NOT duplicate. Neutralize (unconditional triplet append) → duplicates
// → FAIL.
func TestFreshStartDoesNotDuplicateExactTriplet(t *testing.T) {
	t.Parallel()
	target, req, started := startForRecovery(t)
	// Delete the record but keep the append-only log with the exact triplet.
	mustNil(t, os.Remove(started.RecordPath))
	if _, e := target.StartSession(context.Background(), req); e != nil {
		t.Fatalf("fresh start over a pre-existing exact triplet failed: %v", e)
	}
	data, _ := os.ReadFile(started.AuditLogPath)
	for _, ev := range []string{"event=workspace_materialized", "event=bootstrap_ready", "event=session_started "} {
		if n := strings.Count(string(data), ev); n != 1 {
			t.Fatalf("fresh start duplicated %q (count=%d):\n%s", ev, n, data)
		}
	}
}

// TestFinishSessionRejectsTamperedPersistedRenderField (FIX C, L307): a persisted field
// used to render the expected start lines (here bootstrap_id) tampered with WHITESPACE —
// which the record-level newline scan permits (it rejects only \r\n) but which would inject
// a spurious audit token when rendered raw — must be rejected by startEventsComplete's
// audit-token validation. Neutralize (no token validation) → the malformed expected line is
// rendered and matched instead of a clean rejection → FAIL.
func TestFinishSessionRejectsTamperedPersistedRenderField(t *testing.T) {
	t.Parallel()
	target, started := startedForFinishTest(t)
	tamperRecordField(t, started.RecordPath, "bootstrap_id", "bid injected=evil")
	_, e := target.FinishSession(context.Background(), FinishSessionRequest{Started: started, FinishedAt: "2027", ExitStatus: "0"})
	if e == nil || !strings.Contains(e.Error(), "invalid persisted bootstrap id") {
		t.Fatalf("expected tampered-persisted-field rejection, got: %v", e)
	}
}

// TestFreshStartRefusesOverTerminalEvidence (FIX 1, L246): the append-only log records a
// COMPLETED session whose record was lost/deleted. A fresh StartSession must refuse rather
// than resurrect the terminated session over its session_finished evidence. Neutralize
// (skip the terminal check) → it fresh-starts over the finished session → FAIL.
func TestFreshStartRefusesOverTerminalEvidence(t *testing.T) {
	t.Parallel()
	target, req, started := startForRecovery(t)
	finishOnce(t, target, started)
	// Lose the record but keep the append-only log (which carries session_finished).
	mustNil(t, os.Remove(started.RecordPath))
	_, e := target.StartSession(context.Background(), req)
	if e == nil || !strings.Contains(e.Error(), "refusing to fresh-start over terminal evidence") {
		t.Fatalf("expected terminal-evidence refusal, got: %v", e)
	}
}

// TestFinishSessionRejectsIdempotentWithoutStartProvenance (FIX 1, L551): a terminal
// record + a present session_finished line but ABSENT start events must NOT be idempotent-
// accepted — start provenance is verified before the idempotent-success return. Neutralize
// (idempotent-return before the start check) → accepts → FAIL.
func TestFinishSessionRejectsIdempotentWithoutStartProvenance(t *testing.T) {
	t.Parallel()
	target, _, started := startForRecovery(t)
	fin := finishOnce(t, target, started)
	// Terminal record + finish line present, but strip the start events.
	for _, ev := range []string{"event=workspace_materialized", "event=bootstrap_ready", "event=session_started"} {
		removeAuditEvent(t, started.AuditLogPath, ev)
	}
	_, e := target.FinishSession(context.Background(), fin)
	if e == nil || !strings.Contains(e.Error(), "start events") {
		t.Fatalf("expected missing-start-provenance refusal on idempotent finish, got: %v", e)
	}
}

// TestFreshStartValidatesEvidenceBeforePublishingRecord (FIX 2, L249): a fresh start over
// divergent pre-existing audit evidence must be refused BEFORE the running record is
// published — no record file must be left behind. Neutralize (validate-after-publish) →
// the record is published then the error returns → the record file is present → FAIL.
func TestFreshStartValidatesEvidenceBeforePublishingRecord(t *testing.T) {
	t.Parallel()
	target, req, started := startForRecovery(t)
	// Plant a DIFFERENT complete session_started line, then lose the record (fresh path).
	ss := auditLineContaining(t, started.AuditLogPath, "event=session_started ")
	divergent := ss[:len(ss)-1] + "Z" // same length, diverges at the last char (not a prefix)
	appendRawLine(t, started.AuditLogPath, divergent)
	mustNil(t, os.Remove(started.RecordPath))
	_, e := target.StartSession(context.Background(), req)
	if e == nil || !strings.Contains(e.Error(), "conflicting complete start evidence") {
		t.Fatalf("expected conflicting-evidence refusal, got: %v", e)
	}
	if _, serr := os.Stat(started.RecordPath); !os.IsNotExist(serr) {
		t.Fatalf("a record was published before evidence validation (fail-open): %v", serr)
	}
}

// TestStartSessionFlagsCompletePrefixValueLine (L118 sentinel fix): a COMPLETE
// (sentinel-terminated) pre-existing start line whose trailing-field VALUE is a genuine
// PREFIX of the expected value (image_ref=img vs the real img:1) is no longer mistaken for
// a torn fragment — the fixed trailing sentinel makes it diverge before the end, so it is
// flagged as a conflict. Neutralize (drop the sentinel from the render) → the prefix-value
// line is treated as a torn prefix and ignored → no conflict → FAIL.
func TestStartSessionFlagsCompletePrefixValueLine(t *testing.T) {
	t.Parallel()
	target, req, started := startForRecovery(t)
	br := auditLineContaining(t, started.AuditLogPath, "event=bootstrap_ready ")
	// A COMPLETE line (retains the sentinel) whose image_ref value is a prefix of img:1.
	prefixVal := strings.Replace(br, "image_ref=img:1", "image_ref=img", 1)
	if prefixVal == br {
		t.Fatal("failed to craft a prefix-value line")
	}
	appendRawLine(t, started.AuditLogPath, prefixVal)
	_, e := target.StartSession(context.Background(), req)
	if e == nil || !strings.Contains(e.Error(), "conflicting complete start evidence") {
		t.Fatalf("expected complete prefix-value line flagged as conflict, got: %v", e)
	}
}

// TestFinishSessionRejectsDriftedFinalAssurance (FIX 1, L371): a terminal record whose
// final_assurance has drifted from the contract must be rejected on the idempotent finish
// path, not accepted. Neutralize (no final_assurance check) → accepts → FAIL.
func TestFinishSessionRejectsDriftedFinalAssurance(t *testing.T) {
	t.Parallel()
	target, _, started := startForRecovery(t)
	fin := finishOnce(t, target, started)
	tamperRecordField(t, started.RecordPath, "final_assurance", "drifted-assurance")
	_, e := target.FinishSession(context.Background(), fin)
	if e == nil || !strings.Contains(e.Error(), "final_assurance") {
		t.Fatalf("expected drifted-final_assurance rejection, got: %v", e)
	}
}

// TestFreshFinishDoesNotDuplicateExactFinishLine (FIX 2, L495): a fresh (non-terminal)
// finish whose exact finish line is already in the append-only log must append-only-if-
// missing — not duplicate it. Neutralize (unconditional append) → two finish lines → FAIL.
func TestFreshFinishDoesNotDuplicateExactFinishLine(t *testing.T) {
	t.Parallel()
	target, _, started := startForRecovery(t)
	fin := finishOnce(t, target, started)
	// Revert the record to non-terminal but keep the exact finish line in the log.
	tamperRecordFields(t, started.RecordPath, map[string]string{
		"status": "running", "finished_at": "", "exit_status": "", "final_assurance": "",
	})
	if _, e := target.FinishSession(context.Background(), fin); e != nil {
		t.Fatalf("fresh finish over an existing exact finish line failed: %v", e)
	}
	data, _ := os.ReadFile(started.AuditLogPath)
	if n := strings.Count(string(data), "event=session_finished target_kind="); n != 1 {
		t.Fatalf("fresh finish duplicated the finish line (count=%d):\n%s", n, data)
	}
}

// TestFinishSessionRejectsConflictingStartEvidence (FIX 3, L464): the expected start
// triplet is present but an extra DIFFERENT complete session_started line exists — a
// conflicting start line. FinishSession must refuse, not just verify presence. Neutralize
// (presence-only startEventsComplete) → finalizes → FAIL.
func TestFinishSessionRejectsConflictingStartEvidence(t *testing.T) {
	t.Parallel()
	target, _, started := startForRecovery(t)
	ss := auditLineContaining(t, started.AuditLogPath, "event=session_started ")
	divergent := strings.Replace(ss, "workspace_control_plane=", "workspace_control_plane=evil_", 1)
	if divergent == ss {
		t.Fatal("failed to craft a divergent session_started line")
	}
	appendRawLine(t, started.AuditLogPath, divergent)
	_, e := target.FinishSession(context.Background(), FinishSessionRequest{Started: started, FinishedAt: "2027", ExitStatus: "0"})
	if e == nil || !strings.Contains(e.Error(), "conflicting complete start evidence") {
		t.Fatalf("expected conflicting-start-evidence refusal, got: %v", e)
	}
}
