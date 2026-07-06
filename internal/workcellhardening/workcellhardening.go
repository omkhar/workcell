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
// It also re-implements the adjacent managed-profile staging/cleanup block
// (the three scripts/workcell checks between the runtime-invariants group
// and the WORKCELL_COLIMA_TIMEOUT_HARNESS fixture) via
// CheckManagedProfileStaging; see managedProfileStagingChecks.
// It also re-implements the adjacent bootstrap egress-endpoint block (the
// nine checks between the colima-egress-allowlist COLIMA_HOME pin check and
// the per-Dockerfile snapshot CA-bundle loop) via CheckBootstrapEgress;
// see bootstrapEgressChecks.  Eight of those read scripts/workcell; the
// Copilot-release-URL-override probe reads runtime/container/Dockerfile via
// the per-check targetFile field.
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
// check inspects by default.  The shell hard-coded
// ${ROOT_DIR}/scripts/workcell for each of
// head/grep/rg/function_block_contains_fixed.
const launcherRelPath = "scripts/workcell"

// dockerfileRelPath is the repo-relative path to the runtime container
// Dockerfile.  Only the bootstrap-egress Copilot-release-URL-override
// invariant reads this file instead of scripts/workcell; every other
// check leaves check.targetFile empty and so defaults to launcherRelPath.
const dockerfileRelPath = "runtime/container/Dockerfile"

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
	// kindRegexPresent requires the regexp pattern to match somewhere in the
	// launcher, mirroring an affirmative `rg -q REGEX` whose REGEX contains an
	// active metacharacter (e.g. a trailing `.` meaning any char), unlike
	// kindPresent's fixed-string containment.
	kindRegexPresent
	// kindFunctionBlockRegex requires the regexp pattern to match inside the
	// top-level bash function body named functionName, mirroring
	// function_block_contains_regex (sed-range extraction + `grep -q` regex,
	// NOT `grep -Fq`).  Unlike kindFunctionBlock's fixed-string containment,
	// the pattern is a genuine regular expression, matched per-line within the
	// extracted block via regexMatchesAnyLine for `grep`/`rg` line-oriented
	// parity.
	kindFunctionBlockRegex
)

