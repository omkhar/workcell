// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package sessionctl

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/omkhar/workcell/internal/cliexit"
	"github.com/omkhar/workcell/internal/host/stateroot"
	"github.com/omkhar/workcell/internal/shellproto"
)

// MonitorMain implements the option-parsing and state-file validation
// half of `workcell session monitor --state-file PATH`, the Go translation
// of session_monitor_main in scripts/workcell.
//
// Like StopMain/AttachMain, MonitorMain is the host-side parsing layer
// of a hybrid translation.  session_monitor_main's body splits into:
//
//  1. Option parsing for --state-file plus the existence check for the
//     referenced file, then reading and parsing the file as a simple
//     KEY=VALUE env fragment so the bash shim can re-export the values
//     it needs (SESSION_ID, COLIMA_PROFILE, CONTAINER_NAME,
//     EXECUTION_PATH, SESSION_AUDIT_DIR, WORKCELL_STATE_ROOT,
//     COLIMA_STATE_ROOT, optional SESSION_MONITOR_READY_PATH, etc.).
//     That half lives here.
//
//  2. load_session_runtime_metadata, sanitize_host_docker_env, the
//     SESSION_MONITOR_READY_PATH probe touch, session_monitor_wait_status
//     against `docker wait`, capture_session_audit_state /
//     capture_session_file_trace / finalize_session_audit emission, the
//     `docker rm -f` cleanup, and the session_audit_dir teardown.  Those
//     still depend on sourced bash helpers that
//     tests/scenarios/shared/test-session-commands.sh mocks via
//     `bash -lc "source workcell; ..."` (the docker-desktop provider
//     monitor case at line 884 of the test script overrides
//     run_workcell_docker_client_command to return a canned wait/inspect
//     transcript).  Phase 2 therefore stays in the bash shim.
//
// MonitorMain emits the parsed state-file body on stdout one
// state_<KEY>=<VALUE> line at a time so the bash shim can drive a read
// loop and re-export each entry.  The bash side keeps owning the
// file-sourcing semantics because some entries are quoted shell tokens
// and we only forward the few keys the monitor actually consumes; the
// shim still falls back to bash `source` when MonitorMain reports a
// state file that exists.
//
// Usage errors (--state-file missing, unknown flag, missing file) exit
// with code 2 to match the bash CLI surface.  The
// "Missing detached session monitor state file" diagnostic is preserved
// byte-for-byte so the docker-desktop provider monitor test grep keeps
// matching.
func MonitorMain(args []string) error {
	return monitorMain(args, os.Stdout, os.Stderr)
}

func monitorMain(args []string, stdout, stderr io.Writer) error {
	// State-root forwarding mirrors the other session_*_main shims:
	// leading --root=PATH args are consumed here because the shared
	// scripts/lib/sessionctl-shim.sh helper always prepends
	// session_lookup_root_args output before forwarding to go_hostutil.
	// MonitorMain itself does not need the roots (it operates only on
	// the explicit --state-file path the bash detached-start writer
	// produced), but the contract stays consistent with the rest of
	// the session_* dispatch surface.
	_, rest := stateroot.ConsumeRootArgs(args)
	statePath, showHelp, err := parseMonitorArgs(rest)
	if err != nil {
		return err
	}
	if showHelp {
		// Usage banner goes to stderr so the bash shim, which captures
		// stdout into `$plan`, surfaces it to the user instead of
		// swallowing it.
		fmt.Fprint(stderr, UsageText())
		return nil
	}
	if statePath == "" {
		return &cliexit.ExitCodeError{Code: 2, Message: "workcell session monitor requires --state-file."}
	}
	if err := rejectControlChars("session monitor", "--state-file", statePath); err != nil {
		return err
	}

	info, err := os.Stat(statePath)
	if err != nil || info.IsDir() {
		return &cliexit.ExitCodeError{
			Code:    2,
			Message: fmt.Sprintf("Missing detached session monitor state file: %s", statePath),
		}
	}

	return shellproto.WriteField(stdout, "state_file", statePath)
}

// parseMonitorArgs walks the bash session_monitor_main option loop.
//
// --state-file mirrors option_value_or_die: the value must be non-empty.
//
// -h / --help mark help output for the caller.  Bash session_monitor_main
// does not document -h/--help today but every other session_*_main shim
// accepts the flag pair so we add it here for symmetry with the rest of
// the package and so the launcher subcommand has a documented exit path.
//
// Unknown options return an Unsupported-style error matching the bash
// branch so the user-visible stderr stays byte-identical, wrapped in an
// ExitCodeError so the launcher exits 2.
func parseMonitorArgs(args []string) (statePath string, showHelp bool, err error) {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--state-file":
			v, ni, perr := optionValueOrError(args, i, "--state-file")
			if perr != nil {
				return "", false, perr
			}
			statePath = v
			i = ni
		case "-h", "--help":
			showHelp = true
		default:
			return "", false, unsupportedOption("session monitor", args[i])
		}
	}
	return statePath, showHelp, nil
}

// MonitorStateEntries parses a session-monitor state file as a sequence
// of simple KEY=VALUE assignments.  Lines that are blank, comments, or
// have an empty key are skipped.  Surrounding double or single quotes
// are stripped from the value so callers receive the same string the
// bash `source` would set in the environment for the common cases the
// monitor uses (the state file is written from scripts/workcell with no
// shell metacharacters).
//
// The function is exposed for the launcher's monitor subcommand entry
// point in cmd/workcell-hostutil and for tests in this package.
func MonitorStateEntries(path string) ([]MonitorStateEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	entries := []MonitorStateEntry{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		raw := strings.TrimSpace(scanner.Text())
		if raw == "" || strings.HasPrefix(raw, "#") {
			continue
		}
		key, value, ok := strings.Cut(raw, "=")
		if !ok || key == "" {
			continue
		}
		entries = append(entries, MonitorStateEntry{Key: key, Value: trimQuotes(value)})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}

// MonitorStateEntry is one KEY=VALUE pair parsed from a session-monitor
// state file.
type MonitorStateEntry struct {
	Key   string
	Value string
}

func trimQuotes(value string) string {
	if len(value) >= 2 {
		first := value[0]
		last := value[len(value)-1]
		if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
			return value[1 : len(value)-1]
		}
	}
	return value
}
