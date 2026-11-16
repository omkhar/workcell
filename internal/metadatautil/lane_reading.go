// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"cmp"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// The lane reader models a fixed set of bash features and fails closed on
// every other one, so a construct it has never seen cannot keep a script
// wired. It models:
//
//   - a command word that is a literal program name, a function the lane or a
//     library defines, or a builtin in laneModeled or laneInert, whose
//     operands it reads by that builtin's rule;
//   - a command word that expands a variable, when bash cannot split it and
//     the word is a path, as a literal slash outside its expansions makes it,
//     or the variable holds only literal program names;
//   - an assignment to a name it can spell, and the literal value it assigns;
//   - the literal text an eval, a trap action or a child shell runs, read as
//     the lane is, up to eight deep;
//   - a source of a fixed ${ROOT_DIR}/ library, read as part of the lane.
//
// Anything else that may change what a later command runs is unreadable: an
// unmodeled builtin, an option or a name an expansion spells, a command word
// it cannot prove, and a write to a variable bash reads to decide what runs.

// laneModeled are the builtins whose effect on a later command this reader
// models, each with the reason it cannot read one use, or "".
var laneModeled = map[string]func(args []string, read map[string]bool) string{
	"hash": func(args []string, _ map[string]bool) string {
		if reason := laneLiteralOptions(args, "pt"); reason != "" {
			return reason
		}
		if laneOption(args, "p") {
			return "binds a name to a path, as hash -p does"
		}
		return ""
	},
	"set": func(args []string, _ map[string]bool) string {
		if slices.ContainsFunc(args, laneExpands) {
			return "sets options or parameters an expansion spells"
		}
		return laneStopsReason(args)
	},
	"shopt": func(args []string, _ map[string]bool) string {
		if slices.ContainsFunc(args, laneExpands) {
			return "sets options an expansion spells"
		}
		if laneOption(args, "s") && slices.Contains(args, "extdebug") {
			return "lets a DEBUG trap skip a later command, as shopt -s extdebug does"
		}
		if laneOption(args, "o") && (slices.Contains(args, "noexec") || slices.Contains(args, "onecmd")) {
			return "stops bash running a later command, as shopt -so noexec does"
		}
		if laneOption(args, "o") {
			return "sets a set -o option, as set would, which this reader reads only from set"
		}
		if slices.Contains(args, "lastpipe") {
			return "runs the last command of a pipeline in this shell, as shopt -s lastpipe does, where an exit in it ends the lane"
		}
		return ""
	},
	"declare": laneDeclaration, "typeset": laneDeclaration, "local": laneDeclaration,
	"export": laneDeclaration, "readonly": laneDeclaration,
	"read":    func(args []string, _ map[string]bool) string { return laneLiteralOptions(args, "adinNptu") },
	"printf":  func(args []string, _ map[string]bool) string { return laneLiteralOptions(args, "v") },
	"mapfile": laneMapfile, "readarray": laneMapfile,
	"eval": func(args []string, _ map[string]bool) string {
		if slices.ContainsFunc(args, laneExpands) {
			return "evals text this reader cannot spell"
		}
		return ""
	},
	"trap": func(args []string, _ map[string]bool) string {
		if len(args) > 0 && args[0] == "--" {
			args = args[1:]
		}
		if len(args) > 1 && args[0] != "" && args[0] != "-" && slices.ContainsFunc(args[1:], func(signal string) bool {
			return !laneExitSignal(signal)
		}) {
			return "sets a trap on a signal other than EXIT, whose action may run before a later command and end the lane"
		}
		return ""
	},
	"source": laneSourceReason, ".": laneSourceReason,
	"compgen": func(args []string, _ map[string]bool) string {
		if reason := laneLiteralOptions(args, "oAGWFCXPS"); reason != "" {
			return reason
		}
		if laneOption(args, "FC") {
			return "runs a function or a command to complete words, as compgen -F or -C does"
		}
		return ""
	},
}

// laneInert are the builtins and reserved words no use of which changes what
// a later command runs, past the variables they assign, which laneWrites
// reads. exec runs a program only, never a builtin or a function.
var laneInert = strings.Fields(`: [ true false echo test pwd exit return break continue shift umask ulimit
	jobs times wait type unset cd pushd popd dirs exec caller help disown bg fg getopts let builtin
	command time if then else elif fi case esac for select while until do done in function { } ! [[ ]] coproc`)

// laneEnders are the builtins and programs that may signal the lane's own
// shell, as kill -9 $$ does, or stop or end it, so that bash never runs a later
// command: this reader cannot prove which process they reach.
var laneEnders = strings.Fields(`kill pkill killall logout suspend`)

// laneStopsReason returns why a set may stop bash running a later command,
// as set -n, set -t, set -o noexec and set -o onecmd do, or export each
// function a later line defines, as set -a does, or "".
func laneStopsReason(args []string) string {
	if laneOption(args, "nt") || slices.Contains(args, "noexec") || slices.Contains(args, "onecmd") {
		return "stops bash running a later command, as set -n or set -t does"
	}
	if laneOption(args, "a") || slices.Contains(args, "allexport") {
		return "exports each function a later line defines, as set -a does, which a child bash calls in place of the program it names"
	}
	return ""
}

// laneExitSignal reports whether a trap's signal is EXIT, whose action runs
// as the shell ends.
func laneExitSignal(signal string) bool {
	return slices.Contains([]string{"EXIT", "SIGEXIT", "0"}, strings.ToUpper(signal))
}

// laneDeclaration reads declare, typeset, local, export and readonly: each
// option must be literal, a nameref is unreadable, since every write through
// it assigns the name it holds, and each name must be one this reader can
// spell, with no expansion and no brace group, as export P{A,X}TH= has.
func laneDeclaration(args []string, _ map[string]bool) string {
	if reason := laneLiteralOptions(args, ""); reason != "" {
		return reason
	}
	if laneOption(args, "n") {
		return "declares a nameref, through which a write assigns the name it holds"
	}
	if slices.ContainsFunc(args, func(arg string) bool {
		return len(arg) > 1 && (arg[0] == '-' || arg[0] == '+') && strings.Contains(arg, "i")
	}) {
		return "declares an integer variable, whose every assignment bash evaluates as arithmetic"
	}
	for _, arg := range args {
		if name, _, _ := strings.Cut(arg, "="); arg[0] != '-' && arg[0] != '+' && !laneDeclaredName.MatchString(name) {
			return "declares a name this reader cannot spell"
		}
	}
	return ""
}

// laneDeclaredName is a name a declaration may assign: an identifier, an
// optional subscript and the + of +=.
var laneDeclaredName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(?:\[[^]]*\])?\+?$`)

// laneMapfile reads mapfile and readarray, whose -C callback runs text.
func laneMapfile(args []string, _ map[string]bool) string {
	if reason := laneLiteralOptions(args, "dnOsuCc"); reason != "" {
		return reason
	}
	if laneOption(args, "C") {
		return "runs a callback this reader cannot spell"
	}
	return ""
}

// laneSourceReason reads a source, which must name a fixed ${ROOT_DIR}/
// library the reader has read.
func laneSourceReason(args []string, read map[string]bool) string {
	library, fixed := laneLibraryPath(append([]string{"source"}, args...))
	switch {
	case !fixed:
		return "sources a file this reader cannot name"
	case !read[library]:
		return "sources a library this reader has not read"
	}
	return ""
}

// laneExpands reports whether a word holds an expansion bash makes.
func laneExpands(word string) bool { return strings.ContainsAny(word, "$`") }

// laneOption reports whether an option word among args holds a letter.
func laneOption(args []string, letters string) bool {
	return slices.ContainsFunc(args, func(arg string) bool { return strings.HasPrefix(arg, "-") && strings.ContainsAny(arg[1:], letters) })
}

// laneLiteralOptions returns why a builtin's options are unreadable: a word
// in option position, before -- and the first operand, that holds an
// expansion, which may spell any option. The value of an option in valued,
// the next word when the option ends its word, may hold one: laneWrites reads
// it when it names a variable.
func laneLiteralOptions(args []string, valued string) string {
	for at := 0; at < len(args); at++ {
		switch arg := args[at]; {
		case arg == "--":
			return ""
		case strings.HasPrefix(arg, "$") || strings.HasPrefix(arg, "`"):
			return "takes an option an expansion spells"
		case arg == "" || arg[0] != '-' && arg[0] != '+':
			return ""
		case laneExpands(arg):
			return "takes an option an expansion spells"
		case strings.ContainsRune(valued, rune(arg[len(arg)-1])) && strings.IndexAny(arg[1:], valued) == len(arg)-2:
			at++
		}
	}
	return ""
}

