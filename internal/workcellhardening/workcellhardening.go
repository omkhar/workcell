// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

// Package workcellhardening provides Go-native, unit-tested implementations
// of the static-invariant checks that previously lived inline in
// scripts/verify-invariants.sh (roadmap item D3). It is the enforcement
// mechanism behind the security invariants described in docs/invariants.md;
// this package is about HOW those invariants are checked, not what they mean.
//
// # Architecture
//
// Each migrated group is a CheckXxx(rootDir) error function that delegates to
// evaluate(rootDir, []check). A check declares one kind plus the fields that
// kind consumes (targetFile / targetFiles / targetPath, pattern / regex /
// functionName, jsonPath / jsonExpected, minCount, message). evaluate runs the
// checks in order and returns the first violation's message — so the byte-exact
// FIRST stderr line on a multi-violation run is preserved, matching the original
// script's exit-on-first-failure behaviour.
//
// Two groups are the exception to the rootDir convention:
// CheckClaudeMcpProjectServers and CheckClaudeGuardBashHook each take a single
// settings-file path (their subcommands are registered as SETTINGS_PATH and
// invoked once per file inside a shell for-loop over the claude settings paths),
// rather than a repo root.
//
// cmd/workcell-citools exposes each CheckXxx as a subcommand, and
// scripts/verify-invariants.sh delegates to it in place with
// `go_verify_citools <subcommand> "${ROOT_DIR}" || exit 1`. Because the script
// still exits on the first failure, a migrated block must be a CONTIGUOUS run
// of checks: a block that straddles an in-place shell check (or a deferred
// check) is split so ordering is never changed (see the ordering discipline in
// the D3 commit history / PR #422).
//
// # Check kinds
//
// The check.kind enum spans every matching idiom the shell used:
//
//   - Fixed-string containment: kindPresent / kindAbsent (grep -Fq).
//   - Regex containment, evaluated PER LINE to mirror ripgrep's line-oriented
//     semantics (a match cannot cross a newline): kindRegexPresent /
//     kindRegexAbsent (see regexMatchesAnyLine), and kindFirstLineRegex for
//     `head -n1 | grep -q '^...$'`.
//   - Bash function-body scoping via extractNamedFunctionBlock (mirrors
//     `sed -n '/^NAME()/,/^}/p'`): kindFunctionBlock / kindFunctionBlockAbsent
//     (fixed) and kindFunctionBlockRegex / kindFunctionBlockRegexAbsent
//     (per-line regex).
//   - Go function-body scoping with comment stripping (mirrors the
//     go_function_block_contains_fixed awk helper): kindGoFunctionBlock (see
//     extractGoFunctionBlock).
//   - Multi-file OR (`! grep -Fq NEEDLE f1 f2`, present in ANY listed file):
//     kindPresentInAnyFile over targetFiles.
//   - Line-count threshold (`[[ "$(grep -Fc NEEDLE f)" -lt N ]]`, counting
//     matching LINES): kindCountAtLeast with minCount.
//   - Filesystem state (no content read): kindDirExists (`[[ -d ]]`, os.Stat +
//     IsDir), kindExecutable (`[[ -x ]]`, syscall.Access with X_OK — the
//     same access(2) primitive Bash's -x uses), and kindFileExists (`[[ -f ]]`,
//     os.Stat + Mode().IsRegular()).
//   - Typed JSON expression (`jq -e '.a.b == <literal>'` on a static file):
//     kindJSONExprEval, comparing with jq's type-aware == (see navigateJSONPath),
//     which also supports array-index navigation (`.a[0].b`) and an empty-array
//     RHS literal (`== []`).
//   - Bare-path JSON truthiness (`jq -e '.a.b'` — exit 0 iff the value is neither
//     null nor false): kindJSONPathTruthy, with jsonViolateWhenTruthy to express
//     the inverted `if jq -e` polarity.
//   - Piped JSON type test (`jq -e '.a.b | type == "T"'`): kindJSONTypeEquals.
//
// # Parity discipline
//
// Every migration is behaviour-preserving: the file read target, fixed-vs-regex
// semantics, exit codes, and stderr messages match the shell byte-for-byte.
// Regex kinds are per-line for rg parity; the go-function-block and JSON-expr
// kinds are validated by differential tests that shell out to the real awk/jq.
// Every static-file group carries a real-repo assertion (a TestCheckXxxRealRepo
// that runs the check against the actual repo files) plus a count guard, so a
// mistranscribed needle or path is caught by CI against the shipped artifact,
// not just synthetic happy/negative fixtures. The Claude and Gemini
// adapter-settings groups (CheckClaudeMcpProjectServers, CheckClaudeGuardBashHook,
// CheckClaudeManagedBypass, CheckGeminiSettingsBaseline, CheckGeminiSettingsGuards)
// were the last groups to gain that assertion, closing the D3 real-repo backfill
// tail.
//
// # Scope and residual
//
// The BULK of scripts/verify-invariants.sh's static file-content,
// JSON-structural, and filesystem invariants are implemented here (see the
// CheckXxx functions), and every static-file group here carries a real-repo
// assertion. But the migration is NOT complete: scripts/verify-invariants.sh
// remains the orchestration entrypoint and still runs a residual of inline
// checks, and that residual is NOT entirely non-static. It splits into two
// groups.
//
// (1) Static repo-file assertions that REMAIN in bash and are still migratable
// — these read a static repo file and assert on its content, so they CAN move
// here in a further increment and must NOT be read as "done". The literal
// D3-complete exit gate is migrating (or thin-shimming) this remainder; it is a
// remaining lane, not already-drained. A concrete example: the run_in_vm
// awk-ordering block in scripts/verify-invariants.sh (kept inline next to the
// migrated workcell-dualstack-apply-plan check) `sed`-extracts the run_in_vm()
// function from ${ROOT_DIR}/scripts/colima-egress-allowlist.sh and awk-asserts
// its initialize-order — a static repo-file content assertion still outside the
// Go engine. A narrow `rg -q`/`grep -Fq` census understates this remainder
// because such probes use sed/awk rather than rg/grep. The adapter-settings
// `jq -e` guards that went beyond `.<path> == <scalar>` (array equality `== []`,
// array index `[0]`, bare-path truthiness, and `| type`) ARE ported (see
// CheckClaudeGuardBashHook, CheckGeminiSettingsBaseline, and
// CheckGeminiSettingsGuards), each now with a real-repo assertion — but they are
// not the whole static residual.
//
// (2) Checks that are NOT expressible as a static-file invariant and are
// intentionally left in bash:
//
//   - `jq -r` field-equals guards over injection-bundle manifests that are
//     GENERATED at runtime into a `mktemp -d` fixture directory (no static repo
//     path for evaluate to read).
//   - `jq -e` guards reading STDIN from a pipe (e.g. `execpolicy check | jq`).
//   - the Claude guard-bash array-collection guard
//     (`.hooks.*.hooks | any(type == "string" and endswith("guard-bash.sh"))`),
//     which needs array iteration plus jq's `any`/`endswith` builtins — beyond a
//     clean static port and left inline permanently.
//   - awk/sed helper FUNCTIONS (toml-section and disk-space parsing) that are
//     procedural infrastructure used by other checks, not invariants
//     themselves.
//   - HOST_GATE_SCRIPTS self-entrypoint/BASH_ENV harnesses and mktemp-capture
//     runtime-execution assertions that actually run the launcher or a script.
//
// Group (1) is a remaining migration lane (maintainer-scoped: migrate the static
// remainder or thin-shim it, preserving first-failure order). Group (2) would
// require re-introducing a jq/awk dependency in Go or refactoring the runtime
// fixtures into static inputs — a separate workstream, or (for the any/endswith
// guard) is a permanent bash residual.
package workcellhardening

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"syscall"
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

// validatorDockerfileRelPath is the repo-relative path to the validator image
// Dockerfile.  The dockerfile-pins block reads it alongside dockerfileRelPath
// (via the per-check targetFile field) for its snapshot-TLS-bootstrap pin and
// unprivileged-USER invariants, mirroring the shell `rg` probes that iterated
// ${ROOT_DIR}/runtime/container/Dockerfile and ${ROOT_DIR}/tools/validator/Dockerfile.
const validatorDockerfileRelPath = "tools/validator/Dockerfile"

// colimaEgressAllowlistRelPath is the repo-relative path to the Colima
// egress-allowlist helper.  Only the two shadow-enum-egress IPv6 invariants
// read this file instead of scripts/workcell (via the per-check targetFile
// field), mirroring the two shell `rg` probes that ran against
// ${ROOT_DIR}/scripts/colima-egress-allowlist.sh.
const colimaEgressAllowlistRelPath = "scripts/colima-egress-allowlist.sh"

// homeControlPlaneRelPath is the repo-relative path to the runtime home
// control-plane seeding script.  The home-seed/provider-wrapper block reads
// this file (via the per-check targetFile field) for its Gemini/Claude home
// seeding invariants, mirroring the shell probes that ran against
// ${ROOT_DIR}/runtime/container/home-control-plane.sh.
const homeControlPlaneRelPath = "runtime/container/home-control-plane.sh"

// providerWrapperRelPath is the repo-relative path to the runtime provider
// wrapper.  The home-seed/provider-wrapper block reads this file for its
// env-scrub invariants, mirroring the shell probes that ran against
// ${ROOT_DIR}/runtime/container/provider-wrapper.sh.
const providerWrapperRelPath = "runtime/container/provider-wrapper.sh"

// developmentWrapperRelPath is the repo-relative path to the runtime
// development wrapper.  Only the copilot_env scrub loop reads this file (as
// the loop's second wrapper), mirroring the shell probes that ran against
// ${ROOT_DIR}/runtime/container/development-wrapper.sh.
const developmentWrapperRelPath = "runtime/container/development-wrapper.sh"

// containerSmokeRelPath is the repo-relative path to the container smoke
// harness.  The Copilot-token-handoff leaf-permission guard reads this file
// (via the per-check targetFile field) for its stage_copilot_token_handoff_dir
// chmod probes, and the Copilot-docker-run group reads it for its
// Docker-inspect-metadata leak probes, mirroring the shell probes that ran
// against ${ROOT_DIR}/scripts/container-smoke.sh.
const containerSmokeRelPath = "scripts/container-smoke.sh"

// hoststateRelPath is the repo-relative path to the host-state Go source.
// Only the Copilot-docker-run legacy-env-file-cleanup invariant reads this
// file (via the per-check targetFile field), mirroring the shell `grep -Fq`
// that ran against ${ROOT_DIR}/internal/host/hoststate/hoststate.go.
const hoststateRelPath = "internal/host/hoststate/hoststate.go"

// launcherCommonRustRelPath is the repo-relative path to the shared runtime
// launcher Rust helper.  Only the Copilot-docker-run auth-metadata invariant
// reads this file (via the per-check targetFile field), mirroring the shell
// `grep -Fq` that ran against
// ${ROOT_DIR}/runtime/container/rust/src/bin/common/launcher_common.rs.
const launcherCommonRustRelPath = "runtime/container/rust/src/bin/common/launcher_common.rs"

// workcellLauncherRustRelPath is the repo-relative path to the runtime
// workcell-launcher Rust binary.  Only the Copilot-docker-run auth-metadata
// invariant reads this file (via the per-check targetFile field), mirroring the
// shell `grep -Fq` that ran against
// ${ROOT_DIR}/runtime/container/rust/src/bin/workcell-launcher.rs.
const workcellLauncherRustRelPath = "runtime/container/rust/src/bin/workcell-launcher.rs"

// entrypointRelPath is the repo-relative path to the runtime container
// entrypoint.  The Copilot-docker-run group reads this file (via the per-check
// targetFile field) for its token-handoff staging / self-reexec / mapped-user
// invariants, mirroring the shell probes that ran against
// ${ROOT_DIR}/runtime/container/entrypoint.sh.  The hostutil/egress-rg block
// also reads it for its Codex --cd / AGENT_NAME / file-trace-trap invariants.
const entrypointRelPath = "runtime/container/entrypoint.sh"

// goHostutilRelPath is the repo-relative path to the host launcher's Go
// bootstrap helper.  Only the hostutil/egress-rg block reads this file (via the
// per-check targetFile field) for its scrubbed-environment bootstrap-Go
// invariants, mirroring the shell `rg` probes that ran against
// ${ROOT_DIR}/scripts/lib/launcher/go-hostutil.sh.
const goHostutilRelPath = "scripts/lib/launcher/go-hostutil.sh"

// runtimeUserRelPath is the repo-relative path to the runtime user helper.
// Only the Copilot-docker-run runtime-state-path invariant reads this file (via
// the per-check targetFile field), mirroring the shell `grep -Fq` that ran
// against ${ROOT_DIR}/runtime/container/runtime-user.sh.
const runtimeUserRelPath = "runtime/container/runtime-user.sh"

// rustLibRelPath is the repo-relative path to the runtime exec-guard Rust
// library.  Only the provider-launcher-authority exec-guard invariant reads
// this file (via the per-check targetFile field), mirroring the two shell
// `grep -Fq` probes that ran against
// ${ROOT_DIR}/runtime/container/rust/src/lib.rs.
const rustLibRelPath = "runtime/container/rust/src/lib.rs"

// providerPolicyRelPath is the repo-relative path to the runtime provider
// policy helper.  The Copilot-policy-wrapper block reads this file (via the
// per-check targetFile field) for its native-lifecycle-command and
// prompt/short-option gating invariants, mirroring the shell probes that ran
// against ${ROOT_DIR}/runtime/container/provider-policy.sh.
const providerPolicyRelPath = "runtime/container/provider-policy.sh"

// verifyUpstreamCopilotReleaseRelPath is the repo-relative path to the Copilot
// upstream-release verifier.  The Copilot-release-verify block reads this file
// (via the per-check targetFile field) for its help-mode / checksum-path /
// whole-flag-match invariants and its managed-flag loop, mirroring the shell
// `grep -Fq` probes that ran against
// ${ROOT_DIR}/scripts/verify-upstream-copilot-release.sh.
const verifyUpstreamCopilotReleaseRelPath = "scripts/verify-upstream-copilot-release.sh"

// updateProviderPinsRelPath is the repo-relative path to the provider-pin bump
// script.  Only the Copilot-release-verify checksum-only invariant reads this
// file (via the per-check targetFile field), mirroring the shell `grep -Fq`
// that ran against ${ROOT_DIR}/scripts/update-provider-pins.sh.
const updateProviderPinsRelPath = "scripts/update-provider-pins.sh"

// jobValidateRelPath is the repo-relative path to the routine CI validate job.
// Only the Copilot-release-verify checksum-only invariant reads this file (via
// the per-check targetFile field), mirroring the shell `grep -Fq` that ran
// against ${ROOT_DIR}/scripts/ci/job-validate.sh.
const jobValidateRelPath = "scripts/ci/job-validate.sh"

// releaseWorkflowRelPath is the repo-relative path to the release workflow.
// The Copilot-release-verify block reads this file (via the per-check
// targetFile field) for its docker/arm64 release-help invariants, mirroring the
// shell `grep -Fq` probes that ran against
// ${ROOT_DIR}/.github/workflows/release.yml.
const releaseWorkflowRelPath = ".github/workflows/release.yml"

// codexManagedConfigRelPath and codexRequirementsRelPath are the repo-relative
// paths to the two Codex adapter rule files.  The adapter-rule/guard-bash block
// reads both (via the per-check targetFile field) for its provider-mediation
// bypass-path invariants, mirroring the shell `grep -Fq` probes that ran against
// ${ROOT_DIR}/adapters/codex/managed_config.toml and
// ${ROOT_DIR}/adapters/codex/requirements.toml in the codex_rule_file loop.
const codexManagedConfigRelPath = "adapters/codex/managed_config.toml"
const codexRequirementsRelPath = "adapters/codex/requirements.toml"

// claudeGuardBashRelPath is the repo-relative path to the Claude adapter Bash
// guard hook.  The adapter-rule/guard-bash block reads this file (via the
// per-check targetFile field) for its provider-mediation and home
// control-plane bypass-path invariants, mirroring the shell `grep -Fq` probes
// that ran against ${ROOT_DIR}/adapters/claude/hooks/guard-bash.sh.
const claudeGuardBashRelPath = "adapters/claude/hooks/guard-bash.sh"

// assuranceRelPath is the repo-relative path to the in-container session
// assurance script.  The inspect-assurance-loops block's audit-log-field loop
// reads this file alongside scripts/workcell (via the kindPresentInAnyFile
// targetFiles field), mirroring the shell's multi-file
// `grep -Fq -- FIELD scripts/workcell runtime/container/assurance.sh`.
const assuranceRelPath = "runtime/container/assurance.sh"

// validateRepoRelPath is the repo-relative path to the in-validator repo
// validation script.  The validator-dispatch block reads this file (via the
// per-check targetFile field) for its Cargo-target externalization invariant,
// mirroring the shell `grep -Fq` probes that ran against
// ${ROOT_DIR}/scripts/validate-repo.sh.
const validateRepoRelPath = "scripts/validate-repo.sh"

// ciWorkflowRelPath, docsWorkflowRelPath, and mutationWorkflowRelPath are the
// repo-relative paths to the three lane workflows the validator-dispatch block's
// dispatch loop probes (via the per-check targetFile field), mirroring the shell
// `grep -Fq --` probes that ran against ${ROOT_DIR}/.github/workflows/ci.yml,
// docs.yml, and mutation.yml.
const ciWorkflowRelPath = ".github/workflows/ci.yml"
const docsWorkflowRelPath = ".github/workflows/docs.yml"
const mutationWorkflowRelPath = ".github/workflows/mutation.yml"

// preMergeRelPath is the repo-relative path to the pre-merge helper.  The
// validator-dispatch block's dispatch loop reads this file twice (via the
// per-check targetFile field) for its job-validate and job-docs dispatch
// invariants, mirroring the shell `grep -Fq --` probes that ran against
// ${ROOT_DIR}/scripts/pre-merge.sh.
const preMergeRelPath = "scripts/pre-merge.sh"

// runValidateInValidatorRelPath, runDocsInValidatorRelPath, and
// runMutationInValidatorRelPath are the repo-relative paths to the three
// in-validator lane launchers.  The caller-required-contracts block reads them
// (via the per-check targetFile field) as the first three callers of its nested
// caller×required loop, mirroring the shell `grep -Fq --` probes that ran
// against ${ROOT_DIR}/scripts/ci/run-validate-in-validator.sh,
// run-docs-in-validator.sh, and run-mutation-in-validator.sh.  (jobValidateRelPath
// and releaseWorkflowRelPath cover the remaining two callers.)
const runValidateInValidatorRelPath = "scripts/ci/run-validate-in-validator.sh"
const runDocsInValidatorRelPath = "scripts/ci/run-docs-in-validator.sh"
const runMutationInValidatorRelPath = "scripts/ci/run-mutation-in-validator.sh"

// hostExecRelPath is the repo-relative path to the Go host-exec owner that
// inherited resolve_existing_executable_or_die from scripts/workcell.  The
// fnblock-goblock-gitenv block reads this file (via the per-check targetFile
// field) for its kindGoFunctionBlock probe, mirroring the shell
// `go_function_block_contains_fixed ${ROOT_DIR}/internal/publishpr/host_exec.go`.
const hostExecRelPath = "internal/publishpr/host_exec.go"

// publishPrMainRelPath is the repo-relative path to the Go publish-pr owner
// that inherited validate_publish_base_name from scripts/workcell.  The
// publish-base-refcheck block reads this file (via the per-check targetFile
// field) for its kindGoFunctionBlock probe, mirroring the shell
// `go_function_block_contains_fixed ${ROOT_DIR}/internal/publishpr/publish_pr_main.go`.
const publishPrMainRelPath = "internal/publishpr/publish_pr_main.go"

// containerBinGitRelPath is the repo-relative path to the in-container git
// shim.  The fnblock-goblock-gitenv block reads this file (via the per-check
// targetFile field) for its three git-env object-store-redirection pins,
// mirroring the shell `grep -Fq -- "${_git_env_literal}"
// ${ROOT_DIR}/runtime/container/bin/git`.
const containerBinGitRelPath = "runtime/container/bin/git"

// checkKind selects how a check's fixed string or regex is matched against the
// launcher contents.
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
	// kindFirstLineRegex requires the launcher's first line to match regex,
	// mirroring `head -n1 ... | grep -q '^...$'`.
	kindFirstLineRegex
	// kindPresent requires the fixed string to appear anywhere in the
	// launcher, mirroring an affirmative `rg -q FIXED`.
	kindPresent
	// kindAbsent requires the fixed string NOT to appear anywhere in the
	// launcher, mirroring a negative `if rg -q FIXED; then ... exit 1`.
	kindAbsent
	// kindRegexAbsent requires regex NOT to match anywhere in
	// the launcher, mirroring a negative `if rg -q REGEX; then ... exit 1`
	// whose REGEX is a genuine regular expression (an unanchored
	// alternation with active metacharacters), unlike kindAbsent's
	// fixed-string containment.
	kindRegexAbsent
	// kindRegexPresent requires regex to match somewhere in the
	// launcher, mirroring an affirmative `rg -q REGEX` whose REGEX contains an
	// active metacharacter (e.g. a trailing `.` meaning any char), unlike
	// kindPresent's fixed-string containment.
	kindRegexPresent
	// kindFunctionBlockRegex requires regex to match inside the
	// top-level bash function body named functionName, mirroring
	// function_block_contains_regex (sed-range extraction + `grep -q` regex,
	// NOT `grep -Fq`).  Unlike kindFunctionBlock's fixed-string containment,
	// regex is a genuine regular expression, matched per-line within the
	// extracted block via regexMatchesAnyLine for `grep`/`rg` line-oriented
	// parity.
	kindFunctionBlockRegex
	// kindPresentInAnyFile requires the fixed string to appear in AT LEAST ONE
	// of the files listed in targetFiles, mirroring `grep -Fq -- NEEDLE f1 f2`
	// under a negated `if !` guard: grep scans every file and, with -q, exits 0
	// as soon as the needle is found in ANY of them, so `! grep` (the
	// violation) fires only when the needle is absent from ALL listed files.
	// Unlike kindPresent's single targetFile, evaluate ORs the per-file
	// containment predicate (holds) across every path in targetFiles.
	kindPresentInAnyFile
	// kindCountAtLeast requires the fixed string (pattern) to appear on AT
	// LEAST minCount lines of the target file, mirroring the shell's
	// `if [[ "$(grep -Fc 'NEEDLE' file)" -lt N ]]; then ... exit 1`.  As with
	// `grep -Fc`, matching is line-oriented: holds counts how many lines
	// CONTAIN the fixed needle (a line with two occurrences still counts
	// once), not the total number of occurrences, and the check is violated
	// (returns the message) when that line count is < minCount.
	kindCountAtLeast
	// kindGoFunctionBlock requires needle (a fixed string) to appear inside
	// the top-level Go function body named functionName, mirroring
	// go_function_block_contains_fixed (awk extraction from the line
	// beginning `func FUNC(` through the next line that is exactly `}`,
	// with // line comments and /* */ block comments — including multi-line
	// block comments — stripped, followed by grep -Fq).  Unlike
	// kindFunctionBlock's bash sed-range extraction (extractNamedFunctionBlock),
	// this scopes to a Go function via extractGoFunctionBlock so the same
	// literal appearing in an unrelated helper or a comment cannot satisfy it.
	kindGoFunctionBlock
	// kindFunctionBlockRegexAbsent requires regex NOT to match
	// any line inside the top-level bash function body named functionName,
	// mirroring a NEGATED function_block_contains_regex guard
	// (`if function_block_contains_regex FILE FUNC PATTERN; then ... exit 1`):
	// extractNamedFunctionBlock scopes to the block, then regexMatchesAnyLine
	// tests each line, so a match inside the block is a violation (present →
	// exit 1).  Unlike kindFunctionBlockAbsent's fixed-string containment the
	// regex is a genuine regular expression, and unlike kindFunctionBlockRegex
	// the sense is inverted (present is the violation, not absent).
	kindFunctionBlockRegexAbsent
	// kindDirExists requires the repo-relative path targetPath to be an
	// existing directory under rootDir, mirroring `if [[ ! -d "${ROOT_DIR}/PATH" ]]`
	// (a missing path or a non-directory is a violation → exit 1).  Unlike the
	// content kinds this is a FILESYSTEM check: evaluate stats
	// filepath.Join(rootDir, targetPath) via holds rather than reading the path
	// as file content.
	kindDirExists
	// kindExecutable requires the repo-relative path targetPath to be
	// executable by the current user under rootDir, mirroring
	// `if [[ ! -x "${ROOT_DIR}/PATH" ]]` (a missing or non-executable path is a
	// violation → exit 1).  Like kindDirExists this is a FILESYSTEM check: it
	// mirrors Bash `-x` via syscall.Access(path, X_OK) — the same access(2)
	// primitive Bash uses — rather than reading content or inspecting mode bits.
	kindExecutable
	// kindFileExists requires the repo-relative path targetPath to be an
	// existing REGULAR file under rootDir, mirroring
	// `if [[ ! -f "${ROOT_DIR}/PATH" ]]` (a missing path, a directory, or any
	// other non-regular file is a violation → exit 1).  Like kindDirExists /
	// kindExecutable this is a FILESYSTEM check: evaluate stats
	// filepath.Join(rootDir, targetPath) and holds requires os.Stat to succeed
	// AND info.Mode().IsRegular() — the same "regular file" predicate Bash's -f
	// uses (which, unlike -e, rejects directories) — rather than reading the
	// path as file content.
	kindFileExists
	// kindJSONExprEval requires the jq comparison expression
	// `<jsonPath> == <jsonExpectedRaw>` to evaluate TRUE against the target file
	// parsed as JSON, mirroring a NEGATED `jq -e` guard
	// (`if ! jq -e '<path> == <literal>' FILE; then ... exit 1`).  jq -e exits 0
	// when its last output is neither false nor null and non-zero otherwise, and
	// for a `==` expression the sole output is always the boolean true/false, so
	// the invariant holds iff the comparison is true.  Equality is jq's
	// TYPE-AWARE `==`: jsonExpectedRaw is parsed as its own JSON literal (a string
	// `"allow"`, a boolean `false`/`true`, or a number) and compared to the leaf
	// at jsonPath by both JSON type and value, so `.x == false` holds only for the
	// JSON boolean false — never the string "false".  A leaf that is missing or an
	// explicit null renders as JSON null (jq indexing an absent/null key yields
	// null, no error), which never equals a scalar literal → violation.  A file
	// that is not valid JSON, or a path that indexes a non-object scalar/array
	// with a string key, mirrors jq's error exit (non-zero) under the `if !`
	// guard → violation.  Unlike the string-rendering kinds this parses the file
	// as structured JSON (encoding/json) and navigates the dotted object path via
	// navigateJSONPath rather than matching text.  navigateJSONPath also resolves
	// array-index segments (`.a[0].b`), and jsonExpectedRaw may be an empty-array
	// literal `[]` (which reflect.DeepEqual matches only against a JSON array of
	// length zero), so `.tools.allowed == []` holds only for an empty array.
	kindJSONExprEval
	// kindJSONPathTruthy mirrors a BARE-PATH `jq -e '<jsonPath>' FILE` guard (no
	// comparison operator).  jq -e exits 0 when the value at jsonPath is "truthy"
	// (navigated successfully AND neither JSON null nor the boolean false) and
	// non-zero otherwise: a falsy value (null/false, or a missing key that renders
	// as null) exits 1, and a navigation type error or invalid JSON exits with an
	// error code.  The truthiness predicate is therefore `text is valid JSON &&
	// navigateJSONPath succeeds && leaf != nil && leaf != false`.
	// jsonViolateWhenTruthy selects the shell polarity: false mirrors
	// `if ! jq -e PATH` (holds iff truthy), true mirrors `if jq -e PATH; then …
	// exit 1` (a truthy value is the VIOLATION, so holds iff NOT truthy) — the
	// latter is the Gemini `.security.auth.selectedType` guard.  Because both a
	// falsy value and a jq error make jq exit non-zero, they collapse to "not
	// truthy", which the predicate already yields, so under jsonViolateWhenTruthy
	// the invariant holds on a falsy value AND on a type error / invalid JSON,
	// exactly as the `if jq -e` shell guard does.
	kindJSONPathTruthy
	// kindJSONTypeEquals mirrors `jq -e '<jsonPath> | type == "<T>"' FILE`: it
	// pipes the leaf at jsonPath into jq's `type` builtin (which yields the string
	// "null"/"boolean"/"number"/"string"/"array"/"object") and compares it to the
	// JSON string literal jsonExpectedRaw (e.g. `"array"`).  Under the migrated
	// `if ! jq -e` guard the invariant holds iff the leaf's jq type equals the
	// expected type: a missing key or explicit null has type "null" (never the
	// expected non-null type → violation), and a navigation type error or invalid
	// JSON is jq's error exit → violation (navigateJSONPath returns (nil, false)).
	kindJSONTypeEquals
)

