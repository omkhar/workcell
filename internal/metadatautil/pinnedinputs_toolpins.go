// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/omkhar/workcell/internal/rootio"
)

// policy/tool-pins.toml is the single source of the CI and release tool pins.
// No workflow holds a copy of a pin value. Each job that uses a pin runs
// scripts/ci/load-tool-pins.sh as a step with id `pins`, after its checkout and
// before the first step that uses a pin, and binds each value explicitly with
// `${{ steps.pins.outputs.<key> }}` in a `with:` input or a step `env:` entry.
// A toolchain bump then changes the policy file and never a workflow file.
//
// The checks below hold that contract:
//   - the Go parse and the bash loader read the same pins from the policy;
//   - no workflow holds a pin value, a BuildKit or binfmt image reference, or
//     an unbound cosign-release, setup-buildx version or syft-version input;
//   - every pin output a workflow names is in the fixed key table, and is
//     bound only in a step's with: or env:, never in run:, if: or a job field;
//   - the pins step comes after a checkout and before every consumer, runs
//     only the loader, and has no if: or continue-on-error, so a failed load
//     stops the job;
//   - no consumer runs under always(), cancelled(), failure() or a negated
//     success(), which would run it after a failed load with empty pins;
//   - a job with a pins step runs it under the workflow's reviewed bash
//     default: no job defaults or container, and no other workflow shell.
//
// TEMPORARY bridge: until the workflows move to the pins step they hold their
// pins inline, so this contract applies once any workflow runs or names a pins
// step (toolPinBridgeInline). The cleanup change that follows the workflow
// switch (PR 4 of the tool-pin split) removes the bridge.

const (
	toolPinsStepID    = "pins"
	toolPinsStepRun   = "./scripts/ci/load-tool-pins.sh"
	toolPinsLoaderRel = "scripts/ci/load-tool-pins.sh"
)

var (
	// Version parts have no leading zero, so one version has one spelling.
	toolPinReleaseTagPattern = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
	toolPinBareSemverPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
	toolPinSHA256Pattern     = regexp.MustCompile(`^[0-9a-f]{64}$`)
	// Each image pin must name its one reviewed repository on Docker Hub, with
	// no registry host or other prefix: repository:tag@sha256:digest. The loader
	// applies the same per-key patterns.
	toolPinBuildkitPattern = toolPinImagePattern(toolPinBuildkitRepository)
	toolPinQEMUPattern     = toolPinImagePattern(toolPinQEMURepository)

	toolPinOutputRefPattern = regexp.MustCompile(`steps\.pins\.outputs\.([A-Za-z0-9_-]+)`)
	toolPinAnyRefPattern    = regexp.MustCompile(`(?i)steps\s*(\.\s*pins\b|\[\s*['"]pins['"]\s*\]|\.\s*\*)`)
	// A status function other than a plain success() replaces the implicit
	// success() GitHub adds to an if:, so the step can run after a failed load.
	toolPinBypassIfPattern = regexp.MustCompile(`(?i)always\s*\(|cancelled\s*\(|failure\s*\(|!\s*success\s*\(|success\s*\(\s*\)\s*(==|!=)`)
	// Image repositories whose references are tool pins. A workflow must bind
	// them from the pins step, never name them.
	toolPinImageRepositories = []string{toolPinBuildkitRepository, toolPinQEMURepository}
)

const (
	toolPinBuildkitRepository = "moby/buildkit"
	toolPinQEMURepository     = "tonistiigi/binfmt"
)

// toolPinImagePattern is the format of an image pin for one repository.
func toolPinImagePattern(repository string) *regexp.Regexp {
	return regexp.MustCompile(`^` + regexp.QuoteMeta(repository) + `:[A-Za-z0-9_][A-Za-z0-9_.-]*@sha256:[0-9a-f]{64}$`)
}

// toolPinField is one row of the fixed key table: the policy key, the step env
// name a consumer binds it to, the value format, and the parsed value.
type toolPinField struct {
	key, envName string
	format       *regexp.Regexp
	dst          *string
}

