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

func TestParseMonitorArgsRequiresStateFileValue(t *testing.T) {
	t.Parallel()

	_, _, err := parseMonitorArgs([]string{"--state-file"})
	if err == nil {
		t.Fatal("parseMonitorArgs accepted --state-file without a value")
	}
	var ec *cliexit.ExitCodeError
	if !errors.As(err, &ec) {
		t.Fatalf("parseMonitorArgs err = %v, want ExitCodeError", err)
	}
	if ec.Code != 2 {
		t.Fatalf("parseMonitorArgs ExitCodeError.Code = %d, want 2", ec.Code)
	}
	if !strings.Contains(ec.Message, "Option --state-file requires a value") {
		t.Fatalf("parseMonitorArgs message = %q, want missing-value rejection", ec.Message)
	}
}

func TestParseMonitorArgsRejectsEmptyStateFileValue(t *testing.T) {
	t.Parallel()

	_, _, err := parseMonitorArgs([]string{"--state-file", ""})
	if err == nil {
		t.Fatal("parseMonitorArgs accepted empty --state-file value")
	}
}

func TestParseMonitorArgsRejectsUnknownFlag(t *testing.T) {
	t.Parallel()

	_, _, err := parseMonitorArgs([]string{"--bogus"})
	if err == nil {
		t.Fatal("parseMonitorArgs accepted unknown flag")
	}
	var ec *cliexit.ExitCodeError
	if !errors.As(err, &ec) {
		t.Fatalf("parseMonitorArgs err = %v, want ExitCodeError", err)
	}
	if ec.Code != 2 {
		t.Fatalf("parseMonitorArgs ExitCodeError.Code = %d, want 2", ec.Code)
	}
	if !strings.Contains(ec.Message, "Unsupported workcell session monitor option") {
		t.Fatalf("parseMonitorArgs message = %q, want session-monitor-specific message", ec.Message)
	}
}

func TestParseMonitorArgsAcceptsCanonical(t *testing.T) {
	t.Parallel()

	path, help, err := parseMonitorArgs([]string{"--state-file", "/tmp/state"})
	if err != nil {
		t.Fatalf("parseMonitorArgs error = %v", err)
	}
	if help {
		t.Fatal("parseMonitorArgs help = true, want false")
	}
	if path != "/tmp/state" {
		t.Fatalf("parseMonitorArgs state path = %q, want %q", path, "/tmp/state")
	}
}

func TestParseMonitorArgsHandlesHelp(t *testing.T) {
	t.Parallel()

	for _, flag := range []string{"-h", "--help"} {
		_, help, err := parseMonitorArgs([]string{flag})
		if err != nil {
			t.Fatalf("parseMonitorArgs(%s) error = %v", flag, err)
		}
		if !help {
			t.Fatalf("parseMonitorArgs(%s) help = false, want true", flag)
		}
	}
}

func TestMonitorMainRequiresStateFile(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	err := monitorMain([]string{}, &buf, io.Discard)
	if err == nil {
		t.Fatal("monitorMain accepted call without --state-file")
	}
	var ec *cliexit.ExitCodeError
	if !errors.As(err, &ec) {
		t.Fatalf("monitorMain err = %v, want ExitCodeError", err)
	}
	if ec.Code != 2 {
		t.Fatalf("monitorMain ExitCodeError.Code = %d, want 2", ec.Code)
	}
	if !strings.Contains(ec.Message, "workcell session monitor requires --state-file.") {
		t.Fatalf("monitorMain message = %q, want canonical require-state-file message", ec.Message)
	}
}

func TestMonitorMainRejectsMissingStateFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	missing := filepath.Join(dir, "absent.env")
	var buf bytes.Buffer
	err := monitorMain([]string{"--state-file", missing}, &buf, io.Discard)
	if err == nil {
		t.Fatal("monitorMain accepted missing --state-file")
	}
	var ec *cliexit.ExitCodeError
	if !errors.As(err, &ec) {
		t.Fatalf("monitorMain err = %v, want ExitCodeError", err)
	}
	if ec.Code != 2 {
		t.Fatalf("monitorMain ExitCodeError.Code = %d, want 2", ec.Code)
	}
	// Bash diagnostic must stay byte-identical for the docker-desktop
	// provider monitor test scenario in tests/scenarios/shared/test-session-commands.sh.
	want := "Missing detached session monitor state file: " + missing
	if !strings.Contains(ec.Message, want) {
		t.Fatalf("monitorMain message = %q, want canonical missing-file message %q", ec.Message, want)
	}
}

func TestMonitorMainRejectsDirectoryStateFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	var buf bytes.Buffer
	err := monitorMain([]string{"--state-file", dir}, &buf, io.Discard)
	if err == nil {
		t.Fatal("monitorMain accepted directory as --state-file")
	}
	var ec *cliexit.ExitCodeError
	if !errors.As(err, &ec) {
		t.Fatalf("monitorMain err = %v, want ExitCodeError", err)
	}
	if ec.Code != 2 {
		t.Fatalf("monitorMain ExitCodeError.Code = %d, want 2", ec.Code)
	}
}

func TestMonitorMainHelpPrintsUsage(t *testing.T) {
	t.Parallel()

	var stderr bytes.Buffer
	if err := monitorMain([]string{"--help"}, io.Discard, &stderr); err != nil {
		t.Fatalf("monitorMain(--help) error = %v", err)
	}
	if !strings.Contains(stderr.String(), "Usage: workcell session") {
		t.Fatalf("monitorMain(--help) stderr = %q, want usage banner", stderr.String())
	}
}

