// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"slices"
	"strings"
)

// ShellInvocations returns the arguments of each invocation of command in
// script. It joins line continuations and drops comments, inline ones
// included, and heredoc bodies, so that no decoy text counts as a command and
// one call cannot satisfy a two-call rule. A validator that must anchor on the
// commands a script really runs uses this in place of a substring search.
func ShellInvocations(script, command string) [][]string {
	prefix := strings.Fields(command)
	var invocations [][]string
	var current strings.Builder
	var heredocs []string
	for line := range strings.Lines(script) {
		trimmed := strings.TrimSpace(line)
		if len(heredocs) > 0 {
			if trimmed == heredocs[0] {
				heredocs = heredocs[1:]
			}
			continue
		}
		current.WriteString(strings.TrimSpace(strings.TrimSuffix(trimmed, "\\")))
		if strings.HasSuffix(trimmed, "\\") {
			current.WriteString(" ")
			continue
		}
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
func shellWords(line string) (words, heredocs []string) {
	var word strings.Builder
	inWord, quote, pending := false, byte(0), false
	flush := func() {
		if !inWord {
			return
		}
		if pending {
			heredocs = append(heredocs, word.String())
			pending = false
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
			if character == '\\' && index+1 < len(line) {
				index++
				word.WriteByte(line[index])
			} else if character == '"' {
				quote = 0
			} else {
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
			}
			pending = true
		default:
			word.WriteByte(character)
			inWord = true
		}
	}
	flush()
	return words, heredocs
}
