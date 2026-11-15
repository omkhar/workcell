// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/omkhar/workcell/internal/rootio"
	"gopkg.in/yaml.v3"
)

var (
	laneScriptRoot = regexp.MustCompile(`^(\$ROOT_DIR/|\$\{ROOT_DIR\}/)?(\./)?`)
	laneScriptPath = regexp.MustCompile(`^scripts/[A-Za-z0-9_./-]+\.sh$`)
)

// LaneScripts returns each scripts/*.sh path that a lane runs as a command,
// sorted and unique. The lanes are scripts/validate-repo.sh, the
// scripts/ci/job-*.sh scripts and the run steps of the .yml and .yaml
// workflows, and a lane script counts as run itself. A leading ${ROOT_DIR}/,
// $ROOT_DIR/ or ./ is dropped, so every spelling of one script is one path. Any
// other variable names another directory, so its path is not a repository
// script, and neither does an absolute path. A workflow path resolves from the
// step's working directory, and a relative path after a cd, a pushd or a
// source in any lane, or under an expression or absolute directory, resolves
// to no repository script. A step in a shell this package does not read wires
// nothing, and a relative path in a step that may first source a BASH_ENV or
// ENV startup file, or in a later step of its job, resolves to no repository
// script. EveryShellCommand reads each body, so a comment, a heredoc body
// past its substitutions, quoted text or an uncalled function wires nothing,
// while a command behind a guard still wires its script; an exit before a
// later command makes the lane unreadable.
//
// A ${ROOT_DIR}/ path names a repository script only in a lane script whose
// root is pinned, as laneRootPinned reads it; a workflow step never sets
// ROOT_DIR. A lane that defines a function named after a shell, a wrapper, a
// path, trap or eval fails closed, since bash then calls the function instead
// of the program, and so does one that names BASH_CMDS or BASH_ALIASES, which
// rebind a name as hash -p and alias do, or that runs text this reader cannot
// spell, as eval "$x" does. A program word with no slash runs from PATH, not
// from the working directory.
func LaneScripts(rootDir string) ([]string, error) {
	parent, cleaned, err := rootio.OpenParentDirectoryNoFollow(rootDir)
	if err != nil {
		return nil, err
	}
	root, err := rootio.OpenDirAtNoFollow(parent, filepath.Base(cleaned))
	_ = parent.Close()
	if err != nil {
		return nil, err
	}
	defer root.Close()
	type laneRun struct {
		dir, body string
		moved     bool
		pinned    bool          // ${ROOT_DIR} is the repository root
		libraries []laneLibrary // the repository libraries a pinned lane sources
	}
	var runs []laneRun
	var scripts []string
	for _, lane := range []struct{ dir, pattern string }{{"scripts", "validate-repo.sh"}, {"scripts/ci", "job-*.sh"}, {".github/workflows", ""}} {
		matches := mustGlob(filepath.Join(rootDir, lane.dir, lane.pattern))
		if lane.pattern == "" {
			matches = workflowYAMLFiles(filepath.Join(rootDir, lane.dir))
		}
		files, err := readLaneFiles(root, lane.dir, matches)
		if err != nil {
			return nil, err
		}
		for rel, body := range files {
			if lane.pattern != "" {
				pinned, libraries, err := laneRootPinned(root, rel, string(body))
				if err != nil {
					return nil, err
				}
				runs, scripts = append(runs, laneRun{body: string(body), pinned: pinned, libraries: libraries}), append(scripts, rel)
				continue
			}
			var document workflowDocument
			if err := yaml.Unmarshal(body, &document); err != nil {
				return nil, fmt.Errorf("parse %s: %w", rel, err)
			}
			for name, job := range document.Jobs {
				startup := false // a startup file set in this job's GITHUB_ENV holds for its later steps
				for _, step := range job.Steps {
					if unreadShellStep(document, job, step) {
						continue
					}
					dir, err := stepWorkDir(document, job, step)
					if err != nil {
						return nil, fmt.Errorf("%s job %s: %w", rel, name, err)
					}
					startup = startup || startupFileStep(document, job, step)
					runs = append(runs, laneRun{dir: dir, body: step.Run, moved: startup})
				}
			}
		}
	}
	for _, run := range runs {
		// A lane that assigns PATH, BASH_ENV or ENV may change the program each
		// later command runs, a script's own #! interpreter included, so it
		// wires no script.
		startup, err := laneReadable(run.body, run.libraries, run.pinned)
		if err != nil {
			return nil, err
		}
		if startup {
			continue
		}
		moved := run.moved || strings.Contains(run.dir, "${{") || path.IsAbs(run.dir)
		for _, words := range laneCommands(run.body)[0] {
			name, direct := words[0], true
			if _, _, file := shellProgram(words, "$_"); file != "" {
				name, direct = file, false // bash scripts/x.sh runs x.sh
			}
			// A cd or pushd, or a sourced body this reader does not see, may move
			// the directory, so a later relative path names no known script.
			if cmd := unwrapBuiltins(words[assignmentPrefix(words):]); len(cmd) > 0 {
				moved = moved || slices.Contains([]string{"cd", "pushd", "source", "."}, cmd[0])
			}
			prefix := laneScriptRoot.FindStringSubmatch(name)
			name = name[len(prefix[0]):]
			if prefix[1] != "" && !run.pinned {
				continue // ROOT_DIR may name another directory
			}
			if prefix[1] == "" {
				if moved || path.IsAbs(name) || direct && !strings.Contains(prefix[0]+name, "/") {
					continue
				}
				name = path.Join(run.dir, name)
			}
			if laneScriptPath.MatchString(name) {
				scripts = append(scripts, name)
			}
		}
	}
	slices.Sort(scripts)
	return slices.Compact(scripts), nil
}