// fields is the fixed key table, in the loader's output order.
func (pins *toolPins) fields() []toolPinField {
	return []toolPinField{
		{"cosign", "WORKCELL_COSIGN_VERSION", toolPinReleaseTagPattern, &pins.Cosign},
		{"buildx", "WORKCELL_BUILDX_VERSION", toolPinReleaseTagPattern, &pins.Buildx},
		{"buildkit", "WORKCELL_BUILDKIT_IMAGE", toolPinBuildkitPattern, &pins.Buildkit},
		{"qemu", "WORKCELL_QEMU_IMAGE", toolPinQEMUPattern, &pins.QEMU},
		{"syft", "WORKCELL_SYFT_VERSION", toolPinReleaseTagPattern, &pins.Syft},
		{"actionlint_version", "ACTIONLINT_VERSION", toolPinBareSemverPattern, &pins.ActionlintVersion},
		{"actionlint_sha256", "ACTIONLINT_SHA256", toolPinSHA256Pattern, &pins.ActionlintSHA256},
		{"zizmor_version", "ZIZMOR_VERSION", toolPinBareSemverPattern, &pins.ZizmorVersion},
		{"zizmor_sha256", "ZIZMOR_SHA256", toolPinSHA256Pattern, &pins.ZizmorSHA256},
		{"oras_version", "WORKCELL_ORAS_VERSION", toolPinBareSemverPattern, &pins.OrasVersion},
		{"oras_linux_amd64_sha256", "WORKCELL_ORAS_LINUX_AMD64_SHA256", toolPinSHA256Pattern, &pins.OrasLinuxAMD64SHA256},
	}
}

func toolPinBinding(key string) string {
	return "${{ steps.pins.outputs." + key + " }}"
}

// loaderOutput renders the pins exactly as scripts/ci/load-tool-pins.sh writes
// them to GITHUB_OUTPUT.
func (pins toolPins) loaderOutput() string {
	var output strings.Builder
	for _, field := range pins.fields() {
		fmt.Fprintf(&output, "%s=%s\n", field.key, *field.dst)
	}
	return output.String()
}

// validateToolPinLoaderAgreement runs the bash loader that the workflows run
// against the same policy file and requires its output to equal the Go parse
// byte for byte. A policy line one parser reads and the other does not, or a
// value the two read differently, fails the check.
func (check *pinnedInputsCheck) validateToolPinLoaderAgreement() error {
	loader := filepath.Join(check.repoRoot, filepath.FromSlash(toolPinsLoaderRel))
	policy := filepath.Join(check.repoRoot, "policy", "tool-pins.toml")
	got, err := runToolPinLoader(loader, policy)
	if err != nil {
		return err
	}
	if want := check.pins.loaderOutput(); got != want {
		return fmt.Errorf("%s output does not match the Go parse of policy/tool-pins.toml\nloader: %q\nparse:  %q", toolPinsLoaderRel, got, want)
	}
	return nil
}

func runToolPinLoader(loader, policy string) (string, error) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		return "", fmt.Errorf("bash is required to run %s: %w", toolPinsLoaderRel, err)
	}
	outputDir, err := os.MkdirTemp("", "workcell-tool-pins-")
	if err != nil {
		return "", err
	}
	scratchParent, cleaned, err := rootio.OpenParentDirectoryNoFollow(outputDir)
	if err != nil {
		return "", err
	}
	defer scratchParent.Close()
	defer rootio.RemoveAllAtNoFollow(scratchParent, filepath.Base(cleaned)) //nolint:errcheck // scratch directory
	scratch, err := rootio.OpenDirAtNoFollow(scratchParent, filepath.Base(cleaned))
	if err != nil {
		return "", err
	}
	defer scratch.Close()
	const outputName = "github-output"
	cmd := exec.Command(bash, "--noprofile", "--norc", loader, policy)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "LC_ALL=C", "GITHUB_OUTPUT=" + filepath.Join(cleaned, outputName)}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s rejected policy/tool-pins.toml: %w: %s", toolPinsLoaderRel, err, strings.TrimSpace(stderr.String()))
	}
	output, err := rootio.ReadFileAtNoFollow(scratch, outputName, toolPinsLoaderRel+" output", 1<<16)
	if err != nil {
		return "", err
	}
	return string(output), nil
}

