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
	// The head guard keeps the Workcell jump first. Dropping the insert, or
	// checking only that the jump exists somewhere, leaves a new or misordered
	// chain unenforced.
	guardLine := `[[ "$(sudo iptables -S DOCKER-USER | sed -n 2p)" == "-A DOCKER-USER -j WORKCELL_EGRESS" ]] || sudo iptables -I DOCKER-USER 1 -j WORKCELL_EGRESS`
	for name, weaker := range map[string]string{
		"swallowed insert":  `[[ "$(sudo iptables -S DOCKER-USER | sed -n 2p)" == "-A DOCKER-USER -j WORKCELL_EGRESS" ]] || true`,
		"presence check":    "sudo iptables -C DOCKER-USER -j WORKCELL_EGRESS 2>/dev/null || sudo iptables -I DOCKER-USER 1 -j WORKCELL_EGRESS",
		"commented guard":   "# " + guardLine,
		"guard not reached": "exit 0\n" + guardLine,
	} {
		t.Run(name, func(t *testing.T) {
			mutated := strings.Replace(string(script), guardLine, weaker, 1)
			if mutated == string(script) {
				t.Fatal("edit left the script unchanged")
			}
			if err := metadatautil.ValidateColimaEgressAtomicSwap(mutated); err == nil {
				t.Fatal("validator accepted a weaker link guard")
			}
		})
	}

	// A second definition of the plan function replaces the reviewed one, in
	// any valid spelling. A comment or a message that names it does not.
	for name, extra := range map[string]string{
		"later definition":    "\nrender_allowlist_apply_plan() {\n  echo unsafe\n}\n",
		"function keyword":    "\nfunction render_allowlist_apply_plan {\n  echo unsafe\n}\n",
		"spaced parentheses":  "\nrender_allowlist_apply_plan () { echo unsafe; }\n",
		"tab after keyword":   "\nfunction\trender_allowlist_apply_plan { echo unsafe; }\n",
		"continued keyword":   "\nfunction \\\nrender_allowlist_apply_plan { echo unsafe; }\n",
		"indented definition": "\n  render_allowlist_apply_plan() { echo unsafe; }\n",
		"after a separator":   "\ntrue; render_allowlist_apply_plan() { echo unsafe; }\n",
	} {
		t.Run(name, func(t *testing.T) {
			if err := metadatautil.ValidateColimaEgressAtomicSwap(string(script) + extra); err == nil {
				t.Fatal("validator accepted a second plan definition")
			}
		})
	}
	// A quote or an escape before a hash must not hide a definition.
	for name, hidden := range map[string]string{
		"double quoted hash": "\nx=\" #\"; render_allowlist_apply_plan() { echo unsafe; }\n",
		"single quoted hash": "\nx=' #'; render_allowlist_apply_plan() { echo unsafe; }\n",
		"escaped hash":       "\nx=\\# ; render_allowlist_apply_plan() { echo unsafe; }\n",
		"hash inside word":   "\nx=a#b; render_allowlist_apply_plan() { echo unsafe; }\n",
	} {
		t.Run(name, func(t *testing.T) {
			if err := metadatautil.ValidateColimaEgressAtomicSwap(string(script) + hidden); err == nil {
				t.Fatal("validator accepted a definition behind a hash that is not a comment")
			}
		})
	}
	// A comment or a message that names the function passes, definition shape
	// included, because bash ignores it.
	for name, harmless := range map[string]string{
		"comment with shape": "\n# render_allowlist_apply_plan() emits the VM plan\n",
		"trailing comment":   "\ntrue # function render_allowlist_apply_plan { }\n",
		"message":            "\necho \"render_allowlist_apply_plan failed\" >&2\n",
	} {
		t.Run(name, func(t *testing.T) {
			err := metadatautil.ValidateColimaEgressAtomicSwap(string(script) + harmless)
			if err != nil && strings.Contains(err.Error(), "definition") {
				t.Fatalf("harmless mention rejected: %v", err)
			}
		})
	}

	// Moving the endpoint loop across the first heredoc boundary keeps every
	// line and changes the order bash runs them in.
	moved := strings.Replace(string(script), "EOF\n  printf 'WORKCELL_ENDPOINTS=%q\\n' \"${ENDPOINTS}\"\n  cat <<'EOF'\n", "  printf 'WORKCELL_ENDPOINTS=%q\\n' \"${ENDPOINTS}\"\nEOF\n  cat <<'EOF'\n", 1)
	if moved == string(script) {
		t.Fatal("boundary edit left the script unchanged")
	}
	if err := metadatautil.ValidateColimaEgressAtomicSwap(moved); err == nil {
		t.Fatal("validator accepted a plan with moved heredoc boundary")
	}

	// The emitted plan text and the restore payload are fixed, line for line.
	for name, edit := range map[string][2]string{
		"accept before drop": {"\\n%s-A WORKCELL_EGRESS -j DROP", "\\n-A WORKCELL_EGRESS -j ACCEPT\\n%s-A WORKCELL_EGRESS -j DROP"},
		"extra rule line":    {"IPV4_RULES=\"\"\n", "IPV4_RULES=\"\"\nIPV4_RULES+=\"-A WORKCELL_EGRESS -j ACCEPT\"$'\\n'\n"},
		"extra emitter":      {"  printf 'WORKCELL_ENDPOINTS=%q\\n' \"${ENDPOINTS}\"\n", "  printf 'WORKCELL_ENDPOINTS=%q\\n' \"${ENDPOINTS}\"\n  echo 'sudo iptables -A WORKCELL_EGRESS -j ACCEPT'\n"},
	} {
		t.Run(name, func(t *testing.T) {
			mutated := strings.Replace(string(script), edit[0], edit[1], 1)
			if mutated == string(script) {
				t.Fatal("edit left the script unchanged")
			}
			if err := metadatautil.ValidateColimaEgressAtomicSwap(mutated); err == nil {
				t.Fatal("validator accepted a changed plan payload")
			}
		})
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
		"direct insert":       "sudo iptables -I WORKCELL_EGRESS 1 -j ACCEPT",
		"direct replace":      "sudo ip6tables -R WORKCELL_EGRESS6 1 -j ACCEPT",
		"direct append":       "sudo iptables -A WORKCELL_EGRESS -j ACCEPT",
		"policy change":       "sudo iptables -P FORWARD ACCEPT",
		"multi-line assembly": "x=ipt\nx+=ables\nc=WORKCELL_\nc+=EGRESS\ns=su\ns+=do\n$s $x -F \"$c\"",
		"variable split":      "empty=; sudo ipt${empty}ables -F WORKCELL_EGRESS",
		"split word":          `sudo ipt""ables -A WORKCELL_EGRESS -j ACCEPT`,
		"conditional flush":   "if type iptables; then sudo iptables -F WORKCELL_EGRESS; fi",
		"function flush":      "flush_live() { sudo iptables -F WORKCELL_EGRESS; }",
		"comment flush":       "# sudo iptables -F WORKCELL_EGRESS",
	} {
		t.Run(name, func(t *testing.T) {
			mutated := strings.Replace(string(script), "sudo iptables-restore --noflush <<<", hidden+"\nsudo iptables-restore --noflush <<<", 1)
			err := metadatautil.ValidateColimaEgressAtomicSwap(mutated)
			if err == nil {
				t.Fatal("validator accepted a plan that changes a live chain outside the swap")
			}
		})
	}
}
