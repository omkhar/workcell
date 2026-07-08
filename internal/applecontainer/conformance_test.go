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

// rejectsEachField mutates base one field at a time; validate must reject each.
func rejectsEachField[T any](t *testing.T, label string, base T, muts []func(*T), validate func(T) error) {
	t.Helper()
	for i, mut := range muts {
		bad := base
		mut(&bad)
		if validate(bad) == nil {
			t.Fatalf("%s field %d: tampered value accepted (not pinned)", label, i)
		}
	}
}

// rejectsEachPersistedManifestField rewrites the ON-DISK manifest one field at a time; validate
// (which reads the persisted file) must reject each. Restores the original bytes afterward.
func rejectsEachPersistedManifestField[M any](t *testing.T, label, path string, base M, muts []func(*M), validate func() error) {
	t.Helper()
	orig, err := os.ReadFile(path)
	mustNil(t, err)
	defer func() { mustNil(t, os.WriteFile(path, orig, 0o600)) }()
	for i, mut := range muts {
		bad := base
		mut(&bad)
		data, err := json.MarshalIndent(bad, "", "  ")
		mustNil(t, err)
		mustNil(t, os.WriteFile(path, append(data, '\n'), 0o600))
		if validate() == nil {
			t.Fatalf("%s persisted field %d: tampered on-disk value accepted (not pinned)", label, i)
		}
	}
}

// rejectsEachManifestFieldPin tampers BOTH the RETURNED manifest struct AND the persisted file with
// the SAME value per field, so the returned-vs-persisted check (DeepEqual/==) passes and the
// specific per-field pin in validate is what rejects — making each manifest field pin independently
// neutralization-provable (unlike the persisted-only matrix, which the DeepEqual check catches).
func rejectsEachManifestFieldPin[R any, M any](t *testing.T, label, path string, base R, manifest func(*R) *M, muts []func(*M), validate func(R) error) {
	t.Helper()
	orig, err := os.ReadFile(path)
	mustNil(t, err)
	defer func() { mustNil(t, os.WriteFile(path, orig, 0o600)) }()
	for i, mut := range muts {
		bad := base
		m := manifest(&bad)
		mut(m)
		data, err := json.MarshalIndent(*m, "", "  ")
		mustNil(t, err)
		mustNil(t, os.WriteFile(path, append(data, '\n'), 0o600))
		if validate(bad) == nil {
			t.Fatalf("%s field pin %d: consistently-tampered value accepted (pin not exercised)", label, i)
		}
	}
}

func TestAppleContainerTargetPassesConformance(t *testing.T) {
	t.Parallel()

	source := writeSampleWorkspace(t)
	target, err := NewAppleContainerTarget(Contract{})
	mustNil(t, err)
	contract := DefaultContract()
	result, err := RunConformance(context.Background(), target, contract, DefaultConformanceCase(t.TempDir(), source))
	mustNil(t, err)

	if result.Exported.Session.Status != contract.Session.FinalStatus {
		t.Fatalf("final status = %q, want %q", result.Exported.Session.Status, contract.Session.FinalStatus)
	}
}

