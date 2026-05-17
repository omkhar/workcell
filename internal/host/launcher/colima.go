// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package launcher

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// ColimaTimeoutExitCode mirrors GNU coreutils `timeout`'s exit code for a
// process that was killed because its deadline elapsed.  Bash callers
// rely on this value to distinguish a timeout from a colima failure.
const ColimaTimeoutExitCode = 124

// HostColimaInvocation captures the environment used to invoke a
// trusted host `colima` binary on behalf of scripts/workcell.  The
// fields mirror the bash variables HOST_COLIMA_BIN, REAL_HOME,
// COLIMA_STATE_ROOT, and WORKCELL_HOST_COMMAND_CWD.
type HostColimaInvocation struct {
	// ColimaBin is the absolute path to the trusted colima binary.
	ColimaBin string
	// RealHome is the REAL_HOME value (the user's home directory).
	RealHome string
	// ColimaHome is the COLIMA_STATE_ROOT (will be exported as
	// COLIMA_HOME to the colima child process).
	ColimaHome string
	// CWD is the directory the colima process is launched from.
	// When empty, RealHome (then "/") is used.
	CWD string
	// Args are the positional arguments forwarded to colima.
	Args []string
}

// RunHostColima invokes the trusted colima binary with the supplied
// arguments.  The child process inherits stdin/stdout/stderr from the
// current process; on success the function returns 0.  On non-zero
// exit codes it returns the colima exit code and a nil error so
// callers may treat the result the same way as bash's `$?`.
func RunHostColima(inv HostColimaInvocation) (int, error) {
	if len(inv.Args) == 0 {
		return 0, nil
	}
	if inv.ColimaBin == "" {
		return 0, errors.New("RunHostColima: colima binary path is required")
	}
	cmd, err := newColimaCommand(context.Background(), inv)
	if err != nil {
		return 0, err
	}
	return runColimaCommand(cmd)
}

// RunHostColimaWithTimeout invokes the trusted colima binary with a
// deadline.  When timeoutSeconds is zero or negative the call falls
// through to RunHostColima with no timeout.  On timeout the function
// returns ColimaTimeoutExitCode (124) after killing the colima process
// group, matching the bash run_host_colima_with_timeout helper.
func RunHostColimaWithTimeout(timeoutSeconds int, inv HostColimaInvocation) (int, error) {
	if len(inv.Args) == 0 {
		return 0, nil
	}
	if timeoutSeconds <= 0 {
		return RunHostColima(inv)
	}
	if inv.ColimaBin == "" {
		return 0, errors.New("RunHostColimaWithTimeout: colima binary path is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSeconds)*time.Second)
	defer cancel()

	cmd, err := newColimaCommand(ctx, inv)
	if err != nil {
		return 0, err
	}
	// Place the child in its own process group so we can deliver
	// SIGKILL to the whole tree on timeout (mirroring the bash
	// helper's kill_process_tree_by_pid behaviour).
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return killColimaProcessGroup(cmd)
	}
	cmd.WaitDelay = 5 * time.Second

	code, runErr := runColimaCommand(cmd)
	if ctx.Err() == context.DeadlineExceeded {
		return ColimaTimeoutExitCode, nil
	}
	return code, runErr
}

// ValidateColimaStatusOutput checks that the textual output of
// `colima status --profile <profile>` advertises the configuration
// invariants workcell expects (virtualization framework, virtiofs
// mount, docker runtime).  It returns nil when the status text meets
// every requirement, or an error describing the first missing marker.
func ValidateColimaStatusOutput(status, profile string) error {
	if profile == "" {
		return errors.New("ValidateColimaStatusOutput: profile name is required")
	}
	checks := []struct {
		needle  string
		message string
	}{
		{"Virtualization.Framework", "Colima profile " + profile + " is not using Virtualization.Framework."},
		{"mountType: virtiofs", "Colima profile " + profile + " is not using virtiofs."},
		{"runtime: docker", "Colima profile " + profile + " is not using Docker runtime."},
	}
	for _, check := range checks {
		if !strings.Contains(status, check.needle) {
			return errors.New(check.message)
		}
	}
	return nil
}

func newColimaCommand(ctx context.Context, inv HostColimaInvocation) (*exec.Cmd, error) {
	cwd := inv.CWD
	if cwd == "" {
		cwd = inv.RealHome
	}
	if cwd == "" {
		cwd = "/"
	}
	if info, err := os.Stat(cwd); err != nil || !info.IsDir() {
		cwd = "/"
	}

	cmd := exec.CommandContext(ctx, inv.ColimaBin, inv.Args...)
	cmd.Dir = cwd
	cmd.Env = colimaChildEnv(inv)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd, nil
}

func colimaChildEnv(inv HostColimaInvocation) []string {
	// Start from the current process env (which the bash caller has
	// already cleansed via `env -i PATH=... HOME=... LC_ALL=C LANG=C`),
	// then override HOME and COLIMA_HOME with the trusted values
	// supplied by the launcher.  Mirrors the bash invocation:
	//   HOME=${REAL_HOME} COLIMA_HOME=${COLIMA_STATE_ROOT} colima "$@"
	base := os.Environ()
	out := make([]string, 0, len(base)+2)
	for _, entry := range base {
		if strings.HasPrefix(entry, "HOME=") || strings.HasPrefix(entry, "COLIMA_HOME=") {
			continue
		}
		out = append(out, entry)
	}
	if inv.RealHome != "" {
		out = append(out, "HOME="+inv.RealHome)
	}
	if inv.ColimaHome != "" {
		out = append(out, "COLIMA_HOME="+inv.ColimaHome)
	}
	return out
}

func runColimaCommand(cmd *exec.Cmd) (int, error) {
	err := cmd.Run()
	if err == nil {
		return 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return colimaExitCode(exitErr), nil
	}
	return 0, fmt.Errorf("colima invocation failed: %w", err)
}

func colimaExitCode(exitErr *exec.ExitError) int {
	if exitErr == nil {
		return 0
	}
	if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		return 128 + int(status.Signal())
	}
	return exitErr.ExitCode()
}

func killColimaProcessGroup(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	pid := cmd.Process.Pid
	// Negative pid targets the whole process group.  Ignore the
	// "no such process" error that arises if the child already exited
	// between the deadline firing and our kill call.
	if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}
