// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// laneCase is one case command a lane is in, read as bash's parser reads it:
// a pattern list follows the case line and each ;;, ;& or ;;&, a word that
// ends in an unquoted ) ends the list, and each | in it, which the shell
// reader splits as a pipeline, joins the next piece. A branch is a condition
// only when each of its patterns is a literal word, no ;& or ;;& runs it, and
// its subject is the lane's own $1, which laneOwnCase reads against the
// lane's callers, or one expansion of a variable a step's env sets from the
// github context, whose value the lane cannot fix.
type laneCase struct {
	depth    int            // the index of its scope among the nesting's scopes
	function int            // the function body it is in, or 0
	named    bool           // its subject is a variable laneCaseNamed accepts
	own      bool           // its subject is $1 at the lane's own top level
	expect   bool           // the next command starts a pattern list
	piece    bool           // the pattern list goes on in the next command, after a |
	literal  bool           // each piece read so far of the list is a literal word
	through  bool           // a ;& or ;;& runs the branch whether its patterns match or not
	pieces   []string       // the pieces read so far
	cond     bool           // the branch it is in runs only when a literal pattern matches
	branch   *laneOwnBranch // the branch it is in, of an own case
	cases    *laneCases     // the reader it belongs to
	subject  string         // its subject word
}

// laneCases follows the case commands of one body.
type laneCases struct {
	open    []*laneCase
	blind   bool              // a case is open that this reader does not follow
	empty   bool              // the command before was empty
	follows string            // the operator in front of the command before
	env     map[string]string // a step's own env, or nil in a lane script
	run     string            // a step's text
	own     *laneOwnCase      // a lane script's, shared by its libraries
	top     bool              // the body is the lane script's own text
}

// laneOwnCase is what the reader learns of the cases on the lane's own $1:
// each branch in order, and whether anything else changes $1.
type laneOwnCase struct {
	branches []*laneOwnBranch
	cases    int
	changed  bool // a shift outside a branch, a set of the parameters, or a second case
	patches  []laneOwnPatch
}

// laneOwnBranch is one branch of a case on $1.
type laneOwnBranch struct {
	patterns []string
	literal  bool // its patterns are literal and no ;& or ;;& runs it
	shift    int  // the count its one shift drops, 0 for none and -1 when unknown
	selected bool // a caller's argument may select it
}

// laneOwnPatch is a command that ends the lane only when a caller selects its
// branch: its top's place among its body's tops, and its branch.
type laneOwnPatch struct {
	key    string
	index  int
	branch *laneOwnBranch
}

// lanePatternEnds reports whether a word ends a pattern list: its last byte is
// a ) that is neither quoted nor escaped.
func lanePatternEnds(each word) bool {
	return strings.HasSuffix(each.syntax, ")")
}

// laneLiteralPiece reports whether a pattern piece, as the line spells it,
// matches only its own text: it holds no glob, expansion, quote, escape or
// other syntax.
func laneLiteralPiece(raw string) bool {
	return !strings.ContainsAny(raw, "*?[]$`!@+(){}\\|&;<>~'\" \t#")
}

// read reads one command with the nesting. The nesting reads a pattern list
// as one word, so each branch starts a scope however its patterns are
// spelled. A case the command opens is followed from its scope, which is the
// last, and the nesting reads the words after its first pattern list again,
// as it reads a command after a ;;. It returns the scopes the command runs in
// and whether the nesting lost track, as the nesting does; the words that
// run as commands, past any pattern list; and whether the command held a
// pattern list after a ;; or a |.
func (cases *laneCases) read(each command, nesting *laneNesting) (scopes []int, lost bool, body []word, parsed bool) {
	nest, body := each.args, each.args
	if !each.nested {
		if body, parsed = cases.before(each); parsed {
			nest = append([]word{laneBranchWord}, body...)
		}
	}
	for base, prefix := 0, len(nest)-len(body); ; prefix = 1 { // nest holds body from base on, after prefix words
		scopes, lost = nesting.read(nest)
		cases.open = slices.DeleteFunc(cases.open, func(c *laneCase) bool { return c.depth >= len(scopes) })
		if nesting.opened < 0 || each.nested {
			break
		}
		at := base + nesting.opened - prefix
		function := nesting.function(scopes)
		c := &laneCase{cases: cases, expect: true, depth: len(scopes) - 1, function: function}
		if at+2 < len(body) && body[at+2].text == "in" {
			c.subject = body[at+1].text
		}
		c.named = laneCaseNamed(c.subject, cases.env, cases.run)
		if c.own = cases.top && function == 0 && laneOwnSubject.MatchString(c.subject); c.own && cases.own != nil {
			cases.own.cases++
			cases.own.changed = cases.own.changed || cases.own.cases > 1
		}
		cases.open = append(cases.open, c)
		inner, _ := c.patterns(command{args: body[at+min(3, len(body)-at):]})
		if body, base = append(slices.Clip(body[:at]), inner...), at; len(inner) == 0 {
			break
		}
		nest = append([]word{laneBranchWord}, inner...)
	}
	open := 0
	for _, scope := range scopes {
		if nesting.kinds[scope] == "case" {
			open++
		}
	}
	cases.blind = open != len(cases.open)
	return scopes, lost, body, parsed
}