// laneLibraryRoot is the ${ROOT_DIR}/ or $ROOT_DIR/ in front of a library path.
var laneLibraryRoot = regexp.MustCompile(`^\$(?:ROOT_DIR|\{ROOT_DIR\})/`)

// laneRootPlain is an expansion of ROOT_DIR that cannot assign it: the plain
// $ROOT_DIR or ${ROOT_DIR}, or ${ROOT_DIR with an operator that only reads it,
// as ${ROOT_DIR:-}, ${ROOT_DIR%/} or its length ${#ROOT_DIR} do. ${ROOT_DIR:=x} and ${ROOT_DIR=x}
// assign it, so they are left for laneRootNamed to find.
var laneRootPlain = regexp.MustCompile(`\$ROOT_DIR(?:[^A-Za-z0-9_]|$)|\$\{#?ROOT_DIR(?:\}|:?[-+?]|[#%/^,@])`)

// laneRootNamed is any other mention of ROOT_DIR: an assignment, an export or
// declaration, a read, a for loop, a nameref, an assigning expansion or a
// comment. Like namesRoot in the shared reader, a comment counts.
var laneRootNamed = regexp.MustCompile(`ROOT_DIR`)

// laneHashTables names the arrays bash reads as its command hash and its
// aliases, so BASH_CMDS[bash]=/bin/true binds bash as hash -p does.
var laneHashTables = regexp.MustCompile(`BASH_(?:CMDS|ALIASES)`)

// laneCanonicalRoot is the assignment that pins a lane's root to the
// repository, as the shared reader's canonicalRootDir does for a library
// source. UPS is the climb from the lane's directory to the repository.
const laneCanonicalRoot = `^ROOT_DIR="\$\((?:cd "\$\(dirname "\$\{BASH_SOURCE\[0\]\}"\)/UPS"|CDPATH='' cd -- "\$\{BASH_SOURCE\[0\]%/\*\}/UPS") && pwd(?: -P)?\)"$`

// laneReadonlyRoot is the line after the canonical assignment that makes
// ROOT_DIR readonly. Bash then refuses every later write to it, by any name a
// command computes, as printf -v "${v}DIR", read, local or an arithmetic
// ${v}DIR=1 would make, so this reader need not resolve those names.
var laneReadonlyRoot = regexp.MustCompile(`^(?:readonly|declare -r) ROOT_DIR$`)

