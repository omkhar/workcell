// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package sessionctl

import (
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/omkhar/workcell/internal/cliexit"
	"github.com/omkhar/workcell/internal/host/stateroot"
	"github.com/omkhar/workcell/internal/shellproto"
)

// SendMain implements the option-parsing half of `workcell session send
// --id SESSION_ID --message TEXT [--no-newline]`, the Go translation of
// session_send_main in scripts/workcell.
//
// Like AttachMain, SendMain is the host-side parsing layer of a hybrid
// translation.  The bash function's body splits into:
//
//  1. Up-front option parsing (--id, --message, --no-newline) with the
//     bash idioms option_value_or_die / raw_option_value_or_die.  This
//     half lives here.
//
//  2. Live-session metadata load (load_session_runtime_metadata), the
//     `run_profile_docker_command exec` transport call that injects the
//     payload into the detached container's stdin FIFO, audit emission,
//     and the loaded-session summary printout.  Those still depend on
//     sourced bash helpers that tests/scenarios/shared/test-session-commands.sh
//     mocks via `bash -lc "source workcell; ..."`, so they stay in the
//     bash shim.  Crucially, several of those test cases exercise the
//     bash function without populating an on-disk session record, so
//     SendMain deliberately does NOT call FindSessionRecordInRoots -
//     metadata validation is left to bash's load_session_runtime_metadata.
//
// SendMain emits a plan on stdout for the bash shim to consume:
//
//	session_id=<id>
//	message=<message>
//	append_newline=0|1
//
// State-root forwarding mirrors AttachMain/LogsMain: leading
// --root=PATH args are consumed via stateroot.ConsumeRootArgs because
// go_hostutil scrubs WORKCELL_STATE_ROOT/COLIMA_STATE_ROOT from the
// environment.
// The Go side does not currently use those roots, but the bash shim
// keeps prepending them so the contract stays consistent with sibling
// session-* subcommands and so a future tightening of SendMain that
// does need on-disk lookup will not require a shim change.
func SendMain(args []string) error {
	return sendMain(args, os.Stdout, os.Stderr)
}

func sendMain(args []string, stdout, stderr io.Writer) error {
	_, rest := stateroot.ConsumeRootArgs(args)
	sessionID, message, appendNewline, showHelp, err := parseSendArgs(rest)
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
		return &cliexit.ExitCodeError{Code: 2, Message: "workcell session send requires --id."}
	}
	if message == "" {
		return &cliexit.ExitCodeError{Code: 2, Message: "workcell session send requires --message."}
	}
	if err := rejectControlChars("session send", "--id", sessionID); err != nil {
		return err
	}
	if err := rejectControlChars("session send", "--message", message); err != nil {
		return err
	}

	appendFlag := 0
	if appendNewline {
		appendFlag = 1
	}
	return shellproto.WriteFields(stdout, []shellproto.Field{
		{Key: "session_id", Value: sessionID},
		{Key: "message", Value: message},
		{Key: "append_newline", Value: strconv.Itoa(appendFlag)},
	})
}

// parseSendArgs walks the bash session_send_main option loop.
//
// --id uses the strict optionValueOrErrorStrict helper: the value must
// be non-empty and must not start with `--` (catches a missing value
// swallowed by the next flag, e.g. `--id --message foo`).
//
// --message uses the raw optionValueOrError helper: the value must
// only be non-empty so the operator can legitimately send a payload
// that starts with `--`.
//
// --no-newline is a boolean toggle.
//
// -h / --help mark help output for the caller.
//
// Unknown options return an Unsupported-style error matching the bash
// branch so the user-visible stderr stays byte-identical.
func parseSendArgs(args []string) (sessionID, message string, appendNewline, showHelp bool, err error) {
	appendNewline = true
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--id":
			v, ni, perr := optionValueOrErrorStrict(args, i, "--id")
			if perr != nil {
				return "", "", false, false, perr
			}
			sessionID = v
			i = ni
		case "--message":
			v, ni, perr := optionValueOrError(args, i, "--message")
			if perr != nil {
				return "", "", false, false, perr
			}
			message = v
			i = ni
		case "--no-newline":
			appendNewline = false
		case "-h", "--help":
			showHelp = true
		default:
			return "", "", false, false, unsupportedOption("session send", args[i])
		}
	}
	return sessionID, message, appendNewline, showHelp, nil
}
