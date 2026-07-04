// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package injectionpolicy

import (
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
)

// The patterns below mirror scripts/colima-egress-allowlist.sh's
// validate_endpoint (lines ~191-226) so every injection-policy engine
// (internal/authpolicy validate/show, internal/authresolve, and
// internal/injection render) accepts the same egress-endpoint grammar the
// enforcement helper accepts — with one deliberate tightening: a bracketed host
// must parse as a real IPv6 literal (below), which the helper's char-set-only
// check does not verify. Since all operator endpoints pass through this
// validator before reaching the helper, that keeps a malformed bracketed
// literal from ever reaching `ip6tables`.
var (
	ipv6EndpointPattern = regexp.MustCompile(`^\[([0-9A-Fa-f:.]+)\]:([0-9]{1,5})$`)
	hostEndpointPattern = regexp.MustCompile(`^([^:]+):([0-9]{1,5})$`)
	hostLabelPattern    = regexp.MustCompile(`^[A-Za-z0-9.-]+$`)
)

// ValidateEgressEndpoint enforces the enforcement-helper grammar: a bracketed
// `[ipv6]:port` form, or a `host:port` form whose host matches
// ^[A-Za-z0-9.-]+$, does not start with a dot, and contains no `..`; the port
// must be a 1-5 digit number in the 1-65535 range. Errors name the offending
// endpoint so an operator can see exactly which value was rejected. It is
// fail-closed: any input that is not an exact match is rejected.
func ValidateEgressEndpoint(endpoint, label string) error {
	var host, port string
	if m := ipv6EndpointPattern.FindStringSubmatch(endpoint); m != nil {
		host = "[" + m[1] + "]"
		port = m[2]
	} else if m := hostEndpointPattern.FindStringSubmatch(endpoint); m != nil {
		host = m[1]
		port = m[2]
	}
	if host == "" {
		return fmt.Errorf("%s has an invalid endpoint host: %q", label, endpoint)
	}
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		// A bracketed host must be a real IPv6 literal: parse it and reject an
		// IPv4-in-brackets ([1.2.3.4]) or garbage ([::::]). The enforcement
		// helper passes bracketed hosts straight to `ip6tables -d`, which
		// requires a valid IPv6 address, so validating the literal here keeps
		// policy-acceptance fail-closed rather than failing mid-launch.
		ipv6Host := host[1 : len(host)-1]
		if ip := net.ParseIP(ipv6Host); ip == nil || ip.To4() != nil {
			return fmt.Errorf("%s has an invalid endpoint host: %q", label, endpoint)
		}
	} else {
		if !hostLabelPattern.MatchString(host) {
			return fmt.Errorf("%s has an invalid endpoint host: %q", label, endpoint)
		}
		if strings.HasPrefix(host, ".") {
			return fmt.Errorf("%s has an invalid endpoint host: %q", label, endpoint)
		}
		if strings.Contains(host, "..") {
			return fmt.Errorf("%s has an invalid endpoint host: %q", label, endpoint)
		}
	}
	portNum, err := strconv.Atoi(port)
	if err != nil || portNum < 1 || portNum > 65535 {
		return fmt.Errorf("%s has an invalid endpoint port: %q", label, endpoint)
	}
	return nil
}
