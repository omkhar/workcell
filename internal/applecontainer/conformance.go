// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package applecontainer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/omkhar/workcell/internal/host/sessions"
	"github.com/omkhar/workcell/internal/providerid"
)

// rejectDuplicateJSONKeys walks the token stream and rejects any object with a duplicate key.
// encoding/json is LAST-WINS on duplicates, so a persisted record/manifest could smuggle an extra
// `"target_id":"evil"` past field validation; this closes that (like the audit-line dup-key check).
func rejectDuplicateJSONKeys(raw []byte) error {
	return scanJSONValue(json.NewDecoder(bytes.NewReader(raw)))
}

// scanJSONValue consumes exactly one JSON value from dec and checks nested objects for dup keys.
func scanJSONValue(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return nil // scalar
	}
	if delim == '{' {
		seen := map[string]struct{}{}
		for dec.More() {
			key, err := dec.Token()
			if err != nil {
				return err
			}
			name := key.(string)
			if _, dup := seen[name]; dup {
				return fmt.Errorf("duplicate JSON key %q", name)
			}
			seen[name] = struct{}{}
			if err := scanJSONValue(dec); err != nil {
				return err
			}
		}
	} else { // '['
		for dec.More() {
			if err := scanJSONValue(dec); err != nil {
				return err
			}
		}
	}
	_, err = dec.Token() // consume the closing } or ]
	return err
}

// readPersistedManifest decodes the manifest PERSISTED at the derived canonical path via the
// production no-follow reader (readFileSafe: openat O_NOFOLLOW per component, Fstat S_IFREG +
// Nlink==1), so a symlink/hardlink at the canonical path pointing at a decoy outside StateRoot —
// or a missing/stale/divergent file — is rejected rather than followed. Duplicate JSON keys too.
// The decode is STRICT (DisallowUnknownFields + no trailing content): a conformant target must
// emit exactly the v1 schema, so an extra/unknown key in the persisted manifest is rejected.
func readPersistedManifest(stateRoot, path string, into any) error {
	data, err := readFileSafe(stateRoot, path, "manifest")
	if err != nil {
		return err
	}
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return fmt.Errorf("manifest at %q: %w", path, err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		return fmt.Errorf("manifest at %q: %w", path, err)
	}
	// Require a strict io.EOF after the value: a trailing object, token, OR unmatched delimiter
	// (a lone `}`/`]`, which dec.More() would miss) is rejected as trailing data.
	if _, err := dec.Token(); err != io.EOF {
		return fmt.Errorf("manifest at %q: trailing data after manifest", path)
	}
	return nil
}

// requireNoSymlink verifies path (under stateRoot) is reached without traversing any symlink and
// is the expected kind (regular file, or directory when wantDir), via the production no-follow
// stat (statPathSafe: parents O_NOFOLLOW, leaf Fstatat AT_SYMLINK_NOFOLLOW). A regular leaf also
// requires Nlink==1 (mirroring readFileSafe), so a symlink OR a hardlink to a decoy outside
// StateRoot is rejected before any content is read.
func requireNoSymlink(stateRoot, path, label string, wantDir bool) error {
	st, err := statPathSafe(stateRoot, path)
	if err != nil {
		return fmt.Errorf("%s %q: %w", label, path, err)
	}
	want := unix.S_IFREG
	if wantDir {
		want = unix.S_IFDIR
	}
	if int(st.Mode)&unix.S_IFMT != want {
		return fmt.Errorf("%s %q is not the expected file type (mode %#o) — a symlink/decoy is rejected", label, path, st.Mode&unix.S_IFMT)
	}
	if !wantDir && st.Nlink != 1 {
		return fmt.Errorf("%s %q is multiply linked (%d links) — a hardlink to a decoy is rejected", label, path, st.Nlink)
	}
	return nil
}

