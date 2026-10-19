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
		"( exec 1>/dev/null; ); git -C /missing fetch 2>&1\n(\nexec 1>/dev/null\ngit fetch 2>&1\n)\ngit gc 2>&1\nexec 1>/dev/null | cat; git gc 2>&1\n{ exec 1>/dev/null; }; git gc 2>&1\n":                             6, // a subshell or a left pipeline stage keeps its exec redirect to itself; a { } group does not
		"builtin exec 1>/dev/null; git -C /missing fetch 2>&1; echo done\nexec 3>/dev/null; exec 1>&3; git -C /missing fetch 2>&1\nexec 03>/dev/null; exec 01>&03; git -C /missing fetch 02>&01\ngit gc 02>/dev/null\n": 5, // builtin exec keeps no redirect; exec 1>&3 may reach a saved null
		"while read -r f; do :; done < <(false) && cat <(true); wait \"$!\"\n": 2, // a later <( sets $!, so the wait no longer covers the loop
		"out=$(printf '%s' \"$(git ls-files)\")\n":                             1, // the nested substitution owns the call
		"x=$(y=$(git rev-parse HEAD); git ls-files)\n":                         2, // each substitution runs its own tool
		"exec 3>&1; exec 1>/dev/null; git -C /missing fetch 2>&1; echo done\ngit gc 2>&1; echo done\ncommand -- exec 1>/dev/null; git gc 2>&1\n\\exec 2>/dev/null\nexec -- 1>/dev/null; git -C /missing fetch 2>&1; echo done\nFOO=bar exec -a x 2>/dev/null\nexec -afoo 2>/dev/null\nexec -cla x 2>/dev/null\n! exec 2>/dev/null\nif exec 2>/dev/null; then :; fi\ntime -p exec 2>/dev/null\n2>/dev/null exec\n1>/dev/null exec; git gc 2>&1\n": 17, // exec >/dev/null, after an adjacent exec, makes each later 2>&1 a dev-null use; command -- exec and \exec are exec too, and exec options or assignments keep the redirect
	} {
		findings, err = metadatautil.ShellFailOpenFindings(script)
		if err != nil {
			t.Fatal(err)
		}
		if len(findings) != want {
			t.Fatalf("findings for %q = %v, want %d findings", script, findings, want)
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
	// loop follows the completion protocol of scripts/verify-release-outputs.sh.
	loop := "while read -r -d '' f; do if [[ -z \"$f\" ]]; then walk_completed=1; continue; fi; done < <(find . -print0 && printf '\\0')\n"
	cases := []struct {
		name   string
		script string
		rule   string // empty means no finding
	}{
		{"process substitution", "while read -r f; do echo \"$f\"; done < <(find . -type f)\n", "process-substitution"},
		{"process substitution argument", "diff <(git ls-files) x\n", "process-substitution"},
		{"output process substitution", "git ls-files | tee >(git hash-object --stdin)\n", "process-substitution"},
		{"wait covers only the loop form", "diff <(git ls-files) x\nwait \"$!\"\n", "process-substitution"},
		{"sentinel covers only the loop form", strings.Replace(loop, "done <", "diff <(git ls-files) x; done <", 1) + "[[ \"${walk_completed}\" -eq 1 ]] || exit 1\n", "process-substitution"},
		{"command-wrapped tool with || true", "command -p git fetch origin || true\n", "or-true"},
		{"command -- wraps the tool", "command -- git fetch origin || true\ncommand -p -- git fetch || true\n", "or-true"},
		{"redirect on another operand hides no tool status; redirect on a piped stage is not the tool's; time -p keeps the substitution status; an & redirection on a handler is no background; status handler; handler after a separate local; bare return handler; fail handler; die handler; group handler that exits; escaped apostrophe in an ANSI-C span; noclobber redirection target; not a tool call; path is not a tool; tool name as an argument; an exit or die handler ends the list before a later true; exit -- STATUS and a failing subshell keep the failure; an exit status bash reads modulo 256 as nonzero fails; a name before a simple coproc command is the command; a word after a command substitution is no command; command exit 1 ends the list; a path with dev inside a name is no null device; exec that runs a command is that command; a backtick in single quotes is text; a descriptor that ends in 2 is not stderr; a longer path that starts with /dev/null is no null redirect; an exit operand out of range fails with status 2; an arithmetic comparison is no process substitution; handler on a later background segment is not the tool's; bare exit as the only command of a group handler; group handler that ends in false; status handler after a multi-line substitution; status read; background operator ends the operand; status read in the same command; status read behind ||; status read behind || after 1>&2; status test; handler on the continued line; if tests the call; tool name after echo; quoted $( runs nothing; escaped ; is an argument; comment names the idiom; message names the idiom; quoted span across lines; heredoc body; exec stdout to a longer path is no null; a quoted $? still reads the status", "git fetch || { printf '%s\\n' retrying 2>/dev/null; exit 1; }\ngit fetch |& cat 2>/dev/null\ntime -p -- out=$(git ls-files) || exit 1\nx=$(git ls-files) || exit 1\nlocal out; out=$(git ls-files) || exit 1\nx=$(git ls-files) || return\nout=$(git ls-files) || fail \"no list\"\nout=$(git ls-files) || die\nout=$(git ls-files) || { echo no list; exit 1; }\n: $'x\\'; git fetch || true'\n: >| git fetch origin || true\ngrep -q x file || true\nls .git/hooks 2>/dev/null\nprintf '%s\\n' git || true\ngit fetch || exit 1 || true\ngit gc || die x || true\ngit pull || { echo x; exit 1; } || true\ngit fetch || exit -- 1\ngit gc || ( exit 1 )\ngit pull || ( false )\ngit fetch || exit 257\ngit gc || return -1\ngit pull || exit 010\ncoproc worker git fetch 2>/dev/null\necho $(pwd) git 2>/dev/null\ngit fetch || { command exit 1; } || true\ngit fetch 2>/tmp/review-device/null\ngit fetch 2>/tmp/workcell-review-dev/dev/null\nexec 2>/dev/null printf x\necho '`git ls-files`'; echo done\ngit fetch 12>/dev/null; echo done\ngit foo2>/dev/null; echo done\ngit fetch 2>\"/dev/null.backup\"\ngit gc 2>/dev/nullx\ngit fetch || exit 9223372036854775808\nx=1; y=2; ((x<(y))); echo done\ngit fetch & printf retrying || true\nout=$(git ls-files) || { exit; }\nout=$(git ls-files) || { false; exit; }\nx=\"$(\n  git ls-files\n)\" || exit 1\nx=$(git ls-files)\nrc=$?\ngit fetch & printf retrying 2>/dev/null\nout=$(git ls-files) rc=$?\nout=$(git ls-files) || rc=$?\ngit rev-parse HEAD 2>/dev/null 1>&2 || rc=$?\nx=$(git ls-files)\n[[ $? -eq 0 ]] || exit 1\nx=\"$(git ls-files)\" ||\n  die \"no list\"\nif x=$(gh api /y); then :; fi\necho gh 2>/dev/null\nx=$(echo '$(git' x) || exit 1\nif ! x=\"$(find . -exec test -e {} \\; -print)\"; then exit 1; fi\n# git fetch || true\necho 'git fetch || true'\nmsg='\ngit fetch || true\n'\ncat <<'EOF'\ngit fetch || true\nEOF\nexec 1>/dev/null.backup; git fetch 2>&1; echo done\ngit -C /missing fetch 2>/dev/null; rc=\"$?\"; [ \"$rc\" -eq 0 ] || exit 1\n", ""},
		{"pipefail lets a pipeline handler see the tool status", "set -o pipefail\ngit -C /missing fetch 2>/dev/null | cat || exit 1\n", ""},
		{"without pipefail the last stage sets the pipeline status", "git -C /missing fetch 2>/dev/null | cat || exit 1\n", "dev-null"},
		{"later operand keeps its own status test", "git fetch && git gc 2>/dev/null\nprintf done\n", "dev-null"},
		{"assignment with a substitution before a tool", "MODE=$(printf x) git fetch origin || true\n", "or-true"},
		{"redirect inside a substitution is the tool's", "X=\"${X:-$(git log -1 2>/dev/null || printf 0)}\"\n", "dev-null"},
		{"redirect outside a substitution is not the tool's", "foo \"$(git log -1)\" 2>/dev/null\n", "command-substitution"},
		{"every handler that does not fail swallows the failure", "git fetch || 'true'\ngit gc || \":\"\ngit fetch || $'true'\ngit gc || $':'\ngit fetch || $'\\164rue'\ngit gc || $'\\072'\ngit fetch || { true; }\ngit gc || command -p -- true\ngit pull || builtin -- true\ngit push || command -v true\ngit fetch || true-wrapper\ngit gc || echo warn\ngit fetch || t'rue'\ngit gc || 'tr'\"ue\"\ngit fetch || :\ngit fetch || :; echo done\ngit fetch origin || true\n", "or-true"},
		{"ANSI-C quoted /dev/null still hides stderr", "git fetch 2>$'/dev/null'\n", "dev-null"},
		{"ANSI-C escaped /dev/null still hides stderr", "git fetch 2>$'\\057dev/null'\n", "dev-null"},
		{"quoted or escaped tool name is the tool", "g'it' fetch || true\n\"git\" fetch || true\ng\\it gc 2>/dev/null\n", "or-true"},
		{"compound quoted /dev/null still hides stderr", "git fetch 2>/d'ev/null'\ngit fetch 2>\"/dev\"/null; echo done\ngit gc 2>|/dev/null; printf done\ngit pull >&/dev/null\n", "dev-null"},
		{"a later command masks a failure an && list leaves", "git fetch 2>/dev/null && printf done; printf next\n", "dev-null"},
		{"a failure passed on by false still reaches the next handler", "git fetch || false || true\n", "or-true"},
		{"a tool behind env is the tool", "env git -C /missing fetch || true\n/usr/bin/env -u X A=1 git fetch || true\n", "or-true"},
		{"any wrapper option may take a value", "env -iC / git -C /missing fetch || true\n", "or-true"},
		{"sudo long option with a value", "sudo --user root git -C /missing fetch || true\n", "or-true"},
		{"xargs long option with a value", "xargs --max-args 1 git -C /missing fetch || true\n", "or-true"},
		{"env long options take their values", "env --chdir / git -C /missing fetch || true\n", "or-true"},
		{"a tool called by its path is the tool", "/usr/bin/git fetch || true\n", "or-true"},
		{"coproc runs the tool", "coproc git -C /missing fetch 2>/dev/null; echo done\n", "dev-null"},
		{"a named coproc runs its compound command", "coproc worker { git fetch 2>/dev/null; }\n", "dev-null"},
		{"a case arm runs the tool", "case x in x) git -C /missing fetch 2>/dev/null;; esac; echo done\ncase $y in\n  a|b) git gc 2>/dev/null ;;\nesac\n", "dev-null"},
		{"a word after a command substitution is no command", "echo $(pwd) git 2>/dev/null\n", ""},
		{"nice keeps the status of the tool it runs", "nice git fetch || true\ntimeout 5 git pull || true\n", "or-true"},
		{"zsh noglob and nocorrect, and flock, keep the status of the tool", "noglob git -C /missing fetch || true\nnocorrect git gc || true\nflock /tmp/workcell.lock git -C /missing fetch || true\n", "or-true"},
		{"a backtick substitution is a command substitution", "out=`git ls-files`; echo done\n", "command-substitution"},
		{"a backtick substitution may span lines", "out=`\ngit -C /missing rev-parse HEAD\n`\necho done\n", "command-substitution"},
		{"exec 2>/dev/null hides every later failure", "exec 2>/dev/null; git -C /missing fetch; echo done\nexec 3>x &>/dev/null\nexec >&/dev/null; git fetch; echo done\nexec >/dev/null 2>&1; git gc; echo done\n", "dev-null"},
		{"a redirection may come before the command", "2>/dev/null git -C /missing fetch || true\n", "or-true"},
		{"another spelling of the null device path, or a spaced 2>& 1, hides stderr", "git -C /missing fetch 2>/dev//null; echo done\ngit gc 2>/dev/./null\ngit pull 2>/dev/../dev/null\ngit -C /missing fetch >/dev/null 2>& 1; echo done\n", "dev-null"},
		{"a bare PIPESTATUS word reads no status", "git -C /missing fetch 2>/dev/null; echo PIPESTATUS; echo done\n", "dev-null"},
		{"$? after a pipeline reads the last stage, not the tool", "git -C /missing fetch 2>/dev/null | cat; rc=$?; echo done\n", "dev-null"},
		{"a handler after a pipeline reads the last stage, not the tool", "git -C /missing fetch 2>/dev/null | cat || exit 1; echo done\n", "dev-null"},
		{"a { false; } handler or an && status read leaves the failure hidden", "git -C /missing fetch 2>/dev/null || { false; }; echo done\nout=$(git ls-files) || { false; }\ngit -C /missing fetch 2>/dev/null && rc=$?; echo done\n", "dev-null"},
		{"a false handler that does not leave the list still hides the tool", "git -C /missing fetch 2>/dev/null || false; echo done\n", "dev-null"},
		{"a later command in the if test sets its status", "if git -C /missing fetch 2>/dev/null; :; then :; fi; echo done\n", "dev-null"},
		{"$? after coproc is the launch status", "coproc git fetch 2>/dev/null; rc=$?; echo done\n", "dev-null"},
		{"! inverts the status a handler reads", "! git -C /missing fetch 2>/dev/null || exit 1; echo done\n", "dev-null"},
		{"stderr duplicated onto a saved null descriptor", "git -C /missing fetch 3>/dev/null 2>&3; echo done\ngit gc 0</dev/null 2>&0; echo done\ngit -C /missing fetch 10>/dev/null 2>&10; echo done\ngit gc 2>&-; echo done\nexec {fd}>/dev/null; git -C /missing fetch 2>&\"$fd\"; echo done\nexec 1>/dev/null; git -C /missing fetch 2>&1; echo done\n", "dev-null"},
		{"an if test of a pipeline reads the last stage, not the tool", "if git -C /missing fetch 2>/dev/null | cat; then :; fi; echo done\n", "dev-null"},
		{"a quoted tool path is the tool", "\"/usr/bin/git\" -C /missing fetch 2>/dev/null; echo done\n", "dev-null"},
		{"a tool called by its path hides stderr", "/usr/bin/git fetch 2>/dev/null\n", "dev-null"},
		{"an exit status that wraps to zero swallows the failure", "git fetch || exit 256\n", "or-true"},
		{"&& after a pipe reads the last stage", "git fetch 2>/dev/null | cat && echo done\n", "dev-null"},
		{"brace group redirect is its calls'", "{ git fetch; } 2>/dev/null; echo done\n", "dev-null"},
		{"time -p wraps the tool", "time -p -- git fetch || true\n", "or-true"},
		{"quoted /dev/null still hides stderr", "git fetch 2>\"/dev/null\"\n", "dev-null"},
		{"redirect to /dev/null with a duplicated descriptor is the tool's", "git fetch >/dev/null 2>&1 & wait\n", "dev-null"},
		{"redirect on the tool operand of a list", "printf '%s\\n' fetching || git fetch 2>/dev/null\n", "dev-null"},
		{"append redirect to /dev/null", "git fetch 2>>/dev/null\n", "dev-null"},
		{"combined redirect to /dev/null", "git fetch &>/dev/null\n", "dev-null"},
		{"stdout to /dev/null then stderr to it", "git fetch >/dev/null 2>&1\n", "dev-null"},
		{"marked process substitution still counts", "while read -r f; do echo \"$f\"; done < <(find . -type f) # fail-closed: inventory\n", "process-substitution"},
		{"command substitution", "local out\nout=\"$(git ls-files)\"\n", "command-substitution"},
		{"command substitution in a compound body", "f() {\n  if true; then\n    x=$(gh api /y)\n  fi\n}\n", "command-substitution"},
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
		{"status read after a later substitution", "out=$(find . -type f)\ndiscard=$(true) rc=$?\n", "command-substitution"},
		{"status read after a later pipeline stage", "out=$(find . -type f)\nprintf x | rc=$?\n", "command-substitution"},
		{"|| true inside a substitution", "out=$(git ls-files || true) || exit 1\n", "command-substitution"},
		{"later command inside a substitution", "out=$(git ls-files; true) || exit 1\n", "command-substitution"},
		{"wait for another job", "out=$(git ls-files)\nwait \"$pid\"\n", "command-substitution"},
		{"process substitution then a loop status read", "while read -r f; do :; done < <(find . -type f)\nrc=$?\n", "process-substitution"},
		{"process substitution then a loop handler", "while read -r f; do :; done < <(find . -type f)\n[[ $? -eq 0 ]] || exit 1\n", "process-substitution"},
		{"process substitution then wait for another job", "while read -r f; do :; done < <(find . -type f)\nwait \"$pid\"\n", "process-substitution"},
		{"process substitution then a printed sentinel name", "while read -r f; do :; done < <(find . -type f)\nprintf '%s\\n' walk_completed\n", "process-substitution"},
		{"sentinel the loop never sets", "walk_completed=1\nwhile read -r f; do :; done < <(find . -type f)\n[[ \"${walk_completed}\" -eq 1 ]] || exit 1\n", "process-substitution"},
		{"sentinel tested the wrong way", loop + "[[ \"${walk_completed}\" -ne 1 ]] || exit 1\n", "process-substitution"},
		{"sentinel test without a handler", loop + "if [[ \"${walk_completed}\" -ne 1 ]]; then\n  :\nfi\n", "process-substitution"},
		{"sentinel set on an ordinary record", "while read -r -d '' f; do walk_completed=1; done < <(find . -print0 && printf '\\0')\n[[ \"${walk_completed}\" -eq 1 ]] || exit 1\n", "process-substitution"},
		{"sentinel set and then break", strings.Replace(loop, "continue", "break", 1) + "[[ \"${walk_completed}\" -eq 1 ]] || exit 1\n", "process-substitution"},
		{"sentinel set on the terminal record and another", strings.Replace(loop, "do if", "do walk_completed=1; if", 1) + "[[ \"${walk_completed}\" -eq 1 ]] || exit 1\n", "process-substitution"},
		{"sentinel without a terminal record", strings.Replace(loop, " && printf '\\0'", "", 1) + "[[ \"${walk_completed}\" -eq 1 ]] || exit 1\n", "process-substitution"},
		{"sentinel failure branch inside a nested if", loop + "if [[ \"${walk_completed}\" -ne 1 ]]; then\n  if false; then\n    exit 1\n  fi\nfi\n", "process-substitution"},
		{"sentinel failure branch inside a loop", loop + "if [[ \"${walk_completed}\" -ne 1 ]]; then\n  for x in a; do\n    exit 1\n  done\nfi\n", "process-substitution"},
		{"sentinel if branch that exits 0", loop + "if [[ \"${walk_completed}\" -ne 1 ]]; then exit 0; fi\n", "process-substitution"},
		{"sentinel if branch that returns 0", loop + "if [[ \"${walk_completed}\" -ne 1 ]]; then\n  return 0\nfi\n", "process-substitution"},
		{"sentinel handler that exits 0", loop + "[[ \"${walk_completed}\" -eq 1 ]] || exit 0\n", "process-substitution"},
		{"sentinel group handler without an exit", loop + "[[ \"${walk_completed}\" -eq 1 ]] || { echo failed >&2; }\n", "process-substitution"},
		{"sentinel group handler that exits 0", loop + "[[ \"${walk_completed}\" -eq 1 ]] || {\n  echo failed >&2\n  exit 0\n}\n", "process-substitution"},
		{"background job in the loop before wait", "while read -r f; do\n  true &\ndone < <(find . -type f)\nwait \"$!\"\n", "process-substitution"},
		{"one-line background job before wait", "while read -r f; do true & done < <(find . -type f)\nwait \"$!\"\n", "process-substitution"},
		{"coproc in the loop before wait", "while read -r f; do\n  coproc cat\ndone < <(find . -type f)\nwait \"$!\"\n", "process-substitution"},
		{"sentinel name beside a substitution", "out=$(git ls-files) walk_completed=1\n", "command-substitution"},
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
		{"tool behind command --", "command -- git fetch || true\n", "or-true"},
		{"tool behind command -p --", "command -p -- git fetch || true\n", "or-true"},
		{"marker on a later command", "git fetch || true; printf '%s\\n' ok # fail-closed: output is harmless\n", "or-true"},
		{"|| true behind a quote nested in a substitution", "x=\"$(getent passwd \"${uid}\" | cut -d: -f1 || true)\"\n", "or-true"},
		{"pipefail set after the pipeline", "out=$(git ls-files | sort) || exit 1\nset -o pipefail\n", "command-substitution"},
		{"pipefail cleared before the pipeline", "set -o pipefail\nset +o pipefail\nout=$(git ls-files | sort) || exit 1\n", "command-substitution"},
		{"pipefail set in an untaken branch", "if false; then set -o pipefail; fi\nout=$(git ls-files | sort) || exit 1\n", "command-substitution"},
		{"pipefail set right of &&", "true && set -o pipefail\nout=$(git ls-files | sort) || exit 1\n", "command-substitution"},
		{"pipefail set in a function body", "f() {\n  set -o pipefail\n}\nout=$(git ls-files | sort) || exit 1\n", "command-substitution"},
		{"substitution opened after a quoted fragment", "out='a'$(\n  git ls-files; true\n) || exit 1\n", "command-substitution"},
		{"substitution opened after an empty quote", "out=a\"\"$(\ngit ls-files\ntrue) || exit 1\n", "command-substitution"},
		{"quoted ) before a spelled tool name", "out=$(printf \"x)(git\"; git ls-files; true) || exit 1\n", "command-substitution"},
		{"tool behind xargs -n", "printf x | xargs -n 1 git fetch || true\n", "or-true"},
		{"tool behind env -u", "env -u NAME git fetch || true\n", "or-true"},
		{"tool behind sudo -u", "sudo -u user git fetch || true\n", "or-true"},
		{"successful exit handler", "x=$(git ls-files) || exit 0\n", "command-substitution"},
		{"successful return handler", "x=$(git ls-files) || return 0\n", "command-substitution"},
		{"assignment that starts with exit", "x=$(git ls-files) || exit_code=1\n", "command-substitution"},
		{"assignment that names a failure", "out=$(git ls-files) || failure_count=1\n", "command-substitution"},
		{"echo that names a failure", "out=$(git ls-files) || echo fail\n", "command-substitution"},
		{"exit behind an echo handler", "out=$(git ls-files) || echo no list || exit 1\n", "command-substitution"},
		{"group handler without a terminator", "out=$(git ls-files) || { echo no list; }\n", "command-substitution"},
		{"group handler that ends in success", "out=$(git ls-files) || { false; true; }\n", "command-substitution"},
		{"tool behind env", "env git fetch origin || true\n", "or-true"},
		{"tool behind env with options", "env -i -u NAME -C /tmp A=1 git rev-parse HEAD 2>/dev/null\n", "dev-null"},
		{"tool behind env in a substitution", "out=$(env -S 'A=1' git ls-files)\n", "command-substitution"},
		{"status read after a later substitution in the same command", "out=$(git ls-files) discard=$(true) rc=$?\n", "command-substitution"},
		{"&& after a non-failing || handler", "out=$(git ls-files) || echo ignored && echo done\n", "command-substitution"},
		{"handler after local", "local out=$(git ls-files) || exit 1\n", "command-substitution"},
		{"status read after local", "local out=$(git ls-files)\nrc=$?\n", "command-substitution"},
		{"handler after a substitution argument", "echo \"$(git ls-files)\" || exit 1\n", "command-substitution"},
		{"tool in an env -S string", "env -S 'git fetch origin' || true\n", "or-true"},
		{"tool in an attached env -S string", "env -S'git ls-files' 2>/dev/null\n", "dev-null"},
		{"tool in an env --split-string value", "out=$(env --split-string='git ls-files')\n", "command-substitution"},
		{"tool in an env -S string after a long option", "env --ignore-environment -S 'git fetch origin' || true\n", "or-true"},
		{"tool in an env -S string after valued long options", "env --unset=X --chdir /tmp -S 'git ls-files' 2>/dev/null\n", "dev-null"},
		{"tool in an env -S string after a valued short option", "env -u X -S 'git fetch origin' || true\n", "or-true"},
		{"tool in an env -S string after env -P DIR", "env -P /bin -S 'git fetch origin' || true\n", "or-true"},
		{"single-quoted tool name", "'git' fetch origin || true\n", "or-true"},
		{"escaped tool name", "\\git fetch origin || true\n", "or-true"},
		{"double-quoted tool name", "\"git\" rev-parse HEAD 2>/dev/null\n", "dev-null"},

		{"bare exit after a command in a group handler", "out=$(git ls-files) || { printf ignored; exit; }\nf() {\n  out=$(git ls-files) || { printf x; return; }\n}\n", "command-substitution"},
		{"quoted ) inside a substitution", "out=$(printf ')'; git -C /missing ls-files); echo done\n", "command-substitution"},
		{"group handler that ends in false keeps the failure but does not leave", "out=$(git ls-files) || { false; }\n", "command-substitution"},
		{"env alone is no tool", "env 2>/dev/null\n", ""},
		{"env in a pipeline is no tool", "env | grep X || true\n", ""},
		{"env -S string without a tool", "env -S 'printf x' || true\n", ""},
		{"env -S program with a quoted tool name argument", "env -S 'printf %s\\n' 'git' || true\n", ""},
		{"env --help is no tool", "env --help 2>/dev/null || true\n", ""},
		{"status handler after a multi-line substitution", "x=\"$(\n  git ls-files\n)\" || exit 1\n", ""},
		{"status read", "x=$(git ls-files)\nrc=$?\n", ""},
		{"status read in the same command; status read behind ||; status read behind || after 1>&2; status test; handler on the continued line", "out=$(git ls-files) rc=$?\nout=$(git ls-files) || rc=$?\ngit rev-parse HEAD 2>/dev/null 1>&2 || rc=$?\nx=$(git ls-files)\n[[ $? -eq 0 ]] || exit 1\nx=\"$(git ls-files)\" ||\n  die \"no list\"\n", ""},
		{"&& alone does not prove the failure propagates", "out=$(git ls-files) && echo done\n", "command-substitution"},
		{"status test", "x=$(git ls-files)\n[[ $? -eq 0 ]] || exit 1\n", ""},
		{"assignment and redirection after the substitution", "x=$(git ls-files) y=1 2>/dev/null || exit 1\n", ""},
		{"quoted tool name with a handler", "x=$('git' ls-files) || exit 1\n", ""},
		{"handler on the continued line", "x=\"$(git ls-files)\" ||\n  die \"no list\"\n", ""},
		{"if tests the call", "if x=$(gh api /y); then :; fi\n", ""},
		{"wait after the loop", "while read -r f; do :; done < <(find . -type f)\nwait $!\n", ""},
		{"one-line wait after the loop", "while read -r f; do :; done < <(false); wait \"$!\"\n", ""},
		{"wait after a loop with && in it", "while read -r f; do\n  true && :\ndone < <(find . -type f)\nwait \"$!\"\n", ""},
		{"sentinel", loop + "[[ \"${walk_completed}\" -eq 1 ]] || exit 1\n", ""},
		{"sentinel tested by an if", "done_completed=0\nwhile read -r -d '' f; do\n  if [[ -z \"$f\" ]]; then\n    done_completed=1\n    continue\n  fi\ndone < <(find . -print0 && printf '\\0')\n\nif [[ \"${done_completed}\" -ne 1 ]]; then\n  echo failed >&2\n  exit 1\nfi\n", ""},
		{"sentinel group handler", loop + "[[ \"${walk_completed}\" -eq 1 ]] || {\n  echo failed >&2\n  exit 1\n}\n", ""},
		{"sentinel handler that fails", loop + "[[ \"${walk_completed}\" -eq 1 ]] || fail \"listing failed\"\n", ""},
		{"sentinel tested by a one-line if", strings.TrimSuffix(loop, "\n") + "; if [[ \"${walk_completed}\" -ne 1 ]]; then exit 1; fi\n", ""},
		{"inline marker", "git fetch || true # fail-closed: a stale ref is harmless here\n", ""},
		{"marker on the line before", "# fail-closed: best effort cleanup\ngit gc 2>/dev/null\n", ""},
		{"marker needs a reason", "git gc || true # fail-closed:\n", "or-true"},
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
		{"quoted tool name after echo", "echo 'git' || true\n", ""},
		{"quoted tool name as an argument", "printf '%s\\n' \"git\" 2>/dev/null\n", ""},
		{"handled tool after &&", "out=$(cd x && git ls-files) || exit 1\n", ""},
		{"handler inside the substitution", "out=$(git ls-files || exit 1) || exit 1\n", ""},
		{"pipeline stage under pipefail", "set -euo pipefail\nout=$(git ls-files | sort) || exit 1\n", ""},
		{"pipefail set at top level after a function", "f() {\n  :\n}\nset -o pipefail\nout=$(git ls-files | sort) || exit 1\n", ""},
		{"brace closes a group in a substitution", "x=$({\n  \"$go\" env X\n}) || exit 1\n", ""},
		{"command -v runs nothing", "bin=$(command -v docker 2>/dev/null || true)\n", ""},
		{"|| inside a [[ ]] test", "x=\"$([[ \"$a\" == b || \"$c\" == d ]] && printf y)\"\n", ""},
		{"quoted $( runs nothing", "x=$(echo '$(git' x) || exit 1\n", ""},
		{"escaped ; is an argument", "if ! x=\"$(find . -exec test -e {} \\; -print)\"; then exit 1; fi\n", ""},
		{"quiet grep after a pipe under pipefail", "set -euo pipefail\nif ! echo \"$x\" | grep -Fq -- y; then exit 1; fi\n", "quiet-grep-pipe"},
		{"split quiet flag after a pipe", "set -o pipefail\necho \"$x\" | grep -E -q y || exit 1\n", "quiet-grep-pipe"},
		{"command grep after a pipe", "set -o pipefail\nyes | command grep -q y || exit 1\n", "quiet-grep-pipe"},
		{"long quiet option", "set -o pipefail\nyes | grep --quiet y || exit 1\n", "quiet-grep-pipe"},
		{"silent option after another flag", "set -o pipefail\nyes | grep -F --silent y || exit 1\n", "quiet-grep-pipe"},
		{"fgrep through env", "set -o pipefail\nyes | env fgrep -q y || exit 1\n", "quiet-grep-pipe"},
		{"long option that is not quiet", "set -o pipefail\nyes | grep --max-count=1 y >/dev/null || exit 1\n", ""},
		{"quiet grep after |&", "set -o pipefail\nyes |& grep -q y || exit 1\n", "quiet-grep-pipe"},
		{"quiet option after an operand", "set -o pipefail\nyes | grep -e y -q || exit 1\n", "quiet-grep-pipe"},
		{"long quiet option after a long operand", "set -o pipefail\nyes | grep --regexp y --quiet || exit 1\n", "quiet-grep-pipe"},
		{"q as the operand of -e", "set -o pipefail\nyes | grep -eq >/dev/null || exit 1\n", ""},
		{"-q of a later command in the list", "set -o pipefail\nyes | grep y >/dev/null && ls -q || exit 1\n", ""},
		{"-q after the end of options", "set -o pipefail\nyes | grep -- -q >/dev/null || exit 1\n", ""},
		{"quiet grep without pipefail", "echo \"$x\" | grep -q y || exit 1\n", ""},
		{"grep that reads all input", "set -o pipefail\necho \"$x\" | grep -F -- y >/dev/null || exit 1\n", ""},
		{"quiet grep after ||", "set -o pipefail\ntest -n \"$x\" || grep -q y f || exit 1\n", ""},
		{"marked quiet grep", "set -o pipefail\n# fail-closed: find stops after one line\nfind . -print -quit | grep -q . || exit 1\n", ""},
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
	// The lint reads every command, reachable or not, so only a row that
	// turns the anchor into text, a comment, a heredoc or a quoted span, or
	// renames its command, clears the hit. A heredoc whose
	// delimiter the check cannot spell fails closed, so it keeps the hit.
	madeText := map[string]bool{
		"full-line comment": true, "inline comment after || true": true, "echo-quoted": true, "single heredoc": true,
		"multi heredoc <<A <<B": true, "arithmetic shift": true, "here-string": true, "line continuation": true,
		"quoted line span": true, "indented heredoc terminator": true, "escaped closer in a quoted span": true,
		"heredoc opened as a quote closes": true, "noclobber redirection": true, "quoted span closing into arguments": true,
		"quoted separator": true, "ANSI-C heredoc delimiter": true,
		"single-quoted line break": true, "escaped apostrophe in an ANSI-C word": true,
		"ANSI-C span across a line break": true, "prefix extension": true, "flattened argv": true,
	}
	for _, evasion := range Evasions {
		t.Run(evasion.Name, func(t *testing.T) {
			t.Parallel()
			want := !madeText[evasion.Name]
			mutated := evasion.Rewrite(artifact, anchor)
			if mutated == artifact {
				t.Fatalf("evasion %q left the artifact unchanged", evasion.Name)
			}
			// A heredoc delimiter the reader cannot resolve fails the scan closed,
			// which reports the script as a hit does.
			findings, err := metadatautil.ShellFailOpenFindings(mutated)
			if got := err != nil || len(findings) > 0; got != want {
				t.Fatalf("findings = %v, error = %v, want a hit %v", findings, err, want)
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
	for file, shell := range map[string]bool{"x\n#!/bin/sh -e\ngit fetch || true": true, "x\n#!/usr/bin/env -S BASH_ENV= ENV= bash\ngit fetch || true": true, "x\n#!/usr/bin/env python3\ngit fetch || true": false, "x\n#![no_main]\ngit fetch || true": false, "u.bash\n# a sourced module\ngit fetch || true": true, "z\n#!/bin/zsh\ngit fetch || true": true, "e\n#!/usr/bin/env -S -u FOO sh\ngit fetch || true": true, "q\n#!/usr/bin/env -S -u FOO 'sh'\ngit fetch || true": true, "b\n#!/bin/busybox sh\ngit fetch || true": true, "p.bash\ngit fetch 2>/dev/null; rc=${PIPESTATUS[0]}; echo done": true, "p.zsh\ngit fetch 2>/dev/null; rc=$pipestatus; echo done": true, "q.sh\ngit fetch 2>/dev/null; rc=$?x; echo done": true, "r.sh\ngit fetch 2>/dev/null; rc=${?}; [ \"$rc\" = 0 ] || exit 1": false, "f\n#!/usr/bin/fish\ngit fetch || true": false} {
		root := fixtureRepo(t, "#!/bin/bash\n", "")
		name, first, _ := strings.Cut(file, "\n")
		if err := os.WriteFile(filepath.Join(root, "scripts", name), []byte(first+"\n"), 0o755); err != nil {
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
