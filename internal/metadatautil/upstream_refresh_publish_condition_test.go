// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// upstreamRefreshPublishLine returns the expression of the one line of the
// publish job in the real workflow that starts with prefix, indent included.
func upstreamRefreshPublishLine(t *testing.T, prefix string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "upstream-refresh.yml"))
	if err != nil {
		t.Fatal(err)
	}
	_, publish, ok := strings.Cut(string(raw), "\n  publish:\n")
	if !ok {
		t.Fatal("the workflow has no publish job")
	}
	publish, _, _ = strings.Cut(publish, "\n  report:\n")
	var found []string
	for _, line := range strings.Split(publish, "\n") {
		if rest, ok := strings.CutPrefix(line, prefix); ok {
			found = append(found, strings.TrimSpace(rest))
		}
	}
	if len(found) != 1 {
		t.Fatalf("publish job has %d lines starting with %q, want 1", len(found), prefix)
	}
	inner, ok := strings.CutPrefix(found[0], "${{ ")
	if !ok {
		t.Fatalf("%s%s is not one expression", prefix, found[0])
	}
	inner, ok = strings.CutSuffix(inner, " }}")
	if !ok {
		t.Fatalf("%s%s is not one expression", prefix, found[0])
	}
	return inner
}

// evalUpstreamRefreshPublishIf evaluates a conjunction of !cancelled() and
// context == 'literal' terms, the only shape the verifier allows.
func evalUpstreamRefreshPublishIf(t *testing.T, expr string, cancelled bool, context map[string]string) bool {
	t.Helper()
	term := regexp.MustCompile(`^([a-z.-]+) == '([a-z/]+)'$`)
	result := true
	for _, part := range strings.Split(expr, " && ") {
		if part == "!cancelled()" {
			result = result && !cancelled
			continue
		}
		match := term.FindStringSubmatch(part)
		if match == nil {
			t.Fatalf("unexpected term %q in %q", part, expr)
		}
		value, ok := context[match[1]]
		if !ok {
			t.Fatalf("scenario does not set %s", match[1])
		}
		result = result && value == match[2]
	}
	return result
}

// TestUpstreamRefreshPublishIfAndRederiveInputModel checks which job results
// let publish run, and which rederive verdict publish then gets. The if: half
// evaluates the expression read from the real workflow. The input half does
// not evaluate TOOLCHAIN_REDERIVE_RESULT: it requires that line to be the exact
// reviewed string, which the verifier also pins, and then evaluates a Go model
// of how GitHub reads it. A failed rederive job holds only the toolchain
// candidate; a failed refresh or scope-guard job, or a cancelled run,
// publishes nothing.
func TestUpstreamRefreshPublishIfAndRederiveInputModel(t *testing.T) {
	t.Parallel()
	publishIf := upstreamRefreshPublishLine(t, "    if:")
	rederiveInput := upstreamRefreshPublishLine(t, "          TOOLCHAIN_REDERIVE_RESULT:")
	verdict := "rederived:" + strings.Repeat("a", 40) + ":" + strings.Repeat("b", 40)

	input := regexp.MustCompile(`^needs\.rederive\.result == 'success' && needs\.rederive\.outputs\.toolchain \|\| 'failed'$`)
	if !input.MatchString(rederiveInput) {
		t.Fatalf("TOOLCHAIN_REDERIVE_RESULT = %q, want the rederive output only when the job succeeded", rederiveInput)
	}
	// A model of the pinned expression, not an evaluation of it: GitHub reads
	// a && b || c as (a && b) || c, and an empty b is false.
	rederiveVerdict := func(result, output string) string {
		if result == "success" && output != "" {
			return output
		}
		return "failed"
	}

	cases := []struct {
		name                    string
		cancelled               bool
		refresh, guard, rederiv string
		output                  string
		wantPublish             bool
		wantVerdict             string
	}{
		{name: "all jobs passed", refresh: "success", guard: "success", rederiv: "success", output: verdict, wantPublish: true, wantVerdict: verdict},
		{name: "rederive job failed publishes the provider and holds the toolchain", refresh: "success", guard: "success", rederiv: "failure", output: verdict, wantPublish: true, wantVerdict: "failed"},
		{name: "rederive job cancelled by its timeout holds the toolchain", refresh: "success", guard: "success", rederiv: "cancelled", wantPublish: true, wantVerdict: "failed"},
		{name: "rederive step failed holds the toolchain", refresh: "success", guard: "success", rederiv: "success", output: "failed", wantPublish: true, wantVerdict: "failed"},
		{name: "scope-guard job failed publishes nothing", refresh: "success", guard: "failure", rederiv: "success", output: verdict},
		{name: "refresh job failed publishes nothing", refresh: "failure", guard: "skipped", rederiv: "skipped"},
		{name: "cancelled run publishes nothing", cancelled: true, refresh: "success", guard: "success", rederiv: "failure"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			context := map[string]string{
				"github.ref":                      "refs/heads/main",
				"needs.refresh.result":            tc.refresh,
				"needs.scope-guard.result":        tc.guard,
				"needs.rederive.result":           tc.rederiv,
				"needs.refresh.outputs.candidate": "true",
			}
			if got := evalUpstreamRefreshPublishIf(t, publishIf, tc.cancelled, context); got != tc.wantPublish {
				t.Fatalf("publish runs = %v, want %v (if: %s)", got, tc.wantPublish, publishIf)
			}
			if tc.wantPublish {
				if got := rederiveVerdict(tc.rederiv, tc.output); got != tc.wantVerdict {
					t.Fatalf("rederive verdict = %q, want %q", got, tc.wantVerdict)
				}
			}
		})
	}
}