// TestConformanceValidatorsRejectTamperedResults tampers EVERY pinned field of every validator
// (manifests on disk; session records via the persisted record) and requires each rejected, plus
// the empty-destination and audit edge cases.
func TestConformanceValidatorsRejectTamperedResults(t *testing.T) {
	t.Parallel()

	ctx, target, contract, c, layout := newFixture(t)
	mat, boot := materializeAndBootstrap(t, ctx, target, c)
	started, err := target.StartSession(ctx, StartSessionRequest{SessionID: c.SessionID, Agent: c.Agent, Mode: c.Mode, StartedAt: c.StartedAt, Materialization: mat, Bootstrap: boot})
	mustNil(t, err)
	// The AUTHORITATIVE started record (state root, not started.RecordPath).
	startRec, err := requirePersistedRecord(c.StateRoot, layout.recordPath, c.SessionID, contract.Session.StartStatus, "start")
	mustNil(t, err)

	// Tamper the PERSISTED manifest one field at a time (validateMaterialization reads it from disk).
	rejectsEachPersistedManifestField(t, "materialization", mat.ManifestPath, mat.Manifest, []func(*WorkspaceManifest){
		func(m *WorkspaceManifest) { m.Version = 2 },
		func(m *WorkspaceManifest) { m.TargetKind = "x" },
		func(m *WorkspaceManifest) { m.TargetProvider = "x" },
		func(m *WorkspaceManifest) { m.TargetID = "x" },
		func(m *WorkspaceManifest) { m.WorkspaceTransport = "x" },
		func(m *WorkspaceManifest) { m.MaterializationID = "x" },
		func(m *WorkspaceManifest) { m.SourceWorkspace = "x" },
		func(m *WorkspaceManifest) { m.ExcludedPaths = nil },
		func(m *WorkspaceManifest) { m.MaterializedWorkspace = "" },
		func(m *WorkspaceManifest) { m.MaterializedWorkspace = "/different" },
	}, func() error { return validateMaterialization(contract, layout, mat, c) })
	// A MISSING persisted manifest must fail; RESTORE it after so later sub-assertions exercise
	// their real property (an empty-destination reject, not a still-missing manifest).
	matBytes, err := os.ReadFile(mat.ManifestPath)
	mustNil(t, err)
	mustNil(t, os.Remove(mat.ManifestPath))
	if validateMaterialization(contract, layout, mat, c) == nil {
		t.Fatalf("materialization with a missing persisted manifest accepted")
	}
	mustNil(t, os.WriteFile(mat.ManifestPath, matBytes, 0o600))

	// Bootstrap: tamper the PERSISTED manifest (validateBootstrap reads it from disk).
	rejectsEachPersistedManifestField(t, "bootstrap", boot.ManifestPath, boot.Manifest, []func(*BootstrapManifest){
		func(m *BootstrapManifest) { m.Version = 2 },
		func(m *BootstrapManifest) { m.TargetID = "x" },
		func(m *BootstrapManifest) { m.TargetKind = "x" },
		func(m *BootstrapManifest) { m.TargetProvider = "x" },
		func(m *BootstrapManifest) { m.RuntimeAPI = "x" },
		func(m *BootstrapManifest) { m.TargetAssuranceClass = "x" },
		func(m *BootstrapManifest) { m.SupportBoundary = "x" },
		func(m *BootstrapManifest) { m.AccessModel = "x" },
		func(m *BootstrapManifest) { m.BootstrapID = "x" },
		func(m *BootstrapManifest) { m.ImageRef = "x" },
	}, func() error { return validateBootstrap(contract, layout, boot, c) })
	if err := os.Remove(boot.ManifestPath); err == nil {
		if validateBootstrap(contract, layout, boot, c) == nil {
			t.Fatalf("bootstrap with a missing persisted manifest accepted")
		}
	}

	// Started matrix (before FinishSession): each mutation of the authoritative record is rejected.
	rejectsEachField(t, "started", startRec, []func(*sessions.SessionRecord){
		func(r *sessions.SessionRecord) { r.SessionID = "x" },
		func(r *sessions.SessionRecord) { r.Profile = "x" },
		func(r *sessions.SessionRecord) { r.TargetKind = "x" },
		func(r *sessions.SessionRecord) { r.TargetProvider = "x" },
		func(r *sessions.SessionRecord) { r.TargetID = "x" },
		func(r *sessions.SessionRecord) { r.TargetAssuranceClass = "x" },
		func(r *sessions.SessionRecord) { r.RuntimeAPI = "x" },
		func(r *sessions.SessionRecord) { r.WorkspaceTransport = "x" },
		func(r *sessions.SessionRecord) { r.Agent = "x" },
		func(r *sessions.SessionRecord) { r.Mode = "x" },
		func(r *sessions.SessionRecord) { r.Status = "x" },
		func(r *sessions.SessionRecord) { r.Workspace = "x" },
		func(r *sessions.SessionRecord) { r.WorkspaceRoot = "x" },
		func(r *sessions.SessionRecord) { r.WorktreePath = "x" },
		func(r *sessions.SessionRecord) { r.WorkspaceOrigin = "x" },
		func(r *sessions.SessionRecord) { r.AuditLogPath = "x" },
		func(r *sessions.SessionRecord) { r.StartedAt = "x" },
		func(r *sessions.SessionRecord) { r.ObservedAt = "x" },
		func(r *sessions.SessionRecord) { r.InitialAssurance = "x" },
		func(r *sessions.SessionRecord) { r.CurrentAssurance = "x" },
		func(r *sessions.SessionRecord) { r.WorkspaceControlPlane = "x" },
		func(r *sessions.SessionRecord) { r.BootstrapID = "x" },
		func(r *sessions.SessionRecord) { r.ImageRef = "x" },
	}, func(r sessions.SessionRecord) error { return validateStartedSession(contract, layout, r, c) })

	_, err = target.FinishSession(ctx, FinishSessionRequest{Started: started, FinishedAt: c.FinishedAt, ExitStatus: c.ExitStatus})
	mustNil(t, err)
	// The AUTHORITATIVE finished record (state root, not finished.RecordPath).
	finalRec, err := requirePersistedRecord(c.StateRoot, layout.recordPath, c.SessionID, contract.Session.FinalStatus, "finish")
	mustNil(t, err)

	rejectsEachField(t, "finished", finalRec, []func(*sessions.SessionRecord){
		func(r *sessions.SessionRecord) { r.SessionID = "x" },
		func(r *sessions.SessionRecord) { r.TargetID = "x" },
		func(r *sessions.SessionRecord) { r.Status = "x" },
		func(r *sessions.SessionRecord) { r.LiveStatus = "x" },
		func(r *sessions.SessionRecord) { r.FinishedAt = "x" },
		func(r *sessions.SessionRecord) { r.ObservedAt = "x" },
		func(r *sessions.SessionRecord) { r.ExitStatus = "x" },
		func(r *sessions.SessionRecord) { r.FinalAssurance = "x" },
		func(r *sessions.SessionRecord) { r.CurrentAssurance = "x" },
	}, func(r sessions.SessionRecord) error { return validateFinishedSession(contract, r, c) })

	// FinishSession must PRESERVE start-established fields: the real records pass; corrupting a
	// start field (workspace) is rejected; mutating a finish-mutable field (observed_at) is not.
	if err := requireStartFieldsPreserved(startRec, finalRec); err != nil {
		t.Fatalf("real finish wrongly reported a changed start field: %v", err)
	}
	corrupt := finalRec
	corrupt.Workspace = "corrupted"
	if requireStartFieldsPreserved(startRec, corrupt) == nil {
		t.Fatalf("finish that corrupted a start field (workspace) accepted")
	}
	mutable := finalRec
	mutable.ObservedAt = "different"
	if err := requireStartFieldsPreserved(startRec, mutable); err != nil {
		t.Fatalf("a legitimately finish-mutated field (observed_at) tripped the preservation check: %v", err)
	}

	// Plausible manifest over an empty destination must fail.
	mustNil(t, os.RemoveAll(mat.MaterializedWorkspace))
	mustNil(t, os.MkdirAll(mat.MaterializedWorkspace, 0o755))
	if validateMaterialization(contract, layout, mat, c) == nil {
		t.Fatalf("materialization over empty destination accepted")
	}

	// Audit: an event substring near-miss, a missing required id, and a wrong id must all fail.
	audit := func(line string, event string) error {
		return validateAuditEvents([]string{line}, []string{event}, c.auditIDs())
	}
	if audit("ts=1 event=session_started_extra session_id="+c.SessionID+" target_id="+c.TargetID, "session_started") == nil {
		t.Fatalf("audit event substring near-miss accepted")
	}
	if audit("ts=1 event=bootstrap_ready target_id="+c.TargetID+" image_ref="+c.ImageRef, "bootstrap_ready") == nil {
		t.Fatalf("audit line missing required bootstrap_id accepted")
	}
	if audit("ts=1 event=bootstrap_ready bootstrap_id=wrong target_id="+c.TargetID+" image_ref="+c.ImageRef, "bootstrap_ready") == nil {
		t.Fatalf("audit line with wrong bootstrap_id accepted")
	}
	// FIX 2: a duplicate identity field (last-wins would mask the injected evil value)
	// must be rejected.
	if audit("ts=1 event=session_started session_id=evil session_id="+c.SessionID+" target_id="+c.TargetID, "session_started") == nil {
		t.Fatalf("audit line with a duplicate session_id (masking injection) accepted")
	}

	// requireRecordStatus: identity + EXACT status. Post-finish needs the EXACT success status —
	// a still-running record and a terminal-but-FAILED record are both rejected.
	sid := c.SessionID
	if requireRecordStatus(sessions.SessionRecord{SessionID: sid, Status: "running"}, sid, contract.Session.FinalStatus, "finish") == nil {
		t.Fatalf("non-terminal (running) persisted record accepted as finished")
	}
	if requireRecordStatus(sessions.SessionRecord{SessionID: sid, Status: "failed"}, sid, contract.Session.FinalStatus, "finish") == nil {
		t.Fatalf("terminal-as-failed persisted record accepted when success expected")
	}
	if err := requireRecordStatus(sessions.SessionRecord{SessionID: sid, Status: contract.Session.FinalStatus}, sid, contract.Session.FinalStatus, "finish"); err != nil {
		t.Fatalf("exact success final status rejected: %v", err)
	}
	// A divergent identity is rejected even with the right status.
	if requireRecordStatus(sessions.SessionRecord{SessionID: "other", Status: contract.Session.FinalStatus}, sid, contract.Session.FinalStatus, "finish") == nil {
		t.Fatalf("divergent session_id accepted")
	}
	// Post-start requires the started status; a finished record at start time is rejected.
	if err := requireRecordStatus(sessions.SessionRecord{SessionID: sid, Status: contract.Session.StartStatus}, sid, contract.Session.StartStatus, "start"); err != nil {
		t.Fatalf("freshly-started status rejected: %v", err)
	}
	if requireRecordStatus(sessions.SessionRecord{SessionID: sid, Status: contract.Session.FinalStatus}, sid, contract.Session.StartStatus, "start") == nil {
		t.Fatalf("already-finished record accepted as freshly started")
	}
	// A MISSING record is rejected (record path UNDER an empty root so the absent sessions dir is
	// tolerated and the len!=1 check is what rejects).
	emptyRoot := t.TempDir()
	if _, err := requirePersistedRecord(emptyRoot, filepath.Join(emptyRoot, "sessions", sid+".json"), sid, contract.Session.StartStatus, "start"); err == nil {
		t.Fatalf("missing state-root record accepted")
	}
}

