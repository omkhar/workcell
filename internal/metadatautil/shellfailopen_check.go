// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/omkhar/workcell/internal/rootio"
)

// CheckShellFailOpen rejects a shell idiom that hides the exit status of a find, git, gh, docker or getent call.
//
// A `<(` or `>(`, a `$(...)` under `local` or in a list, `|| true` and `2>/dev/null` each turn "the tool failed" into "there was nothing to find", so a gate passes on an empty answer. shellcheck does not flag them.
//
// A hit is accepted when its own command or the command right after it captures the status (see shellFailOpenHandled). A process substitution is always a hit: bash never propagates its status.
//
// The check is a ratchet. policy/shell-fail-open-baseline.tsv records the hits each file carries today; a count above its row fails, and so does a count below it, so the row drops with the repair.
//
// The scan reads lines with shellWords, the reader ShellInvocations uses, so quotes, comments, heredocs and an open `$(` mean the same thing to both.
func CheckShellFailOpen(rootDir string) error {
	files, err := shellFailOpenFiles(rootDir)
	if err != nil {
		return err
	}
	counts := map[shellFailOpenKey]int{}
	details := map[shellFailOpenKey][]string{}
	for _, rel := range files {
		content, readErr := rootio.ReadFileNoFollow(filepath.Join(rootDir, rel), rel, shellFailOpenMaxBytes)
		if readErr != nil {
			return readErr
		}
		if !isShellSource(rel, content) {
			continue
		}
		findings, scanErr := ShellFailOpenFindings(string(content))
		if scanErr != nil {
			return fmt.Errorf("%s: %w", rel, scanErr)
		}
		for _, finding := range findings {
			key := shellFailOpenKey{path: rel, rule: finding.Rule}
			counts[key]++
			details[key] = append(details[key], fmt.Sprintf("%s:%d", rel, finding.Line))
		}
	}
	baseline, err := loadShellFailOpenBaseline(filepath.Join(rootDir, shellFailOpenBaselinePath))
	if err != nil {
		return err
	}
	var failures []string
	for key, count := range counts {
		allowed := baseline[key]
		switch {
		case count == allowed:
		case count < allowed:
			failures = append(failures, fmt.Sprintf("%s: %d %s hit(s), baseline still allows %d; lower the baseline row to %d",
				key.path, count, key.rule, allowed, count))
		default:
			failures = append(failures, fmt.Sprintf("%s: %d %s hit(s), baseline allows %d; capture the status\n    %s",
				key.path, count, key.rule, allowed, strings.Join(details[key], "\n    ")))
		}
	}
	for key := range baseline {
		if _, ok := counts[key]; !ok {
			failures = append(failures, fmt.Sprintf("%s: stale baseline row for %s; remove the row", key.path, key.rule))
		}
	}
	if len(failures) == 0 {
		return nil
	}
	sort.Strings(failures)
	return fmt.Errorf("shell fail-open check failed:\n  %s", strings.Join(failures, "\n  "))
}

const (
	shellFailOpenBaselinePath = "policy/shell-fail-open-baseline.tsv"
	shellFailOpenMaxBytes     = 8 << 20

	ruleProcessSubstitution = "process-substitution"
	ruleCommandSubstitution = "command-substitution"
	ruleOrTrue              = "or-true"
	ruleDevNull             = "dev-null"
)

type shellFailOpenKey struct{ path, rule string }

// ShellFailOpenFinding is one hit and the line it starts on.
type ShellFailOpenFinding struct {
	Rule string
	Line int
}

