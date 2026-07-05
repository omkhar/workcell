// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

// Package workcellhardening re-implements the contiguous block of
// scripts/workcell hardening-invariant checks that previously lived
// inline in scripts/verify-invariants.sh (the eleven checks between the
// codex-managed-config tmpdir cleanup and the install-deps barrier
// fixtures).  It also re-implements the adjacent config-safety block (the
// four scripts/workcell checks between the check_file loop and
// toml_section_assignments) via CheckConfigSafety; see configSafetyChecks.
// It also re-implements the adjacent runtime/gc block (the ten
// scripts/workcell checks between the SSH-collision check and the
// start_managed_profile mount function-block group) via
// CheckRuntimeInvariants; see runtimeInvariantChecks.
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
	// kindFunctionBlockAbsent requires needle (a fixed string) NOT to
	// appear inside the top-level bash function body named functionName,
	// mirroring a negated `function_block_contains_fixed` under a `||`
	// guard (present inside the block is a violation → exit 1).
	kindFunctionBlockAbsent
	// kindFirstLineRegex requires the launcher's first line to match the
	// anchored regex, mirroring `head -n1 ... | grep -q '^...$'`.
	kindFirstLineRegex
	// kindPresent requires the fixed string to appear anywhere in the
	// launcher, mirroring an affirmative `rg -q FIXED`.
	kindPresent
	// kindAbsent requires the fixed string NOT to appear anywhere in the
	// launcher, mirroring a negative `if rg -q FIXED; then ... exit 1`.
	kindAbsent
	// kindRegexAbsent requires the regexp pattern NOT to match anywhere in
	// the launcher, mirroring a negative `if rg -q REGEX; then ... exit 1`
	// whose REGEX is a genuine regular expression (an unanchored
	// alternation with active metacharacters), unlike kindAbsent's
	// fixed-string containment.
	kindRegexAbsent
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
	return evaluate(rootDir, checks)
}

// evaluate reads scripts/workcell under rootDir and returns the first
// violated check's message (as an error), or nil when all hold.  A
// missing or unreadable launcher is treated as empty content, exactly as
// the shell behaved: head/grep/rg/function_block_contains_fixed and
// negated `rg -q` on a missing file all produce no match, so affirmative
// (kindPresent/kindFunctionBlock/kindFirstLineRegex) checks fail while
// negative (kindAbsent/kindRegexAbsent) checks pass.
func evaluate(rootDir string, cs []check) error {
	content, err := os.ReadFile(filepath.Join(rootDir, launcherRelPath))
	if err != nil {
		content = nil
	}
	text := string(content)

	for _, c := range cs {
		if !c.holds(text) {
			return errors.New(c.message)
		}
	}
	return nil
}

