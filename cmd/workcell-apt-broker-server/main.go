// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/omkhar/workcell/internal/aptbroker"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--start" {
		if err := startServer(os.Args[2:]); err != nil {
			fail(err)
		}
		return
	}
	serve(os.Args[1:])
}

func serve(arguments []string) {
	flags := flag.NewFlagSet("workcell-apt-broker-server", flag.ExitOnError)
	socket := flags.String("socket", aptbroker.DefaultSocketPath, "")
	peerUID := flags.Uint64("peer-uid", 0, "")
	concurrency := flags.Int("max-concurrent", 4, "")
	readyFD := flags.Uint("ready-fd", 0, "")
	ackFD := flags.Uint("ack-fd", 0, "")
	if err := flags.Parse(arguments); err != nil {
		fail(err)
	}
	timeout, err := helperTimeout(os.Getenv("WORKCELL_APT_BROKER_HELPER_TIMEOUT_SECONDS"))
	if err != nil {
		fail(fmt.Errorf("invalid helper timeout: %w", err))
	}
	uid, err := peerUIDValue(*peerUID)
	if err != nil {
		fail(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ready, err := startupHandshake(uintptr(*readyFD), uintptr(*ackFD))
	if err != nil {
		fail(err)
	}
	err = aptbroker.Serve(ctx, aptbroker.ServerConfig{SocketPath: *socket, Timeout: timeout, MaxConcurrent: *concurrency, ExpectedPeerUID: uid, RequireRootSocket: true, Ready: ready})
	if err != nil {
		fail(err)
	}
}

func peerUIDValue(value uint64) (uint32, error) {
	if value == 0 || value > uint64(^uint32(0)) {
		return 0, errors.New("invalid peer uid")
	}
	return uint32(value), nil
}

func helperTimeout(raw string) (time.Duration, error) {
	if raw == "" {
		return aptbroker.DefaultTimeout, nil
	}
	seconds, err := strconv.ParseUint(raw, 10, 31)
	if err != nil {
		return 0, err
	}
	return time.Duration(seconds) * time.Second, nil
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, serverDiagnostic(err))
	os.Exit(1)
}

func serverDiagnostic(err error) string {
	return fmt.Sprintf("Workcell apt broker failed: %v", err)
}
