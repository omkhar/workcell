// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"

	"gopkg.in/yaml.v3"
)

var (
	laneScriptRoot = regexp.MustCompile(`^(\$ROOT_DIR/|\$\{ROOT_DIR\}/)?(\./)?`)
	laneScriptPath = regexp.MustCompile(`^scripts/[A-Za-z0-9_./-]+\.sh$`)
)

// LaneScripts returns each scripts/*.sh path that a lane runs as a command,
// sorted and unique. The lanes are scripts/validate-repo.sh, the
// scripts/ci/job-*.sh scripts and the run steps of the workflows, and a lane
// script counts as run itself. A leading ${ROOT_DIR}/, $ROOT_DIR/ or ./ is
// dropped, so every spelling of one script is one path. Any other variable
// names another directory, so its path is not a repository script.
func LaneScripts(rootDir string) ([]string, error) {
	jobs, err := filepath.Glob(filepath.Join(rootDir, "scripts", "ci", "job-*.sh"))
	if err != nil {
		return nil, err
	}
	var runs, scripts []string
	for _, path := range append([]string{filepath.Join(rootDir, "scripts", "validate-repo.sh")}, jobs...) {
		body, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		relative, err := filepath.Rel(rootDir, path)
		if err != nil {
			return nil, err
		}
		runs, scripts = append(runs, string(body)), append(scripts, filepath.ToSlash(relative))
	}
	workflows, err := filepath.Glob(filepath.Join(rootDir, ".github", "workflows", "*.yml"))
	if err != nil {
		return nil, err
	}
	for _, path := range workflows {
		body, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var document workflowDocument
		if err := yaml.Unmarshal(body, &document); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		for _, job := range document.Jobs {
			for _, step := range job.Steps {
				runs = append(runs, step.Run)
			}
		}
	}
	for _, run := range runs {
		for _, name := range ShellCommandWords(run) {
			if name = laneScriptRoot.ReplaceAllString(name, ""); laneScriptPath.MatchString(name) {
				scripts = append(scripts, name)
			}
		}
	}
	slices.Sort(scripts)
	return slices.Compact(scripts), nil
}
