// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"encoding/hex"
	"regexp"
	"slices"
	"strings"
)

// heredoc is one body the shell has still to read: the delimiter word that
// ends it, and whether the <<- form lets the terminator line carry leading
// tabs. Bash ends a <<WORD body only at a line that is the delimiter alone, so
// an indented copy of the word inside the body is text.
//
// unresolved records a delimiter this reader cannot spell. A $'…' delimiter
// carries the ANSI-C escapes bash decodes before it compares a line, so
// <<$'\x50LAN' ends at PLAN, and a $"…" delimiter is translated through the
// locale catalog first, so its text here is not the text bash compares at all.
// Resolving either would put a second copy of bash's own tables in this file,
// and a case one of them got wrong would end the body early and count lines
// bash still reads as data. The body therefore runs to the end of the script,
// which loses invocations rather than inventing them.
//
// quoted records a delimiter with any quoting, which keeps bash from expanding
// the body, and start and end are the bytes of the operator and delimiter in
// the line, so a reader can put the body back where the command reads it.
type heredoc struct {
	delimiter  string
	stripTabs  bool
	unresolved bool
	quoted     bool
	start, end int
}

func (h heredoc) endsAt(line string) bool {
	if h.unresolved {
		return false
	}
	if h.stripTabs {
		return strings.TrimLeft(line, "\t") == h.delimiter
	}
	return line == h.delimiter
}

// word is one word of a logical line, and whether any part of it was quoted.
// Quoting takes a word's meaning as syntax away without changing its meaning as
// a name: bash runs "oras" cp, while a quoted ; is an argument, a quoted fi is
// an ordinary command, and a quoted } closes no group. A reader that returns
// plain strings cannot tell the two apart, so every caller that asks whether a
// word is syntax reads text as syntax.
type word struct {
	text   string
	quoted bool
}

// texts returns the words' text, for the tests that read a word as a name. A
// name keeps its meaning through quotes, so those tests ignore provenance.
func texts(words []word) []string {
	plain := make([]string, len(words))
	for index, each := range words {
		plain[index] = each.text
	}
	return plain
}

// braceDepth returns the change in group nesting the commands make.
func braceDepth(commands []command) int {
	change := 0
	for _, each := range commands {
		change += commandBrace(each)
	}
	return change
}

// commandBrace returns the change in group nesting one command makes. A brace
// groups commands only in command position and only unquoted: bash reads the }
// of echo } as an argument, so a group is still open after it. A brace inside a
// word belongs to an expansion such as ${VAR}. A definition header is not a
// command, so the brace that opens its body stands in command position after
// it, wherever on the header line it is written.
//
// A parenthesis in command position opens a subshell, which bash skips exactly
// as it skips a brace group, so it counts the same. Only a bare ( or ) counts:
// a case pattern ends in a word such as -n), an arithmetic command opens with
// ((, and a definition header is handled above, so none of them reaches here.
// isCommandPrefixWord reports whether the word stands before the command rather
// than being one. bash has exactly three reserved words in that position -- !,
// time and coproc -- and accepts `time -p` and `time --` as well as a bare
// time. coproc may also carry a name before the command it runs, which is an
// ordinary word and is stepped over with it.
func isCommandPrefixWord(text string) bool {
	switch text {
	case "!", "time", "coproc", "-p", "--":
		return true
	}
	return false
}

// shellProgram returns the program a sh, bash, dash, ksh or zsh command runs as
// a script, and whether it runs one. The program is the -c body, or, with -s or
// with no script file operand, stdin: the here-string or heredoc text the
// command reads, or $_ for a stream the reader cannot see, as in cat x | bash.
// A program the reader cannot spell is returned as is, so a lint can fail
// closed on it.
func shellProgram(words []string, stdin string) (string, bool) {
	if len(words) == 0 {
		return "", false
	}
	switch commandName(words[0]) {
	case "sh", "bash", "dash", "ksh", "zsh":
	default:
		return "", false
	}
	// Options come first, and -- may end them. c or s anywhere in a short
	// cluster, as in -lc or -es, counts. Each o or O in a cluster, and
	// --rcfile or --init-file, takes the next word as its value, as in
	// -O extglob or -euo pipefail.
	body, fromStdin, noExec := false, false, false
	i := 1
	for ; i < len(words); i++ {
		option := words[i]
		if option == "--" {
			i++
			break
		}
		if option == "--rcfile" || option == "--init-file" {
			i++
			continue
		}
		if !strings.HasPrefix(option, "-") && !strings.HasPrefix(option, "+") {
			break // the first operand
		}
		if !strings.HasPrefix(option, "--") {
			body = body || strings.Contains(option[1:], "c")
			fromStdin = fromStdin || strings.Contains(option[1:], "s")
			if strings.Contains(option[1:], "n") {
				noExec = option[0] == '-' // -n reads the program and runs nothing; +n runs it
			}
			i += strings.Count(option[1:], "o") + strings.Count(option[1:], "O")
		}
	}
	switch {
	case noExec:
		return "", false
	case body && i < len(words):
		return words[i], true
	case body:
		return "", false // bash -c with no body runs nothing
	case fromStdin || i >= len(words):
		return stdin, true
	}
	return "", false // the operand names a script file
}