// lanePreludeSet is a set command of option words alone, with an optional
// comment, as set -euo pipefail is. A set line that runs another command, as
// set -e; cd /tmp does, or opens a definition or a group around the root
// assignment, is no prelude, so the root is not pinned.
var lanePreludeSet = regexp.MustCompile(`^set(?:[ \t]+(?:[-+][A-Za-z]+|[a-z]+))*[ \t]*(?:#.*)?$`)

// laneLibrary is a repository library a lane sources, by its path and text.
type laneLibrary struct{ path, text string }

// laneRootPinned reports whether ${ROOT_DIR} in the lane script rel names the
// repository root at each command after the prelude, and returns each
// repository library the lane sources. The root is pinned when the first line
// that is not a comment, a blank line or a lanePreludeSet is the canonical
// assignment for the lane's directory, the next line makes ROOT_DIR readonly,
// and no other line of the lane or of a library it sources by a ${ROOT_DIR}/
// path names ROOT_DIR except in an expansion that only reads it, since a child
// shell, as in env ROOT_DIR=/tmp bash -c, has its own. Every word is read
// without its quotes too, so "ROOT_""DIR" names it. The libraries are found
// from the words of every command of the lane and of each library, in every
// function each defines, so "source", \source, command builtin source and a
// source in a function count as source does. A library this reader cannot open
// under the root leaves the root unpinned.
func laneRootPinned(root *os.File, rel, body string) (bool, []laneLibrary, error) {
	ups := strings.TrimSuffix(strings.Repeat("../", strings.Count(rel, "/")), "/")
	canonical := regexp.MustCompile(strings.ReplaceAll(laneCanonicalRoot, "UPS", regexp.QuoteMeta(ups)))
	lines := strings.Split(body, "\n")
	at := slices.IndexFunc(lines, func(line string) bool {
		trimmed := strings.TrimSpace(line)
		return trimmed != "" && !strings.HasPrefix(trimmed, "#") && !lanePreludeSet.MatchString(trimmed)
	})
	if at < 0 || at+1 >= len(lines) || !canonical.MatchString(lines[at]) || !laneReadonlyRoot.MatchString(lines[at+1]) {
		return false, nil, nil
	}
	lines[at], lines[at+1] = "", ""
	if named, opaque := namesLaneRoot(strings.Join(lines, "\n")); opaque {
		return false, nil, errLaneBraces
	} else if named {
		return false, nil, nil
	}
	var libraries []laneLibrary
	seen := map[string]bool{}
	for pending := []string{body}; len(pending) > 0; pending = pending[1:] {
		parts, complete := libraryParts(pending[0])
		if !complete {
			return false, nil, fmt.Errorf("lane wiring is unreadable: a lane nests function definitions deeper than this reader parses")
		}
		for _, part := range parts {
			for _, words := range part.commands {
				library, fixed := laneLibraryPath(words)
				if !fixed || seen[library] {
					continue
				}
				seen[library] = true
				parent, err := rootio.OpenDirAtNoFollow(root, path.Dir(library))
				if err != nil {
					return false, nil, nil
				}
				content, err := rootio.ReadFileAtNoFollow(parent, path.Base(library), "lane library", 1<<20)
				_ = parent.Close()
				if err != nil {
					return false, nil, nil
				}
				if named, opaque := namesLaneRoot(string(content)); opaque {
					return false, nil, errLaneBraces
				} else if named {
					return false, nil, nil
				}
				libraries, pending = append(libraries, laneLibrary{library, string(content)}), append(pending, string(content))
			}
		}
	}
	return true, libraries, nil
}

// lanePart is the commands of one part of a text: outside every function,
// where function is "", or in the body of the function named function.
type lanePart struct {
	function string
	text     string // the body, past every nested definition
	commands [][]string
}

// libraryParts returns the commands of text outside every function it
// defines, then those of each function body on its own, nested ones too,
// since a library's function runs only when a caller runs it. Each nested
// body is read again on its own, so the bytes it reads are charged to a budget
// linear in the text's length; complete is false once that runs out.
func libraryParts(text string) (parts []lanePart, complete bool) {
	var functions, bodies []string
	budget := 16*len(text) + 1<<16
	var split func(function, text string) bool
	split = func(function, text string) bool {
		if budget -= len(text) + 1; budget < 0 {
			return false
		}
		for _, part := range functionBodies(flattenSubstitutions(heredocsAsHereStrings(text))) {
			if part.name == "" {
				functions, bodies = append(functions, function), append(bodies, part.text)
			} else if !split(part.name, part.text) { // a nested definition binds when its outer function runs
				return false
			}
		}
		return true
	}
	if !split("", text) {
		return nil, false
	}
	for at, commands := range laneCommands(bodies...) {
		parts = append(parts, lanePart{functions[at], bodies[at], commands})
	}
	return parts, true
}

