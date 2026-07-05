// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

// Package workcellhardening re-implements the contiguous block of
// scripts/workcell hardening-invariant checks that previously lived
// inline in scripts/verify-invariants.sh (the eleven checks between the
// codex-managed-config tmpdir cleanup and the install-deps barrier
// fixtures).
//
// Each invariant pins one property of the host launcher scripts/workcell:
// that run_host_colima restores the real host HOME, that the shebang
// clears the host environment, that the process/Perl/DYLD/Docker
// environment scrubbers are present, that Perl-backed shasum is absent,
// and that the trusted Docker client / shellproto / sessionctl-shim
// helpers are sourced.
//
// This is a behaviour-preserving migration of the shell block: the file
// read target, the fixed-string vs. regex matching semantics, the exit
// codes, and the stderr messages match the original
// function_block_contains_fixed / head+grep / rg implementation.  The
// per-check matching semantics were chosen to faithfully mirror the
// shell:
//
//   - The run_host_colima check reuses function_block_contains_fixed's
//     sed-range extraction (see extractNamedFunctionBlock) followed by a
//     grep -Fq fixed-string containment.
//   - The shebang check applies an anchored regex to the FIRST line only,
//     mirroring `head -n1 ... | grep -q '^...$'`.
//   - The scrub/unset/DYLD/shasum checks use `rg` patterns that contain
//     no active regex metacharacters (DYLD_\* is a literal `DYLD_*`), so
//     they are fixed-string containment checks; shasum is negative
//     (present is a violation).
//   - The three `source "${ROOT_DIR}/scripts/lib/....sh"` checks use `rg`
//     patterns whose metacharacters are all escaped (\$ \{ \} \.), so
//     they too reduce to fixed-string containment on the literal source
//     line.
package workcellhardening

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// launcherRelPath is the repo-relative path to the host launcher every
// check inspects.  The shell hard-coded ${ROOT_DIR}/scripts/workcell for
// each of head/grep/rg/function_block_contains_fixed.
const launcherRelPath = "scripts/workcell"

// checkKind selects how a check's pattern is matched against the launcher
// contents.
type checkKind int

const (
	// kindFunctionBlock requires needle (a fixed string) to appear inside
	// the top-level bash function body named functionName, mirroring
	// function_block_contains_fixed (sed-range extraction + grep -Fq).
	kindFunctionBlock checkKind = iota
	// kindFirstLineRegex requires the launcher's first line to match the
	// anchored regex, mirroring `head -n1 ... | grep -q '^...$'`.
	kindFirstLineRegex
	// kindPresent requires the fixed string to appear anywhere in the
	// launcher, mirroring an affirmative `rg -q FIXED`.
	kindPresent
	// kindAbsent requires the fixed string NOT to appear anywhere in the
	// launcher, mirroring a negative `if rg -q FIXED; then ... exit 1`.
	kindAbsent
)

// check is one hardening invariant: how to match, what to match, which
// function to scope to (kindFunctionBlock only), and the exact stderr
// message the shell emitted on violation.
type check struct {
	kind         checkKind
	functionName string
	pattern      string
	message      string
}

