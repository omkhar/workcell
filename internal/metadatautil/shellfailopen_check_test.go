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

// TestShellFailOpenFindingsCountEachCommand pins that a second hit of the same rule on one line is a second finding, so the file's baseline count rises.
func TestShellFailOpenFindingsCountEachCommand(t *testing.T) {
	t.Parallel()
	findings, err := metadatautil.ShellFailOpenFindings("git fetch || true; git gc || true\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 2 || findings[0].Rule != "or-true" || findings[1].Rule != "or-true" {
		t.Fatalf("findings = %v, want two or-true findings on one line", findings)
	}
	findings, err = metadatautil.ShellFailOpenFindings("git fetch || true && git gc || true\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 2 {
		t.Fatalf("findings = %v, want two or-true findings on one list", findings)
	}
	for script, want := range map[string]int{
		"git fetch || true && printf retrying || true\n": 1, // the second handler belongs to printf
		"git fetch && printf x || true\n":                1, // the handler covers the whole && list
	} {
		findings, err = metadatautil.ShellFailOpenFindings(script)
		if err != nil {
			t.Fatal(err)
		}
		if len(findings) != want {
			t.Fatalf("findings for %q = %v, want %d command-substitution findings", script, findings, want)
		}
	}
	findings, err = metadatautil.ShellFailOpenFindings("git fetch || true\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 {
		t.Fatalf("findings = %v, want one or-true finding", findings)
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
		{"command-wrapped tool with || true", "command -p git fetch origin || true\n", "or-true"},
		{"command -- wraps the tool", "command -- git fetch origin || true\ncommand -p -- git fetch || true\n", "or-true"},
		{"redirect on another operand hides no tool status", "git fetch || { printf '%s\\n' retrying 2>/dev/null; exit 1; }\n", ""},
		{"later operand keeps its own status test", "git fetch && git gc 2>/dev/null\nprintf done\n", "dev-null"},
		{"redirect on a piped stage is not the tool's", "git fetch |& cat 2>/dev/null\n", ""},
		{"assignment with a substitution before a tool", "MODE=$(printf x) git fetch origin || true\n", "or-true"},
		{"redirect inside a substitution is the tool's", "X=\"${X:-$(git log -1 2>/dev/null || printf 0)}\"\n", "dev-null"},
		{"redirect outside a substitution is not the tool's", "foo \"$(git log -1)\" 2>/dev/null\n", "command-substitution"},
		{"every handler that does not fail swallows the failure", "git fetch || 'true'\ngit gc || \":\"\ngit fetch || $'true'\ngit gc || $':'\ngit fetch || $'\\164rue'\ngit gc || $'\\072'\ngit fetch || { true; }\ngit gc || command -p -- true\ngit pull || builtin -- true\ngit push || command -v true\ngit fetch || true-wrapper\ngit gc || echo warn\ngit fetch || t'rue'\ngit gc || 'tr'\"ue\"\ngit fetch || :\ngit fetch || :; echo done\ngit fetch origin || true\n", "or-true"},
		{"ANSI-C quoted /dev/null still hides stderr", "git fetch 2>$'/dev/null'\n", "dev-null"},
		{"ANSI-C escaped /dev/null still hides stderr", "git fetch 2>$'\\057dev/null'\n", "dev-null"},
		{"time -p keeps the substitution status", "time -p -- out=$(git ls-files) || exit 1\n", ""},
		{"quoted or escaped tool name is the tool", "g'it' fetch || true\n\"git\" fetch || true\ng\\it gc 2>/dev/null\n", "or-true"},
		{"compound quoted /dev/null still hides stderr", "git fetch 2>/d'ev/null'\ngit gc 2>|/dev/null; printf done\ngit pull >&/dev/null\n", "dev-null"},
		{"a later command masks a failure an && list leaves", "git fetch 2>/dev/null && printf done; printf next\n", "dev-null"},
		{"a failure passed on by false still reaches the next handler", "git fetch || false || true\n", "or-true"},
		{"an exit or die handler ends the list before a later true", "git fetch || exit 1 || true\ngit gc || die x || true\ngit pull || { echo x; exit 1; } || true\n", ""},
		{"a tool behind env is the tool", "env git -C /missing fetch || true\n/usr/bin/env -u X A=1 git fetch || true\n", "or-true"},
		{"any wrapper option may take a value", "env -iC / git -C /missing fetch || true\n", "or-true"},
		{"sudo long option with a value", "sudo --user root git -C /missing fetch || true\n", "or-true"},
		{"xargs long option with a value", "xargs --max-args 1 git -C /missing fetch || true\n", "or-true"},
		{"env long options take their values", "env --chdir / git -C /missing fetch || true\n", "or-true"},
		{"an exit status bash reads modulo 256 as nonzero fails", "git fetch || exit 257\ngit gc || return -1\ngit pull || exit 010\n", ""},
		{"a tool called by its path is the tool", "/usr/bin/git fetch || true\n", "or-true"},
		{"coproc runs the tool", "coproc git -C /missing fetch 2>/dev/null; echo done\n", "dev-null"},
		{"a named coproc runs its compound command", "coproc worker { git fetch 2>/dev/null; }\n", "dev-null"},
		{"a name before a simple coproc command is the command", "coproc worker git fetch 2>/dev/null\n", ""},
		{"a case arm runs the tool", "case x in x) git -C /missing fetch 2>/dev/null;; esac; echo done\ncase $y in\n  a|b) git gc 2>/dev/null ;;\nesac\n", "dev-null"},
		{"a word after a command substitution is no command", "echo $(pwd) git 2>/dev/null\n", ""},
		{"a process substitution as an argument hides the tool", "cat <(git -C /missing ls-files)\ntee >(git hash-object --stdin) </dev/null\n", "process-substitution"},
		{"a failing brace group hands the failure on to a later handler", "git fetch || { false; } || true\n", "or-true"},
		{"an exit in a nested branch does not end the handler", "git fetch || { if false; then exit 1; fi; false; } || true\n", "or-true"},
		{"a failing subshell hands the failure on to a later handler", "git fetch || ( exit 1 ) || true\n", "or-true"},
		{"exit -- STATUS and a failing subshell keep the failure", "git fetch || exit -- 1\ngit gc || ( exit 1 )\ngit pull || ( false )\n", ""},
		{"nice keeps the status of the tool it runs", "nice git fetch || true\ntimeout 5 git pull || true\n", "or-true"},
		{"external time keeps the status of the tool it runs", "/usr/bin/time -f '' git fetch || true\n", "or-true"},
		{"a backtick substitution is a command substitution", "out=`git ls-files`; echo done\n", "command-substitution"},
		{"a backtick in single quotes is text", "echo '`git ls-files`'; echo done\n", ""},
		{"a descriptor that ends in 2 is not stderr", "git fetch 12>/dev/null; echo done\n", ""},
		{"an exit operand out of range fails with status 2", "git fetch || exit 9223372036854775808\n", ""},
		{"a longer path that starts with /dev/null is no null redirect", "git fetch 2>\"/dev/null.backup\"\ngit gc 2>/dev/nullx\n", ""},
		{"a quoted tool path is the tool", "\"/usr/bin/git\" -C /missing fetch 2>/dev/null; echo done\n", "dev-null"},
		{"a tool called by its path hides stderr", "/usr/bin/git fetch 2>/dev/null\n", "dev-null"},
		{"an exit status that wraps to zero swallows the failure", "git fetch || exit 256\n", "or-true"},
		{"&& after a pipe reads the last stage", "git fetch 2>/dev/null | cat && echo done\n", "dev-null"},
		{"brace group redirect is its calls'", "{ git fetch; } 2>/dev/null; echo done\n", "dev-null"},
		{"time -p wraps the tool", "time -p -- git fetch || true\n", "or-true"},
		{"quoted /dev/null still hides stderr", "git fetch 2>\"/dev/null\"\n", "dev-null"},
		{"handler on a later background segment is not the tool's", "git fetch & printf retrying || true\n", ""},
		{"background operator ends the operand", "git fetch & printf retrying 2>/dev/null\n", ""},
		{"redirect to /dev/null with a duplicated descriptor is the tool's", "git fetch >/dev/null 2>&1 & wait\n", "dev-null"},
		{"redirect on the tool operand of a list", "printf '%s\\n' fetching || git fetch 2>/dev/null\n", "dev-null"},
		{"append redirect to /dev/null", "git fetch 2>>/dev/null\n", "dev-null"},
		{"combined redirect to /dev/null", "git fetch &>/dev/null\n", "dev-null"},
		{"stdout to /dev/null then stderr to it", "git fetch >/dev/null 2>&1\n", "dev-null"},
		{"command substitution", "local out\nout=\"$(git ls-files)\"\n", "command-substitution"},
		{"command substitution in a compound body", "f() {\n  if true; then\n    x=$(gh api /y)\n  fi\n}\n", "command-substitution"},
		{"dev null", "git rev-parse HEAD 2>/dev/null\n", "dev-null"},
		{"continued line", "docker \\\n  ps 2>/dev/null\n", "dev-null"},
		{"env prefix before the tool", "x=$(LC_ALL=C getent hosts a)\n", "command-substitution"},
		{"quoted substitution across lines", "x=\"$(\n  git ls-files\n)\"\n", "command-substitution"},
		{"substitution across lines", "x=$(\n  git ls-files # list\n)\n", "command-substitution"},
		{"arithmetic joins no line", "# fail-closed: counter only\nn=$((n + 1))\ngit gc || true\n", "or-true"},
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
		{"group handler that ends in success", "out=$(git ls-files) || { false; true; }\n", "command-substitution"},
		{"status read after a later substitution in the same command", "out=$(git ls-files) discard=$(true) rc=$?\n", "command-substitution"},
		{"&& after a non-failing || handler", "out=$(git ls-files) || echo ignored && echo done\n", "command-substitution"},
		{"handler after local", "local out=$(git ls-files) || exit 1\n", "command-substitution"},
		{"status read after local", "local out=$(git ls-files)\nrc=$?\n", "command-substitution"},
		{"handler after a substitution argument", "echo \"$(git ls-files)\" || exit 1\n", "command-substitution"},

		{"status handler", "x=$(git ls-files) || exit 1\n", ""},
		{"handler after a separate local", "local out; out=$(git ls-files) || exit 1\n", ""},
		{"bare return handler", "x=$(git ls-files) || return\n", ""},
		{"fail handler", "out=$(git ls-files) || fail \"no list\"\n", ""},
		{"die handler", "out=$(git ls-files) || die\n", ""},
		{"group handler that exits", "out=$(git ls-files) || { echo no list; exit 1; }\n", ""},
		{"bare exit after a command in a group handler", "out=$(git ls-files) || { printf ignored; exit; }\nf() {\n  out=$(git ls-files) || { printf x; return; }\n}\n", "command-substitution"},
		{"bare exit as the only command of a group handler", "out=$(git ls-files) || { exit; }\nout=$(git ls-files) || { false; exit; }\n", ""},
		{"quoted ) inside a substitution", "out=$(printf ')'; git -C /missing ls-files); echo done\n", "command-substitution"},
		{"group handler that ends in false", "out=$(git ls-files) || { false; }\n", ""},
		{"status handler after a multi-line substitution", "x=\"$(\n  git ls-files\n)\" || exit 1\n", ""},
		{"status read", "x=$(git ls-files)\nrc=$?\n", ""},
		{"status read in the same command", "out=$(git ls-files) rc=$?\n", ""},
		{"status read behind ||", "out=$(git ls-files) || rc=$?\n", ""},
		{"status read behind || after 1>&2", "git rev-parse HEAD 2>/dev/null 1>&2 || rc=$?\n", ""},
		{"&& alone does not prove the failure propagates", "out=$(git ls-files) && echo done\n", "command-substitution"},
		{"status test", "x=$(git ls-files)\n[[ $? -eq 0 ]] || exit 1\n", ""},
		{"handler on the continued line", "x=\"$(git ls-files)\" ||\n  die \"no list\"\n", ""},
		{"if tests the call", "if x=$(gh api /y); then :; fi\n", ""},
		{"comment names the idiom", "# git fetch || true\n", ""},
		{"message names the idiom", "echo 'git fetch || true'\n", ""},
		{"quoted span across lines", "msg='\ngit fetch || true\n'\n", ""},
		{"heredoc body", "cat <<'EOF'\ngit fetch || true\nEOF\n", ""},
		{"escaped apostrophe in an ANSI-C span", ": $'x\\'; git fetch || true'\n", ""},
		{"noclobber redirection target", ": >| git fetch origin || true\n", ""},
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

// The shared evasion corpus, run against the fail-open check. The check reports what may run, so a row that only gates the call, as if false, a function body or a guarded group do, keeps the hit, and a row that turns the call into a comment, a heredoc body or quoted text drops it. Each row states its expectation, so a new row fails here until it does.
func TestShellFailOpenFindingsUnderTheEvasionCorpus(t *testing.T) {
	t.Parallel()
	const anchor = "git fetch origin || true"
	artifact := "#!/bin/bash\n" + anchor + "\n"
	keepsHit := map[string]bool{
		"full-line comment": false, "inline comment after || true": false, "echo-quoted": false,
		"single heredoc": false, "multi heredoc <<A <<B": false, "arithmetic shift": false,
		"here-string": false, "line continuation": false, "quoted line span": false,
		"indented heredoc terminator": false, "uncalled function definition": true, "unreachable branch": true,
		"escaped closer in a quoted span": false, "conditional right-hand side": true, "conditional across a line break": true,
		"exit before the command": true, "definition brace on the next line": true, "heredoc opened as a quote closes": false,
		"exec before the command": true, "noclobber redirection": false, "quoted span closing into arguments": false,
		"quoted separator": false, "quoted compound-command closer": true, "quoted brace in a definition": true,
		"ANSI-C heredoc delimiter": false, "ANSI-C escape in a heredoc delimiter": false, "single-quoted line break": false,
		"locale-translated heredoc delimiter": false, "escaped apostrophe in an ANSI-C word": false, "ANSI-C span across a line break": false,
		"negated guarded group": true, "conditional command group": true, "argument brace in a guarded group": true,
		"argument brace in a definition body": true, "conditional subshell group": true, "negated guarded subshell": true,
		"array assignment in a guarded subshell": true, "attached subshell opener": true, "attached subshell opener closed on its own line": true,
		"substitution inside a subshell opener": true, "quoted fragment in a subshell opener": true, "parameter expansion in a subshell opener": true,
		"subshell behind a reserved prefix": true, "subshell behind a named coproc": true, "paired closers in a subshell opener": true,
		"prefix extension": false, "unrelated placement": true,
	}
	if len(keepsHit) != len(Evasions) {
		t.Fatalf("%d expectations for %d corpus rows; name each row once", len(keepsHit), len(Evasions))
	}
	for _, evasion := range Evasions {
		t.Run(evasion.Name, func(t *testing.T) {
			t.Parallel()
			want, stated := keepsHit[evasion.Name]
			if !stated {
				t.Fatalf("corpus row %q states no expectation", evasion.Name)
			}
			mutated := evasion.Rewrite(artifact, anchor)
			if mutated == artifact {
				t.Fatalf("evasion %q left the artifact unchanged", evasion.Name)
			}
			findings, err := metadatautil.ShellFailOpenFindings(mutated)
			if err != nil {
				t.Fatal(err)
			}
			if got := len(findings) > 0; got != want {
				t.Fatalf("findings = %v, want a hit %v", findings, want)
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

// Negative control and ratchet: a planted hit fails, the same hit passes with a baseline row, and a row that is too high or stale fails.
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

func TestCheckShellFailOpenReadsTheShebangInterpreter(t *testing.T) {
	t.Parallel()
	for file, shell := range map[string]bool{"x\n#!/bin/sh -e": true, "x\n#!/usr/bin/env -S BASH_ENV= ENV= bash": true, "x\n#!/usr/bin/env python3": false, "x\n#![no_main]": false, "u.bash\n# a sourced module": true, "z\n#!/bin/zsh": true, "e\n#!/usr/bin/env -S -u FOO sh": true, "q\n#!/usr/bin/env -S -u FOO 'sh'": true} {
		root := fixtureRepo(t, "#!/bin/bash\n", "")
		name, first, _ := strings.Cut(file, "\n")
		if err := os.WriteFile(filepath.Join(root, "scripts", name), []byte(first+"\ngit fetch || true\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command("git", "-C", root, "add", ".").CombinedOutput(); err != nil {
			t.Fatalf("git add: %v\n%s", err, out)
		}
		if err := metadatautil.CheckShellFailOpen(root); (err != nil) != shell {
			t.Errorf("CheckShellFailOpen with %q = %v, want a hit: %v", first, err, shell)
		}
	}
}
