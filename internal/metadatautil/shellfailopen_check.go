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

// CheckShellFailOpen rejects a shell idiom that hides the exit status of a
// find, git, gh, docker or getent call.
//
// A `< <(` process substitution drops the status of the command inside it, a
// `$(...)` around one of those tools drops it under `local` or in a list,
// `|| true` discards it, and `2>/dev/null` hides the reason it failed. Each
// turns "the tool failed" into "there was nothing to find", so a gate passes on
// an empty answer. Review history holds about 26 accepted findings of this
// class. shellcheck does not flag it.
//
// A hit is accepted when its own command or the command right after it
// captures the status (see shellFailOpenCapture) or when the command states its
// case with a marker:
//
//	# fail-closed: <reason>
//
// The check is a ratchet. policy/shell-fail-open-baseline.tsv records the hits
// each file carries today; a count above its row fails, and so does a count
// below it, so the row drops with the repair.
//
// The scan reads lines with shellWords, the reader ShellInvocations uses, so
// quotes, comments, heredocs and an open `$(` mean the same thing to both.
// ShellInvocations drops the body of a compound command and the inside of `$(`
// and `<(`, where this class hides, so it reads only the `wait "$!"` that
// captures a process substitution: that wait is a command the script must
// run, which the shared evasion corpus can hide.
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
			failures = append(failures, fmt.Sprintf("%s: %d %s hit(s), baseline allows %d; capture the status, or state the reason with # %s <reason>\n    %s",
				key.path, count, key.rule, allowed, shellFailClosedTag, strings.Join(details[key], "\n    ")))
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
	shellFailClosedTag        = "fail-closed:"
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
	shellFailOpenTools = `(?:find|git|gh|docker|getent)`
	// shellCommandPosition ends where a command word starts: after the start,
	// an operator or an opener, then any reserved word, assignment, or xargs or
	// sudo with its options and their values. shellUnwrapped blanks the
	// commandWrappers. `git` in a path or an argument is not a call.
	shellCommandPosition = "(?:^|[;&|(`\n])\\s*(?:(?:[!{]|if|then|do|else|elif|while|until|time|builtin|xargs(?:\\s+(?:-[adEILnPs]\\s+\\S+|-\\S+))*|sudo(?:\\s+(?:-[CDghprTtUu]\\s+\\S+|-\\S+))*|[A-Za-z_][A-Za-z0-9_]*=[^\\s(]*)\\s+)*"
	shellCommandStart    = regexp.MustCompile(shellCommandPosition)
	shellField           = regexp.MustCompile(`\S+`)
	// The tool word ends at a blank, an operator, a closer or a redirection,
	// since bash reads git||true as git then ||.
	shellToolCommand = regexp.MustCompile(shellCommandPosition + shellFailOpenTools + `(?:[\s;&|)<>]|$)`)
	// shellUnnamedCommand is a command word this reader cannot name: an
	// expansion, a quoted word or a command that runs text. It may run a tool,
	// so a substitution that holds one counts as a tool substitution.
	shellUnnamedCommand = regexp.MustCompile(shellCommandPosition + `(?:\$[^(]|"|(?:eval|source|\.)\s)`)
	shellToolName       = regexp.MustCompile(`^` + shellFailOpenTools + `$`)
	// shellTestExpr is a [[ ]] test, whose || and && join no commands.
	shellTestExpr  = regexp.MustCompile(`\[\[[^]]*\]\]`)
	shellSubstOpen = regexp.MustCompile(`\$\((?:[^(]|$)`)
	// shellInnerMask is a command after the tool inside a substitution that
	// does not run only on its success, so its status replaces the tool's.
	// shellInnerHandler is a failure branch, which keeps a failed status.
	shellInnerMask    = regexp.MustCompile(`(?:[;\n&]|\|\|)\s*[^\s;&|)}]`)
	shellInnerHandler = regexp.MustCompile(`&&|[<>]&|\|\|\s*(?:exit|return|die|false)\b`)
	// shellInnerPipe is a later pipeline stage, whose status replaces the
	// tool's unless set -o pipefail ran before it and no set +o pipefail since.
	shellInnerPipe = regexp.MustCompile(`(?:^|[^|])\|\s*[^\s;&|)}]`)
	shellPipefail  = regexp.MustCompile(`\bset\s+([-+])[A-Za-z]*o\s+pipefail\b`)
	shellOrTrue    = regexp.MustCompile(`\|\|\s*true\b`)
	// shellProcessSubst is an input redirection from a process substitution,
	// with any blanks or a continued line between the < and the <(.
	shellProcessSubst = regexp.MustCompile(`(?:^|[^<>])<\s+<\(`)
	shellDevNull      = regexp.MustCompile(`2>\s*/dev/null`)
	// shellFailOpenCapture is a status capture or a completion sentinel: the
	// status is read ($?, PIPESTATUS), a failure branch runs
	// (|| exit, || return, || die, || fail*, || { ... }), the call is the test
	// of an if/while, or the block records completion (walk_completed, the house form of
	// scripts/verify-release-outputs.sh) or names a sentinel that proves it finished.
	shellFailOpenCapture = regexp.MustCompile(`\$\?|PIPESTATUS|\|\|\s*(?:exit|return|die\b|fail|\{|false\b|\w*(?:fail|die|error)\w*)|sentinel|\w_completed\b`)
	// shellFailOpenStatusRead reads the status the command before it left, so
	// it captures a hit only in the command right after the hit. A wait
	// returns the status of the job it names, never of a substitution.
	shellFailOpenStatusRead = regexp.MustCompile(`\$\?|PIPESTATUS`)
	// shellFailOpenTested is a command that is the test of an if/while. It
	// covers a substitution, never a `done < <(` loop header, where the loop's
	// own while says nothing about the inner command.
	shellFailOpenTested = regexp.MustCompile(`^\s*(?:if|elif|while|until)\b`)
	// shellFailOpenAnd is a && after the call, which makes the call its tested
	// left operand. A call on the right of && is tested by nothing.
	shellFailOpenAnd  = regexp.MustCompile(`&&\s*\S`)
	shellFailClosedRe = regexp.MustCompile(`#\s*` + shellFailClosedTag + `\s*\S`)
	// shellAssignment is a word that assigns a name, the only word that may
	// stand beside a substitution whose status the command keeps.
	shellAssignment = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(?:\[[^]]*\])?\+?=`)
	// shellRedirection is a redirection word, which leaves the status alone;
	// shellRedirectOnly is one whose target is the next word.
	shellRedirection  = regexp.MustCompile(`^[0-9]*[<>]`)
	shellRedirectOnly = regexp.MustCompile(`^[0-9]*[<>]+&?$`)
	// shellLaterSubst is a command substitution, not an arithmetic $((.
	shellLaterSubst = regexp.MustCompile("\\$\\((?:[^(]|$)|`")
)

