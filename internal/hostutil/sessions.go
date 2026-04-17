// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package hostutil

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type SessionRecord struct {
	Version               int    `json:"version"`
	SessionID             string `json:"session_id"`
	Profile               string `json:"profile"`
	Agent                 string `json:"agent"`
	Mode                  string `json:"mode"`
	Status                string `json:"status"`
	UI                    string `json:"ui,omitempty"`
	ExecutionPath         string `json:"execution_path,omitempty"`
	Workspace             string `json:"workspace"`
	WorkspaceOrigin       string `json:"workspace_origin,omitempty"`
	WorkspaceRoot         string `json:"workspace_root,omitempty"`
	WorktreePath          string `json:"worktree_path,omitempty"`
	GitBranch             string `json:"git_branch,omitempty"`
	GitHead               string `json:"git_head,omitempty"`
	GitBase               string `json:"git_base,omitempty"`
	ContainerName         string `json:"container_name,omitempty"`
	MonitorPID            string `json:"monitor_pid,omitempty"`
	LiveStatus            string `json:"live_status,omitempty"`
	SessionAuditDir       string `json:"session_audit_dir,omitempty"`
	AuditLogPath          string `json:"audit_log_path,omitempty"`
	DebugLogPath          string `json:"debug_log_path,omitempty"`
	FileTraceLogPath      string `json:"file_trace_log_path,omitempty"`
	TranscriptLogPath     string `json:"transcript_log_path,omitempty"`
	StartedAt             string `json:"started_at"`
	ObservedAt            string `json:"observed_at,omitempty"`
	FinishedAt            string `json:"finished_at,omitempty"`
	ExitStatus            string `json:"exit_status,omitempty"`
	InitialAssurance      string `json:"initial_assurance,omitempty"`
	CurrentAssurance      string `json:"current_assurance,omitempty"`
	FinalAssurance        string `json:"final_assurance,omitempty"`
	WorkspaceControlPlane string `json:"workspace_control_plane,omitempty"`
}

type SessionListOptions struct {
	Workspace string
	Profile   string
}

type SessionExport struct {
	Session      SessionRecord `json:"session"`
	AuditRecords []string      `json:"audit_records,omitempty"`
}

func SessionDiffMetadataLines(record SessionRecord) []string {
	return []string{
		fmt.Sprintf("workspace=%s", record.Workspace),
		fmt.Sprintf("workspace_origin=%s", record.WorkspaceOrigin),
		fmt.Sprintf("workspace_root=%s", record.WorkspaceRoot),
		fmt.Sprintf("worktree_path=%s", record.WorktreePath),
		fmt.Sprintf("git_branch=%s", record.GitBranch),
		fmt.Sprintf("git_head=%s", record.GitHead),
		fmt.Sprintf("git_base=%s", record.GitBase),
		fmt.Sprintf("status=%s", record.Status),
		fmt.Sprintf("live_status=%s", record.LiveStatus),
		fmt.Sprintf("current_assurance=%s", record.CurrentAssurance),
		fmt.Sprintf("audit_log_path=%s", record.AuditLogPath),
		fmt.Sprintf("debug_log_path=%s", record.DebugLogPath),
		fmt.Sprintf("file_trace_log_path=%s", record.FileTraceLogPath),
		fmt.Sprintf("transcript_log_path=%s", record.TranscriptLogPath),
		fmt.Sprintf("container_name=%s", record.ContainerName),
		fmt.Sprintf("observed_at=%s", record.ObservedAt),
		fmt.Sprintf("workspace_control_plane=%s", record.WorkspaceControlPlane),
	}
}

