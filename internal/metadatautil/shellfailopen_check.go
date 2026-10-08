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
// captures the status (see shellFailOpenCapture) or when the line states its
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
// ShellInvocations itself is not usable here: it drops the body of a compound
// command and the inside of `$(` and `<(`, which are exactly the places this
// class hides in.
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
	// toolCommand matches a tool word in command position, so `git` inside a
	// path or an argument is not a call.
	// The tool word ends at a blank, an operator, a closer or a redirection,
	// since bash reads git||true as git then ||.
	shellToolCommand = regexp.MustCompile("(^|[\\s;&|(`])" + shellFailOpenTools + `[\s;&|)<>]`)
	shellToolSubst   = regexp.MustCompile(`\$\(\s*(?:[A-Za-z_][A-Za-z0-9_]*=\S+\s+|command\s+|builtin\s+)*` + shellFailOpenTools + `\b`)
	shellOrTrue      = regexp.MustCompile(`\|\|\s*true\b`)
	// shellProcessSubst is an input redirection from a process substitution,
	// with any blanks or a continued line between the < and the <(.
	shellProcessSubst = regexp.MustCompile(`(?:^|[^<>])<\s+<\(`)
	shellDevNull      = regexp.MustCompile(`2>\s*/dev/null`)
	// shellFailOpenCapture is a status capture or a completion sentinel: the
	// status is read ($?, PIPESTATUS, wait), a failure branch runs
	// (|| exit, || return, || die, || fail*, || { ... }), the call is the test
	// of an if/while, or the block records completion (walk_completed, the house form of
	// scripts/verify-release-outputs.sh) or names a sentinel that proves it finished.
	shellFailOpenCapture = regexp.MustCompile(`\$\?|PIPESTATUS|\bwait\b|\|\|\s*(?:exit|return|die\b|fail|\{|false\b|\w*(?:fail|die|error)\w*)|sentinel|\w_completed\b`)
	// shellFailOpenStatusRead reads the status the command before it left, so
	// it captures a hit only in the command right after the hit.
	shellFailOpenStatusRead = regexp.MustCompile(`\$\?|PIPESTATUS|\bwait\b`)
	// shellFailOpenTested is a command that is the test of an if/while or the
	// left side of &&. It covers a substitution, never a `done < <(` loop
	// header, where the loop's own while says nothing about the inner command.
	shellFailOpenTested = regexp.MustCompile(`^\s*(?:if|elif|while|until)\b|&&\s*\S`)
	shellFailClosedRe   = regexp.MustCompile(`#\s*` + shellFailClosedTag + `\s*\S`)
	// shellAssignment is a word that assigns a name, the only word that may
	// stand before a substitution whose status the command keeps.
	shellAssignment = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(?:\[[^]]*\])?\+?=`)
)

// ShellFailOpenFindings reports the fail-open hits in one script. It skips
// heredoc bodies, reads a continued line, an open `$(` or a quoted span that
// runs past its line as one statement, ignores text inside single quotes and
// inside double quotes that open no command substitution, and takes a marker
// only from a real shell comment. A heredoc whose end it cannot find is an
// error, since the body it skips could hold any command.
func ShellFailOpenFindings(script string) ([]ShellFailOpenFinding, error) {
	type logical struct {
		number        int
		code, comment string
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
		current.comment += comment
		if continues || operator || len(stack) > 0 || depth > 0 || openQuote != 0 {
			continue
		}
		current.code = shellCodeOnly(strings.TrimSuffix(strings.Join(lines, ""), "\n"))
		statements = append(statements, current)
		current, lines = logical{}, nil
	}
	if len(heredocs) > 0 {
		return nil, fmt.Errorf("heredoc ended by %q never closes", heredocs[0].delimiter)
	}
	if len(lines) > 0 {
		// A substitution the script never closes still holds its hits.
		current.code = shellCodeOnly(strings.TrimSuffix(strings.Join(lines, ""), "\n"))
		statements = append(statements, current)
	}
	var findings []ShellFailOpenFinding
	for index, statement := range statements {
		if strings.TrimSpace(statement.code) == "" {
			continue
		}
		if shellFailClosedRe.MatchString(statement.comment) ||
			(index > 0 && strings.TrimSpace(statements[index-1].code) == "" &&
				shellFailClosedRe.MatchString(statements[index-1].comment)) {
			continue
		}
		// The last entry is the first command of the next statement, which
		// reads the status this statement leaves.
		commands := append(shellFailOpenCommands(statement.code), "")
		for next := index + 1; next < len(statements); next++ {
			if strings.TrimSpace(statements[next].code) != "" {
				commands[len(commands)-1] = shellFailOpenCommands(statements[next].code)[0]
				break
			}
		}
		hits := map[string]bool{}
		for at, command := range commands[:len(commands)-1] {
			after := commands[at+1]
			hasTool := shellToolCommand.MatchString(" "+command+" ") || shellToolSubst.MatchString(command)
			// A loop reports its inner status only through a check after it, such
			// as the walk_completed sentinel, so the next command may capture it.
			// A status read or a handler covers only a call written before it.
			captured := shellFailOpenCapture.MatchString(command[shellFailOpenFirstCall(command):])
			looped := captured || shellFailOpenCapture.MatchString(after)
			tested := captured || shellFailOpenStatusRead.MatchString(after) || shellFailOpenTested.MatchString(command)
			hits[ruleProcessSubstitution] = hits[ruleProcessSubstitution] || shellProcessSubst.MatchString(command) && !looped
			hits[ruleCommandSubstitution] = hits[ruleCommandSubstitution] || shellToolSubst.MatchString(command) && (!tested || shellSubstMasked(command))
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

// shellFailOpenFirstCall returns where the first tool call or process
// substitution in a command starts, or the command's length when it has none.
func shellFailOpenFirstCall(command string) int {
	first := len(command)
	if loc := shellToolCommand.FindStringIndex(" " + command + " "); loc != nil {
		first = min(first, max(loc[0]-1, 0))
	}
	for _, re := range []*regexp.Regexp{shellToolSubst, shellProcessSubst} {
		if loc := re.FindStringIndex(command); loc != nil {
			first = min(first, loc[0])
		}
	}
	return first
}

// shellSubstMasked reports whether a tool substitution is an argument of a
// command, as in local x=$(git ...) or [[ -n "$(git ...)" ]]. That command's
// own status replaces the substitution's, so no test or handler can see it.
// Only a command made of assignments keeps the status of its substitution.
func shellSubstMasked(command string) bool {
	for _, loc := range shellToolSubst.FindAllStringIndex(command, -1) {
		prefix := command[:loc[0]]
		words := strings.Fields(prefix[strings.LastIndexAny(prefix, ";&|(`")+1:])
		for len(words) > 0 && slices.Contains([]string{"if", "elif", "while", "until", "then", "do", "else", "!", "{", "time"}, words[0]) {
			words = words[1:]
		}
		for _, each := range words {
			if !shellAssignment.MatchString(each) {
				return true
			}
		}
	}
	return false
}

