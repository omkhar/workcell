// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package sessionctl

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/omkhar/workcell/internal/cliexit"
)

func TestParseDeleteArgsRequiresIDValue(t *testing.T) {
	t.Parallel()

	_, _, _, _, err := parseDeleteArgs([]string{"--id"})
	if err == nil {
		t.Fatal("parseDeleteArgs accepted --id without a value")
	}
	var ec *cliexit.ExitCodeError
	if !errors.As(err, &ec) {
		t.Fatalf("parseDeleteArgs err = %v, want ExitCodeError", err)
	}
	if ec.Code != 2 {
		t.Fatalf("parseDeleteArgs ExitCodeError.Code = %d, want 2", ec.Code)
	}
	if !strings.Contains(ec.Message, "Option --id requires a value") {
		t.Fatalf("parseDeleteArgs message = %q, want missing-value rejection", ec.Message)
	}
}

func TestParseDeleteArgsRejectsEmptyIDValue(t *testing.T) {
	t.Parallel()

	_, _, _, _, err := parseDeleteArgs([]string{"--id", ""})
	if err == nil {
		t.Fatal("parseDeleteArgs accepted empty --id value")
	}
}

func TestParseDeleteArgsRejectsUnknownFlag(t *testing.T) {
	t.Parallel()

	_, _, _, _, err := parseDeleteArgs([]string{"--bogus"})
	if err == nil {
		t.Fatal("parseDeleteArgs accepted unknown flag")
	}
	var ec *cliexit.ExitCodeError
	if !errors.As(err, &ec) {
		t.Fatalf("parseDeleteArgs err = %v, want ExitCodeError", err)
	}
	if ec.Code != 2 {
		t.Fatalf("parseDeleteArgs ExitCodeError.Code = %d, want 2", ec.Code)
	}
	if !strings.Contains(ec.Message, "Unsupported workcell session delete option") {
		t.Fatalf("parseDeleteArgs message = %q, want session-delete-specific message", ec.Message)
	}
}

func TestParseDeleteArgsAcceptsCanonical(t *testing.T) {
	t.Parallel()

	id, recordOnly, dryRun, help, err := parseDeleteArgs([]string{"--id", "session-1"})
	if err != nil {
		t.Fatalf("parseDeleteArgs error = %v", err)
	}
	if help {
		t.Fatal("parseDeleteArgs help = true, want false")
	}
	if recordOnly {
		t.Fatal("parseDeleteArgs record_only = true, want false")
	}
	if dryRun {
		t.Fatal("parseDeleteArgs dry_run = true, want false")
	}
	if id != "session-1" {
		t.Fatalf("parseDeleteArgs id = %q, want %q", id, "session-1")
	}
}

func TestParseDeleteArgsHandlesRecordOnly(t *testing.T) {
	t.Parallel()

	id, recordOnly, dryRun, _, err := parseDeleteArgs([]string{"--id", "s", "--record-only"})
	if err != nil {
		t.Fatalf("parseDeleteArgs error = %v", err)
	}
	if !recordOnly {
		t.Fatal("parseDeleteArgs --record-only did not set recordOnly")
	}
	if dryRun {
		t.Fatal("parseDeleteArgs --record-only set dry_run unexpectedly")
	}
	if id != "s" {
		t.Fatalf("parseDeleteArgs id = %q, want %q", id, "s")
	}
}

func TestParseDeleteArgsHandlesDryRun(t *testing.T) {
	t.Parallel()

	id, recordOnly, dryRun, _, err := parseDeleteArgs([]string{"--id", "s", "--dry-run"})
	if err != nil {
		t.Fatalf("parseDeleteArgs error = %v", err)
	}
	if recordOnly {
		t.Fatal("parseDeleteArgs --dry-run set record_only unexpectedly")
	}
	if !dryRun {
		t.Fatal("parseDeleteArgs --dry-run did not set dryRun")
	}
	if id != "s" {
		t.Fatalf("parseDeleteArgs id = %q, want %q", id, "s")
	}
}

func TestParseDeleteArgsHandlesBothToggles(t *testing.T) {
	t.Parallel()

	_, recordOnly, dryRun, _, err := parseDeleteArgs([]string{"--id", "s", "--record-only", "--dry-run"})
	if err != nil {
		t.Fatalf("parseDeleteArgs error = %v", err)
	}
	if !recordOnly || !dryRun {
		t.Fatalf("parseDeleteArgs combined toggles = (record_only=%v, dry_run=%v), want both true", recordOnly, dryRun)
	}
}

func TestParseDeleteArgsHandlesHelp(t *testing.T) {
	t.Parallel()

	for _, flag := range []string{"-h", "--help"} {
		_, _, _, help, err := parseDeleteArgs([]string{flag})
		if err != nil {
			t.Fatalf("parseDeleteArgs(%s) error = %v", flag, err)
		}
		if !help {
			t.Fatalf("parseDeleteArgs(%s) help = false, want true", flag)
		}
	}
}

