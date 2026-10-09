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

func refsRoot(t *testing.T, step, baseline string) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		".github/workflows/w.yml":           "name: w\njobs:\n  j:\n    runs-on: ubuntu-latest\n    steps:\n" + step,
		"policy/workflow-refs-baseline.tsv": baseline,
		"scripts/present.sh":                "#!/bin/sh\n",
		"sub/README":                        "",
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
		{"bounded gh calls", runStep("gh api --paginate repos/x/y/pulls\ngh pr list --base main --limit 5\ngh -R o/r pr list -B main -L 5\ngh pr list --draft -Bmain --limit=5"), "", ""},
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
		{"gh list in a brace group", runStep("{ gh pr list --base main; }"), "", "gh-pr-list-unbounded"},
		{"gh ls alias", runStep("gh pr ls --base main"), "", "gh-pr-list-unbounded"},
		{"gh group and ls aliases", runStep("gh rs ls"), "", "gh-rs-list-unbounded"},
		{"gh api pagination disabled", runStep("gh api --paginate=false repos/x"), "", "gh-api-unbounded"},
		{"gh api pagination overridden", runStep("gh api --paginate repos/x --paginate=false"), "", "gh-api-unbounded"},
		{"gh list without a limit flag", runStep("gh secret list --limit 1"), "", "gh-secret-list-unbounded"},
		{"script under the step working directory", "      - name: s\n        working-directory: sub\n        run: ./scripts/present.sh\n", "", "missing-script ./scripts/present.sh"},
		{"script under a directory made at run time", "      - name: s\n        working-directory: gen\n        run: ./scripts/present.sh\n", "", "script-cwd-unresolved ./scripts/present.sh"},
		{"script after a cd", runStep("cd sub\n./scripts/present.sh"), "", "script-cwd-unresolved ./scripts/present.sh"},
		{"gh discussion list without a limit", runStep("gh discussion list"), "", "gh-discussion-list-unbounded"},
		{"gh org list with a limit", runStep("gh org list --limit 5\ngh agent-task list -L 5"), "", ""},
		{"gh list of an unknown group", runStep("gh skill list --limit 5"), "", "gh-skill-list-unbounded"},
		{"gh api behind --hostname", runStep("gh --hostname github.com api repos/x"), "", "gh-api-unbounded"},
		{"gh api behind a short global flag", runStep("gh -X GET api repos/x"), "", "gh-api-unbounded"},
		{"gh pr list with an empty base", runStep("gh pr list --base= --limit 1"), "", "gh-pr-list-no-base"},
		{"gh pr list with an empty quoted base", runStep(`gh pr list --base "" --limit 1`), "", "gh-pr-list-no-base"},
		{"gh pr list base overridden", runStep("gh pr list -B main --base= --limit 1"), "", "gh-pr-list-no-base"},
		{"called function body is read", runStep("f() {\n  gh api repos/x\n}\ntrap f EXIT"), "", "gh-api-unbounded"},
		{"one-line called function body is read", runStep("f() { gh api repos/x; }\nf"), "", "gh-api-unbounded"},
		{"uncalled one-line function with a command after it", runStep("f() { true; }\ngh api repos/x"), "", "gh-api-unbounded"},
		{"gh list behind a flag after the group", runStep("gh pr -R o/r list --base main"), "", "gh-pr-list-unbounded"},
		{"gh pr base in another option's value", runStep("gh pr list --search -Bfoo --limit 1"), "", "gh-pr-list-no-base"},
		{"gh api in backticks", runStep("r=`gh api repos/x`"), "", "gh-api-unbounded"},
		{"called function body runs at the call", runStep("f() {\n  cd sub\n}\nf\n./scripts/present.sh"), "", "script-cwd-unresolved ./scripts/present.sh"},
		{"gh list with a zero limit", runStep("gh issue list --limit 0"), "", "gh-issue-list-unbounded"},
		{"gh list with a word limit", runStep("gh issue list --limit=abc"), "", "gh-issue-list-unbounded"},
		{"gh list with a negative limit", runStep("gh issue list -L -1"), "", "gh-issue-list-unbounded"},
		{"gh list limit overridden", runStep("gh issue list -L 5 --limit 0"), "", "gh-issue-list-unbounded"},
		{"gh behind wrappers", runStep("command gh api a\ncommand -p gh api b\nenv -u X A=1 nice -n 5 gh api c\nexec gh api d"), "", "gh-api-unbounded#4"},
		{"command -v only names gh", runStep("command -v gh api a"), "", ""},
		{"duplicate limit behind a value-less flag", runStep("gh discussion list --limit 5 --answered --limit abc"), "", "gh-discussion-list-unbounded"},
		{"every call of a function is expanded", runStep("g() { gh api x; }\nf() { g; g; }\nf\ny=$(g)"), "gh-api-unbounded\tw.yml\tj\ts\treason\ngh-api-unbounded#2\tw.yml\tj\ts\treason\n", "gh-api-unbounded#3"},
		{"recursive function is expanded once per call", runStep("f() { gh api x; f; }\nf"), "gh-api-unbounded\tw.yml\tj\ts\treason\n", ""},
		{"gh behind long wrapper options", runStep("env --unset X gh api a\nenv --unset=X gh api b\nnice --adjustment 5 gh api c\nnice --adj 5 gh api d"), "", "gh-api-unbounded#4"},
		{"function name as data is not a call", runStep("f() { gh api repos/x; }\nprintf '%s\\n' f"), "", ""},
		{"redefined function runs its last body", runStep("f() { gh api repos/x; }\nf() { :; }\nf"), "", ""},
		{"call before a redefinition runs the first body", runStep("f() { gh api repos/x; }\nf\nf() { :; }"), "", "gh-api-unbounded"},
		{"gh help runs nothing", runStep("gh api --help\ngh pr list -h\ngh api repos/x --help=true"), "", ""},
		{"gh help turned off", runStep("gh api --help=false repos/x"), "", "gh-api-unbounded"},
		{"gh behind redirection prefixes", runStep(">out gh api a\n2>/dev/null gh api b\n< in A=1 gh api c\n{fd}>f 2>&1 gh api d"), "", "gh-api-unbounded#4"},
		{"trap runs the last definition", runStep("trap f EXIT\nf() { :; }\nf() { gh api repos/x; }"), "", "gh-api-unbounded"},
		{"same-line definition is not run", runStep("f() { gh api repos/x; }; :\n: && g() { gh api repos/y; }"), "", ""},
		{"same-line definition then its call", runStep("f() { gh api repos/x; }; f"), "", "gh-api-unbounded"},
		{"gh api with an attached redirection", runStep("gh api>/dev/null repos/x"), "", "gh-api-unbounded"},
		{"gh with an attached redirection", runStep("gh>/dev/null api repos/x"), "", "gh-api-unbounded"},
		{"gh behind redirections after the command word", runStep("gh 2>&1 api a\ngh &>/dev/null api b\ngh > out api c\ngh >&2 api d"), "", "gh-api-unbounded#4"},
		{"quoted > inside a word does not split", runStep(`gh "api>x" repos/x`), "", ""},
		{"gh behind env split strings", runStep("env -S 'gh api a'\nenv --split-string='gh api b'\nenv -i --split-s 'A=1 gh' api c\nenv -S'gh api d'\nenv XS=1 gh api e"), "", "gh-api-unbounded#5"},
		{"gh api pagination as another option's value", runStep("gh api --preview --paginate repos/o/r/issues"), "", "gh-api-unbounded"},
		{"gh api pagination after valued options", runStep("gh api --paginate repos/o/r/issues\ngh api -X GET --paginate repos/o/r/issues\ngh api -H 'A: b' --paginate repos/x"), "", ""},
		{"trap action after --", runStep("trap -- f EXIT\nf() { gh api repos/x; }"), "", "gh-api-unbounded"},
		{"gh list with a value-less flag before its limit", runStep("gh discussion list --answered --limit 5\ngh repo list --archived -L 5\ngh release list --exclude-drafts --limit 5"), "", ""},
		{"script in shell data is not probed", runStep("echo './scripts/absent.sh'\nprintf '%s' ./scripts/absent.sh\ncat <<< ./scripts/absent.sh\nexport X=./scripts/absent.sh"), "", ""},
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

