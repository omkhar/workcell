// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"cmp"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/omkhar/workcell/internal/rootio"
	"gopkg.in/yaml.v3"
)

const (
	workflowRefsBaselinePath = "policy/workflow-refs-baseline.tsv"
	workflowRefsMaxBytes     = 4 << 20
)

// workflowScriptRef finds ./scripts/... paths in the words of a run body.
var workflowScriptRef = regexp.MustCompile(`\./scripts/[A-Za-z0-9_./-]+[$*{\[]?`)

// ghListTakesLimit holds each gh group, aliases included, whose list (or ls)
// subcommand takes --limit, per gh 2.102 help. Any other list, an extension's
// or a later gh group's included, cannot be shown bounded, so every call of it
// is a hit.
var ghListTakesLimit = map[string]bool{
	"agent-task": true, "agent-tasks": true, "agent": true, "agents": true, "cache": true, "codespace": true, "cs": true,
	"discussion": true, "gist": true, "issue": true, "label": true, "org": true, "pr": true, "project": true,
	"release": true, "repo": true, "ruleset": true, "rs": true, "run": true, "workflow": true,
}

type workflowRefHit struct{ kind, file, job, step string }

func (h workflowRefHit) key() string {
	return strings.Join([]string{h.kind, h.file, h.job, h.step}, "\t")
}

// CheckWorkflowRefs ratchets workflow run: bodies. A hit that
// is not in the baseline fails, and a baseline row with no hit fails, so the
// baseline can only shrink.
func CheckWorkflowRefs(rootDir string) error {
	hits, err := workflowRefHits(rootDir)
	if err != nil {
		return err
	}
	baseline, err := readWorkflowRefsBaseline(rootDir)
	if err != nil {
		return err
	}
	var problems []string
	seen := map[string]bool{}
	for _, hit := range hits {
		seen[hit.key()] = true
		if !baseline[hit.key()] {
			problems = append(problems, "new workflow reference violation: "+strings.ReplaceAll(hit.key(), "\t", " | "))
		}
	}
	for key := range baseline {
		if !seen[key] {
			problems = append(problems, "stale baseline row, delete it from "+workflowRefsBaselinePath+": "+strings.ReplaceAll(key, "\t", " | "))
		}
	}
	slices.Sort(problems)
	if len(problems) > 0 {
		return fmt.Errorf("workflow reference check failed:\n%s", strings.Join(problems, "\n"))
	}
	return nil
}

func readWorkflowRefsBaseline(rootDir string) (map[string]bool, error) {
	data, err := rootio.ReadFileNoFollow(filepath.Join(rootDir, workflowRefsBaselinePath), "workflow refs baseline", workflowRefsMaxBytes)
	if err != nil {
		return nil, err
	}
	rows := map[string]bool{}
	for line := range strings.Lines(string(data)) {
		line = strings.TrimSuffix(line, "\n")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 5 || fields[4] == "" {
			return nil, fmt.Errorf("%s: want kind, file, job, step, reason: %q", workflowRefsBaselinePath, line)
		}
		rows[strings.Join(fields[:4], "\t")] = true
	}
	return rows, nil
}

func loadWorkflowDocuments(rootDir string) (map[string]workflowDocument, []string, error) {
	paths := workflowYAMLFiles(filepath.Join(rootDir, ".github", "workflows"))
	slices.Sort(paths)
	documents := map[string]workflowDocument{}
	var files []string
	for _, path := range paths {
		data, err := rootio.ReadFileNoFollow(path, "workflow", workflowRefsMaxBytes)
		if err != nil {
			return nil, nil, err
		}
		var document workflowDocument
		if err := yaml.Unmarshal(data, &document); err != nil {
			return nil, nil, fmt.Errorf("%s: %w", path, err)
		}
		name := filepath.Base(path)
		documents[name] = document
		files = append(files, name)
	}
	return documents, files, nil
}

func stepLabel(index int, step workflowStep) string {
	if step.Name != "" {
		return step.Name
	}
	return fmt.Sprintf("#%d", index+1)
}

