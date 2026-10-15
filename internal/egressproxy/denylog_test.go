// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package egressproxy

import (
	"bytes"
	"fmt"
	"slices"
	"strings"
	"testing"
)

func summary(status string, keys int, total uint64, malformed int) string {
	return fmt.Sprintf("egress_summary status=%s deny_keys=%d deny_total=%d malformed_lines=%d", status, keys, total, malformed)
}

// TestDenyAuditRecordsReadsProxyOutput feeds the proxy's own deny lines back,
// so the writer and the reader share one line format.
func TestDenyAuditRecordsReadsProxyOutput(t *testing.T) {
	var log bytes.Buffer
	p := New(&Allowlist{}, &log)
	p.deny("a.example", 443, "sni_not_allowed")
	p.deny("a.example", 443, "sni_not_allowed")
	p.deny("", 22, "port_not_allowed")
	got := DenyAuditRecords(log.Bytes())
	want := []string{
		"egress_deny host=a.example port=443 reason=sni_not_allowed count=2",
		"egress_deny host= port=22 reason=port_not_allowed count=1",
		summary("complete", 2, 3, 0),
	}
	if !slices.Equal(got, want) {
		t.Fatalf("records = %q, want %q", got, want)
	}
}

// TestDenyAuditRecordsRejectsMalformedLines counts each hostile line as
// malformed and keeps the valid line beside it.
func TestDenyAuditRecordsRejectsMalformedLines(t *testing.T) {
	valid := `{"host":"a.example","port":443,"reason":"no_sni","count":1}`
	for name, line := range map[string]string{
		"not json":         `deny a.example`,
		"unknown field":    `{"host":"a","port":443,"reason":"no_sni","count":1,"extra":1}`,
		"trailing object":  valid + `{}`,
		"trailing space":   valid + ` `,
		"blank line":       ``,
		"null":             `null`,
		"array":            `[1]`,
		"port zero":        `{"host":"a","port":0,"reason":"no_sni","count":1}`,
		"missing port":     `{"host":"a","reason":"no_sni","count":1}`,
		"port too large":   `{"host":"a","port":70000,"reason":"no_sni","count":1}`,
		"port as string":   `{"host":"a","port":"443","reason":"no_sni","count":1}`,
		"negative count":   `{"host":"a","port":443,"reason":"no_sni","count":-1}`,
		"count zero":       `{"host":"a","port":443,"reason":"no_sni","count":0}`,
		"fraction count":   `{"host":"a","port":443,"reason":"no_sni","count":1.5}`,
		"host not string":  `{"host":7,"port":443,"reason":"no_sni","count":1}`,
		"host too long":    `{"host":"` + strings.Repeat("a", maxLogHost+1) + `","port":443,"reason":"no_sni","count":1}`,
		"reason space":     `{"host":"a","port":443,"reason":"no sni","count":1}`,
		"reason control":   `{"host":"a","port":443,"reason":"no_sni\u0007","count":1}`,
		"reason newline":   `{"host":"a","port":443,"reason":"no_sni\nevent=exit","count":1}`,
		"reason uppercase": `{"host":"a","port":443,"reason":"NO_SNI","count":1}`,
		"reason empty":     `{"host":"a","port":443,"reason":"","count":1}`,
		"line too long":    `{"host":"a","port":443,"reason":"no_sni","count":1` + strings.Repeat(" ", maxDenyLineBytes) + `}`,
	} {
		t.Run(name, func(t *testing.T) {
			got := DenyAuditRecords([]byte(line + "\n" + valid + "\n"))
			want := []string{"egress_deny host=a.example port=443 reason=no_sni count=1", summary("malformed", 1, 1, 1)}
			if !slices.Equal(got, want) {
				t.Fatalf("records = %q, want %q", got, want)
			}
		})
	}
}

