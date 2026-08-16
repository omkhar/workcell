// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
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

func TestManagedColimaSourceContractRejectsMutants(t *testing.T) {
	workcell := mustReadTestFile(t, filepath.Join("..", "..", "scripts", "workcell"))
	invariants := mustReadTestFile(t, filepath.Join("..", "..", "scripts", "verify-invariants.sh"))
	hostutil := mustReadTestFile(t, "main.go")
	if err := validateManagedColimaSource(workcell, invariants, hostutil); err != nil {
		t.Fatal(err)
	}
	mutants := map[string][3]string{
		"pre-captured list":   {strings.Replace(workcell, "run_host_colima list --json 2>/dev/null |", "list_output=\"$(run_host_colima list --json 2>/dev/null)\"", 1), invariants, hostutil},
		"missing PIPESTATUS":  {strings.Replace(workcell, "run_host_colima list --json 2>/dev/null |\n      run_go_hostutil_preserve_exit helper colima-status \"${profile}\"\n    pipe_status=(\"${PIPESTATUS[@]}\")", "run_host_colima list --json 2>/dev/null |\n      run_go_hostutil_preserve_exit helper colima-status \"${profile}\"\n    pipe_status=(0 0)", 1), invariants, hostutil},
		"shell-managed start": {strings.Replace(workcell, "run_host_colima_with_timeout \"${WORKCELL_COLIMA_START_TIMEOUT_SECONDS:-180}\" start", "run_host_colima start", 1), invariants, hostutil},
		"stale extraction":    {workcell, invariants + "\nextract_top_level_bash_function \"${ROOT_DIR}/scripts/workcell\" terminate_process_tree_by_pid\n", hostutil},
		"unbounded handler":   {workcell, invariants, strings.Replace(hostutil, "readHelperInput(\"Colima status output\", os.Stdin)", "io.ReadAll(os.Stdin)", 1)},
	}
	for name, mutant := range mutants {
		t.Run(name, func(t *testing.T) {
			if err := validateManagedColimaSource(mutant[0], mutant[1], mutant[2]); err == nil {
				t.Fatal("mutant passed source contract")
			}
		})
	}
}

func validateManagedColimaSource(workcell, invariants, hostutil string) error {
	for _, required := range []string{
		"run_host_colima list --json 2>/dev/null |\n      run_go_hostutil_preserve_exit helper colima-status \"${profile}\"\n    pipe_status=(\"${PIPESTATUS[@]}\")",
		"run_host_colima status --profile \"${profile}\" 2>&1 |\n      run_go_hostutil_preserve_exit helper validate-colima-status \"${profile}\"\n    pipe_status=(\"${PIPESTATUS[@]}\")",
		"run_host_colima_with_timeout \"${WORKCELL_COLIMA_START_TIMEOUT_SECONDS:-180}\" start",
	} {
		if !strings.Contains(workcell, required) {
			return fmt.Errorf("scripts/workcell lacks %q", required)
		}
	}
	if !strings.Contains(invariants, "scripts/lib/launcher/go-hostutil.sh\" run_go_hostutil_preserve_exit") {
		return errors.New("verify-invariants does not extract the exit-preserving stream helper")
	}
	for _, obsolete := range []string{"list_output=\"$(run_host_colima list --json", "status=\"$(run_host_colima status --profile", "kill_process_tree_by_pid", "terminate_process_tree_by_pid"} {
		if strings.Contains(workcell, obsolete) || strings.Contains(invariants, "scripts/workcell\" "+obsolete) {
			return fmt.Errorf("obsolete launcher path remains: %s", obsolete)
		}
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