// configSafetyChecks lists the four scripts/workcell config-safety
// invariants in the same order as the former inline block in
// scripts/verify-invariants.sh (the block between the check_file loop and
// toml_section_assignments), so a reviewer can diff the two one-to-one.
//
// The Colima state-root invariant was one shell `if` guarding two
// `rg -q` fixed-string probes joined by `||` under a single message; it
// is expressed here as two ordered kindPresent checks sharing that
// message, which is behaviourally identical (either missing probe yields
// the same stderr and exit 1, before the REAL_HOME check runs).
var configSafetyChecks = []check{
	{
		// kindRegexAbsent: the shell's `rg -q
		// 'WORKCELL_TEST_HARNESS|WORKCELL_(GIT|COLIMA|DOCKER|RUBY)_BIN='`
		// is a genuine unanchored alternation, not a fixed string, so it
		// must NOT match (present is a violation → exit 1).
		kind:    kindRegexAbsent,
		pattern: `WORKCELL_TEST_HARNESS|WORKCELL_(GIT|COLIMA|DOCKER|RUBY)_BIN=`,
		message: "Unexpected test-harness host tool override support remains in scripts/workcell",
	},
	{
		// kindAbsent: the shell's `rg -q 'YAML\.load_file'` has its only
		// metacharacter (`\.`) escaped to a literal dot, so it reduces to
		// fixed-string containment of `YAML.load_file` (present is a
		// violation).
		kind:    kindAbsent,
		pattern: "YAML.load_file",
		message: "scripts/workcell still uses unsafe YAML.load_file parsing for managed profile validation",
	},
	{
		// kindPresent (first half of the Colima state-root guard).
		kind:    kindPresent,
		pattern: "COLIMA_STATE_ROOT=",
		message: "Expected scripts/workcell to pin Colima state operations to one COLIMA_HOME root",
	},
	{
		// kindPresent (second half): the shell's rg pattern
		// `COLIMA_HOME="\$\{COLIMA_STATE_ROOT\}"` escapes every
		// metacharacter, so it is fixed-string containment of the literal
		// assignment.  Shares the first half's message.
		kind:    kindPresent,
		pattern: `COLIMA_HOME="${COLIMA_STATE_ROOT}"`,
		message: "Expected scripts/workcell to pin Colima state operations to one COLIMA_HOME root",
	},
	{
		// kindPresent: `rg -q 'REAL_HOME='` fixed-string containment.
		kind:    kindPresent,
		pattern: "REAL_HOME=",
		message: "Expected scripts/workcell to derive the real host home independently of caller HOME",
	},
}

// CheckConfigSafety runs the four scripts/workcell config-safety
// invariants against the repo rooted at rootDir, in the shell's original
// order.  It returns nil when every invariant holds (the shell's exit 0),
// or an error whose message equals the shell's stderr for the first
// violated invariant (the shell's exit 1).
func CheckConfigSafety(rootDir string) error {
	return evaluate(rootDir, configSafetyChecks)
}

