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
// nothing calls must not count as the swap.
//
// The required commands use the shared parser, which drops what bash may not
// run. The deny rule must do the opposite, so it over-approximates: any
// destructive iptables or ip6tables option on any plan line fails, including
// one inside a conditional, a function body, a comment or a quoted spelling.
func ValidateColimaEgressAtomicSwap(script string) error {
	plan := applyPlanText(script)
	const liveChain = "Expected dual-stack allowlist apply plan to keep the live chain linked and intact until the replacement is complete"
	if hasDestructiveNetfilterOption(plan) {
		return errors.New(liveChain)
	}
	families := []string{"iptables", "ip6tables"}
	for _, family := range families {
		if len(ShellInvocations(plan, "sudo "+family+"-restore --noflush")) == 0 {
			return errors.New("Expected dual-stack allowlist apply plan to replace the chain in one iptables-restore transaction")
		}
	}
	for _, family := range families {
		chain := map[string]string{"iptables": "WORKCELL_EGRESS", "ip6tables": "WORKCELL_EGRESS6"}[family]
		if !linkGuardRuns(plan, family, chain) {
			return errors.New(liveChain)
		}
	}
	return nil
}

// linkGuardRuns reports whether the plan runs the DOCKER-USER link guard for
// family: check for the jump and, when the check fails, insert it. The parser
// drops the insert after ||, so it proves only that the check runs. The plan
// must hold exactly one line that names the check, and that line must be the
// whole guard, so a swallowed insert such as `|| true` cannot pass and a decoy
// copy of the guard cannot stand in for the line that runs.
func linkGuardRuns(plan, family, chain string) bool {
	guard := "sudo " + family + " -C DOCKER-USER -j " + chain + " 2>/dev/null || sudo " + family + " -I DOCKER-USER 1 -j " + chain
	named := 0
	whole := false
	for line := range strings.Lines(plan) {
		if strings.Contains(line, family+" -C") {
			named++
			whole = strings.TrimSpace(line) == guard
		}
	}
	return named == 1 && whole && slices.ContainsFunc(ShellInvocations(plan, "sudo "+family), func(check Invocation) bool {
		return slices.Equal(check.Args[:min(len(check.Args), 4)], []string{"-C", "DOCKER-USER", "-j", chain})
	})
}

// hasDestructiveNetfilterOption reports whether any line of the plan names
// iptables or ip6tables and also carries a delete, flush or delete-chain
// option. Quotes and backslashes are removed first, so "-F" and -\F count, and
// a continued line is joined. A short-option cluster counts when it holds D, F
// or X, and a long option counts when it abbreviates --delete, --flush or
// --delete-chain the way getopt accepts.
func hasDestructiveNetfilterOption(plan string) bool {
	plain := strings.NewReplacer("\\\n", " ", "\"", "", "'", "", "\\", "").Replace(plan)
	for line := range strings.Lines(plain) {
		words := strings.Fields(line)
		if !slices.ContainsFunc(words, func(word string) bool {
			return strings.HasPrefix(word, "iptables") || strings.HasPrefix(word, "ip6tables") ||
				strings.HasSuffix(word, "/iptables") || strings.HasSuffix(word, "/ip6tables")
		}) {
			continue
		}
		if slices.ContainsFunc(words, func(word string) bool {
			if strings.HasPrefix(word, "--") {
				name, _, _ := strings.Cut(word, "=")
				return len(name) >= 4 && (strings.HasPrefix("--delete", name) ||
					strings.HasPrefix("--flush", name) || strings.HasPrefix("--delete-chain", name))
			}
			return strings.HasPrefix(word, "-") && strings.ContainsAny(word, "DFX")
		}) {
			return true
		}
	}
	return false
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
