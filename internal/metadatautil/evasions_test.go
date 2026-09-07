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
	{"escaped apostrophe in an ANSI-C word", replaceAnchor(func(a string) string {
		return prefixCommands(a, `: $'x\'; `)
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
	{"prefix extension", replaceAnchor(extendFirstOption)},
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
		if !continues && found && len(name) > 1 {
			lines[index] = indentOf(line) + "'" + name[:1] + "\\\n" +
				indentOf(anchor) + name[1:] + "' " + rest
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
