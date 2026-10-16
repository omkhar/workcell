// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package testkit

import (
	"path/filepath"
	"testing"
)

// TestEgressProxySetting runs set_egress_proxy_setting over each flag list
// and resolve_egress_proxy over the result, and compares the whole output.
// The bare flag means on, a second flag in any spelling and a value other
// than on or off stop with status 2, and only strict Colima can turn the
// proxy on. An inherited EGRESS_PROXY_SETTING never counts as a flag.
func TestEgressProxySetting(t *testing.T) {
	lib, err := filepath.Abs(filepath.Join("..", "..", "scripts", "lib", "launcher", "egress-endpoints.sh"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, flags, target, mode string
		code                      int
		want                      string
	}{
		{"no flag", "", "colima", "strict", 0, "EGRESS_PROXY=0\n"},
		{"bare flag", "--egress-proxy", "colima", "strict", 0, "EGRESS_PROXY=1\n"},
		{"on", "--egress-proxy=on", "colima", "strict", 0, "EGRESS_PROXY=1\n"},
		{"off", "--egress-proxy=off", "colima", "strict", 0, "EGRESS_PROXY=0\n"},
		{"off on development", "--egress-proxy=off", "colima", "development", 0, "EGRESS_PROXY=0\n"},
		{"off on docker desktop", "--egress-proxy=off", "docker-desktop", "strict", 0, "EGRESS_PROXY=0\n"},
		{"on on development", "--egress-proxy=on", "colima", "development", 2, "--egress-proxy supports only --target colima with --mode strict.\n"},
		{"bare on docker desktop", "--egress-proxy", "docker-desktop", "strict", 2, "--egress-proxy supports only --target colima with --mode strict.\n"},
		{"empty value", "--egress-proxy=", "colima", "strict", 2, "--egress-proxy takes on or off: \n"},
		{"upper case", "--egress-proxy=ON", "colima", "strict", 2, "--egress-proxy takes on or off: ON\n"},
		{"other value", "--egress-proxy=yes", "colima", "strict", 2, "--egress-proxy takes on or off: yes\n"},
		{"bare twice", "--egress-proxy --egress-proxy", "colima", "strict", 2, "--egress-proxy may be given only once.\n"},
		{"bare then off", "--egress-proxy --egress-proxy=off", "colima", "strict", 2, "--egress-proxy may be given only once.\n"},
		{"off then bare", "--egress-proxy=off --egress-proxy", "colima", "strict", 2, "--egress-proxy may be given only once.\n"},
		{"on then off", "--egress-proxy=on --egress-proxy=off", "colima", "strict", 2, "--egress-proxy may be given only once.\n"},
		{"off twice", "--egress-proxy=off --egress-proxy=off", "colima", "strict", 2, "--egress-proxy may be given only once.\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, out := runBashProbe(t, `set -euo pipefail
source "$LIB"
for flag in $FLAGS; do set_egress_proxy_setting "$flag"; done
resolve_egress_proxy "$TARGET" "$MODE"
echo "EGRESS_PROXY=${EGRESS_PROXY}"
`, map[string]string{"EGRESS_PROXY_SETTING": "on", "LIB": lib, "FLAGS": tc.flags, "TARGET": tc.target, "MODE": tc.mode})
			if code != tc.code || out != tc.want {
				t.Fatalf("flags %q on %s %s: code %d, output %q; want code %d with %q", tc.flags, tc.target, tc.mode, code, out, tc.code, tc.want)
			}
		})
	}
}
