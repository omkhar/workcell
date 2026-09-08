// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

//go:build linux

package rootio

import "golang.org/x/sys/unix"

// renameNoReplaceAt publishes from onto to in one step and refuses to replace
// an existing name. Linux spells the flag RENAME_NOREPLACE.
func renameNoReplaceAt(parentFD int, from, to string) error {
	return unix.Renameat2(parentFD, from, parentFD, to, unix.RENAME_NOREPLACE)
}
