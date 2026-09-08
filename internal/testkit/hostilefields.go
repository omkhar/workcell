// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package testkit

import (
	"strings"
	"testing"
)

// HostileField is one untrusted value that a structured record has to carry
// without losing it and without letting it forge a second record.
//
// Why names the reviewed defect the row reproduces, so a row cannot be
// dropped without stating which finding stops being covered.
type HostileField struct {
	Name  string
	Value string
	Why   string
}

// HostileFields returns the shared corpus of untrusted field values.
//
// Every row is a defect the reviewer found in a Workcell encoder or decoder
// before pull request 600: a newline that forges a record, a separator inside
// a value, a quoted or escaped value, an octal or ANSI-C escape, a non-UTF-8
// path byte, a value that is the prefix of a sentinel, and an unmapped key.
// The corpus is exported so that every package with a structured record
// format reads the same table instead of inventing its own decoy string.
func HostileFields() []HostileField {
	return []HostileField{
		{"embedded newline", "a\nb", "a newline forges a second record on a line-delimited format"},
		{"embedded carriage return", "a\rb", "a carriage return spoofs an overwritten line in a captured terminal"},
		{"embedded NUL", "a\x00b", "a NUL truncates the bash read loop that consumes the record"},
		{"trailing newline", "a\n", "a trailing newline appends an empty record"},
		{"separator inside the value", "path=/tmp/x", "an equals sign inside a value splits one field into two"},
		{"event sentinel inside the value", "/tmp/x/event=session_started", "a value that spells a sentinel satisfies an event assertion (audit_test.go)"},
		{"sentinel prefix", "complete_partial", "a value that is the prefix of a sentinel satisfies an exact-match rule"},
		{"space inside the value", "/tmp/a b/c", "a space injects a stray token into a whitespace-delimited record"},
		{"tab inside the value", "/tmp/a\tb", "a tab is field-separating whitespace for the reader"},
		{"comma inside the value", "/tmp/a,b", "a comma splits a mount record"},
		{"double-quoted value", `"quoted"`, "a quoted value is unquoted once too often by a naive reader"},
		{"single-quoted value", `'quoted'`, "the single-quoted form of the same defect"},
		{"escaped double quote", `a\"b`, "a backslash-escaped quote ends the value early"},
		{"literal backslash", `C:\path\to`, "a literal backslash in a legal path is misread as an escape"},
		{"octal escape", `a\0101b`, "an octal escape is expanded by a reader that unescapes"},
		{"ANSI-C escape", `a$'\cX'b`, "an ANSI-C quoted escape is expanded by the shell reader"},
		{"percent sign", "100%done", "a percent sign is the encoder's own escape byte"},
		{"percent escape sequence", "%41%42", "an already-encoded value must not decode twice"},
		{"non-UTF-8 byte", "/tmp/\xff\xfe", "a non-UTF-8 path byte collapses to U+FFFD in a rune loop"},
		{"high byte and DEL", "/tmp/\x7f\x80", "DEL and the high bytes are not printable ASCII"},
		{"leading dash", "--flag", "a value that spells an option is read as one"},
		{"empty value", "", "an empty value must survive as an empty value, not as an absent field"},
		{"only whitespace", "   ", "a whitespace-only value collapses when the reader trims"},
		{"unmapped key name", "workspace_repo_mcp", "an unmapped key must be hard-redacted, not passed through"},
		{"long value", strings.Repeat("a", 4096), "a long value must not truncate silently"},
	}
}

// RequireRoundTripOrReject requires every corpus row to survive an encode and
// decode pair byte for byte, or to be rejected by the encoder.
//
// Silent mangling is the defect this driver exists for: an encoder that drops
// a byte, expands an escape, or truncates at a separator returns a value that
// reads as clean while the evidence it carried is gone. Rejection is an
// acceptable answer, and so is an exact round trip. Nothing else is.
func RequireRoundTripOrReject(t *testing.T, encode func(string) (string, error), decode func(string) (string, error), corpus []HostileField) {
	t.Helper()

	if len(corpus) == 0 {
		t.Fatal("hostile-field corpus is empty; refusing a vacuous pass")
	}
	for _, row := range corpus {
		t.Run(row.Name, func(t *testing.T) {
			encoded, err := encode(row.Value)
			if err != nil {
				return // rejection is a correct answer
			}
			decoded, err := decode(encoded)
			if err != nil {
				t.Fatalf("the encoder accepted %q but the decoder rejected %q: %v (%s)", row.Value, encoded, err, row.Why)
			}
			if decoded != row.Value {
				t.Fatalf("round trip changed the value: encoded %q, decoded %q, want %q (%s)",
					encoded, decoded, row.Value, row.Why)
			}
		})
	}
}

// RequireEncodedFieldIsInert requires the encoded form of every corpus row to
// carry none of the bytes that separate one record or one field from the next.
//
// A round trip alone does not prove this: an encoder that emits the value
// unchanged round-trips perfectly and still lets a newline forge a record.
func RequireEncodedFieldIsInert(t *testing.T, encode func(string) (string, error), separators string, corpus []HostileField) {
	t.Helper()

	if separators == "" {
		t.Fatal("no separator was named; refusing a vacuous pass")
	}
	for _, row := range corpus {
		t.Run(row.Name, func(t *testing.T) {
			encoded, err := encode(row.Value)
			if err != nil {
				return // rejection is a correct answer
			}
			if index := strings.IndexAny(encoded, separators); index >= 0 {
				t.Fatalf("the encoded form of %q keeps the separator %q at offset %d (%s)",
					row.Value, string(encoded[index]), index, row.Why)
			}
		})
	}
}
