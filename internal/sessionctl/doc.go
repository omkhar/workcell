// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

// Package sessionctl carries the parsing-and-validation half of the
// `workcell session <cmd>` user interface that historically lived in
// scripts/workcell as the session_* bash functions.
//
// Each user-facing subcommand has a matching `<Verb>Main` exported
// entry point in this package (AttachMain, SendMain, StopMain,
// DeleteMain, MonitorMain, TimelineMain, LogsMain) plus a private
// `<verb>Main` helper that accepts injected stdout/stderr writers for
// tests.  These functions own:
//
//   - Up-front option parsing (--id, --message, --no-newline, etc.)
//     mirroring the bash option_value_or_die / raw_option_value_or_die
//     idioms, with usage diagnostics emitted to stderr (so the bash
//     shim, which captures stdout into `$plan`, surfaces them to the
//     user instead of swallowing them).
//
//   - Control-character rejection for any value that the bash shim
//     transports as a key=value plan line (rejectControlChars in
//     parsehelpers.go) so a CR/LF in a user-controlled flag can't forge
//     additional plan entries downstream.
//
//   - On-disk session-record lookup via the neighbouring
//     internal/host/sessions package when the bash function needed to
//     load detached-session metadata before executing the docker
//     transport (StopMain, AttachMain).
//
// The docker transport, audit emission, and other side-effecting
// helpers remain in scripts/workcell; each translated main emits a
// plan on stdout (one `key=value` line per record) that the shim reads
// back through bash's `while IFS= read` loop.  This keeps
// tests/scenarios/shared/test-session-commands.sh's bash mocking
// surface intact.
//
// DispatchMain is the Go translation of the session_main dispatcher
// case statement.  It uses CanonicalSubcommands() as the single
// authoritative ordering of dispatch tokens (start, attach, send,
// stop, list, show, delete, logs, timeline, diff, export, monitor).
// The user-facing surface documented in man/workcell.1 and README.md
// is the first eleven tokens; `monitor` is the internal supervisor
// verb that `start` invokes against itself.
//
// The host-side session-record schema and lookup helpers stay in the
// neighbouring internal/host/sessions package (different concern: that
// package is the file/JSON layout, this package is the session CLI
// surface on top of it).
package sessionctl
