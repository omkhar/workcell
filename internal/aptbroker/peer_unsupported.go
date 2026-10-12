// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

//go:build !linux && !darwin

package aptbroker

import (
	"fmt"
	"net"
)

// SocketPeerUID has no peer-credential source on this platform.
func SocketPeerUID(*net.UnixConn) (uint32, error) {
	return 0, fmt.Errorf("socket peer credentials are unsupported on this platform")
}