// canonicalLayout is the set of on-disk paths derived purely from the state root + contract + case
// IDs via the SAME filepath layout the production code uses. The harness asserts every target-
// reported path EQUALS its field here, so a canonical-looking-but-wrong/decoy path cannot match.
// recordPath is used only to re-read the RAW record bytes; the authoritative record is LOCATED by
// listing c.StateRoot, never by a target-reported path.
type canonicalLayout struct {
	targetRoot              string
	materializationRoot     string
	materializedWorkspace   string
	materializationManifest string
	bootstrapManifest       string
	auditLog                string
	recordPath              string
}

// deriveLayout builds the canonical layout from the state root + contract + case IDs,
// mirroring the production path construction in target.go / target_session.go exactly.
func deriveLayout(contract Contract, c ConformanceCase) canonicalLayout {
	root := targetRoot(c.StateRoot, contract.TargetKind, contract.TargetProvider, c.TargetID)
	materializationRoot := filepath.Join(root, "materializations", c.MaterializationID)
	bootstrapRoot := filepath.Join(root, "bootstrap", c.BootstrapID)
	return canonicalLayout{
		targetRoot:              root,
		materializationRoot:     materializationRoot,
		materializedWorkspace:   filepath.Join(materializationRoot, contract.WorkspaceMaterialization.WorkspaceDir),
		materializationManifest: filepath.Join(materializationRoot, contract.WorkspaceMaterialization.ManifestName),
		bootstrapManifest:       filepath.Join(bootstrapRoot, contract.Bootstrap.ManifestName),
		auditLog:                filepath.Join(root, "workcell.audit.log"),
		recordPath:              filepath.Join(root, "sessions", c.SessionID+".json"),
	}
}

// verifyPersistedRecordCanonical reads the RAW record bytes (no-follow, S_IFREG, Nlink==1) and
// asserts the target persisted an ALREADY-CANONICAL record — the reader's field-normalization must
// be a NO-OP (comparing the strictly-decoded raw record to the decoded+normalized one). A target
// must not lean on the reader to backfill a missing/legacy field.
func verifyPersistedRecordCanonical(stateRoot, recordPath string) error {
	data, err := readFileSafe(stateRoot, recordPath, "session record")
	if err != nil {
		return fmt.Errorf("canonical session record at %q: %w", recordPath, err)
	}
	// A duplicate JSON key (last-wins) could smuggle a value past the field validation below.
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return fmt.Errorf("session record at %q: %w", recordPath, err)
	}
	var raw sessions.SessionRecord
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&raw); err != nil {
		return fmt.Errorf("raw session record at %q: %w", recordPath, err)
	}
	normalized, err := sessions.DecodeSessionRecord(data, recordPath)
	if err != nil {
		return fmt.Errorf("session record at %q: %w", recordPath, err)
	}
	if raw != normalized {
		return fmt.Errorf("persisted session record at %q is not canonical (relies on reader normalization)", recordPath)
	}
	return nil
}

// ConformanceCase parameterizes a single deterministic lifecycle run.
type ConformanceCase struct {
	StateRoot         string
	TargetID          string
	MaterializationID string
	BootstrapID       string
	SessionID         string
	Agent             string
	Mode              string
	ImageRef          string
	StartedAt         string
	FinishedAt        string
	ExitStatus        string
	SourceWorkspace   string
}

// ConformanceResult is the aggregate output of a successful lifecycle run.
type ConformanceResult struct {
	Materialization MaterializeResult
	Bootstrap       BootstrapResult
	Started         SessionResult
	Finished        SessionResult
	Exported        sessions.SessionExport
}

// DefaultConformanceCase returns a canonical case for the given state root and
// source workspace.
func DefaultConformanceCase(stateRoot, sourceWorkspace string) ConformanceCase {
	return ConformanceCase{
		StateRoot:         stateRoot,
		TargetID:          "apple-container-target",
		MaterializationID: "fixture-materialization",
		BootstrapID:       "fixture-bootstrap",
		SessionID:         "fixture-session",
		Agent:             providerid.Codex,
		Mode:              "strict",
		ImageRef:          "workcell-applecontainer:test",
		StartedAt:         "2026-06-01T00:00:00Z",
		FinishedAt:        "2026-06-01T00:15:00Z",
		ExitStatus:        "0",
		SourceWorkspace:   sourceWorkspace,
	}
}