// runtimeInvariantChecks lists the ten scripts/workcell runtime/gc
// invariants in the same order as the former inline block in
// scripts/verify-invariants.sh (the block between the SSH-collision check
// and the start_managed_profile mount function-block group), so a
// reviewer can diff the two one-to-one.
//
// Every `rg` pattern in this block is metacharacter-free after
// unescaping (the DOCKER_CONFIG probe escapes every `$ { } .`, and the
// `--self-*-probe` / `strict mode …` / `go_colimautil …` probes contain
// no active regex metacharacters), so each reduces to fixed-string
// containment — kindPresent for affirmative `rg -q`, kindAbsent for the
// negated DOCKER_CONFIG guard.
//
// The runtime_build_codex_arch guard was one shell `if` joining three
// function_block_contains_fixed probes with `||` under a single message:
// the first two are affirmative (musl assets must be present) and the
// third is NEGATED (a gnu asset present is a violation).  It is expressed
// here as two kindFunctionBlock checks plus one kindFunctionBlockAbsent
// check sharing that message, which is behaviourally identical (any of
// the three failing yields the same stderr and exit 1).
//
// Two other guards each joined two affirmative `rg -q` probes with `||`
// under one message (the --gc runtime-image/temp cleanup pair); they are
// expressed as two ordered kindPresent checks sharing that message, which
// is behaviourally identical (either missing probe yields the same
// stderr and exit 1).
var runtimeInvariantChecks = []check{
	{
		kind:    kindPresent,
		pattern: "setup_workcell_trusted_docker_client",
		message: "Expected scripts/workcell to seed a trusted Docker client state before host Docker use",
	},
	{
		// kindAbsent: the shell's rg pattern
		// `DOCKER_CONFIG="\$\{REAL_HOME\}/\.docker"` escapes every
		// metacharacter, so it is fixed-string containment of the literal
		// assignment (present is a violation).
		kind:    kindAbsent,
		pattern: `DOCKER_CONFIG="${REAL_HOME}/.docker"`,
		message: "scripts/workcell still pins DOCKER_CONFIG to the real host home",
	},
	{
		kind:    kindPresent,
		pattern: "buildx_cmd build",
		message: "Expected scripts/workcell to invoke buildx through the trusted absolute plugin path",
	},
	{
		// kindFunctionBlock (musl aarch64 asset must be present).
		kind:         kindFunctionBlock,
		functionName: "runtime_build_codex_arch",
		pattern:      "aarch64-unknown-linux-musl",
		message:      "Expected scripts/workcell Codex release probe to resolve musl release assets",
	},
	{
		// kindFunctionBlock (musl x86_64 asset must be present).
		kind:         kindFunctionBlock,
		functionName: "runtime_build_codex_arch",
		pattern:      "x86_64-unknown-linux-musl",
		message:      "Expected scripts/workcell Codex release probe to resolve musl release assets",
	},
	{
		// kindFunctionBlockAbsent (the NEGATED sub-condition): a gnu asset
		// inside the block is a violation.  Shares the pair's message.
		kind:         kindFunctionBlockAbsent,
		functionName: "runtime_build_codex_arch",
		pattern:      "unknown-linux-gnu",
		message:      "Expected scripts/workcell Codex release probe to resolve musl release assets",
	},
	{
		kind:    kindPresent,
		pattern: "--self-docker-probe",
		message: "Expected scripts/workcell to expose a hidden self-docker probe for invariant testing",
	},
	{
		// kindPresent (first half of the --gc cleanup guard).
		kind:    kindPresent,
		pattern: "prune_runtime_image_cache_dir",
		message: "Expected scripts/workcell --gc to cover bounded runtime-image cache and Workcell-owned temp cleanup",
	},
	{
		// kindPresent (second half): shares the first half's message.
		kind:    kindPresent,
		pattern: "cleanup_workcell_temp_root",
		message: "Expected scripts/workcell --gc to cover bounded runtime-image cache and Workcell-owned temp cleanup",
	},
	{
		kind:    kindPresent,
		pattern: "--self-staging-probe",
		message: "Expected scripts/workcell to expose a hidden staging probe for invariant testing",
	},
	{
		kind:    kindPresent,
		pattern: "strict mode requires --prepare when you explicitly request --rebuild.",
		message: "Expected scripts/workcell to reject explicit strict-mode image rebuild requests",
	},
	{
		kind:    kindPresent,
		pattern: "go_colimautil validate-profile-config",
		message: "Expected scripts/workcell to validate managed Colima config through the dedicated Go helper",
	},
	{
		kind:    kindPresent,
		pattern: "go_colimautil validate-runtime-mounts",
		message: "Expected scripts/workcell to validate managed Lima mounts through the dedicated Go helper",
	},
}

// CheckRuntimeInvariants runs the ten scripts/workcell runtime/gc
// invariants against the repo rooted at rootDir, in the shell's original
// order.  It returns nil when every invariant holds (the shell's exit 0),
// or an error whose message equals the shell's stderr for the first
// violated invariant (the shell's exit 1).
func CheckRuntimeInvariants(rootDir string) error {
	return evaluate(rootDir, runtimeInvariantChecks)
}

// holds reports whether the invariant is satisfied by the launcher text.
func (c check) holds(text string) bool {
	switch c.kind {
	case kindFunctionBlock:
		block := extractNamedFunctionBlock(text, c.functionName)
		return strings.Contains(block, c.pattern)
	case kindFunctionBlockAbsent:
		block := extractNamedFunctionBlock(text, c.functionName)
		return !strings.Contains(block, c.pattern)
	case kindFirstLineRegex:
		return regexp.MustCompile(c.pattern).MatchString(firstLine(text))
	case kindPresent:
		return strings.Contains(text, c.pattern)
	case kindAbsent:
		return !strings.Contains(text, c.pattern)
	case kindRegexAbsent:
		return !regexp.MustCompile(c.pattern).MatchString(text)
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
