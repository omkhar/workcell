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

const refsActionsTSV = "actions/checkout@" + refsSHA + "\tpath,ref\n"
const refsSHA = "3d3c42e5aac5ba805825da76410c181273ba90b1"

func refsRoot(t *testing.T, step, baseline string) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		".github/workflows/w.yml":           "name: w\njobs:\n  j:\n    runs-on: ubuntu-latest\n    steps:\n" + step,
		"policy/workflow-refs-baseline.tsv": baseline,
		"tests/fixtures/actions/inputs.tsv": refsActionsTSV,
		"scripts/present.sh":                "#!/bin/sh\n",
	}
	for name, body := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func runStep(body string) string {
	return "      - name: s\n        run: |\n          " + strings.ReplaceAll(strings.TrimSpace(body), "\n", "\n          ") + "\n"
}

func TestCheckWorkflowRefs(t *testing.T) {
	cases := []struct {
		name, step, baseline, want string
	}{
		{"existing script", runStep("./scripts/present.sh"), "", ""},
		{"dynamic script name is skipped", runStep(`./scripts/ci/job-${X}.sh`), "", ""},
		{"missing script", runStep("./scripts/absent.sh"), "", "missing-script ./scripts/absent.sh"},
		{"script in a comment is not probed", runStep("true # ./scripts/absent.sh\n# bash ./scripts/absent.sh"), "", ""},
		{"script in a heredoc body is not probed", runStep("cat <<'EOF'\n./scripts/absent.sh\nEOF"), "", ""},
		{"script after a heredoc is probed", runStep("cat <<EOF\nx\nEOF\nbash ./scripts/absent.sh"), "", "missing-script ./scripts/absent.sh"},
		{"script path leaving the root", runStep("./scripts/../../../../../../bin/sh"), "", "script-path-escapes"},
		{"bounded gh calls", runStep("gh api --paginate repos/x/y/pulls\ngh pr list --base main --limit 5\ngh -R o/r pr list -B main -L 5"), "", ""},
		{"gh api without bound", runStep(`x="$(gh api repos/x/y/pulls)"`), "", "gh-api-unbounded"},
		{"gh api has no --limit", runStep("gh api repos/x --limit 1"), "", "gh-api-unbounded"},
		{"gh list behind a repo flag", runStep("gh --repo o/r issue list"), "", "gh-issue-list-unbounded"},
		{"gh pr list behind -R", runStep("gh -R o/r pr list --limit 5"), "", "gh-pr-list-no-base"},
		{"gh list in a loop body", runStep("for k in a; do\n  gh issue list --state all\ndone"), "", "gh-issue-list-unbounded"},
		{"gh list in a condition substitution", runStep(`if [[ "$(gh label list --json name)" == "0" ]]; then echo; fi`), "", "gh-label-list-unbounded"},
		{"gh pr list without base", runStep("gh pr list --limit 9"), "", "gh-pr-list-no-base"},
		{"gh in a comment is not run", runStep("# gh api repos/x\ntrue"), "", ""},
		{"gh in a quoted jq program is not run", runStep(`jq -n '"gh api x"'`), "", ""},
		{"second unbounded call in a baselined step", runStep("gh api a\ngh api b"), "gh-api-unbounded\tw.yml\tj\ts\treason\n", "gh-api-unbounded#2"},
		{"baselined call passes", runStep("gh api a"), "gh-api-unbounded\tw.yml\tj\ts\treason\n", ""},
		{"stale baseline row", runStep("true"), "gh-api-unbounded\tw.yml\tj\ts\treason\n", "stale baseline row"},
		{"baseline row needs a reason", runStep("true"), "gh-api-unbounded\tw.yml\tj\ts\t\n", "want kind, file, job, step, reason"},
		{"known input", "      - uses: actions/checkout@" + refsSHA + "\n        with:\n          Path: x\n", "", ""},
		{"unknown input", "      - uses: actions/checkout@" + refsSHA + "\n        with:\n          app-id: x\n", "", "with-unknown-input actions/checkout@" + refsSHA + " app-id"},
		{"uncached action", "      - uses: actions/other@" + refsSHA + "\n", "", "uses-not-cached"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			err := metadatautil.CheckWorkflowRefs(refsRoot(t, testCase.step, testCase.baseline))
			if testCase.want == "" {
				if err != nil {
					t.Fatalf("CheckWorkflowRefs() error = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("CheckWorkflowRefs() error = %v, want %q", err, testCase.want)
			}
		})
	}
}

func TestCheckWorkflowRefsReadsYAMLExtension(t *testing.T) {
	root := refsRoot(t, runStep("true"), "")
	body := "name: v\njobs:\n  j:\n    runs-on: ubuntu-latest\n    steps:\n" + runStep("./scripts/absent.sh")
	if err := os.WriteFile(filepath.Join(root, ".github/workflows/v.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := metadatautil.CheckWorkflowRefs(root); err == nil || !strings.Contains(err.Error(), "v.yaml") {
		t.Fatalf("CheckWorkflowRefs() error = %v, want a v.yaml hit", err)
	}
}

func TestCheckWorkflowRefsPassesOnRepository(t *testing.T) {
	if err := metadatautil.CheckWorkflowRefs("../.."); err != nil {
		t.Fatal(err)
	}
}
