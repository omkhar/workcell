// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package applecontainer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/omkhar/workcell/internal/host/sessions"
)

// runToStart materializes a workspace at src under state and starts a session,
// returning the materialization result and the audit log contents.
func runToStart(t *testing.T, src, state string) (MaterializeResult, string) {
	t.Helper()
	ctx := context.Background()
	target, err := NewAppleContainerTarget(Contract{})
	mustNil(t, err)
	mat, err := target.MaterializeWorkspace(ctx, MaterializeRequest{StateRoot: state, TargetID: "tid", MaterializationID: "mid", SourceWorkspace: src})
	mustNil(t, err)
	boot, err := target.BootstrapTarget(ctx, BootstrapRequest{StateRoot: state, TargetID: "tid", BootstrapID: "bid", ImageRef: "img:1"})
	mustNil(t, err)
	started, err := target.StartSession(ctx, StartSessionRequest{SessionID: "sid", Agent: "codex", Mode: "strict", StartedAt: "2026", Materialization: mat, Bootstrap: boot})
	mustNil(t, err)
	data, err := os.ReadFile(started.AuditLogPath)
	mustNil(t, err)
	return mat, string(data)
}

// auditField returns the raw value of key in the first audit line for event.
func auditField(t *testing.T, log, event, key string) string {
	t.Helper()
	for _, line := range strings.Split(log, "\n") {
		if !strings.Contains(line, "event="+event+" ") && !strings.HasSuffix(line, "event="+event) {
			continue
		}
		for _, tok := range strings.Fields(line) {
			if k, v, ok := strings.Cut(tok, "="); ok && k == key {
				return v
			}
		}
	}
	t.Fatalf("no %s in event=%s line of:\n%s", key, event, log)
	return ""
}

// TestStartSessionRejectsMismatchedTargetIDs: a materialization and bootstrap from
// different targets must not compose into one session; matching ids succeed.
func TestStartSessionRejectsMismatchedTargetIDs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	target, err := NewAppleContainerTarget(Contract{})
	mustNil(t, err)
	source := writeSampleWorkspace(t)
	rootA := t.TempDir()
	matA, err := target.MaterializeWorkspace(ctx, MaterializeRequest{StateRoot: rootA, TargetID: "target-a", MaterializationID: "mid", SourceWorkspace: source})
	mustNil(t, err)

	// Mismatched target ids → rejected.
	bootB, err := target.BootstrapTarget(ctx, BootstrapRequest{StateRoot: t.TempDir(), TargetID: "target-b", BootstrapID: "bid", ImageRef: "img:1"})
	mustNil(t, err)
	if _, e := target.StartSession(ctx, StartSessionRequest{SessionID: "sid", Agent: "codex", Mode: "strict", StartedAt: "2026", Materialization: matA, Bootstrap: bootB}); e == nil {
		t.Fatalf("mismatched materialization/bootstrap target ids accepted")
	}

	// Same target id but a different state root → rejected (evidence would split).
	bootADiffRoot, err := target.BootstrapTarget(ctx, BootstrapRequest{StateRoot: t.TempDir(), TargetID: "target-a", BootstrapID: "bid", ImageRef: "img:1"})
	mustNil(t, err)
	if _, e := target.StartSession(ctx, StartSessionRequest{SessionID: "sid", Agent: "codex", Mode: "strict", StartedAt: "2026", Materialization: matA, Bootstrap: bootADiffRoot}); e == nil {
		t.Fatalf("mismatched materialization/bootstrap target roots accepted")
	}

	// Same target id AND same state root (as the conformance harness uses) → accepted.
	bootA, err := target.BootstrapTarget(ctx, BootstrapRequest{StateRoot: rootA, TargetID: "target-a", BootstrapID: "bid", ImageRef: "img:1"})
	mustNil(t, err)
	if _, e := target.StartSession(ctx, StartSessionRequest{SessionID: "sid", Agent: "codex", Mode: "strict", StartedAt: "2026", Materialization: matA, Bootstrap: bootA}); e != nil {
		t.Fatalf("matching target id and root rejected: %v", e)
	}
}