// laneBranchWord stands for a pattern list the reader has read, so the
// nesting starts a branch at it.
var laneBranchWord = word{text: "_)", syntax: "_)", raw: "_)"}

// before reads a command the case it is in may read as a pattern list, and
// returns the words past the list and whether it held one.
func (cases *laneCases) before(each command) (body []word, parsed bool) {
	body = each.args
	fell := each.follows == "&" && cases.empty && (cases.follows == ";" || cases.follows == ";;")
	cases.empty, cases.follows = len(each.args) == 0, each.follows
	if c := cases.innermost(); c != nil {
		switch {
		case each.follows == ";;":
			c.expect, c.piece, c.through = true, false, false
		case fell: // ;& or ;;&, which the shell reader splits at the &
			c.expect, c.piece, c.through = true, false, true
		}
		if c.expect || c.piece {
			return c.patterns(each)
		}
	}
	return body, false
}

// patterns reads a command where a pattern list starts or goes on, and
// returns the words after the list, or nil while it goes on.
func (c *laneCase) patterns(each command) ([]word, bool) {
	words := each.args
	if len(words) == 0 {
		return nil, false
	}
	if c.expect {
		if !words[0].quoted && words[0].text == "esac" {
			c.expect = false
			return words, false
		}
		c.expect, c.literal, c.pieces = false, true, nil
		if words[0].text == "(" && words[0].syntax == "(" {
			words = words[1:]
		} else if strings.HasPrefix(words[0].syntax, "(") { // (a) opens its list as ( a ) does
			first := words[0]
			first.text, first.syntax, first.raw = first.text[1:], first.syntax[1:], first.raw[1:]
			words = append([]word{first}, words[1:]...)
		}
	}
	for at, each := range words {
		if lanePatternEnds(each) {
			piece := strings.TrimSuffix(each.text, ")")
			c.literal = c.literal && at == 0 && laneLiteralPiece(strings.TrimSuffix(each.raw, ")"))
			c.pieces, c.piece = append(c.pieces, piece), false
			c.start()
			return slices.Clip(words[at+1:]), true
		}
	}
	c.literal = c.literal && len(words) == 1 && laneLiteralPiece(words[0].raw)
	c.pieces, c.piece = append(c.pieces, texts(words)...), true
	return nil, true
}

// start starts the branch whose pattern list the reader has read.
func (c *laneCase) start() {
	literal := c.literal && !c.through
	c.cond, c.branch = c.named && literal, nil
	if c.own && c.cases.own != nil {
		c.branch = &laneOwnBranch{patterns: c.pieces, literal: literal}
		c.cases.own.branches = append(c.cases.own.branches, c.branch)
	}
}

// innermost returns the case the reader follows at the innermost case scope,
// or nil when there is none or the reader is blind.
func (cases *laneCases) innermost() *laneCase {
	if cases.blind || len(cases.open) == 0 {
		return nil
	}
	return cases.open[len(cases.open)-1]
}