// validateWorkflowToolPinBindings applies the tool-pin contract to every
// workflow file.
func (check *pinnedInputsCheck) validateWorkflowToolPinBindings() error {
	paths := workflowYAMLFiles(check.cfg.WorkflowsDir)
	if len(paths) == 0 {
		return fmt.Errorf("no workflow files found in %s", check.cfg.WorkflowsDir)
	}
	if inline, err := check.toolPinBridgeInline(); err != nil || inline {
		return err
	}
	for _, workflowPath := range paths {
		text, err := readText(workflowPath)
		if err != nil {
			return err
		}
		path := ".github/workflows/" + filepath.Base(workflowPath)
		if err := validateWorkflowToolPinBindingsText(text, path, check.pins); err != nil {
			return err
		}
	}
	return nil
}

// toolPinBridgeStepPattern finds a step id of pins in raw workflow text.
var toolPinBridgeStepPattern = regexp.MustCompile(`(?mi)^\s*(-\s+)?id:\s*["']?pins["']?\s*$`)

// toolPinBridgeInline reports whether the workflows still hold their tool pins
// inline: no workflow runs the loader, has a pins step, or names one.
//
// TEMPORARY bridge for the move of the tool pins out of the workflows. While
// it holds, the inline-value checks run exactly as before and the binding
// checks wait. Once any workflow uses the pins step, every workflow gets the
// binding checks, which reject any inline pin left in another workflow, and
// the inline-value checks stop. It reads raw text, so a comment that names the
// pins step also ends the bridge, which only turns stricter checks on. The
// cleanup change (PR 4 of the tool-pin split) removes it.
func (check *pinnedInputsCheck) toolPinBridgeInline() (bool, error) {
	for _, workflowPath := range workflowYAMLFiles(check.cfg.WorkflowsDir) {
		text, err := readText(workflowPath)
		if err != nil {
			return false, err
		}
		if strings.Contains(text, toolPinsLoaderRel) || toolPinAnyRefPattern.MatchString(text) || toolPinBridgeStepPattern.MatchString(text) {
			return false, nil
		}
	}
	return true, nil
}

// inlineToolPinCheck runs one of the inline-value checks only while the bridge
// holds (see toolPinBridgeInline). TEMPORARY: PR 4 of the tool-pin split
// removes it with those checks.
func (check *pinnedInputsCheck) inlineToolPinCheck(validate func() error) func() error {
	return func() error {
		if inline, err := check.toolPinBridgeInline(); err != nil || !inline {
			return err
		}
		return validate()
	}
}

// validateWorkflowToolPinBindingsText checks one workflow against the tool-pin
// contract described at the top of this file.
func validateWorkflowToolPinBindingsText(workflowText, path string, pins toolPins) error {
	root, err := parseWorkflowRoot(workflowText, path)
	if err != nil {
		return err
	}
	if err := rejectInlineToolPins(root, path, pins); err != nil {
		return err
	}
	byEnv := map[string]string{}
	for _, field := range pins.fields() {
		byEnv[field.envName] = field.key
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		key, value := root.Content[i].Value, root.Content[i+1]
		if key == "jobs" {
			continue
		}
		if err := rejectToolPinRefs(value, path, "workflow "+key); err != nil {
			return err
		}
		if key == "env" {
			if err := rejectToolPinEnvNames(value, byEnv, path, "workflow env"); err != nil {
				return err
			}
		}
	}
	shellOK := toolPinWorkflowShellOK(root)
	pinsJobs := false
	for _, jobs := range yamlMappingValues(root, "jobs") {
		if jobs.Kind != yaml.MappingNode {
			return fmt.Errorf("%s must define jobs as a mapping", path)
		}
		for i := 0; i+1 < len(jobs.Content); i += 2 {
			hasPins, err := validateJobToolPinBindings(jobs.Content[i+1], path, jobs.Content[i].Value, byEnv, shellOK)
			if err != nil {
				return err
			}
			pinsJobs = pinsJobs || hasPins
		}
	}
	if pinsJobs {
		for _, env := range yamlMappingValues(root, "env") {
			if err := rejectToolPinLoaderEnv(env, path, "workflow env"); err != nil {
				return err
			}
		}
	}
	return nil
}

