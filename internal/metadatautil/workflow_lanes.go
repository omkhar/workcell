// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type WorkflowLaneManifest struct {
	Version int                         `json:"version"`
	Policy  string                      `json:"policy"`
	Lanes   []WorkflowLaneManifestEntry `json:"lanes"`
}

type WorkflowLaneManifestEntry struct {
	ID                string              `json:"id"`
	WorkflowPath      string              `json:"workflow_path"`
	WorkflowName      string              `json:"workflow_name"`
	JobID             string              `json:"job_id"`
	JobName           string              `json:"job_name"`
	Matrix            map[string]string   `json:"matrix,omitempty"`
	WorkflowEvents    []string            `json:"workflow_events,omitempty"`
	WorkflowPathGlobs map[string][]string `json:"workflow_path_globs,omitempty"`
	RequiredLabels    []string            `json:"required_labels,omitempty"`
	Profiles          []string            `json:"profiles,omitempty"`
	Authority         string              `json:"authority"`
	LocalMode         string              `json:"local_mode"`
	LocalScript       string              `json:"local_script,omitempty"`
	LocalOrder        int                 `json:"local_order,omitempty"`
	GitHubOnlyReason  string              `json:"github_only_reason,omitempty"`
}

type workflowLanePolicyFile struct {
	Version int                                `json:"version"`
	Lanes   map[string]workflowLanePolicyEntry `json:"lanes"`
}

type workflowLanePolicyEntry struct {
	Profiles         []string `json:"profiles,omitempty"`
	Authority        string   `json:"authority"`
	LocalMode        string   `json:"local_mode"`
	LocalScript      string   `json:"local_script,omitempty"`
	LocalOrder       int      `json:"local_order,omitempty"`
	GitHubOnlyReason string   `json:"github_only_reason,omitempty"`
}

type WorkflowLanePlannerConfig struct {
	Profile      string   `json:"profile"`
	Event        string   `json:"event,omitempty"`
	BaseBranch   string   `json:"base_branch,omitempty"`
	Labels       []string `json:"labels,omitempty"`
	ChangedFiles []string `json:"changed_files,omitempty"`
}

type WorkflowLanePlan struct {
	Version      int                     `json:"version"`
	Profile      string                  `json:"profile"`
	Event        string                  `json:"event,omitempty"`
	BaseBranch   string                  `json:"base_branch,omitempty"`
	Labels       []string                `json:"labels,omitempty"`
	ChangedFiles []string                `json:"changed_files,omitempty"`
	Lanes        []WorkflowLanePlanEntry `json:"lanes"`
}

type WorkflowLanePlanEntry struct {
	ID               string            `json:"id"`
	WorkflowPath     string            `json:"workflow_path"`
	JobName          string            `json:"job_name"`
	Matrix           map[string]string `json:"matrix,omitempty"`
	Status           string            `json:"status"`
	Reason           string            `json:"reason,omitempty"`
	LocalScript      string            `json:"local_script,omitempty"`
	LocalOrder       int               `json:"local_order,omitempty"`
	Profiles         []string          `json:"profiles,omitempty"`
	Authority        string            `json:"authority"`
	LocalMode        string            `json:"local_mode"`
	GitHubOnlyReason string            `json:"github_only_reason,omitempty"`
}

type workflowLaneDocument struct {
	Name string                        `yaml:"name"`
	On   any                           `yaml:"on"`
	Jobs map[string]workflowLaneRawJob `yaml:"jobs"`
}

type workflowLaneRawJob struct {
	Name     string                     `yaml:"name"`
	If       string                     `yaml:"if"`
	Strategy workflowLaneRawJobStrategy `yaml:"strategy"`
}

type workflowLaneRawJobStrategy struct {
	Matrix workflowLaneRawMatrix `yaml:"matrix"`
}

type workflowLaneRawMatrix struct {
	Include []map[string]any `yaml:"include"`
}

var workflowLaneRequiredLabelRE = regexp.MustCompile(`contains\(\s*github\.event\.pull_request\.labels\.\*\.name\s*,\s*'([^']+)'\s*\)`)

func LoadJSONFile(path string, target any) error {
	return readJSONFile(path, target)
}

func GenerateWorkflowLaneManifest(rootDir, policyPath, outputPath string) error {
	manifest, err := buildWorkflowLaneManifest(rootDir, policyPath)
	if err != nil {
		return err
	}
	return writeJSONFile(outputPath, manifest)
}

