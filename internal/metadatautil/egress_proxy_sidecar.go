// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"errors"
	"slices"
	"strings"
)

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
// run one `docker create` whose argument vector equals the reviewed vector. The
// shared shell-invocation parser reads the function body, so a comment, a
// heredoc body, a quoted decoy or an unreached branch does not count as the
// command. The body is cut out first because the parser skips function bodies.
func ValidateEgressProxySidecar(script string) error {
	body := functionBody(script, "start_egress_proxy")
	creates := ShellInvocations(body, `run_workcell_docker_client_command ${HOST_DOCKER_BIN} create`)
	if len(creates) != 1 || !slices.Equal(creates[0].Args, egressProxySidecarCreate) {
		return errors.New("Expected the egress proxy sidecar create command to equal the reviewed hardened command")
	}
	return nil
}

// ValidateEgressProxySoleRoute requires egress_proxy_agent_network_args to
// select the per-session network with no resolver, once. The parser skips the
// loop body that adds the host aliases, which the dry-run scenario proves.
func ValidateEgressProxySoleRoute(script string) error {
	const fail = "Expected the egress proxy sole route to put the agent on the per-session network with no resolver"
	body := functionBody(script, "egress_proxy_agent_network_args")
	networks := ShellInvocations(body, `EGRESS_PROXY_NETWORK=wc-${SESSION_ID}`)
	routes := ShellInvocations(body, `RUNTIME_NETWORK_ARGS=(--network`)
	if len(networks) != 1 || len(networks[0].Args) != 0 ||
		len(routes) != 1 || !slices.Equal(routes[0].Args, []string{"${EGRESS_PROXY_NETWORK}", "--dns", "127.0.0.1)"}) ||
		strings.Count(body, "RUNTIME_NETWORK_ARGS=(") != 1 {
		return errors.New(fail)
	}
	return nil
}

// functionBody returns the lines between the `name()` header and the closing
// brace at the start of a line, or "" when the function is absent.
func functionBody(script, name string) string {
	var body []string
	inBlock := false
	for line := range strings.Lines(script) {
		line = strings.TrimSuffix(line, "\n")
		switch {
		case !inBlock:
			inBlock = strings.HasPrefix(line, name+"()")
		case strings.HasPrefix(line, "}"):
			return strings.Join(body, "\n")
		default:
			body = append(body, line)
		}
	}
	return strings.Join(body, "\n")
}
