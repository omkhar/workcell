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

// TestValidateEgressDenySealOrder runs the shared evasion corpus on the seal
// calls, and rejects a direct seal or an auditing proxy stop in every shell
// form that still runs it.
func TestValidateEgressDenySealOrder(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "scripts", "workcell"))
	if err != nil {
		t.Fatal(err)
	}
	script := string(raw)
	if err := metadatautil.ValidateEgressDenySealOrder(script); err != nil {
		t.Fatalf("real script rejected: %v", err)
	}
	const want = "egress-proxy session seal"
	seal := `  stop_egress_proxy_then_finalize "${COLIMA_PROFILE}" "${EXECUTION_PATH}" "${wait_status}"`
	RequireRejectsTextEvasions(t, script, seal, want, metadatautil.ValidateEgressDenySealOrder)

	forms := map[string]string{
		"brace group":        "{ CALL; }",
		"subshell":           "( CALL )",
		"after &&":           "true && CALL",
		"after ||":           "false || CALL",
		"after ;":            ":; CALL",
		"case arm":           "case x in x) CALL ;; esac",
		"function body":      "late() {\n  CALL\n}\nlate",
		"leading assignment": "X=1 CALL",
		"quoted word":        "'CALL_WORD' CALL_ARGS",
		"escaped word":       "\\CALL_WORD CALL_ARGS",
		"command builtin":    "command CALL",
	}
	for _, call := range []string{"finalize_session_audit p e 0", "stop_egress_proxy_for_session p s"} {
		word, args, _ := strings.Cut(call, " ")
		for name, form := range forms {
			t.Run(word+" "+name, func(t *testing.T) {
				text := strings.NewReplacer("CALL_WORD", word, "CALL_ARGS", args, "CALL", call).Replace(form)
				if err := metadatautil.ValidateEgressDenySealOrder(script + "\n" + text + "\n"); err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("validator accepted %q: %v", text, err)
				}
			})
		}
	}
	for name, mutate := range map[string]func(string) string{
		"finalize before the stop": func(s string) string {
			return strings.Replace(s, "    stop_egress_proxy \"$1\"\n    EGRESS_PROXY_STARTED=0\n  fi\n  finalize_session_audit \"$@\"",
				"    EGRESS_PROXY_STARTED=0\n  fi\n  finalize_session_audit \"$@\"\n  stop_egress_proxy \"$1\"", 1)
		},
		"fallback seals before the stop": func(s string) string {
			return strings.Replace(s, "  stop_egress_proxy_for_session \"${profile}\" \"${session_id}\" || return 1\n", "", 1) +
				"\nstop_egress_proxy_for_session p s\n"
		},
		"fallback stops after the seal": func(s string) string {
			stop := "  stop_egress_proxy_for_session \"${profile}\" \"${session_id}\" || return 1\n"
			sign := "  sign_session_audit_head_explicit \"${profile}\" \"${session_id}\" \"${record_path}\"\n"
			return strings.Replace(strings.Replace(s, stop, "", 1), sign, sign+stop, 1)
		},
		"later no-op seal helper": func(s string) string {
			return s + "\nstop_egress_proxy_then_finalize() { :; }\n"
		},
		"delete stop audits": func(s string) string {
			return strings.Replace(s, `"${session_id}" no-audit || exit 1`, `"${session_id}" || exit 1`, 1)
		},
	} {
		t.Run(name, func(t *testing.T) {
			mutated := mutate(script)
			if mutated == script {
				t.Fatal("mutation changed nothing")
			}
			if err := metadatautil.ValidateEgressDenySealOrder(mutated); err == nil {
				t.Fatalf("validator accepted %s", name)
			}
		})
	}
}