var (
	shellFailOpenTools      = `(?:find|git|gh|docker|getent)`
	shellToolName           = regexp.MustCompile(`^(?:[^\s;&|()<>]*/)?` + shellFailOpenTools + `$`)
	shellCommandPosition    = "(?:^|[;(`\n]|(?:^|[^<>])[&|]|(?:^|[;\n]|\\bin)\\s*\\(?[^\\s;&|()]+(?:\\s*\\|\\s*[^\\s;&|()]+)*\\))\\s*(?:(?:[!{]|if|then|do|else|elif|while|until|coproc(?:\\s+[A-Za-z_][A-Za-z0-9_]*\\s+(?:\\{|if|while|until))?|time(?:\\s+-p)?(?:\\s+--)?|builtin|command(?:\\s+-p)?(?:\\s+--)?|xargs(?:\\s+(?:-[adEILnPs]\\s+\\S+|-\\S+))*|sudo(?:\\s+(?:-[CDghprTtUu]\\s+\\S+|-\\S+))*|(?:\\S*/)?env(?:\\s+(?:-[CPSu]\\s+\\S+|--(?:chdir|split-string|unset)\\s+\\S+|-\\S+))*|[A-Za-z_][A-Za-z0-9_]*=[^\\s(]*)\\s+)*" // shellCommandPosition ends where a command word starts: after the start, an operator or an opener, then any reserved word, assignment, or xargs, sudo or env with its options and their values. `git` in a path or an argument is not a call, and nor is the target of a >| or >& redirection.
	shellToolCommand        = regexp.MustCompile(shellCommandPosition + `(?:(?:\S*/)?(?:env|sudo|xargs|nice|nohup|stdbuf|setsid|ionice|timeout|chrt|taskset|time)(?:\s+[^\s;&|()<>]+)*?\s+)?(?:[^\s;&|()<>]*/)?` + shellFailOpenTools + `(?:[\s;&|)<>]|$)`)                                                                                                                                                                                                                                                                               // The tool word ends at a blank, an operator, a closer or a redirection, since bash reads git||true as git then ||. After env, sudo or xargs any later word may be the tool, so their options need no table.
	shellUnnamedCommand     = regexp.MustCompile(shellCommandPosition + `(?:\$[^(]|"|(?:eval|source|\.)\s)`)                                                                                                                                                                                                                                                                                                                                                                                                                              // shellUnnamedCommand is a command word this reader cannot name: an expansion, a quoted word or a command that runs text. It may run a tool, so a substitution that holds one counts as a tool substitution.
	shellTestExpr           = regexp.MustCompile(`\[\[[^]]*\]\]`)                                                                                                                                                                                                                                                                                                                                                                                                                                                                         // shellTestExpr is a [[ ]] test, whose || and && join no commands.
	shellSubstOpen          = regexp.MustCompile(`\$\((?:[^(]|$)`)
	shellProcessSubst       = regexp.MustCompile(`(?:^|[^<>$])[<>]\(`)                                                                                             // shellProcessSubst is a process substitution, <( or >(, as a redirection or an argument.
	shellDevNull            = regexp.MustCompile(`(?:(?:^|[^0-9])2>[>|]?|&>[>|]?|>&)\s*"?/dev/null"?(?:[\s;&|)<>]|$)|>[>|]?\s*"?/dev/null"?\s+2>&1`)               // shellDevNull sends stderr to /dev/null by 2>, 2>>, 2>|, &> or >/dev/null 2>&1.
	shellFailOpenOr         = regexp.MustCompile(`\|\|\s*`)                                                                                                        // shellFailOpenOr is the || that runs a handler.
	shellFailOpenExits      = regexp.MustCompile(`^\s*(?:(?:exit|return)(?:\s+--)?(?:\s+([-+]?[0-9]+))?\s*$|(?:\w+_)?(?:die|fail\w*|error\w*)(?:\s|$)|false\s*$)`) // shellFailOpenExits is a command that ends the script or the function with a failure: exit or return with no operand or a literal bash reads modulo 256 as nonzero (see shellFailOpenExit), die, fail*, error* (with any NAME_ prefix) or false. An exit 0, an assignment such as failed=1 or an echo fail reports nothing.
	shellFailOpenTerminal   = regexp.MustCompile(`^\s*(?:exit|return|(?:\w+_)?(?:die|fail\w*|error\w*))(?:\s|$)`)                                                  // shellFailOpenStatusRead reads the status the command before it left, so it captures a hit only in the command right after the hit. A wait returns the status of the job it names, never of a substitution. shellFailOpenTerminal is an exit, return or die-style command, which ends the script or function, so the list after it never runs.
	shellFailOpenStatusRead = regexp.MustCompile(`\$\?|PIPESTATUS`)
	shellFailOpenRunsFirst  = regexp.MustCompile(shellSubstOpen.String() + "|`|[<>]\\(|\\||(?:^|[^<>])&") // shellFailOpenRunsFirst is a substitution, a process substitution or an operator, which runs a command of its own before a later word. The & of a >& or <& redirection runs nothing.
	shellFailOpenTested     = regexp.MustCompile(`^\s*(?:if|elif|while|until)\b`)                         // shellFailOpenTested is a command that is the test of an if/while. It covers a substitution, never a `done < <(` loop header, where the loop's own while says nothing about the inner command.
	shellFailOpenList       = regexp.MustCompile(`&&|\|\|`)                                               // shellFailOpenList is a && or || operator, which joins two commands.
	shellOutWord            = regexp.MustCompile(`[^\s;&|<>()]+`)
	// shellAssignment is a word that assigns a name, the only word that may
	// stand beside a substitution whose status the command keeps.
	shellAssignment   = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(?:\[[^]]*\])?\+?=`)
	shellRedirection  = regexp.MustCompile(`^[0-9]*[<>]`) // shellRedirection is a redirection word, which leaves the status alone; shellRedirectOnly is one whose target is the next word.
	shellRedirectOnly = regexp.MustCompile(`^[0-9]*[<>]+&?$`)
	shellLaterSubst   = regexp.MustCompile("\\$\\((?:[^(]|$)|`") // shellLaterSubst is a command substitution, not an arithmetic $((.
)

