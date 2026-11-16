// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"path"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// laneStepContext is what a step's job knows when the step starts: the
// workflow's and the job's env, whether an earlier step names GITHUB_ENV, and
// each name an earlier step writes to it as githubEnvWrite reads it.
type laneStepContext struct {
	document  workflowDocument
	job       workflowJob
	envWrites bool
	written   []string
}

// next returns the context of the step after step.
func (context laneStepContext) next(step workflowStep) laneStepContext {
	context.envWrites = context.envWrites || strings.Contains(step.Run, "GITHUB_ENV")
	for _, line := range strings.Split(step.Run, "\n") {
		if write := githubEnvWrite.FindStringSubmatch(line); write != nil {
			context.written = append(slices.Clip(context.written), write[1])
		}
	}
	return context
}

// laneUnwires reports a step from which on a later step's bash may not be the
// bash this reader reads, or a later step may not run the scripts this reader
// reads, beyond what laneStartupStep reads, as later: a body that runs a
// runner command that sets PATH or the env, as ::add-path:: and ::set-env do,
// or names the runner's file-command directory; a write into
// ${RUNNER_TEMP}/bin other than in laneToolLines; and a uses step that checks
// out another repository or writes an artifact or a cache outside the
// runner's temporary directory and dist/. It reports a write to a repository
// script, or to a target this reader cannot spell, which may be one, as
// "$GITHUB_WORKSPACE/scripts/x.sh" is, or may be the file GITHUB_PATH names,
// as now, since it may change what the step itself runs; the caller reads now
// as later too.
func laneUnwires(context laneStepContext, step workflowStep) (now, later bool) {
	if step.Uses != "" {
		action, _, _ := strings.Cut(step.Uses, "@")
		switch action {
		case "actions/checkout":
			return false, step.With["repository"] != ""
		case "actions/download-artifact", "actions/cache/restore":
			return false, slices.ContainsFunc(strings.Split(strings.TrimSpace(step.With["path"]), "\n"), func(line string) bool {
				line = strings.TrimSpace(line)
				return !laneArtifactPath.MatchString(line) || laneArtifactBin.MatchString(line)
			})
		}
		return false, false
	}
	body := laneToolLines(context, step)
	scanned, _, complete := laneScan(body)
	later = !complete || laneRunnerCommand.MatchString(step.Run) || laneToolBin.MatchString(laneUnquoted(body))
	moved := false // a relative path after a cd may name any directory
	for _, each := range scanned {
		words := laneUnredirectedWords(each.args)
		command := laneCommand(laneLeading(texts(words)))
		moved = moved || slices.Contains([]string{"cd", "pushd", "popd"}, firstOf(command))
		for _, target := range laneWriteTargets(each.args, words[len(words)-len(command):]) {
			spelled, script := laneSpelledTarget(context, step, body, target, moved)
			now = now || script || !spelled
		}
	}
	return now, later
}

// laneRunnerCommand is a workflow command the runner acts on when
// ACTIONS_ALLOW_UNSECURE_COMMANDS is set, which a step may print without
// naming that variable, or the runner's file-command directory, where the file
// GITHUB_PATH names lies.
var laneRunnerCommand = regexp.MustCompile(`::add-path::|::set-env|_runner_file_commands`)

// laneToolBin is any mention of the directory laneToolPath adds, in the text
// laneUnquoted makes.
var laneToolBin = regexp.MustCompile(`RUNNER_TEMP\}?/+(?:\./+)*bin\b`)

