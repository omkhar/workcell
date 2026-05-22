// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

// Package sessions defines the on-disk record format and lookup
// helpers for durable detached Workcell sessions.  The CLI surface
// that consumes these records lives in internal/sessionctl; this
// package owns the file/JSON layout, the StateDirs convention for
// where sessions live on disk, and the helpers that read/write a
// single session record.
package sessions