// TestConformanceRejectsNonCanonicalReportedPaths: each target-reported path is pinned to its
// derived-canonical value; the real artifacts stay put, so a rejection is due to the decoy path
// alone (neutralize a pin → the decoy passes → FAIL).
func TestConformanceRejectsNonCanonicalReportedPaths(t *testing.T) {
	t.Parallel()
	ctx, target, contract, c, layout := newFixture(t)
	decoy := t.TempDir()
	mat, boot := materializeAndBootstrap(t, ctx, target, c)

	// Sanity: the honest results pass, so a rejection below is due to the decoy path alone.
	mustNil(t, validateMaterialization(contract, layout, mat, c))
	mustNil(t, validateBootstrap(contract, layout, boot, c))

	decoyRoot := filepath.Join(decoy, "targets", "local_vm", "apple", c.TargetID)
	rejectsEachField(t, "materialization paths", mat, []func(*MaterializeResult){
		func(r *MaterializeResult) { r.TargetRoot = decoyRoot },
		func(r *MaterializeResult) { r.MaterializationRoot = filepath.Join(decoy, "materializations", "x") },
		func(r *MaterializeResult) { r.ManifestPath = filepath.Join(decoy, "materialization.json") },
		func(r *MaterializeResult) { r.MaterializedWorkspace = filepath.Join(decoy, "worktree") },
	}, func(r MaterializeResult) error { return validateMaterialization(contract, layout, r, c) })

	rejectsEachField(t, "bootstrap paths", boot, []func(*BootstrapResult){
		func(r *BootstrapResult) { r.TargetRoot = decoyRoot },
		func(r *BootstrapResult) { r.ManifestPath = filepath.Join(decoy, "bootstrap.json") },
		func(r *BootstrapResult) { r.AuditLogPath = filepath.Join(decoy, "workcell.audit.log") },
	}, func(r BootstrapResult) error { return validateBootstrap(contract, layout, r, c) })
}

