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

// brokerLaunchProbe sources the launcher modules, stubs the host helpers and
// runs body. It returns the combined output and the run error.
func brokerLaunchProbe(t *testing.T, dir, body string, env ...string) (string, error) {
	t.Helper()
	root := repoRoot(t)
	script := `
set -euo pipefail
source "$ROOT/scripts/lib/launcher/host-detect.sh"
source "$ROOT/scripts/lib/launcher/egress-endpoints.sh"
SESSION_ID=S1 COLIMA_PROFILE=prof INJECTION_BUNDLE_ROOT=/bundle TRUSTED_HOST_PATH=/usr/bin:/bin REAL_HOME="$DIR"
INJECTION_CREDENTIAL_PLACEHOLDER=wcph-0123456789abcdef0123456789abcdef
profile_sessions_dir_path() { printf '%s\n' "$DIR/sessions"; }
go_hostutil() {
  case "$2" in
    credential-broker-plan) printf 'terminate=api.anthropic.com=x-api-key=%s\ncredential=claude_api_key=keychain:workcell-claude-api-key\naudit=credential_broker_start key=claude_api_key item=workcell-claude-api-key\n' "$4" ;;
    publish-session-capture-file) cp "$3" "$4" ;;
    credential-broker-audit-records) [[ -f "$3" ]] && printf 'credential_broker_summary status=complete granted=1 denied=0 malformed_lines=0\n' ;;
    *) return 9 ;;
  esac
}
append_session_control_audit_record() { printf '%s\n' "$*" >>"$DIR/audit"; }
go_tool_bin() { GO_TOOL_BIN="$DIR/broker"; }
run_host_colima() {
  printf '%s\n' "$*" >>"$DIR/colima"
  [[ "$1" != ssh-config ]] || printf 'Host colima-prof\n'
  [[ "$*" != *" -- stat "* ]] || printf '%s\n' "${STAT:-65532:65532:600}"
}
` + body
	cmd := exec.Command("bash", "-c", script)
	cmd.Env = append(os.Environ(), append([]string{"ROOT=" + root, "DIR=" + dir, "TMPDIR=" + dir}, env...)...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// TestStartCredentialBrokerKeepsTheTokenOutOfArgv starts the broker against a
// fake binary: the token reaches it on fd 3 only, the stale guest socket goes
// before the forward, the socket goes to the sidecar uid after it appears, and
// the sidecar arguments are the reviewed fixed values.
func TestStartCredentialBrokerKeepsTheTokenOutOfArgv(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sessions"), 0o700); err != nil {
		t.Fatal(err)
	}
	// The broker runs under env -i, so the fake writes beside itself.
	broker := "#!/bin/bash\nd=\"${0%/*}\"\nprintf '%s\\n' \"$@\" >\"$d/argv\"\ncat <&3 >\"$d/token.tmp\"\nmv \"$d/token.tmp\" \"$d/token\"\nexec sleep 30\n"
	if err := os.WriteFile(filepath.Join(dir, "broker"), []byte(broker), 0o700); err != nil {
		t.Fatal(err)
	}
	out, err := brokerLaunchProbe(t, dir, `
append_session_control_audit_record() { printf 'audit %s\n' "$*" >>"$DIR/colima"; }
start_credential_broker
until [[ -s "$DIR/token" ]]; do sleep 0.05; done
read -r pid _ <"$DIR/sessions/S1.broker"
kill "$pid"
printf 'token=%s\n' "$CREDENTIAL_BROKER_TOKEN"
printf 'docker=%s\n' "${EGRESS_PROXY_BROKER_DOCKER_ARGS[*]}"
printf 'proxy=%s\n' "${EGRESS_PROXY_BROKER_PROXY_ARGS[*]}"
`)
	if err != nil {
		t.Fatalf("start_credential_broker failed: %v\n%s", err, out)
	}
	token := strings.TrimPrefix(strings.Split(out, "\n")[0], "token=")
	if len(token) != 64 {
		t.Fatalf("token = %q\n%s", token, out)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "token")); string(got) != token+"\n" {
		t.Fatalf("broker read %q on fd 3, want the session token", got)
	}
	argv, _ := os.ReadFile(filepath.Join(dir, "argv"))
	if strings.Contains(string(argv), token) || !strings.Contains(string(argv), "--guest-socket\n/tmp/workcell-broker-S1.sock\n") ||
		strings.Contains(string(argv), "--keychain") {
		t.Fatalf("broker argv = %q", argv)
	}
	for _, want := range []string{
		"docker=--tmpfs /run/workcell-egress:rw,noexec,nosuid,nodev,size=64k,uid=65532,gid=65532,mode=0700 -v /tmp/workcell-broker-S1.sock:/run/workcell-broker.sock -e WORKCELL_BROKER_TOKEN\n",
		"proxy=-terminate api.anthropic.com=x-api-key=wcph-0123456789abcdef0123456789abcdef -ca-out /run/workcell-egress/ca.pem -ca-ttl 24h -broker-socket /run/workcell-broker.sock\n",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output lacks %q\n%s", want, out)
		}
	}
	colima, _ := os.ReadFile(filepath.Join(dir, "colima"))
	want := "ssh-config --profile prof\nssh --profile prof -- sudo rm -f -- /tmp/workcell-broker-S1.sock\n" +
		"ssh --profile prof -- test -S /tmp/workcell-broker-S1.sock\nssh --profile prof -- sudo chown 65532:65532 -- /tmp/workcell-broker-S1.sock\n" +
		"ssh --profile prof -- sudo chmod 0600 -- /tmp/workcell-broker-S1.sock\nssh --profile prof -- stat -c %u:%g:%a -- /tmp/workcell-broker-S1.sock\n" +
		"audit prof S1 credential_broker_start key=claude_api_key item=workcell-claude-api-key\n"
	if string(colima) != want {
		t.Fatalf("colima calls = %q, want %q", colima, want)
	}
}

