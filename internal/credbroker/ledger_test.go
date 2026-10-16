// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package credbroker

import (
	"strings"
	"testing"
)

// TestLedgerAuditRecordsReadsWhatTheBrokerWrites folds a ledger that a real
// server wrote: one grant and one denial of each kind it can record.
func TestLedgerAuditRecordsReadsWhatTheBrokerWrites(t *testing.T) {
	log := &syncBuffer{}
	s, err := newServer(Config{Token: []byte(testToken), Log: log, Credentials: map[string]string{"claude_api_key": "sk-a"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range []Request{
		{Token: testToken, Host: "api.anthropic.com", Header: "x-api-key"},
		{Token: "bad", Host: "api.anthropic.com", Header: "x-api-key"},
		{Token: testToken, Host: "evil.example", Header: "x-api-key"},
	} {
		response, key := s.answer(request)
		result := response.Error
		if result == "" {
			result = resultGranted
		}
		if err := s.record(request, key, result); err != nil {
			t.Fatal(err)
		}
	}
	for _, result := range []string{resultPeerRejected, resultMalformed, ErrRateLimited} {
		if err := s.record(Request{}, "", result); err != nil {
			t.Fatal(err)
		}
	}
	records, err := LedgerAuditRecords(strings.NewReader(log.buf.String()))
	want := "credential_broker_summary status=complete granted=1 denied=5 malformed_lines=0"
	if err != nil || len(records) != 1 || records[0] != want {
		t.Fatalf("LedgerAuditRecords = %q, %v; want [%q]", records, err, want)
	}
}

// TestLedgerAuditRecordsCountsEveryOtherLineAsMalformed treats the ledger as
// untrusted: each line below is one malformed line, never a grant or a denial.
func TestLedgerAuditRecordsCountsEveryOtherLineAsMalformed(t *testing.T) {
	const ts = `"time":"2026-10-16T00:00:00Z"`
	lines := []string{
		``,
		`not json`,
		`{` + ts + `,"result":"granted","credential":"claude_api_key","host":"evil.example","header":"x-api-key"}`,
		`{` + ts + `,"result":"granted","credential":"claude_api_key","host":"api.anthropic.com","header":"x-goog-api-key"}`,
		`{` + ts + `,"result":"granted","credential":"other","host":"api.anthropic.com","header":"x-api-key"}`,
		`{` + ts + `,"result":"granted"}`,
		`{` + ts + `,"result":"not_granted","credential":"claude_api_key"}`,
		`{` + ts + `,"result":"log_failed"}`,
		`{` + ts + `,"result":"unknown"}`,
		`{"time":"yesterday","result":"bad_token"}`,
		`{"result":"bad_token"}`,
		`{` + ts + `,"result":"bad_token","value":"sk-a"}`,
		`{` + ts + `,"result":"bad_token"} {}`,
		`{` + ts + `,"result":"bad_token"}x`,
	}
	good := `{` + ts + `,"host":"api.anthropic.com","header":"authorization","credential":"claude_api_key","result":"granted"}`
	records, err := LedgerAuditRecords(strings.NewReader(strings.Join(append(lines, good), "\n") + "\n"))
	want := "credential_broker_summary status=malformed granted=1 denied=0 malformed_lines=14"
	if err != nil || len(records) != 1 || records[0] != want {
		t.Fatalf("LedgerAuditRecords = %q, %v; want [%q]", records, err, want)
	}
}

// TestLedgerAuditRecordsRefusesALineTheBrokerCannotWrite fails the read on a
// 32 KiB line instead of folding a partial line, and still folds the longest
// line the broker can write: a request at the size limit whose host is all
// control characters, which JSON escapes to 6 bytes each.
func TestLedgerAuditRecordsRefusesALineTheBrokerCannotWrite(t *testing.T) {
	line := `{"time":"2026-10-16T00:00:00Z","result":"bad_token","host":"` + strings.Repeat("a", 32<<10) + `"}`
	if records, err := LedgerAuditRecords(strings.NewReader(line + "\n")); err == nil {
		t.Fatalf("LedgerAuditRecords = %q, want an error for a line over the cap", records)
	}
	log := &syncBuffer{}
	s, err := newServer(Config{Token: []byte(testToken), Log: log, Credentials: map[string]string{"claude_api_key": "sk-a"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.record(Request{Host: strings.Repeat("\x01", maxLineBytes-64)}, "", ErrBadToken); err != nil {
		t.Fatal(err)
	}
	records, err := LedgerAuditRecords(strings.NewReader(log.buf.String()))
	if err != nil || len(records) != 1 || records[0] != "credential_broker_summary status=complete granted=0 denied=1 malformed_lines=0" {
		t.Fatalf("LedgerAuditRecords of a %d-byte line = %q, %v", log.buf.Len(), records, err)
	}
}

// TestLedgerAuditRecordsOfAnEmptyLedger is a session that sent no request.
func TestLedgerAuditRecordsOfAnEmptyLedger(t *testing.T) {
	records, err := LedgerAuditRecords(strings.NewReader(""))
	if err != nil || len(records) != 1 || records[0] != "credential_broker_summary status=complete granted=0 denied=0 malformed_lines=0" {
		t.Fatalf("LedgerAuditRecords = %q, %v", records, err)
	}
}

// TestStartAuditRecordNamesTheKeychainItem pins the start record of each
// brokered key.
func TestStartAuditRecordNamesTheKeychainItem(t *testing.T) {
	for key, want := range map[string]string{
		"claude_api_key": "credential_broker_start key=claude_api_key item=workcell-claude-api-key",
		"gemini_env":     "credential_broker_start key=gemini_env item=workcell-gemini-api-key",
	} {
		if got := StartAuditRecord(key); got != want {
			t.Errorf("StartAuditRecord(%q) = %q, want %q", key, got, want)
		}
	}
}