// rejectToolPinLoaderEnv keeps names that change how bash starts, where it
// finds its commands, or where the loader writes out of the env a pins step
// inherits. The step's shell reads BASH_ENV (and ENV) and imports BASH_FUNC_
// functions before the loader's first line runs, so any of them could replace
// the loader's output. LD_ names change what the dynamic loader maps into
// bash itself. The env must be a plain mapping, so an expression cannot supply
// such a name either.
func rejectToolPinLoaderEnv(env *yaml.Node, path, where string) error {
	if env.Kind != yaml.MappingNode {
		return fmt.Errorf("%s %s must be a mapping in a workflow that runs a pins step", path, where)
	}
	if err := rejectYAMLMergeKey(env, path, where); err != nil {
		return err
	}
	for i := 0; i+1 < len(env.Content); i += 2 {
		name := strings.ToUpper(env.Content[i].Value)
		switch {
		case name == "BASH_ENV", name == "ENV", name == "SHELLOPTS", name == "BASHOPTS", name == "PATH",
			strings.HasPrefix(name, "BASH_FUNC_"), strings.HasPrefix(name, "GITHUB_"), strings.HasPrefix(name, "RUNNER_"),
			strings.HasPrefix(name, "LD_"):
			return fmt.Errorf("%s %s must not define %s, which the pins step would inherit before the loader runs", path, where, env.Content[i].Value)
		}
	}
	return nil
}

// requireToolPinEnvMapping refuses an env that is neither absent, empty nor a
// plain mapping. A YAML alias or an expression would hide its names from the
// checks that read them.
func requireToolPinEnvMapping(env *yaml.Node, path, where string) error {
	if env == nil || env.Kind == yaml.MappingNode || (env.Kind == yaml.ScalarNode && env.Tag == "!!null") {
		return nil
	}
	return fmt.Errorf("%s %s must be a plain mapping, not a YAML alias or an expression, so its names can be checked", path, where)
}

// rejectYAMLMergeKey refuses a merge key (<<) in an env mapping. The names it
// would merge in are not keys of the mapping, so the name checks here would
// not see them.
func rejectYAMLMergeKey(env *yaml.Node, path, where string) error {
	if env == nil || env.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(env.Content); i += 2 {
		if key := env.Content[i]; key.Tag == "!!merge" || key.Value == "<<" {
			return fmt.Errorf("%s %s must not use a YAML merge key (<<); list each name in the mapping itself", path, where)
		}
	}
	return nil
}

// toolPinsShell is the reviewed workflow run shell the pins step inherits. A
// shell that does not run the loader as bash, or does not stop on its failure,
// could let the step pass with no outputs.
const toolPinsShell = "bash --noprofile --norc -euo pipefail {0}"

// toolPinWorkflowShellOK reports whether the workflow's defaults.run holds only
// the reviewed shell, if anything.
func toolPinWorkflowShellOK(root *yaml.Node) bool {
	for _, defaults := range yamlMappingValues(root, "defaults") {
		if defaults.Kind != yaml.MappingNode {
			return false
		}
		for i := 0; i+1 < len(defaults.Content); i += 2 {
			if defaults.Content[i].Value != "run" || defaults.Content[i+1].Kind != yaml.MappingNode {
				return false
			}
			run := defaults.Content[i+1]
			for j := 0; j+1 < len(run.Content); j += 2 {
				if run.Content[j].Value != "shell" || yamlScalarValue(run.Content[j+1]) != toolPinsShell {
					return false
				}
			}
		}
	}
	return true
}

// rejectInlineToolPins rejects any YAML scalar, key or value, that holds a pin
// value or names a pinned image repository. YAML comments are not scalars, so
// an action's version comment cannot trip it.
func rejectInlineToolPins(root *yaml.Node, path string, pins toolPins) error {
	fields := pins.fields()
	return walkYAMLScalars(root, func(scalar string) error {
		for _, field := range fields {
			if containsToolPinValue(scalar, *field.dst) {
				return fmt.Errorf("%s holds the policy/tool-pins.toml %s value %q inline; bind %s from the pins step instead", path, field.key, *field.dst, toolPinBinding(field.key))
			}
		}
		for _, repository := range toolPinImageRepositories {
			if strings.Contains(scalar, repository) {
				return fmt.Errorf("%s names the tool-pin image %s inline; bind it from the pins step instead", path, repository)
			}
		}
		return nil
	})
}