// TestCredentialBrokerTestKeychainIsHarnessOnly proves that the test keychain
// knob is refused outside the allowed harness, before any broker work, and that
// the launcher scrubs it without the sanitized-entrypoint marker.
func TestCredentialBrokerTestKeychainIsHarnessOnly(t *testing.T) {
	dir := t.TempDir()
	for _, marker := range []string{"0", "1"} {
		out, err := brokerLaunchProbe(t, dir, "start_credential_broker\n",
			"WORKCELL_TEST_CREDENTIAL_BROKER_KEYCHAIN=/tmp/smoke.keychain-db", "WORKCELL_VERIFY_INVARIANTS_SANITIZED_ENTRYPOINT="+marker)
		if err == nil || !strings.Contains(out, "reserved for the Workcell test harness") {
			t.Fatalf("marker %s: knob accepted: %v\n%s", marker, err, out)
		}
		if _, statErr := os.Stat(filepath.Join(dir, "colima")); statErr == nil {
			t.Fatalf("marker %s: broker work ran before the refusal", marker)
		}
	}
	scrub := launcherFunction(t, "scrub_host_process_env")
	cmd := exec.Command("bash", "-c", scrub+"\nscrub_host_process_env\nprintf '%s' \"${WORKCELL_TEST_CREDENTIAL_BROKER_KEYCHAIN-unset}\"")
	cmd.Env = append(os.Environ(), "WORKCELL_TEST_CREDENTIAL_BROKER_KEYCHAIN=/tmp/smoke.keychain-db")
	if out, err := cmd.CombinedOutput(); err != nil || string(out) != "unset" {
		t.Fatalf("scrub kept the knob: %v %q", err, out)
	}
}

