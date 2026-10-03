// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omkhar/workcell/internal/metadatautil"
)

// TestValidateEgressProxySidecarRejectsEvasions runs the shared evasion corpus
// against the real sidecar create command and mutates its flags.
func TestValidateEgressProxySidecarRejectsEvasions(t *testing.T) {
	script, err := os.ReadFile(filepath.Join("..", "..", "scripts", "lib", "launcher", "egress-endpoints.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if err := metadatautil.ValidateEgressProxySidecar(string(script)); err != nil {
		t.Fatalf("real script rejected: %v", err)
	}
	start := strings.Index(string(script), "  run_workcell_docker_client_command \"${HOST_DOCKER_BIN}\" create \\\n")
	end := strings.Index(string(script)[start:], ">/dev/null || return 1\n")
	anchor := string(script)[start : start+end+len(">/dev/null || return 1")]
	RequireRejectsAllEvasions(t, string(script), anchor, "egress proxy sidecar", metadatautil.ValidateEgressProxySidecar)
	network := `  run_workcell_docker_client_command "${HOST_DOCKER_BIN}" network create --internal "${EGRESS_PROXY_NETWORK}" >/dev/null || return 1`
	RequireRejectsAllEvasions(t, string(script), network, "egress proxy sidecar", metadatautil.ValidateEgressProxySidecar)
	for name, replacement := range map[string]string{
		"non-internal network": strings.Replace(network, " --internal", "", 1),
		"echoed internal":      `  echo "network create --internal"` + "\n" + strings.Replace(network, " --internal", "", 1),
		"second network":       network + "\n" + strings.Replace(network, " --internal", "", 1),
		"other network":        strings.Replace(network, "${EGRESS_PROXY_NETWORK}", "bridge", 1),
	} {
		t.Run(name, func(t *testing.T) {
			mutated := strings.Replace(string(script), network, replacement, 1)
			if err := metadatautil.ValidateEgressProxySidecar(mutated); err == nil {
				t.Fatalf("validator accepted %s", name)
			}
		})
	}

	for name, mutate := range map[string]func(string) string{
		"dropped read-only": func(a string) string { return strings.Replace(a, "    --read-only \\\n", "", 1) },
		"second network": func(a string) string {
			return strings.Replace(a, "    --read-only \\\n", "    --read-only \\\n    --network=host \\\n", 1)
		},
		"second user": func(a string) string {
			return strings.Replace(a, "    --read-only \\\n", "    --read-only \\\n    --user 0 \\\n", 1)
		},
		"added capability": func(a string) string {
			return strings.Replace(a, "    --read-only \\\n", "    --read-only \\\n    --cap-add NET_ADMIN \\\n", 1)
		},
		"privileged": func(a string) string {
			return strings.Replace(a, "    --read-only \\\n", "    --read-only \\\n    --privileged \\\n", 1)
		},
		"wildcard listen": func(a string) string { return strings.Replace(a, `-listen "${subnet}"`, `-listen 0.0.0.0`, 1) },
		"bind mount": func(a string) string {
			return strings.Replace(a, "    --read-only \\\n", "    --read-only \\\n    -v /:/host \\\n", 1)
		},
		"echoed flag decoy": func(a string) string { return strings.Replace(a, "    --read-only \\\n", "    --read-only-x \\\n", 1) },
		"pids unlimited": func(a string) string {
			return strings.Replace(a, "    --read-only \\\n", "    --read-only \\\n    --pids-limit=-1 \\\n", 1)
		},
		"read-only false": func(a string) string {
			return strings.Replace(a, "    --read-only \\\n", "    --read-only=false \\\n", 1)
		},
		"entrypoint override": func(a string) string {
			return strings.Replace(a, "    --read-only \\\n", "    --read-only \\\n    --entrypoint=/bin/sh \\\n", 1)
		},
		"memory alias": func(a string) string {
			return strings.Replace(a, "    --read-only \\\n", "    --read-only \\\n    -m 4g \\\n", 1)
		},
		"unbounded log":       func(a string) string { return strings.Replace(a, "    --log-opt max-size=10m \\\n", "", 1) },
		"weaker security opt": func(a string) string { return strings.Replace(a, "no-new-privileges:true", "seccomp=unconfined", 1) },
	} {
		t.Run(name, func(t *testing.T) {
			mutated := strings.Replace(string(script), anchor, mutate(anchor), 1)
			if mutated == string(script) {
				t.Fatal("mutation changed nothing")
			}
			if err := metadatautil.ValidateEgressProxySidecar(mutated); err == nil {
				t.Fatalf("validator accepted %s", name)
			}
		})
	}
}