func workflowRefHits(rootDir string) ([]workflowRefHit, error) {
	documents, files, err := loadWorkflowDocuments(rootDir)
	if err != nil {
		return nil, err
	}
	var hits []workflowRefHit
	for _, file := range files {
		for job, definition := range documents[file].Jobs {
			for index, step := range definition.Steps {
				add := func(kind string) {
					hits = append(hits, workflowRefHit{kind, file, job, stepLabel(index, step)})
				}
				dir, err := stepWorkDir(documents[file], definition, step)
				if err != nil {
					return nil, fmt.Errorf("%s job %s: %w", file, job, err)
				}
				// A relative path resolves from the step's directory. An
				// expression or an absolute directory is not under rootDir,
				// and a cd moves the directory for every later command, so
				// a path after either is a hit rather than a probe.
				moved := strings.Contains(dir, "${{") || filepath.IsAbs(dir)
				for _, words := range shellCommands(step.Run) {
					moved = moved || words[0] == "cd" || words[0] == "pushd"
					for _, match := range scriptRefs(words) {
						switch {
						case strings.ContainsAny(match[len(match)-1:], "$*{["):
							// a dynamic name cannot be resolved statically
						case slices.Contains(strings.Split(dir+"/"+match, "/"), ".."):
							add("script-path-escapes " + match) // never probe outside rootDir
						case moved:
							add("script-cwd-unresolved " + match)
						default:
							if _, err := rootio.ReadFileNoFollow(filepath.Join(rootDir, dir, match[2:]), "workflow script", workflowRefsMaxBytes); err != nil {
								if !errors.Is(err, fs.ErrNotExist) {
									return nil, err
								}
								_, err := os.Lstat(filepath.Join(rootDir, dir))
								switch {
								case errors.Is(err, fs.ErrNotExist):
									add("script-cwd-unresolved " + match) // the job makes the directory at run time
								case err != nil:
									return nil, err
								default:
									add("missing-script " + match)
								}
							}
						}
					}
				}
				for _, args := range commandArgs(step.Run, "gh") {
					if help := ghFlagValues(args, "--help", "-h"); len(help) > 0 {
						if on, err := strconv.ParseBool(cmp.Or(help[len(help)-1], "true")); err != nil || on {
							continue // gh prints usage and runs nothing
						}
					}
					args = ghSubcommand(args)
					sub := ghSubcommand(args[min(1, len(args)):]) // gh pr -R o/r list runs pr list
					switch {
					case len(args) > 0 && args[0] == "api":
						if !ghPaginates(args) { // gh api has no --limit
							add("gh-api-unbounded")
						}
					case len(sub) > 0 && (sub[0] == "list" || sub[0] == "ls"):
						if limit := ghFlagValues(args, "--limit", "-L"); !ghListTakesLimit[args[0]] || len(limit) != 1 || !positiveInt(limit[0]) {
							add("gh-" + args[0] + "-list-unbounded")
						}
						if base := ghFlagValues(args, "--base", "-B"); args[0] == "pr" && (len(base) != 1 || base[0] == "") {
							add("gh-pr-list-no-base")
						}
					}
				}
			}
		}
	}
	// A second identical hit in one step gets an ordinal, so a new call in a
	// step that already has a baseline row still fails.
	counts := map[string]int{}
	for i, hit := range hits {
		counts[hit.key()]++
		if n := counts[hit.key()]; n > 1 {
			hits[i].kind = fmt.Sprintf("%s#%d", hit.kind, n)
		}
	}
	return hits, nil
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

// scriptRefs returns the ./scripts paths in one command's words that the shell
// can run. The arguments of echo, printf and :, a here-string, and an
// assignment are data, so a path written there is not a reference.
func scriptRefs(words []string) []string {
	if slices.Contains([]string{"echo", "printf", ":"}, words[0]) {
		return nil
	}
	var refs []string
	for index, each := range words {
		if shellAssignment.MatchString(each) || strings.HasPrefix(each, "<<<") || (index > 0 && words[index-1] == "<<<") {
			continue
		}
		refs = append(refs, workflowScriptRef.FindAllString(each, -1)...)
	}
	return refs
}

// ghPaginates reports whether gh api args turn pagination on in exactly one
// spelling. A --paginate=false, or a second spelling that can override the
// first, leaves the call unbounded.
func ghPaginates(args []string) bool {
	spellings := slices.DeleteFunc(slices.Clone(args), func(arg string) bool { return arg != "--paginate" && !strings.HasPrefix(arg, "--paginate=") })
	return len(spellings) == 1 && (spellings[0] == "--paginate" || spellings[0] == "--paginate=true")
}

// ghSubcommand drops the flags before the gh subcommand. gh finds its
// subcommand the way cobra strips flags: a --flag or a two-byte -f with no value
// attached takes the next word as its value, so gh --hostname h api runs api.
func ghSubcommand(args []string) []string {
	for len(args) > 0 && strings.HasPrefix(args[0], "-") {
		skip := 1
		if !strings.Contains(args[0], "=") && (strings.HasPrefix(args[0], "--") || len(args[0]) == 2) {
			skip = 2
		}
		args = args[min(skip, len(args)):]
	}
	return args
}

// ghBoolFlags are the gh list flags that take no value.
var ghBoolFlags = map[string]bool{"-d": true, "--draft": true, "-w": true, "--web": true, "--help": true, "-h": true}

// ghFlagValues returns the value of each spelling of a gh flag in args. gh
// drops an empty base and a later spelling overrides an earlier one, so callers
// want exactly one value. An option this list does not know may or may not take
// a value, so args are read both ways, and a disagreement returns no value.
func ghFlagValues(args []string, long, short string) []string {
	read := func(unknownTakesValue bool) []string {
		var values []string
		for i := 0; i < len(args) && args[i] != "--"; i++ {
			name, value, attached := strings.Cut(args[i], "=")
			if !strings.HasPrefix(name, "-") {
				continue
			}
			if !strings.HasPrefix(name, "--") && len(name) > 2 {
				name, value, attached = name[:2], strings.TrimPrefix(args[i][2:], "="), true
			}
			mine := name == long || name == short
			if !attached && !ghBoolFlags[name] && (unknownTakesValue || mine) {
				i++
				value = strings.Join(args[min(i, len(args)):min(i+1, len(args))], "")
			}
			if mine {
				values = append(values, value)
			}
		}
		return values
	}
	if values := read(true); slices.Equal(values, read(false)) {
		return values
	}
	return nil
}

func positiveInt(text string) bool {
	n, err := strconv.Atoi(text)
	return err == nil && n > 0
}

// WorkflowJQProgram is one inline jq program from a workflow run: body. Flags
// holds the declarations the program needs to compile (--arg and -L).
type WorkflowJQProgram struct {
	Where   string
	Program string
	Flags   []string
}

// WorkflowInlineJQPrograms returns every jq program a run: body passes inline,
// from jq itself and from gh --jq.
func WorkflowInlineJQPrograms(rootDir string) ([]WorkflowJQProgram, error) {
	documents, files, err := loadWorkflowDocuments(rootDir)
	if err != nil {
		return nil, err
	}
	var programs []WorkflowJQProgram
	for _, file := range files {
		for job, definition := range documents[file].Jobs {
			for index, step := range definition.Steps {
				where := file + " " + job + " " + stepLabel(index, step)
				for _, args := range commandArgs(step.Run, "jq") {
					if program, ok := parseJQInvocation(rootDir, where, args); ok {
						programs = append(programs, program)
					}
				}
				for _, args := range commandArgs(step.Run, "gh") {
					for i, arg := range args {
						switch {
						case (arg == "--jq" || arg == "-q") && i+1 < len(args):
							programs = append(programs, WorkflowJQProgram{Where: where, Program: args[i+1]})
						case strings.HasPrefix(arg, "--jq="):
							programs = append(programs, WorkflowJQProgram{Where: where, Program: strings.TrimPrefix(arg, "--jq=")})
						case strings.HasPrefix(arg, "-q") && len(arg) > 2:
							programs = append(programs, WorkflowJQProgram{Where: where, Program: strings.TrimPrefix(arg[2:], "=")})
						}
					}
				}
			}
		}
	}
	return programs, nil
}

func parseJQInvocation(rootDir, where string, args []string) (WorkflowJQProgram, bool) {
	var flags []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--arg" || arg == "--argjson" || arg == "--slurpfile" || arg == "--rawfile":
			if i+2 >= len(args) {
				return WorkflowJQProgram{}, false
			}
			value := ""
			switch arg {
			case "--argjson":
				value = "null"
			case "--slurpfile", "--rawfile":
				value = os.DevNull
			}
			flags = append(flags, arg, args[i+1], value)
			i += 2
		case (arg == "-L" || arg == "--library-path") && i+1 < len(args):
			flags = append(flags, "-L", filepath.Join(rootDir, args[i+1]))
			i++
		case arg == "--indent" && i+1 < len(args):
			flags = append(flags, arg, args[i+1])
			i++
		case arg == "--" && i+1 < len(args):
			return WorkflowJQProgram{Where: where, Program: args[i+1], Flags: append(flags, arg)}, true
		case arg == "-f" || arg == "--from-file":
			return WorkflowJQProgram{}, false // the program is in a file
		case strings.HasPrefix(arg, "-"):
			// Every other option goes to jq as written. jq rejects one it does
			// not know, so an option this parser misreads fails the compile
			// check instead of hiding the program behind its value.
			flags = append(flags, arg)
		default:
			return WorkflowJQProgram{Where: where, Program: arg, Flags: flags}, true
		}
	}
	return WorkflowJQProgram{}, false
}

