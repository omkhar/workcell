// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// egressProxyGuardedDigest is the SHA-256 of the reviewed definitions of
// start_egress_proxy and egress_proxy_agent_network_args, header line and
// closing brace included, joined by the separator that guardedFunctionsDigest uses. The parser rules below name the
// rule that failed, but a static check cannot prove that nothing else runs:
// bash assembles commands across quotes, loops and later definitions, and every
// spelling needs its own rule. The digest closes the class, as it does for the
// Colima egress script. Any edit of either body, in any spelling, fails until a
// reviewer regenerates it. The dry-run and Colima smoke scenarios then check
// the behavior of the reviewed functions.
const egressProxyGuardedDigest = "e17b4a4ca3eb718af1640b1dd339deeb810b5096b48d0535fefe523ffdfc0956"

var egressProxyGuardedFunctions = []string{"start_egress_proxy", "egress_proxy_agent_network_args"}

// guardedFunctionsDigest returns the digest of the guarded function bodies.
func guardedFunctionsDigest(script string) string {
	var bodies []string
	for _, name := range egressProxyGuardedFunctions {
		bodies = append(bodies, strings.Join(functionDefinition(script, name), "\n"))
	}
	sum := sha256.Sum256([]byte(strings.Join(bodies, "\n--\n")))
	return hex.EncodeToString(sum[:])
}

// checkGuardedFunctions requires one definition of each guarded function, so
// that a later definition cannot replace the reviewed one, and the reviewed
// bodies. label names the validator in the error.
func checkGuardedFunctions(script, label string) error {
	for _, name := range egressProxyGuardedFunctions {
		definition := regexp.MustCompile(`(?m)^[ \t]*(?:function[ \t]+` + name + `\b|` + name + `[ \t]*\([ \t]*\))`)
		if count := len(definition.FindAllString(script, -1)); count != 1 {
			return fmt.Errorf("Expected exactly one definition of %s for the %s, found %d", name, label, count)
		}
	}
	if got := guardedFunctionsDigest(script); got != egressProxyGuardedDigest {
		return fmt.Errorf("Expected the %s functions to equal the reviewed functions (digest %s)", label, got)
	}
	return nil
}

// egressProxySidecarCreate is the exact argument vector of the sidecar `docker
// create`, after the command words. Equality closes the class that a deny list
// leaves open: a later `--pids-limit=-1`, `--read-only=false`, `-m`, a second
// `--network` or any flag the list never named changes the vector and fails.
// An intended change to the command updates this vector in the same review.
var egressProxySidecarCreate = []string{
	"--name", "${EGRESS_PROXY_CONTAINER}",
	"--network", "${EGRESS_PROXY_NETWORK}",
	"--user", "65532:65532",
	"--cap-drop", "ALL",
	"--security-opt", "no-new-privileges:true",
	"--read-only",
	"--pids-limit", "256",
	"--memory", "256m",
	"--log-driver", "json-file",
	"--log-opt", "max-size=10m",
	"--sysctl", "net.ipv4.ip_unprivileged_port_start=0",
	"--entrypoint", "/usr/local/libexec/workcell/workcell-egress-proxy",
	"${image_id}", "-allow", "${ALLOW_ENDPOINTS}", "-listen", "${subnet}",
	">/dev/null",
}

// ValidateEgressProxySidecar requires start_egress_proxy in the launcher to
// create the internal network once and run one `docker create` whose argument
// vector equals the reviewed vector. The
// shared shell-invocation parser reads the function body, so a comment, a
// heredoc body, a quoted decoy or an unreached branch does not count as the
// command. The body is cut out first because the parser skips function bodies.
func ValidateEgressProxySidecar(script string) error {
	body := functionBody(script, "start_egress_proxy")
	networks := ShellInvocations(body, `run_workcell_docker_client_command ${HOST_DOCKER_BIN} network create`)
	if len(networks) != 1 || !slices.Equal(networks[0].Args, []string{"--internal", "${EGRESS_PROXY_NETWORK}", ">/dev/null"}) {
		return errors.New("Expected the egress proxy sidecar network create command to equal the reviewed internal network command")
	}
	creates := ShellInvocations(body, `run_workcell_docker_client_command ${HOST_DOCKER_BIN} create`)
	if len(creates) != 1 || !slices.Equal(creates[0].Args, egressProxySidecarCreate) {
		return errors.New("Expected the egress proxy sidecar create command to equal the reviewed hardened command")
	}
	return checkGuardedFunctions(script, "egress proxy sidecar")
}

// ValidateEgressProxySoleRoute requires egress_proxy_agent_network_args to
// select the per-session network with no resolver, once. The parser skips the
// loop body that adds the host aliases, so the array name may appear only in the
// assignment and in the one alias line: an appended `+=(--network ...)`, a
// second assignment or a decoy mention fails closed. The dry-run scenario proves
// the aliases at run time.
func ValidateEgressProxySoleRoute(script string) error {
	const fail = "Expected the egress proxy sole route to put the agent on the per-session network with no resolver"
	body := functionBody(script, "egress_proxy_agent_network_args")
	networks := ShellInvocations(body, `EGRESS_PROXY_NETWORK=wc-${SESSION_ID}`)
	routes := ShellInvocations(body, `RUNTIME_NETWORK_ARGS=(--network`)
	if len(networks) != 1 || len(networks[0].Args) != 0 ||
		len(routes) != 1 || !slices.Equal(routes[0].Args, []string{"${EGRESS_PROXY_NETWORK}", "--dns", "127.0.0.1)"}) ||
		strings.Count(body, "RUNTIME_NETWORK_ARGS") != 2 ||
		strings.Count(body, `RUNTIME_NETWORK_ARGS+=(--add-host "${endpoint%:*}:${EGRESS_PROXY_IP_TOKEN}")`) != 1 {
		return errors.New(fail)
	}
	return checkGuardedFunctions(script, "egress proxy sole route")
}

// functionDefinition returns the lines of the function from the `name()` header
// through the closing brace at the start of a line, both included, or "" when
// the function is absent. The header line is part of the definition: a command
// after the opening brace runs with the function.
func functionDefinition(script, name string) []string {
	var lines []string
	inBlock := false
	for line := range strings.Lines(script) {
		line = strings.TrimSuffix(line, "\n")
		if !inBlock {
			if !strings.HasPrefix(line, name+"()") {
				continue
			}
			inBlock = true
		}
		lines = append(lines, line)
		if len(lines) > 1 && strings.HasPrefix(line, "}") {
			break
		}
	}
	return lines
}

// functionBody returns the lines between the header and the closing brace, for
// the parser, which skips a function body it meets as a definition.
func functionBody(script, name string) string {
	lines := functionDefinition(script, name)
	if len(lines) < 2 {
		return ""
	}
	return strings.Join(lines[1:len(lines)-1], "\n")
}
