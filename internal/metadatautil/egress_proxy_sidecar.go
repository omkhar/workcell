// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"errors"
	"slices"
	"strings"
)

// egressProxySidecarFlags are the argument-vector tokens the sidecar `docker
// create` must carry, each exactly once, in the spelling the launcher uses.
var egressProxySidecarFlags = [][]string{
	{"--name", "${EGRESS_PROXY_CONTAINER}"},
	{"--network", "${EGRESS_PROXY_NETWORK}"},
	{"--user", "65532:65532"},
	{"--cap-drop", "ALL"},
	{"--security-opt", "no-new-privileges:true"},
	{"--read-only"},
	{"--pids-limit", "256"},
	{"--memory", "256m"},
	{"--log-driver", "json-file"},
	{"--log-opt", "max-size=10m"},
	{"--entrypoint", "/usr/local/libexec/workcell/workcell-egress-proxy"},
	{"-listen", "${subnet}"},
}

// ValidateEgressProxySidecar requires start_egress_proxy in the launcher to
// run one `docker create` whose argument vector carries each hardening flag
// once, with no privilege-widening flag and no second spelling of a flag. The
// shared shell-invocation parser reads the function body, so a comment, a
// heredoc body, a quoted decoy or an unreached branch does not count as the
// command. The body is cut out first because the parser skips function bodies.
func ValidateEgressProxySidecar(script string) error {
	const fail = "Expected the egress proxy sidecar create command to carry its hardening flags"
	body := functionBody(script, "start_egress_proxy")
	creates := ShellInvocations(body, `run_workcell_docker_client_command ${HOST_DOCKER_BIN} create`)
	if len(creates) != 1 {
		return errors.New(fail)
	}
	args := creates[0].Args
	for _, flag := range egressProxySidecarFlags {
		if countRun(args, flag) != 1 || countToken(args, flag[0]) != 1 {
			return errors.New(fail)
		}
	}
	for _, token := range args {
		switch {
		case token == "--privileged", token == "--pid", token == "--ipc", token == "--uts",
			token == "--cap-add", token == "--volume", token == "-v", token == "--mount",
			token == "--dns", token == "--publish", token == "-p",
			strings.HasPrefix(token, "--network="), strings.HasPrefix(token, "--user="),
			strings.HasPrefix(token, "--cap-add="), strings.HasPrefix(token, "--security-opt="):
			return errors.New(fail)
		}
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

// countRun counts the places where the tokens in run appear next to each other.
func countRun(args, run []string) int {
	count := 0
	for i := 0; i+len(run) <= len(args); i++ {
		if slices.Equal(args[i:i+len(run)], run) {
			count++
		}
	}
	return count
}

func countToken(args []string, token string) int {
	count := 0
	for _, arg := range args {
		if arg == token {
			count++
		}
	}
	return count
}
