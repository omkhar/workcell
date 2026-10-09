// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil_test

import (
	"strings"
	"testing"
)

// Evasion rewrites one artifact so that the anchored command it must contain is
// no longer run, while text that names the command stays in the file. Every row
// is a bypass a reviewer found against a validator that read the artifact as
// text. Rewrite takes the whole artifact and the anchored command's exact text,
// so a row can move the command as well as disguise it.
type Evasion struct {
	Name    string
	Rewrite func(artifact, anchor string) string
}

// anyRejection names the rows that disable or move more than the anchor: the
// rest of the script, the whole step, or a key the closed workflow decoder
// rejects. The validator may then fail at an earlier requirement, so any error
// counts, except a YAML syntax error, which would mean the row broke the file.
var anyRejection = map[string]bool{
	"unrelated placement":                    true,
	"eval-assembled definition":              true,
	"wrapped eval definition":                true,
	"assigned eval definition":               true,
	"sourced definition":                     true,
	"parameter-spliced source":               true,
	"substituted source":                     true,
	"ansi-c quoted source":                   true,
	"brace-expanded source":                  true,
	"eval in an if condition":                true,
	"source in a while condition":            true,
	"time-wrapped eval":                      true,
	"time-wrapped source":                    true,
	"sourcing helper called":                 true,
	"sourcing forwarder called":              true,
	"expanded source in a helper":            true,
	"expanded source forwarded":              true,
	"named array source in a helper":         true,
	"source behind two helpers":              true,
	"forwarder that rewrites its parameters": true,
	"source behind a later-defined helper":   true,
	"second positional parameter forwarder":  true,
	"forwarder called twice":                 true,
	"recursion that changes its words":       true,
	"literal word ahead of forwarded words":  true,
	"alias in a called helper":               true,
	"wrapped alias in a called helper":       true,
	"wrapped hash in a called helper":        true,
	"assigned alias in a called helper":      true,
	"assigned hash in a called helper":       true,
	"alias in an if condition":               true,
	"hash in a while condition":              true,
	"debug trap before the anchor":           true,
	"debug trap in a called helper":          true,
	"step condition if: false":               true,
	"step condition && false":                true,
}

