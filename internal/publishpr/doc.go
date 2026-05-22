// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

// Package publishpr carries the `workcell publish-pr` user interface.
// It owns four concerns:
//
//   - UsageText: the help banner emitted on -h / --help and on usage
//     errors.
//
//   - Argument parsing and validation: ParseArgs walks the publish-pr
//     option vector; ValidateSnapshotName, ValidateBranchName, and
//     ValidateBaseName check inputs; LoadTextArg reconciles inline vs
//     --*-file inputs; Preflight bundles validators with base-mode
//     downgrade and text-arg loading into one pass.
//     RepoOwnedChecksExpected returns the byte-identical literals the
//     dry-run output expects.
//
//   - Host execution layer: BashContext + RunPublishHostCommandInDir +
//     the resolved git/gh wrappers (runCleanGit, etc.) drive host-side
//     process control under an env -i sandbox.  IsTrustedHostToolPath,
//     CanonicalizeHostToolPath, ResolveHostTool, and
//     ResolveExistingExecutableOrDie own the trusted-PATH allowlist.
//
//   - Dry-run emission: EmitCommand + bashQuote produce the
//     `printf %q` shape that
//     tests/scenarios/shared/test-publish-pr-dry-run.sh asserts on.
//
// PublishPRMain wires the four concerns together as the entry point
// for the top-level `workcell-hostutil publish-pr-cli` subcommand.
package publishpr
