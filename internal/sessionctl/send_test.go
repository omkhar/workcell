// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package sessionctl

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/omkhar/workcell/internal/authpolicy"
)

func TestParseSendArgsRequiresIDValue(t *testing.T) {
	t.Parallel()

	_, _, _, _, err := parseSendArgs([]string{"--id"})
	if err == nil {
		t.Fatal("parseSendArgs accepted --id without a value")
	}
	if !strings.Contains(err.Error(), "Option --id requires a value.") {
		t.Fatalf("parseSendArgs error = %v, want canonical require-value message", err)
	}
}

func TestParseSendArgsRejectsEmptyIDValue(t *testing.T) {
	t.Parallel()

	_, _, _, _, err := parseSendArgs([]string{"--id", ""})
	if err == nil {
		t.Fatal("parseSendArgs accepted empty --id value")
	}
}

func TestParseSendArgsRejectsDashDashIDValue(t *testing.T) {
	t.Parallel()

	// --id uses option_value_or_die in bash, which rejects --*-prefixed
	// values so a missing value swallowed by the next flag fails fast.
	_, _, _, _, err := parseSendArgs([]string{"--id", "--message", "hello"})
	if err == nil {
		t.Fatal("parseSendArgs accepted --id with --message swallowed as value")
	}
}

func TestParseSendArgsRequiresMessageValue(t *testing.T) {
	t.Parallel()

	_, _, _, _, err := parseSendArgs([]string{"--message"})
	if err == nil {
		t.Fatal("parseSendArgs accepted --message without a value")
	}
	if !strings.Contains(err.Error(), "Option --message requires a value.") {
		t.Fatalf("parseSendArgs error = %v, want canonical require-value message", err)
	}
}

func TestParseSendArgsRejectsEmptyMessageValue(t *testing.T) {
	t.Parallel()

	_, _, _, _, err := parseSendArgs([]string{"--message", ""})
	if err == nil {
		t.Fatal("parseSendArgs accepted empty --message value")
	}
}

func TestParseSendArgsAcceptsDashDashMessageValue(t *testing.T) {
	t.Parallel()

	// --message uses raw_option_value_or_die in bash, which permits
	// values that start with `--` so operators can send a payload such
	// as "--flag" verbatim.
	id, msg, newline, _, err := parseSendArgs([]string{"--id", "s", "--message", "--literal-flag"})
	if err != nil {
		t.Fatalf("parseSendArgs error = %v", err)
	}
	if id != "s" {
		t.Fatalf("parseSendArgs id = %q, want %q", id, "s")
	}
	if msg != "--literal-flag" {
		t.Fatalf("parseSendArgs message = %q, want %q", msg, "--literal-flag")
	}
	if !newline {
		t.Fatal("parseSendArgs default append_newline = false, want true")
	}
}

func TestParseSendArgsRejectsUnknownFlag(t *testing.T) {
	t.Parallel()

	_, _, _, _, err := parseSendArgs([]string{"--bogus"})
	if err == nil {
		t.Fatal("parseSendArgs accepted unknown flag")
	}
	if !strings.Contains(err.Error(), "Unsupported workcell session send option") {
		t.Fatalf("parseSendArgs error = %v, want session-send-specific message", err)
	}
}

func TestParseSendArgsAcceptsCanonical(t *testing.T) {
	t.Parallel()

	id, msg, newline, help, err := parseSendArgs([]string{"--id", "session-1", "--message", "hello"})
	if err != nil {
		t.Fatalf("parseSendArgs error = %v", err)
	}
	if help {
		t.Fatal("parseSendArgs help = true, want false")
	}
	if !newline {
		t.Fatal("parseSendArgs append_newline = false, want true (default)")
	}
	if id != "session-1" {
		t.Fatalf("parseSendArgs id = %q, want %q", id, "session-1")
	}
	if msg != "hello" {
		t.Fatalf("parseSendArgs message = %q, want %q", msg, "hello")
	}
}