// TestConformanceRejectsBogusReturnedStructs: a backend that persists correctly but returns a bogus
// in-memory handle (SessionResult.Record/RecordPath/AuditLogPath) or manifest struct must be
// rejected — each returned field is pinned to the persisted state. Neutralize a pin → passes → FAIL.
func TestConformanceRejectsBogusReturnedStructs(t *testing.T) {
	t.Parallel()
	ctx, target, contract, c, layout := newFixture(t)
	mat, boot := materializeAndBootstrap(t, ctx, target, c)
	started, err := target.StartSession(ctx, StartSessionRequest{SessionID: c.SessionID, Agent: c.Agent, Mode: c.Mode, StartedAt: c.StartedAt, Materialization: mat, Bootstrap: boot})
	mustNil(t, err)
	startRec, err := requirePersistedRecord(c.StateRoot, layout.recordPath, c.SessionID, contract.Session.StartStatus, "start")
	mustNil(t, err)

	// Returned SessionResult handles must match the authoritative persisted record (FIX 1); the
	// returned Manifest structs must match the persisted manifests (FIX 2, on-disk stays valid).
	rejectsEachField(t, "returned handles", started, []func(*SessionResult){
		func(r *SessionResult) { r.RecordPath = filepath.Join(t.TempDir(), "decoy.json") },
		func(r *SessionResult) { r.AuditLogPath = filepath.Join(t.TempDir(), "decoy.log") },
		func(r *SessionResult) { r.Record.Agent = "returned-bogus" },
	}, func(r SessionResult) error { return requireReturnedHandlesMatch(r, startRec, layout, "start") })
	rejectsEachField(t, "returned materialization manifest", mat, []func(*MaterializeResult){
		func(r *MaterializeResult) { r.Manifest.SourceWorkspace = "decoy" },
		func(r *MaterializeResult) { r.Manifest.Version = 99 },
	}, func(r MaterializeResult) error { return validateMaterialization(contract, layout, r, c) })
	rejectsEachField(t, "returned bootstrap manifest", boot, []func(*BootstrapResult){
		func(r *BootstrapResult) { r.Manifest.ImageRef = "decoy" },
	}, func(r BootstrapResult) error { return validateBootstrap(contract, layout, r, c) })

	// The FINISH returned handles must ALSO match the authoritative persisted final record — the
	// same pins, exercised against the finish-side record (RunConformance checks both phases).
	finished, err := target.FinishSession(ctx, FinishSessionRequest{Started: started, FinishedAt: c.FinishedAt, ExitStatus: c.ExitStatus})
	mustNil(t, err)
	finalRec, err := requirePersistedRecord(c.StateRoot, layout.recordPath, c.SessionID, contract.Session.FinalStatus, "finish")
	mustNil(t, err)
	rejectsEachField(t, "finish handles", finished, []func(*SessionResult){
		func(r *SessionResult) { r.RecordPath = filepath.Join(t.TempDir(), "decoy.json") },
		func(r *SessionResult) { r.AuditLogPath = filepath.Join(t.TempDir(), "decoy.log") },
		func(r *SessionResult) { r.Record.Agent = "finish-bogus" },
	}, func(r SessionResult) error { return requireReturnedHandlesMatch(r, finalRec, layout, "finish") })
}

// bogusReturnedRecordTarget corrupts the Record returned from Start and/or Finish (persisting
// correctly), modelling a target that hands its caller a bogus in-memory record.
type bogusReturnedRecordTarget struct {
	ConformanceTarget
	corruptStart, corruptFinish bool
}

func (b bogusReturnedRecordTarget) StartSession(ctx context.Context, req StartSessionRequest) (SessionResult, error) {
	res, err := b.ConformanceTarget.StartSession(ctx, req)
	if err == nil && b.corruptStart {
		res.Record.Agent = "start-bogus"
	}
	return res, err
}

func (b bogusReturnedRecordTarget) FinishSession(ctx context.Context, req FinishSessionRequest) (SessionResult, error) {
	res, err := b.ConformanceTarget.FinishSession(ctx, req)
	if err == nil && b.corruptFinish {
		res.Record.Agent = "finish-bogus"
	}
	return res, err
}

