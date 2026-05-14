// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package sessionctl

import (
	"fmt"
	"io"
	"os"

	"github.com/omkhar/workcell/internal/cliexit"
)

// DispatchMain implements the subcommand-routing half of
// `workcell session <subcommand> [args...]`, the Go translation of the
// session_main dispatcher in scripts/workcell.
//
// session_main is a wide case statement that fans the user-facing
// subcommand list (start, attach, send, stop, list, show, delete, logs,
// timeline, diff, export, monitor) out to a matching per-subcommand
// handler.  The bulk of the per-subcommand work (option parsing for
// list/show/diff/export, the env -i re-exec for start, the docker
// transport for attach/send/stop, etc.) still depends on sourced bash
// helpers that tests/scenarios/shared/test-session-commands.sh mocks
// via `bash -lc "source workcell; ..."`, so the dispatcher stays in
// bash but routes through this Go shim for the subcommand whitelist.
//
// DispatchMain emits a routing decision on stdout that the bash shim
// consumes:
//
//	subcommand=<name>
//
// where <name> is one of the canonical subcommand tokens (start,
// attach, send, stop, list, show, delete, logs, timeline, diff,
// export, monitor) or `usage` for the empty/help branches.  Any other
// subcommand returns an ExitCodeError with code 2 carrying the bash
// "Unsupported workcell session command: <name>" diagnostic so the
// launcher exits with the historical bash status.
//
// Help flags (-h, --help) and the empty-subcommand case route to
// `subcommand=usage`, which the bash shim treats as a request to print
// the canonical usage and exit 0.  The usage text itself lives in
// usage.go (sessionctl.UsageText) and is already served via the
// session-usage launcher subcommand.
func DispatchMain(args []string) error {
	return dispatchMain(args, os.Stdout)
}

func dispatchMain(args []string, stdout io.Writer) error {
	subcommand := ""
	if len(args) > 0 {
		subcommand = args[0]
	}
	resolved, err := resolveSessionSubcommand(subcommand)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "subcommand=%s\n", resolved)
	return nil
}

// CanonicalSubcommands returns the ordered list of user-facing
// `workcell session` subcommand tokens.  The order matches the bash
// session_main case statement in scripts/workcell so usage prose, the
// bash dispatcher, and the Go dispatcher all agree on a single
// authoritative ordering.  A fresh slice is returned on every call so
// callers may mutate it freely.
func CanonicalSubcommands() []string {
	return []string{
		"start",
		"attach",
		"send",
		"stop",
		"list",
		"show",
		"delete",
		"logs",
		"timeline",
		"diff",
		"export",
		"monitor",
	}
}

// resolveSessionSubcommand mirrors the session_main case branches.  The
// empty-string, -h, and --help cases all collapse to the canonical
// `usage` token so the bash shim has a single branch to handle the
// usage printout.  Every other recognised subcommand returns its own
// canonical token (consumed from CanonicalSubcommands so the ordered
// list has one source of truth).  Anything else returns an
// ExitCodeError with the bash "Unsupported workcell session command:
// <name>" diagnostic and code 2.
func resolveSessionSubcommand(subcommand string) (string, error) {
	switch subcommand {
	case "", "-h", "--help":
		return "usage", nil
	}
	for _, name := range CanonicalSubcommands() {
		if subcommand == name {
			return subcommand, nil
		}
	}
	return "", &cliexit.ExitCodeError{
		Code:    2,
		Message: fmt.Sprintf("Unsupported workcell session command: %s", subcommand),
	}
}