// Evasions is the shared negative corpus. A validator that reads file content to
// decide that a command runs must reject every row. It lives in the test package
// to keep testing out of the shipped tool's dependency graph; move it to a
// non-test file when a validator outside this package needs it.
var Evasions = []Evasion{
	{"full-line comment", replaceAnchor(func(a string) string {
		return commentOut(a)
	})},
	{"inline comment after || true", replaceAnchor(func(a string) string {
		return indentOf(a) + "true || true # " + flatten(a)
	})},
	{"echo-quoted", replaceAnchor(func(a string) string {
		return indentOf(a) + `echo "` + strings.ReplaceAll(flatten(a), `"`, "") + `"`
	})},
	{"single heredoc", replaceAnchor(func(a string) string {
		return hide(a, indentOf(a)+": <<'PLAN' >/dev/null", indentOf(a)+"PLAN")
	})},
	{"multi heredoc <<A <<B", replaceAnchor(func(a string) string {
		i := indentOf(a)
		return hide(a, i+": <<'A' <<'B' >/dev/null\n"+i+"A", i+"B")
	})},
	{"arithmetic shift", replaceAnchor(func(a string) string {
		i := indentOf(a)
		return hide(a, i+": $((1 << 2))\n"+i+": <<'PLAN' >/dev/null", i+"PLAN")
	})},
	{"here-string", replaceAnchor(func(a string) string {
		return indentOf(a) + ": <<<'" + flatten(a) + "'"
	})},
	{"line continuation", replaceAnchor(func(a string) string {
		i := indentOf(a)
		return hide(a, i+": <<'PLAN' \\\n"+i+"  >/dev/null", i+"PLAN")
	})},
	{"quoted line span", replaceAnchor(func(a string) string {
		i := indentOf(a)
		return hide(a, i+`: "`, i+`"`)
	})},
	{"indented heredoc terminator", replaceAnchor(func(a string) string {
		i := indentOf(a)
		return hide(a, i+": <<'PLAN'\n"+i+"  PLAN", i+"PLAN")
	})},
	{"uncalled function definition", replaceAnchor(func(a string) string {
		i := indentOf(a)
		return hide(a, i+"never_called() {", i+"}")
	})},
	{"unreachable branch", replaceAnchor(func(a string) string {
		i := indentOf(a)
		return hide(a, i+"if false; then", i+"fi")
	})},
	{"escaped closer in a quoted span", replaceAnchor(func(a string) string {
		i := indentOf(a)
		return hide(a, i+`: "`+"\n"+i+`\"`, i+`"`)
	})},
	{"conditional right-hand side", replaceAnchor(func(a string) string {
		return prefixCommands(a, "false && ")
	})},
	{"conditional across a line break", replaceAnchor(func(a string) string {
		return prefixCommands(a, "false &&\n"+indentOf(a))
	})},
	{"exit before the command", replaceAnchor(func(a string) string {
		return indentOf(a) + "exit 0\n" + a
	})},
	{"definition brace on the next line", replaceAnchor(func(a string) string {
		i := indentOf(a)
		return hide(a, i+"never_called ()\n"+i+"{", i+"}")
	})},
	{"heredoc opened as a quote closes", replaceAnchor(func(a string) string {
		i := indentOf(a)
		return hide(a, i+`: "`+"\n"+i+"x\n"+i+`" <<PLAN`, i+"PLAN")
	})},
	{"exec before the command", replaceAnchor(func(a string) string {
		return indentOf(a) + "exec true\n" + a
	})},
	{"noclobber redirection", replaceAnchor(func(a string) string {
		return indentOf(a) + ": >| " + flatten(a)
	})},
	{"quoted span closing into arguments", replaceAnchor(func(a string) string {
		i := indentOf(a)
		return i + `: "` + "\n" + i + "x\n" + i + `" ` + flatten(a)
	})},
	{"quoted separator", replaceAnchor(func(a string) string {
		return prefixCommands(a, `: ";" `)
	})},
	{"quoted compound-command closer", replaceAnchor(func(a string) string {
		i := indentOf(a)
		return hide(a, i+"if false; then\n"+i+`"fi"`, i+"fi")
	})},
	{"quoted brace in a definition", replaceAnchor(func(a string) string {
		i := indentOf(a)
		return hide(a, i+"never_called() {\n"+i+`"}"`, i+"}")
	})},
	{"ANSI-C heredoc delimiter", replaceAnchor(func(a string) string {
		i := indentOf(a)
		return hide(a, i+": <<$'PLAN'\n"+i+"$PLAN", i+"PLAN")
	})},
	{"ANSI-C escape in a heredoc delimiter", replaceAnchor(func(a string) string {
		i := indentOf(a)
		return hide(a, i+`: <<$'\x50LAN'`+"\n"+i+`\x50LAN`, i+"PLAN")
	})},
	{"single-quoted line break", replaceAnchor(splitCommandWords)},
	{"locale-translated heredoc delimiter", replaceAnchor(func(a string) string {
		i := indentOf(a)
		return hide(a, i+`: <<$"PLAN"`+"\n"+i+"PLAN", i+"PLAN")
	})},
	{"escaped apostrophe in an ANSI-C word", replaceAnchor(func(a string) string {
		return prefixCommands(a, `: $'x\'; `)
	})},
	{"ANSI-C span across a line break", replaceAnchor(func(a string) string {
		i := indentOf(a)
		return i + `: $'x` + "\n" + prefixCommands(a, `\'; `) + "\n" + i + `'`
	})},
	{"negated guarded group", replaceAnchor(func(a string) string {
		i := indentOf(a)
		return hide(a, i+"false && ! {", i+"}")
	})},
	{"conditional command group", replaceAnchor(func(a string) string {
		i := indentOf(a)
		return hide(a, i+"false && {", i+"}")
	})},
	{"argument brace in a guarded group", replaceAnchor(func(a string) string {
		i := indentOf(a)
		return hide(a, i+"false && {\n"+i+"echo }", i+"}")
	})},
	{"argument brace in a definition body", replaceAnchor(func(a string) string {
		i := indentOf(a)
		return hide(a, i+"never_called() {\n"+i+"echo }", i+"}")
	})},
	{"conditional subshell group", replaceAnchor(func(a string) string {
		i := indentOf(a)
		return hide(a, i+"false && (", i+")")
	})},
	{"negated guarded subshell", replaceAnchor(func(a string) string {
		i := indentOf(a)
		return hide(a, i+"false && ! (", i+")")
	})},
	{"array assignment in a guarded subshell", replaceAnchor(func(a string) string {
		i := indentOf(a)
		return hide(a, i+"false && (\n"+i+"decoy=(\n"+i+"  one\n"+i+")", i+")")
	})},
	{"attached subshell opener", replaceAnchor(func(a string) string {
		i := indentOf(a)
		return hide(a, i+"false && (echo hidden", i+")")
	})},
	{"attached subshell opener closed on its own line", replaceAnchor(func(a string) string {
		i := indentOf(a)
		return hide(a, i+"false && (echo hidden)\n"+i+"false && (echo hidden", i+")")
	})},
	{"substitution inside a subshell opener", replaceAnchor(func(a string) string {
		i := indentOf(a)
		return hide(a, i+"false && (echo $(date)", i+")")
	})},
	{"quoted fragment in a subshell opener", replaceAnchor(func(a string) string {
		i := indentOf(a)
		return hide(a, i+`false && (echo")"`, i+")")
	})},
	{"parameter expansion in a subshell opener", replaceAnchor(func(a string) string {
		i := indentOf(a)
		return hide(a, i+"false && (echo ${x%)}", i+")")
	})},
	{"subshell behind a reserved prefix", replaceAnchor(func(a string) string {
		i := indentOf(a)
		return hide(a, i+"false && time (", i+")")
	})},
	{"subshell behind a named coproc", replaceAnchor(func(a string) string {
		i := indentOf(a)
		return hide(a, i+"false && coproc DECOY (", i+")")
	})},
	{"paired closers in a subshell opener", replaceAnchor(func(a string) string {
		i := indentOf(a)
		return hide(a, i+"false && (echo ${x%))}", i+")")
	})},
	{"prefix extension", replaceAnchor(extendFirstOption)},
	{"duplicate function definition, last wins", replaceAnchor(func(a string) string {
		i := indentOf(a)
		return hide(a, i+"run_anchor() {", i+"}") + "\n" + i + "run_anchor() { :; }\n" + i + "run_anchor"
	})},
	{"function keyword and a tab", replaceAnchor(func(a string) string {
		i := indentOf(a)
		return hide(a, i+"function\tnever_called {", i+"}")
	})},
	{"variable-spliced command word", replaceAnchor(spliceCommandWords)},
	{"eval-assembled definition", replaceAnchor(func(a string) string {
		name := strings.Fields(a)[0]
		return indentOf(a) + "eval '" + name[:1] + "''" + name[1:] + "() { :; }'\n" + a
	})},
	{"wrapped eval definition", replaceAnchor(func(a string) string {
		return indentOf(a) + "command eval '" + strings.Fields(a)[0] + "() { :; }'\n" + a
	})},
	{"assigned eval definition", replaceAnchor(func(a string) string {
		return indentOf(a) + "X=1 command eval '" + strings.Fields(a)[0] + "() { :; }'\n" + a
	})},
	{"sourced definition", replaceAnchor(func(a string) string {
		i := indentOf(a)
		return i + "echo '" + strings.Fields(a)[0] + "() { :; }' > shadow.sh\n" + i + ". ./shadow.sh\n" + a
	})},
	{"parameter-spliced source", sourcedBy("s${x-}ource ./shadow.sh")},
	{"substituted source", sourcedBy("$(printf source) ./shadow.sh")},
	{"ansi-c quoted source", sourcedBy("$'\\x73ource' ./shadow.sh")},
	{"brace-expanded source", sourcedBy("{source,./shadow.sh}")},
	{"eval in an if condition", replaceAnchor(func(a string) string {
		return indentOf(a) + "if eval '" + strings.Fields(a)[0] + "(){ :; }'; then :; fi\n" + a
	})},
	{"source in a while condition", sourcedBy("while source ./shadow.sh; do :; done")},
	{"time-wrapped eval", sourcedBy("time eval 'f(){ :; }'")},
	{"time-wrapped source", sourcedBy("time -p source ./shadow.sh")},
	{"sourcing helper called", replaceAnchor(func(a string) string {
		i := indentOf(a)
		return i + "echo '" + strings.Fields(a)[0] + "() { :; }' > shadow.sh\n" + i + "shadow() {\n" + i + "  source ./shadow.sh\n" + i + "}\n" + i + "shadow\n" + a
	})},
	{"sourcing forwarder called", replaceAnchor(func(a string) string {
		i := indentOf(a)
		return i + "echo '" + strings.Fields(a)[0] + "() { :; }' > shadow.sh\n" + i + "run() {\n" + i + "  \"$@\"\n" + i + "}\n" + i + "run source ./shadow.sh\n" + a
	})},
	{"expanded source in a helper", replaceAnchor(func(a string) string {
		i := indentOf(a)
		return i + "echo '" + strings.Fields(a)[0] + "() { :; }' > shadow.sh\n" + i + "shadow() {\n" + i + "  s${x-}ource ./shadow.sh\n" + i + "}\n" + i + "shadow\n" + a
	})},
	{"expanded source forwarded", replaceAnchor(func(a string) string {
		i := indentOf(a)
		return i + "echo '" + strings.Fields(a)[0] + "() { :; }' > shadow.sh\n" + i + "run() {\n" + i + "  \"$@\"\n" + i + "}\n" + i + "run s${x-}ource ./shadow.sh\n" + a
	})},
	{"named array source in a helper", replaceAnchor(func(a string) string {
		i := indentOf(a)
		return i + "echo '" + strings.Fields(a)[0] + "() { :; }' > shadow.sh\n" + i + "cmd=(source ./shadow.sh)\n" + i + "run() {\n" + i + "  \"${cmd[@]}\"\n" + i + "}\n" + i + "run\n" + a
	})},
	{"source behind two helpers", replaceAnchor(func(a string) string {
		i := indentOf(a)
		return i + "echo '" + strings.Fields(a)[0] + "() { :; }' > shadow.sh\n" + i + "inner() {\n" + i + "  source \"$1\"\n" + i + "}\n" + i + "outer() {\n" + i + "  inner \"$1\"\n" + i + "}\n" + i + "outer ./shadow.sh\n" + a
	})},
	{"forwarder that rewrites its parameters", replaceAnchor(func(a string) string {
		i := indentOf(a)
		return i + "echo '" + strings.Fields(a)[0] + "() { :; }' > shadow.sh\n" + i + "run() {\n" + i + "  set -- source ./shadow.sh\n" + i + "  \"$@\"\n" + i + "}\n" + i + "run\n" + a
	})},
	{"source behind a later-defined helper", replaceAnchor(func(a string) string {
		i := indentOf(a)
		return i + "echo '" + strings.Fields(a)[0] + "() { :; }' > shadow.sh\n" + i + "outer() {\n" + i + "  inner \"$@\"\n" + i + "}\n" + i + "inner() {\n" + i + "  source \"$1\"\n" + i + "}\n" + i + "outer ./shadow.sh\n" + a
	})},
	{"second positional parameter forwarder", replaceAnchor(func(a string) string {
		i := indentOf(a)
		return i + "echo '" + strings.Fields(a)[0] + "() { :; }' > shadow.sh\n" + i + "run() {\n" + i + "  \"$2\" \"$1\"\n" + i + "}\n" + i + "run ./shadow.sh source\n" + a
	})},
	{"forwarder called twice", replaceAnchor(func(a string) string {
		i := indentOf(a)
		return i + "echo '" + strings.Fields(a)[0] + "() { :; }' > shadow.sh\n" + i + "outer() {\n" + i + "  inner :\n" + i + "  inner source ./shadow.sh\n" + i + "}\n" + i + "inner() {\n" + i + "  \"$@\"\n" + i + "}\n" + i + "outer\n" + a
	})},
	{"recursion that changes its words", replaceAnchor(func(a string) string {
		i := indentOf(a)
		return i + "echo '" + strings.Fields(a)[0] + "() { :; }' > shadow.sh\n" + i + "outer() {\n" + i + "  inner \"$@\"\n" + i + "}\n" + i + "inner() {\n" + i + "  if [ \"$1\" = go ]; then\n" + i + "    outer source ./shadow.sh\n" + i + "  fi\n" + i + "  \"$@\"\n" + i + "}\n" + i + "outer go\n" + a
	})},
	{"literal word ahead of forwarded words", replaceAnchor(func(a string) string {
		i := indentOf(a)
		return i + "echo '" + strings.Fields(a)[0] + "() { :; }' > shadow.sh\n" + i + "outer() {\n" + i + "  inner source \"$@\"\n" + i + "}\n" + i + "inner() {\n" + i + "  \"$@\"\n" + i + "}\n" + i + "outer ./shadow.sh\n" + a
	})},
	{"alias in a called helper", replaceAnchor(func(a string) string {
		i := indentOf(a)
		return i + "shadow() {\n" + i + "  shopt -s expand_aliases\n" + i + "  alias " + strings.Fields(a)[0] + "=':'\n" + i + "}\n" + i + "shadow\n" + a
	})},
	{"wrapped alias in a called helper", replaceAnchor(func(a string) string {
		i := indentOf(a)
		return i + "shadow() {\n" + i + "  shopt -s expand_aliases\n" + i + "  command alias " + strings.Fields(a)[0] + "=':'\n" + i + "}\n" + i + "shadow\n" + a
	})},
	{"wrapped hash in a called helper", replaceAnchor(func(a string) string {
		i := indentOf(a)
		return i + "shadow() {\n" + i + "  builtin hash -p /bin/true '" + strings.Fields(a)[0] + "'\n" + i + "}\n" + i + "shadow\n" + a
	})},
	{"assigned alias in a called helper", replaceAnchor(func(a string) string {
		i := indentOf(a)
		return i + "shadow() {\n" + i + "  shopt -s expand_aliases\n" + i + "  X=1 command alias " + strings.Fields(a)[0] + "=':'\n" + i + "}\n" + i + "shadow\n" + a
	})},
	{"assigned hash in a called helper", replaceAnchor(func(a string) string {
		i := indentOf(a)
		return i + "shadow() {\n" + i + "  X=1 builtin hash -p /bin/true '" + strings.Fields(a)[0] + "'\n" + i + "}\n" + i + "shadow\n" + a
	})},
	{"alias in an if condition", replaceAnchor(func(a string) string {
		return indentOf(a) + "shopt -s expand_aliases\n" + indentOf(a) + "if command alias " + strings.Fields(a)[0] + "=':'; then :; fi\n" + a
	})},
	{"hash in a while condition", replaceAnchor(func(a string) string {
		return indentOf(a) + "while X=1 hash -p /bin/true '" + strings.Fields(a)[0] + "'; do break; done\n" + a
	})},
	{"debug trap before the anchor", replaceAnchor(func(a string) string {
		return indentOf(a) + "trap 'exit 0' DEBUG\n" + a
	})},
	{"debug trap in a called helper", replaceAnchor(func(a string) string {
		i := indentOf(a)
		return i + "shadow() {\n" + i + "  command trap 'exit 0' debug\n" + i + "}\n" + i + "shadow\n" + a
	})},
	{"step condition if: false", stepCondition("false")},
	{"step condition && false", stepCondition("${{ success() && false }}")},
	{"flattened argv", replaceAnchor(joinFirstWords)},
	{"unrelated placement", func(artifact, anchor string) string {
		moved := strings.Replace(artifact, anchor, indentOf(anchor)+"true", 1)
		return strings.TrimRight(moved, "\n") + "\nx-decoy: |\n" + anchor + "\n"
	}},
}