// auditIDs is the expected value of every id field the audit lines may carry.
func (c ConformanceCase) auditIDs() map[string]string {
	return map[string]string{"session_id": c.SessionID, "target_id": c.TargetID, "materialization_id": c.MaterializationID, "bootstrap_id": c.BootstrapID, "image_ref": c.ImageRef}
}

// auditRequiredIDs is the id fields each audit event must carry, matching the
// lines the target emits. Presence AND value of these are required.
var auditRequiredIDs = map[string][]string{
	"workspace_materialized": {"materialization_id", "target_id"},
	"bootstrap_ready":        {"bootstrap_id", "target_id", "image_ref"},
	"session_started":        {"session_id", "target_id"},
	"session_finished":       {"session_id", "target_id"},
}

// RunConformance drives target through the full session lifecycle and verifies it
// honors the local-VM contract: manifest fields, the PERSISTED session records,
// exported final status, and the required audit events (with their id fields).
func RunConformance(ctx context.Context, target ConformanceTarget, contract Contract, c ConformanceCase) (ConformanceResult, error) {
	if err := contract.Validate(); err != nil {
		return ConformanceResult{}, err
	}
	// Derive the canonical layout from the state root + contract + IDs ONCE, up front, so
	// every path the harness relies on is checked against a value the target never supplied.
	layout := deriveLayout(contract, c)
	materialization, err := target.MaterializeWorkspace(ctx, MaterializeRequest{StateRoot: c.StateRoot, TargetID: c.TargetID, MaterializationID: c.MaterializationID, SourceWorkspace: c.SourceWorkspace})
	if err != nil {
		return ConformanceResult{}, err
	}
	if err := validateMaterialization(contract, layout, materialization, c); err != nil {
		return ConformanceResult{}, err
	}
	bootstrap, err := target.BootstrapTarget(ctx, BootstrapRequest{StateRoot: c.StateRoot, TargetID: c.TargetID, BootstrapID: c.BootstrapID, ImageRef: c.ImageRef})
	if err != nil {
		return ConformanceResult{}, err
	}
	if err := validateBootstrap(contract, layout, bootstrap, c); err != nil {
		return ConformanceResult{}, err
	}
	started, err := target.StartSession(ctx, StartSessionRequest{SessionID: c.SessionID, Agent: c.Agent, Mode: c.Mode, StartedAt: c.StartedAt, Materialization: materialization, Bootstrap: bootstrap})
	if err != nil {
		return ConformanceResult{}, err
	}
	// Validate the AUTHORITATIVE state-root record (found by listing c.StateRoot, NOT the target-
	// reported RecordPath). Captured for the post-finish preservation check.
	startRec, err := requirePersistedRecord(c.StateRoot, layout.recordPath, c.SessionID, contract.Session.StartStatus, "after StartSession")
	if err != nil {
		return ConformanceResult{}, err
	}
	if err := validateStartedSession(contract, layout, startRec, c); err != nil {
		return ConformanceResult{}, err
	}
	finished, err := target.FinishSession(ctx, FinishSessionRequest{Started: started, FinishedAt: c.FinishedAt, ExitStatus: c.ExitStatus})
	if err != nil {
		return ConformanceResult{}, err
	}
	// Again the AUTHORITATIVE record: it must hold the EXACT contract-expected final status (not
	// merely "is terminal" — failed/aborted are terminal too but are not a successful finish).
	finalRec, err := requirePersistedRecord(c.StateRoot, layout.recordPath, c.SessionID, contract.Session.FinalStatus, "after FinishSession")
	if err != nil {
		return ConformanceResult{}, err
	}
	if err := validateFinishedSession(contract, finalRec, c); err != nil {
		return ConformanceResult{}, err
	}
	// FinishSession must PRESERVE every start-established field — it may only mutate the finish
	// fields; rewriting/corrupting a start field (workspace, agent, identity, …) fails.
	if err := requireStartFieldsPreserved(startRec, finalRec); err != nil {
		return ConformanceResult{}, err
	}
	// The RETURNED handles of both lifecycle calls must match the authoritative persisted state.
	if err := requireReturnedHandlesMatch(started, startRec, layout, "after StartSession"); err != nil {
		return ConformanceResult{}, err
	}
	if err := requireReturnedHandlesMatch(finished, finalRec, layout, "after FinishSession"); err != nil {
		return ConformanceResult{}, err
	}
	if got := sessions.SessionTargetSummary(finalRec); got != fmt.Sprintf("%s/%s/%s", contract.TargetKind, contract.TargetProvider, c.TargetID) {
		return ConformanceResult{}, fmt.Errorf("SessionTargetSummary() = %q", got)
	}
	// The export reads the audit log by FOLLOWING record.AuditLogPath (== layout.auditLog); reject a
	// symlink/hardlink there first, else a decoy log outside StateRoot could be exported.
	if err := requireNoSymlink(c.StateRoot, layout.auditLog, "audit log", false); err != nil {
		return ConformanceResult{}, err
	}
	exported, err := sessions.ExportSessionRecordInRoots([]string{c.StateRoot}, c.SessionID)
	if err != nil {
		return ConformanceResult{}, err
	}
	if err := validateAuditEvents(exported.AuditRecords, contract.RequiredAuditEvents, c.auditIDs()); err != nil {
		return ConformanceResult{}, err
	}
	return ConformanceResult{Materialization: materialization, Bootstrap: bootstrap, Started: started, Finished: finished, Exported: exported}, nil
}

