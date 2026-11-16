// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// Bash evaluates the value of a variable it meets in arithmetic as an
// expression of its own, so x='PATH=1'; (( x )) assigns PATH, and so does a
// value an expansion spells, as (( $x )) does. The lane reader therefore reads
// every arithmetic context, (( )), $(( )), $[ ], let, an integer operator of
// [[ ]], an array subscript and a substring offset, and accepts one only when
// each operand is a number or a variable that holds one: one the lane assigns,
// and every value it assigns is a number literal, a single arithmetic
// expansion, $?, $#, $$, $!, a length, a number let computes, or an expansion
// of another such variable. Any other operand, a positional parameter, $_ or a
// substitution included, is unreadable.

// laneArithmetic collects the arithmetic expressions of the commands a lane
// runs, as laneScan reads them, in order. The reader splits an expression at a
// space, so $(( x )) and ${y: -1} span words, and an arithmetic command or a
// for (( )) header spans the commands it splits at each ;: the words of one
// still open are joined until its closing byte.
type laneArithmetic struct {
	open        []string // the words of an expression not yet closed
	closer      byte     // the byte that closes it
	depth       int      // its open pairs
	test        bool     // a [[ is open, which the reader splits into commands at each && and ||
	expressions []laneExpression
	budget      int // the bytes left to scan, linear in the text's length
}

// newLaneArithmetic returns a reader for a text of a length, whose work is
// bounded by it: nested expansions that each scan to their close fail closed
// once they spend the bound.
func newLaneArithmetic(length int) *laneArithmetic {
	return &laneArithmetic{budget: 16*length + 1<<16}
}

// closing returns the index of the byte that closes the open byte at start,
// counting nested pairs, or len(text) when none does, and charges the bytes it
// scans to the budget.
func (reader *laneArithmetic) closing(text string, start int, open, closing byte) int {
	end := laneClosing(text, start, open, closing)
	reader.budget -= end - start + 1
	return end
}

// laneExpression is one arithmetic expression, with the array it subscripts,
// if any: bash reads the subscript of an associative array as a string.
type laneExpression struct{ text, array string }

// laneCommandArithmetic is an unquoted (( that no $ opens, which starts an
// arithmetic command, wherever in a word bash ends the word before it.
var laneCommandArithmetic = regexp.MustCompile(`(?:^|[^$])\(\(`)

// laneIntegerTests are the operators of [[ ]] that compare their operands as
// arithmetic.
var laneIntegerTests = []string{"-eq", "-ne", "-lt", "-le", "-gt", "-ge"}

// laneOpeners maps each byte that opens an expression to the byte that closes
// it.
var laneOpeners = map[byte]byte{'(': ')', '[': ']', '{': '}'}

// read adds the arithmetic expressions one command's words hold.
func (reader *laneArithmetic) read(args []word) {
	words := texts(args)
	for at, each := range args {
		if reader.open != nil {
			reader.add(each.text)
		} else if at := laneCommandArithmetic.FindStringIndex(each.syntax); at != nil {
			reader.start(each.text[at[1]-2:], '(') // an unquoted (( outside a $(( ), as in (( x )), for ((, while((x))
		}
		expressions, tail := reader.word(each.text)
		reader.expressions = append(reader.expressions, expressions...)
		if tail != "" && reader.open == nil {
			reader.start(tail, tail[1])
		}
		switch {
		case !each.quoted && each.text == "[[":
			reader.test = true
		case !each.quoted && each.text == "]]":
			reader.test = false
		case reader.test && !each.quoted && slices.Contains(laneIntegerTests, each.text):
			for _, operand := range []int{at - 1, at + 1} {
				if operand >= 0 && operand < len(words) {
					reader.expressions = append(reader.expressions, laneExpression{text: words[operand]})
				}
			}
		case reader.test && !each.quoted && each.text == "-v" && at+1 < len(words):
			reader.expressions = append(reader.expressions, laneSubscripts(words[at+1])...)
		}
	}
	command := laneCommand(laneLeading(laneUnredirected(words)))
	switch firstOf(command) {
	case "let":
		for _, operand := range command[1:] {
			reader.expressions = append(reader.expressions, laneExpression{text: operand})
		}
	case "unset":
		for _, operand := range command[1:] {
			reader.expressions = append(reader.expressions, laneSubscripts(operand)...)
		}
	}
}

