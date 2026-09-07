// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"slices"
	"strings"
)

// heredoc is one body a script line opened. The dash form matters after the
// opening line, because it alone lets the terminator carry leading tabs.
type heredoc struct {
	delimiter string
	dashForm  bool
}

// ShellInvocations returns the arguments of each invocation of command in
// script. It joins line continuations and drops comments, inline ones
// included, and heredoc bodies, so that no decoy text counts as a command and
// one call cannot satisfy a two-call rule. A validator that must anchor on the
// commands a script really runs uses this in place of a substring search.
func ShellInvocations(script, command string) [][]string {
	prefix := strings.Fields(command)
	var invocations [][]string
	var current strings.Builder
	var heredocs []heredoc
	for line := range strings.Lines(script) {
		body := strings.TrimSuffix(line, "\n")
		if len(heredocs) > 0 {
			// Bash compares the whole line against the delimiter, and only the
			// dash form removes leading tabs first. A terminator that carries
			// any other text, one space included, leaves the body open and
			// keeps swallowing the commands below it.
			terminator := body
			if heredocs[0].dashForm {
				terminator = strings.TrimLeft(terminator, "\t")
			}
			if terminator == heredocs[0].delimiter {
				heredocs = heredocs[1:]
			}
			continue
		}
		// A backslash continues the line only when it is not itself escaped,
		// and bash then joins the two halves with nothing between them. A
		// space after the backslash escapes the space instead, which ends the
		// line and makes the next one a separate command.
		if backslashes := len(body) - len(strings.TrimRight(body, `\`)); backslashes%2 == 1 {
			current.WriteString(body[:len(body)-1])
			continue
		}
		current.WriteString(body)
		words, opened := shellWords(current.String())
		current.Reset()
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
// word ends the line; and every heredoc the line opens returns as a delimiter,
// in the order bash reads the bodies.
//
// One pass keeps the three answers consistent. A pattern per answer cannot:
// each has to rediscover the quoting, and the one that gets it wrong reads
// syntax where the shell reads text.
func shellWords(line string) (words []string, heredocs []heredoc) {
	var word strings.Builder
	inWord, quote, pending, dashForm := false, byte(0), false, false
	flush := func() {
		if !inWord {
			return
		}
		if pending {
			heredocs = append(heredocs, heredoc{delimiter: word.String(), dashForm: dashForm})
			pending, dashForm = false, false
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
				// quotes. Where it closes is beyond a line reader, so syntax
				// is read to the end of the line. That reads more heredocs and
				// comments than bash, never fewer, so it can only drop an
				// invocation, never invent one.
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
			return words, heredocs
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
				dashForm = true
			}
			pending = true
		case pending && strings.IndexByte(";&|<>()", character) >= 0:
			// A delimiter word ends at an operator, as in cat <<EOF; echo
			// ready, where bash reads the delimiter EOF and runs the echo.
			flush()
		default:
			word.WriteByte(character)
			inWord = true
		}
	}
	flush()
	return words, heredocs
}