// containsToolPinValue reports whether value occurs in text as a whole token.
// A version must not continue a longer version: 1.3.3 does not match 11.3.3 or
// 1.3.30, but does match v1.3.3.
func containsToolPinValue(text, value string) bool {
	if value == "" {
		return false
	}
	for offset := 0; ; {
		index := strings.Index(text[offset:], value)
		if index < 0 {
			return false
		}
		start := offset + index
		end := start + len(value)
		if !toolPinVersionChar(text, start-1) && !toolPinVersionChar(text, end) {
			return true
		}
		offset = start + 1
	}
}

func toolPinVersionChar(text string, index int) bool {
	if index < 0 || index >= len(text) {
		return false
	}
	c := text[index]
	return c == '.' || (c >= '0' && c <= '9')
}

func walkYAMLScalars(node *yaml.Node, visit func(string) error) error {
	if node == nil {
		return nil
	}
	if node.Kind == yaml.ScalarNode {
		return visit(node.Value)
	}
	if node.Kind == yaml.AliasNode {
		return walkYAMLScalars(node.Alias, visit)
	}
	for _, child := range node.Content {
		if err := walkYAMLScalars(child, visit); err != nil {
			return err
		}
	}
	return nil
}

// toolPinRefs returns the pin output keys a node names, and an error for any
// other form of reference to the pins step (bracket access, a bare
// steps.pins.outputs object, steps.*).
func toolPinRefs(node *yaml.Node, path, where string) ([]string, error) {
	var keys []string
	err := walkYAMLScalars(node, func(scalar string) error {
		matches := toolPinOutputRefPattern.FindAllStringSubmatch(scalar, -1)
		if len(toolPinAnyRefPattern.FindAllString(scalar, -1)) != len(matches) {
			return fmt.Errorf("%s %s must reference tool pins only as %s", path, where, toolPinBinding("<key>"))
		}
		for _, match := range matches {
			keys = append(keys, match[1])
		}
		return nil
	})
	return keys, err
}

func rejectToolPinRefs(node *yaml.Node, path, where string) error {
	keys, err := toolPinRefs(node, path, where)
	if err != nil {
		return err
	}
	if len(keys) > 0 {
		return fmt.Errorf("%s %s must not reference the pins step; bind tool pins only in a step's with: or env:", path, where)
	}
	return nil
}

// rejectToolPinEnvNames keeps the pin env names out of workflow and job env,
// where no pins step output exists to bind them.
func rejectToolPinEnvNames(env *yaml.Node, byEnv map[string]string, path, where string) error {
	if err := requireToolPinEnvMapping(env, path, where); err != nil || env.Kind != yaml.MappingNode {
		return err
	}
	if err := rejectYAMLMergeKey(env, path, where); err != nil {
		return err
	}
	for i := 0; i+1 < len(env.Content); i += 2 {
		if key, ok := byEnv[env.Content[i].Value]; ok {
			return fmt.Errorf("%s %s must not define %s; bind %s in the consuming step's env instead", path, where, env.Content[i].Value, toolPinBinding(key))
		}
	}
	return nil
}

// validateJobToolPinBindings checks one job and reports whether it runs a
// pins step.
func validateJobToolPinBindings(job *yaml.Node, path, jobName string, byEnv map[string]string, shellOK bool) (bool, error) {
	hasPins, err := validateJobToolPinSteps(job, path, jobName, byEnv, shellOK)
	if err != nil || !hasPins {
		return hasPins, err
	}
	for _, env := range yamlMappingValues(job, "env") {
		if err := rejectToolPinLoaderEnv(env, path, "job "+jobName+" env"); err != nil {
			return true, err
		}
	}
	return true, nil
}

