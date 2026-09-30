// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package egressproxy

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestParseAllowlist(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		text    string
		sni     []string
		forward map[uint16]string
		wantErr string
	}{
		{name: "sni hosts share 443", text: "api.example.com:443\nb.example:443", sni: []string{"api.example.com", "b.example"}},
		{name: "comments blanks and case", text: "# header\n\n API.Example.COM:443 # trailing\n", sni: []string{"api.example.com"}},
		{name: "trailing dot", text: "api.example.com.:443", sni: []string{"api.example.com"}},
		{name: "one host per forward port", text: "db.example:5432\nDB.example:5432\nssh.example:22", forward: map[uint16]string{5432: "db.example", 22: "ssh.example"}},
		{name: "two hosts on a forward port", text: "a.example:5432\nb.example:5432", wantErr: "more than one host"},
		{name: "ipv4 literal", text: "10.0.0.5:443", wantErr: "IP literal"},
		{name: "ipv6 literal", text: "[2001:db8::1]:443", wantErr: "IP literal"},
		{name: "bad grammar", text: "under_score:443", wantErr: "invalid endpoint"},
		{name: "wildcard", text: "*.example.com:443", wantErr: "invalid endpoint"},
		{name: "missing port", text: "api.example.com", wantErr: "invalid endpoint"},
		{name: "port zero", text: "api.example.com:0", wantErr: "invalid endpoint port"},
		{name: "empty", text: "# nothing\n", wantErr: "no endpoints"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseAllowlist(tc.text, "allowlist")
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("parseAllowlist(%q) error = %v, want %q", tc.text, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseAllowlist(%q) unexpected error: %v", tc.text, err)
			}
			for _, host := range tc.sni {
				if !got.sni[host] {
					t.Errorf("sni[%q] = false, want true", host)
				}
			}
			if len(got.sni) != len(tc.sni) {
				t.Errorf("sni = %v, want %v", got.sni, tc.sni)
			}
			if len(got.forward) != len(tc.forward) {
				t.Errorf("forward = %v, want %v", got.forward, tc.forward)
			}
			for port, host := range tc.forward {
				if got.forward[port] != host {
					t.Errorf("forward[%d] = %q, want %q", port, got.forward[port], host)
				}
			}
		})
	}
}

func TestLoadAllowlistAndPorts(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "allowlist")
	if err := os.WriteFile(path, []byte("db.example:5432\napi.example.com:443\nssh.example:22\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a, err := LoadAllowlist(path)
	if err != nil {
		t.Fatalf("LoadAllowlist: %v", err)
	}
	if got, want := a.Ports(), []uint16{22, 443, 5432}; !slices.Equal(got, want) {
		t.Fatalf("Ports() = %v, want %v", got, want)
	}
	if _, err := LoadAllowlist(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("LoadAllowlist(missing) error = nil, want error")
	}
}
