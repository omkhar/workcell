// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package main

import (
	"errors"
	"flag"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omkhar/workcell/internal/cliexit"
)

func TestRunFailsClosedOnBadConfig(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	shared := filepath.Join(dir, "shared")
	if err := os.WriteFile(shared, []byte("a.example:5432\nb.example:5432\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct {
		args []string
		want string
	}{
		"no allowlist":      {nil, "exactly one of -allowlist or -allow"},
		"both allowlists":   {[]string{"-allowlist", shared, "-allow", "a.example:443"}, "exactly one of -allowlist or -allow"},
		"inline IP literal": {[]string{"-allow", "a.example:443 192.0.2.1:443"}, "IP literal"},
		"missing file":      {[]string{"-allowlist", filepath.Join(dir, "missing")}, "no such file"},
		"shared plain port": {[]string{"-allowlist", shared}, "more than one host"},
		"extra argument":    {[]string{"-allowlist", shared, "typo"}, "unexpected argument"},
		"bad listen":        {[]string{"-allowlist", shared, "-listen", "bad/address"}, "-listen must be an IP address"},
		"unknown flag":      {[]string{"-bogus"}, "flag provided but not defined"},
		"no subnet address": {[]string{"-allowlist", shared, "-listen", "192.0.2.0/24"}, "holds 0 local interface addresses"},
	}
	for name, tc := range cases {
		err := run(tc.args, io.Discard, io.Discard)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: run error = %v, want %q", name, err, tc.want)
		}
		if ec, ok := cliexit.IsExitCodeError(err); !ok || ec.Code != 2 {
			t.Errorf("%s: run error = %v, want exit code 2", name, err)
		}
	}
}

func TestRunReportsHelpAsErrHelp(t *testing.T) {
	t.Parallel()
	if err := run([]string{"-h"}, io.Discard, io.Discard); !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("run(-h) error = %v, want flag.ErrHelp", err)
	}
}

func TestListenAddrPicksTheOneAddressInTheSubnet(t *testing.T) {
	t.Parallel()
	ipnet := func(cidr string) *net.IPNet {
		ip, n, err := net.ParseCIDR(cidr)
		if err != nil {
			t.Fatal(err)
		}
		n.IP = ip
		return n
	}
	// The sidecar has an internal-network address and a bridge address.
	ifaces := []net.Addr{ipnet("127.0.0.1/8"), ipnet("172.18.0.2/16"), ipnet("172.17.0.3/16"), ipnet("fe80::1/64")}
	cases := map[string]string{
		"172.18.0.0/16": "172.18.0.2",
		"172.17.0.0/16": "172.17.0.3",
		"10.1.2.3":      "10.1.2.3",
		"0.0.0.0":       "0.0.0.0",
	}
	for value, want := range cases {
		got, err := listenAddr(value, ifaces)
		if err != nil || got.String() != want {
			t.Errorf("listenAddr(%q) = %v, %v; want %s", value, got, err, want)
		}
	}
	for _, value := range []string{"172.16.0.0/12", "192.0.2.0/24", "bad/address"} {
		if got, err := listenAddr(value, ifaces); err == nil {
			t.Errorf("listenAddr(%q) = %v, want an error", value, got)
		}
	}
}
