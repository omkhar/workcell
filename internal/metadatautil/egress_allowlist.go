// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"errors"
	"slices"
	"strings"
)

// ValidateColimaEgressAtomicSwap requires the VM apply plan of
// scripts/colima-egress-allowlist.sh to replace each chain in one
// iptables-restore --noflush transaction and to touch the live chain only
// through the link guard. The plan is the heredoc text that
// render_allowlist_apply_plan prints, so the parser reads that text and not the
// whole script: a comment, a heredoc body, an unrun branch or a function that
// nothing calls must not count as the swap. Plain iptables and ip6tables
// invocations may only list (-L), check (-C) or insert (-I), so a delete or a
// flush in any spelling fails closed.
func ValidateColimaEgressAtomicSwap(script string) error {
	plan := applyPlanText(script)
	families := []string{"iptables", "ip6tables"}
	for _, family := range families {
		if len(ShellInvocations(plan, "sudo "+family+"-restore --noflush")) == 0 {
			return errors.New("Expected dual-stack allowlist apply plan to replace the chain in one iptables-restore transaction")
		}
	}
	for _, family := range families {
		guarded := false
		for _, invocation := range ShellInvocations(plan, "sudo "+family) {
			if len(invocation.Args) == 0 || !slices.Contains([]string{"-L", "-C", "-I"}, invocation.Args[0]) {
				return errors.New("Expected dual-stack allowlist apply plan to keep the live chain linked and intact until the replacement is complete")
			}
			guarded = guarded || invocation.Args[0] == "-C"
		}
		if !guarded {
			return errors.New("Expected dual-stack allowlist apply plan to keep the live chain linked and intact until the replacement is complete")
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