// ShellFailOpenFindings reports the fail-open hits in one script. It skips heredoc bodies, joins a statement that runs past its line, and ignores quoted text that opens no command substitution.
func ShellFailOpenFindings(script string) ([]ShellFailOpenFinding, error) {
	type logical struct {
		number    int
		raw, code string
	}
	var statements []logical
	var current logical
	var lines []string
	var heredocs []heredoc
	var stack []byte
	var openQuote byte
	depth, number := 0, 0
	for line := range strings.Lines(script) {
		number++
		text := strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		// Bash still runs a $( inside a double-quoted span that runs past its line, so the line is read inside the span rather than skipped.
		reopen := ""
		if openQuote != 0 {
			reopen = quoteOpener(openQuote)
		} else if len(heredocs) > 0 {
			if heredocs[0].endsAt(text) {
				heredocs = heredocs[1:]
			}
			continue
		}
		if len(lines) == 0 {
			current.number = number
		}
		words, opened, quote, rest, continues, comment := shellWords(reopen+text, stack)
		heredocs, openQuote, stack = append(heredocs, opened...), quote, rest
		depth = substitutionDepth(depth, words)
		code := shellBackticksAsSubsts(strings.TrimSuffix(text, comment))
		// A line that ends on && || or | carries its command onto the next one, so a handler written there belongs to the same command.
		fields := strings.Fields(shellCodeOnly(code))
		operator := len(fields) > 0 && continuesLine(fields[len(fields)-1])
		switch {
		case continues:
			// Bash joins a continued line with nothing between the halves.
			code = strings.TrimSuffix(code, "\\")
		case operator:
			code += " "
		default:
			code += "\n"
		}
		lines = append(lines, code)
		if continues || operator || len(stack) > 0 || depth > 0 || openQuote != 0 {
			continue
		}
		current.raw = strings.TrimSuffix(strings.Join(lines, ""), "\n")
		current.code = shellCodeOnly(current.raw)
		statements = append(statements, current)
		current, lines = logical{}, nil
	}
	if len(lines) > 0 {
		// A substitution the script never closes still holds its hits.
		current.raw = strings.TrimSuffix(strings.Join(lines, ""), "\n")
		current.code = shellCodeOnly(current.raw)
		statements = append(statements, current)
	}
	var findings []ShellFailOpenFinding
	for index, statement := range statements {
		if strings.TrimSpace(statement.code) == "" {
			continue
		}
		// The last entry is the first command of the next statement, which reads the status this statement leaves.
		commands, raws := shellFailOpenCommands(statement.code, statement.raw)
		commands, raws = append(commands, ""), append(raws, "")
		for next := index + 1; next < len(statements); next++ {
			if strings.TrimSpace(statements[next].code) != "" {
				nextCommands, nextRaws := shellFailOpenCommands(statements[next].code, statements[next].raw)
				commands[len(commands)-1], raws[len(raws)-1] = nextCommands[0], nextRaws[0]
				break
			}
		}
		// Each command in the statement counts on its own, so a second hit of the same rule on one line raises the file's count.
		hits := map[string]int{}
		start := 0
		for at, command := range commands[:len(commands)-1] {
			after, end := commands[at+1], start+len(raws[at])
			start = end + 1
			// A process substitution is always a hit; see the contract above.
			hits[ruleProcessSubstitution] += len(shellProcessSubst.FindAllStringIndex(command, -1))
			toolSubst := len(shellToolSubsts(command)) > 0
			// A status read or a handler covers only a call written before it.
			later := func() []string {
				later := slices.Clone(commands[at+1 : len(commands)-1])
				for _, each := range statements[index+1:] {
					codes, _ := shellFailOpenCommands(each.code, each.raw)
					later = append(later, codes...)
				}
				return later
			}
			// testedFrom reports whether the status of the call that starts text, a suffix of the command, is read by anything after it.
			testedFrom := func(text string) bool {
				rest := text[shellFailOpenFirstCall(text):]
				captured := shellFailOpenReadsOwnStatus(rest) || shellFailOpenHandled(rest, later)
				return captured || shellFailOpenReadsStatus(after) || shellFailOpenTested.MatchString(text)
			}
			tested := testedFrom(command)
			// Each occurrence counts, so a || true && … || true list on one command cannot stand in for two baselined hits.
			count := func(hit bool, pattern *regexp.Regexp, text string) int {
				if !hit {
					return 0
				}
				return len(pattern.FindAllStringIndex(text, -1))
			}
			if toolSubst && (!tested || shellSubstHidden(command)) {
				hits[ruleCommandSubstitution] += len(shellToolSubsts(command))
			}
			hits[ruleOrTrue] += shellSwallowingHandlers(command, later, false)
			// A redirect belongs to the operand of the && || or | list it is written in, so git fetch || printf x 2>/dev/null hides no git status, and each operand's own status test counts, so the && after git fetch does not cover git gc 2>/dev/null. A redirect inside a substitution belongs to the command there, so $(git x 2>/dev/null) hides git's status while foo "$(git x)" 2>/dev/null hides foo's; each is read in its own text.
			offset := 0
			for _, operand := range shellListOperands(command) {
				untested := !testedFrom(command[offset:])
				if outside := shellOutsideSubsts(operand); shellToolCommand.MatchString(outside) {
					hits[ruleDevNull] += count(untested, shellDevNull, outside)
				}
				for _, start := range shellToolSubsts(operand) {
					body, _ := shellSubstCall(operand, start)
					hits[ruleDevNull] += count(untested, shellDevNull, body)
				}
				offset += len(operand)
			}
		}
		for _, rule := range []string{ruleProcessSubstitution, ruleCommandSubstitution, ruleOrTrue, ruleDevNull} {
			for range hits[rule] {
				findings = append(findings, ShellFailOpenFinding{Rule: rule, Line: statement.number})
			}
		}
	}
	return findings, nil
}

