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
	unreadable := withSID("prof|$SID|egress_summary|status=unreadable|\n")
	for _, tc := range []struct {
		name, logs, helper, mode string
		want, audit              string
		warns                    bool
	}{
		{"logs succeed", "logs-ok", "ok", "", "new\n", withSID("prof|$SID|egress_deny|host=new|count=1|\nprof|$SID|egress_summary|status=complete|\n"), false},
		{"logs fail", "logs-fail", "ok", "", "old\n", unreadable, true},
		{"read fails", "logs-ok", "fail", "", "new\n", unreadable, false},
		{"no-audit", "logs-ok", "ok", "no-audit", "new\n", "", false},
		{"no-audit logs fail", "logs-fail", "ok", "no-audit", "old\n", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			sessions := filepath.Join(dir, "sessions")
			final := filepath.Join(sessions, brokerTestSession+".egress-deny.jsonl")
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
SESSION_ID="$SID" EGRESS_PROXY_CONTAINER=side EGRESS_PROXY_NETWORK=net
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
				"HELPER="+tc.helper, "AUDITED="+audited, "MODE="+tc.mode, "SID="+brokerTestSession)
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
// A failed launch has no seal, so its cleanup stops the proxy before it
// writes the aborted record, the last write of the session.
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

	// metadatautil.ValidateEgressDenySealOrder proves that every other seal
	// path goes through these two functions.
}

// TestCleanupStopsTheProxyBeforeTheAbortedRecord runs the launcher cleanup for
// a session whose detached monitor never became ready: the deny records must
// precede the aborted record, the last write of a session with no seal.
func TestCleanupStopsTheProxyBeforeTheAbortedRecord(t *testing.T) {
	cleanup := launcherFunction(t, "cleanup")
	for flag, want := range map[string]string{"1": "proxy p\naborted\n", "0": "aborted\n"} {
		got := runOrderProbe(t, "set +u\n"+cleanup+`
DETACHED_SESSION_ACTIVE=0 SESSION_RECORD_WRITTEN=1 SESSION_RECORD_FINALIZED=0 COLIMA_PROFILE=p EGRESS_PROXY_STARTED=`+flag+`
stop_egress_proxy() { printf 'proxy %s\n' "$*" >>"$STEPS"; }
write_session_record_final() { printf '%s\n' "$1" >>"$STEPS"; }
session_assurance_initial() { :; }
restore_interactive_host_terminal() { :; }
cleanup_runtime_builder() { :; }
cleanup_workcell_trusted_docker_client() { :; }
cleanup 1
`)
		if got != want {
			t.Fatalf("EGRESS_PROXY_STARTED=%s: steps = %q, want %q", flag, got, want)
		}
	}
}

// TestEgressProxyAliasesUseTheCanonicalHost builds the agent's host aliases
// from the helper's canonical endpoints, not from the raw list: a dotted or
// upper-case entry would otherwise alias a name the agent never looks up
// (issue 823). A list the helper refuses stops the launch.
func TestEgressProxyAliasesUseTheCanonicalHost(t *testing.T) {
	lib, err := filepath.Abs(filepath.Join("..", "..", "scripts", "lib", "launcher", "egress-endpoints.sh"))
	if err != nil {
		t.Fatal(err)
	}
	script := `
set -euo pipefail
source "$LIB"
SESSION_ID=s NETWORK_POLICY=allowlist EGRESS_PROXY=1
go_hostutil() {
  [[ "$1 $2" == "helper validate-egress-proxy-allowlist" ]] || return 9
  case "$3" in
    "API.OpenAI.com.:443 X.example.:8080") printf 'api.openai.com:443\nx.example:8080\n' ;;
    *) return 1 ;;
  esac
}
build_runtime_host_aliases "API.OpenAI.com.:443 X.example.:8080"
printf '%s\n' "${RUNTIME_NETWORK_ARGS[@]}"
if build_runtime_host_aliases "a.example:5432 b.example:5432"; then echo accepted; else echo refused; fi
`
	cmd := exec.Command("bash", "-c", script)
	cmd.Env = append(os.Environ(), "LIB="+lib)
	out, err := cmd.CombinedOutput()
	want := "--network\nwc-s\n--dns\n127.0.0.1\n--add-host\napi.openai.com:egress-proxy-ip\n--add-host\nx.example:egress-proxy-ip\nrefused\n"
	if err != nil || string(out) != want {
		t.Fatalf("aliases = %q, %v; want %q", out, err, want)
	}
}

