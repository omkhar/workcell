// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package launcher

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func ExtractDockerfileArg(dockerfilePath, argName string) (string, error) {
	content, err := os.ReadFile(dockerfilePath)
	if err != nil {
		return "", err
	}
	argPattern := regexp.MustCompile(`(?m)^\s*ARG ` + regexp.QuoteMeta(argName) + `=(.+)$`)
	match := argPattern.FindStringSubmatch(string(content))
	if match == nil {
		return "", errors.New("unable to extract " + argName + " from Dockerfile")
	}
	return strings.TrimSpace(match[1]), nil
}

func ExtractCodexVersion(dockerfilePath string) (string, error) {
	return ExtractDockerfileArg(dockerfilePath, "CODEX_VERSION")
}

// ValidateSecurityOptions parses the JSON returned by
// `docker info --format '{{json .SecurityOptions}}'` and confirms the
// daemon advertises BOTH seccomp AND a kernel mandatory-access-control
// framework (AppArmor on Ubuntu/colima, SELinux on Fedora/RHEL).  Both
// are required for the workcell container hardening contract.
func parseDockerSecurityOptions(raw string) (hasSeccomp, hasMAC bool, err error) {
	var options []any
	if err := json.Unmarshal([]byte(raw), &options); err != nil {
		return false, false, err
	}
	for _, option := range options {
		s, ok := option.(string)
		if !ok {
			continue
		}
		switch {
		case strings.HasPrefix(s, "name=seccomp"):
			hasSeccomp = true
		case strings.HasPrefix(s, "name=apparmor"), strings.HasPrefix(s, "name=selinux"):
			hasMAC = true
		}
	}
	return hasSeccomp, hasMAC, nil
}

func ValidateSecurityOptions(raw string) error {
	hasSeccomp, hasMAC, err := parseDockerSecurityOptions(raw)
	if err != nil {
		return err
	}
	if !hasSeccomp {
		return errors.New("managed runtime requires Docker seccomp support to stay active")
	}
	if !hasMAC {
		return errors.New("managed runtime requires Docker AppArmor or SELinux support to stay active")
	}
	return nil
}

// ValidateCompatSecurityOptions applies the lower-assurance local_compat
// daemon posture check. Docker Desktop for macOS advertises seccomp but not
// AppArmor/SELinux; accepting it here is the explicit compat distinction, not
// a strict-target relaxation.
func ValidateCompatSecurityOptions(raw string) error {
	hasSeccomp, _, err := parseDockerSecurityOptions(raw)
	if err != nil {
		return err
	}
	if !hasSeccomp {
		return errors.New("compat runtime requires Docker seccomp support to stay active")
	}
	return nil
}

// ValidateContainerSecurityOptions inspects the JSON value of a running
// container's HostConfig.SecurityOpt (from
// `docker inspect --format '{{json .HostConfig.SecurityOpt}}'`) and
// fails closed if anything overrides the workcell hardening contract:
// no-new-privileges must remain on, and no caller may have downgraded
// seccomp or AppArmor/SELinux to "unconfined".  Used as a post-launch
// defense-in-depth check that the running container still matches the
// pre-launch daemon posture.
func ValidateContainerSecurityOptions(raw string) error {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || trimmed == "null" {
		return errors.New("managed runtime container is missing explicit HostConfig.SecurityOpt; expected no-new-privileges:true")
	}
	var options []any
	if err := json.Unmarshal([]byte(trimmed), &options); err != nil {
		return err
	}
	hasNoNewPrivileges := false
	for _, option := range options {
		s, ok := option.(string)
		if !ok {
			return errors.New("managed runtime container HostConfig.SecurityOpt entries must be strings")
		}
		switch {
		case s == "no-new-privileges:true":
			hasNoNewPrivileges = true
		case s == "no-new-privileges:false":
			return errors.New("managed runtime container must not disable no-new-privileges")
		case s == "seccomp=unconfined":
			return errors.New("managed runtime container must not run with seccomp=unconfined")
		case s == "apparmor=unconfined", s == "apparmor:unconfined":
			return errors.New("managed runtime container must not run with apparmor unconfined")
		case strings.HasPrefix(s, "label=disable"):
			return errors.New("managed runtime container must not disable SELinux labeling")
		}
	}
	if !hasNoNewPrivileges {
		return errors.New("managed runtime container must run with no-new-privileges:true")
	}
	return nil
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

// SubtractEndpointList removes every endpoint present in deny from allow,
// preserving allow's order.  This implements the A1 operator-tightening
// semantics: a policy's [network].deny_endpoints wins over the host-computed
// allowlist, so an endpoint an operator denies is removed even when a provider
// would otherwise require it.  The function only ever REMOVES endpoints; it
// can never add one, so it cannot weaken the allowlist.
func SubtractEndpointList(allow, deny string) string {
	// Compare on a canonical key so any deny that is semantically equal to a
	// computed endpoint wins, regardless of surface form (host case, IPv6
	// literal spelling, or a port with leading zeros). Otherwise a deny like
	// CHATGPT.COM:443 or chatgpt.com:0443 would fail to remove the provider's
	// chatgpt.com:443 and silently leave a denied endpoint reachable.
	denied := make(map[string]struct{})
	for _, entry := range strings.Fields(deny) {
		denied[canonicalEndpoint(entry)] = struct{}{}
	}
	kept := make([]string, 0)
	for _, entry := range strings.Fields(allow) {
		if _, ok := denied[canonicalEndpoint(entry)]; ok {
			continue
		}
		kept = append(kept, entry)
	}
	return strings.Join(kept, " ")
}

// canonicalEndpoint returns a normalized host:port key for equality comparison:
// the host is lower-cased and, when it is an IP literal, rendered in its
// canonical form; the port has any leading zeros stripped. Inputs that are not
// valid host:port fall back to a lower-cased copy so comparison stays total.
func canonicalEndpoint(endpoint string) string {
	host, port, err := net.SplitHostPort(endpoint)
	if err != nil {
		return strings.ToLower(endpoint)
	}
	host = strings.ToLower(host)
	if ip := net.ParseIP(host); ip != nil {
		host = ip.String()
	}
	if n, err := strconv.Atoi(port); err == nil {
		port = strconv.Itoa(n)
	}
	return net.JoinHostPort(host, port)
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
