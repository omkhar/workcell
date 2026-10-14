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
	workflowActionInputsPath = "tests/fixtures/actions/inputs.tsv"
	workflowRefsMaxBytes     = 4 << 20
)

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

// unspelled reports whether bash may rewrite word: an expansion, a brace or a
// pathname pattern; a lone [ is test.
func unspelled(word string) bool {
	return strings.ContainsAny(word, "$`*?") || braceExpansion.MatchString(word) ||
		strings.Contains(word, "[") && strings.Contains(word, "]")
}

// shellStartupEnv names BASH_ENV or ENV, or sets a variable whose name is an expansion.
var shellStartupEnv = regexp.MustCompile(`(?m)(?:^|[^A-Za-z0-9_])(?:BASH_)?ENV(?:[^A-Za-z0-9_{]|$)|` +
	`(?:^|[;&|(]|\b(?:export|declare|typeset|readonly|local)(?:\s+-\w+)*)\s*\w*\$\{?\w+\}?\w*\+?=`)

var githubEnvWrite = regexp.MustCompile(`^\s*(?:echo|printf)\s+["']?([A-Za-z_][A-Za-z0-9_]*)=[^\n]*>>\s*"?\$\{?GITHUB_ENV\}?"?\s*$`)

var dynamicTarget = regexp.MustCompile("(?:^|[^<>&0-9])>>?\\s*\"?(?:\\$\\{!|\\$\\(|`)|\\b(?:declare|typeset|local)(?:\\s+-\\w+)*\\s+-\\w*n")

// unreadEnvWrite reports a comment-free line with a dynamicTarget, a redirection to a file named at run time, which may be GITHUB_ENV, or a nameref, bash's other indirection, or that names GITHUB_ENV other than as a githubEnvWrite, an echo or printf of a literal NAME=value, of a name that is not a startup file, since a later step's bash may source a BASH_ENV whose name this lint cannot spell.
func unreadEnvWrite(run string) bool {
	return slices.ContainsFunc(strings.Split(strings.ReplaceAll(run, "\\\n", ""), "\n"), func(line string) bool {
		write := githubEnvWrite.FindStringSubmatch(line)
		return !strings.HasPrefix(strings.TrimSpace(line), "#") && (dynamicTarget.MatchString(line) ||
			strings.Contains(line, "GITHUB_ENV") && (write == nil || write[1] == "BASH_ENV" || write[1] == "ENV"))
	})
}

func setsStartupFile(env map[string]string) bool { return env["BASH_ENV"] != "" || env["ENV"] != "" }

// githubHostedPosix matches the GitHub-hosted labels a shell-less step surely runs bash on.
var githubHostedPosix = regexp.MustCompile(`^(?:ubuntu-(?:latest|slim|26\.04|24\.04|22\.04)|ubuntu-(?:26|24|22)\.04-arm|macos-(?:latest|26|15|14|13)(?:-intel|-large|-xlarge)?)$`)

// hostedPosixRunner reports whether runs-on names one githubHostedPosix label, as a scalar or a one-item array.
func hostedPosixRunner(runsOn yaml.Node) bool {
	if runsOn.Kind == yaml.SequenceNode && len(runsOn.Content) == 1 {
		runsOn = *runsOn.Content[0]
	}
	return runsOn.Kind == yaml.ScalarNode && githubHostedPosix.MatchString(runsOn.Value)
}

// stepShell returns a step's shell key, else its job's default, else its workflow's.
func stepShell(document workflowDocument, job workflowJob, step workflowStep) string {
	if step.Shell != "" {
		return step.Shell
	}
	var defaults struct{ Run struct{ Shell string } }
	if job.Defaults.Kind != 0 {
		_ = job.Defaults.Decode(&defaults)
	}
	if defaults.Run.Shell != "" {
		return defaults.Run.Shell
	}
	return document.Def.Run["shell"]
}

// unreadShellStep reports a step whose run body is in a language this package does not read, as pwsh or python: a shell other than a plain sh or bash, or no shell on a runner not surely bash.
func unreadShellStep(document workflowDocument, job workflowJob, step workflowStep) bool {
	shell := stepShell(document, job, step)
	return step.Run != "" && !readShell(shell) && (shell != "" || !hostedPosixRunner(job.RunsOn))
}

