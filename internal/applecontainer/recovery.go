// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package applecontainer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/omkhar/workcell/internal/host/sessions"
)

type StartSessionRequest struct {
	SessionID       string
	Agent           string
	Mode            string
	StartedAt       string
	Materialization MaterializeResult
	Bootstrap       BootstrapResult
}

type FinishSessionRequest struct {
	Started    SessionResult
	FinishedAt string
	ExitStatus string
}

type SessionResult struct {
	Record       sessions.SessionRecord
	RecordPath   string
	AuditLogPath string
}

// ConformanceTarget is the session-lifecycle interface the conformance harness
// drives. It mirrors the four-phase remote-VM lifecycle but with local-VM types.
type ConformanceTarget interface {
	MaterializeWorkspace(context.Context, MaterializeRequest) (MaterializeResult, error)
	BootstrapTarget(context.Context, BootstrapRequest) (BootstrapResult, error)
	StartSession(context.Context, StartSessionRequest) (SessionResult, error)
	FinishSession(context.Context, FinishSessionRequest) (SessionResult, error)
}

// lockSession acquires an exclusive advisory lock for the session (a per-session
// .lock file beside the record, ignored by the session lister which reads only
// .json), serializing concurrent StartSession/FinishSession so their read-then-
// write exactly-once guards are atomic. The returned func releases it.
func lockSession(stateRoot, recordPath string) (func(), error) {
	// openAuditParent walks from the trusted state root with O_NOFOLLOW per
	// component (creating the sessions dir if absent), and the lock file itself is
	// opened O_NOFOLLOW relative to that verified fd, so a symlinked sessions dir or
	// lock file cannot redirect the lock. Two concurrent first-StartSession calls for
	// the same target race to create the sessions dir on demand, and the lock open
	// can see a transient ENOENT during that window; re-derive the parent fd and
	// retry a bounded number of times so the flock is actually acquired (the recovery
	// idempotency below relies on this lock serializing the check→heal).
	lockName := filepath.Base(strings.TrimSuffix(recordPath, ".json")) + ".lock"
	var lastErr error
	for attempt := 0; attempt < 16; attempt++ {
		parentFD, err := openAuditParent(stateRoot, filepath.Dir(recordPath))
		if err != nil {
			return nil, err
		}
		fd, err := unix.Openat(parentFD, lockName, unix.O_CREAT|unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
		unix.Close(parentFD)
		if err == unix.ENOENT {
			lastErr = err
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("open session lock %q: %w", lockName, err)
		}
		// O_CREAT's 0o600 is umask-masked, so under a restrictive umask the lock file
		// can land with narrower-than-intended bits; re-assert the mode on the fd (no
		// path race), umask-independent, matching the record/log Fchmod fixes.
		if err := unix.Fchmod(fd, 0o600); err != nil {
			unix.Close(fd)
			return nil, fmt.Errorf("chmod session lock %q: %w", lockName, err)
		}
		f := os.NewFile(uintptr(fd), lockName)
		if err := unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
			_ = f.Close()
			return nil, err
		}
		return func() {
			_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
			_ = f.Close()
		}, nil
	}
	return nil, fmt.Errorf("open session lock %q: %w", lockName, lastErr)
}

// startedRecordFields is the SINGLE definition of the field map a fresh StartSession
// persists. StartSession writes exactly this map, and startedRecordMatchesRequest
// builds the expected record from it, so the match covers every field the writer sets
// with no hand-maintained enumeration to drift. started_at/observed_at are stamped
// here (with req.StartedAt) but excluded from the match, which re-stamps them.
func (t AppleContainerTarget) startedRecordFields(req StartSessionRequest, sessionID, targetID, ws, auditLogPath string) map[string]string {
	c := t.Contract
	return map[string]string{
		"session_id":              sessionID,
		"profile":                 targetID,
		"target_kind":             c.TargetKind,
		"target_provider":         c.TargetProvider,
		"target_id":               targetID,
		"target_assurance_class":  c.TargetAssuranceClass,
		"runtime_api":             c.RuntimeAPI,
		"workspace_transport":     c.WorkspaceTransport,
		"agent":                   req.Agent,
		"mode":                    req.Mode,
		"status":                  c.Session.StartStatus,
		"workspace":               ws,
		"workspace_origin":        req.Materialization.Manifest.SourceWorkspace,
		"workspace_root":          ws,
		"worktree_path":           ws,
		"audit_log_path":          auditLogPath,
		"started_at":              req.StartedAt,
		"observed_at":             req.StartedAt,
		"initial_assurance":       c.Session.Assurance,
		"current_assurance":       c.Session.Assurance,
		"workspace_control_plane": c.Session.WorkspaceControlPlane,
	}
}

