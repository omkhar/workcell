// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"errors"
	"strings"
)

// ValidateColimaEgressAtomicSwap requires the VM apply plan of
// scripts/colima-egress-allowlist.sh to run one iptables-restore --noflush
// transaction for each address family. The plan is the heredoc text that
// render_allowlist_apply_plan prints, so the parser reads that text and not the
// whole script: a comment, a heredoc body, an unrun branch or a function that
// nothing calls must not count as the swap.
func ValidateColimaEgressAtomicSwap(script string) error {
	plan := applyPlanText(script)
	for _, command := range []string{"sudo iptables-restore --noflush", "sudo ip6tables-restore --noflush"} {
		if len(ShellInvocations(plan, command)) == 0 {
			return errors.New("Expected dual-stack allowlist apply plan to replace the chain in one iptables-restore transaction")
		}
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