var shellAssignment = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

// shellRedirect matches a redirection word; a bare operator takes the next word.
var shellRedirect = regexp.MustCompile(`^([0-9]+|\{[A-Za-z_][A-Za-z0-9_]*\})?(&>|[<>])[<>&|]*`)

var shellKeywords = []string{"if", "then", "do", "else", "elif", "while", "until", "!", "time", "{"}

// commandArgs returns the arguments of every command named name in script.
func commandArgs(script, name string) [][]string {
	var found [][]string
	for _, words := range shellCommands(script) {
		if words[0] == name {
			found = append(found, words[1:])
		}
	}
	return found
}

// shellCommands returns the words of every command in script, including those
// in if, for, and while bodies and in $( ) substitutions, without comments and
// heredoc bodies. It does not use ShellInvocations: that parser drops
// compound-command bodies because they are not proved to run, and this lint
// must read them too. A function body is read only when a command or a trap
// action calls the function, with the last definition before the call, so a
// function nobody calls runs nothing. Trap actions run at the end, when bash
// resolves their calls.
func shellCommands(script string) [][]string {
	var found [][]string
	var traps []string
	bodies := map[string]string{}
	for _, part := range functionBodies(flattenSubstitutions(withoutHeredocBodies(script))) {
		if part.name != "" {
			bodies[part.name] = part.text
			continue
		}
		found = append(found, expandCalls(flatCommands(part.text), bodies, nil, &traps)...)
	}
	for i := 0; i < len(traps); i++ {
		found = append(found, expandCalls(flatCommands(traps[i]), bodies, nil, &traps)...)
	}
	return found
}