func validateJobToolPinSteps(job *yaml.Node, path, jobName string, byEnv map[string]string, shellOK bool) (bool, error) {
	if job.Kind != yaml.MappingNode {
		return false, fmt.Errorf("%s job %s must be a mapping", path, jobName)
	}
	where := "job " + jobName
	var steps *yaml.Node
	for i := 0; i+1 < len(job.Content); i += 2 {
		key, value := job.Content[i].Value, job.Content[i+1]
		if key == "steps" {
			steps = value
			continue
		}
		if err := rejectToolPinRefs(value, path, where+" "+key); err != nil {
			return false, err
		}
		if key == "env" {
			if err := rejectToolPinEnvNames(value, byEnv, path, where+" env"); err != nil {
				return false, err
			}
		}
	}
	if steps == nil {
		return false, nil
	}
	if steps.Kind != yaml.SequenceNode {
		return false, fmt.Errorf("%s %s must define steps as a sequence", path, where)
	}
	pinsAt, firstConsumer, checkoutAt := -1, -1, -1
	for at, step := range steps.Content {
		if step.Kind != yaml.MappingNode {
			return false, fmt.Errorf("%s %s step %d must be a mapping", path, where, at+1)
		}
		label := fmt.Sprintf("%s step %d", where, at+1)
		if strings.HasPrefix(yamlScalarValue(firstYAMLValue(step, "uses")), "actions/checkout@") && checkoutAt < 0 {
			checkoutAt = at
		}
		if strings.EqualFold(yamlScalarValue(firstYAMLValue(step, "id")), toolPinsStepID) {
			if pinsAt >= 0 {
				return false, fmt.Errorf("%s %s must have one pins step", path, where)
			}
			if err := validateToolPinsStep(step, path, label); err != nil {
				return false, err
			}
			pinsAt = at
			continue
		}
		consumes, err := validateStepToolPinBindings(step, path, label, byEnv)
		if err != nil {
			return false, err
		}
		if consumes && firstConsumer < 0 {
			firstConsumer = at
		}
	}
	switch {
	case firstConsumer < 0 && pinsAt < 0:
		return false, nil
	case firstConsumer < 0:
		return true, fmt.Errorf("%s %s has a pins step but no step binds a tool pin", path, where)
	case pinsAt < 0 || pinsAt > firstConsumer:
		return pinsAt >= 0, fmt.Errorf("%s %s must run the pins step (id: pins, run: %s) before its first tool-pin consumer", path, where, toolPinsStepRun)
	case checkoutAt != 0 || pinsAt != 1:
		// Only the checkout runs before the loader, so no earlier step can
		// write BASH_ENV, PATH or another start-up name to GITHUB_ENV.
		return true, fmt.Errorf("%s %s must check out the repository as its first step and run the pins step right after it", path, where)
	case !shellOK:
		return true, fmt.Errorf("%s defaults.run must hold only shell: %s, the shell the pins step runs under", path, toolPinsShell)
	case firstYAMLValue(job, "defaults") != nil || firstYAMLValue(job, "container") != nil:
		return true, fmt.Errorf("%s %s runs a pins step, so it must not set job defaults or a container", path, where)
	}
	return true, nil
}

func firstYAMLValue(mapping *yaml.Node, key string) *yaml.Node {
	values := yamlMappingValues(mapping, key)
	if len(values) == 0 {
		return nil
	}
	return values[0]
}

// validateToolPinsStep requires the pins step to run only the loader. It takes
// no if: and no continue-on-error, so a failed load always stops the job, and
// no env, shell or working-directory that could change what the loader reads.
func validateToolPinsStep(step *yaml.Node, path, label string) error {
	for i := 0; i+1 < len(step.Content); i += 2 {
		switch key := step.Content[i].Value; key {
		case "id", "name":
		case "run":
			if strings.TrimSpace(yamlScalarValue(step.Content[i+1])) != toolPinsStepRun {
				return fmt.Errorf("%s %s pins step must run exactly %s", path, label, toolPinsStepRun)
			}
		case "if", "continue-on-error":
			return fmt.Errorf("%s %s pins step must not set %s", path, label, key)
		default:
			return fmt.Errorf("%s %s pins step must hold only id, name and run, found %s", path, label, key)
		}
	}
	if firstYAMLValue(step, "run") == nil {
		return fmt.Errorf("%s %s pins step must run exactly %s", path, label, toolPinsStepRun)
	}
	return rejectToolPinRefs(step, path, label+" pins step")
}

