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

// TestValidateEgressProxySoleRouteRejectsEvasions runs the shared evasion
// corpus against the network selection and the agent's route flags.
func TestValidateEgressProxySoleRouteRejectsEvasions(t *testing.T) {
	script, err := os.ReadFile(filepath.Join("..", "..", "scripts", "lib", "launcher", "egress-endpoints.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if err := metadatautil.ValidateEgressProxySoleRoute(string(script)); err != nil {
		t.Fatalf("real script rejected: %v", err)
	}
	network := `  EGRESS_PROXY_NETWORK="wc-${SESSION_ID}"`
	route := `  RUNTIME_NETWORK_ARGS=(--network "${EGRESS_PROXY_NETWORK}" --dns 127.0.0.1)`
	RequireRejectsAllEvasions(t, string(script), network, "egress proxy sole route", metadatautil.ValidateEgressProxySoleRoute)
	RequireRejectsAllEvasions(t, string(script), route, "egress proxy sole route", metadatautil.ValidateEgressProxySoleRoute)

	for name, mutate := range map[string]func(string) string{
		"bridge network": func(s string) string {
			return strings.Replace(s, route, strings.Replace(route, `"${EGRESS_PROXY_NETWORK}"`, "bridge", 1), 1)
		},
		"host network": func(s string) string { return strings.Replace(s, route, `  RUNTIME_NETWORK_ARGS=(--net host)`, 1) },
		"resolver":     func(s string) string { return strings.Replace(s, "--dns 127.0.0.1", "--dns 8.8.8.8", 1) },
		"extra route": func(s string) string {
			return strings.Replace(s, route, route+"\n  RUNTIME_NETWORK_ARGS=(--network bridge)", 1)
		},
		"shared network": func(s string) string { return strings.Replace(s, network, `  EGRESS_PROXY_NETWORK="bridge"`, 1) },
		"echoed decoy": func(s string) string {
			return strings.Replace(s, route, "  echo '"+strings.TrimSpace(route)+"'\n  RUNTIME_NETWORK_ARGS=(--network bridge)", 1)
		},
	} {
		t.Run(name, func(t *testing.T) {
			mutated := mutate(string(script))
			if mutated == string(script) {
				t.Fatal("mutation changed nothing")
			}
			if err := metadatautil.ValidateEgressProxySoleRoute(mutated); err == nil {
				t.Fatalf("validator accepted %s", name)
			}
		})
	}
}
