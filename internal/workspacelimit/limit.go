// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

// Package workspacelimit owns the size caps for a workspace that Workcell
// copies into an isolated target. Each materialization path enforces these
// caps before it writes the copy.
package workspacelimit

const (
	// MaxFileBytes caps one regular file.
	MaxFileBytes int64 = 256 << 20
	// MaxBytes caps the sum of all regular-file bytes.
	MaxBytes int64 = 2 << 30
	// MaxEntries caps the number of entries that one walk visits.
	MaxEntries = 100_000
)
