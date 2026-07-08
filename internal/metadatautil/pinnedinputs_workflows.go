// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/omkhar/workcell/internal/tomlsubset"
	"gopkg.in/yaml.v3"
)

// This file isolates the GitHub Actions workflow pinned-input format: the
// helpers that parse workflow YAML, load the reviewed actions allowlist and
// tool pins, and enforce the pull_request_target safety contract. They share
// the metadatautil package (readText, MustString, requireStringSliceTable live
// in sibling files) so the exported API is unchanged; CheckPinnedInputs in
// pinnedinputs.go orchestrates them alongside the other formats.

var (
	workflowPermissionsRE = regexp.MustCompile(`(?m)^permissions:\s+\{\}$`)
	// Every `uses:` reference is extracted by parsing the workflow YAML (see
	// extractWorkflowUses), then validated here: actionRefPattern requires a
	// pinned owner/repo action; anything else (docker://, local ./, unpinned,
	// malformed) is rejected by default, so the scan is default-deny.
	actionRefPattern = regexp.MustCompile(`^([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+(?:/[A-Za-z0-9_.-]+)*)@([^\s#]+)$`)
	commitShaPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

type usesScanWorkflow struct {
	Jobs map[string]usesScanJob `yaml:"jobs"`
}

type usesScanJob struct {
	Uses  string         `yaml:"uses"`
	Steps []usesScanStep `yaml:"steps"`
}

type usesScanStep struct {
	Uses string `yaml:"uses"`
}

// extractWorkflowUses parses a workflow and returns every `uses:` reference
// (job-level reusable-workflow calls and step-level actions), in deterministic
// order. Parsing the YAML — rather than scanning raw lines — means quoted keys
// (`"uses":`), dash-less step keys, and odd spacing all resolve to the same
// `uses` field, so no textual form can slip an action past the allowlist.
func extractWorkflowUses(workflowText string) ([]string, error) {
	var doc usesScanWorkflow
	if err := yaml.Unmarshal([]byte(workflowText), &doc); err != nil {
		return nil, err
	}
	jobNames := make([]string, 0, len(doc.Jobs))
	for name := range doc.Jobs {
		jobNames = append(jobNames, name)
	}
	sort.Strings(jobNames)
	var refs []string
	for _, name := range jobNames {
		job := doc.Jobs[name]
		if strings.TrimSpace(job.Uses) != "" {
			refs = append(refs, job.Uses)
		}
		for _, step := range job.Steps {
			if strings.TrimSpace(step.Uses) != "" {
				refs = append(refs, step.Uses)
			}
		}
	}
	return refs, nil
}

// toolPins is the reviewed canonical set of CI/release tool pins that are
// otherwise duplicated inline across workflows.
type toolPins struct {
	Cosign            string
	Buildx            string
	Buildkit          string
	QEMU              string
	Syft              string
	ActionlintVersion string
	ActionlintSHA256  string
	ZizmorVersion     string
	ZizmorSHA256      string
}

// loadToolPins reads policy/tool-pins.toml and requires every pin to be present.
func loadToolPins(path string) (toolPins, error) {
	text, err := readText(path)
	if err != nil {
		return toolPins{}, err
	}
	return parseToolPins(text, path)
}

// parseToolPins parses the [tool_pins] table out of already-read TOML text and
// requires every pin to be present. Split from loadToolPins so the parse path
// (which handles attacker-influenceable policy text) can be fuzzed directly.
func parseToolPins(text, path string) (toolPins, error) {
	root, err := tomlsubset.Parse(text, path)
	if err != nil {
		return toolPins{}, err
	}
	table, ok := root["tool_pins"].(map[string]any)
	if !ok {
		return toolPins{}, fmt.Errorf("%s must define a [tool_pins] table", path)
	}
	var pins toolPins
	fields := []struct {
		key string
		dst *string
	}{
		{"cosign", &pins.Cosign},
		{"buildx", &pins.Buildx},
		{"buildkit", &pins.Buildkit},
		{"qemu", &pins.QEMU},
		{"syft", &pins.Syft},
		{"actionlint_version", &pins.ActionlintVersion},
		{"actionlint_sha256", &pins.ActionlintSHA256},
		{"zizmor_version", &pins.ZizmorVersion},
		{"zizmor_sha256", &pins.ZizmorSHA256},
	}
	known := make(map[string]bool, len(fields))
	for _, field := range fields {
		known[field.key] = true
		value, ok := MustString(table[field.key])
		if !ok || strings.TrimSpace(value) == "" {
			return toolPins{}, fmt.Errorf("%s [tool_pins] must set a non-empty %s", path, field.key)
		}
		*field.dst = value
	}
	for key := range table {
		if !known[key] {
			return toolPins{}, fmt.Errorf("%s [tool_pins] has an unknown key %q", path, key)
		}
	}
	return pins, nil
}

// loadAllowedActions reads the reviewed GitHub Actions allowlist as a set of
// permitted owner/repo identities.
func loadAllowedActions(path string) (map[string]bool, error) {
	text, err := readText(path)
	if err != nil {
		return nil, err
	}
	root, err := tomlsubset.Parse(text, path)
	if err != nil {
		return nil, err
	}
	entries, err := requireStringSliceTable(root, "actions", "allowed", path)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("%s must list at least one allowed action", path)
	}
	allowed := make(map[string]bool, len(entries))
	for _, entry := range entries {
		allowed[entry] = true
	}
	return allowed, nil
}