// validateStepToolPinBindings checks one non-pins step and reports whether it
// binds a tool pin.
func validateStepToolPinBindings(step *yaml.Node, path, label string, byEnv map[string]string) (bool, error) {
	known := map[string]bool{}
	for _, key := range byEnv {
		known[key] = true
	}
	consumes := false
	for i := 0; i+1 < len(step.Content); i += 2 {
		key, value := step.Content[i].Value, step.Content[i+1]
		keys, err := toolPinRefs(value, path, label+" "+key)
		if err != nil {
			return false, err
		}
		if len(keys) == 0 {
			continue
		}
		if key != "with" && key != "env" {
			return false, fmt.Errorf("%s %s binds a tool pin in %s:; bind it only in with: or env: (never in run: script text)", path, label, key)
		}
		for _, name := range keys {
			if !known[name] {
				return false, fmt.Errorf("%s %s names unknown tool pin output %q; policy/tool-pins.toml keys are fixed", path, label, name)
			}
		}
		consumes = true
	}
	if err := requireToolPinEnvMapping(firstYAMLValue(step, "env"), path, label+" env"); err != nil {
		return false, err
	}
	if err := rejectYAMLMergeKey(firstYAMLValue(step, "env"), path, label+" env"); err != nil {
		return false, err
	}
	if env := firstYAMLValue(step, "env"); env != nil && env.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(env.Content); i += 2 {
			key, ok := byEnv[env.Content[i].Value]
			if !ok {
				continue
			}
			if yamlScalarValue(env.Content[i+1]) != toolPinBinding(key) {
				return false, fmt.Errorf("%s %s env %s must be exactly %s", path, label, env.Content[i].Value, toolPinBinding(key))
			}
		}
	}
	if err := validateToolPinActionInputs(step, path, label); err != nil {
		return false, err
	}
	if consumes {
		if condition := yamlScalarValue(firstYAMLValue(step, "if")); toolPinBypassIfPattern.MatchString(condition) {
			return false, fmt.Errorf("%s %s binds a tool pin, so its if: must not use always(), cancelled(), failure() or a negated success(): %q", path, label, condition)
		}
	}
	return consumes, nil
}

// toolPinActionInputs are the action inputs that install a pinned tool. Every
// step that uses the action must set each input to exactly its binding, so an
// action never falls back to its own default release or takes a literal.
var toolPinActionInputs = []struct {
	action string
	inputs map[string]string
}{
	{"sigstore/cosign-installer@", map[string]string{"cosign-release": toolPinBinding("cosign")}},
	{"docker/setup-buildx-action@", map[string]string{
		"version":     toolPinBinding("buildx"),
		"driver-opts": "image=" + toolPinBinding("buildkit"),
	}},
	{"anchore/sbom-action/download-syft@", map[string]string{"syft-version": toolPinBinding("syft")}},
}

// toolPinInputNames are inputs that hold a tool pin whatever action takes them.
var toolPinInputNames = map[string]string{
	"cosign-release": toolPinBinding("cosign"),
	"syft-version":   toolPinBinding("syft"),
}

func validateToolPinActionInputs(step *yaml.Node, path, label string) error {
	uses := yamlScalarValue(firstYAMLValue(step, "uses"))
	with := firstYAMLValue(step, "with")
	input := func(name string) (string, bool) {
		value := firstYAMLValue(with, name)
		if value == nil {
			return "", false
		}
		return yamlScalarValue(value), true
	}
	for _, rule := range toolPinActionInputs {
		if !strings.HasPrefix(uses, rule.action) {
			continue
		}
		for name, want := range rule.inputs {
			if got, ok := input(name); !ok || got != want {
				return fmt.Errorf("%s %s (%s) must set %s: %s", path, label, strings.TrimSuffix(rule.action, "@"), name, want)
			}
		}
	}
	for name, want := range toolPinInputNames {
		if got, ok := input(name); ok && got != want {
			return fmt.Errorf("%s %s must set %s: %s", path, label, name, want)
		}
	}
	return nil
}
