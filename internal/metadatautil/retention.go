// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// retentionPolicyFile is the version-1 policy that binds each workflow's
// uploaded artifacts to their required retention-days. Binding per artifact
// name (not a workflow-wide value set) lets the check detect a retention value
// being moved from one artifact to another.
type retentionPolicyFile struct {
	Version   int                       `json:"version"`
	Artifacts map[string]map[string]int `json:"artifacts"`
}

type retentionWorkflowDoc struct {
	Jobs map[string]retentionJob `yaml:"jobs"`
}

type retentionJob struct {
	Steps []retentionStep `yaml:"steps"`
}

type retentionStep struct {
	Uses string        `yaml:"uses"`
	With retentionWith `yaml:"with"`
}

type retentionWith struct {
	Name          string `yaml:"name"`
	RetentionDays *int   `yaml:"retention-days"`
}

const uploadArtifactPrefix = "actions/upload-artifact@"

// CheckRetentionPolicy asserts that every actions/upload-artifact step in the
// workflows sets an explicit retention-days and that each uploaded artifact's
// retention matches policy/retention-policy.json exactly (per artifact name),
// so no artifact silently inherits the repository default and the documented
// retention window cannot drift.
func CheckRetentionPolicy(rootDir, policyPath string) error {
	var policy retentionPolicyFile
	if err := readJSONFile(policyPath, &policy); err != nil {
		return err
	}
	if policy.Version != 1 {
		return fmt.Errorf("%s must use version 1", policyPath)
	}
	if len(policy.Artifacts) == 0 {
		return fmt.Errorf("%s must define at least one workflow", policyPath)
	}

	workflowDir := filepath.Join(rootDir, ".github", "workflows")
	workflowPaths, err := filepath.Glob(filepath.Join(workflowDir, "*.yml"))
	if err != nil {
		return err
	}

	// actual[workflowFile][artifactName] = retention-days observed in an upload.
	actual := map[string]map[string]int{}
	var problems []string

	for _, path := range workflowPaths {
		wf := filepath.Base(path)
		content, err := readText(path)
		if err != nil {
			return err
		}
		var doc retentionWorkflowDoc
		if err := yaml.Unmarshal([]byte(content), &doc); err != nil {
			return fmt.Errorf("%s: %w", wf, err)
		}
		for _, jobName := range sortedJobNames(doc.Jobs) {
			for _, step := range doc.Jobs[jobName].Steps {
				if !strings.HasPrefix(step.Uses, uploadArtifactPrefix) {
					continue
				}
				name := strings.TrimSpace(step.With.Name)
				if name == "" {
					problems = append(problems, fmt.Sprintf("%s job %s has an upload-artifact step with no artifact name", wf, jobName))
					continue
				}
				if step.With.RetentionDays == nil {
					problems = append(problems, fmt.Sprintf("%s artifact %q has no explicit retention-days; every upload-artifact step must set one", wf, name))
					continue
				}
				days := *step.With.RetentionDays
				if existing, seen := actual[wf][name]; seen && existing != days {
					problems = append(problems, fmt.Sprintf("%s artifact %q is uploaded with conflicting retention-days (%d and %d)", wf, name, existing, days))
					continue
				}
				if actual[wf] == nil {
					actual[wf] = map[string]int{}
				}
				actual[wf][name] = days
			}
		}
	}

	// Every uploaded artifact must be documented with a matching retention.
	for _, wf := range sortedWorkflowKeys(actual) {
		documented, ok := policy.Artifacts[wf]
		if !ok {
			problems = append(problems, fmt.Sprintf("%s uploads artifacts but is not in %s", wf, filepath.Base(policyPath)))
			continue
		}
		for _, name := range sortedIntMapKeys(actual[wf]) {
			want, ok := documented[name]
			if !ok {
				problems = append(problems, fmt.Sprintf("%s artifact %q is not documented in %s", wf, name, filepath.Base(policyPath)))
				continue
			}
			if want != actual[wf][name] {
				problems = append(problems, fmt.Sprintf("%s artifact %q retention drift: documented %d, workflow has %d", wf, name, want, actual[wf][name]))
			}
		}
	}

	// Every documented artifact must correspond to a real upload.
	for _, wf := range sortedWorkflowKeys(policy.Artifacts) {
		for _, name := range sortedIntMapKeys(policy.Artifacts[wf]) {
			if _, ok := actual[wf][name]; !ok {
				problems = append(problems, fmt.Sprintf("%s documents artifact %q in %s but no such upload-artifact step exists", wf, name, filepath.Base(policyPath)))
			}
		}
	}

	if len(problems) > 0 {
		sort.Strings(problems)
		return fmt.Errorf("retention policy check failed:\n  %s", strings.Join(problems, "\n  "))
	}
	return nil
}

func sortedJobNames(jobs map[string]retentionJob) []string {
	names := make([]string, 0, len(jobs))
	for name := range jobs {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func sortedWorkflowKeys(m map[string]map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedIntMapKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