// ShellFailOpenFindings reports the fail-open hits in one script. It skips
// heredoc bodies, reads a continued line, an open `$(` or a quoted span that
// runs past its line as one statement, ignores text inside single quotes and
// inside double quotes that open no command substitution, and takes a marker
// only from a real shell comment. A heredoc whose end it cannot find is an
// error, since the body it skips could hold any command.
func ShellFailOpenFindings(script string) ([]ShellFailOpenFinding, error) {
	type logical struct {
		number    int
		raw, code string
		marks     []int // where each fail-closed marker sits in raw
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
		// Bash still runs a $( inside a double-quoted span that runs past its
		// line, so the line is read inside the span rather than skipped.
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
		for _, body := range opened {
			if body.unresolved {
				return nil, fmt.Errorf("line %d: heredoc delimiter %q is spelled in a form this reader cannot resolve; use a plain or quoted word", number, body.delimiter)
			}
		}
		heredocs, openQuote, stack = append(heredocs, opened...), quote, rest
		depth = substitutionDepth(depth, words)
		code := strings.TrimSuffix(text, comment)
		if shellFailClosedRe.MatchString(comment) {
			current.marks = append(current.marks, len(strings.Join(lines, ""))+len(code))
		}
		// A line that ends on && || or | carries its command onto the next one,
		// so a handler written there belongs to the same command.
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
	if len(heredocs) > 0 {
		return nil, fmt.Errorf("heredoc ended by %q never closes", heredocs[0].delimiter)
	}
	if len(lines) > 0 {
		// A substitution the script never closes still holds its hits.
		current.raw = strings.TrimSuffix(strings.Join(lines, ""), "\n")
		current.code = shellCodeOnly(current.raw)
		statements = append(statements, current)
	}
	pipefail, nesting := false, 0
	var findings []ShellFailOpenFinding
	for index, statement := range statements {
		if strings.TrimSpace(statement.code) == "" {
			continue
		}
		// A marker covers the command it is written on, and a marker alone on
		// the line before covers the statement's first command.
		if index > 0 && strings.TrimSpace(statements[index-1].code) == "" && len(statements[index-1].marks) > 0 {
			statement.marks = append(statement.marks, 0)
		}
		// The last entry is the first command of the next statement, which
		// reads the status this statement leaves.
		commands, raws := shellFailOpenCommands(statement.code, statement.raw)
		commands, raws = append(commands, ""), append(raws, "")
		for next := index + 1; next < len(statements); next++ {
			if strings.TrimSpace(statements[next].code) != "" {
				nextCommands, nextRaws := shellFailOpenCommands(statements[next].code, statements[next].raw)
				commands[len(commands)-1], raws[len(raws)-1] = nextCommands[0], nextRaws[0]
				break
			}
		}
		hits := map[string]bool{}
		start := 0
		for at, command := range commands[:len(commands)-1] {
			after, end := commands[at+1], start+len(raws[at])
			marked := slices.ContainsFunc(statement.marks, func(mark int) bool { return mark >= start && mark <= end })
			// Only a set certain to run turns pipefail on; any set +o turns it off.
			change, runsSet := shellCommandScope(raws[at])
			if set := shellPipefail.FindStringSubmatch(command); set != nil && (set[1] == "+" || nesting == 0 && runsSet) {
				pipefail = set[1] == "-"
			}
			nesting = max(nesting+change, 0)
			if start = end + 1; marked {
				continue
			}
			toolSubst := len(shellToolSubsts(command)) > 0
			hasTool := shellToolCommand.MatchString(command) || toolSubst
			// A loop reports its inner status only through a check after it, such
			// as the walk_completed sentinel, so the next command may capture it.
			// A status read or a handler covers only a call written before it.
			rest := command[shellFailOpenFirstCall(command):]
			captured := shellFailOpenCapture.MatchString(rest)
			looped := captured || shellFailOpenCapture.MatchString(after) || shellFailOpenWaited(raws[at+1])
			tested := captured || shellFailOpenStatusRead.MatchString(after) || shellFailOpenTested.MatchString(command) ||
				shellFailOpenAnd.MatchString(shellOutsideSubsts(rest))
			hits[ruleProcessSubstitution] = hits[ruleProcessSubstitution] || shellProcessSubst.MatchString(command) && !looped
			hits[ruleCommandSubstitution] = hits[ruleCommandSubstitution] || toolSubst && (!tested || shellSubstMasked(command, pipefail))
			hits[ruleOrTrue] = hits[ruleOrTrue] || hasTool && shellOrTrue.MatchString(command)
			hits[ruleDevNull] = hits[ruleDevNull] || hasTool && shellDevNull.MatchString(command) && !tested
		}
		for _, rule := range []string{ruleProcessSubstitution, ruleCommandSubstitution, ruleOrTrue, ruleDevNull} {
			if hits[rule] {
				findings = append(findings, ShellFailOpenFinding{Rule: rule, Line: statement.number})
			}
		}
	}
	return findings, nil
}

// shellCommandScope returns the change in compound-command nesting the
// command makes, read with the shared controlWords and commandBrace, and
// whether a set starts it and runs whenever it does: not in a pipeline or
// after & in it, so not in a subshell.
func shellCommandScope(raw string) (change int, runsSet bool) {
	words, _, _, _, _, _ := shellWords(raw, nil)
	commands := splitCommands(words)
	for _, each := range commands {
		change += commandBrace(each)
		if len(each.args) > 0 && !each.args[0].quoted {
			change += controlWords[each.args[0].text]
		}
	}
	first := commands[0].args
	runsSet = len(first) > 0 && !first[0].quoted && first[0].text == "set" &&
		(len(commands) == 1 || commands[1].conditional)
	return change, runsSet
}

// shellFailOpenFirstCall returns where the first tool call or process
// substitution in a command starts, or the command's length when it has none.
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

// shellOutsideSubsts returns the command with each $(...) body removed, so
// an operator inside a substitution is not read as one of the command's.
func shellOutsideSubsts(command string) string {
	outside, _ := withoutExpansions(command)
	return outside
}

// shellToolSubsts returns where each $( starts whose body runs a tool or a
// command this reader cannot name, in any command of the body.
func shellToolSubsts(command string) []int {
	var starts []int
	for _, loc := range shellSubstOpen.FindAllStringIndex(command, -1) {
		if _, call := shellSubstCall(command, loc[0]); call >= 0 {
			starts = append(starts, loc[0])
		}
	}
	return starts
}

// shellSubstCall returns the text between the $( at start and its ), with
// each [[ ]] test emptied, and where the first tool or unnamed command in it
// ends, or -1 when it has none.
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

// shellFailOpenWaited reports whether a command is wait "$!", the one wait
// that returns a process substitution's status. ShellInvocations proves the
// wait runs and is the command's first.
func shellFailOpenWaited(raw string) bool {
	return slices.ContainsFunc(ShellInvocations(raw, "wait"), func(each Invocation) bool {
		return each.Position == 1 && slices.Equal(each.Args, []string{"$!"})
	})
}

// shellSubstMasked reports whether a tool substitution is an argument of a
// command, as in local x=$(git ...), [[ -n "$(git ...)" ]] or x=$(git ...)
// true, or is followed by a later substitution, as in x=$(git ...) y=$(true).
// That command's or that substitution's status replaces the tool's, so no
// test or handler can see it. Only a command made of assignments and
// redirections keeps the status of its last substitution. A command after
// the tool inside the substitution masks it too, as in $(git ... || true),
// and so does a later pipeline stage without pipefail.
func shellSubstMasked(command string, pipefail bool) bool {
	for _, start := range shellToolSubsts(command) {
		body, call := shellSubstCall(command, start)
		inner := shellInnerHandler.ReplaceAllString(body[call-1:], " ")
		if shellInnerMask.MatchString(inner) || !pipefail && shellInnerPipe.MatchString(inner) {
			return true
		}
		prefix := command[:start]
		words := strings.Fields(prefix[strings.LastIndexAny(prefix, ";&|(`")+1:])
		for len(words) > 0 && slices.Contains([]string{"if", "elif", "while", "until", "then", "do", "else", "!", "{", "time"}, words[0]) {
			words = words[1:]
		}
		// The simple command runs on past the substitution to the next
		// operator or closer.
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

// shellSubstEnd returns the index just past the ) that closes the $( at
// start, or the command's length when the command does not close it.
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

// shellFailOpenCommands splits a statement's code at each ; and line end that
// no parenthesis encloses, so a test, a handler or a status read covers only
// the command it is written on. The list always holds one command. raw is
// the statement before shellCodeOnly, which keeps its length, so each command
// is cut at the same place in both, read through shellFailOpenNamed, and
// returned raw as well.
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
		case ';', '\n':
			if depth == 0 {
				commands = append(commands, shellFailOpenNamed(code[start:index], raw[start:index]))
				raws = append(raws, raw[start:index])
				start = index + 1
			}
		}
	}
	return append(commands, shellFailOpenNamed(code[start:], raw[start:])), append(raws, raw[start:])
}

