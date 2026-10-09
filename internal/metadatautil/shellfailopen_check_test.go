// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/omkhar/workcell/internal/metadatautil"
)

func TestCheckShellFailOpenAcceptsThisRepository(t *testing.T) {
	if err := metadatautil.CheckShellFailOpen(filepath.Join("..", "..")); err != nil {
		t.Fatalf("CheckShellFailOpen() error = %v", err)
	}
}

func TestShellFailOpenFindings(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		script string
		rule   string // empty means no finding
	}{
		{"process substitution", "while read -r f; do echo \"$f\"; done < <(find . -type f)\n", "process-substitution"},
		{"command substitution", "local out\nout=\"$(git ls-files)\"\n", "command-substitution"},
		{"command substitution in a compound body", "f() {\n  if true; then\n    x=$(gh api /y)\n  fi\n}\n", "command-substitution"},
		{"or true", "git fetch origin || true\n", "or-true"},
		{"dev null", "git rev-parse HEAD 2>/dev/null\n", "dev-null"},
		{"continued line", "docker \\\n  ps 2>/dev/null\n", "dev-null"},
		{"env prefix before the tool", "x=$(LC_ALL=C getent hosts a)\n", "command-substitution"},
		{"quoted substitution across lines", "x=\"$(\n  git ls-files\n)\"\n", "command-substitution"},
		{"substitution across lines", "x=$(\n  git ls-files # list\n)\n", "command-substitution"},
		{"quoted marker is not a comment", "printf '%s' '# fail-closed: decoy'; git fetch || true\n", "or-true"},
		{"arithmetic joins no line", "# fail-closed: counter only\nn=$((n + 1))\ngit gc || true\n", "or-true"},
		{"double-quoted marker is not a comment", "echo \"# fail-closed: decoy\" && git fetch || true\n", "or-true"},
		{"substitution inside a multi-line double quote", "f() {\n  local out=\"\n$(git ls-files)\n\"\n}\n", "command-substitution"},
		{"process substitution after extra blanks", "while read -r f; do :; done <  <(find . -type f)\n", "process-substitution"},
		{"process substitution after a tab", "while read -r f; do :; done <\t<(find . -type f)\n", "process-substitution"},
		{"if body is not the test", "if true; then local out=$(git ls-files); fi\n", "command-substitution"},
		{"handler of a later command", "x=$(git ls-files); cd /y || exit 1\n", "command-substitution"},
		{"unrelated wait later", "files=$(git ls-files)\na=1\nb=2\nwait \"$pid\"\n", "command-substitution"},
		{"status read before the call", "prior=$? out=$(git ls-files)\n", "command-substitution"},
		{"status read on the continued line before", "prior=$? \\\n  out=$(git ls-files)\n", "command-substitution"},
		{"tool attached to ||", "git||true\n", "or-true"},
		{"tool attached to &&", "git&&gh||true\n", "or-true"},
		{"tool attached to a subshell closer", "(git)2>/dev/null\n", "dev-null"},
		{"tool attached to a redirection", "docker>/dev/null 2>/dev/null\n", "dev-null"},
		{"quoted tool name in a substitution", "x=$('git' ls-files)\n", "command-substitution"},
		{"substitution right of &&", "true && out=$(git ls-files)\n", "command-substitution"},
		{"tool after && in a substitution", "out=$(cd x && git ls-files)\n", "command-substitution"},
		{"tool after ; in a substitution", "out=$(printf ready; git ls-files)\n", "command-substitution"},
		{"status read after a later substitution", "out=$(find . -type f)\ndiscard=$(true) rc=$?\n", "command-substitution"},
		{"status read after a later pipeline stage", "out=$(find . -type f)\nprintf x | rc=$?\n", "command-substitution"},
		{"wait for another job", "out=$(git ls-files)\nwait \"$pid\"\n", "command-substitution"},
		{"tool behind xargs", "xargs git rm 2>/dev/null\n", "dev-null"},
		{"tool behind sudo", "sudo git fetch || true\n", "or-true"},
		{"eval in a substitution", "out=$(eval \"$cmd\")\n", "command-substitution"},
		{"variable command word in a substitution", "out=$(\"${tool}\" ls)\n", "command-substitution"},
		{"source in a substitution", "out=$(source ./x.sh)\n", "command-substitution"},
		{"marker on a later command", "git fetch || true; printf '%s\\n' ok # fail-closed: output is harmless\n", "or-true"},
		{"|| true behind a quote nested in a substitution", "x=\"$(getent passwd \"${uid}\" | cut -d: -f1 || true)\"\n", "or-true"},
		{"tool behind xargs -n", "printf x | xargs -n 1 git fetch || true\n", "or-true"},
		{"tool behind sudo -u", "sudo -u user git fetch || true\n", "or-true"},
		{"successful exit handler", "x=$(git ls-files) || exit 0\n", "command-substitution"},
		{"successful return handler", "x=$(git ls-files) || return 0\n", "command-substitution"},
		{"assignment that starts with exit", "x=$(git ls-files) || exit_code=1\n", "command-substitution"},
		{"assignment that names a failure", "out=$(git ls-files) || failure_count=1\n", "command-substitution"},
		{"echo that names a failure", "out=$(git ls-files) || echo fail\n", "command-substitution"},
		{"exit behind an echo handler", "out=$(git ls-files) || echo no list || exit 1\n", "command-substitution"},
		{"group handler without a terminator", "out=$(git ls-files) || { echo no list; }\n", "command-substitution"},
		{"tool behind env", "env git fetch origin || true\n", "or-true"},
		{"tool behind env with options", "env -i -u NAME -C /tmp A=1 git rev-parse HEAD 2>/dev/null\n", "dev-null"},
		{"tool behind env in a substitution", "out=$(env -S 'A=1' git ls-files)\n", "command-substitution"},

		{"status handler", "x=$(git ls-files) || exit 1\n", ""},
		{"bare return handler", "x=$(git ls-files) || return\n", ""},
		{"fail handler", "out=$(git ls-files) || fail \"no list\"\n", ""},
		{"die handler", "out=$(git ls-files) || die\n", ""},
		{"group handler that exits", "out=$(git ls-files) || { echo no list; exit 1; }\n", ""},
		{"env alone is no tool", "env 2>/dev/null\n", ""},
		{"env in a pipeline is no tool", "env | grep X || true\n", ""},
		{"status handler after a multi-line substitution", "x=\"$(\n  git ls-files\n)\" || exit 1\n", ""},
		{"status read", "x=$(git ls-files)\nrc=$?\n", ""},
		{"status test", "x=$(git ls-files)\n[[ $? -eq 0 ]] || exit 1\n", ""},
		{"handler on the continued line", "x=\"$(git ls-files)\" ||\n  die \"no list\"\n", ""},
		{"if tests the call", "if x=$(gh api /y); then :; fi\n", ""},
		{"inline marker", "git fetch || true # fail-closed: a stale ref is harmless here\n", ""},
		{"marker on the line before", "# fail-closed: best effort cleanup\ngit gc 2>/dev/null\n", ""},
		{"marker needs a reason", "git gc || true # fail-closed:\n", "or-true"},
		{"comment names the idiom", "# git fetch || true\n", ""},
		{"message names the idiom", "echo 'git fetch || true'\n", ""},
		{"quoted span across lines", "msg='\ngit fetch || true\n'\n", ""},
		{"heredoc body", "cat <<'EOF'\ngit fetch || true\nEOF\n", ""},
		{"not a tool call", "grep -q x file || true\n", ""},
		{"path is not a tool", "ls .git/hooks 2>/dev/null\n", ""},
		{"tool name as an argument", "printf '%s\\n' git || true\n", ""},
		{"tool name after echo", "echo gh 2>/dev/null\n", ""},
		{"quoted $( runs nothing", "x=$(echo '$(git' x) || exit 1\n", ""},
		{"escaped ; is an argument", "if ! x=\"$(find . -exec test -e {} \\; -print)\"; then exit 1; fi\n", ""},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			findings, err := metadatautil.ShellFailOpenFindings(testCase.script)
			if err != nil {
				t.Fatal(err)
			}
			if testCase.rule == "" {
				if len(findings) != 0 {
					t.Fatalf("findings = %v, want none", findings)
				}
				return
			}
			if !slices.ContainsFunc(findings, func(each metadatautil.ShellFailOpenFinding) bool { return each.Rule == testCase.rule }) {
				t.Fatalf("findings = %v, want rule %s", findings, testCase.rule)
			}
		})
	}
}

