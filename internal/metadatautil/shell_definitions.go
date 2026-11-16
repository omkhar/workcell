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
// literal text an eval or a trap action runs. functionBodies binds a body only
// to a definition it reads in command position, so it misses some of these. A
// check that fails closed on a definition reads this list instead; it may also
// name a word that only looks like one, which fails closed too. Bash refuses a
// quoted or escaped name, so only an unquoted word names one, but a quoted or
// escaped eval, trap, command or builtin still runs, so those are matched by
// their text.
//
// complete is false when the reader stopped short: an eval or trap text nested
// more than eight deep, or more parsing than a bound linear in the text's
// length. Only the first eval or trap of a command is read, since its operand
// holds every word after it, and a run of continued lines is parsed once, so
// no text costs more than a fixed multiple of its length.
func definedFunctionNames(text string) (names []string, complete bool) {
	budget := 16*len(text) + 1<<16
	var read func(text string, depth int) bool
	read = func(text string, depth int) bool {
		return depth <= 8 && shellLogicalCommands(text, &budget, func(each command) bool {
			return readCommand(each.args, depth, read, &names)
		})
	}
	return names, read(text, 0)
}

// shellLogicalCommands calls each with every command of every logical line of
// text, after flattenSubstitutions and heredocsAsHereStrings, and stops when
// each returns false. A run of continued lines is joined as shellContinues
// reads it. Every parse is charged to budget, and the read stops once it runs
// out. It returns whether it read all of text.
func shellLogicalCommands(text string, budget *int, each func(command) bool) bool {
	return shellLines(flattenSubstitutions(heredocsAsHereStrings(text)), budget, each)
}

// shellLines is shellLogicalCommands on text whose substitutions and heredocs
// are already flattened.
func shellLines(text string, budget *int, each func(command) bool) bool {
	parse := func(line string) ([]word, bool) {
		if *budget -= len(line) + 1; *budget < 0 {
			return nil, false
		}
		words, _, _, _, _, _ := shellWords(line, nil)
		return words, true
	}
	var joined strings.Builder
	clean := true
	var carried command // the operator that ended the last line: its && or || decides, and its | pipes, the next command
	for line := range strings.Lines(text) {
		line = strings.TrimSuffix(line, "\n")
		continues, stillClean, ok := shellContinues(joined.String(), line, clean, budget)
		if !ok {
			return false
		}
		if continues {
			joined.WriteString(strings.TrimSuffix(line, `\`))
			clean = stillClean
			continue
		}
		joined.WriteString(line)
		words, ok := parse(joined.String())
		if !ok {
			return false
		}
		joined.Reset()
		clean = true
		commands := splitCommands(words)
		if len(words) > 0 {
			commands[0].conditional = commands[0].conditional || carried.conditional
			commands[0].piped = commands[0].piped || carried.piped
			carried = command{}
			if last := commands[len(commands)-1]; len(commands) > 1 && len(last.args) == 0 {
				carried = last
			}
		}
		for _, command := range commands {
			if !each(command) {
				return false
			}
		}
	}
	return true
}

// readCommand adds each name one command defines and reads the text its
// first eval or trap runs, and reports whether read read it all.
func readCommand(args []word, depth int, read func(string, int) bool, names *[]string) bool {
	for at, arg := range args {
		switch {
		case !arg.quoted && arg.text == "function" && at+1 < len(args) && leadsCommand(args, at):
			*names = append(*names, strings.TrimSuffix(args[at+1].text, "()"))
		case arg.text == "eval" && leadsCommand(args, at):
			return read(strings.Join(texts(args[at+1:]), " "), depth+1)
		case arg.text == "trap" && leadsCommand(args, at):
			operands := args[at+1:]
			if len(operands) > 0 && operands[0].text == "--" {
				operands = operands[1:]
			}
			return len(operands) == 0 || strings.HasPrefix(operands[0].text, "-") || read(operands[0].text, depth+1)
		case arg.quoted:
		default:
			joinedWord := arg.text
			for next := at + 1; next < min(at+3, len(args)) && !args[next].quoted; next++ {
				joinedWord += args[next].text
			}
			if match := definitionWord.FindStringSubmatch(joinedWord); match != nil {
				*names = append(*names, match[1])
			}
		}
	}
	return true
}

// leadsCommand reports whether the word at at stands where bash reads a
// command word: first, or after a reserved word, a case pattern, an
// assignment, or a command or builtin and its options, quoted or not, since
// those run the builtin after them. A redirection and its target, as in
// >/dev/null eval or 2> f eval, may stand before any of these, so they are
// skipped as an assignment is.
func leadsCommand(args []word, at int) bool {
	for at > 0 {
		if shellRedirectionWord(args[at-1]) {
			at-- // a redirection with its target in the same word, as in >/dev/null
		} else if at > 1 && shellRedirectionWord(args[at-2]) && redirectionOperator.FindString(args[at-2].syntax) == args[at-2].syntax {
			at -= 2 // a redirection whose target is the next word, as in > f
		} else {
			break
		}
	}
	if at == 0 {
		return true
	}
	prev := args[at-1]
	if !prev.quoted && (slices.Contains(shellKeywords, prev.text) || isCommandPrefixWord(prev.text) ||
		prev.text == "(" || strings.HasSuffix(prev.syntax, ")") || shellAssignment.MatchString(prev.text)) {
		return true
	}
	for at--; at > 0 && strings.HasPrefix(args[at].text, "-"); at-- {
	}
	return args[at].text == "command" || args[at].text == "builtin"
}

// shellContinues reports whether line, read after the continued lines joined
// before it, ends on a backslash the quoting leaves as syntax, so bash reads
// the next line as the rest of this one, and whether the run is still clean:
// no quote or substitution open at its end. While the run is clean at a word
// break the line alone says so, so a run of continued lines is parsed once in
// all; otherwise the run so far is parsed again. The parse is charged to
// budget, and ok is false once it runs out.
func shellContinues(joined, line string, clean bool, budget *int) (continues, stillClean, ok bool) {
	if !strings.HasSuffix(line, `\`) {
		return false, clean, true
	}
	probe := line
	if !clean || joined != "" && !strings.HasSuffix(joined, " ") && !strings.HasSuffix(joined, "\t") {
		probe = joined + line
	}
	if *budget -= len(probe) + 1; *budget < 0 {
		return false, false, false
	}
	_, _, open, rest, continues, _ := shellWords(probe, nil)
	return continues, open == 0 && len(rest) == 0, true
}

// redirectionOperator is the operator at the start of a redirection word, with
// the descriptor, {name} or & in front of it, as in >f, 2>&1, &>f, <f or
// {fd}>f.
var redirectionOperator = regexp.MustCompile(`^(?:[0-9]+|\{[A-Za-z_][A-Za-z0-9_]*\}|&)?(?:<<<|<<-?|<>|>>|>\||[<>]&?)`)

// shellRedirectionWord reports whether a word starts with a redirection
// operator bash reads as syntax, unquoted.
func shellRedirectionWord(each word) bool {
	return redirectionOperator.MatchString(each.syntax)
}
