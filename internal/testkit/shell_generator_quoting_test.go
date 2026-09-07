// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package testkit

import (
	"go/scanner"
	"go/token"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// minTrackedGoFiles is the inventory floor for the Go-source walk, so a walk
// that returns nothing cannot pass the ban vacuously.
const minTrackedGoFiles = 100

// shellSourceMarker matches a decoded string literal that is shell source text
// rather than prose: an interpreter line, the house prologue, or a source
// command at the start of a script line.
var shellSourceMarker = regexp.MustCompile(`#!/|set -euo|(^|\n)(builtin )?source `)

// goQuoteDirective matches a complete fmt directive whose conversion is q. All
// of the spellings fmt accepts reach the same shell-unsafe conversion: flags
// (%#q), a literal or starred width and precision (%-8q, %*q, %.2q), an
// argument index on either (%[2]*[1]q), and a plain %q.
var goQuoteDirective = regexp.MustCompile(`%[-+# 0]*(\d+|\*|\[\d+\]\*)?(\.(\d+|\*|\[\d+\]\*)?)?(\[\d+\])?q`)

// hasGoQuoteDirective reports whether text applies the q conversion.
//
// A doubled percent sign is a literal percent, so whether a match opens a real
// directive is decided by the parity of the percent run that precedes it. An
// even run is a sequence of complete escaped pairs and the match is a
// directive; an odd run means this match's own percent closes the last pair.
// %%q is therefore a literal, while %%%q is an escaped percent then a real
// conversion.
func hasGoQuoteDirective(text string) bool {
	for _, match := range goQuoteDirective.FindAllStringIndex(text, -1) {
		run := 0
		for i := match[0] - 1; i >= 0 && text[i] == '%'; i-- {
			run++
		}
		if run%2 == 0 {
			return true
		}
	}
	return false
}

// goQuoteInShellLiteral reports the 1-based lines of goSource where a shell
// script literal interpolates a value with Go's q conversion.
//
// The scan tokenises rather than reading lines. A generated script is written
// as string literals joined by +, so the marker that identifies the text as
// shell and the directive that spoils it are usually in different fragments of
// one expression; the fragments are therefore grouped and judged together.
// Taking them from the tokeniser also means comments never reach the scan and
// both literal forms decode the same way, which reading lines cannot do: a
// trailing or block comment about shell syntax is not script text, and a raw
// literal's body lines are.
func goQuoteInShellLiteral(goSource string) []int {
	fileSet := token.NewFileSet()
	file := fileSet.AddFile("", fileSet.Base(), len(goSource))
	var lexer scanner.Scanner
	lexer.Init(file, []byte(goSource), nil, 0)

	var found []int
	group, directiveLines := "", []int(nil)
	flush := func() {
		if shellSourceMarker.MatchString(group) {
			found = append(found, directiveLines...)
		}
		group, directiveLines = "", nil
	}
	for {
		pos, tok, literal := lexer.Scan()
		switch tok {
		case token.EOF:
			flush()
			return found
		case token.STRING:
			text := decodeGoLiteral(literal)
			group += text
			if hasGoQuoteDirective(text) {
				directiveLines = append(directiveLines, fileSet.Position(pos).Line)
			}
		case token.ADD:
			// A concatenation keeps the fragments of one literal together.
		default:
			flush()
		}
	}
}

// decodeGoLiteral returns the text a Go string literal denotes. An unparsable
// literal is returned as written, which can only over-report.
func decodeGoLiteral(literal string) string {
	if text, err := strconv.Unquote(literal); err == nil {
		return text
	}
	return literal
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
		// Read through the same no-follow descriptor discipline the Bash walk
		// uses, so neither walk judges a file other than the one it named.
		content, err := readTrackedFile(filepath.Join(root, rel))
		if err != nil {
			t.Errorf("read %s: %v", rel, err)
			continue
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

// goQuoteFixture is Go source that carries every form the scan must judge.
// The q conversions are assembled from pieces so this file does not report
// itself, and every line is numbered against the fixture, not this file.
func goQuoteFixture() string {
	q := "%" + "q"
	pct := "%"
	tick := "`"
	return strings.Join([]string{
		`package p`,             // 1
		``,                      // 2
		`func concatenated() {`, // 3
		`	_ = fmt.Sprintf("set -euo pipefail\n"+`, // 4: the marker fragment
		`		"builtin source ` + q + `\n"+`,         // 5: reported
		`		"cmd ` + q + `\n", one, two)`,          // 6: reported
		`	t.Fatalf("output ` + q + `", got)`,      // 7: prose, a separate expression
		`}`,                                       // 8
		``,                                        // 9
		`func rawLiteral() {`,                     // 10
		`	_ = fmt.Sprintf(` + tick + `#!/bin/sh`,  // 11: a raw literal opens on the marker
		`printf 'x' >> ` + q,                      // 12: reported, same literal
		`exec /bin/sleep 60`,                      // 13
		tick + `, commandLog)`,                    // 14
		`}`,                                       // 15
		``,                                        // 16
		`func directiveForms() {`,                 // 17
		`	_ = fmt.Sprintf("#!/bin/bash\n"+`,       // 18: the marker fragment
		`		"indexed ` + pct + `[1]q\n"+`,          // 19: reported
		`		"flagged ` + pct + `#q\n"+`,            // 20: reported
		`		"padded ` + pct + `-8q\n"+`,            // 21: reported
		`		"star ` + pct + `*q\n"+`,               // 22: reported
		`		"star indexed ` + pct + `[2]*[1]q\n"+`, // 23: reported
		`		"escaped then real ` + pct + pct + pct + `q\n"+`, // 24: reported
		`		"literal ` + pct + pct + `q\n"+`,                 // 25: an escaped percent only
		`		"safe ` + pct + `s\n", a, b, c)`,                 // 26: a different conversion
		`}`,                                                 // 27
		``,                                                  // 28
		`func comments() {`,                                 // 29
		`	// A comment naming #!/bin/bash and ` + pct + `q is prose.`, // 30
		`	_ = fmt.Sprintf("set -euo pipefail\n") /* ` + pct + `q */`,  // 31: inline comment
		`	/*`, // 32
		`	   #!/bin/bash ` + pct + `q in a block comment`, // 33
		`	*/`, // 34
		`}`,   // 35
	}, "\n")
}

// TestGoQuoteInShellLiteralFindsEveryGeneratorForm drives the whole fixture at
// once. Every reported line is a real defect and every unreported line is a
// form that only resembles one.
func TestGoQuoteInShellLiteralFindsEveryGeneratorForm(t *testing.T) {
	t.Parallel()

	requireLines(t, goQuoteInShellLiteral(goQuoteFixture()),
		5, 6, // concatenated fragments after a marker fragment
		11,                     // a raw literal, reported at the line its single token starts on
		19, 20, 21, 22, 23, 24, // the directive spellings
	)
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
