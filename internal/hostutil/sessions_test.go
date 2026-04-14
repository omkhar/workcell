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
		"git_branch":              "feature/session-diff",
		"git_head":                "abcdef1234567890",
		"git_base":                "1234567890abcdef",
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
	if record.GitBranch != "feature/session-diff" || record.GitHead != "abcdef1234567890" || record.GitBase != "1234567890abcdef" {
		t.Fatalf("git metadata was not preserved: %+v", record)
	}
	info, err := os.Stat(recordPath)
	if err != nil {
		t.Fatalf("Stat(%s) error = %v", recordPath, err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("session record mode = %o, want 0600", info.Mode().Perm())
	}
	entries, err := os.ReadDir(filepath.Dir(recordPath))
	if err != nil {
		t.Fatalf("ReadDir(%s) error = %v", filepath.Dir(recordPath), err)
	}
	if len(entries) != 1 || entries[0].Name() != "session-1.json" {
		t.Fatalf("session record directory contains unexpected files: %+v", entries)
	}
}

func TestSessionDiffMetadataLines(t *testing.T) {
	t.Parallel()

	lines := SessionDiffMetadataLines(SessionRecord{
		Workspace: "/tmp/workspace",
	})

	want := []string{
		"workspace=/tmp/workspace",
		"git_branch=",
		"git_head=",
		"git_base=",
	}
	if len(lines) != len(want) {
		t.Fatalf("SessionDiffMetadataLines() len = %d, want %d", len(lines), len(want))
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Fatalf("SessionDiffMetadataLines()[%d] = %q, want %q", i, lines[i], want[i])
		}
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

func TestListSessionRecordsReturnsEmptySliceWhenColimaRootMissing(t *testing.T) {
	t.Parallel()

	records, err := ListSessionRecords(filepath.Join(t.TempDir(), "missing"), SessionListOptions{})
	if err != nil {
		t.Fatalf("ListSessionRecords() error = %v", err)
	}
	if records == nil {
		t.Fatal("ListSessionRecords() returned nil, want empty slice")
	}
	if len(records) != 0 {
		t.Fatalf("ListSessionRecords() len = %d, want 0", len(records))
	}
}

func TestListSessionRecordsSkipsSymlinkedSessionsDirectories(t *testing.T) {
	t.Parallel()

	colimaRoot := t.TempDir()
	externalSessions := filepath.Join(t.TempDir(), "external-sessions")
	writeSessionFixture(t, filepath.Join(externalSessions, "session-1.json"), SessionRecord{
		Version:    1,
		SessionID:  "session-1",
		Profile:    "wcl-one",
		Agent:      "codex",
		Mode:       "strict",
		Status:     "exited",
		Workspace:  "/tmp/workspace-a",
		StartedAt:  "2026-04-08T10:00:00Z",
		FinishedAt: "2026-04-08T10:05:00Z",
		ExitStatus: "0",
	})
	profileDir := filepath.Join(colimaRoot, "wcl-one")
	if err := os.MkdirAll(profileDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(externalSessions, filepath.Join(profileDir, "sessions")); err != nil {
		t.Fatal(err)
	}

	records, err := ListSessionRecords(colimaRoot, SessionListOptions{})
	if err != nil {
		t.Fatalf("ListSessionRecords() error = %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("ListSessionRecords() len = %d, want 0", len(records))
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
