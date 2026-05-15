// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package sessionctl

import (
	"strings"
	"testing"

	"github.com/omkhar/workcell/internal/cliexit"
)

func TestOptionValueOrErrorReturnsValueAndAdvancedIndex(t *testing.T) {
	t.Parallel()

	value, next, err := optionValueOrError([]string{"--id", "session-1"}, 0, "--id")
	if err != nil {
		t.Fatalf("optionValueOrError err = %v, want nil", err)
	}
	if value != "session-1" {
		t.Errorf("value = %q, want session-1", value)
	}
	if next != 1 {
		t.Errorf("next = %d, want 1", next)
	}
}

func TestOptionValueOrErrorRejectsEmptyNextToken(t *testing.T) {
	t.Parallel()

	_, _, err := optionValueOrError([]string{"--id", ""}, 0, "--id")
	ec, ok := cliexit.IsExitCodeError(err)
	if !ok || ec.Code != 2 {
		t.Fatalf("err = %v, want *cliexit.ExitCodeError{Code:2}", err)
	}
	if !strings.Contains(ec.Message, "requires a value") {
		t.Errorf("Message = %q, want substring 'requires a value'", ec.Message)
	}
}

func TestOptionValueOrErrorRejectsAtEndOfArgs(t *testing.T) {
	t.Parallel()

	_, _, err := optionValueOrError([]string{"--id"}, 0, "--id")
	ec, ok := cliexit.IsExitCodeError(err)
	if !ok || ec.Code != 2 {
		t.Fatalf("err = %v, want *cliexit.ExitCodeError{Code:2}", err)
	}
	if !strings.Contains(ec.Message, "--id") {
		t.Errorf("Message = %q, want substring '--id'", ec.Message)
	}
}

func TestOptionValueOrErrorAcceptsDashDashPrefixedValue(t *testing.T) {
	t.Parallel()

	// optionValueOrError is the raw mode: --message values may
	// legitimately begin with `--` (the operator is sending a
	// payload), so only the non-empty check fires.
	value, next, err := optionValueOrError([]string{"--message", "--hello"}, 0, "--message")
	if err != nil {
		t.Fatalf("optionValueOrError err = %v, want nil", err)
	}
	if value != "--hello" || next != 1 {
		t.Fatalf("value=%q next=%d, want --hello 1", value, next)
	}
}

func TestOptionValueOrErrorStrictRejectsDashDashPrefixedValue(t *testing.T) {
	t.Parallel()

	// optionValueOrErrorStrict is the bash option_value_or_die
	// contract: a `--`-prefixed next token is treated as the missing
	// value swallowed by the next flag.
	_, _, err := optionValueOrErrorStrict([]string{"--id", "--message"}, 0, "--id")
	ec, ok := cliexit.IsExitCodeError(err)
	if !ok || ec.Code != 2 {
		t.Fatalf("err = %v, want *cliexit.ExitCodeError{Code:2}", err)
	}
	if !strings.Contains(ec.Message, "--id") {
		t.Errorf("Message = %q, want substring '--id'", ec.Message)
	}
}

func TestOptionValueOrErrorStrictAcceptsPlainValue(t *testing.T) {
	t.Parallel()

	value, next, err := optionValueOrErrorStrict([]string{"--id", "session-1"}, 0, "--id")
	if err != nil {
		t.Fatalf("optionValueOrErrorStrict err = %v, want nil", err)
	}
	if value != "session-1" || next != 1 {
		t.Fatalf("value=%q next=%d, want session-1 1", value, next)
	}
}

func TestUnsupportedOptionBuildsMessageAndExit2(t *testing.T) {
	t.Parallel()

	err := unsupportedOption("session send", "--bogus")
	ec, ok := cliexit.IsExitCodeError(err)
	if !ok || ec.Code != 2 {
		t.Fatalf("err = %v, want *cliexit.ExitCodeError{Code:2}", err)
	}
	if !strings.Contains(ec.Message, "Unsupported workcell session send option: --bogus") {
		t.Errorf("Message = %q, want substring 'Unsupported workcell session send option: --bogus'", ec.Message)
	}
}

func TestRejectControlCharsAcceptsCleanValue(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"session-1", "Verify PR title", "tab\there", ""} {
		if err := rejectControlChars("session send", "--id", value); err != nil {
			t.Errorf("rejectControlChars(%q) err = %v, want nil", value, err)
		}
	}
}

func TestRejectControlCharsRejectsNewline(t *testing.T) {
	t.Parallel()

	err := rejectControlChars("session send", "--message", "hello\nsession_id=other")
	ec, ok := cliexit.IsExitCodeError(err)
	if !ok || ec.Code != 2 {
		t.Fatalf("err = %v, want *cliexit.ExitCodeError{Code:2}", err)
	}
	if !strings.Contains(ec.Message, "newline") {
		t.Errorf("Message = %q, want substring 'newline'", ec.Message)
	}
}

func TestRejectControlCharsRejectsCarriageReturn(t *testing.T) {
	t.Parallel()

	err := rejectControlChars("session send", "--message", "hello\rsmuggled")
	ec, ok := cliexit.IsExitCodeError(err)
	if !ok || ec.Code != 2 {
		t.Fatalf("err = %v, want *cliexit.ExitCodeError{Code:2}", err)
	}
	if !strings.Contains(ec.Message, "carriage-return") {
		t.Errorf("Message = %q, want substring 'carriage-return'", ec.Message)
	}
}

func TestRejectControlCharsAcceptsTab(t *testing.T) {
	t.Parallel()

	// Tab is allowed: it isn't a record/field separator in the bash
	// `IFS= read -r line` loop that parses the KEY=VALUE plan, and
	// users may legitimately include tabs in --message bodies.
	if err := rejectControlChars("session send", "--message", "col1\tcol2"); err != nil {
		t.Errorf("rejectControlChars(tab) err = %v, want nil", err)
	}
}