// TestDenyAuditRecordsRewritesHostileHosts keeps the denial of a host outside
// the DNS name characters but never writes its bytes into the audit record.
func TestDenyAuditRecordsRewritesHostileHosts(t *testing.T) {
	for _, host := range []string{`a b`, `a\u001b[31m`, `a\nevent=exit`, `café`, `Kelvin`, `a=b`, `$(id)`} {
		line := `{"host":"` + host + `","port":443,"reason":"sni_not_allowed","count":4}`
		got := DenyAuditRecords([]byte(line))
		want := []string{"egress_deny host=(invalid) port=443 reason=sni_not_allowed count=4", summary("complete", 1, 4, 0)}
		if !slices.Equal(got, want) {
			t.Fatalf("host %q: records = %q, want %q", host, got, want)
		}
	}
	got := DenyAuditRecords([]byte(`{"host":"(overflow)","port":443,"reason":"sni_not_allowed","count":1}`))
	if got[0] != "egress_deny host=(overflow) port=443 reason=sni_not_allowed count=1" {
		t.Fatalf("overflow host = %q", got[0])
	}
}

// TestDenyAuditRecordsBounds caps the egress_deny records, keeps the largest
// counts, refuses more keys than the proxy can write and saturates the total.
func TestDenyAuditRecordsBounds(t *testing.T) {
	var log strings.Builder
	for i := range maxAuditDenyKeys + 8 {
		for c := range i + 1 {
			fmt.Fprintf(&log, `{"host":"h%d.example","port":443,"reason":"no_sni","count":%d}`+"\n", i, c+1)
		}
	}
	got := DenyAuditRecords([]byte(log.String()))
	if len(got) != maxAuditDenyKeys+1 {
		t.Fatalf("got %d records, want %d", len(got), maxAuditDenyKeys+1)
	}
	if want := fmt.Sprintf("egress_deny host=h%d.example port=443 reason=no_sni count=%d", maxAuditDenyKeys+7, maxAuditDenyKeys+8); got[0] != want {
		t.Fatalf("first record = %q, want %q", got[0], want)
	}
	n := maxAuditDenyKeys + 8
	if want := summary("truncated", n, uint64(n*(n+1)/2), 0); got[len(got)-1] != want {
		t.Fatalf("summary = %q, want %q", got[len(got)-1], want)
	}

	log.Reset()
	for i := range maxDenyKeys + 2 {
		fmt.Fprintf(&log, `{"host":"h%d","port":443,"reason":"no_sni","count":1}`+"\n", i)
	}
	got = DenyAuditRecords([]byte(log.String()))
	if want := summary("malformed", maxDenyKeys+1, maxDenyKeys+1, 1); got[len(got)-1] != want {
		t.Fatalf("key cap summary = %q, want %q", got[len(got)-1], want)
	}

	// A later, smaller running count for a key never lowers the folded count.
	got = DenyAuditRecords([]byte(`{"host":"a","port":1,"reason":"no_sni","count":3}` + "\n" +
		`{"host":"a","port":1,"reason":"no_sni","count":1}`))
	if want := "egress_deny host=a port=1 reason=no_sni count=3"; got[0] != want {
		t.Fatalf("folded record = %q, want %q", got[0], want)
	}

	got = DenyAuditRecords([]byte(`{"host":"a","port":1,"reason":"no_sni","count":18446744073709551615}` + "\n" +
		`{"host":"b","port":1,"reason":"no_sni","count":2}`))
	if want := summary("complete", 2, 18446744073709551615, 0); got[len(got)-1] != want {
		t.Fatalf("saturated summary = %q, want %q", got[len(got)-1], want)
	}
}

func TestDenyAuditRecordsPortZeroOnlyForOverflow(t *testing.T) {
	t.Parallel()
	got := DenyAuditRecords([]byte(`{"host":"(overflow)","port":0,"reason":"key_limit","count":2}`))
	if want := "egress_deny host=(overflow) port=0 reason=key_limit count=2"; got[0] != want {
		t.Fatalf("overflow record = %q, want %q", got[0], want)
	}
	for _, line := range []string{
		`{"host":"a.example","port":0,"reason":"key_limit","count":1}`,
		`{"host":"(overflow)","port":0,"reason":"no_sni","count":1}`,
	} {
		if got := DenyAuditRecords([]byte(line)); !strings.HasSuffix(got[len(got)-1], "malformed_lines=1") {
			t.Fatalf("%s: summary = %q, want one malformed line", line, got[len(got)-1])
		}
	}
}
