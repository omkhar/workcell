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

// runtimeStateValue runs the real workcell_runtime_state_value from
// runtime-user.sh against one mode-state file and returns its output and
// whether it succeeded.
func runtimeStateValue(t *testing.T, stateFile string) (string, bool) {
	t.Helper()

	source := runtimeUserSource(t)
	start := strings.Index(source, "\nworkcell_runtime_state_value() {\n")
	if start < 0 {
		t.Fatal("runtime-user.sh no longer defines workcell_runtime_state_value")
	}
	end := strings.Index(source[start:], "\n}\n")
	if end < 0 {
		t.Fatal("workcell_runtime_state_value has no closing brace")
	}
	script := source[start:start+end+3] +
		"WORKCELL_RUNTIME_MODE_FILE=\"$1\"\nworkcell_runtime_state_value WORKCELL_MODE\n"

	cmd := exec.Command("bash", "-c", script, "bash", stateFile)
	// The runtime is Linux; give a macOS host GNU stat so the check is real.
	if exec.Command("stat", "-c", "%u", "/").Run() != nil {
		gstat, err := exec.LookPath("gstat")
		if err != nil {
			t.Skip("GNU stat is not available")
		}
		shim := t.TempDir()
		if err := os.Symlink(gstat, filepath.Join(shim, "stat")); err != nil {
			t.Fatal(err)
		}
		cmd.Env = append(os.Environ(), "PATH="+shim+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
	out, err := cmd.Output()
	return string(out), err == nil
}

// In a readonly session /run/workcell is a tmpfs that the mapped agent uid
// owns, and no root phase writes the state files. The provider wrappers prefer
// a state file over the PID1 environment, so a planted file must not be
// trusted: only root writes real session state.
func TestRuntimeStateValueRejectsNonRootOwnedFile(t *testing.T) {
	t.Parallel()

	if os.Geteuid() == 0 {
		t.Skip("needs a non-root uid to plant a non-root-owned state file")
	}
	planted := filepath.Join(t.TempDir(), "mode")
	if err := os.WriteFile(planted, []byte("build\n"), 0o444); err != nil {
		t.Fatal(err)
	}
	if out, ok := runtimeStateValue(t, planted); ok || out != "" {
		t.Fatalf("planted state file was trusted: ok=%v out=%q", ok, out)
	}

	// Negative control: a root-owned file is still read.
	if out, ok := runtimeStateValue(t, "/etc/passwd"); !ok || out == "" {
		t.Fatalf("root-owned state file was refused: ok=%v out=%q", ok, out)
	}
}
