// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package hostutil

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"regexp"
	"strings"
	"time"
)

var codexVersionPattern = regexp.MustCompile(`(?m)^\s*ARG CODEX_VERSION=(.+)$`)

func ExtractCodexVersion(dockerfilePath string) (string, error) {
	content, err := os.ReadFile(dockerfilePath)
	if err != nil {
		return "", err
	}
	match := codexVersionPattern.FindStringSubmatch(string(content))
	if match == nil {
		return "", errors.New("unable to extract CODEX_VERSION from Dockerfile")
	}
	return strings.TrimSpace(match[1]), nil
}

func ValidateSecurityOptions(raw string) error {
	var options []any
	if err := json.Unmarshal([]byte(raw), &options); err != nil {
		return err
	}
	for _, option := range options {
		if s, ok := option.(string); ok && strings.HasPrefix(s, "name=seccomp") {
			return nil
		}
	}
	return errors.New("managed runtime requires Docker seccomp support to stay active")
}

func CanonicalizeToolPath(candidate string) (string, error) {
	if candidate == "" {
		return "", nil
	}
	return CanonicalizePath(candidate)
}

func DedupeEndpointList(raw string) string {
	seen := make(map[string]struct{})
	ordered := make([]string, 0)
	for _, entry := range strings.Fields(raw) {
		if entry == "" {
			continue
		}
		if _, ok := seen[entry]; ok {
			continue
		}
		seen[entry] = struct{}{}
		ordered = append(ordered, entry)
	}
	return strings.Join(ordered, " ")
}

func ResolveEndpoints(raw string) ([]string, error) {
	endpoints := strings.Fields(raw)
	results := make([]string, 0)
	for _, endpoint := range endpoints {
		host, _, ok := strings.Cut(endpoint, ":")
		if !ok {
			continue
		}
		if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
			continue
		}
		if isNumericHost(host) {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		cancel()
		if err != nil {
			continue
		}
		seen := map[string]struct{}{}
		var ipv4Addrs []string
		var ipv6Addrs []string
		for _, addr := range addrs {
			ip := addr.IP.String()
			if ip == "" {
				continue
			}
			if _, ok := seen[ip]; ok {
				continue
			}
			seen[ip] = struct{}{}
			if addr.IP.To4() != nil {
				ipv4Addrs = append(ipv4Addrs, ip)
			} else {
				ipv6Addrs = append(ipv6Addrs, ip)
			}
		}
		for _, ip := range append(ipv4Addrs, ipv6Addrs...) {
			results = append(results, host+"\t"+ip)
		}
	}
	return results, nil
}

func isNumericHost(host string) bool {
	if host == "" {
		return false
	}
	return strings.Trim(host, ".") != "" && strings.IndexFunc(host, func(r rune) bool {
		return (r < '0' || r > '9') && r != '.'
	}) == -1
}