// check is one hardening invariant: how to match, what to match, which
// function to scope to (kindFunctionBlock only), which repo-relative file
// to read (empty means launcherRelPath), and the exact stderr message the
// shell emitted on violation.
type check struct {
	kind         checkKind
	functionName string
	pattern      string
	message      string
	// targetFile is the repo-relative file this check reads.  An empty
	// value defaults to launcherRelPath (scripts/workcell), so every
	// existing check keeps its original read target unchanged; only the
	// bootstrap-egress Copilot-release-URL-override invariant sets it (to
	// dockerfileRelPath), mirroring the shell probe that ran `rg` against
	// ${ROOT_DIR}/runtime/container/Dockerfile instead of scripts/workcell.
	targetFile string
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

// evaluate reads each check's target file under rootDir (defaulting to
// scripts/workcell) and returns the first violated check's message (as an
// error), or nil when all hold.  Distinct target files are read at most
// once and cached, so a group that mixes scripts/workcell and
// runtime/container/Dockerfile probes reads each file a single time.
//
// A missing or unreadable target file is treated as empty content,
// exactly as the shell behaved: head/grep/rg/function_block_contains_fixed
// and negated `rg -q` on a missing file all produce no match, so
// affirmative (kindPresent/kindRegexPresent/kindFunctionBlock/
// kindFirstLineRegex) checks fail while negative
// (kindAbsent/kindRegexAbsent/kindFunctionBlockAbsent) checks pass.
func evaluate(rootDir string, cs []check) error {
	cache := make(map[string]string)
	readTarget := func(rel string) string {
		if text, ok := cache[rel]; ok {
			return text
		}
		content, err := os.ReadFile(filepath.Join(rootDir, rel))
		if err != nil {
			content = nil
		}
		text := string(content)
		cache[rel] = text
		return text
	}

	for _, c := range cs {
		rel := c.targetFile
		if rel == "" {
			rel = launcherRelPath
		}
		if !c.holds(readTarget(rel)) {
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
		// rg treats the trailing `.` as "any char", so match it as a regex for
		// exact `rg -q` parity (the only active metacharacter in the pattern).
		kind:    kindRegexPresent,
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

// managedProfileStagingChecks lists the three scripts/workcell
// managed-profile staging/cleanup invariants in the same order as the
// former inline block in scripts/verify-invariants.sh (the block between
// the runtime-invariants group and the WORKCELL_COLIMA_TIMEOUT_HARNESS
// fixture), so a reviewer can diff the two one-to-one.
//
// Each of the three former shell `if` guards joined several
// function_block_contains_fixed / `rg -q` probes with `||` under a single
// message; they are expressed here as ordered checks sharing that message,
// which is behaviourally identical (any probe failing yields the same
// stderr and exit 1 as the corresponding shell `if`).  Every probe is
// metacharacter-free after unescaping, so each is fixed-string
// containment: kindFunctionBlock for the block-scoped
// function_block_contains_fixed probes, kindPresent for the affirmative
// `rg -q`, and kindAbsent for the negated `rg -q` (present is a
// violation).
//
// The scoping of each block-scoped probe mirrors the shell's second
// argument to function_block_contains_fixed: the mount and
// staging-cache-root probes scope to start_managed_profile; the
// staging-cache-root probes also scope to prepare_injection_bundle and
// prepare_workspace_control_plane_shadow; and the fail-closed cleanup
// probes scope to cleanup_default_injection_bundles.
var managedProfileStagingChecks = []check{
	// Guard 1: managed Colima launch mounts all three staging cache roots
	// (host-inputs, shadow, token-handoff) with the reviewed access modes.
	{
		kind:         kindFunctionBlock,
		functionName: "start_managed_profile",
		pattern:      "workcell-host-inputs",
		message:      "Expected managed Colima launch to mount Workcell staging cache roots with reviewed access modes",
	},
	{
		kind:         kindFunctionBlock,
		functionName: "start_managed_profile",
		pattern:      "workcell-shadow",
		message:      "Expected managed Colima launch to mount Workcell staging cache roots with reviewed access modes",
	},
	{
		kind:         kindFunctionBlock,
		functionName: "start_managed_profile",
		pattern:      "workcell-token-handoff",
		message:      "Expected managed Colima launch to mount Workcell staging cache roots with reviewed access modes",
	},
	{
		kind:         kindFunctionBlock,
		functionName: "start_managed_profile",
		pattern:      `--mount "${host_inputs_root}"`,
		message:      "Expected managed Colima launch to mount Workcell staging cache roots with reviewed access modes",
	},
	{
		kind:         kindFunctionBlock,
		functionName: "start_managed_profile",
		pattern:      `--mount "${shadow_root}"`,
		message:      "Expected managed Colima launch to mount Workcell staging cache roots with reviewed access modes",
	},
	{
		kind:         kindFunctionBlock,
		functionName: "start_managed_profile",
		pattern:      `--mount "${token_handoff_root}:w"`,
		message:      "Expected managed Colima launch to mount Workcell staging cache roots with reviewed access modes",
	},
	// Guard 2: staging cache roots reject symlinked host components before
	// staging or mounting.  The reject_symlinked_colima_staging_cache_roots
	// probe is a whole-file `rg -q` (kindPresent); the three
	// prepare_colima_staging_cache_roots probes are block-scoped to the
	// three functions that must call it before staging or mounting.
	{
		kind:    kindPresent,
		pattern: "reject_symlinked_colima_staging_cache_roots",
		message: "Expected Workcell staging cache roots to reject symlinked host components before staging or mounting",
	},
	{
		kind:         kindFunctionBlock,
		functionName: "prepare_injection_bundle",
		pattern:      "prepare_colima_staging_cache_roots",
		message:      "Expected Workcell staging cache roots to reject symlinked host components before staging or mounting",
	},
	{
		kind:         kindFunctionBlock,
		functionName: "prepare_workspace_control_plane_shadow",
		pattern:      "prepare_colima_staging_cache_roots",
		message:      "Expected Workcell staging cache roots to reject symlinked host components before staging or mounting",
	},
	{
		kind:         kindFunctionBlock,
		functionName: "start_managed_profile",
		pattern:      "prepare_colima_staging_cache_roots",
		message:      "Expected Workcell staging cache roots to reject symlinked host components before staging or mounting",
	},
	// Guard 3: stale injection cleanup fails closed when the default bundle
	// parent is rejected.  The bare
	// `cleanup_stale_injection_bundles "$(default_injection_bundle_parent)"`
	// call (unbraced command substitution, no fail-closed capture) is a
	// violation, so it is a negated `rg -q` (kindAbsent: present → exit 1);
	// the four fail-closed probes are block-scoped to
	// cleanup_default_injection_bundles.
	{
		kind:    kindAbsent,
		pattern: `cleanup_stale_injection_bundles "$(default_injection_bundle_parent)"`,
		message: "Expected stale injection cleanup to fail closed when the default bundle parent is rejected",
	},
	{
		kind:         kindFunctionBlock,
		functionName: "cleanup_default_injection_bundles",
		pattern:      `bundle_parent="$(default_injection_bundle_parent)" || return $?`,
		message:      "Expected stale injection cleanup to fail closed when the default bundle parent is rejected",
	},
	{
		kind:         kindFunctionBlock,
		functionName: "cleanup_default_injection_bundles",
		pattern:      `token_handoff_parent="$(default_copilot_token_handoff_parent)" || return $?`,
		message:      "Expected stale injection cleanup to fail closed when the default bundle parent is rejected",
	},
	{
		kind:         kindFunctionBlock,
		functionName: "cleanup_default_injection_bundles",
		pattern:      `cleanup_stale_injection_bundles "${bundle_parent}"`,
		message:      "Expected stale injection cleanup to fail closed when the default bundle parent is rejected",
	},
	{
		kind:         kindFunctionBlock,
		functionName: "cleanup_default_injection_bundles",
		pattern:      `cleanup_stale_injection_bundles "${token_handoff_parent}"`,
		message:      "Expected stale injection cleanup to fail closed when the default bundle parent is rejected",
	},
}

// CheckManagedProfileStaging runs the three scripts/workcell
// managed-profile staging/cleanup invariants against the repo rooted at
// rootDir, in the shell's original order.  It returns nil when every
// invariant holds (the shell's exit 0), or an error whose message equals
// the shell's stderr for the first violated invariant (the shell's exit
// 1).
func CheckManagedProfileStaging(rootDir string) error {
	return evaluate(rootDir, managedProfileStagingChecks)
}

// bootstrapEgressChecks lists the nine bootstrap egress-endpoint
// invariants in the same order as the former inline block in
// scripts/verify-invariants.sh (the block between the
// colima-egress-allowlist COLIMA_HOME pin check and the per-Dockerfile
// snapshot CA-bundle loop), so a reviewer can diff the two one-to-one.
//
// Eight probes read scripts/workcell (the default target); the
// Copilot-release-URL-override probe reads runtime/container/Dockerfile
// (targetFile: dockerfileRelPath), mirroring the one shell `rg` that ran
// against the Dockerfile rather than the launcher.
//
// Matching semantics mirror the shell exactly:
//   - The seven fixed-string probes had every rg metacharacter escaped
//     (`snapshot\.debian\.org:443`, `resolve_copilot_release_url\(\)`,
//     `--build-arg "COPILOT_RELEASE_URL=\$\{copilot_release_url\}"`, ...),
//     so they reduce to fixed-string containment: kindPresent for the
//     affirmative `rg -q`, kindAbsent for the two negated guards
//     (static.rust-lang.org:443 and snapshot.debian.org:80 present are
//     violations).
//   - The R2 blob-storage probe is a genuine regex — its `[^.]+` is a
//     one-or-more-non-dot subdomain wildcard — so it is kindRegexPresent
//     with the pattern verbatim.  RE2 matches the same hosts as rg (e.g.
//     docker-images-prod.abc123.r2.cloudflarestorage.com:443).
//   - The Dockerfile Copilot-release probe anchored `^ARG` to a line
//     start; rg is line-based, so in Go it is a kindRegexPresent with a
//     multiline `(?m)^ARG COPILOT_RELEASE_URL=` pattern that anchors to any
//     line start of the Dockerfile.
var bootstrapEgressChecks = []check{
	{
		kind:    kindPresent,
		pattern: "snapshot.debian.org:443",
		message: "Expected scripts/workcell bootstrap endpoints to allow snapshot.debian.org",
	},
	{
		kind:    kindPresent,
		pattern: "snapshot-cloudflare.debian.org:443",
		message: "Expected scripts/workcell bootstrap endpoints to allow the snapshot-cloudflare.debian.org CDN mirror",
	},
	{
		// kindAbsent: an unused static.rust-lang.org egress entry is a
		// violation (present → exit 1).
		kind:    kindAbsent,
		pattern: "static.rust-lang.org:443",
		message: "Expected scripts/workcell bootstrap endpoints to avoid unused static.rust-lang.org egress",
	},
	{
		// kindRegexPresent: the `[^.]+` subdomain wildcard is a genuine
		// regex, so this matches as a regex rather than a fixed string.
		kind:    kindRegexPresent,
		pattern: `docker-images-prod\.[^.]+\.r2\.cloudflarestorage\.com:443`,
		message: "Expected scripts/workcell bootstrap endpoints to allow Docker blob storage on Cloudflare R2",
	},
	{
		kind:    kindPresent,
		pattern: "production.cloudfront.docker.com:443",
		message: "Expected scripts/workcell bootstrap endpoints to allow Docker blob storage on CloudFront",
	},
	{
		// kindRegexPresent against runtime/container/Dockerfile: the shell's
		// line-anchored `^ARG COPILOT_RELEASE_URL=` becomes a multiline
		// `(?m)^ARG ...` so `^` anchors to any Dockerfile line start.
		kind:       kindRegexPresent,
		pattern:    `(?m)^ARG COPILOT_RELEASE_URL=`,
		message:    "Expected runtime Dockerfile to accept a host-resolved Copilot release URL override",
		targetFile: dockerfileRelPath,
	},
	{
		// kindPresent: the shell's `resolve_copilot_release_url\(\)` escaped
		// its parens, so it is fixed-string containment of the literal
		// `resolve_copilot_release_url()`.
		kind:    kindPresent,
		pattern: "resolve_copilot_release_url()",
		message: "Expected scripts/workcell to resolve Copilot release URLs on the host before runtime builds",
	},
	{
		// kindPresent: the shell's
		// `--build-arg "COPILOT_RELEASE_URL=\$\{copilot_release_url\}"`
		// escaped every metacharacter, so it is fixed-string containment of
		// the literal --build-arg invocation.
		kind:    kindPresent,
		pattern: `--build-arg "COPILOT_RELEASE_URL=${copilot_release_url}"`,
		message: "Expected scripts/workcell runtime builds to pass host-resolved Copilot release URLs into Docker",
	},
	{
		// kindAbsent: an unused snapshot.debian.org:80 (plaintext) egress
		// entry is a violation (present → exit 1).
		kind:    kindAbsent,
		pattern: "snapshot.debian.org:80",
		message: "Expected scripts/workcell bootstrap endpoints to avoid unused snapshot.debian.org:80 egress",
	},
}

// CheckBootstrapEgress runs the nine bootstrap egress-endpoint invariants
// against the repo rooted at rootDir, in the shell's original order.  It
// returns nil when every invariant holds (the shell's exit 0), or an error
// whose message equals the shell's stderr for the first violated invariant
// (the shell's exit 1).
func CheckBootstrapEgress(rootDir string) error {
	return evaluate(rootDir, bootstrapEgressChecks)
}

// bootstrapAuditMetadataChecks lists the two scripts/workcell
// bootstrap-audit-metadata invariants in the same order as the former
// inline block in scripts/verify-invariants.sh (the block between the
// verify-reproducible-build.sh OCI-export check and the
// validate_colima_profile function-block group), so a reviewer can diff the
// two one-to-one.
//
// The audit-record guard was one shell `if` joining two `rg -q` probes with
// `||` under a single message; it is expressed here as two ordered
// kindPresent checks sharing that message, which is behaviourally identical
// (either missing probe yields the same stderr and exit 1, before the
// bootstrap-policy check runs).  Both rg patterns escape every
// metacharacter (`\$ \{ \} \( \[ \] \| \)`), so they reduce to fixed-string
// containment of the literal audit-record fields (bootstrap_applied and
// bootstrap_endpoints) emitted by scripts/workcell; the second field's
// literal command substitution is reproduced verbatim in the check pattern
// below.
//
// The bootstrap-policy guard's rg pattern
// `bootstrap_policy=allowlist endpoints=%s` has no active metacharacters, so
// it too is fixed-string containment.
var bootstrapAuditMetadataChecks = []check{
	{
		// kindPresent (first half of the audit-record guard): the
		// bootstrap_applied field.
		kind:    kindPresent,
		pattern: `"bootstrap_applied=${BOOTSTRAP_APPLIED}"`,
		message: "Expected scripts/workcell audit records to include bootstrap network metadata",
	},
	{
		// kindPresent (second half): the bootstrap_endpoints field.  The rg
		// pattern escaped `$ ( [ ] | )`, so this is fixed-string containment
		// of the literal command substitution.  Shares the first half's
		// message.
		kind:    kindPresent,
		pattern: `"bootstrap_endpoints=$([[ "${BOOTSTRAP_APPLIED}" -eq 1 ]] && printf '%s' "${BOOTSTRAP_ENDPOINTS}" || printf '')"`,
		message: "Expected scripts/workcell audit records to include bootstrap network metadata",
	},
	{
		// kindPresent: the temporary bootstrap network policy activation
		// announcement.
		kind:    kindPresent,
		pattern: "bootstrap_policy=allowlist endpoints=%s",
		message: "Expected scripts/workcell to announce temporary bootstrap network policy activation",
	},
}

// CheckBootstrapAuditMetadata runs the two scripts/workcell
// bootstrap-audit-metadata invariants against the repo rooted at rootDir, in
// the shell's original order.  It returns nil when every invariant holds
// (the shell's exit 0), or an error whose message equals the shell's stderr
// for the first violated invariant (the shell's exit 1).
func CheckBootstrapAuditMetadata(rootDir string) error {
	return evaluate(rootDir, bootstrapAuditMetadataChecks)
}

// gitIndexShadowChecks lists the five scripts/workcell git-index shadow
// invariants in the same order as the former inline block in
// scripts/verify-invariants.sh (the block between the runtime/container/bin/git
// object-store-redirection loop and the validate-repo.sh virtualenv-prune
// check), so a reviewer can diff the two one-to-one.
//
// All five are block-scoped to a named function body, mirroring the shell's
// function_block_contains_regex / function_block_contains_fixed helpers (both
// use extract_named_function_block, i.e. `sed -n '/^NAME()/,/^}/p'`):
//
//   - The three kindFunctionBlockRegex checks migrate
//     function_block_contains_regex (`grep -q` — a genuine regex).  Their
//     patterns (cat-file blob, failed to read tracked blob,
//     git_config_key_is_blocked) contain no active regex metacharacters, so
//     they behave like fixed-string containment today, but the kind is a
//     genuine regex for correctness under future patterns.
//   - The two kindFunctionBlock checks migrate function_block_contains_fixed
//     (`grep -Fq` — fixed-string containment).  The git_index_populate_shadow_dir
//     needle `*/../*` contains `*` which grep -Fq treats literally, so it is a
//     fixed-string containment of the literal `*/../*`.
var gitIndexShadowChecks = []check{
	{
		kind:         kindFunctionBlockRegex,
		functionName: "git_index_materialize_regular_file",
		pattern:      "cat-file blob",
		message:      "Expected git_index_materialize_regular_file to materialize tracked blobs without checkout-index",
	},
	{
		kind:         kindFunctionBlockRegex,
		functionName: "git_index_materialize_regular_file",
		pattern:      "failed to read tracked blob",
		message:      "Expected git_index_materialize_regular_file to fail closed when a tracked control-plane blob is unreadable",
	},
	{
		// kindFunctionBlock (function_block_contains_fixed): fixed-string
		// containment of the literal partial-file cleanup.
		kind:         kindFunctionBlock,
		functionName: "git_index_materialize_regular_file",
		pattern:      `rm -f "${destination_path}"`,
		message:      "Expected git_index_materialize_regular_file to remove partially materialized files after blob read failures",
	},
	{
		// kindFunctionBlock (function_block_contains_fixed): `grep -Fq` treats
		// the `*` in `*/../*` literally, so this is fixed-string containment.
		kind:         kindFunctionBlock,
		functionName: "git_index_populate_shadow_dir",
		pattern:      "*/../*",
		message:      "Expected git_index_populate_shadow_dir to reject unsafe index paths before shadow materialization",
	},
	{
		kind:         kindFunctionBlockRegex,
		functionName: "sanitize_shadowed_git_config",
		pattern:      "git_config_key_is_blocked",
		message:      "Expected sanitize_shadowed_git_config to reuse the shared blocked git-config key matcher",
	},
}

// CheckGitIndexShadow runs the five scripts/workcell git-index shadow
// invariants against the repo rooted at rootDir, in the shell's original
// order.  It returns nil when every invariant holds (the shell's exit 0), or
// an error whose message equals the shell's stderr for the first violated
// invariant (the shell's exit 1).
func CheckGitIndexShadow(rootDir string) error {
	return evaluate(rootDir, gitIndexShadowChecks)
}

// publishPrShadowMountChecks lists the four scripts/workcell publish-PR /
// shadow-mount invariants in the same order as the former inline block in
// scripts/verify-invariants.sh (the block between the
// publishpr.ValidateBaseName checkRefFormat check and the
// prepare_workspace_control_plane_shadow `find ... -name .git` check), so a
// reviewer can diff the two one-to-one.
//
// All four are block-scoped to a named function body, mirroring the shell's
// function_block_contains_regex / function_block_contains_fixed helpers (both
// use extract_named_function_block, i.e. `sed -n '/^NAME()/,/^}/p'`):
//
//   - The two kindFunctionBlockRegex checks migrate
//     function_block_contains_regex (`grep -q` — a genuine regex).  The
//     core.hooksPath probe's `.` characters are regex any-char metacharacters
//     (as `grep`/`rg` would treat them), so the pattern is used verbatim as a
//     regex.  The --no-verify probe is metacharacter-free, but it is kept a
//     regex kind for fidelity with the shell's function_block_contains_regex.
//   - The two kindFunctionBlock checks migrate function_block_contains_fixed
//     (`grep -Fq` — fixed-string containment).  The add_shadow_git_config_mount
//     needle is the literal `! -L "${source_path}"` (the shell wrote it as
//     "! -L \"\${source_path}\""), matched as a fixed string.
var publishPrShadowMountChecks = []check{
	{
		// kindFunctionBlockRegex: the `.` chars in core.hooksPath=/dev/null are
		// regex any-char metacharacters, used verbatim as in the shell's
		// function_block_contains_regex (`grep -q`).
		kind:         kindFunctionBlockRegex,
		functionName: "publish_pr_main",
		pattern:      "core.hooksPath=/dev/null",
		message:      "Expected publish_pr_main to disable repo hooks for host-side publish git commands",
	},
	{
		// kindFunctionBlockRegex: metacharacter-free, but kept a regex kind for
		// fidelity with the shell's function_block_contains_regex.
		kind:         kindFunctionBlockRegex,
		functionName: "publish_pr_main",
		pattern:      "--no-verify",
		message:      "Expected publish_pr_main to bypass repo hooks explicitly on host-side commit and push",
	},
	{
		// kindFunctionBlock (function_block_contains_fixed): fixed-string
		// containment of the symlink-free copy helper.
		kind:         kindFunctionBlock,
		functionName: "add_shadow_git_hooks_mount",
		pattern:      "copy_tree_without_symlinks",
		message:      "Expected add_shadow_git_hooks_mount to avoid copying symlinked hook content into the readonly shadow",
	},
	{
		// kindFunctionBlock (function_block_contains_fixed): the shell needle
		// "! -L \"\${source_path}\"" is the literal `! -L "${source_path}"`,
		// matched as a fixed string.
		kind:         kindFunctionBlock,
		functionName: "add_shadow_git_config_mount",
		pattern:      `! -L "${source_path}"`,
		message:      "Expected add_shadow_git_config_mount to ignore symlinked git config files",
	},
}

// CheckPublishPrShadowMounts runs the four scripts/workcell publish-PR /
// shadow-mount invariants against the repo rooted at rootDir, in the shell's
// original order.  It returns nil when every invariant holds (the shell's exit
// 0), or an error whose message equals the shell's stderr for the first
// violated invariant (the shell's exit 1).
func CheckPublishPrShadowMounts(rootDir string) error {
	return evaluate(rootDir, publishPrShadowMountChecks)
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
	case kindFunctionBlockRegex:
		block := extractNamedFunctionBlock(text, c.functionName)
		return regexMatchesAnyLine(c.pattern, block)
	case kindFirstLineRegex:
		return regexp.MustCompile(c.pattern).MatchString(firstLine(text))
	case kindPresent:
		return strings.Contains(text, c.pattern)
	case kindAbsent:
		return !strings.Contains(text, c.pattern)
	case kindRegexAbsent:
		return !regexMatchesAnyLine(c.pattern, text)
	case kindRegexPresent:
		return regexMatchesAnyLine(c.pattern, text)
	default:
		return false
	}
}

// regexMatchesAnyLine reports whether pattern matches any single line of text,
// emulating ripgrep's default line-oriented matching: without `--multiline`, an
// `rg` match cannot cross a line terminator, so a negated class like `[^.]+`
// never spans a newline. Matching whole-file would let such a class consume `\n`
// and diverge from the migrated `rg -q` probe.
func regexMatchesAnyLine(pattern, text string) bool {
	re := regexp.MustCompile(pattern)
	for _, line := range strings.Split(text, "\n") {
		if re.MatchString(line) {
			return true
		}
	}
	return false
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