// startupFileStep reports a step whose shell may source a BASH_ENV or ENV file before its run body, set by the workflow, job or step env or by the body itself, or whose body writes one to GITHUB_ENV for a later step.
func startupFileStep(document workflowDocument, job workflowJob, step workflowStep) bool {
	return slices.ContainsFunc([]map[string]string{document.Env, job.Env, step.Env}, setsStartupFile) ||
		unreadEnvWrite(step.Run) || shellStartupEnv.MatchString(strings.NewReplacer("\\\n", "", `"`, "", `'`, "", `\`, "").Replace(step.Run))
}

// ghInArguments reports a gh api call in another command's words, as any
// wrapper or quoted program would hand it on unseen; echo, printf, command -v,
// and find, trap, builtin, a sudo left standing, which runs nothing, and a
// shell, whose programs are read, only hold the text.
func ghInArguments(words []string) bool {
	name, text := commandName(words[0]), " "+strings.Join(words[1:], " ")+" "
	data := name == "echo" || name == "printf" || name == "command" && strings.Contains(text, " -v") ||
		slices.Contains([]string{"find", "trap", "builtin", "sudo", "sh", "bash", "dash", "ksh"}, name) // their programs are read as commands
	var tokens []string // escapes decoded, split on any blank, as a program would read them
	for _, word := range words[1:] {
		if decoded, err := strconv.Unquote(`"` + strings.ReplaceAll(word, `"`, `\"`) + `"`); err == nil {
			word = decoded
		}
		tokens = append(tokens, strings.Fields(word)...)
	}
	pair := false
	for i := 0; i+1 < len(tokens); i++ {
		pair = pair || commandName(tokens[i]) == "gh" && tokens[i+1] == "api"
	}
	return name != "gh" && !data && pair || interpreters[name] && (len(words) < 2 || strings.HasPrefix(words[1], "-"))
}

// interpreters given an option or no script file may run an inline program, which this lint does not read, so it may run gh unseen; only a script file operand first is read as clean.
var interpreters = map[string]bool{"python": true, "python3": true, "perl": true, "ruby": true, "node": true, "php": true}

// unmodeledWrappers run a program after options, or in a language, this lint
// does not read.
var unmodeledWrappers = map[string]bool{
	"pwsh": true, "powershell": true, "cmd": true, "setarch": true,
	"chrt": true, "taskset": true, "doas": true, "su": true, "runuser": true, "chroot": true,
	"unshare": true, "nsenter": true, "strace": true, "ltrace": true, "script": true, "watch": true,
	"unbuffer": true, "caffeinate": true, "systemd-run": true, "busybox": true, "prlimit": true,
	"setpriv": true, "fakeroot": true, "firejail": true, "bwrap": true, "proot": true, "cpulimit": true,
}

var braceExpansion = regexp.MustCompile(`\{[^}]*(,|\.\.)[^}]*\}`)

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

// readShell reports whether a step shell runs the body as a bash or sh script this lint reads: the bash or sh keyword, or a template that runs one with only the options shellTemplateOption knows and the script {0} last.
func readShell(shell string) bool {
	words := strings.Fields(shell)
	if len(words) == 0 || commandName(words[0]) != "bash" && commandName(words[0]) != "sh" {
		return false
	}
	return len(words) == 1 || words[len(words)-1] == "{0}" && !slices.ContainsFunc(words[1:len(words)-1], func(w string) bool { return !shellTemplateOption.MatchString(w) })
}

var shellTemplateOption = regexp.MustCompile(`^(?:--noprofile|--norc|-[euxo]+|pipefail)$`)

var ghCommands = strings.Fields("agent agents agent-tasks at cs rs skills agent-task api attestation auth browse cache codespace completion config copilot discussion gist gpg-key help issue label licenses org pr preview project release repo ruleset run search secret skill ssh-key status variable version workflow") // gh 2.102's built-in commands and their built-in short names, which no alias or extension can shadow; alias and extension are left out.

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
				if unreadShellStep(documents[file], definition, step) {
					add("command-unresolved") // a body in a language this lint does not read, as pwsh
					continue
				}
				if startupFileStep(documents[file], definition, step) {
					add("command-unresolved") // bash sources BASH_ENV, or sh ENV, before the run body
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
				for _, words := range EveryShellCommand(step.Run) {
					moved = moved || words[0] == "cd" || words[0] == "pushd"
					if words[0] == "eval" { // A lint of what may run fails closed on a command it cannot spell, and on an alias definition, which can rename any later command.
						add("eval-unresolved")
					} else if unspelled(words[0]) || (words[0] == "alias" && len(words) > 1) ||
						words[0] == "source" || words[0] == "." || unmodeledWrappers[commandName(words[0])] || ghInArguments(words) {
						add("command-unresolved") // a file, or a program, the lint does not see
					} else if _, ok, _ := shellProgram(words, "$_"); ok {
						add("command-unresolved") // EveryShellCommand reads each shell program it can spell in place of the shell, so a shell left here runs one it cannot.
					}
					if match := scriptRef(words); match != "" {
						switch {
						case unspelled(match):
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
					case len(args) > 0 && (unspelled(args[0]) || !slices.Contains(ghCommands, args[0])):
						add("command-unresolved") // the subcommand cannot be spelled, or it may be an alias or extension this lint cannot read
					case len(args) > 0 && args[0] == "api":
						if !ghPaginates(args) { // gh api has no --limit
							add("gh-api-unbounded")
						} else if slices.ContainsFunc(args, unspelled) {
							add("command-unresolved") // an expansion, as ${{ … }} or {--paginate=false,}, can disable pagination
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
	counts := map[string]int{} // A second identical hit in one step gets an ordinal, so a new call in a step that already has a baseline row still fails.
	for i, hit := range hits {
		counts[hit.key()]++
		if n := counts[hit.key()]; n > 1 {
			hits[i].kind = fmt.Sprintf("%s#%d", hit.kind, n)
		}
	}
	return hits, nil
}

// scriptRef returns the ./scripts path one command runs: its command word, or
// the script operand of a shell, whole as the reader parsed it. Any other
// argument is data, as the operand of test -e is.
func scriptRef(words []string) string {
	ref := words[0]
	if _, _, file := shellProgram(words, "$_"); file != "" {
		ref = file
	}
	if !strings.HasPrefix(ref, "./scripts/") {
		return ""
	}
	return ref
}

// ghPaginates reports whether gh api args turn pagination on in exactly one spelling. A --paginate=false, or a second spelling that can override the first, leaves the call unbounded. Another option's value is not a spelling.
func ghPaginates(args []string) bool {
	spellings := ghFlagValues(args, "--paginate", "")
	return len(spellings) == 1 && (spellings[0] == "" || spellings[0] == "true")
}

// ghSubcommand drops the flags before the gh subcommand. gh finds its subcommand the way cobra strips flags: a --flag or a two-byte -f with no value attached takes the next word as its value, so gh --hostname h api runs api.
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

// ghFlagTakesValue says whether each known gh list and gh api flag takes a
// value, per gh 2.102 help. A short flag that takes a value in one checked
// command and none in another, such as -a, stays unknown.
var ghFlagTakesValue = map[string]bool{
	"-d": false, "--draft": false, "-w": false, "--web": false, "--help": false, "-h": false,
	"--paginate": false, "--slurp": false, "-i": false, "--include": false, "--silent": false, "--verbose": false,
	"--allow-escape-sequences": false, "--answered": false, "--all": false, "--exclude-drafts": false,
	"--exclude-pre-releases": false, "--archived": false, "--no-archived": false, "--fork": false, "--source": false,
	"--include-content": false, "--public": false, "--secret": false, "--closed": false, "--parents": false,
	"-f": true, "--raw-field": true, "-F": true, "--field": true, "-H": true, "--header": true, "-X": true,
	"--method": true, "-p": true, "--preview": true, "-q": true, "--jq": true, "-t": true, "--template": true,
	"--cache": true, "--input": true, "--hostname": true,
}

// ghFlagValues returns the value of each spelling of a gh flag in args. gh drops an empty base and a later spelling overrides an earlier one, so callers want exactly one value. An option this list does not know may or may not take a value, so args are read both ways, and a disagreement returns no value.
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
			if takes, known := ghFlagTakesValue[name]; !attached && (takes || !known && (unknownTakesValue || mine)) {
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
					// another flag's value, such as --search --jq, is not a program
					for _, program := range ghFlagValues(args, "--jq", "-q") {
						programs = append(programs, WorkflowJQProgram{Where: where, Program: program})
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

// commandArgs returns the arguments of every command named name in script, bare or by path, as /usr/bin/gh runs gh.
func commandArgs(script, name string) [][]string {
	var found [][]string
	for _, words := range EveryShellCommand(script) {
		if commandName(words[0]) == name {
			found = append(found, words[1:])
		}
	}
	return found
}
