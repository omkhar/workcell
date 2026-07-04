// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package injection

import (
	"errors"
	"fmt"

	"github.com/omkhar/workcell/internal/injectionpolicy"
)

// renderNetwork parses the optional top-level `[network]` table and returns only
// the operator-declared allow/deny endpoint slices — no path to NETWORK_POLICY or
// the enforcement mode. `allow_endpoints` EXTENDS the allowlist, `deny_endpoints`
// TIGHTENS it (deny wins) — the no-weakening invariant for A1. Validation is
// fail-closed (shared injectionpolicy.ValidateEgressEndpoint grammar; unknown
// keys / non-array values rejected), naming the offending value.
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
		if err := injectionpolicy.ValidateEgressEndpoint(endpoint, "network."+key); err != nil {
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