// shellFailOpenExit reports whether command is a shellFailOpenExits command whose exit or return status, modulo 256 as bash takes it, is not zero.
func shellFailOpenExit(command string) bool {
	match := shellFailOpenExits.FindStringSubmatch(command)
	if match == nil || match[1] == "" {
		return match != nil
	}
	status, err := strconv.ParseInt(match[1], 10, 64)
	return err != nil || status%256 != 0 // bash exits 2 on an operand out of range
}

// shellFailOpenHandled reports whether the first || runs a failure branch: a shellFailOpenExits command, or a { } group ending in one; later spans a group.
func shellFailOpenHandled(rest string, later func() []string) bool {
	outside := shellOutsideSubsts(rest)
	loc := shellFailOpenOr.FindStringIndex(outside)
	if loc == nil {
		return false
	}
	handler := outside[loc[1]:]
	for opener, closer := range map[string]string{"{": "}", "(": ")"} { // a group's or a subshell's status is its last command's
		if group, grouped := strings.CutPrefix(handler, opener); grouped {
			return shellFailOpenBranchExits(append(strings.Split(strings.Replace(group, closer, "; "+closer, 1), ";"), later()...), closer)
		}
	}
	return shellFailOpenExit(handler[:strings.IndexAny(handler+";", ";&|)}")])
}

