// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

// Command workcell-egress-proxy runs the per-session egress proxy. It listens
// on each allowlisted port and writes one JSONL line per denied connection to
// stdout.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"

	"github.com/omkhar/workcell/internal/egressproxy"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "workcell-egress-proxy: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("workcell-egress-proxy", flag.ContinueOnError)
	flags.SetOutput(stderr)
	allowPath := flags.String("allowlist", "", "file with one host:port entry per line")
	listenHost := flags.String("listen", "0.0.0.0", "address to listen on")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *allowPath == "" {
		return errors.New("-allowlist is required")
	}
	allow, err := egressproxy.LoadAllowlist(*allowPath)
	if err != nil {
		return err
	}
	proxy := egressproxy.New(allow, stdout)
	errs := make(chan error)
	for _, port := range allow.Ports() {
		ln, err := net.Listen("tcp", net.JoinHostPort(*listenHost, strconv.Itoa(int(port))))
		if err != nil {
			return err
		}
		go func() { errs <- proxy.Serve(ln, port) }()
	}
	return <-errs
}
