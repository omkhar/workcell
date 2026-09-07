// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

//go:build linux

package aptbroker

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestServeRoundTripPreservesHelperResult(t *testing.T) {
	ctx, socket := startTestBroker(t)
	exchange, cancelExchange := context.WithTimeout(ctx, testBrokerExchangeTimeout)
	defer cancelExchange()
	lookup := func(string) (string, bool) { return "noninteractive", true }
	response, status, err := RunClient(exchange, socket, []string{"apt-get"}, []string{"DEBIAN_FRONTEND"}, lookup)
	if err != nil {
		t.Fatal(err)
	}
	if status != 37 || response.Status != status {
		t.Fatalf("client status = %d, response status = %d", status, response.Status)
	}
	if string(response.Stdout) != "noninteractive" || !strings.HasSuffix(string(response.Stderr), "fixture-stderr") {
		t.Fatalf("helper stdout = %q, stderr = %q", response.Stdout, response.Stderr)
	}
}

func TestServerReportsMalformedRequest(t *testing.T) {
	server, client := unixConnectionPair(t)
	defer server.Close()
	defer client.Close()
	if _, err := client.Write([]byte("invalid frame")); err != nil {
		t.Fatal(err)
	}
	if err := client.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	var diagnostics bytes.Buffer
	err := handleConnection(context.Background(), server, ServerConfig{ErrorWriter: &diagnostics})
	if err == nil || diagnostics.Len() != 0 {
		t.Fatalf("handleConnection() = %v, premature diagnostics = %q", err, diagnostics.String())
	}
	ServerConfig{ErrorWriter: &diagnostics}.report(err)
	if got := diagnostics.String(); got == "" || !strings.Contains(got, "read request") {
		t.Fatalf("diagnostic = %q", got)
	}
}

// A privileged socket path must be unreachable from any directory an
// unprivileged uid can write, so no ancestor can be repointed after validation.
func TestValidateSocketAncestryRejectsMutableAncestors(t *testing.T) {
	if err := validateSocketAncestry("/"); err != nil {
		t.Fatalf("root-owned ancestry was rejected: %v", err)
	}
	// /tmp is world-writable, so nothing beneath it can be trusted.
	if err := validateSocketAncestry("/tmp"); err == nil {
		t.Fatal("world-writable ancestor was accepted")
	}
	if err := validateSocketAncestry(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing ancestor was accepted")
	}
}

const (
	testBrokerReadyTimeout    = 10 * time.Second
	testBrokerExchangeTimeout = 30 * time.Second
)

// The returned context is the broker lifetime, so a dialing caller derives its
// own per-exchange deadline and a slow exchange cannot close the listener under
// itself. Readiness comes from ServerConfig.Ready: polling for the socket file
// would see the pathname between bind(2) and listen(2), where a dial is refused.
func startTestBroker(t *testing.T) (context.Context, string) {
	t.Helper()
	// ServerConfig rejects peer uid 0, so root is never an admissible peer for
	// its own broker and this round trip has no uid to dial from.
	if os.Getuid() == 0 {
		t.Skip("round trip requires a non-root client: peer uid 0 is not admissible")
	}
	socket := filepath.Join(shortSocketDir(t), "broker.sock")
	helper := writeHelper(t, "printf '%s' \"$DEBIAN_FRONTEND\"\nprintf fixture-stderr >&2\nexit 37\n")
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	finished := make(chan error, 1)
	go func() {
		finished <- Serve(ctx, ServerConfig{
			SocketPath:      socket,
			HelperPath:      helper,
			ExpectedPeerUID: uint32(os.Getuid()),
			Ready:           func() error { close(ready); return nil },
		})
	}()
	t.Cleanup(func() {
		cancel()
		awaitTestBrokerShutdown(t, finished)
	})
	select {
	case <-ready:
	case <-time.After(testBrokerReadyTimeout):
		t.Fatal("broker did not report readiness")
	}
	return ctx, socket
}

func awaitTestBrokerShutdown(t *testing.T, finished <-chan error) {
	t.Helper()
	select {
	case err := <-finished:
		if err != nil {
			t.Errorf("broker shutdown failed: %v", err)
		}
	case <-time.After(6 * time.Second):
		t.Error("broker did not finish after cancellation")
	}
}