// condition returns whether a command in function runs only where a named
// case branch is selected, and the own branch it is in, if any.
func (cases *laneCases) condition(function int) (named bool, own *laneOwnBranch) {
	if cases.blind {
		return false, nil
	}
	for _, c := range cases.open {
		if c.function == function {
			named = named || c.cond
			if c.branch != nil {
				own = c.branch
			}
		}
	}
	return named, own
}

// shift records a shift or a set of the lane's own parameters, outside a
// function: one shift directly in an own branch, run whenever the branch
// is, drops that many parameters, and any other changes $1.
func (cases *laneCases) shift(command []string, scopes []int, certain bool) {
	if cases.own == nil || len(command) == 0 {
		return
	}
	switch command[0] {
	case "shift":
		var c *laneCase
		for _, each := range cases.open {
			if each.own {
				c = each
			}
		}
		count, err := 1, error(nil)
		if len(command) > 1 {
			count, err = strconv.Atoi(command[1])
		}
		if c == nil || c.branch == nil || !certain || len(scopes) != c.depth+1 || err != nil {
			cases.own.changed = true
			return
		}
		c.branch.shift = map[bool]int{true: count, false: -1}[c.branch.shift == 0]
	case "set":
		for at := 1; at < len(command); at++ {
			option := command[at]
			if option == "-" || option == "--" || !strings.HasPrefix(option, "-") && !strings.HasPrefix(option, "+") {
				cases.own.changed = true
				return
			}
			if strings.Contains(option[1:], "o") {
				at++
			}
		}
	}
}

// laneOwnSubject is the lane's own first parameter as a case subject.
var laneOwnSubject = regexp.MustCompile(`^\$(?:1|\{1\})$`)

// safe reports whether no caller selects an own branch it does not
// name, by reading each caller's arguments as the case on $1 reads them in a
// loop: each argument it reaches must be a literal word that a literal
// pattern of an earlier branch than any other matches, and that branch's one
// shift drops the arguments it reads. It marks each branch an argument
// selects.
func (own *laneOwnCase) safe(calls [][]string) bool {
	if own.changed || len(calls) == 0 {
		return false
	}
	for _, args := range calls {
		for at := 0; at < len(args); {
			var chosen *laneOwnBranch
			for _, branch := range own.branches {
				if !branch.literal {
					return false
				}
				if slices.Contains(branch.patterns, args[at]) {
					chosen = branch
					break
				}
			}
			if chosen == nil {
				return false
			}
			chosen.selected = true
			if chosen.shift < 1 {
				return false
			}
			at += chosen.shift
		}
	}
	return true
}

// laneCaseNamed reports whether a case subject is one expansion of a variable
// whose value no lane can fix: a step's env sets it to a github context
// value, bash sets no variable by its name, and the step's text names it only
// to expand it. A step sources no file, which this reader refuses.
func laneCaseNamed(subject string, env map[string]string, run string) bool {
	match := laneCaseVariable.FindStringSubmatch(subject)
	if match == nil {
		return false
	}
	name := match[1] + match[2]
	if !laneGithubValue.MatchString(env[name]) || laneBashNames.MatchString(name) {
		return false
	}
	read := regexp.MustCompile(`\$(?:` + name + `\b|\{` + name + `\})`)
	return !regexp.MustCompile(`\b` + name + `\b`).MatchString(read.ReplaceAllString(run, " "))
}

var (
	laneCaseVariable = regexp.MustCompile(`^\$(?:([A-Za-z_][A-Za-z0-9_]*)|\{([A-Za-z_][A-Za-z0-9_]*)\})$`)
	laneGithubValue  = regexp.MustCompile(`^\$\{\{ *github\.[A-Za-z0-9_.-]+ *\}\}$`)
	laneBashNames    = regexp.MustCompile(`^(?:BASH.*|COMP_.*|GITHUB_.*|RUNNER_.*|LINENO|PWD|OLDPWD|UID|EUID|PPID|RANDOM|SRANDOM|SECONDS|SHLVL|HOSTNAME|HOSTTYPE|OSTYPE|MACHTYPE|GROUPS|FUNCNAME|OPTIND|OPTARG|OPTERR|REPLY|PIPESTATUS|EPOCHSECONDS|EPOCHREALTIME|HISTCMD|COLUMNS|LINES|DIRSTACK|SHELLOPTS|IFS|PATH|ENV|PS4)$`)
)