// shellFailOpenEnds reports whether a handler ends the script at its own level with an exit, return or die-style command; a ( ) subshell's exit ends only the subshell.
func shellFailOpenEnds(handler string) bool {
	group, grouped := strings.CutPrefix(handler, "{")
	if !grouped {
		group = handler[:strings.IndexAny(handler+";", ";&|)}")]
	}
	group, _, _ = strings.Cut(group, "}")
	commands := slices.DeleteFunc(strings.Split(group, ";"), func(c string) bool { return strings.TrimSpace(c) == "" })
	return !strings.HasPrefix(handler, "(") && len(commands) > 0 && shellFailOpenTerminal.MatchString(commands[len(commands)-1]) // the last command only; an exit in a nested branch may not run
}

// shellFailOpenNesting returns the nesting change a command's first word makes.
func shellFailOpenNesting(command string) int {
	for _, each := range strings.Fields(command) {
		if !slices.Contains([]string{"then", "do", "else", "{", "!"}, each) {
			return controlWords[each]
		}
	}
	return 0
}

// shellFailOpenBranchExits reports whether the branch leaves a failure at its own depth: a failing exit or a last shellFailOpenExits command before closers.
func shellFailOpenBranchExits(codes []string, closers ...string) bool {
	depth, failing, ran := 0, false, false
	for _, each := range codes {
		fields := strings.Fields(each)
		if len(fields) > 0 && fields[0] == "then" {
			fields = fields[1:]
		}
		if depth == 0 && len(fields) > 0 {
			if slices.Contains(closers, fields[0]) {
				return failing
			}
			if len(fields) == 1 && (fields[0] == "exit" || fields[0] == "return") {
				return !ran || failing // a bare exit keeps the last command's status
			}
			failing = shellFailOpenExit(strings.Join(fields, " "))
			if failing && (fields[0] == "exit" || fields[0] == "return") {
				return true
			}
		}
		depth += shellFailOpenNesting(each)
		ran = ran || len(fields) > 0
	}
	return false
}

// shellFailOpenFirstCall returns where a command's first tool call starts, or its length.
func shellFailOpenFirstCall(command string) int {
	first := len(command)
	for _, re := range []*regexp.Regexp{shellToolCommand, shellProcessSubst} {
		if loc := re.FindStringIndex(command); loc != nil {
			first = min(first, loc[0])
		}
	}
	if starts := shellToolSubsts(command); len(starts) > 0 {
		first = min(first, starts[0])
	}
	return first
}

// shellOutsideSubsts returns the command with each $(...) body removed, so an operator inside a substitution is not read as one of the command's.
func shellOutsideSubsts(command string) string {
	outside, _ := withoutExpansions(command)
	return outside
}

// shellToolSubsts returns where each $( starts whose body runs a tool or an unnamed command.
func shellToolSubsts(command string) []int {
	var starts []int
	for _, loc := range shellSubstOpen.FindAllStringIndex(command, -1) {
		if _, call := shellSubstCall(command, loc[0]); call >= 0 {
			starts = append(starts, loc[0])
		}
	}
	return starts
}

// shellSubstCall returns the body of the $( at start, [[ ]] tests emptied, and where its first tool or unnamed command ends, or -1.
func shellSubstCall(command string, start int) (body string, call int) {
	body = shellTestExpr.ReplaceAllString(strings.TrimSuffix(command[start+2:shellSubstEnd(command, start)], ")"), "[[ ]]")
	call = -1
	for _, re := range []*regexp.Regexp{shellToolCommand, shellUnnamedCommand} {
		if loc := re.FindStringIndex(body); loc != nil && (call < 0 || loc[1] < call) {
			call = loc[1]
		}
	}
	return body, call
}

// shellFailOpenReadsStatus reports whether a command reads $? or PIPESTATUS before anything in it runs, so the read sees the status the command before it left. In discard=$(true) rc=$? the read sees the status of true.
func shellFailOpenReadsStatus(command string) bool {
	read := shellFailOpenStatusRead.FindStringIndex(command)
	return read != nil && !shellFailOpenRunsFirst.MatchString(command[:read[0]])
}

