// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// laneIf is one node of a GitHub if expression, read for its truth: a test of
// the event name against a string, an operand the reader does not know, a
// constant, or a !, && or || of nodes.
type laneIf struct {
	op    string // "event", "atom", "const", "!", "&&" or "||"
	event string // the string an event test compares the event name with
	not   bool   // the event test is !=, or the constant is false
	atom  int    // the unknown operand's index
	cmp   bool   // the node is a comparison, which a ! binds before
	kids  []*laneIf
}

// holds reports the node's truth on an event, with the unknown operands true
// where truth has their bit set.
func (node *laneIf) holds(event string, truth int) bool {
	switch node.op {
	case "event":
		return strings.EqualFold(event, node.event) != node.not
	case "atom":
		return truth>>node.atom&1 == 1
	case "const":
		return !node.not
	case "!":
		return !node.kids[0].holds(event, truth)
	case "&&":
		return node.kids[0].holds(event, truth) && node.kids[1].holds(event, truth)
	}
	return node.kids[0].holds(event, truth) || node.kids[1].holds(event, truth)
}

// laneReachable returns the events of events on which a job's and a step's
// if may both hold: with the event name fixed to the event, some truth of the
// other operands, each read as one unknown, makes each if true. It reports
// false, with every event, when it cannot be sure that each event it returns
// can reach the step, so a case on the event name must count every branch: an
// if it cannot parse, the event name read other than by == or != against a
// string, fromJSON or format, which may build any value, an operand that may
// copy the event name, two operands that read one value or one inside the
// other, which may contradict, or more operands than it tries.
func laneReachable(events []string, ifs ...string) ([]string, bool) {
	parser := laneIfParser{atoms: map[string]int{}, keys: map[string]string{}}
	var trees []*laneIf
	for _, text := range ifs {
		text = strings.TrimSpace(text)
		if strings.HasPrefix(text, "${{") && strings.HasSuffix(text, "}}") {
			text = text[3 : len(text)-2]
		}
		if text == "" {
			continue
		}
		tree, ok := parser.parse(text)
		if !ok {
			return events, false
		}
		trees = append(trees, tree)
	}
	if len(parser.atoms) > 12 {
		return events, false
	}
	reach := []string{}
	for _, event := range events {
		for truth := 0; truth < 1<<len(parser.atoms); truth++ {
			if !slices.ContainsFunc(trees, func(tree *laneIf) bool { return !tree.holds(event, truth) }) {
				reach = append(reach, event)
				break
			}
		}
	}
	return reach, true
}

// laneIfToken is one token of an if expression: a string, a number, a name,
// an operator or a punctuation mark.
var laneIfToken = regexp.MustCompile(`^(?:'(?:[^']|'')*'|-?[0-9][0-9A-Za-z.]*|[A-Za-z_][A-Za-z0-9_-]*|==|!=|<=|>=|&&|\|\||[!<>()\[\].,*])`)

// laneIfComparisons are the operators that compare two operands.
var laneIfComparisons = []string{"==", "!=", "<", "<=", ">", ">="}

// laneIfParser reads if expressions into nodes, numbering the operands it
// does not know across them.
type laneIfParser struct {
	tokens []string
	at     int
	atoms  map[string]int    // each unknown operand's text, with its index
	keys   map[string]string // each context value an unknown operand reads, with that operand's text
}

func (p *laneIfParser) parse(text string) (*laneIf, bool) {
	p.tokens, p.at = nil, 0
	for text = strings.TrimSpace(text); text != ""; {
		token := laneIfToken.FindString(text)
		if lower := strings.ToLower(token); token == "" || lower == "fromjson" || lower == "format" {
			return nil, false
		}
		p.tokens, text = append(p.tokens, token), strings.TrimSpace(text[len(token):])
	}
	tree, ok := p.chain(0)
	return tree, ok && p.at == len(p.tokens)
}

func (p *laneIfParser) peek() string {
	if p.at < len(p.tokens) {
		return p.tokens[p.at]
	}
	return ""
}

// chain reads an || chain at level 0, or an && chain at level 1.
func (p *laneIfParser) chain(level int) (*laneIf, bool) {
	op, next := "||", func() (*laneIf, bool) { return p.chain(1) }
	if level == 1 {
		op, next = "&&", p.not
	}
	tree, ok := next()
	for ok && p.peek() == op {
		p.at++
		var right *laneIf
		right, ok = next()
		tree = &laneIf{op: op, kids: []*laneIf{tree, right}}
	}
	return tree, ok
}

// not reads a node with any ! in front. A ! binds before a comparison, so
// one in front of a comparison with no parentheses is not read.
func (p *laneIfParser) not() (*laneIf, bool) {
	if p.peek() != "!" {
		return p.compare()
	}
	p.at++
	kid, ok := p.not()
	return &laneIf{op: "!", kids: []*laneIf{kid}}, ok && !kid.cmp
}

