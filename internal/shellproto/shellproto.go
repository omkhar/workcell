// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

// Package shellproto is the canonical KEY=VALUE protocol that workcell
// Go CLIs use to hand structured state back to their bash callers.
//
// The protocol is the lowest-common-denominator format the bash
// `while IFS= read -r line; key=${line%%=*}; value=${line#*=}; case ...`
// pattern can parse, and it is what every translated session_*_main,
// publish-pr dry-run header, and injection-bundle result already emits.
//
// Centralising the emit logic in this package replaces the dozen-odd
// ad-hoc `fmt.Fprintf(stdout, "%s=%s\n", k, v)` call sites that grew up
// alongside the bash↔Go translation effort.  Two invariants are
// enforced at the output boundary:
//
//  1. Keys may not be empty, contain '=', '\n', '\r', or '\0' — bash's
//     read loop relies on '=' as the key/value separator and on '\n' as
//     the line terminator, so any of these would corrupt parsing.
//  2. Values may not contain '\n', '\r', or '\0' — '\n' would forge a
//     new key=value record (a CRLF-injection that the input-side
//     parsehelpers.rejectControlChars catches as a belt-and-suspenders
//     defence), and '\0' truncates the bash read loop.
//
// rejectControlChars in internal/sessionctl/parsehelpers.go remains the
// input-boundary defence on user-controlled flags; WriteField is the
// second line of defence at the output boundary so that any future
// emitter that forgets to validate the input cannot silently inject
// forged plan lines.
package shellproto

import (
	"fmt"
	"io"
	"strings"
)

// Field is one KEY=VALUE pair.  Use a slice of Field rather than a map
// when iteration order matters, which it does for every workcell
// emitter the bash shim consumes today.
type Field struct {
	Key   string
	Value string
}

// WriteField emits a single KEY=VALUE line to w.  It returns an error
// if key or value contains a byte the bash parser cannot survive
// ('\n', '\r', '\0', or — for keys — '=' / empty), and propagates any
// write error from w unchanged.
func WriteField(w io.Writer, key, value string) error {
	if err := validateKey(key); err != nil {
		return err
	}
	if err := validateValue(value); err != nil {
		return err
	}
	_, err := fmt.Fprintf(w, "%s=%s\n", key, value)
	return err
}

// WriteFields emits multiple KEY=VALUE lines to w in the given order.
// Callers that build the field set from a map should sort the keys
// before constructing the slice if reproducible byte output matters
// (every workcell emitter that consumes WriteFields today builds the
// slice in a fixed source-code order, matching the bash original).
func WriteFields(w io.Writer, fields []Field) error {
	for _, f := range fields {
		if err := WriteField(w, f.Key, f.Value); err != nil {
			return err
		}
	}
	return nil
}

// validateKey enforces the key-side invariants.  An empty key cannot
// be recovered by bash's `key=${line%%=*}` pattern, and '=' / control
// characters would either split or terminate the line in unexpected
// places.
func validateKey(key string) error {
	if key == "" {
		return fmt.Errorf("shellproto: key must not be empty")
	}
	if strings.ContainsAny(key, "=\n\r\x00") {
		return fmt.Errorf("shellproto: key %q must not contain '=', newline, carriage-return, or NUL", key)
	}
	return nil
}

// validateValue enforces the value-side invariants.  '\n' would forge a
// second key=value record on the bash side; '\r' could spoof a
// partially overwritten line in a terminal capturing the plan; '\0'
// truncates bash's read.
func validateValue(value string) error {
	if strings.ContainsAny(value, "\n\r\x00") {
		return fmt.Errorf("shellproto: value %q must not contain newline, carriage-return, or NUL", value)
	}
	return nil
}