func VerifyWorkflowLaneManifest(rootDir, policyPath, manifestPath string) error {
	var committed WorkflowLaneManifest
	if err := readJSONFile(manifestPath, &committed); err != nil {
		return err
	}
	generated, err := buildWorkflowLaneManifest(rootDir, policyPath)
	if err != nil {
		return err
	}
	if !slices.EqualFunc(committed.Lanes, generated.Lanes, func(a, b WorkflowLaneManifestEntry) bool {
		return a.ID == b.ID &&
			a.WorkflowPath == b.WorkflowPath &&
			a.WorkflowName == b.WorkflowName &&
			a.JobID == b.JobID &&
			a.JobName == b.JobName &&
			slices.Equal(a.WorkflowEvents, b.WorkflowEvents) &&
			slices.Equal(a.RequiredLabels, b.RequiredLabels) &&
			slices.Equal(a.Profiles, b.Profiles) &&
			a.Authority == b.Authority &&
			a.LocalMode == b.LocalMode &&
			a.LocalScript == b.LocalScript &&
			a.LocalOrder == b.LocalOrder &&
			a.GitHubOnlyReason == b.GitHubOnlyReason &&
			workflowLaneStringMapEqual(a.Matrix, b.Matrix) &&
			workflowLanePathGlobsEqual(a.WorkflowPathGlobs, b.WorkflowPathGlobs)
	}) || committed.Version != generated.Version || committed.Policy != generated.Policy {
		return fmt.Errorf("%s is out of date; regenerate it from %s", manifestPath, policyPath)
	}
	return nil
}

func PlanWorkflowLanes(manifestPath string, cfg WorkflowLanePlannerConfig) (WorkflowLanePlan, error) {
	var manifest WorkflowLaneManifest
	if err := readJSONFile(manifestPath, &manifest); err != nil {
		return WorkflowLanePlan{}, err
	}
	if cfg.Profile == "" {
		return WorkflowLanePlan{}, errors.New("workflow lane planner requires a profile")
	}

	labels := uniqueSortedStrings(cfg.Labels)
	changedFiles := uniqueSortedStrings(cfg.ChangedFiles)
	plan := WorkflowLanePlan{
		Version:      1,
		Profile:      cfg.Profile,
		Event:        cfg.Event,
		BaseBranch:   cfg.BaseBranch,
		Labels:       labels,
		ChangedFiles: changedFiles,
		Lanes:        make([]WorkflowLanePlanEntry, 0, len(manifest.Lanes)),
	}

	for _, lane := range manifest.Lanes {
		entry := WorkflowLanePlanEntry{
			ID:               lane.ID,
			WorkflowPath:     lane.WorkflowPath,
			JobName:          lane.JobName,
			Matrix:           nilIfEmptyStringMap(lane.Matrix),
			Profiles:         append([]string{}, lane.Profiles...),
			Authority:        lane.Authority,
			LocalMode:        lane.LocalMode,
			LocalScript:      lane.LocalScript,
			LocalOrder:       lane.LocalOrder,
			GitHubOnlyReason: lane.GitHubOnlyReason,
		}

		if !slices.Contains(lane.Profiles, cfg.Profile) {
			entry.Status = "skipped"
			entry.Reason = "not-in-profile"
			plan.Lanes = append(plan.Lanes, entry)
			continue
		}

		switch cfg.Profile {
		case "repo-core", "release-preflight":
			// Local profiles are explicit and do not attempt to mirror GitHub
			// event routing exactly.
		case "pr-parity":
			if cfg.Event == "" {
				return WorkflowLanePlan{}, errors.New("pr-parity planning requires an event")
			}
			if !slices.Contains(lane.WorkflowEvents, cfg.Event) {
				entry.Status = "skipped"
				entry.Reason = "event-not-selected"
				plan.Lanes = append(plan.Lanes, entry)
				continue
			}
			if cfg.Event == "pull_request" && len(lane.RequiredLabels) > 0 {
				missingLabel := ""
				for _, required := range lane.RequiredLabels {
					if !slices.Contains(labels, required) {
						missingLabel = required
						break
					}
				}
				if missingLabel != "" {
					entry.Status = "skipped"
					entry.Reason = fmt.Sprintf("missing-label:%s", missingLabel)
					plan.Lanes = append(plan.Lanes, entry)
					continue
				}
			}
			if len(changedFiles) > 0 && workflowLaneHasPathGlobs(lane.WorkflowPathGlobs, cfg.Event) {
				if !workflowLaneMatchesAnyPath(changedFiles, lane.WorkflowPathGlobs[cfg.Event]) {
					entry.Status = "skipped"
					entry.Reason = "path-filter-not-selected"
					plan.Lanes = append(plan.Lanes, entry)
					continue
				}
			}
		default:
			return WorkflowLanePlan{}, fmt.Errorf("unsupported workflow lane planner profile: %s", cfg.Profile)
		}

		if lane.LocalMode == "none" {
			entry.Status = "github-only"
			entry.Reason = "not-mirrored-locally"
			plan.Lanes = append(plan.Lanes, entry)
			continue
		}
		if lane.LocalMode == "partial" {
			if ok, reason := workflowLaneAvailableLocally(lane); !ok {
				entry.Status = "github-only"
				entry.Reason = reason
				plan.Lanes = append(plan.Lanes, entry)
				continue
			}
		}
		entry.Status = "local"
		plan.Lanes = append(plan.Lanes, entry)
	}

	return plan, nil
}