func SessionRuntimeMetadataLines(record SessionRecord) []string {
	return []string{
		fmt.Sprintf("session_id=%s", record.SessionID),
		fmt.Sprintf("profile=%s", record.Profile),
		fmt.Sprintf("workspace=%s", record.Workspace),
		fmt.Sprintf("workspace_origin=%s", record.WorkspaceOrigin),
		fmt.Sprintf("workspace_root=%s", record.WorkspaceRoot),
		fmt.Sprintf("worktree_path=%s", record.WorktreePath),
		fmt.Sprintf("container_name=%s", record.ContainerName),
		fmt.Sprintf("status=%s", record.Status),
		fmt.Sprintf("mode=%s", record.Mode),
		fmt.Sprintf("monitor_pid=%s", record.MonitorPID),
		fmt.Sprintf("live_status=%s", record.LiveStatus),
		fmt.Sprintf("current_assurance=%s", record.CurrentAssurance),
		fmt.Sprintf("session_audit_dir=%s", record.SessionAuditDir),
		fmt.Sprintf("audit_log_path=%s", record.AuditLogPath),
		fmt.Sprintf("debug_log_path=%s", record.DebugLogPath),
		fmt.Sprintf("file_trace_log_path=%s", record.FileTraceLogPath),
		fmt.Sprintf("transcript_log_path=%s", record.TranscriptLogPath),
		fmt.Sprintf("observed_at=%s", record.ObservedAt),
	}
}

func SessionControlMode(record SessionRecord) string {
	if strings.TrimSpace(record.MonitorPID) != "" || strings.TrimSpace(record.SessionAuditDir) != "" {
		return "detached"
	}
	currentStatus := strings.TrimSpace(record.LiveStatus)
	if currentStatus == "" {
		currentStatus = strings.TrimSpace(record.Status)
	}
	switch currentStatus {
	case "starting", "running", "stopping":
		if strings.TrimSpace(record.Profile) != "" && strings.TrimSpace(record.ContainerName) != "" {
			return "detached"
		}
	}
	return "attached"
}

func SessionDisplayWorkspace(record SessionRecord) string {
	if strings.TrimSpace(record.WorkspaceOrigin) != "" {
		return record.WorkspaceOrigin
	}
	return record.Workspace
}

func SessionMatchesWorkspace(record SessionRecord, workspace string) bool {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return true
	}
	if strings.TrimSpace(record.Workspace) == workspace {
		return true
	}
	return strings.TrimSpace(record.WorkspaceOrigin) == workspace
}

func isTerminalSessionStatus(status string) bool {
	switch status {
	case "exited", "failed", "aborted":
		return true
	default:
		return false
	}
}

func WriteSessionRecord(path string, updates map[string]string) error {
	record := SessionRecord{Version: 1}
	existingRecord := SessionRecord{}
	hadExisting := false
	if existing, err := ReadSessionRecord(path); err == nil {
		record = existing
		existingRecord = existing
		hadExisting = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	for key, value := range updates {
		switch key {
		case "session_id":
			record.SessionID = value
		case "profile":
			record.Profile = value
		case "agent":
			record.Agent = value
		case "mode":
			record.Mode = value
		case "status":
			record.Status = value
		case "ui":
			record.UI = value
		case "execution_path":
			record.ExecutionPath = value
		case "workspace":
			record.Workspace = value
		case "workspace_origin":
			record.WorkspaceOrigin = value
		case "workspace_root":
			record.WorkspaceRoot = value
		case "worktree_path":
			record.WorktreePath = value
		case "git_branch":
			record.GitBranch = value
		case "git_head":
			record.GitHead = value
		case "git_base":
			record.GitBase = value
		case "container_name":
			record.ContainerName = value
		case "monitor_pid":
			record.MonitorPID = value
		case "live_status":
			record.LiveStatus = value
		case "session_audit_dir":
			record.SessionAuditDir = value
		case "audit_log_path":
			record.AuditLogPath = value
		case "debug_log_path":
			record.DebugLogPath = value
		case "file_trace_log_path":
			record.FileTraceLogPath = value
		case "transcript_log_path":
			record.TranscriptLogPath = value
		case "started_at":
			record.StartedAt = value
		case "observed_at":
			record.ObservedAt = value
		case "finished_at":
			record.FinishedAt = value
		case "exit_status":
			record.ExitStatus = value
		case "initial_assurance":
			record.InitialAssurance = value
		case "current_assurance":
			record.CurrentAssurance = value
		case "final_assurance":
			record.FinalAssurance = value
		case "workspace_control_plane":
			record.WorkspaceControlPlane = value
		default:
			return fmt.Errorf("unsupported session record field %q", key)
		}
	}

	if hadExisting && isTerminalSessionStatus(existingRecord.Status) && !isTerminalSessionStatus(record.Status) {
		return fmt.Errorf("%s: refusing to overwrite terminal session status %q with %q", path, existingRecord.Status, record.Status)
	}

	if err := validateSessionRecord(record, path); err != nil {
		return err
	}

	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return writeFileAtomically(path, data, 0o600)
}

func ReadSessionRecord(path string) (SessionRecord, error) {
	var record SessionRecord

	data, err := os.ReadFile(path)
	if err != nil {
		return record, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil {
		return record, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return record, fmt.Errorf("%s: unexpected trailing content", path)
	}
	if err := validateSessionRecord(record, path); err != nil {
		return SessionRecord{}, err
	}
	return record, nil
}

func ListSessionRecords(colimaRoot string, opts SessionListOptions) ([]SessionRecord, error) {
	root := filepath.Clean(colimaRoot)
	info, err := os.Stat(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []SessionRecord{}, nil
		}
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", root)
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}

	records := make([]SessionRecord, 0)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if opts.Profile != "" && entry.Name() != opts.Profile {
			continue
		}

		profileDir := filepath.Join(root, entry.Name())
		if isSymlink(profileDir) {
			continue
		}
		sessionDir := filepath.Join(profileDir, "sessions")
		if isSymlink(sessionDir) {
			continue
		}
		sessionEntries, err := os.ReadDir(sessionDir)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, err
		}
		for _, sessionEntry := range sessionEntries {
			if sessionEntry.IsDir() {
				continue
			}
			if filepath.Ext(sessionEntry.Name()) != ".json" {
				continue
			}
			sessionPath := filepath.Join(sessionDir, sessionEntry.Name())
			if isSymlink(sessionPath) {
				continue
			}
			record, err := ReadSessionRecord(sessionPath)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", sessionPath, err)
			}
			if !SessionMatchesWorkspace(record, opts.Workspace) {
				continue
			}
			records = append(records, record)
		}
	}

	sort.SliceStable(records, func(i, j int) bool {
		if records[i].StartedAt == records[j].StartedAt {
			return records[i].SessionID > records[j].SessionID
		}
		return records[i].StartedAt > records[j].StartedAt
	})
	if records == nil {
		return []SessionRecord{}, nil
	}
	return records, nil
}

