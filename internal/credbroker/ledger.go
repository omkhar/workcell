// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package credbroker

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

// maxLedgerLineBytes is above the longest line the broker can write: a
// request is at most maxLineBytes, and JSON escaping grows a byte to at most 6.
const maxLedgerLineBytes = 8 * maxLineBytes

// maxLedgerRecords bounds the summary read. The broker's request budgets allow
// about 864,000 lines in the 24-hour session CA lifetime; past this limit the
// summary stops and reports status=truncated.
var maxLedgerRecords uint64 = 1_000_000

// deniedResults are the results the broker records for a request it refused.
var deniedResults = map[string]bool{
	ErrBadToken: true, ErrRateLimited: true, ErrNotGranted: true, resultPeerRejected: true, resultMalformed: true,
}

// StartAuditRecord is the session audit record for a key the broker serves.
func StartAuditRecord(key string) string {
	return fmt.Sprintf("credential_broker_start key=%s item=%s", key, KeychainItem(key))
}

// LedgerAuditRecords folds a broker request log into one
// "credential_broker_summary" audit record. The log is read back after the
// session as untrusted input: a line that is not one strict ledger object, a
// grant outside the grant table or a denial that names a credential counts in
// malformed_lines, never in silence. A read error, such as a line longer than
// the broker can write, fails the whole read.
func LedgerAuditRecords(log io.Reader) ([]string, error) {
	var granted, denied, malformed, lines uint64
	truncated := false
	scanner := bufio.NewScanner(log)
	scanner.Buffer(make([]byte, 0, maxLineBytes), maxLedgerLineBytes)
	for scanner.Scan() {
		if lines == maxLedgerRecords {
			truncated = true
			break
		}
		lines++
		record, ok := parseLedgerLine(scanner.Text())
		switch {
		case !ok:
			malformed++
		case record.Result == resultGranted:
			granted++
		default:
			denied++
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read credential broker ledger: %w", err)
	}
	status := "complete"
	switch {
	case truncated:
		status = "truncated"
	case malformed > 0:
		status = "malformed"
	}
	return []string{fmt.Sprintf("credential_broker_summary status=%s granted=%d denied=%d malformed_lines=%d", status, granted, denied, malformed)}, nil
}

// parseLedgerLine accepts one logRecord with a valid time and nothing after
// it: a grant of a (host, header) pair its credential holds, or a denial with
// a known result and no credential.
func parseLedgerLine(line string) (logRecord, bool) {
	var record logRecord
	if !uniqueObjectKeys(line) {
		return record, false
	}
	dec := json.NewDecoder(strings.NewReader(line))
	dec.DisallowUnknownFields()
	if dec.Decode(&record) != nil || dec.InputOffset() != int64(len(line)) {
		return record, false
	}
	if _, err := time.Parse(time.RFC3339Nano, record.Time); err != nil {
		return record, false
	}
	if record.Result == resultGranted {
		for _, g := range grants[record.Credential] {
			if g == (grant{host: record.Host, header: record.Header}) {
				return record, true
			}
		}
		return record, false
	}
	return record, deniedResults[record.Result] && record.Credential == ""
}

// uniqueObjectKeys reports whether line is one JSON object whose keys are all
// distinct. encoding/json keeps the last of a repeated key, so a line the
// broker never writes could otherwise pass with a second "credential".
func uniqueObjectKeys(line string) bool {
	dec := json.NewDecoder(strings.NewReader(line))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return false
	}
	seen := map[string]bool{}
	for dec.More() {
		tok, err := dec.Token()
		key, ok := tok.(string)
		if err != nil || !ok || seen[key] {
			return false
		}
		seen[key] = true
		var value json.RawMessage
		if dec.Decode(&value) != nil {
			return false
		}
	}
	return true
}