// shellFailOpenReadsOwnStatus reports whether the command that starts with a call reads $? or PIPESTATUS with nothing run between, as x=$(git a) || rc=$? does.
func shellFailOpenReadsOwnStatus(rest string) bool {
	if strings.HasPrefix(rest, "$(") {
		rest = rest[shellSubstEnd(rest, 0):]
	}
	if loc := shellFailOpenList.FindStringIndex(rest); loc != nil {
		rest = rest[:loc[0]] + " " + rest[loc[1]:]
	}
	return shellFailOpenReadsStatus(rest)
}

// shellSubstHidden reports whether a tool substitution's status is replaced: by a command it is an argument of, as local x=$(git ...), or a later substitution.
func shellSubstHidden(command string) bool {
	for _, start := range shellToolSubsts(command) {
		prefix := command[:start]
		words := strings.Fields(prefix[strings.LastIndexAny(prefix, ";&|(`")+1:])
		for len(words) > 0 && slices.Contains([]string{"if", "elif", "while", "until", "then", "do", "else", "!", "{", "time"}, words[0]) {
			for words = words[1:]; len(words) > 0 && (words[0] == "-p" || words[0] == "--"); {
				words = words[1:] // time -p --
			}
		}
		// The simple command runs on past the substitution to the next operator or closer.
		tail := command[shellSubstEnd(command, start):]
		tail = tail[:strings.IndexAny(tail+";", ";&|)")]
		if shellLaterSubst.MatchString(tail) {
			return true
		}
		after := strings.Fields(tail)
		if len(after) > 0 && strings.HasPrefix(tail, after[0]) {
			after = after[1:] // the rest of the word the substitution is in
		}
		for index := 0; index < len(after); index++ {
			if shellRedirection.MatchString(after[index]) {
				if shellRedirectOnly.MatchString(after[index]) {
					index++
				}
				continue
			}
			words = append(words, after[index])
		}
		for _, each := range words {
			if !shellAssignment.MatchString(each) {
				return true
			}
		}
	}
	return false
}

// shellSubstEnd returns the index just past the ) that closes the $( at start, or the command's length when the command does not close it.
func shellSubstEnd(command string, start int) int {
	depth := 0
	for index := start + 1; index < len(command); index++ {
		switch command[index] {
		case '(':
			depth++
		case ')':
			if depth--; depth == 0 {
				return index + 1
			}
		}
	}
	return len(command)
}

// shellFailOpenCommands splits code, and raw at the same places, at each ; and line end outside a parenthesis or brace group, so each check reads one command.
func shellFailOpenCommands(code, raw string) (commands, raws []string) {
	depth, start := 0, 0
	for index := 0; index < len(code); index++ {
		switch code[index] {
		case '\\':
			index++
		case '(':
			depth++
		case ')':
			depth = max(depth-1, 0)
		case '{', '}':
			// A brace group is one command, so its redirect stays with its calls.
			before := index == 0 || strings.ContainsRune(" \t;\n", rune(code[index-1]))
			if code[index] == '{' && before && index+1 < len(code) && strings.ContainsRune(" \t\n", rune(code[index+1])) {
				depth++
			} else if code[index] == '}' && before {
				depth = max(depth-1, 0)
			}
		case ';', '\n':
			if depth == 0 {
				commands = append(commands, code[start:index])
				raws = append(raws, raw[start:index])
				start = index + 1
			}
		}
	}
	commands, raws = append(commands, code[start:]), append(raws, raw[start:])
	return commands, raws
}

// shellListOperands splits a command at each && || | |& or lone & outside a substitution, with the operator left on the operand it ends; the & of &> >& or <& is a redirection, not an operator.
func shellListOperands(command string) []string {
	var operands []string
	depth, start := 0, 0
	for index := 0; index < len(command); index++ {
		switch command[index] {
		case '\\':
			index++
		case '(':
			depth++
		case ')':
			depth = max(depth-1, 0)
		case '&', '|':
			if depth != 0 {
				continue
			}
			width := 1
			if index+1 < len(command) && (command[index+1] == '&' || command[index+1] == '|') {
				width = 2 // && || or |&
			}
			redirect := width == 1 && (command[index] == '&' && index+1 < len(command) && command[index+1] == '>' ||
				index > 0 && strings.ContainsRune("<>|", rune(command[index-1])) && (command[index] == '&' || command[index-1] == '>'))
			if !redirect && (command[index] == '|' || command[index] == '&') {
				operands = append(operands, command[start:index+width])
				start = index + width
			}
			index += width - 1
		}
	}
	return append(operands, command[start:])
}

