// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package testkit

import (
	"os"
	"os/exec"
	"testing"
)

// TestCredentialModeFollowsThePreparedPlaceholder runs prepare_injection_bundle
// with a stubbed helper and then credential_mode_label: a placeholder from the
// helper makes the session brokered, and a launch whose helper prints none is
// direct even after an earlier value.
func TestCredentialModeFollowsThePreparedPlaceholder(t *testing.T) {
	body := launcherFunction(t, "prepare_injection_bundle") + launcherFunction(t, "credential_mode_label") + `
prepare_colima_staging_cache_roots() { :; }
egress_proxy_brokers_credentials() { printf '0\n'; }
go_hostutil() {
  [[ -z "${PROBE_PLACEHOLDER}" ]] || printf 'INJECTION_CREDENTIAL_PLACEHOLDER=%s\n' "${PROBE_PLACEHOLDER}"
}
REAL_HOME=/nonexistent
INJECTION_CREDENTIAL_PLACEHOLDER=wcph-ffffffffffffffffffffffffffffffff
prepare_injection_bundle claude strict
credential_mode_label
`
	for placeholder, want := range map[string]string{
		"wcph-0123456789abcdef0123456789abcdef": "brokered\n",
		"":                                      "direct\n",
	} {
		cmd := exec.Command("bash", "-c", "set -euo pipefail\n"+body)
		cmd.Env = append(os.Environ(), "PROBE_PLACEHOLDER="+placeholder)
		out, err := cmd.CombinedOutput()
		if err != nil || string(out) != want {
			t.Errorf("placeholder %q: credential_mode_label = %q, %v; want %q", placeholder, out, err, want)
		}
	}
}
