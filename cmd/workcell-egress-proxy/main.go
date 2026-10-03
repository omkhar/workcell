// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

// Command workcell-egress-proxy runs the per-session egress proxy. It listens
// on each allowlisted port of one address and writes one JSONL line per denied connection to
// stdout. Overload refusals are best-effort: they are dropped if stdout stalls.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/netip"
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
	allowText := flags.String("allow", "", "space-separated host:port entries, in place of -allowlist")
	listen := flags.String("listen", "0.0.0.0", "IP address to listen on, or a CIDR subnet that holds exactly one local interface address")
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
	ifaceAddrs, err := net.InterfaceAddrs()
	if err != nil {
		return err
	}
	listenHost, err := listenAddr(*listen, ifaceAddrs)
	if err != nil {
		return usageError(err)
	}
	var allow *egressproxy.Allowlist
	if *allowText != "" {
		allow, err = egressproxy.ParseAllowlist(*allowText, "-allow")
	} else {
		allow, err = egressproxy.LoadAllowlist(*allowPath)
	}
	if err != nil {
		return usageError(err)
	}
	proxy := egressproxy.New(allow, stdout)
	errs := make(chan error)
	for _, port := range allow.Ports() {
		ln, err := net.Listen("tcp", net.JoinHostPort(listenHost.String(), strconv.Itoa(int(port))))
		if err != nil {
			return err
		}
		go func() { errs <- proxy.Serve(ln, port) }()
	}
	return <-errs
}

// listenAddr returns the -listen address. A CIDR subnet selects the one local
// interface address inside it, so a sidecar on two networks listens only on the
// network that the subnet names.
func listenAddr(value string, ifaceAddrs []net.Addr) (netip.Addr, error) {
	if addr, err := netip.ParseAddr(value); err == nil {
		return addr, nil
	}
	subnet, err := netip.ParsePrefix(value)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("-listen must be an IP address or a CIDR subnet: %q", value)
	}
	var found []netip.Addr
	for _, a := range ifaceAddrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		if addr, ok := netip.AddrFromSlice(ipnet.IP); ok && subnet.Contains(addr.Unmap()) {
			found = append(found, addr.Unmap())
		}
	}
	if len(found) != 1 {
		return netip.Addr{}, fmt.Errorf("-listen subnet %s holds %d local interface addresses, want 1", subnet, len(found))
	}
	return found[0], nil
}
