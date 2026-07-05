// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package authpolicy

import (
	"strings"
	"testing"
)

// TestValidatePolicyNetworkFailClosed proves the acceptance-time [network]
// validation rejects exactly what launch rejects, so `workcell policy validate`
// cannot report a policy valid that launch later refuses.
func TestValidatePolicyNetworkFailClosed(t *testing.T) {
	cases := []struct {
		name    string
		network map[string]any
		wantErr string
	}{
		{"mode key network_policy", map[string]any{"network_policy": "unrestricted"}, "unsupported keys: network_policy"},
		{"mode key mode", map[string]any{"mode": "unrestricted"}, "unsupported keys: mode"},
		{"malformed endpoint", map[string]any{"allow_endpoints": []any{"bad"}}, "invalid endpoint"},
		{"empty endpoint", map[string]any{"deny_endpoints": []any{""}}, "invalid endpoint"},
		{"non-array", map[string]any{"allow_endpoints": "api:443"}, "must be an array"},
		{"non-string element", map[string]any{"allow_endpoints": []any{443}}, "found non-string element"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validatePolicyNetwork(map[string]any{"network": tc.network})
			if err == nil {
				t.Fatalf("validatePolicyNetwork accepted an invalid [network]: %v", tc.network)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %q, want substring %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestValidatePolicyNetworkAcceptsValid(t *testing.T) {
	err := validatePolicyNetwork(map[string]any{"network": map[string]any{
		"allow_endpoints": []any{"registry.internal.example:443"},
		"deny_endpoints":  []any{"chatgpt.com:443"},
	}})
	if err != nil {
		t.Fatalf("validatePolicyNetwork rejected a valid [network]: %v", err)
	}
}