// requireRecordStatus asserts a persisted record matches the session identity and the
// EXACT expected status (not merely "is terminal" — failed/aborted are terminal too but are
// not a successful finish). phase names the lifecycle point in errors.
func requireRecordStatus(r sessions.SessionRecord, sessionID, expectedStatus, phase string) error {
	if r.SessionID != sessionID {
		return fmt.Errorf("%s: authoritative session_id %q, want %q", phase, r.SessionID, sessionID)
	}
	if r.Status != expectedStatus {
		return fmt.Errorf("%s: authoritative session status %q, want %q", phase, r.Status, expectedStatus)
	}
	return nil
}

// requireRegularSessionsDir readdir-scans sessionsDir (no opens) and rejects any non-regular entry
// via statPathSafe (no open, cannot hang). The listing that follows opens each *.json with
// os.ReadFile, which BLOCKS on a FIFO (and would touch sockets/devices), so pre-rejecting
// FIFO/socket/device/symlink/dir entries guarantees it only runs over regular files. A missing dir
// is fine (the caller's len!=1 check reports the absent record).
func requireRegularSessionsDir(stateRoot, sessionsDir string) error {
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("sessions dir %q: %w", sessionsDir, err)
	}
	for _, e := range entries {
		p := filepath.Join(sessionsDir, e.Name())
		st, err := statPathSafe(stateRoot, p)
		if err != nil {
			return fmt.Errorf("sessions dir entry %q: %w", p, err)
		}
		if int(st.Mode)&unix.S_IFMT != unix.S_IFREG {
			return fmt.Errorf("sessions dir entry %q is not a regular file (mode %#o) — FIFO/socket/device/symlink/dir rejected", p, st.Mode&unix.S_IFMT)
		}
	}
	return nil
}

