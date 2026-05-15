// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package sessionctl

import (
	"fmt"

	"github.com/omkhar/workcell/internal/cliexit"
	"github.com/omkhar/workcell/internal/host/sessions"
	"github.com/omkhar/workcell/internal/host/stateroot"
)

// TimelineMain implements `workcell session timeline --id SESSION_ID`,
// the Go translation of the bash session_timeline_main function in
// scripts/workcell.  It mirrors the bash function's argument parsing
// exactly: --id is required, -h/--help prints UsageText, anything else
// is a hard error.
//
// State-root discovery matches scripts/workcell's session_lookup_root_args:
// pull WORKCELL_STATE_ROOT and COLIMA_STATE_ROOT from the environment
// and pass both to sessions.SessionTimelineRecordsInRoots so legacy
// records continue to resolve.
func TimelineMain(args []string) error {
	roots, rest := stateroot.ConsumeRootArgs(args)
	sessionID, showHelp, err := parseTimelineArgs(rest)
	if err != nil {
		return err
	}
	if showHelp {
		fmt.Print(UsageText())
		return nil
	}
	if sessionID == "" {
		return &cliexit.ExitCodeError{Code: 2, Message: "workcell session timeline requires --id."}
	}

	if len(roots) == 0 {
		roots = stateroot.LookupRoots()
	}
	lines, err := sessions.SessionTimelineRecordsInRoots(roots, sessionID)
	if err != nil {
		return err
	}
	for _, line := range lines {
		fmt.Println(line)
	}
	return nil
}

func parseTimelineArgs(args []string) (sessionID string, showHelp bool, err error) {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--id":
			value, next, perr := optionValueOrError(args, i, "--id")
			if perr != nil {
				return "", false, perr
			}
			sessionID = value
			i = next
		case "-h", "--help":
			showHelp = true
		default:
			return "", false, unsupportedOption("session timeline", args[i])
		}
	}
	return sessionID, showHelp, nil
}
