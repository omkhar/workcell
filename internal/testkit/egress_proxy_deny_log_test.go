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
// docker, publish and audit helpers. A failed log collection must not replace
// the final file, and the sidecar must still go. Every stop appends the
// helper's audit records, or egress_summary status=unreadable when the save or
// the read fails. A no-audit stop (session delete) appends nothing.
func TestStopEgressProxyDenyLogPublish(t *testing.T) {
	lib, err := filepath.Abs(filepath.Join("..", "..", "scripts", "lib", "launcher", "egress-endpoints.sh"))
	if err != nil {
		t.Fatal(err)
	}
	const unreadable = "prof|S1|egress_summary|status=unreadable|\n"
	for _, tc := range []struct {
		name, logs, helper, mode string
		want, audit              string
		warns                    bool
	}{
		{"logs succeed", "logs-ok", "ok", "", "new\n", "prof|S1|egress_deny|host=new|count=1|\nprof|S1|egress_summary|status=complete|\n", false},
		{"logs fail", "logs-fail", "ok", "", "old\n", unreadable, true},
		{"read fails", "logs-ok", "fail", "", "new\n", unreadable, false},
		{"no-audit", "logs-ok", "ok", "no-audit", "new\n", "", false},
		{"no-audit logs fail", "logs-fail", "ok", "no-audit", "old\n", "", true},
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
			audited := filepath.Join(dir, "audited")
			script := `
set -euo pipefail
source "$LIB"
SESSION_ID=S1 EGRESS_PROXY_CONTAINER=side EGRESS_PROXY_NETWORK=net
profile_sessions_dir_path() { printf '%s\n' "$SESSIONS"; }
go_hostutil() {
  case "$2" in
    publish-session-capture-file) cp "$3" "$4" ;;
    egress-deny-audit-records)
      [[ "$HELPER" == ok ]] || return 1
      printf 'egress_deny host=%s count=1\negress_summary status=complete\n' "$(cat "$3")"
      ;;
    *) return 9 ;;
  esac
}
append_session_control_audit_record() { printf '%s|' "$@" >>"$AUDITED"; printf '\n' >>"$AUDITED"; }
run_profile_docker_command() {
  shift
  case "$1" in
    logs) if [[ "$LOGS" == logs-fail ]]; then printf 'partial\n'; return 1; fi; printf 'new\n' ;;
    rm|network) printf '%s\n' "$1" >>"$REMOVED" ;;
  esac
}
stop_egress_proxy prof ${MODE}
`
			cmd := exec.Command("bash", "-c", script)
			cmd.Env = append(os.Environ(), "LIB="+lib, "SESSIONS="+sessions, "LOGS="+tc.logs, "REMOVED="+removed, "TMPDIR="+dir,
				"HELPER="+tc.helper, "AUDITED="+audited, "MODE="+tc.mode)
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
			if got, _ := os.ReadFile(audited); string(got) != tc.audit {
				t.Fatalf("audit records = %q, want %q", got, tc.audit)
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

// launcherFunction returns the definition of name in scripts/workcell, from
// its header through the closing brace at the start of a line.
func launcherFunction(t *testing.T, name string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(repoRoot(t), "scripts", "workcell"))
	if err != nil {
		t.Fatal(err)
	}
	script := string(content)
	start := strings.Index(script, "\n"+name+"() {\n")
	if start < 0 {
		t.Fatalf("scripts/workcell has no %s", name)
	}
	end := strings.Index(script[start:], "\n}\n")
	return script[start+1 : start+end+3]
}

// runOrderProbe runs a launcher function with stubs that log each step, and
// returns the log.
func runOrderProbe(t *testing.T, body string) string {
	t.Helper()
	log := filepath.Join(t.TempDir(), "steps")
	cmd := exec.Command("bash", "-c", "set -euo pipefail\n"+body)
	cmd.Env = append(os.Environ(), "STEPS="+log)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("probe failed: %v\n%s", err, out)
	}
	got, _ := os.ReadFile(log)
	return string(got)
}

// TestEgressDenyRecordsPrecedeTheSeal is the gate for egress-deny-audited:
// the seal signs the last session record, so each path that seals a session
// stops the egress proxy, which appends the deny records, before it seals.
func TestEgressDenyRecordsPrecedeTheSeal(t *testing.T) {
	finalize := launcherFunction(t, "stop_egress_proxy_then_finalize")
	for flag, want := range map[string]string{"1": "proxy prof\nseal prof path 3\nflag=0\n", "0": "seal prof path 3\nflag=0\n"} {
		got := runOrderProbe(t, finalize+`
EGRESS_PROXY_STARTED=`+flag+`
stop_egress_proxy() { printf 'proxy %s\n' "$*" >>"$STEPS"; }
finalize_session_audit() { printf 'seal %s\n' "$*" >>"$STEPS"; }
stop_egress_proxy_then_finalize prof path 3
printf 'flag=%s\n' "$EGRESS_PROXY_STARTED" >>"$STEPS"
`)
		if got != want {
			t.Fatalf("EGRESS_PROXY_STARTED=%s: steps = %q, want %q", flag, got, want)
		}
	}

	fallback := launcherFunction(t, "finish_stopped_session_fallback") + `
SESSION_META_CONTAINER_NAME=c SESSION_META_CURRENT_ASSURANCE=managed-mutable
session_container_exit_code() { printf '7\n'; }
append_session_control_audit_record() { printf 'record %s %s\n' "$3" "$4" >>"$STEPS"; }
session_write_stop_requested_terminal_record() { printf 'terminal %s\n' "$*" >>"$STEPS"; }
sign_session_audit_head_explicit() { printf 'seal %s\n' "$*" >>"$STEPS"; }
`
	got := runOrderProbe(t, fallback+`
stop_egress_proxy_for_session() { printf 'proxy %s\n' "$*" >>"$STEPS"; }
finish_stopped_session_fallback prof S1 rec.json
`)
	if want := "proxy prof S1\nrecord exit source=host-stop-fallback\nterminal rec.json 7\nseal prof S1 rec.json\n"; got != want {
		t.Fatalf("fallback steps = %q, want %q", got, want)
	}
	got = runOrderProbe(t, fallback+`
stop_egress_proxy_for_session() { return 1; }
! finish_stopped_session_fallback prof S1 rec.json
`)
	if got != "" {
		t.Fatalf("a failed proxy stop must stop the fallback before any record, got %q", got)
	}

	// Every other seal path must go through these two functions, and every
	// other proxy stop for a recorded session appends nothing.
	content, err := os.ReadFile(filepath.Join(repoRoot(t), "scripts", "workcell"))
	if err != nil {
		t.Fatal(err)
	}
	script := strings.Replace(string(content), finalize, "", 1)
	script = strings.Replace(script, launcherFunction(t, "finish_stopped_session_fallback"), "", 1)
	for line := range strings.Lines(script) {
		code := strings.TrimSpace(line)
		if strings.HasPrefix(code, "finalize_session_audit \"") ||
			strings.HasPrefix(code, "stop_egress_proxy_for_session ") && !strings.Contains(code, " no-audit ") {
			t.Fatalf("scripts/workcell bypasses the deny-record order: %s", code)
		}
	}
}