// requireRegularAllSessionDirs pre-scans EVERY session dir the listing will walk. The listing
// discovers records across all sessions.StateDirs(root) — each target root PLUS every top-level dir
// — so a FIFO/special *.json in ANY of them (not just the canonical dir) would block the open.
// Enumerating via the SAME StateDirs discovery misses none; StateDirs is readdir/stat-only.
func requireRegularAllSessionDirs(root string) error {
	stateDirs, err := sessions.StateDirs(root)
	if err != nil {
		return err
	}
	for _, sd := range stateDirs {
		if err := requireRegularSessionsDir(root, filepath.Join(sd, "sessions")); err != nil {
			return err
		}
	}
	return nil
}

// requirePersistedRecord finds the AUTHORITATIVE state-root session record (via
// ListSessionRecordsInRoots, NOT the target-reported RecordPath), asserts it exists exactly
// once, validates its identity + exact expected status, and confirms the target persisted it
// ALREADY-CANONICAL at the derived recordPath (raw bytes, no reader-normalization backfill),
// returning it for further checks.
func requirePersistedRecord(stateRoot, recordPath, sessionID, expectedStatus, phase string) (sessions.SessionRecord, error) {
	// Reject any special file (FIFO/socket/device/symlink/dir) in EVERY session dir the listing
	// will walk BEFORE it opens entries — os.ReadFile on a FIFO would block indefinitely.
	if err := requireRegularAllSessionDirs(stateRoot); err != nil {
		return sessions.SessionRecord{}, fmt.Errorf("%s: %w", phase, err)
	}
	records, err := sessions.ListSessionRecordsInRoots([]string{stateRoot}, sessions.SessionListOptions{})
	if err != nil {
		return sessions.SessionRecord{}, err
	}
	if len(records) != 1 {
		return sessions.SessionRecord{}, fmt.Errorf("%s: persisted session records = %d, want 1", phase, len(records))
	}
	if err := requireRecordStatus(records[0], sessionID, expectedStatus, phase); err != nil {
		return sessions.SessionRecord{}, err
	}
	if err := verifyPersistedRecordCanonical(stateRoot, recordPath); err != nil {
		return sessions.SessionRecord{}, fmt.Errorf("%s: %w", phase, err)
	}
	return records[0], nil
}

// requireReturnedHandlesMatch asserts a SessionResult's RETURNED handles are consistent with the
// AUTHORITATIVE persisted record: a backend that persists correctly but hands its caller a bogus
// Record / RecordPath / AuditLogPath is rejected.
func requireReturnedHandlesMatch(res SessionResult, rec sessions.SessionRecord, layout canonicalLayout, phase string) error {
	if got := filepath.Clean(res.RecordPath); got != layout.recordPath {
		return fmt.Errorf("%s: returned RecordPath %q, want %q", phase, got, layout.recordPath)
	}
	if got := filepath.Clean(res.AuditLogPath); got != layout.auditLog {
		return fmt.Errorf("%s: returned AuditLogPath %q, want %q", phase, got, layout.auditLog)
	}
	if res.Record != rec {
		return fmt.Errorf("%s: returned Record diverges from the authoritative persisted record", phase)
	}
	return nil
}

// requireStartFieldsPreserved asserts FinishSession changed ONLY the finish fields and left
// every start-established field untouched. It copies the finish-mutable fields from final
// into a probe, then compares the WHOLE record by value — so any other field (current or
// future) that a finish corrupted is caught, not just an enumerated subset.
func requireStartFieldsPreserved(start, final sessions.SessionRecord) error {
	probe := final
	probe.Status = start.Status
	probe.LiveStatus = start.LiveStatus
	probe.ObservedAt = start.ObservedAt
	probe.FinishedAt = start.FinishedAt
	probe.ExitStatus = start.ExitStatus
	probe.FinalAssurance = start.FinalAssurance
	if probe != start {
		return fmt.Errorf("FinishSession changed a start-established record field (started=%+v final=%+v)", start, final)
	}
	return nil
}

// fieldCheck is one exact-equality assertion of a manifest/record field.
type fieldCheck struct{ name, got, want string }

func checkFields(prefix string, checks []fieldCheck) error {
	for _, f := range checks {
		if f.got != f.want {
			return fmt.Errorf("%s %s = %q, want %q", prefix, f.name, f.got, f.want)
		}
	}
	return nil
}

