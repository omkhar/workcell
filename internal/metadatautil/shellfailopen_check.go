// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
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
// A hit is accepted when the same block captures the status (see
// shellFailOpenCapture) or when the line states its case with a marker:
//
//	# fail-closed: <reason>
//
// The check is a ratchet. policy/shell-fail-open-baseline.tsv records the hits
// each file carries today; a count above its row fails, and so does a count
// below it, so the row drops with the repair.
//
// The scan reads lines the way ShellPortabilityFindings does. ShellInvocations
// is not usable here: it drops the body of a compound command and the inside of
// `$(` and `<(`, which are exactly the places this class hides in.
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
		for _, finding := range ShellFailOpenFindings(string(content)) {
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

	// shellFailOpenWindow is how many lines after a hit still count as its
	// block, so a status test after a multi-line `$(` or a `done < <(` loop
	// header is found.
	shellFailOpenWindow = 6

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
	shellToolCommand = regexp.MustCompile("(^|[\\s;&|(`])" + shellFailOpenTools + `\s`)
	shellToolSubst   = regexp.MustCompile(`\$\(\s*(?:[A-Za-z_][A-Za-z0-9_]*=\S+\s+|command\s+|builtin\s+)*` + shellFailOpenTools + `\b`)
	shellOrTrue      = regexp.MustCompile(`\|\|\s*true\b`)
	shellDevNull     = regexp.MustCompile(`2>\s*/dev/null`)
	// shellFailOpenCapture is a status capture or a completion sentinel: the
	// status is read ($?, PIPESTATUS, wait), a failure branch runs
	// (|| exit, || return, || die, || fail*, || { ... }), the call is the test
	// of an if/while, or the block records completion (walk_completed, the house form of
	// scripts/verify-release-outputs.sh) or names a sentinel that proves it finished.
	shellFailOpenCapture = regexp.MustCompile(`\$\?|PIPESTATUS|\bwait\b|\|\|\s*(?:exit|return|die\b|fail|\{|false\b|\w*(?:fail|die|error)\w*)|sentinel|\w_completed\b`)
	// shellFailOpenTested is a call that is the test of an if/while or the left
	// side of &&. It covers a substitution, never a `done < <(` loop header,
	// where the loop's own while says nothing about the inner command.
	shellFailOpenTested = regexp.MustCompile(`^\s*(?:if|elif|while|until)\b|&&\s*\S`)
	shellFailClosedRe   = regexp.MustCompile(`#\s*` + shellFailClosedTag + `\s*\S`)
)

// ShellFailOpenFindings reports the fail-open hits in one script. It skips
// comment lines and heredoc bodies, reads each continued line as one
// statement, and ignores text inside single quotes and inside double quotes
// that open no command substitution.
func ShellFailOpenFindings(script string) []ShellFailOpenFinding {
	type logical struct {
		number int
		raw    string
		code   string
	}
	var statements []logical
	var pending, joinedRaw string
	var heredoc string
	startNumber, number := 0, 0
	for line := range strings.Lines(script) {
		number++
		text := strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if heredoc != "" {
			if strings.TrimSpace(text) == heredoc {
				heredoc = ""
			}
			continue
		}
		if delimiter := heredocDelimiter(text); delimiter != "" {
			heredoc = delimiter
		}
		if pending == "" {
			startNumber = number
		}
		joinedRaw += text + "\n"
		if strings.HasSuffix(text, "\\") && !strings.HasPrefix(strings.TrimSpace(text), "#") {
			pending += strings.TrimSuffix(text, "\\") + " "
			continue
		}
		code := pending + text
		statements = append(statements, logical{startNumber, joinedRaw, shellCodeOnly(code)})
		pending, joinedRaw = "", ""
	}
	var findings []ShellFailOpenFinding
	for index, statement := range statements {
		if strings.TrimSpace(statement.code) == "" {
			continue
		}
		previous := ""
		if index > 0 {
			previous = statements[index-1].raw
		}
		if shellFailClosedRe.MatchString(statement.raw) ||
			(strings.HasPrefix(strings.TrimSpace(previous), "#") && shellFailClosedRe.MatchString(previous)) {
			continue
		}
		window := statement.code
		for next := index + 1; next < len(statements) && next <= index+shellFailOpenWindow; next++ {
			window += "\n" + statements[next].code
		}
		hasTool := shellToolCommand.MatchString(" "+statement.code+" ") || shellToolSubst.MatchString(statement.code)
		captured := shellFailOpenCapture.MatchString(window)
		tested := captured || shellFailOpenTested.MatchString(statement.code)
		add := func(rule string) {
			findings = append(findings, ShellFailOpenFinding{Rule: rule, Line: statement.number})
		}
		if strings.Contains(statement.code, "< <(") && !captured {
			add(ruleProcessSubstitution)
		}
		if shellToolSubst.MatchString(statement.code) && !tested {
			add(ruleCommandSubstitution)
		}
		if hasTool && shellOrTrue.MatchString(statement.code) {
			add(ruleOrTrue)
		}
		if hasTool && shellDevNull.MatchString(statement.code) && !tested {
			add(ruleDevNull)
		}
	}
	return findings
}

// shellCodeOnly blanks single-quoted text, double-quoted text that holds no
// `$(`, and a trailing comment, so a message that names git or `|| true` is
// not a command.
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
			case c == '#' && (i == 0 || line[i-1] == ' ' || line[i-1] == '\t'):
				return out.String()
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
