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

// TestValidateColimaEgressAtomicSwapRejectsEvasions runs the shared evasion
// corpus against both restore commands of the real apply plan.
func TestValidateColimaEgressAtomicSwapRejectsEvasions(t *testing.T) {
	script, err := os.ReadFile(filepath.Join("..", "..", "scripts", "colima-egress-allowlist.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if err := metadatautil.ValidateColimaEgressAtomicSwap(string(script)); err != nil {
		t.Fatalf("real script rejected: %v", err)
	}
	for _, anchor := range []string{
		`sudo iptables-restore --noflush <<<"${IPV4_RESTORE}"`,
		`sudo ip6tables-restore --noflush <<<"${IPV6_RESTORE}"`,
	} {
		t.Run(anchor, func(t *testing.T) {
			RequireRejectsAllEvasions(t, string(script), anchor, "one iptables-restore transaction",
				metadatautil.ValidateColimaEgressAtomicSwap)
		})
	}
	guard := "sudo iptables -C DOCKER-USER -j WORKCELL_EGRESS 2>/dev/null || sudo iptables -I DOCKER-USER 1 -j WORKCELL_EGRESS\n" +
		"sudo ip6tables -C DOCKER-USER -j WORKCELL_EGRESS6 2>/dev/null || sudo ip6tables -I DOCKER-USER 1 -j WORKCELL_EGRESS6"
	RequireRejectsAllEvasions(t, string(script), guard, "live chain linked",
		metadatautil.ValidateColimaEgressAtomicSwap)

	// A guard whose insert is swallowed leaves a new profile unlinked.
	unlinked := strings.Replace(string(script),
		"sudo iptables -C DOCKER-USER -j WORKCELL_EGRESS 2>/dev/null || sudo iptables -I DOCKER-USER 1 -j WORKCELL_EGRESS\nsudo ip6tables",
		"sudo iptables -C DOCKER-USER -j WORKCELL_EGRESS || true\nsudo ip6tables", 1)
	if err := metadatautil.ValidateColimaEgressAtomicSwap(unlinked); err == nil || !strings.Contains(err.Error(), "live chain linked") {
		t.Fatalf("swallowed insert error = %v, want live chain rejection", err)
	}

	// A commented copy of the guard must not cover a swallowed insert.
	decoy := strings.Replace(unlinked, "sudo iptables -C DOCKER-USER -j WORKCELL_EGRESS || true\n",
		"sudo iptables -C DOCKER-USER -j WORKCELL_EGRESS || true\n# sudo iptables -C DOCKER-USER -j WORKCELL_EGRESS 2>/dev/null || sudo iptables -I DOCKER-USER 1 -j WORKCELL_EGRESS\n", 1)
	if err := metadatautil.ValidateColimaEgressAtomicSwap(decoy); err == nil || !strings.Contains(err.Error(), "live chain linked") {
		t.Fatalf("decoy guard error = %v, want live chain rejection", err)
	}

	// The live chain must never be deleted or flushed, in any spelling.
	for name, hidden := range map[string]string{
		"quoted flush":        `sudo iptables "-F" WORKCELL_EGRESS`,
		"continued flush":     "sudo iptables \\\n  -F WORKCELL_EGRESS",
		"ip6 delete":          "sudo ip6tables -D DOCKER-USER -j WORKCELL_EGRESS6",
		"chain delete":        "sudo iptables -X WORKCELL_EGRESS",
		"wait before flush":   "sudo iptables -w -F WORKCELL_EGRESS",
		"long flush":          "sudo iptables --flush WORKCELL_EGRESS",
		"flush without chain": "sudo ip6tables -F",
		"escaped flush":       `sudo iptables -\F WORKCELL_EGRESS`,
		"cluster flush":       "sudo iptables -wF WORKCELL_EGRESS",
		"abbreviated flush":   "sudo iptables --fl WORKCELL_EGRESS",
		"conditional flush":   "if type iptables; then sudo iptables -F WORKCELL_EGRESS; fi",
		"function flush":      "flush_live() { sudo iptables -F WORKCELL_EGRESS; }",
		"comment flush":       "# sudo iptables -F WORKCELL_EGRESS",
	} {
		t.Run(name, func(t *testing.T) {
			mutated := strings.Replace(string(script), "sudo iptables-restore --noflush <<<", hidden+"\nsudo iptables-restore --noflush <<<", 1)
			err := metadatautil.ValidateColimaEgressAtomicSwap(mutated)
			if err == nil || !strings.Contains(err.Error(), "live chain linked") {
				t.Fatalf("error = %v, want live chain rejection", err)
			}
		})
	}
}
