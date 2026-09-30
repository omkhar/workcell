// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// colimaEgressScriptDigest is the SHA-256 of the reviewed
// scripts/colima-egress-allowlist.sh. Regenerate it only after a review of the
// change and a pass of the replay in scripts/verify-invariants.sh.
const colimaEgressScriptDigest = "a3da23cd3456de25a5482086327e763190fbe18af1d8fe1044edc30f6439e768"

// ValidateColimaEgressAtomicSwap requires scripts/colima-egress-allowlist.sh to
// replace each chain in one iptables-restore --noflush transaction, to keep the
// DOCKER-USER head guard, and to equal the reviewed script byte for byte.
//
// The shared parser proves the restore commands in the plan that
// render_allowlist_apply_plan prints, so a comment, a heredoc body, an unrun
// branch or an uncalled function does not count as the swap, and the failing
// rule gets a name. A static check cannot prove that nothing else runs: bash
// assembles commands and function definitions across lines, quotes and
// evaluation, and every spelling needs its own rule. The digest closes the
// class. Any edit of the script, in any spelling, fails until a reviewer
// regenerates it, and the replay in scripts/verify-invariants.sh then checks
// the behavior of the reviewed script against a stub netfilter model.
func ValidateColimaEgressAtomicSwap(script string) error {
	plan := applyPlanText(script)
	families := []struct{ name, chain string }{{"iptables", "WORKCELL_EGRESS"}, {"ip6tables", "WORKCELL_EGRESS6"}}
	for _, family := range families {
		if len(ShellInvocations(plan, "sudo "+family.name+"-restore --noflush")) == 0 {
			return errors.New("Expected dual-stack allowlist apply plan to replace the chain in one iptables-restore transaction")
		}
	}
	for _, family := range families {
		guard := `[[ "$(sudo ` + family.name + ` -S DOCKER-USER | sed -n 2p)" == "-A DOCKER-USER -j ` + family.chain + `" ]] || sudo ` + family.name + ` -I DOCKER-USER 1 -j ` + family.chain
		if !slices.ContainsFunc(strings.Split(plan, "\n"), func(line string) bool { return strings.TrimSpace(line) == guard }) {
			return errors.New("Expected dual-stack allowlist apply plan to keep the live chain linked and intact until the replacement is complete")
		}
	}
	sum := sha256.Sum256([]byte(script))
	if got := hex.EncodeToString(sum[:]); got != colimaEgressScriptDigest {
		return fmt.Errorf("Expected the Colima egress allowlist script to equal the reviewed script (digest %s)", got)
	}
	return nil
}

// applyPlanText returns the heredoc bodies inside render_allowlist_apply_plan.
func applyPlanText(script string) string {
	var plan []string
	inFunction, inHeredoc := false, false
	for line := range strings.Lines(script) {
		line = strings.TrimSuffix(line, "\n")
		switch {
		case inHeredoc:
			if line == "EOF" {
				inHeredoc = false
				continue
			}
			plan = append(plan, line)
		case !inFunction:
			inFunction = strings.HasPrefix(line, "render_allowlist_apply_plan()")
		case line == "}":
			return strings.Join(plan, "\n")
		default:
			inHeredoc = strings.Contains(line, "<<'EOF'")
		}
	}
	return strings.Join(plan, "\n")
}
