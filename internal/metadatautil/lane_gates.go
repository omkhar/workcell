// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"slices"
	"strings"
)

// laneGating is what laneGates learns of a body's commands, as laneScan reads
// them: each one's words as laneGateWords gives them, whether its failure
// fails the body, and whether it runs whenever the body runs.
type laneGating struct {
	norms   [][]string
	gates   []bool
	certain []bool
}

// find reports whether a command, as EveryShellCommand gives it, is one the
// body runs where its failure fails the body, and one it runs whenever the
// body does, at some place laneScan reads it. A command the scan does not
// spell as EveryShellCommand does, as one xargs or find runs, is neither.
func (gating laneGating) find(words []string) (gated, certain bool) {
	norm := laneGateWords(words)
	for at, each := range gating.norms {
		if len(norm) > 0 && slices.Equal(each, norm) {
			gated, certain = gated || gating.gates[at], certain || gating.certain[at]
		}
	}
	return gated, certain
}

// laneGateWords returns a command's words past its reserved words,
// redirections, assignments, builtin wrappers and exec, without the ( and )
// a subshell's first and last words carry, as in (cd x && y).
func laneGateWords(words []string) []string {
	words = slices.Clone(words)
	if len(words) > 0 && len(words[0]) > 1 && strings.HasPrefix(words[0], "(") {
		words[0] = words[0][1:]
	}
	if last := len(words) - 1; last >= 0 && len(words[last]) > 1 && strings.HasSuffix(words[last], ")") && !strings.HasSuffix(words[last], "))") {
		words[last] = strings.TrimSuffix(words[last], ")")
	}
	words = laneCommand(laneLeading(laneUnredirected(words)))
	if firstOf(words) == "exec" {
		words = words[1:]
	}
	return words
}

// laneGateState is one command as laneGates reads it.
type laneGateState struct {
	held      []int    // the openers it runs in, by the number laneNesting gave each when it opened
	plain     []string // its words past its reserved words and redirections
	function  int      // the function body it is in, or 0
	lost      bool     // the reader lost track, or eval, trap or a child shell runs it
	negated   bool     // a ! stands in front of its pipeline
	condition bool     // it is in the condition of an if, elif, while or until, or a coproc
	errexit   bool     // errexit is on before it, as the sets before it leave it
	pipefail  bool
}

