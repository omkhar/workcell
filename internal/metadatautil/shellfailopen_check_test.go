// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil_test

import (
	"fmt"
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
		{"local in an if test", "if local out=$(git ls-files); then :; fi\n", "command-substitution"},
		{"test of a substitution", "if [[ -n \"$(git ls-files)\" ]]; then :; fi\n", "command-substitution"},
		{"local before a handler", "local out=$(git ls-files) || exit 1\n", "command-substitution"},
		{"status read before the call", "prior=$? out=$(git ls-files)\n", "command-substitution"},
		{"status read on the continued line before", "prior=$? \\\n  out=$(git ls-files)\n", "command-substitution"},
		{"tool attached to ||", "git||true\n", "or-true"},
		{"tool attached to &&", "git&&gh||true\n", "or-true"},
		{"tool attached to a subshell closer", "(git)2>/dev/null\n", "dev-null"},
		{"tool attached to a redirection", "docker>/dev/null 2>/dev/null\n", "dev-null"},
		{"tool name split by quotes", "\"gi\"\"t\" ls-files || true\n", "or-true"},
		{"tool name with a quoted fragment", "'g'it fetch || true\n", "or-true"},
		{"tool name with an escape", "g\\it fetch || true\n", "or-true"},
		{"quoted tool name in a substitution", "x=$('git' ls-files)\n", "command-substitution"},
		{"substitution right of &&", "true && out=$(git ls-files)\n", "command-substitution"},
		{"command word after the substitution", "OUT=$(git ls-files) true || exit 1\n", "command-substitution"},
		{"later substitution owns the status", "out=$(git ls-files) filler=$(true)\nrc=$?\n", "command-substitution"},
		{"tool after && in a substitution", "out=$(cd x && git ls-files)\n", "command-substitution"},
		{"tool after ; in a substitution", "out=$(printf ready; git ls-files)\n", "command-substitution"},
		{"|| true inside a substitution", "out=$(git ls-files || true) || exit 1\n", "command-substitution"},
		{"later command inside a substitution", "out=$(git ls-files; true) || exit 1\n", "command-substitution"},
		{"wait for another job", "out=$(git ls-files)\nwait \"$pid\"\n", "command-substitution"},
		{"process substitution then wait for another job", "while read -r f; do :; done < <(find . -type f)\nwait \"$pid\"\n", "process-substitution"},
		{"tool behind env", "env LC_ALL=C git fetch || true\n", "or-true"},
		{"tool behind command", "command git fetch || true\n", "or-true"},
		{"tool behind nice", "nice -n 5 git gc || true\n", "or-true"},
		{"tool behind xargs", "xargs git rm 2>/dev/null\n", "dev-null"},
		{"tool behind sudo", "sudo git fetch || true\n", "or-true"},
		{"eval in a substitution", "out=$(eval \"$cmd\")\n", "command-substitution"},
		{"variable command word in a substitution", "out=$(\"${tool}\" ls)\n", "command-substitution"},
		{"source in a substitution", "out=$(source ./x.sh)\n", "command-substitution"},
		{"pipeline stage in a substitution", "out=$(git ls-files | sort) || exit 1\n", "command-substitution"},
		{"later command after eval in a substitution", "out=$(eval \"$cmd\"; true) || exit 1\n", "command-substitution"},
		{"marker on a later command", "git fetch || true; printf '%s\\n' ok # fail-closed: output is harmless\n", "or-true"},
		{"tool behind command --", "command -- git fetch || true\n", "or-true"},
		{"tool behind command -p --", "command -p -- git fetch || true\n", "or-true"},
		{"|| true behind a quote nested in a substitution", "x=\"$(getent passwd \"${uid}\" | cut -d: -f1 || true)\"\n", "or-true"},

		{"status handler", "x=$(git ls-files) || exit 1\n", ""},
		{"status handler after a multi-line substitution", "x=\"$(\n  git ls-files\n)\" || exit 1\n", ""},
		{"status read", "x=$(git ls-files)\nrc=$?\n", ""},
		{"assignment and redirection after the substitution", "x=$(git ls-files) y=1 2>/dev/null || exit 1\n", ""},
		{"quoted tool name with a handler", "x=$('git' ls-files) || exit 1\n", ""},
		{"handler on the continued line", "x=\"$(git ls-files)\" ||\n  die \"no list\"\n", ""},
		{"if tests the call", "if x=$(gh api /y); then :; fi\n", ""},
		{"wait after the loop", "while read -r f; do :; done < <(find . -type f)\nwait $!\n", ""},
		{"sentinel", "while read -r f; do walk_completed=1; done < <(find . -type f)\n[[ \"${walk_completed}\" -eq 1 ]] || exit 1\n", ""},
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
		{"handled tool after &&", "out=$(cd x && git ls-files) || exit 1\n", ""},
		{"handler inside the substitution", "out=$(git ls-files || exit 1) || exit 1\n", ""},
		{"pipeline stage under pipefail", "set -euo pipefail\nout=$(git ls-files | sort) || exit 1\n", ""},
		{"brace closes a group in a substitution", "x=$({\n  \"$go\" env X\n}) || exit 1\n", ""},
		{"command -v runs nothing", "bin=$(command -v docker 2>/dev/null || true)\n", ""},
		{"|| inside a [[ ]] test", "x=\"$([[ \"$a\" == b || \"$c\" == d ]] && printf y)\"\n", ""},
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

// The wait a process substitution needs is a command the script must run, so
// every row of the shared corpus hides it and must leave the hit reported.
func TestShellFailOpenFindingsRejectsEvasions(t *testing.T) {
	t.Parallel()
	anchor := `wait "$!"`
	script := "while read -r f; do :; done < <(find . -type f)\n" + anchor + "\n"
	validate := func(script string) error {
		findings, err := metadatautil.ShellFailOpenFindings(script)
		if err != nil {
			return fmt.Errorf("process-substitution unproven: %w", err)
		}
		if len(findings) > 0 {
			return fmt.Errorf("%s at line %d", findings[0].Rule, findings[0].Line)
		}
		return nil
	}
	if err := validate(script); err != nil {
		t.Fatal(err)
	}
	RequireRejectsAllEvasions(t, script, anchor, "process-substitution", validate)
}

// A heredoc body this reader cannot end could hide any command, so the scan
// fails closed instead of skipping the rest of the script.
func TestShellFailOpenFindingsRejectsUnendedHeredocs(t *testing.T) {
	t.Parallel()
	for name, script := range map[string]string{
		"ANSI-C delimiter":          ": <<$'\\x50LAN'\nx\nPLAN\ngit fetch || true\n",
		"locale delimiter":          ": <<$\"PLAN\"\nx\nPLAN\ngit fetch || true\n",
		"heredoc that never closes": "cat <<EOF\ngit fetch || true\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if findings, err := metadatautil.ShellFailOpenFindings(script); err == nil {
				t.Fatalf("findings = %v, want an error", findings)
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
