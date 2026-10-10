// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"cmp"
	"fmt"
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

type workflowRefHit struct{ kind, file, job, step string }

// unspelled reports whether bash may rewrite word: an expansion, a brace or a
// pathname pattern, or a Windows name with a \ or .exe; a lone [ is test.
func unspelled(word string) bool {
	return strings.ContainsAny(word, "$`*?\\") || braceExpansion.MatchString(word) ||
		strings.Contains(word, "[") && strings.Contains(word, "]") || strings.HasSuffix(strings.ToLower(word), ".exe")
}

// shellStartupEnv names BASH_ENV or ENV, or sets a variable whose name is an expansion.
var shellStartupEnv = regexp.MustCompile(`(?m)(?:^|[^A-Za-z0-9_])(?:BASH_)?ENV(?:[^A-Za-z0-9_{]|$)|` +
	`(?:^|[;&|(]|\b(?:export|declare|typeset|readonly|local)(?:\s+-\w+)*)\s*\w*\$\{?\w+\}?\w*\+?=|GITHUB_ENV.*\$\{?\w+\}?\w*=|\$\{?\w+\}?\w*=.*GITHUB_ENV`)

func setsStartupFile(env map[string]string) bool { return env["BASH_ENV"] != "" || env["ENV"] != "" }

// githubHostedPosix matches the GitHub-hosted labels a shell-less step surely runs bash on.
var githubHostedPosix = regexp.MustCompile(`^(?:ubuntu-(?:latest|slim|24\.04|22\.04)|ubuntu-(?:24|22)\.04-arm|macos-(?:latest|26|15|14|13)(?:-intel|-large|-xlarge)?)$`)

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

// ghInArguments reports a gh api call in another command's words, as any
// wrapper or quoted program would hand it on unseen; echo, printf, command -v,
// find without -exec and a shell, whose program is read, only hold the text.
func ghInArguments(words []string) bool {
	name, text := commandName(words[0]), " "+strings.Join(words[1:], " ")+" "
	data := name == "echo" || name == "printf" || name == "command" && strings.Contains(text, " -v") ||
		name == "find" && !strings.Contains(text, " -exec") && !strings.Contains(text, " -ok") || slices.Contains([]string{"sh", "bash", "dash", "ksh"}, name)
	return name != "gh" && !data && strings.Contains(text, " gh api ")
}

// unmodeledWrappers run a program after options, or in a language, this lint does not read.
var unmodeledWrappers = map[string]bool{
	"pwsh": true, "powershell": true, "cmd": true, "setarch": true, "setsid": true, "stdbuf": true, "xargs": true, "flock": true,
	"ionice": true, "chrt": true, "taskset": true, "doas": true, "su": true, "runuser": true, "chroot": true, "unshare": true,
	"nsenter": true, "strace": true, "ltrace": true, "script": true, "watch": true, "unbuffer": true, "caffeinate": true,
	"systemd-run": true, "busybox": true, "prlimit": true, "setpriv": true, "fakeroot": true, "firejail": true, "bwrap": true, "proot": true, "cpulimit": true,
}

var braceExpansion = regexp.MustCompile(`\{[^}]*(,|\.\.)[^}]*\}`)

func (h workflowRefHit) key() string {
	return strings.Join([]string{h.kind, h.file, h.job, h.step}, "\t")
}

// CheckWorkflowRefs ratchets workflow run: bodies: a hit not in the baseline
// fails, and so does a baseline row with no hit, so the baseline only shrinks.
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
				if shell := commandName(strings.Fields(stepShell(documents[file], definition, step) + " bash")[0]); step.Run != "" &&
					(shell != "bash" && shell != "sh" || stepShell(documents[file], definition, step) == "" &&
						(definition.RunsOn.Kind != yaml.ScalarNode || !githubHostedPosix.MatchString(definition.RunsOn.Value))) {
					add("command-unresolved") // a body in a language this lint does not read, as pwsh
					continue
				}
				if slices.ContainsFunc([]map[string]string{documents[file].Env, definition.Env, step.Env}, setsStartupFile) ||
					shellStartupEnv.MatchString(strings.NewReplacer("\\\n", "", `"`, "", `'`, "", `\`, "").Replace(step.Run)) {
					add("command-unresolved") // bash sources BASH_ENV, or sh ENV, before the run body
				}
				for _, words := range EveryShellCommand(step.Run) {
					// A lint of what may run fails closed on a command it cannot spell,
					// and on an alias definition, which can rename any later command.
					if words[0] == "eval" {
						add("eval-unresolved")
					} else if unspelled(words[0]) || (words[0] == "alias" && len(words) > 1) ||
						words[0] == "source" || words[0] == "." || unmodeledWrappers[commandName(words[0])] || ghInArguments(words) ||
						commandName(words[0]) == "find" && slices.ContainsFunc(words, func(w string) bool { return strings.HasPrefix(w, "-exec") || strings.HasPrefix(w, "-ok") }) {
						add("command-unresolved") // a file, or a program, the lint does not see
					} else if _, ok := shellProgram(words, "$_"); ok {
						// EveryShellCommand reads each shell program it can spell in
						// place of the shell, so a shell left here runs one it cannot.
						add("command-unresolved")
					}
				}
				for _, args := range commandArgs(step.Run, "gh") {
					if help := ghFlagValues(args, "--help", "-h"); len(help) > 0 {
						if on, err := strconv.ParseBool(cmp.Or(help[len(help)-1], "true")); err != nil || on {
							continue // gh prints usage and runs nothing
						}
					}
					args = ghSubcommand(args)
					switch {
					case len(args) > 0 && unspelled(args[0]):
						add("command-unresolved") // the subcommand cannot be spelled
					case len(args) > 0 && args[0] == "api":
						if !ghPaginates(args) { // gh api has no --limit
							add("gh-api-unbounded")
						} else if slices.ContainsFunc(args, unspelled) {
							add("command-unresolved") // an expansion, as ${{ … }} or {--paginate=false,}, can disable pagination
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

// ghPaginates reports whether gh api args turn pagination on in exactly one
// spelling. A --paginate=false, or a second spelling that can override the
// first, leaves the call unbounded. Another option's value is not a spelling.
func ghPaginates(args []string) bool {
	spellings := ghFlagValues(args, "--paginate", "")
	return len(spellings) == 1 && (spellings[0] == "" || spellings[0] == "true")
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

// ghFlagTakesValue says whether each known gh api flag takes a
// value, per gh 2.102 help. A short flag that takes a value in one checked
// command and none in another, such as -a, stays unknown.
var ghFlagTakesValue = map[string]bool{
	"--help": false, "-h": false,
	"--paginate": false, "--slurp": false, "-i": false, "--include": false, "--silent": false, "--verbose": false,
	"-f": true, "--raw-field": true, "-F": true, "--field": true, "-H": true, "--header": true, "-X": true,
	"--method": true, "-p": true, "--preview": true, "-q": true, "--jq": true, "-t": true, "--template": true,
	"--cache": true, "--input": true, "--hostname": true,
}

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

// commandArgs returns the arguments of every command named name in script,
// bare or by path, as /usr/bin/gh runs gh.
func commandArgs(script, name string) [][]string {
	var found [][]string
	for _, words := range EveryShellCommand(script) {
		if commandName(words[0]) == name {
			found = append(found, words[1:])
		}
	}
	return found
}
