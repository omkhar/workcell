// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// laneExpands reports whether a word holds an expansion bash makes.
func laneExpands(word string) bool { return strings.ContainsAny(word, "$`") }

// laneReading is what the reader learns from every command a lane and its
// libraries may run before it reads any call.
type laneReading struct {
	read      map[string]bool        // the libraries the lane sources
	functions map[string]bool        // the functions the lane and its libraries define
	writes    map[string][]laneWrite // each variable's writes
	pinned    bool                   // ROOT_DIR is the repository root
}

// laneReadable reports, as an error, why this reader cannot show what a lane
// runs, after reading the lane and every library it sources, and whether the
// lane assigns PATH, BASH_ENV or ENV, which may change the program any later
// command runs, a script's own #! interpreter included. It first reads every
// command, as written, of the lane, its libraries and the text each runs, for
// the functions they define, the variables they write and the child shells
// they run, and then each call as EveryShellCommand reads it.
func laneReadable(body string, libraries []laneLibrary, pinned bool) (startup bool, err error) {
	bodies := []string{body}
	reading := laneReading{read: map[string]bool{}, functions: map[string]bool{}, writes: map[string][]laneWrite{}, pinned: pinned}
	for _, library := range libraries {
		bodies, reading.read[library.path] = append(bodies, library.text), true
	}
	var calls []laneCall
	for at, text := range bodies {
		where := "lane"
		if at > 0 {
			where = "library a lane sources"
		}
		scanned, children, complete := laneScan(text)
		if !complete {
			return false, fmt.Errorf("lane wiring is unreadable: a %s nests eval, trap or child shell text more than eight deep, or holds more text than this reader parses", where)
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
				reading.functions[name] = true
			}
		}
		if laneArithmeticAssigns.MatchString(strings.ReplaceAll(text, "\\\n", "")) {
			return false, fmt.Errorf("lane wiring is unreadable: a %s assigns in arithmetic to a variable whose name this reader cannot spell, or that bash reads to decide what runs", where)
		}
		switch named, opaque := namesShellVariable(text, laneHashTables.MatchString); {
		case opaque:
			return false, errLaneBraces
		case named:
			return false, fmt.Errorf("lane wiring is unreadable: a %s names BASH_CMDS or BASH_ALIASES, which may rebind the scripts it runs", where)
		}
		for _, args := range scanned {
			words := texts(args)
			if laneChildNamesRoot(words) {
				return false, fmt.Errorf("lane wiring is unreadable: a %s runs %q, a child shell whose own program names ROOT_DIR, which the child does not inherit pinned", where, strings.Join(words, " "))
			}
			writes, _, opaque := laneScanWrites(args)
			if opaque {
				return false, errLaneBraces
			}
			for _, write := range writes {
				switch {
				case slices.Contains(laneShellNames, write.name):
					return false, fmt.Errorf("lane wiring is unreadable: a %s runs %q, which assigns %s, a variable bash reads to decide what runs", where, strings.Join(words, " "), write.name)
				case slices.Contains(laneStartupNames, write.name) && at > 0:
					return false, fmt.Errorf("lane wiring is unreadable: a library a lane sources runs %q, which assigns %s and may change the program a script runs", strings.Join(words, " "), write.name)
				}
				startup = startup || slices.Contains(laneStartupNames, write.name)
				reading.writes[write.name] = append(reading.writes[write.name], write)
			}
		}
		if at == 0 {
			for _, words := range laneCommands(body)[0] { // the lane runs its own commands as EveryShellCommand reads them
				calls = append(calls, laneCall{words: words})
			}
			continue
		}
		parts, complete := libraryParts(text)
		if !complete {
			return false, fmt.Errorf("lane wiring is unreadable: a lane nests function definitions deeper than this reader parses")
		}
		for _, part := range parts {
			for _, words := range part.commands {
				calls = append(calls, laneCall{words, part.function})
			}
		}
	}
	for _, call := range calls {
		if slices.Equal(call.words, shellReadBound) {
			return false, fmt.Errorf("lane wiring is unreadable: a lane runs %q, which stands for text past what the shell reader reads", strings.Join(call.words, " "))
		}
	}
	return startup, nil
}

// laneCall is one command a lane or a library may run, with the library
// function whose body holds it, or "".
type laneCall struct {
	words    []string
	function string
}

