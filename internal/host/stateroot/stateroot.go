// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

// Package stateroot translates the scripts/workcell
// session_lookup_root_args bash helper.  It centralises the
// --root=PATH flag handling that every sessionctl subcommand
// (logs/timeline/attach/stop/...) needs, so the bash contract has a
// single Go owner.
package stateroot

import (
	"fmt"
	"os"
	"strings"

	"github.com/omkhar/workcell/internal/cliexit"
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
//
// Returns a *cliexit.ExitCodeError (Code 2) if either env var contains
// newline, carriage-return, or NUL — those would let an
// attacker-controlled env propagate the same forged-record injection
// that FormatRootArgs defends against on the argv-driven path. The
// check stays symmetric across both helpers so any future
// plan-emission consumer of LookupRoots inherits the same
// input-boundary defense.
//
// The typed return lets sessionctl callers propagate the error
// directly without re-wrapping in a cliexit.ExitCodeError — the typed
// pointer's nil-comparison is safe (no nil-interface-containing-nil-
// pointer gotcha) because the function never returns a typed nil
// pointer wrapped in an error interface.
func LookupRoots() ([]string, *cliexit.ExitCodeError) {
	for _, name := range envVarNames {
		v := os.Getenv(name)
		if strings.ContainsAny(v, "\n\r\x00") {
			return nil, &cliexit.ExitCodeError{
				Code:    2,
				Message: fmt.Sprintf("%s must not contain newline, carriage-return, or NUL", name),
			}
		}
	}
	roots := make([]string, 0, len(envVarNames))
	for _, name := range envVarNames {
		if v := os.Getenv(name); v != "" {
			roots = append(roots, v)
		}
	}
	return roots, nil
}

// FormatRootArgs returns the --root=VALUE strings for non-empty
// workcellRoot and colimaRoot, in that fixed order. This is the single
// Go owner of the bash↔Go state-root contract; scripts/workcell shells
// out through `workcell-hostutil helper lookup-state-roots` to
// consume it, so the order and the "skip empty" rule live in exactly
// one place.
//
// Argv-driven (rather than env-driven) because scripts/workcell routes
// through go_hostutil → run_clean_host_command, which calls `env -i`
// and strips the process env. The bash shim forwards
// `${WORKCELL_STATE_ROOT:-} ${COLIMA_STATE_ROOT:-}` on argv.
//
// Returns an error if either input contains newline, carriage-return,
// or NUL — those would let an attacker-controlled env var inject
// forged --root= lines into the bash consumer's `while read` loop.
func FormatRootArgs(workcellRoot, colimaRoot string) ([]string, error) {
	// Validate in fixed envVarNames order so error messages are
	// deterministic when both inputs are bad — map iteration would
	// randomize the labeled name reported back.
	pairs := [...]struct {
		label, root string
	}{
		{envVarNames[0], workcellRoot},
		{envVarNames[1], colimaRoot},
	}
	for _, p := range pairs {
		if strings.ContainsAny(p.root, "\n\r\x00") {
			return nil, fmt.Errorf("%s must not contain newline, carriage-return, or NUL", p.label)
		}
	}
	out := make([]string, 0, len(pairs))
	for _, p := range pairs {
		if p.root != "" {
			out = append(out, "--root="+p.root)
		}
	}
	return out, nil
}