func commandBrace(each command) int {
	args := each.args
	// ! negates the status of the command after it and time reports how long it
	// takes; neither is a command of its own, so a brace or a parenthesis behind
	// one still stands in command position.
	coproc := false
	for len(args) > 0 && !args[0].quoted &&
		(isCommandPrefixWord(args[0].text) || (coproc && !carriesParen(args[0]))) {
		coproc = coproc || args[0].text == "coproc"
		args = args[1:]
	}
	if len(args) == 0 {
		return 0
	}
	// Quoting takes a word's meaning as syntax away, so a quoted } closes no
	// group and a quoted fi ends no compound command. A word that begins with (
	// is the exception this test cannot make: quote removal has already run, so
	// (echo")" arrives as the balanced-looking (echo) with only a whole-word
	// quoted flag to show for it, while bash opened a subshell on the unquoted
	// ( it began with. Such a word is therefore still read as an opener.
	if args[0].quoted && !strings.HasPrefix(args[0].text, "(") {
		return 0
	}
	if definedName(args) != "" {
		if strings.HasSuffix(args[0].text, "(){") || slices.ContainsFunc(args[1:],
			func(each word) bool {
				return !each.quoted && (each.text == "{" || each.text == "(")
			}) {
			return 1
		}
		return 0
	}
	switch {
	case args[0].text == "{":
		return 1
	case args[0].text == "}":
		return -1
	case args[0].text == ")":
		return -1
	}
	if carriesParen(args[0]) {
		if args[0].quoted {
			// Quote removal has already run, so the parentheses left in the
			// text no longer say which of them were syntax: false && (echo")"
			// reads as a balanced (echo) while bash opens a subshell on the
			// unquoted (. A command word that begins with ( is a subshell
			// opener in every spelling bash accepts, so it opens one here and
			// the count is not trusted. That loses invocations rather than
			// inventing them.
			return 1
		}
		return parenBalance(args)
	}
	return 0
}

// carriesParen reports whether the word puts a parenthesis where it can open a
// subshell: a bare (, one attached to the command after it, or one the shell is
// still reading at the end of a word (x=( , foo=$(). (( is arithmetic, which
// ends at )) rather than at a bare ), so it opens nothing here.
func carriesParen(first word) bool {
	if strings.HasPrefix(first.text, "((") {
		return false
	}
	if strings.HasPrefix(first.text, "(") {
		return true
	}
	return !first.quoted && strings.HasSuffix(first.text, "(")
}

// parenBalance sums the parentheses of one command that stand as syntax.
// Counting rather than testing the last word for a trailing ) is what keeps a
// substitution from closing the group it did not open: `(echo $(date)` ends in
// ) and still leaves a subshell open, because the ( of $( is on the same word.
// An expansion is removed first, so the ) of `${x%)}` closes nothing.
func parenBalance(args []word) int {
	balance := 0
	for _, each := range args {
		if each.quoted {
			continue
		}
		text, unclosed := withoutExpansions(each.text)
		// An expansion the word does not close is a substitution the shell is
		// still reading, as in the foo=$( of a multi-line assignment. Its ) is
		// a bare word on a later line, so the opener has to count.
		balance += unclosed + strings.Count(text, "(") - strings.Count(text, ")")
	}
	return balance
}

// withoutExpansions removes every $(…) and ${…} span from a word, including
// nested ones, and returns how many of them the word leaves open. A parenthesis
// or a brace inside an expansion is part of the expansion's own syntax -- the )
// of ${x%)} is a pattern, and the ) of $(date) closes the substitution -- so
// neither can open or close a command group. A span the word does not close is
// one the shell is still reading on the next line, which does.
func withoutExpansions(text string) (string, int) {
	var kept strings.Builder
	var closers []byte
	for index := 0; index < len(text); index++ {
		if text[index] == '$' && index+1 < len(text) &&
			(text[index+1] == '(' || text[index+1] == '{') {
			closers = append(closers, expansionCloser(text[index+1]))
			index++
			continue
		}
		if len(closers) > 0 {
			// Only the delimiter this expansion opened with closes it: the two
			// ) of ${x%))} are the pattern, not the end of the expansion and
			// then a subshell closer.
			switch {
			case text[index] == '(' || text[index] == '{':
				closers = append(closers, expansionCloser(text[index]))
			case text[index] == closers[len(closers)-1]:
				closers = closers[:len(closers)-1]
			}
			continue
		}
		kept.WriteByte(text[index])
	}
	return kept.String(), len(closers)
}