func validateMaterialization(contract Contract, layout canonicalLayout, result MaterializeResult, c ConformanceCase) error {
	// Every target-reported path must EQUAL its derived-canonical value (the reads below use the
	// DERIVED paths, so a decoy path is both rejected here and never followed).
	if err := checkFields("materialization", []fieldCheck{
		{"target_root", filepath.Clean(result.TargetRoot), layout.targetRoot},
		{"materialization_root", filepath.Clean(result.MaterializationRoot), layout.materializationRoot},
		{"manifest_path", filepath.Clean(result.ManifestPath), layout.materializationManifest},
		{"materialized_workspace", filepath.Clean(result.MaterializedWorkspace), layout.materializedWorkspace},
	}); err != nil {
		return err
	}
	// Read the PERSISTED manifest from the DERIVED path (no-follow), and require the RETURNED
	// in-memory Manifest to equal it — so a stale/corrupt returned struct, or a missing/divergent
	// persisted file, is caught.
	var m WorkspaceManifest
	if err := readPersistedManifest(c.StateRoot, layout.materializationManifest, &m); err != nil {
		return fmt.Errorf("materialization manifest at %q: %w", layout.materializationManifest, err)
	}
	if !reflect.DeepEqual(result.Manifest, m) {
		return fmt.Errorf("materialization: returned Manifest diverges from the persisted manifest")
	}
	// Pin every id/contract field the persisted manifest carries, and pin materialized_workspace to
	// the DERIVED workspace (a correct tree with a wrong manifest workspace must fail).
	if err := checkFields("materialization", []fieldCheck{
		{"version", fmt.Sprint(m.Version), "1"},
		{"target_kind", m.TargetKind, contract.TargetKind},
		{"target_provider", m.TargetProvider, contract.TargetProvider},
		{"target_id", m.TargetID, c.TargetID},
		{"workspace_transport", m.WorkspaceTransport, contract.WorkspaceTransport},
		{"materialization_id", m.MaterializationID, c.MaterializationID},
		{"source_workspace", m.SourceWorkspace, c.SourceWorkspace},
		{"materialized_workspace", m.MaterializedWorkspace, layout.materializedWorkspace},
	}); err != nil {
		return err
	}
	if !slices.Equal(m.ExcludedPaths, contract.WorkspaceMaterialization.ExcludedPaths) {
		return fmt.Errorf("materialization excluded_paths = %v, want %v", m.ExcludedPaths, contract.WorkspaceMaterialization.ExcludedPaths)
	}
	// Reject a SYMLINKED workspace root before any tree read: copyWorkspaceTree EvalSymlinks-es its
	// root, so a symlinked canonical path would let a target certify a decoy tree outside StateRoot.
	if err := requireNoSymlink(c.StateRoot, layout.materializedWorkspace, "materialized workspace", true); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(layout.materializedWorkspace, ".git")); !os.IsNotExist(err) {
		return fmt.Errorf("materialized workspace must not contain .git")
	}
	mirrorRoot, err := os.MkdirTemp("", "workcell-applecontainer-compare.")
	if err != nil {
		return err
	}
	defer os.RemoveAll(mirrorRoot)
	// Certify the manifest against the actual destination on disk AND the source.
	for i, tree := range []struct {
		label    string
		root     string
		excluded []string
	}{
		{"the destination workspace on disk", layout.materializedWorkspace, nil},
		{"copied workspace", c.SourceWorkspace, contract.WorkspaceMaterialization.ExcludedPaths},
	} {
		got, err := copyWorkspaceTree(tree.root, filepath.Join(mirrorRoot, fmt.Sprintf("cmp%d", i)), tree.excluded)
		if err != nil {
			return err
		}
		if !slices.Equal(got, m.Entries) {
			return fmt.Errorf("materialization manifest entries do not match %s", tree.label)
		}
	}
	// Every certified regular file must be single-linked (Nlink==1): a conformant materialization
	// produces ISOLATED copies, not hardlinks sharing an inode with a path (possibly outside the
	// workspace). Extends the hardlink defense already applied to manifests/record/audit.
	return requireSingleLinkedWorkspace(c.StateRoot, layout.materializedWorkspace, m.Entries)
}

