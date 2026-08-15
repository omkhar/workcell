// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

//go:build darwin && !ios

package applecontainer

import "golang.org/x/sys/unix"

const workspaceMaterializationSupported = true

func workspacePublish(fromFD int, from string, toFD int, to string) error {
	return unix.RenameatxNp(fromFD, from, toFD, to, unix.RENAME_EXCL)
}