// TestStartSessionRejectsNonCanonicalTargetRoot: a self-consistent request whose
// TargetRoot is not the canonical targets/<kind>/<provider>/<id> is rejected.
func TestStartSessionRejectsNonCanonicalTargetRoot(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	target, err := NewAppleContainerTarget(Contract{})
	mustNil(t, err)
	source := writeSampleWorkspace(t)
	state := t.TempDir()
	mat, err := target.MaterializeWorkspace(ctx, MaterializeRequest{StateRoot: state, TargetID: "tid", MaterializationID: "mid", SourceWorkspace: source})
	mustNil(t, err)
	boot, err := target.BootstrapTarget(ctx, BootstrapRequest{StateRoot: state, TargetID: "tid", BootstrapID: "bid", ImageRef: "img:1"})
	mustNil(t, err)

	// The genuine canonical layout starts.
	if _, e := target.StartSession(ctx, StartSessionRequest{SessionID: "sid-ok", Agent: "codex", Mode: "strict", StartedAt: "2026", Materialization: mat, Bootstrap: boot}); e != nil {
		t.Fatalf("canonical target root rejected: %v", e)
	}

	// Relocate the tree to a non-canonical leaf and rewrite the self-consistent
	// paths to it: every path-derived guard passes, only the layout check catches it.
	oldRoot := boot.TargetRoot
	newRoot := oldRoot + "-evil"
	mustNil(t, os.Rename(oldRoot, newRoot))
	fix := func(s string) string { return strings.Replace(s, oldRoot, newRoot, 1) }
	mat.TargetRoot = fix(mat.TargetRoot)
	mat.MaterializationRoot = fix(mat.MaterializationRoot)
	mat.ManifestPath = fix(mat.ManifestPath)
	mat.MaterializedWorkspace = fix(mat.MaterializedWorkspace)
	mat.Manifest.MaterializedWorkspace = fix(mat.Manifest.MaterializedWorkspace)
	boot.TargetRoot = fix(boot.TargetRoot)
	boot.ManifestPath = fix(boot.ManifestPath)
	boot.AuditLogPath = fix(boot.AuditLogPath)
	// Rewrite the on-disk manifest's materialized_workspace so the byte check still
	// matches — the attacker controls the relocated tree, so it stays consistent.
	manifestBytes, err := os.ReadFile(mat.ManifestPath)
	mustNil(t, err)
	mustNil(t, os.WriteFile(mat.ManifestPath, []byte(strings.ReplaceAll(string(manifestBytes), oldRoot, newRoot)), 0o600))

	if _, e := target.StartSession(ctx, StartSessionRequest{SessionID: "sid-evil", Agent: "codex", Mode: "strict", StartedAt: "2026", Materialization: mat, Bootstrap: boot}); e == nil {
		t.Fatalf("StartSession accepted a non-canonical target root")
	} else if !strings.Contains(e.Error(), "canonical") {
		t.Fatalf("expected canonical-layout rejection, got: %v", e)
	}
}

func TestStartSessionRejectsMissingWorkspace(t *testing.T) {
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
	// Remove the materialized workspace after materialization but before start.
	mustNil(t, os.RemoveAll(mat.MaterializedWorkspace))
	if _, e := target.StartSession(ctx, StartSessionRequest{SessionID: "sid", Agent: "codex", Mode: "strict", StartedAt: "2026", Materialization: mat, Bootstrap: boot}); e == nil {
		t.Fatalf("start session accepted a removed materialized workspace")
	}
}

