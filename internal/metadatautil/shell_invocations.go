// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"encoding/hex"
	"maps"
	"path"
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
// quoted records a delimiter with any quoting, which keeps bash from expanding the body, and start and end are the bytes of the operator and delimiter in the line, so a reader can put the body back where the command reads it.
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
//
// decoded records a $'…' span in the word. Bash decodes its escapes before it
// runs the word, so $'\x73ource' runs source while its text here does not.
type word struct {
	text    string
	quoted  bool
	decoded bool
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

// spelled returns the words' text with the $ of a $'…' span put back, so the
// barrier test reads a word bash decodes as one it expands.
func spelled(words []word) []string {
	plain := texts(words)
	for index, each := range words {
		if each.decoded {
			plain[index] = "$" + plain[index]
		}
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

// shellProgram returns the program a sh, bash, dash, ksh or zsh command runs as a script, and whether it runs one: the -c body, or stdin with -s or no script operand, which is $_ for a stream the reader cannot see.
func shellProgram(words []string, stdin string) (string, bool) {
	if len(words) == 0 {
		return "", false
	}
	switch commandName(words[0]) {
	case "zsh":
		return "$_", true // zsh reads .zshenv, under any ZDOTDIR, before -c
	case "sh", "bash", "dash", "ksh":
	case "source", ".":
		// source /dev/stdin runs the here-document fed to it in the current
		// shell; any other operand is a file this reader does not see.
		if len(words) > 1 && slices.Contains([]string{"/dev/stdin", "/dev/fd/0", "/proc/self/fd/0"}, words[1]) {
			return stdin, true
		}
		return "", false
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
		if option == "--rcfile" || option == "--init-file" || option == "--login" || strings.HasPrefix(option, "-") && !strings.HasPrefix(option, "--") && strings.ContainsAny(option[1:], "il") {
			return "$_", true // a startup file, rc or profile, runs before the program
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
	case fromStdin || i >= len(words) || slices.Contains([]string{"/dev/stdin", "/dev/fd/0", "/proc/self/fd/0"}, words[i]):
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
// withoutCasePatternAlternatives drops the alternatives of a case pattern
// such as ” | . | /*): the line splits at each | like a pipeline, but the
// single words before the one that ends the pattern with ) are words to match,
// not commands, and . among them is no source. Commands written before the
// pattern on the line, such as source ./x.sh; case …, stay, since each holds
// more than one word or ends that run of single words.
func withoutCasePatternAlternatives(commands []command) []command {
	for at := len(commands) - 1; at > 0; at-- {
		if args := commands[at].args; len(args) > 0 && !args[0].quoted && strings.HasSuffix(args[0].text, ")") &&
			!strings.HasPrefix(args[0].text, "(") && !strings.Contains(args[0].text, "$(") {
			start := at
			for start > 0 && len(commands[start-1].args) == 1 && !commands[start-1].conditional {
				start--
			}
			return append(slices.Clone(commands[:start]), commands[at:]...)
		}
	}
	return commands
}

// gluedSubshellClosers counts the ) that end a logical line and close a ( the
// same line opened, as in (cd x && go run ./y) or (cd x && "$@"): commandBrace
// counts the bare opener but not a closer inside a word, and quote removal
// hides where the quotes were. A ) that ends the line is outside quotes, since
// a quoted string ends with its quote, and unescaped ones count. A case pattern
// such as linux) opens nothing on its line, and a closer on a later line or
// before a comment stays unread, which loses invocations rather than inventing
// them.
func gluedSubshellClosers(commands []command, logical string) int {
	open := 0
	for _, each := range commands {
		if len(each.args) > 0 && carriesParen(each.args[0]) && commandBrace(each) > 0 {
			open++
		}
	}
	trimmed := strings.TrimRight(logical, " \t\n")
	closers := 0
	for closers < open && strings.HasSuffix(trimmed, ")") && !strings.HasSuffix(trimmed, "\\)") {
		trimmed = trimmed[:len(trimmed)-1]
		closers++
	}
	return closers
}

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

// substitutionDepth returns how many unquoted $( spans are still open after a line's words, given the count open before it. A quoted "$( is the stack shellWords returns; an unquoted one leaves no trace there, so a reader that stops at the line end splits x=$( from the command on the next line. Only a $( starts the count, so a bare subshell ( does not join lines, and the count is the balance of the whole line, so $((a + 1)) split into words closes on the line it opens.
func substitutionDepth(open int, words []word) int {
	if open == 0 && !slices.ContainsFunc(words, func(each word) bool {
		_, unclosed := withoutExpansions(each.text)
		return !each.quoted && unclosed > 0
	}) {
		return 0
	}
	return max(open+parenBalance(words), 0)
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
	// Inside a [[ … ]] test, && and || are test operators, not command
	// separators, so the test stays one command and its operands never
	// become command words.
	inTest := 0
	for _, each := range words {
		if !each.quoted {
			switch each.text {
			case "[[":
				inTest++
			case "]]":
				inTest = max(inTest-1, 0)
			}
		}
		if inTest > 0 && !each.quoted && (each.text == "&&" || each.text == "||") {
			last := &commands[len(commands)-1]
			last.args = append(last.args, each)
			continue
		}
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

// definitionEnds reports whether a definition read from nesting definedAt has
// closed, now that the nesting is depth. A body may open on a later line, as in
// never_called () followed by { on its own, so opened records that it has
// opened before its end is sought.
func definitionEnds(depth, definedAt int, opened *bool) bool {
	if depth > definedAt {
		*opened = true
	}
	return *opened && depth <= definedAt
}

// definitionHeaders splits each function definition header from the command
// after its opening brace, as in f() { g() { gh api x, so that command, which
// may define a nested function, is read on its own.
func definitionHeaders(commands []command) []command {
	var out []command
	for _, each := range commands {
		for definedName(each.args) != "" {
			at := slices.IndexFunc(each.args, func(arg word) bool { return !arg.quoted && arg.text == "{" })
			if strings.HasSuffix(each.args[0].text, "(){") {
				at = 0
			}
			if at < 0 || at == len(each.args)-1 {
				break
			}
			out = append(out, command{args: each.args[:at+1]})
			each = command{args: each.args[at+1:]}
		}
		out = append(out, each)
	}
	return out
}

// functionPart is a run of lines outside every definition, with no name, or
// one function definition.
type functionPart struct{ name, text string }

// functionBodies splits text, in order, into the lines outside every function
// definition and the lines of each definition, for a reader that keeps
// compound bodies and so must read a body only when the function is called. A
// later definition replaces an earlier one for the calls after it. A line that
// mixes a definition with a command outside it is split per command, unless it
// continues onto the next line; then it stays outside, as does a definition that
// never closes: both read too much rather than too little. text holds no
// heredoc body and no line that ends inside a quote.
func functionBodies(text string) []functionPart {
	var current strings.Builder
	parts := []functionPart{{}}
	emit := func(name, text string) {
		if last := &parts[len(parts)-1]; name == "" && last.name == "" {
			last.text += text // keep a continued command in one part
		} else {
			parts = append(parts, functionPart{name, text})
		}
	}
	name, depth, definedAt, opened := "", 0, 0, false
	for line := range strings.Lines(text) {
		words, _, _, _, continues, _ := shellWords(strings.TrimSuffix(line, "\n"), nil)
		mixed, closed, defining, header := false, "", name != "", false
		type piece struct { // a command quoted again, and the definition it is in
			owner, text string
			closes      bool
		}
		var split []piece
		for _, each := range definitionHeaders(splitCommands(words)) {
			if len(each.args) == 0 {
				continue
			}
			if name == "" {
				name, definedAt, opened = definedName(each.args), depth, false
				mixed = mixed || name == ""
				if header = name != ""; header {
					depth += commandBrace(each)
					split = append(split, piece{owner: name}) // a body holds no header
					continue
				}
			}
			defining = defining || name != ""
			var text strings.Builder
			for _, arg := range each.args {
				if arg.quoted {
					arg.text = "'" + strings.ReplaceAll(arg.text, "'", `'\''`) + "'"
				}
				text.WriteString(arg.text + " ")
			}
			owner := name
			depth += commandBrace(each)
			closes := name != "" && definitionEnds(depth, definedAt, &opened)
			if closes {
				closed, name = name, ""
			}
			split = append(split, piece{owner, text.String() + "\n", closes})
		}
		if header && !continues {
			line = ""
			for _, each := range split {
				line += each.text
			}
		}
		switch {
		case mixed && defining && !continues:
			for _, each := range split {
				if each.owner == "" {
					emit("", each.text)
					continue
				}
				current.WriteString(each.text)
				if each.closes {
					emit(each.owner, current.String())
					current.Reset()
				}
			}
		case mixed:
			emit("", current.String()+line)
			current.Reset()
		case closed != "" && name == "":
			emit(closed, current.String()+line)
			current.Reset()
		case name != "":
			current.WriteString(line)
		default:
			emit("", line)
		}
	}
	emit("", current.String())
	return parts
}

// ansiCQuote names an open $'…' span in the one byte the reader carries between
// physical lines. A shell quote is only ' or ", so $ is free to stand for the
// third case: an apostrophe closes the span, and a backslash escapes the byte
// after it, which a plain single-quoted span does not.
const ansiCQuote = '$'

// quoteOpener returns the text that opens a span of the kind quote names, so a reader can resume a span that runs past its line by reading the opener first.
func quoteOpener(quote byte) string {
	if quote == ansiCQuote {
		return "$'"
	}
	return string(quote)
}

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

// heredocsAsHereStrings moves every heredoc body into the line that opens it, as the here-string heredocWord spells, so the command that reads it keeps it.
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
		_, opened, openQuote, stack, _, _ = shellWords(text, stack)
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

// heredocWord is the here-string for a literal heredoc body: hex keeps it one word, and a \x01 byte, which no workflow holds, marks it for spelledProgram.
func heredocWord(body string) string {
	return "<<<\x01" + hex.EncodeToString([]byte(body))
}

// spelledProgram returns a shellProgram script and whether the reader can spell it: a heredocWord body is literal, any other expansion is not.
func spelledProgram(program string) (string, bool) {
	if encoded, ok := strings.CutPrefix(program, "\x01"); ok {
		body, err := hex.DecodeString(encoded)
		return string(body), err == nil
	}
	return program, !strings.ContainsAny(program, "$`")
}

// flattenSubstitutions moves each $( ... ) and ` ... ` onto lines before its command and leaves $_ in its place; a quoted ) or $( is left alone.
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

// markQuotedNewline turns a quoted newline into quotedNewline, so a word stays on one line; commandWords turns it back, so a shell program keeps its lines.
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
	// bodyConditional is the depth outside a group that a && or || opened in
	// a function body; the commands inside it run only on that branch.
	bodyConditional := -1
	// bodyControl counts the if, case and loop blocks open in a function body;
	// a command inside one runs only on that branch.
	bodyControl := 0
	// bodyReturns records a return earlier in the body, after which a later
	// exit no longer runs at every call.
	bodyReturns := false
	var defining, bodyOpened bool
	// definingName is the function whose body is being skipped, and
	// barrierFunctions lists the defined functions whose body sources or
	// evals: a later call runs that text in the current shell.
	var definingName string
	barrierFunctions, forwarders := map[string]bool{}, map[string]bool{}
	// helperCalls records, per defined function, the helpers its body calls
	// and the words it hands them, so a barrier reached through a helper that
	// is defined after its caller is still found when the caller is called.
	helperCalls := map[string][]helperCall{}
	graph := helperGraph{barrierFunctions, forwarders, helperCalls}
	// prelude holds while only commands that cannot move ROOT_DIR have run
	// (see preludeSafe). fixedRoot holds when the first command, in execution
	// order, that names ROOT_DIR is the canonical assignment on a line of its
	// own and no command since has named it, as ROOT_DIR[0]=… or read
	// ROOT_DIR would move it. Together they let a source of a reviewed
	// library pass.
	prelude, fixedRoot, rootNamed := true, false, false
	// rewritten lists the functions whose body rewrites its positional
	// parameters with set or shift, so what "$@" runs is not what the caller
	// passed and the forwarder is a barrier instead.
	rewritten := map[string]bool{}
	// exiting names the helpers whose body ends the shell on every call, as
	// stop() { exit 0; } does; an unconditional call to one ends the step.
	exiting := map[string]bool{}
	// exitCalls records the helpers each body calls unconditionally, so a
	// helper that calls one defined later still ends the step once both exist.
	exitCalls := map[string][]string{}
	var terminates func(name string, seen map[string]bool) bool
	terminates = func(name string, seen map[string]bool) bool {
		if exiting[name] {
			return true
		}
		if seen[name] {
			return false
		}
		seen[name] = true
		return slices.ContainsFunc(exitCalls[name], func(callee string) bool { return terminates(callee, seen) })
	}
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
		words, opened, quote, rest, continues, _ := shellWords(current.String(), quotes)
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
		logical := current.String()
		current.Reset()
		openQuote, quotes = quote, rest
		heredocs = append(heredocs, opened...)
		// A function definition is not a call. Bash reads the body and runs
		// nothing, so a required command written inside a function that
		// nobody calls does not satisfy a rule about what the step runs.
		commands := withoutCasePatternAlternatives(splitCommands(words))
		if !defining {
			if name := definedName(words); name != "" {
				if name == prefix[0] {
					// Every later call runs the definition, not the program.
					return nil
				}
				defining, definedAt, bodyOpened, definingName = true, depth, false, name
				bodyConditional, bodyControl, bodyReturns = -1, 0, false
				prelude = false // a function defined before the source could move ROOT_DIR when called
			}
		}
		if !defining {
			depth += braceDepth(commands) - gluedSubshellClosers(commands, logical)
		} else {
			// A body that sources or evals, or runs an expanded command word
			// the reader cannot spell, makes every call a barrier; a body whose
			// command word is a positional parameter, such as "$@", forwards its
			// caller's words, so a call that passes eval, source or dot is one.
			// A body that calls a helper already known as a barrier, or hands a
			// forwarder such words, is a barrier as well, so the barrier
			// propagates through helpers defined before it.
			// A body may open on a later line, as in never_called ()
			// followed by { on its own, so wait for it before seeking its end;
			// a one-line definition opens and closes within its commands.
			for _, each := range commands {
				outside := depth
				if depth += commandBrace(each); depth > definedAt {
					bodyOpened = true
				}
				if bodyConditional >= 0 && depth <= bodyConditional {
					bodyConditional = -1
				}
				if each.conditional && depth > outside && bodyConditional < 0 {
					bodyConditional = outside
				}
				conditional := each.conditional || bodyConditional >= 0 || bodyControl > 0
				if len(each.args) > 0 && !each.args[0].quoted {
					if change, found := controlWords[each.args[0].text]; found {
						bodyControl = max(bodyControl+change, 0) // after this command is judged, so if exit 1; then still exits
					}
				}
				if len(each.args) == 0 {
					continue
				}
				names := bodyWords(commandWords(spelled(each.args)))
				switch {
				case rebindsCommand(names):
					// A called body that rebinds a name may shadow every later
					// use of the command, so the reader stops at the call.
					barrierFunctions[definingName] = true
				case rewritesParameters(names):
					rewritten[definingName] = true
				case graph.callsBarrier(names):
					barrierFunctions[definingName] = true
				case evaluates(names) && forwards(names):
					forwarders[definingName] = true
				case evaluates(names):
					barrierFunctions[definingName] = true
				case len(names) > 0 && names[0] == "return":
					bodyReturns = true
				case !conditional && !bodyReturns && len(names) > 0 && (endsShell(names) || exiting[names[0]]):
					// A body that exits, or replaces the shell, whenever it runs
					// ends the step at every call; die() { exit 1; } called as
					// cmd || die runs only on failure and ends nothing proved.
					exiting[definingName] = true
				default:
					if callee, args, ok := helperCallWords(names); ok {
						helperCalls[definingName] = append(helperCalls[definingName], helperCall{callee, args})
						if !conditional {
							exitCalls[definingName] = append(exitCalls[definingName], callee)
						}
					}
				}
			}
			depth -= gluedSubshellClosers(commands, logical)
			if bodyOpened && depth <= definedAt {
				defining = false
				if forwarders[definingName] && rewritten[definingName] {
					delete(forwarders, definingName)
					barrierFunctions[definingName] = true
				}
			}
			continue
		}
		if namesRoot(logical, commands) {
			fixedRoot = !rootNamed && control == 0 && depth == 0 && strings.TrimSpace(logical) == canonicalRootDir
			rootNamed = true
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
			names := commandWords(spelled(args))
			library, reviewed := reviewedLibrarySource(names)
			if reviewed && prelude && fixedRoot {
				for _, name := range library.forwarders {
					forwarders[name] = true
				}
			} else if evaluates(names) || graph.callsBarrier(names) {
				// eval and source run text this reader never sees as code, and
				// that text can define a function or an alias with the command's
				// name, as eval 'or''as() { :; }' does. No later call is proved to
				// run the program; the calls before it already ran. A condition or
				// a guarded branch may run, so the barrier holds there too.
				return invocations
			}
			prelude = prelude && preludeSafe(names)
			if rebindsCommand(names) {
				// An alias or a hash -p rebinds a name for every later use, in a
				// condition or a guarded branch as well, so no use is proved.
				return nil
			}
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
			names = texts(args)
			called := names[assignmentPrefix(names):]
			if endsShell(names) || names[0] == "return" ||
				len(called) > 0 && terminates(called[0], map[string]bool{}) {
				// The step ends here; nothing written after it runs, also after
				// X=1 stop or a helper that calls a later-defined stop.
				return invocations
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
// reads the next physical line as the rest of this one; comment is the text
// from the # that starts a comment to the end of the line, or empty.
//
// One pass keeps the six answers consistent. A pattern per answer cannot:
// each has to rediscover the quoting, and the one that gets it wrong reads
// syntax where the shell reads text.
func shellWords(line string, stack []byte) (
	words []word, heredocs []heredoc, open byte, rest []byte, continues bool, comment string,
) {
	var text strings.Builder
	inWord, quoted, quote, pending, stripTabs := false, false, byte(0), false, false
	ansiC, unresolved, decoded := false, false, false
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
			words = append(words, word{text.String(), quoted, decoded})
		}
		text.Reset()
		inWord, quoted, unresolved, decoded = false, false, false, false
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
			decoded = decoded || ansiC
			unresolved = unresolved || !ansiC
			inWord, quoted = true, true
		case character == ' ' || character == '\t':
			flush()
		case character == '#' && !inWord:
			if substituted {
				return nil, heredocs, 0, stack, false, line[index:]
			}
			return words, heredocs, 0, stack, false, line[index:]
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
		return words, heredocs, 0, stack, continues, ""
	}
	return words, heredocs, quote, stack, continues, ""
}

// reviewedShellLibraries pins each repository library a script may source
// without a barrier, by the SHA-256 of its reviewed text. An edit to a library
// fails TestReviewedShellLibrariesMatchTheirFiles until the pin is reviewed.
type reviewedLibrary struct {
	digest     string   // SHA-256 of the reviewed text
	forwarders []string // its functions that run their caller's words, as run() { "$@"; } does
}

var reviewedShellLibraries = map[string]reviewedLibrary{
	"trusted-docker-client.sh": {digest: "3b5ec8e0c5f46857e31acf4e54b8d398fe54c4fdc097a27d1fddb25a32a4d674", forwarders: []string{"run_workcell_docker_client_command"}},
}

// canonicalRootDir is the one ROOT_DIR assignment that pins a script's root.
const canonicalRootDir = `ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"`

// preludeSafe reports whether a command before a reviewed source leaves
// ROOT_DIR as the script set it: an assignment, set, a trap on ERR, EXIT or a
// signal only (a RETURN trap runs when the source returns, and could redefine
// the anchored command), exec, a test, a closing word, or a declaration
// that names each variable literally and makes no nameref, as declare -n
// x=ROOT_DIR would let x=/tmp move the root.
func preludeSafe(names []string) bool {
	rest := names[assignmentPrefix(names):]
	if len(rest) == 0 {
		return true
	}
	switch rest[0] {
	case "set", "exec", "[[", "[", "test", "fi", "done", "esac", "{", "}":
		return true
	case "trap":
		return len(rest) > 2 && !strings.HasPrefix(rest[1], "-") && !slices.ContainsFunc(rest[2:], func(signal string) bool {
			return !preludeTrapSignals[strings.TrimPrefix(strings.ToUpper(signal), "SIG")]
		})
	case "export", "readonly", "declare", "typeset", "local":
		return !slices.ContainsFunc(rest[1:], func(arg string) bool {
			name, _, _ := strings.Cut(arg, "=")
			return strings.ContainsAny(name, "$`") || strings.HasPrefix(name, "-") && strings.Contains(name, "n")
		})
	}
	return false
}

// namesRoot reports whether a logical line names ROOT_DIR other than as one
// reviewed library source. The text is read, not the words, because the
// reader keeps no word for an assignment such as ROOT_DIR="$(…)", and
// ROOT_DIR[0]=…, read ROOT_DIR or a comment all count.
func namesRoot(logical string, commands []command) bool {
	if len(commands) == 1 {
		if _, reviewed := reviewedLibrarySource(commandWords(spelled(commands[0].args))); reviewed {
			return false
		}
	}
	return strings.Contains(logical, "ROOT_DIR")
}

// preludeTrapSignals are the trap conditions that cannot fire while the
// reviewed source runs: an error, the shell's exit, or a signal.
var preludeTrapSignals = map[string]bool{"ERR": true, "EXIT": true, "0": true, "INT": true, "TERM": true, "HUP": true, "QUIT": true, "PIPE": true}

var reviewedLibraryPath = regexp.MustCompile(`^\$\{?ROOT_DIR\}?/scripts/lib/([A-Za-z0-9_.-]+\.sh)$`)

// reviewedLibrarySource returns the pinned library the words source by its
// fixed path under ROOT_DIR, if any. Its text is reviewed, so the source is
// no barrier when only commands that cannot move ROOT_DIR ran before it and
// ROOT_DIR is the canonical root; its forwarders then count as forwarders.
func reviewedLibrarySource(names []string) (reviewedLibrary, bool) {
	names = unwrapBuiltins(names[assignmentPrefix(names):])
	if len(names) != 2 || (names[0] != "source" && names[0] != ".") {
		return reviewedLibrary{}, false
	}
	match := reviewedLibraryPath.FindStringSubmatch(names[1])
	if match == nil {
		return reviewedLibrary{}, false
	}
	library, ok := reviewedShellLibraries[match[1]]
	return library, ok
}

// evaluates reports whether the words run eval, source or ., each of which
// runs code this reader never sees in the current shell: bare, behind command
// or builtin, which run it in this shell, or as the script of sh -c or bash -c.
// Assignments before any of these, as in X=1 eval, still run it here. A child
// shell cannot shadow the command, but the barrier only loses invocations, so
// it holds for every spelling.
func evaluates(names []string) bool {
	names = names[assignmentPrefix(names):]
	// A word list that opens with an option is an argument line, such as an
	// element of a multi-line array, so its expansions name no command.
	optionLed := len(names) > 0 && strings.HasPrefix(names[0], "-")
	// time runs its command in the current shell, so time eval and time source
	// evaluate like the bare builtins.
	names = unwrapBuiltins(names)
	if len(names) == 0 {
		return false
	}
	if names[0] == "eval" || names[0] == "source" || names[0] == "." {
		return true
	}
	if names[0] == "trap" && slices.ContainsFunc(names[1:], func(word string) bool { return strings.EqualFold(word, "DEBUG") || strings.EqualFold(word, "RETURN") }) {
		// A DEBUG trap runs its action as text before every later command,
		// and trap 'exit 0' DEBUG ends the script before the next one. A
		// RETURN trap runs its action as a function or a source returns, so
		// it can redefine a command the caller runs next.
		return true
	}
	if !optionLed && !runsAPath(names[0]) && (strings.ContainsAny(names[0], "$`") ||
		(names[0] != "{" && strings.Contains(names[0], "{"))) {
		// Bash expands this word before it runs it, so s${x-}ource, $(printf
		// source) and {source,f} can each run source. The reader cannot spell
		// the result, so the word is a barrier.
		return true
	}
	if shell := path.Base(names[0]); shell != "sh" && shell != "bash" {
		return false
	}
	at := slices.Index(names, "-c")
	if at < 0 || at+1 == len(names) {
		return false
	}
	return evaluates(strings.Fields(names[at+1]))
}

// runsAPath reports whether an expanded command word still holds a literal /
// once its ${...} and $NAME expansions are removed, as ${ROOT_DIR}/scripts/x.sh
// does. Bash runs a command name with a slash as a file and never looks it up
// as a builtin or a function, so such a word cannot become eval or source. A
// command substitution or a backtick can still produce any word, so a word
// with one stays a barrier, and so does a brace expansion, whose first word
// may be source.
func runsAPath(word string) bool {
	if strings.ContainsAny(word, "`") || strings.Contains(word, "$(") {
		return false
	}
	plain := plainExpansion.ReplaceAllString(word, "")
	return strings.Contains(plain, "/") && !strings.Contains(plain, "{") // {source,./x} expands to source first
}

var plainExpansion = regexp.MustCompile(`\$(?:\{[^}]*\}|[A-Za-z_][A-Za-z0-9_]*|[0-9@*#?$!-])`)

// commandWords drops the reserved words and the case pattern that stand in
// front of the command a word list runs, so the command in if eval … or in
// linux) source … is the one the barrier test reads.
func commandWords(names []string) []string {
	for len(names) > 0 {
		switch {
		case slices.Contains([]string{"if", "elif", "then", "else", "while", "until", "do", "!"}, names[0]):
		case strings.HasSuffix(names[0], ")") && !strings.HasPrefix(names[0], "(") && !strings.Contains(names[0], "$(") && !strings.Contains(names[0], "`"):
		default:
			return names
		}
		names = names[1:]
	}
	return names
}

// unwrapBuiltins drops the command, builtin and time words and the options in
// front of the command word they run; the last word stays so a bare option
// line keeps its first word.
func unwrapBuiltins(names []string) []string {
	for len(names) > 1 && (names[0] == "command" || names[0] == "builtin" || names[0] == "time" || strings.HasPrefix(names[0], "-")) {
		if names[0] == "command" && commandLooksUp(names[1:]) {
			return names // command -v or -V prints how a name resolves and runs nothing
		}
		names = names[1:]
	}
	return names
}

// commandLooksUp reports whether the options of a command builtin include -v
// or -V, which look a name up instead of running it.
func commandLooksUp(args []string) bool {
	for _, arg := range args {
		if arg == "--" || !strings.HasPrefix(arg, "-") {
			return false
		}
		if strings.ContainsAny(arg[1:], "vV") {
			return true
		}
	}
	return false
}

// forwarderWord matches a command word that is the caller's first positional
// parameter or all of them, so the command run is the caller's first word. A
// named array such as "${cmd[@]}" holds words the caller never passed, and a
// later parameter such as "$2" runs a word this reader does not align, so
// neither is a forwarder: such a body is a barrier.
var forwarderWord = regexp.MustCompile(`^\$(?:[@*1]|\{(?:[@*]|1)\})$`)

// rewritesParameters reports whether the command rewrites the positional
// parameters: shift, or set with -- or with an operand that is no option.
func rewritesParameters(names []string) bool {
	names = unwrapBuiltins(names[assignmentPrefix(names):])
	if len(names) == 0 {
		return false
	}
	if names[0] == "shift" {
		return true
	}
	if names[0] != "set" {
		return false
	}
	for _, arg := range names[1:] {
		if arg == "--" || !strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "+") {
			return true
		}
	}
	return false
}

// forwards reports whether the command word, once unwrapped, is a positional
// parameter or an array expansion, as in run() { "$@"; }.
func forwards(names []string) bool {
	names = unwrapBuiltins(names[assignmentPrefix(names):])
	return len(names) > 0 && forwarderWord.MatchString(names[0])
}

// callsBarrier reports whether the command word names a defined function whose
// body sources or evals, so the call runs that text in the current shell, or a
// forwarder such as run() { "$@"; } whose forwarded words are themselves a
// barrier: eval, source or dot, or a word the reader cannot spell, such as
// s${x-}ource.
func (g helperGraph) callsBarrier(names []string) bool {
	callee, args, ok := helperCallWords(names)
	return ok && g.reaches(callee, args, map[string]bool{})
}

// helperCall is one call a function body makes: the helper's name and the
// words handed to it.
type helperCall struct {
	callee string
	args   []string
}

// helperGraph holds what the reader knows about defined functions: the
// barriers, the forwarders, and the helper calls each body makes.
type helperGraph struct {
	barriers, forwarders map[string]bool
	calls                map[string][]helperCall
}

// helperCallWords splits a command into its command word and arguments after
// the assignments and builtin wrappers.
func helperCallWords(names []string) (string, []string, bool) {
	names = unwrapBuiltins(names[assignmentPrefix(names):])
	if len(names) == 0 {
		return "", nil, false
	}
	return names[0], names[1:], true
}

// helperPathBound is the longest chain of helper calls the reader follows;
// a real script's chain is a few calls deep, and a deeper one is a recursion.
const helperPathBound = 32

// bodyWords drops the header of a one-line definition from its first
// command, as shadow() { source x; } puts the header, the brace and the
// first body command in one command, so the body command is classified.
func bodyWords(names []string) []string {
	if len(names) > 1 && (names[0] == "function" || names[1] == "()" || names[1] == "(){") {
		names = names[2:] // function NAME, or NAME () with the parentheses spaced
	}
	for len(names) > 0 && (names[0] == "{" || strings.HasSuffix(names[0], "()") || strings.HasSuffix(names[0], "(){")) {
		names = names[1:]
	}
	return names
}

// reaches reports whether calling callee with args runs a barrier: callee is a
// barrier, or a forwarder handed a word the reader cannot spell or eval,
// source or dot, or a helper whose body calls such a helper. A body that hands
// its own parameters on, as inner "$@", passes the caller's words along. The
// visited set is scoped to the current path and keyed by the callee and its
// words, so a helper called twice with different words, or a recursion that
// changes its words, is judged on each call and only an exact repeat is cut.
func (g helperGraph) reaches(callee string, args []string, visited map[string]bool) bool {
	// The path key holds the words too, so a recursive call that changes its
	// words is followed, and only a call that repeats itself exactly is cut.
	key := callee + "\x00" + strings.Join(args, "\x00")
	if visited[key] {
		// A call that repeats itself exactly, as loop() { loop "$@"; } does,
		// never returns in bash, so nothing after it is proved to run.
		return true
	}
	if len(visited) >= helperPathBound {
		// A path this deep is a recursion that grows its words, as loop "$@" x
		// does, which never repeats a key; the reader stops at it as a barrier.
		return true
	}
	visited[key] = true
	defer delete(visited, key)
	if g.barriers[callee] {
		return true
	}
	if g.forwarders[callee] && evaluates(args) {
		return true
	}
	for _, call := range g.calls[callee] {
		// A forwarded word is replaced in place, so inner source "$@" keeps
		// its literal source ahead of the caller's words.
		var handed []string
		for _, word := range call.args {
			switch {
			case !forwarderWord.MatchString(word):
				handed = append(handed, word)
			case strings.Contains(word, "1"):
				handed = append(handed, args[:min(1, len(args))]...)
			default:
				handed = append(handed, args...)
			}
		}
		if g.reaches(call.callee, handed, visited) {
			return true
		}
	}
	return false
}

// assignmentPrefix returns how many leading words are assignments bash applies
// to the command word after them. Any word with "=" counts, even a-b=1.
func assignmentPrefix(words []string) int {
	count := 0
	for count < len(words) && strings.Contains(words[count], "=") {
		count++
	}
	return count
}

// replacesShell reports whether the words are an exec that names a program,
// which replaces the shell so that nothing written after it runs. An exec
// carrying only redirections, as in exec 2>&1, changes the shell's own
// descriptors and the script continues, so it must not stop the scan.
// endsShell reports whether the command ends the shell: exit, or an exec that
// replaces it, also behind command, builtin or time, which run it in the
// current shell.
func endsShell(names []string) bool {
	names = unwrapBuiltins(names)
	return len(names) > 0 && (names[0] == "exit" || replacesShell(names))
}

func replacesShell(args []string) bool {
	if args[0] != "exec" {
		return false
	}
	return slices.ContainsFunc(args[1:], func(word string) bool {
		operand := strings.TrimLeft(word, "0123456789")
		return !strings.HasPrefix(operand, ">") && !strings.HasPrefix(operand, "<")
	})
}

// rebindsCommand reports whether the command defines an alias or binds a
// name with hash -p, behind any assignments, command or builtin. With
// expand_aliases every later use of that name runs the binding, not the
// program, and the reader does not resolve which name, so it proves no
// later invocation.
func rebindsCommand(names []string) bool {
	names = unwrapBuiltins(names[assignmentPrefix(names):])
	if len(names) < 2 {
		return false
	}
	switch names[0] {
	case "alias":
		return slices.ContainsFunc(names[1:], func(word string) bool { return strings.Contains(word, "=") })
	case "hash":
		return slices.Contains(names[1:], "-p")
	}
	return false
}

// commandWrappers maps each command that runs the command after it to its valued options; a long option matches a unique abbreviation, as getopt allows.
var commandWrappers = map[string][]string{
	"builtin": nil, "command": nil, "exec": {"-a"}, "nohup": nil, "nice": {"-n", "--adjustment"},
	"env":     {"-u", "-C", "-P", "-S", "--unset", "--chdir", "--split-string"},
	"timeout": {"-k", "-s", "--kill-after", "--signal"}, "setsid": nil,
	"flock":  {"-w", "-E", "--wait", "--timeout", "--conflict-exit-code"},
	"ionice": {"-c", "-n", "-p", "-P", "-u", "--class", "--classdata", "--pid", "--pgid", "--uid"},
	"stdbuf": {"-i", "-o", "-e", "--input", "--output", "--error"},
	"xargs": {"-a", "-d", "-E", "-I", "-L", "-n", "-P", "-s", "--arg-file", "--delimiter", "--eof", "--max-lines",
		"--max-args", "--max-procs", "--max-chars", "--process-slot-var"},
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

// bashBuiltins names the builtins that run another command, which builtin
// can run; builtin gh runs nothing, and builtin printf names no command.
var bashBuiltins = map[string]bool{".": true, "builtin": true, "command": true, "eval": true, "exec": true, "source": true}

// wrappedCommand returns words from the command that a chain of wrappers, such
// as env A=1 nice -n 5 command -p gh, runs: command -v only names a command,
// env -S splits its value, timeout reads a DURATION and flock a lock file
// first, flock -c runs a shell program, xargs runs its command with the words
// it reads, and sudo -s or -i keeps the words, which sudo escapes, or runs
// $SHELL with no command.
func wrappedCommand(words []string) []string {
	for {
		name := commandName(words[0])
		valued, wraps := commandWrappers[name]
		if !wraps {
			return words
		}
		if name == "builtin" && (len(words) < 2 || !bashBuiltins[words[1]]) {
			return words // builtin runs a builtin only; a program after it does not run
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
					parsed, _, _, _, _, _ := shellWords(split, nil)
					words = append(append([]string{"env"}, texts(parsed)...), words[min(i+1, len(words)):]...)
					i = 0
				}
			}
		}
		if name == "timeout" || name == "flock" {
			i++ // the DURATION, or the lock file, before the command
		}
		if name == "flock" && i+1 < len(words) && (words[i] == "-c" || words[i] == "--command") {
			return append([]string{"sh", "-c"}, words[i+1:]...) // flock FILE -c runs a shell program
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
// run, from the command word wrappedCommand finds: the reachability-insensitive
// counterpart of ShellInvocations, so branches, guarded commands, subshells
// and substitutions are kept. A shell's program and an eval's words are read
// as a script when the reader can spell them; otherwise the command stays,
// for a lint to fail closed on. A function body is read after each call, with
// the last definition before it, and a trap action runs once at the end.
// PR 804's ShellCommandWords has a similar intent; merge the two when both land.
func EveryShellCommand(script string) [][]string {
	return everyShellCommand(script)
}

// everyShellCommand is EveryShellCommand for a script a child shell runs, such
// as a sh -c body, which has its own definitions and traps.
func everyShellCommand(script string) [][]string {
	traps := trapState{map[string]string{}, map[string]bool{}}
	bodies := map[string]string{}
	found := readScript(flattenSubstitutions(heredocsAsHereStrings(script)), bodies, nil, traps)
	// An action can set another trap, so read until every action is read.
	for read := map[string]bool{}; ; {
		for _, action := range traps.set {
			traps.armed[action] = true
		}
		actions := slices.Sorted(maps.Keys(traps.armed))
		at := slices.IndexFunc(actions, func(action string) bool { return !read[action] })
		if at < 0 {
			return found
		}
		read[actions[at]] = true
		found = append(found, expandCalls(flattenedCommands(actions[at]), bodies, nil, traps)...)
	}
}

// readScript returns the commands text runs. A function definition in text
// runs nothing: it binds the name for the calls after it, as bash does.
func readScript(text string, bodies map[string]string, calling []string, traps trapState) [][]string {
	var found [][]string
	for _, part := range functionBodies(text) {
		if part.name != "" {
			bodies[part.name] = part.text
			continue
		}
		found = append(found, expandCalls(flattenedCommands(part.text), bodies, calling, traps)...)
	}
	return found
}

// trapState holds the action set for each signal, and every action that was
// set while a command ran. That command can fail under bash -e, or exit, and
// run the action, so a later trap does not undo it.
type trapState struct {
	set   map[string]string
	armed map[string]bool
}

// expandCalls puts each called function's body right after every command that
// calls it, so a cd in the body moves the commands after the call. calling
// holds the functions being expanded, so a recursive call is read once. Each
// trap command updates traps.
func expandCalls(commands [][]string, bodies map[string]string, calling []string, traps trapState) [][]string {
	var found [][]string
	for _, words := range commands {
		found = append(found, words)
		if words[0] == "trap" {
			setTrap(words[1:], traps.set)
		} else {
			for _, action := range traps.set {
				traps.armed[action] = true
			}
		}
		if body, defined := bodies[words[0]]; defined && !slices.Contains(calling, words[0]) {
			found = append(found, readScript(body, bodies, append(calling, words[0]), traps)...)
		}
	}
	return found
}

// setTrap applies the arguments of one trap command to the action set for each
// signal. A later action for a signal replaces the earlier one, and - or an
// empty action removes it, as does a signal named alone. trap -p and trap -l
// print and set nothing.
func setTrap(args []string, traps map[string]string) {
	if len(args) > 0 && args[0] == "--" {
		args = args[1:]
	} else if len(args) > 0 && len(args[0]) > 1 && args[0][0] == '-' {
		return
	}
	if len(args) == 0 {
		return
	}
	action, signals := args[0], args[1:]
	if len(args) == 1 {
		action, signals = "-", args
	}
	for _, signal := range signals {
		signal = strings.TrimPrefix(strings.ToUpper(signal), "SIG")
		if signal == "0" {
			signal = "EXIT"
		}
		if action == "-" || action == "" {
			delete(traps, signal)
		} else {
			traps[signal] = action
		}
	}
}

// flattenedCommands returns the words of every command in text, which flattenSubstitutions has rewritten; a here-string is its command's stdin.
func flattenedCommands(text string) [][]string {
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
			found = append(found, flattenedCommands(text)...) // eval runs its words as a script
		} else if shell && spelled {
			// A shell runs its program as a script in a child shell with its
			// own definitions and traps, so an uncalled function in it runs nothing.
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
	// A case pattern, the words from in or ;; up to ), names no command. A )
	// that closes no ( is one too: its case header may sit in another part.
	caseDepth, expectIn, pending, parens := 0, false, false, 0
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
		words, _, _, _, continues, _ := shellWords(joined+strings.TrimSuffix(line, "\n"), nil)
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
					switch {
					case pending && rest[at] == ')':
						discard() // the pattern ends
						pending = false
					case pending: // a ( opens a pattern
					case rest[at] == '(':
						parens++
						end()
					case parens > 0:
						parens--
						end()
					default:
						discard() // a pattern after a split
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

// sudoRunsNothing reports a sudo mode that runs no command, as sudo -e or -l.
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

// shellRedirect matches a redirection word; a bare operator takes the next word.
var shellRedirect = regexp.MustCompile(`^` + shellFDPattern + `?(&>|[<>])[<>&|]*`)

// shellFD matches an fd number or {name} that a redirection operator follows.
var shellFD = regexp.MustCompile(`^` + shellFDPattern + `$`)

const shellFDPattern = `([0-9]+|\{[A-Za-z_][A-Za-z0-9_]*\})`

// shellKeywords are the reserved words before a command that isCommandPrefixWord does not cover.
var shellKeywords = []string{"if", "then", "do", "else", "elif", "while", "until", "{"}
