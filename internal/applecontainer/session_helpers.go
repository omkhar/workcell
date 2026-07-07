// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package applecontainer

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/omkhar/workcell/internal/host/sessions"
)

// auditLineSentinel is a FIXED terminal field appended to EVERY rendered audit line so the
// variable identity fields (image_ref/workspace/exit_status/…) are never the last token.
// It lets conflictingCompleteLine's prefix rule cleanly separate a TORN fragment (a strict
// prefix of the expected line, cut before the sentinel → ignored/healed) from a COMPLETE
// line whose value merely happens to be a prefix of the expected value (e.g. image_ref=img
// vs img:2), which diverges before the sentinel → NOT a prefix → correctly flagged as a
// conflict. `v` is the audit-line schema version; it is applecontainer-private (remotevm
// renders its own audit lines) and additive (conformance tolerates extra fields).
const auditLineSentinel = " v=1"

// appendAudit is the audit-append indirection used by Start/Finish. Production always
// uses appendAuditLine; tests replace it with a failpoint to simulate a short/ENOSPC
// write (some complete lines land, then an error) and exercise the leave-partial-state
// recovery paths. It is never reassigned outside tests.
var appendAudit = appendAuditLine

// startEventsComplete reports whether all three EXACT complete start-audit lines for an
// already-started session are present in lines. The expected lines are reconstructed
// from the PERSISTED record via the SAME startEventLines renderer StartSession uses, so
// a start line truncated after its event= token (which carries the token but not the
// rest of the line) is NOT recognized — letting FinishSession refuse to finalize on torn
// start evidence, consistent with the StartSession-side exact-line recovery check. The
// materialization id is not a record field; it is the <materializations>/<id>/<workspace>
// path segment (workspace_dir is the single segment "workspace"), so Base(Dir(workspace))
// recovers it.
func (t AppleContainerTarget) startEventsComplete(current sessions.SessionRecord, sessionID, targetID string, lines []string) (bool, error) {
	// The expected lines are rendered from PERSISTED record fields; a field tampered to
	// carry whitespace/control/newline would produce a malformed "expected" line (and could
	// inject an audit token) that misleads the slices.Contains match. Validate each RAW-
	// rendered persisted field as an audit token first — the symmetric check StartSession
	// applies on the write path — and reject a tampered one. (workspace/workspace_origin are
	// percent-encoded in the render, so they cannot inject; they are not token-checked here.)
	mid := filepath.Base(filepath.Dir(current.Workspace))
	for _, f := range []struct{ label, value string }{
		{"persisted started at", current.StartedAt},
		{"persisted bootstrap id", current.BootstrapID},
		{"persisted image ref", current.ImageRef},
		{"persisted materialization id", mid},
	} {
		if err := validateAuditToken(f.label, f.value); err != nil {
			return false, fmt.Errorf("session %q record holds an invalid %s: %w", sessionID, f.label, err)
		}
	}
	synth := StartSessionRequest{
		Materialization: MaterializeResult{Manifest: WorkspaceManifest{
			MaterializationID: mid,
			SourceWorkspace:   current.WorkspaceOrigin,
		}},
		Bootstrap: BootstrapResult{Manifest: BootstrapManifest{
			BootstrapID: current.BootstrapID,
			ImageRef:    current.ImageRef,
		}},
	}
	for _, e := range t.startEventLines(current.StartedAt, sessionID, targetID, current.Workspace, synth) {
		if !slices.Contains(lines, e.line) {
			return false, nil
		}
	}
	return true, nil
}

// lastFieldKey returns the key of the last whitespace-delimited key=value token of line.
func lastFieldKey(line string) string {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return ""
	}
	k, _, _ := strings.Cut(fields[len(fields)-1], "=")
	return k
}

// conflictingCompleteLine reports whether lines holds a COMPLETE <event> line (carrying
// the trailing field of want — want's own last key) that DIFFERS from want: a second,
// disagreeing complete record for the same event (corruption/tampering) that must not be
// idempotent-accepted alongside the expected line. A torn fragment (missing that trailing
// field) is not complete and is ignored here (it is healed elsewhere).
// reconcileStartEvents rejects a conflicting COMPLETE start line for the session (a
// different complete line for the same event present alongside an expected one) and
// appends only the expected lines whose exact complete form is ABSENT from existing, in
// ONE write — so a pre-existing exact line is never duplicated and a torn fragment (not
// complete) is healed by appending its missing complete line. Returns the count appended
// (0 = all already present). Shared by fresh-start and recovery; appends via the appendAudit
// seam so fault-injection tests still intercept it.
func (t AppleContainerTarget) reconcileStartEvents(stateRoot, logPath, sessionID string, expected []startEvent, existing []string) (int, error) {
	var missing []string
	for _, e := range expected {
		if conflictingCompleteLine(existing, e.name, e.line) {
			return 0, fmt.Errorf("session %q has conflicting complete start evidence for %s — refusing to reconcile", sessionID, e.name)
		}
		if !slices.Contains(existing, e.line) {
			missing = append(missing, e.line)
		}
	}
	if len(missing) == 0 {
		return 0, nil
	}
	if err := appendAudit(stateRoot, logPath, strings.Join(missing, "\n")); err != nil {
		return 0, err
	}
	return len(missing), nil
}

func conflictingCompleteLine(lines []string, event, want string) bool {
	lastKey := lastFieldKey(want)
	for _, line := range lines {
		// The expected line, and any strict PREFIX of it, are not conflicts: a prefix is a
		// torn fragment of THIS same line (a short/ENOSPC write cut it off mid-field, even
		// mid-value of the trailing field) and heals when the complete line is re-appended.
		// A genuinely different line (different request / real tampering) diverges from want
		// before either string ends, so it is NOT a prefix and is flagged below. (A line
		// that has want as a prefix — longer, with extra trailing content — is anomalous,
		// not a clean tear, so it is NOT excluded here and is treated as a conflict.)
		if strings.HasPrefix(want, line) {
			continue
		}
		if auditLineFieldValue([]string{line}, event, lastKey) != "" {
			return true
		}
	}
	return false
}

// assertRecordContract pins a persisted record's contract-identity fields to this
// target's contract — the same contract-sourced set startedRecordFields writes — so a
// record whose kind/provider/id place it under the canonical target path but whose other
// contract fields have DRIFTED is rejected rather than finalized.
func (t AppleContainerTarget) assertRecordContract(r sessions.SessionRecord) error {
	c := t.Contract
	if r.TargetKind != c.TargetKind ||
		r.TargetProvider != c.TargetProvider ||
		r.TargetAssuranceClass != c.TargetAssuranceClass ||
		r.RuntimeAPI != c.RuntimeAPI ||
		r.WorkspaceTransport != c.WorkspaceTransport ||
		r.InitialAssurance != c.Session.Assurance ||
		r.CurrentAssurance != c.Session.Assurance ||
		r.WorkspaceControlPlane != c.Session.WorkspaceControlPlane {
		return fmt.Errorf("persisted record contract fields do not match this target's contract")
	}
	return nil
}