// laneUnquoted drops each quote and backslash, so "${RUNNER_TEMP}"/b'i'n reads
// as ${RUNNER_TEMP}/bin.
func laneUnquoted(text string) string {
	return strings.NewReplacer(`"`, "", `'`, "", `\`, "").Replace(text)
}

// laneToolForms are the lines that may write a tool into ${RUNNER_TEMP}/bin
// after a sha256sum -c, in the forms the release and security workflows use:
// extract one named member into ${RUNNER_TEMP} or into the directory, install
// one named tool into it, make that tool executable, and add the directory to
// GITHUB_PATH. None of the tools has the name of a program a lane runs.
var laneToolForms = regexp.MustCompile(`(?m)^[ \t]*(?:` +
	`tar -xzf "\$\{[a-z_]+\}" -C "\$\{RUNNER_TEMP\}" (?:actionlint|oras)|` +
	`install -m 0755 (?:[a-z]+|"\$\{[a-z_]+\}"|"\$\{RUNNER_TEMP\}/[a-z]+") "\$\{RUNNER_TEMP\}/bin/(?:actionlint|oras|zizmor)"|` +
	`tar -xzf [a-z.]+ -C "\$\{RUNNER_TEMP\}/bin" (?:actionlint|zizmor)|` +
	`chmod 0755 "\$\{RUNNER_TEMP\}/bin/(?:actionlint|oras|zizmor)"|` +
	`echo "\$\{RUNNER_TEMP\}/bin" >> "\$\{GITHUB_PATH\}")[ \t]*$`)

// laneToolDirectory is the line that makes the directory laneToolPath adds,
// which writes no file into it.
var laneToolDirectory = regexp.MustCompile(`^[ \t]*mkdir -p "\$\{RUNNER_TEMP\}/bin"[ \t]*$`)

// laneToolLines returns a step's body with each laneToolDirectory line and
// each laneToolForms line after a sha256sum -c blanked, when laneEnvPlain
// reads RUNNER_TEMP plain and the body has no cd, pushd or popd before the
// last of them. Otherwise the body is returned as it is.
func laneToolLines(context laneStepContext, step workflowStep) string {
	if !laneEnvPlain(context, step, "RUNNER_TEMP") {
		return step.Run
	}
	lines := strings.Split(step.Run, "\n")
	checked := false
	for at, line := range lines {
		for _, command := range laneCommands(line)[0] {
			checked = checked || slices.Equal(command, []string{"sha256sum", "-c", "-"})
			if slices.Contains([]string{"cd", "pushd", "popd"}, firstOf(command)) {
				return step.Run
			}
		}
		if checked && laneToolForms.MatchString(line) || laneToolDirectory.MatchString(line) {
			lines[at] = ""
		}
	}
	return strings.Join(lines, "\n")
}

// laneEnvPlain reports whether no env of the workflow, the job or the step,
// and no earlier step's write to GITHUB_ENV, sets name, in any case, and the
// step's body names it only to expand it, as $NAME and ${NAME} do, so it
// holds the value the runner gave it.
func laneEnvPlain(context laneStepContext, step workflowStep, name string) bool {
	keys := slices.Clone(context.written)
	for _, env := range []map[string]string{context.document.Env, context.job.Env, step.Env} {
		for key := range env {
			keys = append(keys, key)
		}
	}
	plain := regexp.MustCompile(`\$(?:` + name + `\b|\{` + name + `\})`)
	return !slices.ContainsFunc(keys, func(key string) bool { return strings.EqualFold(key, name) }) &&
		!strings.Contains(plain.ReplaceAllString(step.Run, ""), name)
}

// laneWriteTargets returns the words a command writes to: the target of each
// redirection that writes, other than a descriptor it copies, and, past its
// wrappers, the files tee, cp, mv, install, ln, rsync, dd, curl, wget, tar,
// unzip and sed -i write. A cp, mv, install, ln or rsync writes its last
// operand, or the directory -t names. words is the command's own words, past
// its redirections, reserved words and assignments.
func laneWriteTargets(args, words []word) []word {
	var targets []word
	for at, each := range args {
		spelled := laneSpelling(each)
		operator := redirectionOperator.FindString(spelled)
		if !strings.Contains(operator, ">") {
			continue
		}
		target := each
		if operator == spelled {
			if at+1 >= len(args) {
				continue
			}
			target = args[at+1]
		} else {
			target.text, target.raw, target.syntax = target.text[len(operator):], spelled[len(operator):], target.syntax[min(len(operator), len(target.syntax)):]
		}
		if strings.HasSuffix(operator, "&") && laneDescriptor.MatchString(target.text) {
			continue // >&2 and 2>&1 copy a descriptor
		}
		targets = append(targets, target)
	}
	if len(words) == 0 {
		return targets
	}
	wrapped := wrappedCommand(texts(words))
	words = words[len(words)-len(wrapped):]
	name, operands := commandName(texts(words)[0]), laneOperands(words[1:])
	switch name {
	case "tee", "sed":
		if name == "sed" && !slices.ContainsFunc(words[1:], func(each word) bool { return strings.HasPrefix(each.text, "-i") || each.text == "--in-place" }) {
			break
		}
		targets = append(targets, operands...)
	case "cp", "mv", "install", "ln", "rsync":
		if directory := laneOptionValue(words[1:], "-t", "--target-directory"); directory != nil {
			targets = append(targets, *directory)
		} else if len(operands) > 0 {
			targets = append(targets, operands[len(operands)-1])
		}
	case "dd":
		for _, each := range words[1:] {
			if strings.HasPrefix(each.text, "of=") {
				each.text, each.raw = each.text[3:], strings.TrimPrefix(each.raw, "of=")
				targets = append(targets, each)
			}
		}
	case "curl", "wget", "tar", "unzip":
		short := map[string]string{"curl": "-o", "wget": "-O", "tar": "-C", "unzip": "-d"}[name]
		long := map[string]string{"curl": "--output", "wget": "--output-document", "tar": "--directory", "unzip": "-d"}[name]
		if value := laneOptionValue(words[1:], short, long); value != nil {
			targets = append(targets, *value)
		}
	}
	return targets
}

// laneSpelling returns a word as the line spells it.
func laneSpelling(each word) string {
	if each.raw != "" {
		return each.raw
	}
	return each.text
}

// laneDescriptor is the operand of a >& that copies or closes a descriptor.
var laneDescriptor = regexp.MustCompile(`^(?:[0-9]+|-)$`)

// laneOperands returns the words of a command that are no option, after a
// -- too, and past the value of each option that takes one.
func laneOperands(words []word) []word {
	var operands []word
	for at := 0; at < len(words); at++ {
		switch text := words[at].text; {
		case text == "--":
			return append(operands, words[at+1:]...)
		case slices.Contains([]string{"-m", "-o", "-g", "-S", "-t", "-e", "-f", "--suffix", "--mode", "--owner", "--group", "--target-directory"}, text):
			at++ // the option's value
		case strings.HasPrefix(text, "-") && text != "-":
		default:
			operands = append(operands, words[at])
		}
	}
	return operands
}

// laneOptionValue returns the value of the option short, a dash and one
// letter, or long: the word after it, the rest of its word after the letter
// in a cluster of short options, or the text after long=.
func laneOptionValue(words []word, short, long string) *word {
	for at, each := range words {
		value := each
		switch letter := strings.Index(each.text, short[1:]); {
		case each.text == long || strings.HasPrefix(each.text, "-") && !strings.HasPrefix(each.text, "--") && letter == len(each.text)-1:
			if at+1 < len(words) {
				return &words[at+1]
			}
		case strings.HasPrefix(each.text, long+"="):
			value.text, value.raw = each.text[len(long)+1:], strings.TrimPrefix(each.raw, long+"=")
			return &value
		case strings.HasPrefix(each.text, "-") && !strings.HasPrefix(each.text, "--") && letter > 0:
			value.text, value.raw = each.text[letter+1:], each.raw[strings.Index(each.raw, short[1:])+1:]
			return &value
		}
	}
	return nil
}

// laneSpelledTarget reports whether a write's target is a path this reader
// spells, and whether that path is in the repository's scripts directory: a
// literal path, relative only where the body has not changed directory; a
// file the runner names, as GITHUB_OUTPUT does, that laneEnvPlain reads
// plain; ${RUNNER_TEMP} and a laneTempPath other than bin; or a variable that
// laneSetOnce reads, with an optional laneTempPath after it.
func laneSpelledTarget(context laneStepContext, step workflowStep, body string, target word, moved bool) (spelled, script bool) {
	text := strings.TrimSuffix(target.text, ")") // the ) that closes a subshell, as in ( printf x >"${f}" )
	if !strings.ContainsAny(target.raw, "$`") && !strings.ContainsAny(target.syntax, "*?[]{}~") {
		return laneLiteralTarget(text, moved)
	}
	match := laneTargetVariable.FindStringSubmatch(text)
	if match == nil {
		return false, false
	}
	name, rest := match[1]+match[2], match[3]
	switch {
	case slices.Contains([]string{"GITHUB_OUTPUT", "GITHUB_STEP_SUMMARY", "GITHUB_ENV", "GITHUB_PATH", "GITHUB_STATE"}, name):
		return rest == "" && laneEnvPlain(context, step, name), false
	case name == "RUNNER_TEMP":
		return rest != "" && !strings.HasPrefix(rest+"/", "/bin/") && laneEnvPlain(context, step, name), false
	}
	unset, value := laneSetOnce(body, name)
	if value == "" || !laneEnvPlain(context, workflowStep{Run: unset, Env: step.Env}, name) {
		return false, false
	}
	if strings.HasPrefix(value, "$") {
		return true, false
	}
	return laneLiteralTarget(value+rest, moved)
}

