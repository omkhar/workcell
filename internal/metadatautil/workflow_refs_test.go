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
		{"bounded gh calls", runStep("gh api --paginate repos/x/y/pulls"), "", ""},
		{"gh api without bound", runStep(`x="$(gh api repos/x/y/pulls)"`), "", "gh-api-unbounded"},
		{"gh api has no --limit", runStep("gh api repos/x --limit 1"), "", "gh-api-unbounded"},
		{"gh in a comment is not run", runStep("# gh api repos/x\ntrue"), "", ""},
		{"gh in a quoted jq program is not run", runStep(`jq -n '"gh api x"'`), "", ""},
		{"second unbounded call in a baselined step", runStep("gh api a\ngh api b"), "gh-api-unbounded\tw.yml\tj\ts\treason\n", "gh-api-unbounded#2"},
		{"baselined call passes", runStep("gh api a"), "gh-api-unbounded\tw.yml\tj\ts\treason\n", ""},
		{"stale baseline row", runStep("true"), "gh-api-unbounded\tw.yml\tj\ts\treason\n", "stale baseline row"},
		{"baseline row needs a reason", runStep("true"), "gh-api-unbounded\tw.yml\tj\ts\t\n", "want kind, file, job, step, reason"},
		{"gh api pagination disabled", runStep("gh api --paginate=false repos/x"), "", "gh-api-unbounded"},
		{"gh api pagination overridden", runStep("gh api --paginate repos/x --paginate=false"), "", "gh-api-unbounded"},
		{"gh api behind --hostname", runStep("gh --hostname github.com api repos/x"), "", "gh-api-unbounded"},
		{"gh api behind a short global flag", runStep("gh -X GET api repos/x"), "", "gh-api-unbounded"},
		{"called function body is read", runStep("f() {\n  gh api repos/x\n}\ntrap f EXIT"), "", "gh-api-unbounded"},
		{"one-line called function body is read", runStep("f() { gh api repos/x; }\nf"), "", "gh-api-unbounded"},
		{"uncalled one-line function with a command after it", runStep("f() { true; }\ngh api repos/x"), "", "gh-api-unbounded"},
		{"gh api in backticks", runStep("r=`gh api repos/x`"), "", "gh-api-unbounded"},
		{"gh behind wrappers", runStep("command gh api a\ncommand -p gh api b\nenv -u X A=1 nice -n 5 gh api c\nexec gh api d"), "", "gh-api-unbounded#4"},
		{"command -v only names gh", runStep("command -v gh api a"), "", ""},
		{"recursive function is expanded once per call", runStep("f() { gh api x; f; }\nf"), "gh-api-unbounded\tw.yml\tj\ts\treason\n", ""},
		{"gh behind long wrapper options", runStep("env --unset X gh api a\nenv --unset=X gh api b\nnice --adjustment 5 gh api c\nnice --adj 5 gh api d"), "", "gh-api-unbounded#4"},
		{"call before a redefinition runs the first body", runStep("f() { gh api repos/x; }\nf\nf() { :; }"), "", "gh-api-unbounded"},
		{"gh help runs nothing", runStep("gh api --help\ngh pr list -h\ngh api repos/x --help=true"), "", ""},
		{"gh help turned off", runStep("gh api --help=false repos/x"), "", "gh-api-unbounded"},
		{"gh behind redirection prefixes", runStep(">out gh api a\n2>/dev/null gh api b\n< in A=1 gh api c\n{fd}>f 2>&1 gh api d"), "", "gh-api-unbounded#4"},
		{"trap runs the last definition", runStep("trap f EXIT\nf() { :; }\nf() { gh api repos/x; }"), "", "gh-api-unbounded"},
		{"same-line definition then its call", runStep("f() { gh api repos/x; }; f"), "", "gh-api-unbounded"},
		{"gh api with an attached redirection", runStep("gh api>/dev/null repos/x"), "", "gh-api-unbounded"},
		{"gh with an attached redirection", runStep("gh>/dev/null api repos/x"), "", "gh-api-unbounded"},
		{"gh behind redirections after the command word", runStep("gh 2>&1 api a\ngh &>/dev/null api b\ngh > out api c\ngh >&2 api d"), "", "gh-api-unbounded#4"},
		{"quoted > inside a word does not split", runStep(`gh "api>x" repos/x`), "", ""},
		{"gh behind env split strings", runStep("env -S 'gh api a'\nenv --split-string='gh api b'\nenv -i --split-s 'A=1 gh' api c\nenv -S'gh api d'\nenv XS=1 gh api e"), "", "gh-api-unbounded#5"},
		{"gh api pagination as another option's value", runStep("gh api --preview --paginate repos/o/r/issues"), "", "gh-api-unbounded"},
		{"gh api pagination after valued options", runStep("gh api --paginate repos/o/r/issues\ngh api -X GET --paginate repos/o/r/issues\ngh api -H 'A: b' --paginate repos/x"), "", ""},
		{"trap action after --", runStep("trap -- f EXIT\nf() { gh api repos/x; }"), "", "gh-api-unbounded"},
		{"gh api behind timeout", runStep("timeout 30 gh api a"), "", "gh-api-unbounded"},
		{"gh api behind timeout -k", runStep("timeout -k 5 30 gh api a"), "", "gh-api-unbounded"},
		{"gh api behind timeout --signal=", runStep("timeout --signal=TERM 30 gh api a"), "", "gh-api-unbounded"},
		{"gh api behind timeout options", runStep("timeout --preserve-status --foreground -v -s KILL --kill-after 5 30 gh api a\ntimeout -vk5 30 gh api b\ntimeout -- 30 gh api c"), "", "gh-api-unbounded#3"},
		{"replaced trap runs a handler set while a command ran", runStep("f() { gh api x; }\ntrap f EXIT\nmay_fail\ntrap - EXIT"), "", "gh-api-unbounded"},
		{"trap on another signal keeps the handler", runStep("f() { gh api x; }\ntrap f EXIT\ntrap : INT"), "", "gh-api-unbounded"},
		{"gh api behind eval", runStep("eval gh api repos/o/r/issues\neval \"gh api repos/o/r/issues\""), "", "gh-api-unbounded#2"},
		{"eval of an expansion is unresolved", runStep(`eval "$CMD"`), "", "eval-unresolved"},
		{"command word with a substitution is unresolved", runStep("g$(printf h) api repos/o/r/issues"), "", "command-unresolved"},
		{"substitution in a command word still runs", runStep("x$(gh api repos/x) y"), "command-unresolved\tw.yml\tj\ts\treason\n", "gh-api-unbounded"},
		{"substitution in a gh argument is unresolved", runStep(`gh api --paginate "$(cat x)"`), "", "command-unresolved"},
		{"nested definition called in its body", runStep("f() { g() { gh api repos/x; }; g; }; f"), "", "gh-api-unbounded"},
		{"gh api behind time", runStep("time -p gh api repos/o/r/issues\ntime -- gh api repos/o/r/issues\n! time -p -- A=1 gh api repos/o/r/issues"), "", "gh-api-unbounded#3"},
		{"time as an argument is not a prefix", runStep("echo time -p gh api repos/o/r/issues"), "", ""},
		{"gh api behind coproc", runStep("coproc gh api repos/o/r/issues\ncoproc worker { gh api repos/o/r/issues; }"), "", "gh-api-unbounded#2"},
		{"gh api behind builtin eval", runStep("builtin eval gh api repos/o/r/issues"), "", "gh-api-unbounded"},
		{"gh api in a process substitution", runStep("cat <(gh api repos/o/r/issues)\ntee >(gh api repos/o/r/issues)"), "", "gh-api-unbounded#2"},
		{"process substitution of another command", runStep("while read -r f; do echo \"$f\"; done < <(find . -type f)"), "", ""},
		{"process substitution inside a quoted substitution", runStep("x=\"$(cat <(true); gh api repos/o/r/issues)\"\ny=\"$( (true); gh api repos/o/r/issues)\""), "", "gh-api-unbounded#2"},
		{"gh subcommand with a substitution is unresolved", runStep("gh $(printf api) repos/o/r/issues"), "", "command-unresolved"},
		{"gh by path", runStep("/usr/bin/gh api repos/o/r/issues"), "", "gh-api-unbounded"},
		{"alias definition is unresolved", runStep("shopt -s expand_aliases; alias g=gh; g api repos/o/r/issues"), "", "command-unresolved"},
		{"alias as an argument is not a definition", runStep("echo alias g=gh"), "", ""},
		{"gh api in a bash -c body", runStep("bash -c 'gh api repos/o/r/issues'"), "", "gh-api-unbounded"},
		{"gh api in a clustered sh -lc body", runStep(`sh -lc "gh api repos/o/r/issues"`), "", "gh-api-unbounded"},
		{"bash -c body with an expansion is unresolved", runStep(`bash -c "$CMD"`), "", "command-unresolved"},
		{"bash -c body without gh is clean", runStep("bash -c 'echo gh api'"), "", ""},
		{"bounded gh api in a bash -c body", runStep("bash -c 'gh api --paginate repos/o/r/issues'"), "", ""},
		{"gh api in a bash -c -- body", runStep("bash -c -- 'gh api repos/o/r/issues'"), "", "gh-api-unbounded"},
		{"gh api in a bash -cl body", runStep("bash -cl 'gh api repos/o/r/issues'"), "", "gh-api-unbounded"},
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
		{"no-execute bash body is clean", runStep("bash -n -c 'gh api repos/o/r/issues'\nbash -nc 'gh api repos/o/r/issues'"), "", ""},
		{"case pattern is not a command", runStep("case $x in\n  a|*.txt) :;;\n  *)\n    gh api repos/o/r/issues\n    ;;\nesac"), "", "gh-api-unbounded"},
		{"Windows executable names are unresolved", runStep("gh.exe api repos/o/r/issues\n\"C:\\\\tools\\\\GH.EXE\" api repos/o/r/issues\nbash.exe -c 'gh api repos/o/r/issues'"), "", "command-unresolved"},
		{"configured non-bash shell is unresolved", "      - name: s\n        shell: pwsh\n        run: Invoke-Expression 'gh api repos/o/r/issues'\n", "", "command-unresolved"},
		{"Windows runner with no shell is unresolved", "name: w\njobs:\n  j:\n    runs-on: windows-latest\n    steps:\n      - run: Invoke-Expression 'gh api repos/o/r/issues'\n", "", "command-unresolved"},
		{"expression runner with no shell is unresolved", "name: w\njobs:\n  j:\n    runs-on: ${{ matrix.os }}\n    steps:\n      - run: gh api --paginate repos/o/r/issues\n", "", "command-unresolved"},
		{"expression runner with a bash default is read", "name: w\ndefaults:\n  run:\n    shell: bash\njobs:\n  j:\n    runs-on: ${{ matrix.os }}\n    steps:\n      - run: gh api --paginate repos/o/r/issues\n", "", ""},
		{"expansion in a paginated gh api call is unresolved", runStep("gh api --paginate ${{ github.event.issue.title }}\ngh api --paginate \"repos/${REPO}/issues\""), "", "command-unresolved"},
		{"configured bash shell is read", "      - name: s\n        shell: bash --noprofile --norc -euo pipefail {0}\n        run: gh api repos/o/r/issues\n", "", "gh-api-unbounded"},
		{"workflow default shell this lint does not read", "name: w\ndefaults:\n  run:\n    shell: pwsh\njobs:\n  j:\n    runs-on: windows-latest\n    steps:\n      - run: gh api repos/o/r/issues\n", "", "command-unresolved"},
		{"job default shell this lint does not read", "name: w\njobs:\n  j:\n    runs-on: windows-latest\n    defaults:\n      run:\n        shell: cmd\n    steps:\n      - run: gh api repos/o/r/issues\n", "", "command-unresolved"},
		{"sudo edit and query modes run nothing", runStep("sudo -e gh api repos/o/r/issues\nsudo -l gh api repos/o/r/issues\nsudo --list gh api repos/o/r/issues\nsudo -nv gh api repos/o/r/issues"), "", ""},
		{"coproc runs its command", runStep("coproc gh api repos/o/r/issues"), "", "gh-api-unbounded"},
		{"coproc name before a simple command is its first word", runStep("coproc worker gh api repos/o/r/issues"), "", ""},
		{"upper-case names are read on Windows", runStep("GH api repos/o/r/issues\nENV Gh api repos/o/r/issues\nBASH -c 'gh api repos/o/r/issues'"), "", "gh-api-unbounded"},
		{"source of a file is unresolved", runStep("source ./lib.sh\n. ./lib.sh\nsource /dev/stdin <<'EOF'\ngh api repos/o/r/issues\nEOF"), "", "command-unresolved"},
		{"unmodeled wrapper is unresolved", runStep("chrt 10 gh api repos/o/r/issues\ntaskset 1 gh api repos/o/r/issues\nsetsid gh api x\nstdbuf -oL gh api x\nxargs gh api\nflock /tmp/x.lock gh api x\nionice -c 3 gh api x"), "", "command-unresolved"},
		{"Windows child shell is unresolved", runStep("pwsh -Command \"gh api repos/o/r/issues\"\nPowerShell.exe -c 'gh api x'\ncmd /c \"gh api x\""), "", "command-unresolved"},
		{"builtin before command wraps gh", runStep("builtin command gh api repos/o/r/issues"), "", "gh-api-unbounded"},
		{"builtin before eval runs its words", runStep("builtin eval 'gh api repos/o/r/issues'"), "", "gh-api-unbounded"},
		{"find -exec is unresolved", runStep("find . -exec gh api repos/o/r/issues \\;\nfind . -name x -execdir sh -c 'gh api repos/o/r/issues' \\;\nfind . -ok gh api repos/o/r/issues {} +"), "", "command-unresolved"},
		{"find without -exec runs nothing", runStep("find . -name 'gh api' -print"), "", ""},
		{"no-execute switched back on runs the body", runStep("bash -n +n -c 'gh api repos/o/r/issues'"), "", "gh-api-unbounded"},
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

// TestEveryShellCommandEndsOnCommandlessEnvSplit guards the env -S rewrite: a
// split string that names no command must end the wrapper loop.
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
	body := "name: v\njobs:\n  j:\n    runs-on: ubuntu-latest\n    steps:\n" + runStep("gh api repos/x")
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

// TestCheckWorkflowRefsRejectsEvasions runs the shared corpus against a
// baselined gh api hit and a baselined unresolved eval. A row that turns the
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
	t.Run("eval", func(t *testing.T) {
		const anchor = "          eval \"$CMD\""
		if err := validate("eval-unresolved\tw.yml\tj\ts\treason\n")(head + anchor + "\n"); err != nil {
			t.Fatalf("CheckWorkflowRefs() error = %v, want the baselined hit", err)
		}
		RequireRejectsTextEvasions(t, head+anchor+"\n", anchor, "stale baseline row",
			validate("eval-unresolved\tw.yml\tj\ts\treason\n"))
	})
}
