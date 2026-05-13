// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package sessionctl

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/omkhar/workcell/internal/host/sessions"
)

// AttachMain implements the validation half of `workcell session attach
// --id SESSION_ID [--no-stdin]`, the Go translation of session_attach_main
// in scripts/workcell.
//
// The bash function's body splits cleanly in two:
//
//  1. Up-front parsing plus session-record validation - profile and
//     container_name must be non-empty and the record must describe a
//     detached session (monitor_pid present).  These are all data
//     checks against the on-disk session JSON, so they live here.
//
//  2. Live-container preflight, audit emission and the actual
//     `docker attach` invocation.  Those still need `run_profile_docker_command`
//     and `append_session_control_audit_record`, which are sourced
//     bash helpers that tests/scenarios/shared/test-session-commands.sh
//     mocks via `bash -lc "source workcell; ..."`.  AttachMain leaves
//     phase 2 to the bash shim so the existing mock surface keeps
//     working and an interactive `docker attach` still has the host
//     tty.
//
// AttachMain emits a plan on stdout for the bash shim to consume:
//
//	session_id=<id>
//	no_stdin=0|1
//	profile=<profile>
//	container_name=<container>
//
// State-root forwarding mirrors LogsMain/TimelineMain: leading
// --root=PATH args are consumed via consumeRootArgs because go_hostutil
// scrubs WORKCELL_STATE_ROOT/COLIMA_STATE_ROOT from the environment.
func AttachMain(args []string) error {
	return attachMain(args, os.Stdout)
}

func attachMain(args []string, stdout io.Writer) error {
	roots, rest := consumeRootArgs(args)
	sessionID, noStdin, showHelp, err := parseAttachArgs(rest)
	if err != nil {
		return err
	}
	if showHelp {
		fmt.Fprint(stdout, UsageText())
		return nil
	}
	if sessionID == "" {
		return errors.New("workcell session attach requires --id.")
	}

	if len(roots) == 0 {
		roots = lookupRoots()
	}
	record, err := sessions.FindSessionRecordInRoots(roots, sessionID)
	if err != nil {
		return err
	}

	if record.Profile == "" {
		return fmt.Errorf("session attach record is missing a profile: %s", sessionID)
	}
	if !sessionIsDetached(record) {
		return fmt.Errorf("session attach only works for detached sessions started with 'workcell session start': %s\nUse 'workcell session list' to check the control column; attached records are not stoppable.", sessionID)
	}
	if record.ContainerName == "" {
		return fmt.Errorf("session attach record is missing a container name: %s", sessionID)
	}

	noStdinFlag := 0
	if noStdin {
		noStdinFlag = 1
	}
	fmt.Fprintf(stdout, "session_id=%s\n", record.SessionID)
	fmt.Fprintf(stdout, "no_stdin=%d\n", noStdinFlag)
	fmt.Fprintf(stdout, "profile=%s\n", record.Profile)
	fmt.Fprintf(stdout, "container_name=%s\n", record.ContainerName)
	return nil
}

func parseAttachArgs(args []string) (sessionID string, noStdin, showHelp bool, err error) {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--id":
			if i+1 >= len(args) || args[i+1] == "" {
				return "", false, false, fmt.Errorf("--id requires a non-empty value")
			}
			sessionID = args[i+1]
			i++
		case "--no-stdin":
			noStdin = true
		case "-h", "--help":
			showHelp = true
		default:
			return "", false, false, fmt.Errorf("Unsupported workcell session attach option: %s", args[i])
		}
	}
	return sessionID, noStdin, showHelp, nil
}

// sessionIsDetached mirrors scripts/workcell's
// session_require_detached_runtime_metadata: a session record is
// detached iff monitor_pid is set (legacy live-detached records that
// lack monitor_pid are accepted by the bash helper only when called
// with allow_legacy_live=1, but here we are not in a recovery path).
func sessionIsDetached(record sessions.SessionRecord) bool {
	return record.MonitorPID != ""
}