// laneCommands returns the commands EveryShellCommand reads in each text.
func laneCommands(texts ...string) [][][]string {
	commands := make([][][]string, len(texts))
	for at, text := range texts {
		commands[at] = EveryShellCommand(text)
	}
	return commands
}

// namesLaneRoot reports whether text names ROOT_DIR other than in an
// expansion that only reads it, as namesShellVariable reads text.
func namesLaneRoot(text string) (named, opaque bool) {
	return namesShellVariable(text, func(text string) bool { return laneRootNamed.MatchString(laneRootPlain.ReplaceAllString(text, "")) })
}

// namesShellVariable reports whether names holds for text: for its own text,
// for that text with each backslash-newline removed, as bash joins
// ROOT\<newline>_DIR, or for a word read without its quotes, in each word its
// brace expansion makes, as export ROOT{_,}DIR does. A brace expansion this
// reader does not expand may spell any name, so opaque reports it.
func namesShellVariable(text string, names func(string) bool) (named, opaque bool) {
	joined := strings.ReplaceAll(text, "\\\n", "")
	if names(text) || names(joined) {
		return true, false
	}
	for line := range strings.Lines(joined) {
		words, _, _, _, _, _ := shellWords(strings.TrimSuffix(line, "\n"), nil)
		for _, each := range words {
			variants, unread := braceVariants(each.text, each.syntax)
			if unread {
				return false, true
			}
			if slices.ContainsFunc(variants, names) {
				return true, false
			}
		}
	}
	return false, false
}

// braceLimit bounds the words braceVariants makes and braceWordLimit the bytes
// of a word with a group it expands, so a long or deeply grouped word costs a
// fixed amount of work.
const braceLimit, braceWordLimit = 256, 4096

// braceSequence is the body of a sequence expression, as in {a..z}, {1..9}
// or {a..z..2}.
var braceSequence = regexp.MustCompile(`^(?:-?[0-9]+\.\.-?[0-9]+|([A-Za-z])\.\.([A-Za-z]))(?:\.\.(-?[0-9]+))?$`)