// laneLiteralTarget reports whether a literal path is one a write may name,
// not empty and absolute or in a body that has not changed directory, and
// whether it is in the repository's scripts directory.
func laneLiteralTarget(text string, moved bool) (spelled, script bool) {
	clean := path.Clean(text)
	return text != "" && (path.IsAbs(text) || !moved), !path.IsAbs(text) && (clean == "scripts" || strings.HasPrefix(clean, "scripts/"))
}

// laneTargetVariable is a write target that is one expansion of a variable,
// with an optional laneTempPath after it.
var laneTargetVariable = regexp.MustCompile(`^\$(?:([A-Za-z_][A-Za-z0-9_]*)|\{([A-Za-z_][A-Za-z0-9_]*)\})(` + laneTempPath + `)?$`)

// laneTempPath is a path under a directory: each part starts with a letter
// or a digit, so none is . or .. and none climbs out of it.
const laneTempPath = `(?:/[A-Za-z0-9][A-Za-z0-9._-]*)+`

// laneSetOnce returns body without the one line that sets name, and the
// value it sets, when one line does, before any other mention of it, to a
// literal path, to ${RUNNER_TEMP} and a laneTempPath other than bin, or to a
// mktemp of such a path. Otherwise it returns body and no value.
func laneSetOnce(body, name string) (string, string) {
	assign := regexp.MustCompile(`(?m)^[ \t]*` + regexp.QuoteMeta(name) + `="([A-Za-z0-9._/-]+|\$\{RUNNER_TEMP\}` + laneTempPath +
		`|\$\((?:umask [0-7]+ && )?mktemp "\$\{RUNNER_TEMP\}/[A-Za-z0-9][A-Za-z0-9._-]*"\))"`)
	found := assign.FindAllStringSubmatchIndex(body, -1)
	if len(found) != 1 || strings.Contains(body[:found[0][0]], name) {
		return body, ""
	}
	value := body[found[0][2]:found[0][3]]
	if strings.HasPrefix(value, "${RUNNER_TEMP}/bin/") || value == "${RUNNER_TEMP}/bin" || strings.Contains(value, "..") {
		return body, ""
	}
	return body[:found[0][0]] + body[found[0][1]:], value
}