func buildWorkflowLaneManifest(rootDir, policyPath string) (WorkflowLaneManifest, error) {
	policy, err := loadWorkflowLanePolicy(policyPath)
	if err != nil {
		return WorkflowLaneManifest{}, err
	}
	policyRelPath, err := filepath.Rel(rootDir, policyPath)
	if err != nil {
		return WorkflowLaneManifest{}, err
	}
	workflowPaths, err := filepath.Glob(filepath.Join(rootDir, ".github", "workflows", "*.yml"))
	if err != nil {
		return WorkflowLaneManifest{}, err
	}
	sort.Strings(workflowPaths)

	derived := make([]WorkflowLaneManifestEntry, 0)
	seenPolicy := map[string]struct{}{}
	for _, workflowPath := range workflowPaths {
		entries, expandErr := expandWorkflowLaneManifestEntries(rootDir, workflowPath, policy.Lanes)
		if expandErr != nil {
			return WorkflowLaneManifest{}, expandErr
		}
		for _, entry := range entries {
			seenPolicy[entry.ID] = struct{}{}
			derived = append(derived, entry)
		}
	}
	for laneID := range policy.Lanes {
		if _, ok := seenPolicy[laneID]; !ok {
			return WorkflowLaneManifest{}, fmt.Errorf("%s contains an unknown workflow lane id: %s", policyPath, laneID)
		}
	}

	sort.Slice(derived, func(i, j int) bool {
		return derived[i].ID < derived[j].ID
	})
	return WorkflowLaneManifest{
		Version: 1,
		Policy:  filepath.ToSlash(policyRelPath),
		Lanes:   derived,
	}, nil
}

func loadWorkflowLanePolicy(path string) (workflowLanePolicyFile, error) {
	var policy workflowLanePolicyFile
	if err := readJSONFile(path, &policy); err != nil {
		return workflowLanePolicyFile{}, err
	}
	if policy.Version != 1 {
		return workflowLanePolicyFile{}, fmt.Errorf("%s must use version 1", path)
	}
	if len(policy.Lanes) == 0 {
		return workflowLanePolicyFile{}, fmt.Errorf("%s must define at least one workflow lane", path)
	}
	for laneID, entry := range policy.Lanes {
		if strings.TrimSpace(laneID) == "" {
			return workflowLanePolicyFile{}, fmt.Errorf("%s contains an empty workflow lane id", path)
		}
		switch entry.LocalMode {
		case "mirrored", "partial":
			if entry.LocalScript == "" {
				return workflowLanePolicyFile{}, fmt.Errorf("%s must define local_script for mirrored lane %s", path, laneID)
			}
			if len(entry.Profiles) == 0 {
				return workflowLanePolicyFile{}, fmt.Errorf("%s must define profiles for mirrored lane %s", path, laneID)
			}
		case "none":
			if entry.GitHubOnlyReason == "" {
				return workflowLanePolicyFile{}, fmt.Errorf("%s must define github_only_reason for non-local lane %s", path, laneID)
			}
			if entry.LocalScript != "" {
				return workflowLanePolicyFile{}, fmt.Errorf("%s must not define local_script for non-local lane %s", path, laneID)
			}
		default:
			return workflowLanePolicyFile{}, fmt.Errorf("%s lane %s has unsupported local_mode %q", path, laneID, entry.LocalMode)
		}
		switch entry.Authority {
		case "repo-core", "pr-parity", "release-only", "github-only":
		default:
			return workflowLanePolicyFile{}, fmt.Errorf("%s lane %s has unsupported authority %q", path, laneID, entry.Authority)
		}
		for _, profile := range entry.Profiles {
			switch profile {
			case "repo-core", "pr-parity", "release-preflight":
			default:
				return workflowLanePolicyFile{}, fmt.Errorf("%s lane %s has unsupported profile %q", path, laneID, profile)
			}
		}
	}
	return policy, nil
}