// laneGates reads a body as bash runs it with errexit and pipefail as given,
// and returns, for each command it runs, whether its failure fails the body
// and whether it runs whenever the body does. A failure fails the body when
// laneGateReader places the command, and errexit is on before it, or it is
// the body's last command, or exec runs it. In a function, some call of the
// function must be such a command, with errexit on before it, or the command
// is the function's last or exec runs it. A set or shopt that may turn
// errexit or pipefail off turns it off from there on, and everywhere when a
// function or a library makes it, and a source of a file other than the
// libraries turns both off, since the file may. Only a set at the top level
// that runs whenever the body does turns one on.
func laneGates(body string, errexit, pipefail bool, libraries []laneLibrary) laneGating {
	scanned, _, complete := laneScan(body)
	gating := laneGating{norms: make([][]string, len(scanned)), gates: make([]bool, len(scanned)), certain: make([]bool, len(scanned))}
	if !complete {
		return gating
	}
	read := laneGateReader{scanned: scanned, states: make([]laneGateState, len(scanned)), off: map[string]bool{}, calls: map[int][2]int{}}
	numbered := 0
	nesting := laneNesting{next: &numbered}
	cases := laneCases{}
	var conditions []bool // for each open if, elif, while or until, true, and for each open for or select, false
	negated, broken := false, false
	for _, library := range libraries {
		for option, value := range laneTurnsOff(library.text) {
			read.off[option] = read.off[option] || !value
		}
	}
	on := map[string]bool{"errexit": errexit, "pipefail": pipefail}
	for at, each := range scanned {
		scopes, lost, words, _ := cases.read(each, &nesting)
		state := laneGateState{held: nesting.at, function: nesting.function(scopes), lost: lost || each.nested, errexit: on["errexit"], pipefail: on["pipefail"]}
		if each.follows != "|" && each.follows != "|&" {
			negated = false
		}
		leading := texts(words)
	reserved:
		for len(leading) > 0 && !words[len(words)-len(leading)].quoted {
			switch word := leading[0]; {
			case word == "then" || word == "do":
				broken = broken || len(conditions) == 0
				conditions = conditions[:max(len(conditions)-1, 0)]
			case word == "if" || word == "elif" || word == "while" || word == "until":
				conditions = append(conditions, true)
			case word == "for" || word == "select":
				conditions = append(conditions, false)
				break reserved
			case word == "!":
				negated = true
			case word == "coproc":
				state.condition = true
			case word == "function" && len(leading) > 1:
				leading = leading[1:]
			case slices.Contains([]string{"else", "{", "}", "fi", "done", "esac", "(", ")", "time", "-p"}, word) || strings.HasSuffix(word, "()") || strings.HasSuffix(word, "(){"):
			case len(leading) > 1 && leading[1] == "()":
				leading = leading[1:]
			default:
				break reserved
			}
			leading = leading[1:]
		}
		state.negated, state.condition = negated, state.condition || slices.Contains(conditions, true)
		state.lost = state.lost || broken
		state.plain = laneLeading(laneUnredirected(texts(words)))
		norm := laneGateWords(texts(words))
		gating.norms[at], read.states[at] = norm, state
		switch firstOf(norm) {
		case "set", "shopt":
			top := len(state.held) == 0 && !state.lost && !each.conditional && !each.piped && !state.condition && !state.negated
			for option, value := range laneSetOptions(norm) {
				read.off[option] = read.off[option] || !value && state.function != 0
				on[option] = value && (on[option] || top)
			}
		case "source", ".":
			if library, fixed := laneLibraryPath(norm); !fixed || !slices.ContainsFunc(libraries, func(each laneLibrary) bool { return each.path == library }) {
				on["errexit"], on["pipefail"] = false, false
			}
		}
	}
	read.closers, read.opens = laneGateClosers(scanned, read.states)
	read.kinds, read.names, read.placings, read.throughs, read.named = nesting.kinds, nesting.names, map[int]bool{}, map[int]int{}, map[string][]int{}
	for at, state := range read.states {
		name := firstOf(laneGateWords(state.plain))
		read.named[name] = append(read.named[name], at)
	}
	for at, each := range scanned {
		state := read.states[at]
		gating.gates[at] = read.gates(at)
		gating.certain[at] = !state.lost && len(state.held) == 0 && !each.conditional
	}
	return gating
}

// laneSetOptions returns each of errexit and pipefail that a set or shopt
// command turns on, as true, or may turn off, as false. An operand this
// reader cannot spell may turn either off.
func laneSetOptions(command []string) map[string]bool {
	options := map[string]bool{}
	unspelled := func(word string) bool { return strings.ContainsAny(word, "$`*?[{") }
	if command[0] == "shopt" {
		flags := strings.Join(slices.DeleteFunc(slices.Clone(command[1:]), func(word string) bool { return !strings.HasPrefix(word, "-") }), "")
		for _, operand := range command[1:] {
			switch {
			case unspelled(operand):
				options["errexit"], options["pipefail"] = false, false
			case (operand == "errexit" || operand == "pipefail") && strings.Contains(flags, "o"):
				options[operand] = !strings.Contains(flags, "u")
			}
		}
		return options
	}
	for at := 1; at < len(command); at++ {
		operand := command[at]
		switch {
		case unspelled(operand):
			options["errexit"], options["pipefail"] = false, false
		case operand == "-" || operand == "--" || !strings.HasPrefix(operand, "-") && !strings.HasPrefix(operand, "+"):
			return options // the rest are positional parameters
		default:
			for _, letter := range operand[1:] {
				switch {
				case letter == 'e':
					options["errexit"] = operand[0] == '-'
				case letter == 'o' && at+1 < len(command):
					at++
					switch name := command[at]; {
					case name == "errexit" || name == "pipefail":
						options[name] = operand[0] == '-'
					case unspelled(name):
						options["errexit"], options["pipefail"] = false, false
					}
				}
			}
		}
	}
	return options
}

