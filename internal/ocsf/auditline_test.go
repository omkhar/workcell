// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package ocsf

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/omkhar/workcell/internal/applecontainer"
)

func fieldMap(fields []auditField) map[string]string {
	m := make(map[string]string, len(fields))
	for _, f := range fields {
		m[f.key] = f.value
	}
	return m
}

// TestAuditEncodingForProvider proves the decoder is selected from the session's
// writer: only the apple-container provider selects the percent decoder; every
// launcher backend (and an unknown/empty provider) uses the bash %q decoder.
func TestAuditEncodingForProvider(t *testing.T) {
	if got := auditEncodingForProvider(applecontainer.Provider); got != encodingPercentPath {
		t.Errorf("apple-container provider should select percent decoding, got %v", got)
	}
	for _, p := range []string{"colima", "docker-desktop", "aws-ec2-ssm", "gcp-vm", "", "unknown"} {
		if got := auditEncodingForProvider(p); got != encodingBashQuote {
			t.Errorf("provider %q should select bash %%q decoding, got %v", p, got)
		}
	}
}

// TestDecodeAuditLinePercentPreservesBackslash is the load-bearing regression:
// an AppleContainer record with a literal backslash in a POSIX path must survive
// intact under the percent decoder, and the OLD unconditional %q decoder is
// proven to CORRUPT it (dropping the backslash) — so per-writer selection is
// what preserves the evidence.
func TestDecodeAuditLinePercentPreservesBackslash(t *testing.T) {
	// The AppleContainer writer leaves a literal backslash unchanged
	// (encodeAuditPathValue only percent-encodes %, ctrl, space, high bytes).
	line := `ts=2026-07-05T11:00:00Z session_id=s event=workspace_materialized target_provider=apple-container workspace_origin=/tmp/src workspace=/tmp/a\b`

	fields, err := decodeAuditLine(line, encodingPercentPath)
	if err != nil {
		t.Fatalf("decodeAuditLine percent: %v", err)
	}
	if got := fieldMap(fields)["workspace"]; got != `/tmp/a\b` {
		t.Fatalf("percent decoder must preserve the backslash: got %q want %q", got, `/tmp/a\b`)
	}

	// Load-bearing: the bash %q decoder corrupts the same value (drops the \).
	corrupt, err := decodeAuditLine(line, encodingBashQuote)
	if err != nil {
		t.Fatalf("decodeAuditLine bashquote: %v", err)
	}
	if got := fieldMap(corrupt)["workspace"]; got != "/tmp/ab" {
		t.Fatalf("expected the %%q decoder to corrupt the backslash path to /tmp/ab (proving selection matters), got %q", got)
	}
}

// TestDecodeAuditLinePercentDecodesSpace proves a space in an AppleContainer path
// (encoded as %20) round-trips back to a real space through the canonical
// applecontainer decoder.
func TestDecodeAuditLinePercentDecodesSpace(t *testing.T) {
	line := `event=workspace_materialized target_provider=apple-container workspace=/tmp/a%20b`
	fields, err := decodeAuditLine(line, encodingPercentPath)
	if err != nil {
		t.Fatalf("decodeAuditLine percent: %v", err)
	}
	if got := fieldMap(fields)["workspace"]; got != "/tmp/a b" {
		t.Fatalf("percent %%20 not decoded to space: got %q", got)
	}
}

// TestDecodeAuditLinePercentRejectsDuplicateKey proves dup-key rejection is kept
// for the percent writer too.
func TestDecodeAuditLinePercentRejectsDuplicateKey(t *testing.T) {
	line := `event=workspace_materialized session_id=A session_id=B workspace=/tmp/x`
	if _, err := decodeAuditLine(line, encodingPercentPath); err == nil {
		t.Fatal("expected duplicate-key rejection under percent decoding")
	}
}

// TestDecodeAuditLinePercentLeavesRawFieldsUntouched proves only the encoded
// path fields are percent-decoded; a raw field carrying a literal % is not
// corrupted.
func TestDecodeAuditLinePercentLeavesRawFieldsUntouched(t *testing.T) {
	line := `event=workspace_materialized target_provider=apple-container image_ref=repo/name%2Ffoo workspace=/tmp/x`
	fields, err := decodeAuditLine(line, encodingPercentPath)
	if err != nil {
		t.Fatalf("decodeAuditLine percent: %v", err)
	}
	if got := fieldMap(fields)["image_ref"]; got != "repo/name%2Ffoo" {
		t.Fatalf("raw non-path field must not be percent-decoded: got %q", got)
	}
}

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

	fields, err := decodeAuditLine(line, encodingBashQuote)
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