// RequireRejectsAllEvasions applies every row of the corpus to artifact and
// requires validate to reject the result, naming want in its error. want keeps a
// row honest: a rewrite that only broke the syntax would otherwise pass.
func RequireRejectsAllEvasions(t *testing.T, artifact, anchor, want string, validate func(string) error) {
	t.Helper()

	if !strings.Contains(artifact, anchor) {
		t.Fatalf("anchor %q is absent from the artifact", anchor)
	}
	for _, evasion := range Evasions {
		t.Run(evasion.Name, func(t *testing.T) {
			mutated := evasion.Rewrite(artifact, anchor)
			if mutated == artifact {
				t.Fatalf("evasion %q left the artifact unchanged", evasion.Name)
			}
			err := validate(mutated)
			if err != nil && anyRejection[evasion.Name] && !strings.Contains(err.Error(), "yaml: line") {
				return
			}
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("validate() error = %v, want %q", err, want)
			}
		})
	}
}

// replaceAnchor lifts a rewrite of the anchored command alone into a rewrite
// of the artifact that holds it.
func replaceAnchor(rewrite func(anchor string) string) func(artifact, anchor string) string {
	return func(artifact, anchor string) string {
		return strings.Replace(artifact, anchor, rewrite(anchor), 1)
	}
}

