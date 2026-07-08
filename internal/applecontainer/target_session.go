// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package applecontainer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/omkhar/workcell/internal/host/sessions"
)

func (t AppleContainerTarget) StartSession(_ context.Context, req StartSessionRequest) (SessionResult, error) {
	sessionID, err := statePathSegment("session id", req.SessionID)
	if err != nil {
		return SessionResult{}, err
	}
	if strings.TrimSpace(req.Agent) == "" {
		return SessionResult{}, fmt.Errorf("agent is required")
	}
	if strings.TrimSpace(req.Mode) == "" {
		return SessionResult{}, fmt.Errorf("mode is required")
	}
	if strings.TrimSpace(req.StartedAt) == "" {
		return SessionResult{}, fmt.Errorf("started at is required")
	}
	// started_at is the ts= token of every started-lifecycle audit line.
	if err := validateAuditToken("started at", req.StartedAt); err != nil {
		return SessionResult{}, err
	}
	// Re-validate the manifest-derived tokens interpolated into audit lines, and
	// treat the id values used to build state paths as safe single segments.
	matID, err := statePathSegment("materialization id", req.Materialization.Manifest.MaterializationID)
	if err != nil {
		return SessionResult{}, err
	}
	bootID, err := statePathSegment("bootstrap id", req.Bootstrap.Manifest.BootstrapID)
	if err != nil {
		return SessionResult{}, err
	}
	if err := validateAuditToken("target id", req.Bootstrap.Manifest.TargetID); err != nil {
		return SessionResult{}, err
	}
	if err := validateAuditToken("image ref", req.Bootstrap.Manifest.ImageRef); err != nil {
		return SessionResult{}, err
	}
	// Normalize both TargetRoots up front (collapse trailing slashes, `.`, `//`,
	// interior `..`) so all spellings compare/derive identically: a trailing slash
	// on one but not the other must not make a matching canonical pair look
	// mismatched, and every downstream join/derivation uses the cleaned value. Clean
	// cannot create a bypass — the canonical-layout assertion below still requires
	// the exact trailing components, so a `..` that changed them is still rejected.
	matTargetRoot := filepath.Clean(req.Materialization.TargetRoot)
	bootTargetRoot := filepath.Clean(req.Bootstrap.TargetRoot)
	// Pin each manifest to the EXACT constructed path and require it to serialize
	// identically to the in-memory copy, certifying against on-disk state rather
	// than a reconstructed/stale Result's paths or manifest fields.
	matManifestPath := filepath.Join(matTargetRoot, "materializations", matID, t.Contract.WorkspaceMaterialization.ManifestName)
	if filepath.Clean(req.Materialization.ManifestPath) != matManifestPath {
		return SessionResult{}, fmt.Errorf("materialization manifest %q is not the expected %q", req.Materialization.ManifestPath, matManifestPath)
	}
	// Reject a symlink from the state root down to the materialization dir BEFORE
	// the manifest read/workspace Lstat: a symlinked materializations/<id> would
	// redirect both to an attacker tree carrying a copied manifest.
	matStateRoot := stateRootFor(matTargetRoot)
	if err := rejectSymlinkChain(matStateRoot, filepath.Dir(matManifestPath)); err != nil {
		return SessionResult{}, err
	}
	if err := verifyPersistedManifest(matStateRoot, matManifestPath, req.Materialization.Manifest); err != nil {
		return SessionResult{}, err
	}
	// The byte-compare only proves persisted == in-memory Result manifest; a
	// self-consistent tampered PAIR could still carry wrong contract identity. Pin
	// the manifest's contract-sourced fields to this target's contract.
	if err := t.assertManifestContract(req.Materialization.Manifest); err != nil {
		return SessionResult{}, err
	}
	bootManifestPath := filepath.Join(bootTargetRoot, "bootstrap", bootID, t.Contract.Bootstrap.ManifestName)
	if filepath.Clean(req.Bootstrap.ManifestPath) != bootManifestPath {
		return SessionResult{}, fmt.Errorf("bootstrap manifest %q is not the expected %q", req.Bootstrap.ManifestPath, bootManifestPath)
	}
	// Same symlink guard for the bootstrap manifest's parent chain (a symlinked
	// bootstrap/<id> would redirect the read to a copied manifest).
	bootStateRoot := stateRootFor(bootTargetRoot)
	if err := rejectSymlinkChain(bootStateRoot, filepath.Dir(bootManifestPath)); err != nil {
		return SessionResult{}, err
	}
	if err := verifyPersistedManifest(bootStateRoot, bootManifestPath, req.Bootstrap.Manifest); err != nil {
		return SessionResult{}, err
	}
	if err := t.assertManifestContract(req.Bootstrap.Manifest); err != nil {
		return SessionResult{}, err
	}
	// The materialization and bootstrap must belong to the same target, or the
	// session would record/audit one target's evidence under the other's id.
	if req.Materialization.Manifest.TargetID != req.Bootstrap.Manifest.TargetID {
		return SessionResult{}, fmt.Errorf("materialization target_id %q does not match bootstrap target_id %q", req.Materialization.Manifest.TargetID, req.Bootstrap.Manifest.TargetID)
	}
	// They must share one target root, or lifecycle evidence would split state trees.
	if matTargetRoot != bootTargetRoot {
		return SessionResult{}, fmt.Errorf("materialization target root %q does not match bootstrap target root %q", matTargetRoot, bootTargetRoot)
	}
	// TargetRoot must be the canonical <stateRoot>/targets/<kind>/<provider>/
	// <target_id> the target constructs, not merely a path both results agree on:
	// stateRootFor and every derived path flow from it, so a self-consistent
	// request naming an arbitrary dir would redirect all downstream writes. Verify
	// its trailing four components against the contract kind/provider and manifest
	// target_id (validated single segments), exactly as targetRoot() assembles them.
	provider, err := statePathSegment("target provider", t.Contract.TargetProvider)
	if err != nil {
		return SessionResult{}, err
	}
	canonicalID, err := statePathSegment("target id", req.Bootstrap.Manifest.TargetID)
	if err != nil {
		return SessionResult{}, err
	}
	troot := bootTargetRoot
	if filepath.Base(troot) != canonicalID ||
		filepath.Base(filepath.Dir(troot)) != provider ||
		filepath.Base(filepath.Dir(filepath.Dir(troot))) != t.Contract.TargetKind ||
		filepath.Base(filepath.Dir(filepath.Dir(filepath.Dir(troot)))) != "targets" {
		return SessionResult{}, fmt.Errorf("bootstrap target root %q is not the canonical targets/%s/%s/%s layout", bootTargetRoot, t.Contract.TargetKind, provider, canonicalID)
	}
	// The audit log must be the EXACT constructed path <TargetRoot>/
	// workcell.audit.log, not merely under the target root: pointing it at another
	// target-managed file (e.g. the record) would corrupt that file.
	expectedAuditLog := filepath.Join(bootTargetRoot, "workcell.audit.log")
	if filepath.Clean(req.Bootstrap.AuditLogPath) != expectedAuditLog {
		return SessionResult{}, fmt.Errorf("bootstrap audit log %q is not the expected %q", req.Bootstrap.AuditLogPath, expectedAuditLog)
	}
	// The materialized workspace must be the exact constructed path and the manifest
	// must agree, so a reconstructed Result cannot keep valid paths while pointing
	// the session/audit at an unrelated real directory.
	expectedWorkspace := filepath.Join(bootTargetRoot, "materializations", req.Materialization.Manifest.MaterializationID, t.Contract.WorkspaceMaterialization.WorkspaceDir)
	if filepath.Clean(req.Materialization.MaterializedWorkspace) != expectedWorkspace {
		return SessionResult{}, fmt.Errorf("materialized workspace %q is not the expected %q", req.Materialization.MaterializedWorkspace, expectedWorkspace)
	}
	if filepath.Clean(req.Materialization.Manifest.MaterializedWorkspace) != expectedWorkspace {
		return SessionResult{}, fmt.Errorf("manifest materialized_workspace %q is not the expected %q", req.Materialization.Manifest.MaterializedWorkspace, expectedWorkspace)
	}
	// Stat the DERIVED canonical workspace (not the caller's spelling) through the
	// openat-verified parent (statPathSafe: each parent O_NOFOLLOW, leaf Fstatat
	// AT_SYMLINK_NOFOLLOW) rather than a fresh path-based Lstat, so a
	// materializations/<id> parent swapped for a symlink after the earlier chain
	// check cannot redirect the check; require a directory.
	if st, err := statPathSafe(matStateRoot, expectedWorkspace); err != nil {
		return SessionResult{}, err
	} else if st.Mode&unix.S_IFMT != unix.S_IFDIR {
		return SessionResult{}, fmt.Errorf("materialized workspace %q is not a real directory (symlinks are rejected)", expectedWorkspace)
	}
	recordPath := filepath.Join(bootTargetRoot, "sessions", sessionID+".json")
	stateRoot := stateRootFor(bootTargetRoot)
	// Reject a symlink on any target-managed dir below the state root, so a
	// renamed-aside + symlinked parent cannot redirect the record write.
	if err := rejectSymlinkChain(stateRoot, filepath.Dir(recordPath)); err != nil {
		return SessionResult{}, err
	}
	// Serialize concurrent calls for this session id under an exclusive advisory
	// lock so the read-then-write exactly-once guard is atomic.
	unlock, err := lockSession(stateRoot, recordPath)
	if err != nil {
		return SessionResult{}, err
	}
	defer unlock()
	targetID := req.Bootstrap.Manifest.TargetID
	ws := expectedWorkspace // the derived canonical workspace, not the caller's spelling
	// Recovery-aware idempotency: the record write and the audit append are two steps
	// under this flock, so a crash between them can leave a record with no start
	// events — which would dead-end a retry ("already started" here, then "missing
	// start events" in FinishSession). If the record already exists AND belongs to
	// this session/target, heal-or-return idempotently instead of hard-rejecting.
	if _, statErr := os.Lstat(recordPath); statErr == nil {
		current, err := readSessionRecordSafe(stateRoot, recordPath)
		if err != nil {
			return SessionResult{}, err
		}
		// Only recover a record that equals what THIS request would write, on every
		// semantically-significant field the writer sets (identity + all contract
		// fields + workspace/origin + audit log + assurance + control plane), so a
		// retry with a DIFFERENT request (foreign identity, other materialization,
		// changed assurance, etc.) is a distinct start and is rejected — not recovered.
		// Only the re-stampable timestamps (started_at/observed_at) are excluded.
		if !t.startedRecordMatchesRequest(current, req, sessionID, targetID, ws, expectedAuditLog) {
			return SessionResult{}, fmt.Errorf("session %q already started with a divergent record (different request, not a retry)", sessionID)
		}
		// Are the start events already present? Read via the hardened reader; a missing
		// audit log means they are absent (partial start). auditHasEvents parses exact
		// event= tokens, so a torn final crash line is tolerated.
		var sessionLines []string
		if auditData, rerr := readAuditLog(stateRoot, expectedAuditLog); rerr == nil {
			sessionLines = filterAuditSessionLines(auditData, sessionID)
		} else if !errors.Is(rerr, os.ErrNotExist) {
			return SessionResult{}, rerr
		}
		// Presence of each start event is judged by its EXACT complete expected line, not
		// just the event= token: a line truncated after `event=<name>` (a short/failed/
		// ENOSPC append, or tampering) carries the token but is not the real event, so
		// token-only matching would wrongly treat it as satisfied and skip it. Matching
		// the whole expected line makes torn/partial start lines unrecognized → absent →
		// re-appended complete. The lines are stamped with the PERSISTED started_at (the
		// first attempt's time) so the recovered audit stays consistent with the committed
		// record; it is attacker-influenceable on-disk state, so validate it before use.
		if err := validateAuditToken("persisted started at", current.StartedAt); err != nil {
			return SessionResult{}, fmt.Errorf("session %q record holds an invalid started_at, refusing to recover: %w", sessionID, err)
		}
		// Reconcile: reject a conflicting complete line and append only the ABSENT exact
		// lines (idempotent when all present). Stamped with the PERSISTED started_at so the
		// recovered audit matches the committed record.
		expected := t.startEventLines(current.StartedAt, sessionID, targetID, ws, req)
		if _, err := t.reconcileStartEvents(stateRoot, expectedAuditLog, sessionID, expected, sessionLines); err != nil {
			return SessionResult{}, err
		}
		return SessionResult{Record: current, RecordPath: recordPath, AuditLogPath: expectedAuditLog}, nil
	} else if !os.IsNotExist(statErr) {
		return SessionResult{}, statErr
	}
	// Fresh start (no record yet). Read the shared log (mode-healed if write-only); it may
	// already hold start lines for this session id — pre-planted, or the record was deleted
	// while the append-only log kept the events. Create the record via the SAME field map
	// startedRecordMatchesRequest compares against, then reconcile: reject a divergent
	// complete line and append ONLY the ABSENT exact lines, never duplicating a pre-existing
	// exact one. On an audit-append failure the record is left in place (not removed) so the
	// retry heals via the recovery path above rather than fresh-creating a duplicate.
	var existing []string
	if data, rerr := readAuditLog(stateRoot, expectedAuditLog); rerr == nil {
		existing = filterAuditSessionLines(data, sessionID)
	} else if !errors.Is(rerr, os.ErrNotExist) {
		// Only genuine ABSENCE means "no evidence, proceed". Any other read error means the
		// log EXISTS but is unusable — a symlink/FIFO/hard-link the hardened reader rejects,
		// an un-healable EACCES, etc. Publishing a running record over an unusable/hostile
		// audit log is wrong; refuse before creating the record.
		return SessionResult{}, fmt.Errorf("session %q audit log at %s is present but unreadable — refusing to start: %w", sessionID, expectedAuditLog, rerr)
	}
	// Validate the pre-existing audit evidence BEFORE publishing the record, so a fresh
	// start over bad evidence never leaves a published running record behind. Two checks,
	// fail-closed and pre-publish: (a) the log already records this session as FINISHED (its
	// record was lost/deleted after it completed) — refuse rather than resurrect it; and
	// (b) a conflicting COMPLETE start line for any event — refuse rather than publish over
	// divergent evidence. Only once the evidence is confirmed usable do we create the record
	// and append the missing start events.
	expected := t.startEventLines(req.StartedAt, sessionID, targetID, ws, req)
	if auditHasEvents(existing, "session_finished") {
		return SessionResult{}, fmt.Errorf("session %q already terminated; refusing to fresh-start over terminal evidence", sessionID)
	}
	for _, e := range expected {
		if conflictingCompleteLine(existing, e.name, e.line) {
			return SessionResult{}, fmt.Errorf("session %q has conflicting complete start evidence for %s — refusing to start", sessionID, e.name)
		}
	}
	if err := writeSessionRecordAtomic(stateRoot, recordPath, t.startedRecordFields(req, sessionID, targetID, ws, expectedAuditLog), true); err != nil {
		return SessionResult{}, err
	}
	if _, err := t.reconcileStartEvents(stateRoot, expectedAuditLog, sessionID, expected, existing); err != nil {
		return SessionResult{}, err
	}
	record, err := readSessionRecordSafe(stateRoot, recordPath)
	if err != nil {
		return SessionResult{}, err
	}
	return SessionResult{Record: record, RecordPath: recordPath, AuditLogPath: expectedAuditLog}, nil
}