// TestStopCredentialBrokerSignalsOnlyThisSessionBroker proves that the stop
// finds this session's broker by its argv words, with or without the state
// file, signals only it, removes the guest socket, publishes the ledger and
// removes the private directory only when it has the private shape. Another
// session's broker, or this session's socket inside another argument, is left
// alone. A stop that found a broker appends the ledger summary, or
// status=unreadable when no ledger was saved; a no-audit stop appends nothing.
func TestStopCredentialBrokerSignalsOnlyThisSessionBroker(t *testing.T) {
	const bin = "/x/workcell-hostutil-abc "
	const serve = bin + "credential-broker-cli serve --socket $PRIVATE/b.sock "
	const s1 = "--guest-socket /tmp/workcell-broker-S1.sock --log l"
	for _, tc := range []struct {
		name, argv             string
		state, killed, cleaned bool
	}{
		{"state this session", serve + "--token-fd 3 " + s1, true, true, true},
		{"state other session", serve + "--guest-socket /tmp/workcell-broker-S2.sock", true, false, true},
		{"no state this session", serve + "--token-fd 3 " + s1, false, true, true},
		{"no state other session", serve + "--guest-socket /tmp/workcell-broker-S2.sock", false, false, false},
		{"socket inside another argument", serve + "--ssh-host x--guest-socket /tmp/workcell-broker-S1.sock --log l", false, false, false},
		{"not a serving broker", bin + "credential-broker-cli --socket $PRIVATE/b.sock " + s1, false, false, false},
		{"serve words inside a shell command", bin + "x credential-broker-cli serve --socket $PRIVATE/b.sock " + s1, false, false, false},
		{"another binary", "/bin/zsh credential-broker-cli serve --socket $PRIVATE/b.sock " + s1, false, false, false},
		{"unshaped socket directory", bin + "credential-broker-cli serve --socket $DIR/keep/b.sock " + s1, false, true, false},
	} {
		for _, audit := range []string{"audit", "no-audit"} {
			t.Run(tc.name+" "+audit, func(t *testing.T) {
				dir := t.TempDir()
				private := filepath.Join(dir, "wcb.AbC123")
				for _, d := range []string{filepath.Join(dir, "sessions"), private, filepath.Join(dir, "keep")} {
					if err := os.MkdirAll(d, 0o700); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.WriteFile(filepath.Join(private, "ledger.jsonl"), []byte("{\"result\":\"granted\"}\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				state := "0"
				if tc.state {
					state = "1"
				}
				out, err := brokerLaunchProbe(t, dir, `
argv="${ARGV//\$PRIVATE/$PRIVATE}"
argv="${argv//\$DIR/$DIR}"
(exec -a "$argv" sleep 30) &
pid=$!
[[ "$STATE" != 1 ]] || printf '%s %s\n' "$pid" "$PRIVATE" >"$DIR/sessions/S1.broker"
for _ in {1..40}; do [[ "$(ps -p "$pid" -o command=)" == *credential-broker-cli* ]] && break; sleep 0.05; done
credential_broker_find S1
printf 'found=[%s]\n' "$CREDENTIAL_BROKER_FOUND_DIR"
stop_credential_broker prof S1 "$AUDIT"
if state="$(ps -p "$pid" -o stat=)" && [[ "$state" != *Z* ]]; then echo alive; kill "$pid"; else echo killed; fi
`, "ARGV="+tc.argv, "PRIVATE="+private, "STATE="+state, "AUDIT="+audit)
				if err != nil {
					t.Fatalf("stop_credential_broker failed: %v\n%s", err, out)
				}
				found := "found=[]"
				if tc.killed && tc.cleaned {
					found = "found=[" + private + "]"
				}
				if !strings.Contains(out, found+"\n") {
					t.Fatalf("found directory: want %s\n%s", found, out)
				}
				if killed := strings.Contains(out, "killed"); killed != tc.killed {
					t.Fatalf("killed = %v, want %v\n%s", killed, tc.killed, out)
				}
				colima, _ := os.ReadFile(filepath.Join(dir, "colima"))
				if want := tc.killed || tc.state; (string(colima) == "ssh --profile prof -- sudo rm -f -- /tmp/workcell-broker-S1.sock\n") != want {
					t.Fatalf("colima calls = %q, want the guest socket removal %v", colima, want)
				}
				ledger, _ := os.ReadFile(filepath.Join(dir, "sessions", "S1.broker.jsonl"))
				_, privateErr := os.Stat(private)
				if (string(ledger) == "{\"result\":\"granted\"}\n") != tc.cleaned || os.IsNotExist(privateErr) != tc.cleaned {
					t.Fatalf("ledger = %q, private dir error = %v, want cleaned %v", ledger, privateErr, tc.cleaned)
				}
				for _, kept := range []string{filepath.Join(dir, "keep")} {
					if _, err := os.Stat(kept); err != nil {
						t.Fatalf("%s removed: %v", kept, err)
					}
				}
				if _, err := os.Stat(filepath.Join(dir, "sessions", "S1.broker")); !os.IsNotExist(err) {
					t.Fatalf("state file remains: %v", err)
				}
				want := ""
				switch {
				case audit == "no-audit" || !(tc.killed || tc.state):
				case tc.cleaned:
					want = "prof S1 credential_broker_summary status=complete granted=1 denied=0 malformed_lines=0\n"
				default:
					want = "prof S1 credential_broker_summary status=unreadable\n"
				}
				if got, _ := os.ReadFile(filepath.Join(dir, "audit")); string(got) != want {
					t.Fatalf("audit records = %q, want %q", got, want)
				}
			})
		}
	}
}

// TestStartCredentialBrokerRollsBackEveryFailure fails each step of the start
// in turn. Every failure after the private directory exists stops a started
// broker, removes the guest socket and the private directory, and clears the
// token, so a failed launch leaves no forward and no resolved key behind.
func TestStartCredentialBrokerRollsBackEveryFailure(t *testing.T) {
	broker := "#!/bin/bash\n[[ ! -f \"${0%/*}/exit-now\" ]] || exit 3\nexec -a \"fakebroker ${0%/*} \" sleep 30\n"
	for _, fail := range []string{"ssh-config", "state", "publish", "exit", "timeout", "chown", "chmod", "stat", "mode", "owner", "audit"} {
		t.Run(fail, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.MkdirAll(filepath.Join(dir, "sessions"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "broker"), []byte(broker), 0o700); err != nil {
				t.Fatal(err)
			}
			if fail == "exit" {
				if err := os.WriteFile(filepath.Join(dir, "exit-now"), nil, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			out, err := brokerLaunchProbe(t, dir, `
sleep() { command sleep 0.05; SECONDS=$((SECONDS + 10)); }
go_tool_bin() {
  GO_TOOL_BIN="$DIR/broker"
  [[ "$FAIL" != state ]] || mkdir "${CREDENTIAL_BROKER_DIR}/state"
}
go_hostutil() {
  case "$2" in
    credential-broker-plan) printf 'credential=claude_api_key=keychain:workcell-claude-api-key\naudit=credential_broker_start key=claude_api_key\n' ;;
    publish-session-capture-file) [[ "$FAIL" != publish ]] && cp "$3" "$4" ;;
    *) return 9 ;;
  esac
}
append_session_control_audit_record() { [[ "$FAIL" != audit ]]; }
run_host_colima() {
  printf '%s\n' "$*" >>"$DIR/colima"
  case "$FAIL $*" in
    "ssh-config ssh-config"* | "timeout ssh --profile prof -- test -S"* | "exit ssh --profile prof -- test -S"* | "chown ssh --profile prof -- sudo chown"*) return 1 ;;
    "chmod ssh --profile prof -- sudo chmod"* | "stat ssh --profile prof -- stat"*) return 1 ;;
    "mode ssh --profile prof -- stat"*) printf '65532:65532:660\n' ;;
    "owner ssh --profile prof -- stat"*) printf '0:0:600\n' ;;
    *" -- stat "*) printf '65532:65532:600\n' ;;
  esac
  [[ "$1" != ssh-config ]] || printf 'Host colima-prof\n'
}
if start_credential_broker; then echo started; fi
printf 'token=[%s] dir=[%s]\n' "$CREDENTIAL_BROKER_TOKEN" "$CREDENTIAL_BROKER_DIR"
# A broker that was not stopped would sleep for 30 seconds.
for _ in {1..40}; do
  running="$(ps -axo command=)"
  grep -qF -- "fakebroker $DIR " <<<"$running" || break
  command sleep 0.05
done
if grep -qF -- "fakebroker $DIR " <<<"$running"; then echo alive; fi
`, "FAIL="+fail)
			if err != nil || strings.Contains(out, "started") || !strings.Contains(out, "token=[] dir=[]") {
				t.Fatalf("start did not fail and roll back: %v\n%s", err, out)
			}
			if strings.Contains(out, "alive") {
				t.Fatalf("the broker still runs after the failed start\n%s", out)
			}
			colima, _ := os.ReadFile(filepath.Join(dir, "colima"))
			if !strings.HasSuffix(string(colima), "ssh --profile prof -- sudo rm -f -- /tmp/workcell-broker-S1.sock\n") {
				t.Fatalf("guest socket not removed last: %q", colima)
			}
			if left, _ := filepath.Glob(filepath.Join(dir, "wcb.*")); len(left) != 0 {
				t.Fatalf("private directory left behind: %v", left)
			}
		})
	}
}

// TestBrokerSocketAndTokenReachOnlyTheSidecar runs the real broker start and
// sidecar start with a recording docker client. Only the sidecar create gets
// the guest socket mount and the token, and the token rides only that call's
// environment. The agent command gets neither the socket, the token name nor
// the token value; it gets only the placeholder that the bundle stages.
func TestBrokerSocketAndTokenReachOnlyTheSidecar(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sessions"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "broker"), []byte("#!/bin/bash\nexec sleep 30\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	out, err := brokerLaunchProbe(t, dir, `
EGRESS_PROXY_NETWORK=net EGRESS_PROXY_CONTAINER=side ALLOW_ENDPOINTS=api.anthropic.com:443 HOST_DOCKER_BIN=docker IMAGE_TAG=img
DOCKER_RUN=(docker run --add-host api.anthropic.com:egress-proxy-ip img claude)
go_hostutil() {
  case "$2" in
    credential-broker-plan) printf 'terminate=api.anthropic.com=x-api-key=%s\ncredential=claude_api_key=keychain:workcell-claude-api-key\n' "$4" ;;
    publish-session-capture-file) cp "$3" "$4" ;;
    publish-egress-ca) ;;
    *) return 9 ;;
  esac
}
run_workcell_docker_client_command() {
  shift
  printf '%s|%s\n' "${WORKCELL_BROKER_TOKEN:-}" "$*" >>"$DIR/docker"
  case "$1 $2" in
    "network inspect") printf '10.9.0.0/24 \n' ;;
    "inspect -f") printf '10.9.0.2\n' ;;
    "exec side") [[ "$3" != cat || "$4" != /proc/net/tcp ]] || printf '  0: 0200090A:01BB 00000000:0000 0A 0\n' ;;
  esac
}
start_credential_broker
token="$CREDENTIAL_BROKER_TOKEN"
start_egress_proxy sha256:abc
read -r pid _ <"$DIR/sessions/S1.broker"
kill "$pid"
printf 'token=%s\n' "$token"
printf 'agent=%s\n' "${DOCKER_RUN[*]}"
`)
	if err != nil {
		t.Fatalf("probe failed: %v\n%s", err, out)
	}
	lines := strings.Split(out, "\n")
	token := strings.TrimPrefix(lines[0], "token=")
	if lines[1] != "agent=docker run --add-host api.anthropic.com:10.9.0.2 img claude" || len(token) != 64 {
		t.Fatalf("agent command or token = %q", out)
	}
	calls, _ := os.ReadFile(filepath.Join(dir, "docker"))
	for _, call := range strings.Split(strings.TrimSuffix(string(calls), "\n"), "\n") {
		env, args, _ := strings.Cut(call, "|")
		create := strings.HasPrefix(args, "create --name side ")
		if (env != "") != create || (env != "" && env != token) {
			t.Fatalf("token environment on the wrong call: %q", call)
		}
		if strings.Contains(args, "/run/workcell-broker.sock") != create || strings.Contains(args, "WORKCELL_BROKER_TOKEN") != create ||
			strings.Contains(args, token) {
			t.Fatalf("broker socket or token on the wrong call: %q", call)
		}
	}
}

// TestStopEgressProxyForSessionStopsAnOrphanBroker proves that a session with
// no proxy network still gets its broker stopped: a launch that died after the
// broker start and before the network create leaves only the broker.
func TestStopEgressProxyForSessionStopsAnOrphanBroker(t *testing.T) {
	out, err := brokerLaunchProbe(t, t.TempDir(), `
