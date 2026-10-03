// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

//go:build linux

package credbroker

import (
	"net"

	"github.com/omkhar/workcell/internal/aptbroker"
)

// The broker runs on macOS hosts; Linux support exists so the package tests
// and mutation lane run on Linux CI.
func peerUID(connection *net.UnixConn) (uint32, error) {
	return aptbroker.SocketPeerUID(connection)
}