func (t AppleContainerTarget) FinishSession(_ context.Context, req FinishSessionRequest) (SessionResult, error) {
	if strings.TrimSpace(req.FinishedAt) == "" {
		return SessionResult{}, fmt.Errorf("finished at is required")
	}
	// finished_at (ts=) and exit_status are written to the session_finished audit line.
	if err := validateAuditToken("finished at", req.FinishedAt); err != nil {
		return SessionResult{}, err
	}
	if strings.TrimSpace(req.ExitStatus) == "" {
		return SessionResult{}, fmt.Errorf("exit status is required")
	}
	if err := validateAuditToken("exit status", req.ExitStatus); err != nil {
		return SessionResult{}, err
	}
	if req.Started.RecordPath == "" {
		return SessionResult{}, fmt.Errorf("started session record path is required")
	}
	// The record lives at <targetRoot>/sessions/<id>.json, so the target root is two
	// levels up and the state root that owns it is stateRootFor of that. These are
	// the trusted basis for re-deriving the canonical audit log / identity below
	// instead of trusting mutable fields the persisted record carries.
	targetRoot := filepath.Clean(filepath.Dir(filepath.Dir(req.Started.RecordPath)))
	stateRoot := stateRootFor(targetRoot)
	// The record path is caller-supplied; verify targetRoot is the canonical
	// <stateRoot>/targets/<kind>/<provider>/<id> location (the same layout check
	// StartSession applies) BEFORE any read/write, so a stale or forged RecordPath
	// cannot redirect the finalize. The leaf must be a valid single path segment.
	provider, err := statePathSegment("target provider", t.Contract.TargetProvider)
	if err != nil {
		return SessionResult{}, err
	}
	troot := filepath.Clean(targetRoot)
	if _, err := statePathSegment("target id", filepath.Base(troot)); err != nil {
		return SessionResult{}, err
	}
	if filepath.Base(filepath.Dir(troot)) != provider ||
		filepath.Base(filepath.Dir(filepath.Dir(troot))) != t.Contract.TargetKind ||
		filepath.Base(filepath.Dir(filepath.Dir(filepath.Dir(troot)))) != "targets" {
		return SessionResult{}, fmt.Errorf("record target root %q is not the canonical targets/%s/%s/<id> layout", targetRoot, t.Contract.TargetKind, provider)
	}
	// Pin the record path to EXACTLY <targetRoot>/sessions/<session_id>.json — not
	// merely under a canonical target root — so a tampered RecordPath pointing at
	// another target-managed file (the audit log, a manifest) cannot redirect the
	// read/finalize. The session id is the record's own filename; validate it as a
	// strict single path segment (statePathSegment rejects empty, ".", "..", "/",
	// "\", absolute, AND whitespace/control — a superset of the audit-token check),
	// so a degenerate record like sessions/..json (id ".") is refused up front.
	recordPath := filepath.Clean(req.Started.RecordPath)
	sessionID := strings.TrimSuffix(filepath.Base(recordPath), ".json")
	if _, err := statePathSegment("session id", sessionID); err != nil {
		return SessionResult{}, err
	}
	if recordPath != filepath.Join(targetRoot, "sessions", sessionID+".json") {
		return SessionResult{}, fmt.Errorf("record path %q is not the canonical <targetRoot>/sessions/<id>.json", req.Started.RecordPath)
	}
	if err := rejectSymlinkChain(stateRoot, filepath.Dir(recordPath)); err != nil {
		return SessionResult{}, err
	}
	// Serialize concurrent FinishSession calls for this session id so the
	// read-then-finalize exactly-once guard is atomic.
	unlock, err := lockSession(stateRoot, recordPath)
	if err != nil {
		return SessionResult{}, err
	}
	defer unlock()
	// Exactly-once finalization: reject an already-final session so a retry cannot
	// rewrite finished_at/exit_status or append a second finished line. Read through
	// the hardened path so a record swapped for a FIFO/symlink cannot HANG or
	// redirect the finalize.
	current, err := readSessionRecordSafe(stateRoot, recordPath)
	if err != nil {
		return SessionResult{}, err
	}
	// Re-derive every path/identity the finalize uses from the trusted TargetRoot /
	// record path — NOT from mutable fields the persisted record carries, which an
	// attacker can edit between start and finish. The canonical audit log is where
	// StartSession/BootstrapTarget wrote it; the session id is the record's own file
	// name; the target id is the canonical TargetRoot leaf. Done BEFORE the terminal
	// recovery below so the same identity assertions gate recovery.
	auditLogPath := filepath.Join(targetRoot, "workcell.audit.log")
	targetID := filepath.Base(targetRoot)
	// The target id (target-root leaf) enters the finish audit line; validate it as a
	// strict single path segment (same as the canonical check above; superset of the
	// audit-token check). The session id was validated above.
	if _, err := statePathSegment("target id", targetID); err != nil {
		return SessionResult{}, err
	}
	// The persisted identity must match its trusted location; a mismatch means a
	// tampered or misplaced record, so refuse rather than finalize it.
	if current.SessionID != sessionID {
		return SessionResult{}, fmt.Errorf("persisted session_id %q does not match record path %q", current.SessionID, recordPath)
	}
	if current.TargetID != targetID {
		return SessionResult{}, fmt.Errorf("persisted target_id %q does not match canonical target root leaf %q", current.TargetID, targetID)
	}
	// The record must belong to THIS target on EVERY contract-identity field, not just
	// kind/provider: a record under the canonical path with the right kind/provider but a
	// drifted target_assurance_class / runtime_api / workspace_transport / assurance /
	// control-plane is not a record this target wrote and must not be finalized.
	if err := t.assertRecordContract(current); err != nil {
		return SessionResult{}, fmt.Errorf("session %q: %w", sessionID, err)
	}
	// Recovery-aware idempotency (symmetric to StartSession): the terminal record
	// rewrite and the session_finished append are two steps under this flock, so a
	// crash between them leaves a finished record with no finish event — which a
	// retry would dead-end here. When the record is already terminal, verify the
	// finish CONTENT matches this request (exit_status; finished_at is a re-stampable
	// timestamp), then heal-or-return idempotently.
	if sessions.IsTerminalSessionStatus(current.Status) {
		// Idempotent-success/recovery applies only to a record already terminal in the
		// EXACT status this finish writes. A record terminal in a DIFFERENT status
		// (e.g. persisted failed/aborted while this finish produces exited) is not the
		// same finish — reject rather than idempotent-succeed or recover.
		if current.Status != t.Contract.Session.FinalStatus {
			return SessionResult{}, fmt.Errorf("session %q already finalized with a different terminal status %q (this finish writes %q)", sessionID, current.Status, t.Contract.Session.FinalStatus)
		}
		if req.ExitStatus != current.ExitStatus {
			return SessionResult{}, fmt.Errorf("session %q already finished with exit_status %q, not %q", sessionID, current.ExitStatus, req.ExitStatus)
		}
		// final_assurance is only set on a terminal record (assertRecordContract, which runs
		// on non-terminal records too, cannot check it), so pin it to the contract here — a
		// terminal record whose final_assurance has drifted is not a record this target
		// finalized and must not be idempotent-accepted.
		if current.FinalAssurance != t.Contract.Session.Assurance {
			return SessionResult{}, fmt.Errorf("session %q terminal record final_assurance %q does not match this target's contract %q", sessionID, current.FinalAssurance, t.Contract.Session.Assurance)
		}
		// Recovery only heals a GENUINE crash-partial, where the start triplet (written
		// by StartSession, BEFORE FinishSession ran) is still present and only the
		// session_finished event is missing. A missing/tampered log or absent start
		// events is evidence LOSS, not a partial finish — refuse rather than fabricate a
		// provenance-less finish-only log. (Asymmetric with StartSession recovery, where
		// the start triplet is the FIRST audit write, so a missing log there is
		// legitimately (re)created; here the start events must already exist.)
		auditData, rerr := readAuditLog(stateRoot, auditLogPath)
		if rerr != nil {
			return SessionResult{}, fmt.Errorf("session %q audit evidence missing — refusing to finalize: %w", sessionID, rerr)
		}
		sessionLines := filterAuditSessionLines(auditData, sessionID)
		// The persisted finish fields are attacker-influenceable on-disk state; validate
		// each token we render into the recovered audit line (no whitespace/control)
		// before use, symmetric with StartSession's started_at check.
		for _, f := range []struct{ label, value string }{
			{"persisted finished at", current.FinishedAt},
			{"persisted status", current.Status},
			{"persisted exit status", current.ExitStatus},
		} {
			if err := validateAuditToken(f.label, f.value); err != nil {
				return SessionResult{}, fmt.Errorf("session %q record holds an invalid %s, refusing to recover: %w", sessionID, f.label, err)
			}
		}
		// The recovered finish line, built from the PERSISTED record so the audit matches
		// the committed record. Because the terminal-status and exit_status gates above
		// pin current.Status/current.ExitStatus to what the first finish wrote, this is
		// byte-identical to the line the first finish appended.
		finishedLine := fmt.Sprintf("ts=%s session_id=%s event=session_finished target_kind=%s target_provider=%s target_id=%s status=%s exit_status=%s", current.FinishedAt, sessionID, t.Contract.TargetKind, t.Contract.TargetProvider, targetID, current.Status, current.ExitStatus) + auditLineSentinel
		// Only a COMPLETE, well-formed finish line counts as done. auditHasEvents matches
		// the exact event= token but would accept a line torn AFTER that token (fields
		// truncated) as present; require the whole expected line so a torn append is
		// treated as absent and healed with a complete one rather than prematurely
		// declared idempotent. (A torn fragment lingers as inert garbage — readers match
		// exact event tokens, so a fragment like `event=se` is never recognized.)
		// Before the idempotent-success return, reject a DIFFERENT COMPLETE finish line — a
		// distinct finish whose full field set survived (its trailing exit_status present)
		// but which is not our expected line. Idempotent-returning first would accept the
		// expected line while a second, disagreeing complete finish lingers. A line torn
		// after `event=session_finished` (trailing fields lost) is NOT complete: inert
		// garbage treated as "no finish present" and healed below, not conflicting evidence.
		if conflictingCompleteLine(sessionLines, "session_finished", finishedLine) {
			return SessionResult{}, fmt.Errorf("session %q has a different complete session_finished line — refusing to heal conflicting finish evidence", sessionID)
		}
		// Verify start provenance BEFORE the idempotent-success return: a present finish
		// line with MISSING/incomplete start events (removed or tampered) must not be
		// idempotent-accepted as a finalized session with no start evidence.
		if ok, err := t.startEventsComplete(current, sessionID, targetID, sessionLines); err != nil {
			return SessionResult{}, err
		} else if !ok {
			return SessionResult{}, fmt.Errorf("session %q audit log at %s is missing or has incomplete start events — refusing to finalize a record with no start provenance", sessionID, auditLogPath)
		}
		if slices.Contains(sessionLines, finishedLine) {
			return SessionResult{Record: current, RecordPath: recordPath, AuditLogPath: auditLogPath}, nil // idempotent retry
		}
		// Genuine crash-partial: start events present, NO session_finished line at all.
		if err := appendAuditLine(stateRoot, auditLogPath, finishedLine); err != nil {
			return SessionResult{}, err
		}
		return SessionResult{Record: current, RecordPath: recordPath, AuditLogPath: auditLogPath}, nil
	}
	// The log must still exist AND carry this session's start events. Read via the
	// hardened reader (not os.ReadFile): a log swapped for a FIFO would HANG here
	// and a symlinked/hard-linked log would be followed; readAuditLog rejects these
	// and leaves the session non-final for a retry.
	auditData, err := readAuditLog(stateRoot, auditLogPath)
	if err != nil {
		return SessionResult{}, err
	}
	startLines := filterAuditSessionLines(auditData, sessionID)
	if ok, err := t.startEventsComplete(current, sessionID, targetID, startLines); err != nil {
		return SessionResult{}, err
	} else if !ok {
		return SessionResult{}, fmt.Errorf("session %q audit log at %s is missing or has incomplete start events", sessionID, auditLogPath)
	}
	// Even on the fresh (non-terminal) finish, scan for a pre-existing complete
	// session_finished line before finalizing: appending ours alongside a different
	// complete finish line would leave two disagreeing finishes and permanently fail-close
	// later retries. Refuse (fail closed).
	freshFinish := fmt.Sprintf("ts=%s session_id=%s event=session_finished target_kind=%s target_provider=%s target_id=%s status=%s exit_status=%s", req.FinishedAt, sessionID, t.Contract.TargetKind, t.Contract.TargetProvider, targetID, t.Contract.Session.FinalStatus, req.ExitStatus) + auditLineSentinel
	if conflictingCompleteLine(startLines, "session_finished", freshFinish) {
		return SessionResult{}, fmt.Errorf("session %q already has a different complete session_finished line — refusing to finalize", sessionID)
	}
	if err := writeSessionRecordAtomic(stateRoot, recordPath, map[string]string{
		"status":            t.Contract.Session.FinalStatus,
		"live_status":       "stopped",
		"observed_at":       req.FinishedAt,
		"finished_at":       req.FinishedAt,
		"exit_status":       req.ExitStatus,
		"final_assurance":   t.Contract.Session.Assurance,
		"current_assurance": t.Contract.Session.Assurance,
		// Heal the persisted audit_log_path to the canonical path we re-derive, so a
		// value tampered between start and finish does not survive into the finalized
		// exported record (the finish event already goes to the canonical log).
		"audit_log_path": auditLogPath,
	}, false); err != nil {
		return SessionResult{}, err
	}
	// The terminal record rewrite has committed — the session IS finished. On an audit-
	// append failure (short/ENOSPC), do NOT roll the record back to non-terminal: the
	// complete session_finished line may already be on disk, and reverting would make the
	// retry finalize AGAIN and append a SECOND finish line (double-finish). Leaving the
	// record terminal routes the retry through the terminal-recovery path (exact finish
	// line present → idempotent no-op; absent → re-append the one line). Error still returned.
	//
	// Append only if the exact finish line is not already present (pre-planted, or a prior
	// finish whose record reverted to non-terminal) — mirrors the start-path append-only-
	// if-missing so a fresh finish never duplicates an existing exact finish line.
	if !slices.Contains(startLines, freshFinish) {
		if err := appendAudit(stateRoot, auditLogPath, freshFinish); err != nil {
			return SessionResult{}, err
		}
	}
	record, err := readSessionRecordSafe(stateRoot, recordPath)
	if err != nil {
		return SessionResult{}, err
	}
	return SessionResult{Record: record, RecordPath: recordPath, AuditLogPath: auditLogPath}, nil
}