// TestConformanceRejectsBogusReturnedHandleEndToEnd (FIX 2): RunConformance validates the RETURNED
// SessionResult after BOTH StartSession and FinishSession. A wrapper returning a bogus Record at
// either phase must be rejected with the phase-specific error. Neutralize the corresponding
// requireReturnedHandlesMatch call in RunConformance → that bad handle passes → FAIL.
func TestConformanceRejectsBogusReturnedHandleEndToEnd(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, phase   string
		start, finish bool
	}{
		{"start", "after StartSession", true, false},
		{"finish", "after FinishSession", false, true},
	} {
		_, real, contract, c, _ := newFixture(t)
		target := bogusReturnedRecordTarget{ConformanceTarget: real, corruptStart: tc.start, corruptFinish: tc.finish}
		_, err := RunConformance(context.Background(), target, contract, c)
		if err == nil || !strings.Contains(err.Error(), tc.phase) {
			t.Fatalf("%s: bogus returned handle accepted: %v", tc.name, err)
		}
	}
}

// TestConformanceManifestFieldPinsRejectTampering (L43): each manifest field pin in
// validateMaterialization/validateBootstrap must independently reject a value tampered CONSISTENTLY
// in both the returned struct and the persisted file (so the returned-vs-persisted check passes and
// the field pin is the rejecter). Neutralize a field pin → its case passes → FAIL.
func TestConformanceManifestFieldPinsRejectTampering(t *testing.T) {
	t.Parallel()
	ctx, target, contract, c, layout := newFixture(t)
	mat, boot := materializeAndBootstrap(t, ctx, target, c)

	rejectsEachManifestFieldPin(t, "materialization", layout.materializationManifest, mat,
		func(r *MaterializeResult) *WorkspaceManifest { return &r.Manifest },
		[]func(*WorkspaceManifest){
			func(m *WorkspaceManifest) { m.Version = 2 },
			func(m *WorkspaceManifest) { m.TargetKind = "x" },
			func(m *WorkspaceManifest) { m.TargetProvider = "x" },
			func(m *WorkspaceManifest) { m.TargetID = "x" },
			func(m *WorkspaceManifest) { m.WorkspaceTransport = "x" },
			func(m *WorkspaceManifest) { m.MaterializationID = "x" },
			func(m *WorkspaceManifest) { m.SourceWorkspace = "x" },
			func(m *WorkspaceManifest) { m.MaterializedWorkspace = "/different" },
			func(m *WorkspaceManifest) { m.ExcludedPaths = nil },
		},
		func(r MaterializeResult) error { return validateMaterialization(contract, layout, r, c) })

	rejectsEachManifestFieldPin(t, "bootstrap", layout.bootstrapManifest, boot,
		func(r *BootstrapResult) *BootstrapManifest { return &r.Manifest },
		[]func(*BootstrapManifest){
			func(m *BootstrapManifest) { m.Version = 2 },
			func(m *BootstrapManifest) { m.TargetID = "x" },
			func(m *BootstrapManifest) { m.TargetKind = "x" },
			func(m *BootstrapManifest) { m.TargetProvider = "x" },
			func(m *BootstrapManifest) { m.RuntimeAPI = "x" },
			func(m *BootstrapManifest) { m.TargetAssuranceClass = "x" },
			func(m *BootstrapManifest) { m.SupportBoundary = "x" },
			func(m *BootstrapManifest) { m.AccessModel = "x" },
			func(m *BootstrapManifest) { m.BootstrapID = "x" },
			func(m *BootstrapManifest) { m.ImageRef = "x" },
		},
		func(r BootstrapResult) error { return validateBootstrap(contract, layout, r, c) })
}

// injectDupKey smuggles a duplicate target_id (evil) into the JSON at path; last-wins decoding
// keeps the real value, so only a raw duplicate-key scan catches it.
// injectFirstKey inserts kv (a `"key":value` pair) into the first JSON object at path.
func injectFirstKey(t *testing.T, path, kv string) {
	t.Helper()
	data, err := os.ReadFile(path)
	mustNil(t, err)
	mustNil(t, os.WriteFile(path, []byte(strings.Replace(string(data), "{", "{"+kv+",", 1)), 0o600))
}

// TestConformanceRejectsDuplicateJSONKeys: encoding/json is LAST-WINS on duplicate keys, so a
// smuggled `"target_id":"evil"` decodes cleanly and passes field validation — the raw duplicate-key
// scan rejects it in both the record and the manifest. Neutralize the scan → accepted → FAIL.
func TestConformanceRejectsDuplicateJSONKeys(t *testing.T) {
	t.Parallel()
	// Helper correctness: clean nested JSON passes; a dup key at any nesting level is rejected.
	mustNil(t, rejectDuplicateJSONKeys([]byte(`{"a":1,"b":{"c":2},"d":[{"e":3}]}`)))
	for _, bad := range []string{`{"a":1,"a":2}`, `{"o":{"k":1,"k":2}}`, `[{"y":1,"y":2}]`} {
		if rejectDuplicateJSONKeys([]byte(bad)) == nil {
			t.Fatalf("duplicate key not rejected: %s", bad)
		}
	}

	ctx, target, contract, c, layout := newFixture(t)
	mat, boot := materializeAndBootstrap(t, ctx, target, c)
	_, err := target.StartSession(ctx, StartSessionRequest{SessionID: c.SessionID, Agent: c.Agent, Mode: c.Mode, StartedAt: c.StartedAt, Materialization: mat, Bootstrap: boot})
	mustNil(t, err)

	// Duplicate key in the persisted RECORD → rejected by verifyPersistedRecordCanonical.
	injectFirstKey(t, layout.recordPath, `"target_id":"evil"`)
	if err := verifyPersistedRecordCanonical(c.StateRoot, layout.recordPath); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate-key record accepted: %v", err)
	}
	// Duplicate key in the persisted MANIFEST → rejected by validateMaterialization's read.
	injectFirstKey(t, layout.materializationManifest, `"target_id":"evil"`)
	if err := validateMaterialization(contract, layout, mat, c); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate-key manifest accepted: %v", err)
	}
}

