// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omkhar/workcell/internal/cliexit"
	"github.com/omkhar/workcell/internal/host/hoststate"
	"github.com/omkhar/workcell/internal/host/sessions"
)

// TestUsageReturnsExitCode2 pins the D8 exit-code contract: top-level usage
// errors (missing/unknown command, and the per-group path/release/helper
// usage) carry ExitCodeError{Code:2} so main() exits 2, matching the other
// workcell Go CLIs. Previously these returned plain errors and exited 1.
func TestUsageReturnsExitCode2(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"definitely-not-a-subcommand"},
		{"path"},
		{"release"},
		{"helper"},
	} {
		err := run(args)
		ec, ok := cliexit.IsExitCodeError(err)
		if !ok || ec.Code != 2 {
			t.Fatalf("run(%v) = %v (ok=%v), want ExitCodeError{Code:2}", args, err, ok)
		}
		if !strings.Contains(ec.Error(), "usage:") {
			t.Fatalf("run(%v) message = %q, want usage text", args, ec.Error())
		}
	}
}

func TestReapColimaProfileProcessesHelperAcceptsAbsentProfile(t *testing.T) {
	profile := "workcell-hostutil-test-no-process"
	if err := run([]string{"helper", "reap-colima-profile-processes", profile}); err != nil {
		t.Fatalf("reap absent profile: %v", err)
	}
}