// laneArtifactPath is a download or cache path a lane may follow: one in the
// runner's temporary directory, other than its bin, where a later step may add
// a tool directory to PATH, or in dist/, by a literal path whose every part
// starts with a letter or a digit, so none is . or ..
var laneArtifactPath = regexp.MustCompile(`^(?:\$\{\{ *runner\.temp *\}\}|dist)(?:/[A-Za-z0-9][A-Za-z0-9._-]*)*$`)

// laneArtifactBin is a laneArtifactPath in the runner's bin directory.
var laneArtifactBin = regexp.MustCompile(`^\$\{\{ *runner\.temp *\}\}/bin(?:/|$)`)

// laneNeverIf reports an if under which the job or step never runs on a run
// whose steps all pass, or whose value no run changes: with its quoted strings
// dropped, it names no context and calls no status function, as false or
// ${{ 'github.x' == 'y' }} do; it calls failure() or cancelled() without a !
// in front; or an operand of && is a bare false or 0.
func laneNeverIf(condition yaml.Node) bool {
	if condition.Kind == 0 {
		return false
	}
	text := laneIfString.ReplaceAllString(condition.Value, "''")
	text = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(text), "${{"), "}}"))
	for _, call := range laneIfStatus.FindAllStringSubmatch(text, -1) {
		if call[1] == "" {
			return true
		}
	}
	return !laneIfContext.MatchString(text) || laneIfFalse.MatchString(text)
}

var (
	laneIfString = regexp.MustCompile(`'(?:[^']|'')*'`)
	laneIfStatus = regexp.MustCompile(`(!\s*)?\b(?:failure|cancelled)\s*\(`)
	laneIfFalse  = regexp.MustCompile(`(?:^|&&)\s*\(*\s*(?:false|0)\s*\)*\s*(?:&&|$)`)
)
