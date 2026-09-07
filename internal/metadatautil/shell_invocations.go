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

// command is one command of a logical line, and whether an earlier command on
// that line decides if it runs. Bash runs the right side of && or || only on
// the exit status of the left, so a command written there is not proved to run.
type command struct {
	args        []string
	conditional bool
}

// splitCommands cuts one logical line into the separate commands bash runs,
// at every control operator. Without this, a decoy after ; or || donates its
// words to the invocation before it, as in
// oras cp --from-oci-layout missing || true; : --to-oci-layout <target>.
func splitCommands(words []string) []command {
	commands := make([]command, 1)
	for _, word := range words {
		if isOperator(word) {
			commands = append(commands, command{conditional: word == "&&" || word == "||"})
			continue
		}
		last := &commands[len(commands)-1]
		last.args = append(last.args, word)
	}
	return commands
}

// definedName returns the name a function definition binds, in either the
// name() or the function name spelling, or the empty string when the words do
// not define one.
func definedName(words []string) string {
	if len(words) == 0 {
		return ""
	}
	if words[0] == "function" {
		if len(words) < 2 {
			return ""
		}
		return strings.TrimSuffix(words[1], "()")
	}
	if name, _, found := strings.Cut(words[0], "("); found && name != "" &&
		strings.HasPrefix(words[0][len(name):], "()") {
		return name
	}
	if len(words) > 1 && words[1] == "()" {
		return words[0]
	}
	return ""
}

// quoteCloseIndex returns the index of the byte that closes an open quote, or
// -1 when the line does not close it. A backslash escapes the next byte inside
// a double-quoted span, so an escaped quote is a literal character and not the
// closer. A single-quoted span has no escapes.
func quoteCloseIndex(line string, quote byte) int {
	for index := 0; index < len(line); index++ {
		if quote == '"' && line[index] == '\\' {
			index++
			continue
		}
		if line[index] == quote {
			return index
		}
	}
	return -1
}

// ShellInvocations returns the arguments of each invocation of command in
// script. It joins line continuations, splits each line at the operators that
// end one command, and drops comments, inline ones included, heredoc bodies,
// the body of a function definition, the body of a compound command, and the
// rest of a quoted word that runs past the end of its line, so that no decoy
// text counts as a command and one call cannot satisfy a two-call rule. A
// validator that must anchor on the commands a script really runs uses this in
// place of a substring search.
func ShellInvocations(script, commandName string) [][]string {
	prefix := strings.Fields(commandName)
	if len(prefix) == 0 {
		return nil
	}
	var invocations [][]string
	var current strings.Builder
	var heredocs []heredoc
	var openQuote byte
	var quotes []byte
	var depth, definedAt, control int
	var defining, bodyOpened bool
	for line := range strings.Lines(script) {
		text := strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if openQuote != 0 {
			// Bash reads these lines as text inside one word, as in
			// : "<newline>oras cp …<newline>", which runs no command. Read
			// them as text too, up to the byte that closes the quote. What
			// follows on that same line is syntax again, so read it: a closer
			// such as " <<PLAN still opens a heredoc.
			at := quoteCloseIndex(text, openQuote)
			if at < 0 {
				continue
			}
			openQuote = 0
			text = text[at+1:]
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
		if strings.HasSuffix(trimmed, "&&") || strings.HasSuffix(trimmed, "|") {
			// A list operator at the end of a line continues the command list
			// onto the next one, so false && <newline> oras cp … is one
			// logical line whose right side bash decides on the left.
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
		if !defining {
			if name := definedName(words); name != "" {
				if name == prefix[0] {
					// The step redefines the anchored command itself, so every
					// later call runs the definition rather than the program.
					// Nothing in this script proves the command ran.
					return nil
				}
				defining, definedAt, bodyOpened = true, depth, false
			}
		}
		depth += braceDepth(words)
		if defining {
			// A body may open on a later line, as in never_called ()
			// followed by { on its own line, so wait for it before looking
			// for its end.
			if depth > definedAt {
				bodyOpened = true
			}
			if bodyOpened && depth <= definedAt {
				defining = false
			}
			continue
		}
		commands := splitCommands(words)
		nested := control > 0
		for _, each := range commands {
			args := each.args
			if len(args) == 0 {
				continue
			}
			if change, found := controlWords[args[0]]; found {
				control = max(control+change, 0)
				nested = true
				continue
			}
			if nested || control > 0 || each.conditional {
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
	arithmetic := 0
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
		case character == '$' && index+2 < len(line) && line[index+1] == '(' && line[index+2] == '(':
			// An arithmetic expansion is not shell syntax: the << inside
			// $((1 << 2)) is a shift, not a heredoc operator.
			arithmetic++
			word.WriteString(line[index : index+3])
			index += 2
			inWord = true
		case arithmetic > 0 && character == ')' && index+1 < len(line) && line[index+1] == ')':
			arithmetic--
			word.WriteString("))")
			index++
			inWord = true
		case arithmetic == 0 && character == '<' && index+1 < len(line) && line[index+1] == '<':
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