// hide wraps the anchored command in a body that the shell reads as text.
func hide(anchor, open, end string) string {
	return open + "\n" + anchor + "\n" + end
}

// commentOut comments every line of the anchored command, keeping the
// indentation that makes the result valid YAML.
func commentOut(anchor string) string {
	lines := strings.Split(anchor, "\n")
	for index, line := range lines {
		lines[index] = indentOf(line) + "# " + strings.TrimSpace(line)
	}
	return strings.Join(lines, "\n")
}

// flatten joins the anchored command into one line of text.
func flatten(anchor string) string {
	var words []string
	for _, line := range strings.Split(anchor, "\n") {
		words = append(words, strings.Fields(strings.TrimSuffix(strings.TrimSpace(line), "\\"))...)
	}
	return strings.Join(words, " ")
}

// extendFirstOption lengthens the first long option of the anchored command,
// the bypass that turns --oci-layout into --oci-layout-disabled. Most anchored
// commands carry no long option, so the command word itself is lengthened
// instead: either spelling keeps the anchored text in the file while bash runs
// something else. Without the second spelling the row rewrites nothing and the
// driver stops, which keeps the corpus off every such validator.
func extendFirstOption(anchor string) string {
	words := strings.Fields(anchor)
	if len(words) == 0 {
		return anchor
	}
	target := words[0]
	for _, word := range words {
		if strings.HasPrefix(word, "--") {
			target = word
			break
		}
	}
	return strings.Replace(anchor, target, target+"-disabled", 1)
}

