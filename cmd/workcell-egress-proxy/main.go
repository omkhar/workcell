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

	"github.com/omkhar/workcell/internal/cliexit"
	"github.com/omkhar/workcell/internal/egressproxy"
)

func main() {
	err := run(os.Args[1:], os.Stdout, os.Stderr)
	if err == nil || errors.Is(err, flag.ErrHelp) { // help: usage was printed
		return
	}
	fmt.Fprintf(os.Stderr, "workcell-egress-proxy: %v\n", err)
	if ec, ok := cliexit.IsExitCodeError(err); ok {
		os.Exit(ec.Code)
	}
	os.Exit(1)
}

// usageError marks a usage or precondition error, which exits 2.
func usageError(err error) error {
	return &cliexit.ExitCodeError{Code: 2, Message: err.Error()}
}

func run(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("workcell-egress-proxy", flag.ContinueOnError)
	flags.SetOutput(stderr)
	allowPath := flags.String("allowlist", "", "file with one host:port entry per line")
	listenHost := flags.String("listen", "0.0.0.0", "address to listen on")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return err
		}
		return usageError(err)
	}
	if flags.NArg() != 0 {
		return usageError(fmt.Errorf("unexpected argument: %q", flags.Arg(0)))
	}
	if *allowPath == "" {
		return usageError(errors.New("-allowlist is required"))
	}
	allow, err := egressproxy.LoadAllowlist(*allowPath)
	if err != nil {
		return usageError(err)
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
