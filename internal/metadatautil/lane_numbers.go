// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// A function's local is a variable of its own while the function runs, so
// the lane reader reads a number there from the function's own writes. Any
// write outside a function that declares the name local may reach the local
// of whichever function is running, a function's caller included, so it
// counts against every copy. A guard of digits and an eval whose expansions
// are numbers are read here too.

// laneLocalKey is the name under which the reader keeps a function's local:
// the variable's name and the number of the function's body.
func laneLocalKey(name string, function int) string {
	return name + "@" + strconv.Itoa(function)
}

// laneLocalNames returns the names a command declares local, as a plain
// local with no option does, or none.
func laneLocalNames(command []string) []string {
	if len(command) < 2 || command[0] != "local" || slices.ContainsFunc(command[1:], func(operand string) bool { return strings.HasPrefix(operand, "-") }) {
		return nil
	}
	var names []string
	for _, operand := range command[1:] {
		if name, _, _ := strings.Cut(operand, "="); laneVariableName.MatchString(name) {
			names = append(names, name)
		}
	}
	return names
}

// laneGuard is a test that a variable holds only digits, which skips the
// rest of the loop body, or returns from the function, when it does not: the
// variable holds digits for the rest of that scope until a write to it.
type laneGuard struct {
	name            string
	scope, function int
	exit            string // continue or return
}

// laneGuardLimit bounds the guards in force at once, so the work of reading
// each command stays constant.
const laneGuardLimit = 8

// laneDigitsGuard is the variable a test of only digits reads, as
// [[ "${v}" =~ ^[0-9]+$ ]] does with an unquoted regex anchored at both ends.
var laneDigitsGuard = regexp.MustCompile(`^\$(?:([A-Za-z_][A-Za-z0-9_]*)|\{([A-Za-z_][A-Za-z0-9_]*)\})$`)

// laneGuardTest returns the variable a command tests for digits, or "".
func laneGuardTest(args []word) string {
	if len(args) != 5 || args[0].quoted || args[0].text != "[[" || args[2].quoted || args[2].text != "=~" ||
		args[3].quoted || args[3].text != "^[0-9]+$" || args[4].quoted || args[4].text != "]]" {
		return ""
	}
	match := laneDigitsGuard.FindStringSubmatch(args[1].text)
	if match == nil {
		return ""
	}
	return match[1] + match[2]
}

// laneGuardExit returns the name a command runs with at most a literal
// status or loop count, as continue 2 or return 1 do, or "".
func laneGuardExit(args []word) string {
	words := texts(args)
	if len(words) == 0 || len(words) > 2 || args[0].quoted {
		return ""
	}
	if len(words) == 2 && (args[1].quoted || !laneDigits.MatchString(words[1])) {
		return ""
	}
	return words[0]
}

// laneDigits is a literal status or loop count.
var laneDigits = regexp.MustCompile(`^[0-9]+$`)

// laneEvalOperands returns the operands of an eval a command runs whose
// words expand, with no redirection among them, or false.
func laneEvalOperands(args []word) ([]word, bool) {
	at := len(args) - len(laneLeading(texts(args)))
	if at >= len(args) || args[at].quoted || args[at].text != "eval" {
		return nil, false
	}
	operands := args[at+1:]
	if !slices.ContainsFunc(operands, func(each word) bool { return laneExpands(each.text) }) ||
		slices.ContainsFunc(operands, func(each word) bool { return redirectionOperator.FindString(each.text) != "" }) {
		return nil, false
	}
	return operands, true
}

// laneEvalSpelling returns the texts an eval may run when each expansion of
// its operands is a number, and the names those expansions read, or false
// when an operand holds any other expansion: one with an operator, a
// substitution, a special or positional parameter, or one this reader cannot
// place, as a word it did not read whole. A number may be empty or signed,
// which changes the words the eval reads, so the reader spells each shape.
func laneEvalSpelling(operands []word) (variants, names []string, ok bool) {
	var raws []string
	for _, each := range operands {
		if each.raw == "" {
			return nil, nil, false
		}
		raws = append(raws, each.raw)
	}
	joined := strings.Join(raws, " ")
	var spans [][2]int
	quote := byte(0)
	for at := 0; at < len(joined); at++ {
		switch c := joined[at]; {
		case quote == '\'':
			if c == '\'' {
				quote = 0
			}
		case c == '\\':
			at++
		case c == '\'' && quote == 0, c == '"' && quote == 0:
			quote = c
		case c == '"':
			quote = 0
		case c == '`':
			return nil, nil, false
		case c == '$':
			match := laneSimpleExpansion.FindStringSubmatch(joined[at:])
			if match == nil || match[1]+match[2] == "_" {
				return nil, nil, false
			}
			names = append(names, match[1]+match[2])
			spans = append(spans, [2]int{at, at + len(match[0])})
			at += len(match[0]) - 1
		}
	}
	if quote != 0 {
		return nil, nil, false
	}
	for _, value := range []string{"0", "-0", "+0", ""} {
		var spelled strings.Builder
		last := 0
		for _, span := range spans {
			spelled.WriteString(joined[last:span[0]])
			spelled.WriteString(value)
			if value != "0" && span[1] < len(joined) && strings.IndexByte("<>", joined[span[1]]) >= 0 {
				spelled.WriteByte(' ') // only digits make a descriptor: bash reads -0<f as a word and then a redirection
			}
			last = span[1]
		}
		spelled.WriteString(joined[last:])
		words, heredocs, open, rest, continues, _ := shellWords(spelled.String(), nil)
		if open != 0 || len(rest) > 0 || continues || len(heredocs) > 0 {
			return nil, nil, false
		}
		variants = append(variants, strings.Join(texts(words), " "))
	}
	return variants, names, true
}

// laneSimpleExpansion is an expansion of a named variable, as $v or ${v}
// spell it, with no operator.
var laneSimpleExpansion = regexp.MustCompile(`^\$(?:([A-Za-z_][A-Za-z0-9_]*)|\{([A-Za-z_][A-Za-z0-9_]*)\})`)
