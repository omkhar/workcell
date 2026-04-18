// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omkhar/workcell/internal/hostutil"
)

func TestRunLauncherSessionTimeline(t *testing.T) {
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

	sessionPath := filepath.Join(colimaRoot, "wcl-one", "sessions", "session-1.json")
	if err := hostutil.WriteSessionRecord(sessionPath, map[string]string{
		"session_id":      "session-1",
		"profile":         "wcl-one",
		"agent":           "codex",
		"mode":            "strict",
		"status":          "exited",
		"workspace":       "/tmp/workspace-a",
		"started_at":      "2026-04-08T10:00:00Z",
		"finished_at":     "2026-04-08T10:05:00Z",
		"exit_status":     "0",
		"audit_log_path":  auditLogPath,
		"final_assurance": "managed-mutable",
	}); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	defer func() {
		os.Stdout = oldStdout
	}()

	done := make(chan struct{})
	go func() {
		_, _ = stdout.ReadFrom(r)
		close(done)
	}()

	runErr := run([]string{"launcher", "session-timeline", colimaRoot, "session-1"})
	_ = w.Close()
	<-done
	_ = r.Close()

	if runErr != nil {
		t.Fatalf("run() error = %v", runErr)
	}
	got := strings.TrimSpace(stdout.String())
	want := strings.Join([]string{
		"timestamp=2026-04-08T10:00:00Z event=launch session_id=session-1 record_digest=aaa",
		"timestamp=2026-04-08T10:02:00Z event=exit session_id=session-1 record_digest=ccc",
	}, "\n")
	if got != want {
		t.Fatalf("session timeline output = %q, want %q", got, want)
	}
}

func TestRunLauncherSessionRuntimeMetadata(t *testing.T) {
	colimaRoot := t.TempDir()
	sessionPath := filepath.Join(colimaRoot, "wcl-one", "sessions", "session-1.json")
	if err := hostutil.WriteSessionRecord(sessionPath, map[string]string{
		"session_id":          "session-1",
		"profile":             "wcl-one",
		"agent":               "codex",
		"mode":                "strict",
		"status":              "running",
		"workspace":           "/tmp/workspace-a",
		"workspace_origin":    "/tmp/source-workspace",
		"worktree_path":       "/tmp/workspace-a/.worktrees/session-1",
		"container_name":      "workcell-session-1",
		"monitor_pid":         "4242",
		"started_at":          "2026-04-08T10:00:00Z",
		"live_status":         "running",
		"current_assurance":   "managed-mutable",
		"session_audit_dir":   "/tmp/session-audit.1234",
		"audit_log_path":      "/tmp/audit.log",
		"debug_log_path":      "/tmp/debug.log",
		"file_trace_log_path": "/tmp/file-trace.log",
		"transcript_log_path": "/tmp/transcript.log",
		"observed_at":         "2026-04-08T10:01:00Z",
	}); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	defer func() {
		os.Stdout = oldStdout
	}()

	done := make(chan struct{})
	go func() {
		_, _ = stdout.ReadFrom(r)
		close(done)
	}()

	runErr := run([]string{"launcher", "session-runtime-metadata", colimaRoot, "session-1"})
	_ = w.Close()
	<-done
	_ = r.Close()

	if runErr != nil {
		t.Fatalf("run() error = %v", runErr)
	}
	got := strings.TrimSpace(stdout.String())
	if !strings.Contains(got, "workspace_origin=/tmp/source-workspace") {
		t.Fatalf("runtime metadata output = %q, want workspace_origin line", got)
	}
	if !strings.Contains(got, "monitor_pid=4242") {
		t.Fatalf("runtime metadata output = %q, want monitor_pid line", got)
	}
	if !strings.Contains(got, "session_audit_dir=/tmp/session-audit.1234") {
		t.Fatalf("runtime metadata output = %q, want session_audit_dir line", got)
	}
	if !strings.Contains(got, "transcript_log_path=/tmp/transcript.log") {
		t.Fatalf("runtime metadata output = %q, want transcript_log_path line", got)
	}
}

func TestRunLauncherSessionListShowsLiveStatusAndControl(t *testing.T) {
	colimaRoot := t.TempDir()
	if err := hostutil.WriteSessionRecord(filepath.Join(colimaRoot, "wcl-one", "sessions", "session-1.json"), map[string]string{
		"session_id":        "session-1",
		"profile":           "wcl-one",
		"agent":             "codex",
		"mode":              "strict",
		"status":            "running",
		"live_status":       "running",
		"container_name":    "workcell-session-1",
		"session_audit_dir": "/tmp/session-audit.attached",
		"workspace":         "/tmp/workspace-a",
		"workspace_origin":  "/tmp/workspace-a",
		"worktree_path":     "/tmp/workspace-a",
		"started_at":        "2026-04-08T10:00:00Z",
		"current_assurance": "managed-mutable",
	}); err != nil {
		t.Fatal(err)
	}
	if err := hostutil.WriteSessionRecord(filepath.Join(colimaRoot, "wcl-two", "sessions", "session-2.json"), map[string]string{
		"session_id":        "session-2",
		"profile":           "wcl-two",
		"agent":             "claude",
		"mode":              "development",
		"status":            "running",
		"live_status":       "running",
		"workspace":         "/tmp/workspace-b",
		"workspace_origin":  "/tmp/source-workspace",
		"container_name":    "workcell-session-2",
		"started_at":        "2026-04-08T11:00:00Z",
		"current_assurance": "lower-assurance-package-mutation",
		"initial_assurance": "managed-mutable",
	}); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	defer func() {
		os.Stdout = oldStdout
	}()

	done := make(chan struct{})
	go func() {
		_, _ = stdout.ReadFrom(r)
		close(done)
	}()

	runErr := run([]string{"launcher", "session-list", colimaRoot})
	_ = w.Close()
	<-done
	_ = r.Close()

	if runErr != nil {
		t.Fatalf("run() error = %v", runErr)
	}
	got := strings.TrimSpace(stdout.String())
	if !strings.Contains(got, "session-2\trunning\trunning\tdetached\tclaude\tdevelopment\twcl-two\t2026-04-08T11:00:00Z\tlower-assurance-package-mutation\t/tmp/source-workspace") {
		t.Fatalf("session list output = %q, want detached record with live status and control", got)
	}
	if !strings.Contains(got, "session-1\trunning\trunning\tattached\tcodex\tstrict\twcl-one\t2026-04-08T10:00:00Z\tmanaged-mutable\t/tmp/workspace-a") {
		t.Fatalf("session list output = %q, want live attached record with attached control", got)
	}
}

func TestResolveHostOutputDirectoryCandidateRejectsRegularFile(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "session-audit")
	if err := os.WriteFile(filePath, []byte("not-a-directory"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := hostutil.ResolveHostOutputDirectoryCandidate(filePath)
	if err == nil {
		t.Fatal("ResolveHostOutputDirectoryCandidate unexpectedly accepted a regular file")
	}
	if !strings.Contains(err.Error(), "directory or a new directory path") {
		t.Fatalf("ResolveHostOutputDirectoryCandidate error = %q, want directory-specific guidance", err)
	}
}

func TestLauncherUsageListsDirectoryCandidateResolver(t *testing.T) {
	if err := launcherUsage(); err == nil {
		t.Fatal("launcherUsage unexpectedly returned nil")
	} else if !strings.Contains(err.Error(), "resolve-host-output-directory-candidate") {
		t.Fatalf("launcherUsage error = %q, want resolve-host-output-directory-candidate", err)
	}
}
