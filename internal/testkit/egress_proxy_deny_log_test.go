// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package testkit

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestStopEgressProxyDenyLogPublish runs stop_egress_proxy against stubbed
// docker and publish helpers. A failed log collection must not replace the
// final file, and the sidecar must still go.
func TestStopEgressProxyDenyLogPublish(t *testing.T) {
	lib, err := filepath.Abs(filepath.Join("..", "..", "scripts", "lib", "launcher", "egress-endpoints.sh"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, logs string
		want       string
		warns      bool
	}{
		{"logs succeed", "logs-ok", "new\n", false},
		{"logs fail", "logs-fail", "old\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			sessions := filepath.Join(dir, "sessions")
			final := filepath.Join(sessions, "S1.egress-deny.jsonl")
			if err := os.MkdirAll(sessions, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(final, []byte("old\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			removed := filepath.Join(dir, "removed")
			script := `
set -euo pipefail
source "$LIB"
SESSION_ID=S1 EGRESS_PROXY_CONTAINER=side EGRESS_PROXY_NETWORK=net
profile_sessions_dir_path() { printf '%s\n' "$SESSIONS"; }
go_hostutil() { cp "$3" "$4"; }
run_profile_docker_command() {
  shift
  case "$1" in
    logs) if [[ "$LOGS" == logs-fail ]]; then printf 'partial\n'; return 1; fi; printf 'new\n' ;;
    rm|network) printf '%s\n' "$1" >>"$REMOVED" ;;
  esac
}
stop_egress_proxy prof
`
			cmd := exec.Command("bash", "-c", script)
			cmd.Env = append(os.Environ(), "LIB="+lib, "SESSIONS="+sessions, "LOGS="+tc.logs, "REMOVED="+removed, "TMPDIR="+dir)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("stop_egress_proxy failed: %v\n%s", err, out)
			}
			got, _ := os.ReadFile(final)
			if string(got) != tc.want {
				t.Fatalf("final file = %q, want %q", got, tc.want)
			}
			if warned := strings.Contains(string(out), "could not save the egress proxy deny log"); warned != tc.warns {
				t.Fatalf("warning = %v, want %v\n%s", warned, tc.warns, out)
			}
			gone, _ := os.ReadFile(removed)
			if string(gone) != "rm\nnetwork\n" {
				t.Fatalf("cleanup calls = %q, want rm then network", gone)
			}
			if left, _ := filepath.Glob(filepath.Join(dir, "workcell-egress-deny.*")); len(left) != 0 {
				t.Fatalf("staging directory left behind: %v", left)
			}
		})
	}
}