// expandCalls puts each called function's body right after every command that
// calls it, so a cd in the body moves the commands after the call. calling
// holds the functions being expanded, so a recursive call is read once. Each
// new trap action goes to traps.
func expandCalls(commands [][]string, bodies map[string]string, calling []string, traps *[]string) [][]string {
	var found [][]string
	for _, words := range commands {
		found = append(found, words)
		if words[0] == "trap" && len(words) > 1 && !slices.Contains(*traps, words[1]) {
			*traps = append(*traps, words[1])
		}
		if body, defined := bodies[words[0]]; defined && !slices.Contains(calling, words[0]) {
			found = append(found, expandCalls(flatCommands(body), bodies, append(calling, words[0]), traps)...)
		}
	}
	return found
}

// flatCommands returns the words of every command in text that
// flattenSubstitutions has already rewritten.
func flatCommands(text string) [][]string {
	var found [][]string
	var words []string
	var word strings.Builder
	inWord := false
	endWord := func() {
		if inWord {
			words = append(words, word.String())
			word.Reset()
			inWord = false
		}
	}
	endCommand := func() {
		endWord()
		i := 0
		for i < len(words) {
			redirect := shellRedirect.FindString(words[i])
			if redirect == "" && !slices.Contains(shellKeywords, words[i]) && !shellAssignment.MatchString(words[i]) {
				break
			}
			if redirect != "" && redirect == words[i] {
				i++
			}
			i++
		}
		if i < len(words) {
			found = append(found, wrappedCommand(words[i:]))
		}
		words = nil
	}
	var quote byte
	for i := 0; i < len(text); i++ {
		c := text[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			} else if c == '\\' && quote == '"' && i+1 < len(text) {
				i++
				word.WriteByte(text[i])
			} else {
				word.WriteByte(c)
			}
		case c == '\'' || c == '"':
			quote, inWord = c, true
		case c == '\\' && i+1 < len(text):
			i++
			if text[i] != '\n' {
				word.WriteByte(text[i])
				inWord = true
			}
		case c == ' ' || c == '\t':
			endWord()
		case (c == '&' || c == '|') && strings.HasSuffix(word.String(), ">"), c == '&' && strings.HasSuffix(word.String(), "<"):
			word.WriteByte(c) // a redirection such as >&2 or >|f
		case c == '\n' || c == ';' || c == '&' || c == '|' || c == '(' || c == ')':
			endCommand()
		default:
			word.WriteByte(c)
			inWord = true
		}
	}
	endCommand()
	return found
}
