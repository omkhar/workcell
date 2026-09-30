// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// applyPlanDigest is the SHA-256 of the reviewed apply plan: the heredoc bodies
// of render_allowlist_apply_plan and the lines of that function outside them,
// as applyPlanDigestOf joins them. Regenerate it only after a review of the plan
// and a pass of the replay in scripts/verify-invariants.sh.
const applyPlanDigest = "0bf6987731321fbdd1706e58077d73a02d7135c103179743d43d682c345103c1"

var (
	commentStart   = regexp.MustCompile(`(^|\s)#`)
	planDefinition = regexp.MustCompile(`(^|[;&|{(]|\s)(function\s+)?render_allowlist_apply_plan(\s*\(\s*\)|\s*\{|\s*$)`)
)

// ValidateColimaEgressAtomicSwap requires the VM apply plan of
// scripts/colima-egress-allowlist.sh to replace each chain in one
// iptables-restore --noflush transaction, to keep the DOCKER-USER head guard,
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
	// Bash runs the last definition of a name, so a reviewed copy kept ahead of
	// a second definition would pass the checks below. Count each statement
	// that defines the function, in any valid spelling, after joining line
	// continuations and dropping comments. A definition that bash assembles at
	// run time is outside what a static check can close; the replay in
	// scripts/verify-invariants.sh covers the plan the real apply path captures.
	definitions := 0
	for line := range strings.Lines(strings.ReplaceAll(script, "\\\n", " ")) {
		if comment := commentStart.FindStringIndex(line); comment != nil {
			line = line[:comment[0]]
		}
		definitions += len(planDefinition.FindAllString(line, -1))
	}
	if definitions != 1 {
		return errors.New("Expected exactly one render_allowlist_apply_plan definition")
	}
	plan, stream := applyPlanText(script)
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
	if got := applyPlanDigestOf(stream); got != applyPlanDigest {
		return fmt.Errorf("Expected dual-stack allowlist apply plan to equal the reviewed plan text (digest %s)", got)
	}
	return nil
}

func applyPlanDigestOf(stream string) string {
	sum := sha256.Sum256([]byte(stream))
	return hex.EncodeToString(sum[:])
}

// applyPlanText returns the heredoc bodies inside render_allowlist_apply_plan,
// and the same lines interleaved with the function lines outside any heredoc
// in the order bash runs them, each line tagged with where it came from. The
// digest covers the second form, so moving text across a heredoc boundary
// changes it.
func applyPlanText(script string) (plan, stream string) {
	var body, ordered []string
	inFunction, inHeredoc := false, false
	for line := range strings.Lines(script) {
		line = strings.TrimSuffix(line, "\n")
		switch {
		case inHeredoc:
			if line == "EOF" {
				inHeredoc = false
				ordered = append(ordered, "E:"+line)
				continue
			}
			body = append(body, line)
			ordered = append(ordered, "H:"+line)
		case !inFunction:
			inFunction = strings.HasPrefix(line, "render_allowlist_apply_plan()")
		case line == "}":
			return strings.Join(body, "\n"), strings.Join(ordered, "\n")
		default:
			ordered = append(ordered, "F:"+strings.TrimSpace(line))
			inHeredoc = strings.Contains(line, "<<'EOF'")
		}
	}
	return strings.Join(body, "\n"), strings.Join(ordered, "\n")
}
