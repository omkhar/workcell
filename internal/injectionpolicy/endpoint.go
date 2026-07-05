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
// validate_endpoint so every injection-policy engine accepts the same grammar
// the helper does — with one tightening: an IP-shaped host (bracketed or bare
// dotted-numeric) must parse as a real IP (below). All operator endpoints pass
// this validator first, so a malformed literal never reaches iptables.
var (
	ipv6EndpointPattern = regexp.MustCompile(`^\[([0-9A-Fa-f:.]+)\]:([0-9]{1,5})$`)
	hostEndpointPattern = regexp.MustCompile(`^([^:]+):([0-9]{1,5})$`)
	hostLabelPattern    = regexp.MustCompile(`^[A-Za-z0-9.-]+$`)
	// A host made only of digits and dots is an attempted dotted IPv4 literal,
	// which the enforcement helper routes to `iptables -d` as an address rather
	// than resolving via DNS. Such a host must therefore be a real IPv4.
	ipv4ShapedPattern = regexp.MustCompile(`^[0-9.]+$`)
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
		// A bracketed host must be a real IPv6 literal (the helper passes it to
		// ip6tables -d): reject IPv4-in-brackets and garbage here, fail-closed.
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
		// A dotted-numeric host is an attempted IPv4 literal; require a real IPv4.
		if ipv4ShapedPattern.MatchString(host) && net.ParseIP(host) == nil {
			return fmt.Errorf("%s has an invalid endpoint host: %q", label, endpoint)
		}
	}
	portNum, err := strconv.Atoi(port)
	if err != nil || portNum < 1 || portNum > 65535 {
		return fmt.Errorf("%s has an invalid endpoint port: %q", label, endpoint)
	}
	return nil
}