// laneTurnsOff returns what laneSetOptions reads of each set or shopt a text
// runs, the last for each option, and false for both when the text is past
// what laneScan reads.
func laneTurnsOff(text string) map[string]bool {
	scanned, _, complete := laneScan(text)
	options := map[string]bool{}
	if !complete {
		return map[string]bool{"errexit": false, "pipefail": false}
	}
	for _, each := range scanned {
		norm := laneGateWords(texts(laneUnredirectedWords(each.args)))
		if first := firstOf(norm); first == "set" || first == "shopt" {
			for option, value := range laneSetOptions(norm) {
				options[option] = options[option] && value || !has(options, option) && value
			}
		}
	}
	return options
}

// has reports whether options holds name.
func has(options map[string]bool, name string) bool {
	_, found := options[name]
	return found
}

// laneGateClosers returns, for each opener by its number, the index of the
// command that closes it: the first command past it that no longer runs in
// it, or the one before, when that one ends with the ) that closes a
// subshell; and the index of the first command that runs in it. An opener
// that never closes maps to -1. Each command's openers are a stack whose
// numbers rise, so the openers still open are the longest start of the
// last command's that the command keeps, and the work is linear.
func laneGateClosers(scanned []command, states []laneGateState) (closers, opens map[int]int) {
	closers, opens = map[int]int{}, map[int]int{}
	var open []int
	for at, state := range states {
		kept := len(open)
		for ; kept > 0; kept-- {
			if _, found := slices.BinarySearch(state.held, open[kept-1]); found {
				break
			}
		}
		for _, held := range open[kept:] {
			closers[held] = at
			if args := scanned[at].args; len(args) == 0 || !slices.Contains([]string{"}", ")", "fi", "done", "esac"}, args[0].text) {
				closers[held] = at - 1
			}
		}
		start := 0
		if open = open[:kept]; kept > 0 {
			start, _ = slices.BinarySearch(state.held, open[kept-1])
			start++
		}
		for _, held := range state.held[start:] {
			closers[held], opens[held], open = -1, at, append(open, held)
		}
	}
	return closers, opens
}

// laneGateReader decides, for each command, whether its failure fails the
// body, as laneGates says.
type laneGateReader struct {
	scanned      []command
	states       []laneGateState
	closers      map[int]int
	opens        map[int]int // the first command in each opener
	placings     map[int]bool
	throughs     map[int]int      // through for each opener
	named        map[string][]int // the commands each command word runs
	off          map[string]bool  // the options a function or a library may turn off
	calls        map[int][2]int   // for each function: whether a call of it gates, with errexit on and at all, as 1 for no and 2 for yes
	kinds, names map[int]string
}

// placed reports whether nothing at the command's place masks its status:
// it is not under ! or in a condition, passes lets its status through, and
// each opener around it inside its function is a command substitution whose
// last command it is, of the one value of a plain assignment that is placed
// itself, or closes where passes lets the status through.
func (read laneGateReader) placed(at int) bool {
	if placed, found := read.placings[at]; found {
		return placed
	}
	placed := read.placing(at)
	read.placings[at] = placed
	return placed
}

// placing is placed without the memo.
func (read laneGateReader) placing(at int) bool {
	state := read.states[at]
	if state.lost || len(state.plain) == 0 || state.negated || state.condition || !read.passes(at) {
		return false
	}
	need := read.through(state.held)
	return need == -1 || need == at
}

