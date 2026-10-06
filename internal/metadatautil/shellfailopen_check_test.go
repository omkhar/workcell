// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil_test

import (
	"os"
	"os/exec"
	"path/filepath"
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

		{"status handler", "x=$(git ls-files) || exit 1\n", ""},
		{"status handler after a multi-line substitution", "x=\"$(\n  git ls-files\n)\" || exit 1\n", ""},
		{"status read", "x=$(git ls-files)\nrc=$?\n", ""},
		{"if tests the call", "if x=$(gh api /y); then :; fi\n", ""},
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
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			findings := metadatautil.ShellFailOpenFindings(testCase.script)
			if testCase.rule == "" {
				if len(findings) != 0 {
					t.Fatalf("findings = %v, want none", findings)
				}
				return
			}
			if len(findings) == 0 || findings[0].Rule != testCase.rule {
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