// splitCommandWords breaks the command word of every command line of the anchor
// across a single-quoted newline. Inside single quotes bash keeps both the
// backslash and the newline, so the word names no program and the command does
// not run, while a reader that joins the halves before it knows the quoting
// sees the anchored command with the arguments it expects.
func splitCommandWords(anchor string) string {
	lines := strings.Split(anchor, "\n")
	continues := false
	for index, line := range lines {
		name, rest, found := strings.Cut(strings.TrimLeft(line, " \t"), " ")
		if !continues && len(name) > 1 {
			if found {
				rest = " " + rest
			}
			lines[index] = indentOf(line) + "'" + name[:1] + "\\\n" +
				indentOf(anchor) + name[1:] + "'" + rest
		}
		continues = strings.HasSuffix(strings.TrimSpace(line), "\\")
	}
	return strings.Join(lines, "\n")
}

// indentOf returns the leading whitespace of the first line of text.
func indentOf(text string) string {
	first, _, _ := strings.Cut(text, "\n")
	return first[:len(first)-len(strings.TrimLeft(first, " \t"))]
}

// prefixCommands puts text in front of every line of the anchored command that
// starts one, leaving the continuation lines alone.
func prefixCommands(anchor, text string) string {
	lines := strings.Split(anchor, "\n")
	continues := false
	for index, line := range lines {
		if !continues {
			lines[index] = indentOf(line) + text + strings.TrimLeft(line, " \t")
		}
		continues = strings.HasSuffix(strings.TrimSpace(line), "\\")
	}
	return strings.Join(lines, "\n")
}

