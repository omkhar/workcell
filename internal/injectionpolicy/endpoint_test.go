// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package injectionpolicy

import "testing"

func TestValidateEgressEndpoint(t *testing.T) {
	valid := []string{"api.openai.com:443", "registry.internal.example:8443", "[2001:db8::1]:443", "[::1]:443", "host-1.sub.example:1"}
	for _, e := range valid {
		if err := ValidateEgressEndpoint(e, "network.allow_endpoints"); err != nil {
			t.Fatalf("ValidateEgressEndpoint(%q) unexpected error: %v", e, err)
		}
	}
	invalid := []string{"", "bad", "host", "host:", ":443", "host:0", "host:70000", "..evil:443", ".evil:443", "ev il:443", "host:44a", "[nothex!]:443"}
	for _, e := range invalid {
		if err := ValidateEgressEndpoint(e, "network.allow_endpoints"); err == nil {
			t.Fatalf("ValidateEgressEndpoint(%q) accepted an invalid endpoint", e)
		}
	}
}