// expansionCloser returns the delimiter that ends a span the opener started.
func expansionCloser(opener byte) byte {
	if opener == '{' {
		return '}'
	}
	return ')'
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

// continuesLine reports whether an operator at the end of a line carries its
// command onto the next one, so that false && <newline> oras cp … is one
// logical line. A ; or an & ends the command instead.
func continuesLine(word string) bool {
	switch word {
	case "&&", "||", "|":
		return true
	}
	return false
}

// command is one command of a logical line, and whether an earlier command on
// that line decides if it runs. Bash runs the right side of && or || only on
// the exit status of the left, so a command written there is not proved to run.
type command struct {
	args        []word
	conditional bool
}

// splitCommands cuts one logical line into the separate commands bash runs, at
// every unquoted control operator. Without this a decoy after ; or || donates
// its words to the invocation before it, and without the quote test a quoted
// separator such as : ";" oras cp … starts a command bash never runs.
func splitCommands(words []word) []command {
	commands := make([]command, 1)
	for _, each := range words {
		if !each.quoted && isOperator(each.text) {
			commands = append(commands, command{conditional: each.text == "&&" || each.text == "||"})
			continue
		}
		last := &commands[len(commands)-1]
		last.args = append(last.args, each)
	}
	return commands
}

// definedName returns the name a function definition binds, in either the
// name() or the function name spelling, or the empty string when the words do
// not define one. A quoted first word defines nothing: bash reads "never_called()"
// as a command name, so the lines after it are commands rather than a body.
func definedName(words []word) string {
	if len(words) == 0 || words[0].quoted {
		return ""
	}
	first := words[0].text
	if first == "function" {
		if len(words) < 2 {
			return ""
		}
		return strings.TrimSuffix(words[1].text, "()")
	}
	if name, _, found := strings.Cut(first, "("); found && name != "" &&
		strings.HasPrefix(first[len(name):], "()") {
		return name
	}
	if len(words) > 1 && !words[1].quoted && words[1].text == "()" {
		return first
	}
	return ""
}

// ansiCQuote names an open $'…' span in the one byte the reader carries between
// physical lines. A shell quote is only ' or ", so $ is free to stand for the
// third case: an apostrophe closes the span, and a backslash escapes the byte
// after it, which a plain single-quoted span does not.
const ansiCQuote = '$'

// quoteCloseIndex returns the index of the byte that closes an open quote, or
// -1 when the line does not close it. A backslash escapes the next byte inside
// a double-quoted or an ANSI-C span; a plain single-quoted span has no escapes.
func quoteCloseIndex(line string, quote byte) int {
	closer := quote
	if quote == ansiCQuote {
		closer = '\''
	}
	for index := 0; index < len(line); index++ {
		if quote != '\'' && line[index] == '\\' {
			index++
			continue
		}
		if line[index] == closer {
			return index
		}
	}
	return -1
}

// heredocsAsHereStrings moves every heredoc body in script into the line that
// opens it, for a reader that must keep the commands ShellInvocations drops as
// unproved. Each operator and delimiter becomes the here-string heredocWord
// spells, so the command that reads the body, such as bash, still carries it.
func heredocsAsHereStrings(script string) string {
	var out, body strings.Builder
	var opened, pending []heredoc
	var bodies []string
	var held string // the line that opened the pending bodies
	var openQuote byte
	var stack []byte
	release := func() {
		for i, each := range slices.Backward(opened) {
			text := "<<<$_" // a body that never ends is not one the reader can spell
			if i < len(bodies) {
				text = bodies[i]
			}
			held = held[:each.start] + text + held[each.end:]
		}
		out.WriteString(held)
		opened, bodies = nil, nil
	}
	for line := range strings.Lines(script) {
		text := strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if len(pending) > 0 {
			switch each := pending[0]; {
			case !each.endsAt(text) && each.stripTabs:
				body.WriteString(strings.TrimLeft(line, "\t"))
			case !each.endsAt(text):
				body.WriteString(line)
			default:
				// bash expands an unquoted delimiter's body, so one with an
				// expansion or an escape cannot be spelled.
				if text := body.String(); !each.quoted && strings.ContainsAny(text, "$`\\") {
					bodies = append(bodies, "<<<$_")
				} else {
					bodies = append(bodies, heredocWord(text))
				}
				body.Reset()
				if pending = pending[1:]; len(pending) == 0 {
					release()
				}
			}
			continue
		}
		offset := 0
		if openQuote != 0 {
			at := quoteCloseIndex(text, openQuote)
			if at < 0 {
				out.WriteString(line)
				continue
			}
			text, openQuote, offset = text[at+1:], 0, at+1
		}
		_, opened, openQuote, stack, _ = shellWords(text, stack)
		if len(opened) == 0 {
			out.WriteString(line)
			continue
		}
		for i := range opened {
			opened[i].start += offset
			opened[i].end += offset
		}
		pending, held = opened, line
	}
	if len(pending) > 0 {
		release()
	}
	return out.String()
}

// heredocWord is the here-string that stands for a literal heredoc body. Hex
// keeps the body one word through the line readers, and a \x01 byte, which no
// workflow holds, marks it for spelledProgram.
func heredocWord(body string) string {
	return "<<<\x01" + hex.EncodeToString([]byte(body))
}

// spelledProgram returns the script in a program shellProgram returns, and
// whether the reader can spell it. A heredoc body heredocWord spelled is
// literal; any other program with an expansion is not.
func spelledProgram(program string) (string, bool) {
	if encoded, ok := strings.CutPrefix(program, "\x01"); ok {
		body, err := hex.DecodeString(encoded)
		return string(body), err == nil
	}
	return program, !strings.ContainsAny(program, "$`")
}

// flattenSubstitutions moves the text of each $( ... ) and ` ... ` in a run body
// onto its own lines, before the command that holds it, and leaves $_ in its
// place. The commands inside are then read as top-level commands, and a
// command word that held one keeps an expansion, so a reader cannot take it
// for a command it can spell. It tracks quotes, so a ) or $( inside a quoted jq
// program is left alone.
func flattenSubstitutions(script string) string {
	var out strings.Builder
	// One frame per open substitution: its text since the last command
	// boundary, whether that text is inside double quotes, the byte that
	// closes it, and how many unquoted parentheses inside it are open, so the
	// ) of <(…) or of a subshell does not close a $( ).
	type frame struct {
		text   strings.Builder
		quoted bool
		closer byte
		parens int
	}
	frames := []*frame{{}}
	single := false
	for i := 0; i < len(script); i++ {
		c := script[i]
		top := frames[len(frames)-1]
		switch {
		case single:
			single = c != '\''
			top.text.WriteByte(markQuotedNewline(c))
		case c == '\\' && i+1 < len(script):
			top.text.WriteString(script[i : i+2])
			i++
		case c == '#' && !top.quoted && (i == 0 || strings.ContainsRune(" \t\n;(", rune(script[i-1]))):
			for i < len(script) && script[i] != '\n' {
				i++
			}
			c = '\n' // the comment ends the command
			top.text.WriteByte(c)
		case c == '$' && !top.quoted && strings.HasPrefix(script[i:], "$'"):
			// bash decodes the escapes in $'…' and this reader does not, so a
			// span with one becomes $_, which no reader spells.
			end := i + 2
			for end < len(script) && script[end] != '\'' {
				if script[end] == '\\' {
					end++
				}
				end++
			}
			if strings.Contains(script[i:min(end, len(script))], `\`) {
				top.text.WriteString("$_")
				i = end
			} else {
				top.text.WriteByte(c)
			}
		case c == '\'' && !top.quoted:
			single = true
			top.text.WriteByte(c)
		case c == '"':
			top.quoted = !top.quoted
			top.text.WriteByte(c)
		case c == '`' && top.closer != '`' || strings.HasPrefix(script[i:], "$(") && !strings.HasPrefix(script[i:], "$(("):
			top.text.WriteString("$_")
			frames = append(frames, &frame{closer: ')'})
			if c == '`' {
				frames[len(frames)-1].closer = c
			} else {
				i++
			}
		case top.closer == ')' && !top.quoted && c == '(':
			top.parens++
			top.text.WriteByte(c)
		case top.closer == ')' && !top.quoted && c == ')' && top.parens > 0:
			top.parens--
			top.text.WriteByte(c)
		case c == top.closer && !top.quoted:
			frames = frames[:len(frames)-1]
			out.WriteString("\n" + top.text.String() + "\n")
		case top.quoted:
			top.text.WriteByte(markQuotedNewline(c))
		default:
			top.text.WriteByte(c)
		}
		// A substitution moves to the last newline, ; or && or || before it.
		if text := frames[0].text.String(); len(frames) == 1 && !frames[0].quoted && !single &&
			(c == '\n' || c == ';' && !strings.HasSuffix(text, ";;") && !strings.HasPrefix(script[i+1:], ";") ||
				strings.HasSuffix(text, "&&") || strings.HasSuffix(text, "||")) {
			out.WriteString(text)
			frames[0].text.Reset()
		}
	}
	for _, open := range slices.Backward(frames) {
		out.WriteString("\n" + open.text.String())
	}
	return out.String()
}

// markQuotedNewline turns a newline inside a quoted word into quotedNewline,
// so a multi-line word stays on one line for the line reader. commandWords
// turns it back, so a shell program keeps its lines.
func markQuotedNewline(c byte) byte {
	if c == '\n' {
		return quotedNewline
	}
	return c
}

// quotedNewline is a byte no workflow holds.
const quotedNewline = '\x02'

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
	// conditionalGroup is the brace depth outside a command group that a && or
	// a || guards, or -1 when no such group is open, and groupDepth is the
	// nesting the commands read so far have opened. Bash decides the whole
	// group on one exit status, so nothing written inside false && { … } is
	// proved to run.
	conditionalGroup, groupDepth := -1, 0
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
		current.WriteString(text)
		words, opened, quote, rest, continues := shellWords(current.String(), quotes)
		if continues {
			// The line ends with a backslash the quoting leaves as syntax, and
			// bash joins the two halves with nothing between them. Counting
			// backslashes in the text before any quote state exists reads the
			// literal one of 'or\ <newline> as' as a continuation and joins a
			// command word bash keeps apart, in the fail-open direction.
			joined := current.String()
			current.Reset()
			current.WriteString(joined[:len(joined)-1])
			continue
		}
		if last := len(words) - 1; last >= 0 && !words[last].quoted &&
			continuesLine(words[last].text) {
			current.WriteString(" ")
			continue
		}
		current.Reset()
		openQuote, quotes = quote, rest
		heredocs = append(heredocs, opened...)
		// A function definition is not a call. Bash reads the body and runs
		// nothing, so a required command written inside a function that
		// nobody calls does not satisfy a rule about what the step runs.
		commands := splitCommands(words)
		if !defining {
			if name := definedName(words); name != "" {
				if name == prefix[0] {
					// Every later call runs the definition, not the program.
					return nil
				}
				defining, definedAt, bodyOpened = true, depth, false
			}
		}
		depth += braceDepth(commands)
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
		nested := control > 0
		for _, each := range commands {
			args := each.args
			if len(args) == 0 {
				continue
			}
			position++
			outside := groupDepth
			groupDepth += commandBrace(each)
			if conditionalGroup >= 0 {
				// Nothing inside the guarded group is proved to run, however
				// many lines later the closing brace is. The commands written
				// after that brace on its own line are outside the group and
				// are read.
				if groupDepth <= conditionalGroup {
					conditionalGroup = -1
				}
				continue
			}
			if each.conditional && groupDepth > outside {
				// The group this command opens is the one the && or the || in
				// front of it guards. A conditional command elsewhere on the
				// line guards no group of its own, so false && true; { runs
				// its group whatever the status of true.
				conditionalGroup = outside
			}
			if !args[0].quoted {
				// A quoted reserved word is an ordinary command, so a "fi"
				// inside if false; then closes no branch and the lines after
				// it stay inside the branch bash never runs.
				if change, found := controlWords[args[0].text]; found {
					control = max(control+change, 0)
					nested = true
					continue
				}
			}
			if nested || control > 0 || each.conditional {
				continue
			}
			names := texts(args)
			if names[0] == "exit" || names[0] == "return" || replacesShell(names) {
				// The step ends here; nothing written after it runs.
				return invocations
			}
			if names[0] == "alias" && shadowsByAlias(names, prefix[0]) {
				return nil // Every later use expands to the alias.
			}
			if names[0] == "hash" && slices.Contains(names[1:], "-p") &&
				slices.Contains(names[1:], prefix[0]) {
				// hash -p pathname name makes pathname the full filename for
				// name, so every later line runs that path, not the program.
				return nil
			}
			if len(names) >= len(prefix) && slices.Equal(names[:len(prefix)], prefix) {
				invocations = append(invocations, Invocation{names[len(prefix):], position})
			}
		}
	}
	return invocations
}