// TestDecodeAnsiCOctalBytes proves octal escapes decode as raw BYTES: a UTF-8
// multibyte value that bash %q under LC_ALL=C emits as a per-byte octal sequence
// (é → \303\251, 😀 → \360\237\230\200) must reassemble to the original string,
// not one rune per byte.
func TestDecodeAnsiCOctalBytes(t *testing.T) {
	cases := map[string]string{
		`$'\303\251'`:         "é",
		`$'caf\303\251'`:      "café",
		`$'\360\237\230\200'`: "😀",
		`$'\342\200\223'`:     "–", // en dash
	}
	for enc, want := range cases {
		fields, err := decodeAuditLine("k="+enc, encodingBashQuote)
		if err != nil {
			t.Fatalf("decodeAuditLine(%q): %v", enc, err)
		}
		if len(fields) != 1 || fields[0].value != want {
			t.Errorf("decode %q: got %+v, want value %q", enc, fields, want)
		}
	}
}

// TestDecodeAuditLineRoundTripBashUTF8LocaleC proves a non-ASCII value round-trips
// through the real `bash printf %q` run under LC_ALL=C — the session-start locale
// (scripts/workcell) under which bash emits octal per-byte escapes.
func TestDecodeAuditLineRoundTripBashUTF8LocaleC(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available for authoritative round-trip")
	}
	const value = "workspace=/home/dev/café/naïve-😀"
	cmd := exec.Command(bash, "-c", `printf '%q' "$1"`, "_", value)
	cmd.Env = append(cmd.Environ(), "LC_ALL=C", "LANG=C")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("bash printf %%q under LC_ALL=C: %v", err)
	}
	fields, err := decodeAuditLine("field="+string(out), encodingBashQuote)
	if err != nil {
		t.Fatalf("decodeAuditLine: %v\ntoken=%q", err, out)
	}
	if len(fields) != 1 || fields[0].value != value {
		t.Errorf("LC_ALL=C round-trip: decoded %+v, want value %q (token=%q)", fields, value, out)
	}
}

// TestDecodeAnsiCNamedControlEscapesRoundTripBash proves every ANSI-C named
// control escape that bash `%q` emits (\a \b \e \f \n \r \t \v) round-trips to
// its real byte, not the literal letter. bash %q renders these control bytes in
// the $'...' named form, so without the named-escape cases BEL would decode to
// "a" and silently corrupt the exported evidence.
func TestDecodeAnsiCNamedControlEscapesRoundTripBash(t *testing.T) {
	// One value per named control byte bash %q emits in named form.
	value := "x\ay\bz\x1bp\fq\nr\rs\tt\vu"
	q, ok := bashQuoteToken(t, "note="+value)
	if !ok {
		t.Skip("bash not available for authoritative round-trip")
	}
	fields, err := decodeAuditLine(q, encodingBashQuote)
	if err != nil {
		t.Fatalf("decodeAuditLine: %v\ntoken=%q", err, q)
	}
	if len(fields) != 1 || fields[0].key != "note" {
		t.Fatalf("decoded %+v, want one note field (token=%q)", fields, q)
	}
	if fields[0].value != value {
		t.Errorf("control-byte value decoded %q, want %q (token=%q)", fields[0].value, value, q)
	}
}

// TestDecodeAnsiCNamedControlEscapes decodes each named control escape directly
// (independent of a local bash) so the mapping is pinned even where bash is
// absent.
func TestDecodeAnsiCNamedControlEscapes(t *testing.T) {
	cases := map[string]string{
		`$'\a'`: "\a",
		`$'\b'`: "\b",
		`$'\e'`: "\x1b",
		`$'\E'`: "\x1b",
		`$'\f'`: "\f",
		`$'\n'`: "\n",
		`$'\r'`: "\r",
		`$'\t'`: "\t",
		`$'\v'`: "\v",
	}
	for enc, want := range cases {
		fields, err := decodeAuditLine("k="+enc, encodingBashQuote)
		if err != nil {
			t.Fatalf("decodeAuditLine(%q): %v", enc, err)
		}
		if len(fields) != 1 || fields[0].value != want {
			t.Errorf("decode %q: got %+v, want value %q", enc, fields, want)
		}
	}
}

// TestDecodeAuditLineSpacedValueDeterministic pins the concrete escaped form of
// a space-delimited endpoints allowlist (independent of a local bash), proving
// the value is not truncated at the first space.
func TestDecodeAuditLineSpacedValueDeterministic(t *testing.T) {
	line := `event=launch endpoints=a:443\ b:443\ c:443 exit_status=0`
	fields, err := decodeAuditLine(line, encodingBashQuote)
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
		if _, err := decodeAuditLine(line, encodingBashQuote); err == nil {
			t.Errorf("expected duplicate-key rejection for %q, got nil error", line)
		}
	}
}

// TestDecodeAuditLineTolueratesTornToken keeps the existing readers' tolerance
// for a torn crash line: a token without '=' is skipped, not an error.
func TestDecodeAuditLineToleratesTornToken(t *testing.T) {
	fields, err := decodeAuditLine("event=launch torn_fragment_no_newline session_id=A", encodingBashQuote)
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
	fields, err := decodeAuditLine(`event=launch note=$'line1\nline2'`, encodingBashQuote)
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
	if _, err := decodeAuditLine(`event=launch note=$'oops`, encodingBashQuote); err == nil {
		t.Fatal("expected error on unterminated $'...' block")
	}
}
