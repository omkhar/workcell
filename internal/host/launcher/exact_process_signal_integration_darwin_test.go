// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

//go:build darwin

package launcher

func exactProcessSignalIntegrationUnavailableReason() string {
	if !darwinAuditTokenSignalAvailable() {
		return "Darwin audit-token signaling requires Darwin kernel 23.2 or newer"
	}
	return ""
}