// shellWords splits one logical line the way bash reads it. Quotes and
// backslash escapes are removed but recorded, so a quoted argument stays one
// word, a separator inside it is text rather than syntax, and the caller can
// still tell a word that was quoted from one that was not; an unquoted # that
// starts a word ends the line; every heredoc the line opens returns as a
// delimiter, in the order bash reads the bodies; open is the quote character
// still waiting to be closed, which tells the caller that the word runs on into
// the next line; rest is the quote each unclosed command substitution
// suspended, which the caller gives back on the next line so that the ) which
// closes the substitution also restores the quote around it; and continues says
// the line ended on a backslash that the quoting leaves as syntax, so bash
// reads the next physical line as the rest of this one.
//
// One pass keeps the five answers consistent. A pattern per answer cannot:
// each has to rediscover the quoting, and the one that gets it wrong reads
// syntax where the shell reads text.
func shellWords(line string, stack []byte) (
	words []word, heredocs []heredoc, open byte, rest []byte, continues bool,
) {
	var text strings.Builder
	inWord, quoted, quote, pending, stripTabs := false, false, byte(0), false, false
	ansiC, unresolved := false, false
	arithmetic := 0
	// A line that carries a quoted command substitution donates no words. Where
	// the substitution ends is beyond a line reader, and reading syntax over
	// text invents words: bash keeps the rest of the quoted argument in one
	// word, while this reader can split it on an escaped space and hand the
	// validator an option and value that no command received. The line can
	// lose an invocation, never invent one. A stack that is already open says
	// the line is the tail of such a substitution, so it donates none either.
	substituted := len(stack) > 0
	index, operatorAt := 0, 0
	flush := func() {
		if !inWord {
			return
		}
		if pending {
			heredocs = append(heredocs, heredoc{text.String(), stripTabs, unresolved, quoted, operatorAt, index})
			pending, stripTabs = false, false
		} else {
			words = append(words, word{text.String(), quoted})
		}
		text.Reset()
		inWord, quoted, unresolved = false, false, false
	}
	for ; index < len(line); index++ {
		character := line[index]
		switch {
		case quote == '\'':
			// A backslash is literal inside single quotes, and an escape bash
			// would decode inside $'…' makes the word unspellable here.
			switch {
			case character == '\'':
				quote, ansiC = 0, false
			case character == '\\' && ansiC && index+1 < len(line):
				// A backslash escapes the next byte inside $'…', so an escaped
				// apostrophe is a literal one and does not close the span. The
				// span therefore keeps the rest of the line as text, where
				// closing on it would expose a separator bash never reads.
				unresolved = true
				text.WriteByte(character)
				index++
				text.WriteByte(line[index])
			default:
				text.WriteByte(character)
			}
		case quote == '"':
			switch {
			case character == '\\' && index+1 == len(line):
				// Bash removes a backslash-newline pair inside double quotes,
				// so the line continues here as it does outside them.
				continues = true
			case character == '\\' && index+1 < len(line) && strings.IndexByte("$`\"\\", line[index+1]) >= 0:
				// A backslash escapes only these characters here. Before any
				// other one it is a literal byte of the word, so a name such
				// as "or\as" is not the command it resembles.
				index++
				text.WriteByte(line[index])
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
				text.WriteByte(character)
			default:
				text.WriteByte(character)
			}
		case character == '\\' && index+1 == len(line):
			// The line ends on a backslash no quote made literal, so bash
			// reads the next physical line as the rest of this one. Only the
			// reader that knows the quoting can say so.
			continues = true
		case character == '\\':
			// An escaped character is a literal one, not the start of a quoted
			// span, so it cannot hide the syntax that follows it. It is also
			// no longer syntax itself, so \; is an argument rather than a
			// separator, which is why the escape records provenance.
			index++
			text.WriteByte(line[index])
			inWord, quoted = true, true
		case character == '\'' || character == '"':
			quote = character
			inWord, quoted = true, true
		case character == '$' && index+1 < len(line) &&
			(line[index+1] == '\'' || line[index+1] == '"'):
			// $'…' and $"…" are quoting forms whose $ bash removes with the
			// quotes, so a body opened as <<$'PLAN' ends at a PLAN line. A
			// $"…" word is translated through the locale catalog before that,
			// so what it spells here is not what bash compares, and a
			// delimiter written that way is unresolved from the start.
			index++
			quote = line[index]
			ansiC = quote == '\''
			unresolved = unresolved || !ansiC
			inWord, quoted = true, true
		case character == ' ' || character == '\t':
			flush()
		case character == '#' && !inWord:
			if substituted {
				return nil, heredocs, 0, stack, false
			}
			return words, heredocs, 0, stack, false
		case character == '$' && index+2 < len(line) && line[index+1] == '(' && line[index+2] == '(':
			// The << inside $((1 << 2)) is a shift, not a heredoc operator.
			arithmetic++
			text.WriteString(line[index : index+3])
			index += 2
			inWord = true
		case arithmetic > 0 && character == ')' && index+1 < len(line) && line[index+1] == ')':
			arithmetic--
			text.WriteString("))")
			index++
			inWord = true
		case arithmetic == 0 && character == '<' && index+1 < len(line) && line[index+1] == '<':
			flush()
			operatorAt = index
			index++
			if index+1 < len(line) && line[index+1] == '<' {
				// A here-string takes its text from the line, not from a body.
				// The operator stays in the word, so a reader can tell the text
				// from an argument.
				index++
				text.WriteString("<<<")
				inWord = true
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
				strings.IndexByte("<>", line[index-1]) >= 0) &&
			!(character == '&' && index+1 < len(line) && line[index+1] == '>'):
			// An operator ends the command before it. The guards keep a
			// redirection whole where one of its bytes would otherwise read as
			// an operator: the & of 2>&1 and of &>file, and the | of the
			// noclobber override >|, which redirects rather than starting a
			// pipeline.
			flush()
			operator := string(character)
			if index+1 < len(line) && line[index+1] == character {
				index++
				operator += string(character)
			}
			words = append(words, word{text: operator})
		case len(stack) > 0 && (character == ')' || character == '`'):
			// A substitution ends at ) or at its backtick, and the quote it
			// suspended comes back with it.
			quote = stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			text.WriteByte(character)
			inWord = true
		default:
			text.WriteByte(character)
			inWord = true
		}
	}
	flush()
	if substituted {
		words = nil
	}
	if quote == '\'' && ansiC {
		// The span runs on into the next line with its escapes still live, so
		// the caller must skip it by the ANSI-C rules, not the plain ones.
		quote = ansiCQuote
	}
	if len(stack) > 0 {
		// A substitution is still open, so the logical line has not ended and
		// the quote around it is not the caller's to skip.
		return words, heredocs, 0, stack, continues
	}
	return words, heredocs, quote, stack, continues
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

// commandWrappers maps each command that runs the command after it to its own
// options that take a value. A long option matches any unique abbreviation of
// three or more bytes, as getopt allows, and a short option may end a cluster
// such as -vk with its value attached or in the next word.
var commandWrappers = map[string][]string{
	"builtin": nil, "command": nil, "exec": {"-a"}, "nohup": nil, "nice": {"-n", "--adjustment"},
	"env":     {"-u", "-C", "-P", "-S", "--unset", "--chdir", "--split-string"},
	"timeout": {"-k", "-s", "--kill-after", "--signal"},
	"sudo": {"-u", "-g", "-C", "-D", "-h", "-p", "-r", "-t", "-T", "-U", "-R", "-a", "-c", "--user", "--group",
		"--close-from", "--chdir", "--host", "--prompt", "--role", "--type", "--command-timeout", "--other-user",
		"--chroot", "--auth-type", "--login-class"},
}

// commandName returns the program a command word names: the last path
// element after a / or a \, without a Windows .exe suffix, so /usr/bin/gh,
// gh.exe and "C:\tools\GH.EXE" all name gh. A Windows runner finds a name
// without regard to case, so every name is lowered; on Linux, GH is then
// read as gh, which over-reports and so fails closed.
func commandName(word string) string {
	name := strings.ToLower(word[strings.LastIndexAny(word, `/\`)+1:])
	return strings.TrimSuffix(name, ".exe")
}

// wrappedCommand returns words from the command that a chain of wrappers, such
// as env A=1 nice -n 5 command -p gh, runs. command -v only names a command.
// env -S splits its value into words that env then reads as its own arguments.
// timeout reads a DURATION operand before the command. A wrapper named by
// path, as /usr/bin/env, is the same wrapper. sudo -h with a value names a
// host. sudo -s or -i runs the command through $SHELL -c with its
// metacharacters escaped, so the words stay the command. With no command it
// runs $SHELL, which reads a script the reader cannot see.
func wrappedCommand(words []string) []string {
	for {
		name := commandName(words[0])
		valued, wraps := commandWrappers[name]
		if !wraps {
			return words
		}
		i, shell := 1, false
		for ; i < len(words) && (strings.HasPrefix(words[i], "-") || (name == "env" || name == "sudo") && shellAssignment.MatchString(words[i])); i++ {
			if name == "command" && strings.ContainsAny(words[i], "vV") || name == "sudo" && sudoRunsNothing(words[i]) {
				return words
			}
			shell = shell || name == "sudo" && words[i][0] == '-' && (!strings.HasPrefix(words[i], "--") && strings.ContainsAny(words[i], "si") ||
				len(words[i]) > 3 && (strings.HasPrefix("--shell", words[i]) || strings.HasPrefix("--login", words[i])))
			if words[i] == "--" {
				i++
				break
			}
			option, split, attached := strings.Cut(words[i], "=")
			for at := 1; len(words[i]) > 2 && words[i][0] == '-' && words[i][1] != '-' && at < len(words[i]); at++ {
				if short := "-" + words[i][at:at+1]; slices.Contains(valued, short) {
					option, split, attached = short, words[i][at+1:], at+1 < len(words[i]) // -k5, -iS or -S'gh api'
					break
				}
			}
			if slices.ContainsFunc(valued, func(each string) bool {
				return each == option || len(option) > 2 && strings.HasPrefix(each, "--") && strings.HasPrefix(each, option)
			}) {
				if !attached {
					i++
					split = strings.Join(words[i:min(i+1, len(words))], "")
				}
				if name == "env" && (option == "-S" || strings.HasPrefix("--split-string", option)) {
					parsed, _, _, _, _ := shellWords(split, nil)
					words = append(append([]string{"env"}, texts(parsed)...), words[min(i+1, len(words)):]...)
					i = 0
				}
			}
		}
		if name == "timeout" {
			i++
		}
		if i >= len(words) && shell {
			return []string{"$SHELL"}
		}
		if i >= len(words) {
			return words
		}
		words = words[i:]
	}
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

// EveryShellCommand returns the words of every command in script that bash may
// run, from the command word wrappedCommand finds. It is the
// reachability-insensitive counterpart of ShellInvocations: it keeps if, loop
// and case bodies, guarded commands, subshells and substitutions, which a lint
// of what a script may run must read. It drops comments, redirections, the
// heredoc bodies of commands other than a shell, and the reserved words and
// assignments before a command. A shell's -c body, heredoc or here-string runs
// as a script in place of the shell, unless the reader cannot spell it. A
// function body is read where it is defined. eval runs its words as a script,
// unless they hold an expansion; then the eval stays a command, as does a
// command word with an expansion, since the reader cannot spell what either
// runs. PR 804 adds a ShellCommandWords reader with a similar intent; merge the
// two when both land.
func EveryShellCommand(script string) [][]string {
	return everyShellCommand(script)
}

// everyShellCommand is EveryShellCommand for a script a child shell runs, such
// as a sh -c body.
func everyShellCommand(script string) [][]string {
	return commandWords(flattenSubstitutions(heredocsAsHereStrings(script)))
}

// commandWords returns the words of every command in text that
// flattenSubstitutions has already rewritten, as shellWords reads each logical
// line. An unquoted parenthesis ends a command, as a subshell or a case pattern
// does. A redirection is not a word of the command, and a bare operator such
// as > takes the next word as its target, so that word goes too. A here-string
// is the command's stdin, which a shell can run as its program.
func commandWords(text string) [][]string {
	var found [][]string
	var command []string
	dropTarget, readStdin, stdin := false, false, "$_"
	add := func(text string) {
		switch {
		case readStdin:
			readStdin, stdin = false, text
		case dropTarget:
			dropTarget = false
		default:
			command = append(command, text)
		}
	}
	discard := func() {
		command, dropTarget, readStdin, stdin = nil, false, false, "$_"
	}
	// emit records the command the words run: the program a shell or eval
	// runs, the words themselves, and each command a find -exec runs.
	var emit func(words []string, stdin string)
	emit = func(words []string, stdin string) {
		words = wrappedCommand(words)
		program, shell := shellProgram(words, stdin)
		script, spelled := spelledProgram(program)
		if text := strings.Join(words[1:], " "); words[0] == "eval" && !strings.ContainsAny(text, "$`") {
			found = append(found, commandWords(text)...) // eval runs its words as a script
		} else if shell && spelled {
			// A shell runs its program as a script in a child shell.
			found = append(found, everyShellCommand(script)...)
		} else {
			found = append(found, words)
		}
		if commandName(words[0]) == "find" {
			for _, body := range findExecBodies(words[1:]) {
				emit(body, "$_")
			}
		}
	}
	end := func() {
		i := 0
		for i < len(command) && (slices.Contains(shellKeywords, command[i]) ||
			isCommandPrefixWord(command[i]) || shellAssignment.MatchString(command[i])) {
			if command[i] == "coproc" && i+2 < len(command) && slices.Contains(shellKeywords, command[i+2]) {
				i++ // coproc NAME runs the compound command after the name
			}
			i++
		}
		if i < len(command) {
			emit(command[i:], stdin)
		}
		discard()
	}
	// A case pattern, the words from in or ;; up to ), names no command.
	caseDepth, expectIn, pending := 0, false, false
	addPiece := func(piece string) {
		// An unquoted < or > starts a redirection anywhere in a
		// word. Only an fd number or {name} before it belongs to it.
		at := strings.IndexAny(piece, "<>")
		if at < 0 {
			if piece != "" {
				add(piece)
			}
			return
		}
		if at > 0 && piece[at-1] == '&' {
			at-- // &>file
		}
		if prefix := piece[:at]; prefix != "" && !shellFD.MatchString(prefix) {
			add(prefix)
		}
		if text, ok := strings.CutPrefix(piece[at:], "<<<"); ok && text != "" {
			stdin = text
		} else if ok {
			readStdin = true
		} else {
			dropTarget = shellRedirect.FindString(piece[at:]) == piece[at:]
		}
	}
	joined, test := "", false
	for line := range strings.Lines(text) {
		words, _, _, _, continues := shellWords(joined+strings.TrimSuffix(line, "\n"), nil)
		if continues {
			joined += strings.TrimSuffix(line, "\n")
			joined = joined[:len(joined)-1]
			continue
		}
		joined = ""
		for _, each := range words {
			each.text = strings.ReplaceAll(each.text, string(quotedNewline), "\n")
			switch {
			case test || !each.quoted && each.text == "[[":
				// [[ reads its words up to ]] as one expression: an operator,
				// a parenthesis, a < or > and a line break are part of it.
				test = each.quoted || each.text != "]]"
				add(each.text)
			case each.quoted && strings.HasPrefix(each.text, "<<<"):
				stdin = each.text[3:] // <<<'text' is one word
			case each.quoted:
				add(each.text)
			case each.text == "case":
				caseDepth, expectIn = caseDepth+1, true
				add(each.text)
			case expectIn && each.text == "in":
				expectIn, pending = false, true
				add(each.text)
			case each.text == "esac":
				caseDepth, pending = max(caseDepth-1, 0), false
				add(each.text)
			case isOperator(each.text):
				if pending {
					discard() // a | between patterns
				} else {
					end()
				}
				pending = pending || caseDepth > 0 && each.text != ";" && strings.HasPrefix(each.text, ";")
			default:
				rest := each.text
				for at := strings.IndexAny(rest, "()"); at >= 0; at = strings.IndexAny(rest, "()") {
					addPiece(rest[:at])
					if pending && rest[at] == ')' {
						discard() // the pattern ends
						pending = false
					} else if !pending {
						end()
					}
					rest = rest[at+1:]
				}
				addPiece(rest)
			}
		}
		if !test {
			end()
		}
	}
	end()
	return found
}

// sudoRunsNothing reports a sudo mode that edits, lists or validates and runs
// no command, as sudo -e FILE or sudo -l do.
func sudoRunsNothing(option string) bool {
	return !strings.HasPrefix(option, "--") && strings.ContainsAny(option[1:], "elvVkK") ||
		slices.Contains([]string{"--edit", "--list", "--validate", "--version", "--help", "--remove-timestamp", "--reset-timestamp"}, option)
}

// findExecBodies returns each command that find runs for a match: the words
// after -exec, -execdir, -ok or -okdir up to the ; or + that ends them.
func findExecBodies(args []string) [][]string {
	var bodies [][]string
	for i := 0; i < len(args); i++ {
		if !slices.Contains([]string{"-exec", "-execdir", "-ok", "-okdir"}, args[i]) {
			continue
		}
		end := i + 1
		for end < len(args) && args[end] != ";" && args[end] != "+" {
			end++
		}
		if end > i+1 {
			bodies = append(bodies, args[i+1:end])
		}
		i = end
	}
	return bodies
}

var shellAssignment = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

// shellRedirect matches a redirection word; a bare operator takes the next word.
var shellRedirect = regexp.MustCompile(`^` + shellFDPattern + `?(&>|[<>])[<>&|]*`)

// shellFD matches an fd number or {name} that a redirection operator follows.
var shellFD = regexp.MustCompile(`^` + shellFDPattern + `$`)

const shellFDPattern = `([0-9]+|\{[A-Za-z_][A-Za-z0-9_]*\})`

// shellKeywords are the reserved words before a command that isCommandPrefixWord
// does not cover.
var shellKeywords = []string{"if", "then", "do", "else", "elif", "while", "until", "{"}
