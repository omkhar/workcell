// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package aptbroker

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestWaitForHandlersHasTerminalBound(t *testing.T) {
	var handlers sync.WaitGroup
	handlers.Add(1)
	if err := waitForHandlers(&handlers, time.Millisecond); err == nil {
		t.Fatal("handler wait had no terminal bound")
	}
	handlers.Done()
}

func TestPrepareSocketPathRejectsSymlink(t *testing.T) {
	path := filepath.Join(t.TempDir(), "broker.sock")
	if err := os.Symlink("target", path); err != nil {
		t.Fatal(err)
	}
	if err := prepareSocketPath(path); err == nil {
		t.Fatal("symlink socket path was accepted")
	}
}

func TestValidateSocketParentRejectsWritableDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "socket-root")
	if err := os.Mkdir(path, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := validateSocketParent(path, false); err == nil {
		t.Fatal("writable socket parent was accepted")
	}
}

func TestValidateSocketParentRequiresExactRootMode(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("root ownership check requires root")
	}
	path := filepath.Join(t.TempDir(), "socket-root")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := validateSocketParent(path, true); err == nil {
		t.Fatal("root-owned socket parent with mode 0700 was accepted")
	}
}

func TestServerConfigRejectsUnsafeLimits(t *testing.T) {
	for _, config := range []ServerConfig{
		{ExpectedPeerUID: 0},
		{ExpectedPeerUID: 1, MaxConcurrent: maxConcurrent + 1},
		{ExpectedPeerUID: 1, Timeout: MaxTimeout + time.Second},
	} {
		if err := config.normalize(); err == nil {
			t.Fatal("unsafe server configuration was accepted")
		}
	}
}

func TestReserveAdmissionRejectsExcessConnection(t *testing.T) {
	admitted := make(chan struct{}, maxConcurrent)
	for range maxConcurrent {
		if !reserveAdmission(admitted) {
			t.Fatal("admission closed before the configured limit")
		}
	}
	if reserveAdmission(admitted) {
		t.Fatal("admission exceeded the configured limit")
	}
}

func TestValidatePeerUIDRejectsUntrustedPeers(t *testing.T) {
	probeErr := errors.New("peer probe failed")
	for _, test := range []struct {
		uid      uint32
		probeErr error
	}{
		{uid: 1000, probeErr: probeErr},
		{uid: 0},
		{uid: 1001},
	} {
		if err := validatePeerUID(test.uid, test.probeErr, 1000); err == nil {
			t.Fatalf("untrusted peer uid %d was accepted", test.uid)
		}
	}
	if err := validatePeerUID(1000, nil, 1000); err != nil {
		t.Fatalf("expected peer was rejected: %v", err)
	}
}

func TestListenSocketSetsModeAndPreservesReplacement(t *testing.T) {
	path := filepath.Join(shortSocketDir(t), "broker.sock")
	listener, err := listenSocket(path, false)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(path)
	if err != nil || info.Mode().Perm() != 0o666 || info.Mode()&os.ModeSocket == 0 {
		t.Fatalf("socket info = (%v, %v)", info, err)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	removeSocket(path, listener)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("replacement was removed: %v", err)
	}
}

func TestPrepareSocketPathRejectsExistingSocketWithoutRemovingIt(t *testing.T) {
	path := filepath.Join(shortSocketDir(t), "broker.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if err := prepareSocketPath(path); err == nil {
		t.Fatal("managed startup accepted an existing socket")
	}
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSocket == 0 {
		t.Fatalf("existing socket was removed: (%v, %v)", info, err)
	}
}

func TestServeRunsReadinessBeforeAccept(t *testing.T) {
	if validateServerSupport() != nil {
		t.Skip("server is unsupported on this platform")
	}
	path := filepath.Join(shortSocketDir(t), "broker.sock")
	ready := make(chan struct{})
	release := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	go func() {
		finished <- Serve(ctx, ServerConfig{
			SocketPath:      path,
			ExpectedPeerUID: uint32(os.Getuid()),
			Ready: func() error {
				close(ready)
				<-release
				return nil
			},
		})
	}()
	<-ready
	connection, err := net.DialTimeout("unix", path, 20*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := connection.Write([]byte("not accepted yet")); err != nil {
		t.Fatal(err)
	}
	close(release)
	cancel()
	connection.Close()
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
}

func TestServeRejectsUnsupportedPlatformBeforeSocketCreation(t *testing.T) {
	if validateServerSupport() == nil {
		t.Skip("server is supported on this platform")
	}
	path := filepath.Join(t.TempDir(), "broker.sock")
	err := Serve(context.Background(), ServerConfig{SocketPath: path, ExpectedPeerUID: 1})
	if err == nil {
		t.Fatal("unsupported server unexpectedly started")
	}
}

func TestHelperConfigurationPreservesExecutionAndDiagnostics(t *testing.T) {
	var diagnostics bytes.Buffer
	config := ServerConfig{HelperPath: "/fixture/helper", Timeout: 7 * time.Second, ErrorWriter: &diagnostics}
	helper := config.helperConfig()
	if helper.path != config.HelperPath || helper.timeout != config.Timeout {
		t.Fatalf("helper configuration = %#v", helper)
	}
	if helper.report == nil {
		t.Fatal("helper diagnostic callback is missing")
	}
	helper.report(nil)
	if diagnostics.Len() != 0 {
		t.Fatalf("nil error produced diagnostics: %q", diagnostics.String())
	}
	helper.report(errors.New("fixture cleanup failure"))
	if got := diagnostics.String(); got != "Workcell apt broker: fixture cleanup failure\n" {
		t.Fatalf("helper diagnostic = %q", got)
	}
}

// A bound socket path must fit the AF_UNIX sun_path limit, which the test name
// that t.TempDir() embeds overruns under the long TMPDIR that CI sets. Tests
// that bind a real path need a short directory instead.
func shortSocketDir(t *testing.T) string {
	t.Helper()
	directory, err := os.MkdirTemp("/tmp", "aptbroker-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	return directory
}
