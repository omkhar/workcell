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

// DeleteMain implements the option-parsing half of `workcell session
// delete --id SESSION_ID [--record-only] [--dry-run]`, the Go translation
// of session_delete_main in scripts/workcell.
//
// Like SendMain, DeleteMain is the host-side parsing layer of a hybrid
// translation.  The bash function's body splits into:
//
//  1. Up-front option parsing (--id, --record-only, --dry-run) plus the
//     no-value check that bash performs with option_value_or_die.  This
//     half lives here.
//
//  2. load_session_runtime_metadata, terminal-status validation,
//     record-path existence check, container-state probe via
//     run_profile_docker_command, artifact resolution via
//     session_delete_resolve_*_artifact, the planned-remove rollup,
//     dry-run output, and the actual `docker rm`/`rm -f`/`rm -rf`
//     mutations.  Those still depend on sourced bash helpers that
//     tests/scenarios/shared/test-session-commands.sh mocks via
//     `bash -lc "source workcell; ..."` - in particular the cleanup,
//     compat, dry-run, record-only, and live-container fixtures all
//     override load_session_runtime_metadata to inject canned
//     SESSION_META_* globals, so the Go side cannot independently load
//     the on-disk record without diverging from the established test
//     surface.  Phase 2 therefore stays in the bash shim.
//
// DeleteMain emits a plan on stdout for the bash shim to consume:
//
//	session_id=<id>
//	record_only=0|1
//	dry_run=0|1
//
// Usage errors (--id missing, unknown flag) exit with code 2 to match
// the bash CLI surface.
//
// State-root forwarding mirrors SendMain/AttachMain/LogsMain: leading
// --root=PATH args are consumed via stateroot.ConsumeRootArgs because
// go_hostutil scrubs WORKCELL_STATE_ROOT/COLIMA_STATE_ROOT from the
// environment.  The Go side does not currently use those roots, but
// the bash shim keeps prepending them so the contract stays consistent
// with sibling session-* subcommands.
func DeleteMain(args []string) error {
	return deleteMain(args, os.Stdout, os.Stderr)
}

func deleteMain(args []string, stdout, stderr io.Writer) error {
	_, rest := stateroot.ConsumeRootArgs(args)
	sessionID, recordOnly, dryRun, showHelp, err := parseDeleteArgs(rest)
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
		return &cliexit.ExitCodeError{Code: 2, Message: "workcell session delete requires --id."}
	}
	if err := rejectControlChars("session delete", "--id", sessionID); err != nil {
		return err
	}

	recordOnlyFlag := 0
	if recordOnly {
		recordOnlyFlag = 1
	}
	dryRunFlag := 0
	if dryRun {
		dryRunFlag = 1
	}
	return shellproto.WriteFields(stdout, []shellproto.Field{
		{Key: "session_id", Value: sessionID},
		{Key: "record_only", Value: strconv.Itoa(recordOnlyFlag)},
		{Key: "dry_run", Value: strconv.Itoa(dryRunFlag)},
	})
}

// parseDeleteArgs walks the bash session_delete_main option loop.
//
// --id mirrors option_value_or_die: the value must be non-empty.
//
// --record-only and --dry-run are boolean toggles that flip recordOnly
// and dryRun respectively.
//
// -h / --help mark help output for the caller.
//
// Unknown options return an Unsupported-style error matching the bash
// branch so the user-visible stderr stays byte-identical, wrapped in
// an ExitCodeError so the helper exits 2.
func parseDeleteArgs(args []string) (sessionID string, recordOnly, dryRun, showHelp bool, err error) {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--id":
			v, ni, perr := optionValueOrError(args, i, "--id")
			if perr != nil {
				return "", false, false, false, perr
			}
			sessionID = v
			i = ni
		case "--record-only":
			recordOnly = true
		case "--dry-run":
			dryRun = true
		case "-h", "--help":
			showHelp = true
		default:
			return "", false, false, false, unsupportedOption("session delete", args[i])
		}
	}
	return sessionID, recordOnly, dryRun, showHelp, nil
}