// requireSingleLinkedWorkspace asserts every regular file in the certified workspace tree has
// Nlink==1, using the no-follow statPathSafe (consistent with the manifest/record/audit reads).
func requireSingleLinkedWorkspace(stateRoot, workspaceRoot string, entries []WorkspaceEntry) error {
	for _, e := range entries {
		if e.Kind != "file" {
			continue
		}
		p := filepath.Join(workspaceRoot, filepath.FromSlash(e.Path))
		st, err := statPathSafe(stateRoot, p)
		if err != nil {
			return fmt.Errorf("workspace file %q: %w", p, err)
		}
		if st.Nlink != 1 {
			return fmt.Errorf("workspace file %q is multiply linked (%d links) — a conformant materialization uses isolated copies", p, st.Nlink)
		}
	}
	return nil
}

func validateBootstrap(contract Contract, layout canonicalLayout, bootstrap BootstrapResult, c ConformanceCase) error {
	// Every target-reported path must EQUAL its derived-canonical value (incl. the audit-log path
	// the record later echoes).
	if err := checkFields("bootstrap", []fieldCheck{
		{"target_root", filepath.Clean(bootstrap.TargetRoot), layout.targetRoot},
		{"manifest_path", filepath.Clean(bootstrap.ManifestPath), layout.bootstrapManifest},
		{"audit_log_path", filepath.Clean(bootstrap.AuditLogPath), layout.auditLog},
	}); err != nil {
		return err
	}
	// Read the PERSISTED manifest from the DERIVED path (no-follow); require the returned struct ==.
	var m BootstrapManifest
	if err := readPersistedManifest(c.StateRoot, layout.bootstrapManifest, &m); err != nil {
		return fmt.Errorf("bootstrap manifest at %q: %w", layout.bootstrapManifest, err)
	}
	if bootstrap.Manifest != m {
		return fmt.Errorf("bootstrap: returned Manifest diverges from the persisted manifest")
	}
	return checkFields("bootstrap", []fieldCheck{
		{"version", fmt.Sprint(m.Version), "1"},
		{"target_id", m.TargetID, c.TargetID},
		{"target_kind", m.TargetKind, contract.TargetKind},
		{"target_provider", m.TargetProvider, contract.TargetProvider},
		{"runtime_api", m.RuntimeAPI, contract.RuntimeAPI},
		{"target_assurance_class", m.TargetAssuranceClass, contract.TargetAssuranceClass},
		{"support_boundary", m.SupportBoundary, contract.SupportBoundary},
		{"access_model", m.AccessModel, contract.AccessModel},
		{"bootstrap_id", m.BootstrapID, c.BootstrapID},
		{"image_ref", m.ImageRef, c.ImageRef},
	})
}

