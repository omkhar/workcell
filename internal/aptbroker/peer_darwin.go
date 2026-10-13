// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

//go:build darwin

package aptbroker

import (
	"net"

	"golang.org/x/sys/unix"
)

// SocketPeerUID returns the uid of the process on the other end of connection.
func SocketPeerUID(connection *net.UnixConn) (uint32, error) {
	raw, err := connection.SyscallConn()
	if err != nil {
		return 0, err
	}
	var uid uint32
	var controlErr error
	err = raw.Control(func(fd uintptr) {
		credential, err := unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
		if err != nil {
			controlErr = err
			return
		}
		uid = credential.Uid
	})
	if err != nil {
		return 0, err
	}
	return uid, controlErr
}