// TestSessionStopOfAFailedSessionStopsTheProxyBeforeTheSeal stops a detached
// session whose record turns failed. Whoever seals the session must first stop
// the proxy with audit, which appends the deny records and the broker summary;
// a no-audit stop before an unsealed seal would leave them out of the sealed
// chain (issue 823). The monitor can be gone from the start, or alive when
// the stop writes the terminal record and gone before its own proxy stop and
// seal, either before the dead-monitor cleanup or before the seal recheck:
// then the proxy network is still up and the fallback runs. With no network
// left, the monitor already stopped the proxy, and only the recheck seals. A
// network lookup that fails takes the fallback, whose own proxy stop then
// fails, so nothing seals; a session already sealed takes no fallback. A
// stopped container whose monitor is gone at the second look seals nothing.
func TestSessionStopOfAFailedSessionStopsTheProxyBeforeTheSeal(t *testing.T) {
	const stopped = "record stop-request\nterminal 7\n"
	const fallback = "proxy prof S\nrecord exit\nterminal 7\nseal\n"
	cases := []struct{ name, liveCalls, network, container, sealed, want string }{
		{"monitor gone", "0", "wc-S", "running", "", "record stop-request\n" + fallback + "proxy prof S no-audit\n"},
		{"monitor dies before its proxy stop", "1", "wc-S", "running", "", stopped + fallback},
		{"monitor dies after its proxy stop", "1", "", "running", "", stopped + "proxy prof S no-audit\nseal\n"},
		{"monitor dies before the seal recheck", "2", "wc-S", "running", "", stopped + fallback},
		{"monitor dies after its proxy stop, before the seal recheck", "2", "", "running", "", stopped + "seal\n"},
		{"monitor live throughout", "9", "wc-S", "running", "", stopped + "seal\n"},
		{"network lookup fails after the monitor dies", "1", "fail", "running", "", stopped + "proxy prof S\nexit 1\n"},
		{"network lookup fails before the seal recheck", "2", "fail", "running", "", stopped + "proxy prof S\nexit 1\n"},
		{"monitor dies before the seal recheck of a sealed session", "2", "wc-S", "running", "1", stopped},
		{"stopped container, monitor gone at the second look", "1", "wc-S", "stopped", "", "exit 1\n"},
		{"stopped container, monitor live", "9", "wc-S", "stopped", "", "seal\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := runOrderProbe(t, launcherFunction(t, "session_stop_main")+launcherFunction(t, "finish_stopped_session_fallback")+`
DIR="$(dirname "$STEPS")"
LIVE_CALLS=`+c.liveCalls+` NETWORK=`+c.network+` CONTAINER=`+c.container+`
[[ -z "`+c.sealed+`" ]] || touch "$DIR/S.audit-sig"
[[ "$CONTAINER" == running ]] || touch "$DIR/stopped"
session_run_cli_with_roots() { printf 'session_id=S\nforce=0\nprofile=prof\ncontainer_name=c\n'; }
shellproto_field() { case "$2" in session_id) printf 'S\n' ;; force) printf '0\n' ;; *) printf '%s\n' "${3:-}" ;; esac; }
resolve_host_tool() { :; }
sanitize_host_docker_env() { :; }
load_session_runtime_metadata() {
  SESSION_META_PROFILE=prof SESSION_META_CONTAINER_NAME=c SESSION_META_RECORD_PATH="$DIR/S.json"
  SESSION_META_CURRENT_ASSURANCE=managed-mutable
  if [[ -e "$DIR/stopped" ]]; then SESSION_META_STATUS=failed SESSION_META_LIVE_STATUS=stopped
  else SESSION_META_STATUS=running SESSION_META_LIVE_STATUS=running; fi
  [[ ! -e "$DIR/S.audit-sig" ]] || SESSION_META_STATUS=failed
}
session_container_live_state() { printf '%s\n' "$CONTAINER"; }
# The monitor answers live for its first LIVE_CALLS checks, then is gone.
session_monitor_pid_is_live() {
  local n=0
  [[ ! -e "$DIR/live-checks" ]] || n="$(cat "$DIR/live-checks")"
  printf '%s\n' "$((n + 1))" >"$DIR/live-checks"
  [[ "$n" -lt "$LIVE_CALLS" ]]
}
egress_proxy_network_for_session() {
  [[ "$NETWORK" != fail ]] || return 1
  [[ -z "$NETWORK" ]] || printf '%s\n' "$NETWORK"
}
session_record_stop_request_marker() { :; }
session_clear_stop_request_marker() { :; }
emit_loaded_session_control_summary() { :; }
write_session_record() { :; }
run_profile_docker_command() { touch "$DIR/stopped"; }
session_container_exit_code() { printf '7\n'; }
append_session_control_audit_record() { printf 'record %s\n' "$3" >>"$STEPS"; }
stop_egress_proxy_for_session() { printf 'proxy %s\n' "$*" >>"$STEPS"; [[ "$NETWORK" != fail ]]; }
session_write_stop_requested_terminal_record() { printf 'terminal %s\n' "$2" >>"$STEPS"; }
sign_session_audit_head_explicit() { printf 'seal\n' >>"$STEPS"; touch "$DIR/S.audit-sig"; }
set +e
(session_stop_main --id S >/dev/null 2>&1)
status=$?
set -e
[[ "$status" -eq 0 ]] || printf 'exit %s\n' "$status" >>"$STEPS"
`)
			if got != c.want {
				t.Fatalf("stop steps = %q, want %q", got, c.want)
			}
		})
	}
}