// laneReading is what the reader learns from every command a lane and its
// libraries may run before it reads any call.
type laneReading struct {
	read       map[string]bool        // the libraries the lane sources
	functions  map[string]bool        // the functions the lane and its libraries define
	writes     map[string][]laneWrite // each variable's writes
	split      map[string]bool        // each word text seen, true when some use of it leaves an expansion, a glob or a brace group unquoted
	computed   map[string]string      // each name word an expansion computes, with a command that assigns through it
	inherited  map[string]bool        // each variable a command may read before the lane assigns it
	pinned     bool                   // ROOT_DIR is the repository root
	local      map[string][]laneWrite // the writes to each function's local, by laneLocalKey
	global     map[string][]laneWrite // each variable's writes outside a function that declares it local
	evals      []laneEval             // each eval whose operands expand
	spelled    map[string]bool        // the operands of each eval every run of which expands only numbers
	programs   map[string][]int       // for each word that is one expansion of a variable and each function's name, the bodies of that name that declare the variable local where the word stands
	open       map[string]bool        // each such word and name where the word stands somewhere its variable is no local of the body
	programmed map[string][]laneWrite // the writes programWrites returns for each such word and name, once read
	caller     string                 // the function whose body holds the call being read
	lapsed     map[string]bool        // the operands of each eval unproven where a guard of a name it expands lapsed
	unsetBy    []string               // each variable an unset reads the names it removes from
	bared      []string               // the prefixes of the names such an unset may remove, past the names it lists
}

// laneEval is an eval whose operands expand: its operands, the names they
// expand, if they expand only names, and where it runs.
type laneEval struct {
	operands string
	names    []string
	spelled  bool
	context  *laneContext
	lapsed   bool // a name it expands lost its guard to laneGuardLimit
}

