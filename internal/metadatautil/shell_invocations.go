// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"cmp"
	"regexp"
	"strings"
)

// heredocPattern consumes quoted words before it reads a redirection, so that
// a << inside an argument such as "text <<B" cannot open a delimiter. Only the
// third alternative captures, and it captures the delimiter it opens.
var heredocPattern = regexp.MustCompile(`'[^']*'|"(?:[^"\\]|\\.)*"|<<-?\s*(?:'([^']*)'|"([^"]*)"|([A-Za-z_][A-Za-z0-9_]*))`)

// inlineComment consumes quoted words before it reads a comment, so that a #
// inside an argument such as 'note: # here' cannot truncate the line. As in
// bash, only an unquoted # at the start of a word opens a comment.
var inlineComment = regexp.MustCompile(`'[^']*'|"(?:[^"\\]|\\.)*"|(?:^|\s)#.*$`)

// ShellInvocations returns the arguments of each invocation of command in
// script. It joins line continuations and drops comments, inline ones
// included, and heredoc bodies, so that no decoy text counts as a command and
// one call cannot satisfy a two-call rule. A validator that must anchor on the
// commands a script really runs uses this in place of a substring search.
func ShellInvocations(script, command string) [][]string {
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
		if current.Len() > 0 {
			current.WriteString(" ")
		}
		current.WriteString(strings.TrimSpace(strings.TrimSuffix(trimmed, "\\")))
		if strings.HasSuffix(trimmed, "\\") {
			continue
		}
		logical := strings.TrimSpace(stripComment(current.String()))
		current.Reset()
		// Here-strings are blanked first so that a redirection such as
		// <<<"${value}" is not read as a heredoc opening the delimiter ${value}.
		// Every delimiter on the line opens a body, and bash reads them in the
		// order they appear, so they are queued rather than overwritten. A match
		// with no capture is a consumed quoted word, not a redirection.
		for _, match := range heredocPattern.FindAllStringSubmatch(strings.ReplaceAll(logical, "<<<", " "), -1) {
			if delimiter := cmp.Or(match[1], match[2], match[3]); delimiter != "" {
				heredocs = append(heredocs, delimiter)
			}
		}
		if rest, found := strings.CutPrefix(logical, command); found && (rest == "" || rest[0] == ' ' || rest[0] == '\t') {
			invocations = append(invocations, strings.Fields(rest))
		}
	}
	return invocations
}

// stripComment removes an unquoted comment from one logical line. The pattern
// keeps the quoted words it consumed and deletes only the comment.
func stripComment(logical string) string {
	return inlineComment.ReplaceAllStringFunc(logical, func(match string) string {
		if strings.HasPrefix(match, "'") || strings.HasPrefix(match, `"`) {
			return match
		}
		return ""
	})
}
