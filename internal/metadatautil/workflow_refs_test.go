// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/omkhar/workcell/internal/metadatautil"
)

const refsActionsTSV = "actions/checkout@" + refsSHA + "\tpath,ref\n"
const refsSHA = "3d3c42e5aac5ba805825da76410c181273ba90b1"

func refsRoot(t *testing.T, step, baseline string) string {
	t.Helper()
	root := t.TempDir()
	document := "name: w\njobs:\n  j:\n    runs-on: ubuntu-latest\n    steps:\n" + step
	if strings.HasPrefix(step, "name:") {
		document = step // a whole workflow, as a default shell needs
	}
	files := map[string]string{
		".github/workflows/w.yml":           document,
		"policy/workflow-refs-baseline.tsv": baseline,
		"tests/fixtures/actions/inputs.tsv": refsActionsTSV,
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
		{"dynamic script name is skipped", runStep(`bash ./scripts/ci/job-${X}.sh`), "", ""},
		{"missing script", runStep("./scripts/absent.sh"), "", "missing-script ./scripts/absent.sh"},
		{"whole word names the script", runStep("./scripts/present.sh+missing"), "", "missing-script ./scripts/present.sh+missing"},
		{"test operand is not a script", runStep("[[ ! -e ./scripts/generated.sh ]]\ntest -f ./scripts/generated.sh"), "", ""},
		{"shell operand is a script", runStep("bash -e ./scripts/absent.sh arg"), "", "missing-script ./scripts/absent.sh"},
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
		{"known input", "      - uses: actions/checkout@" + refsSHA + "\n        with:\n          Path: x\n", "", ""},
		{"unknown input", "      - uses: actions/checkout@" + refsSHA + "\n        with:\n          app-id: x\n", "", "with-unknown-input actions/checkout@" + refsSHA + " app-id"},
		{"uncached action", "      - uses: actions/other@" + refsSHA + "\n", "", "uses-not-cached"},
		{"gh list in a brace group", runStep("{ gh pr list --base main; }"), "", "gh-pr-list-unbounded"},
		{"gh ls alias", runStep("gh pr ls --base main"), "", "gh-pr-list-unbounded"},
		{"gh group and ls aliases", runStep("gh rs ls"), "", "gh-rs-list-unbounded"},
		{"gh api pagination disabled", runStep("gh api --paginate=false repos/x"), "", "gh-api-unbounded"},
		{"gh api pagination overridden", runStep("gh api --paginate repos/x --paginate=false"), "", "gh-api-unbounded"},
		{"gh api behind sudo -k", runStep("sudo -k gh api repos/o/r/issues"), "", "gh-api-unbounded"},
		{"gh api behind sudo --reset-timestamp", runStep("sudo --reset-timestamp gh api repos/o/r/issues"), "", "gh-api-unbounded"},
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
		{"command -v only names gh; gh help runs nothing", runStep("command -v gh api a\ngh api --help\ngh pr list -h\ngh api repos/x --help=true"), "", ""},
		{"duplicate limit behind a value-less flag", runStep("gh discussion list --limit 5 --answered --limit abc"), "", "gh-discussion-list-unbounded"},
		{"every call of a function is expanded", runStep("g() { gh api x; }\nf() { g; g; }\nf\ny=$(g)"), "gh-api-unbounded\tw.yml\tj\ts\treason\ngh-api-unbounded#2\tw.yml\tj\ts\treason\n", "gh-api-unbounded#3"},
		{"recursive function is expanded once per call", runStep("f() { gh api x; f; }\nf"), "gh-api-unbounded\tw.yml\tj\ts\treason\n", ""},
		{"gh behind long wrapper options", runStep("env --unset X gh api a\nenv --unset=X gh api b\nnice --adjustment 5 gh api c\nnice --adj 5 gh api d"), "", "gh-api-unbounded#4"},
		{"function name as data is not a call", runStep("f() { gh api repos/x; }\nprintf '%s\\n' f"), "", ""},
		{"redefined function runs its last body", runStep("f() { gh api repos/x; }\nf() { :; }\nf"), "", ""},
		{"call before a redefinition runs the first body", runStep("f() { gh api repos/x; }\nf\nf() { :; }"), "", "gh-api-unbounded"},
		{"gh help turned off", runStep("gh api --help=false repos/x"), "", "gh-api-unbounded"},
		{"gh behind redirection prefixes", runStep(">out gh api a\n2>/dev/null gh api b\n< in A=1 gh api c\n{fd}>f 2>&1 gh api d"), "", "gh-api-unbounded#4"},
		{"trap runs the last definition", runStep("trap f EXIT\nf() { :; }\nf() { gh api repos/x; }"), "", "gh-api-unbounded"},
		{"same-line definition is not run", runStep("f() { gh api repos/x; }; :\n: && g() { gh api repos/y; }"), "", ""},
		{"same-line definition then its call", runStep("f() { gh api repos/x; }; f"), "", "gh-api-unbounded"},
		{"gh api with an attached redirection", runStep("gh api>/dev/null repos/x"), "", "gh-api-unbounded"},
		{"gh with an attached redirection", runStep("gh>/dev/null api repos/x"), "", "gh-api-unbounded"},
		{"gh behind redirections after the command word", runStep("gh 2>&1 api a\ngh &>/dev/null api b\ngh > out api c\ngh >&2 api d"), "", "gh-api-unbounded#4"},
		{"quoted > inside a word does not split", runStep(`gh "api>x" repos/x`), "", "command-unresolved"},
		{"gh behind env split strings", runStep("env -S 'gh api a'\nenv --split-string='gh api b'\nenv -i --split-s 'A=1 gh' api c\nenv -S'gh api d'\nenv XS=1 gh api e"), "", "gh-api-unbounded#5"},
		{"gh api pagination as another option's value", runStep("gh api --preview --paginate repos/o/r/issues"), "", "gh-api-unbounded"},
		{"gh api pagination after valued options; look-alike startup names are clean", runStep("gh api --paginate repos/o/r/issues\ngh api -X GET --paginate repos/o/r/issues\ngh api -H 'A: b' --paginate repos/x\necho \"$MY_ENV\"\necho \"A=1\" >> \"${GITHUB_ENV}\"\nprintf '%s' '$ENV{X}'\necho \"${kind}=${url}\" >> \"$GITHUB_OUTPUT\""), "", ""},
		{"startup file in the run body or env is unresolved", runStep("export BASH_E\\\nNV=init\nexport BASH_E\"NV\"=init\nexport ${N}=init\necho \"${k}=init\" >> \"$GITHUB_ENV\"\nBASH_ENV=init bash -c true") + "      - name: t\n        env:\n          ENV: init\n        run: sh -c true\n", "", "command-unresolved"},
		{"a gh api pair split by any blank or escape is unresolved", runStep("my-runner $'gh\\tapi repos/o/r/issues'\nmy-runner 'gh\n  api x'"), "", "command-unresolved"},
		{"an inline interpreter program is unresolved", runStep("python3 -c 'import os; os.system(\"g\" + \"h api x\")'\nperl -e 'system q{gh api x}'"), "", "command-unresolved"},
		{"an interpreter given any option or no script is unresolved", runStep("php -r 'system(\"g\" . \"h api x\");'\nnode --eval 'x'\npython3 -c'x'\nruby"), "", "command-unresolved"},
		{"bash - reads its program from stdin without a panic", runStep("bash - <<'EOF'\ngh api repos/o/r/issues\nEOF"), "", "gh-api-unbounded"},
		{"a gh alias or extension adds subcommands the lint cannot read", runStep("gh alias set fetch 'api repos/o/r/issues'\ngh fetch\ngh extension install o/gh-x"), "", "command-unresolved"},
		{"a GITHUB_ENV write whose name is built at run time is unresolved", runStep("p=BASH_; s=E; s+=NV; printf '%s%s=init\\n' \"$p\" \"$s\" >> \"$GITHUB_ENV\"\nf=$GITHUB_ENV; echo \"X=1\" >> \"$f\""), "", "command-unresolved"},
		{"a write to an indirect or computed target is unresolved", runStep("a=BASH_; b=E; b+=NV; target=GITHUB_; target+=E; target+=NV; printf '%s%s=init\\n' \"$a\" \"$b\" >> \"${!target}\""), "", "command-unresolved"},
		{"a nameref is unresolved", runStep("t=GITHUB_; t+=E; t+=NV; declare -n target=$t; printf 'X=1\\n' >> \"$target\""), "", "command-unresolved"},
		{"a commented GITHUB_ENV write and an indirect read are clean; gh's built-in short names are read", runStep("# echo 'X=1' >> \"$GITHUB_ENV\"\nv=A; echo \"${!v}\" > out.txt\ngh at verify x\ngh cs list --limit 5\ngh rs list --limit 5"), "", ""},
		{"a gh subcommand that is not built in may be a config alias", runStep("GH_CONFIG_DIR=cfg gh fetch"), "", "command-unresolved"},
		{"ubuntu 26.04 runners run bash", "name: w\njobs:\n  j:\n    runs-on: ubuntu-26.04-arm\n    steps:\n      - run: gh api --paginate repos/o/r/issues\n", "", ""},
		{"an interpreter running a script file is clean; time as an argument is not a prefix", runStep("python3 tools/x.py\nperl tools/x.pl\necho time -p gh api repos/o/r/issues"), "", ""},
		{"zsh and a gh call in another command's words are unresolved", runStep("ZDOTDIR=z zsh -c true\nnice-wrapper gh api x\necho 'gh api x' | sh"), "", "command-unresolved"},
		{"startup-file shell options are unresolved", runStep("bash --rcfile init -c true\nbash -ic true\nbash -l -c true\nbash --login -c true"), "", "command-unresolved"},
		{"bash reads its stdin script operand", runStep("bash /dev/stdin <<'EOF'\ngh api repos/o/r/issues\nEOF"), "", "gh-api-unbounded"},
		{"literal trap action is read", runStep("trap 'gh api repos/o/r/issues' EXIT\ntrap -- 'rm -f x' INT"), "", "gh-api-unbounded"},
		{"trap action after --", runStep("trap -- f EXIT\nf() { gh api repos/x; }"), "", "gh-api-unbounded"},
		{"gh list with a value-less flag before its limit", runStep("gh discussion list --answered --limit 5\ngh repo list --archived -L 5\ngh release list --exclude-drafts --limit 5"), "", ""},
		{"gh api behind timeout", runStep("timeout 30 gh api a"), "", "gh-api-unbounded"},
		{"gh api behind timeout -k", runStep("timeout -k 5 30 gh api a"), "", "gh-api-unbounded"},
		{"gh api behind timeout --signal=", runStep("timeout --signal=TERM 30 gh api a"), "", "gh-api-unbounded"},
		{"gh api behind timeout options", runStep("timeout --preserve-status --foreground -v -s KILL --kill-after 5 30 gh api a\ntimeout -vk5 30 gh api b\ntimeout -- 30 gh api c"), "", "gh-api-unbounded#3"},
		{"replaced trap runs only its last handler", runStep("f() { gh api x; }\ng() { :; }\ntrap f EXIT\ntrap g EXIT"), "", ""},
		{"removed trap runs nothing", runStep("f() { gh api x; }\ntrap f EXIT\ntrap - EXIT\ntrap f INT\ntrap '' SIGINT\ntrap f 0\ntrap EXIT"), "", ""},
		{"replaced trap runs a handler set while a command ran", runStep("f() { gh api x; }\ntrap f EXIT\nmay_fail\ntrap - EXIT"), "", "gh-api-unbounded"},
		{"trap on another signal keeps the handler", runStep("f() { gh api x; }\ntrap f EXIT\ntrap : INT"), "", "gh-api-unbounded"},
		{"gh api behind eval", runStep("eval gh api repos/o/r/issues\neval \"gh api repos/o/r/issues\""), "", "gh-api-unbounded#2"},
		{"eval of an expansion is unresolved", runStep(`eval "$CMD"`), "", "eval-unresolved"},
		{"command word with a substitution is unresolved", runStep("g$(printf h) api repos/o/r/issues"), "", "command-unresolved"},
		{"substitution in a command word still runs", runStep("x$(./scripts/absent.sh) y"), "command-unresolved\tw.yml\tj\ts\treason\n", "missing-script ./scripts/absent.sh"},
		{"substitution in a gh argument is unresolved", runStep(`gh api --paginate "$(cat x)"`), "", "command-unresolved"},
		{"nested definition in a called body is not run", runStep("f() { g() { gh api repos/x; }; :; }; f\nh() {\n  k() { gh api repos/x; }\n  :\n}\nh"), "", ""},
		{"nested definition called in its body", runStep("f() { g() { gh api repos/x; }; g; }; f"), "", "gh-api-unbounded"},
		{"gh api behind time", runStep("time -p gh api repos/o/r/issues\ntime -- gh api repos/o/r/issues\n! time -p -- A=1 gh api repos/o/r/issues"), "", "gh-api-unbounded#3"},
		{"gh api behind coproc", runStep("coproc gh api repos/o/r/issues\ncoproc worker { gh api repos/o/r/issues; }"), "", "gh-api-unbounded#2"},
		{"gh api behind builtin eval", runStep("builtin eval gh api repos/o/r/issues"), "", "gh-api-unbounded"},
		{"gh api in a process substitution", runStep("cat <(gh api repos/o/r/issues)\ntee >(gh api repos/o/r/issues)"), "", "gh-api-unbounded#2"},
		{"process substitution of another command; alias as an argument is not a definition", runStep("while read -r f; do echo \"$f\"; done < <(find . -type f)\necho alias g=gh"), "", ""},
		{"process substitution inside a quoted substitution", runStep("x=\"$(cat <(true); gh api repos/o/r/issues)\"\ny=\"$( (true); gh api repos/o/r/issues)\""), "", "gh-api-unbounded#2"},
		{"gh subcommand with a substitution is unresolved", runStep("gh $(printf api) repos/o/r/issues"), "", "command-unresolved"},
		{"gh by path", runStep("/usr/bin/gh api repos/o/r/issues"), "", "gh-api-unbounded"},
		{"alias definition is unresolved", runStep("shopt -s expand_aliases; alias g=gh; g api repos/o/r/issues"), "", "command-unresolved"},
		{"gh api in a bash -c body", runStep("bash -c 'gh api repos/o/r/issues'"), "", "gh-api-unbounded"},
		{"gh api in a clustered sh -lc body is unresolved", runStep(`sh -lc "gh api repos/o/r/issues"`), "", "command-unresolved"}, // a login shell runs a profile first
		{"bash -c body with an expansion is unresolved", runStep(`bash -c "$CMD"`), "", "command-unresolved"},
		{"bash -c body without gh is clean; bounded gh api in a bash -c body", runStep("bash -c 'echo gh api'\nbash -c 'gh api --paginate repos/o/r/issues'"), "", ""},
		{"gh api in a bash -c -- body", runStep("bash -c -- 'gh api repos/o/r/issues'"), "", "gh-api-unbounded"},
		{"gh api in a bash -cl body is unresolved", runStep("bash -cl 'gh api repos/o/r/issues'"), "", "command-unresolved"}, // a login shell runs a profile first
		{"gh api in a bash -O extglob -c body", runStep("bash -O extglob -c 'gh api repos/o/r/issues'"), "", "gh-api-unbounded"},
		{"gh api in a bash -o posix -c body", runStep("bash -o posix -c 'gh api repos/o/r/issues'"), "", "gh-api-unbounded"},
		{"bash -o posix -c body without gh is clean", runStep("bash -o posix -c 'echo gh'"), "", ""},
		{"gh api in a quoted-delimiter heredoc fed to bash", runStep("bash <<'EOF'\ngh api repos/o/r/issues\nEOF"), "", "gh-api-unbounded"},
		{"gh api in a heredoc fed to sh", runStep("sh <<-EOF\n\tgh api repos/o/r/issues\nEOF\nbash -s x <<EOF\ngh api repos/o/r/issues\nEOF"), "", "gh-api-unbounded#2"},
		{"gh api in a here-string fed to bash", runStep("bash <<< 'gh api repos/o/r/issues'\nbash <<<'gh api repos/o/r/issues'"), "", "gh-api-unbounded#2"},
		{"heredoc with an expansion fed to bash is unresolved", runStep("bash <<EOF\ngh $SUB repos/o/r/issues\nEOF"), "", "command-unresolved"},
		{"shell reading a pipe is unresolved", runStep("cat script | bash"), "", "command-unresolved"},
		{"bare shell is unresolved", runStep("bash"), "", "command-unresolved"},
		{"quoted-delimiter heredoc with an expansion is read", runStep("bash <<'EOF'\necho \"$HOME\"\nEOF"), "", ""},
		{"shell running a script file is clean", runStep("bash script.sh\nbash -- script.sh\nsh -e script.sh <<<x"), "", ""},
		{"heredoc fed to another command is ignored", runStep("cat <<'EOF'\ngh api repos/o/r/issues\nEOF\njq -n <<EOF\ngh api $X\nEOF\ncat <<<'gh api x'"), "", ""},
		{"multi-line single-quoted bash -c body", runStep("bash -c '# note\n  gh api repos/o/r/issues'"), "", "gh-api-unbounded"},
		{"multi-line double-quoted bash -c body", runStep("bash -c \"# note\n  gh api repos/o/r/issues\""), "", "gh-api-unbounded"},
		{"multi-line here-string fed to bash", runStep("bash <<<'# note\n  gh api repos/o/r/issues'"), "", "gh-api-unbounded"},
		{"ANSI-C bash -c body with an escape is unresolved", runStep(`bash -c $'gh api repos/x\n# tail'`), "", "command-unresolved"},
		{"ANSI-C here-string with an escape is unresolved", runStep(`bash <<< $'# note\ngh api repos/x'`), "", "command-unresolved"},
		{"comment-only multi-line bash -c body is clean", runStep("bash -c '# one\n  # two'\nbash -c $'echo plain'"), "", ""},
		{"uncalled function in a bash -c body runs nothing", runStep("bash -c 'f() { gh api repos/x; }; :'"), "", ""},
		{"called function in a bash -c body is a hit", runStep("bash -c 'f() { gh api repos/x; }; f'"), "", "gh-api-unbounded"},
		{"env by path wraps gh", runStep("/usr/bin/env gh api repos/o/r/issues"), "", "gh-api-unbounded"},
		{"brace-expanded command word is unresolved", runStep("{gh,} api repos/o/r/issues"), "", "command-unresolved"},
		{"brace-expanded gh subcommand is unresolved", runStep("gh {api,} repos/o/r/issues"), "", "command-unresolved"},
		{"sudo wraps gh", runStep("sudo -n gh api repos/o/r/issues"), "", "gh-api-unbounded"},
		{"sudo with a lone dash wraps gh", runStep("sudo - gh api repos/o/r/issues"), "", "gh-api-unbounded"},
		{"sudo with a user wraps gh", runStep("sudo -u runner -- gh api repos/o/r/issues"), "", "gh-api-unbounded"},
		{"sudo shell with no command is unresolved", runStep("sudo -s"), "", "command-unresolved"},
		{"sudo help and edit run no gh", runStep("sudo -h\nsudo -e file"), "", ""},
		{"pathname-pattern gh subcommand is unresolved", runStep("gh a?i repos/o/r/issues"), "", "command-unresolved"},
		{"pathname-pattern command word is unresolved", runStep("g[h] api repos/o/r/issues"), "", "command-unresolved"},
		{"test command ahead of gh is a hit", runStep("[ -f x ] && gh api repos/o/r/issues"), "", "gh-api-unbounded"},
		{"setsid wraps gh", runStep("setsid -w gh api repos/o/r/issues"), "", "gh-api-unbounded"},
		{"no-execute bash body is clean", runStep("bash -n -c 'gh api repos/o/r/issues'\nbash -nc 'gh api repos/o/r/issues'"), "", ""},
		{"case pattern is not a command", runStep("case $x in\n  a|*.txt) :;;\n  *)\n    gh api repos/o/r/issues\n    ;;\nesac"), "", "gh-api-unbounded"},
		{"Windows executable name is gh", runStep("gh.exe api repos/o/r/issues"), "", "gh-api-unbounded"},
		{"Windows path to gh is gh", runStep("\"C:\\\\tools\\\\GH.EXE\" api repos/o/r/issues"), "", "gh-api-unbounded"},
		{"Windows bash runs its body", runStep("bash.exe -c 'gh api repos/o/r/issues'"), "", "gh-api-unbounded"},
		{"configured non-bash shell is unresolved", "      - name: s\n        shell: pwsh\n        run: Invoke-Expression 'gh api repos/o/r/issues'\n", "", "command-unresolved"},
		{"Windows runner with no shell is unresolved", "name: w\njobs:\n  j:\n    runs-on: windows-latest\n    steps:\n      - run: Invoke-Expression 'gh api repos/o/r/issues'\n", "", "command-unresolved"},
		{"custom runner label with no shell is unresolved", "name: w\njobs:\n  j:\n    runs-on: ubuntu-99.99\n    steps:\n      - run: Invoke-Expression 'gh api repos/o/r/issues'\n", "", "command-unresolved"},
		{"one-label hosted runner array runs bash", "name: w\njobs:\n  j:\n    runs-on: [ubuntu-latest]\n    steps:\n      - run: gh api --paginate repos/o/r/issues\n", "", ""},
		{"two-label hosted runner array is unresolved", "name: w\njobs:\n  j:\n    runs-on: [ubuntu-latest, ubuntu-24.04]\n    steps:\n      - run: gh api --paginate repos/o/r/issues\n", "", "command-unresolved"},
		{"one unknown runner label array is unresolved", "name: w\njobs:\n  j:\n    runs-on: [unknown-label]\n    steps:\n      - run: gh api --paginate repos/o/r/issues\n", "", "command-unresolved"},
		{"self-hosted runner with no shell is unresolved", "name: w\njobs:\n  j:\n    runs-on: [self-hosted, x64]\n    steps:\n      - run: Invoke-Expression 'gh api repos/o/r/issues'\n", "", "command-unresolved"},
		{"expression runner with no shell is unresolved", "name: w\njobs:\n  j:\n    runs-on: ${{ matrix.os }}\n    steps:\n      - run: gh api --paginate repos/o/r/issues\n", "", "command-unresolved"},
		{"expression runner with a bash default is read", "name: w\ndefaults:\n  run:\n    shell: bash\njobs:\n  j:\n    runs-on: ${{ matrix.os }}\n    steps:\n      - run: gh api --paginate repos/o/r/issues\n", "", ""},
		{"brace or pathname expansion in a paginated gh api call is unresolved", runStep("gh api --paginate repos/o/r/issues {--paginate=false,}\ngh api --paginate repos/o/r/issues --pag*"), "", "command-unresolved"},
		{"expansion in a paginated gh api call is unresolved", runStep("gh api --paginate ${{ github.event.issue.title }}\ngh api --paginate \"repos/${REPO}/issues\""), "", "command-unresolved"},
		{"a custom shell template with a program is unresolved", "      - name: s\n        shell: bash -c 'gh api repos/o/r/issues' {0}\n        run: \"true\"\n", "", "command-unresolved"},
		{"a custom shell template with a startup file is unresolved", "      - name: s\n        shell: bash --rcfile x {0}\n        run: \"true\"\n", "", "command-unresolved"},
		{"configured bash shell is read", "      - name: s\n        shell: bash --noprofile --norc -euo pipefail {0}\n        run: gh api repos/o/r/issues\n", "", "gh-api-unbounded"},
		{"workflow default shell this lint does not read", "name: w\ndefaults:\n  run:\n    shell: pwsh\njobs:\n  j:\n    runs-on: windows-latest\n    steps:\n      - run: gh api repos/o/r/issues\n", "", "command-unresolved"},
		{"job default shell this lint does not read", "name: w\njobs:\n  j:\n    runs-on: windows-latest\n    defaults:\n      run:\n        shell: cmd\n    steps:\n      - run: gh api repos/o/r/issues\n", "", "command-unresolved"},
		{"sudo edit and query modes run nothing", runStep("sudo -e gh api repos/o/r/issues\nsudo -l gh api repos/o/r/issues\nsudo --list gh api repos/o/r/issues\nsudo -nv gh api repos/o/r/issues"), "", ""},
		{"coproc runs its command", runStep("coproc gh api repos/o/r/issues"), "", "gh-api-unbounded"},
		{"coproc name before a simple command is its first word, which may run gh", runStep("coproc worker gh api repos/o/r/issues"), "", "command-unresolved"},
		{"stdbuf wraps gh", runStep("stdbuf -oL gh api repos/o/r/issues\nstdbuf -o L -e0 gh api repos/o/r/issues"), "", "gh-api-unbounded"},
		{"upper-case names are read on Windows", runStep("GH api repos/o/r/issues\nENV Gh api repos/o/r/issues\nBASH -c 'gh api repos/o/r/issues'"), "", "gh-api-unbounded"},
		{"xargs wraps gh", runStep("printf 'repos/o/r/issues\\n' | xargs gh api\nxargs -n 1 -I {} --max-procs=2 gh api {} < list"), "", "gh-api-unbounded"},
		{"flock wraps gh", runStep("flock /tmp/x.lock gh api repos/o/r/issues\nflock -n -w 5 /tmp/x.lock gh api repos/o/r/issues"), "", "gh-api-unbounded"},
		{"flock -c runs a shell program", runStep("flock --wait 5 /tmp/x.lock -c 'gh api repos/o/r/issues'"), "", "gh-api-unbounded"},
		{"flock on a descriptor runs nothing", runStep("flock 9"), "", ""},
		{"ionice wraps gh", runStep("ionice -c 3 gh api repos/o/r/issues\nionice --class 2 -n 7 gh api repos/o/r/issues"), "", "gh-api-unbounded"},
		{"source of stdin is unresolved", runStep("source /dev/stdin <<'EOF'\ngh api repos/o/r/issues\nEOF"), "", "command-unresolved"},
		{"a function defined in sourced stdin is unresolved", runStep("source /dev/stdin <<'EOF'\nf() { gh api repos/o/r/issues; }\nEOF\nf"), "", "command-unresolved"},
		{"dot of stdin is unresolved", runStep(". /dev/stdin <<'EOF'\ntrue\nEOF"), "", "command-unresolved"},
		{"source of a file is unresolved", runStep("source ./lib.sh\n. ./lib.sh"), "", "command-unresolved"},
		{"unmodeled wrapper is unresolved", runStep("chrt 10 gh api repos/o/r/issues\nsetarch x86_64 gh api x\ntaskset 1 gh api repos/o/r/issues"), "", "command-unresolved"},
		{"Windows child shell is unresolved", runStep("pwsh -Command \"gh api repos/o/r/issues\"\nPowerShell.exe -c 'gh api x'\ncmd /c \"gh api x\""), "", "command-unresolved"},
		{"builtin before a program runs nothing", runStep("builtin gh api repos/o/r/issues"), "", ""},
		{"builtin before command wraps gh", runStep("builtin command gh api repos/o/r/issues"), "", "gh-api-unbounded"},
		{"builtin before eval runs its words", runStep("builtin eval 'gh api repos/o/r/issues'"), "", "gh-api-unbounded"},
		{"find -exec runs gh", runStep("find . -exec gh api repos/o/r/issues \\;\nfind . -name x -execdir sh -c 'gh api repos/o/r/issues' \\;\nfind . -ok gh api repos/o/r/issues {} +"), "", "gh-api-unbounded"},
		{"find without -exec runs nothing", runStep("find . -name 'gh api' -print"), "", ""},
		{"no-execute switched back on runs the body", runStep("bash -n +n -c 'gh api repos/o/r/issues'"), "", "gh-api-unbounded"},
		{"case arm after a definition in an earlier arm", runStep("case $x in\n  a)\n    f() { :; }\n    ;;\n  *)\n    gh api repos/o/r/issues\n    ;;\nesac"), "", "gh-api-unbounded"},
		{"subshell after a case pattern is read", runStep("( case $x in a) :;; esac; gh api repos/o/r/issues )"), "", "gh-api-unbounded"},
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

// TestEveryShellCommandEndsOnCommandlessEnvSplit guards the env -S rewrite: a split string that names no command must end the wrapper loop.
func TestEveryShellCommandEndsOnCommandlessEnvSplit(t *testing.T) {
	for _, script := range []string{"env -S 'env'", "env -S ''", "env -S 'env -S env'"} {
		done := make(chan [][]string, 1)
		go func() { done <- metadatautil.EveryShellCommand(script) }()
		select {
		case got := <-done:
			if slices.ContainsFunc(got, func(words []string) bool { return words[0] == "gh" }) {
				t.Fatalf("EveryShellCommand(%q) = %q, want no gh command", script, got)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("EveryShellCommand(%q) did not return within 2s", script)
		}
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

func TestWorkflowInlineJQProgramsSkipOptionValues(t *testing.T) {
	programs, err := metadatautil.WorkflowInlineJQPrograms(refsRoot(t, runStep("jq --indent 2 --bogus -r '.a'"), ""))
	if err != nil || len(programs) != 1 || programs[0].Program != ".a" || !slices.Equal(programs[0].Flags, []string{"--indent", "2", "--bogus", "-r"}) {
		t.Fatalf("WorkflowInlineJQPrograms() = %+v, %v; want program .a with every option passed to jq", programs, err)
	}
}

func TestWorkflowInlineJQProgramsReadAttachedGHFilters(t *testing.T) {
	programs, err := metadatautil.WorkflowInlineJQPrograms(refsRoot(t, runStep("gh pr list --json n -q'.a'\ngh api x --jq=.b\ngh api y -q=.c"), ""))
	if err != nil || len(programs) != 3 || programs[0].Program != ".a" || programs[1].Program != ".b" || programs[2].Program != ".c" {
		t.Fatalf("WorkflowInlineJQPrograms() = %+v, %v; want programs .a, .b and .c", programs, err)
	}
}

func TestWorkflowInlineJQProgramsReadPastArgumentModes(t *testing.T) {
	programs, err := metadatautil.WorkflowInlineJQPrograms(refsRoot(t, runStep("jq --args '.a' x\njq -n --jsonargs '.b' 1\njq -n -- '-1' x"), ""))
	if err != nil || len(programs) != 3 || programs[0].Program != ".a" || programs[1].Program != ".b" || programs[2].Program != "-1" ||
		!slices.Equal(programs[1].Flags, []string{"-n", "--jsonargs"}) || !slices.Equal(programs[2].Flags, []string{"-n", "--"}) {
		t.Fatalf("WorkflowInlineJQPrograms() = %+v, %v; want programs .a, .b and -1 after the argument modes and --", programs, err)
	}
}

func TestWorkflowInlineJQProgramsSkipOtherFlagValues(t *testing.T) {
	programs, err := metadatautil.WorkflowInlineJQPrograms(refsRoot(t, runStep("gh pr list --search --jq --base main --limit 1\ngh api --paginate repos/x --jq '.a'"), ""))
	if err != nil || len(programs) != 1 || programs[0].Program != ".a" {
		t.Fatalf("WorkflowInlineJQPrograms() = %+v, %v; want only program .a", programs, err)
	}
}

func TestCheckWorkflowRefsPassesOnRepository(t *testing.T) {
	if err := metadatautil.CheckWorkflowRefs("../.."); err != nil {
		t.Fatal(err)
	}
}

// TestCheckWorkflowRefsRejectsEvasions runs the shared corpus against a
// baselined gh api hit and a baselined missing script. A row that turns the
// command into text must leave the baseline row stale, and a gated row must
// keep the hit.
func TestCheckWorkflowRefsRejectsEvasions(t *testing.T) {
	validate := func(baseline string) func(string) error {
		return func(workflow string) error {
			root := refsRoot(t, "", baseline)
			if err := os.WriteFile(filepath.Join(root, ".github/workflows/w.yml"), []byte(workflow), 0o644); err != nil {
				t.Fatal(err)
			}
			return metadatautil.CheckWorkflowRefs(root)
		}
	}
	const head = "name: w\njobs:\n  j:\n    runs-on: ubuntu-latest\n    steps:\n      - name: s\n        run: |\n          true\n"
	t.Run("gh api", func(t *testing.T) {
		const anchor = "          gh api repos/o/r/issues"
		if err := validate("gh-api-unbounded\tw.yml\tj\ts\treason\n")(head + anchor + "\n"); err != nil {
			t.Fatalf("CheckWorkflowRefs() error = %v, want the baselined hit", err)
		}
		RequireRejectsTextEvasions(t, head+anchor+"\n", anchor, "stale baseline row",
			validate("gh-api-unbounded\tw.yml\tj\ts\treason\n"))
	})
	t.Run("script", func(t *testing.T) {
		const anchor = "          ./scripts/absent.sh"
		if err := validate("missing-script ./scripts/absent.sh\tw.yml\tj\ts\treason\n")(head + anchor + "\n"); err != nil {
			t.Fatalf("CheckWorkflowRefs() error = %v, want the baselined hit", err)
		}
		RequireRejectsTextEvasions(t, head+anchor+"\n", anchor, "stale baseline row",
			validate("missing-script ./scripts/absent.sh\tw.yml\tj\ts\treason\n"))
	})
}