// spliceCommandWords puts an expansion inside the command word of every command
// line. The variable is unset, so ${splice-x} expands to x and bash runs a
// program that does not exist, while a reader that drops expansions before it
// compares the word sees the anchored command.
func spliceCommandWords(anchor string) string {
	lines := strings.Split(anchor, "\n")
	continues := false
	for index, line := range lines {
		word := strings.TrimLeft(line, " \t")
		if !continues && len(word) > 1 {
			lines[index] = indentOf(line) + word[:1] + "${splice-x}" + word[1:]
		}
		continues = strings.HasSuffix(strings.TrimSpace(line), "\\")
	}
	return strings.Join(lines, "\n")
}

// joinFirstWords quotes the command word of the anchored command and the word
// after it into one argument, past any VAR=value prefix. bash then runs a
// program whose name holds a space, while a reader that compares
// strings.Join(argv, " ") sees the anchored text unchanged.
func joinFirstWords(anchor string) string {
	words := strings.Fields(anchor)
	first := 0
	for first < len(words)-1 && strings.Contains(words[first], "=") {
		first++
	}
	if first == len(words)-1 {
		return strings.Replace(anchor, words[first], `"`+words[first]+` "`, 1)
	}
	pair := words[first] + " " + words[first+1]
	return strings.Replace(anchor, pair, `"`+pair+`"`, 1)
}