// TestSessionTokenRejection: started_at, finished_at, and exit_status reject
// whitespace/control (audit-line injection prevention).
func TestSessionTokenRejection(t *testing.T) {
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

	// started_at rejections fail before the record is written (exactly-once only on success).
	for _, v := range []string{"a\nb", "a b", "a\tb", "a\rb"} {
		if _, e := target.StartSession(ctx, StartSessionRequest{SessionID: "sid", Agent: "codex", Mode: "strict", StartedAt: v, Materialization: mat, Bootstrap: boot}); e == nil {
			t.Fatalf("started_at %q accepted", v)
		}
	}
	started, err := target.StartSession(ctx, StartSessionRequest{SessionID: "sid", Agent: "codex", Mode: "strict", StartedAt: "2026", Materialization: mat, Bootstrap: boot})
	mustNil(t, err)
	// finished_at/exit_status rejections fail before finalization; session stays retryable.
	for _, v := range []string{"a\nb", "a b", "a\tb", "a\rb"} {
		if _, e := target.FinishSession(ctx, FinishSessionRequest{Started: started, FinishedAt: v, ExitStatus: "0"}); e == nil {
			t.Fatalf("finished_at %q accepted", v)
		}
		if _, e := target.FinishSession(ctx, FinishSessionRequest{Started: started, FinishedAt: "2027", ExitStatus: v}); e == nil {
			t.Fatalf("exit_status %q accepted", v)
		}
	}
}

// TestSessionLifecycleIdempotency: a clean lifecycle succeeds; a second Start and a
// second Finish both return idempotently (no duplicate start triplet or finish
// event), so no lifecycle audit event is ever duplicated.
func TestSessionLifecycleIdempotency(t *testing.T) {
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
	startReq := StartSessionRequest{SessionID: "sid", Agent: "codex", Mode: "strict", StartedAt: "2026", Materialization: mat, Bootstrap: boot}

	started, err := target.StartSession(ctx, startReq)
	mustNil(t, err)
	// A retry of a fully-started session returns idempotently (no duplicate triplet).
	if _, e := target.StartSession(ctx, startReq); e != nil {
		t.Fatalf("idempotent StartSession retry rejected: %v", e)
	}
	if data, _ := os.ReadFile(started.AuditLogPath); strings.Count(string(data), "event=workspace_materialized") != 1 {
		t.Fatalf("duplicate start audit triplet:\n%s", data)
	}

	finishReq := FinishSessionRequest{Started: started, FinishedAt: "2027", ExitStatus: "0"}
	if _, e := target.FinishSession(ctx, finishReq); e != nil {
		t.Fatalf("clean finish rejected: %v", e)
	}
	// A retry of a finished session returns idempotently (no duplicate finish event).
	if _, e := target.FinishSession(ctx, finishReq); e != nil {
		t.Fatalf("idempotent FinishSession retry rejected: %v", e)
	}
	if data, _ := os.ReadFile(started.AuditLogPath); strings.Count(string(data), "event=session_finished") != 1 {
		t.Fatalf("duplicate finish audit event:\n%s", data)
	}
}

// TestStartSessionRevalidatesManifestAuditTokens: a manifest token with
// whitespace/newline (in materialization_id/bootstrap_id/image_ref/target_id) is
// rejected with no audit written.
func TestStartSessionRevalidatesManifestAuditTokens(t *testing.T) {
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
	base := StartSessionRequest{SessionID: "sid", Agent: "codex", Mode: "strict", StartedAt: "2026", Materialization: mat, Bootstrap: boot}

	for i, tamper := range []func(*StartSessionRequest){
		func(r *StartSessionRequest) { r.Bootstrap.Manifest.ImageRef = "img\n1" },
		func(r *StartSessionRequest) { r.Bootstrap.Manifest.BootstrapID = "b id" },
		func(r *StartSessionRequest) { r.Materialization.Manifest.MaterializationID = "m\tid" },
		func(r *StartSessionRequest) { r.Bootstrap.Manifest.TargetID = "t\rid" },
	} {
		bad := base
		tamper(&bad)
		if _, e := target.StartSession(ctx, bad); e == nil {
			t.Fatalf("tamper %d: start accepted a manifest token with whitespace/control", i)
		}
	}
	if _, err := os.Stat(boot.AuditLogPath); !os.IsNotExist(err) {
		t.Fatalf("audit log written despite rejected manifest tokens")
	}
}

