// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package ocsf

import (
	"os/exec"
	"strings"
	"testing"
)

// bashQuoteToken encodes s exactly as scripts/workcell does — via the real
// `bash printf %q` that writes the audit records — so the round-trip test is
// validated against the authoritative encoder, not a reimplementation.
func bashQuoteToken(t *testing.T, s string) (string, bool) {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		return "", false
	}
	out, err := exec.Command(bash, "-c", `printf '%q' "$1"`, "_", s).Output()
	if err != nil {
		t.Fatalf("bash printf %%q failed for %q: %v", s, err)
	}
	return string(out), true
}

// TestDecodeAuditLineRoundTripBash proves decodeAuditLine is the exact inverse
// of the real `bash printf %q` audit encoder for values containing spaces, tabs,
// and shell metacharacters — the cases a naive strings.Fields split corrupts.
func TestDecodeAuditLineRoundTripBash(t *testing.T) {
	cases := map[string]string{
		"event":       "launch",
		"session_id":  "sess-1",
		"endpoints":   "api.anthropic.com:443 registry-1.docker.io:443 github.com:443",
		"argv":        "run the tests",
		"note":        "a\tb\tc",
		"weird":       `quote'and"dollar$brace{}`,
		"empty_after": "",
	}

	var tokens []string
	for _, k := range []string{"event", "session_id", "endpoints", "argv", "note", "weird", "empty_after"} {
		q, ok := bashQuoteToken(t, k+"="+cases[k])
		if !ok {
			t.Skip("bash not available for authoritative round-trip")
		}
		tokens = append(tokens, q)
	}
	line := strings.Join(tokens, " ")

	fields, err := decodeAuditLine(line)
	if err != nil {
		t.Fatalf("decodeAuditLine: %v\nline=%q", err, line)
	}
	got := make(map[string]string, len(fields))
	for _, f := range fields {
		got[f.key] = f.value
	}
	for k, want := range cases {
		if got[k] != want {
			t.Errorf("field %q: decoded %q, want %q (line=%q)", k, got[k], want, line)
		}
	}
}

// TestDecodeAuditLineSpacedValueDeterministic pins the concrete escaped form of
// a space-delimited endpoints allowlist (independent of a local bash), proving
// the value is not truncated at the first space.
func TestDecodeAuditLineSpacedValueDeterministic(t *testing.T) {
	line := `event=launch endpoints=a:443\ b:443\ c:443 exit_status=0`
	fields, err := decodeAuditLine(line)
	if err != nil {
		t.Fatalf("decodeAuditLine: %v", err)
	}
	got := make(map[string]string, len(fields))
	for _, f := range fields {
		got[f.key] = f.value
	}
	if got["endpoints"] != "a:443 b:443 c:443" {
		t.Fatalf("spaced endpoints truncated: got %q want %q", got["endpoints"], "a:443 b:443 c:443")
	}
	if got["exit_status"] != "0" {
		t.Fatalf("trailing field after a spaced value was lost: got %q", got["exit_status"])
	}
}

// TestDecodeAuditLineRejectsDuplicateKey is the C1 dup-key defense: a tampered
// record with a repeated key is rejected fail-closed, not silently first/last.
func TestDecodeAuditLineRejectsDuplicateKey(t *testing.T) {
	for _, line := range []string{
		"session_id=A session_id=B event=launch",
		"event=launch event=exit session_id=A",
		"a=1 b=2 a=3",
	} {
		if _, err := decodeAuditLine(line); err == nil {
			t.Errorf("expected duplicate-key rejection for %q, got nil error", line)
		}
	}
}

// TestDecodeAuditLineTolueratesTornToken keeps the existing readers' tolerance
// for a torn crash line: a token without '=' is skipped, not an error.
func TestDecodeAuditLineToleratesTornToken(t *testing.T) {
	fields, err := decodeAuditLine("event=launch torn_fragment_no_newline session_id=A")
	if err != nil {
		t.Fatalf("torn token should be tolerated, got %v", err)
	}
	got := make(map[string]string, len(fields))
	for _, f := range fields {
		got[f.key] = f.value
	}
	if got["event"] != "launch" || got["session_id"] != "A" {
		t.Fatalf("torn token disrupted parsing: %+v", got)
	}
}

// TestDecodeAnsiCNewline proves the ANSI-C $'...' form (how bash %q encodes a
// newline-bearing value) round-trips to a real newline.
func TestDecodeAnsiCNewline(t *testing.T) {
	fields, err := decodeAuditLine(`event=launch note=$'line1\nline2'`)
	if err != nil {
		t.Fatalf("decodeAuditLine: %v", err)
	}
	got := make(map[string]string, len(fields))
	for _, f := range fields {
		got[f.key] = f.value
	}
	if got["note"] != "line1\nline2" {
		t.Fatalf("ANSI-C newline not decoded: got %q", got["note"])
	}
}

// TestDecodeAnsiCUnterminated fails closed on a corrupt $'...' block.
func TestDecodeAnsiCUnterminated(t *testing.T) {
	if _, err := decodeAuditLine(`event=launch note=$'oops`); err == nil {
		t.Fatal("expected error on unterminated $'...' block")
	}
}