func TestRunHelperSessionTimeline(t *testing.T) {
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
	if err := sessions.WriteSessionRecord(sessionPath, map[string]string{
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

	runErr := run([]string{"helper", "session-timeline", colimaRoot, "session-1"})
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

func TestRunHelperSessionRuntimeMetadata(t *testing.T) {
	colimaRoot := t.TempDir()
	sessionPath := filepath.Join(colimaRoot, "wcl-one", "sessions", "session-1.json")
	if err := sessions.WriteSessionRecord(sessionPath, map[string]string{
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

	runErr := run([]string{"helper", "session-runtime-metadata", colimaRoot, "session-1"})
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
	if !strings.Contains(got, "target_kind=local_vm") || !strings.Contains(got, "target_provider=colima") {
		t.Fatalf("runtime metadata output = %q, want target metadata", got)
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

func TestRunHelperSessionRuntimeMetadataSupportsMultipleRoots(t *testing.T) {
	stateRoot := t.TempDir()
	legacyRoot := t.TempDir()
	sessionPath := filepath.Join(stateRoot, "targets", "local_vm", "colima", "wcl-one", "sessions", "session-1.json")
	if err := sessions.WriteSessionRecord(sessionPath, map[string]string{
		"session_id":        "session-1",
		"profile":           "wcl-one",
		"agent":             "codex",
		"mode":              "strict",
		"status":            "running",
		"workspace":         "/tmp/workspace-a",
		"started_at":        "2026-04-08T10:00:00Z",
		"current_assurance": "managed-mutable",
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

	runErr := run([]string{
		"helper",
		"session-runtime-metadata",
		"--root=" + stateRoot,
		"--root=" + legacyRoot,
		"session-1",
	})
	_ = w.Close()
	<-done
	_ = r.Close()

	if runErr != nil {
		t.Fatalf("run() error = %v", runErr)
	}
	got := strings.TrimSpace(stdout.String())
	if !strings.Contains(got, "profile=wcl-one") {
		t.Fatalf("runtime metadata output = %q, want profile line", got)
	}
	if !strings.Contains(got, "record_path="+sessionPath) {
		t.Fatalf("runtime metadata output = %q, want record_path line", got)
	}
}

func TestRunHelperSessionListShowsLiveStatusAndControl(t *testing.T) {
	colimaRoot := t.TempDir()
	if err := sessions.WriteSessionRecord(filepath.Join(colimaRoot, "wcl-one", "sessions", "session-1.json"), map[string]string{
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
	if err := sessions.WriteSessionRecord(filepath.Join(colimaRoot, "wcl-two", "sessions", "session-2.json"), map[string]string{
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

	runErr := run([]string{"helper", "session-list", colimaRoot})
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
	if strings.Contains(got, "\tlocal_vm\t") {
		t.Fatalf("session list output = %q, want compact text output without extra target columns", got)
	}
}

func TestRunHelperSessionListVerboseShowsTargetMetadata(t *testing.T) {
	colimaRoot := t.TempDir()
	if err := sessions.WriteSessionRecord(filepath.Join(colimaRoot, "wcl-one", "sessions", "session-1.json"), map[string]string{
		"session_id":        "session-1",
		"profile":           "wcl-one",
		"agent":             "codex",
		"mode":              "strict",
		"status":            "running",
		"live_status":       "running",
		"workspace":         "/tmp/workspace-a",
		"workspace_origin":  "/tmp/source-workspace",
		"started_at":        "2026-04-08T10:00:00Z",
		"current_assurance": "managed-mutable",
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

	runErr := run([]string{"helper", "session-list", colimaRoot, "--verbose"})
	_ = w.Close()
	<-done
	_ = r.Close()

	if runErr != nil {
		t.Fatalf("run() error = %v", runErr)
	}
	got := strings.TrimSpace(stdout.String())
	want := "session-1\trunning\trunning\tattached\tcodex\tstrict\twcl-one\tlocal_vm/colima/wcl-one\tstrict\tisolated-worktree-mount\tnone\t/tmp/workspace-a\t2026-04-08T10:00:00Z\tmanaged-mutable\t/tmp/source-workspace"
	if got != want {
		t.Fatalf("session list verbose output = %q, want %q", got, want)
	}
}

func TestRunHelperSessionListParallelGroupsSiblingsByOrigin(t *testing.T) {
	colimaRoot := t.TempDir()
	// Two isolated sessions launched against the same origin repo: distinct
	// profiles, workspace clones, and containers, shared workspace_origin.
	if err := sessions.WriteSessionRecord(filepath.Join(colimaRoot, "wcl-a", "sessions", "session-a.json"), map[string]string{
		"session_id":        "session-a",
		"profile":           "wcl-a",
		"agent":             "codex",
		"mode":              "strict",
		"status":            "running",
		"live_status":       "running",
		"container_name":    "workcell-a",
		"monitor_pid":       "4242",
		"session_audit_dir": "/tmp/audit-a",
		"workspace":         "/clones/session-a/repo",
		"workspace_origin":  "/src/repo",
		"worktree_path":     "/clones/session-a/repo",
		"git_branch":        "workcell/session-a",
		"started_at":        "2026-04-08T10:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	if err := sessions.WriteSessionRecord(filepath.Join(colimaRoot, "wcl-b", "sessions", "session-b.json"), map[string]string{
		"session_id":        "session-b",
		"profile":           "wcl-b",
		"agent":             "claude",
		"mode":              "development",
		"status":            "running",
		"live_status":       "running",
		"container_name":    "workcell-b",
		"monitor_pid":       "4243",
		"session_audit_dir": "/tmp/audit-b",
		"workspace":         "/clones/session-b/repo",
		"workspace_origin":  "/src/repo",
		"worktree_path":     "/clones/session-b/repo",
		"git_branch":        "workcell/session-b",
		"started_at":        "2026-04-08T11:00:00Z",
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

	runErr := run([]string{"helper", "session-list", colimaRoot, "--parallel"})
	_ = w.Close()
	<-done
	_ = r.Close()

	if runErr != nil {
		t.Fatalf("run() error = %v", runErr)
	}
	got := stdout.String()
	if !strings.Contains(got, "origin=/src/repo\nsessions=2\n") {
		t.Fatalf("parallel output = %q, want a single origin group of two siblings", got)
	}
	if !strings.Contains(got, "session_id=session-b\nlive_status=running\ncontrol=detached\nagent=claude\nmode=development\ngit_branch=workcell/session-b\nworktree=/clones/session-b/repo\ncontainer_name=workcell-b\n") {
		t.Fatalf("parallel output = %q, want detached sibling b fields", got)
	}
	if !strings.Contains(got, "session_id=session-a\nlive_status=running\ncontrol=detached\nagent=codex\nmode=strict\ngit_branch=workcell/session-a\nworktree=/clones/session-a/repo\ncontainer_name=workcell-a\n") {
		t.Fatalf("parallel output = %q, want detached sibling a fields", got)
	}
}

func TestRunHelperSessionListParallelRejectsCombinedFormats(t *testing.T) {
	colimaRoot := t.TempDir()
	if err := run([]string{"helper", "session-list", colimaRoot, "--parallel", "--json"}); err == nil {
		t.Fatal("expected error combining --parallel and --json")
	}
}

func TestRunHelperSessionShowText(t *testing.T) {
	colimaRoot := t.TempDir()
	sessionPath := filepath.Join(colimaRoot, "wcl-one", "sessions", "session-1.json")
	if err := sessions.WriteSessionRecord(sessionPath, map[string]string{
		"session_id":              "session-1",
		"profile":                 "wcl-one",
		"agent":                   "codex",
		"mode":                    "strict",
		"status":                  "running",
		"live_status":             "running",
		"workspace":               "/tmp/workspace-a",
		"workspace_origin":        "/tmp/source-workspace",
		"worktree_path":           "/tmp/workspace-a/.worktrees/session-1",
		"git_branch":              "feature/session-observability",
		"container_name":          "workcell-session-1",
		"monitor_pid":             "4242",
		"session_audit_dir":       "/tmp/session-audit.1234",
		"started_at":              "2026-04-08T10:00:00Z",
		"current_assurance":       "managed-mutable",
		"workspace_control_plane": "masked",
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

	runErr := run([]string{"helper", "session-show", colimaRoot, "session-1", "--text"})
	_ = w.Close()
	<-done
	_ = r.Close()

	if runErr != nil {
		t.Fatalf("run() error = %v", runErr)
	}
	got := strings.TrimSpace(stdout.String())
	if !strings.Contains(got, "target_summary=local_vm/colima/wcl-one") {
		t.Fatalf("session show --text output = %q, want target summary", got)
	}
	if !strings.Contains(got, "control=detached") {
		t.Fatalf("session show --text output = %q, want control line", got)
	}
	if !strings.Contains(got, "git_branch=feature/session-observability") {
		t.Fatalf("session show --text output = %q, want git branch", got)
	}
	if !strings.Contains(got, "workspace_transport=isolated-worktree-mount") {
		t.Fatalf("session show --text output = %q, want workspace transport", got)
	}
	if !strings.Contains(got, "display_worktree=/tmp/workspace-a/.worktrees/session-1") {
		t.Fatalf("session show --text output = %q, want display worktree", got)
	}
	if !strings.Contains(got, "display_git_branch=feature/session-observability") {
		t.Fatalf("session show --text output = %q, want display git branch", got)
	}
}

func TestResolveHostOutputDirectoryCandidateRejectsRegularFile(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "session-audit")
	if err := os.WriteFile(filePath, []byte("not-a-directory"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := hoststate.ResolveHostOutputDirectoryCandidate(filePath)
	if err == nil {
		t.Fatal("ResolveHostOutputDirectoryCandidate unexpectedly accepted a regular file")
	}
	if !strings.Contains(err.Error(), "directory or a new directory path") {
		t.Fatalf("ResolveHostOutputDirectoryCandidate error = %q, want directory-specific guidance", err)
	}
}

func TestHelperUsageListsDirectoryCandidateResolver(t *testing.T) {
	if err := helperUsage(); err == nil {
		t.Fatal("helperUsage unexpectedly returned nil")
	} else if !strings.Contains(err.Error(), "resolve-host-output-directory-candidate") {
		t.Fatalf("helperUsage error = %q, want resolve-host-output-directory-candidate", err)
	}
}

func TestHelperInputLimit(t *testing.T) {
	exact := bytes.Repeat([]byte{'x'}, int(maxHelperStdinBytes))
	got, err := readHelperInput("fixture", bytes.NewReader(exact))
	if err != nil || len(got) != len(exact) {
		t.Fatalf("readHelperInput(exact) = %d bytes, %v", len(got), err)
	}
	for _, subject := range []string{"container inventory", "Colima profile inventory", "Colima status output"} {
		_, err := readHelperInput(subject, bytes.NewReader(append(exact, 'x')))
		if err == nil || !strings.Contains(err.Error(), subject+" exceeds 4194304-byte limit") {
			t.Fatalf("readHelperInput(%q) err = %v", subject, err)
		}
	}
}

func TestParseColimaInvocationCapsTimeout(t *testing.T) {
	seconds, _, _, err := parseColimaInvocationArgs([]string{"86400", "--", "start"})
	if err != nil || seconds != 86400 {
		t.Fatalf("parse exact timeout = %d, %v", seconds, err)
	}
	if _, _, _, err := parseColimaInvocationArgs([]string{"86401", "--", "start"}); err == nil {
		t.Fatal("parseColimaInvocationArgs accepted more than 24 hours")
	}
}

func TestManagedColimaSignalSourceRejectsMutants(t *testing.T) {
	workcell := mustReadTestFile(t, filepath.Join("..", "..", "scripts", "workcell"))
	invariants := mustReadTestFile(t, filepath.Join("..", "..", "scripts", "verify-invariants.sh"))
	supervisor := mustReadTestFile(t, filepath.Join("..", "..", "internal", "host", "launcher", "process_group_supervisor.go"))
	if err := validateManagedColimaSignalSource(workcell, invariants, supervisor); err != nil {
		t.Fatal(err)
	}
	mutants := map[string]string{
		"shell recursive kill": workcell + "\nterminate_process_tree_by_pid() { :; }\n",
		"direct shell start":   strings.Replace(workcell, "run_host_colima_with_timeout \"${WORKCELL_COLIMA_START_TIMEOUT_SECONDS:-180}\" start", "run_host_colima start", 1),
		"missing start gate":   strings.Replace(workcell, "    case \"${start_status}\" in\n      130 | 143)\n        return \"${start_status}\"\n        ;;\n    esac", "", 1),
		"missing delete gate":  strings.Replace(workcell, "  case \"${delete_status}\" in\n    130 | 143)\n      return \"${delete_status}\"\n      ;;\n  esac", "", 1),
		"missing retry gate":   strings.Replace(workcell, "        case \"${build_status}\" in\n          130 | 143)\n            return \"${build_status}\"\n            ;;\n        esac", "", 1),
		"missing build gate":   strings.Replace(workcell, "    case \"${build_status}\" in\n      130 | 143)\n        return \"${build_status}\"\n        ;;\n    esac", "", 1),
		"normalized refresh":   strings.Replace(workcell, "refresh_managed_profile \"Refreshing managed Colima profile ${COLIMA_PROFILE} after Colima start timed out.\" || return $?", "refresh_managed_profile \"Refreshing managed Colima profile ${COLIMA_PROFILE} after Colima start timed out.\" || return 2", 1),
	}
	for name, mutant := range mutants {
		t.Run(name, func(t *testing.T) {
			if mutant == workcell {
				t.Fatal("mutant did not change source")
			}
			if err := validateManagedColimaSignalSource(mutant, invariants, supervisor); err == nil {
				t.Fatal("mutant passed source validation")
			}
			if strings.HasPrefix(name, "missing ") || name == "normalized refresh" {
				runManagedColimaSignalFixture(t, mutant, true)
			}
		})
	}
}

func validateManagedColimaSignalSource(workcell, invariants, supervisor string) error {
	for _, required := range []string{
		"run_host_colima_with_timeout \"${WORKCELL_COLIMA_START_TIMEOUT_SECONDS:-180}\" start",
		"case \"${start_status}\" in\n      130 | 143)\n        return \"${start_status}\"",
		"case \"${delete_status}\" in\n    130 | 143)\n      return \"${delete_status}\"",
		"case \"${build_status}\" in\n          130 | 143)\n            return \"${build_status}\"",
		"case \"${build_status}\" in\n      130 | 143)\n        return \"${build_status}\"",
		"run_runtime_image_build_with_retries \"${BUILD_SOURCE_DATE_EPOCH}\" || BUILD_STATUS=$?\n  if [[ \"${BUILD_STATUS}\" -ne 0 ]]; then\n    restore_runtime_egress\n    exit \"${BUILD_STATUS}\"",
		"make(chan error, 1)", "Setpgid = true", "syscall.SIGTERM", "syscall.SIGKILL",
	} {
		if !strings.Contains(workcell+supervisor, required) {
			return fmt.Errorf("managed Colima source lacks %q", required)
		}
	}
	if strings.Contains(workcell+invariants, "kill_process_tree_by_pid") || strings.Contains(workcell+invariants, "terminate_process_tree_by_pid") {
		return fmt.Errorf("recursive shell process killer remains")
	}
	returns, exits := 0, 0
	for _, line := range strings.Split(workcell, "\n") {
		if !strings.Contains(line, "refresh_managed_profile \"") {
			continue
		}
		if strings.HasSuffix(line, " || return $?") {
			returns++
		} else if strings.HasSuffix(line, " || exit $?") {
			exits++
		} else {
			return fmt.Errorf("refresh call does not preserve status: %s", line)
		}
	}
	if returns != 5 || exits != 4 || strings.Count(workcell, "start_managed_profile || build_status=$?") != 2 {
		return fmt.Errorf("managed status sinks = %d returns, %d exits", returns, exits)
	}
	return nil
}

func TestManagedColimaSignalCallerOutcomes(t *testing.T) {
	runManagedColimaSignalFixture(t, mustReadTestFile(t, filepath.Join("..", "..", "scripts", "workcell")), false)
}

func runManagedColimaSignalFixture(t *testing.T, workcell string, wantFailure bool) {
	t.Helper()
	functions := "set -euo pipefail\n"
	for _, name := range []string{"refresh_managed_profile", "start_managed_profile", "run_runtime_image_build_with_retries"} {
		functions += extractBashFunction(t, workcell, name) + "\n"
	}
	script := functions + managedColimaSignalFixture
	cmd := exec.Command("/bin/bash", "-c", script)
	cmd.Env = append(os.Environ(),
		"WORKCELL_REFRESH_FUNCTION="+extractBashFunction(t, workcell, "refresh_managed_profile"),
		"WORKCELL_START_FUNCTION="+extractBashFunction(t, workcell, "start_managed_profile"),
	)
	output, err := cmd.CombinedOutput()
	if wantFailure == (err == nil) {
		t.Fatalf("signal fixture err = %v, output = %q", err, output)
	}
}

func extractBashFunction(t *testing.T, source, name string) string {
	t.Helper()
	start := strings.Index(source, "\n"+name+"() {\n")
	if start < 0 {
		t.Fatalf("function %s not found", name)
	}
	block := source[start+1:]
	end := strings.Index(block, "\n}\n")
	if end < 0 {
		t.Fatalf("function %s has no end", name)
	}
	return block[:end+2]
}

const managedColimaSignalFixture = `
COLIMA_PROFILE=p; REAL_HOME=/tmp; WORKSPACE=/tmp/w; PROFILE_WORKSPACE_ROOT=/tmp/w
COLIMA_CPU=1; COLIMA_MEMORY=1; COLIMA_DISK=1; TARGET_BACKEND=colima; NETWORK_POLICY=disabled; BOOTSTRAP_ENDPOINTS=""; SOURCE_DATE_EPOCH=1; IMAGE_TAG=i; ROOT_DIR=/tmp
prepare_colima_staging_cache_roots(){ :; }; maybe_reap_stale_profile_processes(){ :; }; reap_stale_profile_processes(){ REAP=$((REAP+1)); [[ "${REAP_FAIL_AT:-0}" -ne "${REAP}" ]]; }
colima_start_hit_recoverable_docker_boot_race(){ return 0; }; resolve_codex_release_url(){ :; }; resolve_copilot_release_url(){ :; }; validate_colima_profile(){ :; }
validate_runtime_security_posture(){ :; }; record_validated_profile_state(){ :; }; begin_runtime_builder(){ BUILDX_BUILDER=b; }; create_runtime_builder(){ :; }
cleanup_runtime_builder(){ CLEAN=$((CLEAN+1)); }; buildx_cmd(){ :; }; remember_profile_runtime_image_for_refresh(){ :; }; stash_profile_audit_log(){ :; }
remove_profile_state_dirs(){ REMOVE=$((REMOVE+1)); }; profile_process_pids(){ [[ "${RESIDUE:-}" == pid ]] && echo 1; }; profile_state_dirs_exist(){ [[ "${RESIDUE:-}" == state ]]; }
assert(){ [[ "$1" == "$2" ]] || { printf 'assert:%s != %s\n' "$1" "$2" >&2; exit 97; }; }
for SIGNAL in 130 143; do
  eval "${WORKCELL_START_FUNCTION}"
  RUN=0; REFRESH=0; REAP=0; PROFILE_RUNNING=0
  run_command_with_debug_log(){ RUN=$((RUN+1)); return "${SIGNAL}"; }; refresh_managed_profile(){ REFRESH=$((REFRESH+1)); :; }
  status=0; start_managed_profile || status=$?; assert "${status},${RUN},${REFRESH}" "${SIGNAL},1,0"
  RUN=0; REFRESH=0; run_command_with_debug_log(){ RUN=$((RUN+1)); return 124; }; refresh_managed_profile(){ REFRESH=$((REFRESH+1)); return "${SIGNAL}"; }
  status=0; start_managed_profile || status=$?; assert "${status},${RUN},${REFRESH}" "${SIGNAL},1,1"
  BUILD=0; CLEAN=0; SLEEP=0; REFRESH=0; run_command_with_debug_log(){ BUILD=$((BUILD+1)); return "${SIGNAL}"; }; sleep(){ SLEEP=$((SLEEP+1)); }
  status=0; run_runtime_image_build_with_retries 1 || status=$?; assert "${status},${BUILD},${CLEAN},${SLEEP},${REFRESH}" "${SIGNAL},1,1,0,0"
  BUILD=0; CLEAN=0; SLEEP=0; REFRESH=0; START=0; run_command_with_debug_log(){ BUILD=$((BUILD+1)); return 37; }
  refresh_managed_profile(){ REFRESH=$((REFRESH+1)); :; }; start_managed_profile(){ START=$((START+1)); return "${SIGNAL}"; }
  status=0; run_runtime_image_build_with_retries 1 || status=$?; assert "${status},${BUILD},${CLEAN},${SLEEP},${REFRESH},${START}" "${SIGNAL},1,1,1,1,1"
  eval "${WORKCELL_REFRESH_FUNCTION}"
  REAP=0; REMOVE=0; DELETE=0; RESIDUE=""; PROFILE_WAS_REFRESHED=0; run_host_colima_with_timeout(){ DELETE=$((DELETE+1)); return "${SIGNAL}"; }
  status=0; refresh_managed_profile x || status=$?; assert "${status},${DELETE},${REAP},${REMOVE},${PROFILE_WAS_REFRESHED}" "${SIGNAL},1,1,0,0"
done
run_host_colima_with_timeout(){ :; }
for MODE in first second pid state; do
  REAP=0; REMOVE=0; RESIDUE=""; REAP_FAIL_AT=0; [[ "${MODE}" == first ]] && REAP_FAIL_AT=1; [[ "${MODE}" == second ]] && REAP_FAIL_AT=2; [[ "${MODE}" == pid ]] && RESIDUE=pid; [[ "${MODE}" == state ]] && RESIDUE=state
  status=0; refresh_managed_profile x || status=$?; assert "${status}" 2
done
`

func TestHostInputSourceContractRejectsMutants(t *testing.T) {
	workcell := mustReadTestFile(t, filepath.Join("..", "..", "scripts", "workcell"))
	invariants := mustReadTestFile(t, filepath.Join("..", "..", "scripts", "verify-invariants.sh"))
	hostutil := mustReadTestFile(t, "main.go")
	colima := mustReadTestFile(t, filepath.Join("..", "..", "internal", "host", "launcher", "colima.go"))
	if err := validateHostInputSource(workcell, invariants, hostutil, colima); err != nil {
		t.Fatal(err)
	}

	type sources struct {
		workcell   string
		invariants string
		hostutil   string
		colima     string
	}
	mutants := map[string]sources{
		"pre-captured profile inventory": {
			workcell:   strings.Replace(workcell, "run_host_colima list --json 2>/dev/null |", "list_output=\"$(run_host_colima list --json 2>/dev/null)\"", 1),
			invariants: invariants, hostutil: hostutil, colima: colima,
		},
		"missing profile PIPESTATUS": {
			workcell:   strings.Replace(workcell, "    pipe_status=(\"${PIPESTATUS[@]}\")", "    pipe_status=(0 0)", 1),
			invariants: invariants, hostutil: hostutil, colima: colima,
		},
		"unquarantined profile stdout": {
			workcell:   strings.Replace(workcell, " helper colima-status \"${profile}\" >\"${status_output}\"", " helper colima-status \"${profile}\"", 1),
			invariants: invariants, hostutil: hostutil, colima: colima,
		},
		"unquarantined profile stderr": {
			workcell:   strings.Replace(workcell, " >\"${status_output}\" 2>\"${status_error}\"", " >\"${status_output}\"", 1),
			invariants: invariants, hostutil: hostutil, colima: colima,
		},
		"early profile status publish": {
			workcell:   strings.Replace(workcell, "    pipe_status=(\"${PIPESTATUS[@]}\")\n    set -e", "    cat -- \"${status_output}\"\n    pipe_status=(\"${PIPESTATUS[@]}\")\n    set -e", 1),
			invariants: invariants, hostutil: hostutil, colima: colima,
		},
		"late-bound cleanup": {
			workcell:   strings.Replace(workcell, "    printf -v cleanup_command 'rm -rf -- %q' \"${status_dir}\"\n    # Expand the escaped path before Bash unwinds this local scope.\n    # shellcheck disable=SC2064\n    trap \"${cleanup_command}\" EXIT", "    trap 'rm -rf -- \"${status_dir}\"' EXIT", 1),
			invariants: invariants, hostutil: hostutil, colima: colima,
		},
		"pre-captured Colima status": {
			workcell:   strings.Replace(workcell, "run_host_colima status --profile \"${profile}\" 2>&1 |", "status=\"$(run_host_colima status --profile \"${profile}\" 2>&1)\"", 1),
			invariants: invariants, hostutil: hostutil, colima: colima,
		},
		"pre-captured container inventory": {
			workcell:   strings.Replace(workcell, "run_profile_docker_command \"${profile}\" ps -a --format '{{.Names}}' 2>&1 |", "inventory=\"$(run_profile_docker_command \"${profile}\" ps -a --format '{{.Names}}' 2>&1)\"", 1),
			invariants: invariants, hostutil: hostutil, colima: colima,
		},
		"stale exit adapter": {
			workcell:   workcell,
			invariants: strings.Replace(invariants, "scripts/lib/launcher/go-hostutil.sh\" run_go_hostutil_preserve_exit", "scripts/workcell\" go_hostutil", 1),
			hostutil:   hostutil, colima: colima,
		},
		"unbounded container handler": {
			workcell: workcell, invariants: invariants,
			hostutil: strings.Replace(hostutil, "readHelperInput(\"container inventory\", os.Stdin)", "io.ReadAll(os.Stdin)", 1), colima: colima,
		},
		"unbounded inventory handler": {
			workcell: workcell, invariants: invariants,
			hostutil: strings.Replace(hostutil, "readHelperInput(\"Colima profile inventory\", os.Stdin)", "io.ReadAll(os.Stdin)", 1), colima: colima,
		},
		"unbounded status handler": {
			workcell: workcell, invariants: invariants,
			hostutil: strings.Replace(hostutil, "readHelperInput(\"Colima status output\", os.Stdin)", "io.ReadAll(os.Stdin)", 1), colima: colima,
		},
		"missing absolute-path validation": {
			workcell: workcell, invariants: invariants, hostutil: hostutil,
			colima: strings.Replace(colima, "validateColimaBinary(inv.ColimaBin)", "error(nil)", 1),
		},
	}
	for name, mutant := range mutants {
		t.Run(name, func(t *testing.T) {
			if mutant.workcell == workcell && mutant.invariants == invariants && mutant.hostutil == hostutil && mutant.colima == colima {
				t.Fatal("mutant did not change its target source")
			}
			if err := validateHostInputSource(mutant.workcell, mutant.invariants, mutant.hostutil, mutant.colima); err == nil {
				t.Fatal("mutant passed source contract")
			}
		})
	}
}

func validateHostInputSource(workcell, invariants, hostutil, colima string) error {
	requiredWorkcell := []string{
		"run_profile_docker_command \"${profile}\" ps -a --format '{{.Names}}' 2>&1 |\n    go_hostutil helper session-container-absent-for-delete",
		"run_host_colima list --json 2>/dev/null |\n      run_go_hostutil_preserve_exit helper colima-status \"${profile}\" >\"${status_output}\" 2>\"${status_error}\"\n    pipe_status=(\"${PIPESTATUS[@]}\")",
		"printf -v cleanup_command 'rm -rf -- %q' \"${status_dir}\"\n    # Expand the escaped path before Bash unwinds this local scope.\n    # shellcheck disable=SC2064\n    trap \"${cleanup_command}\" EXIT",
		"if ((pipe_status[0] != 0)); then\n      exit 1\n    fi\n    if ((pipe_status[1] == 3)); then\n      exit 3\n    fi\n    if ((pipe_status[1] != 0)); then\n      cat -- \"${status_error}\" >&2\n      exit \"${pipe_status[1]}\"\n    fi\n    cat -- \"${status_error}\" >&2\n    cat -- \"${status_output}\"",
		"run_host_colima status --profile \"${profile}\" 2>&1 |\n      run_go_hostutil_preserve_exit helper validate-colima-status \"${profile}\"\n    pipe_status=(\"${PIPESTATUS[@]}\")",
	}
	for _, required := range requiredWorkcell {
		if !strings.Contains(workcell, required) {
			return fmt.Errorf("scripts/workcell lacks %q", required)
		}
	}
	for _, obsolete := range []string{
		"list_output=\"$(run_host_colima list --json",
		"status=\"$(run_host_colima status --profile",
		"inventory=\"$(run_profile_docker_command",
	} {
		if strings.Contains(workcell, obsolete) {
			return fmt.Errorf("pre-captured host input remains: %s", obsolete)
		}
	}
	if strings.Count(invariants, "scripts/lib/launcher/go-hostutil.sh\" run_go_hostutil_preserve_exit") != 2 {
		return fmt.Errorf("verify-invariants must extract the exit-preserving stream adapter for both harnesses")
	}
	for _, call := range []string{
		"readHelperInput(\"container inventory\", os.Stdin)",
		"readHelperInput(\"Colima profile inventory\", os.Stdin)",
		"readHelperInput(\"Colima status output\", os.Stdin)",
	} {
		if !strings.Contains(hostutil, call) {
			return fmt.Errorf("hostutil lacks bounded read %q", call)
		}
	}
	if strings.Count(colima, "validateColimaBinary(inv.ColimaBin)") != 2 {
		return fmt.Errorf("Colima direct and timeout entries must validate the executable path")
	}
	return nil
}

func mustReadTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
