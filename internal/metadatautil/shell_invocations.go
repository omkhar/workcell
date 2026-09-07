// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"slices"
	"strings"
)

// heredoc is one body the shell has still to read: the delimiter word that
// ends it, and whether the <<- form lets the terminator line carry leading
// tabs. Bash ends a <<WORD body only at a line that is the delimiter alone, so
// an indented copy of the word inside the body is text.
type heredoc struct {
	delimiter string
	stripTabs bool
}

func (h heredoc) endsAt(line string) bool {
	if h.stripTabs {
		return strings.TrimLeft(line, "\t") == h.delimiter
	}
	return line == h.delimiter
}

// definesFunction reports whether the words open a function definition, in
// either the name() or the function name spelling.
func definesFunction(words []string) bool {
	if len(words) == 0 {
		return false
	}
	if words[0] == "function" {
		return true
	}
	name, _, found := strings.Cut(words[0], "(")
	if found && name != "" && strings.HasPrefix(words[0][len(name):], "()") {
		return true
	}
	return len(words) > 1 && words[1] == "()"
}

// braceDepth returns the change in brace nesting the words make. Only a brace
// that stands as its own word or ends one groups commands; a brace inside a
// word belongs to an expansion such as ${VAR}.
func braceDepth(words []string) int {
	change := 0
	for _, word := range words {
		switch {
		case word == "{" || strings.HasSuffix(word, "(){"):
			change++
		case word == "}" || word == "};":
			change--
		}
	}
	return change
}

// controlWords maps each word that opens or closes a compound command to the
// change it makes to nesting. A command inside one of these is not proved to
// run: bash never runs the body of if false, so a required command written
// there does not satisfy a rule about what the step runs.
var controlWords = map[string]int{
	"if": 1, "while": 1, "until": 1, "for": 1, "case": 1, "select": 1,
	"fi": -1, "done": -1, "esac": -1,
}

// isOperator reports whether the word is a control operator that ends one
// command and starts the next.
func isOperator(word string) bool {
	switch word {
	case ";", ";;", "&&", "||", "|", "|&", "&":
		return true
	}
	return false
}

// splitCommands cuts one logical line into the separate commands bash runs,
// at every control operator. Without this, a decoy after ; or || donates its
// words to the invocation before it, as in
// oras cp --from-oci-layout missing || true; : --to-oci-layout <target>.
func splitCommands(words []string) [][]string {
	commands := make([][]string, 1)
	for _, word := range words {
		if isOperator(word) {
			commands = append(commands, nil)
			continue
		}
		commands[len(commands)-1] = append(commands[len(commands)-1], word)
	}
	return commands
}

// closesQuote reports whether the line closes an open quote. A backslash
// escapes the next byte inside a double-quoted span, so an escaped quote is a
// literal character and not the closer. A single-quoted span has no escapes.
func closesQuote(line string, quote byte) bool {
	for index := 0; index < len(line); index++ {
		if quote == '"' && line[index] == '\\' {
			index++
			continue
		}
		if line[index] == quote {
			return true
		}
	}
	return false
}

// ShellInvocations returns the arguments of each invocation of command in
// script. It joins line continuations, splits each line at the operators that
// end one command, and drops comments, inline ones included, heredoc bodies,
// the body of a function definition, the body of a compound command, and the
// rest of a quoted word that runs past the end of its line, so that no decoy
// text counts as a command and one call cannot satisfy a two-call rule. A
// validator that must anchor on the commands a script really runs uses this in
// place of a substring search.
func ShellInvocations(script, command string) [][]string {
	prefix := strings.Fields(command)
	var invocations [][]string
	var current strings.Builder
	var heredocs []heredoc
	var openQuote byte
	var quotes []byte
	var depth, definedAt, control int
	var defining bool
	for line := range strings.Lines(script) {
		text := strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if openQuote != 0 {
			// Bash reads these lines as text inside one word, as in
			// : "<newline>oras cp …<newline>", which runs no command. Read
			// them as text too, up to the line that closes the quote.
			if closesQuote(text, openQuote) {
				openQuote = 0
			}
			continue
		}
		if len(heredocs) > 0 {
			if heredocs[0].endsAt(text) {
				heredocs = heredocs[1:]
			}
			continue
		}
		trimmed := strings.TrimSpace(text)
		current.WriteString(strings.TrimSpace(strings.TrimSuffix(trimmed, "\\")))
		if strings.HasSuffix(trimmed, "\\") {
			current.WriteString(" ")
			continue
		}
		words, opened, quote, rest := shellWords(current.String(), quotes)
		current.Reset()
		openQuote, quotes = quote, rest
		heredocs = append(heredocs, opened...)
		// A function definition is not a call. Bash reads the body and runs
		// nothing, so a required command written inside a function that
		// nobody calls does not satisfy a rule about what the step runs.
		if !defining && definesFunction(words) {
			defining, definedAt = true, depth
		}
		depth += braceDepth(words)
		if defining {
			if depth <= definedAt {
				defining = false
			}
			continue
		}
		commands := splitCommands(words)
		nested := control > 0
		for _, args := range commands {
			if len(args) == 0 {
				continue
			}
			if change, found := controlWords[args[0]]; found {
				control = max(control+change, 0)
				nested = true
				continue
			}
			if nested || control > 0 {
				continue
			}
			if len(args) >= len(prefix) && slices.Equal(args[:len(prefix)], prefix) {
				invocations = append(invocations, args[len(prefix):])
			}
		}
	}
	return invocations
}

