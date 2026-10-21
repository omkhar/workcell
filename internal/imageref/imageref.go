// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

// Package imageref owns the rule that an image reference names its image by
// an immutable digest.
package imageref

import "regexp"

var pinnedPattern = regexp.MustCompile(`^[^@]+@sha256:[0-9a-f]{64}$`)

// Pinned reports whether ref ends in @sha256: and a lowercase SHA-256 digest.
func Pinned(ref string) bool {
	return pinnedPattern.MatchString(ref)
}