// fixtureRepo builds a git repository holding one script and a baseline.
func fixtureRepo(t *testing.T, script, baseline string) string {
	t.Helper()
	root := t.TempDir()
	for name, body := range map[string]string{
		"scripts/a.sh":                        script,
		"policy/shell-fail-open-baseline.tsv": baseline,
	} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "."}} {
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return root
}

// Negative control and ratchet: a planted hit fails, the same hit passes with
// a baseline row, and a row that is too high or stale fails.
func TestCheckShellFailOpenRatchet(t *testing.T) {
	t.Parallel()
	planted := "#!/bin/bash\ngit fetch origin || true\n"
	row := "scripts/a.sh\tor-true\t1\tpredates the rule\n"
	cases := []struct {
		name, script, baseline, want string
	}{
		{"planted violation fails", planted, "", "baseline allows 0"},
		{"baselined hit passes", planted, row, ""},
		{"fixed hit leaves headroom", "#!/bin/bash\ngit fetch origin || exit 1\n", row, "stale baseline row"},
		{"row above the count fails", planted, strings.Replace(row, "\t1\t", "\t2\t", 1), "lower the baseline row to 1"},
		{"second hit fails", planted + "git gc || true\n", row, "baseline allows 1"},
		{"row without a reason fails", planted, "scripts/a.sh\tor-true\t1\t\n", "non-empty REASON"},
		{"unknown rule fails", planted, "scripts/a.sh\tbogus\t1\twhy\n", "unknown rule"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			err := metadatautil.CheckShellFailOpen(fixtureRepo(t, testCase.script, testCase.baseline))
			if testCase.want == "" {
				if err != nil {
					t.Fatalf("error = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("error = %v, want %q", err, testCase.want)
			}
		})
	}
}