// braceVariants returns the words bash's brace expansion makes of one word,
// read from its syntax, so a quoted brace or comma is text and the { of a ${
// expansion opens none. A letter sequence such as {a..e} makes a word for each
// letter. A number sequence makes digits, which spell no name, so one 0 stands
// for it. A word with a group that is over braceWordLimit bytes, or whose
// groups may make more than braceLimit words, is opaque: this reader does not
// expand it. A word with no group is itself at any length. One pass matches
// every brace, and the product of each group's alternatives bounds the words
// before any is made.
func braceVariants(text, syntax string) (variants []string, opaque bool) {
	type group struct {
		open, end    int
		commas       []int
		alternatives []string // the words of a sequence
	}
	var groups, stack []group
	expansion := map[int]bool{} // the { of a ${ expansion
	product := 1
	for at := 0; at < len(syntax); at++ {
		switch syntax[at] {
		case '{':
			expansion[at] = at > 0 && syntax[at-1] == '$'
			stack = append(stack, group{open: at})
		case ',':
			if len(stack) > 0 && !expansion[stack[len(stack)-1].open] {
				stack[len(stack)-1].commas = append(stack[len(stack)-1].commas, at)
			}
		case '}':
			if len(stack) == 0 {
				continue
			}
			closed := stack[len(stack)-1]
			stack, closed.end = stack[:len(stack)-1], at
			if expansion[closed.open] {
				continue
			}
			count := len(closed.commas) + 1
			if match := braceSequence.FindStringSubmatch(syntax[closed.open+1 : at]); count == 1 && match != nil {
				closed.alternatives = []string{"0"}
				if match[1] != "" {
					first, last, step := int(match[1][0]), int(match[2][0]), 1
					if increment, err := strconv.Atoi(match[3]); err == nil && increment != 0 {
						step = max(increment, -increment)
					}
					if last < first {
						step = -step
					}
					closed.alternatives = nil
					for letter := first; (last-letter)*step >= 0; letter += step {
						closed.alternatives = append(closed.alternatives, string(rune(letter)))
					}
				}
				count = len(closed.alternatives)
			} else if count == 1 {
				continue
			}
			if product *= count; product > braceLimit {
				return nil, true
			}
			groups = append(groups, closed)
		}
	}
	if len(groups) == 0 {
		return []string{text}, false
	}
	if len(syntax) > braceWordLimit {
		return nil, true
	}
	first := slices.MinFunc(groups, func(a, b group) int { return a.open - b.open })
	var texts, syntaxes []string
	for index, start := range append([]int{first.open}, first.commas...) {
		stop := first.end
		if index < len(first.commas) {
			stop = first.commas[index]
		}
		texts, syntaxes = append(texts, text[start+1:stop]), append(syntaxes, syntax[start+1:stop])
	}
	if first.alternatives != nil {
		texts, syntaxes = first.alternatives, first.alternatives
	}
	for index := range texts {
		more, unread := braceVariants(text[:first.open]+texts[index]+text[first.end+1:], syntax[:first.open]+syntaxes[index]+syntax[first.end+1:])
		if variants = append(variants, more...); unread || len(variants) > braceLimit {
			return nil, true
		}
	}
	return variants, false
}

// laneSource is a source of a fixed repository path, whose text this check reads as a lane of its own when it is one.
var laneSource = regexp.MustCompile(`^\$(?:ROOT_DIR|\{ROOT_DIR\})/[A-Za-z0-9_./-]+$`)

// laneCommand returns the words from the command word a command runs, past
// its assignments and any command, builtin or time in front of it, so builtin
// hash -p and command builtin source are read as hash -p and source.
func laneCommand(words []string) []string {
	return unwrapBuiltins(words[assignmentPrefix(words):])
}

// laneLibraryPath returns the repository path that a source of a fixed
// ${ROOT_DIR}/ path reads, cleaned, as source "${ROOT_DIR}"/scripts/lib/x.sh
// reads scripts/lib/x.sh, and whether the command is such a source.
func laneLibraryPath(words []string) (string, bool) {
	command := laneCommand(words)
	if len(command) < 2 || command[0] != "source" && command[0] != "." || !laneSource.MatchString(command[1]) || strings.Contains(command[1], "..") {
		return "", false
	}
	return path.Clean(laneLibraryRoot.ReplaceAllString(command[1], "")), true
}

// stepWorkDir returns the directory a step's run: body starts in, relative to
// the checkout: the step's working-directory, else the job default, else the
// workflow default.
func stepWorkDir(document workflowDocument, job workflowJob, step workflowStep) (string, error) {
	if step.WorkDir != "" {
		return step.WorkDir, nil
	}
	var defaults workflowDefaults
	if job.Defaults.Kind != 0 {
		if err := job.Defaults.Decode(&defaults); err != nil {
			return "", err
		}
	}
	if dir := defaults.Run["working-directory"]; dir != "" {
		return dir, nil
	}
	return document.Def.Run["working-directory"], nil
}

// readLaneFiles reads each matched file of dir, keyed by its repository path.
// It follows no symlink, so a lane cannot borrow text from outside the
// repository, and a dir with no match fails.
func readLaneFiles(root *os.File, dir string, matches []string) (map[string][]byte, error) {
	if len(matches) == 0 {
		return nil, fmt.Errorf("no lane file matches in %s", dir)
	}
	parent, err := rootio.OpenDirAtNoFollow(root, dir)
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	files := map[string][]byte{}
	for _, match := range matches {
		name := filepath.Base(match)
		if files[dir+"/"+name], err = rootio.ReadFileAtNoFollow(parent, name, "lane file", 1<<20); err != nil {
			return nil, err
		}
	}
	return files, nil
}