func expandWorkflowLaneManifestEntries(rootDir, workflowPath string, policy map[string]workflowLanePolicyEntry) ([]WorkflowLaneManifestEntry, error) {
	content, err := os.ReadFile(workflowPath)
	if err != nil {
		return nil, err
	}
	var document workflowLaneDocument
	if err := yaml.Unmarshal(content, &document); err != nil {
		return nil, fmt.Errorf("%s: parse workflow lanes: %w", workflowPath, err)
	}

	events, pathGlobs, err := workflowLaneEvents(document.On)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", workflowPath, err)
	}
	workflowRelPath, err := filepath.Rel(rootDir, workflowPath)
	if err != nil {
		return nil, err
	}
	workflowRelPath = filepath.ToSlash(workflowRelPath)

	jobIDs := make([]string, 0, len(document.Jobs))
	for jobID := range document.Jobs {
		jobIDs = append(jobIDs, jobID)
	}
	sort.Strings(jobIDs)

	var lanes []WorkflowLaneManifestEntry
	for _, jobID := range jobIDs {
		job := document.Jobs[jobID]
		expandedMatrix := workflowLaneExpandMatrix(job.Strategy.Matrix.Include)
		requiredLabels := workflowLaneRequiredLabels(job.If)
		for _, matrix := range expandedMatrix {
			laneID := workflowLaneID(filepath.Base(workflowPath), jobID, matrix)
			policyEntry, ok := policy[laneID]
			if !ok {
				return nil, fmt.Errorf("%s is missing workflow lane policy for %s", filepath.ToSlash(filepath.Join(rootDir, "policy", "workflow-lane-policy.json")), laneID)
			}
			lane := WorkflowLaneManifestEntry{
				ID:                laneID,
				WorkflowPath:      workflowRelPath,
				WorkflowName:      strings.TrimSpace(document.Name),
				JobID:             jobID,
				JobName:           workflowLaneRenderJobName(job.Name, matrix),
				Matrix:            nilIfEmptyStringMap(matrix),
				WorkflowEvents:    append([]string{}, events...),
				WorkflowPathGlobs: cloneStringSliceMap(pathGlobs),
				RequiredLabels:    append([]string{}, requiredLabels...),
				Profiles:          uniqueSortedStrings(policyEntry.Profiles),
				Authority:         policyEntry.Authority,
				LocalMode:         policyEntry.LocalMode,
				LocalScript:       policyEntry.LocalScript,
				LocalOrder:        policyEntry.LocalOrder,
				GitHubOnlyReason:  policyEntry.GitHubOnlyReason,
			}
			lanes = append(lanes, lane)
		}
	}
	return lanes, nil
}

func workflowLaneExpandMatrix(include []map[string]any) []map[string]string {
	if len(include) == 0 {
		return []map[string]string{{}}
	}
	expanded := make([]map[string]string, 0, len(include))
	for _, row := range include {
		entry := make(map[string]string, len(row))
		for key, value := range row {
			entry[key] = fmt.Sprint(value)
		}
		expanded = append(expanded, entry)
	}
	return expanded
}

func workflowLaneID(workflowBase, jobID string, matrix map[string]string) string {
	id := fmt.Sprintf("%s/%s", workflowBase, jobID)
	if len(matrix) == 0 {
		return id
	}
	keys := make([]string, 0, len(matrix))
	for key := range matrix {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%s", key, matrix[key]))
	}
	return fmt.Sprintf("%s[%s]", id, strings.Join(parts, ","))
}