// through reads the openers a command runs in, a stack, from its function
// body in, and returns -2 when one of them masks the status, or else the
// command that must be the last of the innermost command substitution
// among them, or -1 when none is one. A substitution starts afresh, since
// placed(host) has read the openers around its host. Each opener's answer
// holds for every command in it, so it is kept, and the work is linear.
func (read laneGateReader) through(held []int) int {
	end := len(held)
	for ; end > 0; end-- {
		if _, found := read.throughs[held[end-1]]; found || read.kinds[held[end-1]] == "function" {
			break
		}
	}
	need := -1
	if end > 0 && read.kinds[held[end-1]] != "function" {
		need = read.throughs[held[end-1]]
	}
	for _, opener := range held[end:] {
		closer := read.closers[opener]
		switch {
		case closer < 0:
			need = -2
		case read.kinds[opener] == "(" && read.substitution(opener):
			// placed(host) already reads every opener around the host,
			// so the substitution's last command is all that is needed.
			host := read.next(closer)
			need = read.last(closer)
			if assignmentPrefix(read.states[host].plain) != len(read.states[host].plain) ||
				strings.Count(strings.Join(read.states[host].plain, " "), "$_") != 1 || !read.placed(host) {
				need = -2
			}
		case !read.passes(closer):
			need = -2
		}
		read.throughs[opener] = need
	}
	return need
}

// passes reports whether the operator after a command lets its status
// through: none, ;, or a | under pipefail.
func (read laneGateReader) passes(at int) bool {
	after := ""
	if at+1 < len(read.scanned) {
		after = read.scanned[at+1].follows
	}
	switch after {
	case "&&", "||", "&":
		return false
	case "|", "|&":
		return read.states[at].pipefail && !read.off["pipefail"]
	}
	return true
}

// substitution reports whether the subshell held is a command substitution
// the shell reader moved in front of its command: it opens and closes on
// lines of their own, and the command after it reads its value as $_.
func (read laneGateReader) substitution(held int) bool {
	open, closer := read.opens[held], read.closers[held]
	host := read.next(closer)
	return slices.Equal(texts(read.scanned[open].args), []string{"("}) && slices.Equal(texts(read.scanned[closer].args), []string{")"}) &&
		host >= 0 && strings.Contains(strings.Join(read.states[host].plain, " "), "$_")
}

// final reports whether a command's status is the status of what holds it,
// up to the command at end, or the body's end for -1: no command with words
// runs after it before end, and no loop it is in inside its function may run
// another command after it.
func (read laneGateReader) final(at, end int) bool {
	if next := read.next(at); next >= 0 && (end < 0 || next <= end) {
		return false
	}
	state := read.states[at]
	for _, held := range state.held[slices.Index(state.held, state.function)+1:] {
		if read.kinds[held] == "do" {
			return false
		}
	}
	return true
}

// next returns the first command past at that has words, or -1.
func (read laneGateReader) next(at int) int {
	for at++; at < len(read.scanned); at++ {
		if len(read.states[at].plain) > 0 {
			return at
		}
	}
	return -1
}

// last returns the last command before closer that has words, or -1.
func (read laneGateReader) last(closer int) int {
	for at := closer - 1; at >= 0; at-- {
		if len(read.states[at].plain) > 0 {
			return at
		}
	}
	return -1
}

// gates reports whether a command's failure fails the body.
func (read laneGateReader) gates(at int) bool {
	state := read.states[at]
	if !read.placed(at) {
		return false
	}
	ends := firstOf(state.plain[assignmentPrefix(state.plain):]) == "exec" // exec ends the shell with the command's status
	errexit := state.errexit && !read.off["errexit"]
	if state.function == 0 {
		return errexit || ends || read.final(at, -1)
	}
	function := state.function
	if read.calls[function] == [2]int{} {
		read.calls[function] = [2]int{1, 1} // a call in the function itself does not gate
		calls := [2]int{1, 1}
		for _, call := range read.named[read.names[function]] {
			if read.gates(call) {
				calls[1] = 2
				if read.states[call].errexit && !read.off["errexit"] {
					calls[0] = 2
				}
			}
		}
		read.calls[function] = calls
	}
	last := read.closers[function] >= 0 && read.final(at, read.closers[function])
	return read.calls[function][0] == 2 || (last || ends) && read.calls[function][1] == 2
}
