// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package providerid

import "testing"

func TestPlannedProvidersRemainUnsupportedUntilCertified(t *testing.T) {
	for _, tc := range []struct {
		name string
		got  string
		want string
	}{
		{name: "Antigravity", got: Antigravity, want: "antigravity"},
		{name: "Copilot", got: Copilot, want: "copilot"},
	} {
		if tc.got != tc.want {
			t.Fatalf("%s = %q, want %q", tc.name, tc.got, tc.want)
		}
		if IsValid(tc.got) {
			t.Fatalf("%s must stay out of the supported-provider set until runtime support and certification land", tc.name)
		}
	}
}