// shellSwallowingHandlers counts each || after a pipeline that runs a tool whose handler neither fails, as shellFailOpenHandled reads it, nor reads the status.
func shellSwallowingHandlers(segment string, later func() []string, inSubst bool) int {
	count, end, offset, pipeline, failing := 0, 0, 0, "", false
	for _, operand := range shellListOperands(segment) {
		offset += len(operand)
		ends := func(suffix string) bool { return strings.HasSuffix(operand, suffix) }
		if pipeline += operand; ends("&&") || ends("|&") || ends("|") && !ends("||") {
			continue // a handler covers the && list and, under pipefail, every pipeline stage before it
		}
		left := shellTestExpr.ReplaceAllString(pipeline, "[[ ]]")
		pipeline = ""
		// A tool's failure, or one a failing handler such as false passes on, reaches this ||, as in git fetch || false || true.
		tool := failing || shellToolCommand.MatchString(shellOutsideSubsts(left)) || len(shellToolSubsts(left)) > 0 ||
			inSubst && shellUnnamedCommand.MatchString(shellOutsideSubsts(left)) // a substitution counts a named call
		failing = false
		if !tool || !ends("||") || shellFailOpenReadsOwnStatus(shellOutsideSubsts(segment[offset-2:])) {
			continue
		}
		rest := shellOutsideSubsts(segment[offset-2:])
		handled := shellFailOpenHandled(rest, later)
		if !handled {
			count++ // the || ends an operand that runs a tool
		}
		failing = handled && !shellFailOpenEnds(strings.TrimSpace(rest[2:])) // a handler that fails without ending the script hands the failure on
	}
	for _, start := range shellToolSubsts(segment) {
		if start >= end { // a substitution's handlers are read in its own body
			end = shellSubstEnd(segment, start)
			body, _ := shellSubstCall(segment, start)
			count += shellSwallowingHandlers(body, func() []string { return nil }, true)
		}
	}
	return count
}

// shellBackticksAsSubsts spells each `...` command substitution outside single quotes as $(...), so one reader covers both.
func shellBackticksAsSubsts(code string) string {
	var out strings.Builder
	single, escaped, open := false, false, false
	for _, r := range code {
		if r == '`' && !single && !escaped {
			open = !open
			out.WriteString(map[bool]string{true: "$(", false: ")"}[open])
			continue
		}
		single, escaped = single != (r == '\'' && !escaped), r == '\\' && !escaped && !single
		out.WriteRune(r)
	}
	return out.String()
}