// laneReadable reports, as an error, why this reader cannot show what a lane
// runs, after reading the lane and every library it sources, and whether the
// lane assigns PATH, BASH_ENV or ENV, which may change the program any later
// command runs, a script's own #! interpreter included. It first reads every
// command, as written, of the lane, its libraries and the text each runs, for
// the functions they define, the variables they write and the child shells
// they run, and then each call as EveryShellCommand reads it.
func laneReadable(body string, libraries []laneLibrary, pinned bool, site laneSite) (startup bool, err error) {
	bodies := []string{body}
	reading := laneReading{read: map[string]bool{}, functions: map[string]bool{}, writes: map[string][]laneWrite{}, split: map[string]bool{}, computed: map[string]string{}, pinned: pinned,
		local: map[string][]laneWrite{}, global: map[string][]laneWrite{}, spelled: map[string]bool{}, programs: map[string][]int{}, open: map[string]bool{},
		programmed: map[string][]laneWrite{}, lapsed: map[string]bool{}}
	for _, library := range libraries {
		bodies, reading.read[library.path] = append(bodies, library.text), true
	}
	var calls []laneCall
	var arithmetic []*laneArithmetic // each body's
	var indirect []string            // each variable an indirect expansion reads a name from
	events, sourced, numbered := make([][]laneEvent, len(bodies)), map[string]int{}, 0
	for at, library := range libraries {
		sourced[library.path] = at + 1
	}
	stable, forwarders := map[string]bool{}, map[string]bool{}
	var counted []laneCall          // each command once per definition that holds it, which the eval count reads
	bodied := map[string][]string{} // each function's body texts
	ends := laneEnds{tops: map[string][]laneTop{}, where: map[string]string{}, sourced: sourced, functions: reading.functions, forked: map[string]bool{}}
	var exitActions []string // the action of each EXIT trap
	opened := map[int]bool{} // whether the command that opens each scope runs whenever its body does
	var own *laneOwnCase     // the lane script's cases on its own $1
	if site.lane {
		own = &laneOwnCase{}
	}
	for at, text := range bodies {
		where := laneWhere(at)
		if _, ended := rewriteHeredocsEnding(text); !ended {
			return false, fmt.Errorf("lane wiring is unreadable: a %s opens a heredoc whose end this reader cannot find, so it cannot read what runs after it", where)
		}
		scanned, children, complete, unended := laneScanCases(text)
		if !complete {
			return false, fmt.Errorf("lane wiring is unreadable: a %s nests eval, trap or child shell text more than eight deep, or holds more text than this reader parses", where)
		}
		if unended {
			return false, fmt.Errorf("lane wiring is unreadable: a %s has a substitution that holds a case with no esac after it, so the ) of a pattern may end the substitution where bash does not", where)
		}
		for _, each := range append([]string{text}, children...) {
			names, complete := definedFunctionNames(each)
			if !complete {
				return false, fmt.Errorf("lane wiring is unreadable: a %s nests eval or trap text more than eight deep, or holds more text than this reader parses", where)
			}
			for _, name := range names {
				if shadowsLaneProgram(name) {
					return false, fmt.Errorf("lane wiring is unreadable: a %s defines a function named %q, which bash calls instead of the program", where, name)
				}
				if slices.Contains(laneBuiltins, name) { // as return() { :; } makes a guard's return go on
					return false, fmt.Errorf("lane wiring is unreadable: a %s defines a function named %q, which bash calls instead of the builtin this reader reads", where, name)
				}
				reading.functions[name] = true
			}
		}
		if reason := laneOperatorReason(text); reason != "" {
			return false, fmt.Errorf("lane wiring is unreadable: a %s %s", where, reason)
		}
		names, opaque := laneIndirections(text)
		if opaque {
			return false, fmt.Errorf("lane wiring is unreadable: a %s expands a variable whose name a parameter or an expansion computes", where)
		}
		indirect = append(indirect, names...)
		if laneArithmeticAssigns.MatchString(strings.ReplaceAll(text, "\\\n", "")) {
			return false, fmt.Errorf("lane wiring is unreadable: a %s assigns in arithmetic to a variable whose name this reader cannot spell, or that bash reads to decide what runs", where)
		}
		switch named, opaque := namesShellVariable(text, laneHashTables.MatchString); {
		case opaque:
			return false, errLaneBraces
		case named:
			return false, fmt.Errorf("lane wiring is unreadable: a %s names BASH_CMDS or BASH_ALIASES, which may rebind the scripts it runs", where)
		}
		switch named, opaque := namesShellVariable(text, laneLocalInherit.MatchString); {
		case opaque:
			return false, errLaneBraces
		case named:
			return false, fmt.Errorf("lane wiring is unreadable: a %s names localvar_inherit, under which a local keeps a value no write this reader counts assigned", where)
		}
		arithmetic = append(arithmetic, newLaneArithmetic(len(text)))
		nesting := laneNesting{next: &numbered}
		locals := map[int]map[string]int{} // the names each function body declares local, in order
		var guards []laneGuard             // the guards in force
		var lapsed []laneGuard             // the guards past laneGuardLimit, which no longer count
		var pending *laneGuard             // a test for digits the next command may make a guard
		heading := map[int]bool{}          // the if scopes whose condition the reader is in, which runs whenever the if does
		cases := laneCases{env: site.env, events: site.events, run: body, own: own, top: at == 0}
		for _, each := range scanned {
			args := each.args
			held := len(nesting.scopes) // the scopes open before the command
			scopes, lost, run, parsed := cases.read(each, &nesting)
			function := nesting.function(scopes)
			outside := func(guard laneGuard) bool { return !slices.Contains(scopes, guard.scope) || guard.function != function }
			guards, lapsed = slices.DeleteFunc(guards, outside), slices.DeleteFunc(lapsed, outside)
			var context *laneContext // text an eval, a trap or a child shell runs reads a number by its name alone
			if !each.nested {
				context = &laneContext{function: function, locals: locals[function], declared: len(locals[function])}
				for _, guard := range guards {
					context.guarded = append(context.guarded, guard.name)
				}
			}
			read := len(arithmetic[at].expressions)
			arithmetic[at].read(args)
			for index := read; index < len(arithmetic[at].expressions); index++ {
				arithmetic[at].expressions[index].context = context
			}
			words := texts(args)
			alone := !each.nested && (!each.piped || parsed) // a command that runs in this shell, where a | in a pattern list is no pipeline
			certain := alone && !each.conditional && !lost
			key := fmt.Sprint(at) // the body the command runs in
			if function != 0 {
				key = "function " + nesting.names[function]
			}
			if each.piped && len(scopes) != held || slices.ContainsFunc(args, func(arg word) bool { return !arg.quoted && arg.text == "coproc" }) {
				ends.forked[key] = true // a group or compound command in a pipeline, behind & or in a coproc runs in a subshell, where a return returns from nothing
			}
			if len(scopes) > 0 {
				last := scopes[len(scopes)-1]
				if _, seen := opened[last]; !seen && firstOf(words) == "if" {
					heading[last] = true
				}
				if firstOf(words) == "then" {
					heading[last] = false
				}
			}
			for _, scope := range scopes { // a group the command opens runs only when the command would, as cmd || { exit 1; } does
				if _, seen := opened[scope]; !seen {
					opened[scope] = certain || nesting.kinds[scope] == "case"
				}
			}
			command := laneCommand(laneLeading(laneUnredirected(texts(run))))
			if function == 0 {
				cases.shift(command, scopes, certain)
			}
			if len(command) > 0 && !each.nested { // each command the body runs, which may follow one that ends it
				inside := scopes[slices.Index(scopes, function)+1:]
				top := laneTop{command: command}
				switch {
				case !alone:
				case command[0] == "return": // a body may return before any exit after it, but not from a subshell
					top.placed = !lost && !slices.ContainsFunc(inside, func(scope int) bool { return nesting.kinds[scope] == "(" })
					top.certain = top.placed && !each.conditional && len(inside) == 0
				case !each.conditional: // where the reader lost track, any command may run
					named, branch := cases.condition(function)
					top.placed = lost || !slices.ContainsFunc(inside, func(scope int) bool { return nesting.kinds[scope] != "{" || !opened[scope] })
					top.may = lost || !named && !slices.ContainsFunc(inside, func(scope int) bool {
						return !opened[scope] || nesting.kinds[scope] == "(" || nesting.kinds[scope] == "if" && !heading[scope]
					})
					if top.may && branch != nil { // a branch on $1 runs only where a caller selects it
						own.patches, top.may = append(own.patches, laneOwnPatch{key, len(ends.tops[key]), branch}), false
					}
				}
				if ends.tops[key] == nil {
					ends.order = append(ends.order, key)
					ends.where[key] = map[bool]string{true: key, false: where}[function != 0]
				}
				ends.tops[key] = append(ends.tops[key], top)
			}
			if command := laneTrapCommand(words); len(command) > 2 && slices.ContainsFunc(command[2:], laneExitSignal) {
				exitActions = append(exitActions, command[1])
			}
			event := laneEvent{reads: laneReads(args, arithmetic[at].expressions[read:]), scopes: scopes, certain: !each.conditional && !lost}
			if operands, found := laneEvalOperands(args); found {
				variants, names, spelled := laneEvalSpelling(operands)
				lapse := slices.ContainsFunc(lapsed, func(guard laneGuard) bool { return slices.Contains(names, guard.name) })
				reading.evals = append(reading.evals, laneEval{strings.Join(texts(operands), "\x00"), names, spelled, context, lapse})
				for _, variant := range variants {
					for _, words := range laneCommands(variant)[0] {
						calls = append(calls, laneCall{words: words})
					}
				}
			} else {
				for _, key := range laneEvalSites(args) { // an eval by any other spelling is never proven
					reading.evals = append(reading.evals, laneEval{operands: key})
				}
			}
			if names := laneLocalNames(laneCommand(laneLeading(laneUnredirected(words)))); context != nil && !each.piped && event.certain &&
				function != 0 && scopes[len(scopes)-1] == function {
				if locals[function] == nil {
					locals[function] = map[string]int{}
				}
				for _, name := range names {
					if _, declared := locals[function][name]; !declared {
						locals[function][name] = len(locals[function])
					}
				}
				context = &laneContext{function, locals[function], len(locals[function]), context.guarded} // its own writes assign its locals
			}
			if library, fixed := laneLibraryPath(words); fixed {
				event.sources = sourced[library]
			}
			for _, each := range args {
				reading.split[each.text] = reading.split[each.text] || laneUnquotedRisk(each)
				if variable := laneProgramVariable.FindStringSubmatch(each.text); variable != nil {
					key := each.text + "\x00" + nesting.names[function]
					if bodies := reading.programs[key]; context != nil && context.local(variable[1]+variable[2]) {
						if len(bodies) == 0 || bodies[len(bodies)-1] != function {
							reading.programs[key] = append(bodies, function)
						}
					} else {
						reading.open[key] = true
					}
				}
			}
			if laneChildNamesRoot(words) {
				return false, fmt.Errorf("lane wiring is unreadable: a %s runs %q, a child shell whose own program names ROOT_DIR, which the child does not inherit pinned", where, strings.Join(words, " "))
			}
			writes, computed, opaque := laneScanWrites(args)
			if opaque {
				return false, errLaneBraces
			}
			kept := !each.nested && !each.piped && laneAssigns(words) // only such a write leaves its value in this shell
			for _, name := range computed {
				reading.computed[name] = strings.Join(words, " ")
			}
			for _, write := range writes {
				if write.names {
					reading.unsetBy = append(reading.unsetBy, write.name)
					continue
				}
				bares := write.unset && write.name != "PATH" // only a lookup in a missing PATH changes what a name runs
				switch {
				case bares:
				case slices.Contains(laneShellNames, write.name):
					return false, fmt.Errorf("lane wiring is unreadable: a %s runs %q, which assigns %s, a variable bash reads to decide what runs", where, strings.Join(words, " "), write.name)
				case slices.Contains(laneStartupNames, write.name) && at > 0:
					return false, fmt.Errorf("lane wiring is unreadable: a library a lane sources runs %q, which assigns %s and may change the program a script runs", strings.Join(words, " "), write.name)
				}
				startup = startup || !bares && slices.Contains(laneStartupNames, write.name)
				reading.writes[write.name] = append(reading.writes[write.name], write)
				if context != nil && context.local(write.name) {
					key := laneLocalKey(write.name, function)
					reading.local[key] = append(reading.local[key], write)
				} else {
					reading.global[write.name] = append(reading.global[write.name], write)
				}
				written := func(guard laneGuard) bool { return guard.name == write.name }
				guards, lapsed = slices.DeleteFunc(guards, written), slices.DeleteFunc(lapsed, written)
				if kept && !write.shaped { // a word shaped like an assignment may be text an eval, a trap or a child shell runs
					event.writes = append(event.writes, write.name)
				}
			}
			events[at] = append(events[at], event)
			if exit := laneGuardExit(args); pending != nil && context != nil && each.orElse && !each.piped && exit != "" &&
				len(scopes) > 0 && scopes[len(scopes)-1] == pending.scope && function == pending.function &&
				(exit == "continue" && nesting.kinds[pending.scope] == "do" || exit == "return" && pending.scope == function) {
				if len(guards) == laneGuardLimit {
					lapsed, guards = append(lapsed, guards[0]), guards[1:] // the oldest guard lapses, which only fails closed
				}
				guards = append(guards, *pending)
			}
			if len(args) > 0 { // an empty command ends a line after ||, whose next line runs the exit
				pending = nil
			}
			if name := laneGuardTest(args); name != "" && context != nil && !each.piped && event.certain && len(scopes) > 0 &&
				context.local(name) {
				pending = &laneGuard{name: name, scope: scopes[len(scopes)-1], function: function}
			}
		}
		parts, complete := libraryParts(text)
		if !complete {
			return false, fmt.Errorf("lane wiring is unreadable: a lane nests function definitions deeper than this reader parses")
		}
		if defined, complete := evalDefinedFunctionNames(text); len(defined) > 0 || !complete { // a body in eval or trap text is one functionBodies never binds
			return false, fmt.Errorf("lane wiring is unreadable: a %s defines a function in text an eval or a trap runs, whose body this reader does not read", where)
		}
		for _, part := range parts {
			bodied[part.function] = append(bodied[part.function], part.text)
			for _, words := range part.commands {
				counted = append(counted, laneCall{words, part.function})
			}
		}
		if at == 0 {
			for _, words := range laneCommands(body)[0] { // the lane runs its own commands as EveryShellCommand reads them
				calls = append(calls, laneCall{words: words})
			}
			continue
		}
		for _, part := range parts {
			stable[part.function], forwarders[part.function] = laneBodyRuns(part.text)
			forwarders[part.function] = forwarders[part.function] && part.function != ""
			for _, words := range part.commands {
				calls = append(calls, laneCall{words, part.function})
			}
		}
	}
	if own != nil {
		safe := own.safe(site.callers)
		for _, patch := range own.patches {
			ends.tops[patch.key][patch.index].may = !safe || !patch.branch.literal || patch.branch.selected
		}
	}
	reading.unsetsListed()
	outputs, err := reading.outputs(calls, stable)
	if err != nil {
		return false, err
	}
	reading.inherited = laneInherited(events)
	numbers, listed := reading.numbers(), laneListed(reading.writes, reading.inherited)
	sites := map[string]int{} // the evals the reader read of each text
	for _, each := range reading.evals {
		proven := each.spelled && !slices.ContainsFunc(each.names, func(name string) bool { return !numbers(each.context)(name) })
		spelled, seen := reading.spelled[each.operands]
		reading.spelled[each.operands] = proven && (spelled || !seen)
		reading.lapsed[each.operands] = reading.lapsed[each.operands] || !proven && each.lapsed
		sites[each.operands]++
	}
	for _, call := range counted { // an eval of a text the reader did not read where it stands is never proven
		if command := laneCommand(laneUnredirectedFront(call.words)); firstOf(command) == "eval" {
			key := strings.Join(command[1:], "\x00")
			if sites[key]--; sites[key] < 0 {
				reading.spelled[key] = false
			}
		}
	}
	for _, name := range indirect {
		if !listed[name] {
			return false, fmt.Errorf("lane wiring is unreadable: a lane expands ${!%s}, whose name %s may spell an array element whose subscript bash evaluates", name, name)
		}
	}
	for at, each := range arithmetic {
		if each.budget < 0 {
			return false, fmt.Errorf("lane wiring is unreadable: a %s nests more expansions in a word than this reader parses", laneWhere(at))
		}
		for _, expression := range each.remaining() {
			reason := arithmeticReason(expression.text, numbers(expression.context))
			if expression.named {
				reason = laneNamedReason(expression.text, numbers(expression.context), listed)
			}
			if reason != "" {
				return false, fmt.Errorf("lane wiring is unreadable: a %s %s", laneWhere(at), reason)
			}
		}
	}
	for _, call := range calls {
		reading.caller = call.function
		if slices.Equal(call.words, shellReadBound) {
			return false, fmt.Errorf("lane wiring is unreadable: a lane runs %q, which stands for text past what the shell reader reads", strings.Join(call.words, " "))
		}
		command := laneCommand(laneUnredirectedFront(call.words))
		if forwarders[call.function] && slices.Equal(command, []string{"$@"}) {
			continue // each call of the forwarder is read as the command it forwards
		}
		reason := reading.callReason(command)
		if len(command) > 0 && forwarders[command[0]] && reason == "" {
			reason = reading.forwardedReason(command[1:])
		}
		for _, at := range outputs[firstOf(command)] {
			if name := strings.Join(command[min(at, len(command)):min(at+1, len(command))], ""); !laneOutputName(name) {
				reason = fmt.Sprintf("names %q for a variable %s assigns, which bash may read to decide what runs", name, command[0])
			}
		}
		if reason != "" {
			return false, fmt.Errorf("lane wiring is unreadable: a lane runs %q, which %s", strings.Join(call.words, " "), reason)
		}
	}
	if key, command := ends.ended(); command != nil {
		return false, fmt.Errorf("lane wiring is unreadable: a %s runs %q, which ends the lane's shell before a later command", key, strings.Join(command, " "))
	}
	for _, action := range exitActions {
		if laneMasks(action, bodied, map[string]bool{}) {
			return false, fmt.Errorf("lane wiring is unreadable: a lane sets an EXIT trap whose action may replace the status the lane ends with, as an exit 0, a later bare exit or an exec does, which may turn a failure before a later command into success")
		}
	}
	return startup, nil
}