func workflowLaneRenderJobName(name string, matrix map[string]string) string {
	rendered := name
	for key, value := range matrix {
		rendered = strings.ReplaceAll(rendered, fmt.Sprintf("${{ matrix.%s }}", key), value)
	}
	return strings.TrimSpace(rendered)
}

func workflowLaneEvents(raw any) ([]string, map[string][]string, error) {
	events := make([]string, 0)
	pathGlobs := map[string][]string{}
	switch typed := raw.(type) {
	case string:
		events = append(events, typed)
	case []any:
		for _, item := range typed {
			text, ok := item.(string)
			if !ok {
				return nil, nil, fmt.Errorf("workflow on sequence contains unsupported entry: %v", item)
			}
			events = append(events, text)
		}
	case map[string]any:
		for key, value := range typed {
			events = append(events, key)
			table, ok := value.(map[string]any)
			if !ok {
				continue
			}
			rawPaths, ok := table["paths"].([]any)
			if !ok {
				continue
			}
			globs := make([]string, 0, len(rawPaths))
			for _, rawPath := range rawPaths {
				pathText, ok := rawPath.(string)
				if !ok {
					return nil, nil, fmt.Errorf("workflow on.%s.paths contains unsupported entry: %v", key, rawPath)
				}
				globs = append(globs, pathText)
			}
			sort.Strings(globs)
			pathGlobs[key] = globs
		}
	case nil:
	default:
		return nil, nil, fmt.Errorf("workflow on stanza uses unsupported type %T", raw)
	}
	sort.Strings(events)
	return uniqueSortedStrings(events), pathGlobs, nil
}

func workflowLaneRequiredLabels(condition string) []string {
	if strings.TrimSpace(condition) == "" {
		return nil
	}
	matches := workflowLaneRequiredLabelRE.FindAllStringSubmatch(condition, -1)
	labels := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		labels = append(labels, match[1])
	}
	return uniqueSortedStrings(labels)
}

func workflowLaneHasPathGlobs(pathGlobs map[string][]string, event string) bool {
	return len(pathGlobs[event]) > 0
}

func workflowLaneMatchesAnyPath(changedFiles, globs []string) bool {
	for _, changedFile := range changedFiles {
		for _, glob := range globs {
			if workflowLaneMatchPath(glob, changedFile) {
				return true
			}
		}
	}
	return false
}

func workflowLaneMatchPath(glob, path string) bool {
	pattern := regexp.QuoteMeta(filepath.ToSlash(glob))
	pattern = strings.ReplaceAll(pattern, `\*\*`, `.*`)
	pattern = strings.ReplaceAll(pattern, `\*`, `[^/]*`)
	pattern = strings.ReplaceAll(pattern, `\?`, `[^/]`)
	re := regexp.MustCompile("^" + pattern + "$")
	return re.MatchString(filepath.ToSlash(path))
}

func workflowLaneAvailableLocally(entry WorkflowLaneManifestEntry) (bool, string) {
	if entry.LocalMode == "mirrored" {
		return true, ""
	}
	if entry.LocalMode != "partial" {
		return false, "not-mirrored-locally"
	}
	if platform := entry.Matrix["platform"]; platform != "" {
		switch runtime.GOARCH {
		case "amd64":
			if platform == "linux/amd64" {
				return true, ""
			}
		case "arm64":
			if platform == "linux/arm64" {
				return true, ""
			}
		}
		return false, fmt.Sprintf("platform-not-available-locally:%s", platform)
	}
	return true, ""
}

func uniqueSortedStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	if len(result) == 0 {
		return nil
	}
	return result
}

func nilIfEmptyStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	copyValues := make(map[string]string, len(values))
	for key, value := range values {
		copyValues[key] = value
	}
	return copyValues
}

func cloneStringSliceMap(values map[string][]string) map[string][]string {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string][]string, len(values))
	for key, rows := range values {
		cloned[key] = append([]string{}, rows...)
	}
	return cloned
}

func workflowLanePathGlobsEqual(a, b map[string][]string) bool {
	if len(a) != len(b) {
		return false
	}
	for key, aValues := range a {
		bValues, ok := b[key]
		if !ok {
			return false
		}
		if !slices.Equal(aValues, bValues) {
			return false
		}
	}
	return true
}

func workflowLaneStringMapEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if b[key] != value {
			return false
		}
	}
	return true
}
