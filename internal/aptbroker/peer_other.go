// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

//go:build !linux

package aptbroker

import (
	"fmt"
	"net"
	"os"
)

func validateServerSupport() error {
	return fmt.Errorf("apt broker server is unsupported on this platform")
}

func peerUID(*net.UnixConn) (uint32, error) {
	return 0, fmt.Errorf("apt broker server is unsupported on this platform")
}
func fileUID(os.FileInfo) uint32 {
	return ^uint32(0)
}
