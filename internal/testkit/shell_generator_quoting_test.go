// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package testkit

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// minTrackedGoFiles is the inventory floor for the Go-source walk, so a walk
// that returns nothing cannot pass the ban vacuously.
const minTrackedGoFiles = 100

// shellSourceMarker matches a Go string fragment that is Bash source text
// rather than prose: an interpreter line, the house prologue, or a source
// command at the start of a script line.
var shellSourceMarker = regexp.MustCompile(`#!/|set -euo|\\n(builtin )?source |"(builtin )?source `)

// goQuoteInShellLiteral reports the 1-based lines of goSource where a shell
// script literal interpolates a value with Go's %q verb. A marker opens the
// block and the following fragments of the same literal stay inside it, so a
// %q on a later line is reported too.
//
// Both Go literal forms carry script text and both are tracked. Concatenated
// interpreted fragments continue the block while each line opens with a double
// quote. A raw literal has no such fragments: its body lines are plain script
// text, so the block instead stays open until the closing backquote. A line
// holding an odd number of backquotes opens or closes one.
//
// The scan is deliberately a line scan rather than a Go parse: the generator
// sites are few, and a greppable rule stays readable to the shell reviewers it
// serves.
func goQuoteInShellLiteral(goSource string) []int {
	var found []int
	inShell, inRawLiteral := false, false
	for i, line := range strings.Split(goSource, "\n") {
		switch {
		case inRawLiteral:
			// The raw literal body continues; only its closing backquote ends it.
		case shellSourceMarker.MatchString(line):
			inShell = true
		case !strings.HasPrefix(strings.TrimSpace(line), `"`):
			inShell = false
		}
		if inShell && strings.Count(line, "`")%2 == 1 {
			inRawLiteral = !inRawLiteral
		}
		if inShell && strings.Contains(line, "%q") {
			found = append(found, i+1)
		}
	}
	return found
}

// TestGeneratedShellScriptsDoNotUseGoQuoting bans %q from the literals that
// become Bash source. %q emits a Go literal; the shell re-reads it and expands
// what it finds, so a value holding $HOME or $(...) escapes the quoting the
// author believed they had applied.
func TestGeneratedShellScriptsDoNotUseGoQuoting(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	scanned := 0
	for _, rel := range trackedFiles(t, root) {
		if !strings.HasSuffix(rel, ".go") {
			continue
		}
		content, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		scanned++
		for _, line := range goQuoteInShellLiteral(string(content)) {
			t.Errorf("%s:%d: a Bash script literal interpolates with %%q; use testkit.ShellQuote with %%s", rel, line)
		}
	}
	if scanned < minTrackedGoFiles {
		t.Fatalf("scanned %d Go files, want at least %d; the tracked-file walk did not complete", scanned, minTrackedGoFiles)
	}
}

func TestGoQuoteInShellLiteralFindsTheGeneratorForms(t *testing.T) {
	t.Parallel()

	// The verb is assembled so that this fixture does not report its own file.
	const verb = "%" + "q"
	source := strings.Join([]string{
		`package p`, // 1
		`func a() { _ = fmt.Sprintf("#!/bin/bash\nx ` + verb + `\n") }`, // 2: marker and verb on one line
		`func b() {`, // 3
		`	_ = fmt.Sprintf("set -euo pipefail\n"+`, // 4: marker opens the block
		`		"builtin source ` + verb + `\n"+`,      // 5: inside the block
		`		"cmd ` + verb + `\n", one, two)`,       // 6: still inside the block
		`	t.Fatalf("output ` + verb + `", got)`,   // 7: prose, block already closed
		`}`,                                       // 8
	}, "\n")

	requireLines(t, goQuoteInShellLiteral(source), 2, 5, 6)
}

// TestGoQuoteInShellLiteralTracksRawLiterals covers the second Go literal form.
// A raw literal's body lines are plain script text, so the double-quote rule
// that continues a concatenated literal does not apply to them and the block
// must stay open until the closing backquote instead.
func TestGoQuoteInShellLiteralTracksRawLiterals(t *testing.T) {
	t.Parallel()

	const verb = "%" + "q"
	const tick = "`"
	source := strings.Join([]string{
		`package p`,  // 1
		`func c() {`, // 2
		`	script := fmt.Sprintf(` + tick + `#!/bin/sh`, // 3: the marker opens a raw literal
		`printf 'x' >> ` + verb,                        // 4: raw body, reported
		`exec /bin/sleep 60`,                           // 5: raw body, no verb
		tick + `, commandLog)`,                         // 6: the raw literal closes
		`	t.Fatalf("output ` + verb + `", got)`,        // 7: prose, block already closed
		`}`,                                            // 8
	}, "\n")

	requireLines(t, goQuoteInShellLiteral(source), 4)
}

func requireLines(t *testing.T, got []int, want ...int) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("goQuoteInShellLiteral() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("goQuoteInShellLiteral() = %v, want %v", got, want)
		}
	}
}
