// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package sessionctl

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseAttachArgsRequiresIDValue(t *testing.T) {
	t.Parallel()

	_, _, _, err := parseAttachArgs([]string{"--id"})
	if err == nil {
		t.Fatal("parseAttachArgs accepted --id without a value")
	}
	if !strings.Contains(err.Error(), "Option --id requires a value.") {
		t.Fatalf("parseAttachArgs error = %v, want canonical require-value message", err)
	}
}

func TestParseAttachArgsRejectsEmptyIDValue(t *testing.T) {
	t.Parallel()

	_, _, _, err := parseAttachArgs([]string{"--id", ""})
	if err == nil {
		t.Fatal("parseAttachArgs accepted empty --id value")
	}
}

func TestParseAttachArgsRejectsUnknownFlag(t *testing.T) {
	t.Parallel()

	_, _, _, err := parseAttachArgs([]string{"--bogus"})
	if err == nil {
		t.Fatal("parseAttachArgs accepted unknown flag")
	}
	if !strings.Contains(err.Error(), "Unsupported workcell session attach option") {
		t.Fatalf("parseAttachArgs error = %v, want session-attach-specific message", err)
	}
}

func TestParseAttachArgsAcceptsCanonical(t *testing.T) {
	t.Parallel()

	id, noStdin, help, err := parseAttachArgs([]string{"--id", "session-1"})
	if err != nil {
		t.Fatalf("parseAttachArgs error = %v", err)
	}
	if help {
		t.Fatal("parseAttachArgs help = true, want false")
	}
	if noStdin {
		t.Fatal("parseAttachArgs no_stdin = true, want false")
	}
	if id != "session-1" {
		t.Fatalf("parseAttachArgs id = %q, want %q", id, "session-1")
	}
}

func TestParseAttachArgsHandlesNoStdin(t *testing.T) {
	t.Parallel()

	id, noStdin, help, err := parseAttachArgs([]string{"--id", "session-1", "--no-stdin"})
	if err != nil {
		t.Fatalf("parseAttachArgs error = %v", err)
	}
	if help {
		t.Fatal("parseAttachArgs help = true, want false")
	}
	if !noStdin {
		t.Fatal("parseAttachArgs no_stdin = false, want true")
	}
	if id != "session-1" {
		t.Fatalf("parseAttachArgs id = %q, want %q", id, "session-1")
	}
}

func TestParseAttachArgsHandlesHelp(t *testing.T) {
	t.Parallel()

	for _, flag := range []string{"-h", "--help"} {
		_, _, help, err := parseAttachArgs([]string{flag})
		if err != nil {
			t.Fatalf("parseAttachArgs(%s) error = %v", flag, err)
		}
		if !help {
			t.Fatalf("parseAttachArgs(%s) help = false, want true", flag)
		}
	}
}

func TestAttachMainRequiresID(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	err := attachMain([]string{}, &buf, io.Discard)
	if err == nil {
		t.Fatal("attachMain accepted call without --id")
	}
	if !strings.Contains(err.Error(), "workcell session attach requires --id.") {
		t.Fatalf("attachMain error = %v, want canonical require-id message", err)
	}
}

func TestAttachMainHelpPrintsUsage(t *testing.T) {
	t.Parallel()

	var stderr bytes.Buffer
	if err := attachMain([]string{"--help"}, io.Discard, &stderr); err != nil {
		t.Fatalf("attachMain(--help) error = %v", err)
	}
	if !strings.Contains(stderr.String(), "Usage: workcell session") {
		t.Fatalf("attachMain(--help) stderr = %q, want usage banner", stderr.String())
	}
}

func TestAttachMainEmitsPlanFromRecord(t *testing.T) {
	root := t.TempDir()
	writeAttachFixtureRecord(t, root, "wcl-detached-fixture", "fixture-1", map[string]string{
		"profile":           "wcl-detached-fixture",
		"container_name":    "workcell-session-fixture",
		"monitor_pid":       "4242",
		"session_audit_dir": "/tmp/audit-fixture",
		"status":            "running",
		"live_status":       "running",
	})

	var buf bytes.Buffer
	args := []string{"--root=" + root, "--id", "fixture-1"}
	if err := attachMain(args, &buf, io.Discard); err != nil {
		t.Fatalf("attachMain error = %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"session_id=fixture-1\n",
		"no_stdin=0\n",
		"profile=wcl-detached-fixture\n",
		"container_name=workcell-session-fixture\n",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("attachMain output missing %q\nfull output:\n%s", want, out)
		}
	}
}

func TestAttachMainPropagatesNoStdin(t *testing.T) {
	root := t.TempDir()
	writeAttachFixtureRecord(t, root, "wcl-detached-fixture", "fixture-1", map[string]string{
		"profile":           "wcl-detached-fixture",
		"container_name":    "workcell-session-fixture",
		"monitor_pid":       "4242",
		"session_audit_dir": "/tmp/audit-fixture",
		"status":            "running",
		"live_status":       "running",
	})

	var buf bytes.Buffer
	args := []string{"--root=" + root, "--id", "fixture-1", "--no-stdin"}
	if err := attachMain(args, &buf, io.Discard); err != nil {
		t.Fatalf("attachMain error = %v", err)
	}
	if !strings.Contains(buf.String(), "no_stdin=1\n") {
		t.Fatalf("attachMain --no-stdin did not propagate; output:\n%s", buf.String())
	}
}

func TestAttachMainRejectsAttachedSession(t *testing.T) {
	root := t.TempDir()
	writeAttachFixtureRecord(t, root, "wcl-attached", "fixture-attached", map[string]string{
		"profile":        "wcl-attached",
		"container_name": "workcell-session-fixture",
		// no monitor_pid -> attached session
		"status":      "running",
		"live_status": "running",
	})

	var buf bytes.Buffer
	args := []string{"--root=" + root, "--id", "fixture-attached"}
	err := attachMain(args, &buf, io.Discard)
	if err == nil {
		t.Fatal("attachMain accepted an attached session")
	}
	if !strings.Contains(err.Error(), "only works for detached sessions") {
		t.Fatalf("attachMain error = %v, want detached-required message", err)
	}
}

func TestAttachMainRejectsMissingContainerName(t *testing.T) {
	root := t.TempDir()
	writeAttachFixtureRecord(t, root, "wcl-detached-fixture", "fixture-bad", map[string]string{
		"profile":           "wcl-detached-fixture",
		"monitor_pid":       "4242",
		"session_audit_dir": "/tmp/audit-fixture",
		"status":            "running",
		"live_status":       "running",
	})

	var buf bytes.Buffer
	args := []string{"--root=" + root, "--id", "fixture-bad"}
	err := attachMain(args, &buf, io.Discard)
	if err == nil {
		t.Fatal("attachMain accepted record missing container_name")
	}
	if !strings.Contains(err.Error(), "missing a container name") {
		t.Fatalf("attachMain error = %v, want container_name complaint", err)
	}
}

// writeAttachFixtureRecord lays out a minimal session JSON record under
// root/<profile>/sessions/<session_id>.json so AttachMain's lookup via
// sessions.FindSessionRecordInRoots can find it without needing the
// full session-record-write path.
func writeAttachFixtureRecord(t *testing.T, root, profile, sessionID string, fields map[string]string) {
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
