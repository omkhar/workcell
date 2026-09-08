// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

//go:build darwin

package rootio

import "golang.org/x/sys/unix"

// renameNoReplaceAt publishes from onto to in one step and refuses to replace
// an existing name. Darwin spells the flag RENAME_EXCL.
func renameNoReplaceAt(parentFD int, from, to string) error {
	return unix.RenameatxNp(parentFD, from, parentFD, to, unix.RENAME_EXCL)
}