func TestCheckWorkflowRefsReadsJobWorkingDirectory(t *testing.T) {
	root := refsRoot(t, runStep("true"), "")
	body := "name: v\njobs:\n  j:\n    runs-on: ubuntu-latest\n    defaults:\n      run:\n        working-directory: sub\n    steps:\n" + runStep("./scripts/present.sh")
	if err := os.WriteFile(filepath.Join(root, ".github/workflows/v.yml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := metadatautil.CheckWorkflowRefs(root); err == nil || !strings.Contains(err.Error(), "missing-script ./scripts/present.sh") {
		t.Fatalf("CheckWorkflowRefs() error = %v, want a missing-script hit under sub", err)
	}
}

// TestCheckWorkflowRefsSkipsUncalledFunctions runs the shared corpus rows that
// hide a command in a function definition. The body never runs, so the lint
// must not read the command in it.
func TestCheckWorkflowRefsSkipsUncalledFunctions(t *testing.T) {
	const anchor = "gh api repos/x"
	rows := 0
	for _, evasion := range Evasions {
		if !strings.Contains(evasion.Name, "definition") {
			continue
		}
		rows++
		t.Run(evasion.Name, func(t *testing.T) {
			if err := metadatautil.CheckWorkflowRefs(refsRoot(t, runStep(evasion.Rewrite(anchor, anchor)), "")); err != nil {
				t.Fatalf("CheckWorkflowRefs() error = %v, want nil", err)
			}
		})
	}
	if rows < 4 {
		t.Fatalf("found %d definition rows in the shared corpus, want at least 4", rows)
	}
}

func TestCheckWorkflowRefsPassesOnRepository(t *testing.T) {
	if err := metadatautil.CheckWorkflowRefs("../.."); err != nil {
		t.Fatal(err)
	}
}
