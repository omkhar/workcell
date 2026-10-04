// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

// Command workcell-egress-proxy runs the per-session egress proxy. It listens
// on each allowlisted port and writes one JSONL line per denied connection to
// stdout. Overload refusals are best-effort: they are dropped if stdout stalls.
// With -terminate it terminates TLS for the named hosts under a session CA,
// writes the CA certificate to -ca-out, and swaps placeholder headers for
// values from the credential broker at -broker-socket.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/omkhar/workcell/internal/cliexit"
	"github.com/omkhar/workcell/internal/egressproxy"
	"github.com/omkhar/workcell/internal/rootio"
)

// newBroker connects to the credential broker. The broker client ships with
// the broker package; until then -terminate fails closed here.
func newBroker(socket string) (egressproxy.Broker, error) {
	return nil, fmt.Errorf("-broker-socket %s: this build has no credential broker client", socket)
}

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
	allowText := flags.String("allow", "", "space-separated host:port entries, in place of -allowlist")
	listenHost := flags.String("listen", "0.0.0.0", "address to listen on")
	var rules []egressproxy.TerminateRule
	flags.Func("terminate", "host=header=placeholder to terminate and swap (repeatable)", func(text string) error {
		rule, err := egressproxy.ParseTerminateRule(text)
		rules = append(rules, rule)
		return err
	})
	caOut := flags.String("ca-out", "", "file to write the session CA certificate to (with -terminate)")
	caTTL := flags.Duration("ca-ttl", 0, "session CA lifetime, at most 24h (with -terminate)")
	brokerSocket := flags.String("broker-socket", "", "credential broker socket (with -terminate)")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return err
		}
		return usageError(err)
	}
	if flags.NArg() != 0 {
		return usageError(fmt.Errorf("unexpected argument: %q", flags.Arg(0)))
	}
	if (*allowPath == "") == (*allowText == "") {
		return usageError(errors.New("exactly one of -allowlist or -allow is required"))
	}
	if _, err := netip.ParseAddr(*listenHost); err != nil {
		return usageError(fmt.Errorf("-listen must be an IP address: %q", *listenHost))
	}
	var allow *egressproxy.Allowlist
	var err error
	if *allowText != "" {
		allow, err = egressproxy.ParseAllowlist(*allowText, "-allow")
	} else {
		allow, err = egressproxy.LoadAllowlist(*allowPath)
	}
	if err != nil {
		return usageError(err)
	}
	proxy := egressproxy.New(allow, stdout)
	if len(rules) == 0 {
		if *caOut != "" || *caTTL != 0 || *brokerSocket != "" {
			return usageError(errors.New("-ca-out, -ca-ttl and -broker-socket require -terminate"))
		}
	} else if err := terminate(proxy, rules, *caOut, *caTTL, *brokerSocket, newBroker); err != nil {
		return err
	}
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

// terminate sets up TLS termination and writes the session CA certificate.
func terminate(proxy *egressproxy.Proxy, rules []egressproxy.TerminateRule, caOut string, caTTL time.Duration, socket string, dial func(string) (egressproxy.Broker, error)) error {
	if caOut == "" || socket == "" {
		return usageError(errors.New("-terminate requires -ca-out, -ca-ttl and -broker-socket"))
	}
	if caTTL <= 0 || caTTL > egressproxy.MaxCALifetime {
		return usageError(fmt.Errorf("-ca-ttl must be above 0 and at most %s: %s", egressproxy.MaxCALifetime, caTTL))
	}
	hosts := make([]string, len(rules))
	for i, r := range rules {
		hosts[i] = r.Host
	}
	ca, err := egressproxy.NewSessionCA(hosts, time.Now().Add(caTTL))
	if err != nil {
		return err
	}
	broker, err := dial(socket)
	if err != nil {
		return usageError(err)
	}
	if err := proxy.Terminate(ca, broker, rules); err != nil {
		return usageError(err)
	}
	parent, path, err := rootio.OpenParentDirectoryNoFollow(caOut)
	if err != nil {
		return err
	}
	defer parent.Close()
	// Owner-only like every state write; the launcher hands the certificate to
	// the agent container through the injection bundle, never through this file's mode.
	return rootio.StageAndPublishAt(parent, filepath.Base(path), ca.CertPEM(), 0o600, ".egress-ca-")
}
