// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package main

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omkhar/workcell/internal/cliexit"
	"github.com/omkhar/workcell/internal/egressproxy"
)

func TestRunFailsClosedOnBadConfig(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	shared := filepath.Join(dir, "shared")
	if err := os.WriteFile(shared, []byte("a.example:5432\nb.example:5432\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rule := "example.com=x-api-key=" + placeholder
	terminateArgs := []string{"-allow", "example.com:443", "-broker-socket", "/s", "-terminate", rule, "-ca-out", filepath.Join(dir, "ca.pem")}
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
		"bad rule":          {[]string{"-allow", "example.com:443", "-terminate", "example.com=x-api-key=secret"}, "placeholder must be"},
		"rule without ca":   {append(terminateArgs[:4:4], "-terminate", rule), "-terminate requires"},
		"ttl over a day":    {append(append([]string{}, terminateArgs...), "-ca-ttl", "25h"), "-ca-ttl must be"},
		"zero ttl":          {append(append([]string{}, terminateArgs...), "-ca-ttl", "0s"), "-ca-ttl must be"},
		"no broker client":  {append(append([]string{}, terminateArgs...), "-ca-ttl", "1h"), "no credential broker client"},
		"orphan ca-out":     {[]string{"-allow", "example.com:443", "-ca-out", filepath.Join(dir, "ca.pem")}, "require -terminate"},
		"orphan ca-ttl":     {[]string{"-allow", "example.com:443", "-ca-ttl", "1h"}, "require -terminate"},
		"orphan socket":     {[]string{"-allow", "example.com:443", "-broker-socket", "/s"}, "require -terminate"},
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

const placeholder = "wcph-0123456789abcdef0123456789abcdef"

type nopBroker struct{}

func (nopBroker) Lookup(context.Context, string, string) (string, error) { return "", nil }

func TestTerminateWritesConstrainedCA(t *testing.T) {
	t.Parallel()
	allow, err := egressproxy.ParseAllowlist("example.com:443 other.example:443", "test")
	if err != nil {
		t.Fatal(err)
	}
	dial := func(socket string) (egressproxy.Broker, error) {
		if socket != "/run/broker.sock" {
			t.Errorf("broker socket = %q", socket)
		}
		return nopBroker{}, nil
	}
	rules := []egressproxy.TerminateRule{{Host: "example.com", Header: "x-api-key", Placeholder: placeholder}}
	out := filepath.Join(t.TempDir(), "ca.pem")
	if err := terminate(egressproxy.New(allow, io.Discard), rules, out, time.Hour, "/run/broker.sock", dial); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		t.Fatalf("-ca-out is not PEM: %q", data)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if !cert.IsCA || len(cert.PermittedDNSDomains) != 1 || cert.PermittedDNSDomains[0] != "example.com" {
		t.Errorf("CA IsCA %v permitted %v, want only example.com", cert.IsCA, cert.PermittedDNSDomains)
	}
	if left := time.Until(cert.NotAfter); left > time.Hour || left < 59*time.Minute {
		t.Errorf("CA expires in %s, want the -ca-ttl of 1h", left)
	}

	offList := []egressproxy.TerminateRule{{Host: "absent.example", Header: "x-api-key", Placeholder: placeholder}}
	if err := terminate(egressproxy.New(allow, io.Discard), offList, out, time.Hour, "/run/broker.sock", dial); err == nil || !strings.Contains(err.Error(), "not on the port 443 allowlist") {
		t.Errorf("off-allowlist rule: error = %v", err)
	}
}

func TestRunReportsHelpAsErrHelp(t *testing.T) {
	t.Parallel()
	if err := run([]string{"-h"}, io.Discard, io.Discard); !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("run(-h) error = %v, want flag.ErrHelp", err)
	}
}
