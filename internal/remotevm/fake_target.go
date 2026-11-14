// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package remotevm

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/omkhar/workcell/internal/host/sessions"
	"github.com/omkhar/workcell/internal/pathutil"
	"github.com/omkhar/workcell/internal/rootio"
	"github.com/omkhar/workcell/internal/workspacelimit"
	"golang.org/x/sys/unix"
)

type WorkspaceEntry struct {
	Path       string      `json:"path"`
	Kind       string      `json:"kind"`
	Mode       fs.FileMode `json:"mode"`
	SHA256     string      `json:"sha256,omitempty"`
	LinkTarget string      `json:"link_target,omitempty"`
}

type WorkspaceManifest struct {
	Version               int              `json:"version"`
	TargetKind            string           `json:"target_kind"`
	TargetProvider        string           `json:"target_provider"`
	TargetID              string           `json:"target_id"`
	WorkspaceTransport    string           `json:"workspace_transport"`
	SourceWorkspace       string           `json:"source_workspace"`
	MaterializationID     string           `json:"materialization_id"`
	MaterializedWorkspace string           `json:"materialized_workspace"`
	ExcludedPaths         []string         `json:"excluded_paths"`
	Entries               []WorkspaceEntry `json:"entries"`
	ArchiveSHA256         string           `json:"archive_sha256,omitempty"`
	// GuestTransfer is GuestTransferUnconfirmed from the time a brokered
	// target starts the broker until the guest acknowledges the archive.
	GuestTransfer string `json:"guest_transfer,omitempty"`
}

// GuestTransferUnconfirmed marks a host manifest whose archive went to the
// broker without a guest acknowledgment that the host accepted. The guest can
// hold this workspace, an earlier one with the same ID, or none.
const GuestTransferUnconfirmed = "unconfirmed"

type BootstrapManifest struct {
	Version              int    `json:"version"`
	TargetKind           string `json:"target_kind"`
	TargetProvider       string `json:"target_provider"`
	TargetID             string `json:"target_id"`
	TargetAssuranceClass string `json:"target_assurance_class"`
	SupportBoundary      string `json:"support_boundary"`
	RuntimeAPI           string `json:"runtime_api"`
	AccessModel          string `json:"access_model"`
	BootstrapID          string `json:"bootstrap_id"`
	ImageRef             string `json:"image_ref"`
}

type MaterializeRequest struct {
	StateRoot         string
	TargetID          string
	MaterializationID string
	SourceWorkspace   string
}

type MaterializeResult struct {
	TargetRoot            string
	MaterializationRoot   string
	ManifestPath          string
	MaterializedWorkspace string
	Manifest              WorkspaceManifest
}

type BootstrapRequest struct {
	StateRoot   string
	TargetID    string
	BootstrapID string
	ImageRef    string
}

type BootstrapResult struct {
	TargetRoot   string
	ManifestPath string
	AuditLogPath string
	Manifest     BootstrapManifest
}

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

type ConformanceTarget interface {
	MaterializeWorkspace(context.Context, MaterializeRequest) (MaterializeResult, error)
	BootstrapTarget(context.Context, BootstrapRequest) (BootstrapResult, error)
	StartSession(context.Context, StartSessionRequest) (SessionResult, error)
	FinishSession(context.Context, FinishSessionRequest) (SessionResult, error)
}

type FakeTarget struct {
	Contract Contract
}

func NewFakeTarget(contract Contract) (FakeTarget, error) {
	if contract.Version == 0 &&
		contract.TargetKind == "" &&
		contract.TargetProvider == "" &&
		contract.TargetAssuranceClass == "" {
		contract = DefaultContract()
	}
	if err := contract.Validate(); err != nil {
		return FakeTarget{}, err
	}
	return FakeTarget{Contract: contract}, nil
}

func (f FakeTarget) MaterializeWorkspace(ctx context.Context, req MaterializeRequest) (MaterializeResult, error) {
	return materializeInStateRoot(ctx, f.Contract, req)
}

// materializeInStateRoot stages the source workspace in the host state root
// and writes its manifest. The fake target stops here.
func materializeInStateRoot(ctx context.Context, contract Contract, req MaterializeRequest) (MaterializeResult, error) {
	result, staged, err := stageInStateRoot(ctx, contract, req)
	if err != nil {
		return MaterializeResult{}, err
	}
	err = staged.commit(result.ManifestPath, result.Manifest)
	if err = errors.Join(err, staged.close()); err != nil {
		return MaterializeResult{}, err
	}
	return result, nil
}