func IsSafePullRequestTargetWorkflow(workflowText, workflowPath string) error {
	if filepath.Base(workflowPath) != "pr-base-policy.yml" {
		return fmt.Errorf("%s must not contain pull_request_target triggers", workflowPath)
	}
	if !strings.Contains(workflowText, "kusari-inspector suppress") {
		return fmt.Errorf("%s must document the reviewed Kusari suppression for pull_request_target", workflowPath)
	}
	root, err := parseWorkflowRoot(workflowText, workflowPath)
	if err != nil {
		return err
	}
	permissionsNodes := yamlMappingValues(root, "permissions")
	if len(permissionsNodes) != 1 || permissionsNodes[0].Kind != yaml.MappingNode || len(permissionsNodes[0].Content) != 0 {
		return fmt.Errorf("%s must keep top-level permissions: {}", workflowPath)
	}
	jobsNodes := yamlMappingValues(root, "jobs")
	if len(jobsNodes) != 1 || jobsNodes[0].Kind != yaml.MappingNode {
		return fmt.Errorf("%s must define pull_request_target jobs as a mapping", workflowPath)
	}
	for i := 1; i < len(jobsNodes[0].Content); i += 2 {
		job := jobsNodes[0].Content[i]
		if job.Kind != yaml.MappingNode {
			return fmt.Errorf("%s must define pull_request_target jobs as mapping nodes", workflowPath)
		}
		if len(yamlMappingValues(job, "permissions")) > 0 {
			return fmt.Errorf("%s must not grant job-level permissions under pull_request_target", workflowPath)
		}
		if len(yamlMappingValues(job, "uses")) > 0 {
			return fmt.Errorf("%s must not call reusable workflows under pull_request_target", workflowPath)
		}
		for _, steps := range yamlMappingValues(job, "steps") {
			if steps.Kind != yaml.SequenceNode {
				return fmt.Errorf("%s must define pull_request_target steps as a sequence", workflowPath)
			}
			for _, step := range steps.Content {
				if step.Kind != yaml.MappingNode {
					return fmt.Errorf("%s must define pull_request_target steps as mapping nodes", workflowPath)
				}
				for _, uses := range yamlMappingValues(step, "uses") {
					if strings.HasPrefix(yamlScalarValue(uses), "actions/checkout@") {
						return fmt.Errorf("%s must not checkout repository contents when using pull_request_target", workflowPath)
					}
					return fmt.Errorf("%s must not use external actions when using pull_request_target", workflowPath)
				}
			}
		}
	}
	return nil
}

func parseWorkflowRoot(workflowText, workflowPath string) (*yaml.Node, error) {
	var document yaml.Node
	if err := yaml.Unmarshal([]byte(workflowText), &document); err != nil {
		return nil, fmt.Errorf("%s: parse workflow YAML: %w", workflowPath, err)
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%s must be a YAML mapping", workflowPath)
	}
	return document.Content[0], nil
}

func yamlMappingValues(mapping *yaml.Node, key string) []*yaml.Node {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	values := []*yaml.Node{}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Kind == yaml.ScalarNode && mapping.Content[i].Value == key {
			values = append(values, mapping.Content[i+1])
		}
	}
	return values
}

func yamlScalarValue(node *yaml.Node) string {
	if node == nil || node.Kind != yaml.ScalarNode {
		return ""
	}
	return strings.TrimSpace(node.Value)
}

func mustGlob(pattern string) []string {
	matches, err := filepath.Glob(pattern)
	if err != nil {
		panic(err)
	}
	return matches
}

// workflowYAMLFiles returns every workflow file, covering both `.yml` and
// `.yaml` (GitHub executes either), so a `.yaml` workflow cannot dodge the scan.
func workflowYAMLFiles(dir string) []string {
	return append(mustGlob(filepath.Join(dir, "*.yml")), mustGlob(filepath.Join(dir, "*.yaml"))...)
}
