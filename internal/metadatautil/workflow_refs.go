// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/omkhar/workcell/internal/rootio"
	"gopkg.in/yaml.v3"
)

const (
	workflowRefsBaselinePath = "policy/workflow-refs-baseline.tsv"
	workflowActionInputsPath = "tests/fixtures/actions/inputs.tsv"
	workflowRefsMaxBytes     = 4 << 20
)

// workflowScriptRef finds ./scripts/... paths in the words of a run body.
var workflowScriptRef = regexp.MustCompile(`\./scripts/[A-Za-z0-9_./-]+[$*{\[]?`)

// ponytail: gh list subcommands are a fixed table; derive from `gh help` if gh grows more.
var ghListGroups = []string{"cache", "codespace", "gist", "gpg-key", "issue", "label", "pr", "project", "release", "repo", "ruleset", "run", "secret", "ssh-key", "variable", "workflow"}

type workflowRefHit struct{ kind, file, job, step string }

func (h workflowRefHit) key() string {
	return strings.Join([]string{h.kind, h.file, h.job, h.step}, "\t")
}

// CheckWorkflowRefs ratchets workflow run: bodies and uses: steps. A hit that
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

func readWorkflowActionInputs(rootDir string) (map[string]map[string]bool, error) {
	data, err := rootio.ReadFileNoFollow(filepath.Join(rootDir, workflowActionInputsPath), "action inputs cache", workflowRefsMaxBytes)
	if err != nil {
		return nil, err
	}
	actions := map[string]map[string]bool{}
	for line := range strings.Lines(string(data)) {
		ref, names, found := strings.Cut(strings.TrimSuffix(line, "\n"), "\t")
		if !found || ref == "" {
			return nil, fmt.Errorf("%s: malformed row %q", workflowActionInputsPath, line)
		}
		inputs := map[string]bool{}
		for name := range strings.SplitSeq(names, ",") {
			inputs[strings.ToLower(name)] = true
		}
		actions[ref] = inputs
	}
	return actions, nil
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
	actions, err := readWorkflowActionInputs(rootDir)
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
				var refs []string
				for _, words := range shellCommands(step.Run) {
					for _, each := range words {
						refs = append(refs, workflowScriptRef.FindAllString(each, -1)...)
					}
				}
				for _, match := range refs {
					if strings.ContainsAny(match[len(match)-1:], "$*{[") {
						continue // a dynamic name cannot be resolved statically
					}
					if slices.Contains(strings.Split(match, "/"), "..") {
						add("script-path-escapes " + match) // never probe outside rootDir
						continue
					}
					if _, err := rootio.ReadFileNoFollow(filepath.Join(rootDir, match[2:]), "workflow script", workflowRefsMaxBytes); err != nil {
						if !errors.Is(err, fs.ErrNotExist) {
							return nil, err
						}
						add("missing-script " + match)
					}
				}
				for _, args := range commandArgs(step.Run, "gh") {
					args = ghSubcommand(args)
					switch {
					case len(args) > 0 && args[0] == "api":
						if !hasAnyArg(args, "--paginate") { // gh api has no --limit
							add("gh-api-unbounded")
						}
					case len(args) > 1 && args[1] == "list" && slices.Contains(ghListGroups, args[0]):
						if !hasAnyArg(args, "--limit", "-L") {
							add("gh-" + args[0] + "-list-unbounded")
						}
						if args[0] == "pr" && !hasAnyArg(args, "--base", "-B") {
							add("gh-pr-list-no-base")
						}
					}
				}
				if step.Uses == "" || strings.HasPrefix(step.Uses, "./") || strings.HasPrefix(step.Uses, "docker://") {
					continue
				}
				inputs, ok := actions[step.Uses]
				if !ok {
					add("uses-not-cached " + step.Uses)
					continue
				}
				for key := range step.With {
					if !inputs[strings.ToLower(key)] {
						add("with-unknown-input " + step.Uses + " " + key)
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

// ghSubcommand drops the flags before the gh subcommand, such as -R owner/repo.
func ghSubcommand(args []string) []string {
	for len(args) > 0 && strings.HasPrefix(args[0], "-") {
		skip := 1
		if (args[0] == "-R" || args[0] == "--repo") && len(args) > 1 {
			skip = 2
		}
		args = args[skip:]
	}
	return args
}

// hasAnyArg matches a flag as a whole word, as --flag=value, or (single dash) as -Fvalue.
func hasAnyArg(args []string, flags ...string) bool {
	for _, arg := range args {
		for _, flag := range flags {
			if arg == flag || strings.HasPrefix(arg, flag+"=") || (len(flag) == 2 && flag[0] == '-' && flag[1] != '-' && strings.HasPrefix(arg, flag)) {
				return true
			}
		}
	}
	return false
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
		case arg == "-L" && i+1 < len(args):
			flags = append(flags, "-L", filepath.Join(rootDir, args[i+1]))
			i++
		case arg == "-f" || arg == "--from-file" || arg == "--args" || arg == "--jsonargs":
			return WorkflowJQProgram{}, false // program is in a file, or the rest are data
		case strings.HasPrefix(arg, "-"):
			// output flags such as -r -c -e -n -S do not change compilation
		default:
			return WorkflowJQProgram{Where: where, Program: arg, Flags: flags}, true
		}
	}
	return WorkflowJQProgram{}, false
}

// flattenSubstitutions rewrites each $( ... ) in a run body into its own line,
// so ShellInvocations, which reads top-level commands, sees the commands inside.
// It tracks quotes, so a ) or $( inside a quoted jq program is left alone. Where
// a substitution sits inside double quotes, the quote is closed before the new
// line and reopened after it.
func flattenSubstitutions(script string) string {
	var out strings.Builder
	frames := []bool{false} // one entry per open substitution: is it inside double quotes
	single := false
	for i := 0; i < len(script); i++ {
		c := script[i]
		top := len(frames) - 1
		switch {
		case single:
			single = c != '\''
			out.WriteByte(joinQuotedNewline(c))
		case c == '\\' && i+1 < len(script):
			out.WriteByte(c)
			i++
			out.WriteByte(script[i])
		case c == '#' && !frames[top] && (i == 0 || strings.ContainsRune(" \t\n;(", rune(script[i-1]))):
			for i < len(script) && script[i] != '\n' {
				i++
			}
			out.WriteByte('\n')
		case c == '\'' && !frames[top]:
			single = true
			out.WriteByte(c)
		case c == '"':
			frames[top] = !frames[top]
			out.WriteByte(c)
		case c == '$' && strings.HasPrefix(script[i:], "$(") && !strings.HasPrefix(script[i:], "$(("):
			if frames[top] {
				out.WriteByte('"')
			}
			out.WriteByte('\n')
			frames = append(frames, false)
			i++
		case c == ')' && top > 0 && !frames[top]:
			frames = frames[:top]
			out.WriteByte('\n')
			if frames[top-1] {
				out.WriteByte('"')
			}
		case frames[top]:
			out.WriteByte(joinQuotedNewline(c))
		default:
			out.WriteByte(c)
		}
	}
	return out.String()
}

// joinQuotedNewline turns a newline inside a quoted word into a space, so a
// multi-line jq program stays one word. ShellInvocations drops the rest of a
// quoted word that runs past the end of its line.
func joinQuotedNewline(c byte) byte {
	if c == '\n' {
		return ' '
	}
	return c
}

var shellAssignment = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

var shellKeywords = []string{"if", "then", "do", "else", "elif", "while", "until", "!", "time"}

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
// must read them too.
func shellCommands(script string) [][]string {
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
		for i < len(words) && (slices.Contains(shellKeywords, words[i]) || shellAssignment.MatchString(words[i])) {
			i++
		}
		if i < len(words) {
			found = append(found, words[i:])
		}
		words = nil
	}
	var quote byte
	text := flattenSubstitutions(withoutHeredocBodies(script))
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
