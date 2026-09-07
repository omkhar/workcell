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

// ShellInvocations returns the arguments of each invocation of command in
// script. It joins line continuations and drops comments, inline ones
// included, heredoc bodies, and the rest of a quoted word that runs past the
// end of its line, so that no decoy text counts as a command and one call
// cannot satisfy a two-call rule. A validator that must anchor on the commands
// a script really runs uses this in place of a substring search.
func ShellInvocations(script, command string) [][]string {
	prefix := strings.Fields(command)
	var invocations [][]string
	var current strings.Builder
	var heredocs []heredoc
	var openQuote byte
	var quotes []byte
	for line := range strings.Lines(script) {
		text := strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if openQuote != 0 {
			// Bash reads these lines as text inside one word, as in
			// : "<newline>oras cp …<newline>", which runs no command. Read
			// them as text too, up to the line that closes the quote.
			if strings.IndexByte(text, openQuote) >= 0 {
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
		if len(words) >= len(prefix) && slices.Equal(words[:len(prefix)], prefix) {
			invocations = append(invocations, words[len(prefix):])
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