// start opens an expression with its first text, from the byte that opens it.
func (reader *laneArithmetic) start(text string, opener byte) {
	reader.open, reader.closer, reader.depth = nil, laneOpeners[opener], 0
	reader.add(text)
}

// add adds one word to the open expression, and closes it once its pairs do:
// the text of a ${ } is read again for the expressions it holds.
func (reader *laneArithmetic) add(text string) {
	opener := map[byte]byte{')': '(', ']': '[', '}': '{'}[reader.closer]
	reader.open = append(reader.open, text)
	reader.depth += strings.Count(text, string(opener)) - strings.Count(text, string(reader.closer))
	if reader.depth > 0 {
		return
	}
	joined := strings.Join(reader.open, " ")
	reader.open, reader.depth = nil, 0
	if reader.closer == '}' {
		expressions, _ := reader.word(joined)
		reader.expressions = append(reader.expressions, expressions...)
		return
	}
	reader.expressions = append(reader.expressions, laneExpression{text: joined})
}

// remaining returns the expressions read and any still open, which an
// unclosed expression ends: every word after it is read as part of it.
func (reader *laneArithmetic) remaining() []laneExpression {
	if reader.open == nil {
		return reader.expressions
	}
	return append(reader.expressions, laneExpression{text: strings.Join(reader.open, " ")})
}

// laneSubscripts returns the subscript of a word that names an array element,
// as a[i] does, or none.
func laneSubscripts(text string) []laneExpression {
	if match := laneElement.FindStringSubmatch(text); match != nil {
		return []laneExpression{{match[2], match[1]}}
	}
	return nil
}

