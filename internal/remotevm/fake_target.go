// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package remotevm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/omkhar/workcell/internal/hostutil"
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
}

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
	Record       hostutil.SessionRecord
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

func (f FakeTarget) MaterializeWorkspace(_ context.Context, req MaterializeRequest) (MaterializeResult, error) {
	if strings.TrimSpace(req.StateRoot) == "" {
		return MaterializeResult{}, fmt.Errorf("state root is required")
	}
	targetProvider, err := statePathSegment("target provider", f.Contract.TargetProvider)
	if err != nil {
		return MaterializeResult{}, err
	}
	targetID, err := statePathSegment("target id", req.TargetID)
	if err != nil {
		return MaterializeResult{}, err
	}
	materializationID, err := statePathSegment("materialization id", req.MaterializationID)
	if err != nil {
		return MaterializeResult{}, err
	}
	if strings.TrimSpace(req.SourceWorkspace) == "" {
		return MaterializeResult{}, fmt.Errorf("source workspace is required")
	}
	targetRoot := targetRoot(req.StateRoot, targetProvider, targetID)
	materializationRoot := filepath.Join(targetRoot, "materializations", materializationID)
	workspaceRoot := filepath.Join(materializationRoot, f.Contract.WorkspaceMaterialization.WorkspaceDir)
	manifestPath := filepath.Join(materializationRoot, f.Contract.WorkspaceMaterialization.ManifestName)
	if err := os.RemoveAll(materializationRoot); err != nil {
		return MaterializeResult{}, err
	}
	if err := os.MkdirAll(workspaceRoot, 0o755); err != nil {
		return MaterializeResult{}, err
	}
	entries, err := copyWorkspaceTree(req.SourceWorkspace, workspaceRoot, f.Contract.WorkspaceMaterialization.ExcludedPaths)
	if err != nil {
		return MaterializeResult{}, err
	}
	manifest := WorkspaceManifest{
		Version:               1,
		TargetKind:            f.Contract.TargetKind,
		TargetProvider:        f.Contract.TargetProvider,
		TargetID:              targetID,
		WorkspaceTransport:    f.Contract.WorkspaceTransport,
		SourceWorkspace:       req.SourceWorkspace,
		MaterializationID:     materializationID,
		MaterializedWorkspace: workspaceRoot,
		ExcludedPaths:         append([]string(nil), f.Contract.WorkspaceMaterialization.ExcludedPaths...),
		Entries:               entries,
	}
	if err := writeJSON(manifestPath, manifest); err != nil {
		return MaterializeResult{}, err
	}
	return MaterializeResult{
		TargetRoot:            targetRoot,
		MaterializationRoot:   materializationRoot,
		ManifestPath:          manifestPath,
		MaterializedWorkspace: workspaceRoot,
		Manifest:              manifest,
	}, nil
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
	record := hostutil.SessionRecord{
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
	if err := hostutil.WriteSessionRecord(recordPath, map[string]string{
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
	record, err = hostutil.ReadSessionRecord(recordPath)
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
	if err := hostutil.WriteSessionRecord(req.Started.RecordPath, map[string]string{
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
	record, err := hostutil.ReadSessionRecord(req.Started.RecordPath)
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

func copyWorkspaceTree(sourceRoot, destRoot string, excluded []string) ([]WorkspaceEntry, error) {
	entries := make([]WorkspaceEntry, 0)
	err := filepath.WalkDir(sourceRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == sourceRoot {
			return nil
		}
		rel, err := filepath.Rel(sourceRoot, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if isExcludedPath(rel, excluded) {
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
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
				return err
			}
			if err := os.Symlink(target, destPath); err != nil {
				return err
			}
			entries = append(entries, WorkspaceEntry{Path: rel, Kind: "symlink", Mode: info.Mode(), LinkTarget: target})
		case info.IsDir():
			if err := os.MkdirAll(destPath, info.Mode().Perm()); err != nil {
				return err
			}
			entries = append(entries, WorkspaceEntry{Path: rel, Kind: "dir", Mode: info.Mode()})
		case info.Mode().IsRegular():
			if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
				return err
			}
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if err := os.WriteFile(destPath, content, info.Mode().Perm()); err != nil {
				return err
			}
			sum := sha256.Sum256(content)
			entries = append(entries, WorkspaceEntry{
				Path:   rel,
				Kind:   "file",
				Mode:   info.Mode(),
				SHA256: hex.EncodeToString(sum[:]),
			})
		default:
			return fmt.Errorf("unsupported workspace entry kind for %s", rel)
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

func isExcludedPath(rel string, excluded []string) bool {
	for _, item := range excluded {
		if rel == item || strings.HasPrefix(rel, item+"/") {
			return true
		}
	}
	return false
}

func writeJSON(path string, value any) error {
	content, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	content = append(content, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, content, 0o644)
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