// verifyPersistedManifest requires the on-disk manifest at path to serialize
// identically to the in-memory manifest (writeJSON emits MarshalIndent + "\n"),
// so every field — not just the path — is certified against persisted state. The
// manifest is read through the hardened path so a manifest FILE swapped for a
// symlink/FIFO is refused, not followed, even though its parent chain was checked.
func verifyPersistedManifest(stateRoot, path string, manifest any) error {
	onDisk, err := readFileSafe(stateRoot, path, "manifest")
	if err != nil {
		return err
	}
	want, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	if !bytes.Equal(onDisk, append(want, '\n')) {
		return fmt.Errorf("persisted manifest %q does not match the in-memory manifest", path)
	}
	return nil
}

// assertManifestContract pins a manifest's contract-sourced identity fields to
// this target's contract, so a self-consistent tampered manifest pair (persisted
// and Result both edited to agree, same target_id/root) still cannot certify with
// a wrong target_kind/provider/transport/runtime/access/assurance/support value.
func (t AppleContainerTarget) assertManifestContract(manifest any) error {
	switch m := manifest.(type) {
	case WorkspaceManifest:
		if m.Version != t.Contract.Version ||
			m.TargetKind != t.Contract.TargetKind ||
			m.TargetProvider != t.Contract.TargetProvider ||
			m.WorkspaceTransport != t.Contract.WorkspaceTransport ||
			!slices.Equal(m.ExcludedPaths, t.Contract.WorkspaceMaterialization.ExcludedPaths) {
			return fmt.Errorf("materialization manifest contract fields do not match the target contract")
		}
	case BootstrapManifest:
		if m.Version != t.Contract.Version ||
			m.TargetKind != t.Contract.TargetKind ||
			m.TargetProvider != t.Contract.TargetProvider ||
			m.TargetAssuranceClass != t.Contract.TargetAssuranceClass ||
			m.SupportBoundary != t.Contract.SupportBoundary ||
			m.RuntimeAPI != t.Contract.RuntimeAPI ||
			m.AccessModel != t.Contract.AccessModel {
			return fmt.Errorf("bootstrap manifest contract fields do not match the target contract")
		}
	default:
		return fmt.Errorf("unknown manifest type %T", manifest)
	}
	return nil
}