func TestDeleteMainRequiresID(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	err := deleteMain([]string{}, &buf, io.Discard)
	if err == nil {
		t.Fatal("deleteMain accepted call without --id")
	}
	var ec *cliexit.ExitCodeError
	if !errors.As(err, &ec) {
		t.Fatalf("deleteMain err = %v, want ExitCodeError", err)
	}
	if ec.Code != 2 {
		t.Fatalf("deleteMain ExitCodeError.Code = %d, want 2", ec.Code)
	}
	if !strings.Contains(ec.Message, "workcell session delete requires --id.") {
		t.Fatalf("deleteMain message = %q, want canonical require-id message", ec.Message)
	}
}

func TestDeleteMainHelpPrintsUsage(t *testing.T) {
	t.Parallel()

	var stderr bytes.Buffer
	if err := deleteMain([]string{"--help"}, io.Discard, &stderr); err != nil {
		t.Fatalf("deleteMain(--help) error = %v", err)
	}
	if !strings.Contains(stderr.String(), "Usage: workcell session") {
		t.Fatalf("deleteMain(--help) stderr = %q, want usage banner", stderr.String())
	}
}

func TestDeleteMainEmitsPlan(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	if err := deleteMain([]string{"--id", "session-1"}, &buf, io.Discard); err != nil {
		t.Fatalf("deleteMain error = %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"session_id=session-1\n",
		"record_only=0\n",
		"dry_run=0\n",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("deleteMain output missing %q\nfull output:\n%s", want, out)
		}
	}
}

func TestDeleteMainPropagatesRecordOnly(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	args := []string{"--id", "session-1", "--record-only"}
	if err := deleteMain(args, &buf, io.Discard); err != nil {
		t.Fatalf("deleteMain error = %v", err)
	}
	if !strings.Contains(buf.String(), "record_only=1\n") {
		t.Fatalf("deleteMain --record-only did not propagate; output:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "dry_run=0\n") {
		t.Fatalf("deleteMain --record-only flipped dry_run; output:\n%s", buf.String())
	}
}

func TestDeleteMainPropagatesDryRun(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	args := []string{"--id", "session-1", "--dry-run"}
	if err := deleteMain(args, &buf, io.Discard); err != nil {
		t.Fatalf("deleteMain error = %v", err)
	}
	if !strings.Contains(buf.String(), "dry_run=1\n") {
		t.Fatalf("deleteMain --dry-run did not propagate; output:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "record_only=0\n") {
		t.Fatalf("deleteMain --dry-run flipped record_only; output:\n%s", buf.String())
	}
}

func TestDeleteMainPropagatesBothToggles(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	args := []string{"--id", "session-1", "--record-only", "--dry-run"}
	if err := deleteMain(args, &buf, io.Discard); err != nil {
		t.Fatalf("deleteMain error = %v", err)
	}
	if !strings.Contains(buf.String(), "record_only=1\n") {
		t.Fatalf("deleteMain combined --record-only missing; output:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "dry_run=1\n") {
		t.Fatalf("deleteMain combined --dry-run missing; output:\n%s", buf.String())
	}
}

func TestDeleteMainStripsRootArgs(t *testing.T) {
	t.Parallel()

	// --root= forwarding is unused by DeleteMain today but must not break
	// argument parsing - the bash shim unconditionally prepends
	// session_lookup_root_args output.
	var buf bytes.Buffer
	args := []string{"--root=/tmp/state-1", "--root=", "--id", "session-1", "--dry-run"}
	if err := deleteMain(args, &buf, io.Discard); err != nil {
		t.Fatalf("deleteMain error = %v", err)
	}
	if !strings.Contains(buf.String(), "session_id=session-1\n") {
		t.Fatalf("deleteMain stripped roots but lost session_id; output:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "dry_run=1\n") {
		t.Fatalf("deleteMain stripped roots but lost dry_run; output:\n%s", buf.String())
	}
}

func TestDeleteMainRejectsUnknownOption(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	err := deleteMain([]string{"--bogus"}, &buf, io.Discard)
	if err == nil {
		t.Fatal("deleteMain accepted unknown option")
	}
	var ec *cliexit.ExitCodeError
	if !errors.As(err, &ec) || ec.Code != 2 {
		t.Fatalf("deleteMain error = %v, want ExitCodeError{Code:2}", err)
	}
}

// TestDeleteMainRejectsNewlineInID — sibling guard to
// monitor_test.go's TestMonitorMainRejectsNewlineInStateFile.
func TestDeleteMainRejectsNewlineInID(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"session-1\nsession_id=other", "session-1\rsession_id=other"} {
		var buf bytes.Buffer
		err := deleteMain([]string{"--id", value}, &buf, io.Discard)
		if err == nil {
			t.Fatalf("deleteMain accepted --id value containing control character: %q", value)
		}
		var ec *cliexit.ExitCodeError
		if !errors.As(err, &ec) || ec.Code != 2 {
			t.Fatalf("deleteMain error = %v, want ExitCodeError{Code:2}", err)
		}
		if !strings.Contains(ec.Message, "must not contain newline or carriage-return") {
			t.Fatalf("deleteMain message = %q, want newline-rejection diagnostic", ec.Message)
		}
		if buf.Len() != 0 {
			t.Fatalf("deleteMain wrote %q on rejection, want no stdout output", buf.String())
		}
	}
}
