// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

// Package sessionctl carries the `workcell session <cmd>` user
// interface that historically lived in scripts/workcell as the
// session_* bash functions.  This package is the first step of the
// /sethify PR 22 Phase 5 decomposition; future sub-PRs will move
// session_timeline_main, session_logs_main, session_attach_main,
// session_send_main, session_stop_main, session_delete_main, and the
// session_main dispatcher into this package one cluster at a time.
//
// The host-side session-record schema and lookup helpers stay in the
// neighbouring internal/host/sessions package (different concern: that
// package is the file/JSON layout, this package is the launcher CLI
// surface on top of it).
package sessionctl
