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

func TestAuditLineClaimsSessionMatchesRealToken(t *testing.T) {
	if !AuditLineClaimsSession("timestamp=t session_id=sess-A event=exit record_digest=d", "colima", "sess-A") {
		t.Fatal("a genuine session_id token must claim the session")
	}
	if AuditLineClaimsSession("timestamp=t session_id=sess-A event=exit record_digest=d", "colima", "sess-B") {
		t.Fatal("must not claim a different session")
	}
}

func TestAuditLineClaimsSessionIgnoresArgvSpoof(t *testing.T) {
	// A session-B record whose bash-%q argv splits to a fake session_id=sess-A
	// token under a raw scan must NOT claim sess-A: the tokenizer rejoins argv.
	spoof := `timestamp=t session_id=sess-B event=command argv=run\ session_id=sess-A\ now record_digest=d`
	if AuditLineClaimsSession(spoof, "colima", "sess-A") {
		t.Fatal("an argv substring must not forge a session claim")
	}
	if !AuditLineClaimsSession(spoof, "colima", "sess-B") {
		t.Fatal("the record's true session_id token must claim")
	}
}

func TestAuditLineClaimsSessionToleratesBareToken(t *testing.T) {
	// A malformed line (bare token) that carries a genuine session_id token still
	// structurally claims the session, so callers can treat it as tamper.
	if !AuditLineClaimsSession("timestamp=t session_id=sess-A event=exit FORGED", "colima", "sess-A") {
		t.Fatal("a bare-token line with a real session_id token must still claim")
	}
}

func TestDecodeAuditLineStrictRejectsBareTokenPercentProvider(t *testing.T) {
	// The AppleContainer percent-path provider tokenizes on whitespace; a bare
	// token must still be rejected under that encoding.
	if _, err := DecodeAuditLineStrict("timestamp=t session_id=s FORGED record_digest=d", "apple-container"); err == nil {
		t.Fatal("bare token must be rejected under percent-path encoding")
	}
}
