// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package ocsf

import "testing"

func TestDecodeAuditLineStrictAcceptsGenuineRecord(t *testing.T) {
	fields, err := DecodeAuditLineStrict("timestamp=t session_id=s event=exit record_digest=d", "colima")
	if err != nil {
		t.Fatalf("genuine record must decode: %v", err)
	}
	if len(fields) != 4 || fields[1].Key != "session_id" || fields[1].Value != "s" {
		t.Fatalf("unexpected fields: %+v", fields)
	}
}

func TestDecodeAuditLineStrictRejectsBareToken(t *testing.T) {
	if _, err := DecodeAuditLineStrict("timestamp=t session_id=s FORGED record_digest=d", "colima"); err == nil {
		t.Fatal("bare token must be rejected")
	}
}

func TestDecodeAuditLineStrictRejectsDuplicateKey(t *testing.T) {
	if _, err := DecodeAuditLineStrict("timestamp=t session_id=a session_id=b record_digest=d", "colima"); err == nil {
		t.Fatal("duplicate key must be rejected")
	}
}

func TestDecodeAuditLineStrictRejectsBareTokenPercentProvider(t *testing.T) {
	// The AppleContainer percent-path provider tokenizes on whitespace; a bare
	// token must still be rejected under that encoding.
	if _, err := DecodeAuditLineStrict("timestamp=t session_id=s FORGED record_digest=d", "apple-container"); err == nil {
		t.Fatal("bare token must be rejected under percent-path encoding")
	}
}
