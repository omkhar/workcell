// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package testkit

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// errDuplicateFlag is the rejection a strict parser reports.
var errDuplicateFlag = errors.New("flag given more than once")

// parseLongSpaceOnly is the parser a hand-written assertion actually proves:
// it rejects a duplicate only in the "--flag value" spelling. Every other
// spelling reaches the last-wins assignment. It is the negative control for
// FlagForms.
func parseLongSpaceOnly(argv []string, flag string) error {
	seen := 0
	for _, arg := range argv {
		if arg == "--"+flag {
			seen++
		}
	}
	if seen > 1 {
		return errDuplicateFlag
	}
	return nil
}

// parseEveryForm rejects a duplicate in every spelling, using the same
// tokenised count the contract requires.
func parseEveryForm(argv []string, flag, short string) error {
	if CountFlagOccurrences(argv, flag, short) > 1 {
		return errDuplicateFlag
	}
	return nil
}

// TestFlagFormsExposesTheSpellingsALongSpaceAssertionMisses proves the matrix
// is not vacuous: against a parser that only knows "--flag value", four of the
// five forms must slip through. A FlagForms that emitted five variations of
// the long-space spelling would make this test fail.
func TestFlagFormsExposesTheSpellingsALongSpaceAssertionMisses(t *testing.T) {
	t.Parallel()

	base := []string{"--hostname", "trusted.example"}
	escaped := 0
	for _, form := range FlagForms("hostname", "H", "attacker.example") {
		argv := append(append([]string(nil), base...), form...)
		if parseLongSpaceOnly(argv, "hostname") == nil {
			escaped++
		}
	}
	if escaped != 4 {
		t.Fatalf("long-space-only parser accepted %d of the attached spellings, want 4", escaped)
	}
}

func TestFlagFormsOmitsShortSpellingsWhenThereIsNoShortName(t *testing.T) {
	t.Parallel()

	forms := FlagForms("assets-dir", "", "/tmp/assets")
	if len(forms) != 2 {
		t.Fatalf("FlagForms() with no short name = %q, want the two long spellings", forms)
	}
}

func TestAssertRejectsLaterOverrideAcceptsAParserThatRejectsEveryForm(t *testing.T) {
	t.Parallel()

	run := func(argv []string) error { return parseEveryForm(argv, "hostname", "H") }
	AssertRejectsLaterOverride(t, run, []string{"--hostname", "trusted.example"}, "hostname", "H", "attacker.example")
}

// TestCountFlagOccurrencesIgnoresAValueContainingTheFlag is the negative
// control for the substring defect: a path holding the flag text must not be
// counted as an occurrence, while the flattened-string scan the contract bans
// does count it.
func TestCountFlagOccurrencesIgnoresAValueContainingTheFlag(t *testing.T) {
	t.Parallel()

	argv := []string{"--assets-dir", "/tmp/review--hostname-tmp", "--hostname", "trusted.example"}
	if got := CountFlagOccurrences(argv, "hostname", "H"); got != 1 {
		t.Fatalf("CountFlagOccurrences() = %d, want 1", got)
	}
	if got := strings.Count(strings.Join(argv, " "), "--hostname"); got != 2 {
		t.Fatalf("flattened-argument scan found %d matches; the fixture no longer reproduces the substring defect", got)
	}
}

func TestCountFlagOccurrencesCountsEveryFormOnce(t *testing.T) {
	t.Parallel()

	for _, form := range FlagForms("hostname", "H", "attacker.example") {
		if got := CountFlagOccurrences(form, "hostname", "H"); got != 1 {
			t.Errorf("CountFlagOccurrences(%q) = %d, want 1", form, got)
		}
	}
}

// TestShellQuoteSurvivesShellReexpansion proves the quoting round-trips a
// hostile value through Bash, and that Go's %q verb does not. The second half
// is the negative control: if %q ever started surviving, the ban this package
// enforces would be pointless.
func TestShellQuoteSurvivesShellReexpansion(t *testing.T) {
	t.Parallel()

	hostile := "$HOME 'quoted' $(id) `id` \\n \"dq\" $((1+1))"

	code, output := runBashProbe(t, "printf '%s' "+ShellQuote(hostile), nil)
	if code != 0 {
		t.Fatalf("quoted probe exit code = %d output=%q", code, output)
	}
	if output != hostile {
		t.Fatalf("ShellQuote() round trip = %q, want %q", output, hostile)
	}

	code, naive := runBashProbe(t, "printf '%s' "+fmt.Sprintf("%q", hostile), nil)
	if code == 0 && naive == hostile {
		t.Fatal("the Go quoting verb survived the shell round trip; the fixture no longer reproduces the re-expansion defect")
	}
}