// TestStartSessionRejectsUnpersistedManifest: rejects a non-constructed manifest
// path and an in-memory manifest whose fields differ from the persisted file.
func TestStartSessionRejectsUnpersistedManifest(t *testing.T) {
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
	base := StartSessionRequest{SessionID: "sid", Agent: "codex", Mode: "strict", StartedAt: "2026", Materialization: mat, Bootstrap: boot}

	// (a) ManifestPath pointing at an unrelated existing file (≠ constructed path).
	other := filepath.Join(t.TempDir(), "other.json")
	mustNil(t, os.WriteFile(other, []byte("{}\n"), 0o600))
	badPath := base
	badPath.Bootstrap.ManifestPath = other
	if _, e := target.StartSession(ctx, badPath); e == nil {
		t.Fatalf("start accepted a bootstrap manifest path that is not the constructed path")
	}

	// (b) ManifestPath = constructed, but an in-memory manifest field differs from
	// the persisted manifest.
	badImage := base
	badImage.Bootstrap.Manifest.ImageRef = "img:evil"
	if _, e := target.StartSession(ctx, badImage); e == nil {
		t.Fatalf("start accepted a bootstrap manifest whose image_ref differs from disk")
	}
	badTarget := base
	badTarget.Materialization.Manifest.TargetID = "other-target"
	if _, e := target.StartSession(ctx, badTarget); e == nil {
		t.Fatalf("start accepted a materialization manifest whose target_id differs from disk")
	}
}

func TestStartSessionRejectsForeignMaterializedWorkspace(t *testing.T) {
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
	foreign := t.TempDir()
	mustNil(t, os.MkdirAll(filepath.Join(foreign, "sub"), 0o755))

	// Result points at a foreign dir but the manifest still names the real one.
	bad := StartSessionRequest{SessionID: "sid", Agent: "codex", Mode: "strict", StartedAt: "2026", Materialization: mat, Bootstrap: boot}
	bad.Materialization.MaterializedWorkspace = foreign
	if _, e := target.StartSession(ctx, bad); e == nil {
		t.Fatalf("start accepted a foreign materialized workspace")
	}
	// Result AND manifest agree on the foreign dir → still rejected (not constructed).
	bad2 := StartSessionRequest{SessionID: "sid", Agent: "codex", Mode: "strict", StartedAt: "2026", Materialization: mat, Bootstrap: boot}
	bad2.Materialization.MaterializedWorkspace = foreign
	bad2.Materialization.Manifest.MaterializedWorkspace = foreign
	if _, e := target.StartSession(ctx, bad2); e == nil {
		t.Fatalf("start accepted a self-consistent foreign materialized workspace")
	}
}

// TestStartSessionConcurrentExactlyOnce: two overlapping StartSession calls for
// the same session id both succeed idempotently — the flock serializes them and
// the second recovers/returns without duplicating the start triplet (the log has
// exactly one).
func TestStartSessionConcurrentExactlyOnce(t *testing.T) {
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
	req := StartSessionRequest{SessionID: "sid", Agent: "codex", Mode: "strict", StartedAt: "2026", Materialization: mat, Bootstrap: boot}

	ok, fail := concurrentCalls(2, func() error { _, e := target.StartSession(ctx, req); return e })
	if ok != 2 || fail != 0 {
		t.Fatalf("concurrent StartSession: ok=%d fail=%d, want 2/0 (idempotent)", ok, fail)
	}
	if data, _ := os.ReadFile(boot.AuditLogPath); strings.Count(string(data), "event=session_started ") != 1 {
		t.Fatalf("audit log has duplicate start triplets:\n%s", data)
	}
}

