// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package testkit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runtimeUserSource(tb testing.TB) string {
	tb.Helper()

	body, err := os.ReadFile(filepath.Join(repoRoot(tb), "runtime", "container", "runtime-user.sh"))
	if err != nil {
		tb.Fatal(err)
	}
	return string(body)
}

// The mapped runtime identity used to come with a NOPASSWD sudoers grant on the
// package helper, because the shell broker needed the caller to reach root
// through sudo. The broker socket carries that privilege now, so the grant is
// standing authority that nothing spends: a session-scoped file naming a root
// command that any process running as the mapped uid could invoke.
func TestPrepareRuntimeIdentityGrantsNoSudoAuthority(t *testing.T) {
	t.Parallel()

	source := runtimeUserSource(t)
	for _, forbidden := range []string{
		"/etc/sudoers.d",
		"NOPASSWD",
		"workcell-runtime-user",
	} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("runtime-user.sh still writes a sudo grant: %q", forbidden)
		}
	}
}

// Startup is the server's own responsibility: --start lays out the socket
// directory, launches the detached server, and returns only after the readiness
// handshake and socket validation both pass. A shell that polled for a pid file
// could not tell "not started yet" from "failed", so this asserts the script
// asks the binary rather than reintroducing a poll loop.
func TestStartAptBrokerDelegatesToTheServerBinary(t *testing.T) {
	t.Parallel()

	source := runtimeUserSource(t)
	for _, required := range []string{
		`WORKCELL_APT_BROKER_SERVER="/usr/local/libexec/workcell/workcell-apt-broker-server"`,
		`"${WORKCELL_APT_BROKER_SERVER}" --start --peer-uid "${uid}"`,
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("runtime-user.sh no longer starts the broker server: %q", required)
		}
	}
}

// The shell broker and its spool protocol are gone. A leftover reference would
// mean some path still expects a request directory, a results directory, or a
// pid file that nothing publishes any more.
func TestRuntimeUserKeepsNoShellBrokerState(t *testing.T) {
	t.Parallel()

	source := runtimeUserSource(t)
	for _, forbidden := range []string{
		"apt-broker.sh",
		"WORKCELL_APT_BROKER_ROOT",
		"WORKCELL_APT_BROKER_REQUESTS_DIR",
		"WORKCELL_APT_BROKER_RESULTS_DIR",
		"WORKCELL_APT_BROKER_PID_FILE",
		"workcell_apt_broker_running",
		"workcell_wait_for_apt_broker",
	} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("runtime-user.sh still carries shell broker state: %q", forbidden)
		}
	}
}