func TestMonitorMainEmitsStateFilePath(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	statePath := filepath.Join(dir, "session-monitor.env")
	if err := os.WriteFile(statePath, []byte("SESSION_ID=abc\nCOLIMA_PROFILE=p\n"), 0o600); err != nil {
		t.Fatalf("write state file: %v", err)
	}
	var buf bytes.Buffer
	if err := monitorMain([]string{"--state-file", statePath}, &buf, io.Discard); err != nil {
		t.Fatalf("monitorMain error = %v", err)
	}
	want := "state_file=" + statePath + "\n"
	if got := buf.String(); got != want {
		t.Fatalf("monitorMain output = %q, want %q", got, want)
	}
}

func TestMonitorMainRejectsUnknownOption(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	err := monitorMain([]string{"--bogus"}, &buf, io.Discard)
	if err == nil {
		t.Fatal("monitorMain accepted unknown option")
	}
	var ec *cliexit.ExitCodeError
	if !errors.As(err, &ec) || ec.Code != 2 {
		t.Fatalf("monitorMain error = %v, want ExitCodeError{Code:2}", err)
	}
}

// TestMonitorMainRejectsNewlineInStateFile guards the rejectControlChars
// hook: a newline in --state-file would let an attacker forge additional
// state_<KEY>=<VALUE> plan lines into the shim's read loop (CRLF
// injection).  Mirrors the matching guards in attach/send/stop/delete.
func TestMonitorMainRejectsNewlineInStateFile(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"/tmp/state\nstate_file=/etc/passwd", "/tmp/state\rstate_file=/etc/passwd"} {
		var buf bytes.Buffer
		err := monitorMain([]string{"--state-file", value}, &buf, io.Discard)
		if err == nil {
			t.Fatalf("monitorMain accepted --state-file value containing control character: %q", value)
		}
		var ec *cliexit.ExitCodeError
		if !errors.As(err, &ec) || ec.Code != 2 {
			t.Fatalf("monitorMain error = %v, want ExitCodeError{Code:2}", err)
		}
		if !strings.Contains(ec.Message, "must not contain newline or carriage-return") {
			t.Fatalf("monitorMain message = %q, want newline-rejection diagnostic", ec.Message)
		}
		if buf.Len() != 0 {
			t.Fatalf("monitorMain wrote %q on rejection, want no stdout output", buf.String())
		}
	}
}

func TestMonitorStateEntriesParsesSimple(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "state.env")
	body := strings.Join([]string{
		"# leading comment",
		"",
		"SESSION_ID=detached-1",
		"COLIMA_PROFILE=wcl-detached",
		"CONTAINER_NAME=workcell-session-fixture",
		"EXECUTION_PATH=managed-tier1",
		"WORKCELL_STATE_ROOT=/tmp/state",
		"=invalid-empty-key",
		"SESSION_MONITOR_READY_PATH=/tmp/state/ready",
	}, "\n")
	if err := os.WriteFile(path, []byte(body+"\n"), 0o600); err != nil {
		t.Fatalf("write state file: %v", err)
	}
	entries, err := MonitorStateEntries(path)
	if err != nil {
		t.Fatalf("MonitorStateEntries error = %v", err)
	}
	want := []MonitorStateEntry{
		{Key: "SESSION_ID", Value: "detached-1"},
		{Key: "COLIMA_PROFILE", Value: "wcl-detached"},
		{Key: "CONTAINER_NAME", Value: "workcell-session-fixture"},
		{Key: "EXECUTION_PATH", Value: "managed-tier1"},
		{Key: "WORKCELL_STATE_ROOT", Value: "/tmp/state"},
		{Key: "SESSION_MONITOR_READY_PATH", Value: "/tmp/state/ready"},
	}
	if len(entries) != len(want) {
		t.Fatalf("MonitorStateEntries len = %d, want %d (entries=%+v)", len(entries), len(want), entries)
	}
	for i, entry := range entries {
		if entry != want[i] {
			t.Fatalf("MonitorStateEntries[%d] = %+v, want %+v", i, entry, want[i])
		}
	}
}

func TestMonitorStateEntriesStripsQuotes(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "state.env")
	body := strings.Join([]string{
		`SESSION_ID="quoted-id"`,
		`COLIMA_PROFILE='single-quoted'`,
		`MIXED="left-only`,
		`STRAY='only-right"`,
	}, "\n")
	if err := os.WriteFile(path, []byte(body+"\n"), 0o600); err != nil {
		t.Fatalf("write state file: %v", err)
	}
	entries, err := MonitorStateEntries(path)
	if err != nil {
		t.Fatalf("MonitorStateEntries error = %v", err)
	}
	got := map[string]string{}
	for _, entry := range entries {
		got[entry.Key] = entry.Value
	}
	if got["SESSION_ID"] != "quoted-id" {
		t.Fatalf("MonitorStateEntries SESSION_ID = %q, want %q", got["SESSION_ID"], "quoted-id")
	}
	if got["COLIMA_PROFILE"] != "single-quoted" {
		t.Fatalf("MonitorStateEntries COLIMA_PROFILE = %q, want %q", got["COLIMA_PROFILE"], "single-quoted")
	}
	// Mismatched quotes must be preserved so bash can still parse the
	// quoting itself; trimQuotes only strips when both ends match.
	if got["MIXED"] != `"left-only` {
		t.Fatalf("MonitorStateEntries MIXED = %q, want %q", got["MIXED"], `"left-only`)
	}
	if got["STRAY"] != `'only-right"` {
		t.Fatalf("MonitorStateEntries STRAY = %q, want %q", got["STRAY"], `'only-right"`)
	}
}

func TestMonitorStateEntriesMissingFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	_, err := MonitorStateEntries(filepath.Join(dir, "absent.env"))
	if err == nil {
		t.Fatal("MonitorStateEntries accepted a missing file")
	}
}