// TestFinishSessionConcurrentExactlyOnce: two overlapping FinishSession calls both
// succeed idempotently — the flock serializes them and the second returns without
// duplicating the session_finished event (the log has exactly one).
func TestFinishSessionConcurrentExactlyOnce(t *testing.T) {
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
	started, err := target.StartSession(ctx, StartSessionRequest{SessionID: "sid", Agent: "codex", Mode: "strict", StartedAt: "2026", Materialization: mat, Bootstrap: boot})
	mustNil(t, err)
	finishReq := FinishSessionRequest{Started: started, FinishedAt: "2027", ExitStatus: "0"}

	ok, fail := concurrentCalls(2, func() error { _, e := target.FinishSession(ctx, finishReq); return e })
	if ok != 2 || fail != 0 {
		t.Fatalf("concurrent FinishSession: ok=%d fail=%d, want 2/0 (idempotent)", ok, fail)
	}
	if data, _ := os.ReadFile(started.AuditLogPath); strings.Count(string(data), "event=session_finished ") != 1 {
		t.Fatalf("audit log has duplicate session_finished events:\n%s", data)
	}
}

// TestFinishSessionRejectsForgedStartEvent: the start-events check parses the
// exact event= field, so "event=session_started" appearing only as a substring
// of another field (no genuine line) is rejected, not accepted as started.
func TestFinishSessionRejectsForgedStartEvent(t *testing.T) {
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
	started, err := target.StartSession(ctx, StartSessionRequest{SessionID: "sid", Agent: "codex", Mode: "strict", StartedAt: "2026", Materialization: mat, Bootstrap: boot})
	mustNil(t, err)

	// session_started appears only as a substring of a field value (event is "noop").
	crafted := "ts=2026 session_id=sid event=workspace_materialized target_id=tid\n" +
		"ts=2026 session_id=sid event=bootstrap_ready target_id=tid\n" +
		"ts=2026 session_id=sid event=noop decoy=x/event=session_started\n"
	mustNil(t, os.WriteFile(started.AuditLogPath, []byte(crafted), 0o600))
	if _, e := target.FinishSession(ctx, FinishSessionRequest{Started: started, FinishedAt: "2027", ExitStatus: "0"}); e == nil {
		t.Fatalf("finish accepted a forged session_started substring (no genuine event line)")
	}
}

func TestFinishSessionRejectsDeletedAuditLog(t *testing.T) {
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
	started, err := target.StartSession(ctx, StartSessionRequest{SessionID: "sid", Agent: "codex", Mode: "strict", StartedAt: "2026", Materialization: mat, Bootstrap: boot})
	mustNil(t, err)

	mustNil(t, os.Remove(started.AuditLogPath))
	if _, e := target.FinishSession(ctx, FinishSessionRequest{Started: started, FinishedAt: "2027", ExitStatus: "0"}); e == nil {
		t.Fatalf("finish accepted a deleted audit log")
	}
	rec, err := sessions.ReadSessionRecord(started.RecordPath)
	mustNil(t, err)
	if rec.Status == DefaultContract().Session.FinalStatus {
		t.Fatalf("record finalized despite a deleted audit log")
	}
}

// concurrentCalls runs fn in n goroutines and returns the (ok, fail) counts.
func concurrentCalls(n int, fn func() error) (ok, fail int) {
	var wg sync.WaitGroup
	var mu sync.Mutex
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			err := fn()
			mu.Lock()
			if err == nil {
				ok++
			} else {
				fail++
			}
			mu.Unlock()
		}()
	}
	close(start)
	wg.Wait()
	return ok, fail
}