// validateStartedSession pins the fields of the AUTHORITATIVE state-root record (passed in
// by the caller, NOT read from the target-reported RecordPath, which a target could redirect
// at a separate well-formed decoy). Workspace and audit-log fields are pinned to the DERIVED
// canonical paths (not target-reported values), so a decoy path echoed into the record fails.
func validateStartedSession(contract Contract, layout canonicalLayout, r sessions.SessionRecord, c ConformanceCase) error {
	ws := layout.materializedWorkspace
	return checkFields("started session", []fieldCheck{
		{"session_id", r.SessionID, c.SessionID},
		{"profile", r.Profile, c.TargetID},
		{"target_kind", r.TargetKind, contract.TargetKind},
		{"target_provider", r.TargetProvider, contract.TargetProvider},
		{"target_id", r.TargetID, c.TargetID},
		{"target_assurance_class", r.TargetAssuranceClass, contract.TargetAssuranceClass},
		{"runtime_api", r.RuntimeAPI, contract.RuntimeAPI},
		{"workspace_transport", r.WorkspaceTransport, contract.WorkspaceTransport},
		{"agent", r.Agent, c.Agent},
		{"mode", r.Mode, c.Mode},
		{"status", r.Status, contract.Session.StartStatus},
		{"workspace", r.Workspace, ws},
		{"workspace_root", r.WorkspaceRoot, ws},
		{"worktree_path", r.WorktreePath, ws},
		{"workspace_origin", r.WorkspaceOrigin, c.SourceWorkspace},
		{"audit_log_path", r.AuditLogPath, layout.auditLog},
		{"started_at", r.StartedAt, c.StartedAt},
		{"observed_at", r.ObservedAt, c.StartedAt},
		{"initial_assurance", r.InitialAssurance, contract.Session.Assurance},
		{"current_assurance", r.CurrentAssurance, contract.Session.Assurance},
		{"workspace_control_plane", r.WorkspaceControlPlane, contract.Session.WorkspaceControlPlane},
		{"bootstrap_id", r.BootstrapID, c.BootstrapID},
		{"image_ref", r.ImageRef, c.ImageRef},
	})
}

// validateFinishedSession pins the fields of the AUTHORITATIVE state-root record (passed in,
// NOT read from the target-reported RecordPath).
func validateFinishedSession(contract Contract, r sessions.SessionRecord, c ConformanceCase) error {
	return checkFields("finished session", []fieldCheck{
		{"session_id", r.SessionID, c.SessionID},
		{"target_id", r.TargetID, c.TargetID},
		{"status", r.Status, contract.Session.FinalStatus},
		{"live_status", r.LiveStatus, "stopped"},
		{"finished_at", r.FinishedAt, c.FinishedAt},
		{"observed_at", r.ObservedAt, c.FinishedAt},
		{"exit_status", r.ExitStatus, c.ExitStatus},
		{"final_assurance", r.FinalAssurance, contract.Session.Assurance},
		{"current_assurance", r.CurrentAssurance, contract.Session.Assurance},
	})
}

// validateAuditEvents requires exactly the required events in order (exact event=
// parse), that each event carries the id fields required for it (present AND
// matching), and that any other id a line carries also matches.
func validateAuditEvents(records, required []string, ids map[string]string) error {
	if len(records) != len(required) {
		return fmt.Errorf("audit record len = %d, want %d", len(records), len(required))
	}
	for idx, event := range required {
		fields, err := auditLineFields(records[idx])
		if err != nil {
			return fmt.Errorf("audit record %d: %w", idx, err)
		}
		if fields["event"] != event {
			return fmt.Errorf("audit record %d event = %q, want %q", idx, fields["event"], event)
		}
		for _, key := range auditRequiredIDs[event] {
			got, present := fields[key]
			if !present {
				return fmt.Errorf("audit record %d (%s) missing required %s", idx, event, key)
			}
			if got != ids[key] {
				return fmt.Errorf("audit record %d %s = %q, want %q", idx, key, got, ids[key])
			}
		}
		for key, wantID := range ids {
			if got, present := fields[key]; present && got != wantID {
				return fmt.Errorf("audit record %d %s = %q, want %q", idx, key, got, wantID)
			}
		}
	}
	return nil
}

func auditLineFields(line string) (map[string]string, error) {
	fields := make(map[string]string)
	for _, token := range strings.Fields(line) {
		if key, value, ok := strings.Cut(token, "="); ok {
			// Reject a duplicate key: a last-wins map would let a corrupted line like
			// `session_id=evil session_id=<expected>` pass by masking the injected value,
			// so a repeated identity field can never override/mask a required check.
			if _, dup := fields[key]; dup {
				return nil, fmt.Errorf("audit line has a duplicate %q field: %q", key, line)
			}
			fields[key] = value
		}
	}
	return fields, nil
}
