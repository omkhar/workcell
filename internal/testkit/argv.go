// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package testkit

import (
	"strings"
	"testing"
)

// FlagForms returns every accepted spelling of one flag and its value:
//
//	--flag value    --flag=value    -s value    -s=value    -svalue
//
// flag and short are bare names, without leading dashes. An empty short
// suppresses the three short forms.
//
// A test that asserts a security-sensitive flag must exercise all of them. A
// hand-written assertion covers the long-space form only, so a parser that
// splits the argument vector on whitespace and looks at the token after the
// flag accepts every attached spelling unread.
func FlagForms(flag, short, value string) [][]string {
	forms := [][]string{
		{"--" + flag, value},
		{"--" + flag + "=" + value},
	}
	if short == "" {
		return forms
	}
	return append(forms,
		[]string{"-" + short, value},
		[]string{"-" + short + "=" + value},
		[]string{"-" + short + value},
	)
}

// CountFlagOccurrences reports how many entries of argv spell flag or short,
// in any form FlagForms produces.
//
// The comparison is per argv entry, never a substring scan of the flattened
// argument string. A value that merely contains the flag text, such as a
// temporary directory named ".../review--hostname-tmp", is not an occurrence,
// and a `$*` substring test counts it as one. Callers assert exactly one
// occurrence for a security-sensitive flag.
//
// ponytail: a short name is matched at entry position 1 only; a clustered
// form such as "-vH value" is not counted. Widen when a caller needs it.
func CountFlagOccurrences(argv []string, flag, short string) int {
	long := "--" + flag
	count := 0
	for _, arg := range argv {
		if arg == long || strings.HasPrefix(arg, long+"=") {
			count++
			continue
		}
		if short != "" && !strings.HasPrefix(arg, "--") && strings.HasPrefix(arg, "-"+short) {
			count++
		}
	}
	return count
}

// AssertRejectsLaterOverride fails t unless run rejects base with hostile
// appended in every spelling FlagForms produces.
//
// A last-wins parser is the danger. The trusted value in base stays visible to
// an assertion that reads the first occurrence, while the process being driven
// uses the attacker's later one. Rejecting the duplicate outright is the only
// behaviour that is safe under both readings.
func AssertRejectsLaterOverride(t *testing.T, run func([]string) error, base []string, flag, short, hostile string) {
	t.Helper()
	for _, form := range FlagForms(flag, short, hostile) {
		argv := append(append([]string(nil), base...), form...)
		if err := run(argv); err == nil {
			t.Errorf("argv %q accepted a later %s override; want a rejection", argv, strings.Join(form, " "))
		}
	}
}

// ShellQuote returns s as one POSIX shell word: single-quoted, with every
// embedded single quote closed, escaped and reopened.
//
// Go's %q verb is not a substitute. It emits a Go literal, and the shell then
// re-reads that literal under its own rules: a value holding $HOME, a backtick
// or a $(...) passes through %q unchanged and the shell expands it. Single
// quotes suppress every expansion, so this is safe for any byte sequence.
func ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
