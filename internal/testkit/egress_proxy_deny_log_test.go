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

// TestStartEgressProxyReplacesOnlyGeneratedAliases proves that the token swap
// touches the --add-host values before the image and no user argument after it.
func TestStartEgressProxyReplacesOnlyGeneratedAliases(t *testing.T) {
	lib, err := filepath.Abs(filepath.Join("..", "..", "scripts", "lib", "launcher", "egress-endpoints.sh"))
	if err != nil {
		t.Fatal(err)
	}
	script := `
set -euo pipefail
source "$LIB"
EGRESS_PROXY_NETWORK=net EGRESS_PROXY_CONTAINER=side ALLOW_ENDPOINTS=a.example:443 HOST_DOCKER_BIN=docker IMAGE_TAG=img
DOCKER_RUN=(docker run --add-host a.example:egress-proxy-ip -e X=y:egress-proxy-ip img prompt:egress-proxy-ip --add-host b.example:egress-proxy-ip)
run_workcell_docker_client_command() {
  shift
  case "$1 $2" in
    "network inspect") printf '10.9.0.0/24 \n' ;;
    "inspect -f") printf '10.9.0.2\n' ;;
    "exec side") printf '  0: 0200090A:01BB 00000000:0000 0A 00000000:00000000\n' ;;
  esac
}
start_egress_proxy sha256:abc
printf '%s\n' "${DOCKER_RUN[@]}"
`
	cmd := exec.Command("bash", "-c", script)
	cmd.Env = append(os.Environ(), "LIB="+lib)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("start_egress_proxy failed: %v\n%s", err, out)
	}
	want := "docker\nrun\n--add-host\na.example:10.9.0.2\n-e\nX=y:egress-proxy-ip\nimg\nprompt:egress-proxy-ip\n--add-host\nb.example:egress-proxy-ip\n"
	if string(out) != want {
		t.Fatalf("DOCKER_RUN = %q, want %q", out, want)
	}
}

// TestStartEgressProxyWaitsForListeners proves that start_egress_proxy fails
// the launch when the sidecar runs but does not listen on an allowlisted port,
// and passes when every port listens.
func TestStartEgressProxyWaitsForListeners(t *testing.T) {
	lib, err := filepath.Abs(filepath.Join("..", "..", "scripts", "lib", "launcher", "egress-endpoints.sh"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, sockets string
		wantFail      bool
	}{
		{"listening", "  0: 0200090A:01BB 00000000:0000 0A 0", false},
		{"leading-zero port", "  0: 0200090A:01BB 00000000:0000 0A 0", false},
		{"other port", "  0: 0200090A:0050 00000000:0000 0A 0", true},
		{"not listening", "  0: 0200090A:01BB 0300090A:9C40 01 0", true},
		{"no sockets", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			endpoint := "a.example:443"
			if tc.name == "leading-zero port" {
				endpoint = "a.example:0443"
			}
			script := `
set -euo pipefail
source "$LIB"
sleep() { :; }
EGRESS_PROXY_NETWORK=net EGRESS_PROXY_CONTAINER=side ALLOW_ENDPOINTS="$ENDPOINT" HOST_DOCKER_BIN=docker IMAGE_TAG=img
DOCKER_RUN=(docker run img)
run_workcell_docker_client_command() {
  shift
  case "$1 $2" in
    "network inspect") printf '10.9.0.0/24 \n' ;;
    "inspect -f") printf '10.9.0.2\n' ;;
    "exec side") printf '%s\n' "$SOCKETS" ;;
  esac
}
start_egress_proxy sha256:abc
`
			cmd := exec.Command("bash", "-c", script)
			cmd.Env = append(os.Environ(), "LIB="+lib, "SOCKETS="+tc.sockets, "ENDPOINT="+endpoint)
			out, err := cmd.CombinedOutput()
			if failed := err != nil; failed != tc.wantFail {
				t.Fatalf("failed = %v, want %v\n%s", failed, tc.wantFail, out)
			}
		})
	}
}