// compare reads a group, or one operand or a comparison of two.
func (p *laneIfParser) compare() (*laneIf, bool) {
	if p.peek() == "(" {
		p.at++
		tree, ok := p.chain(0)
		if !ok || p.peek() != ")" {
			return nil, false
		}
		p.at++
		group := *tree
		group.cmp = false // a ! before parentheses negates what they hold
		return &group, true
	}
	start := p.at
	left, literal, keys, ok := p.operand()
	if !ok {
		return nil, false
	}
	if !slices.Contains(laneIfComparisons, p.peek()) {
		switch {
		case !literal:
			return p.atom(start, keys)
		case left[0] == '\'':
			return &laneIf{op: "const", not: left == "''"}, true
		case left == "true" || left == "false" || left == "null":
			return &laneIf{op: "const", not: left != "true"}, true
		}
		number, err := strconv.ParseFloat(left, 64)
		return &laneIf{op: "const", not: number == 0}, err == nil
	}
	op := p.tokens[p.at]
	p.at++
	right, rightLiteral, rightKeys, ok := p.operand()
	if left == "github.event_name" && strings.HasPrefix(right, "'") {
		left, right = right, left
	}
	switch {
	case !ok || literal && rightLiteral:
		return nil, false
	case right == "github.event_name" && (op == "==" || op == "!=") && strings.HasPrefix(left, "'"):
		return &laneIf{op: "event", event: left[1 : len(left)-1], not: op == "!=", cmp: true}, true
	}
	tree, ok := p.atom(start, append(keys, rightKeys...))
	if ok {
		tree.cmp = true
	}
	return tree, ok
}

// operand reads one string, number or constant, or one context value or
// function call with what follows it, and returns its lower-case text,
// whether it is a literal, and the context values it reads.
func (p *laneIfParser) operand() (text string, literal bool, keys []string, ok bool) {
	start, token := p.at, strings.ToLower(p.peek())
	p.at++
	switch {
	case token == "":
		return "", false, nil, false
	case token[0] == '\'' || token[0] == '-' || token[0] >= '0' && token[0] <= '9' || token == "true" || token == "false" || token == "null":
		return p.tokens[start], true, nil, true
	case !laneIfName.MatchString(token):
		return "", false, nil, false
	case p.peek() == "(":
		for p.at++; p.peek() != ")"; {
			_, _, inner, ok := p.operand()
			if !ok || p.peek() != "," && p.peek() != ")" {
				return "", false, nil, false
			}
			if keys = append(keys, inner...); p.peek() == "," {
				p.at++
			}
		}
		p.at++
		if len(keys) == 0 && slices.Contains([]string{"success", "always", "failure", "cancelled"}, token) {
			keys = []string{"status()"} // the job's status, which each status function reads
		}
	}
	for p.peek() == "." || p.peek() == "[" {
		if p.at++; p.tokens[p.at-1] == "." {
			if name := strings.ToLower(p.peek()); name != "*" && !laneIfName.MatchString(name) {
				return "", false, nil, false
			}
			p.at++
			continue
		}
		_, _, inner, ok := p.operand()
		if keys = append(keys, inner...); !ok || p.peek() != "]" {
			return "", false, nil, false
		}
		p.at++
	}
	text = strings.ToLower(strings.Join(p.tokens[start:p.at], ""))
	return text, false, append(keys, text), true
}

// laneIfName is a name in an if expression, in lower case.
var laneIfName = regexp.MustCompile(`^[a-z_][a-z0-9_-]*$`)

// laneIfCopies are the values an operand reads that may hold a copy of the
// event name: the whole github context, or one that a workflow or a run sets.
var laneIfCopies = regexp.MustCompile(`^(?:(?:env|matrix|needs|steps|jobs)\b|github(?:$|\[|\.\*))`)

// atom numbers the unknown operand that runs from start to the parser's
// place. It refuses one that names the event name, since only a test of it
// against a string is read, one that may copy it, and one that reads a value,
// or a value inside one, that another operand reads, which may contradict it.
func (p *laneIfParser) atom(start int, keys []string) (*laneIf, bool) {
	text := strings.ToLower(strings.Join(p.tokens[start:p.at], " "))
	if strings.Contains(text, "event_name") || slices.ContainsFunc(keys, laneIfCopies.MatchString) {
		return nil, false
	}
	index, seen := p.atoms[text]
	if !seen {
		index = len(p.atoms)
		p.atoms[text] = index
	}
	for _, key := range keys {
		for other, owner := range p.keys {
			if owner != text && (key == other || strings.HasPrefix(key, other+".") || strings.HasPrefix(key, other+"[") ||
				strings.HasPrefix(other, key+".") || strings.HasPrefix(other, key+"[")) {
				return nil, false
			}
		}
		p.keys[key] = text
	}
	return &laneIf{op: "atom", atom: index}, true
}
