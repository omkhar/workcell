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
// iptables-restore --noflush transaction and to touch the live chains only
// through the link guard. The plan is the heredoc text that
// render_allowlist_apply_plan prints, so the parser reads that text and not the
// whole script: a comment, a heredoc body, an unrun branch or a function that
// nothing calls must not count as the swap.
//
// The shared parser drops what bash may not run, which suits the required
// commands and not a deny rule. So the deny rule is an allowlist over the
// text: every plan line that names iptables or ip6tables, after quotes and
// backslashes are removed, must be one of the exact lines below. A comment, a
// conditional, a function body or a different option on such a line fails.
func ValidateColimaEgressAtomicSwap(script string) error {
	plan := applyPlanText(script)
	const liveChain = "Expected dual-stack allowlist apply plan to keep the live chain linked and intact until the replacement is complete"
	allowed := []string{
		`if ! type ip6tables >/dev/null 2>&1; then`,
		`echo "Workcell requires ip6tables support to enforce dual-stack allowlist egress policy." >&2`,
		`sudo ip6tables -L WORKCELL_EGRESS6 >/dev/null 2>&1 || true`,
	}
	families := []struct{ name, chain, restoreInput string }{
		{"iptables", "WORKCELL_EGRESS", "IPV4_RESTORE"},
		{"ip6tables", "WORKCELL_EGRESS6", "IPV6_RESTORE"},
	}
	for _, family := range families {
		allowed = append(allowed,
			"sudo "+family.name+"-restore --noflush <<<\"${"+family.restoreInput+"}\"",
			"sudo "+family.name+" -C DOCKER-USER -j "+family.chain+" 2>/dev/null || sudo "+family.name+" -I DOCKER-USER 1 -j "+family.chain)
		if len(ShellInvocations(plan, "sudo "+family.name+"-restore --noflush")) == 0 {
			return errors.New("Expected dual-stack allowlist apply plan to replace the chain in one iptables-restore transaction")
		}
	}
	for _, family := range families {
		if !slices.ContainsFunc(ShellInvocations(plan, "sudo "+family.name), func(check Invocation) bool {
			return slices.Equal(check.Args[:min(len(check.Args), 4)], []string{"-C", "DOCKER-USER", "-j", family.chain})
		}) {
			return errors.New(liveChain)
		}
	}
	plainLine := strings.NewReplacer("\\\n", " ", "\"", "", "'", "", "\\", "")
	for i := range allowed {
		allowed[i] = plainLine.Replace(allowed[i])
	}
	for line := range strings.Lines(plainLine.Replace(plan)) {
		if strings.Contains(line, "tables") && !slices.Contains(allowed, strings.TrimSpace(line)) {
			return errors.New(liveChain)
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
