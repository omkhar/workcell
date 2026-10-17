// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package testkit

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestEgressEnforcementReachesTheRecordAndTheLaunchAudit runs
// write_session_record_initial and append_launch_audit_record with stubbed
// writers. The session record carries egress_enforcement, and the launch
// audit record carries it as its last field, so readers that match the
// earlier fields keep working.
func TestEgressEnforcementReachesTheRecordAndTheLaunchAudit(t *testing.T) {
	lib, err := filepath.Abs(filepath.Join("..", "..", "scripts", "lib", "launcher", "egress-endpoints.sh"))
	if err != nil {
		t.Fatal(err)
	}
	body := launcherFunction(t, "write_session_record_initial") + launcherFunction(t, "append_launch_audit_record") + `
source "$LIB"
# Every other launcher helper prints nothing here; only the label matters.
command_not_found_handle() { :; }
mkdir() { :; }
write_session_record() { shift; printf 'record %s\n' "$@"; }
append_audit_record() { shift; printf 'audit %s\n' "$@"; }
SESSION_ID=S1 TARGET_BACKEND=colima NETWORK_POLICY=allowlist
write_session_record_initial prof path
append_launch_audit_record prof path
`
	for proxy, want := range map[string]string{"1": "egress_enforcement=proxy", "0": "egress_enforcement=allowlist"} {
		cmd := exec.Command("bash", "-c", "set -o pipefail\nEGRESS_PROXY="+proxy+"\n"+body)
		cmd.Env = append(os.Environ(), "LIB="+lib)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("probe failed: %v\n%s", err, out)
		}
		var record, audit []string
		for _, line := range strings.Split(strings.TrimSuffix(string(out), "\n"), "\n") {
			if field, ok := strings.CutPrefix(line, "record "); ok {
				record = append(record, field)
			} else if field, ok := strings.CutPrefix(line, "audit "); ok {
				audit = append(audit, field)
			}
		}
		if !slices.Contains(record, want) {
			t.Errorf("EGRESS_PROXY=%s: session record fields %q lack %q", proxy, record, want)
		}
		if len(audit) == 0 || audit[0] != "event=launch" || audit[len(audit)-1] != want {
			t.Errorf("EGRESS_PROXY=%s: launch audit fields %q; want event=launch first and %q last", proxy, audit, want)
		}
	}
}
