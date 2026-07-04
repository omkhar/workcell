// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package injection

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// renderNetwork parses the optional top-level `[network]` injection-policy
// table and returns the operator-declared allow/deny endpoint lists.
//
// The `[network]` surface can ONLY contribute endpoint lists.  It has no path
// to NETWORK_POLICY, the enforcement mode, or the default-deny allowlist
// mechanism: renderNetwork returns endpoint slices and nothing else, and the
// shell caller uses `allow_endpoints` to EXTEND the computed allowlist and
// `deny_endpoints` to TIGHTEN it (deny wins).  There is deliberately no return
// value or side effect through which a policy could disable the allowlist or
// switch to an unrestricted posture — the no-weakening invariant for A1.
//
// Every endpoint is validated to the SAME host:port / [ipv6]:port grammar the
// enforcement helper scripts/colima-egress-allowlist.sh applies
// (validate_endpoint, lines ~186-231), so an endpoint this policy accepts is
// one the helper will also accept.  Validation is fail-closed: any malformed
// endpoint, empty string, unknown key under `[network]`, or non-array value
// aborts the render with an error that names the offending value.
func renderNetwork(policy map[string]any) (allowEndpoints, denyEndpoints []string, err error) {
	raw, ok := policy["network"]
	if !ok || raw == nil {
		return []string{}, []string{}, nil
	}
	network, ok := raw.(map[string]any)
	if !ok {
		return nil, nil, errors.New("network must be a TOML table")
	}
	if err := validateAllowedKeys(network, mapKeysSet([]string{"allow_endpoints", "deny_endpoints"}), "network"); err != nil {
		return nil, nil, err
	}
	allowEndpoints, err = parseNetworkEndpointList(network, "allow_endpoints")
	if err != nil {
		return nil, nil, err
	}
	denyEndpoints, err = parseNetworkEndpointList(network, "deny_endpoints")
	if err != nil {
		return nil, nil, err
	}
	return allowEndpoints, denyEndpoints, nil
}

// parseNetworkEndpointList reads one `[network]` list key, validates every
// element against the enforcement-helper grammar, and returns the endpoints
// de-duplicated and sorted.  A missing key yields an empty (non-nil) slice.
func parseNetworkEndpointList(network map[string]any, key string) ([]string, error) {
	raw, ok := network[key]
	if !ok || raw == nil {
		return []string{}, nil
	}
	items, err := networkEndpointArray(raw, "network."+key)
	if err != nil {
		return nil, err
	}
	set := map[string]struct{}{}
	for _, endpoint := range items {
		if err := validateEgressEndpoint(endpoint, "network."+key); err != nil {
			return nil, err
		}
		set[endpoint] = struct{}{}
	}
	return sortedKeys(set), nil
}

// networkEndpointArray requires the value to be an array of strings; a scalar
// (or an array containing a non-string element) is rejected so a `[network]`
// key can never smuggle a non-endpoint value into the allowlist computation.
func networkEndpointArray(raw any, label string) ([]string, error) {
	switch typed := raw.(type) {
	case []string:
		return append([]string(nil), typed...), nil
	case []any:
		items := make([]string, 0, len(typed))
		for _, item := range typed {
			value, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("%s must be an array of host:port strings; found non-string element: %v", label, item)
			}
			items = append(items, value)
		}
		return items, nil
	default:
		return nil, fmt.Errorf("%s must be an array of host:port strings", label)
	}
}

// mergeEndpointLists returns the union of the supplied endpoint lists,
// de-duplicated and sorted.  Used to fold `[network].allow_endpoints` into the
// credential-derived extra endpoints WITHOUT clobbering either source.
func mergeEndpointLists(lists ...[]string) []string {
	set := map[string]struct{}{}
	for _, list := range lists {
		for _, endpoint := range list {
			if endpoint == "" {
				continue
			}
			set[endpoint] = struct{}{}
		}
	}
	return sortedKeys(set)
}

// The three patterns below mirror scripts/colima-egress-allowlist.sh's
// validate_endpoint (lines ~191-226) byte-for-byte so the Go policy gate and
// the shell enforcement helper accept exactly the same endpoint grammar.
var (
	ipv6EndpointPattern = regexp.MustCompile(`^\[([0-9A-Fa-f:.]+)\]:([0-9]{1,5})$`)
	hostEndpointPattern = regexp.MustCompile(`^([^:]+):([0-9]{1,5})$`)
	hostLabelPattern    = regexp.MustCompile(`^[A-Za-z0-9.-]+$`)
	ipv6BodyPattern     = regexp.MustCompile(`^[0-9A-Fa-f:.]+$`)
)

// validateEgressEndpoint enforces the enforcement-helper grammar: a
// bracketed `[ipv6]:port` form, or a `host:port` form whose host matches
// ^[A-Za-z0-9.-]+$, does not start with a dot, and contains no `..`; the port
// must be a 1-5 digit number in the 1-65535 range.  Errors name the offending
// endpoint so an operator can see exactly which value was rejected.
func validateEgressEndpoint(endpoint, label string) error {
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
		ipv6Host := host[1 : len(host)-1]
		if !ipv6BodyPattern.MatchString(ipv6Host) {
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
