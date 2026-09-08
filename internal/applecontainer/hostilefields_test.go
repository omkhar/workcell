// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package applecontainer

import (
	"strings"
	"testing"

	"github.com/omkhar/workcell/internal/testkit"
)

// auditFieldSeparators lists the bytes that end one field of an audit line.
// An equals sign is not one of them: the reader splits a field at its first
// equals sign, so a later one stays inside the value.
const auditFieldSeparators = " \t\n\r\x00"

func encodeAuditPathField(value string) (string, error) {
	return encodeAuditPathValue(value), nil
}

func decodeAuditPathField(value string) (string, error) {
	return decodeAuditPathValue(value), nil
}

// The audit path encoder claims that it recovers the exact original byte
// string for any input. The shared corpus is the assertion of that claim.
func TestAuditPathValueRoundTripsHostileFields(t *testing.T) {
	t.Parallel()
	testkit.RequireRoundTripOrReject(t, encodeAuditPathField, decodeAuditPathField, testkit.HostileFields())
}

// A round trip alone does not prove the encoded form is safe to interpolate:
// an encoder that returns its input unchanged round-trips perfectly and still
// lets a newline forge an audit record.
func TestAuditPathValueEncodingIsInert(t *testing.T) {
	t.Parallel()
	testkit.RequireEncodedFieldIsInert(t, encodeAuditPathField, auditFieldSeparators, testkit.HostileFields())
}

// FuzzAuditPathValueRoundTrip carries the two audit-field invariants past the
// corpus: the encoded form holds no field separator, and the decoder recovers
// the exact input bytes. The corpus seeds it, so every reviewed defect is the
// starting point of the mutation rather than the whole of the coverage.
func FuzzAuditPathValueRoundTrip(f *testing.F) {
	for _, row := range testkit.HostileFields() {
		f.Add(row.Value)
	}
	f.Fuzz(func(t *testing.T, value string) {
		encoded := encodeAuditPathValue(value)
		if index := strings.IndexAny(encoded, auditFieldSeparators); index >= 0 {
			t.Fatalf("encoded form of %q keeps the separator %q at offset %d", value, string(encoded[index]), index)
		}
		if decoded := decodeAuditPathValue(encoded); decoded != value {
			t.Fatalf("round trip changed the value: encoded %q, decoded %q, want %q", encoded, decoded, value)
		}
	})
}