// TestAuditPathValuesRoundTrip: paths containing spaces materialize, and their
// audit-line values are whitespace-free tokens that decode to the exact paths.
func TestAuditPathValuesRoundTrip(t *testing.T) {
	t.Parallel()
	src := filepath.Join(t.TempDir(), "My Source Dir")
	mustNil(t, os.MkdirAll(filepath.Join(src, "src"), 0o755))
	mustNil(t, os.WriteFile(filepath.Join(src, "src", "main.go"), []byte("package main\n"), 0o644))
	state := filepath.Join(t.TempDir(), "My State Dir")

	mat, log := runToStart(t, src, state)

	origin := auditField(t, log, "workspace_materialized", "workspace_origin")
	if strings.ContainsAny(origin, " \t\n") {
		t.Fatalf("encoded workspace_origin contains whitespace: %q", origin)
	}
	if decodeAuditPathValue(origin) != src {
		t.Fatalf("workspace_origin round-trip = %q, want %q", decodeAuditPathValue(origin), src)
	}
	ws := auditField(t, log, "workspace_materialized", "workspace")
	if decodeAuditPathValue(ws) != mat.MaterializedWorkspace {
		t.Fatalf("workspace round-trip = %q, want %q", decodeAuditPathValue(ws), mat.MaterializedWorkspace)
	}
}

// TestAuditPathNewlineRejectedBeforeAudit: a newline in a source path is rejected
// at the record write (records forbid newlines) before any audit line is emitted.
func TestAuditPathNewlineRejectedBeforeAudit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	target, err := NewAppleContainerTarget(Contract{})
	mustNil(t, err)
	src := filepath.Join(t.TempDir(), "line\nts=2026 session_id=victim event=forged")
	mustNil(t, os.MkdirAll(filepath.Join(src, "src"), 0o755))
	mustNil(t, os.WriteFile(filepath.Join(src, "src", "main.go"), []byte("package main\n"), 0o644))
	state := t.TempDir()

	mat, err := target.MaterializeWorkspace(ctx, MaterializeRequest{StateRoot: state, TargetID: "tid", MaterializationID: "mid", SourceWorkspace: src})
	mustNil(t, err)
	boot, err := target.BootstrapTarget(ctx, BootstrapRequest{StateRoot: state, TargetID: "tid", BootstrapID: "bid", ImageRef: "img:1"})
	mustNil(t, err)
	if _, e := target.StartSession(ctx, StartSessionRequest{SessionID: "sid", Agent: "codex", Mode: "strict", StartedAt: "2026", Materialization: mat, Bootstrap: boot}); e == nil {
		t.Fatalf("start session with newline source path accepted")
	}
	if data, _ := os.ReadFile(boot.AuditLogPath); strings.Contains(string(data), "event=forged") {
		t.Fatalf("forged event leaked into audit log:\n%s", data)
	}
}

func TestFinishSessionUsesPersistedAuditLog(t *testing.T) {
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
	started, err := target.StartSession(ctx, StartSessionRequest{SessionID: "sid", Agent: "codex", Mode: "strict", StartedAt: "2026", Materialization: mat, Bootstrap: boot})
	mustNil(t, err)

	// A caller passes a stale/wrong audit log path in the started result; the
	// finish event must still land in the log the persisted record names.
	stale := started
	wrong := filepath.Join(t.TempDir(), "wrong-audit.log")
	stale.AuditLogPath = wrong
	finished, err := target.FinishSession(ctx, FinishSessionRequest{Started: stale, FinishedAt: "2027", ExitStatus: "0"})
	mustNil(t, err)
	if finished.AuditLogPath != started.AuditLogPath {
		t.Fatalf("finish used %q, want persisted %q", finished.AuditLogPath, started.AuditLogPath)
	}
	if data, _ := os.ReadFile(started.AuditLogPath); !strings.Contains(string(data), "event=session_finished") {
		t.Fatalf("session_finished not appended to the persisted audit log")
	}
	if _, e := os.Stat(wrong); e == nil {
		t.Fatalf("finish wrote to the caller-supplied wrong log path %q", wrong)
	}
}
