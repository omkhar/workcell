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
// A `<(` or `>(`, a `$(...)` under `local` or in a list, `|| true` and `2>/dev/null` each turn "the tool failed" into "there was nothing to find", so a gate passes on an empty answer. shellcheck does not flag them.
//
// A hit is accepted when its own command or the command right after it
// captures the status (see shellFailOpenHandled) or when the command states its
// case with a marker:
//
//	# fail-closed: <reason>
//
// The marker does not cover a process substitution: bash never propagates
// its status, so every one is a hit until the script repairs it.
//
// The check is a ratchet. policy/shell-fail-open-baseline.tsv records the hits each file carries today; a count above its row fails, and so does a count below it, so the row drops with the repair.
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
	ruleQuietGrepPipe       = "quiet-grep-pipe"
)

type shellFailOpenKey struct{ path, rule string }

// ShellFailOpenFinding is one hit and the line it starts on.
type ShellFailOpenFinding struct {
	Rule string
	Line int
}

var (
	shellOtherFD               = `(?:0*(?:[3-9]|[1-9][0-9]+)|0+(?:[^0-9]|$)|-|"|\$)`                                                                                                                                                                                                                                                                                                                          // a descriptor other than 1 or 2, in any zero-padded spelling, or a close, quoted or variable target
	shellBuiltinCall           = `(?:(?:command|builtin)(?:\s+(?:-p|--))*\s+)?`                                                                                                                                                                                                                                                                                                                               // command or builtin runs the builtin after it
	shellDupStdout             = regexp.MustCompile(`0*2>&\s*0*1(?:[^0-9]|$)`)                                                                                                                                                                                                                                                                                                                                // stderr onto stdout
	shellQuotedStatus          = regexp.MustCompile(`^(?:\w+=)?\$(?:\?|\{\?\})$`)                                                                                                                                                                                                                                                                                                                             // a quoted "$?" or rc="$?", restored so the status read is seen
	shellExecNull              = regexp.MustCompile(shellDevNull.String() + `|(?:^|\s)(?:0*1)?>[>|]?\s*"?` + strings.Trim(shellNullDevice.String(), "^$") + `"?(?:[\s;&|)<>]|$)|(?:^|\s)(?:0*1)?>&\s*` + shellOtherFD)                                                                                                                                                                                        // an exec null redirect of stderr or of stdout, which makes a later 2>&1 null too
	shellFailOpenPassesOn      = regexp.MustCompile(`\|\|\s*(?:false\b|\(|\{\s*false\s*;\s*\})`)                                                                                                                                                                                                                                                                                                              // a false or ( ) handler keeps the failure but does not leave the list
	shellFailOpenThenDo        = regexp.MustCompile(`^\s*(?:then|do)\b`)                                                                                                                                                                                                                                                                                                                                      // the tool is the last command of an if or while test
	shellArithmetic            = regexp.MustCompile(`\$?\(\((?:[^()]|\([^()]*\))*\)\)`)                                                                                                                                                                                                                                                                                                                       // an arithmetic (( )), whose <( is a comparison
	shellNullDevice            = regexp.MustCompile(`^(?:/+\.{1,2})*/+dev(?:/+\.|/+\.\./+dev)*/+null$`)                                                                                                                                                                                                                                                                                                       // the null device in any spelling of its path, such as /dev//null
	shellExecDevNull           = regexp.MustCompile(`(?:^\s*|[;&|({]\s*)(?:(?:!|if|then|do|else|elif|while|until|time(?:\s+-p)?(?:\s+--)?)\s+)*(?:[A-Za-z_][A-Za-z0-9_]*=[^\s;&|()]*\s+)*((?:[0-9]*(?:[<>]|&>)[>&|]?\s*"?[^\s;&|()]+\s+)*(?:command(?:\s+(?:-p|--))*\s+)?\\?["']?exec["']?(?:\s+(?:-[cl]*a(?:\s+)?[^\s;&|()<>]+|-[cl]+|--))*(?:\s+[0-9]*(?:[<>]|&>)[>&|]?\s*"?[^\s;&|()]+)*)\s*(?:[;&|)]|$)`) // exec with no command sends stderr to /dev/null for every later command
	shellFailOpenOtherLanguage = regexp.MustCompile(`^(?:python[0-9.]*|perl[0-9.]*|ruby|node|deno|bun|php|lua|tclsh|expect|g?awk|mawk|sed|osascript|Rscript|swift|pwsh|fish|t?csh)$`)                                                                                                                                                                                                                         // other languages; a #![ line is a Rust attribute
	shellFailOpenTools         = `(?:find|git|gh|docker|getent)`
	shellToolName              = regexp.MustCompile(`^(?:[^\s;&|()<>]*/)?` + shellFailOpenTools + `$`)
	// shellCommandPosition ends where a command word starts: after the start,
	// an operator or an opener, then any reserved word, assignment, or xargs or
	// sudo with its options and their values; shellUnwrapped blanks command,
	// builtin and the other wrappers that commandWrappers names. shellUnwrapped blanks the
	// commandWrappers. `git` in a path or an argument is not a call, and nor
	// is the target of a >| or >& redirection.
	shellCommandPosition = "(?:^|[;(`\n]|(?:^|[^<>])[&|]|(?:^|[;\n]|\\bin)\\s*\\(?[^\\s;&|()]+(?:\\s*\\|\\s*[^\\s;&|()]+)*\\))\\s*(?:(?:[!{]|[0-9]*(?:[<>]|&>)[>&|]?\\s*[^\\s;&|()<>]+|if|then|do|else|elif|while|until|coproc(?:\\s+[A-Za-z_][A-Za-z0-9_]*\\s+(?:\\{|if|while|until))?|time(?:\\s+-p)?(?:\\s+--)?|builtin|xargs(?:\\s+(?:-[adEILnPs]\\s+\\S+|-\\S+))*|sudo(?:\\s+(?:-[CDghprTtUu]\\s+\\S+|-\\S+))*|[A-Za-z_][A-Za-z0-9_]*=[^\\s(]*)\\s+)*"
	shellCommandStart    = regexp.MustCompile(shellCommandPosition)
	shellField           = regexp.MustCompile(`\S+`)
	// The tool word ends at a blank, an operator, a closer or a redirection,
	// since bash reads git||true as git then ||.
	// After env, sudo or xargs any later word may be the tool, so their options need no table.
	shellToolCommand = regexp.MustCompile(shellCommandPosition + `(?:(?:\S*/)?(?:env|sudo|xargs|nice|nohup|stdbuf|setsid|ionice|timeout|chrt|taskset|time|flock|doas|chroot|unshare|nsenter|runuser|setpriv|caffeinate|noglob|nocorrect)(?:\s+[^\s;&|()<>]+)*?\s+)?(?:[^\s;&|()<>]*/)?` + shellFailOpenTools + `(?:[\s;&|)<>]|$)`)
	// shellUnnamedCommand is a command word this reader cannot name: an expansion, a quoted word or a command that runs text. It may run a tool, so a substitution that holds one counts as a tool substitution.
	shellUnnamedCommand = regexp.MustCompile(shellCommandPosition + `(?:\$[^(]|"|(?:eval|source|\.)\s)`)
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
	// shellQuietGrepPipe is a grep -q as a later pipeline stage. It exits at the
	// first match, so under pipefail the stage before it can die of SIGPIPE and
	// fail the pipeline although the text matched.
	shellQuietGrepPipe = regexp.MustCompile(`(?:^|[^|])\|\s*grep\s+(?:-[A-Za-z]+\s+)*-[A-Za-z]*q`)
	// shellProcessSubst is a process substitution, <( or >(, as a redirection or an argument.
	shellProcessSubst = regexp.MustCompile(`(?:^|[^$])[<>]\(`)
	// shellLoopSubst is the done < <( loop form, the only one a wait "$!" or a completion sentinel covers.
	shellLoopSubst = regexp.MustCompile(`\bdone\s*<\s+<\(`)
	// shellDevNull sends stderr to /dev/null by 2>, 2>>, 2>|, &> or >/dev/null 2>&1.
	shellDevNull = regexp.MustCompile(`(?:(?:^|[\s;&|()])0*2>[>|]?|&>[>|]?|>&)\s*"?(?:/+\.{1,2})*/+dev(?:/+\.|/+\.\./+dev)*/+null"?(?:[\s;&|)<>]|$)|>[>|]?\s*"?(?:/+\.{1,2})*/+dev(?:/+\.|/+\.\./+dev)*/+null"?\s+0*2>&\s*0*1|(?:^|[\s;&|()])0*2>&\s*` + shellOtherFD) // shellDevNull sends stderr to /dev/null by 2>, 2>>, 2>|, &> or >/dev/null 2>&1.
	shellEnvWord = `[^\s;&|()<>]+`
	// shellEnvSplit is env, its options with any operand, which the shared
	// commandWrappers names, and its -S or --split-string option with the one
	// word that holds the string env splits.
	envShort, envLong = envOperands()
	shellEnvSplit     = regexp.MustCompile(`(\benv(?:\s+(?:-[^\sS-]*[` + envShort + `]\s+` + shellEnvWord +
		`|(?:` + strings.Join(envLong, "|") + `)\s+` + shellEnvWord + `|-[^\sS-]+|--[a-z-]+(?:=` + shellEnvWord + `)?))*\s+)` +
		`(?:-S\s*|--split-string(?:=|\s+))(` + shellEnvWord + `)`)
	// shellFailOpenOr is the || that runs a handler.
	shellFailOpenOr = regexp.MustCompile(`\|\|\s*`)
	// shellFailOpenExits is a command that ends the script or the function with a failure: exit or return with no operand or a literal bash reads modulo 256 as nonzero (see shellFailOpenExit), die, fail*, error* (with any NAME_ prefix) or false. An exit 0, an assignment such as failed=1 or an echo fail reports nothing.
	shellFailOpenExits = regexp.MustCompile(`^\s*(?:(?:exit|return)(?:\s+--)?(?:\s+([-+]?[0-9]+))?\s*$|(?:\w+_)?(?:die|fail\w*|error\w*)(?:\s|$)|false\s*$)`)
	// shellFailOpenSentinelOr and shellFailOpenSentinelIf are the two tests
	// of a completion sentinel that the house loops use, as in
	// scripts/verify-release-outputs.sh and scripts/validate-repo.sh: the
	// loop sets NAME_completed=1 when the producer's last record arrives, and
	// the next command fails unless it is 1. shellFailOpenRecordRead is the
	// loop's read of NUL-ended records, and shellFailOpenTerminalRecord the
	// empty record the producer prints last, only after it succeeds.
	shellFailOpenRecordRead     = regexp.MustCompile(`^while\s.*\bread\s.*-d\s*(?:''|"")\s+([A-Za-z_]\w*)$`)
	shellFailOpenTerminalRecord = regexp.MustCompile(`&&\s*printf\s+(?:'\\0'|"\\0")\s*\)\s*$`)
	shellFailOpenSentinelOr     = regexp.MustCompile(`^\s*\[\[\s*"?\$\{?(\w+_completed)\}?"?\s+(?:-eq|==)\s+"?1"?\s*\]\]\s*(\|\|.*)$`)
	shellFailOpenSentinelIf     = regexp.MustCompile(`^\s*if\s+\[\[\s*"?\$\{?(\w+_completed)\}?"?\s+(?:-ne|!=)\s+"?1"?\s*\]\]\s*$`)
	// shellFailOpenStatusRead reads the status the command before it left, so
	// it captures a hit only in the command right after the hit. A wait
	// returns the status of the job it names, never of a substitution.
	// shellFailOpenTerminal is an exit, return or die-style command, which
	// ends the script or function, so the list after it never runs.
	shellFailOpenTerminal   = regexp.MustCompile(`^\s*(?:(?:command|builtin)(?:\s+(?:-p|--))*\s+)?(?:exit|(?:\w+_)?(?:die|fail\w*|error\w*))(?:\s|$)`) // shellFailOpenStatusRead reads the status the command before it left, so it captures a hit only in the command right after the hit. A wait returns the status of the job it names, never of a substitution. shellFailOpenTerminal is an exit or die-style command, which ends the script, so the list after it never runs; a return fails outside a function, so it does not end the list.
	shellFailOpenStatusRead = regexp.MustCompile(`\$(?:\?|\{\?\})(?:[^\w]|$)`)
	shellFailOpenRunsFirst  = regexp.MustCompile(shellSubstOpen.String() + "|`|[<>]\\(|\\||(?:^|[^<>])&") // shellFailOpenRunsFirst is a substitution, a process substitution or an operator, which runs a command of its own before a later word. The & of a >& or <& redirection runs nothing.
	shellFailOpenTested     = regexp.MustCompile(`^\s*(?:if|elif|while|until)\b`)                         // shellFailOpenTested is a command that is the test of an if/while. It covers a substitution, never a `done < <(` loop header, where the loop's own while says nothing about the inner command.
	// shellFailOpenList is a && or || operator, which joins two commands.
	shellFailOpenList = regexp.MustCompile(`&&|\|\|`)
	shellOutWord      = regexp.MustCompile(`[^\s;&|<>()]+`)
	shellFailClosedRe = regexp.MustCompile(`#\s*` + shellFailClosedTag + `\s*\S`)
	// shellAssignment is a word that assigns a name, the only word that may
	// stand beside a substitution whose status the command keeps.
	shellAssignment   = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(?:\[[^]]*\])?\+?=`)
	shellRedirection  = regexp.MustCompile(`^[0-9]*[<>]`) // shellRedirection is a redirection word, which leaves the status alone; shellRedirectOnly is one whose target is the next word.
	shellRedirectOnly = regexp.MustCompile(`^[0-9]*[<>]+&?$`)
	shellLaterSubst   = regexp.MustCompile("\\$\\((?:[^(]|$)|`") // shellLaterSubst is a command substitution, not an arithmetic $((.
)