// unsetsListed reads each unset "${name}": a literal name name holds is a
// write that removes that variable, and a listing ${!P@} may remove any name
// that starts with P, as may any other value, which laneNamedReason refuses.
func (reading *laneReading) unsetsListed() {
	for _, variable := range reading.unsetBy {
		for _, write := range slices.Clone(reading.writes[variable]) {
			listing := laneNamesExpansion.FindStringSubmatch(write.value)
			switch {
			case write.literal && write.value == "": // unset of nothing
			case write.literal && laneVariableName.MatchString(write.value):
				bared := laneWrite{name: write.value, unset: true}
				reading.writes[bared.name], reading.global[bared.name] = append(reading.writes[bared.name], bared), append(reading.global[bared.name], bared)
			case listing != nil:
				reading.bared = append(reading.bared, listing[1])
			default:
				reading.bared = append(reading.bared, "")
			}
		}
	}
}

// bares reports whether an unset may remove a variable by a name it reads.
func (reading laneReading) bares(name string) bool {
	return slices.ContainsFunc(reading.bared, func(prefix string) bool { return strings.HasPrefix(name, prefix) })
}

// numbers returns, for where a command runs, whether a variable holds a
// number there, as laneNumbers reads it: a function's local by its own
// writes and every write outside a function that declares it local, and a
// guarded local when no such write exists, so no call can change it.
func (reading laneReading) numbers() func(*laneContext) func(string) bool {
	writes, inherited := maps.Clone(reading.writes), maps.Clone(reading.inherited)
	for key, each := range reading.local {
		name, _, _ := strings.Cut(key, "@")
		writes[key], inherited[key] = append(slices.Clone(each), reading.global[name]...), reading.inherited[name]
	}
	numbers := laneNumbers(writes, inherited)
	return func(context *laneContext) func(string) bool {
		return func(name string) bool {
			switch {
			case reading.bares(name):
				return false
			case context == nil:
				return numbers[name]
			case slices.Contains(context.guarded, name) && len(reading.global[name]) == 0:
				return true
			case context.local(name):
				return numbers[laneLocalKey(name, context.function)]
			}
			return numbers[name]
		}
	}
}

// laneWhere names the body a reader reads: the lane, at 0, or a library.
func laneWhere(at int) string {
	if at > 0 {
		return "library a lane sources"
	}
	return "lane"
}

// laneCall is one command a lane or a library may run, with the library
// function whose body holds it, or "".
type laneCall struct {
	words    []string
	function string
}

// laneBodyRuns reads a library function's body. stable reports that each
// command of it runs a literal program, a function or a builtin in
// laneForwardSafe, none of which can change the positional parameters, as
// shift, set or an eval of either would. forwards reports that the function
// also forwards its arguments as a command, which this reader models in one
// form only: a stable body with a command that is "$@" alone and quoted. Each
// call of it then runs the words after the function's name.
func laneBodyRuns(body string) (stable, forwards bool) {
	budget := 16*len(body) + 1<<16
	stable = shellLogicalCommands(body, &budget, func(each command) bool {
		words := laneUnredirected(texts(each.args))
		for len(words) > 0 && words[0] != "[[" && (slices.Contains(laneLeadingWords, words[0]) || isCommandPrefixWord(words[0]) || laneAssignmentWord.MatchString(words[0]) || strings.HasSuffix(words[0], ")")) {
			words = words[1:]
		}
		switch {
		case len(words) == 0:
			return true
		case words[0] == "$@" || words[0] == "${@}":
			forwards = len(words) == 1 && !slices.ContainsFunc(each.args, func(arg word) bool { return laneExpands(arg.text) && laneExpands(arg.syntax) })
			return forwards
		}
		if name := words[0]; laneExpands(name) || name != "[" && name != "[[" && strings.ContainsAny(name, "*?[{") {
			return false
		}
		return slices.Contains(laneForwardSafe, words[0]) || !slices.Contains(laneBuiltins, words[0])
	})
	return stable, stable && forwards
}

// outputs returns, for each library function, the positional parameters it
// uses as the name of a variable it assigns, as printf -v "${name}" does after
// local name="$2". A write to a name an expansion computes is an error unless
// it is one such parameter: a local of a stable library function that holds
// it, under a name every write to which is a local of that same parameter, so
// no function the outer one calls rebinds it through bash's dynamic scope.
func (reading laneReading) outputs(calls []laneCall, stable map[string]bool) (map[string][]int, error) {
	parameters := map[string]map[string]int{} // function, local name, positional index
	for _, call := range calls {
		writes, _ := laneWrites(call.words)
		for _, write := range writes {
			match := laneParameter.FindStringSubmatch(write.name + "=" + write.value)
			if match == nil || call.function == "" || !stable[call.function] || firstOf(laneCommand(call.words)) != "local" ||
				slices.ContainsFunc(reading.writes[write.name], func(other laneWrite) bool { return !other.parameter || other.value != write.value }) {
				continue
			}
			if parameters[call.function] == nil {
				parameters[call.function] = map[string]int{}
			}
			parameters[call.function][match[1]] = int(match[2][0] - '0')
		}
	}
	outputs, accepted := map[string][]int{}, map[string]bool{}
	for _, call := range calls {
		_, computed := laneWrites(call.words)
		for _, name := range computed {
			if match := laneComputedName.FindStringSubmatch(name); match != nil && parameters[call.function][match[1]] > 0 {
				outputs[call.function] = append(outputs[call.function], parameters[call.function][match[1]])
				accepted[name] = true
			}
		}
	}
	for name, command := range reading.computed {
		if !accepted[name] {
			return nil, fmt.Errorf("lane wiring is unreadable: a lane runs %q, which assigns a variable whose name this reader cannot spell", command)
		}
	}
	for _, call := range calls {
		command := laneCommand(laneUnredirectedFront(call.words))
		for _, at := range outputs[firstOf(command)] {
			if at < len(command) {
				reading.writes[command[at]] = append(reading.writes[command[at]], laneWrite{name: command[at]}) // a value the function computes
			}
		}
	}
	return outputs, nil
}

