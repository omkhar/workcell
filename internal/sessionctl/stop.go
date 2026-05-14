// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package sessionctl

import (
	"fmt"
	"io"
	"os"

	"github.com/omkhar/workcell/internal/cliexit"
	"github.com/omkhar/workcell/internal/host/sessions"
)

// StopMain implements the option-parsing and record-validation half of
// `workcell session stop --id SESSION_ID [--force]`, the Go translation
// of session_stop_main in scripts/workcell.
//
// Like AttachMain, StopMain is the host-side parsing layer of a hybrid
// translation.  session_stop_main's body splits into:
//
//  1. Option parsing (--id, --force) plus the up-front record load and
//     detached-session validation - profile and container_name must be
//     non-empty and the record must describe a detached session
//     (monitor_pid present).  Those are all on-disk data checks against
//     the session JSON, so they live here.
//
//  2. Live container state probes (session_container_live_state,
//     session_monitor_pid_is_live, session_container_exit_code), the
//     graceful->force-kill docker stop/kill transport, audit record
//     emission, stop-request marker management, and the
//     stopping/stopped session-record writes.  Those still depend on
//     sourced bash helpers that
//     tests/scenarios/shared/test-session-commands.sh mocks via
//     `bash -lc "source workcell; ..."`, and the docker invocations
//     need the host docker binary on the host PATH, so they stay in
//     the bash shim.
//
// StopMain emits a plan on stdout for the bash shim to consume:
//
//	session_id=<id>
//	force=0|1
//	profile=<profile>
//	container_name=<container>
//
// Usage errors (--id missing, unknown flag) exit with code 2 to match
// the bash CLI surface.  All other errors propagate to main() for the
// default exit-1 path.
//
// State-root forwarding mirrors AttachMain/LogsMain: leading
// --root=PATH args are consumed via consumeRootArgs because
// go_hostutil scrubs WORKCELL_STATE_ROOT/COLIMA_STATE_ROOT from the
// environment.
func StopMain(args []string) error {
	return stopMain(args, os.Stdout, os.Stderr)
}

func stopMain(args []string, stdout, stderr io.Writer) error {
	roots, rest := consumeRootArgs(args)
	sessionID, force, showHelp, err := parseStopArgs(rest)
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
	if sessionID == "" {
		return &cliexit.ExitCodeError{Code: 2, Message: "workcell session stop requires --id."}
	}
	if err := rejectControlChars("session stop", "--id", sessionID); err != nil {
		return err
	}

	if len(roots) == 0 {
		roots = lookupRoots()
	}
	record, err := sessions.FindSessionRecordInRoots(roots, sessionID)
	if err != nil {
		return err
	}

	if record.Profile == "" {
		return fmt.Errorf("session stop record is missing a profile: %s", sessionID)
	}
	if !sessionIsDetached(record) {
		return fmt.Errorf("session stop only works for detached sessions started with 'workcell session start': %s\nUse 'workcell session list' to check the control column; attached records are not stoppable.", sessionID)
	}
	if record.ContainerName == "" {
		return fmt.Errorf("session stop record is missing a container name: %s", sessionID)
	}

	forceFlag := 0
	if force {
		forceFlag = 1
	}
	fmt.Fprintf(stdout, "session_id=%s\n", record.SessionID)
	fmt.Fprintf(stdout, "force=%d\n", forceFlag)
	fmt.Fprintf(stdout, "profile=%s\n", record.Profile)
	fmt.Fprintf(stdout, "container_name=%s\n", record.ContainerName)
	return nil
}

// parseStopArgs walks the bash session_stop_main option loop.
//
// --id mirrors option_value_or_die: the value must be non-empty.
//
// --force is a boolean toggle that selects docker kill instead of
// docker stop in the bash transport half.
//
// -h / --help mark help output for the caller.
//
// Unknown options return an Unsupported-style error matching the bash
// branch so the user-visible stderr stays byte-identical, wrapped in
// an ExitCodeError so the launcher exits 2.
func parseStopArgs(args []string) (sessionID string, force, showHelp bool, err error) {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--id":
			v, ni, perr := optionValueOrError(args, i, "--id")
			if perr != nil {
				return "", false, false, perr
			}
			sessionID = v
			i = ni
		case "--force":
			force = true
		case "-h", "--help":
			showHelp = true
		default:
			return "", false, false, unsupportedOption("session stop", args[i])
		}
	}
	return sessionID, force, showHelp, nil
}