// TestConformanceRejectsUnknownManifestKey (L79): a persisted v1 manifest carrying an extra/unknown
// key must be rejected by the strict decode (DisallowUnknownFields). Neutralize (plain Unmarshal
// drops the extra key) → accepted → FAIL.
func TestConformanceRejectsUnknownManifestKey(t *testing.T) {
	t.Parallel()
	ctx, target, contract, c, layout := newFixture(t)
	mat, boot := materializeAndBootstrap(t, ctx, target, c)
	injectFirstKey(t, layout.materializationManifest, `"unknown_extra":"x"`)
	if err := validateMaterialization(contract, layout, mat, c); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("materialization manifest with an unknown key accepted: %v", err)
	}
	injectFirstKey(t, layout.bootstrapManifest, `"unknown_extra":"x"`)
	if err := validateBootstrap(contract, layout, boot, c); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("bootstrap manifest with an unknown key accepted: %v", err)
	}
}

// TestConformanceRejectsTrailingManifestData (L88): a valid manifest object FOLLOWED by trailing
// data — an unmatched delimiter (`}`/`]`, which dec.More() misses), another object, or a bare token
// — must be rejected by the strict io.EOF check. Neutralize (weaker dec.More() check) → the
// trailing-`}` case is accepted → FAIL.
func TestConformanceRejectsTrailingManifestData(t *testing.T) {
	t.Parallel()
	for _, trailing := range []string{"}", "]", `{"x":1}`, "5"} {
		ctx, target, contract, c, layout := newFixture(t)
		mat, _ := materializeAndBootstrap(t, ctx, target, c)
		data, err := os.ReadFile(layout.materializationManifest)
		mustNil(t, err)
		mustNil(t, os.WriteFile(layout.materializationManifest, append(data, []byte(trailing)...), 0o600))
		if err := validateMaterialization(contract, layout, mat, c); err == nil || !strings.Contains(err.Error(), "trailing") {
			t.Fatalf("manifest with trailing %q accepted: %v", trailing, err)
		}
	}
}

// TestConformanceRejectsHardlinkedWorkspaceFile (L506): a certified workspace regular file that is
// hard-linked (Nlink==2) to a file OUTSIDE the workspace — identical content+mode, so the tree
// comparison still matches — must be rejected by the Nlink==1 check. Neutralize (skip the check) →
// accepted → FAIL.
func TestConformanceRejectsHardlinkedWorkspaceFile(t *testing.T) {
	t.Parallel()
	ctx, target, contract, c, layout := newFixture(t)
	mat, _ := materializeAndBootstrap(t, ctx, target, c)

	var rel string
	for _, e := range mat.Manifest.Entries {
		if e.Kind == "file" {
			rel = e.Path
			break
		}
	}
	if rel == "" {
		t.Fatal("no regular file in the sample workspace")
	}
	wp := filepath.Join(layout.materializedWorkspace, filepath.FromSlash(rel))
	content, err := os.ReadFile(wp)
	mustNil(t, err)
	info, err := os.Stat(wp)
	mustNil(t, err)
	// A valid outside file with identical content+mode, then hardlink the workspace path at it
	// (Nlink==2) so ONLY the Nlink check can reject.
	outside := filepath.Join(t.TempDir(), "outside")
	mustNil(t, os.WriteFile(outside, content, info.Mode().Perm()))
	mustNil(t, os.Chmod(outside, info.Mode().Perm()))
	mustNil(t, os.Remove(wp))
	mustNil(t, os.Link(outside, wp))

	if err := validateMaterialization(contract, layout, mat, c); err == nil || !strings.Contains(err.Error(), "multiply linked") {
		t.Fatalf("hardlinked workspace file accepted: %v", err)
	}
}

// decoyRecordPathTarget corrupts a start field on the ACTUAL state-root record while returning a
// pristine decoy RecordPath — a target reporting a good RecordPath while the authority diverges.
type decoyRecordPathTarget struct {
	ConformanceTarget
	decoyDir string
}

func (d decoyRecordPathTarget) StartSession(ctx context.Context, req StartSessionRequest) (SessionResult, error) {
	res, err := d.ConformanceTarget.StartSession(ctx, req)
	if err != nil {
		return res, err
	}
	good, err := os.ReadFile(res.RecordPath)
	if err != nil {
		return res, err
	}
	decoy := filepath.Join(d.decoyDir, "decoy.json")
	if err := os.WriteFile(decoy, good, 0o600); err != nil {
		return res, err
	}
	rec, err := sessions.ReadSessionRecord(res.RecordPath)
	if err != nil {
		return res, err
	}
	rec.Workspace = "corrupted-by-decoy-target" // divergent from the manifest workspace
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return res, err
	}
	if err := os.WriteFile(res.RecordPath, append(data, '\n'), 0o600); err != nil {
		return res, err
	}
	res.RecordPath = decoy // report the pristine decoy, hiding the corrupted state-root record
	return res, nil
}

