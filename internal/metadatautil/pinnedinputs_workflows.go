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

// toolPins is the reviewed canonical set of CI/release tool pins. The values
// live only in policy/tool-pins.toml; the workflows read them at run time
// through scripts/ci/load-tool-pins.sh (see pinnedinputs_toolpins.go).
type toolPins struct {
	Cosign               string
	Buildx               string
	Buildkit             string
	QEMU                 string
	Syft                 string
	ActionlintVersion    string
	ActionlintSHA256     string
	ZizmorVersion        string
	ZizmorSHA256         string
	OrasVersion          string
	OrasLinuxAMD64SHA256 string
}

// loadToolPins reads policy/tool-pins.toml and requires every pin to be present.
func loadToolPins(path string) (toolPins, error) {
	text, err := readText(path)
	if err != nil {
		return toolPins{}, err
	}
	return parseToolPins(text, path)
}

// parseToolPins parses the [tool_pins] table out of already-read TOML text,
// requires every key of the fixed table, and requires each value to match its
// key's format. Split from loadToolPins so the parse path (which handles
// attacker-influenceable policy text) can be fuzzed directly.
func parseToolPins(text, path string) (toolPins, error) {
	root, err := tomlsubset.Parse(text, path)
	if err != nil {
		return toolPins{}, err
	}
	for key := range root {
		if key != "tool_pins" {
			return toolPins{}, fmt.Errorf("%s must define only the [tool_pins] table, found %q", path, key)
		}
	}
	table, ok := root["tool_pins"].(map[string]any)
	if !ok {
		return toolPins{}, fmt.Errorf("%s must define a [tool_pins] table", path)
	}
	var pins toolPins
	fields := pins.fields()
	known := make(map[string]bool, len(fields))
	for _, field := range fields {
		known[field.key] = true
		value, ok := MustString(table[field.key])
		if !ok || strings.TrimSpace(value) == "" {
			return toolPins{}, fmt.Errorf("%s [tool_pins] must set a non-empty %s", path, field.key)
		}
		if !field.format.MatchString(value) {
			return toolPins{}, fmt.Errorf("%s [tool_pins] %s must match %s, found %q", path, field.key, field.format, value)
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
	return validatePRBasePolicyLockdown(root, workflowPath)
}

// The pull_request_target exception is trusted only for the reviewed,
// metadata-only shape of pr-base-policy.yml. The checks above reject the
// obvious escapes (checkout, actions, reusable workflows, permissions); the
// lockdown below is default-deny for everything else, so a later edit cannot
// read secrets, change the shell, or fetch pull request code through run:.
// Every check decodes structure (YAML nodes, a closed expression tokenizer, a
// closed run-line grammar) instead of matching text, so anything it does not
// recognize is refused.
var (
	prBaseTopLevelKeys = map[string]bool{"name": true, "on": true, "permissions": true, "defaults": true, "concurrency": true, "jobs": true}
	prBaseJobKeys      = map[string]bool{"name": true, "runs-on": true, "timeout-minutes": true, "env": true, "steps": true}
	prBaseStepKeys     = map[string]bool{"name": true, "if": true, "run": true}
	prBaseRunners      = map[string]bool{"ubuntu-latest": true, "ubuntu-24.04": true, "ubuntu-22.04": true}
	// The only contexts an expression may read. env.NAME is also allowed: job
	// env values are expressions too, so they can only carry these contexts.
	prBaseAllowedContexts = map[string]bool{
		"github.workflow":                    true,
		"github.event.pull_request.number":   true,
		"github.event.pull_request.base.ref": true,
		"github.event.pull_request.draft":    true,
	}
	prBaseDefaultShell = "bash --noprofile --norc -euo pipefail {0}"
)

func validatePRBasePolicyLockdown(root *yaml.Node, workflowPath string) error {
	if err := rejectYAMLAnchors(root, workflowPath); err != nil {
		return err
	}
	if err := requireOnlyKeys(root, prBaseTopLevelKeys, workflowPath, "top level"); err != nil {
		return err
	}
	onNodes := yamlMappingValues(root, "on")
	if len(onNodes) != 1 || onNodes[0].Kind != yaml.MappingNode || len(onNodes[0].Content) != 2 || onNodes[0].Content[0].Value != "pull_request_target" {
		return fmt.Errorf("%s must trigger on pull_request_target only", workflowPath)
	}
	if defaults := yamlMappingValues(root, "defaults"); len(defaults) > 0 {
		run := yamlMappingValues(defaults[0], "run")
		if defaults[0].Kind != yaml.MappingNode || len(defaults[0].Content) != 2 || len(run) != 1 ||
			run[0].Kind != yaml.MappingNode || len(run[0].Content) != 2 ||
			yamlScalarValue(firstOrNil(yamlMappingValues(run[0], "shell"))) != prBaseDefaultShell {
			return fmt.Errorf("%s must keep defaults.run.shell set to %q under pull_request_target", workflowPath, prBaseDefaultShell)
		}
	}
	jobs := yamlMappingValues(root, "jobs")[0]
	for i := 0; i+1 < len(jobs.Content); i += 2 {
		job := jobs.Content[i+1]
		if err := requireOnlyKeys(job, prBaseJobKeys, workflowPath, "job "+jobs.Content[i].Value); err != nil {
			return err
		}
		runsOn := yamlMappingValues(job, "runs-on")
		if len(runsOn) != 1 || runsOn[0].Kind != yaml.ScalarNode || !prBaseRunners[runsOn[0].Value] {
			return fmt.Errorf("%s must run pull_request_target jobs on a GitHub-hosted ubuntu runner", workflowPath)
		}
		for _, env := range yamlMappingValues(job, "env") {
			if env.Kind != yaml.MappingNode {
				return fmt.Errorf("%s must define pull_request_target job env as a mapping", workflowPath)
			}
			for j := 1; j < len(env.Content); j += 2 {
				if env.Content[j].Kind != yaml.ScalarNode {
					return fmt.Errorf("%s must keep pull_request_target env values scalar", workflowPath)
				}
			}
		}
		for _, steps := range yamlMappingValues(job, "steps") {
			for _, step := range steps.Content {
				if err := requireOnlyKeys(step, prBaseStepKeys, workflowPath, "step"); err != nil {
					return err
				}
				for _, run := range yamlMappingValues(step, "run") {
					if run.Kind != yaml.ScalarNode {
						return fmt.Errorf("%s must keep pull_request_target run scripts scalar", workflowPath)
					}
					for _, line := range scriptLines(run.Value) {
						if !isPRBaseRunLine(line) {
							return fmt.Errorf("%s run steps under pull_request_target may only echo fixed text or exit; found %q", workflowPath, line)
						}
					}
				}
				for _, cond := range yamlMappingValues(step, "if") {
					if err := checkPRBaseCondition(yamlScalarValue(cond), workflowPath); err != nil {
						return err
					}
				}
			}
		}
	}
	return checkPRBaseExpressions(root, workflowPath)
}

// workflowDeclaresTrigger decodes the workflow (aliases, merge keys and
// escaped scalars resolved) and reports whether its on: names trigger, so an
// escaped spelling of the trigger cannot skip the pull_request_target checks.
func workflowDeclaresTrigger(workflowText, workflowPath, trigger string) (bool, error) {
	var workflow map[string]any
	if err := yaml.Unmarshal([]byte(workflowText), &workflow); err != nil {
		return false, fmt.Errorf("%s: parse workflow YAML: %w", workflowPath, err)
	}
	switch on := workflow["on"].(type) {
	case string:
		return on == trigger, nil
	case []any:
		for _, name := range on {
			if name == trigger {
				return true, nil
			}
		}
	case map[string]any:
		_, ok := on[trigger]
		return ok, nil
	}
	return false, nil
}

func firstOrNil(nodes []*yaml.Node) *yaml.Node {
	if len(nodes) == 0 {
		return nil
	}
	return nodes[0]
}

func rejectYAMLAnchors(node *yaml.Node, workflowPath string) error {
	if node.Kind == yaml.AliasNode || node.Anchor != "" {
		return fmt.Errorf("%s must not use YAML anchors or aliases under pull_request_target", workflowPath)
	}
	for _, child := range node.Content {
		if err := rejectYAMLAnchors(child, workflowPath); err != nil {
			return err
		}
	}
	return nil
}

func requireOnlyKeys(mapping *yaml.Node, allowed map[string]bool, workflowPath, where string) error {
	if mapping.Kind != yaml.MappingNode {
		return fmt.Errorf("%s must define the pull_request_target %s as a mapping", workflowPath, where)
	}
	seen := map[string]bool{}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		key := mapping.Content[i]
		if key.Kind != yaml.ScalarNode || !allowed[key.Value] {
			return fmt.Errorf("%s must not set %q in the pull_request_target %s", workflowPath, key.Value, where)
		}
		if seen[key.Value] {
			return fmt.Errorf("%s must not repeat %q in the pull_request_target %s", workflowPath, key.Value, where)
		}
		seen[key.Value] = true
	}
	return nil
}

// scriptLines returns the non-blank lines of a run script, trimmed of the
// spaces and tabs around them.
func scriptLines(script string) []string {
	var lines []string
	start := 0
	for i := 0; i <= len(script); i++ {
		if i == len(script) || script[i] == '\n' {
			line := trimBlanks(script[start:i])
			if line != "" {
				lines = append(lines, line)
			}
			start = i + 1
		}
	}
	return lines
}

func trimBlanks(text string) string {
	start, end := 0, len(text)
	for start < end && (text[start] == ' ' || text[start] == '\t' || text[start] == '\r') {
		start++
	}
	for end > start && (text[end-1] == ' ' || text[end-1] == '\t' || text[end-1] == '\r') {
		end--
	}
	return text[start:end]
}

// isPRBaseRunLine accepts exactly the closed run-line grammar the reviewed
// workflow uses: true, exit DIGITS, or echo "TEXT" with an optional >&2, where
// TEXT holds no character the shell would expand or end the word on.
func isPRBaseRunLine(line string) bool {
	if line == "true" {
		return true
	}
	if len(line) > len("exit ") && line[:len("exit ")] == "exit " {
		for i := len("exit "); i < len(line); i++ {
			if line[i] < '0' || line[i] > '9' {
				return false
			}
		}
		return true
	}
	const opener = "echo \""
	if len(line) < len(opener)+1 || line[:len(opener)] != opener {
		return false
	}
	i := len(opener)
	for ; i < len(line) && line[i] != '"'; i++ {
		switch line[i] {
		case '$', '`', '\\', '!':
			return false
		}
	}
	if i == len(line) {
		return false
	}
	rest := line[i+1:]
	return rest == "" || rest == " >&2"
}

// checkPRBaseCondition checks an if: value, which GitHub reads as an
// expression whether or not it is wrapped in ${{ }}. A wrapped value must be
// exactly one expression; checkPRBaseExpressions checks its body.
func checkPRBaseCondition(condition, workflowPath string) error {
	if len(condition) < 3 || condition[:3] != "${{" {
		return checkPRBaseExpression(condition, workflowPath)
	}
	expressions, ok := workflowExpressions(condition)
	if !ok || len(expressions) != 1 || "${{"+expressions[0]+"}}" != condition {
		return fmt.Errorf("%s must write an if: condition as one expression under pull_request_target", workflowPath)
	}
	return nil
}

// checkPRBaseExpressions walks every scalar, keys included, and checks each
// ${{ }} expression it holds.
func checkPRBaseExpressions(node *yaml.Node, workflowPath string) error {
	if node.Kind == yaml.ScalarNode {
		expressions, ok := workflowExpressions(node.Value)
		if !ok {
			return fmt.Errorf("%s has an unterminated expression under pull_request_target", workflowPath)
		}
		for _, expression := range expressions {
			if err := checkPRBaseExpression(expression, workflowPath); err != nil {
				return err
			}
		}
	}
	for _, child := range node.Content {
		if err := checkPRBaseExpressions(child, workflowPath); err != nil {
			return err
		}
	}
	return nil
}

// workflowExpressions returns the body of each ${{ }} in text. A '...' string
// literal inside an expression may hold }}. It reports false when an
// expression or a string literal is never closed.
func workflowExpressions(text string) ([]string, bool) {
	var expressions []string
	for i := 0; i+2 < len(text); i++ {
		if text[i] != '$' || text[i+1] != '{' || text[i+2] != '{' {
			continue
		}
		start := i + 3
		closed := false
		for j := start; j < len(text); j++ {
			if text[j] == '\'' {
				for j++; j < len(text) && text[j] != '\''; j++ {
				}
				continue
			}
			if text[j] == '}' && j+1 < len(text) && text[j+1] == '}' {
				expressions = append(expressions, text[start:j])
				i = j + 1
				closed = true
				break
			}
		}
		if !closed {
			return nil, false
		}
	}
	return expressions, true
}

// checkPRBaseExpression tokenizes a GitHub Actions expression and refuses any
// property path that is not an allowed context, any index or function call,
// and any character outside the operators the reviewed workflow needs.
func checkPRBaseExpression(expression, workflowPath string) error {
	for i := 0; i < len(expression); {
		c := expression[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '\'':
			j := i + 1
			for ; j < len(expression); j++ {
				if expression[j] == '\'' {
					if j+1 < len(expression) && expression[j+1] == '\'' {
						j++
						continue
					}
					break
				}
			}
			if j >= len(expression) {
				return fmt.Errorf("%s has an unterminated string in an expression under pull_request_target", workflowPath)
			}
			i = j + 1
		case c == '=' || c == '!' || c == '&' || c == '|' || c == '<' || c == '>' || c == '(' || c == ')':
			i++
		case isExpressionNameByte(c):
			j := i
			for j < len(expression) && (isExpressionNameByte(expression[j]) || expression[j] == '.') {
				j++
			}
			path := expression[i:j]
			if j < len(expression) && (expression[j] == '[' || expression[j] == '(') {
				return fmt.Errorf("%s must not index or call %q in an expression under pull_request_target", workflowPath, path)
			}
			if !isPRBaseExpressionPath(path) {
				return fmt.Errorf("%s must not read %q under pull_request_target", workflowPath, path)
			}
			i = j
		default:
			return fmt.Errorf("%s has %q in an expression under pull_request_target", workflowPath, string(c))
		}
	}
	return nil
}

func isExpressionNameByte(c byte) bool {
	return c == '_' || c == '-' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

func isPRBaseExpressionPath(path string) bool {
	if prBaseAllowedContexts[path] || path == "true" || path == "false" || path == "null" {
		return true
	}
	isNumber := path != ""
	for i := 0; i < len(path); i++ {
		if (path[i] < '0' || path[i] > '9') && path[i] != '.' {
			isNumber = false
		}
	}
	if isNumber {
		return true
	}
	const envPrefix = "env."
	if len(path) <= len(envPrefix) || path[:len(envPrefix)] != envPrefix {
		return false
	}
	for i := len(envPrefix); i < len(path); i++ {
		c := path[i]
		if c != '_' && (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			return false
		}
	}
	return true
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
