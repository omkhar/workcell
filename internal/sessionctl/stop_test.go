// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package sessionctl

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omkhar/workcell/internal/cliexit"
)

func TestParseStopArgsRequiresIDValue(t *testing.T) {
	t.Parallel()

	_, _, _, err := parseStopArgs([]string{"--id"})
	if err == nil {
		t.Fatal("parseStopArgs accepted --id without a value")
	}
	var ec *cliexit.ExitCodeError
	if !errors.As(err, &ec) {
		t.Fatalf("parseStopArgs err = %v, want ExitCodeError", err)
	}
	if ec.Code != 2 {
		t.Fatalf("parseStopArgs ExitCodeError.Code = %d, want 2", ec.Code)
	}
	if !strings.Contains(ec.Message, "Option --id requires a value") {
		t.Fatalf("parseStopArgs message = %q, want missing-value rejection", ec.Message)
	}
}

func TestParseStopArgsRejectsEmptyIDValue(t *testing.T) {
	t.Parallel()

	_, _, _, err := parseStopArgs([]string{"--id", ""})
	if err == nil {
		t.Fatal("parseStopArgs accepted empty --id value")
	}
}

func TestParseStopArgsRejectsFlagLikeIDValue(t *testing.T) {
	t.Parallel()

	_, _, _, err := parseStopArgs([]string{"--id", "--force"})
	if err == nil {
		t.Fatal("parseStopArgs accepted flag-like --id value")
	}
	if !strings.Contains(err.Error(), "Option --id requires a value") {
		t.Fatalf("parseStopArgs error = %v, want missing-value rejection", err)
	}
}

func TestParseStopArgsRejectsUnknownFlag(t *testing.T) {
	t.Parallel()

	_, _, _, err := parseStopArgs([]string{"--bogus"})
	if err == nil {
		t.Fatal("parseStopArgs accepted unknown flag")
	}
	var ec *cliexit.ExitCodeError
	if !errors.As(err, &ec) {
		t.Fatalf("parseStopArgs err = %v, want ExitCodeError", err)
	}
	if ec.Code != 2 {
		t.Fatalf("parseStopArgs ExitCodeError.Code = %d, want 2", ec.Code)
	}
	if !strings.Contains(ec.Message, "Unsupported workcell session stop option") {
		t.Fatalf("parseStopArgs message = %q, want session-stop-specific message", ec.Message)
	}
}

func TestParseStopArgsAcceptsCanonical(t *testing.T) {
	t.Parallel()

	id, force, help, err := parseStopArgs([]string{"--id", "session-1"})
	if err != nil {
		t.Fatalf("parseStopArgs error = %v", err)
	}
	if help {
		t.Fatal("parseStopArgs help = true, want false")
	}
	if force {
		t.Fatal("parseStopArgs force = true, want false")
	}
	if id != "session-1" {
		t.Fatalf("parseStopArgs id = %q, want %q", id, "session-1")
	}
}

func TestParseStopArgsHandlesForce(t *testing.T) {
	t.Parallel()

	id, force, help, err := parseStopArgs([]string{"--id", "session-1", "--force"})
	if err != nil {
		t.Fatalf("parseStopArgs error = %v", err)
	}
	if help {
		t.Fatal("parseStopArgs help = true, want false")
	}
	if !force {
		t.Fatal("parseStopArgs force = false, want true")
	}
	if id != "session-1" {
		t.Fatalf("parseStopArgs id = %q, want %q", id, "session-1")
	}
}

func TestParseStopArgsHandlesHelp(t *testing.T) {
	t.Parallel()

	for _, flag := range []string{"-h", "--help"} {
		_, _, help, err := parseStopArgs([]string{flag})
		if err != nil {
			t.Fatalf("parseStopArgs(%s) error = %v", flag, err)
		}
		if !help {
			t.Fatalf("parseStopArgs(%s) help = false, want true", flag)
		}
	}
}

func TestStopMainRequiresID(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	err := stopMain([]string{}, &buf, io.Discard)
	if err == nil {
		t.Fatal("stopMain accepted call without --id")
	}
	var ec *cliexit.ExitCodeError
	if !errors.As(err, &ec) {
		t.Fatalf("stopMain err = %v, want ExitCodeError", err)
	}
	if ec.Code != 2 {
		t.Fatalf("stopMain ExitCodeError.Code = %d, want 2", ec.Code)
	}
	if !strings.Contains(ec.Message, "workcell session stop requires --id.") {
		t.Fatalf("stopMain message = %q, want canonical require-id message", ec.Message)
	}
}

func TestStopMainHelpPrintsUsage(t *testing.T) {
	t.Parallel()

	var stderr bytes.Buffer
	if err := stopMain([]string{"--help"}, io.Discard, &stderr); err != nil {
		t.Fatalf("stopMain(--help) error = %v", err)
	}
	if !strings.Contains(stderr.String(), "Usage: workcell session") {
		t.Fatalf("stopMain(--help) stderr = %q, want usage banner", stderr.String())
	}
}

