// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package testkit

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Codex 0.158.0 adds the `tcp-tunnel` subcommand (network tunnel). The managed
// session must reject it, and must keep rejecting `mcp-server`.
func TestCodexDeniesTunnelAndMCPServerSubcommands(t *testing.T) {
	t.Parallel()

	policy := filepath.Join(repoRoot(t), "runtime", "container", "provider-policy.sh")
	for _, sub := range []string{"tcp-tunnel", "mcp-server"} {
		cmd := exec.Command("bash", "-c", `source "$1"; reject_unsafe_codex_args "$2"`, "bash", policy, sub)
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatalf("expected %s to be rejected, got success: %s", sub, out)
		}
		if !strings.Contains(string(out), "Workcell blocked unsupported Codex CLI subcommand: "+sub) {
			t.Fatalf("unexpected rejection output for %s: %s", sub, out)
		}
	}
}
