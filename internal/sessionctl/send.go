// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package sessionctl

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/omkhar/workcell/internal/cliexit"
	"github.com/omkhar/workcell/internal/host/stateroot"
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
// --root=PATH args are consumed via consumeRootArgs because go_hostutil
// scrubs WORKCELL_STATE_ROOT/COLIMA_STATE_ROOT from the environment.
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
	fmt.Fprintf(stdout, "session_id=%s\n", sessionID)
	fmt.Fprintf(stdout, "message=%s\n", message)
	fmt.Fprintf(stdout, "append_newline=%d\n", appendFlag)
	return nil
}

// parseSendArgs walks the bash session_send_main option loop.
//
// --id mirrors option_value_or_die: the value must be non-empty and
// must not start with `--` (catches a missing value swallowed by the
// next flag, e.g. `--id --message foo`).
//
// --message mirrors raw_option_value_or_die: the value must only be
// non-empty so the operator can legitimately send a payload that starts
// with `--`.
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
			v, perr := optionValueForSend(args, i, false)
			if perr != nil {
				return "", "", false, false, perr
			}
			sessionID = v
			i++
		case "--message":
			v, perr := optionValueForSend(args, i, true)
			if perr != nil {
				return "", "", false, false, perr
			}
			message = v
			i++
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

// optionValueForSend returns the value following args[i].  When raw is
// false, the value mirrors option_value_or_die: empty or `--`-prefixed
// values are rejected (so the missing-value case is caught when the
// next flag is consumed).  When raw is true, the value mirrors
// raw_option_value_or_die: only empty is rejected.
//
// The exit-2 wrapping matches the bash CLI contract that usage errors
// flow back as exit status 2.  This helper is intentionally separate
// from the shared optionValueOrError because parseSendArgs needs the
// raw/strict distinction; sibling parse functions only ever need the
// "non-empty" check.
func optionValueForSend(args []string, i int, raw bool) (string, error) {
	option := args[i]
	if i+1 >= len(args) {
		return "", &cliexit.ExitCodeError{
			Code:    2,
			Message: fmt.Sprintf("Option %s requires a value.", option),
		}
	}
	value := args[i+1]
	if value == "" {
		return "", &cliexit.ExitCodeError{
			Code:    2,
			Message: fmt.Sprintf("Option %s requires a value.", option),
		}
	}
	if !raw && strings.HasPrefix(value, "--") {
		return "", &cliexit.ExitCodeError{
			Code:    2,
			Message: fmt.Sprintf("Option %s requires a value.", option),
		}
	}
	return value, nil
}
