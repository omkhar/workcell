// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"fmt"
	"os"
	"path"
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
// scripts/ci/job-*.sh scripts and the run steps of the workflows, and a lane
// script counts as run itself. A leading ${ROOT_DIR}/, $ROOT_DIR/ or ./ is
// dropped, so every spelling of one script is one path. Any other variable
// names another directory, so its path is not a repository script.
func LaneScripts(rootDir string) ([]string, error) {
	root, err := os.Open(rootDir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	var runs, scripts []string
	for _, lane := range [][2]string{{"scripts", "validate-repo.sh"}, {"scripts/ci", "job-*.sh"}, {".github/workflows", "*.yml"}} {
		files, err := readLaneFiles(root, lane[0], lane[1])
		if err != nil {
			return nil, err
		}
		for rel, body := range files {
			if !strings.HasSuffix(rel, ".yml") {
				runs, scripts = append(runs, string(body)), append(scripts, rel)
				continue
			}
			var document workflowDocument
			if err := yaml.Unmarshal(body, &document); err != nil {
				return nil, fmt.Errorf("parse %s: %w", rel, err)
			}
			for _, job := range document.Jobs {
				for _, step := range job.Steps {
					runs = append(runs, step.Run)
				}
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

// readLaneFiles reads each file in dir that matches pattern, keyed by its
// repository path. It follows no symlink, so a lane cannot borrow text from
// outside the repository, and a dir with no match fails.
func readLaneFiles(root *os.File, dir, pattern string) (map[string][]byte, error) {
	parent, err := rootio.OpenDirAtNoFollow(root, dir)
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	names, err := parent.Readdirnames(-1)
	if err != nil {
		return nil, err
	}
	files := map[string][]byte{}
	for _, name := range names {
		if matched, _ := path.Match(pattern, name); matched {
			if files[dir+"/"+name], err = rootio.ReadFileAtNoFollow(parent, name, "lane file", 1<<20); err != nil {
				return nil, err
			}
		}
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no lane file matches %s/%s", dir, pattern)
	}
	return files, nil
}