// TestConformanceUsesAuthoritativeRecordNotRecordPath: a target that returns a pristine decoy
// RecordPath while the authoritative state-root record has a corrupted start field must be REJECTED
// (validating RecordPath instead would hide the corruption).
func TestConformanceUsesAuthoritativeRecordNotRecordPath(t *testing.T) {
	t.Parallel()
	source := writeSampleWorkspace(t)
	real, err := NewAppleContainerTarget(Contract{})
	mustNil(t, err)
	contract := DefaultContract()
	c := DefaultConformanceCase(t.TempDir(), source)
	target := decoyRecordPathTarget{ConformanceTarget: real, decoyDir: t.TempDir()}
	_, err = RunConformance(context.Background(), target, contract, c)
	if err == nil || !strings.Contains(err.Error(), "workspace") {
		t.Fatalf("conformance accepted a corrupted state-root record hidden behind a decoy RecordPath: %v", err)
	}
}

// newFixture builds a fresh honest target + contract + case + derived layout.
func newFixture(t *testing.T) (context.Context, ConformanceTarget, Contract, ConformanceCase, canonicalLayout) {
	t.Helper()
	target, err := NewAppleContainerTarget(Contract{})
	mustNil(t, err)
	contract := DefaultContract()
	c := DefaultConformanceCase(t.TempDir(), writeSampleWorkspace(t))
	return context.Background(), target, contract, c, deriveLayout(contract, c)
}

// materializeAndBootstrap runs the two pre-session steps and returns their results.
func materializeAndBootstrap(t *testing.T, ctx context.Context, target ConformanceTarget, c ConformanceCase) (MaterializeResult, BootstrapResult) {
	t.Helper()
	mat, err := target.MaterializeWorkspace(ctx, MaterializeRequest{StateRoot: c.StateRoot, TargetID: c.TargetID, MaterializationID: c.MaterializationID, SourceWorkspace: c.SourceWorkspace})
	mustNil(t, err)
	boot, err := target.BootstrapTarget(ctx, BootstrapRequest{StateRoot: c.StateRoot, TargetID: c.TargetID, BootstrapID: c.BootstrapID, ImageRef: c.ImageRef})
	mustNil(t, err)
	return mat, boot
}

// symlinkLifecycle materializes + bootstraps a sanity-checked valid state for the symlink tests.
func symlinkLifecycle(t *testing.T) (context.Context, ConformanceTarget, Contract, ConformanceCase, canonicalLayout, MaterializeResult, BootstrapResult) {
	t.Helper()
	ctx, target, contract, c, layout := newFixture(t)
	mat, boot := materializeAndBootstrap(t, ctx, target, c)
	mustNil(t, validateMaterialization(contract, layout, mat, c))
	mustNil(t, validateBootstrap(contract, layout, boot, c))
	return ctx, target, contract, c, layout, mat, boot
}

// symlinkToDecoy renames real to a valid decoy outside StateRoot, then symlinks real at it (so ONLY
// a no-follow guard can reject).
func symlinkToDecoy(t *testing.T, real, decoy string) {
	t.Helper()
	mustNil(t, os.Rename(real, decoy))
	mustNil(t, os.Symlink(decoy, real))
}

// TestConformanceRejectsSymlinkedManifest: a symlink at the canonical materialization/bootstrap
// manifest path (→ VALID decoy outside StateRoot) must be rejected by the no-follow read.
// Neutralizing readPersistedManifest to os.ReadFile → the decoy is followed → FAIL.
func TestConformanceRejectsSymlinkedManifest(t *testing.T) {
	t.Parallel()
	_, _, contract, c, layout, mat, boot := symlinkLifecycle(t)
	outside := t.TempDir()

	symlinkToDecoy(t, layout.materializationManifest, filepath.Join(outside, "materialization.json"))
	if validateMaterialization(contract, layout, mat, c) == nil {
		t.Fatalf("symlinked materialization manifest (valid decoy outside StateRoot) accepted")
	}
	symlinkToDecoy(t, layout.bootstrapManifest, filepath.Join(outside, "bootstrap.json"))
	if validateBootstrap(contract, layout, boot, c) == nil {
		t.Fatalf("symlinked bootstrap manifest (valid decoy outside StateRoot) accepted")
	}
}

// TestConformanceRejectsSymlinkedWorkspaceRoot: a symlinked materialized-workspace root (→ VALID
// decoy tree outside StateRoot) must be rejected, else copyWorkspaceTree EvalSymlinks-es it and
// certifies the decoy. The manifest stays real, isolating the workspace-root guard. Neutralize →
// decoy walked → FAIL.
func TestConformanceRejectsSymlinkedWorkspaceRoot(t *testing.T) {
	t.Parallel()
	_, _, contract, c, layout, mat, _ := symlinkLifecycle(t)
	symlinkToDecoy(t, layout.materializedWorkspace, filepath.Join(t.TempDir(), "worktree"))
	if validateMaterialization(contract, layout, mat, c) == nil {
		t.Fatalf("symlinked materialized workspace root (valid decoy tree outside StateRoot) accepted")
	}
}

// redirectAuditLogTarget, after FinishSession writes the audit log, moves it to a valid decoy
// outside StateRoot and re-points the canonical path at it via link (os.Symlink or os.Link).
type redirectAuditLogTarget struct {
	ConformanceTarget
	auditPath, decoyDir string
	link                func(oldname, newname string) error
}