// laneElement is a word that names an array element, and laneAssignedElement
// one that may assign one, as a[i]=1, a[i]+=1 or the [i]=1 of a=([i]=1) do.
var (
	laneElement         = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)\[(.*)\]$`)
	laneAssignedElement = regexp.MustCompile(`^(?:([A-Za-z_][A-Za-z0-9_]*)=?)?\(?[A-Za-z_0-9]*\[`)
)

// word returns the arithmetic expressions in one word's text:
// each $(( )) and $[ ], each subscript and substring offset of a ${ }, and the
// subscript of an element the word assigns, and the text from the first that
// the word does not close, which runs on into the next word. A quote does not
// stop bash evaluating one, so the text is read whole.
func (reader *laneArithmetic) word(text string) (expressions []laneExpression, tail string) {
	reader.budget -= len(text)
	if match := laneAssignedElement.FindStringSubmatch(text); match != nil {
		start := strings.IndexByte(text, '[')
		if end := reader.closing(text, start, '[', ']'); end < len(text) && strings.HasPrefix(strings.TrimPrefix(text[end+1:], "+"), "=") {
			array := match[1]
			if strings.Contains(text[:start], "=") {
				array = "" // an element of a compound assignment, whose array this reader does not track
			}
			expressions = append(expressions, laneExpression{text[start+1 : end], array})
		}
	}
	for at := 0; at < len(text) && reader.budget >= 0; at++ {
		if !strings.HasPrefix(text[at:], "$((") && !strings.HasPrefix(text[at:], "$[") && !strings.HasPrefix(text[at:], "${") {
			continue
		}
		opener := text[at+1]
		end := reader.closing(text, at+1, opener, laneOpeners[opener])
		if end == len(text) {
			return expressions, text[at:]
		}
		if opener == '{' {
			expressions = append(expressions, reader.parameter(text[at:end+1])...)
		} else {
			expressions = append(expressions, laneExpression{text: text[at : end+1]})
		}
	}
	return expressions, ""
}

// parameter returns the arithmetic expressions of the ${ } that
// is text: its subscript, unless @ or *, and the offset and length of a
// substring, as ${y:x:n} has. ${y:-x}, ${y:=x}, ${y:+x} and ${y:?x} take no
// offset.
func (reader *laneArithmetic) parameter(text string) []laneExpression {
	var expressions []laneExpression
	at := 2
	if at < len(text) && (text[at] == '#' || text[at] == '!') {
		at++
	}
	name := laneParameterName.FindString(text[at:])
	at += len(name)
	if at < len(text) && text[at] == '[' {
		end := reader.closing(text, at, '[', ']')
		if subscript := text[at+1 : max(at+1, end)]; subscript != "@" && subscript != "*" {
			expressions = append(expressions, laneExpression{subscript, name})
		}
		at = max(at+1, end+1)
	}
	if name == "" || at+1 >= len(text) || text[at] != ':' || strings.IndexByte("-=+?", text[at+1]) >= 0 {
		return expressions
	}
	reader.budget -= len(text)
	offset, length, _ := strings.Cut(text[at+1:len(text)-1], ":")
	return append(expressions, laneExpression{text: offset}, laneExpression{text: length})
}

// laneParameterName is the name a ${ } expands: a variable, a positional
// parameter or a special one.
var laneParameterName = regexp.MustCompile(`^(?:[A-Za-z_][A-Za-z0-9_]*|[0-9]+|[@*#?$!-])`)

// laneClosing returns the index of the byte that closes the open byte at
// start, counting nested pairs, or len(text) when none does.
func laneClosing(text string, start int, open, closing byte) int {
	depth := 0
	for at := start; at < len(text); at++ {
		switch text[at] {
		case open:
			depth++
		case closing:
			if depth--; depth == 0 {
				return at
			}
		}
	}
	return len(text)
}

// laneArithmeticOperand is one operand of an arithmetic expression: a number,
// in any base, a variable, by name or as an expansion, a length or a special
// parameter that holds a number, as the major, minor and patch elements of
// bash's own BASH_VERSINFO do, an arithmetic expansion's opener, or any other
// expansion, which no reader can show holds a number.
var laneArithmeticOperand = regexp.MustCompile("^(?:" +
	`(?P<number>[0-9]+#[0-9A-Za-z@_]+|0[xX][0-9A-Fa-f]+|[0-9]+)|` +
	`(?P<counted>\$\{#?[?#$!]\}|\$[?#$!]|\$\{#[A-Za-z_][A-Za-z0-9_]*|\$\(\(|\$\[|\$?\{?BASH_VERSINFO\[[012]\])|` +
	`\$\{?(?P<expanded>[A-Za-z_][A-Za-z0-9_]*)|` +
	`(?P<name>[A-Za-z_][A-Za-z0-9_]*)|` +
	"(?P<other>\\$|`))")

// arithmeticReason returns why this reader cannot show that an expression
// assigns nothing it does not spell: an operand that is not a number nor a
// variable in numbers, or "".
func arithmeticReason(expression string, numbers map[string]bool) string {
	for at := 0; at < len(expression); at++ {
		match := laneArithmeticOperand.FindStringSubmatch(expression[at:])
		if match == nil {
			continue
		}
		name := match[laneArithmeticOperand.SubexpIndex("expanded")] + match[laneArithmeticOperand.SubexpIndex("name")]
		switch {
		case match[laneArithmeticOperand.SubexpIndex("other")] != "":
			return fmt.Sprintf("evaluates %q in arithmetic, an expansion this reader cannot show holds a number", strings.TrimSpace(expression))
		case name != "" && (name == "_" || !numbers[name]):
			return fmt.Sprintf("evaluates %q in arithmetic, where bash reads the value of %s as an expression this reader cannot show is a number", strings.TrimSpace(expression), name)
		}
		at += len(match[0]) - 1
	}
	return ""
}

// laneNumbers returns the variables a lane assigns only numbers, as
// arithmeticReason accepts them.
func laneNumbers(writes map[string][]laneWrite) map[string]bool {
	numbers := map[string]bool{}
	for changed := true; changed; {
		changed = false
		for name, each := range writes {
			if numbers[name] || !slices.ContainsFunc(each, func(write laneWrite) bool { return !write.shaped }) ||
				slices.ContainsFunc(each, func(write laneWrite) bool { return !laneNumberWrite(write, numbers) }) {
				continue
			}
			numbers[name], changed = true, true
		}
	}
	return numbers
}

// laneNumberWrite reports whether a write assigns a number: a literal one, or
// none, an array of them, a number let computes, a single arithmetic expansion, a special
// parameter or a length that holds one, or an expansion of a variable in
// numbers.
func laneNumberWrite(write laneWrite, numbers map[string]bool) bool {
	value := write.value
	switch {
	case write.integer, write.literal && laneNumberLiteral.MatchString(value):
		return true
	case laneNumberArray.MatchString(value):
		return true
	case strings.HasPrefix(value, "$(("):
		return laneClosing(value, 1, '(', ')') == len(value)-1
	case strings.HasPrefix(value, "$["):
		return laneClosing(value, 1, '[', ']') == len(value)-1
	}
	match := laneNumberExpansion.FindStringSubmatch(value)
	return match != nil && (match[1] == "" || numbers[match[1]])
}

// laneNumberArray is an array of numbers, PIPESTATUS's included,
// laneNumberLiteral a literal value bash reads as a number, empty included,
// and laneNumberExpansion a value that is one expansion of a
// special parameter or a length, which hold one, or of a named variable.
var (
	laneNumberArray     = regexp.MustCompile(`^\((?:\s*(?:[-+]?[0-9]+|\$\{PIPESTATUS\[@\]\}))*\s*\)$`)
	laneNumberLiteral   = regexp.MustCompile(`^[-+]?[0-9]*$`)
	laneNumberExpansion = regexp.MustCompile(`^(?:\$\{?[?#$!]\}?|\$\{#[A-Za-z_][A-Za-z0-9_]*(?:\[[@*]\])?\}|\$\{?([A-Za-z_][A-Za-z0-9_]*)\}?)$`)
)

// laneExpansionOperator is a ${ } with an @ operator, and laneInertOperators
// the operators that only transform the value: any other, as @P, which
// expands the value as a prompt and so runs its substitutions and assigns its
// defaults, is unreadable.
var laneExpansionOperator = regexp.MustCompile(`\$\{[^{}]*@([A-Za-z])\}`)

const laneInertOperators = "QEUuLaKkA"

// laneOperatorReason returns why a text's expansions are unreadable: one with
// an @ operator outside laneInertOperators, or "".
func laneOperatorReason(text string) string {
	for _, match := range laneExpansionOperator.FindAllStringSubmatch(text, -1) {
		if !strings.Contains(laneInertOperators, match[1]) {
			return fmt.Sprintf("expands %s, whose @%s operator may run or assign text this reader cannot spell", match[0], match[1])
		}
	}
	return ""
}

// laneAssociative returns the arrays the lane declares associative before it
// names them any other way, whose subscripts bash reads as strings: a declare
// or typeset -A at the lane's top level, outside any function, group, subshell
// or compound command, that is the first command of the lane to name the
// array, when no other command of the lane or a library declares, unsets or
// names it as a local, and no library names it at all. scanned holds each
// body's commands, the lane's first.
func laneAssociative(scanned [][][]word) map[string]bool {
	associative, seen, rejected := map[string]bool{}, map[string]bool{}, map[string]bool{}
	var nesting laneNesting
	for at, body := range scanned {
		for _, args := range body {
			top := nesting.read(args) && at == 0
			command := laneCommand(laneLeading(laneUnredirected(texts(args))))
			declares := slices.Contains([]string{"declare", "typeset"}, firstOf(command)) && laneOption(command[1:], "A")
			for _, name := range laneMentions(args) {
				if at == 0 && top && declares && !seen[name] && slices.ContainsFunc(command[1:], func(operand string) bool {
					return strings.SplitN(operand, "=", 2)[0] == name
				}) {
					associative[name] = true
				} else if at > 0 || associative[name] && slices.Contains(laneBindings, firstOf(command)) && slices.ContainsFunc(command[1:], func(operand string) bool {
					return strings.SplitN(operand, "=", 2)[0] == name
				}) {
					rejected[name] = true
				}
				seen[name] = true
			}
		}
	}
	for name := range rejected {
		delete(associative, name)
	}
	return associative
}

// laneBindings are the builtins that may make a name an indexed array or
// another variable again.
var laneBindings = []string{"declare", "typeset", "local", "unset", "export", "readonly"}

// laneMentions returns each name a command's words spell.
func laneMentions(args []word) []string {
	var names []string
	for _, each := range args {
		names = append(names, laneIdentifier.FindAllString(each.text, -1)...)
	}
	return names
}

// laneIdentifier is a name bash may read as a variable.
var laneIdentifier = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)

// laneNesting follows the groups, subshells, compound commands and function
// bodies a lane's commands open and close, in order, to tell a command at the
// top level from one inside them. A closer it cannot match makes it lose
// track, and then no command is at the top level.
type laneNesting struct {
	open []string // the openers still open, innermost last
	lost bool
}

// laneNestingClosers maps each closer to the opener it closes.
var laneNestingClosers = map[string]string{"}": "{", ")": "(", "fi": "if", "done": "do", "esac": "case"}

// read follows one command and reports whether it runs at the top level.
func (nesting *laneNesting) read(args []word) bool {
	push := func(opener string) { nesting.open = append(nesting.open, opener) }
	innermost := func() string {
		if len(nesting.open) == 0 {
			return ""
		}
		return nesting.open[len(nesting.open)-1]
	}
	pop := func(closer string) {
		if innermost() != laneNestingClosers[closer] {
			nesting.lost = true
			return
		}
		nesting.open = nesting.open[:len(nesting.open)-1]
	}
	words := args
	for leading := true; leading && len(words) > 0 && !words[0].quoted; {
		switch text := words[0].text; {
		case text == "{" || text == "if":
			push(text)
		case text == "while" || text == "until":
			push("do")
		case text == "for" || text == "select" || text == "case":
			push(map[bool]string{true: "case", false: "do"}[text == "case"])
			leading = false // the words after it are not commands
		case laneNestingClosers[text] != "":
			pop(text)
		case innermost() == "case" && strings.HasSuffix(text, ")"):
			// a case pattern
		case strings.HasPrefix(text, "((") || text == "[[":
			leading = false
		case strings.HasPrefix(text, "("):
			push("(")
			leading = text == "("
		case strings.HasSuffix(text, "(){"):
			push("{")
		case text == "function" && len(words) > 1:
			words = words[1:]
		case strings.HasSuffix(text, "()") || slices.Contains([]string{"then", "else", "elif", "do", "!", "time"}, text):
		default:
			leading = false
			continue
		}
		words = words[1:]
	}
	top := len(nesting.open) == 0 && !nesting.lost
	for _, each := range words {
		if !each.quoted && strings.HasSuffix(each.text, "=(") {
			push("(") // an array assignment whose elements run on to a later line
		}
	}
	if len(words) > 0 && !words[len(words)-1].quoted && strings.HasSuffix(words[len(words)-1].text, ")") && innermost() == "(" {
		pop(")")
	}
	return top
}