egress_proxy_network_for_session() { :; }
stop_credential_broker() { printf 'broker %s\n' "$*"; }
stop_egress_proxy() { printf 'proxy %s\n' "$*"; }
stop_egress_proxy_for_session prof S9 no-audit
stop_egress_proxy_for_session prof S9
`)
	if err != nil || out != "broker prof S9 no-audit\nbroker prof S9 audit\n" {
		t.Fatalf("stop_egress_proxy_for_session = %q, %v; want only the broker stop", out, err)
	}
}

// TestStopEgressProxyPassesTheAuditModeToTheBroker proves that the proxy stop,
// which every sealing path runs before the seal, has the broker append its
// summary, and that a no-audit stop (session delete) passes no-audit on.
func TestStopEgressProxyPassesTheAuditModeToTheBroker(t *testing.T) {
	dir := t.TempDir()
	out, err := brokerLaunchProbe(t, dir, `
EGRESS_PROXY_CONTAINER=side EGRESS_PROXY_NETWORK=net
run_profile_docker_command() { :; }
stop_credential_broker() { printf 'broker %s\n' "$*" >>"$DIR/audit"; }
stop_egress_proxy prof
stop_egress_proxy prof no-audit
`)
	got, _ := os.ReadFile(filepath.Join(dir, "audit"))
	if want := "broker prof S1 audit\nprof S1 egress_summary status=unreadable\nbroker prof S1 no-audit\n"; err != nil || string(got) != want {
		t.Fatalf("stop_egress_proxy steps = %q, %v; want %q\n%s", got, err, want, out)
	}
}

// TestCredentialBrokerTerminateKeepsUnshapedDirectories proves that the
// cleanup removes only an owned, real wcb.* directory: a directory of another
// shape and a wcb.* symlink to the private directory both stay.
func TestCredentialBrokerTerminateKeepsUnshapedDirectories(t *testing.T) {
	dir := t.TempDir()
	private := filepath.Join(dir, "wcb.AbC123")
	for _, d := range []string{private, filepath.Join(dir, "keep")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(private, filepath.Join(dir, "wcb.Sym123")); err != nil {
		t.Fatal(err)
	}
	out, err := brokerLaunchProbe(t, dir, `
