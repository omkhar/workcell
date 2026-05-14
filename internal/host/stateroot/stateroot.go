// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

// Package stateroot translates the scripts/workcell
// session_lookup_root_args bash helper.  It centralises the
// --root=PATH flag handling that every sessionctl subcommand
// (logs/timeline/attach/stop/...) needs, so the bash contract has a
// single Go owner.
package stateroot

import (
	"os"
	"strings"
)

// ConsumeRootArgs strips any leading --root=PATH arguments and
// returns the bare paths in roots plus the remaining args.  Empty
// values (`--root=`) are dropped so callers never receive a bogus
// path — scripts/workcell emits `--root=` when one of
// WORKCELL_STATE_ROOT / COLIMA_STATE_ROOT is unset, and the bash
// session_lookup_root_args helper skips those entries the same way.
//
// Only the leading run of --root= flags is consumed; once the first
// non-matching arg appears the remainder is returned verbatim so a
// later --root= flag (e.g. inside a positional argument list) is not
// silently absorbed.
func ConsumeRootArgs(args []string) (roots, rest []string) {
	for len(args) > 0 && strings.HasPrefix(args[0], "--root=") {
		if v := strings.TrimPrefix(args[0], "--root="); v != "" {
			roots = append(roots, v)
		}
		args = args[1:]
	}
	return roots, args
}

// envVarNames lists the env vars consulted by LookupRoots in the
// exact order scripts/workcell session_lookup_root_args emits them.
// WORKCELL_STATE_ROOT comes first so the workcell-owned state always
// wins over a legacy colima profile pointer when both are set.
var envVarNames = []string{"WORKCELL_STATE_ROOT", "COLIMA_STATE_ROOT"}

// LookupRoots mirrors scripts/workcell's session_lookup_root_args:
// emit the workcell state root and the colima state root in that
// order, skipping unset/empty values so the caller never receives a
// bogus path.  Returns a fresh slice each call.
func LookupRoots() []string {
	roots := make([]string, 0, len(envVarNames))
	for _, name := range envVarNames {
		if v := os.Getenv(name); v != "" {
			roots = append(roots, v)
		}
	}
	return roots
}