// check is one hardening invariant: how to match, which fixed string or regex
// to match, which function to scope to (function-block kinds only), which
// repo-relative file to read (empty means launcherRelPath), and the exact
// stderr message the shell emitted on violation.
type check struct {
	kind         checkKind
	functionName string
	// pattern is fixed-string match data for kindPresent/kindAbsent,
	// kindPresentInAnyFile, kindCountAtLeast, and the fixed function-block
	// kinds. Regex kinds must use regex instead so fixed strings never flow into
	// regexp compilation.
	pattern string
	// regex is regular-expression match data for kindRegexPresent /
	// kindRegexAbsent, kindFirstLineRegex, and the regex function-block kinds.
	regex   string
	message string
	// targetFile is the repo-relative file this check reads.  An empty
	// value defaults to launcherRelPath (scripts/workcell), so every
	// existing check keeps its original read target unchanged; only the
	// bootstrap-egress Copilot-release-URL-override invariant sets it (to
	// dockerfileRelPath), mirroring the shell probe that ran `rg` against
	// ${ROOT_DIR}/runtime/container/Dockerfile instead of scripts/workcell.
	targetFile string
	// targetFiles lists the repo-relative files a kindPresentInAnyFile check
	// reads.  It is used only by kindPresentInAnyFile (every other kind reads a
	// single file via targetFile); evaluate ORs the containment predicate
	// across these paths, mirroring the shell's multi-file
	// `grep -Fq -- NEEDLE f1 f2`.
	targetFiles []string
	// minCount is the minimum number of lines of the target file that must
	// contain pattern for a kindCountAtLeast check to hold.  It is used only by
	// kindCountAtLeast (every other kind ignores it), mirroring the N in the
	// shell's `grep -Fc ... -lt N` count guard.
	minCount int
	// targetPath is the repo-relative path a filesystem check (kindDirExists /
	// kindExecutable / kindFileExists) stats under rootDir.  It is used only by
	// those three kinds (every other kind reads file content via targetFile /
	// targetFiles); the filesystem kinds never read the path as content, so
	// leaving targetFile empty for them does not trigger a launcherRelPath read.
	targetPath string
	// jsonPath is the dotted JSON object path a kindJSONExprEval check navigates
	// (e.g. ".security.folderTrust.enabled"), mirroring the left-hand side of the
	// migrated `jq -e '<path> == <literal>'` expression.  It is used only by
	// kindJSONExprEval (every other kind matches text).
	jsonPath string
	// jsonExpectedRaw is the right-hand JSON literal of a kindJSONExprEval check,
	// verbatim as it appeared in the jq expression (`"allow"`, `false`, `true`, a
	// number, or the empty-array literal `[]`).  holds parses it as its own JSON
	// value so the comparison is jq-typed.  kindJSONTypeEquals reuses this field
	// to hold the JSON string type literal it compares against (e.g. `"array"`).
	jsonExpectedRaw string
	// jsonViolateWhenTruthy inverts a kindJSONPathTruthy check's polarity.  When
	// false (the default) the invariant holds iff the path is jq-truthy, mirroring
	// `if ! jq -e PATH`; when true a jq-truthy path is the violation, mirroring
	// `if jq -e PATH; then … exit 1` (the Gemini selectedType guard).  It is used
	// only by kindJSONPathTruthy.
	jsonViolateWhenTruthy bool
	// messageIncludesTargetPath, when true, appends the absolute target path
	// (rootDir + "/" + targetPath) to message when evaluate builds the
	// violation error, mirroring a shell message that interpolated the full
	// ${ROOT_DIR}/<path> literal (the pre-commit hook executable check emitted
	// "Expected executable repo pre-commit hook: ${REPO_PRECOMMIT_HOOK}", i.e.
	// the absolute hook path).  It is used only by filesystem kinds.
	messageIncludesTargetPath bool
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
		regex:   `^#!/usr/bin/env -S -i PATH=.* BASH_ENV= ENV= /bin/bash$`,
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
// (kindAbsent/kindRegexAbsent/kindFunctionBlockAbsent/
// kindFunctionBlockRegexAbsent) checks pass.
//
// The filesystem kinds (kindDirExists/kindExecutable/kindFileExists) do not
// read content: evaluate stats rootDir/targetPath, mirroring the shell's
// `[[ ! -d ]]` / `[[ ! -x ]]` / `[[ ! -f ]]` tests, so a missing path is a
// violation for all three.
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
		if err := c.validatePatternFields(); err != nil {
			return err
		}
		if c.kind == kindPresentInAnyFile {
			// Mirror `grep -Fq -- NEEDLE f1 f2`: the check holds when the
			// per-file containment predicate (holds) is true for ANY listed
			// file, and is violated only when it is false for every file.
			satisfied := false
			for _, rel := range c.targetFiles {
				if c.holds(readTarget(rel), rootDir) {
					satisfied = true
					break
				}
			}
			if !satisfied {
				return errors.New(c.violationMessage(rootDir))
			}
			continue
		}
		if c.kind == kindDirExists || c.kind == kindExecutable || c.kind == kindFileExists {
			// Filesystem kinds stat rootDir/targetPath instead of reading a
			// file's content, mirroring the shell's `[[ ! -d ]]` / `[[ ! -x ]]` /
			// `[[ ! -f ]]` path tests; holds ignores the (empty) text argument
			// for them.
			if !c.holds("", rootDir) {
				return errors.New(c.violationMessage(rootDir))
			}
			continue
		}
		rel := c.targetFile
		if rel == "" {
			rel = launcherRelPath
		}
		if !c.holds(readTarget(rel), rootDir) {
			return errors.New(c.violationMessage(rootDir))
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
		regex:   `WORKCELL_TEST_HARNESS|WORKCELL_(GIT|COLIMA|DOCKER|RUBY)_BIN=`,
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
		regex:   "strict mode requires --prepare when you explicitly request --rebuild.",
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
		regex:   `docker-images-prod\.[^.]+\.r2\.cloudflarestorage\.com:443`,
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
		regex:      `(?m)^ARG COPILOT_RELEASE_URL=`,
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
		regex:        "cat-file blob",
		message:      "Expected git_index_materialize_regular_file to materialize tracked blobs without checkout-index",
	},
	{
		kind:         kindFunctionBlockRegex,
		functionName: "git_index_materialize_regular_file",
		regex:        "failed to read tracked blob",
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
		regex:        "git_config_key_is_blocked",
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
		regex:        "core.hooksPath=/dev/null",
		message:      "Expected publish_pr_main to disable repo hooks for host-side publish git commands",
	},
	{
		// kindFunctionBlockRegex: metacharacter-free, but kept a regex kind for
		// fidelity with the shell's function_block_contains_regex.
		kind:         kindFunctionBlockRegex,
		functionName: "publish_pr_main",
		regex:        "--no-verify",
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

// shadowEnumEgressChecks lists the seven shadow-enumeration / IPv6-egress
// invariants in the same order as the former inline block in
// scripts/verify-invariants.sh (the block between the publish-PR /
// shadow-mount group and the hostile-BASH_ENV runtime fixture), so a
// reviewer can diff the two one-to-one.
//
// The first five are whole-file `grep -Fq` fixed-string containment probes
// against scripts/workcell (the default target).  The last four of those
// five came from a `for needle in ...; do grep -Fq -- "${needle}" ...; done`
// loop whose stderr interpolated the needle into the message; because every
// needle is a fixed single-quoted shell literal (backslashes and parens are
// literal, matched by `grep -Fq`), each message is computed here verbatim as
// "Expected prepare_workspace_control_plane_shadow to match snippet: " +
// needle.
//
// The final two probes read scripts/colima-egress-allowlist.sh (via
// targetFile), mirroring the two shell `rg` probes that ran against that
// helper: the disable_ipv6=1 probe is NEGATED (present is a violation →
// kindAbsent) and the ip6tables-support message probe is affirmative
// (kindPresent).  Both rg patterns are metacharacter-free, so they reduce to
// fixed-string containment.
var shadowEnumEgressChecks = []check{
	{
		// kindPresent: the shell's `grep -Fq "find \"\${workspace}\" -type d
		// -name .git -prune -print0"` is fixed-string containment of the
		// literal .git enumeration.
		kind:    kindPresent,
		pattern: `find "${workspace}" -type d -name .git -prune -print0`,
		message: "Expected prepare_workspace_control_plane_shadow to enumerate only real .git directories",
	},
	{
		// Needle 1 of the former loop: the single-quoted shell literal
		// `find "${workspace}/${git_rel}/modules" \` (trailing backslash is a
		// literal character matched by grep -Fq).
		kind:    kindPresent,
		pattern: `find "${workspace}/${git_rel}/modules" \`,
		message: `Expected prepare_workspace_control_plane_shadow to match snippet: find "${workspace}/${git_rel}/modules" \`,
	},
	{
		// Needle 2 of the former loop.
		kind:    kindPresent,
		pattern: `-type l \) -name hooks`,
		message: `Expected prepare_workspace_control_plane_shadow to match snippet: -type l \) -name hooks`,
	},
	{
		// Needle 3 of the former loop.
		kind:    kindPresent,
		pattern: `-type l \) \( -name config -o -name config.worktree \)`,
		message: `Expected prepare_workspace_control_plane_shadow to match snippet: -type l \) \( -name config -o -name config.worktree \)`,
	},
	{
		// Needle 4 of the former loop.
		kind:    kindPresent,
		pattern: `-type l \) -name worktrees`,
		message: `Expected prepare_workspace_control_plane_shadow to match snippet: -type l \) -name worktrees`,
	},
	{
		// kindAbsent against scripts/colima-egress-allowlist.sh: silently
		// disabling IPv6 as an allowlist-enforcement fallback is a violation
		// (present → exit 1).
		kind:       kindAbsent,
		pattern:    "disable_ipv6=1",
		message:    "Workcell should not silently disable IPv6 as a fallback for allowlist enforcement",
		targetFile: colimaEgressAllowlistRelPath,
	},
	{
		// kindPresent against scripts/colima-egress-allowlist.sh: the helper
		// must fail closed when dual-stack allowlist enforcement is
		// unavailable.
		kind:       kindPresent,
		pattern:    "requires ip6tables support to enforce dual-stack allowlist egress policy",
		message:    "Expected allowlist egress helper to fail closed when dual-stack allowlist enforcement is unavailable",
		targetFile: colimaEgressAllowlistRelPath,
	},
}

// CheckShadowEnumEgress runs the seven shadow-enumeration / IPv6-egress
// invariants against the repo rooted at rootDir, in the shell's original
// order.  It returns nil when every invariant holds (the shell's exit 0), or
// an error whose message equals the shell's stderr for the first violated
// invariant (the shell's exit 1).
func CheckShadowEnumEgress(rootDir string) error {
	return evaluate(rootDir, shadowEnumEgressChecks)
}

// copilotAmbientEnvKnobs lists the twenty-four Copilot/GitHub ambient env
// variables that the shell's `for copilot_env in ...` loop asserted each
// runtime wrapper scrubs.  Order matches the shell verbatim so the generated
// checks reproduce the shell's first-failure stderr exactly.
var copilotAmbientEnvKnobs = []string{
	"unset GH_CONFIG_DIR",
	"unset GH_HOST",
	"unset GH_TOKEN",
	"unset GITHUB_TOKEN",
	"unset OTEL_INSTRUMENTATION_GENAI_CAPTURE_MESSAGE_CONTENT",
	"unset PLAIN_DIFF",
	"unset USE_BUILTIN_RIPGREP",
	"unset OTEL_EXPORTER_OTLP_ENDPOINT",
	"unset OTEL_EXPORTER_OTLP_HEADERS",
	"unset OTEL_EXPORTER_OTLP_PROTOCOL",
	"unset OTEL_EXPORTER_OTLP_TIMEOUT",
	"unset OTEL_EXPORTER_OTLP_TRACES_ENDPOINT",
	"unset OTEL_EXPORTER_OTLP_TRACES_HEADERS",
	"unset OTEL_EXPORTER_OTLP_TRACES_PROTOCOL",
	"unset OTEL_EXPORTER_OTLP_TRACES_TIMEOUT",
	"unset OTEL_EXPORTER_OTLP_METRICS_ENDPOINT",
	"unset OTEL_EXPORTER_OTLP_METRICS_HEADERS",
	"unset OTEL_EXPORTER_OTLP_METRICS_PROTOCOL",
	"unset OTEL_EXPORTER_OTLP_METRICS_TIMEOUT",
	"unset OTEL_EXPORTER_OTLP_LOGS_ENDPOINT",
	"unset OTEL_EXPORTER_OTLP_LOGS_HEADERS",
	"unset OTEL_EXPORTER_OTLP_LOGS_PROTOCOL",
	"unset OTEL_EXPORTER_OTLP_LOGS_TIMEOUT",
	"unset OTEL_RESOURCE_ATTRIBUTES",
}

// homeSeedProviderWrapperChecks returns the fifty-seven home-seeding /
// provider-wrapper env-scrub invariants in the same order as the former
// inline block in scripts/verify-invariants.sh (the block between the
// Gemini-auth-selection harness cleanup and the Copilot-prefix-scrub `for`
// loop), so a reviewer can diff the two one-to-one.
//
// The nine leading probes are whole-file `grep -Fq` / `rg -q` checks:
//   - The trustedFolders probe's shell `rg -q 'trustedFolders\.json'` escapes
//     its only metacharacter (`\.`), so it reduces to fixed-string
//     containment of the literal `trustedFolders.json` (kindPresent).
//   - Five `grep -Fq` Gemini/Claude home-seeding probes and two affirmative
//     `grep -Fq` provider-wrapper unset probes are fixed-string containment
//     (kindPresent); their needles reproduce the shell double-quoted literals
//     byte-exact (`\"`→`"`, `\$`→`$`, including the `|| true` suffix).
//   - The `export HOME CODEX_HOME CLAUDE_CONFIG_DIR ...` probe is a NEGATED
//     `grep -Fq` (present is a violation → kindAbsent).
//
// The forty-eight trailing checks migrate the shell's nested
// `for copilot_env in ...; do for copilot_wrapper in ...; do
// grep -Fq -- "${copilot_env}" "${copilot_wrapper}"; done; done` loop.  The
// outer loop is the env knob (copilotAmbientEnvKnobs) and the inner loop is
// the wrapper (provider-wrapper.sh then development-wrapper.sh), so the checks
// are emitted knob-major to reproduce the shell's first-failure order.  Each
// message interpolates basename(wrapper) and the knob exactly as the shell's
// `echo "Expected $(basename "${copilot_wrapper}") to scrub Copilot/GitHub
// ambient env knob: ${copilot_env}"` did; every knob is a fixed-string
// `grep -Fq` needle (kindPresent) read from the per-check targetFile.
func homeSeedProviderWrapperChecks() []check {
	cs := []check{
		{
			// kindPresent: the shell's `rg -q 'trustedFolders\.json'` escapes
			// its only metacharacter, so it is fixed-string containment.
			kind:       kindPresent,
			pattern:    "trustedFolders.json",
			message:    "Expected Gemini home seeding to provision trustedFolders.json",
			targetFile: homeControlPlaneRelPath,
		},
		{
			kind:       kindPresent,
			pattern:    `workcell_reset_session_target "${HOME}/.gemini/settings.json" "Gemini settings"`,
			message:    "Expected Gemini home seeding to reset settings.json through workcell_reset_session_target",
			targetFile: homeControlPlaneRelPath,
		},
		{
			kind:       kindPresent,
			pattern:    `workcell_set_gemini_tool_sandbox "${HOME}/.gemini/settings.json" false`,
			message:    "Expected Gemini home seeding to pin the nested sandbox setting explicitly",
			targetFile: homeControlPlaneRelPath,
		},
		{
			kind:       kindPresent,
			pattern:    `workcell_copy_manifest_credential_file claude_auth "${HOME}/.claude/.credentials.json" || true`,
			message:    "Expected Claude home seeding to copy auth into .claude/.credentials.json",
			targetFile: homeControlPlaneRelPath,
		},
		{
			kind:       kindPresent,
			pattern:    `workcell_copy_manifest_credential_file claude_auth "${HOME}/.claude/.claude.json" || true`,
			message:    "Expected Claude home seeding to copy auth into .claude/.claude.json",
			targetFile: homeControlPlaneRelPath,
		},
		{
			kind:       kindPresent,
			pattern:    `workcell_copy_manifest_credential_file claude_auth "${HOME}/.claude.json" || true`,
			message:    "Expected Claude home seeding to copy auth into .claude.json",
			targetFile: homeControlPlaneRelPath,
		},
		{
			kind:       kindPresent,
			pattern:    "unset CLAUDE_CONFIG_DIR",
			message:    "Expected provider wrapper to discard caller-supplied CLAUDE_CONFIG_DIR",
			targetFile: providerWrapperRelPath,
		},
		{
			// kindAbsent: exporting CLAUDE_CONFIG_DIR for non-Claude launches is
			// a violation (present → exit 1).
			kind:       kindAbsent,
			pattern:    "export HOME CODEX_HOME CLAUDE_CONFIG_DIR TMPDIR WORKCELL_MODE CODEX_PROFILE WORKCELL_AGENT_AUTONOMY WORKCELL_CONTAINER_MUTABILITY",
			message:    "Provider wrapper should not export CLAUDE_CONFIG_DIR for non-Claude launches",
			targetFile: providerWrapperRelPath,
		},
		{
			kind:       kindPresent,
			pattern:    "unset DISABLE_AUTOUPDATER",
			message:    "Expected provider wrapper to discard caller-supplied DISABLE_AUTOUPDATER",
			targetFile: providerWrapperRelPath,
		},
	}
	for _, knob := range copilotAmbientEnvKnobs {
		for _, rel := range []string{providerWrapperRelPath, developmentWrapperRelPath} {
			cs = append(cs, check{
				kind:       kindPresent,
				pattern:    knob,
				message:    "Expected " + filepath.Base(rel) + " to scrub Copilot/GitHub ambient env knob: " + knob,
				targetFile: rel,
			})
		}
	}
	return cs
}

// CheckHomeSeedProviderWrapper runs the fifty-seven home-seeding /
// provider-wrapper env-scrub invariants against the repo rooted at rootDir, in
// the shell's original order.  It returns nil when every invariant holds (the
// shell's exit 0), or an error whose message equals the shell's stderr for the
// first violated invariant (the shell's exit 1).
func CheckHomeSeedProviderWrapper(rootDir string) error {
	return evaluate(rootDir, homeSeedProviderWrapperChecks())
}

// copilotTokenHandoffChecks returns the twenty-nine Copilot prefix-scrub /
// token-handoff invariants in the same order as the former inline block in
// scripts/verify-invariants.sh (the block between the
// home-seed-provider-wrapper subcommand and the internal/host/hoststate
// stale-cleanup guard), so a reviewer can diff the two one-to-one.
//
// The first eight checks migrate the shell's `for copilot_wrapper in
// provider-wrapper.sh development-wrapper.sh; do ...; done` loop, which ran
// four prefix-scrub probes per wrapper.  The shell looped wrapper-outer,
// probe-inner, so the checks are emitted wrapper-major (provider's four
// probes, then development's four) to reproduce the shell's first-failure
// stderr exactly.  Two probes are affirmative (`if ! grep -Fq` → the prefix
// scrub must be present → kindPresent); two are negated (`if grep -Fq` → a
// duplicate OIDC/experiment loop present is a violation → kindAbsent).  Each
// message interpolates basename(wrapper) exactly as the shell's
// `$(basename "${copilot_wrapper}")` did.  Every needle is a double-quoted
// shell literal whose only escape is `\$`→`$`, reproduced byte-exact.
//
// The two-file COPILOT_HOME guard was one shell `if grep -Fq NEEDLE
// provider-wrapper.sh home-control-plane.sh; then ... exit 1` — a negated
// probe across TWO files (present in EITHER is a violation).  It is expressed
// here as two ordered kindAbsent checks (absent from provider-wrapper.sh,
// then absent from home-control-plane.sh) sharing the one message, which is
// behaviourally identical: either file containing the needle makes the
// first-matching check fail with that shared stderr.
//
// The remaining guards mirror the shell exactly.  Each multi-probe `||`
// guard (several `grep -Fq` / function_block_contains_fixed probes joined by
// `||` under one message) becomes an ordered run of checks sharing that
// message, which is behaviourally identical (any probe failing yields the
// same stderr and exit 1).  Affirmative `if ! grep -Fq` probes are
// kindPresent; affirmative `if ! function_block_contains_fixed` probes are
// kindFunctionBlock (grep -Fq fixed-string containment inside the named
// block); every needle is metacharacter-free after unescaping the shell
// quoting (`\$`→`$`, `\"`→`"`), so each is fixed-string containment against
// the per-check targetFile.
func copilotTokenHandoffChecks() []check {
	var cs []check
	// Guard 1: the copilot_wrapper prefix-scrub loop (wrapper-outer,
	// probe-inner).
	for _, rel := range []string{providerWrapperRelPath, developmentWrapperRelPath} {
		base := filepath.Base(rel)
		cs = append(cs,
			check{
				kind:       kindPresent,
				pattern:    "${!COPILOT_@}",
				message:    "Expected " + base + " to scrub unknown future Copilot env variables by prefix",
				targetFile: rel,
			},
			check{
				kind:       kindPresent,
				pattern:    "${!GITHUB_COPILOT_@}",
				message:    "Expected " + base + " to scrub unknown future GitHub Copilot env variables by prefix",
				targetFile: rel,
			},
			check{
				// kindAbsent: a duplicate OIDC token loop present is a violation.
				kind:       kindAbsent,
				pattern:    "${!GITHUB_COPILOT_OIDC_MCP_TOKEN@}",
				message:    base + " must rely on the GITHUB_COPILOT_ prefix scrub instead of a duplicate OIDC token loop",
				targetFile: rel,
			},
			check{
				// kindAbsent: a duplicate experiment loop present is a violation.
				kind:       kindAbsent,
				pattern:    "${!COPILOT_EXP_@}",
				message:    base + " must rely on the COPILOT_ prefix scrub instead of a duplicate experiment loop",
				targetFile: rel,
			},
		)
	}
	cs = append(cs, []check{
		// Guard 2: the two-file COPILOT_HOME token-copy guard (negated grep
		// across provider-wrapper.sh then home-control-plane.sh).
		{
			kind:       kindAbsent,
			pattern:    "${COPILOT_HOME}/workcell-token",
			message:    "Copilot auth token must not be copied into COPILOT_HOME",
			targetFile: providerWrapperRelPath,
		},
		{
			kind:       kindAbsent,
			pattern:    "${COPILOT_HOME}/workcell-token",
			message:    "Copilot auth token must not be copied into COPILOT_HOME",
			targetFile: homeControlPlaneRelPath,
		},
		// Guard 3: launcher prepares the host-mounted token handoff.
		{
			kind:    kindPresent,
			pattern: `prepare_copilot_token_handoff_mount "$@"`,
			message: "Expected launcher to prepare a host-mounted Copilot token handoff before docker run",
		},
		// Guard 4: launcher removes the Copilot token direct mounts.
		{
			kind:    kindPresent,
			pattern: `DIRECT_SOURCE_MOUNTS=("${filtered_mounts[@]}")`,
			message: "Expected launcher to remove Copilot token direct mounts after host-side handoff preparation",
		},
		// Guard 5: host and runtime Copilot auth classifiers share the no-auth
		// subcommand helper (five function-block probes across scripts/workcell
		// and provider-wrapper.sh, sharing one message).
		{
			kind:         kindFunctionBlock,
			functionName: "copilot_no_auth_invocation",
			pattern:      `-h | --help | -v | --version | help | version | completion)`,
			message:      "Expected host and runtime Copilot auth classifiers to share the no-auth subcommand helper",
		},
		{
			kind:         kindFunctionBlock,
			functionName: "copilot_no_auth_invocation",
			pattern:      `-h | --help | -v | --version | help | version | completion)`,
			message:      "Expected host and runtime Copilot auth classifiers to share the no-auth subcommand helper",
			targetFile:   providerWrapperRelPath,
		},
		{
			kind:         kindFunctionBlock,
			functionName: "copilot_host_invocation_requires_auth",
			pattern:      `if copilot_no_auth_invocation "$@"; then`,
			message:      "Expected host and runtime Copilot auth classifiers to share the no-auth subcommand helper",
		},
		{
			kind:         kindFunctionBlock,
			functionName: "fail_fast_for_missing_copilot_auth",
			pattern:      `if copilot_no_auth_invocation "$@"; then`,
			message:      "Expected host and runtime Copilot auth classifiers to share the no-auth subcommand helper",
		},
		{
			kind:         kindFunctionBlock,
			functionName: "copilot_invocation_requires_auth",
			pattern:      `if copilot_no_auth_invocation "$@"; then`,
			message:      "Expected host and runtime Copilot auth classifiers to share the no-auth subcommand helper",
			targetFile:   providerWrapperRelPath,
		},
		// Guard 6: token handoff directory lives in the dedicated writable
		// Colima handoff root (two whole-file grep probes sharing one message).
		{
			kind:    kindPresent,
			pattern: "workcell-token-handoff",
			message: "Expected Copilot token handoff directory to live in the dedicated writable Colima handoff root",
		},
		{
			kind:    kindPresent,
			pattern: `COPILOT_TOKEN_HANDOFF_DIR="$(mktemp -d "${token_handoff_parent}/copilot-token-handoff.XXXXXX")"`,
			message: "Expected Copilot token handoff directory to live in the dedicated writable Colima handoff root",
		},
		// Guard 7: token handoff writes re-check the guarded Colima handoff
		// root at the write site (three function-block probes in
		// prepare_copilot_token_handoff_mount sharing one message).
		{
			kind:         kindFunctionBlock,
			functionName: "prepare_copilot_token_handoff_mount",
			pattern:      `token_handoff_parent="$(default_copilot_token_handoff_parent)" || exit $?`,
			message:      "Expected Copilot token handoff writes to re-check the guarded Colima handoff root at the write site",
		},
		{
			kind:         kindFunctionBlock,
			functionName: "prepare_copilot_token_handoff_mount",
			pattern:      `chmod 0700 "${token_handoff_parent}"`,
			message:      "Expected Copilot token handoff writes to re-check the guarded Colima handoff root at the write site",
		},
		{
			kind:         kindFunctionBlock,
			functionName: "prepare_copilot_token_handoff_mount",
			pattern:      `reject_symlinked_colima_staging_cache_roots || exit $?`,
			message:      "Expected Copilot token handoff writes to re-check the guarded Colima handoff root at the write site",
		},
		// Guard 8: token handoff leaf permissions support cap-dropped container
		// root (four function-block probes: two in scripts/workcell's
		// prepare_copilot_token_handoff_mount, two in container-smoke.sh's
		// stage_copilot_token_handoff_dir, sharing one message).
		{
			kind:         kindFunctionBlock,
			functionName: "prepare_copilot_token_handoff_mount",
			pattern:      `chmod 0733 "${COPILOT_TOKEN_HANDOFF_DIR}"`,
			message:      "Expected Copilot token handoff leaf permissions to support cap-dropped container root without exposing parent traversal",
		},
		{
			kind:         kindFunctionBlock,
			functionName: "prepare_copilot_token_handoff_mount",
			pattern:      `chmod 0444 "${COPILOT_TOKEN_HANDOFF_FILE}"`,
			message:      "Expected Copilot token handoff leaf permissions to support cap-dropped container root without exposing parent traversal",
		},
		{
			kind:         kindFunctionBlock,
			functionName: "stage_copilot_token_handoff_dir",
			pattern:      `chmod 0733 "${token_handoff_dir}"`,
			message:      "Expected Copilot token handoff leaf permissions to support cap-dropped container root without exposing parent traversal",
			targetFile:   containerSmokeRelPath,
		},
		{
			kind:         kindFunctionBlock,
			functionName: "stage_copilot_token_handoff_dir",
			pattern:      `chmod 0444 "${token_handoff_file}"`,
			message:      "Expected Copilot token handoff leaf permissions to support cap-dropped container root without exposing parent traversal",
			targetFile:   containerSmokeRelPath,
		},
		// Guard 9: detached launches wait for the wrapper-side consumed marker
		// (two whole-file grep probes sharing one message).
		{
			kind:    kindPresent,
			pattern: `COPILOT_TOKEN_HANDOFF_CONSUMED_FILE="${COPILOT_TOKEN_HANDOFF_DIR}/copilot-token-consumed"`,
			message: "Expected detached Copilot launches to wait for the wrapper-side token consumed marker",
		},
		{
			kind:    kindPresent,
			pattern: `while [[ ! -e "${COPILOT_TOKEN_HANDOFF_CONSUMED_FILE}" ]]; do`,
			message: "Expected detached Copilot launches to wait for the wrapper-side token consumed marker",
		},
		// Guard 10: token handoff removes the staged token copy from the mounted
		// injection bundle (one function-block probe).
		{
			kind:         kindFunctionBlock,
			functionName: "prepare_copilot_token_handoff_mount",
			pattern:      `rm -f -- "${source_path}"`,
			message:      "Expected Copilot token handoff to remove the staged token copy from the mounted injection bundle",
		},
	}...)
	return cs
}

