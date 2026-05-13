// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

// Package publishpr carries the `workcell publish-pr` user interface
// that historically lived in scripts/workcell as the publish_pr_*
// bash functions.  This package is the first step of the /sethify
// PR 24 decomposition; a future sub-PR will move publish_pr_main and
// its helpers into this package.
//
// Host-side git and GitHub CLI invocation stays outside this package
// for now (different concern: that is host-side process control, this
// package is the host-side CLI surface text).
package publishpr