// shellFailOpenNamed returns the command as bash names its words when a quoted
// or escaped fragment spells a tool, as in 'g'it, "gi""t" or g\it, which
// shellCodeOnly blanks. shellWords joins the fragments of a word as bash
// does; every other quoted word stays blank, so a message is still no command.
// It also blanks each wrapper before a command word with shellUnwrapped.
func shellFailOpenNamed(code, raw string) string {
	words, _, _, _, _, _ := shellWords(raw, nil)
	named, spelled := make([]string, len(words)), false
	for index, each := range words {
		named[index] = each.text
		if !each.quoted {
			continue
		}
		// The name may follow the $( or ( that opens it in the same word; a
		// quoted ( or ) is text and stays blank, so it cannot end a $( early.
		if cut := strings.LastIndexAny(each.syntax, "(`") + 1; shellToolName.MatchString(each.text[cut:]) {
			named[index], spelled = each.syntax[:cut]+each.text[cut:], true
		} else {
			named[index] = `""`
		}
	}
	if !spelled {
		return shellUnwrapped(code)
	}
	return shellUnwrapped(strings.Join(named, " "))
}

// shellUnwrapped blanks each wrapper in command position and the options
// wrappedCommand steps over, as the env -u NAME of env -u NAME git, so the
// command it runs stands in command position. No offset moves.
func shellUnwrapped(command string) string {
	out := []byte(command)
	for _, loc := range shellCommandStart.FindAllStringIndex(command, -1) {
		rest := command[loc[1]:]
		rest = rest[:strings.IndexAny(rest+";", ";&|)\n")]
		fields := shellField.FindAllStringIndex(rest, -1)
		words := make([]string, len(fields))
		for index, field := range fields {
			words[index] = rest[field[0]:field[1]]
		}
		if len(words) == 0 {
			continue
		}
		if run := len(words) - len(wrappedCommand(words)); run > 0 {
			copy(out[loc[1]:], strings.Repeat(" ", fields[run][0]))
		}
	}
	return string(out)
}

// shellCodeOnly blanks quoted text, so a message that names git or `|| true`
// is not a command. A $( inside double quotes opens code again, with quoting
// of its own, until the ) that closes it. The caller has already cut each
// line's comment with shellWords. The result keeps the line's length, so an
// offset in it is the same offset in the line.
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
	return string(out)
}

func isShellSource(rel string, content []byte) bool {
	if strings.HasSuffix(rel, ".sh") {
		return true
	}
	first, _, _ := strings.Cut(string(content), "\n")
	return strings.HasPrefix(first, "#!") && (strings.Contains(first, "bash") || strings.HasSuffix(strings.TrimSpace(first), "sh"))
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

// loadShellFailOpenBaseline reads PATH<TAB>RULE<TAB>COUNT<TAB>REASON rows. The
// reason is required, so a row cannot be added without saying why it stays.
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