// ShellFailOpenFindings reports the fail-open hits in one script. It skips
// heredoc bodies, joins a statement that runs past its line, and ignores
// quoted text that opens no command substitution. A heredoc whose end it
// cannot find is an error, since the body it skips could hold any command.
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
	depth, number, backtick, stdoutNull, kept := 0, 0, false, false, []bool(nil) // stdoutNull: an exec sent stdout to the null device, so a later 2>&1 hides stderr; kept: its value outside each open ( )
	for line := range strings.Lines(script) {
		number++
		text := strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		reopen := "" // Bash still runs a $( inside a double-quoted span that runs past its line, so the line is read inside the span rather than skipped.
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
		code, open := shellBackticksAsSubsts(strings.TrimSuffix(text, comment), backtick)
		backtick = open
		if shellFailClosedRe.MatchString(comment) {
			current.marks = append(current.marks, len(strings.Join(lines, ""))+len(code))
		}
		// A line that ends on && || or | carries its command onto the next one, so a handler written there belongs to the same command.
		fields := strings.Fields(shellCodeOnly(code))
		operator := len(fields) > 0 && continuesLine(fields[len(fields)-1])
		switch {
		case continues:
			code = strings.TrimSuffix(code, "\\") // Bash joins a continued line with nothing between the halves.
		case operator:
			code += " "
		default:
			code += "\n"
		}
		lines = append(lines, code)
		if continues || operator || len(stack) > 0 || depth > 0 || openQuote != 0 || backtick {
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
		current.raw = strings.TrimSuffix(strings.Join(lines, ""), "\n") // A substitution the script never closes still holds its hits.
		current.code = shellCodeOnly(current.raw)
		statements = append(statements, current)
	}
	pipefail, nesting := false, 0
	var findings []ShellFailOpenFinding
	var seen []string // the raw text of each command read so far
	var opens []int   // where in seen each open compound command starts
	for index, statement := range statements {
		if strings.TrimSpace(statement.code) == "" {
			continue
		}
		// A marker covers the command it is written on, and a marker alone on
		// the line before covers the statement's first command.
		if index > 0 && strings.TrimSpace(statements[index-1].code) == "" && len(statements[index-1].marks) > 0 {
			statement.marks = append(statement.marks, 0)
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
		hits := map[string]int{} // Each command in the statement counts on its own, so a second hit of the same rule on one line raises the file's count.
		start := 0
		for at, command := range commands[:len(commands)-1] {
			// A done closes the loop whose command starts at loop in seen.
			loop := len(seen)
			if change := shellFailOpenNesting(command); change > 0 {
				opens = append(opens, loop)
			} else if change < 0 && len(opens) > 0 {
				loop, opens = opens[len(opens)-1], opens[:len(opens)-1]
			}
			body := slices.Clone(seen[loop:])
			seen = append(seen, raws[at])
			after, end := commands[at+1], start+len(raws[at])
			marked := slices.ContainsFunc(statement.marks, func(mark int) bool { return mark >= start && mark <= end })
			start = end + 1
			// Only a set certain to run turns pipefail on; any set +o turns it off.
			change, runsSet := shellCommandScope(raws[at])
			if set := shellPipefail.FindStringSubmatch(command); set != nil && (set[1] == "+" || nesting == 0 && runsSet) {
				pipefail = set[1] == "-"
			}
			nesting = max(nesting+change, 0)
			// A process substitution loses its producer's status wherever it
			// stands, so it needs proof: a loop reports its inner status only
			// through a completion sentinel its body sets or a wait "$!" after it.
			loopForm := shellLoopSubst.MatchString(command)
			looped := loopForm && shellFailOpenWaited(raws[at+1]) && shellFailOpenLastSubst(command) && !slices.ContainsFunc(append(body, raws[at]), shellFailOpenBackgrounds)
			if !looped && loopForm {
				codes, following := commands[at+1:len(commands)-1], raws[at+1:len(raws)-1]
				for _, later := range statements[index+1:] {
					laterCodes, laterRaws := shellFailOpenCommands(later.code, later.raw)
					codes, following = append(slices.Clip(codes), laterCodes...), append(slices.Clip(following), laterRaws...)
				}
				looped = shellFailOpenCompleted(body, raws[at], codes, following)
			}
			// Any other <( or >( stays a hit even when the loop form is covered.
			hits[ruleProcessSubstitution] += len(shellProcessSubst.FindAllStringIndex(shellArithmetic.ReplaceAllString(command, ""), -1))
			if looped {
				hits[ruleProcessSubstitution] -= len(shellLoopSubst.FindAllStringIndex(command, -1))
			} else {
				hits[ruleDevNull] += len(slices.DeleteFunc(shellExecDevNull.FindAllStringSubmatch(raws[at], -1), func(e []string) bool { return !shellExecNull.MatchString(e[1] + " ") })) // raw: this branch blanks wrapper words such as exec in command
			}
			inner := stdoutNull || slices.ContainsFunc(shellExecDevNull.FindAllStringSubmatch(raws[at], -1), func(e []string) bool { return shellExecNull.MatchString(e[1] + " ") })
			if marked {
				continue
			}
			toolSubst := len(shellToolSubsts(command)) > 0
			later := func() []string { // A status read or a handler covers only a call written before it.
				later := slices.Clone(commands[at+1 : len(commands)-1])
				for _, each := range statements[index+1:] {
					codes, _ := shellFailOpenCommands(each.code, each.raw)
					later = append(later, codes...)
				}
				return later
			}
			testedFrom := func(text string) bool { // testedFrom reports whether the status of the call that starts text, a suffix of the command, is read by anything after it.
				rest := text[shellFailOpenFirstCall(text):]
				stages := shellOutsideSubsts(rest)
				if pipefail {
					stages = strings.NewReplacer("||", "||", "|&", "", "|", "").Replace(stages) // under pipefail a failing stage sets the pipeline status, so only a background & detaches the tool
				}
				piped := shellFailOpenDetached(stages)                                                                              // without pipefail a later stage sets the status, not the tool
				launched := strings.HasPrefix(strings.TrimSpace(text), "coproc") || strings.HasPrefix(strings.TrimSpace(text), "!") // $? is the launch status, or ! inverts it
				return !piped && !launched && (shellFailOpenReadsOwnStatus(rest) || shellFailOpenHandled(rest, later) && !shellFailOpenPassesOn.MatchString(strings.Join(append([]string{rest}, later()[:min(1, len(later()))]...), ";")) || shellFailOpenReadsStatus(after) || shellFailOpenTested.MatchString(text) && shellFailOpenThenDo.MatchString(after))
			}
			tested := testedFrom(command)
			count := func(hit bool, pattern *regexp.Regexp, text string) int { // Each occurrence counts, so a || true && … || true list on one command cannot stand in for two baselined hits.
				return map[bool]int{true: len(pattern.FindAllStringIndex(text, -1))}[hit]
			}
			if toolSubst && (!tested || shellSubstMasked(command, pipefail)) {
				hits[ruleCommandSubstitution] += len(shellToolSubsts(command))
			}
			hits[ruleOrTrue] += shellSwallowingHandlers(command, later, false)
			if pipefail {
				hits[ruleQuietGrepPipe] += len(shellQuietGrepPipe.FindAllStringIndex(command, -1))
			}
			offset := 0 // A redirect belongs to the operand of the && || or | list it is written in, so git fetch || printf x 2>/dev/null hides no git status, and each operand's own status test counts, so the && after git fetch does not cover git gc 2>/dev/null. A redirect inside a substitution belongs to the command there, so $(git x 2>/dev/null) hides git's status while foo "$(git x)" 2>/dev/null hides foo's; each is read in its own text.
			for _, operand := range shellListOperands(command) {
				untested := !testedFrom(command[offset:])
				if outside := shellOutsideSubsts(operand); shellToolCommand.MatchString(outside) {
					hits[ruleDevNull] += count(untested, shellDevNull, outside) + count(untested && inner, shellDupStdout, outside)
				}
				for _, start := range shellToolSubsts(operand) {
					body, _ := shellSubstCall(operand, start)
					hits[ruleDevNull] += count(untested, shellDevNull, body)
				}
				offset += len(operand)
			}
			stdoutNull = stdoutNull || slices.ContainsFunc(shellExecDevNull.FindAllStringSubmatchIndex(raws[at], -1), func(m []int) bool { // an exec inside a ( ) or $( ) that the command opens keeps its redirect there
				return strings.Count(raws[at][:m[2]], "(") == strings.Count(raws[at][:m[2]], ")") && shellExecNull.MatchString(raws[at][m[2]:m[3]]+" ") && !(strings.ContainsAny(raws[at][m[1]-1:m[1]], "|&") && !strings.HasPrefix(raws[at][m[1]:], raws[at][m[1]-1:m[1]])) // a | or & after the exec runs it in a subshell, as a left pipeline stage or a background job
			})
			net := strings.Count(command, "(") - strings.Count(command, ")")
			kept = append(kept, slices.Repeat([]bool{stdoutNull}, max(net, 0))...) // each subshell left open saves the state its ) restores
			if closed := min(-min(net, 0), len(kept)); closed > 0 {
				stdoutNull, kept = kept[len(kept)-closed], kept[:len(kept)-closed]
			}
		}
		for _, rule := range []string{ruleProcessSubstitution, ruleCommandSubstitution, ruleOrTrue, ruleDevNull, ruleQuietGrepPipe} {
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
			_, tail, _ := strings.Cut(group, closer)
			return !shellFailOpenDetached(tail) && shellFailOpenBranchExits(append(strings.Split(strings.Replace(group, closer, "; "+closer, 1), ";"), later()...), closer)
		}
	}
	end := strings.IndexAny(handler+";", ";&|)}")
	return shellFailOpenExit(handler[:end]) && !shellFailOpenDetached(handler[end:])
}

// shellFailOpenDetached reports whether the rest of a handler's list puts it in a pipeline, whose last stage sets the status, or in the background, which returns at once; && || >& and <& do neither; &> may background in a POSIX sh, so it fails closed.
func shellFailOpenDetached(tail string) bool {
	tail = strings.NewReplacer("&&", "", "||", "", ">&", "", "<&", "", "|&", "|").Replace(tail)
	return strings.ContainsAny(tail[:strings.IndexAny(tail+";", ";\n")], "&|")
}

// shellFailOpenEnds reports whether a handler ends the script at its own level with an exit, return or die-style command; a ( ) subshell's exit ends only the subshell.
func shellFailOpenEnds(handler string) bool {
	group, grouped := strings.CutPrefix(handler, "{")
	group, _, _ = strings.Cut(map[bool]string{true: group, false: handler[:strings.IndexAny(handler+";", ";&|)}")]}[grouped], "}") // a { } group, or the bare command
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
		for len(fields) > 1 && slices.Contains([]string{"then", "command", "builtin", "-p", "--"}, fields[0]) { // command exit is still exit
			fields = fields[1:]
		}
		for at, word := range fields { // an exit 0, or an exit behind && or ||, may run and succeed
			operand := slices.DeleteFunc(slices.Clone(fields[at+1:min(at+3, len(fields))]), func(w string) bool { return w == "--" })
			if (word == "exit" || word == "return") && (depth > 0 || at > 0) && (len(operand) == 0 || !shellFailOpenExit(word+" "+operand[0])) {
				return false
			}
		}
		if depth == 0 && len(fields) > 0 {
			if slices.Contains(closers, fields[0]) {
				return failing
			}
			if len(fields) == 1 && (fields[0] == "exit" || fields[0] == "return") {
				return !ran || failing // a bare exit keeps the last command's status
			}
			failing = shellFailOpenExit(strings.Join(fields, " "))
			if fields[0] == "exit" || fields[0] == "return" {
				return failing // the branch ends here, so a later command never runs
			}
		}
		depth += shellFailOpenNesting(each)
		ran = ran || len(fields) > 0
	}
	return false
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
	// A nested substitution owns its own tool call and is counted on its
	// own, so its inside is blanked, keeping its $( and ), before this one
	// is searched.
	masked := []byte(body)
	for _, loc := range shellSubstOpen.FindAllStringIndex(body, -1) {
		for i := loc[0] + 2; i < shellSubstEnd(body, loc[0])-1 && i < len(masked); i++ {
			masked[i] = ' '
		}
	}
	call = -1
	for _, re := range []*regexp.Regexp{shellToolCommand, shellUnnamedCommand} {
		if loc := re.FindStringIndex(string(masked)); loc != nil && (call < 0 || loc[1] < call) {
			call = loc[1]
		}
	}
	return body, call
}

