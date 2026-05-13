// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

// Package publishpr carries the `workcell publish-pr` user interface
// that historically lived in scripts/workcell as the publish_pr_*
// bash functions. The decomposition is intentionally staged across
// the /sethify PR 24 chain:
//
//   - PR 24.1 (landed): UsageText() — the help banner.
//   - PR 24.2a (this file's home): ParseArgs, the validators
//     (ValidateSnapshotName, ValidateBranchName, ValidateBaseName),
//     LoadTextArg, RepoOwnedChecksExpected, and Preflight. These are
//     pure-Go translations of the argument parsing + validation +
//     text-arg loading section of publish_pr_main and carry no host
//     filesystem or process side effects.
//   - PR 24.2b (next): the host execution layer that drives git/gh,
//     emits the dry-run command list, and wires `workcell-hostutil
//     launcher publish-pr-cli` so scripts/workcell publish_pr_main
//     becomes a thin shim.
//
// Host-side git and GitHub CLI invocation stays outside this package
// for now (different concern: that is host-side process control, this
// package is the host-side CLI surface text and validation).
package publishpr