// startedRecordMatchesRequest reports whether the persisted record equals — EXHAUSTIVELY,
// over every SessionRecord field — the record StartSession would write for this request.
// Rather than compare a hand-enumerated field list (which silently accepts a persisted
// record that ALSO carries unexpected populated metadata the request never produces),
// it builds the full expected record via the writer's OWN encode path (the same
// EncodeSessionRecordFrom map->struct the atomic writer uses) and compares the whole
// structs. SessionRecord is entirely comparable scalar fields (no slices/maps/unexported),
// so struct == is an exact, by-construction-complete comparison covering current AND future
// fields: any extra optional field populated on the persisted record is a mismatch.
//
// Only started_at/observed_at are excluded — the field map is seeded with the PERSISTED
// values before encoding, so they are identical on both sides — because a genuine retry
// legitimately re-stamps them. (Seeding also lets the writer's non-empty-started_at
// validation pass; a persisted record with an empty started_at is not a valid started
// record, so encode fails and we correctly report no match.) Every other field must match
// or the record is a distinct start (rejected, not idempotent/recovered).
func (t AppleContainerTarget) startedRecordMatchesRequest(current sessions.SessionRecord, req StartSessionRequest, sessionID, targetID, workspace, auditLogPath string) bool {
	fields := t.startedRecordFields(req, sessionID, targetID, workspace, auditLogPath)
	fields["started_at"] = current.StartedAt   // re-stampable per attempt — excluded
	fields["observed_at"] = current.ObservedAt // re-stampable per attempt — excluded
	encoded, err := sessions.EncodeSessionRecordFrom(nil, fields)
	if err != nil {
		return false
	}
	expected, err := sessions.DecodeSessionRecord(encoded, "expected started record")
	if err != nil {
		return false
	}
	return expected == current
}

// startTriplet builds the three start-audit events (workspace_materialized,
// bootstrap_ready, session_started) as one newline-joined append, stamped with the
// given ts. Callers pass req.StartedAt for a fresh start and the PERSISTED
// started_at when recovering a crash-partial, so a recovered line matches the
// already-committed record instead of being re-stamped with the retry's time.
// startEvent is one start-audit event: its exact event name and rendered line.
type startEvent struct {
	name string
	line string
}

// startEventLines renders the three start-audit events in canonical order
// (workspace_materialized, bootstrap_ready, session_started), each paired with
// its event name and all stamped with ts. Recovery appends only the subset whose
// name is absent from the log so a crash mid-triplet is healed without duplicates.
func (t AppleContainerTarget) startEventLines(ts, sessionID, targetID, ws string, req StartSessionRequest) []startEvent {
	return []startEvent{
		{"workspace_materialized", fmt.Sprintf("ts=%s session_id=%s event=workspace_materialized target_kind=%s target_provider=%s target_id=%s workspace_transport=%s materialization_id=%s workspace_origin=%s workspace=%s", ts, sessionID, t.Contract.TargetKind, t.Contract.TargetProvider, targetID, t.Contract.WorkspaceTransport, req.Materialization.Manifest.MaterializationID, encodeAuditPathValue(req.Materialization.Manifest.SourceWorkspace), encodeAuditPathValue(ws))},
		{"bootstrap_ready", fmt.Sprintf("ts=%s session_id=%s event=bootstrap_ready target_kind=%s target_provider=%s target_id=%s runtime_api=%s access_model=%s bootstrap_id=%s image_ref=%s", ts, sessionID, t.Contract.TargetKind, t.Contract.TargetProvider, targetID, t.Contract.RuntimeAPI, t.Contract.AccessModel, req.Bootstrap.Manifest.BootstrapID, req.Bootstrap.Manifest.ImageRef)},
		{"session_started", fmt.Sprintf("ts=%s session_id=%s event=session_started target_kind=%s target_provider=%s target_id=%s status=%s workspace_control_plane=%s", ts, sessionID, t.Contract.TargetKind, t.Contract.TargetProvider, targetID, t.Contract.Session.StartStatus, t.Contract.Session.WorkspaceControlPlane)},
	}
}

// startTriplet joins all three start events into one appendable block (fresh start).
func (t AppleContainerTarget) startTriplet(ts, sessionID, targetID, ws string, req StartSessionRequest) string {
	events := t.startEventLines(ts, sessionID, targetID, ws, req)
	lines := make([]string, len(events))
	for i, e := range events {
		lines[i] = e.line
	}
	return strings.Join(lines, "\n")
}

// auditLineFieldValue returns the value of the exact `key=` token on the first line
// carrying event=<event>, or "" if not found. Uses the same exact-token parsing as
// auditHasEvents (whitespace-split, cut at the first '='). bootstrap_id/image_ref
// are validateAuditToken'd (no whitespace), so they are not percent-encoded and
// need no decode.
func auditLineFieldValue(lines []string, event, key string) string {
	for _, line := range lines {
		isEvent, val := false, ""
		for _, tok := range strings.Fields(line) {
			k, v, ok := strings.Cut(tok, "=")
			if !ok {
				continue
			}
			if k == "event" && v == event {
				isEvent = true
			}
			if k == key {
				val = v
			}
		}
		if isEvent {
			return val
		}
	}
	return ""
}
