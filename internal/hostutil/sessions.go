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
	ContainerName         string `json:"container_name,omitempty"`
	SessionAuditDir       string `json:"session_audit_dir,omitempty"`
	AuditLogPath          string `json:"audit_log_path,omitempty"`
	DebugLogPath          string `json:"debug_log_path,omitempty"`
	FileTraceLogPath      string `json:"file_trace_log_path,omitempty"`
	TranscriptLogPath     string `json:"transcript_log_path,omitempty"`
	StartedAt             string `json:"started_at"`
	FinishedAt            string `json:"finished_at,omitempty"`
	ExitStatus            string `json:"exit_status,omitempty"`
	InitialAssurance      string `json:"initial_assurance,omitempty"`
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

func WriteSessionRecord(path string, updates map[string]string) error {
	record := SessionRecord{Version: 1}
	if existing, err := ReadSessionRecord(path); err == nil {
		record = existing
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
		case "container_name":
			record.ContainerName = value
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
		case "finished_at":
			record.FinishedAt = value
		case "exit_status":
			record.ExitStatus = value
		case "initial_assurance":
			record.InitialAssurance = value
		case "final_assurance":
			record.FinalAssurance = value
		case "workspace_control_plane":
			record.WorkspaceControlPlane = value
		default:
			return fmt.Errorf("unsupported session record field %q", key)
		}
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
	return os.WriteFile(path, data, 0o600)
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
			if opts.Workspace != "" && record.Workspace != opts.Workspace {
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

	export := SessionExport{Session: record}
	if record.AuditLogPath == "" {
		return export, nil
	}

	data, err := os.ReadFile(record.AuditLogPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return export, nil
		}
		return SessionExport{}, err
	}

	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if auditLineHasSessionID(line, record.SessionID) {
			export.AuditRecords = append(export.AuditRecords, line)
		}
	}
	return export, nil
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
		{name: "workspace", value: record.Workspace},
		{name: "started_at", value: record.StartedAt},
	} {
		if strings.TrimSpace(field.value) == "" {
			return fmt.Errorf("%s: missing required session record field %s", source, field.name)
		}
	}

	switch record.Status {
	case "running", "exited", "failed", "aborted":
	default:
		return fmt.Errorf("%s: unsupported session status %q", source, record.Status)
	}

	if record.Status == "running" {
		if record.FinishedAt != "" || record.ExitStatus != "" || record.FinalAssurance != "" {
			return fmt.Errorf("%s: running sessions may not set finished_at, exit_status, or final_assurance", source)
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