func TestStopMainEmitsPlanFromRecord(t *testing.T) {
	root := t.TempDir()
	writeStopFixtureRecord(t, root, "wcl-detached-fixture", "fixture-1", map[string]string{
		"profile":           "wcl-detached-fixture",
		"container_name":    "workcell-session-fixture",
		"monitor_pid":       "4242",
		"session_audit_dir": "/tmp/audit-fixture",
		"status":            "running",
		"live_status":       "running",
	})

	var buf bytes.Buffer
	args := []string{"--root=" + root, "--id", "fixture-1"}
	if err := stopMain(args, &buf, io.Discard); err != nil {
		t.Fatalf("stopMain error = %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"session_id=fixture-1\n",
		"force=0\n",
		"profile=wcl-detached-fixture\n",
		"container_name=workcell-session-fixture\n",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("stopMain output missing %q\nfull output:\n%s", want, out)
		}
	}
}

func TestStopMainPropagatesForce(t *testing.T) {
	root := t.TempDir()
	writeStopFixtureRecord(t, root, "wcl-detached-fixture", "fixture-1", map[string]string{
		"profile":           "wcl-detached-fixture",
		"container_name":    "workcell-session-fixture",
		"monitor_pid":       "4242",
		"session_audit_dir": "/tmp/audit-fixture",
		"status":            "running",
		"live_status":       "running",
	})

	var buf bytes.Buffer
	args := []string{"--root=" + root, "--id", "fixture-1", "--force"}
	if err := stopMain(args, &buf, io.Discard); err != nil {
		t.Fatalf("stopMain error = %v", err)
	}
	if !strings.Contains(buf.String(), "force=1\n") {
		t.Fatalf("stopMain --force did not propagate; output:\n%s", buf.String())
	}
}

func TestStopMainRejectsAttachedSession(t *testing.T) {
	root := t.TempDir()
	writeStopFixtureRecord(t, root, "wcl-attached", "fixture-attached", map[string]string{
		"profile":        "wcl-attached",
		"container_name": "workcell-session-fixture",
		// no monitor_pid -> attached session
		"status":      "running",
		"live_status": "running",
	})

	var buf bytes.Buffer
	args := []string{"--root=" + root, "--id", "fixture-attached"}
	err := stopMain(args, &buf, io.Discard)
	if err == nil {
		t.Fatal("stopMain accepted an attached session")
	}
	if !strings.Contains(err.Error(), "only works for detached sessions") {
		t.Fatalf("stopMain error = %v, want detached-required message", err)
	}
	if !strings.Contains(err.Error(), "Use 'workcell session list'") {
		t.Fatalf("stopMain error = %v, want canonical follow-up hint", err)
	}
}

func TestStopMainRejectsMissingContainerName(t *testing.T) {
	root := t.TempDir()
	writeStopFixtureRecord(t, root, "wcl-detached-fixture", "fixture-bad", map[string]string{
		"profile":           "wcl-detached-fixture",
		"monitor_pid":       "4242",
		"session_audit_dir": "/tmp/audit-fixture",
		"status":            "running",
		"live_status":       "running",
	})

	var buf bytes.Buffer
	args := []string{"--root=" + root, "--id", "fixture-bad"}
	err := stopMain(args, &buf, io.Discard)
	if err == nil {
		t.Fatal("stopMain accepted record missing container_name")
	}
	if !strings.Contains(err.Error(), "missing a container name") {
		t.Fatalf("stopMain error = %v, want container_name complaint", err)
	}
}

// writeStopFixtureRecord lays out a minimal session JSON record under
// root/<profile>/sessions/<session_id>.json so StopMain's lookup via
// sessions.FindSessionRecordInRoots can find it without needing the
// full session-record-write path.  Mirrors writeAttachFixtureRecord
// in attach_test.go.
func writeStopFixtureRecord(t *testing.T, root, profile, sessionID string, fields map[string]string) {
	t.Helper()
	sessionsDir := filepath.Join(root, profile, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatalf("mkdir sessions dir: %v", err)
	}
	body := `{
  "version": 1,
  "session_id": "` + sessionID + `",
  "agent": "codex",
  "mode": "strict",
  "workspace": "/tmp/fixture-workspace",
  "started_at": "2026-04-08T14:00:00Z"`
	for key, value := range fields {
		body += `,
  "` + key + `": "` + value + `"`
	}
	body += "\n}\n"
	path := filepath.Join(sessionsDir, sessionID+".json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write session record: %v", err)
	}
}

// TestStopMainRejectsNewlineInID — sibling guard to
// monitor_test.go's TestMonitorMainRejectsNewlineInStateFile. CRLF
// injection into --id would let the shim see forged plan lines.
func TestStopMainRejectsNewlineInID(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"session-1\nsession_id=other", "session-1\rsession_id=other"} {
		var buf bytes.Buffer
		err := stopMain([]string{"--id", value}, &buf, io.Discard)
		if err == nil {
			t.Fatalf("stopMain accepted --id value containing control character: %q", value)
		}
		var ec *cliexit.ExitCodeError
		if !errors.As(err, &ec) || ec.Code != 2 {
			t.Fatalf("stopMain error = %v, want ExitCodeError{Code:2}", err)
		}
		if !strings.Contains(ec.Message, "must not contain newline or carriage-return") {
			t.Fatalf("stopMain message = %q, want newline-rejection diagnostic", ec.Message)
		}
		if buf.Len() != 0 {
			t.Fatalf("stopMain wrote %q on rejection, want no stdout output", buf.String())
		}
	}
}