func FindSessionRecord(colimaRoot, sessionID string) (SessionRecord, error) {
	records, err := ListSessionRecords(colimaRoot, SessionListOptions{})
	if err != nil {
		return SessionRecord{}, err
	}

	var match *SessionRecord
	for i := range records {
		if records[i].SessionID != sessionID {
			continue
		}
		if match != nil {
			return SessionRecord{}, fmt.Errorf("multiple session records matched %q", sessionID)
		}
		match = &records[i]
	}
	if match == nil {
		return SessionRecord{}, os.ErrNotExist
	}
	return *match, nil
}

func ExportSessionRecord(colimaRoot, sessionID string) (SessionExport, error) {
	record, err := FindSessionRecord(colimaRoot, sessionID)
	if err != nil {
		return SessionExport{}, err
	}

	records, err := SessionAuditRecords(record)
	if err != nil {
		return SessionExport{}, err
	}
	return SessionExport{Session: record, AuditRecords: records}, nil
}

func SessionTimelineRecords(colimaRoot, sessionID string) ([]string, error) {
	record, err := FindSessionRecord(colimaRoot, sessionID)
	if err != nil {
		return nil, err
	}
	return SessionAuditRecords(record)
}

func SessionAuditRecords(record SessionRecord) ([]string, error) {
	if record.AuditLogPath == "" {
		return []string{}, nil
	}

	data, err := os.ReadFile(record.AuditLogPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []string{}, nil
		}
		return nil, err
	}

	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	records := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if auditLineHasSessionID(line, record.SessionID) {
			records = append(records, line)
		}
	}
	return records, nil
}

func auditLineHasSessionID(line, sessionID string) bool {
	for _, field := range strings.Fields(line) {
		key, value, ok := strings.Cut(field, "=")
		if ok && key == "session_id" && value == sessionID {
			return true
		}
	}
	return false
}