// CheckCopilotTokenHandoff runs the twenty-nine Copilot prefix-scrub /
// token-handoff invariants against the repo rooted at rootDir, in the shell's
// original order.  It returns nil when every invariant holds (the shell's exit
// 0), or an error whose message equals the shell's stderr for the first
// violated invariant (the shell's exit 1).
func CheckCopilotTokenHandoff(rootDir string) error {
	return evaluate(rootDir, copilotTokenHandoffChecks())
}

// copilotDockerRunChecks lists the twenty-five Copilot / docker-run
// invariants in the same order as the former inline block in
// scripts/verify-invariants.sh (the block between the
// internal/host/hoststate stale-cleanup guard and the
// WORKCELL_PROVIDER_LAUNCHER_AUTHORITY provider-launcher-authority group),
// so a reviewer can diff the two one-to-one.
//
// Seven distinct files are read (via the per-check targetFile field, or the
// default scripts/workcell launcher):
//   - internal/host/hoststate/hoststate.go (legacy env-file cleanup probe)
//   - scripts/workcell (the default launcher: env-file ban, PID-1 wiring,
//     token-handoff mount, host-computed auth metadata, consumed-marker wait,
//     /run/workcell tmpfs)
//   - runtime/container/rust/src/bin/common/launcher_common.rs and
//     runtime/container/rust/src/bin/workcell-launcher.rs (auth-metadata
//     plumbing)
//   - runtime/container/entrypoint.sh (staging, container-dir env, host token
//     file read/unlink, runtime-state record, self-reexec, mapped-user
//     creation, and the negated caller-token / chown / re-export guards)
//   - scripts/container-smoke.sh (Docker-inspect metadata-leak proof)
//   - runtime/container/runtime-user.sh (runtime-state token path)
//
// Matching semantics mirror the shell exactly.  Every affirmative probe was a
// `grep -Fq`/`grep -Fq --` fixed-string containment (kindPresent); each needle
// reproduces the shell double-quoted / single-quoted literal byte-exact
// (`\"`→`"`, `\$`→`$`; single-quoted needles are verbatim including `${...}`,
// `[[`, `(--init)`, `:rw`).  The two variable-needle probes resolve their
// shell assignments to concrete literals:
//   - copilot_container_dir_env_needle →
//     `WORKCELL_COPILOT_TOKEN_HANDOFF_CONTAINER_DIR="${COPILOT_TOKEN_HANDOFF_CONTAINER_DIR}"`
//   - copilot_auth_required_env_needle →
//     `WORKCELL_COPILOT_AUTH_REQUIRED="${COPILOT_AUTH_REQUIRED}"`
//
// Two whole-file negated `grep -Fq` guards (a caller-supplied token source and
// a chown dependency present in entrypoint.sh) are kindAbsent (present → exit
// 1).  The final guard was a `grep -Eq` genuine regex — its `.*` is an active
// metacharacter, `\$` a literal `$` — so it is kindRegexAbsent with the pattern
// verbatim (present → exit 1); regexMatchesAnyLine gives the same line-oriented
// parity as `grep -E`.
//
// Each multi-probe `||` guard (several `grep -Fq` probes joined by `||` under
// one message) becomes an ordered run of checks sharing that message, which is
// behaviourally identical (any probe failing yields the same stderr and exit 1
// as the corresponding shell `if`).
var copilotDockerRunChecks = []check{
	{
		kind:       kindPresent,
		pattern:    `strings.HasPrefix(suffix, "env.")`,
		message:    "Expected legacy stale Copilot token env-file cleanup to cover production mktemp suffixes",
		targetFile: hoststateRelPath,
	},
	{
		// kindAbsent: a Docker --env-file for the Copilot token is a violation
		// (Docker stores env-files in container metadata → exit 1).
		kind:    kindAbsent,
		pattern: `--env-file "${COPILOT_TOKEN`,
		message: "Copilot auth must not use Docker env-files because Docker stores them in container metadata",
	},
	// PID-1 guard: three probes sharing one message.
	{
		kind:    kindPresent,
		pattern: `if [[ -z "${COPILOT_TOKEN_HANDOFF_DIR}" ]]; then`,
		message: "Expected Copilot token handoff launches to keep the Workcell entrypoint as PID 1",
	},
	{
		kind:    kindPresent,
		pattern: "DOCKER_RUN_BASE+=(--init)",
		message: "Expected Copilot token handoff launches to keep the Workcell entrypoint as PID 1",
	},
	{
		kind:    kindPresent,
		pattern: "DOCKER_RUN_PREFIX_LEN=2",
		message: "Expected Copilot token handoff launches to keep the Workcell entrypoint as PID 1",
	},
	{
		kind:    kindPresent,
		pattern: "${COPILOT_TOKEN_HANDOFF_DIR}:${COPILOT_TOKEN_HANDOFF_CONTAINER_DIR}:rw",
		message: "Expected docker run to mount only the Copilot token handoff directory, not the original token source",
	},
	// Host-computed auth metadata guard: four probes across scripts/workcell,
	// launcher_common.rs, and workcell-launcher.rs sharing one message.  The
	// first two needles are the resolved shell variable needles.
	{
		kind:    kindPresent,
		pattern: `WORKCELL_COPILOT_TOKEN_HANDOFF_CONTAINER_DIR="${COPILOT_TOKEN_HANDOFF_CONTAINER_DIR}"`,
		message: "Expected Copilot launches to pass validated host-computed auth metadata through PID 1 and scrub caller-supplied metadata before provider wrapper exec",
	},
	{
		kind:    kindPresent,
		pattern: `WORKCELL_COPILOT_AUTH_REQUIRED="${COPILOT_AUTH_REQUIRED}"`,
		message: "Expected Copilot launches to pass validated host-computed auth metadata through PID 1 and scrub caller-supplied metadata before provider wrapper exec",
	},
	{
		kind:       kindPresent,
		pattern:    "WORKCELL_COPILOT_AUTH_REQUIRED",
		message:    "Expected Copilot launches to pass validated host-computed auth metadata through PID 1 and scrub caller-supplied metadata before provider wrapper exec",
		targetFile: launcherCommonRustRelPath,
	},
	{
		kind:       kindPresent,
		pattern:    "copilot_auth_required_for_pid1(request.target_name)",
		message:    "Expected Copilot launches to pass validated host-computed auth metadata through PID 1 and scrub caller-supplied metadata before provider wrapper exec",
		targetFile: workcellLauncherRustRelPath,
	},
	{
		kind:    kindPresent,
		pattern: "wait_for_copilot_token_handoff_consumed",
		message: "Expected detached Copilot launches to wait until the managed wrapper consumes the token handoff",
	},
	{
		kind:    kindPresent,
		pattern: `--tmpfs "/run/workcell:nosuid,nodev,size=4m,mode=755,uid=${HOST_UID},gid=${HOST_GID}"`,
		message: "Expected readonly Copilot token handoff state to use a mapped-user writable /run/workcell tmpfs",
	},
	{
		kind:       kindPresent,
		pattern:    `stage_copilot_token_handoff_file "$@"`,
		message:    "Expected runtime entrypoint to stage the Copilot host handoff token into a transient runtime file",
		targetFile: entrypointRelPath,
	},
	// Read-and-unlink guard: three entrypoint.sh probes sharing one message.
	{
		kind:       kindPresent,
		pattern:    `WORKCELL_COPILOT_TOKEN_HANDOFF_CONTAINER_DIR="${WORKCELL_COPILOT_TOKEN_HANDOFF_CONTAINER_DIR:-/opt/workcell/copilot-token-handoff}"`,
		message:    "Expected runtime entrypoint to read and unlink the mounted Copilot token handoff file",
		targetFile: entrypointRelPath,
	},
	{
		kind:       kindPresent,
		pattern:    `WORKCELL_COPILOT_HOST_TOKEN_FILE="${WORKCELL_COPILOT_TOKEN_HANDOFF_CONTAINER_DIR}/copilot-github-token.txt"`,
		message:    "Expected runtime entrypoint to read and unlink the mounted Copilot token handoff file",
		targetFile: entrypointRelPath,
	},
	{
		kind:       kindPresent,
		pattern:    `rm -f -- "${host_token_file}"`,
		message:    "Expected runtime entrypoint to read and unlink the mounted Copilot token handoff file",
		targetFile: entrypointRelPath,
	},
	// Metadata-leak proof guard: two container-smoke.sh probes sharing one
	// message.
	{
		kind:       kindPresent,
		pattern:    `COPILOT_METADATA_ENV="$(docker_cmd inspect`,
		message:    "Expected container smoke to prove Copilot token material is absent from Docker inspect metadata",
		targetFile: containerSmokeRelPath,
	},
	{
		kind:       kindPresent,
		pattern:    "Copilot token leaked into Docker container metadata",
		message:    "Expected container smoke to prove Copilot token material is absent from Docker inspect metadata",
		targetFile: containerSmokeRelPath,
	},
	// Runtime-state record guard: one runtime-user.sh probe + one entrypoint.sh
	// probe sharing one message.
	{
		kind:       kindPresent,
		pattern:    `WORKCELL_RUNTIME_COPILOT_TOKEN_FILE_PATH="${WORKCELL_RUNTIME_STATE_DIR}/copilot-token-file"`,
		message:    "Expected runtime entrypoint to record the staged Copilot token path in root-controlled runtime state",
		targetFile: runtimeUserRelPath,
	},
	{
		kind:       kindPresent,
		pattern:    `workcell_write_readonly_state_file "${WORKCELL_RUNTIME_COPILOT_TOKEN_FILE_PATH}" "${token_file}"`,
		message:    "Expected runtime entrypoint to record the staged Copilot token path in root-controlled runtime state",
		targetFile: entrypointRelPath,
	},
	{
		kind:       kindPresent,
		pattern:    "exec env -u WORKCELL_COPILOT_GITHUB_TOKEN",
		message:    "Expected runtime entrypoint to self-reexec without the Copilot token env variable",
		targetFile: entrypointRelPath,
	},
	{
		kind:       kindPresent,
		pattern:    `setpriv --reuid "${uid}" --regid "${gid}" --init-groups /bin/bash -c`,
		message:    "Expected runtime entrypoint to create the Copilot token handoff file as the mapped runtime user",
		targetFile: entrypointRelPath,
	},
	{
		// kindAbsent: accepting a caller-supplied WORKCELL_COPILOT_GITHUB_TOKEN
		// as an auth source is a violation (present → exit 1).
		kind:       kindAbsent,
		pattern:    `token="${WORKCELL_COPILOT_GITHUB_TOKEN:-}"`,
		message:    "Runtime entrypoint must not accept caller-supplied WORKCELL_COPILOT_GITHUB_TOKEN as a Copilot auth source",
		targetFile: entrypointRelPath,
	},
	{
		// kindAbsent: depending on chown for token-handoff ownership is a
		// violation (present → exit 1).
		kind:       kindAbsent,
		pattern:    `chown "${uid}:${gid}"`,
		message:    "Runtime entrypoint must not depend on chown for Copilot token handoff ownership",
		targetFile: entrypointRelPath,
	},
	{
		// kindRegexAbsent: the shell's `grep -Eq` genuine regex — `.*` is active,
		// `\$` is a literal `$` — so reintroducing the Copilot token env variable
		// when launching the provider child is a violation (present → exit 1).
		kind:       kindRegexAbsent,
		regex:      `WORKCELL_COPILOT_GITHUB_TOKEN=.*"\$@"`,
		message:    "Runtime entrypoint must not reintroduce the Copilot token env variable when launching the provider child",
		targetFile: entrypointRelPath,
	},
}

// CheckCopilotDockerRun runs the twenty-five Copilot / docker-run invariants
// against the repo rooted at rootDir, in the shell's original order.  It
// returns nil when every invariant holds (the shell's exit 0), or an error
// whose message equals the shell's stderr for the first violated invariant (the
// shell's exit 1).
func CheckCopilotDockerRun(rootDir string) error {
	return evaluate(rootDir, copilotDockerRunChecks)
}

// geminiSandboxEnvKnobs lists the twelve Gemini sandbox env variables that the
// shell's `for gemini_sandbox_env in ...` loop asserted the provider wrapper
// scrubs.  Order matches the shell verbatim so the generated checks reproduce
// the shell's first-failure stderr exactly.  Each knob is a fixed
// single-quoted shell literal, matched by `grep -Fq`.
var geminiSandboxEnvKnobs = []string{
	"unset GEMINI_SANDBOX",
	"unset GEMINI_SANDBOX_IMAGE",
	"unset GEMINI_SANDBOX_IMAGE_DEFAULT",
	"unset GEMINI_SANDBOX_PROXY_COMMAND",
	"unset BUILD_SANDBOX",
	"unset SANDBOX",
	"unset SANDBOX_FLAGS",
	"unset SANDBOX_MOUNTS",
	"unset SANDBOX_ENV",
	"unset SANDBOX_PORTS",
	"unset SANDBOX_SET_UID_GID",
	"unset SEATBELT_PROFILE",
}

