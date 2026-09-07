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
// change it makes to nesting. Bash never runs the body of if false, so a
// command inside one is not proved to run.
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

// splitCommands cuts one logical line into the separate commands bash runs, at
// every control operator. Without this a decoy after ; or || donates its words
// to the invocation before it.
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
// a double-quoted span; a single-quoted span has no escapes.
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

// Invocation is one invocation the script proves it runs: the arguments after
// the command name, and the ordinal of the command word in the stream of
// commands the parser proves the script reaches. Position does not depend on
// the command being searched for, so two invocations parsed from the same
// script compare directly, and a validator that requires one command to run
// before another compares those positions rather than where the two names
// first appear as text.
type Invocation struct {
	Args     []string
	Position int
}

// ShellInvocations returns each invocation of command in script, with the
// arguments it receives. It joins line continuations, splits each line at the
// operators that end one command, and drops comments, inline ones included,
// heredoc bodies, the body of a function definition, the body of a compound
// command, and the rest of a quoted word that runs past the end of its line,
// so that no decoy text counts as a command and one call cannot satisfy a
// two-call rule. A validator that must anchor on the commands a script really
// runs uses this in place of a substring search.
func ShellInvocations(script, commandName string) []Invocation {
	prefix := strings.Fields(commandName)
	if len(prefix) == 0 {
		return nil
	}
	var invocations []Invocation
	var current strings.Builder
	var heredocs []heredoc
	var openQuote byte
	var quotes []byte
	var position int
	var depth, definedAt, control int
	var defining, bodyOpened bool
	for line := range strings.Lines(script) {
		text := strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if openQuote != 0 {
			// Bash reads these lines as text inside one word, which runs no
			// command. What follows the closing byte on that line is syntax
			// again, so read it: a closer such as " <<PLAN opens a heredoc.
			at := quoteCloseIndex(text, openQuote)
			if at < 0 {
				continue
			}
			openQuote = 0
			// The words after the closer still belong to the command the
			// quoted word is an argument of, and that command word was read
			// before the span opened. A null command carries them, so
			// : " … " oras cp … stays one run of : rather than becoming an
			// oras invocation. A control operator in the rest still ends it
			// and starts a command of its own.
			text = ": " + text[at+1:]
		}
		if len(heredocs) > 0 {
			if heredocs[0].endsAt(text) {
				heredocs = heredocs[1:]
			}
			continue
		}
		// A backslash continues the line only when it is not itself escaped,
		// and bash then joins the two halves with nothing between them. A
		// space after the backslash escapes the space instead, which ends the
		// line and makes the next one a separate command.
		if backslashes := len(text) - len(strings.TrimRight(text, `\`)); backslashes%2 == 1 {
			current.WriteString(text[:len(text)-1])
			continue
		}
		current.WriteString(text)
		if trimmed := strings.TrimRight(text, " \t"); strings.HasSuffix(trimmed, "&&") ||
			strings.HasSuffix(trimmed, "|") {
			// A list operator at the end of a line continues onto the next,
			// so false && <newline> oras cp … is one logical line.
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
					// Every later call runs the definition, not the program.
					return nil
				}
				defining, definedAt, bodyOpened = true, depth, false
			}
		}
		depth += braceDepth(words)
		if defining {
			// A body may open on a later line, as in never_called ()
			// followed by { on its own, so wait for it before seeking its end.
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
			position++
			if change, found := controlWords[args[0]]; found {
				control = max(control+change, 0)
				nested = true
				continue
			}
			if nested || control > 0 || each.conditional {
				continue
			}
			if args[0] == "exit" || args[0] == "return" || replacesShell(args) {
				// The step ends here; nothing written after it runs.
				return invocations
			}
			if args[0] == "alias" && shadowsByAlias(args, prefix[0]) {
				return nil // Every later use expands to the alias.
			}
			if args[0] == "hash" && slices.Contains(args[1:], "-p") &&
				slices.Contains(args[1:], prefix[0]) {
				// hash -p pathname name makes pathname the full filename for
				// name, so every later line runs that path, not the program.
				return nil
			}
			if len(args) >= len(prefix) && slices.Equal(args[:len(prefix)], prefix) {
				invocations = append(invocations, Invocation{args[len(prefix):], position})
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
	// A line that carries a quoted command substitution donates no words. Where
	// the substitution ends is beyond a line reader, and reading syntax over
	// text invents words: bash keeps the rest of the quoted argument in one
	// word, while this reader can split it on an escaped space and hand the
	// validator an option and value that no command received. The line can
	// lose an invocation, never invent one. A stack that is already open says
	// the line is the tail of such a substitution, so it donates none either.
	substituted := len(stack) > 0
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
				// is on a later line. The line's words are dropped, so a
				// separator this reader finds inside the quoted argument
				// cannot become an option the command never received.
				substituted = true
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
			if substituted {
				return nil, heredocs, 0, stack
			}
			return words, heredocs, 0, stack
		case character == '$' && index+2 < len(line) && line[index+1] == '(' && line[index+2] == '(':
			// The << inside $((1 << 2)) is a shift, not a heredoc operator.
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
			!(strings.IndexByte("&|", character) >= 0 && index > 0 &&
				strings.IndexByte("<>", line[index-1]) >= 0):
			// An operator ends the command before it. The guard keeps a
			// redirection whole where its second byte would otherwise read as
			// an operator: the & of 2>&1, and the | of the noclobber override
			// >|, which redirects rather than starting a pipeline.
			flush()
			operator := string(character)
			if index+1 < len(line) && line[index+1] == character {
				index++
				operator += string(character)
			}
			words = append(words, operator)
		case len(stack) > 0 && (character == ')' || character == '`'):
			// A substitution ends at ) or at its backtick, and the quote it
			// suspended comes back with it.
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
	if substituted {
		words = nil
	}
	if len(stack) > 0 {
		// A substitution is still open, so the logical line has not ended and
		// the quote around it is not the caller's to skip.
		return words, heredocs, 0, stack
	}
	return words, heredocs, quote, stack
}

// replacesShell reports whether the words are an exec that names a program,
// which replaces the shell so that nothing written after it runs. An exec
// carrying only redirections, as in exec 2>&1, changes the shell's own
// descriptors and the script continues, so it must not stop the scan.
func replacesShell(args []string) bool {
	if args[0] != "exec" {
		return false
	}
	return slices.ContainsFunc(args[1:], func(word string) bool {
		operand := strings.TrimLeft(word, "0123456789")
		return !strings.HasPrefix(operand, ">") && !strings.HasPrefix(operand, "<")
	})
}

// shadowsByAlias reports whether an alias command rebinds name, as in
// alias oras=':' after shopt -s expand_aliases.
func shadowsByAlias(args []string, name string) bool {
	for _, word := range args[1:] {
		if bound, _, found := strings.Cut(word, "="); found && bound == name {
			return true
		}
	}
	return false
}
