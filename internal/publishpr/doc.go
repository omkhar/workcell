// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

// Package publishpr carries the `workcell publish-pr` user interface
// that historically lived in scripts/workcell as the publish_pr_*
// bash functions.  The package owns four concerns:
//
//   - UsageText: the help banner emitted on -h / --help and on usage
//     errors.
//
//   - Argument parsing and validation: ParseArgs walks the publish-pr
//     option vector with the same diagnostics the bash function did;
//     ValidateSnapshotName, ValidateBranchName, and ValidateBaseName
//     mirror the validate_publish_* bash helpers; LoadTextArg reconciles
//     inline vs --*-file inputs; Preflight bundles the validators with
//     base-mode downgrade and text-arg loading into one pass.
//     RepoOwnedChecksExpected mirrors publish_pr_repo_owned_checks_expected
//     so the dry-run output keeps byte-identical literals.
//
//   - Host execution layer: BashContext + RunPublishHostCommandInDir +
//     the resolved git/gh wrappers (runCleanGit, etc.) drive the host-
//     side process control under the env -i sandbox.  IsTrustedHostToolPath,
//     CanonicalizeHostToolPath, ResolveHostTool, and
//     ResolveExistingExecutableOrDie own the trusted-PATH allowlist
//     that scripts/workcell::is_trusted_host_tool_path enforces.
//
//   - Dry-run emission: EmitCommand + bashQuote replicate the
//     `printf %q` shape that
//     tests/scenarios/shared/test-publish-pr-dry-run.sh greps for.
//
// PublishPRMain wires the four concerns together as the launcher entry
// point for `workcell-hostutil launcher publish-pr-cli`; scripts/workcell
// publish_pr_main is the thin shim that forwards bash-side globals as
// --bash-* flags.
package publishpr