// shellFailOpenCommands splits a statement's code at each ; and line end that
// no parenthesis encloses, so a test, a handler or a status read covers only
// the command it is written on. The list always holds one command.
func shellFailOpenCommands(code string) []string {
	var commands []string
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
				commands = append(commands, code[start:index])
				start = index + 1
			}
		}
	}
	return append(commands, code[start:])
}

// shellCodeOnly blanks single-quoted text and double-quoted text that holds no
// `$(`, so a message that names git or `|| true` is not a command. The caller
// has already cut each line's comment with shellWords.
func shellCodeOnly(line string) string {
	var out strings.Builder
	var quote byte
	start := 0
	for i := 0; i < len(line); i++ {
		c := line[i]
		if quote == 0 {
			switch {
			case c == '\\' && i+1 < len(line):
				out.WriteString(line[i : i+2])
				i++
			case c == '\'' || c == '"':
				quote, start = c, i
			default:
				out.WriteByte(c)
			}
			continue
		}
		if quote == '"' && c == '\\' {
			i++
			continue
		}
		if c == quote {
			out.WriteString(quotedSegment(line[start:i+1], quote))
			quote = 0
		}
	}
	if quote != 0 {
		out.WriteString(quotedSegment(line[start:], quote))
	}
	return out.String()
}

func quotedSegment(segment string, quote byte) string {
	if quote == '"' && strings.Contains(segment, "$(") {
		return segment
	}
	return `""`
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
