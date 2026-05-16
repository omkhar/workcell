// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package sessionctl

import (
	"fmt"
	"strings"

	"github.com/omkhar/workcell/internal/cliexit"
)

// optionValueOrError reads args[i+1] as the value for the args[i] flag.
// It returns the value, the new index (i+1), and a *cliexit.ExitCodeError
// with Code 2 if the value is missing or empty.  This mirrors the bash
// option_value_or_die helper for the simple "next token is the value"
// case shared across every session_* parser (parseStopArgs,
// parseDeleteArgs, parseMonitorArgs, parseAttachArgs,
// parseTimelineArgs, parseLogsArgs, and parseSendArgs --message).
//
// parseSendArgs additionally rejects `--`-prefixed values for --id to
// mirror the bash strict variant; for that mode call
// optionValueOrErrorStrict, which is the same helper with the
// `--`-prefix check enabled.
func optionValueOrError(args []string, i int, flag string) (string, int, error) {
	return optionValueOrErrorMode(args, i, flag, false)
}

// optionValueOrErrorStrict is the strict variant: it additionally
// rejects values starting with `--` so the operator's missing-value
// gets caught when the next flag is consumed.  Used by parseSendArgs
// for --id (bash option_value_or_die contract); parseSendArgs --message
// keeps the raw mode so payloads may legitimately begin with `--`.
func optionValueOrErrorStrict(args []string, i int, flag string) (string, int, error) {
	return optionValueOrErrorMode(args, i, flag, true)
}

func optionValueOrErrorMode(args []string, i int, flag string, rejectDashDash bool) (string, int, error) {
	if i+1 >= len(args) {
		return "", i, missingOptionValueErr(flag)
	}
	value := args[i+1]
	if value == "" {
		return "", i, missingOptionValueErr(flag)
	}
	if rejectDashDash && strings.HasPrefix(value, "--") {
		return "", i, missingOptionValueErr(flag)
	}
	return value, i + 1, nil
}

func missingOptionValueErr(flag string) error {
	return &cliexit.ExitCodeError{
		Code:    2,
		Message: fmt.Sprintf("Option %s requires a value.", flag),
	}
}

// unsupportedOption returns the exit-2 error for an unknown flag.  subcmd
// is the human-readable subcommand name (e.g. "session send") used in the
// "Unsupported workcell <subcmd> option" diagnostic to mirror the bash
// case-statement's "*)" branch.
func unsupportedOption(subcmd, flag string) error {
	return &cliexit.ExitCodeError{
		Code:    2,
		Message: fmt.Sprintf("Unsupported workcell %s option: %s", subcmd, flag),
	}
}

// rejectControlChars returns a *cliexit.ExitCodeError with Code 2 when the
// supplied value contains a newline or carriage return.  The bash shim
// transports key=value plan lines from the Go side as newline-separated
// records, so a CR/LF in a user-controlled field would let an attacker
// forge additional plan entries (a CRLF-injection).  Reject conservatively
// so the bash shim never sees a multi-line value.
//
// shellproto.WriteField is the second line of defence at the output
// boundary: every emitter call goes through that helper and re-validates
// the value, so a future shim that forgets to call rejectControlChars
// still cannot forge plan records.  rejectControlChars stays as the
// first line because (a) it produces a user-visible error that names the
// offending flag, and (b) it ensures the Go side never gets far enough
// to compute additional state from a tainted input.
func rejectControlChars(subcmd, flag, value string) error {
	if strings.ContainsAny(value, "\n\r") {
		return &cliexit.ExitCodeError{
			Code:    2,
			Message: fmt.Sprintf("workcell %s %s must not contain newline or carriage-return characters.", subcmd, flag),
		}
	}
	return nil
}