// providerLauncherAuthorityChecks returns the thirty provider-launcher-authority
// invariants in the same order as the former inline block in
// scripts/verify-invariants.sh (the block between the Copilot / docker-run
// group `go_verify_citools` call and the pinned-native-Claude-exec probe at the
// `rm -f -- "${token_file}"` guard), so a reviewer can diff the two one-to-one.
//
// The block reads four files via the per-check targetFile field: the runtime
// provider wrapper (runtime/container/provider-wrapper.sh), the workcell-launcher
// Rust binary, the shared launcher_common.rs helper, and the exec-guard
// runtime/container/rust/src/lib.rs.  Every probe is a whole-file `grep -Fq`
// fixed-string containment, so each is kindPresent for the affirmative probes and
// kindAbsent for the two negated guards (a caller-supplied WORKCELL_COPILOT_TOKEN_FILE
// declaration or a WORKCELL_COPILOT_GITHUB_TOKEN env fallback present is a
// violation).
//
// Three former shell `if` guards each joined two probes with `||` under a single
// message; they are expressed here as ordered checks sharing that message, which
// is behaviourally identical (the first failing probe yields the same stderr and
// exit 1 as the shell `if`):
//   - the parent-verification guard (workcell_provider_parent_is_launcher and the
//     readlink "/proc/${PPID}/exe" probe, both against provider-wrapper.sh);
//   - the exec-guard pair (current_process_parent_is_approved_native_launcher and
//     approved_wrapper_requires_native_launcher_parent, both against lib.rs);
//   - the consumed-marker guard (the copilot_token_handoff_consumed_file
//     assignment and the `: >"${...}"` write, both against provider-wrapper.sh);
//   - the staged-token guard, whose two probes are mixed present/absent: the
//     first affirmative (the token_file handoff requirement must be present) and
//     the second NEGATED (a WORKCELL_COPILOT_GITHUB_TOKEN:- env fallback present
//     is a violation), matched here as kindPresent then kindAbsent sharing one
//     message.  The shell short-circuits `! grep A || grep B` to A-then-B, which
//     the ordered checks reproduce.
//
// The twelve Gemini sandbox scrub checks migrate the shell's
// `for gemini_sandbox_env in ...; do grep -Fq -- "${gemini_sandbox_env}"
// provider-wrapper.sh; done` loop.  Each message interpolates the knob exactly as
// the shell's `echo "Expected provider wrapper to scrub Gemini sandbox env knob:
// ${gemini_sandbox_env}"` did; every knob is a fixed-string `grep -Fq` needle
// (kindPresent) read from provider-wrapper.sh.
//
// The three exec/handoff needles reproduce the shell double-quoted literals
// byte-exact after unescaping (`\"`→`"`, `\$`→`$`, `\\`→ a single trailing `\`),
// so the pinned-Claude and pinned-Gemini exec lines keep their trailing
// backslash and the copilot_github_token assignment keeps its command
// substitution.
func providerLauncherAuthorityChecks() []check {
	cs := []check{
		{
			kind:       kindPresent,
			pattern:    "WORKCELL_PROVIDER_LAUNCHER_AUTHORITY",
			message:    "Expected provider wrapper to require the managed launcher authority marker",
			targetFile: providerWrapperRelPath,
		},
		{
			kind:       kindPresent,
			pattern:    "WORKCELL_PROVIDER_LAUNCHER_AUTHORITY",
			message:    "Expected workcell-launcher to set the provider-wrapper authority marker",
			targetFile: workcellLauncherRustRelPath,
		},
		{
			kind:       kindPresent,
			pattern:    "WORKCELL_PROVIDER_LAUNCHER_AUTHORITY",
			message:    "Expected workcell-launcher env sanitization to discard caller-supplied provider authority markers",
			targetFile: launcherCommonRustRelPath,
		},
		{
			kind:       kindPresent,
			pattern:    "spawn_and_wait_request",
			message:    "Expected workcell-launcher to keep a native parent supervising shell wrappers",
			targetFile: workcellLauncherRustRelPath,
		},
		// Parent-verification guard (first probe): shares the pair's message.
		{
			kind:       kindPresent,
			pattern:    "workcell_provider_parent_is_launcher",
			message:    "Expected provider wrapper to require a native Workcell launcher parent before managed provider launch",
			targetFile: providerWrapperRelPath,
		},
		// Parent-verification guard (second probe): shares the pair's message.
		{
			kind:       kindPresent,
			pattern:    `readlink "/proc/${PPID}/exe"`,
			message:    "Expected provider wrapper to require a native Workcell launcher parent before managed provider launch",
			targetFile: providerWrapperRelPath,
		},
		// Exec-guard pair (first probe): shares the pair's message.
		{
			kind:       kindPresent,
			pattern:    "current_process_parent_is_approved_native_launcher",
			message:    "Expected exec guard to reject protected runtime wrapper approval without a native launcher parent",
			targetFile: rustLibRelPath,
		},
		// Exec-guard pair (second probe): shares the pair's message.
		{
			kind:       kindPresent,
			pattern:    "approved_wrapper_requires_native_launcher_parent",
			message:    "Expected exec guard to reject protected runtime wrapper approval without a native launcher parent",
			targetFile: rustLibRelPath,
		},
	}
	// The twelve Gemini sandbox scrub checks (former `for` loop).
	for _, knob := range geminiSandboxEnvKnobs {
		cs = append(cs, check{
			kind:       kindPresent,
			pattern:    knob,
			message:    "Expected provider wrapper to scrub Gemini sandbox env knob: " + knob,
			targetFile: providerWrapperRelPath,
		})
	}
	cs = append(cs,
		// Pinned native Claude exec line (trailing backslash preserved).
		check{
			kind:       kindPresent,
			pattern:    `DISABLE_AUTOUPDATER=1 CLAUDE_CONFIG_DIR="${HOME}/.claude" exec /usr/local/libexec/workcell/real/claude \`,
			message:    "Expected provider wrapper to launch the pinned native Claude binary with managed env",
			targetFile: providerWrapperRelPath,
		},
		// Pinned Gemini native sandbox-off exec line (trailing backslash preserved).
		check{
			kind:       kindPresent,
			pattern:    `GEMINI_CLI_NO_RELAUNCH=1 GEMINI_SANDBOX=false exec /usr/local/libexec/workcell/real/node \`,
			message:    "Expected provider wrapper to pin Gemini native sandbox off on the managed path",
			targetFile: providerWrapperRelPath,
		},
		check{
			kind:       kindPresent,
			pattern:    `copilot_github_token="$(workcell_load_copilot_github_token)"`,
			message:    "Expected provider wrapper to load Copilot auth from the staged host-side token handoff",
			targetFile: providerWrapperRelPath,
		},
		check{
			kind:       kindPresent,
			pattern:    `token_file="$(head -n1 "${WORKCELL_RUNTIME_COPILOT_TOKEN_FILE_PATH}")"`,
			message:    "Expected provider wrapper to read the Copilot token handoff path from root-controlled runtime state",
			targetFile: providerWrapperRelPath,
		},
		// Consumed-marker guard (first probe): shares the pair's message.
		check{
			kind:       kindPresent,
			pattern:    `copilot_token_handoff_consumed_file="${WORKCELL_COPILOT_TOKEN_HANDOFF_CONTAINER_DIR}/copilot-token-consumed"`,
			message:    "Expected provider wrapper to write a host-visible Copilot token consumed marker",
			targetFile: providerWrapperRelPath,
		},
		// Consumed-marker guard (second probe): shares the pair's message.
		check{
			kind:       kindPresent,
			pattern:    `: >"${copilot_token_handoff_consumed_file}"`,
			message:    "Expected provider wrapper to write a host-visible Copilot token consumed marker",
			targetFile: providerWrapperRelPath,
		},
		check{
			kind:       kindPresent,
			pattern:    "unset GH_CONFIG_DIR",
			message:    "Expected provider wrapper to scrub GitHub CLI config directory overrides before provider launch",
			targetFile: providerWrapperRelPath,
		},
		// kindAbsent: a caller-supplied WORKCELL_COPILOT_TOKEN_FILE default is a
		// violation (present → exit 1).
		check{
			kind:       kindAbsent,
			pattern:    `local token_file="${WORKCELL_COPILOT_TOKEN_FILE:-}"`,
			message:    "Provider wrapper must not trust caller-supplied WORKCELL_COPILOT_TOKEN_FILE",
			targetFile: providerWrapperRelPath,
		},
		// Staged-token guard (first probe, affirmative): shares the pair's message.
		check{
			kind:       kindPresent,
			pattern:    `[[ -n "${token_file}" ]] || workcell_die "Copilot auth token handoff file is required."`,
			message:    "Expected provider wrapper to require staged Copilot token files instead of caller-supplied token env fallbacks",
			targetFile: providerWrapperRelPath,
		},
		// Staged-token guard (second probe, NEGATED): a WORKCELL_COPILOT_GITHUB_TOKEN
		// env fallback present is a violation.  Shares the pair's message.
		check{
			kind:       kindAbsent,
			pattern:    "WORKCELL_COPILOT_GITHUB_TOKEN:-",
			message:    "Expected provider wrapper to require staged Copilot token files instead of caller-supplied token env fallbacks",
			targetFile: providerWrapperRelPath,
		},
	)
	return cs
}

// CheckProviderLauncherAuthority runs the thirty provider-launcher-authority
// invariants against the repo rooted at rootDir, in the shell's original order.
// It returns nil when every invariant holds (the shell's exit 0), or an error
// whose message equals the shell's stderr for the first violated invariant (the
// shell's exit 1).
func CheckProviderLauncherAuthority(rootDir string) error {
	return evaluate(rootDir, providerLauncherAuthorityChecks())
}

// copilotPolicyWrapperChecks lists the twenty-two Copilot-policy-wrapper
// invariants in the same order as the former inline block in
// scripts/verify-invariants.sh (the block between the pinned-native-Claude-exec
// `rm -f -- "${token_file}"` guard's successor `unset
// WORKCELL_COPILOT_GITHUB_TOKEN` probe and the `for unsafe_copilot_flag` loop),
// so a reviewer can diff the two one-to-one.
//
// The block reads three files via the per-check targetFile field: the runtime
// provider wrapper (runtime/container/provider-wrapper.sh), the runtime provider
// policy helper (runtime/container/provider-policy.sh), and the container smoke
// harness (scripts/container-smoke.sh).  Every probe is a whole-file `grep -Fq`
// fixed-string containment except one negated `grep -Eq` (kindRegexAbsent), so
// each is kindPresent for the affirmative probes and kindAbsent for the two
// negated fixed-string guards.
//
// The negated shell-tool grant guard is a genuine `grep -Eq` regular expression
// (`--available-tools=[^"]*(shell|bash|run|exec)`), NOT a fixed string, so it is
// kindRegexAbsent (a match is a violation → exit 1).
//
// Two former shell `if` guards each joined two `grep -Fq` probes with `||` under
// a single message; they are expressed here as ordered checks sharing that
// message, which is behaviourally identical (the first failing probe yields the
// same stderr and exit 1 as the shell `if`):
//   - the all-tools/all-paths guard, whose two probes are BOTH negated (either
//     `--allow-all-tools` or `--allow-all-paths` present is a violation),
//     matched here as two ordered kindAbsent checks sharing one message.  The
//     shell short-circuits `grep A || grep B` to A-then-B, which the ordered
//     checks reproduce.
//
// Two former shell `if ! grep A || ! grep B || ...` guards each joined multiple
// affirmative probes across provider-policy.sh and container-smoke.sh under a
// single message; they are expressed here as ordered kindPresent checks sharing
// that message (the attached-prompt guard's four probes and the bundled
// short-option guard's three probes), which is behaviourally identical (the
// first missing probe yields the same stderr and exit 1).
//
// The double-quoted shell needles reproduce their literals byte-exact after
// unescaping (`\"`→`"`, `\$`→`$`, `\\`→ a single trailing `\`), so the pinned
// Copilot exec line keeps its trailing backslash and the `${arg}` /
// `${copilot_github_token}` needles keep their literal parameter-expansion text.
var copilotPolicyWrapperChecks = []check{
	{
		kind:       kindPresent,
		pattern:    "unset WORKCELL_COPILOT_GITHUB_TOKEN",
		message:    "Expected provider wrapper to discard the host-side Copilot token handoff variable before exec",
		targetFile: providerWrapperRelPath,
	},
	{
		kind:       kindPresent,
		pattern:    "unset WORKCELL_COPILOT_TOKEN_FILE",
		message:    "Expected provider wrapper to discard the Copilot token handoff path before exec",
		targetFile: providerWrapperRelPath,
	},
	{
		kind:       kindPresent,
		pattern:    `COPILOT_GITHUB_TOKEN="${copilot_github_token}"`,
		message:    "Expected provider wrapper to expose Copilot auth only as COPILOT_GITHUB_TOKEN to the managed child",
		targetFile: providerWrapperRelPath,
	},
	{
		kind:       kindPresent,
		pattern:    "COPILOT_ENABLE_HTTP2=false",
		message:    "Expected provider wrapper to pin Copilot HTTP/2 off on the managed path",
		targetFile: providerWrapperRelPath,
	},
	{
		kind:       kindPresent,
		pattern:    "--secret-env-vars=GH_TOKEN,GITHUB_TOKEN,COPILOT_GITHUB_TOKEN",
		message:    "Expected provider wrapper to declare Copilot/GitHub token env as provider secrets",
		targetFile: providerWrapperRelPath,
	},
	{
		kind:       kindPresent,
		pattern:    "--disallow-temp-dir",
		message:    "Expected provider wrapper to deny Copilot temp-dir access on the managed path",
		targetFile: providerWrapperRelPath,
	},
	{
		kind:       kindPresent,
		pattern:    `"--available-tools=view,create,edit,apply_patch,grep,glob"`,
		message:    "Expected provider wrapper to keep Copilot prompt/yolo tool grants shell-free",
		targetFile: providerWrapperRelPath,
	},
	{
		// kindRegexAbsent: the shell's `grep -Eq --
		// '--available-tools=[^"]*(shell|bash|run|exec)'` is a genuine ERE, not
		// a fixed string, so it must NOT match (present is a violation → exit 1).
		kind:       kindRegexAbsent,
		regex:      `--available-tools=[^"]*(shell|bash|run|exec)`,
		message:    "Provider wrapper must not grant Copilot shell-like tools on the safe path",
		targetFile: providerWrapperRelPath,
	},
	// All-tools/all-paths guard (first probe, NEGATED): shares the pair's message.
	{
		kind:       kindAbsent,
		pattern:    "--allow-all-tools",
		message:    "Provider wrapper must not grant Copilot all tools or all paths on the safe path",
		targetFile: providerWrapperRelPath,
	},
	// All-tools/all-paths guard (second probe, NEGATED): shares the pair's message.
	{
		kind:       kindAbsent,
		pattern:    "--allow-all-paths",
		message:    "Provider wrapper must not grant Copilot all tools or all paths on the safe path",
		targetFile: providerWrapperRelPath,
	},
	{
		// Pinned native Copilot exec line (trailing backslash preserved).
		kind:       kindPresent,
		pattern:    `exec /usr/local/libexec/workcell/real/copilot \`,
		message:    "Expected provider wrapper to launch the pinned native Copilot binary",
		targetFile: providerWrapperRelPath,
	},
	{
		kind:       kindPresent,
		pattern:    `Workcell blocked Claude lifecycle command: ${arg}`,
		message:    "Expected provider policy to reject native Claude lifecycle commands that bypass the pinned image",
		targetFile: providerPolicyRelPath,
	},
	{
		kind:       kindPresent,
		pattern:    `Workcell blocked Copilot lifecycle/control-plane command: ${arg}`,
		message:    "Expected provider policy to reject native Copilot lifecycle/control-plane commands",
		targetFile: providerPolicyRelPath,
	},
	{
		kind:       kindPresent,
		pattern:    "-p | --prompt)",
		message:    "Expected provider policy to treat only Copilot -p/--prompt as value-taking prompt flags",
		targetFile: providerPolicyRelPath,
	},
	// Attached-prompt guard (first probe): shares the group's message.
	{
		kind:       kindPresent,
		pattern:    `attached_prompt_value="${arg:2}"`,
		message:    "Expected provider policy and smoke coverage to reject attached dash-prefixed Copilot prompt values",
		targetFile: providerPolicyRelPath,
	},
	// Attached-prompt guard (second probe): shares the group's message.
	{
		kind:       kindPresent,
		pattern:    `attached_prompt_value="${arg#--prompt=}"`,
		message:    "Expected provider policy and smoke coverage to reject attached dash-prefixed Copilot prompt values",
		targetFile: providerPolicyRelPath,
	},
	// Attached-prompt guard (third probe, container-smoke.sh): shares the message.
	{
		kind:       kindPresent,
		pattern:    "workcell-copilot-policy-attached-short-prompt-allow-tool.out",
		message:    "Expected provider policy and smoke coverage to reject attached dash-prefixed Copilot prompt values",
		targetFile: containerSmokeRelPath,
	},
	// Attached-prompt guard (fourth probe, container-smoke.sh): shares the message.
	{
		kind:       kindPresent,
		pattern:    "workcell-copilot-policy-attached-long-prompt-allow-tool.out",
		message:    "Expected provider policy and smoke coverage to reject attached dash-prefixed Copilot prompt values",
		targetFile: containerSmokeRelPath,
	},
	// Bundled-short-option guard (first probe): shares the group's message.
	{
		kind:       kindPresent,
		pattern:    "-[!-]?*)",
		message:    "Expected provider policy and smoke coverage to reject bundled Copilot short options",
		targetFile: providerPolicyRelPath,
	},
	// Bundled-short-option guard (second probe): shares the group's message.
	{
		kind:       kindPresent,
		pattern:    `Workcell blocked bundled Copilot short options: ${arg}`,
		message:    "Expected provider policy and smoke coverage to reject bundled Copilot short options",
		targetFile: providerPolicyRelPath,
	},
	// Bundled-short-option guard (third probe, container-smoke.sh): shares the message.
	{
		kind:       kindPresent,
		pattern:    "workcell-copilot-policy-bundled-short-options.out",
		message:    "Expected provider policy and smoke coverage to reject bundled Copilot short options",
		targetFile: containerSmokeRelPath,
	},
	{
		// kindAbsent: the shell's `if grep -Fq -- '-p | --prompt | -i |
		// --interactive)' ...; then ... exit 1` is a negated fixed-string guard
		// (treating -i/--interactive as a prompt alias present is a violation).
		kind:       kindAbsent,
		pattern:    "-p | --prompt | -i | --interactive)",
		message:    "Expected provider policy not to treat Copilot -i/--interactive as prompt aliases",
		targetFile: providerPolicyRelPath,
	},
}

// CheckCopilotPolicyWrapper runs the twenty-two Copilot-policy-wrapper invariants
// against the repo rooted at rootDir, in the shell's original order.  It returns
// nil when every invariant holds (the shell's exit 0), or an error whose message
// equals the shell's stderr for the first violated invariant (the shell's exit 1).
func CheckCopilotPolicyWrapper(rootDir string) error {
	return evaluate(rootDir, copilotPolicyWrapperChecks)
}

// copilotUnsafeLongFlags lists the sixteen fixed Copilot flags the shell's
// `for unsafe_copilot_flag in ...` loop asserted the provider policy rejects.
// Order matches the shell verbatim so the generated checks reproduce the
// shell's first-failure stderr exactly.  Every flag is a fixed single-quoted
// shell literal, matched by `grep -Fq`.
var copilotUnsafeLongFlags = []string{
	"--config-dir",
	"--allow-tool",
	"--allow-all-tools",
	"--allow-all-mcp-server-instructions",
	"--available-tools",
	"--secret-env-vars",
	"--no-auto-update",
	"--no-remote",
	"--no-remote-export",
	"--disable-builtin-mcps",
	"--disallow-temp-dir",
	"--dynamic-retrieval",
	"--interactive",
	"--no-bash-env",
	"--plan",
	"--worktree",
}

// copilotUnsafeAttachedShortForms lists the five Copilot attached short-flag
// forms the shell's `for unsafe_copilot_short_form in ...` loop asserted the
// provider policy rejects.  `grep -Fq` treats the `?` and `*` glob characters
// as LITERAL, so each is a fixed string (e.g. the literal `-c?*`), not a glob;
// order matches the shell verbatim.
var copilotUnsafeAttachedShortForms = []string{
	"-c?*",
	"-i?*",
	"-a?*",
	"-A?*",
	"-w?*",
}

// copilotUnsafeBareShortForms lists the two Copilot bare short-flag case
// snippets the shell's `for unsafe_copilot_bare_short in ...` loop asserted are
// rejected and smoke-tested.  Each is a fixed single-quoted shell literal
// (including its spaces, pipes, parens, and trailing `)`), matched by
// `grep -Fq` across BOTH scripts/container-smoke.sh and
// runtime/container/provider-policy.sh (a multi-file OR: present in either file
// satisfies the check).  Order matches the shell verbatim.
var copilotUnsafeBareShortForms = []string{
	"copilot_short_flag in -C -i -n -r -w",
	"-C | -i | -n | -r | -w)",
}

// copilotUnsafeFlagsChecks returns the thirty-one Copilot-unsafe-flag
// invariants in the same order as the former inline block in
// scripts/verify-invariants.sh (the block between the copilot-policy-wrapper
// `go_verify_citools` call and the Copilot upstream-release-verifier
// `# shellcheck disable=SC2016` group), so a reviewer can diff the two
// one-to-one.
//
// The block reads six files via the per-check targetFile / targetFiles fields:
// the runtime provider policy (runtime/container/provider-policy.sh), the
// container smoke harness (scripts/container-smoke.sh), the runtime provider
// wrapper (runtime/container/provider-wrapper.sh), the runtime development
// wrapper (runtime/container/development-wrapper.sh), the exec-guard Rust
// library (runtime/container/rust/src/lib.rs), and the shared launcher_common.rs
// helper.
//
// Matching semantics mirror the shell exactly:
//   - The three `for` loops each ran `grep -Fq -- NEEDLE ...`.  The first two
//     loops probe a single file (provider-policy.sh), so each item is a
//     kindPresent check whose message interpolates the loop variable exactly as
//     the shell's `echo "... ${unsafe_copilot_flag}"` /
//     `${unsafe_copilot_short_form}` did.  Every needle is fixed-string
//     containment: `grep -Fq` treats the `?`/`*` glob characters in the short
//     forms as literal.
//   - The third loop ran `grep -Fq -- NEEDLE container-smoke.sh
//     provider-policy.sh` — a multi-file grep whose `! grep` guard fails only
//     when the needle is absent from BOTH files.  Each item is therefore a
//     kindPresentInAnyFile check over those two files, sharing the loop's
//     interpolated message.
//   - The eight trailing guards are single-file `! grep -Fq NEEDLE file` probes
//     (kindPresent, per-check targetFile).  Two former shell `if` guards each
//     joined two `grep -Fq` probes with `||` under one message (the exec-guard
//     wrapper-specific pair, both against rust/src/lib.rs; the forged-auth pair,
//     against launcher_common.rs then container-smoke.sh); they are expressed as
//     ordered kindPresent checks sharing that message, which is behaviourally
//     identical (the first missing probe yields the same stderr and exit 1).
func copilotUnsafeFlagsChecks() []check {
	var cs []check
	// Loop 1: sixteen fixed unsafe long flags rejected by provider-policy.sh.
	for _, flag := range copilotUnsafeLongFlags {
		cs = append(cs, check{
			kind:       kindPresent,
			pattern:    flag,
			message:    "Expected provider policy to reject Copilot unsafe flag: " + flag,
			targetFile: providerPolicyRelPath,
		})
	}
	// Loop 2: five attached short-flag forms rejected by provider-policy.sh.
	for _, form := range copilotUnsafeAttachedShortForms {
		cs = append(cs, check{
			kind:       kindPresent,
			pattern:    form,
			message:    "Expected provider policy to reject Copilot attached unsafe short flag: " + form,
			targetFile: providerPolicyRelPath,
		})
	}
	// Loop 3: two bare short-flag snippets present in container-smoke.sh OR
	// provider-policy.sh (multi-file grep OR).
	for _, form := range copilotUnsafeBareShortForms {
		cs = append(cs, check{
			kind:        kindPresentInAnyFile,
			pattern:     form,
			message:     "Expected Copilot bare unsafe short flags to be rejected and smoke-tested: " + form,
			targetFiles: []string{containerSmokeRelPath, providerPolicyRelPath},
		})
	}
	cs = append(cs,
		check{
			kind:       kindPresent,
			pattern:    "reject_unsafe_copilot_args",
			message:    "Expected provider wrapper to re-check Copilot argv before launch",
			targetFile: providerWrapperRelPath,
		},
		check{
			kind:       kindPresent,
			pattern:    `reject_protected_runtime_arguments "$@"`,
			message:    "Expected development wrapper to reject loader-mediated protected runtime targets before exec",
			targetFile: developmentWrapperRelPath,
		},
		check{
			kind:       kindPresent,
			pattern:    "development-wrapper-copilot-loader",
			message:    "Expected container smoke to cover development-wrapper loader-mediated Copilot execution",
			targetFile: containerSmokeRelPath,
		},
		check{
			kind:       kindPresent,
			pattern:    "workcell-copilot-real-copy",
			message:    "Expected container smoke to cover development-wrapper execution of copied protected Copilot payloads",
			targetFile: containerSmokeRelPath,
		},
		// Exec-guard wrapper-specific pair (first probe): shares the pair's message.
		check{
			kind:       kindPresent,
			pattern:    "ApprovedWrapper::Development | ApprovedWrapper::None => false",
			message:    "Expected exec guard to keep protected runtime authorization wrapper-specific",
			targetFile: rustLibRelPath,
		},
		// Exec-guard wrapper-specific pair (second probe): shares the pair's message.
		check{
			kind:       kindPresent,
			pattern:    "approved_wrapper_allows_runtime",
			message:    "Expected exec guard to keep protected runtime authorization wrapper-specific",
			targetFile: rustLibRelPath,
		},
		// Forged-auth pair (first probe, launcher_common.rs): shares the pair's message.
		check{
			kind:       kindPresent,
			pattern:    "WORKCELL_COPILOT_GITHUB_TOKEN",
			message:    "Expected launcher and smoke coverage to reject forged Copilot auth env",
			targetFile: launcherCommonRustRelPath,
		},
		// Forged-auth pair (second probe, container-smoke.sh): shares the pair's message.
		check{
			kind:       kindPresent,
			pattern:    "forged-copilot-token",
			message:    "Expected launcher and smoke coverage to reject forged Copilot auth env",
			targetFile: containerSmokeRelPath,
		},
	)
	return cs
}

// CheckCopilotUnsafeFlags runs the thirty-one Copilot-unsafe-flag invariants
// against the repo rooted at rootDir, in the shell's original order.  It returns
// nil when every invariant holds (the shell's exit 0), or an error whose message
// equals the shell's stderr for the first violated invariant (the shell's exit
// 1).
func CheckCopilotUnsafeFlags(rootDir string) error {
	return evaluate(rootDir, copilotUnsafeFlagsChecks())
}

// copilotReleaseHelpFlags lists the eleven managed Copilot flags the shell's
// `for copilot_release_help_flag in ...` loop asserted the upstream-release
// verifier requires.  Order matches the shell verbatim so the generated checks
// reproduce the shell's first-failure stderr exactly.  Every flag is a fixed
// single-quoted shell literal, matched by `grep -Fq --`.
var copilotReleaseHelpFlags = []string{
	"--allow-tool",
	"--available-tools",
	"--disable-builtin-mcps",
	"--disallow-temp-dir",
	"--log-dir",
	"--no-ask-user",
	"--no-auto-update",
	"--no-custom-instructions",
	"--no-remote",
	"--no-remote-export",
	"--secret-env-vars",
}

// copilotReleaseVerifyChecks returns the twenty-four Copilot upstream-release
// verifier invariants in the same order as the former inline block in
// scripts/verify-invariants.sh (the block between the copilot-unsafe-flags
// `go_verify_citools` call and the release-workflow native-help-count guard),
// so a reviewer can diff the two one-to-one.
//
// The block reads four files via the per-check targetFile field: the Copilot
// upstream-release verifier (scripts/verify-upstream-copilot-release.sh), the
// provider-pin bump script (scripts/update-provider-pins.sh), the routine CI
// validate job (scripts/ci/job-validate.sh), and the release workflow
// (.github/workflows/release.yml).  Every probe is a whole-file `grep -Fq`
// fixed-string containment (kindPresent); every needle is metacharacter-free
// under `grep -Fq` (fixed-string search), so each is fixed-string containment.
//
// Matching semantics mirror the shell exactly:
//   - The help-mode guard was one shell `if` joining six `! grep -Fq` probes
//     with `||` under a single message; it is expressed here as six ordered
//     kindPresent checks sharing that message, which is behaviourally identical
//     (the first missing probe yields the same stderr and exit 1 as the shell
//     `if`).  The sixth needle was written with shell double-quote escaping
//     (`\"`→`"`, `\$`→`$`); it is a `grep -Fq` fixed-string search of the
//     literal `grep -Eq -- "(^|[^[:alnum:]_-])${flag}([^[:alnum:]_-]|$)"`
//     (the embedded regex metacharacters are matched literally, not as a regex,
//     because `grep -Fq` treats the whole needle as a fixed string).
//   - The managed-flag loop ran `! grep -Fq -- "${copilot_release_help_flag}"`;
//     each item is a kindPresent check whose message interpolates the flag
//     exactly as the shell's `echo "... ${copilot_release_help_flag}"` did.
//   - The checksum-only guard was one shell `if` joining two `! grep -Fq`
//     probes for the SAME resolved variable needle against two files
//     (update-provider-pins.sh AND job-validate.sh) with `||` under a single
//     message; it is expressed here as two ordered kindPresent checks (one per
//     file) sharing that message, which is behaviourally identical.  The needle
//     resolves the shell `copilot_checksum_verify_needle` assignment to the
//     concrete literal `WORKCELL_COPILOT_RELEASE_HELP_MODE=checksum
//     "${ROOT_DIR}/scripts/verify-upstream-copilot-release.sh"` (shell
//     double-quote escaping `\"`→`"`, `\$`→`$`).
//   - The container-smoke release-help guard (two probes) and the arm64
//     release-help guard (three probes) each joined their `! grep -Fq` probes
//     with `||` under a single message against .github/workflows/release.yml;
//     they are expressed here as ordered kindPresent checks sharing that
//     message.
func copilotReleaseVerifyChecks() []check {
	const helpModeMessage = "Expected Copilot upstream release verifier to track native/Docker help probes separately, support checksum-only paths, and match whole safety flags"
	cs := []check{
		// Help-mode guard (six ordered probes sharing one message).
		{
			kind:       kindPresent,
			pattern:    `COPILOT_HELP_MODE="${WORKCELL_COPILOT_RELEASE_HELP_MODE:-auto}"`,
			message:    helpModeMessage,
			targetFile: verifyUpstreamCopilotReleaseRelPath,
		},
		{
			kind:       kindPresent,
			pattern:    "COPILOT_NATIVE_HELP_DONE=0",
			message:    helpModeMessage,
			targetFile: verifyUpstreamCopilotReleaseRelPath,
		},
		{
			kind:       kindPresent,
			pattern:    "COPILOT_DOCKER_HELP_DONE=0",
			message:    helpModeMessage,
			targetFile: verifyUpstreamCopilotReleaseRelPath,
		},
		{
			kind:       kindPresent,
			pattern:    "auto | native | docker | checksum)",
			message:    helpModeMessage,
			targetFile: verifyUpstreamCopilotReleaseRelPath,
		},
		{
			kind:       kindPresent,
			pattern:    `[[ "${COPILOT_HELP_MODE}" == "checksum" ]] && return 0`,
			message:    helpModeMessage,
			targetFile: verifyUpstreamCopilotReleaseRelPath,
		},
		{
			// kindPresent: `grep -Fq` fixed-string search of the literal
			// whole-flag matcher; the embedded ERE metacharacters are matched
			// literally because grep -Fq treats the whole needle as a fixed
			// string.  The shell needle unescaped `\"`→`"` and `\$`→`$`.
			kind:       kindPresent,
			pattern:    `grep -Eq -- "(^|[^[:alnum:]_-])${flag}([^[:alnum:]_-]|$)"`,
			message:    helpModeMessage,
			targetFile: verifyUpstreamCopilotReleaseRelPath,
		},
	}
	// The managed-flag loop (eleven fixed flags required by the verifier).
	for _, flag := range copilotReleaseHelpFlags {
		cs = append(cs, check{
			kind:       kindPresent,
			pattern:    flag,
			message:    "Expected Copilot upstream release verifier to require managed flag: " + flag,
			targetFile: verifyUpstreamCopilotReleaseRelPath,
		})
	}
	const checksumNeedle = `WORKCELL_COPILOT_RELEASE_HELP_MODE=checksum "${ROOT_DIR}/scripts/verify-upstream-copilot-release.sh"`
	const checksumMessage = "Expected provider bump and routine validate paths to use checksum-only Copilot release verification before smoke images exist"
	const smokeMessage = "Expected release container-smoke job to force Copilot release help verification inside the runtime image"
	const arm64Message = "Expected release workflow to verify Copilot release help inside an arm64 runtime image before publication"
	cs = append(cs,
		// Checksum-only guard (same needle in two files, sharing one message).
		check{
			kind:       kindPresent,
			pattern:    checksumNeedle,
			message:    checksumMessage,
			targetFile: updateProviderPinsRelPath,
		},
		check{
			kind:       kindPresent,
			pattern:    checksumNeedle,
			message:    checksumMessage,
			targetFile: jobValidateRelPath,
		},
		// Container-smoke release-help guard (two release.yml probes sharing one
		// message).
		check{
			kind:       kindPresent,
			pattern:    "WORKCELL_COPILOT_RELEASE_HELP_MODE: docker",
			message:    smokeMessage,
			targetFile: releaseWorkflowRelPath,
		},
		check{
			kind:       kindPresent,
			pattern:    "WORKCELL_COPILOT_RELEASE_HELP_IMAGE: workcell:smoke",
			message:    smokeMessage,
			targetFile: releaseWorkflowRelPath,
		},
		// Arm64 release-help guard (three release.yml probes sharing one message).
		check{
			kind:       kindPresent,
			pattern:    "preflight-arm64-copilot-runtime:",
			message:    arm64Message,
			targetFile: releaseWorkflowRelPath,
		},
		check{
			kind:       kindPresent,
			pattern:    "WORKCELL_COPILOT_RELEASE_HELP_IMAGE: workcell:copilot-arm64-smoke",
			message:    arm64Message,
			targetFile: releaseWorkflowRelPath,
		},
		check{
			kind:       kindPresent,
			pattern:    "preflight-arm64-copilot-runtime",
			message:    arm64Message,
			targetFile: releaseWorkflowRelPath,
		},
	)
	return cs
}

// CheckCopilotReleaseVerify runs the twenty-four Copilot upstream-release
// verifier invariants against the repo rooted at rootDir, in the shell's
// original order.  It returns nil when every invariant holds (the shell's exit
// 0), or an error whose message equals the shell's stderr for the first
// violated invariant (the shell's exit 1).
func CheckCopilotReleaseVerify(rootDir string) error {
	return evaluate(rootDir, copilotReleaseVerifyChecks())
}

// adapterRuleGuardBashChecks lists the eighteen adapter-rule / Bash-guard
// invariants in the same order as the former inline block in
// scripts/verify-invariants.sh (the block starting at the release.yml
// native-help count guard, through the codex_rule_file loop, and ending with
// the Claude Bash guard checks), so a reviewer can diff the two one-to-one.
//
// The block reads four files via the per-check targetFile field:
// .github/workflows/release.yml (the native-help count guard),
// adapters/codex/managed_config.toml and adapters/codex/requirements.toml (the
// codex_rule_file loop), and adapters/claude/hooks/guard-bash.sh (the Bash
// guard checks).
//
// Two shell constructs are flattened into ordered checks sharing one message,
// exactly as in the earlier groups:
//
//   - Each codex_rule_file probe-3 and the guard-bash multi-path probe were a
//     single shell `if` guarding several `grep -Fq` probes joined by `||`
//     (every needle must be present); each is expressed here as two/four
//     ordered kindPresent checks sharing that probe's message, which is
//     behaviourally identical (any missing needle yields the same stderr and
//     exit 1 before later checks run).
//   - The `if grep -Fq '@anthropic-ai/claude-code/cli.js'; then ... exit 1`
//     probes (present is a violation) become kindAbsent checks.
//
// The codex_rule_file loop ran the same four probes against managed_config.toml
// then requirements.toml, interpolating basename(file) into each message; the
// file-outer / probe-inner order is preserved here.  The guard-bash
// provider-wrapper needle is the regex-escaped `provider-wrapper\.sh` (a literal
// backslash-dot, byte-for-byte as it appears inside the guard's regex), unlike
// the codex loop's unescaped `provider-wrapper.sh`; the `\\.copilot` and
// `copilot\.md` needles are likewise copied byte-exact from the guard regex.
func adapterRuleGuardBashChecks() []check {
	const guardBypassMessage = "Expected Claude Bash guard to block Copilot provider and home control-plane bypass paths"
	cs := []check{
		// Release-workflow native-help count guard: the native help-mode needle
		// must appear on at least two lines (the amd64 and arm64 lanes).
		{
			kind:       kindCountAtLeast,
			pattern:    "WORKCELL_COPILOT_RELEASE_HELP_MODE: native",
			minCount:   2,
			message:    "Expected release workflow to force native Copilot release help verification for amd64 and arm64 lanes",
			targetFile: releaseWorkflowRelPath,
		},
	}
	// codex_rule_file loop: the same four probes against each rule file, with
	// basename(file) interpolated into every message.  Probe 3 (the Copilot
	// mediation-bypass guard) was a two-needle `||` and is two ordered
	// kindPresent checks sharing one message.
	for _, f := range []struct{ path, base string }{
		{codexManagedConfigRelPath, "managed_config.toml"},
		{codexRequirementsRelPath, "requirements.toml"},
	} {
		cs = append(cs,
			check{
				kind:       kindPresent,
				pattern:    "/usr/local/libexec/workcell/provider-wrapper.sh",
				message:    "Expected " + f.base + " to block direct provider-wrapper launches",
				targetFile: f.path,
			},
			check{
				kind:       kindPresent,
				pattern:    "/usr/local/libexec/workcell/real/claude",
				message:    "Expected " + f.base + " to block the native Claude binary path",
				targetFile: f.path,
			},
			check{
				kind:       kindPresent,
				pattern:    "/usr/local/libexec/workcell/core/copilot",
				message:    "Expected " + f.base + " to block Copilot provider mediation bypass paths",
				targetFile: f.path,
			},
			check{
				kind:       kindPresent,
				pattern:    "/usr/local/libexec/workcell/real/copilot",
				message:    "Expected " + f.base + " to block Copilot provider mediation bypass paths",
				targetFile: f.path,
			},
			check{
				kind:       kindAbsent,
				pattern:    "@anthropic-ai/claude-code/cli.js",
				message:    f.base + " should not reference the removed Claude npm entrypoint",
				targetFile: f.path,
			},
		)
	}
	// Claude Bash guard checks.  The provider-wrapper needle carries the
	// literal backslash-dot from the guard's regex; the multi-path probe (four
	// needles under one `||` guard) becomes four ordered kindPresent checks
	// sharing guardBypassMessage.
	cs = append(cs,
		check{
			kind:       kindPresent,
			pattern:    `/usr/local/libexec/workcell/provider-wrapper\.sh`,
			message:    "Expected Claude Bash guard to block direct provider-wrapper launches",
			targetFile: claudeGuardBashRelPath,
		},
		check{
			kind:       kindPresent,
			pattern:    "/usr/local/libexec/workcell/real/claude",
			message:    "Expected Claude Bash guard to block direct native Claude binary launches",
			targetFile: claudeGuardBashRelPath,
		},
		check{
			kind:       kindPresent,
			pattern:    "/usr/local/libexec/workcell/core/copilot",
			message:    guardBypassMessage,
			targetFile: claudeGuardBashRelPath,
		},
		check{
			kind:       kindPresent,
			pattern:    "/usr/local/libexec/workcell/real/copilot",
			message:    guardBypassMessage,
			targetFile: claudeGuardBashRelPath,
		},
		check{
			kind:       kindPresent,
			pattern:    `\\.copilot`,
			message:    guardBypassMessage,
			targetFile: claudeGuardBashRelPath,
		},
		check{
			kind:       kindPresent,
			pattern:    `copilot\.md`,
			message:    guardBypassMessage,
			targetFile: claudeGuardBashRelPath,
		},
		check{
			kind:       kindAbsent,
			pattern:    "@anthropic-ai/claude-code/cli.js",
			message:    "Claude Bash guard should not reference the removed Claude npm entrypoint",
			targetFile: claudeGuardBashRelPath,
		},
	)
	return cs
}

// CheckAdapterRuleGuardBash runs the eighteen adapter-rule / Bash-guard
// invariants against the repo rooted at rootDir, in the shell's original order.
// It returns nil when every invariant holds (the shell's exit 0), or an error
// whose message equals the shell's stderr for the first violated invariant (the
// shell's exit 1).
func CheckAdapterRuleGuardBash(rootDir string) error {
	return evaluate(rootDir, adapterRuleGuardBashChecks())
}

// inspectMountViewNeedles are the five workcell mount-view validation snippets
// the shell's first `for needle in ...` loop required in scripts/workcell via
// `grep -Fq -- "${needle}" "${ROOT_DIR}/scripts/workcell"`.  Each is fixed-string
// containment (some carry `()` or `${...}` treated literally by `grep -F`).
var inspectMountViewNeedles = []string{
	"workspace_runtime_probe_path()",
	"validate_colima_runtime_workspace_view()",
	`validate_colima_runtime_workspace_view "${profile}" "${workspace}"`,
	"Refreshing managed Colima profile ${COLIMA_PROFILE} because the running VM is not exposing the expected workspace contents.",
	"Refreshing managed Colima profile ${COLIMA_PROFILE} because the started VM did not expose the expected workspace view.",
}

// inspectEgressSafeCwdNeedles are the three egress-helper safe-cwd snippets the
// shell's second loop required in scripts/colima-egress-allowlist.sh via
// `grep -Fq -- "${needle}" "${ROOT_DIR}/scripts/colima-egress-allowlist.sh"`.
var inspectEgressSafeCwdNeedles = []string{
	`cd "${home}" &&`,
	"cd / &&",
	"LIMA_WORKDIR=/",
}

// inspectContractTokens are the eight --inspect contract tokens the shell's
// `for token in ...` loop required in scripts/workcell via
// `grep -Fq -- "${token}" "${ROOT_DIR}/scripts/workcell"`.
var inspectContractTokens = []string{
	"--inspect",
	"print_inspect_state",
	"provider_native_sandbox_configured",
	"provider_native_sandbox_effective",
	"provider_native_sandbox_reason",
	"codex",
	"claude",
	"gemini",
}

// inspectAuditLogFields are the nine audit-log fields the shell's `for field in
// ...` loop required in scripts/workcell OR runtime/container/assurance.sh.  Its
// guard `! grep -Fq -- "$field" workcell && ! grep -Fq -- "$field" assurance.sh`
// fails only when the field is absent from BOTH files, so each maps to a
// kindPresentInAnyFile check over those two files.
var inspectAuditLogFields = []string{
	"workspace",
	"network_policy",
	"session_assurance_initial",
	"provider_native_sandbox_configured",
	"provider_native_sandbox_effective",
	"provider_native_sandbox_reason",
	"codex",
	"claude",
	"gemini",
}

// inspectAssuranceLoopsChecks lists the twenty-five --inspect / session-assurance
// invariants in the same order as the four contiguous `for` loops they were
// migrated from in scripts/verify-invariants.sh, so a reviewer can diff the two
// one-to-one.
//
// Matching semantics mirror the shell exactly:
//   - Loop 1 (mount-view) and Loop 3 (--inspect contract tokens) each ran
//     `grep -Fq -- NEEDLE scripts/workcell`, so every item is a kindPresent
//     check (default targetFile scripts/workcell) whose message interpolates the
//     loop variable exactly as the shell's `echo` did.
//   - Loop 2 (egress safe-cwd) ran `grep -Fq -- NEEDLE
//     scripts/colima-egress-allowlist.sh`, so each item is a kindPresent check
//     with targetFile colimaEgressAllowlistRelPath.
//   - Loop 4 (audit-log field) ran `! grep -Fq FIELD workcell && ! grep -Fq
//     FIELD assurance.sh` — a violation only when the field is absent from BOTH
//     files — so each item is a kindPresentInAnyFile check over those two files.
//
// Every needle is fixed-string containment (`grep -Fq`), so glob/regex
// metacharacters in the items are matched literally.
func inspectAssuranceLoopsChecks() []check {
	var cs []check
	// Loop 1: mount-view validation snippets in scripts/workcell.
	for _, needle := range inspectMountViewNeedles {
		cs = append(cs, check{
			kind:    kindPresent,
			pattern: needle,
			message: "Expected workcell mount-view validation snippet missing: " + needle,
		})
	}
	// Loop 2: safe-cwd snippets in scripts/colima-egress-allowlist.sh.
	for _, needle := range inspectEgressSafeCwdNeedles {
		cs = append(cs, check{
			kind:       kindPresent,
			pattern:    needle,
			message:    "Expected egress helper safe-cwd snippet missing: " + needle,
			targetFile: colimaEgressAllowlistRelPath,
		})
	}
	// Loop 3: --inspect contract tokens in scripts/workcell.
	for _, token := range inspectContractTokens {
		cs = append(cs, check{
			kind:    kindPresent,
			pattern: token,
			message: "Expected workcell to contain --inspect contract token: " + token,
		})
	}
	// Loop 4: audit-log fields present in scripts/workcell OR assurance.sh.
	for _, field := range inspectAuditLogFields {
		cs = append(cs, check{
			kind:        kindPresentInAnyFile,
			pattern:     field,
			message:     "Expected audit log field referenced in control scripts: " + field,
			targetFiles: []string{launcherRelPath, assuranceRelPath},
		})
	}
	return cs
}

// CheckInspectAssuranceLoops runs the twenty-five --inspect / session-assurance
// invariants against the repo rooted at rootDir, in the shell's original order.
// It returns nil when every invariant holds (the shell's exit 0), or an error
// whose message equals the shell's stderr for the first violated invariant (the
// shell's exit 1).
func CheckInspectAssuranceLoops(rootDir string) error {
	return evaluate(rootDir, inspectAssuranceLoopsChecks())
}

// buildAndTestRelPath is the repo-relative path to the build-and-test
// driver.  The validator-writable-state block reads this file (via the
// per-check targetFile field) for its --docker caller-UID/GID isolation
// probes, mirroring the shell `grep -Fq` loop that ran against
// ${ROOT_DIR}/scripts/build-and-test.sh.
const buildAndTestRelPath = "scripts/build-and-test.sh"

// trustedDockerClientRelPath is the repo-relative path to the trusted
// Docker client helper.  Only the validator-writable-state isolated-home
// probe reads this file (via the per-check targetFile field), mirroring the
// shell `grep -Fq` that ran against
// ${ROOT_DIR}/scripts/lib/trusted-docker-client.sh.
const trustedDockerClientRelPath = "scripts/lib/trusted-docker-client.sh"

// verifyReleaseBundleRelPath is the repo-relative path to the release-bundle
// verifier.  The validator-writable-state block reads this file (via the
// per-check targetFile field) for its caller-UID/GID isolation probes and its
// mounted-repo-write-avoidance probe, mirroring the shell `grep -Fq` loop and
// guard that ran against ${ROOT_DIR}/scripts/verify-release-bundle.sh.
const verifyReleaseBundleRelPath = "scripts/verify-release-bundle.sh"

// verifyBuildInputManifestRelPath is the repo-relative path to the
// build-input-manifest verifier.  Only the validator-writable-state
// mounted-repo-write-avoidance probe reads this file (via the per-check
// targetFile field), mirroring the shell `grep -Fq` guard that ran against
// ${ROOT_DIR}/scripts/verify-build-input-manifest.sh.
const verifyBuildInputManifestRelPath = "scripts/verify-build-input-manifest.sh"

// verifyControlPlaneManifestRelPath is the repo-relative path to the
// control-plane-manifest verifier.  Only the validator-writable-state
// mounted-repo-write-avoidance probe reads this file (via the per-check
// targetFile field), mirroring the shell `grep -Fq` guard that ran against
// ${ROOT_DIR}/scripts/verify-control-plane-manifest.sh.
const verifyControlPlaneManifestRelPath = "scripts/verify-control-plane-manifest.sh"

// verifyReproducibleBuildRelPath is the repo-relative path to the
// reproducible-build verifier.  Only the validator-writable-state
// mounted-repo-write-avoidance probe reads this file (via the per-check
// targetFile field), mirroring the shell `grep -Fq` guard that ran against
// ${ROOT_DIR}/scripts/verify-reproducible-build.sh.
const verifyReproducibleBuildRelPath = "scripts/verify-reproducible-build.sh"

// buildAndTestValidatorIsolationNeedles lists the ten fixed-string snippets
// the shell's `for required in ...; do grep -Fq -- "${required}"
// scripts/build-and-test.sh; done` loop asserted are present, in the shell's
// verbatim order.  Each needle reproduces the shell double-quoted literal
// byte-exact (`\$`→`$`, `\"`→`"`, `\${...}`→`${...}`).
var buildAndTestValidatorIsolationNeedles = []string{
	"WORKCELL_BUILD_AND_TEST_VALIDATOR_UID=",
	"WORKCELL_BUILD_AND_TEST_VALIDATOR_GID=",
	`--user "${WORKCELL_BUILD_AND_TEST_VALIDATOR_UID}:${WORKCELL_BUILD_AND_TEST_VALIDATOR_GID}"`,
	`-e HOME="${WORKCELL_BUILD_AND_TEST_VALIDATOR_HOME}"`,
	`-e XDG_CACHE_HOME="${WORKCELL_BUILD_AND_TEST_VALIDATOR_CACHE}"`,
	`-e GOCACHE="${WORKCELL_BUILD_AND_TEST_VALIDATOR_CACHE}/go-build"`,
	`-e GOMODCACHE="${WORKCELL_BUILD_AND_TEST_VALIDATOR_CACHE}/go-mod"`,
	`-e CARGO_TARGET_DIR="${WORKCELL_BUILD_AND_TEST_VALIDATOR_CACHE}/cargo-target"`,
	`-e TMPDIR="${WORKCELL_BUILD_AND_TEST_VALIDATOR_TMP}"`,
	`mkdir -p "${HOME}" "${XDG_CACHE_HOME}" "${GOCACHE}" "${GOMODCACHE}" "${CARGO_TARGET_DIR}" "${TMPDIR}"`,
}

// releaseBundleValidatorIsolationNeedles lists the eight fixed-string snippets
// the shell's `for required in ...; do grep -Fq -- "${required}"
// scripts/verify-release-bundle.sh; done` loop asserted are present, in the
// shell's verbatim order.  Each needle reproduces the shell double-quoted
// literal byte-exact.
var releaseBundleValidatorIsolationNeedles = []string{
	`--user "${validator_uid}:${validator_gid}"`,
	`-e HOME="${validator_home}"`,
	`-e XDG_CACHE_HOME="${validator_cache_root}"`,
	`-e GOCACHE="${validator_cache_root}/go-build"`,
	`-e GOMODCACHE="${validator_cache_root}/go-mod"`,
	`-e CARGO_TARGET_DIR="${validator_cache_root}/cargo-target"`,
	`-e TMPDIR="${validator_tmpdir}"`,
	`mkdir -p "${HOME}" "${XDG_CACHE_HOME}" "${GOCACHE}" "${GOMODCACHE}" "${CARGO_TARGET_DIR}" "${TMPDIR}"`,
}

// validatorWritableStateChecks returns the twenty-three validator
// writable-state isolation invariants in the same order as the former inline
// block in scripts/verify-invariants.sh (the block between the
// build-and-test.sh caller-UID/GID loop's predecessor — the
// verify-release-bundle.sh validator loop — and the
// go_verify_citools workcell-bootstrap-audit dispatch), so a reviewer can diff
// the two one-to-one.
//
// The block asserts that validator work runs under an explicit caller UID/GID
// with isolated writable state, and that the manifest/bundle verifiers avoid
// writing under the mounted repo:
//
//   - The ten build-and-test.sh probes and eight verify-release-bundle.sh
//     probes came from two `for required in ...; do grep -Fq -- "${required}"
//     FILE; done` loops whose stderr interpolated the needle into a shared
//     per-file message; because every needle is a fixed `grep -Fq` literal,
//     each check is kindPresent with the message computed verbatim as the
//     shell's "Expected ... isolated writable state (${required})".
//   - The trusted-docker-client.sh isolated-home probe is a single affirmative
//     `grep -Fq` (kindPresent) with a fixed message.
//   - The four mounted-repo-write-avoidance probes are NEGATED `grep -Fq`
//     guards (`if grep -Fq X FILE; then ... exit 1`): the literal
//     `${ROOT_DIR}/tmp/workcell-*` temp-root snippet present in the target is a
//     violation, so each is kindAbsent (the `${` `}` are matched literally, as
//     grep -Fq does).
func validatorWritableStateChecks() []check {
	cs := make([]check, 0, len(buildAndTestValidatorIsolationNeedles)+1+len(releaseBundleValidatorIsolationNeedles)+4)
	for _, needle := range buildAndTestValidatorIsolationNeedles {
		cs = append(cs, check{
			kind:       kindPresent,
			pattern:    needle,
			message:    "Expected scripts/build-and-test.sh --docker to launch validator work under an explicit caller UID/GID with isolated writable state (" + needle + ")",
			targetFile: buildAndTestRelPath,
		})
	}
	cs = append(cs, check{
		kind:       kindPresent,
		pattern:    `fallback_home="${fallback_parent%/}/workcell-home-${uid}"`,
		message:    "Expected trusted-docker-client.sh to synthesize an isolated home for passwd-less caller UIDs",
		targetFile: trustedDockerClientRelPath,
	})
	for _, needle := range releaseBundleValidatorIsolationNeedles {
		cs = append(cs, check{
			kind:       kindPresent,
			pattern:    needle,
			message:    "Expected scripts/verify-release-bundle.sh to build bundles in the validator under an explicit caller UID/GID with isolated writable state (" + needle + ")",
			targetFile: verifyReleaseBundleRelPath,
		})
	}
	cs = append(cs,
		check{
			kind:       kindAbsent,
			pattern:    "${ROOT_DIR}/tmp/workcell-build-input-nested",
			message:    "Expected verify-build-input-manifest.sh nested-source checks to avoid writing under the mounted repo",
			targetFile: verifyBuildInputManifestRelPath,
		},
		check{
			kind:       kindAbsent,
			pattern:    "${ROOT_DIR}/tmp/workcell-control-plane-nested",
			message:    "Expected verify-control-plane-manifest.sh nested-source checks to avoid writing under the mounted repo",
			targetFile: verifyControlPlaneManifestRelPath,
		},
		check{
			kind:       kindAbsent,
			pattern:    "${ROOT_DIR}/tmp/workcell-release-bundle",
			message:    "Expected verify-release-bundle.sh temp roots to avoid writing under the mounted repo",
			targetFile: verifyReleaseBundleRelPath,
		},
		check{
			kind:       kindAbsent,
			pattern:    "${ROOT_DIR}/tmp/workcell-repro",
			message:    "Expected verify-reproducible-build.sh OCI exports to avoid writing under the mounted repo",
			targetFile: verifyReproducibleBuildRelPath,
		},
	)
	return cs
}

// CheckValidatorWritableState runs the twenty-three validator writable-state
// isolation invariants against the repo rooted at rootDir, in the shell's
// original order.  It returns nil when every invariant holds (the shell's exit
// 0), or an error whose message equals the shell's stderr for the first
// violated invariant (the shell's exit 1).
func CheckValidatorWritableState(rootDir string) error {
	return evaluate(rootDir, validatorWritableStateChecks())
}

// hostutilEgressRgChecks lists the twenty-one hostutil / entrypoint /
// colima-egress `rg` invariants in the same order as the former inline block in
// scripts/verify-invariants.sh (the contiguous run of `rg`/`head`+`grep`
// guards between the runtime-build-retry harness and the HOST_GATE_SCRIPTS
// self-sanitizing loop), so a reviewer can diff the two one-to-one.  The block
// ends at that HOST_GATE_SCRIPTS `for` loop, which iterates a dynamic array of
// scripts rather than one fixed file and so falls outside the single-file
// `rg`/`grep` shape migrated here.
//
// Every probe is an `rg -q` regex (line-oriented, matched per line via
// regexMatchesAnyLine for `rg` parity), except the colima-egress shebang probe
// which is a `head -n1 ... | grep -q '^...$'` first-line anchored regex
// (kindFirstLineRegex).  Matching semantics mirror the shell exactly:
//
//   - Affirmative `if ! rg -q P` guards → kindRegexPresent (P must match).
//   - Negated `if rg -q P; then ... exit 1` guards → kindRegexAbsent (P
//     matching is a violation).  These are the two entrypoint probes
//     (`set -- codex --cd `, `AGENT_NAME="\$\{AGENT_NAME:-codex\}"`) and the
//     colima-egress PATH-trust probe.
//
// Unlike the earlier metacharacter-free blocks migrated as fixed-string
// kindPresent/kindAbsent, these patterns are kept verbatim as regexes:
//
//   - The escaped-literal patterns (`"\$\{ROOT_DIR\}"`, `GOPATH="\$\{GOPATH\}"`,
//     `"\$@"`, `AGENT_NAME="\$\{AGENT_NAME:-codex\}"`, `DYLD_\*`, ...) use the
//     rg regex escapes `\$ \{ \} \*` to match the literal `$ { } *`; Go's
//     regexp interprets the same escapes, so the pattern is used byte-for-byte.
//   - The colima-egress PATH-trust probe `command -v|type -P|which ` is a
//     genuine alternation (the `|` are real regex OR), so it is a true regex
//     kindRegexAbsent (present is a violation).
//
// Three former shell `if` guards each joined several `! rg -q` probes with `||`
// under a single message (the go-hostutil bootstrap-Go guard, the
// entrypoint file-trace-trap guard, and the colima-egress Go-runtime guard);
// they are expressed here as ordered kindRegexPresent checks sharing that
// message, which is behaviourally identical (any missing probe yields the same
// stderr and exit 1 as the corresponding shell `if`).
//
// Target files: five probes read scripts/lib/launcher/go-hostutil.sh, four read
// runtime/container/entrypoint.sh, and twelve read
// scripts/colima-egress-allowlist.sh (all via the per-check targetFile field).
var hostutilEgressRgChecks = []check{
	// Guard 1: go-hostutil.sh invokes the bootstrap Go helper from the repo
	// root under a scrubbed environment with explicit Go caches (five ordered
	// `! rg -q` probes sharing one message).
	{
		kind:       kindRegexPresent,
		regex:      `run_clean_host_command_in_dir "\$\{ROOT_DIR\}" env`,
		message:    "Expected scripts/lib/launcher/go-hostutil.sh to invoke the bootstrap Go helper from the repo root under a scrubbed environment with explicit Go caches",
		targetFile: goHostutilRelPath,
	},
	{
		kind:       kindRegexPresent,
		regex:      `GOPATH="\$\{GOPATH\}"`,
		message:    "Expected scripts/lib/launcher/go-hostutil.sh to invoke the bootstrap Go helper from the repo root under a scrubbed environment with explicit Go caches",
		targetFile: goHostutilRelPath,
	},
	{
		kind:       kindRegexPresent,
		regex:      `GOMODCACHE="\$\{GOMODCACHE\}"`,
		message:    "Expected scripts/lib/launcher/go-hostutil.sh to invoke the bootstrap Go helper from the repo root under a scrubbed environment with explicit Go caches",
		targetFile: goHostutilRelPath,
	},
	{
		kind:       kindRegexPresent,
		regex:      `GOCACHE="\$\{GOCACHE\}"`,
		message:    "Expected scripts/lib/launcher/go-hostutil.sh to invoke the bootstrap Go helper from the repo root under a scrubbed environment with explicit Go caches",
		targetFile: goHostutilRelPath,
	},
	{
		kind:       kindRegexPresent,
		regex:      `"\$\{HOST_GO_BIN\}" run ./cmd/workcell-hostutil "\$@"`,
		message:    "Expected scripts/lib/launcher/go-hostutil.sh to invoke the bootstrap Go helper from the repo root under a scrubbed environment with explicit Go caches",
		targetFile: goHostutilRelPath,
	},
	// entrypoint.sh must not inject a blocked default Codex --cd override
	// (negated `if rg -q ...` → present is a violation).
	{
		kind:       kindRegexAbsent,
		regex:      `set -- codex --cd `,
		message:    "runtime/container/entrypoint.sh still injects a blocked default Codex --cd override",
		targetFile: entrypointRelPath,
	},
	// entrypoint.sh must not default AGENT_NAME to codex (negated `if rg -q`;
	// the pattern escapes `$ { }` to match the literal assignment).
	{
		kind:       kindRegexAbsent,
		regex:      `AGENT_NAME="\$\{AGENT_NAME:-codex\}"`,
		message:    "runtime/container/entrypoint.sh still defaults AGENT_NAME to codex",
		targetFile: entrypointRelPath,
	},
	// Guard: entrypoint.sh traps INT/TERM and finalizes file-trace shutdown
	// before exit (two ordered `! rg -q` probes sharing one message).
	{
		kind:       kindRegexPresent,
		regex:      `trap 'workcell_run_command_with_file_trace_signal INT' INT`,
		message:    "Expected runtime/container/entrypoint.sh to trap INT/TERM and finalize file-trace shutdown before exit",
		targetFile: entrypointRelPath,
	},
	{
		kind:       kindRegexPresent,
		regex:      `trap 'workcell_run_command_with_file_trace_signal TERM' TERM`,
		message:    "Expected runtime/container/entrypoint.sh to trap INT/TERM and finalize file-trace shutdown before exit",
		targetFile: entrypointRelPath,
	},
	// colima-egress-allowlist.sh must not trust PATH for executed host tools
	// (negated `if rg -q`; `command -v|type -P|which ` is a genuine
	// alternation, so present is a violation via kindRegexAbsent).
	{
		kind:       kindRegexAbsent,
		regex:      `command -v|type -P|which `,
		message:    "scripts/colima-egress-allowlist.sh still trusts PATH for executed host tools",
		targetFile: colimaEgressAllowlistRelPath,
	},
	{
		kind:       kindRegexPresent,
		regex:      `REAL_HOME=`,
		message:    "Expected scripts/colima-egress-allowlist.sh to derive the real host home independently of caller HOME",
		targetFile: colimaEgressAllowlistRelPath,
	},
	// First-line anchored regex mirroring `head -n1 ... | grep -q '^...$'`.
	{
		kind:       kindFirstLineRegex,
		regex:      `^#!/usr/bin/env -S -i PATH=.* BASH_ENV= ENV= /bin/bash$`,
		message:    "Expected scripts/colima-egress-allowlist.sh to use env -S -i with an absolute /bin/bash and cleared host environment",
		targetFile: colimaEgressAllowlistRelPath,
	},
	{
		kind:       kindRegexPresent,
		regex:      `scrub_host_process_env`,
		message:    "Expected scripts/colima-egress-allowlist.sh to scrub hostile host process environment before host tool lookup",
		targetFile: colimaEgressAllowlistRelPath,
	},
	{
		kind:       kindRegexPresent,
		regex:      `unset PERL5OPT PERL5LIB PERLLIB PERL_MB_OPT PERL_MM_OPT`,
		message:    "Expected scripts/colima-egress-allowlist.sh to scrub hostile Perl environment before host tool lookup",
		targetFile: colimaEgressAllowlistRelPath,
	},
	{
		// `DYLD_\*` escapes the `*`, matching the literal `DYLD_*`.
		kind:       kindRegexPresent,
		regex:      `DYLD_\*`,
		message:    "Expected scripts/colima-egress-allowlist.sh to scrub DYLD_* variables before host tool lookup",
		targetFile: colimaEgressAllowlistRelPath,
	},
	{
		kind:       kindRegexPresent,
		regex:      `is_trusted_host_tool_path`,
		message:    "Expected scripts/colima-egress-allowlist.sh to canonicalize and trust-check host tool paths",
		targetFile: colimaEgressAllowlistRelPath,
	},
	// Guard: colima-egress-allowlist.sh invokes Go runtime helpers under a
	// scrubbed environment with explicit Go caches (five ordered `! rg -q`
	// probes sharing one message).
	{
		kind:       kindRegexPresent,
		regex:      `run_clean_repo_command env`,
		message:    "Expected scripts/colima-egress-allowlist.sh to invoke Go runtime helpers under a scrubbed environment with explicit Go caches",
		targetFile: colimaEgressAllowlistRelPath,
	},
	{
		kind:       kindRegexPresent,
		regex:      `GOPATH="\$\{GOPATH\}"`,
		message:    "Expected scripts/colima-egress-allowlist.sh to invoke Go runtime helpers under a scrubbed environment with explicit Go caches",
		targetFile: colimaEgressAllowlistRelPath,
	},
	{
		kind:       kindRegexPresent,
		regex:      `GOMODCACHE="\$\{GOMODCACHE\}"`,
		message:    "Expected scripts/colima-egress-allowlist.sh to invoke Go runtime helpers under a scrubbed environment with explicit Go caches",
		targetFile: colimaEgressAllowlistRelPath,
	},
	{
		kind:       kindRegexPresent,
		regex:      `GOCACHE="\$\{GOCACHE\}"`,
		message:    "Expected scripts/colima-egress-allowlist.sh to invoke Go runtime helpers under a scrubbed environment with explicit Go caches",
		targetFile: colimaEgressAllowlistRelPath,
	},
	{
		kind:       kindRegexPresent,
		regex:      `"\$\{GO_BIN\}" run ./cmd/workcell-runtimeutil "\$@"`,
		message:    "Expected scripts/colima-egress-allowlist.sh to invoke Go runtime helpers under a scrubbed environment with explicit Go caches",
		targetFile: colimaEgressAllowlistRelPath,
	},
}

// CheckHostutilEgressRg runs the twenty-one hostutil / entrypoint /
// colima-egress `rg` invariants against the repo rooted at rootDir, in the
// shell's original order.  It returns nil when every invariant holds (the
// shell's exit 0), or an error whose message equals the shell's stderr for the
// first violated invariant (the shell's exit 1).
func CheckHostutilEgressRg(rootDir string) error {
	return evaluate(rootDir, hostutilEgressRgChecks)
}

// dockerfilePinRelPaths lists, in the shell loop's order, the two Dockerfiles
// whose snapshot-TLS-bootstrap pins and unprivileged-USER default the migrated
// block asserts: runtime/container/Dockerfile then tools/validator/Dockerfile.
// Both shell `for dockerfile in ...; do ...; done` loops iterated exactly this
// fixed pair, so preserving the order keeps first-violation parity.
var dockerfilePinRelPaths = []string{dockerfileRelPath, validatorDockerfileRelPath}

// dockerfilePinSpec pairs one `rg -q` pattern with the tail of the shell's echo
// message (everything after "Expected ${dockerfile} ").  The pinInner guards
// that joined several `! rg -q` probes with `||` under one message are expressed
// as consecutive specs sharing the same messageSuffix, mirroring the shell (any
// missing probe yields the same stderr and exit 1 as the corresponding `if`).
type dockerfilePinSpec struct {
	pattern       string
	messageSuffix string
}

// dockerfilePinSpecs lists the fourteen per-Dockerfile snapshot-TLS-bootstrap
// pin probes in the same order as the former inline `for dockerfile` loop in
// scripts/verify-invariants.sh, so a reviewer can diff the two one-to-one.
//
// Every probe is an `rg -q` regex (line-oriented, matched per line via
// regexMatchesAnyLine for `rg` parity), kept verbatim as a regex: the
// escaped-literal patterns (`debian-bootstrap\.env`,
// `rm -f "\$\{output\}";`, `sleep "\$\(\(attempt \* 5\)\)";`, `\| sha256sum`,
// ...) use the rg regex escapes `\. \$ \{ \} \( \) \* \|` to match the literal
// chars; Go's regexp interprets the same escapes, so each pattern is used
// byte-for-byte.
//
// The last eight specs form the two `||`-joined guards: three
// retry/discard-partial probes sharing one message, then five fail-closed
// download/checksum/dpkg probes sharing another.
var dockerfilePinSpecs = []dockerfilePinSpec{
	{`^COPY --chmod=0444 runtime/container/debian-bootstrap\.env /usr/local/share/workcell/debian-bootstrap\.env$`, "to copy the canonical Debian bootstrap manifest read-only"},
	{`mapfile -t debian_bootstrap_pins < /usr/local/share/workcell/debian-bootstrap\.env`, "to parse the Debian bootstrap manifest without shell evaluation"},
	{`\[\[ "\$\{#debian_bootstrap_pins\[@\]\}" -eq 7 \]\]`, "to require exactly seven Debian bootstrap manifest records"},
	{`Acquire::Retries "5";`, "to pin apt retry count for snapshot fetch resilience"},
	{`Acquire::http::Timeout "30";`, "to pin apt HTTP timeout for snapshot fetch resilience"},
	{`Acquire::https::Timeout "30";`, "to pin apt HTTPS timeout for snapshot fetch resilience"},
	{`for attempt in 1 2 3 4 5 6 7 8; do`, "snapshot TLS bootstrap downloads to retry and discard partial packages"},
	{`rm -f "\$\{output\}";`, "snapshot TLS bootstrap downloads to retry and discard partial packages"},
	{`sleep "\$\(\(attempt \* 5\)\)";`, "snapshot TLS bootstrap downloads to retry and discard partial packages"},
	{`fetch_snapshot_bootstrap_package "\$\{openssl_url\}" /tmp/workcell-bootstrap-openssl\.deb`, "snapshot TLS bootstrap to fail closed across download, checksum, and dpkg steps"},
	{`&& echo "\$\{openssl_sha256\}  /tmp/workcell-bootstrap-openssl\.deb" \| sha256sum -c -`, "snapshot TLS bootstrap to fail closed across download, checksum, and dpkg steps"},
	{`&& fetch_snapshot_bootstrap_package "\$\{ca_url\}" /tmp/workcell-bootstrap-ca-certificates\.deb`, "snapshot TLS bootstrap to fail closed across download, checksum, and dpkg steps"},
	{`&& echo "\$\{ca_sha256\}  /tmp/workcell-bootstrap-ca-certificates\.deb" \| sha256sum -c -`, "snapshot TLS bootstrap to fail closed across download, checksum, and dpkg steps"},
	{`&& dpkg -i /tmp/workcell-bootstrap-openssl\.deb /tmp/workcell-bootstrap-ca-certificates\.deb`, "snapshot TLS bootstrap to fail closed across download, checksum, and dpkg steps"},
}

// dockerfilePinsChecks builds the thirty dockerfile-pin invariants for the repo
// rooted at rootDir, in the shell's original order: the fourteen
// snapshot-TLS-bootstrap pins for each Dockerfile (first loop, both Dockerfiles)
// followed by the unprivileged-USER default for each Dockerfile (second loop,
// both Dockerfiles).
//
// The shell echoes interpolated the loop variable ${dockerfile}, whose value was
// the array element "${ROOT_DIR}/<relpath>" — i.e. the ABSOLUTE path once
// ${ROOT_DIR} expands.  Each message is therefore constructed dynamically here as
// "Expected " + rootDir + "/" + relpath + " " + suffix, using literal string
// concatenation (not filepath.Join) to reproduce the shell's byte-exact
// rendering.  The read target stays the repo-relative path via targetFile, so
// evaluate reads the same file the message names.
func dockerfilePinsChecks(rootDir string) []check {
	cs := make([]check, 0, len(dockerfilePinRelPaths)*(len(dockerfilePinSpecs)+1))
	for _, rel := range dockerfilePinRelPaths {
		df := rootDir + "/" + rel
		for _, spec := range dockerfilePinSpecs {
			cs = append(cs, check{
				kind:       kindRegexPresent,
				regex:      spec.pattern,
				message:    "Expected " + df + " " + spec.messageSuffix,
				targetFile: rel,
			})
		}
	}
	for _, rel := range dockerfilePinRelPaths {
		df := rootDir + "/" + rel
		cs = append(cs, check{
			kind:       kindRegexPresent,
			regex:      `^USER workcell$`,
			message:    "Expected " + df + " to default to the named unprivileged workcell user",
			targetFile: rel,
		})
	}
	return cs
}

// CheckDockerfilePins runs the thirty dockerfile-pin invariants against the repo
// rooted at rootDir, in the shell's original order.  It returns nil when every
// invariant holds (the shell's exit 0), or an error whose message equals the
// shell's stderr for the first violated invariant (the shell's exit 1).
func CheckDockerfilePins(rootDir string) error {
	return evaluate(rootDir, dockerfilePinsChecks(rootDir))
}

// validatorEnvPinNeedles lists, in the shell for-loop's order, the six ENV pins
// the validator Dockerfile must carry so its default nonroot writable state
// lives under /home/workcell.  Each was a fixed `grep -Fq "${required}"` literal
// in the migrated `for required in ...; do` loop.
var validatorEnvPinNeedles = []string{
	"ENV HOME=/home/workcell",
	"ENV XDG_CACHE_HOME=/home/workcell/.cache",
	"ENV GOCACHE=/home/workcell/.cache/go-build",
	"ENV GOMODCACHE=/home/workcell/.cache/go-mod",
	"ENV CARGO_TARGET_DIR=/home/workcell/.cache/cargo-target",
	"ENV TMPDIR=/home/workcell/.tmp",
}

// dispatchSpec pairs one dispatch-loop target file with the fixed `grep -Fq --`
// needle that file must contain.  The shell `for dispatch_check in ...` loop
// carried each pair as a single "FILE:NEEDLE" element split on its first colon;
// preserving the slice order keeps first-violation parity.
type dispatchSpec struct {
	relPath string
	needle  string
}

// validatorDispatchSpecs lists the five dispatch-loop probes in the shell
// loop's order.  Each element's file path was ${ROOT_DIR}/<relPath> (the shell
// split "${dispatch_check%%:*}"), and its needle was the loop's
// "${dispatch_check#*:}"; both scripts/pre-merge.sh entries reuse preMergeRelPath
// with distinct needles, mirroring the two pre-merge dispatch probes.
var validatorDispatchSpecs = []dispatchSpec{
	{ciWorkflowRelPath, "./scripts/ci/job-validate.sh --profile pr-parity"},
	{docsWorkflowRelPath, "./scripts/ci/job-docs.sh"},
	{mutationWorkflowRelPath, "./scripts/ci/job-mutation.sh"},
	{preMergeRelPath, "scripts/ci/job-validate.sh"},
	{preMergeRelPath, "scripts/ci/job-docs.sh"},
}

// validatorDispatchLoopsChecks builds the thirteen validator-dispatch invariants
// for the repo rooted at rootDir, in the shell's original order: the six
// validator-Dockerfile ENV-pin probes, the two validate-repo Cargo-target
// externalization probes, then the five CI-dispatch probes.
//
// Two of the three migrated blocks echoed the loop's file variable, whose value
// was ${ROOT_DIR}/<relpath> — i.e. the ABSOLUTE path once ${ROOT_DIR} expands.
// Those messages are therefore constructed dynamically as
// "Expected " + rootDir + "/" + relpath + " ...", using literal string
// concatenation (not filepath.Join) to reproduce the shell's byte-exact
// rendering.  The read target stays the repo-relative path via targetFile, so
// evaluate reads the same file the message names.
//
// The validate-repo probe was one shell `if` guarding two `! grep -Fq` probes
// joined by `||` under a single fixed message; it is expressed here as two
// ordered kindPresent checks sharing that message, which is behaviourally
// identical (either missing probe yields the same stderr and exit 1).
func validatorDispatchLoopsChecks(rootDir string) []check {
	cs := make([]check, 0, len(validatorEnvPinNeedles)+2+len(validatorDispatchSpecs))

	validatorDF := rootDir + "/" + validatorDockerfileRelPath
	for _, needle := range validatorEnvPinNeedles {
		cs = append(cs, check{
			kind:       kindPresent,
			pattern:    needle,
			message:    "Expected " + validatorDF + " to pin its default nonroot writable state under /home/workcell (" + needle + ")",
			targetFile: validatorDockerfileRelPath,
		})
	}

	const validateRepoMsg = "Expected scripts/validate-repo.sh to externalize Cargo target writes under the Workcell-owned validation cache"
	cs = append(cs,
		check{
			kind:       kindPresent,
			pattern:    `WORKCELL_VALIDATE_CACHE_HOME="${WORKCELL_VALIDATE_CACHE_HOME:-${XDG_CACHE_HOME}/workcell/validate}"`,
			message:    validateRepoMsg,
			targetFile: validateRepoRelPath,
		},
		check{
			kind:       kindPresent,
			pattern:    `CARGO_TARGET_DIR="${CARGO_TARGET_DIR:-${WORKCELL_VALIDATE_CACHE_HOME}/cargo-target}"`,
			message:    validateRepoMsg,
			targetFile: validateRepoRelPath,
		},
	)

	for _, spec := range validatorDispatchSpecs {
		df := rootDir + "/" + spec.relPath
		cs = append(cs, check{
			kind:       kindPresent,
			pattern:    spec.needle,
			message:    "Expected " + df + " to dispatch validator parity through the shared CI entrypoints (" + spec.needle + ")",
			targetFile: spec.relPath,
		})
	}

	return cs
}

// CheckValidatorDispatchLoops runs the thirteen validator-dispatch invariants
// against the repo rooted at rootDir, in the shell's original order.  It returns
// nil when every invariant holds (the shell's exit 0), or an error whose message
// equals the shell's stderr for the first violated invariant (the shell's exit
// 1).
func CheckValidatorDispatchLoops(rootDir string) error {
	return evaluate(rootDir, validatorDispatchLoopsChecks(rootDir))
}

// callerRequiredContractsCallers lists, in the shell outer for-loop's order, the
// five caller files that must each launch validator work under an explicit
// caller UID/GID with isolated writable state.  The shell iterated
// `for caller in "${ROOT_DIR}/<path>" ...`, so each caller's value was the
// ABSOLUTE ${ROOT_DIR}/<relPath>; the repo-relative path here is both the
// per-check read target (via targetFile) and the tail the message reconstructs.
var callerRequiredContractsCallers = []string{
	runValidateInValidatorRelPath,
	runDocsInValidatorRelPath,
	runMutationInValidatorRelPath,
	jobValidateRelPath,
	releaseWorkflowRelPath,
}

// callerRequiredContractsNeedles lists, in the shell inner for-loop's order, the
// ten fixed strings each caller must contain.  Each was a fixed
// `grep -Fq -- "${required}"` literal in the migrated `for required in ...; do`
// loop; the shell's `\"`/`\$`/`\${` escapes render to the literal `"`/`$`/`${`
// bytes captured here as Go raw strings.
var callerRequiredContractsNeedles = []string{
	`validator_uid="$(id -u)"`,
	`validator_gid="$(id -g)"`,
	`--user "${validator_uid}:${validator_gid}"`,
	`-e HOME="${validator_home}"`,
	`-e XDG_CACHE_HOME="${validator_cache}"`,
	`-e GOCACHE="${validator_cache}/go-build"`,
	`-e GOMODCACHE="${validator_cache}/go-mod"`,
	`-e CARGO_TARGET_DIR="${validator_cache}/cargo-target"`,
	`-e TMPDIR="${validator_tmp}"`,
	`mkdir -p "${HOME}" "${XDG_CACHE_HOME}" "${GOCACHE}" "${GOMODCACHE}" "${CARGO_TARGET_DIR}" "${TMPDIR}"`,
}

// callerRequiredContractsChecks builds the fifty caller-required invariants for
// the repo rooted at rootDir by iterating the caller list (outer) over the
// required-needle list (inner), exactly mirroring the shell's nested
// `for caller in ...; do for required in ...; do` order so the first violated
// (caller, required) pair matches the shell's first-failure exit.
//
// The shell echoed `${caller}`, whose value was ${ROOT_DIR}/<relPath> — i.e. the
// ABSOLUTE path once ${ROOT_DIR} expands.  The message is therefore constructed
// as "Expected " + rootDir + "/" + relPath + " ... (" + needle + ")" using
// literal string concatenation (not filepath.Join) to reproduce the shell's
// byte-exact rendering, while the read target stays the repo-relative path via
// targetFile so evaluate reads the same file the message names.
func callerRequiredContractsChecks(rootDir string) []check {
	cs := make([]check, 0, len(callerRequiredContractsCallers)*len(callerRequiredContractsNeedles))
	for _, rel := range callerRequiredContractsCallers {
		caller := rootDir + "/" + rel
		for _, needle := range callerRequiredContractsNeedles {
			cs = append(cs, check{
				kind:       kindPresent,
				pattern:    needle,
				message:    "Expected " + caller + " to launch validator work under an explicit caller UID/GID with isolated writable state (" + needle + ")",
				targetFile: rel,
			})
		}
	}
	return cs
}

// CheckCallerRequiredContracts runs the fifty caller-required invariants against
// the repo rooted at rootDir, in the shell's original caller-outer/required-inner
// order.  It returns nil when every invariant holds (the shell's exit 0), or an
// error whose message equals the shell's stderr for the first violated (caller,
// required) pair (the shell's exit 1).
func CheckCallerRequiredContracts(rootDir string) error {
	return evaluate(rootDir, callerRequiredContractsChecks(rootDir))
}

// fnBlockGoBlockGitEnvGitEnvVars lists the three git object-store environment
// variables the in-container git shim must block, in the shell loop's original
// order (`for _git_env_var in GIT_OBJECT_DIRECTORY
// GIT_ALTERNATE_OBJECT_DIRECTORIES GIT_INDEX_FILE`).
var fnBlockGoBlockGitEnvGitEnvVars = []string{
	"GIT_OBJECT_DIRECTORY",
	"GIT_ALTERNATE_OBJECT_DIRECTORIES",
	"GIT_INDEX_FILE",
}

// fnBlockGoBlockGitEnvChecks lists the six fnblock/goblock/gitenv invariants
// migrated out of scripts/verify-invariants.sh, in the shell's original order:
// two bash-function-block regex probes (scripts/workcell), one Go-function-block
// fixed-string probe (publishpr.ResolveExistingExecutableOrDie), and the three
// git-env object-store-redirection pins (runtime/container/bin/git).  These are
// exactly the checks that ran BEFORE the go_verify_citools workcell-git-index-shadow
// call; the validate-repo.sh venv-prune and go-run-env.sh buildvcs greps that
// ran AFTER it remain inline in the shell to preserve first-failure order.  The
// git-env pins reproduce the shell loop that built each needle with
// `printf -v _git_env_literal '"${%s:-}"' "${_git_env_var}"`, i.e. the literal
// `"${VAR:-}"`, and interpolated ${_git_env_var} into the message.
func fnBlockGoBlockGitEnvChecks() []check {
	cs := []check{
		{
			// kindFunctionBlockRegex (function_block_contains_regex): the
			// pattern is a plain identifier with no regex metacharacters, so
			// `grep -q` reduces to fixed-string containment within the bash
			// validate_colima_profile block of scripts/workcell.
			kind:         kindFunctionBlockRegex,
			functionName: "validate_colima_profile",
			regex:        "validate_colima_profile_config",
			message:      "Expected validate_colima_profile to re-check the managed Colima config before reusing a running profile",
		},
		{
			kind:         kindFunctionBlockRegex,
			functionName: "git_alias_value_is_blocked",
			regex:        "git_commit_short_arg_is_no_verify",
			message:      "Expected git_alias_value_is_blocked to reuse the precise short-option no-verify parser",
		},
		{
			// kindGoFunctionBlock (go_function_block_contains_fixed): scope the
			// fixed needle to the Go ResolveExistingExecutableOrDie body so the
			// same text in an unrelated helper or comment cannot satisfy it.
			kind:         kindGoFunctionBlock,
			functionName: "ResolveExistingExecutableOrDie",
			pattern:      `!IsTrustedHostToolPath(rawPath, ctx) || !IsTrustedHostToolPath(canonical, ctx)`,
			message:      "Expected publishpr.ResolveExistingExecutableOrDie to reject untrusted host executable paths",
			targetFile:   hostExecRelPath,
		},
	}
	for _, envVar := range fnBlockGoBlockGitEnvGitEnvVars {
		cs = append(cs, check{
			kind:       kindPresent,
			pattern:    `"${` + envVar + `:-}"`,
			message:    "Expected runtime/container/bin/git to block " + envVar + " to prevent object-store redirection",
			targetFile: containerBinGitRelPath,
		})
	}
	return cs
}

// CheckFnBlockGoBlockGitEnv runs the six fnblock/goblock/gitenv invariants
// against the repo rooted at rootDir, in the shell's original order.  It returns
// nil when every invariant holds (the shell's exit 0), or an error whose message
// equals the shell's stderr for the first violated invariant (the shell's
// exit 1).
func CheckFnBlockGoBlockGitEnv(rootDir string) error {
	return evaluate(rootDir, fnBlockGoBlockGitEnvChecks())
}

// jobDocsRelPath is the repo-relative path to the docs CI lane.  Only the
// buildx-builder-trust validator-image-cleanup invariant reads this file (via
// the per-check targetFile field), mirroring the shell `rg -q` that ran against
// ${ROOT_DIR}/scripts/ci/job-docs.sh.
const jobDocsRelPath = "scripts/ci/job-docs.sh"

// goRunEnvRelPath is the repo-relative path to the Go run-env helper.  Only the
// doc-scan-go-vcs Go-VCS-stamping invariant reads this file (via the per-check
// targetFile field), mirroring the shell `grep -Fq` that ran against
// ${ROOT_DIR}/scripts/lib/go-run-env.sh.
const goRunEnvRelPath = "scripts/lib/go-run-env.sh"

// buildxBuilderTrustChecks lists the eight buildx-builder-trust invariants in
// the same order as the former inline block in scripts/verify-invariants.sh
// (the block between the buildx_cmd trusted-plugin loop and the
// colima-egress-allowlist COLIMA_HOME pin check that immediately precedes the
// already-migrated workcell-bootstrap-egress call), so a reviewer can diff the
// two one-to-one.
//
// Every probe was an affirmative whole-file `rg -q` and every rg pattern is
// metacharacter-free after unescaping (the trusted-docker-client probes escape
// `\( \)` and `\$ \{ \}`; the others carry no active metacharacters), so each
// reduces to fixed-string containment (kindPresent).
//
// The validator-image-cleanup guard was one shell `if` joining three `rg -q`
// probes with `||` under a single message (WORKCELL_KEEP_VALIDATOR_IMAGE in
// build-and-test.sh, cleanup_workcell_validator_image in job-validate.sh and
// job-docs.sh); it is expressed here as three ordered kindPresent checks sharing
// that message, which is behaviourally identical (any missing probe yields the
// same stderr and exit 1).  Each check reads its own target file via the
// per-check targetFile field.
var buildxBuilderTrustChecks = []check{
	{
		kind:       kindPresent,
		pattern:    `BUILDX_BUILDER="workcell-release-`,
		message:    "Expected verify-release-bundle.sh to choose a deterministic context-scoped Buildx builder by default",
		targetFile: verifyReleaseBundleRelPath,
	},
	{
		// kindPresent (first probe of the validator-image-cleanup guard).
		kind:       kindPresent,
		pattern:    "WORKCELL_KEEP_VALIDATOR_IMAGE",
		message:    "Expected local validator lanes to remove disposable validator images unless explicitly retained",
		targetFile: buildAndTestRelPath,
	},
	{
		// kindPresent (second probe): shares the guard's message.
		kind:       kindPresent,
		pattern:    "cleanup_workcell_validator_image",
		message:    "Expected local validator lanes to remove disposable validator images unless explicitly retained",
		targetFile: jobValidateRelPath,
	},
	{
		// kindPresent (third probe): shares the guard's message.
		kind:       kindPresent,
		pattern:    "cleanup_workcell_validator_image",
		message:    "Expected local validator lanes to remove disposable validator images unless explicitly retained",
		targetFile: jobDocsRelPath,
	},
	{
		kind:       kindPresent,
		pattern:    "WORKCELL_REPRO_OWNS_BUILDER",
		message:    "Expected reproducible-build validation to remove its default Workcell-owned Buildx builder on exit",
		targetFile: verifyReproducibleBuildRelPath,
	},
	{
		// kindPresent: the shell's `buildx_expected_endpoints\(\)` escaped its
		// parens, so it is fixed-string containment of the literal
		// `buildx_expected_endpoints()`.
		kind:       kindPresent,
		pattern:    "buildx_expected_endpoints()",
		message:    "Expected trusted-docker-client.sh to compute accepted Buildx endpoints from the active Docker context or host",
		targetFile: trustedDockerClientRelPath,
	},
	{
		// kindPresent: the shell's
		// `docker context inspect "\$\{DOCKER_CONTEXT_NAME\}" --format` escaped
		// every metacharacter, so it is fixed-string containment of the literal
		// invocation.
		kind:       kindPresent,
		pattern:    `docker context inspect "${DOCKER_CONTEXT_NAME}" --format`,
		message:    "Expected trusted-docker-client.sh to resolve Docker context host URIs when validating existing Buildx builders",
		targetFile: trustedDockerClientRelPath,
	},
	{
		// kindPresent: the shell's `COLIMA_HOME="\$\{colima_home\}"` escaped every
		// metacharacter, so it is fixed-string containment of the literal
		// assignment.
		kind:       kindPresent,
		pattern:    `COLIMA_HOME="${colima_home}"`,
		message:    "Expected scripts/colima-egress-allowlist.sh to pin COLIMA_HOME while operating on Lima state",
		targetFile: colimaEgressAllowlistRelPath,
	},
}

// CheckBuildxBuilderTrust runs the eight buildx-builder-trust invariants against
// the repo rooted at rootDir, in the shell's original order.  It returns nil when
// every invariant holds (the shell's exit 0), or an error whose message equals
// the shell's stderr for the first violated invariant (the shell's exit 1).
func CheckBuildxBuilderTrust(rootDir string) error {
	return evaluate(rootDir, buildxBuilderTrustChecks)
}

// docScanGoVcsChecks lists the two doc-scan / Go-VCS-stamping invariants in the
// same order as the contiguous inline pair in scripts/verify-invariants.sh (the
// two grep -Fq probes between the already-migrated workcell-git-index-shadow call
// and the go_cache_root ensure_go_run_env exec block).  Only this contiguous pair
// is migrated: the go_cache_root exec/`case $(uname -s)` block and the
// publishpr.ValidateBaseName go_function_block probe that follow it stay inline in
// the shell to preserve first-failure order (the exec block is not a simple
// grep/rg check, so bundling across it would not be byte-exact).
//
// Both probes were fixed-string `grep -Fq` (the venv-prune needle carries a `${}`
// the shell passed literally; the go-vcs needle escaped its `"` and `$`), so each
// is kindPresent.  Each reads its own target file via the per-check targetFile
// field.
var docScanGoVcsChecks = []check{
	{
		kind:       kindPresent,
		pattern:    `-path "${ROOT_DIR}/.venv" -prune -o`,
		message:    "Expected validate-repo.sh to prune repo-local virtualenv content from documentation scans",
		targetFile: validateRepoRelPath,
	},
	{
		kind:       kindPresent,
		pattern:    `build -buildvcs=false -o "${output_path}"`,
		message:    "Expected build_go_tool_in_repo to disable Go VCS stamping in untrusted repos",
		targetFile: goRunEnvRelPath,
	},
}

// CheckDocScanGoVcs runs the two doc-scan / Go-VCS-stamping invariants against
// the repo rooted at rootDir, in the shell's original order.  It returns nil when
// every invariant holds (the shell's exit 0), or an error whose message equals
// the shell's stderr for the first violated invariant (the shell's exit 1).
func CheckDocScanGoVcs(rootDir string) error {
	return evaluate(rootDir, docScanGoVcsChecks)
}

// smokeChownTarChecks lists the three container-smoke chown/tar invariants in the
// same order as the former inline block in scripts/verify-invariants.sh (the
// block between the container-smoke --self-entrypoint-probe exec fixture and the
// container-smoke --self-test-host-path-hardening exec fixture), so a reviewer can
// diff the two one-to-one.
//
// All three were NEGATED `if rg -q PATTERN; then ... exit 1` guards (a match is a
// violation), and every rg pattern is metacharacter-free after unescaping (the
// chown and tar-create probes escape `\$ \{ \}`; `tar -xf -` carries no active
// metacharacters), so each reduces to negated fixed-string containment
// (kindAbsent).  All three read scripts/container-smoke.sh via the per-check
// targetFile field.
var smokeChownTarChecks = []check{
	{
		kind:       kindAbsent,
		pattern:    `chown -R "${HOST_UID}:${HOST_GID}" "${target_path}"`,
		message:    "Expected scripts/container-smoke.sh to avoid raw recursive chown on host-managed paths",
		targetFile: containerSmokeRelPath,
	},
	{
		kind:       kindAbsent,
		pattern:    `tar --null -T "${path_list_filtered}" -cf -`,
		message:    "Expected scripts/container-smoke.sh to avoid tar-based smoke workspace staging",
		targetFile: containerSmokeRelPath,
	},
	{
		kind:       kindAbsent,
		pattern:    "tar -xf -",
		message:    "Expected scripts/container-smoke.sh to avoid tar-based extraction for smoke workspace staging",
		targetFile: containerSmokeRelPath,
	},
}

// CheckSmokeChownTar runs the three container-smoke chown/tar invariants against
// the repo rooted at rootDir, in the shell's original order.  It returns nil when
// every invariant holds (the shell's exit 0), or an error whose message equals
// the shell's stderr for the first violated invariant (the shell's exit 1).
func CheckSmokeChownTar(rootDir string) error {
	return evaluate(rootDir, smokeChownTarChecks)
}

// dualStackApplyPlanChecks lists the eight dual-stack allowlist-apply-plan
// invariants in the same order as the contiguous inline run in
// scripts/verify-invariants.sh (the run between the bracketed-IPv6-literal
// egress-plan exec probes and the run_in_vm awk-ordering block).  The eighth
// check is the former NEGATED render_allowlist_apply_plan clear_rules
// function-block-regex guard, migrated via kindFunctionBlockRegexAbsent now that
// the kind exists; only the run_in_vm awk-ordering block after it stays inline
// (it is not a simple grep/rg check), preserving first-failure order.
//
// All seven read scripts/colima-egress-allowlist.sh via the per-check targetFile
// field.  Matching semantics mirror the shell exactly:
//   - The two whole-file affirmative `rg -q` probes escaped every metacharacter
//     (`run_in_vm "\$\(render_allowlist_apply_plan\)"`) or carried none
//     (`if ! type ip6tables >/dev/null 2>&1; then`), so they are fixed-string
//     containment (kindPresent).
//   - The render_clear_plan definition probe anchored `^render_clear_plan\(\)`
//     to a line start with escaped parens, so it is a genuine (line-anchored)
//     regex (kindRegexPresent) whose `\(\)` matches literal `()`.
//   - The three affirmative function_block_contains_regex probes scope to
//     render_allowlist_apply_plan (kindFunctionBlockRegex); their patterns
//     (render_clear_plan, resolve_vm_endpoint_ips, getent ahosts) are
//     metacharacter-free, so they behave like fixed-string containment today but
//     keep genuine-regex semantics for parity with `grep -q`.
//   - The NEGATED function_block_contains_regex probe for render_allowlist_plan
//     has a metacharacter-free pattern, so its `grep -q` reduces to fixed-string
//     containment; a match inside the block is a violation, expressed as
//     kindFunctionBlockAbsent.
var dualStackApplyPlanChecks = []check{
	{
		kind:       kindPresent,
		pattern:    `run_in_vm "$(render_allowlist_apply_plan)"`,
		message:    "Expected dual-stack allowlist apply path to use the guarded apply plan",
		targetFile: colimaEgressAllowlistRelPath,
	},
	{
		kind:       kindPresent,
		pattern:    "if ! type ip6tables >/dev/null 2>&1; then",
		message:    "Expected dual-stack allowlist apply plan to preflight ip6tables before rewriting rules",
		targetFile: colimaEgressAllowlistRelPath,
	},
	{
		// kindRegexPresent: the shell's line-anchored `^render_clear_plan\(\)`
		// keeps its `^` anchor; regexMatchesAnyLine anchors it to each line's
		// start (rg's line-oriented default), and `\(\)` matches literal `()`.
		kind:       kindRegexPresent,
		regex:      `^render_clear_plan\(\)`,
		message:    "Expected dual-stack allowlist helper to render clear rules in the VM apply plan",
		targetFile: colimaEgressAllowlistRelPath,
	},
	{
		kind:         kindFunctionBlockRegex,
		functionName: "render_allowlist_apply_plan",
		regex:        "render_clear_plan",
		message:      "Expected dual-stack allowlist apply plan to include render_clear_plan",
		targetFile:   colimaEgressAllowlistRelPath,
	},
	{
		kind:         kindFunctionBlockRegex,
		functionName: "render_allowlist_apply_plan",
		regex:        "resolve_vm_endpoint_ips",
		message:      "Expected dual-stack allowlist apply plan to resolve hostnames inside the VM before applying rules",
		targetFile:   colimaEgressAllowlistRelPath,
	},
	{
		kind:         kindFunctionBlockRegex,
		functionName: "render_allowlist_apply_plan",
		regex:        "getent ahosts",
		message:      "Expected dual-stack allowlist apply plan to use VM DNS results for hostname endpoints",
		targetFile:   colimaEgressAllowlistRelPath,
	},
	{
		// kindFunctionBlockAbsent: the shell's negated
		// function_block_contains_regex has a metacharacter-free pattern, so
		// `grep -q` reduces to fixed-string containment; a match inside the
		// render_allowlist_apply_plan block is a violation.  render_allowlist_plan
		// is not a substring of the block's render_allowlist_apply_plan opening
		// line, so the block name cannot false-match.
		kind:         kindFunctionBlockAbsent,
		functionName: "render_allowlist_apply_plan",
		pattern:      "render_allowlist_plan",
		message:      "Expected dual-stack allowlist apply plan to avoid host-resolved endpoint rules",
		targetFile:   colimaEgressAllowlistRelPath,
	},
	{
		// kindFunctionBlockRegexAbsent: the shell's negated
		// function_block_contains_regex tested the render_allowlist_apply_plan
		// block for a bare `clear_rules` line via the GENUINE regex
		// `^[[:space:]]*clear_rules$` (a line that is only optional leading
		// whitespace followed by `clear_rules`); a match inside the block is a
		// violation.  render_clear_plan and render_allowlist_apply_plan do not
		// match the anchored pattern, so neither the in-block clear-plan call nor
		// the block's own opening line can false-match.
		kind:         kindFunctionBlockRegexAbsent,
		functionName: "render_allowlist_apply_plan",
		regex:        `^[[:space:]]*clear_rules$`,
		message:      "Expected dual-stack allowlist apply plan to avoid invoking clear_rules during render",
		targetFile:   colimaEgressAllowlistRelPath,
	},
}

// CheckDualStackApplyPlan runs the eight dual-stack allowlist-apply-plan
// invariants against the repo rooted at rootDir, in the shell's original order.
// It returns nil when every invariant holds (the shell's exit 0), or an error
// whose message equals the shell's stderr for the first violated invariant (the
// shell's exit 1).
func CheckDualStackApplyPlan(rootDir string) error {
	return evaluate(rootDir, dualStackApplyPlanChecks)
}

// publishBaseRefcheckChecks holds the single publish-pr base-name invariant
// migrated out of scripts/verify-invariants.sh (the lone
// go_function_block_contains_fixed probe sitting between the ensure_go_run_env
// cache-root exec block and the workcell-publish-pr-shadow group).  It mirrors
// the shell's go_function_block_contains_fixed helper (awk function-body
// extraction with comment stripping, then `grep -Fq`), scoping the fixed-string
// containment to internal/publishpr/publish_pr_main.go's ValidateBaseName body
// so the needle cannot be satisfied by unrelated text or a comment.
var publishBaseRefcheckChecks = []check{
	{
		kind:         kindGoFunctionBlock,
		functionName: "ValidateBaseName",
		pattern:      "!checkRefFormat(base)",
		message:      "Expected publishpr.ValidateBaseName to validate the publish-pr --base branch name through checkRefFormat",
		targetFile:   publishPrMainRelPath,
	},
}

// CheckPublishBaseRefcheck runs the single publish-pr base-name invariant
// against the repo rooted at rootDir.  It returns nil when the invariant holds
// (the shell's exit 0), or an error whose message equals the shell's stderr for
// the violated invariant (the shell's exit 1).
func CheckPublishBaseRefcheck(rootDir string) error {
	return evaluate(rootDir, publishBaseRefcheckChecks)
}

// runtimeSecurityPostureChecks lists the two validate_runtime_security_posture
// invariants in the same order as the contiguous inline pair in
// scripts/verify-invariants.sh (the pair between the go_verify_hostutil
// security-option exec fixtures and the prompt-autonomy dry-run exec block).
// Both migrate function_block_contains_fixed (`grep -Fq` fixed-string
// containment) scoped to the validate_runtime_security_posture body in
// scripts/workcell (the default target), so each is a kindFunctionBlock probe.
var runtimeSecurityPostureChecks = []check{
	{
		kind:         kindFunctionBlock,
		functionName: "validate_runtime_security_posture",
		pattern:      "go_hostutil helper validate-security-options",
		message:      "Expected validate_runtime_security_posture to validate daemon SecurityOptions through the helper subcommand",
	},
	{
		kind:         kindFunctionBlock,
		functionName: "validate_runtime_security_posture",
		pattern:      "go_hostutil helper validate-compat-security-options",
		message:      "Expected validate_runtime_security_posture to validate Docker Desktop compat daemon SecurityOptions through the compat helper subcommand",
	},
}

// CheckRuntimeSecurityPosture runs the two validate_runtime_security_posture
// invariants against the repo rooted at rootDir, in the shell's original order.
// It returns nil when every invariant holds (the shell's exit 0), or an error
// whose message equals the shell's stderr for the first violated invariant (the
// shell's exit 1).
func CheckRuntimeSecurityPosture(rootDir string) error {
	return evaluate(rootDir, runtimeSecurityPostureChecks)
}

// smokeAptBrokerProbeChecks lists the six container-smoke apt-broker slow-wait
// invariants in the same order as the former inline
// `for required in ...; do grep -Fq -- "${required}" container-smoke.sh; done`
// loop in scripts/verify-invariants.sh.  Each iteration was a fixed-string
// presence probe whose stderr interpolated the needle into the message, so each
// message is computed here verbatim as
// "Expected scripts/container-smoke.sh to keep the Linux runtime apt-broker
// slow-wait probe (" + needle + ")".  All six read scripts/container-smoke.sh
// via the per-check targetFile field.
var smokeAptBrokerProbeChecks = func() []check {
	needles := []string{
		"slow_apt_helper=/state/tmp/workcell-slow-apt-helper.sh",
		"/bin/bash /usr/local/libexec/workcell/apt-broker.sh",
		"sudo -n /usr/local/libexec/workcell/apt-helper.sh apt-get update",
		"slow-apt-helper-ok",
		"expected sudo-wrapper to wait for a slow apt broker request by default",
		"expected default apt broker waits to avoid timing out slow requests",
	}
	cs := make([]check, 0, len(needles))
	for _, needle := range needles {
		cs = append(cs, check{
			kind:       kindPresent,
			pattern:    needle,
			message:    "Expected scripts/container-smoke.sh to keep the Linux runtime apt-broker slow-wait probe (" + needle + ")",
			targetFile: containerSmokeRelPath,
		})
	}
	return cs
}()

// CheckSmokeAptBrokerProbe runs the six container-smoke apt-broker slow-wait
// invariants against the repo rooted at rootDir, in the shell's original order.
// It returns nil when every invariant holds (the shell's exit 0), or an error
// whose message equals the shell's stderr for the first violated invariant (the
// shell's exit 1).
func CheckSmokeAptBrokerProbe(rootDir string) error {
	return evaluate(rootDir, smokeAptBrokerProbeChecks)
}

// copilotTokenHandoffCleanupChecks lists the three Copilot token-handoff cleanup
// invariants migrated out of scripts/verify-invariants.sh.  They came from one
// multi-probe `if ! grep -Fq A || ! grep -Fq B || ! grep -Fq C; then ... exit 1`
// guard against internal/host/hoststate/hoststate.go, so they are three ordered
// kindPresent probes sharing the guard's single stderr message; ordering them
// preserves the shell's first-failure (short-circuit) behavior.
var copilotTokenHandoffCleanupChecks = []check{
	{
		kind:       kindPresent,
		pattern:    "copilotStandaloneTokenHandoffName",
		message:    "Expected stale Copilot token handoff directories to be covered by host cleanup",
		targetFile: hoststateRelPath,
	},
	{
		kind:       kindPresent,
		pattern:    "copilotTokenHandoffBundleName",
		message:    "Expected stale Copilot token handoff directories to be covered by host cleanup",
		targetFile: hoststateRelPath,
	},
	{
		kind:       kindPresent,
		pattern:    "removeCopilotTokenHandoffArtifacts",
		message:    "Expected stale Copilot token handoff directories to be covered by host cleanup",
		targetFile: hoststateRelPath,
	},
}

// CheckCopilotTokenHandoffCleanup runs the three Copilot token-handoff cleanup
// invariants against the repo rooted at rootDir, in the shell's original order.
// It returns nil when every invariant holds (the shell's exit 0), or an error
// whose message equals the shell's stderr for the first violated invariant (the
// shell's exit 1).
func CheckCopilotTokenHandoffCleanup(rootDir string) error {
	return evaluate(rootDir, copilotTokenHandoffCleanupChecks)
}

// providerTokenUnlinkChecks holds the single provider-wrapper token-unlink
// invariant migrated out of scripts/verify-invariants.sh (the lone `grep -Fq`
// probe between the workcell-provider-launcher-authority group and the
// workcell-copilot-policy-wrapper group).  The shell needle
// "rm -f -- \"\${token_file}\"" is the literal `rm -f -- "${token_file}"`,
// matched as a fixed string against runtime/container/provider-wrapper.sh.
var providerTokenUnlinkChecks = []check{
	{
		kind:       kindPresent,
		pattern:    `rm -f -- "${token_file}"`,
		message:    "Expected provider wrapper to unlink the runtime Copilot token handoff file before managed exec",
		targetFile: providerWrapperRelPath,
	},
}

// CheckProviderTokenUnlink runs the single provider-wrapper token-unlink
// invariant against the repo rooted at rootDir.  It returns nil when the
// invariant holds (the shell's exit 0), or an error whose message equals the
// shell's stderr for the violated invariant (the shell's exit 1).
func CheckProviderTokenUnlink(rootDir string) error {
	return evaluate(rootDir, providerTokenUnlinkChecks)
}

// validateRepoScenarioRefsChecks lists the three scenario-script reference
// invariants migrated out of scripts/verify-invariants.sh (the former
// `for scenario_script_basename in ...; do grep -Fq -- "${basename}"
// validate-repo.sh; done` loop).  Each iteration was a fixed-string presence
// probe whose stderr interpolated the basename into the message, so each message
// is computed here verbatim as "validate-repo.sh must reference " + basename.
// All three read scripts/validate-repo.sh via the per-check targetFile field.
var validateRepoScenarioRefsChecks = func() []check {
	basenames := []string{
		"run-scenario-tests.sh",
		"verify-scenario-coverage.sh",
		"verify-control-plane-parity.sh",
	}
	cs := make([]check, 0, len(basenames))
	for _, basename := range basenames {
		cs = append(cs, check{
			kind:       kindPresent,
			pattern:    basename,
			message:    "validate-repo.sh must reference " + basename,
			targetFile: validateRepoRelPath,
		})
	}
	return cs
}()

// CheckValidateRepoScenarioRefs runs the three scenario-script reference
// invariants against the repo rooted at rootDir, in the shell's original order.
// It returns nil when every invariant holds (the shell's exit 0), or an error
// whose message equals the shell's stderr for the first violated invariant (the
// shell's exit 1).
func CheckValidateRepoScenarioRefs(rootDir string) error {
	return evaluate(rootDir, validateRepoScenarioRefsChecks)
}

// precommitHookExecChecks holds the single repo pre-commit hook executable
// invariant migrated out of scripts/verify-invariants.sh (the lone
// `if [[ ! -x "${REPO_PRECOMMIT_HOOK}" ]]` guard, where
// REPO_PRECOMMIT_HOOK="${ROOT_DIR}/.githooks/pre-commit").  It is a kindExecutable
// filesystem check that stats rootDir/.githooks/pre-commit; the shell message
// interpolated the absolute hook path (${REPO_PRECOMMIT_HOOK}), so
// messageIncludesTargetPath appends rootDir + "/" + targetPath to reproduce it
// byte-for-byte.
var precommitHookExecChecks = []check{
	{
		kind:                      kindExecutable,
		targetPath:                ".githooks/pre-commit",
		message:                   "Expected executable repo pre-commit hook: ",
		messageIncludesTargetPath: true,
	},
}

// CheckPrecommitHookExec runs the single repo pre-commit hook executable
// invariant against the repo rooted at rootDir.  It returns nil when the hook
// exists and is executable (the shell's exit 0), or an error whose message
// equals the shell's stderr (the shell's exit 1) — the fixed prefix plus the
// absolute hook path.
func CheckPrecommitHookExec(rootDir string) error {
	return evaluate(rootDir, precommitHookExecChecks)
}

// docsExamplesDirChecks holds the single docs/examples directory invariant
// migrated out of scripts/verify-invariants.sh (the lone
// `if [[ ! -d "${ROOT_DIR}/docs/examples" ]]` guard).  It is a kindDirExists
// filesystem check that stats rootDir/docs/examples; the shell message was a
// static "docs/examples/ must exist" with no path interpolation.
var docsExamplesDirChecks = []check{
	{
		kind:       kindDirExists,
		targetPath: "docs/examples",
		message:    "docs/examples/ must exist",
	},
}

// CheckDocsExamplesDir runs the single docs/examples directory invariant against
// the repo rooted at rootDir.  It returns nil when docs/examples/ exists as a
// directory (the shell's exit 0), or an error whose message equals the shell's
// stderr (the shell's exit 1).
func CheckDocsExamplesDir(rootDir string) error {
	return evaluate(rootDir, docsExamplesDirChecks)
}

// scenarioScriptsPresentChecks holds the four contiguous scenario-harness
// filesystem invariants migrated out of scripts/verify-invariants.sh, in the
// shell's original order.  The first is the lone
// `if [[ ! -f "${ROOT_DIR}/tests/scenarios/manifest.json" ]]` guard (a
// kindFileExists check that stats rootDir/tests/scenarios/manifest.json; the
// shell message was a static "tests/scenarios/manifest.json must exist" with no
// path interpolation).  The remaining three are the static
// `for scenario_script in ...; do [[ ! -x "${scenario_script}" ]]; done` loop
// over the three scenario scripts (kindExecutable checks); the shell message
// interpolated the absolute ${scenario_script} (`${ROOT_DIR}/<path>`), so
// messageIncludesTargetPath appends rootDir + "/" + targetPath to reproduce it
// byte-for-byte.  The `jq -e` PreToolUse-hook guard that immediately follows the
// loop in the shell (an array/pipe expression) stays inline in bash, so this
// group is wired only across the four contiguous filesystem checks.
var scenarioScriptsPresentChecks = []check{
	{
		kind:       kindFileExists,
		targetPath: "tests/scenarios/manifest.json",
		message:    "tests/scenarios/manifest.json must exist",
	},
	{
		kind:                      kindExecutable,
		targetPath:                "scripts/run-scenario-tests.sh",
		message:                   "Expected executable scenario script: ",
		messageIncludesTargetPath: true,
	},
	{
		kind:                      kindExecutable,
		targetPath:                "scripts/verify-scenario-coverage.sh",
		message:                   "Expected executable scenario script: ",
		messageIncludesTargetPath: true,
	},
	{
		kind:                      kindExecutable,
		targetPath:                "scripts/verify-control-plane-parity.sh",
		message:                   "Expected executable scenario script: ",
		messageIncludesTargetPath: true,
	},
}

// CheckScenarioScriptsPresent runs the four scenario-harness filesystem
// invariants (the tests/scenarios/manifest.json regular-file check plus the
// three executable scenario-script checks) against the repo rooted at rootDir,
// in the shell's original order.  It returns nil when every invariant holds (the
// shell's exit 0), or an error whose message equals the shell's stderr for the
// first violated invariant (the shell's exit 1) — for the manifest check the
// static message, and for a scenario-script check the fixed prefix plus the
// absolute ${ROOT_DIR}/<path> script path.
func CheckScenarioScriptsPresent(rootDir string) error {
	return evaluate(rootDir, scenarioScriptsPresentChecks)
}

// claudeManagedSettingsRelPath is the repo-relative path to the Claude adapter
// managed-settings file.  The claude-managed-bypass check reads it for its
// bypass-permissions invariant, mirroring the shell `jq -e` probe that ran
// against ${ROOT_DIR}/adapters/claude/managed-settings.json.
const claudeManagedSettingsRelPath = "adapters/claude/managed-settings.json"

// geminiSettingsRelPath is the repo-relative path to the Gemini adapter
// settings file.  The gemini-settings-guards block reads it for its
// folder-trust and interactive-shell invariants, mirroring the shell `jq -e`
// probes that ran against ${ROOT_DIR}/adapters/gemini/.gemini/settings.json.
const geminiSettingsRelPath = "adapters/gemini/.gemini/settings.json"

// CheckClaudeMcpProjectServers runs the single `jq -e` project-MCP-servers
// invariant migrated out of the scripts/verify-invariants.sh settings_path loop
// (the `.enableAllProjectMcpServers == false` guard).  Unlike the other
// migrated groups this takes the settings FILE path directly (the shell iterated
// the loop over ${ROOT_DIR}/adapters/claude/.claude/settings.json and
// ${ROOT_DIR}/adapters/claude/managed-settings.json, calling this once per file
// in place), so the caller passes each path and the per-file `$(basename …)`
// message is preserved by prefixing filepath.Base of the given path.  It returns
// nil when the field is the JSON boolean false (the shell's exit 0), or an error
// whose message equals the shell's stderr for that file (the shell's exit 1),
// including when the file is missing or not valid JSON (jq's non-zero exit under
// the `if !` guard).
func CheckClaudeMcpProjectServers(settingsPath string) error {
	text := ""
	if content, err := os.ReadFile(settingsPath); err == nil {
		text = string(content)
	}
	c := check{
		kind:            kindJSONExprEval,
		jsonPath:        ".enableAllProjectMcpServers",
		jsonExpectedRaw: "false",
	}
	if !c.holds(text, "") {
		return errors.New(filepath.Base(settingsPath) + " settings must disable auto-enabled project MCP servers")
	}
	return nil
}

// CheckClaudeGuardBashHook runs the array-index `jq -e` guard-bash-hook invariant
// migrated out of the scripts/verify-invariants.sh settings_path loop
// (`.hooks.PreToolUse[0].hooks[0].command ==
// "/opt/workcell/adapters/claude/hooks/guard-bash.sh"`).  Like
// CheckClaudeMcpProjectServers it takes the settings FILE path directly (the loop
// calls it once per Claude settings file, in place) and preserves the per-file
// `$(basename …)` message by prefixing filepath.Base of the given path.  It
// returns nil when the first PreToolUse hook command is the managed guard path
// (the shell's exit 0), or an error whose message equals the shell's stderr for
// that file (the shell's exit 1), including when the file is missing or not valid
// JSON, or when the object/array path does not resolve to that string (jq's
// non-zero exit under the `if !` guard).
func CheckClaudeGuardBashHook(settingsPath string) error {
	text := ""
	if content, err := os.ReadFile(settingsPath); err == nil {
		text = string(content)
	}
	c := check{
		kind:            kindJSONExprEval,
		jsonPath:        ".hooks.PreToolUse[0].hooks[0].command",
		jsonExpectedRaw: `"/opt/workcell/adapters/claude/hooks/guard-bash.sh"`,
	}
	if !c.holds(text, "") {
		return errors.New(filepath.Base(settingsPath) + " settings must use the managed guard-bash.sh hook")
	}
	return nil
}

// claudeManagedBypassChecks holds the single Claude managed-settings
// bypass-permissions invariant migrated out of scripts/verify-invariants.sh (the
// `if ! jq -e '.disableBypassPermissionsMode == "allow"'` guard).  It is a
// kindJSONExprEval check whose RHS literal is the JSON string "allow".
var claudeManagedBypassChecks = []check{
	{
		kind:            kindJSONExprEval,
		targetFile:      claudeManagedSettingsRelPath,
		jsonPath:        ".disableBypassPermissionsMode",
		jsonExpectedRaw: `"allow"`,
		message:         "Claude managed settings must allow bypass-permissions mode under the external Workcell boundary",
	},
}

// CheckClaudeManagedBypass runs the single Claude managed-settings
// bypass-permissions invariant against the repo rooted at rootDir.  It returns
// nil when .disableBypassPermissionsMode is the JSON string "allow" (the shell's
// exit 0), or an error whose message equals the shell's stderr (the shell's exit
// 1).
func CheckClaudeManagedBypass(rootDir string) error {
	return evaluate(rootDir, claudeManagedBypassChecks)
}

// geminiSettingsBaselineChecks holds the three contiguous Gemini adapter-settings
// invariants migrated out of scripts/verify-invariants.sh between the
// managed-bypass and folder-trust guards, in the shell's original order:
// `.tools.allowed == []`, `.mcp.allowed == []` (empty-array equality — each holds
// only when the field is a JSON array of length zero), and the
// `.security.auth.selectedType` truthiness probe.  The selectedType guard used the
// INVERTED `if jq -e` polarity (a present/truthy selected auth type is the
// violation, and a null/absent value or a jq error passes), so it is a
// kindJSONPathTruthy check with jsonViolateWhenTruthy set; the two array guards
// reuse kindJSONExprEval with the `[]` literal.
var geminiSettingsBaselineChecks = []check{
	{
		kind:            kindJSONExprEval,
		targetFile:      geminiSettingsRelPath,
		jsonPath:        ".tools.allowed",
		jsonExpectedRaw: "[]",
		message:         "Gemini adapter must not seed allowed tools",
	},
	{
		kind:            kindJSONExprEval,
		targetFile:      geminiSettingsRelPath,
		jsonPath:        ".mcp.allowed",
		jsonExpectedRaw: "[]",
		message:         "Gemini adapter must not seed allowed MCP servers",
	},
	{
		kind:                  kindJSONPathTruthy,
		targetFile:            geminiSettingsRelPath,
		jsonPath:              ".security.auth.selectedType",
		jsonViolateWhenTruthy: true,
		message:               "Gemini adapter baseline must not hardcode a selected auth type",
	},
}

// CheckGeminiSettingsBaseline runs the three Gemini adapter-settings baseline
// invariants (no seeded allowed tools, no seeded allowed MCP servers, no
// hardcoded selected auth type) against the repo rooted at rootDir, in the
// shell's original order.  It returns nil when all three hold (the shell's exit
// 0), or an error whose message equals the shell's stderr for the first violated
// invariant (the shell's exit 1).
func CheckGeminiSettingsBaseline(rootDir string) error {
	return evaluate(rootDir, geminiSettingsBaselineChecks)
}

// geminiSettingsGuardChecks holds the three contiguous Gemini adapter-settings
// invariants migrated out of scripts/verify-invariants.sh (the
// `.security.folderTrust.enabled == false` and
// `.tools.shell.enableInteractiveShell == false` `jq -e` guards, followed by the
// `.advanced.excludedEnvVars | type == "array"` pipe), in the shell's original
// order.  The first two are kindJSONExprEval checks whose RHS literal is the JSON
// boolean false, so each holds only when the field is boolean false — never the
// string "false"; the third is a kindJSONTypeEquals check that holds only when
// the field's jq type is the string "array".
var geminiSettingsGuardChecks = []check{
	{
		kind:            kindJSONExprEval,
		targetFile:      geminiSettingsRelPath,
		jsonPath:        ".security.folderTrust.enabled",
		jsonExpectedRaw: "false",
		message:         "Gemini adapter must disable Gemini folder trust inside the managed runtime",
	},
	{
		kind:            kindJSONExprEval,
		targetFile:      geminiSettingsRelPath,
		jsonPath:        ".tools.shell.enableInteractiveShell",
		jsonExpectedRaw: "false",
		message:         "Gemini adapter must disable interactive shell mode",
	},
	{
		kind:            kindJSONTypeEquals,
		targetFile:      geminiSettingsRelPath,
		jsonPath:        ".advanced.excludedEnvVars",
		jsonExpectedRaw: `"array"`,
		message:         "Gemini adapter must exclude sensitive environment variables",
	},
}

// CheckGeminiSettingsGuards runs the three Gemini adapter-settings invariants
// against the repo rooted at rootDir, in the shell's original order.  It returns
// nil when both boolean fields are the JSON boolean false AND excludedEnvVars is
// a JSON array (the shell's exit 0), or an error whose message equals the shell's
// stderr for the first violated invariant (the shell's exit 1).
func CheckGeminiSettingsGuards(rootDir string) error {
	return evaluate(rootDir, geminiSettingsGuardChecks)
}

// hostGateScriptRelPaths lists, in the HOST_GATE_SCRIPTS array's order, the
// twenty-two host-gate scripts that must carry an absolute privileged Bash
// shebang and self-sanitize their host entrypoint before running release or
// boundary checks.  The shell array elements were "${ROOT_DIR}/<relpath>", so
// each element rendered the ABSOLUTE path once ${ROOT_DIR} expanded; the
// repo-relative form here is both the read target (targetFile) and the tail of
// the reconstructed absolute message path.
var hostGateScriptRelPaths = []string{
	"scripts/build-and-test.sh",
	"scripts/check-pinned-inputs.sh",
	"scripts/check-public-contract.sh",
	"scripts/container-smoke.sh",
	"scripts/generate-build-input-manifest.sh",
	"scripts/generate-control-plane-manifest.sh",
	"scripts/generate-builder-environment-manifest.sh",
	"scripts/generate-release-checksums.sh",
	"scripts/publish-provider-bump-pr.sh",
	"scripts/publish-upstream-refresh-pr.sh",
	"scripts/update-upstream-pins.sh",
	"scripts/update-provider-pins.sh",
	"scripts/publish-github-release.sh",
	"scripts/verify-build-input-manifest.sh",
	"scripts/verify-control-plane-manifest.sh",
	"scripts/verify-github-macos-release-test-runners.sh",
	"scripts/verify-release-bundle.sh",
	"scripts/verify-reproducible-build.sh",
	"scripts/verify-upstream-claude-release.sh",
	"scripts/verify-upstream-codex-release.sh",
	"scripts/verify-upstream-copilot-release.sh",
	"scripts/verify-upstream-gemini-release.sh",
}

// hostGateEntrypointSanitizeChecks builds the forty-four host-gate entrypoint
// invariants for the repo rooted at rootDir, in the shell's original order.  The
// migrated `for script in "${HOST_GATE_SCRIPTS[@]}"` loop body ran two ordered
// checks per iteration, so for each script (in array order) this emits the
// first-line shebang check followed by the entrypoint self-sanitize check,
// preserving the loop's per-iteration first-violation order exactly.
//
//   - The shebang probe was `head -n 1 "${script}" | grep -q '^#!/bin/bash -p$'`,
//     an anchored first-line regex → kindFirstLineRegex with the pattern verbatim
//     (no active metacharacters besides the anchors).
//   - The entrypoint probe was `rg -q 'WORKCELL_SANITIZED_ENTRYPOINT|trusted-entrypoint\.sh'`,
//     a genuine `|` alternation (the `\.` matches a literal dot) → kindRegexPresent
//     with the pattern verbatim, matched per line for rg parity.
//
// The shell echoes interpolated ${script}, whose value was the array element
// "${ROOT_DIR}/<relpath>" — i.e. the ABSOLUTE path once ${ROOT_DIR} expands.  Each
// message is therefore constructed as "Expected " + rootDir + "/" + relpath + " ...",
// using literal string concatenation (not filepath.Join) to reproduce the shell's
// byte-exact rendering.  The read target stays the repo-relative path via
// targetFile, so evaluate reads the same file the message names.
func hostGateEntrypointSanitizeChecks(rootDir string) []check {
	cs := make([]check, 0, len(hostGateScriptRelPaths)*2)
	for _, rel := range hostGateScriptRelPaths {
		script := rootDir + "/" + rel
		cs = append(cs,
			check{
				kind:       kindFirstLineRegex,
				regex:      `^#!/bin/bash -p$`,
				message:    "Expected " + script + " to use an absolute privileged Bash shebang before self-sanitizing its host entrypoint",
				targetFile: rel,
			},
			check{
				kind:       kindRegexPresent,
				regex:      `WORKCELL_SANITIZED_ENTRYPOINT|trusted-entrypoint\.sh`,
				message:    "Expected " + script + " to self-sanitize its host entrypoint before running release or boundary checks",
				targetFile: rel,
			},
		)
	}
	return cs
}

// CheckHostGateEntrypointSanitize runs the forty-four host-gate entrypoint
// invariants (an absolute privileged Bash shebang and an entrypoint
// self-sanitize probe for each of the twenty-two HOST_GATE_SCRIPTS) against the
// repo rooted at rootDir, in the shell's original order.  It returns nil when
// every invariant holds (the shell's exit 0), or an error whose message equals
// the shell's stderr for the first violated invariant (the shell's exit 1).
func CheckHostGateEntrypointSanitize(rootDir string) error {
	return evaluate(rootDir, hostGateEntrypointSanitizeChecks(rootDir))
}

// precommitUpstreamPinGateChecks holds the single repo pre-commit hook invariant
// migrated out of scripts/verify-invariants.sh (the lone
// `if ! rg -q 'scripts/update-upstream-pins\.sh" --check' "${REPO_PRECOMMIT_HOOK}"`
// guard, where REPO_PRECOMMIT_HOOK="${ROOT_DIR}/.githooks/pre-commit").  The
// shell message interpolated no path, so it is a static kindRegexPresent check
// whose pattern's only metacharacter (`\.`) is an escaped literal dot; the
// pattern is kept verbatim and matched per line for rg parity.
var precommitUpstreamPinGateChecks = []check{
	{
		kind:       kindRegexPresent,
		regex:      `scripts/update-upstream-pins\.sh" --check`,
		message:    "Expected repo pre-commit hook to gate commits on pending pinned upstream updates",
		targetFile: ".githooks/pre-commit",
	},
}

// CheckPrecommitUpstreamPinGate runs the single repo pre-commit hook
// upstream-pin-gate invariant against the repo rooted at rootDir.  It returns nil
// when the hook gates commits on pending pinned upstream updates (the shell's
// exit 0), or an error whose message equals the shell's stderr (the shell's exit
// 1).
func CheckPrecommitUpstreamPinGate(rootDir string) error {
	return evaluate(rootDir, precommitUpstreamPinGateChecks)
}

// trustedDockerClientScriptRelPaths lists, in the shell loops' order, the four
// scripts that must source the trusted Docker client helper, seed a trusted
// client state, drop the caller HOME across their sanitized entrypoint re-exec,
// and invoke buildx through the trusted absolute plugin path.  The shell loop
// elements were "${ROOT_DIR}/<relpath>", so each rendered the ABSOLUTE path once
// ${ROOT_DIR} expanded.
var trustedDockerClientScriptRelPaths = []string{
	"scripts/container-smoke.sh",
	"scripts/generate-builder-environment-manifest.sh",
	"scripts/verify-release-bundle.sh",
	"scripts/verify-reproducible-build.sh",
}

// trustedDockerClientRgChecks builds the sixteen trusted-Docker-client
// invariants for the repo rooted at rootDir, in the shell's original order: the
// first `for script` loop's three ordered `! rg -q` probes (source the helper,
// seed trusted client state, drop caller HOME) for each script, followed by the
// second `for script` loop's single `! rg -q 'buildx_cmd '` probe for each
// script.  Splitting into two loops (rather than four probes per script)
// reproduces the shell's first-violation ordering exactly.
//
// Every pattern is kept verbatim as a regex and matched per line for rg parity:
// the source-helper probe escapes `\$ \{ \} \.` to match the literal
// `$ { } .`, while the setup / HOME / buildx probes are metacharacter-free and
// so reduce to fixed-string matching under regexMatchesAnyLine.
//
// The shell echoes interpolated ${script}, whose value was the loop element
// "${ROOT_DIR}/<relpath>" — the ABSOLUTE path once ${ROOT_DIR} expands.  Each
// message is therefore constructed as "Expected " + rootDir + "/" + relpath + " ...",
// using literal string concatenation (not filepath.Join) to reproduce the
// shell's byte-exact rendering.  The read target stays the repo-relative path via
// targetFile, so evaluate reads the same file the message names.
func trustedDockerClientRgChecks(rootDir string) []check {
	cs := make([]check, 0, len(trustedDockerClientScriptRelPaths)*4)
	for _, rel := range trustedDockerClientScriptRelPaths {
		script := rootDir + "/" + rel
		cs = append(cs,
			check{
				kind:       kindRegexPresent,
				regex:      `source "\$\{ROOT_DIR\}/scripts/lib/trusted-docker-client\.sh"`,
				message:    "Expected " + script + " to source the trusted Docker client helper",
				targetFile: rel,
			},
			check{
				kind:       kindRegexPresent,
				regex:      `setup_workcell_trusted_docker_client`,
				message:    "Expected " + script + " to seed a trusted Docker client state before using Docker",
				targetFile: rel,
			},
			check{
				kind:       kindRegexPresent,
				regex:      `HOME=/tmp`,
				message:    "Expected " + script + " to stop preserving caller HOME across its sanitized entrypoint re-exec",
				targetFile: rel,
			},
		)
	}
	for _, rel := range trustedDockerClientScriptRelPaths {
		script := rootDir + "/" + rel
		cs = append(cs, check{
			kind:       kindRegexPresent,
			regex:      `buildx_cmd `,
			message:    "Expected " + script + " to invoke buildx through the trusted absolute plugin path",
			targetFile: rel,
		})
	}
	return cs
}

// CheckTrustedDockerClientRg runs the sixteen trusted-Docker-client invariants
// (source-helper, seed-client-state, drop-caller-HOME, and buildx-trusted-path
// probes across container-smoke.sh, generate-builder-environment-manifest.sh,
// verify-release-bundle.sh and verify-reproducible-build.sh) against the repo
// rooted at rootDir, in the shell's original order.  It returns nil when every
// invariant holds (the shell's exit 0), or an error whose message equals the
// shell's stderr for the first violated invariant (the shell's exit 1).
func CheckTrustedDockerClientRg(rootDir string) error {
	return evaluate(rootDir, trustedDockerClientRgChecks(rootDir))
}

// violationMessage returns the stderr string the shell emitted for a violated
// check.  For most checks this is the static message; filesystem checks that
// interpolated the absolute ${ROOT_DIR}/<path> into their shell message set
// messageIncludesTargetPath so the joined path is appended here (raw
// concatenation of rootDir + "/" + targetPath, mirroring the shell's literal
// "${ROOT_DIR}/<path>" expansion byte-for-byte).
func (c check) violationMessage(rootDir string) string {
	if c.messageIncludesTargetPath {
		return c.message + rootDir + "/" + c.targetPath
	}
	return c.message
}

func (c check) validatePatternFields() error {
	if c.usesRegex() {
		if c.regex == "" || c.pattern != "" {
			return errors.New("internal workcellhardening regex check must set regex and leave pattern empty")
		}
		return nil
	}
	if c.regex != "" {
		return errors.New("internal workcellhardening fixed-string check must not set regex")
	}
	return nil
}

func (c check) usesRegex() bool {
	switch c.kind {
	case kindFirstLineRegex, kindRegexAbsent, kindRegexPresent, kindFunctionBlockRegex, kindFunctionBlockRegexAbsent:
		return true
	default:
		return false
	}
}

// holds reports whether the invariant is satisfied.  Content kinds inspect
// text (the target file's contents); filesystem kinds (kindDirExists /
// kindExecutable / kindFileExists) ignore text and stat rootDir/targetPath
// instead.
func (c check) holds(text, rootDir string) bool {
	switch c.kind {
	case kindFunctionBlock:
		block := extractNamedFunctionBlock(text, c.functionName)
		return strings.Contains(block, c.pattern)
	case kindFunctionBlockAbsent:
		block := extractNamedFunctionBlock(text, c.functionName)
		return !strings.Contains(block, c.pattern)
	case kindFunctionBlockRegex:
		block := extractNamedFunctionBlock(text, c.functionName)
		return regexMatchesAnyLine(c.regex, block)
	case kindFunctionBlockRegexAbsent:
		// Negated function_block_contains_regex: a regex match on any line of
		// the extracted block is a violation, so the invariant holds only when
		// regex matches no line of the block.
		block := extractNamedFunctionBlock(text, c.functionName)
		return !regexMatchesAnyLine(c.regex, block)
	case kindDirExists:
		// `[[ ! -d "${ROOT_DIR}/PATH" ]]`: holds only when the path exists and
		// is a directory.
		info, err := os.Stat(filepath.Join(rootDir, c.targetPath))
		return err == nil && info.IsDir()
	case kindExecutable:
		// `[[ ! -x "${ROOT_DIR}/PATH" ]]`: holds only when the path is
		// executable by the current user. Bash `-x` is defined via access(2)
		// with X_OK ("executable by you"), which honours owner/group/other
		// selection and ACLs — not "any execute bit set" — so mirror it with
		// syscall.Access rather than inspecting Mode() bits. A missing path
		// yields ENOENT, so this correctly returns false.
		return syscall.Access(filepath.Join(rootDir, c.targetPath), 0x1) == nil
	case kindFileExists:
		// `[[ ! -f "${ROOT_DIR}/PATH" ]]`: holds only when the path exists and
		// is a regular file. Bash `-f` (unlike `-e`) rejects directories and
		// other special files, so mirror it with info.Mode().IsRegular(); a
		// missing path yields an error and correctly returns false.
		info, err := os.Stat(filepath.Join(rootDir, c.targetPath))
		return err == nil && info.Mode().IsRegular()
	case kindGoFunctionBlock:
		block := extractGoFunctionBlock(text, c.functionName)
		return strings.Contains(block, c.pattern)
	case kindFirstLineRegex:
		return regexp.MustCompile(c.regex).MatchString(firstLine(text))
	case kindPresent:
		return strings.Contains(text, c.pattern)
	case kindPresentInAnyFile:
		// Per-file containment predicate for a single listed file; evaluate
		// ORs this across every path in targetFiles to reproduce grep's
		// multi-file OR semantics.
		return strings.Contains(text, c.pattern)
	case kindCountAtLeast:
		// Mirror `grep -Fc`: count how many LINES contain the fixed needle
		// (a line with multiple occurrences still counts once), and hold only
		// when that line count is at least minCount.
		count := 0
		for _, line := range strings.Split(text, "\n") {
			if strings.Contains(line, c.pattern) {
				count++
			}
		}
		return count >= c.minCount
	case kindAbsent:
		return !strings.Contains(text, c.pattern)
	case kindRegexAbsent:
		return !regexMatchesAnyLine(c.regex, text)
	case kindRegexPresent:
		return regexMatchesAnyLine(c.regex, text)
	case kindJSONExprEval:
		// Mirror `jq -e '<jsonPath> == <jsonExpectedRaw>' FILE`: parse the file as
		// JSON, navigate the dotted object path, and compare the leaf to the RHS
		// literal with jq's type-aware equality.  Any jq-error condition (invalid
		// JSON, or indexing a non-object scalar/array) maps to a non-zero jq exit
		// under the `if !` guard, i.e. the invariant does not hold.
		var root interface{}
		if err := json.Unmarshal([]byte(text), &root); err != nil {
			return false
		}
		leaf, ok := navigateJSONPath(root, c.jsonPath)
		if !ok {
			return false
		}
		var expected interface{}
		if err := json.Unmarshal([]byte(c.jsonExpectedRaw), &expected); err != nil {
			return false
		}
		// Both leaf and expected come from encoding/json, so numbers are float64,
		// strings are string, booleans are bool, and null is nil; reflect.DeepEqual
		// then reproduces jq's typed `==` (a string never equals a boolean, and
		// 1 == 1.0 because both decode to float64).
		return reflect.DeepEqual(leaf, expected)
	case kindJSONPathTruthy:
		// Mirror a bare-path `jq -e '<jsonPath>' FILE` guard.  truthy is true only
		// when the file is valid JSON, the path navigates without a jq type error,
		// and the leaf is neither JSON null nor boolean false — jq -e's exit-0
		// condition ("last output is neither null nor false").  jsonViolateWhenTruthy
		// selects the shell polarity (see the kind's doc comment); a falsy value and
		// a navigation/parse error both yield truthy=false, so the inverted polarity
		// holds on both, exactly as `if jq -e` does.
		truthy := false
		var root interface{}
		if err := json.Unmarshal([]byte(text), &root); err == nil {
			if leaf, ok := navigateJSONPath(root, c.jsonPath); ok {
				truthy = leaf != nil && leaf != false
			}
		}
		if c.jsonViolateWhenTruthy {
			return !truthy
		}
		return truthy
	case kindJSONTypeEquals:
		// Mirror `jq -e '<jsonPath> | type == "<T>"' FILE`: invalid JSON or a
		// navigation type error is jq's error exit → violation (holds false); a
		// resolved leaf (including nil for a missing key or explicit null, which jq
		// types as "null") holds iff its jq type equals the expected type string.
		var root interface{}
		if err := json.Unmarshal([]byte(text), &root); err != nil {
			return false
		}
		leaf, ok := navigateJSONPath(root, c.jsonPath)
		if !ok {
			return false
		}
		var expected interface{}
		if err := json.Unmarshal([]byte(c.jsonExpectedRaw), &expected); err != nil {
			return false
		}
		expectedType, ok := expected.(string)
		if !ok {
			return false
		}
		return jqType(leaf) == expectedType
	default:
		return false
	}
}

// jqType returns jq's `type` builtin string for a value decoded by
// encoding/json ("null"/"boolean"/"number"/"string"/"array"/"object"),
// mirroring how jq classifies the six JSON kinds.  A nil interface (a missing
// key or explicit null) is "null".
func jqType(v interface{}) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case float64:
		return "number"
	case string:
		return "string"
	case []interface{}:
		return "array"
	case map[string]interface{}:
		return "object"
	default:
		return ""
	}
}

// navigateJSONPath walks a jq path (e.g. ".a.b.c" or ".a[0].b") from root,
// mirroring jq's traversal for the object-key + array-index subset used by the
// migrated `jq -e` checks.  It returns (leaf, true) for a resolved value —
// including nil for a missing key, an out-of-range array index, or an explicit
// null, because jq indexes an absent/null value or a past-the-end array slot as
// null rather than erroring — and (nil, false) only when a segment errors the
// way jq does: indexing a non-object scalar or array with a string key, or
// indexing a non-array scalar or object with a numeric index.  A leading "." is
// stripped and "." (or "") returns root unchanged.
func navigateJSONPath(root interface{}, path string) (interface{}, bool) {
	current := root
	for _, tok := range parseJSONPath(path) {
		if tok.isIndex {
			// jq numeric indexing: an array yields the element (out-of-range → null),
			// null yields null, and any other type is a jq error (non-zero exit).
			switch v := current.(type) {
			case []interface{}:
				if tok.index < 0 || tok.index >= len(v) {
					current = nil
				} else {
					current = v[tok.index]
				}
			case nil:
				current = nil
			default:
				return nil, false
			}
			continue
		}
		switch v := current.(type) {
		case map[string]interface{}:
			// An absent key yields the zero value nil, i.e. jq null.
			current = v[tok.key]
		case nil:
			// jq: `null | .key` → null, no error.
			current = nil
		default:
			// A string, number, boolean, or array indexed with a string key is a
			// jq error → non-zero exit → violation.
			return nil, false
		}
	}
	return current, true
}

// jsonPathToken is one navigation step of a jq path: either an object key
// (isIndex false) or a zero-based array index (isIndex true).
type jsonPathToken struct {
	key     string
	index   int
	isIndex bool
}

// jsonPathTokenRE matches one navigation step of the jq path subset the migrated
// checks use: a dotted identifier `.key` or a bracketed array index `[N]`.
var jsonPathTokenRE = regexp.MustCompile(`\.([A-Za-z_][A-Za-z0-9_]*)|\[([0-9]+)\]`)

// parseJSONPath tokenises a jq path (e.g. ".a.b[0].c") into its ordered
// navigation steps, mirroring the object-key and array-index traversal the
// migrated `jq -e` checks use.  A leading/whole-document "." (or "") yields no
// tokens.  Only the simple `.identifier` and `[number]` forms are supported; if
// any character is not consumed by a token (a malformed or unsupported path, or
// the bare "." root) the result is nil, so navigation resolves the whole
// document rather than silently dropping a segment.
func parseJSONPath(path string) []jsonPathToken {
	matches := jsonPathTokenRE.FindAllStringSubmatchIndex(path, -1)
	tokens := make([]jsonPathToken, 0, len(matches))
	consumed := 0
	for _, m := range matches {
		if m[0] != consumed {
			return nil
		}
		consumed = m[1]
		if m[2] >= 0 {
			// `.key` capture group.
			tokens = append(tokens, jsonPathToken{key: path[m[2]:m[3]]})
		} else {
			// `[number]` capture group.
			n, _ := strconv.Atoi(path[m[4]:m[5]])
			tokens = append(tokens, jsonPathToken{index: n, isIndex: true})
		}
	}
	if consumed != len(path) {
		return nil
	}
	return tokens
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

// goBlockInlineComment matches an inline /* ... */ block comment with no
// embedded asterisk, mirroring the awk gsub(/\/\*[^*]*\*\//, "", line) in
// go_function_block_contains_fixed.
var goBlockInlineComment = regexp.MustCompile(`/\*[^*]*\*/`)

// extractGoFunctionBlock replicates go_function_block_contains_fixed's awk
// extraction: it returns the lines from the first line beginning with
// `func NAME(` through the next line that is exactly `}` (both inclusive),
// with comments stripped before matching so a needle appearing only in a
// // line comment or a /* */ block comment (including a multi-line block
// comment) inside the function cannot satisfy the fixed-string check.  Unlike
// extractNamedFunctionBlock (bash sed-range, closing on any `^}` prefix), the
// closing line here must equal `}` exactly, mirroring the awk `$0 == "}"`.
// The result feeds a strings.Contains fixed-string check (grep -Fq parity).
func extractGoFunctionBlock(text, name string) string {
	openPrefix := "func " + name + "("
	var out []string
	inBlock := false
	inComment := false
	for _, orig := range strings.Split(text, "\n") {
		if !inBlock {
			if !strings.HasPrefix(orig, openPrefix) {
				continue
			}
			inBlock = true
		}
		// Strip comments exactly as the awk does, in order: close any open
		// block comment (greedy through the last `*/`), remove inline
		// `/* ... */` comments, open a block comment at the first remaining
		// `/*`, then drop a trailing `//` line comment.
		line := orig
		if inComment {
			if idx := strings.LastIndex(line, "*/"); idx >= 0 {
				line = line[idx+2:]
				inComment = false
			} else {
				line = ""
			}
		}
		line = goBlockInlineComment.ReplaceAllString(line, "")
		if idx := strings.Index(line, "/*"); idx >= 0 {
			line = line[:idx]
			inComment = true
		}
		if idx := strings.Index(line, "//"); idx >= 0 {
			line = line[:idx]
		}
		out = append(out, line)
		// The exit test runs on the ORIGINAL line ($0 == "}"), after the
		// comment-stripped line has been appended, so the closing brace line
		// is included before extraction stops.
		if orig == "}" {
			break
		}
	}
	return strings.Join(out, "\n")
}