// stepCondition gives the workflow step that holds the anchored command the
// condition, which GitHub evaluates to false, so the step never runs. A step
// that already has a condition gets the new one in its place. An artifact
// with no run: key is a shell script, and the condition becomes an if
// statement whose test always fails.
func stepCondition(condition string) func(artifact, anchor string) string {
	return func(artifact, anchor string) string {
		at := strings.Index(artifact, anchor)
		lines := strings.SplitAfter(artifact[:at], "\n")
		for index := len(lines) - 1; index >= 0; index-- {
			key := strings.TrimLeft(lines[index], " \t")
			indent := lines[index][:len(lines[index])-len(key)]
			if !strings.HasPrefix(key, "run:") && !strings.HasPrefix(key, "- run:") {
				continue
			}
			if strings.HasPrefix(key, "- ") {
				lines[index] = indent + "- if: " + condition + "\n" + indent + "  " + key[2:]
				return strings.Join(lines, "") + artifact[at:]
			}
			for step := index - 1; step >= 0; step-- {
				previous := strings.TrimLeft(lines[step], " \t")
				if strings.HasPrefix(previous, "if:") || strings.HasPrefix(previous, "- if:") {
					lines[step] = lines[step][:strings.Index(lines[step], "if:")] + "if: " + condition + "\n"
					return strings.Join(lines, "") + artifact[at:]
				}
				if strings.HasPrefix(previous, "- ") {
					break
				}
			}
			lines[index] = indent + "if: " + condition + "\n" + lines[index]
			return strings.Join(lines, "") + artifact[at:]
		}
		i := indentOf(anchor)
		return strings.Replace(artifact, anchor, hide(anchor, i+"if [[ -z x ]]; then", i+"fi"), 1)
	}
}

// sourcedBy writes a definition of the anchored command to shadow.sh and runs
// the line before the anchor, which sources that file in a spelling bash
// resolves only when it runs the line.
func sourcedBy(line string) func(artifact, anchor string) string {
	return replaceAnchor(func(a string) string {
		i := indentOf(a)
		return i + "echo '" + strings.Fields(a)[0] + "() { :; }' > shadow.sh\n" + i + line + "\n" + a
	})
}