credential_broker_terminate prof "" /tmp/workcell-broker-S1.sock "$DIR/keep" ""
credential_broker_terminate prof "" /tmp/workcell-broker-S1.sock "$DIR/wcb.Sym123" ""
`)
	if err != nil {
		t.Fatalf("credential_broker_terminate failed: %v\n%s", err, out)
	}
	for _, kept := range []string{private, filepath.Join(dir, "keep"), filepath.Join(dir, "wcb.Sym123")} {
		if _, err := os.Lstat(kept); err != nil {
			t.Fatalf("%s removed: %v", kept, err)
		}
	}
}

// TestSessionDeleteStopsAnOrphanBrokerWithoutANetwork is the case of issue
// 862: a launch that died after the broker start and before the network create
// leaves a broker, its state file and its ledger, and no proxy network. The
// delete stops the broker and removes the state file and the ledger; a dry run
// only reports them. A broker that already exited still leaves its state file,
// which the delete removes the same way. A broker that outlives the stop
// fails the delete and keeps the record.
func TestSessionDeleteStopsAnOrphanBrokerWithoutANetwork(t *testing.T) {
	deleteMain := launcherFunction(t, "session_delete_main") + launcherFunction(t, "csv_join_or_none")
	for _, tc := range []struct{ dry, live, stuck string }{{"0", "1", "0"}, {"1", "1", "0"}, {"0", "0", "0"}, {"0", "1", "1"}} {
		dry := tc.dry
		t.Run("dry_run="+dry+" live="+tc.live+" stuck="+tc.stuck, func(t *testing.T) {
			dir := t.TempDir()
			sessions := filepath.Join(dir, "sessions")
			private := filepath.Join(dir, "wcb.AbC123")
			for _, d := range []string{sessions, private} {
				if err := os.MkdirAll(d, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			for name, data := range map[string]string{
				filepath.Join(sessions, "S1.json"):         "{}\n",
				filepath.Join(sessions, "S1.broker.jsonl"): "{}\n",
				filepath.Join(private, "ledger.jsonl"):     "{}\n",
			} {
				if err := os.WriteFile(name, []byte(data), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			out, err := brokerLaunchProbe(t, dir, `
