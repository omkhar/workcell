// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package sessionctl

import (
	"fmt"
	"io"
	"os"

	"github.com/omkhar/workcell/internal/authpolicy"
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
// --root=PATH args are consumed via consumeRootArgs because go_hostutil
// scrubs WORKCELL_STATE_ROOT/COLIMA_STATE_ROOT from the environment.
// The Go side does not currently use those roots, but the bash shim
// keeps prepending them so the contract stays consistent with sibling
// session-* subcommands.
func DeleteMain(args []string) error {
	return deleteMain(args, os.Stdout)
}

func deleteMain(args []string, stdout io.Writer) error {
	_, rest := consumeRootArgs(args)
	sessionID, recordOnly, dryRun, showHelp, err := parseDeleteArgs(rest)
	if err != nil {
		return err
	}
	if showHelp {
		fmt.Fprint(stdout, UsageText())
		return nil
	}
	if sessionID == "" {
		return &authpolicy.ExitCodeError{Code: 2, Message: "workcell session delete requires --id."}
	}

	recordOnlyFlag := 0
	if recordOnly {
		recordOnlyFlag = 1
	}
	dryRunFlag := 0
	if dryRun {
		dryRunFlag = 1
	}
	fmt.Fprintf(stdout, "session_id=%s\n", sessionID)
	fmt.Fprintf(stdout, "record_only=%d\n", recordOnlyFlag)
	fmt.Fprintf(stdout, "dry_run=%d\n", dryRunFlag)
	return nil
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
// an ExitCodeError so the launcher exits 2.
func parseDeleteArgs(args []string) (sessionID string, recordOnly, dryRun, showHelp bool, err error) {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--id":
			if i+1 >= len(args) || args[i+1] == "" {
				return "", false, false, false, &authpolicy.ExitCodeError{
					Code:    2,
					Message: "Option --id requires a value.",
				}
			}
			sessionID = args[i+1]
			i++
		case "--record-only":
			recordOnly = true
		case "--dry-run":
			dryRun = true
		case "-h", "--help":
			showHelp = true
		default:
			return "", false, false, false, &authpolicy.ExitCodeError{
				Code:    2,
				Message: fmt.Sprintf("Unsupported workcell session delete option: %s", args[i]),
			}
		}
	}
	return sessionID, recordOnly, dryRun, showHelp, nil
}