// laneArithmeticAssigns is an assignment in arithmetic, in (( )), let, $[ ],
// a subscript or a test's integer operand, to a name an expansion computes or
// to one bash reads to decide what runs, which no word of the command spells
// as an assignment when it is written with spaces, as (( PATH = 1 )) is.
var laneArithmeticAssigns = regexp.MustCompile(`(?:\(\(|\blet\b|\$\[|\w\[|-(?:eq|ne|lt|le|gt|ge)\s)[^)\]\n]*(?:\$\{?[A-Za-z0-9_]+\}?\w*|\b(?:PATH|ENV|BASH\w*|PS4|SHELLOPTS)\b)(?:\[[^\]]*\])?\s*(?:[-+*/%&|^]|<<|>>)?=(?:[^=]|$)`)

// laneParameter is a local assignment of a positional parameter, as in
// local name="$2" read without its quotes, and laneComputedName is a name word
// that is one expansion of a variable.
var (
	laneParameter    = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)=\$\{?([1-9])\}?$`)
	laneComputedName = regexp.MustCompile(`^\$\{?([A-Za-z_][A-Za-z0-9_]*)\}?$`)
)

// laneOutputName reports whether a caller may hand a library function name as
// the variable it assigns: a plain identifier that bash does not read to
// decide what runs, as it reads BASH_CMDS, BASHOPTS, PATH, ENV and PS4, and
// not ROOT_DIR.
func laneOutputName(name string) bool {
	return laneVariableName.MatchString(name) && !strings.HasPrefix(name, "BASH") && !slices.Contains(laneStartupNames, name) &&
		!slices.Contains(laneShellNames, name) && name != "ROOT_DIR"
}

// laneForwardSafe are the builtins a forwarder's body may run: none of them
// changes the positional parameters.
var laneForwardSafe = strings.Fields(`: [ [[ test true false echo printf pwd return exit break continue cd pushd popd dirs
	local declare typeset readonly export unset read mapfile readarray umask ulimit kill wait jobs type let`)

// forwardedReason returns why this reader cannot show what a forwarder runs
// for one call, or "": the forwarded command word must name a program, since
// a builtin or a function it forwards to would run text, or bind names, this
// reader never sees as a command of the lane, as run eval or run trap would.
func (reading laneReading) forwardedReason(args []string) string {
	command := laneCommand(args)
	if len(command) == 0 {
		return ""
	}
	if name := command[0]; !laneExpands(name) && (slices.Contains(laneBuiltins, name) || reading.functions[name]) {
		return fmt.Sprintf("forwards its arguments to %s, a builtin or a function this reader does not read through", name)
	}
	return reading.callReason(command)
}

// callReason returns why this reader cannot show what a command, from its
// command word on, does to a later command, or "" when it can.
func (reading laneReading) callReason(command []string) string {
	if len(command) == 0 {
		return ""
	}
	name, args := command[0], command[1:]
	if laneExpands(name) {
		return reading.expandedReason(name)
	}
	if name != "[" && name != "[[" && (strings.ContainsAny(name, "*?[") || len(braceWords(name)) > 1) {
		return "runs a command word that a glob or a brace group may spell"
	}
	if name == "eval" && reading.spelled[strings.Join(args, "\x00")] {
		return "" // the reader reads each text it may run
	}
	if name == "eval" && reading.lapsed[strings.Join(args, "\x00")] {
		return fmt.Sprintf("evals text this reader cannot spell: a digits guard it needs lapsed past the limit of %d guards in force at once", laneGuardLimit)
	}
	if slices.Contains(laneEnders, commandName(name)) {
		return "may stop or end the lane's shell before a later command runs"
	}
	if name == "export" && (laneOption(args, "f") || slices.ContainsFunc(args, func(arg string) bool { return reading.functions[strings.SplitN(arg, "=", 2)[0]] })) ||
		slices.Contains([]string{"declare", "typeset", "local"}, name) && laneOption(args, "f") && laneOption(args, "x") {
		return "exports a function, which a child bash calls in place of the program it names"
	}
	if rule, modeled := laneModeled[name]; modeled {
		return rule(args, reading.read)
	}
	if slices.Contains(laneBuiltins, name) && !slices.Contains(laneInert, name) {
		return fmt.Sprintf("runs %s, a builtin this reader does not model", name)
	}
	return ""
}

// expandedReason returns why this reader cannot prove what program a command
// word with an expansion runs, or "". A word bash may split, one that some
// use leaves unquoted and that expands a variable the lane writes, is
// unreadable. Otherwise a word with a slash outside its expansions is a path,
// which bash never runs as a builtin or a function, and a word that is one
// expansion of a variable runs a program when the lane assigns it, since an
// inherited value runs past any default, and its default and every value the
// lane writes to it is a literal program name. A pinned ROOT_DIR holds
// an absolute path, so a word that starts with it is a path too.
func (reading laneReading) expandedReason(name string) string {
	if reading.pinned && laneRootStart.MatchString(name) {
		return ""
	}
	if split, seen := reading.split[name]; split || !seen {
		for _, variable := range laneExpansionNames.FindAllStringSubmatch(name, -1) {
			if !laneVariableName.MatchString(variable[1]) || variable[1] == "_" || len(reading.writes[variable[1]]) > 0 {
				return "runs a command word bash may split, which expands a variable the lane writes"
			}
		}
	}
	if strings.Contains(laneExpansion.ReplaceAllString(name, ""), "/") {
		return ""
	}
	variable := laneProgramVariable.FindStringSubmatch(name)
	if variable == nil || variable[1]+variable[2] == "_" {
		return "runs a command whose name this reader cannot spell"
	}
	writes := reading.programWrites(name, variable[1]+variable[2])
	if reading.bares(variable[1] + variable[2]) {
		writes = append(slices.Clone(writes), laneWrite{unset: true})
	}
	for _, write := range append([]laneWrite{{value: variable[3], literal: true}}, writes...) {
		if !write.literal || !laneProgramWord(write.value, reading.functions) {
			return fmt.Sprintf("may run %q, which is not a program this reader can name", write.value)
		}
	}
	if !slices.ContainsFunc(writes, func(write laneWrite) bool { return !write.shaped }) {
		return fmt.Sprintf("runs %s, a variable the lane never assigns, which may hold any program it inherits", variable[1]+variable[2])
	}
	if reading.inherited[variable[1]+variable[2]] {
		return fmt.Sprintf("runs %s, a variable some command may read before the lane assigns it, which may hold any program it inherits", variable[1]+variable[2])
	}
	return ""
}

// programWrites returns the writes a command word that is one expansion of a
// variable may read in the caller's body. Where every use of the word in a
// body of that function's name stands where the body declares the variable
// local, that is those bodies' own writes and every write outside a function
// that declares it local. Otherwise it is every write to the variable.
func (reading laneReading) programWrites(word, name string) []laneWrite {
	key := word + "\x00" + reading.caller
	bodies, local := reading.programs[key]
	if !local || reading.open[key] {
		return reading.writes[name]
	}
	if writes, read := reading.programmed[key]; read {
		return writes
	}
	writes := slices.Clone(reading.global[name])
	for _, body := range slices.Compact(slices.Sorted(slices.Values(bodies))) {
		writes = append(writes, reading.local[laneLocalKey(name, body)]...)
	}
	reading.programmed[key] = writes
	return writes
}

// laneRootStart is a word that starts with an expansion of ROOT_DIR.
var laneRootStart = regexp.MustCompile(`^\$(?:ROOT_DIR|\{ROOT_DIR\})(?:/|$)`)

// laneExpansion is one expansion in a word: ${...} with no nested brace, a
// named or special parameter, or a backquoted substitution, and
// laneExpansionNames captures the name each expands. laneVariableName is a
// name a lane may write.
var (
	laneExpansion      = regexp.MustCompile("\\$\\{[^}]*\\}|\\$(?:[A-Za-z_][A-Za-z0-9_]*|[0-9@*#?$!-])|`[^`]*`")
	laneExpansionNames = regexp.MustCompile("\\$\\{?[!#]?([A-Za-z_][A-Za-z0-9_]*|[0-9@*#?$!-])|`")
	laneVariableName   = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

// laneProgramVariable is a command word that is one expansion of a named
// variable, with an optional literal default, as ${GO_BIN} or
// ${HOST_DOCKER_BIN:-docker} are.
var laneProgramVariable = regexp.MustCompile(`^\$(?:([A-Za-z_][A-Za-z0-9_]*)|\{([A-Za-z_][A-Za-z0-9_]*)(?::?-([A-Za-z0-9_./+-]*))?\})$`)

// laneUnquotedRisk reports whether a word, as written, leaves an expansion, a
// glob or a brace group unquoted, so bash may split it into other words.
func laneUnquotedRisk(each word) bool {
	return strings.ContainsAny(each.syntax, "$`*?") || strings.Contains(each.syntax, "[") || len(braceWords(each.syntax)) > 1
}

// braceWords returns the words a brace expansion makes of a word, read as its
// own syntax, with two words standing for one too large to expand.
func braceWords(text string) []string {
	variants, opaque := braceVariants(text, text)
	if opaque {
		return []string{"", ""}
	}
	return variants
}

// laneScan returns every command text may run, as written: each command of
// each logical line, a function body's included, the body of each heredoc bash
// expands, as one word of the command that reads it, and, read the same way,
// the literal text the first eval or trap of a command or a child shell runs,
// up to eight deep, with the program of each child shell. A substitution, a
// trap action and a child shell's program are read as a subshell, since none
// of their writes reaches a later command. complete is false past that depth,
// past a work bound linear in the text's length, or where a heredoc this
// reader cannot end hides the rest of a text.
func laneScan(text string) (commands []command, children []string, complete bool) {
	commands, children, complete, _ = laneScanCases(text)
	return commands, children, complete
}

// laneScanCases is laneScan, and also reports whether a substitution in any
// text it reads closed while a case in it had no esac, as
// flattenGroupedCases reads it.
func laneScanCases(text string) (commands []command, children []string, complete, unended bool) {
	budget := 16*len(text) + 1<<16
	var scan func(text string, depth int) bool
	scan = func(text string, depth int) bool {
		rewritten, ended := rewriteHeredocsEnding(text)
		flat, cased := flattenGroupedCases(rewritten, true)
		unended = unended || cased
		return depth <= 8 && ended && shellLines(flat, &budget, func(each command) bool {
			each.nested = depth > 0
			commands = append(commands, each)
			inner, child, found := laneInnerText(texts(each.args))
			if found && child {
				children = append(children, inner)
			}
			if operands, expands := laneEvalOperands(each.args); expands && !found {
				variants, _, _ := laneEvalSpelling(operands)
				for _, variant := range variants { // an eval of numbers runs one of these texts
					if children = append(children, variant); !scan(variant, depth+1) {
						return false
					}
				}
			}
			if found && firstOf(laneCommand(laneLeading(laneUnredirected(texts(each.args))))) != "eval" {
				inner = "(\n" + inner + "\n)"
			}
			return !found || scan(inner, depth+1)
		})
	}
	complete = scan(laneProcessesAsCommands(text), 0)
	return commands, children, complete, unended
}

// laneProcessesAsCommands rewrites each unquoted <( or >( as $(, so the shell
// reader moves the process a process substitution runs out of the command's
// words and reads it as its own command, as it reads a command substitution.
func laneProcessesAsCommands(text string) string {
	out := []byte(text)
	single, double := false, false
	for at := 0; at < len(out); at++ {
		switch c := out[at]; {
		case single:
			single = c != '\''
		case c == '\\':
			at++
		case c == '\'' && !double:
			single = true
		case c == '"':
			double = !double
		case !double && (c == '<' || c == '>') && at+1 < len(out) && out[at+1] == '(':
			out[at] = '$'
		}
	}
	return string(out)
}

// laneInnerText returns the text a command runs, if it runs one this reader
// can spell: an eval's operands, a trap's action or a child shell's program,
// and whether it is a child shell's.
func laneInnerText(words []string) (text string, child, found bool) {
	command := laneCommand(laneLeading(laneUnredirected(words)))
	if len(command) == 0 {
		return "", false, false
	}
	switch command[0] {
	case "eval":
		return strings.Join(command[1:], " "), false, !slices.ContainsFunc(command[1:], laneExpands)
	case "trap":
		operands := command[1:]
		if len(operands) > 0 && operands[0] == "--" {
			operands = operands[1:]
		}
		if len(operands) > 0 && !strings.HasPrefix(operands[0], "-") {
			return operands[0], false, true
		}
		return "", false, false
	}
	program, runs, _ := shellProgram(wrappedCommand(command), laneStdin(words))
	script, spelled := spelledProgram(program)
	return script, true, runs && spelled
}

// laneStdin returns the here-string a command reads, or $_ for none.
func laneStdin(words []string) string {
	stdin := "$_"
	for at, word := range words {
		if body, ok := strings.CutPrefix(word, "<<<"); ok {
			if body == "" && at+1 < len(words) {
				body = words[at+1]
			}
			stdin = body
		}
	}
	return stdin
}

// errLaneBraces is the error for a word whose brace expansion this reader does
// not expand: one over braceWordLimit bytes with a group, or one whose groups
// may make more than braceLimit words.
var errLaneBraces = fmt.Errorf("lane wiring is unreadable: a lane has a word whose brace expansion is too large for this reader to expand")

// shadowsLaneProgram reports whether a function so named shadows a program a
// lane names: a shell or a wrapper whose operand the reader takes as the
// command, trap or eval, whose text the reader runs, or a name with a slash,
// which bash calls in place of the file the name spells.
func shadowsLaneProgram(defined string) bool {
	name := commandName(defined)
	_, wrapper := commandWrappers[name]
	return wrapper || strings.Contains(defined, "/") || slices.Contains([]string{"sh", "bash", "dash", "ksh", "zsh", "trap", "eval"}, name)
}

// laneLeadingWords are the words before a command's first word that run
// nothing themselves: reserved words, the ( { (( and [[ that open a group or
// a test, and the closers that may share its line.
var laneLeadingWords = append(slices.Clone(shellKeywords), "}", "fi", "done", "esac", "!", "(", ")", "((", "[[")

// laneLeading returns words from the first assignment or command word, past
// the reserved words, group openers and case patterns in front of it, and a
// function definition's name, so the words of f() { x=1; } start at x=1. A
// word that ends in ) is a case pattern unless it assigns, as y=$((x)) does,
// and a case line starts at the command after its first pattern, if any.
func laneLeading(words []string) []string {
	for len(words) > 0 {
		switch {
		case words[0] == "case":
			end := slices.IndexFunc(words, func(word string) bool { return strings.HasSuffix(word, ")") })
			if end < 0 {
				return nil
			}
			words = words[end+1:]
		case words[0] == "function" && len(words) > 1:
			words = words[2:]
		case len(words) > 1 && words[1] == "()":
			words = words[2:]
		case len(words) > 2 && words[1] == "(" && words[2] == ")":
			words = words[3:]
		case slices.Contains(laneLeadingWords, words[0]) || isCommandPrefixWord(words[0]) || strings.HasSuffix(words[0], ")") && !laneAssignmentWord.MatchString(words[0]):
			words = words[1:]
		default:
			return words
		}
	}
	return words
}

// laneAssignmentWord is a word that assigns a variable, as a prefix, an env
// operand or a declaration does: a name, an optional subscript, and = or +=.
var laneAssignmentWord = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)(?:\[[^\]]*\])?(\+?)=`)

// laneAssigns reports whether a command leaves the value it spells in the
// shell that runs it: past the reserved words, group openers and case
// patterns in front of it, a bare assignment, a declaration with no option
// whose every operand assigns, as local n=0 does, or for NAME in, each with
// only redirections besides. A prefix assignment, a declaration with an
// option or with no value, a coproc and a command time or ! runs are not
// counted, since what each leaves in the shell is not the value it spells.
func laneAssigns(words []string) bool {
	unredirected := laneUnredirected(words)
	command := laneLeading(unredirected)
	if len(command) == 0 || slices.ContainsFunc(unredirected[:len(unredirected)-len(command)], isCommandPrefixWord) {
		return false
	}
	switch {
	case len(command) > 2 && command[0] == "for" && command[2] == "in":
		return laneVariableName.MatchString(command[1])
	case slices.Contains(laneDeclarations, command[0]):
		command = command[1:]
	}
	return !slices.ContainsFunc(command, func(word string) bool { return !laneAssignmentWord.MatchString(word) })
}

// laneDeclarations are the builtins that assign their NAME=value operands.
var laneDeclarations = []string{"local", "declare", "typeset", "readonly", "export"}

// laneEnvOperands returns how many of args a shaped write may stand in: all
// of them, but where env runs a program by an absolute path that is no shell,
// as env -i PATH=/usr/bin /usr/bin/perl does, the words from env on, whose
// assignments reach only that program, or nothing when env runs none. A program env looks up, or a shell,
// reads them first, so they are kept.
func laneEnvOperands(args []word, runs []string) int {
	if len(runs) == 0 || commandName(runs[0]) != "env" {
		return len(args)
	}
	program := wrappedCommand(runs)
	if len(program) == 0 || !strings.HasPrefix(program[0], "/") || slices.Contains(laneShells, commandName(program[0])) {
		return len(args)
	}
	for at, each := range args {
		if !each.quoted && each.text == runs[0] {
			return at
		}
	}
	return len(args)
}

// laneShells are the shells whose startup reads the environment env passes.
var laneShells = []string{"sh", "bash", "dash", "ksh", "zsh"}

// laneLocalInherit is the shopt option under which a local starts with the
// value of the variable it shadows.
var laneLocalInherit = regexp.MustCompile(`localvar_inherit`)

// laneUnredirected returns words without each redirection and its target.
func laneUnredirected(words []string) []string {
	var kept []string
	for at := 0; at < len(words); at++ {
		if operator := redirectionOperator.FindString(words[at]); operator != "" {
			if operator == words[at] {
				at++ // the target is the next word
			}
			continue
		}
		kept = append(kept, words[at])
	}
	return kept
}

// laneUnredirectedWords returns words without each redirection and its target,
// as laneUnredirected does, but reads the operator from the word as the line
// spells it, so a quoted word that starts like one, as the value of
// for fd in '0>&0' does, stays a word.
func laneUnredirectedWords(words []word) []word {
	var kept []word
	for at := 0; at < len(words); at++ {
		spelled := cmp.Or(words[at].raw, words[at].text)
		if operator := redirectionOperator.FindString(spelled); operator != "" {
			if operator == spelled {
				at++ // the target is the next word
			}
			continue
		}
		kept = append(kept, words[at])
	}
	return kept
}

// laneUnredirectedFront returns words past each redirection in front of the
// command word. The shell reader keeps a redirection whose target is quoted,
// as exec 3<"${f}" is, and its words no longer say what was quoted, so a later
// word that looks like one stays: it may be an operand, as eval's quoted text is.
func laneUnredirectedFront(words []string) []string {
	for len(words) > 0 && redirectionOperator.MatchString(words[0]) {
		words = words[1:] // the reader drops a target that stands apart, as in 3< "${f}"
	}
	return words
}

// laneScanWrites returns each variable a command assigns, as laneWrites reads
// it from the command's first word, and every other word that has the shape
// of an assignment too, wherever it stands and in each word its brace
// expansion makes, so env PATH=x, echo x=1 and env P{A,X}TH=x count: reading
// more writes than bash makes only fails closed. A redirection and its target
// assign nothing, so they are dropped first. opaque reports a word whose
// brace expansion is too large to expand.
func laneScanWrites(args []word) (writes []laneWrite, computed []string, opaque bool) {
	command := laneLeading(texts(laneUnredirectedWords(args)))
	writes, computed = laneWrites(command)
	defaults := []string{":"} // a redirection's target, a here-string and an expanded heredoc's body expand in this shell too
	for _, each := range texts(args) {
		if !slices.Contains(command, each) {
			defaults = append(defaults, laneDefaultAssign.FindAllString(each, -1)...)
		}
	}
	more, named := laneWrites(defaults)
	writes, computed = append(writes, more...), append(computed, named...)
	operands := laneEnvOperands(args, laneCommand(command))
	for at, each := range args {
		if at >= operands {
			break
		}
		variants, unread := braceVariants(each.text, each.syntax)
		if unread {
			return nil, nil, true
		}
		for _, variant := range variants {
			if match := laneAssignmentWord.FindStringSubmatch(variant); match != nil {
				value := variant[len(match[0]):]
				writes = append(writes, laneWrite{name: match[1], value: value, literal: match[2] == "" && !laneExpands(value), shaped: true})
			}
		}
	}
	for at, each := range args {
		if !each.quoted && each.text == "coproc" && at+2 < len(args) { // coproc NAME { ...; } assigns NAME and NAME_PID
			writes = append(writes, laneWrite{name: args[at+1].text}, laneWrite{name: args[at+1].text + "_PID"})
		}
	}
	for at := range writes {
		writes[at].parameter = firstOf(laneCommand(command)) == "local" && laneParameter.MatchString(writes[at].name+"="+writes[at].value)
	}
	return writes, computed, false
}

// laneChildNamesRoot reports whether a command runs a shell that reads a
// program, as shellProgram finds it in a -c body, a here-document, a
// here-string, -s or /dev/stdin, that names ROOT_DIR. ROOT_DIR is not
// exported, so the child reads its own, unpinned one.
func laneChildNamesRoot(words []string) bool {
	command := laneCommand(laneLeading(laneUnredirected(words)))
	if len(command) == 0 {
		return false
	}
	program, runs, _ := shellProgram(wrappedCommand(command), laneStdin(words))
	script, _ := spelledProgram(program)
	return runs && laneRootNamed.MatchString(script)
}

// laneStartupNames are the variables whose value changes the program a later
// command runs: PATH, which a name is looked up in, and BASH_ENV and ENV,
// which a child shell sources first. A lane that assigns one wires nothing,
// and a library that does is unreadable.
var laneStartupNames = []string{"PATH", "BASH_ENV", "ENV"}

// laneShellNames are the other variables bash reads to decide what runs, so
// a write to one, in a lane or a library, is unreadable: PS4, whose command
// substitutions run under set -x, and SHELLOPTS and BASHOPTS, which set
// options such as noexec in a child shell.
var laneShellNames = []string{"PS4", "SHELLOPTS", "BASHOPTS"}

// laneWrite is one variable a command assigns, with its value.
type laneWrite struct {
	name, value string
	literal     bool // the command assigns value, spelled with no expansion
	parameter   bool // a local assigns a positional parameter, as local name="$2" does
	integer     bool // the command assigns a number it computes, as let does
	shaped      bool // a word has the shape of an assignment, which bash may not make
	unset       bool // unset removes the variable, which bares any copy a caller holds
	names       bool // unset "${name}" removes each variable name lists, and assigns none to name
}

// laneSubscripted is a variable name with a subscript, as unset 'a[1]' names it.
var laneSubscripted = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)\[[^]]*\]$`)

// laneDefaultAssign is an expansion that assigns a default, as ${name:=value}
// or ${!name=value} do.
var laneDefaultAssign = regexp.MustCompile(`\$\{(!?)([^}:=+?#%/^,-]*):?=([^}]*)\}`)

// laneWrites returns each variable a command assigns, and each name word an
// expansion computes: a prefix assignment, an operand of declare, typeset,
// local, export or readonly, a name read, mapfile, readarray, printf -v or
// getopts assigns, the variable of a for or select loop, an assignment let
// makes, a name unset removes, and an expansion that assigns a default. Only a plain assignment of
// a value with no expansion is literal: += appends, and read, mapfile, printf
// -v, getopts and let assign what the command computes. A declaration with
// no value is literal and empty, as local x makes it.
func laneWrites(words []string) (writes []laneWrite, computed []string) {
	add := func(word string, assigns bool) {
		name, value, _ := strings.Cut(word, "=")
		if !assigns && !laneExpands(name) && !laneVariableName.MatchString(strings.TrimSuffix(name, "+")) {
			computed = append(computed, name) // read P{A,X}TH or printf -v P[A]TH assigns a name bash expands
			return
		}
		appends := strings.HasSuffix(name, "+")
		name = strings.TrimSuffix(name, "+")
		if at := strings.IndexByte(name, '['); at > 0 {
			name = name[:at]
		}
		if name == "" || laneExpands(name) {
			computed = append(computed, name)
			return
		}
		writes = append(writes, laneWrite{name: name, value: value, literal: assigns && !appends && !laneExpands(value)})
	}
	for _, word := range words {
		for _, match := range laneDefaultAssign.FindAllStringSubmatch(word, -1) {
			if match[1] != "" {
				computed = append(computed, "${!"+match[2]+"}")
			} else {
				add(match[2]+"="+match[3], true)
			}
		}
	}
	prefix := assignmentPrefix(words)
	for _, word := range words[:prefix] {
		add(word, true)
	}
	command := unwrapBuiltins(words[prefix:])
	if len(command) == 0 {
		return writes, computed
	}
	// operands returns the operands after the options, which end at -- or the
	// first operand, skipping the value of each option in valued, and adds the
	// value of each option in named as a name the command assigns.
	operands := func(valued, named string) []string {
		for at := 1; at < len(command); at++ {
			arg := command[at]
			if arg == "--" {
				return command[at+1:]
			}
			if len(arg) < 2 || arg[0] != '-' && arg[0] != '+' {
				return command[at:]
			}
			if slices.Contains([]string{"declare", "typeset", "local"}, command[0]) && arg[0] == '-' && strings.Contains(arg, "n") {
				computed = append(computed, arg) // a nameref
			}
			for index, option := range arg[1:] {
				if !strings.ContainsRune(valued, option) {
					continue
				}
				value := arg[index+2:]
				if value == "" && at+1 < len(command) {
					at++
					value = command[at]
				}
				if strings.ContainsRune(named, option) {
					add(value, false)
				}
				break
			}
		}
		return nil
	}
	switch command[0] {
	case "declare", "typeset", "local", "export", "readonly":
		for _, arg := range operands("", "") {
			add(arg, true)
		}
	case "read":
		for _, arg := range operands("adinNptu", "a") {
			add(arg, false)
		}
	case "mapfile", "readarray":
		if rest := operands("dnOsuCc", ""); len(rest) > 0 {
			add(rest[len(rest)-1], false)
		}
	case "printf":
		operands("v", "v")
	case "wait":
		operands("p", "p")
	case "unset":
		rest := operands("", "")
		if laneOption(command[1:len(command)-len(rest)], "f") {
			break // unset -f removes functions, and bash refuses -fv
		}
		for _, arg := range rest {
			before := len(writes)
			if match := laneComputedName.FindStringSubmatch(arg); match != nil {
				writes = append(writes, laneWrite{name: match[1], names: true})
			} else {
				add(laneSubscripted.ReplaceAllString(arg, "$1"), false)
			}
			for at := before; at < len(writes); at++ {
				writes[at].unset = true // an unset may bare a caller's copy or an inherited value
			}
		}
	case "getopts":
		if len(command) > 2 {
			add(command[2], false)
		}
	case "let":
		for _, arg := range command[1:] {
			if name, _, found := strings.Cut(arg, "="); found {
				before := len(writes)
				add(strings.TrimRight(name, "-+*/%&|^<> ")+"=", true)
				for at := before; at < len(writes); at++ {
					writes[at].literal, writes[at].integer = false, true // let assigns the number it computes
				}
			}
		}
	case "for", "select":
		if len(command) > 1 && !strings.HasPrefix(command[1], "((") { // a for (( )) header is arithmetic, which laneArithmetic reads
			if len(command) > 2 && command[2] == "in" {
				for _, value := range command[3:] {
					add(command[1]+"="+value, true)
				}
			} else {
				add(command[1], false)
			}
		}
	}
	return writes, computed
}

// laneProgramName is a value that names a program: a path or a bare name of
// path characters alone.
var laneProgramName = regexp.MustCompile(`^[A-Za-z0-9_./+-]+$`)

// laneBuiltins are bash's builtins and reserved words, which bash runs in the
// shell itself, so a variable that holds one may rebind what the lane runs.
var laneBuiltins = strings.Fields(`. : [ alias bg bind break builtin caller cd command compgen complete compopt
	continue declare dirs disown echo enable eval exec exit export false fc fg getopts hash help history jobs kill
	let local logout mapfile popd printf pushd pwd read readarray readonly return set shift shopt source suspend test
	times trap true type typeset ulimit umask unalias unset wait if then else elif fi case esac for select while until
	do done in function time { } ! [[ ]] coproc`)

// laneProgramWord reports whether a value a lane spells for a program variable
// names a program: empty, which runs nothing, or a program name that is not a
// builtin, a reserved word, a laneEnders program or a function the lane or a
// library defines.
func laneProgramWord(value string, functions map[string]bool) bool {
	return value == "" || laneProgramName.MatchString(value) && !slices.Contains(laneBuiltins, value) && !functions[value] &&
		!slices.Contains(laneEnders, commandName(value))
}

// firstOf returns the first word, or "".
func firstOf(words []string) string {
	if len(words) == 0 {
		return ""
	}
	return words[0]
}

// laneEnds holds the commands each body runs whenever it runs, the lane's or
// a library's top level or a function's body, outside every branch, loop,
// subshell and pipeline, in order.
type laneEnds struct {
	tops      map[string][]laneTop
	order     []string          // each body's key, as the reader first reads it
	where     map[string]string // each body, as an error names it
	sourced   map[string]int
	functions map[string]bool
	memo      map[string]bool
	forked    map[string]bool // the bodies that pipe or background a group or compound command, whose returns this reader does not place
}

// laneTop is one command a body runs, and where it stands.
type laneTop struct {
	command []string
	placed  bool // any exit there ends the body: no branch, loop, subshell or group behind || or && holds it
	may     bool // an exit there that may end with 0 ends the body: no if branch, subshell, group behind || or &&, or case branch of a lane option holds it
	certain bool // a return there runs whenever the body runs
}

// ended returns where a body runs a command that ends the lane's shell, and
// that command, when the body runs another command after it, or nil.
func (ends *laneEnds) ended() (string, []string) {
	ends.memo = map[string]bool{}
	for _, key := range ends.order {
		for index, top := range ends.tops[key] {
			if top.command[0] != "return" && (top.placed && ends.ending(top.command, false) || top.may && ends.ending(top.command, true)) {
				if index+1 < len(ends.tops[key]) {
					return ends.where[key], top.command
				}
				break
			}
		}
	}
	return "", nil
}

// ending reports whether a command ends the lane's shell, or with zero only
// one that may end it with 0: an exit, an exec of a program, a call of a
// function or a source of a library whose body ends it, or an eval of text
// that may.
func (ends *laneEnds) ending(command []string, zero bool) bool {
	if library, fixed := laneLibraryPath(command); fixed && ends.sourced[library] > 0 {
		return ends.ends(fmt.Sprint(ends.sourced[library]), zero)
	}
	switch {
	case command[0] == "exit":
		return !zero || len(command) == 1 || !laneFailing(command[1])
	case laneExecs(command):
		return true
	case ends.functions[command[0]]:
		return ends.ends("function "+command[0], zero)
	case command[0] == "eval": // a proven eval expands only numbers, as in eval "exec $fd<&-"
		return ends.evalEnds(laneExpansion.ReplaceAllString(strings.Join(command[1:], " "), "0"))
	}
	return false
}

// evalEnds reports whether eval text runs, anywhere in it, an exit, an exec
// of a program or a call of a function whose body may end the shell.
func (ends *laneEnds) evalEnds(text string) bool {
	commands, _, complete := laneScan(text)
	return !complete || slices.ContainsFunc(commands, func(each command) bool {
		inner := unwrapBuiltins(laneCommand(laneLeading(laneUnredirected(texts(each.args)))))
		return len(inner) > 0 && (inner[0] == "exit" || laneExecs(inner) || ends.functions[inner[0]] &&
			(ends.ends("function "+inner[0], false) || ends.ends("function "+inner[0], true)))
	})
}

// ends reports whether a body ends the shell: with zero, by a command that
// may end it with 0 before any return that always runs, and otherwise by a
// command that always runs before any return.
func (ends *laneEnds) ends(key string, zero bool) bool {
	memo := fmt.Sprint(zero, " ", key)
	if ended, seen := ends.memo[memo]; seen {
		return ended
	}
	ends.memo[memo] = false // a body that calls itself ends nothing more by that call
	for _, top := range ends.tops[key] {
		if top.command[0] == "return" {
			if zero && top.certain || !zero && top.placed && !ends.forked[key] {
				break
			}
			continue
		}
		if zero && top.may && ends.ending(top.command, true) || !zero && top.placed && (ends.ending(top.command, false) || ends.ending(top.command, true)) {
			ends.memo[memo] = true
			break
		}
	}
	return ends.memo[memo]
}

// laneExecs reports whether a command is an exec of a program, past the
// descriptor the shell reader splits from a quoted here-string, as in exec
// 3<<<"x".
func laneExecs(command []string) bool {
	words := slices.DeleteFunc(slices.Clone(unwrapBuiltins(command)), func(word string) bool {
		return strings.Trim(word, "0123456789") == ""
	})
	return len(words) > 0 && replacesShell(words)
}

// laneTrapCommand returns a trap command's words from trap on, past a --.
func laneTrapCommand(words []string) []string {
	command := laneCommand(laneLeading(laneUnredirected(words)))
	if firstOf(command) != "trap" {
		return nil
	}
	if len(command) > 1 && command[1] == "--" {
		return append([]string{"trap"}, command[2:]...)
	}
	return command
}

// laneFailing reports whether a status is a literal that fails, which bash
// keeps modulo 256.
func laneFailing(status string) bool {
	number, err := strconv.Atoi(status)
	return err == nil && number%256 != 0
}

// laneMasks reports whether an EXIT trap's action, or a function it calls,
// may replace the status the lane ends with: an exit with a status that may
// be 0, a bare exit after another command, an exec of a program, a source,
// or text this reader cannot read.
func laneMasks(text string, bodied map[string][]string, read map[string]bool) bool {
	commands, _, complete := laneScan(text)
	ran := 0 // the commands before this one, past the empty ones that end lines
	for _, each := range commands {
		inner := unwrapBuiltins(laneCommand(laneLeading(laneUnredirected(texts(each.args)))))
		if len(inner) > 0 {
			ran++
		}
		switch name := firstOf(inner); {
		case name == "exit" && (len(inner) > 1 && !laneFailing(inner[1]) || len(inner) == 1 && ran > 1), // a bare exit keeps the status only first
			laneExecs(inner), name == "source", name == ".":
			return true
		case name != "" && bodied[name] != nil && !read[name]:
			read[name] = true
			if slices.ContainsFunc(bodied[name], func(body string) bool { return laneMasks(body, bodied, read) }) {
				return true
			}
		}
	}
	return !complete
}
