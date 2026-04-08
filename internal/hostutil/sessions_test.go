// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package hostutil

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteSessionRecordRoundTrip(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	recordPath := filepath.Join(root, "wcl-fixture", "sessions", "session-1.json")
	if err := WriteSessionRecord(recordPath, map[string]string{
		"session_id":              "session-1",
		"profile":                 "wcl-fixture",
		"agent":                   "codex",
		"mode":                    "strict",
		"status":                  "running",
		"ui":                      "cli",
		"execution_path":          "managed-tier1",
		"workspace":               "/tmp/workspace",
		"started_at":              "2026-04-08T12:00:00Z",
		"initial_assurance":       "managed-mutable",
		"workspace_control_plane": "masked",
	}); err != nil {
		t.Fatalf("WriteSessionRecord(running) error = %v", err)
	}

	if err := WriteSessionRecord(recordPath, map[string]string{
		"status":              "exited",
		"finished_at":         "2026-04-08T12:05:00Z",
		"exit_status":         "0",
		"final_assurance":     "managed-mutable",
		"audit_log_path":      "/tmp/audit.log",
		"debug_log_path":      "/tmp/debug.log",
		"file_trace_log_path": "/tmp/file-trace.log",
	}); err != nil {
		t.Fatalf("WriteSessionRecord(exited) error = %v", err)
	}

	record, err := ReadSessionRecord(recordPath)
	if err != nil {
		t.Fatalf("ReadSessionRecord() error = %v", err)
	}
	if record.Status != "exited" || record.ExitStatus != "0" || record.FinalAssurance != "managed-mutable" {
		t.Fatalf("unexpected record: %+v", record)
	}
}

func TestWriteSessionRecordRejectsUnknownField(t *testing.T) {
	t.Parallel()

	err := WriteSessionRecord(filepath.Join(t.TempDir(), "record.json"), map[string]string{
		"session_id": "session-1",
		"profile":    "wcl-fixture",
		"agent":      "codex",
		"mode":       "strict",
		"status":     "running",
		"workspace":  "/tmp/workspace",
		"started_at": "2026-04-08T12:00:00Z",
		"unknown":    "value",
	})
	if err == nil {
		t.Fatal("WriteSessionRecord() unexpectedly succeeded")
	}
}

func TestListSessionRecordsSortsNewestFirstAndFilters(t *testing.T) {
	t.Parallel()

	colimaRoot := t.TempDir()
	writeSessionFixture(t, filepath.Join(colimaRoot, "wcl-one", "sessions", "session-1.json"), SessionRecord{
		Version:        1,
		SessionID:      "session-1",
		Profile:        "wcl-one",
		Agent:          "codex",
		Mode:           "strict",
		Status:         "exited",
		Workspace:      "/tmp/workspace-a",
		StartedAt:      "2026-04-08T10:00:00Z",
		FinishedAt:     "2026-04-08T10:05:00Z",
		ExitStatus:     "0",
		FinalAssurance: "managed-mutable",
	})
	writeSessionFixture(t, filepath.Join(colimaRoot, "wcl-two", "sessions", "session-2.json"), SessionRecord{
		Version:        1,
		SessionID:      "session-2",
		Profile:        "wcl-two",
		Agent:          "claude",
		Mode:           "development",
		Status:         "failed",
		Workspace:      "/tmp/workspace-b",
		StartedAt:      "2026-04-08T11:00:00Z",
		FinishedAt:     "2026-04-08T11:03:00Z",
		ExitStatus:     "17",
		FinalAssurance: "managed-mutable",
	})

	records, err := ListSessionRecords(colimaRoot, SessionListOptions{})
	if err != nil {
		t.Fatalf("ListSessionRecords() error = %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("ListSessionRecords() len = %d, want 2", len(records))
	}
	if records[0].SessionID != "session-2" || records[1].SessionID != "session-1" {
		t.Fatalf("unexpected sort order: %+v", records)
	}

	filtered, err := ListSessionRecords(colimaRoot, SessionListOptions{Workspace: "/tmp/workspace-a"})
	if err != nil {
		t.Fatalf("ListSessionRecords(filter) error = %v", err)
	}
	if len(filtered) != 1 || filtered[0].SessionID != "session-1" {
		t.Fatalf("unexpected filter result: %+v", filtered)
	}
}

func TestExportSessionRecordIncludesMatchingAuditLines(t *testing.T) {
	t.Parallel()

	colimaRoot := t.TempDir()
	auditLogPath := filepath.Join(colimaRoot, "wcl-one", "workcell.audit.log")
	if err := os.MkdirAll(filepath.Dir(auditLogPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(auditLogPath, []byte(
		"timestamp=2026-04-08T10:00:00Z event=launch session_id=session-1 record_digest=aaa\n"+
			"timestamp=2026-04-08T10:01:00Z event=launch session_id=session-2 record_digest=bbb\n"+
			"timestamp=2026-04-08T10:02:00Z event=exit session_id=session-1 record_digest=ccc\n",
	), 0o600); err != nil {
		t.Fatal(err)
	}

	writeSessionFixture(t, filepath.Join(colimaRoot, "wcl-one", "sessions", "session-1.json"), SessionRecord{
		Version:        1,
		SessionID:      "session-1",
		Profile:        "wcl-one",
		Agent:          "codex",
		Mode:           "strict",
		Status:         "exited",
		Workspace:      "/tmp/workspace-a",
		StartedAt:      "2026-04-08T10:00:00Z",
		FinishedAt:     "2026-04-08T10:05:00Z",
		ExitStatus:     "0",
		FinalAssurance: "managed-mutable",
		AuditLogPath:   auditLogPath,
	})

	exported, err := ExportSessionRecord(colimaRoot, "session-1")
	if err != nil {
		t.Fatalf("ExportSessionRecord() error = %v", err)
	}
	if len(exported.AuditRecords) != 2 {
		t.Fatalf("ExportSessionRecord() audit record len = %d, want 2", len(exported.AuditRecords))
	}
}

func TestReadSessionRecordRejectsUnknownJSONField(t *testing.T) {
	t.Parallel()

	recordPath := filepath.Join(t.TempDir(), "session.json")
	if err := os.WriteFile(recordPath, []byte(`{
  "version": 1,
  "session_id": "session-1",
  "profile": "wcl-one",
  "agent": "codex",
  "mode": "strict",
  "status": "running",
  "workspace": "/tmp/workspace",
  "started_at": "2026-04-08T10:00:00Z",
  "unexpected": "value"
}
`), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := ReadSessionRecord(recordPath)
	if err == nil {
		t.Fatal("ReadSessionRecord() unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("ReadSessionRecord() error = %v, want unknown field failure", err)
	}
}

func TestWriteSessionRecordRejectsCompletedRecordMissingExitMetadata(t *testing.T) {
	t.Parallel()

	err := WriteSessionRecord(filepath.Join(t.TempDir(), "record.json"), map[string]string{
		"session_id":  "session-1",
		"profile":     "wcl-fixture",
		"agent":       "codex",
		"mode":        "strict",
		"status":      "failed",
		"workspace":   "/tmp/workspace",
		"started_at":  "2026-04-08T12:00:00Z",
		"finished_at": "2026-04-08T12:05:00Z",
	})
	if err == nil {
		t.Fatal("WriteSessionRecord() unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "completed sessions must set exit_status") {
		t.Fatalf("WriteSessionRecord() error = %v, want exit_status failure", err)
	}
}

func writeSessionFixture(tb testing.TB, path string, record SessionRecord) {
	tb.Helper()

	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		tb.Fatal(err)
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		tb.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		tb.Fatal(err)
	}
}
