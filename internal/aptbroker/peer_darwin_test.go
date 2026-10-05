// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

//go:build darwin

package aptbroker

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestSocketPeerUIDDarwin(t *testing.T) {
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(t.TempDir(), "p.sock"), Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	client, err := net.DialUnix("unix", nil, listener.Addr().(*net.UnixAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	server, err := listener.AcceptUnix()
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	if uid, err := SocketPeerUID(server); err != nil || uid != uint32(os.Getuid()) {
		t.Fatalf("SocketPeerUID() = %d, %v; want %d", uid, err, os.Getuid())
	}
}