// laneArithmeticAssigns is an assignment in arithmetic, in (( )), let, $[ ],
// a subscript or a test's integer operand, to a name an expansion computes or
// to one bash reads to decide what runs, which no word of the command spells
// as an assignment when it is written with spaces, as (( PATH = 1 )) is.
var laneArithmeticAssigns = regexp.MustCompile(`(?:\(\(|\blet\b|\$\[|\w\[|-(?:eq|ne|lt|le|gt|ge)\s)[^)\]\n]*(?:\$\{?[A-Za-z0-9_]+\}?\w*|\b(?:PATH|ENV|BASH\w*|PS4|SHELLOPTS)\b)(?:\[[^\]]*\])?\s*(?:[-+*/%&|^]|<<|>>)?=(?:[^=]|$)`)

// laneScan returns the words of every command text may run, as written: each
// command of each logical line, a function body's included, and, read the same
// way, the literal text the first eval or trap of a command or a child shell
// runs, up to eight deep, with the program of each child shell. complete is
// false past that depth or past a work bound linear in the text's length.
func laneScan(text string) (commands [][]word, children []string, complete bool) {
	budget := 16*len(text) + 1<<16
	var scan func(text string, depth int) bool
	scan = func(text string, depth int) bool {
		return depth <= 8 && shellLogicalCommands(text, &budget, func(each command) bool {
			commands = append(commands, each.args)
			inner, child, found := laneInnerText(texts(each.args))
			if found && child {
				children = append(children, inner)
			}
			return !found || scan(inner, depth+1)
		})
	}
	complete = scan(text, 0)
	return commands, children, complete
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
var laneLeadingWords = append(slices.Clone(shellKeywords), "}", "fi", "done", "esac", "!", "(", ")", "((", "[[", "case")

// laneLeading returns words from the first assignment or command word, past
// the reserved words, group openers and case patterns in front of it, and a
// function definition's name, so the words of f() { x=1; } start at x=1.
func laneLeading(words []string) []string {
	for len(words) > 0 {
		switch {
		case words[0] == "function" && len(words) > 1:
			words = words[2:]
		case len(words) > 1 && words[1] == "()":
			words = words[2:]
		case len(words) > 2 && words[1] == "(" && words[2] == ")":
			words = words[3:]
		case slices.Contains(laneLeadingWords, words[0]) || isCommandPrefixWord(words[0]) || strings.HasSuffix(words[0], ")"):
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

// laneScanWrites returns each variable a command assigns, as laneWrites reads
// it from the command's first word, and every other word that has the shape
// of an assignment too, wherever it stands and in each word its brace
// expansion makes, so env PATH=x, echo x=1 and env P{A,X}TH=x count: reading
// more writes than bash makes only fails closed. A redirection and its target
// assign nothing, so they are dropped first. opaque reports a word whose
// brace expansion is too large to expand.
func laneScanWrites(args []word) (writes []laneWrite, computed []string, opaque bool) {
	command := laneLeading(laneUnredirected(texts(args)))
	writes, computed = laneWrites(command)
	for _, each := range args {
		variants, unread := braceVariants(each.text, each.syntax)
		if unread {
			return nil, nil, true
		}
		for _, variant := range variants {
			if match := laneAssignmentWord.FindStringSubmatch(variant); match != nil {
				value := variant[len(match[0]):]
				writes = append(writes, laneWrite{name: match[1], value: value, literal: match[2] == "" && !laneExpands(value)})
			}
		}
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
}

// laneDefaultAssign is an expansion that assigns a default, as ${name:=value}
// or ${!name=value} do.
var laneDefaultAssign = regexp.MustCompile(`\$\{(!?)([^}:=+?#%/^,-]*):?=([^}]*)\}`)

// laneWrites returns each variable a command assigns, and each name word an
// expansion computes: a prefix assignment, an operand of declare, typeset,
// local, export or readonly, a name read, mapfile, readarray, printf -v or
// getopts assigns, the variable of a for or select loop, an assignment let
// makes, and an expansion that assigns a default. Only a plain assignment of
// a value with no expansion is literal: += appends, and read, mapfile, printf
// -v, getopts and let assign what the command computes. A declaration with
// no value is literal and empty, as local x makes it.
func laneWrites(words []string) (writes []laneWrite, computed []string) {
	add := func(word string, assigns bool) {
		name, value, _ := strings.Cut(word, "=")
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
	case "getopts":
		if len(command) > 2 {
			add(command[2], false)
		}
	case "let":
		for _, arg := range command[1:] {
			if name, _, found := strings.Cut(arg, "="); found {
				add(strings.TrimRight(name, "-+*/%&|^<> ")+"=", false)
			}
		}
	case "for", "select":
		if len(command) > 1 {
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