// stageInStateRoot copies the source workspace into the host state root,
// beside the host copy of an earlier run with the same ID, and returns its
// manifest unwritten. The caller puts the copy in place and writes the
// manifest together through the returned stagedHostCopy, and then closes it.
func stageInStateRoot(ctx context.Context, contract Contract, req MaterializeRequest) (MaterializeResult, *stagedHostCopy, error) {
	if strings.TrimSpace(req.StateRoot) == "" {
		return MaterializeResult{}, nil, fmt.Errorf("state root is required")
	}
	targetProvider, err := statePathSegment("target provider", contract.TargetProvider)
	if err != nil {
		return MaterializeResult{}, nil, err
	}
	targetID, err := statePathSegment("target id", req.TargetID)
	if err != nil {
		return MaterializeResult{}, nil, err
	}
	materializationID, err := statePathSegment("materialization id", req.MaterializationID)
	if err != nil {
		return MaterializeResult{}, nil, err
	}
	if strings.TrimSpace(req.SourceWorkspace) == "" {
		return MaterializeResult{}, nil, fmt.Errorf("source workspace is required")
	}
	workspaceDir, err := statePathSegment("workspace dir", contract.WorkspaceMaterialization.WorkspaceDir)
	if err != nil {
		return MaterializeResult{}, nil, err
	}
	targetRoot := targetRoot(req.StateRoot, targetProvider, targetID)
	materializationRoot := filepath.Join(targetRoot, "materializations", materializationID)
	workspaceRoot := filepath.Join(materializationRoot, workspaceDir)
	manifestPath := filepath.Join(materializationRoot, contract.WorkspaceMaterialization.ManifestName)
	if err := mkdirAllSynced(materializationRoot); err != nil {
		return MaterializeResult{}, nil, err
	}
	// The copy keeps the source file modes, so the directory above it must
	// keep other local accounts out. An earlier run can have left it open.
	if err := restrictToOwner(materializationRoot); err != nil {
		return MaterializeResult{}, nil, err
	}
	staged, entries, err := stageWorkspaceCopy(ctx, req.SourceWorkspace, materializationRoot, workspaceDir, contract.WorkspaceMaterialization.ManifestName, contract.WorkspaceMaterialization.ExcludedPaths)
	if err != nil {
		return MaterializeResult{}, nil, err
	}
	manifest := WorkspaceManifest{
		Version:               1,
		TargetKind:            contract.TargetKind,
		TargetProvider:        contract.TargetProvider,
		TargetID:              targetID,
		WorkspaceTransport:    contract.WorkspaceTransport,
		SourceWorkspace:       req.SourceWorkspace,
		MaterializationID:     materializationID,
		MaterializedWorkspace: workspaceRoot,
		ExcludedPaths:         append([]string(nil), contract.WorkspaceMaterialization.ExcludedPaths...),
		Entries:               entries,
	}
	return MaterializeResult{
		TargetRoot:            targetRoot,
		MaterializationRoot:   materializationRoot,
		ManifestPath:          manifestPath,
		MaterializedWorkspace: workspaceRoot,
		Manifest:              manifest,
	}, staged, nil
}

// stagedHostCopy is a host copy staged beside the host workspace. commit puts
// it in place together with its manifest, and close removes it if it was
// never put in place.
type stagedHostCopy struct {
	parent       *os.File
	name         string
	workspaceDir string
	path         string
	// leftovers are the names that earlier runs left. Only a committed
	// send removes them.
	leftovers []string
}