source "$ROOT/scripts/lib/shellproto.sh"
`+deleteMain+`
if [[ "$LIVE" == 1 ]]; then
  (exec -a "/x/workcell-hostutil-abc credential-broker-cli serve --socket $DIR/wcb.AbC123/b.sock --guest-socket /tmp/workcell-broker-S1.sock" sleep 30) &
  pid=$!
else
  (exit 0) &
  pid=$!
  wait "$pid"
fi
printf '%s %s\n' "$pid" "$DIR/wcb.AbC123" >"$DIR/sessions/S1.broker"
for _ in {1..40}; do [[ "$LIVE" == 0 || "$(ps -p "$pid" -o command=)" == *credential-broker-cli* ]] && break; sleep 0.05; done
session_run_cli_with_roots() { printf 'session_id=S1\nrecord_only=0\ndry_run=%s\n' "$DRY"; }
load_session_runtime_metadata() {
  SESSION_META_PROFILE=prof SESSION_META_STATUS=failed SESSION_META_LIVE_STATUS="" SESSION_META_MONITOR_PID=""
  SESSION_META_RECORD_PATH="$DIR/sessions/S1.json" SESSION_META_CONTAINER_NAME=c SESSION_META_SESSION_AUDIT_DIR=""
}
session_is_terminal_status() { return 0; }
profile_docker_transport_available() { return 0; }
resolve_host_tool() { :; }
sanitize_host_docker_env() { :; }
session_container_state_for_delete() { printf 'missing\n'; }
egress_proxy_network_for_session() { :; }
session_delete_resolve_dir_artifact() { return 1; }
session_delete_resolve_file_artifact() { return 1; }
[[ "$STUCK" != 1 ]] || stop_credential_broker() { :; }
if (session_delete_main --id S1); then echo deleted; else echo refused; fi
if state="$(ps -p "$pid" -o stat=)" && [[ "$state" != *Z* ]]; then echo alive; kill "$pid"; else echo stopped; fi
`, "DRY="+dry, "LIVE="+tc.live, "STUCK="+tc.stuck)
			if err != nil {
				t.Fatalf("session delete failed: %v\n%s", err, out)
			}
			_, stateErr := os.Stat(filepath.Join(sessions, "S1.broker"))
			_, ledgerErr := os.Stat(filepath.Join(sessions, "S1.broker.jsonl"))
			if tc.stuck == "1" {
				_, recordErr := os.Stat(filepath.Join(sessions, "S1.json"))
				if !strings.Contains(out, "could not confirm the credential broker cleanup") || !strings.Contains(out, "refused\n") || recordErr != nil {
					t.Fatalf("a broker that outlived the stop did not fail the delete: record %v\n%s", recordErr, out)
				}
				return
			}
			if dry == "1" {
				if !strings.Contains(out, "would_remove=record,credential_broker,credential_broker_ledger\n") ||
					!strings.Contains(out, "alive\n") || stateErr != nil || ledgerErr != nil {
					t.Fatalf("dry run changed or missed the broker: state %v, ledger %v\n%s", stateErr, ledgerErr, out)
				}
				return
			}
			if !strings.Contains(out, "removed=record,credential_broker,credential_broker_ledger\n") || !strings.Contains(out, "stopped\n") ||
				!os.IsNotExist(stateErr) || !os.IsNotExist(ledgerErr) {
				t.Fatalf("delete left the broker: state %v, ledger %v\n%s", stateErr, ledgerErr, out)
			}
			if _, err := os.Stat(private); !os.IsNotExist(err) {
				t.Fatalf("private directory remains: %v", err)
			}
		})
	}
}
