// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"regexp"
	"slices"
	"strings"
)

// definitionWord is a function name and the () after it, read from a word and
// the two words after it joined, so name(), name () and name ( ) all match.
var definitionWord = regexp.MustCompile("^([^\\s()<>|&;=$`'\"\\\\]+)\\(\\)")

// definedFunctionNames returns the name of each function text may define, in
// order, wherever bash reads a definition: in command position, after a
// reserved word such as then or do, in a case arm, nested in a body, with a
// body in ( ) as well as { }, with name ( ) spelled as three words, and in the
// literal text an eval runs. functionBodies binds a body only to a definition
// it reads in command position, so it misses some of these. A check that fails
// closed on a definition reads this list instead; it may also name a word that
// only looks like one, which fails closed too. Bash refuses a quoted or escaped
// name, so only an unquoted word names one.
func definedFunctionNames(text string) []string {
	var names []string
	var read func(text string, depth int)
	read = func(text string, depth int) {
		joined := ""
		for line := range strings.Lines(flattenSubstitutions(heredocsAsHereStrings(text))) {
			line = strings.TrimSuffix(line, "\n")
			words, _, _, _, continues, _ := shellWords(joined+line, nil)
			if continues {
				joined += strings.TrimSuffix(line, `\`)
				continue
			}
			joined = ""
			for _, each := range splitCommands(words) {
				args := each.args
				// leads reports whether the word at at stands where bash reads a
				// command word: first, or after a reserved word, a case pattern,
				// an assignment or a wrapper that runs a builtin.
				leads := func(at int) bool {
					if at == 0 {
						return true
					}
					prev := args[at-1]
					return !prev.quoted && (slices.Contains(shellKeywords, prev.text) || isCommandPrefixWord(prev.text) ||
						prev.text == "(" || strings.HasSuffix(prev.syntax, ")") || shellAssignment.MatchString(prev.text) ||
						prev.text == "command" || prev.text == "builtin")
				}
				for at, arg := range args {
					switch {
					case arg.quoted:
					case arg.text == "function" && at+1 < len(args) && leads(at):
						names = append(names, strings.TrimSuffix(args[at+1].text, "()"))
					case arg.text == "eval" && leads(at) && depth < 8:
						read(strings.Join(texts(args[at+1:]), " "), depth+1)
					default:
						joinedWord := arg.text
						for next := at + 1; next < min(at+3, len(args)) && !args[next].quoted; next++ {
							joinedWord += args[next].text
						}
						if match := definitionWord.FindStringSubmatch(joinedWord); match != nil {
							names = append(names, match[1])
						}
					}
				}
			}
		}
	}
	read(text, 0)
	return names
}