func writeFileAtomically(path string, data []byte, mode os.FileMode) error {
	tempFile, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tempPath := tempFile.Name()
	removeTemp := true
	defer func() {
		if removeTemp {
			_ = os.Remove(tempPath)
		}
	}()

	if err := tempFile.Chmod(mode); err != nil {
		_ = tempFile.Close()
		return err
	}
	if _, err := tempFile.Write(data); err != nil {
		_ = tempFile.Close()
		return err
	}
	if err := tempFile.Sync(); err != nil {
		_ = tempFile.Close()
		return err
	}
	if err := tempFile.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		return err
	}
	removeTemp = false
	return os.Chmod(path, mode)
}

func validateSessionRecord(record SessionRecord, source string) error {
	if record.Version != 1 {
		return fmt.Errorf("%s: unsupported session record version %d", source, record.Version)
	}
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "session_id", value: record.SessionID},
		{name: "profile", value: record.Profile},
		{name: "agent", value: record.Agent},
		{name: "mode", value: record.Mode},
		{name: "status", value: record.Status},
		{name: "ui", value: record.UI},
		{name: "execution_path", value: record.ExecutionPath},
		{name: "workspace", value: record.Workspace},
		{name: "workspace_origin", value: record.WorkspaceOrigin},
		{name: "workspace_root", value: record.WorkspaceRoot},
		{name: "worktree_path", value: record.WorktreePath},
		{name: "git_branch", value: record.GitBranch},
		{name: "git_head", value: record.GitHead},
		{name: "git_base", value: record.GitBase},
		{name: "container_name", value: record.ContainerName},
		{name: "monitor_pid", value: record.MonitorPID},
		{name: "live_status", value: record.LiveStatus},
		{name: "session_audit_dir", value: record.SessionAuditDir},
		{name: "audit_log_path", value: record.AuditLogPath},
		{name: "debug_log_path", value: record.DebugLogPath},
		{name: "file_trace_log_path", value: record.FileTraceLogPath},
		{name: "transcript_log_path", value: record.TranscriptLogPath},
		{name: "started_at", value: record.StartedAt},
		{name: "observed_at", value: record.ObservedAt},
		{name: "finished_at", value: record.FinishedAt},
		{name: "exit_status", value: record.ExitStatus},
		{name: "initial_assurance", value: record.InitialAssurance},
		{name: "current_assurance", value: record.CurrentAssurance},
		{name: "final_assurance", value: record.FinalAssurance},
		{name: "workspace_control_plane", value: record.WorkspaceControlPlane},
	} {
		if strings.ContainsAny(field.value, "\r\n") {
			return fmt.Errorf("%s: session record field %s may not contain newlines", source, field.name)
		}
	}
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "session_id", value: record.SessionID},
		{name: "profile", value: record.Profile},
		{name: "agent", value: record.Agent},
		{name: "mode", value: record.Mode},
		{name: "status", value: record.Status},
		{name: "workspace", value: record.Workspace},
		{name: "started_at", value: record.StartedAt},
	} {
		if strings.TrimSpace(field.value) == "" {
			return fmt.Errorf("%s: missing required session record field %s", source, field.name)
		}
	}

	switch record.Status {
	case "starting", "running", "stopping", "exited", "failed", "aborted":
	default:
		return fmt.Errorf("%s: unsupported session status %q", source, record.Status)
	}

	if strings.TrimSpace(record.MonitorPID) != "" {
		monitorPID, err := strconv.Atoi(record.MonitorPID)
		if err != nil || monitorPID <= 0 {
			return fmt.Errorf("%s: invalid monitor_pid %q", source, record.MonitorPID)
		}
		if strings.TrimSpace(record.SessionAuditDir) == "" {
			return fmt.Errorf("%s: monitor_pid requires session_audit_dir", source)
		}
	}

	if record.Status == "starting" || record.Status == "running" || record.Status == "stopping" {
		if record.FinishedAt != "" || record.ExitStatus != "" || record.FinalAssurance != "" {
			return fmt.Errorf("%s: %s sessions may not set finished_at, exit_status, or final_assurance", source, record.Status)
		}
		return nil
	}

	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "finished_at", value: record.FinishedAt},
		{name: "exit_status", value: record.ExitStatus},
		{name: "final_assurance", value: record.FinalAssurance},
	} {
		if strings.TrimSpace(field.value) == "" {
			return fmt.Errorf("%s: completed sessions must set %s", source, field.name)
		}
	}
	return nil
}
