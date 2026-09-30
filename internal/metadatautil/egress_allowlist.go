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

// applyPlanDigest is the SHA-256 of the reviewed apply plan: the heredoc bodies
// of render_allowlist_apply_plan and the lines of that function outside them,
// as applyPlanDigestOf joins them. Regenerate it only after a review of the plan
// and a pass of the replay in scripts/verify-invariants.sh.
const applyPlanDigest = "4d197047a9c0328934ab648a274bd74264eb67141ea4e09a4efd023327de1906"

// ValidateColimaEgressAtomicSwap requires the VM apply plan of
// scripts/colima-egress-allowlist.sh to replace each chain in one
// iptables-restore --noflush transaction, to keep the DOCKER-USER link guard,
// and to equal the reviewed plan text. The plan is what
// render_allowlist_apply_plan prints, so the parser reads that text and not the
// whole script: a comment, a heredoc body, an unrun branch or a function that
// nothing calls must not count as the swap.
//
// The shared parser proves the two required commands and names the failing
// rule. It cannot prove that nothing else runs, because bash can assemble a
// command across lines. The digest closes that: any edit of the plan, in any
// spelling, fails until a reviewer regenerates it. The replay in
// scripts/verify-invariants.sh then checks the behavior of the reviewed plan.
func ValidateColimaEgressAtomicSwap(script string) error {
	// Bash runs the last definition of a name, so a reviewed copy kept as a
	// decoy ahead of another definition would pass the checks below. Bash
	// accepts many spellings of a definition, so the name may appear only in
	// the one reviewed definition line and the one reviewed call.
	definitions := 0
	for line := range strings.Lines(script) {
		switch text := strings.TrimSpace(line); {
		case !strings.Contains(text, "render_allowlist_apply_plan"):
		case strings.TrimSuffix(line, "\n") == "render_allowlist_apply_plan() {":
			definitions++
		case text == `run_in_vm "$(render_allowlist_apply_plan)"`:
		default:
			return errors.New("Expected exactly one render_allowlist_apply_plan definition and no other mention")
		}
	}
	if definitions != 1 {
		return errors.New("Expected exactly one render_allowlist_apply_plan definition and no other mention")
	}
	plan, emitters := applyPlanText(script)
	families := []struct{ name, chain string }{{"iptables", "WORKCELL_EGRESS"}, {"ip6tables", "WORKCELL_EGRESS6"}}
	for _, family := range families {
		if len(ShellInvocations(plan, "sudo "+family.name+"-restore --noflush")) == 0 {
			return errors.New("Expected dual-stack allowlist apply plan to replace the chain in one iptables-restore transaction")
		}
	}
	for _, family := range families {
		if !slices.ContainsFunc(ShellInvocations(plan, "sudo "+family.name), func(check Invocation) bool {
			return slices.Equal(check.Args[:min(len(check.Args), 4)], []string{"-C", "DOCKER-USER", "-j", family.chain})
		}) {
			return errors.New("Expected dual-stack allowlist apply plan to keep the live chain linked and intact until the replacement is complete")
		}
	}
	if got := applyPlanDigestOf(plan, emitters); got != applyPlanDigest {
		return fmt.Errorf("Expected dual-stack allowlist apply plan to equal the reviewed plan text (digest %s)", got)
	}
	return nil
}

func applyPlanDigestOf(plan string, emitters []string) string {
	sum := sha256.Sum256([]byte(plan + "\n--\n" + strings.Join(emitters, "\n")))
	return hex.EncodeToString(sum[:])
}

// applyPlanText returns the heredoc bodies inside render_allowlist_apply_plan
// and the lines of that function outside any heredoc, which also emit plan text.
func applyPlanText(script string) (plan string, emitters []string) {
	var body []string
	inFunction, inHeredoc := false, false
	for line := range strings.Lines(script) {
		line = strings.TrimSuffix(line, "\n")
		switch {
		case inHeredoc:
			if line == "EOF" {
				inHeredoc = false
				continue
			}
			body = append(body, line)
		case !inFunction:
			inFunction = strings.HasPrefix(line, "render_allowlist_apply_plan()")
		case line == "}":
			return strings.Join(body, "\n"), emitters
		default:
			emitters = append(emitters, strings.TrimSpace(line))
			inHeredoc = strings.Contains(line, "<<'EOF'")
		}
	}
	return strings.Join(body, "\n"), emitters
}
