// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

// Package bashquote owns the Go form of the bash `printf %q` encoding that
// scripts/workcell writes.
package bashquote

import (
	"fmt"
	"strings"
)

// Quote replicates `printf %q` as bash 3.2 (the macOS system bash
// scripts/workcell runs under) emits it: always backslash-escape unsafe
// characters; never wrap the whole token in single quotes when only
// printable characters are present; use ANSI-C `$'...'` only for non-
// printable bytes. The empty string becomes a pair of bare ASCII
// single-quote characters (returned by the `if s == ""` branch
// below). The dry-run scenario in
// tests/scenarios/shared/test-publish-pr-dry-run.sh greps for the
// exact backslash form, so any divergence surfaces there.
func Quote(s string) string {
	if s == "" {
		return "''"
	}
	if hasUnprintable(s) {
		var b strings.Builder
		b.WriteString("$'")
		for _, r := range s {
			switch r {
			case '\n':
				b.WriteString(`\n`)
			case '\t':
				b.WriteString(`\t`)
			case '\r':
				b.WriteString(`\r`)
			case '\\':
				b.WriteString(`\\`)
			case '\'':
				b.WriteString(`\'`)
			default:
				if r < 0x20 || r == 0x7f {
					fmt.Fprintf(&b, `\%03o`, r)
				} else {
					b.WriteRune(r)
				}
			}
		}
		b.WriteString("'")
		return b.String()
	}
	if isShellSafe(s) {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		if isShellSafeRune(r) {
			b.WriteRune(r)
		} else {
			b.WriteByte('\\')
			b.WriteRune(r)
		}
	}
	return b.String()
}

func isShellSafe(s string) bool {
	for _, r := range s {
		if !isShellSafeRune(r) {
			return false
		}
	}
	return true
}

func isShellSafeRune(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	}
	switch r {
	case '%', '+', ',', '-', '.', '/', ':', '=', '@', '_':
		return true
	}
	return false
}

func hasUnprintable(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}
