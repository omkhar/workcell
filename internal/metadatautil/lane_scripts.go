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
// script. EveryShellCommand reads each body, so a
// comment, a heredoc body, quoted text or an uncalled function wires nothing,
// while a command behind a guard or after an exit still wires its script.
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
				runs, scripts = append(runs, laneRun{body: string(body)}), append(scripts, rel)
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
					runs = append(runs, laneRun{dir, step.Run, startup})
				}
			}
		}
	}
	for _, run := range runs {
		moved := run.moved || strings.Contains(run.dir, "${{") || path.IsAbs(run.dir)
		for _, words := range EveryShellCommand(run.body) {
			if rebindsLane(words) {
				return nil, fmt.Errorf("lane wiring is unreadable: a lane runs %q, which may rebind the scripts it runs", strings.Join(words, " "))
			}
			name := words[0]
			if _, _, file := shellProgram(words, "$_"); file != "" {
				name = file // bash scripts/x.sh runs x.sh
			}
			// A cd or pushd, or a sourced body this reader does not see, may move
			// the directory, so a later relative path names no known script.
			if cmd := unwrapBuiltins(words[assignmentPrefix(words):]); len(cmd) > 0 {
				moved = moved || slices.Contains([]string{"cd", "pushd", "source", "."}, cmd[0])
			}
			prefix := laneScriptRoot.FindStringSubmatch(name)
			name = name[len(prefix[0]):]
			if prefix[1] == "" {
				if moved || path.IsAbs(name) {
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

// laneSource is a source of a fixed repository path, whose text this check reads as a lane of its own when it is one.
var laneSource = regexp.MustCompile(`^\$\{?ROOT_DIR\}?/[A-Za-z0-9_./-]+$`)

// lanePlainProgram is one expansion of a named variable before an optional literal path, a program the lane chose, such as ${GO_BIN}.
var lanePlainProgram = regexp.MustCompile(`^\$\{?[A-Za-z][A-Za-z0-9_]*\}?(?:/[A-Za-z0-9_./-]*)?$`)

// rebindsLane reports whether the words may change what a later command runs, so the lane text no longer shows its wiring: an alias, a source of a path other than a fixed ${ROOT_DIR}/ one, or a program the reader cannot name, which may itself be source, as s${x-}ource or "$@" is.
func rebindsLane(words []string) bool {
	switch words[0] {
	case "alias":
		return len(words) > 1
	case "source", ".":
		return len(words) < 2 || !laneSource.MatchString(words[1]) || strings.Contains(words[1], "..")
	}
	return unspelled(words[0]) && !lanePlainProgram.MatchString(words[0])
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