// shellWords splits one logical line the way bash reads it. Quotes and
// backslash escapes are removed, so a quoted argument stays one word and a
// separator inside it is text rather than syntax; an unquoted # that starts a
// word ends the line; every heredoc the line opens returns as a delimiter, in
// the order bash reads the bodies; open is the quote character still waiting to
// be closed, which tells the caller that the word runs on into the next line;
// and rest is the quote each unclosed command substitution suspended, which the
// caller gives back on the next line so that the ) which closes the
// substitution also restores the quote around it.
//
// One pass keeps the four answers consistent. A pattern per answer cannot:
// each has to rediscover the quoting, and the one that gets it wrong reads
// syntax where the shell reads text.
func shellWords(line string, stack []byte) (words []string, heredocs []heredoc, open byte, rest []byte) {
	var word strings.Builder
	inWord, quote, pending, stripTabs := false, byte(0), false, false
	flush := func() {
		if !inWord {
			return
		}
		if pending {
			heredocs = append(heredocs, heredoc{word.String(), stripTabs})
			pending, stripTabs = false, false
		} else {
			words = append(words, word.String())
		}
		word.Reset()
		inWord = false
	}
	for index := 0; index < len(line); index++ {
		character := line[index]
		switch {
		case quote == '\'':
			// A backslash is literal inside single quotes.
			if character == '\'' {
				quote = 0
			} else {
				word.WriteByte(character)
			}
		case quote == '"':
			switch {
			case character == '\\' && index+1 < len(line) && strings.IndexByte("$`\"\\", line[index+1]) >= 0:
				// A backslash escapes only these characters here. Before any
				// other one it is a literal byte of the word, so a name such
				// as "or\as" is not the command it resembles.
				index++
				word.WriteByte(line[index])
			case character == '"':
				quote = 0
			case character == '`' || (character == '$' && index+1 < len(line) && line[index+1] == '('):
				// A command substitution resumes shell syntax inside the
				// quotes. Suspend the quote rather than forget it, so that the
				// ) which closes the substitution restores it, even when that )
				// is on a later line.
				stack = append(stack, quote)
				quote = 0
				word.WriteByte(character)
			default:
				word.WriteByte(character)
			}
		case character == '\\' && index+1 < len(line):
			// An escaped quote is a literal character, not the start of a
			// quoted span, so it cannot hide the syntax that follows it.
			index++
			word.WriteByte(line[index])
			inWord = true
		case character == '\'' || character == '"':
			quote = character
			inWord = true
		case character == ' ' || character == '\t':
			flush()
		case character == '#' && !inWord:
			return words, heredocs, 0, stack
		case character == '<' && index+1 < len(line) && line[index+1] == '<':
			flush()
			index++
			if index+1 < len(line) && line[index+1] == '<' {
				// A here-string takes its text from the line, not from a body.
				index++
				break
			}
			if index+1 < len(line) && line[index+1] == '-' {
				index++
				stripTabs = true
			}
			pending = true
		case pending && strings.IndexByte(";&|<>()", character) >= 0:
			// A delimiter word ends at an operator, as in cat <<EOF; echo
			// ready, where bash reads the delimiter EOF and runs the echo.
			flush()
		case strings.IndexByte(";&|", character) >= 0 &&
			!(character == '&' && index > 0 && strings.IndexByte("<>", line[index-1]) >= 0):
			// An operator ends the command before it. The guard keeps the & of
			// a redirection such as 2>&1 as part of that word.
			flush()
			operator := string(character)
			if index+1 < len(line) && line[index+1] == character {
				index++
				operator += string(character)
			}
			words = append(words, operator)
		case character == ')' && len(stack) > 0:
			quote = stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			word.WriteByte(character)
			inWord = true
		default:
			word.WriteByte(character)
			inWord = true
		}
	}
	flush()
	if len(stack) > 0 {
		// A substitution is still open, so the logical line has not ended and
		// the quote around it is not the caller's to skip.
		return words, heredocs, 0, stack
	}
	return words, heredocs, quote, stack
}
