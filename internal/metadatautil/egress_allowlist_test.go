// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil_test

import (
	"os"
	"path/filepath"
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
}