// shellFailOpenCompleted reports whether the script text that follows a
// done < <( loop starts with a test of a completion sentinel that the loop
// body sets to 1, with a failure branch. The producer inside <( runs in a
// subshell, so a sentinel it sets never reaches the test. A name alone, as
// in printf '%s\n' walk_completed, proves nothing.
//
// body holds the loop's commands, from its while header, done the command
// that closes it, and codes and following the commands after it, as code and
// raw text. The producer must end with && printf '\0', the
// header must read VAR with read -d and an empty delimiter, and the body's
// one assignment of the name must be
// if [[ -z "${VAR}" ]]; then NAME=1; continue; fi, so the name is 1 only
// after the terminal record.
func shellFailOpenCompleted(body []string, done string, codes, following []string) bool {
	for len(codes) > 0 && strings.TrimSpace(codes[0]) == "" {
		codes, following = codes[1:], following[1:]
	}
	if len(codes) == 0 {
		return false
	}
	var name []string
	if test := shellFailOpenSentinelOr.FindStringSubmatch(following[0]); test != nil && shellFailOpenHandled(test[2], func() []string { return codes[1:] }) {
		name = test
	}
	if test := shellFailOpenSentinelIf.FindStringSubmatch(following[0]); test != nil && shellFailOpenBranchExits(codes[1:], "fi", "else", "elif") {
		name = test
	}
	if name == nil || len(body) == 0 || !shellFailOpenTerminalRecord.MatchString(done) {
		return false
	}
	record := shellFailOpenRecordRead.FindStringSubmatch(strings.TrimSpace(body[0]))
	if record == nil {
		return false
	}
	var trimmed []string
	for _, each := range body {
		trimmed = append(trimmed, strings.TrimSpace(each))
	}
	joined := strings.Join(trimmed, "; ")
	terminal := `(?:^|; )(?:do )?if \[\[\s+-z\s+"?\$\{?` + record[1] + `\}?"?\s+\]\]; then;? ` + name[1] + `=1; continue; fi(?:;|$)`
	return regexp.MustCompile(terminal).MatchString(joined) &&
		len(regexp.MustCompile(`(?:^|[\s;])`+name[1]+`=`).FindAllString(joined, -1)) == 1
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
	loc := append(shellFailOpenList.FindStringIndex(rest), len(rest), len(rest))                                // the end of rest when no list operator follows
	return !strings.HasPrefix(rest[loc[0]:], "&&") && shellFailOpenReadsStatus(rest[:loc[0]]+" "+rest[loc[1]:]) // after && the command runs only on success
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
		tail := command[shellSubstEnd(command, start):] // The simple command runs on past the substitution to the next operator or closer.
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

// shellFailOpenWaited reports whether a command is wait "$!", the one wait
// that returns a process substitution's status. ShellInvocations proves the
// wait runs and is the command's first.
func shellFailOpenWaited(raw string) bool {
	return slices.ContainsFunc(ShellInvocations(raw, "wait"), func(each Invocation) bool {
		return each.Position == 1 && slices.Equal(each.Args, []string{"$!"})
	})
}

// shellFailOpenLastSubst reports whether the command's last done < <( starts
// its last process substitution, since each later <( or >( sets $! too and a
// wait "$!" then waits for that one instead of the loop's producer.
func shellFailOpenLastSubst(command string) bool {
	loops := shellLoopSubst.FindAllStringIndex(command, -1)
	return !shellProcessSubst.MatchString(command[loops[len(loops)-1][1]:])
}

// shellFailOpenBackgrounds reports whether a command starts a job in the
// background with & or coproc, which sets $! to that job, so a wait "$!"
// after the loop no longer waits for the producer.
func shellFailOpenBackgrounds(raw string) bool {
	words, _, _, _, _, _ := shellWords(raw, nil)
	return slices.ContainsFunc(words, func(each word) bool {
		return !each.quoted && (each.text == "&" || each.text == "coproc")
	})
}

// shellSubstMasked reports whether a tool substitution's status is lost: the
// command hides it, as shellSubstHidden reads, or a command after the tool
// inside the substitution masks it, as in $(git ... || true), and so does a
// later pipeline stage without pipefail.
func shellSubstMasked(command string, pipefail bool) bool {
	for _, start := range shellToolSubsts(command) {
		body, call := shellSubstCall(command, start)
		inner := shellInnerHandler.ReplaceAllString(body[call-1:], " ")
		if shellInnerMask.MatchString(inner) || !pipefail && shellInnerPipe.MatchString(inner) {
			return true
		}
	}
	return shellSubstHidden(command)
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

// shellFailOpenCommands splits code, and raw at the same places, at each ; and
// line end outside a parenthesis or brace group, so each check reads one
// command, read through shellFailOpenNamed with its env -S strings split.
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
			before := index == 0 || strings.ContainsRune(" \t;\n", rune(code[index-1])) // A brace group is one command, so its redirect stays with its calls.
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
	for index := range commands {
		commands[index] = shellFailOpenNamed(shellEnvUnsplit(commands[index], raws[index]))
	}
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
		tool := failing || shellToolCommand.MatchString(shellOutsideSubsts(left)) || len(shellToolSubsts(left)) > 0 || // A tool's failure, or one a failing handler such as false passes on, reaches this ||, as in git fetch || false || true.
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

// shellEnvUnsplit replaces each env -S STRING or --split-string=STRING in a
// command's code and raw text with the words env splits STRING into, read from
// raw by the shared shellFields as wrappedCommand reads them. The program STRING
// names then stands in command position, as in env git fetch, and
// shellFailOpenNamed reads the same words from the raw text it returns.
func shellEnvUnsplit(code, raw string) (string, string) {
	for _, loc := range slices.Backward(shellEnvSplit.FindAllStringSubmatchIndex(code, -1)) {
		words := strings.Join(shellFields(strings.Join(shellFields(raw[loc[4]:loc[5]]), " ")), " ")
		code, raw = code[:loc[3]]+words+code[loc[5]:], raw[:loc[3]]+words+raw[loc[5]:]
	}
	return code, raw
}

// envOperands returns env's options that take an operand, from the shared
// commandWrappers, as short letters and long names, without -S and
// --split-string, whose value is more arguments.
func envOperands() (short string, long []string) {
	for _, option := range commandWrappers["env"] {
		switch {
		case option == "-S" || option == "--split-string":
		case strings.HasPrefix(option, "--"):
			long = append(long, option)
		default:
			short += option[1:]
		}
	}
	return short, long
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

// shellBackticksAsSubsts spells each `...` command substitution outside single quotes as $(...), so one reader covers both.
func shellBackticksAsSubsts(code string, open bool) (string, bool) { // open carries a backtick span across lines
	var out strings.Builder
	single, escaped := false, false
	for _, r := range code {
		if r == '`' && !single && !escaped {
			open = !open
			out.WriteString(map[bool]string{true: "$(", false: ")"}[open])
			continue
		}
		single, escaped = single != (r == '\'' && !escaped), r == '\\' && !escaped && !single
		out.WriteRune(r)
	}
	return out.String(), open
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
			out[i] = '"' // A backslash escapes the next byte of a $'...' span, even a '.
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
	for _, loc := range shellOutWord.FindAllStringIndex(string(out), -1) { // a word whose quoted fragments spell a tool, true, :, exec or /dev/null is that word
		raw := line[loc[0]:loc[1]]
		words, _, _, _, _, _ := shellWords(raw, nil)
		if !strings.ContainsAny(raw, `'"\`) || len(words) != 1 {
			continue
		}
		value := words[0].text
		if decoded, err := strconv.Unquote(`"` + strings.ReplaceAll(value, `"`, `\"`) + `"`); strings.Contains(raw, "$'") && err == nil {
			value = decoded
		}
		if value == "true" || value == ":" || value == "exec" || shellNullDevice.MatchString(value) || shellToolName.MatchString(value) || shellQuotedStatus.MatchString(value) {
			copy(out[loc[0]:loc[1]], value+strings.Repeat(" ", loc[1]-loc[0]))
		}
	}
	return string(out)
}

func isShellSource(rel string, content []byte) bool {
	interpreter := shebangInterpreter(content) // fail closed: any interpreter not known to be another language is a shell
	return slices.Contains([]string{".sh", ".bash", ".ksh", ".zsh"}, filepath.Ext(rel)) || interpreter != "" && !shellFailOpenOtherLanguage.MatchString(interpreter)
}

// shebangInterpreter returns the base name of the program a #! line runs, after env and its options, assignments and operands, or "" when there is none.
func shebangInterpreter(content []byte) string {
	first, _, _ := strings.Cut(string(content), "\n")
	words := shellFields(strings.TrimPrefix(first, "#!"))
	if !strings.HasPrefix(first, "#!") || strings.HasPrefix(first, "#![") || len(words) == 0 {
		return ""
	}
	// The interpreter is the first word, or the first word env and the other
	// shared commandWrappers run; the options after it name no other program.
	return filepath.Base(wrappedCommand(words)[0])
}

// shellFields returns a line's words with their quotes removed, read by the
// shared shellWords, so "bash" and 'bash' name bash.
func shellFields(line string) []string {
	words, _, _, _, _, _ := shellWords(line, nil)
	return texts(words)
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
		case ruleProcessSubstitution, ruleCommandSubstitution, ruleOrTrue, ruleDevNull, ruleQuietGrepPipe:
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
