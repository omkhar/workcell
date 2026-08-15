// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

//go:build (!darwin && !linux) || ios || android

package applecontainer

const workspaceMaterializationSupported = false

func workspacePublish(int, string, int, string) error { return errWorkspaceMaterializationUnsupported }