func (r redirectAuditLogTarget) FinishSession(ctx context.Context, req FinishSessionRequest) (SessionResult, error) {
	res, err := r.ConformanceTarget.FinishSession(ctx, req)
	if err != nil {
		return res, err
	}
	decoy := filepath.Join(r.decoyDir, "workcell.audit.log")
	if err := os.Rename(r.auditPath, decoy); err != nil {
		return res, err
	}
	return res, r.link(decoy, r.auditPath)
}

// TestConformanceRejectsSymlinkedAuditLog: the export follows record.AuditLogPath (os.ReadFile), so
// a symlinked canonical audit log (→ VALID decoy outside StateRoot) must be rejected by the
// no-follow pre-check. Neutralize → the symlink is followed → FAIL.
func TestConformanceRejectsSymlinkedAuditLog(t *testing.T) {
	t.Parallel()
	_, real, contract, c, layout := newFixture(t)
	target := redirectAuditLogTarget{ConformanceTarget: real, auditPath: layout.auditLog, decoyDir: t.TempDir(), link: os.Symlink}
	_, err := RunConformance(context.Background(), target, contract, c)
	if err == nil || !strings.Contains(err.Error(), "audit log") {
		t.Fatalf("conformance accepted a symlinked audit log (valid decoy outside StateRoot): %v", err)
	}
}

// TestConformanceRejectsHardlinkedAuditLog: a hardlinked canonical audit log (Nlink==2, same
// content) passes an S_IFREG-only check, so the pre-check also requires Nlink==1. Neutralize the
// Nlink check → accepted → FAIL.
func TestConformanceRejectsHardlinkedAuditLog(t *testing.T) {
	t.Parallel()
	_, real, contract, c, layout := newFixture(t)
	target := redirectAuditLogTarget{ConformanceTarget: real, auditPath: layout.auditLog, decoyDir: t.TempDir(), link: os.Link}
	_, err := RunConformance(context.Background(), target, contract, c)
	if err == nil || !strings.Contains(err.Error(), "multiply linked") {
		t.Fatalf("conformance accepted a hardlinked audit log (valid decoy outside StateRoot): %v", err)
	}
}

// nonCanonicalRecordTarget, after StartSession, blanks a normalizable field (target_kind) in the
// persisted record, relying on the reader to backfill it — the listed record then looks canonical
// but the RAW persisted bytes are not.
type nonCanonicalRecordTarget struct {
	ConformanceTarget
	recordPath string
}

func (n nonCanonicalRecordTarget) StartSession(ctx context.Context, req StartSessionRequest) (SessionResult, error) {
	res, err := n.ConformanceTarget.StartSession(ctx, req)
	if err != nil {
		return res, err
	}
	rec, err := sessions.ReadSessionRecord(n.recordPath)
	if err != nil {
		return res, err
	}
	rec.TargetKind = "" // rely on reader normalization to backfill it
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return res, err
	}
	return res, os.WriteFile(n.recordPath, append(data, '\n'), 0o600)
}

// TestConformanceRejectsNonCanonicalRecord: a non-canonical persisted record (a normalizable field
// left for the reader to backfill) passes every field pin once normalized, so only the RAW
// canonical-bytes check catches it. Neutralize raw!=normalized → accepted → FAIL.
func TestConformanceRejectsNonCanonicalRecord(t *testing.T) {
	t.Parallel()
	_, real, contract, c, layout := newFixture(t)
	target := nonCanonicalRecordTarget{ConformanceTarget: real, recordPath: layout.recordPath}
	_, err := RunConformance(context.Background(), target, contract, c)
	if err == nil || !strings.Contains(err.Error(), "canonical") {
		t.Fatalf("conformance accepted a non-canonical persisted record (reader-normalized): %v", err)
	}
}

// TestConformanceRejectsFIFOSessionRecord (FIX 1 / L318): a FIFO masquerading as a record — here in
// a SIBLING discoverable session dir (the case the canonical-only scan missed) — must be rejected
// by the no-follow pre-scan BEFORE the listing's os.ReadFile blocks on it. Neutralize → hang →
// mustNotHang trips → FAIL.
func TestConformanceRejectsFIFOSessionRecord(t *testing.T) {
	t.Parallel()
	ctx, target, contract, c, layout := newFixture(t)
	mat, boot := materializeAndBootstrap(t, ctx, target, c)
	_, err := target.StartSession(ctx, StartSessionRequest{SessionID: c.SessionID, Agent: c.Agent, Mode: c.Mode, StartedAt: c.StartedAt, Materialization: mat, Bootstrap: boot})
	mustNil(t, err)

	// Drop a FIFO masquerading as a record in a SIBLING discoverable session dir (NOT the
	// canonical one): StateDirs also enumerates every top-level dir under the state root, so the
	// listing would walk <root>/sibling/sessions and block on the FIFO there.
	siblingSessions := filepath.Join(c.StateRoot, "sibling", "sessions")
	mustNil(t, os.MkdirAll(siblingSessions, 0o755))
	mkfifoOrSkip(t, filepath.Join(siblingSessions, "hang.json"))

	err = mustNotHang(t, "FIFO .json in a sibling session dir hung the listing — special-file guard missing", func() error {
		_, e := requirePersistedRecord(c.StateRoot, layout.recordPath, c.SessionID, contract.Session.StartStatus, "fifo")
		return e
	})
	if err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("FIFO .json in a sibling session dir not rejected before a blocking open: %v", err)
	}
}