func TestParseSendArgsHandlesNoNewline(t *testing.T) {
	t.Parallel()

	id, msg, newline, _, err := parseSendArgs([]string{"--id", "s", "--message", "m", "--no-newline"})
	if err != nil {
		t.Fatalf("parseSendArgs error = %v", err)
	}
	if newline {
		t.Fatal("parseSendArgs --no-newline did not clear append_newline")
	}
	if id != "s" || msg != "m" {
		t.Fatalf("parseSendArgs id/msg = %q/%q, want s/m", id, msg)
	}
}

func TestParseSendArgsHandlesHelp(t *testing.T) {
	t.Parallel()

	for _, flag := range []string{"-h", "--help"} {
		_, _, _, help, err := parseSendArgs([]string{flag})
		if err != nil {
			t.Fatalf("parseSendArgs(%s) error = %v", flag, err)
		}
		if !help {
			t.Fatalf("parseSendArgs(%s) help = false, want true", flag)
		}
	}
}

func TestSendMainRequiresID(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	err := sendMain([]string{"--message", "hello"}, &buf)
	if err == nil {
		t.Fatal("sendMain accepted call without --id")
	}
	if !strings.Contains(err.Error(), "workcell session send requires --id.") {
		t.Fatalf("sendMain error = %v, want canonical require-id message", err)
	}
	var ec *authpolicy.ExitCodeError
	if !errors.As(err, &ec) || ec.Code != 2 {
		t.Fatalf("sendMain error = %v, want ExitCodeError{Code:2}", err)
	}
}

func TestSendMainRequiresMessage(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	err := sendMain([]string{"--id", "session-1"}, &buf)
	if err == nil {
		t.Fatal("sendMain accepted call without --message")
	}
	if !strings.Contains(err.Error(), "workcell session send requires --message.") {
		t.Fatalf("sendMain error = %v, want canonical require-message message", err)
	}
	var ec *authpolicy.ExitCodeError
	if !errors.As(err, &ec) || ec.Code != 2 {
		t.Fatalf("sendMain error = %v, want ExitCodeError{Code:2}", err)
	}
}

func TestSendMainHelpPrintsUsage(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	if err := sendMain([]string{"--help"}, &buf); err != nil {
		t.Fatalf("sendMain(--help) error = %v", err)
	}
	if !strings.Contains(buf.String(), "Usage: workcell session") {
		t.Fatalf("sendMain(--help) output = %q, want usage banner", buf.String())
	}
}

func TestSendMainEmitsPlan(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	args := []string{"--id", "session-1", "--message", "hello world"}
	if err := sendMain(args, &buf); err != nil {
		t.Fatalf("sendMain error = %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"session_id=session-1\n",
		"message=hello world\n",
		"append_newline=1\n",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("sendMain output missing %q\nfull output:\n%s", want, out)
		}
	}
}

func TestSendMainPropagatesNoNewline(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	args := []string{"--id", "session-1", "--message", "hello", "--no-newline"}
	if err := sendMain(args, &buf); err != nil {
		t.Fatalf("sendMain error = %v", err)
	}
	if !strings.Contains(buf.String(), "append_newline=0\n") {
		t.Fatalf("sendMain --no-newline did not propagate; output:\n%s", buf.String())
	}
}

func TestSendMainStripsRootArgs(t *testing.T) {
	t.Parallel()

	// --root= forwarding is unused by SendMain today but must not break
	// argument parsing - the bash shim unconditionally prepends
	// session_lookup_root_args output.
	var buf bytes.Buffer
	args := []string{"--root=/tmp/state-1", "--root=", "--id", "session-1", "--message", "beta"}
	if err := sendMain(args, &buf); err != nil {
		t.Fatalf("sendMain error = %v", err)
	}
	if !strings.Contains(buf.String(), "session_id=session-1\n") {
		t.Fatalf("sendMain stripped roots but lost session_id; output:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "message=beta\n") {
		t.Fatalf("sendMain stripped roots but lost message; output:\n%s", buf.String())
	}
}

func TestSendMainRejectsUnknownOption(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	err := sendMain([]string{"--bogus"}, &buf)
	if err == nil {
		t.Fatal("sendMain accepted unknown option")
	}
	var ec *authpolicy.ExitCodeError
	if !errors.As(err, &ec) || ec.Code != 2 {
		t.Fatalf("sendMain error = %v, want ExitCodeError{Code:2}", err)
	}
}