// stageWorkspaceCopy recovers what an interrupted earlier run left, and then
// copies the source workspace into a new sibling of the workspace directory.
// A failed copy removes the sibling and leaves the earlier host copy, which a
// kept manifest of that run still describes.
func stageWorkspaceCopy(ctx context.Context, sourceWorkspace, materializationRoot, workspaceDir, manifestName string, excluded []string) (*stagedHostCopy, []WorkspaceEntry, error) {
	// The parent of a name in materializationRoot is that root, opened
	// without a follow.
	parent, _, err := rootio.OpenParentDirectoryNoFollow(filepath.Join(materializationRoot, workspaceDir))
	if err != nil {
		return nil, nil, err
	}
	// A second send of this ID waits for this lock on the parent, which close
	// ends. A child that a fork starts holds the lock too until its exec, so
	// a send that does not wait could fail with no second send running. The
	// wait polls, so that it ends with ctx.
	err = unix.Flock(int(parent.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	for wait := time.Millisecond; errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EINTR); wait = min(2*wait, 100*time.Millisecond) {
		select {
		case <-ctx.Done():
			err = ctx.Err()
		case <-time.After(wait):
			err = unix.Flock(int(parent.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		}
	}
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		return nil, nil, errors.Join(err, parent.Close())
	}
	leftovers, err := recoverInterruptedSwap(parent, materializationRoot, workspaceDir, manifestName)
	if err != nil {
		return nil, nil, errors.Join(err, parent.Close())
	}
	name, err := mkdirTempAt(parent, "."+workspaceDir+stagingInfix)
	if err != nil {
		return nil, nil, errors.Join(err, parent.Close())
	}
	staged := &stagedHostCopy{parent: parent, name: name, workspaceDir: workspaceDir, path: filepath.Join(materializationRoot, name), leftovers: leftovers}
	entries, err := copyWorkspaceTree(sourceWorkspace, staged.path, excluded)
	if err != nil {
		return nil, nil, errors.Join(err, staged.close())
	}
	return staged, entries, nil
}

const (
	stagingInfix  = "-staging-"
	previousInfix = "-previous-"
	archivePrefix = ".archive-"
)

// maxManifestBytes caps a manifest when it is written and when it is read
// back. Tests lower it.
var maxManifestBytes int64 = 64 << 20

// recoverInterruptedSwap makes sure that the host workspace is the copy that
// the kept manifest describes, if a run killed part way left that copy under
// a staging or previous name. It compares content, so it needs no record of
// the swap. It never removes a name: it moves the workspace aside and renames
// the matching copy into its place. It returns the names that earlier runs
// left, which a committed send removes. A copy that it scans must be a real
// directory that this user owns. Two sends of the same ID must not run at
// once.
func recoverInterruptedSwap(parent *os.File, materializationRoot, workspaceDir, manifestName string) ([]string, error) {
	names, err := parent.Readdirnames(-1)
	if err != nil {
		return nil, err
	}
	var leftovers, candidates []string
	for _, name := range names {
		copyName := strings.HasPrefix(name, "."+workspaceDir+stagingInfix) || strings.HasPrefix(name, "."+workspaceDir+previousInfix)
		if copyName || strings.HasPrefix(name, archivePrefix) || strings.HasPrefix(name, ".manifest-") {
			leftovers = append(leftovers, name)
		}
		if copyName && ownedDirectoryAt(parent, name) {
			candidates = append(candidates, name)
		}
	}
	if len(candidates) == 0 {
		return leftovers, nil
	}
	raw, err := rootio.ReadFileAtNoFollow(parent, manifestName, "host manifest", maxManifestBytes)
	if errors.Is(err, os.ErrNotExist) {
		return leftovers, nil
	}
	var kept WorkspaceManifest
	if err == nil {
		err = json.Unmarshal(raw, &kept)
	}
	if err == nil && (kept.Version != 1 || kept.Entries == nil) {
		err = errors.New("no version 1 manifest with entries")
	}
	if err != nil {
		return nil, fmt.Errorf("read the kept host manifest: %w", err)
	}
	matches := func(name string) bool {
		entries, err := copyWorkspaceTree(filepath.Join(materializationRoot, name), "", nil)
		return err == nil && slices.EqualFunc(entries, kept.Entries, func(a, b WorkspaceEntry) bool {
			return a.Path == b.Path && a.Kind == b.Kind && a.SHA256 == b.SHA256 && a.LinkTarget == b.LinkTarget &&
				(a.Kind == "symlink" || a.Mode.Perm() == b.Mode.Perm())
		})
	}
	if ownedDirectoryAt(parent, workspaceDir) && matches(workspaceDir) {
		return leftovers, nil
	}
	for _, name := range candidates {
		if !matches(name) {
			continue
		}
		aside, err := moveAside(parent, workspaceDir)
		if err != nil {
			return nil, err
		}
		if err := unix.Renameat(int(parent.Fd()), name, int(parent.Fd()), workspaceDir); err != nil {
			return nil, err
		}
		leftovers = slices.DeleteFunc(leftovers, func(n string) bool { return n == name })
		if aside != "" {
			leftovers = append(leftovers, aside)
		}
		return leftovers, parent.Sync()
	}
	return leftovers, nil
}

// ownedDirectoryAt reports whether name in parent is a directory, not a
// symlink, that this user owns.
func ownedDirectoryAt(parent *os.File, name string) bool {
	st, err := rootio.LstatAtNoFollow(parent, name)
	return err == nil && st.Mode&unix.S_IFMT == unix.S_IFDIR && int(st.Uid) == os.Geteuid()
}

// moveAside renames name in parent, if it is there, to a new previous name
// and returns that name. It moves a non-directory too, without a follow.
func moveAside(parent *os.File, name string) (string, error) {
	fd := int(parent.Fd())
	aside, err := mkdirTempAt(parent, "."+name+previousInfix)
	if err != nil {
		return "", err
	}
	// Renaming a directory onto the new empty directory replaces it in one
	// step. A non-directory cannot replace it, so it takes the freed name.
	err = unix.Renameat(fd, name, fd, aside)
	if errors.Is(err, unix.EISDIR) || errors.Is(err, unix.ENOTDIR) {
		if err = unix.Unlinkat(fd, aside, unix.AT_REMOVEDIR); err == nil {
			err = unix.Renameat(fd, name, fd, aside)
		}
	}
	if errors.Is(err, unix.ENOENT) {
		return "", unix.Unlinkat(fd, aside, unix.AT_REMOVEDIR)
	}
	return aside, err
}

// commit moves the earlier host copy aside, moves the staged copy into its
// place, and writes the manifest. If a step fails before the manifest is in
// place, the earlier host copy goes back, so the host copy and its manifest
// change together. A kill or a failed rollback leaves both copies, and the
// next run puts back the one that the kept manifest describes. Only after
// the manifest is written does commit remove the earlier copy and the
// leftovers.
func (s *stagedHostCopy) commit(manifestPath string, manifest any) error {
	content, err := manifestContent(manifest)
	if err != nil {
		return err
	}
	fd := int(s.parent.Fd())
	if err := swapStep("first rename", s.parent); err != nil {
		return err
	}
	previous, err := moveAside(s.parent, s.workspaceDir)
	if err != nil {
		return err
	}
	placed := false
	rollback := func(err error) error {
		// close removes the new copy from its staged name.
		if placed {
			err = errors.Join(err, unix.Renameat(fd, s.workspaceDir, fd, s.name))
		}
		if previous != "" {
			err = errors.Join(err, unix.Renameat(fd, previous, fd, s.workspaceDir))
		}
		return errors.Join(err, s.parent.Sync())
	}
	if err := swapStep("second rename", s.parent); err != nil {
		return rollback(err)
	}
	if err := unix.Renameat(fd, s.name, fd, s.workspaceDir); err != nil {
		return rollback(err)
	}
	placed = true
	if err := s.parent.Sync(); err != nil {
		return rollback(err)
	}
	name := filepath.Base(manifestPath)
	if err := writeManifestAt(s.parent, name, content, 0o600, ".manifest-"); err != nil {
		// A write can fail after its rename. Roll back only when the kept
		// manifest is known not to be the new one. Otherwise the new copy
		// stays in place with it, and the next run checks both.
		kept, readErr := rootio.ReadFileAtNoFollow(s.parent, name, "host manifest", maxManifestBytes)
		if errors.Is(readErr, os.ErrNotExist) || readErr == nil && !bytes.Equal(kept, content) {
			return rollback(err)
		}
		return err
	}
	for _, name := range append(s.leftovers, previous) {
		if name != "" {
			_ = rootio.RemoveAllAtNoFollow(s.parent, name)
		}
	}
	_ = swapStep("done", s.parent)
	return s.parent.Sync()
}

// close removes the staged copy if it is still under its staged name, which
// no kept manifest then describes, and closes the parent directory. Closing
// the parent ends the lock.
func (s *stagedHostCopy) close() error {
	return errors.Join(rootio.RemoveAllAtNoFollow(s.parent, s.name), s.parent.Close())
}

// mkdirTempAt creates a new owner-only directory under parent with a random
// name that starts with prefix, and returns that name.
func mkdirTempAt(parent *os.File, prefix string) (string, error) {
	for range 32 {
		var suffix [8]byte
		if _, err := rand.Read(suffix[:]); err != nil {
			return "", err
		}
		name := prefix + hex.EncodeToString(suffix[:])
		err := unix.Mkdirat(int(parent.Fd()), name, 0o700)
		if err == nil {
			return name, nil
		}
		if !errors.Is(err, unix.EEXIST) {
			return "", err
		}
	}
	return "", fmt.Errorf("cannot create a staging directory under %q", parent.Name())
}

func (f FakeTarget) BootstrapTarget(_ context.Context, req BootstrapRequest) (BootstrapResult, error) {
	if strings.TrimSpace(req.StateRoot) == "" {
		return BootstrapResult{}, fmt.Errorf("state root is required")
	}
	targetProvider, err := statePathSegment("target provider", f.Contract.TargetProvider)
	if err != nil {
		return BootstrapResult{}, err
	}
	targetID, err := statePathSegment("target id", req.TargetID)
	if err != nil {
		return BootstrapResult{}, err
	}
	if strings.TrimSpace(req.BootstrapID) == "" {
		return BootstrapResult{}, fmt.Errorf("bootstrap id is required")
	}
	if strings.TrimSpace(req.ImageRef) == "" {
		return BootstrapResult{}, fmt.Errorf("image ref is required")
	}
	targetRoot := targetRoot(req.StateRoot, targetProvider, targetID)
	bootstrapRoot := filepath.Join(targetRoot, "bootstrap")
	if err := os.MkdirAll(bootstrapRoot, 0o755); err != nil {
		return BootstrapResult{}, err
	}
	manifestPath := filepath.Join(bootstrapRoot, f.Contract.Bootstrap.ManifestName)
	manifest := BootstrapManifest{
		Version:              1,
		TargetKind:           f.Contract.TargetKind,
		TargetProvider:       f.Contract.TargetProvider,
		TargetID:             targetID,
		TargetAssuranceClass: f.Contract.TargetAssuranceClass,
		SupportBoundary:      f.Contract.SupportBoundary,
		RuntimeAPI:           f.Contract.RuntimeAPI,
		AccessModel:          f.Contract.AccessModel,
		BootstrapID:          req.BootstrapID,
		ImageRef:             req.ImageRef,
	}
	if err := writeJSON(manifestPath, manifest); err != nil {
		return BootstrapResult{}, err
	}
	return BootstrapResult{
		TargetRoot:   targetRoot,
		ManifestPath: manifestPath,
		AuditLogPath: filepath.Join(targetRoot, "workcell.audit.log"),
		Manifest:     manifest,
	}, nil
}

func (f FakeTarget) StartSession(_ context.Context, req StartSessionRequest) (SessionResult, error) {
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
	if _, err := os.Stat(req.Materialization.ManifestPath); err != nil {
		return SessionResult{}, err
	}
	if _, err := os.Stat(req.Bootstrap.ManifestPath); err != nil {
		return SessionResult{}, err
	}
	recordPath := filepath.Join(req.Bootstrap.TargetRoot, "sessions", sessionID+".json")
	record := sessions.SessionRecord{
		Version:               1,
		SessionID:             sessionID,
		Profile:               req.Bootstrap.Manifest.TargetID,
		TargetKind:            f.Contract.TargetKind,
		TargetProvider:        f.Contract.TargetProvider,
		TargetID:              req.Bootstrap.Manifest.TargetID,
		TargetAssuranceClass:  f.Contract.TargetAssuranceClass,
		RuntimeAPI:            f.Contract.RuntimeAPI,
		WorkspaceTransport:    f.Contract.WorkspaceTransport,
		Agent:                 req.Agent,
		Mode:                  req.Mode,
		Status:                f.Contract.Session.StartStatus,
		Workspace:             req.Materialization.MaterializedWorkspace,
		WorkspaceOrigin:       req.Materialization.Manifest.SourceWorkspace,
		WorkspaceRoot:         req.Materialization.MaterializedWorkspace,
		WorktreePath:          req.Materialization.MaterializedWorkspace,
		AuditLogPath:          req.Bootstrap.AuditLogPath,
		StartedAt:             req.StartedAt,
		ObservedAt:            req.StartedAt,
		InitialAssurance:      f.Contract.Session.Assurance,
		CurrentAssurance:      f.Contract.Session.Assurance,
		WorkspaceControlPlane: f.Contract.Session.WorkspaceControlPlane,
	}
	if err := sessions.WriteSessionRecord(recordPath, map[string]string{
		"session_id":              record.SessionID,
		"profile":                 record.Profile,
		"target_kind":             record.TargetKind,
		"target_provider":         record.TargetProvider,
		"target_id":               record.TargetID,
		"target_assurance_class":  record.TargetAssuranceClass,
		"runtime_api":             record.RuntimeAPI,
		"workspace_transport":     record.WorkspaceTransport,
		"agent":                   record.Agent,
		"mode":                    record.Mode,
		"status":                  record.Status,
		"workspace":               record.Workspace,
		"workspace_origin":        record.WorkspaceOrigin,
		"workspace_root":          record.WorkspaceRoot,
		"worktree_path":           record.WorktreePath,
		"audit_log_path":          record.AuditLogPath,
		"started_at":              record.StartedAt,
		"observed_at":             record.ObservedAt,
		"initial_assurance":       record.InitialAssurance,
		"current_assurance":       record.CurrentAssurance,
		"workspace_control_plane": record.WorkspaceControlPlane,
	}); err != nil {
		return SessionResult{}, err
	}
	for _, line := range []string{
		fmt.Sprintf("ts=%s session_id=%s event=workspace_materialized target_kind=%s target_provider=%s target_id=%s workspace_transport=%s materialization_id=%s workspace_origin=%s workspace=%s", req.StartedAt, sessionID, f.Contract.TargetKind, f.Contract.TargetProvider, req.Bootstrap.Manifest.TargetID, f.Contract.WorkspaceTransport, req.Materialization.Manifest.MaterializationID, req.Materialization.Manifest.SourceWorkspace, req.Materialization.MaterializedWorkspace),
		fmt.Sprintf("ts=%s session_id=%s event=bootstrap_ready target_kind=%s target_provider=%s target_id=%s runtime_api=%s access_model=%s bootstrap_id=%s image_ref=%s", req.StartedAt, sessionID, f.Contract.TargetKind, f.Contract.TargetProvider, req.Bootstrap.Manifest.TargetID, f.Contract.RuntimeAPI, f.Contract.AccessModel, req.Bootstrap.Manifest.BootstrapID, req.Bootstrap.Manifest.ImageRef),
		fmt.Sprintf("ts=%s session_id=%s event=session_started target_kind=%s target_provider=%s target_id=%s status=%s workspace_control_plane=%s", req.StartedAt, sessionID, f.Contract.TargetKind, f.Contract.TargetProvider, req.Bootstrap.Manifest.TargetID, f.Contract.Session.StartStatus, f.Contract.Session.WorkspaceControlPlane),
	} {
		if err := appendAuditLine(req.Bootstrap.AuditLogPath, line); err != nil {
			return SessionResult{}, err
		}
	}
	record, err = sessions.ReadSessionRecord(recordPath)
	if err != nil {
		return SessionResult{}, err
	}
	return SessionResult{Record: record, RecordPath: recordPath, AuditLogPath: req.Bootstrap.AuditLogPath}, nil
}

func (f FakeTarget) FinishSession(_ context.Context, req FinishSessionRequest) (SessionResult, error) {
	if strings.TrimSpace(req.FinishedAt) == "" {
		return SessionResult{}, fmt.Errorf("finished at is required")
	}
	if strings.TrimSpace(req.ExitStatus) == "" {
		return SessionResult{}, fmt.Errorf("exit status is required")
	}
	if req.Started.RecordPath == "" {
		return SessionResult{}, fmt.Errorf("started session record path is required")
	}
	if err := sessions.WriteSessionRecord(req.Started.RecordPath, map[string]string{
		"status":            f.Contract.Session.FinalStatus,
		"live_status":       "stopped",
		"observed_at":       req.FinishedAt,
		"finished_at":       req.FinishedAt,
		"exit_status":       req.ExitStatus,
		"final_assurance":   f.Contract.Session.Assurance,
		"current_assurance": f.Contract.Session.Assurance,
	}); err != nil {
		return SessionResult{}, err
	}
	if err := appendAuditLine(req.Started.AuditLogPath, fmt.Sprintf("ts=%s session_id=%s event=session_finished target_kind=%s target_provider=%s target_id=%s status=%s exit_status=%s", req.FinishedAt, req.Started.Record.SessionID, f.Contract.TargetKind, f.Contract.TargetProvider, req.Started.Record.TargetID, f.Contract.Session.FinalStatus, req.ExitStatus)); err != nil {
		return SessionResult{}, err
	}
	record, err := sessions.ReadSessionRecord(req.Started.RecordPath)
	if err != nil {
		return SessionResult{}, err
	}
	return SessionResult{Record: record, RecordPath: req.Started.RecordPath, AuditLogPath: req.Started.AuditLogPath}, nil
}

func targetRoot(stateRoot, targetProvider, targetID string) string {
	return filepath.Join(stateRoot, "targets", TargetKind, targetProvider, targetID)
}

func statePathSegment(label, value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("%s is required", label)
	}
	if strings.TrimSpace(value) != value {
		return "", fmt.Errorf("%s must not contain leading or trailing whitespace", label)
	}
	if value == "." || value == ".." || filepath.IsAbs(value) || strings.ContainsAny(value, `/\`) {
		return "", fmt.Errorf("%s must be a single path segment", label)
	}
	return value, nil
}

type workspaceLimits struct {
	maxFileBytes, maxBytes int64
	maxEntries             int
}

var defaultWorkspaceLimits = workspaceLimits{workspacelimit.MaxFileBytes, workspacelimit.MaxBytes, workspacelimit.MaxEntries}

func copyWorkspaceTree(sourceRoot, destRoot string, excluded []string) ([]WorkspaceEntry, error) {
	return copyWorkspaceTreeWithReservation(sourceRoot, destRoot, excluded, newWorkspaceDestinationState(), defaultWorkspaceLimits)
}

// An empty destRoot only scans sourceRoot and returns its entries.
func copyWorkspaceTreeWithReservation(sourceRoot, destRoot string, excluded []string, destinations workspaceDestinationReservation, limits workspaceLimits) ([]WorkspaceEntry, error) {
	entries, scanOnly := make([]WorkspaceEntry, 0), destRoot == ""
	entryCount, totalBytes := 0, int64(0)
	if destinations == nil {
		return nil, fmt.Errorf("workspace destination reservation is required")
	}
	resolvedRoot, err := filepath.EvalSymlinks(sourceRoot)
	if err != nil {
		return nil, err
	}
	source, err := os.OpenRoot(sourceRoot)
	if err != nil {
		return nil, err
	}
	defer source.Close()
	err = filepath.WalkDir(sourceRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == sourceRoot {
			return nil
		}
		if entryCount >= limits.maxEntries {
			return fmt.Errorf("workspace has more than %d entries", limits.maxEntries)
		}
		entryCount++
		if _, err := pathutil.CollisionKey(d.Name()); err != nil {
			return fmt.Errorf("invalid workspace entry: %w", err)
		}
		rel, err := filepath.Rel(sourceRoot, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		excludedPath, err := isExcludedPath(rel, excluded)
		if err != nil {
			return err
		}
		if excludedPath {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		destPath := filepath.Join(destRoot, filepath.FromSlash(rel))
		if err := destinations.reserve(rel); err != nil {
			return err
		}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return fmt.Errorf("read workspace symlink %q: %q", rel, err.Error())
			}
			if err := checkWorkspaceSymlink(resolvedRoot, path, rel, target); err != nil {
				return err
			}
			if !scanOnly {
				if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
					return err
				}
				if err := os.Symlink(target, destPath); err != nil {
					return err
				}
			}
			entries = append(entries, WorkspaceEntry{Path: rel, Kind: "symlink", Mode: info.Mode(), LinkTarget: target})
		case info.IsDir():
			if err := mkdirAllUnless(scanOnly, destPath, info.Mode().Perm()); err != nil {
				return err
			}
			// The umask must not change the modes that the manifest records.
			if err := chmodUnless(scanOnly, destPath, info.Mode().Perm()); err != nil {
				return err
			}
			entries = append(entries, WorkspaceEntry{Path: rel, Kind: "dir", Mode: info.Mode()})
		case info.Mode().IsRegular():
			if err := mkdirAllUnless(scanOnly, filepath.Dir(destPath), 0o755); err != nil {
				return err
			}
			// The bounded read stops one byte past the remaining budget, so an
			// oversized or growing file fails without a full read into memory.
			allowed := min(limits.maxFileBytes, limits.maxBytes-totalBytes)
			content, err := readWorkspaceFile(source, rel, info, allowed)
			if err != nil {
				return err
			}
			if int64(len(content)) > allowed {
				return workspaceSizeError(rel, int64(len(content)), limits)
			}
			totalBytes += int64(len(content))
			if !scanOnly {
				if err := os.WriteFile(destPath, content, info.Mode().Perm()); err != nil {
					return err
				}
				if err := os.Chmod(destPath, info.Mode().Perm()); err != nil {
					return err
				}
			}
			sum := sha256.Sum256(content)
			entries = append(entries, WorkspaceEntry{
				Path:   rel,
				Kind:   "file",
				Mode:   info.Mode(),
				SHA256: hex.EncodeToString(sum[:]),
			})
		default:
			return fmt.Errorf("unsupported workspace entry kind for %q", rel)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Path < entries[j].Path
	})
	return entries, nil
}

// checkWorkspaceSymlink fails closed unless the symlink at path, which is rel
// under the workspace root that resolves to resolvedRoot, stays inside it.
func checkWorkspaceSymlink(resolvedRoot, path, rel, target string) error {
	if err := checkWorkspaceSymlinkTarget(rel, target); err != nil {
		return err
	}
	// The lexical check above is evadable through symlink chains
	// (an in-workspace link to a parent directory re-routes a later
	// ".." through the kernel), so also require the kernel-resolved
	// target to stay inside the resolved workspace root. Dangling
	// links fail closed.
	physical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return fmt.Errorf("workspace symlink %q does not resolve inside the workspace: %q", rel, err.Error())
	}
	if physical != resolvedRoot && !strings.HasPrefix(physical, resolvedRoot+string(filepath.Separator)) {
		return fmt.Errorf("workspace symlink %q escapes the workspace: %q", rel, target)
	}
	return nil
}

// checkWorkspaceSymlinkTarget is the lexical half of the symlink rule. The
// remote-vm contract promises host-auditable materialization: a symlink that
// resolves outside the materialized workspace would smuggle non-workspace
// content past that audit, so fail closed.
func checkWorkspaceSymlinkTarget(rel, target string) error {
	if filepath.IsAbs(target) {
		return fmt.Errorf("workspace symlink %q targets an absolute path: %q", rel, target)
	}
	resolved := filepath.ToSlash(filepath.Clean(filepath.Join(filepath.Dir(filepath.FromSlash(rel)), target)))
	if resolved == ".." || strings.HasPrefix(resolved, "../") {
		return fmt.Errorf("workspace symlink %q escapes the workspace: %q", rel, target)
	}
	return nil
}

// readWorkspaceFile reads rel under source only when it is still the regular
// file that the walk classified as info. The root keeps the open inside the
// workspace when a writer swaps a parent or the leaf for a symlink, O_NONBLOCK
// keeps a swapped-in FIFO from blocking the open, and the SameFile check
// refuses any other object at rel.
func readWorkspaceFile(source *os.Root, rel string, info fs.FileInfo, allowed int64) ([]byte, error) {
	file, err := source.OpenFile(filepath.FromSlash(rel), os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(opened, info) {
		return nil, fmt.Errorf("workspace file %q changed during the copy", rel)
	}
	return io.ReadAll(io.LimitReader(file, allowed+1))
}

func workspaceSizeError(rel string, size int64, limits workspaceLimits) error {
	if size > limits.maxFileBytes {
		return fmt.Errorf("workspace file %q exceeds the per-file limit of %d bytes", rel, limits.maxFileBytes)
	}
	return fmt.Errorf("workspace exceeds the aggregate file-byte limit of %d bytes", limits.maxBytes)
}

func isExcludedPath(rel string, excluded []string) (bool, error) {
	if _, err := pathutil.CollisionKey(rel); err != nil {
		return false, err
	}
	for _, item := range excluded {
		inside, err := pathutil.WithinOrEqual(item, rel, true)
		if err != nil {
			return false, err
		}
		if inside {
			return true, nil
		}
	}
	return false, nil
}

type workspaceDestinationReservation interface {
	reserve(string) error
}

type workspaceDestinationState struct {
	paths map[string]string
}

func newWorkspaceDestinationState() *workspaceDestinationState {
	return &workspaceDestinationState{paths: make(map[string]string)}
}

func (s *workspaceDestinationState) reserve(path string) error {
	key, err := pathutil.CollisionKey(path)
	if err != nil {
		return fmt.Errorf("invalid workspace destination path: %w", err)
	}
	if previous, exists := s.paths[key]; exists {
		return fmt.Errorf("workspace destination path collision between %s and %q", previous, path)
	}
	s.paths[key] = fmt.Sprintf("%q", path)
	return nil
}

// mkdirAllSynced creates dir and its missing ancestors. From the deepest
// directory that exists, it opens each component without following a symlink
// and syncs each directory that gains an entry. A manifest that writeJSON
// then publishes in dir cannot lose a created ancestor in a crash. Each
// directory that it creates is owner-only.
func mkdirAllUnless(skip bool, dir string, perm os.FileMode) error {
	if skip {
		return nil
	}
	return os.MkdirAll(dir, perm)
}

func chmodUnless(skip bool, path string, perm os.FileMode) error {
	if skip {
		return nil
	}
	return os.Chmod(path, perm)
}

func mkdirAllSynced(dir string) error {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	existing := dir
	for {
		_, err := os.Lstat(existing)
		if err == nil {
			break
		}
		if !errors.Is(err, fs.ErrNotExist) || existing == filepath.Dir(existing) {
			return err
		}
		existing = filepath.Dir(existing)
	}
	relative, err := filepath.Rel(existing, dir)
	if err != nil || relative == "." {
		return err
	}
	// The parent of a name in existing is existing, opened without a follow.
	anchor, _, err := rootio.OpenParentDirectoryNoFollow(filepath.Join(existing, "entry"))
	if err != nil {
		return err
	}
	defer anchor.Close()
	return rootio.MkdirAllSyncedAt(anchor, relative, 0o700)
}

// restrictToOwner gives dir mode 0700 through a descriptor that no symlink
// can redirect. It fails for a directory that another account owns.
func restrictToOwner(dir string) error {
	handle, _, err := rootio.OpenParentDirectoryNoFollow(filepath.Join(dir, "entry"))
	if err != nil {
		return err
	}
	defer handle.Close()
	if err := handle.Chmod(0o700); err != nil {
		return err
	}
	return handle.Sync()
}

// manifestContent is the exact content that writeJSON writes for value.
func manifestContent(value any) ([]byte, error) {
	content, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	// A manifest that is written must be one that can be read back.
	if content = append(content, '\n'); int64(len(content)) > maxManifestBytes {
		return nil, fmt.Errorf("manifest of %d bytes exceeds the limit of %d bytes", len(content), maxManifestBytes)
	}
	return content, nil
}

func writeJSON(path string, value any) error {
	content, err := manifestContent(value)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	// A manifest is the host record of a transfer, so it is staged, synced and
	// renamed into place with its parent synced: a crash or a failed write
	// leaves the old manifest or the new one, never a truncated file. The
	// staged file gets mode 0600 through its descriptor, so a rewrite never
	// keeps an older file's permissions.
	parent, cleaned, err := rootio.OpenParentDirectoryNoFollow(path)
	if err != nil {
		return err
	}
	defer parent.Close()
	return rootio.WriteFileAtomicAtNoFollow(parent, filepath.Base(cleaned), content, 0o600, ".manifest-")
}

func appendAuditLine(path, line string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	handle, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer handle.Close()
	if _, err := io.WriteString(handle, line+"\n"); err != nil {
		return err
	}
	return nil
}
