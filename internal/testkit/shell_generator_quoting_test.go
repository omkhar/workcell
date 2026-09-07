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

// goQuoteInShellLiteral reports the 1-based lines of goSource that build a
// shell script literal interpolating a value with Go's q conversion.
//
// The scan tokenises rather than reading lines, and judges a whole
// concatenation rather than its fragments. A generated script is written as
// string literals joined by +, so both the marker that identifies the text as
// shell and the directive that spoils it are properties of the joined value:
// "printf " + "%" + "q" is a real directive that no fragment contains, and
// "%" + "%q" is an escaped percent that one fragment appears to contain.
// Taking the tokens from go/scanner also keeps comments out of the scan and
// decodes both literal forms alike, which reading lines cannot do: a trailing
// or block comment about shell syntax is not script text, and a raw literal's
// body lines are. Each group is reported at the line its first fragment
// starts on.
func goQuoteInShellLiteral(goSource string) []int {
	fileSet := token.NewFileSet()
	file := fileSet.AddFile("", fileSet.Base(), len(goSource))
	var lexer scanner.Scanner
	lexer.Init(file, []byte(goSource), nil, 0)

	var found []int
	group, groupLine, open := "", 0, false
	flush := func() {
		if open && shellSourceMarker.MatchString(group) && hasGoQuoteDirective(group) {
			found = append(found, groupLine)
		}
		group, groupLine, open = "", 0, false
	}
	for {
		pos, tok, literal := lexer.Scan()
		switch tok {
		case token.EOF:
			flush()
			return found
		case token.STRING:
			if !open {
				groupLine, open = fileSet.Position(pos).Line, true
			}
			group += decodeGoLiteral(literal)
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

// goQuoteFixture is Go source carrying every form the scan must judge. Each
// generator is its own statement, so each reports at its own line. The q
// conversions are assembled from pieces so this file does not report itself,
// and the line numbers are the fixture's own.
func goQuoteFixture() string {
	q := "%" + "q"
	pct := "%"
	tick := "`"
	return strings.Join([]string{
		`package p`,             // 1
		``,                      // 2
		`func concatenated() {`, // 3
		`	_ = fmt.Sprintf("set -euo pipefail\n"+`, // 4: reported, the group starts here
		`		"builtin source ` + q + `\n"+`,         // 5: same group
		`		"cmd ` + q + `\n", one, two)`,          // 6: same group
		`	t.Fatalf("output ` + q + `", got)`,      // 7: its own group, no marker
		`}`,                                       // 8
		``,                                        // 9
		`func rawLiteral() {`,                     // 10
		`	_ = fmt.Sprintf(` + tick + `#!/bin/sh`,  // 11: reported, one token spans to 14
		`printf 'x' >> ` + q,                      // 12
		`exec /bin/sleep 60`,                      // 13
		tick + `, commandLog)`,                    // 14
		`}`,                                       // 15
		``,                                        // 16
		`func directiveForms() {`,                 // 17
		`	_ = fmt.Sprintf("#!/bin/bash\nindexed ` + pct + `[1]q\n", a)`,                    // 18: reported
		`	_ = fmt.Sprintf("#!/bin/bash\nflagged ` + pct + `#q\n", a)`,                      // 19: reported
		`	_ = fmt.Sprintf("#!/bin/bash\npadded ` + pct + `-8q\n", a)`,                      // 20: reported
		`	_ = fmt.Sprintf("#!/bin/bash\nstar ` + pct + `*q\n", w, a)`,                      // 21: reported
		`	_ = fmt.Sprintf("#!/bin/bash\nstar indexed ` + pct + `[2]*[1]q\n", a, w)`,        // 22: reported
		`	_ = fmt.Sprintf("#!/bin/bash\nescaped then real ` + pct + pct + pct + `q\n", a)`, // 23: reported
		`	_ = fmt.Sprintf("#!/bin/bash\nliteral ` + pct + pct + `q\n")`,                    // 24: an escaped percent only
		`	_ = fmt.Sprintf("#!/bin/bash\nsafe ` + pct + `s\n", a)`,                          // 25: a different conversion
		`}`,                             // 26
		``,                              // 27
		`func splitAcrossFragments() {`, // 28
		`	_ = fmt.Sprintf("#!/bin/bash\nprintf " + "` + pct + `" + "q\n", a)`,         // 29: reported, the joined value is a directive
		`	_ = fmt.Sprintf("#!/bin/bash\nprintf " + "` + pct + `" + "` + pct + `q\n")`, // 30: the joined value is an escaped percent
		`}`,                 // 31
		``,                  // 32
		`func comments() {`, // 33
		`	// A comment naming #!/bin/bash and ` + pct + `q is prose.`, // 34
		`	_ = fmt.Sprintf("set -euo pipefail\n") /* ` + pct + `q */`,  // 35: trailing comment
		`	/*`, // 36
		`	   #!/bin/bash ` + pct + `q in a block comment`, // 37
		`	*/`, // 38
		`}`,   // 39
	}, "\n")
}

// TestGoQuoteInShellLiteralFindsEveryGeneratorForm drives the whole fixture at
// once. Every reported line is a real defect and every unreported line is a
// form that only resembles one.
func TestGoQuoteInShellLiteralFindsEveryGeneratorForm(t *testing.T) {
	t.Parallel()

	requireLines(t, goQuoteInShellLiteral(goQuoteFixture()),
		4,                      // a concatenation whose marker and directives are in different fragments
		11,                     // a raw literal, reported where its single token starts
		18, 19, 20, 21, 22, 23, // the directive spellings
		29, // a directive spelled across fragments
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
