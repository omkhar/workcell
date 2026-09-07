// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil_test

import (
	"strings"
	"testing"
)

// Evasion rewrites one artifact so that the anchored command it must contain
// is no longer run, while text that names the command stays in the file. Every
// row is a bypass a reviewer found against a validator that read the artifact
// as text instead of as shell words.
//
// Rewrite takes the whole artifact and the exact text of the anchored command,
// so a row can move the command as well as disguise it.
type Evasion struct {
	Name    string
	Rewrite func(artifact, anchor string) string
}

// Evasions is the shared negative corpus. A validator that reads file content
// to decide that a command runs must reject every row.
//
// The corpus lives in the test package so that the parser it exercises stays
// out of the shipped tool's dependency graph. Move it to a non-test file when
// a validator outside this package needs it.
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
	{"prefix extension", replaceAnchor(extendFirstOption)},
	{"unrelated placement", func(artifact, anchor string) string {
		moved := strings.Replace(artifact, anchor, indentOf(anchor)+"true", 1)
		return strings.TrimRight(moved, "\n") + "\nx-decoy: |\n" + anchor + "\n"
	}},
}

// RequireRejectsAllEvasions applies every row of the corpus to artifact and
// requires validate to reject the result, naming want in its error. want keeps
// a row honest: a rewrite that merely broke the file's syntax would otherwise
// pass for the wrong reason.
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
// the bypass that turns --oci-layout into --oci-layout-disabled.
func extendFirstOption(anchor string) string {
	for _, word := range strings.Fields(anchor) {
		if strings.HasPrefix(word, "--") {
			return strings.Replace(anchor, word, word+"-disabled", 1)
		}
	}
	return anchor
}

// indentOf returns the leading whitespace of the first line of text.
func indentOf(text string) string {
	first, _, _ := strings.Cut(text, "\n")
	return first[:len(first)-len(strings.TrimLeft(first, " \t"))]
}
