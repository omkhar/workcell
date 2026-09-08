// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

//go:build !darwin && !linux

package rootio

import "golang.org/x/sys/unix"

// renameNoReplaceAt has no one-step spelling on this platform, so the caller
// falls back to the link-and-unlink publication.
func renameNoReplaceAt(int, string, string) error {
	return unix.ENOTSUP
}
