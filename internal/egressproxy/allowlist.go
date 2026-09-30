// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

// Package egressproxy is the per-session egress proxy. It admits a TLS
// connection only when its SNI and port are on an exact allowlist, and it
// forwards a non-TLS port only to the one host that owns that port. It
// resolves every host itself and refuses non-public addresses.
package egressproxy

import (
	"fmt"
	"net"
	"net/netip"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/omkhar/workcell/internal/injectionpolicy"
)

const tlsPort = 443

// Allowlist is the exact (host, port) set the proxy admits.
type Allowlist struct {
	sni     map[string]bool   // hosts allowed as SNI on port 443
	forward map[uint16]string // each non-443 port to its single host
}

// LoadAllowlist reads an allowlist file with one host:port entry per line.
// Blank lines and text after '#' are ignored.
func LoadAllowlist(path string) (*Allowlist, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseAllowlist(string(data), path)
}

func parseAllowlist(text, label string) (*Allowlist, error) {
	a := &Allowlist{sni: map[string]bool{}, forward: map[uint16]string{}}
	for _, line := range strings.Split(text, "\n") {
		line, _, _ = strings.Cut(line, "#")
		for _, entry := range strings.Fields(line) {
			if err := injectionpolicy.ValidateEgressEndpoint(entry, label); err != nil {
				return nil, err
			}
			host, portText, err := net.SplitHostPort(entry)
			if err != nil {
				return nil, fmt.Errorf("%s has an invalid endpoint: %q", label, entry)
			}
			// The proxy routes by name: an IP literal has no SNI and no host alias.
			if _, err := netip.ParseAddr(host); err == nil {
				return nil, fmt.Errorf("%s has an IP literal endpoint: %q", label, entry)
			}
			host = strings.TrimSuffix(strings.ToLower(host), ".")
			port64, err := strconv.ParseUint(portText, 10, 16)
			if err != nil {
				return nil, fmt.Errorf("%s has an invalid endpoint port: %q", label, entry)
			}
			port := uint16(port64)
			if port == tlsPort {
				a.sni[host] = true
				continue
			}
			if prev, ok := a.forward[port]; ok && prev != host {
				return nil, fmt.Errorf("%s maps port %d to more than one host (%s, %s); only port 443 can share a port", label, port, prev, host)
			}
			a.forward[port] = host
		}
	}
	if len(a.sni) == 0 && len(a.forward) == 0 {
		return nil, fmt.Errorf("%s has no endpoints", label)
	}
	return a, nil
}

// Ports lists the ports the proxy must listen on, in ascending order.
func (a *Allowlist) Ports() []uint16 {
	var ports []uint16
	if len(a.sni) > 0 {
		ports = append(ports, tlsPort)
	}
	for port := range a.forward {
		ports = append(ports, port)
	}
	slices.Sort(ports)
	return ports
}
