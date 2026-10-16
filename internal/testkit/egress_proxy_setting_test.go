// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package testkit

import (
	"path/filepath"
	"testing"
)

// TestEgressProxySetting runs set_egress_proxy_setting over each flag list
// and resolve_egress_proxy over the result, and compares the whole output.
// The proxy is the default only for strict Colima, and --egress-proxy=off
// turns it off there. The bare flag means on, a second flag in any spelling
// and a value other than on or off stop with status 2, and only strict Colima
// can turn the proxy on. An inherited EGRESS_PROXY_SETTING never counts as a
// flag.
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
		{"default on strict colima", "", "colima", "strict", 0, "EGRESS_PROXY=1\n"},
		{"default on development", "", "colima", "development", 0, "EGRESS_PROXY=0\n"},
		{"default on build", "", "colima", "build", 0, "EGRESS_PROXY=0\n"},
		{"default on breakglass", "", "colima", "breakglass", 0, "EGRESS_PROXY=0\n"},
		{"default on docker desktop", "", "docker-desktop", "strict", 0, "EGRESS_PROXY=0\n"},
		{"default on aws", "", "aws-ec2-ssm", "strict", 0, "EGRESS_PROXY=0\n"},
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
		{"space off", "--egress-proxy off", "colima", "strict", 2, "--egress-proxy takes its value after =: --egress-proxy=off\n"},
		{"space on", "--egress-proxy on", "colima", "strict", 2, "--egress-proxy takes its value after =: --egress-proxy=on\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, out := runBashProbe(t, `set -euo pipefail
source "$LIB"
set -- $FLAGS
while (($#)); do
  set_egress_proxy_setting "$1" "${2-}"
  shift
done
resolve_egress_proxy "$TARGET" "$MODE"
echo "EGRESS_PROXY=${EGRESS_PROXY}"
`, map[string]string{"EGRESS_PROXY_SETTING": "on", "LIB": lib, "FLAGS": tc.flags, "TARGET": tc.target, "MODE": tc.mode})
			if code != tc.code || out != tc.want {
				t.Fatalf("flags %q on %s %s: code %d, output %q; want code %d with %q", tc.flags, tc.target, tc.mode, code, out, tc.code, tc.want)
			}
		})
	}
}

// TestEgressProxyBrokersCredentials checks that only a proxy session that
// prepares a launch brokers its API keys. --auth-status, --doctor and
// --inspect stage the policy without the broker, so a key from source does
// not stop them.
func TestEgressProxyBrokersCredentials(t *testing.T) {
	lib, err := filepath.Abs(filepath.Join("..", "..", "scripts", "lib", "launcher", "egress-endpoints.sh"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ proxy, authStatus, doctor, inspect, want string }{
		{"1", "0", "0", "0", "1\n"},
		{"0", "0", "0", "0", "0\n"},
		{"1", "1", "0", "0", "0\n"},
		{"1", "0", "1", "0", "0\n"},
		{"1", "0", "0", "1", "0\n"},
	} {
		code, out := runBashProbe(t, `set -euo pipefail
source "$LIB"
EGRESS_PROXY="$PROXY"
egress_proxy_brokers_credentials "$AUTH" "$DOCTOR" "$INSPECT"
`, map[string]string{"LIB": lib, "PROXY": tc.proxy, "AUTH": tc.authStatus, "DOCTOR": tc.doctor, "INSPECT": tc.inspect})
		if code != 0 || out != tc.want {
			t.Errorf("proxy %s auth-status %s doctor %s inspect %s: code %d, output %q; want %q", tc.proxy, tc.authStatus, tc.doctor, tc.inspect, code, out, tc.want)
		}
	}
}