// shellCodeOnly blanks quoted text, so a message that names git is not a command; a $( inside double quotes opens code again. The result keeps the line's length, so an offset in it is the same offset in the line.
func shellCodeOnly(line string) string {
	out := []byte(line)
	type frame struct {
		quote  byte
		parens int
	}
	stack := []frame{{}}
	for i := 0; i < len(line); i++ {
		top, c := &stack[len(stack)-1], line[i]
		switch {
		case top.quote == '\'':
			if c == '\'' {
				top.quote = 0
			}
			out[i] = '"'
		case top.quote == ansiCQuote:
			// A backslash escapes the next byte of a $'...' span, even a '.
			out[i] = '"'
			if c == '\\' && i+1 < len(line) {
				i++
				out[i] = '"'
			} else if c == '\'' {
				top.quote = 0
			}
		case top.quote == '"' && c == '$' && i+1 < len(line) && line[i+1] == '(':
			stack = append(stack, frame{})
			i++
		case top.quote == '"':
			out[i] = '"'
			if c == '\\' && i+1 < len(line) {
				i++
				out[i] = '"'
			} else if c == '"' {
				top.quote = 0
			}
		case c == '\\' && i+1 < len(line):
			out[i+1] = '"' // an escaped byte is quoted text, as in \;
			i++
		case c == '$' && i+1 < len(line) && line[i+1] == '\'':
			top.quote, out[i+1] = ansiCQuote, '"'
			i++
		case c == '\'' || c == '"':
			top.quote, out[i] = c, '"'
		case c == '(':
			top.parens++
		case c == ')' && top.parens == 0 && len(stack) > 1:
			stack = stack[:len(stack)-1]
		case c == ')':
			top.parens = max(top.parens-1, 0)
		}
	}
	// A word whose quoted fragments spell a tool, true, : or /dev/null is that word.
	for _, loc := range shellOutWord.FindAllStringIndex(string(out), -1) {
		raw := line[loc[0]:loc[1]]
		words, _, _, _, _, _ := shellWords(raw, nil)
		if !strings.ContainsAny(raw, `'"\`) || len(words) != 1 {
			continue
		}
		value := words[0].text
		if decoded, err := strconv.Unquote(`"` + strings.ReplaceAll(value, `"`, `\"`) + `"`); strings.Contains(raw, "$'") && err == nil {
			value = decoded
		}
		if value == "true" || value == ":" || value == "/dev/null" || shellToolName.MatchString(value) {
			copy(out[loc[0]:loc[1]], value+strings.Repeat(" ", loc[1]-loc[0]))
		}
	}
	return string(out)
}

func isShellSource(rel string, content []byte) bool {
	if slices.Contains([]string{".sh", ".bash", ".ksh", ".zsh"}, filepath.Ext(rel)) { // a sourced module needs no shebang
		return true
	}
	first, _, _ := strings.Cut(string(content), "\n")
	words := strings.Fields(strings.TrimPrefix(first, "#!")) // the interpreter, after env and its options and assignments
	for len(words) > 1 && (filepath.Base(words[0]) == "env" || strings.HasPrefix(words[0], "-") || strings.Contains(words[0], "=")) {
		words = words[1:]
	}
	return strings.HasPrefix(first, "#!") && len(words) > 0 && slices.Contains([]string{"sh", "bash", "dash", "ksh", "zsh"}, filepath.Base(words[0]))
}

func shellFailOpenFiles(rootDir string) ([]string, error) {
	listing, err := exec.Command("git", "-C", rootDir, "ls-files", "-z", "--", "scripts", "runtime/container").Output()
	if err != nil {
		return nil, fmt.Errorf("list tracked scripts: %w", err)
	}
	var files []string
	for _, path := range strings.Split(string(listing), "\x00") {
		if path != "" {
			files = append(files, path)
		}
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("tracked script listing is empty; refusing a vacuous pass")
	}
	return files, nil
}

// loadShellFailOpenBaseline reads PATH<TAB>RULE<TAB>COUNT<TAB>REASON rows. The reason is required, so a row cannot be added without saying why it stays.
func loadShellFailOpenBaseline(path string) (map[shellFailOpenKey]int, error) {
	content, err := rootio.ReadFileNoFollow(path, shellFailOpenBaselinePath, shellFailOpenMaxBytes)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", shellFailOpenBaselinePath, err)
	}
	baseline := map[shellFailOpenKey]int{}
	for number, line := range strings.Split(string(content), "\n") {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 4 || strings.TrimSpace(fields[3]) == "" {
			return nil, fmt.Errorf("%s:%d: expected PATH, RULE, COUNT and a non-empty REASON", shellFailOpenBaselinePath, number+1)
		}
		switch fields[1] {
		case ruleProcessSubstitution, ruleCommandSubstitution, ruleOrTrue, ruleDevNull:
		default:
			return nil, fmt.Errorf("%s:%d: unknown rule %q", shellFailOpenBaselinePath, number+1, fields[1])
		}
		count, convErr := strconv.Atoi(fields[2])
		if convErr != nil || count <= 0 {
			return nil, fmt.Errorf("%s:%d: count must be a positive integer, found %q", shellFailOpenBaselinePath, number+1, fields[2])
		}
		key := shellFailOpenKey{fields[0], fields[1]}
		if _, dup := baseline[key]; dup {
			return nil, fmt.Errorf("%s:%d: duplicate row for %s %s", shellFailOpenBaselinePath, number+1, key.path, key.rule)
		}
		baseline[key] = count
	}
	return baseline, nil
}