// checks lists the eleven invariants in the same order as the former
// inline block in scripts/verify-invariants.sh, so a reviewer can diff
// the two one-to-one.
var checks = []check{
	{
		kind:         kindFunctionBlock,
		functionName: "run_host_colima",
		pattern:      `HOME="${REAL_HOME}"`,
		message:      "Expected run_host_colima to restore the real host HOME instead of the Docker client sandbox home",
	},
	{
		kind:    kindFirstLineRegex,
		pattern: `^#!/usr/bin/env -S -i PATH=.* BASH_ENV= ENV= /bin/bash$`,
		message: "Expected scripts/workcell to use env -S -i with an absolute /bin/bash and cleared host environment",
	},
	{
		kind:    kindPresent,
		pattern: "scrub_host_process_env",
		message: "Expected scripts/workcell to scrub hostile host process environment before host tool lookup",
	},
	{
		kind:    kindPresent,
		pattern: "unset PERL5OPT PERL5LIB PERLLIB PERL_MB_OPT PERL_MM_OPT",
		message: "Expected scripts/workcell to scrub hostile Perl environment before host tool lookup",
	},
	{
		kind:    kindPresent,
		pattern: "DYLD_*",
		message: "Expected scripts/workcell to scrub DYLD_* variables before host tool lookup",
	},
	{
		kind:    kindAbsent,
		pattern: "shasum -a 256",
		message: "scripts/workcell still uses Perl-backed shasum for profile hashing",
	},
	{
		kind:    kindPresent,
		pattern: "unset DOCKER_CONTEXT",
		message: "Expected scripts/workcell to scrub caller Docker context overrides before binding the managed daemon",
	},
	{
		kind:    kindPresent,
		pattern: "unset DOCKER_CLI_PLUGIN_EXTRA_DIRS",
		message: "Expected scripts/workcell to scrub caller Docker CLI plugin overrides",
	},
	{
		kind:    kindPresent,
		pattern: `source "${ROOT_DIR}/scripts/lib/trusted-docker-client.sh"`,
		message: "Expected scripts/workcell to source the trusted Docker client helper",
	},
	{
		kind:    kindPresent,
		pattern: `source "${ROOT_DIR}/scripts/lib/shellproto.sh"`,
		message: "Expected scripts/workcell to source the shellproto helper",
	},
	{
		kind:    kindPresent,
		pattern: `source "${ROOT_DIR}/scripts/lib/sessionctl-shim.sh"`,
		message: "Expected scripts/workcell to source the sessionctl shim helper",
	},
}

// Check runs the eleven scripts/workcell hardening invariants against the
// repo rooted at rootDir, in the shell's original order.  It returns nil
// when every invariant holds (the shell's exit 0), or an error whose
// message equals the shell's stderr for the first violated invariant (the
// shell's exit 1).
//
// A missing or unreadable scripts/workcell is treated as empty content,
// exactly as the shell behaved: head/grep/rg/function_block_contains_fixed
// all produce no match on a missing file, so the first affirmative check
// (run_host_colima's HOME restore) fails with its message.
func Check(rootDir string) error {
	content, err := os.ReadFile(filepath.Join(rootDir, launcherRelPath))
	if err != nil {
		content = nil
	}
	text := string(content)

	for _, c := range checks {
		if !c.holds(text) {
			return errors.New(c.message)
		}
	}
	return nil
}

// holds reports whether the invariant is satisfied by the launcher text.
func (c check) holds(text string) bool {
	switch c.kind {
	case kindFunctionBlock:
		block := extractNamedFunctionBlock(text, c.functionName)
		return strings.Contains(block, c.pattern)
	case kindFirstLineRegex:
		return regexp.MustCompile(c.pattern).MatchString(firstLine(text))
	case kindPresent:
		return strings.Contains(text, c.pattern)
	case kindAbsent:
		return !strings.Contains(text, c.pattern)
	default:
		return false
	}
}

// firstLine returns text up to (but excluding) the first newline,
// mirroring the single line that `head -n1` feeds to grep.
func firstLine(text string) string {
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		return text[:i]
	}
	return text
}

// extractNamedFunctionBlock replicates the shell's
// extract_named_function_block, i.e. `sed -n '/^NAME()/,/^}/p'`: it
// returns the lines from the first line beginning with `NAME()` through
// the next line beginning with `}` (both inclusive).  As in sed, the
// closing `^}` pattern is only tested on lines after the opening line, and
// the range re-triggers if a later `NAME()` line appears, so every such
// range is concatenated.  The result feeds a grep -Fq fixed-string check.
func extractNamedFunctionBlock(text, name string) string {
	openPrefix := name + "()"
	var out []string
	inBlock := false
	for _, line := range strings.Split(text, "\n") {
		if !inBlock {
			if strings.HasPrefix(line, openPrefix) {
				inBlock = true
				out = append(out, line)
			}
			continue
		}
		out = append(out, line)
		if strings.HasPrefix(line, "}") {
			inBlock = false
		}
	}
	return strings.Join(out, "\n")
}
