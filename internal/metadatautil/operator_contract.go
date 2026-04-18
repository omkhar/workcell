// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type operatorWorkflow struct {
	ID              string
	Title           string
	Support         string
	Canonical       string
	Aliases         []string
	AliasLifecycle  string
	AliasProbes     []string
	Discoverability []string
	Docs            []string
	Evidence        []string
	Requirements    []string
	Remediation     []string
	TargetState     string
}

type operatorContract struct {
	Workflows map[string]operatorWorkflow
}

type requirementTraceability struct {
	Workflows map[string]struct{}
	Docs      map[string]struct{}
	Evidence  map[string]struct{}
}

func ValidateOperatorContract(rootDir, contractPath, requirementsPath string) error {
	if err := ValidateRequirements(rootDir, requirementsPath); err != nil {
		return err
	}

	contract, err := loadOperatorContract(contractPath)
	if err != nil {
		return err
	}

	requirements, err := loadRequirementTraceability(rootDir, requirementsPath)
	if err != nil {
		return err
	}

	for requirementID, requirement := range requirements {
		for workflowID := range requirement.Workflows {
			if _, ok := contract.Workflows[workflowID]; !ok {
				return fmt.Errorf("%s requirement %s references unknown workflow %s", requirementsPath, requirementID, workflowID)
			}
		}
	}

	surfaces, err := loadOperatorSurfaces(rootDir, contract)
	if err != nil {
		return err
	}

	workcellSource, err := readText(filepath.Join(rootDir, "scripts", "workcell"))
	if err != nil {
		return err
	}

	publicWorkflowCovered := map[string]struct{}{}
	workflowIDs := make([]string, 0, len(contract.Workflows))
	for workflowID := range contract.Workflows {
		workflowIDs = append(workflowIDs, workflowID)
	}
	sort.Strings(workflowIDs)

	for _, workflowID := range workflowIDs {
		workflow := contract.Workflows[workflowID]
		if isPublicWorkflowTier(workflow.Support) && len(workflow.Requirements) == 0 {
			return fmt.Errorf("%s workflow %s must cite at least one requirement", contractPath, workflowID)
		}
		if isPublicWorkflowTier(workflow.Support) && len(workflow.Docs) == 0 {
			return fmt.Errorf("%s workflow %s must cite at least one documentation path", contractPath, workflowID)
		}
		if isPublicWorkflowTier(workflow.Support) && len(workflow.Evidence) == 0 {
			return fmt.Errorf("%s workflow %s must cite at least one evidence path", contractPath, workflowID)
		}

		requirementDocs := map[string]struct{}{}
		requirementEvidence := map[string]struct{}{}
		for _, requirementID := range workflow.Requirements {
			declared, ok := requirements[requirementID]
			if !ok {
				return fmt.Errorf("%s workflow %s references unknown requirement %s", contractPath, workflowID, requirementID)
			}
			if _, ok := declared.Workflows[workflowID]; !ok {
				return fmt.Errorf("%s workflow %s must appear in %s requirement %s workflows array", contractPath, workflowID, requirementsPath, requirementID)
			}
			mergePathSets(requirementDocs, declared.Docs)
			mergePathSets(requirementEvidence, declared.Evidence)
			publicWorkflowCovered[workflowID] = struct{}{}
		}

		if err := validateWorkflowPathReferences(rootDir, contractPath, requirementsPath, workflowID, "docs", workflow.Docs, requirementDocs); err != nil {
			return err
		}
		if err := validateWorkflowPathReferences(rootDir, contractPath, requirementsPath, workflowID, "evidence", workflow.Evidence, requirementEvidence); err != nil {
			return err
		}

		for _, surface := range workflow.Discoverability {
			content, ok := surfaces[surface]
			if !ok {
				return fmt.Errorf("%s workflow %s references unknown discoverability surface %s", contractPath, workflowID, surface)
			}
			if !strings.Contains(content, workflow.Canonical) {
				return fmt.Errorf("%s workflow %s canonical syntax %q missing from %s", contractPath, workflowID, workflow.Canonical, surface)
			}
		}

		for _, remediation := range workflow.Remediation {
			if !strings.Contains(workcellSource, remediation) {
				return fmt.Errorf("%s workflow %s remediation text %q missing from scripts/workcell", contractPath, workflowID, remediation)
			}
		}

		if err := validateAliasProbes(rootDir, contractPath, workflowID, workflow); err != nil {
			return err
		}
	}

	for _, workflowID := range workflowIDs {
		workflow := contract.Workflows[workflowID]
		if !isPublicWorkflowTier(workflow.Support) {
			continue
		}
		if _, ok := publicWorkflowCovered[workflowID]; ok {
			continue
		}
		return fmt.Errorf("%s workflow %s must be mapped to at least one requirement", contractPath, workflowID)
	}

	return nil
}

func loadOperatorContract(contractPath string) (operatorContract, error) {
	text, err := readText(contractPath)
	if err != nil {
		return operatorContract{}, err
	}
	document, err := ParseTOMLSubset(text, contractPath)
	if err != nil {
		return operatorContract{}, err
	}

	version, ok := document["version"].(int)
	if !ok || version != 1 {
		return operatorContract{}, fmt.Errorf("%s must set version = 1", contractPath)
	}

	rawWorkflows, ok := document["workflows"].(map[string]any)
	if !ok || len(rawWorkflows) == 0 {
		return operatorContract{}, fmt.Errorf("%s must define a non-empty [workflows] table", contractPath)
	}

	workflows := make(map[string]operatorWorkflow, len(rawWorkflows))
	for workflowID, rawSpec := range rawWorkflows {
		spec, ok := rawSpec.(map[string]any)
		if !ok {
			return operatorContract{}, fmt.Errorf("%s workflow %s must be a table", contractPath, workflowID)
		}
		workflow, err := parseOperatorWorkflow(contractPath, workflowID, spec)
		if err != nil {
			return operatorContract{}, err
		}
		workflows[workflowID] = workflow
	}

	return operatorContract{Workflows: workflows}, nil
}

func parseOperatorWorkflow(contractPath, workflowID string, spec map[string]any) (operatorWorkflow, error) {
	allowedKeys := map[string]struct{}{
		"title":           {},
		"support":         {},
		"canonical":       {},
		"aliases":         {},
		"alias_lifecycle": {},
		"alias_probes":    {},
		"discoverability": {},
		"docs":            {},
		"evidence":        {},
		"requirements":    {},
		"remediation":     {},
		"target_state":    {},
	}
	for key := range spec {
		if _, ok := allowedKeys[key]; !ok {
			return operatorWorkflow{}, fmt.Errorf("%s workflow %s contains unsupported key %s", contractPath, workflowID, key)
		}
	}
	if !isValidWorkflowID(workflowID) {
		return operatorWorkflow{}, fmt.Errorf("%s workflow %s must use lowercase snake_case ids", contractPath, workflowID)
	}

	title, ok := MustString(spec["title"])
	if !ok || strings.TrimSpace(title) == "" {
		return operatorWorkflow{}, fmt.Errorf("%s workflow %s must define a non-empty title", contractPath, workflowID)
	}
	support, ok := MustString(spec["support"])
	if !ok || !isAllowedOperatorSupportTier(support) {
		return operatorWorkflow{}, fmt.Errorf("%s workflow %s must define support as supported, compatibility-only, or internal", contractPath, workflowID)
	}
	canonical, ok := MustString(spec["canonical"])
	if !ok || strings.TrimSpace(canonical) == "" {
		return operatorWorkflow{}, fmt.Errorf("%s workflow %s must define a non-empty canonical value", contractPath, workflowID)
	}
	targetState, ok := MustString(spec["target_state"])
	if !ok || !isAllowedTargetState(targetState) {
		return operatorWorkflow{}, fmt.Errorf("%s workflow %s must define target_state as retain, deprecate, or remove", contractPath, workflowID)
	}

	aliases, err := optionalStringSlice(spec, "aliases")
	if err != nil {
		return operatorWorkflow{}, fmt.Errorf("%s workflow %s aliases: %w", contractPath, workflowID, err)
	}
	aliasProbes, err := optionalStringSlice(spec, "alias_probes")
	if err != nil {
		return operatorWorkflow{}, fmt.Errorf("%s workflow %s alias_probes: %w", contractPath, workflowID, err)
	}
	aliasLifecycle, ok := MustString(spec["alias_lifecycle"])
	if len(aliases) > 0 {
		if !ok || !isAllowedAliasLifecycle(aliasLifecycle) {
			return operatorWorkflow{}, fmt.Errorf("%s workflow %s must define alias_lifecycle for aliases", contractPath, workflowID)
		}
		if len(aliasProbes) == 0 {
			return operatorWorkflow{}, fmt.Errorf("%s workflow %s must define alias_probes for aliases", contractPath, workflowID)
		}
	} else if ok && strings.TrimSpace(aliasLifecycle) != "" {
		return operatorWorkflow{}, fmt.Errorf("%s workflow %s must not define alias_lifecycle without aliases", contractPath, workflowID)
	} else if len(aliasProbes) > 0 {
		return operatorWorkflow{}, fmt.Errorf("%s workflow %s must not define alias_probes without aliases", contractPath, workflowID)
	}

	discoverability, err := optionalStringSlice(spec, "discoverability")
	if err != nil {
		return operatorWorkflow{}, fmt.Errorf("%s workflow %s discoverability: %w", contractPath, workflowID, err)
	}
	for _, surface := range discoverability {
		if err := validateDiscoverabilitySurface(surface); err != nil {
			return operatorWorkflow{}, fmt.Errorf("%s workflow %s discoverability %s: %w", contractPath, workflowID, surface, err)
		}
	}
	if support == "internal" && len(discoverability) > 0 {
		return operatorWorkflow{}, fmt.Errorf("%s workflow %s must not declare discoverability surfaces for internal workflows", contractPath, workflowID)
	}
	if isPublicWorkflowTier(support) && len(discoverability) == 0 {
		return operatorWorkflow{}, fmt.Errorf("%s workflow %s must declare at least one discoverability surface", contractPath, workflowID)
	}

	docs, err := optionalStringSlice(spec, "docs")
	if err != nil {
		return operatorWorkflow{}, fmt.Errorf("%s workflow %s docs: %w", contractPath, workflowID, err)
	}
	evidence, err := optionalStringSlice(spec, "evidence")
	if err != nil {
		return operatorWorkflow{}, fmt.Errorf("%s workflow %s evidence: %w", contractPath, workflowID, err)
	}

	requirements, err := optionalStringSlice(spec, "requirements")
	if err != nil {
		return operatorWorkflow{}, fmt.Errorf("%s workflow %s requirements: %w", contractPath, workflowID, err)
	}
	for _, requirementID := range requirements {
		if !strings.Contains(requirementID, ".") {
			return operatorWorkflow{}, fmt.Errorf("%s workflow %s requirement %s must include a category prefix such as functional.FR-001", contractPath, workflowID, requirementID)
		}
	}

	remediation, err := optionalStringSlice(spec, "remediation")
	if err != nil {
		return operatorWorkflow{}, fmt.Errorf("%s workflow %s remediation: %w", contractPath, workflowID, err)
	}

	return operatorWorkflow{
		ID:              workflowID,
		Title:           title,
		Support:         support,
		Canonical:       canonical,
		Aliases:         aliases,
		AliasLifecycle:  aliasLifecycle,
		AliasProbes:     aliasProbes,
		Discoverability: discoverability,
		Docs:            docs,
		Evidence:        evidence,
		Requirements:    requirements,
		Remediation:     remediation,
		TargetState:     targetState,
	}, nil
}

func optionalStringSlice(spec map[string]any, key string) ([]string, error) {
	value, ok := spec[key]
	if !ok {
		return nil, nil
	}
	values, found, err := MustStringSlice(value)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("must be an array of strings")
	}
	for _, item := range values {
		if strings.TrimSpace(item) == "" {
			return nil, fmt.Errorf("entries may not be empty")
		}
	}
	return values, nil
}

func isValidWorkflowID(workflowID string) bool {
	for _, char := range workflowID {
		switch {
		case char >= 'a' && char <= 'z':
		case char >= '0' && char <= '9':
		case char == '_':
		default:
			return false
		}
	}
	return workflowID != ""
}

func isAllowedOperatorSupportTier(value string) bool {
	switch value {
	case "supported", "compatibility-only", "internal":
		return true
	default:
		return false
	}
}

func isAllowedAliasLifecycle(value string) bool {
	switch value {
	case "supported", "compatibility-only", "hidden", "deprecated", "removal-candidate":
		return true
	default:
		return false
	}
}

func isAllowedTargetState(value string) bool {
	switch value {
	case "retain", "deprecate", "remove":
		return true
	default:
		return false
	}
}

func isPublicWorkflowTier(value string) bool {
	return value == "supported" || value == "compatibility-only"
}

func validateDiscoverabilitySurface(surface string) error {
	switch {
	case surface == "top-level-help":
		return nil
	case surface == "readme":
		return nil
	case surface == "manpage":
		return nil
	case strings.HasPrefix(surface, "subcommand-help:"):
		name := strings.TrimPrefix(surface, "subcommand-help:")
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("subcommand-help entries require a command name")
		}
		if strings.Contains(name, " ") {
			return fmt.Errorf("subcommand-help entries support only a single command token")
		}
		return nil
	default:
		return fmt.Errorf("unsupported discoverability surface")
	}
}

func loadOperatorSurfaces(rootDir string, contract operatorContract) (map[string]string, error) {
	surfaces := map[string]string{}
	requested := map[string]struct{}{}
	for _, workflow := range contract.Workflows {
		for _, surface := range workflow.Discoverability {
			requested[surface] = struct{}{}
		}
	}

	keys := make([]string, 0, len(requested))
	for surface := range requested {
		keys = append(keys, surface)
	}
	sort.Strings(keys)
	for _, surface := range keys {
		content, err := renderDiscoverabilitySurface(rootDir, surface)
		if err != nil {
			return nil, err
		}
		surfaces[surface] = content
	}
	return surfaces, nil
}

func renderDiscoverabilitySurface(rootDir, surface string) (string, error) {
	switch surface {
	case "top-level-help":
		return runWorkcellHelp(rootDir)
	case "readme":
		return readText(filepath.Join(rootDir, "README.md"))
	case "manpage":
		return renderManpageText(filepath.Join(rootDir, "man", "workcell.1"))
	}

	if strings.HasPrefix(surface, "subcommand-help:") {
		commandName := strings.TrimPrefix(surface, "subcommand-help:")
		return runWorkcellHelp(rootDir, commandName)
	}
	return "", fmt.Errorf("unsupported discoverability surface %s", surface)
}

func runWorkcellHelp(rootDir string, args ...string) (string, error) {
	scriptPath := workcellHelpBinary(rootDir)
	commandArgs := append(append([]string{}, args...), "--help")
	cmd := exec.Command(scriptPath, commandArgs...)
	cmd.Dir = rootDir
	cmd.Env = sanitizedScriptEnvironment(os.Environ())
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("running %s %s: %w\n%s", scriptPath, strings.Join(commandArgs, " "), err, strings.TrimSpace(string(output)))
	}
	return string(output), nil
}

func renderManpageText(manpagePath string) (string, error) {
	source, err := readText(manpagePath)
	if err != nil {
		return "", err
	}
	trimmedSource := strings.TrimSpace(source)
	if !strings.HasPrefix(trimmedSource, ".") {
		return source, nil
	}
	if _, err := exec.LookPath("mandoc"); err == nil {
		output, renderErr := exec.Command("mandoc", "-Tascii", manpagePath).CombinedOutput()
		if renderErr != nil {
			return "", fmt.Errorf("rendering %s with mandoc: %w\n%s", manpagePath, renderErr, strings.TrimSpace(string(output)))
		}
		return stripManpageFormatting(string(output)), nil
	}
	if _, err := exec.LookPath("nroff"); err == nil {
		output, renderErr := exec.Command("nroff", "-man", manpagePath).CombinedOutput()
		if renderErr != nil {
			return "", fmt.Errorf("rendering %s with nroff: %w\n%s", manpagePath, renderErr, strings.TrimSpace(string(output)))
		}
		return stripManpageFormatting(string(output)), nil
	}
	return source, nil
}

func stripManpageFormatting(content string) string {
	var builder strings.Builder
	builder.Grow(len(content))
	for i := 0; i < len(content); i++ {
		if i+2 < len(content) && content[i+1] == '\b' {
			builder.WriteByte(content[i+2])
			i += 2
			continue
		}
		if content[i] == '\b' {
			continue
		}
		builder.WriteByte(content[i])
	}
	return builder.String()
}

func sanitizedScriptEnvironment(base []string) []string {
	filtered := make([]string, 0, len(base)+2)
	for _, entry := range base {
		switch {
		case strings.HasPrefix(entry, "BASH_ENV="):
			continue
		case strings.HasPrefix(entry, "ENV="):
			continue
		default:
			filtered = append(filtered, entry)
		}
	}
	filtered = append(filtered, "BASH_ENV=", "ENV=")
	return filtered
}

func loadRequirementTraceability(rootDir, requirementsPath string) (map[string]requirementTraceability, error) {
	text, err := readText(requirementsPath)
	if err != nil {
		return nil, err
	}
	document, err := ParseTOMLSubset(text, requirementsPath)
	if err != nil {
		return nil, err
	}

	refs := map[string]requirementTraceability{}
	for _, category := range []string{"functional", "nonfunctional"} {
		rawTable, ok := document[category].(map[string]any)
		if !ok {
			continue
		}
		for requirementID, rawValue := range rawTable {
			qualifiedRequirementID := category + "." + requirementID
			table, ok := rawValue.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("%s requirement %s must be a table", requirementsPath, qualifiedRequirementID)
			}
			workflows, err := optionalStringSlice(table, "workflows")
			if err != nil {
				return nil, fmt.Errorf("%s requirement %s workflows: %w", requirementsPath, qualifiedRequirementID, err)
			}
			workflowSet := map[string]struct{}{}
			for _, workflowID := range workflows {
				if !isValidWorkflowID(workflowID) {
					return nil, fmt.Errorf("%s requirement %s workflow %s must use lowercase snake_case ids", requirementsPath, qualifiedRequirementID, workflowID)
				}
				workflowSet[workflowID] = struct{}{}
			}

			evidenceValues, _, err := MustStringSlice(table["evidence"])
			if err != nil {
				return nil, fmt.Errorf("%s requirement %s evidence: %w", requirementsPath, qualifiedRequirementID, err)
			}
			evidenceSet := map[string]struct{}{}
			for _, path := range evidenceValues {
				canonicalPath, err := canonicalRequirementRepoPath(rootDir, path)
				if err != nil {
					return nil, fmt.Errorf("%s requirement %s evidence path %s: %w", requirementsPath, qualifiedRequirementID, path, err)
				}
				evidenceSet[canonicalPath] = struct{}{}
			}

			docValues, _, err := MustStringSlice(table["docs"])
			if err != nil {
				return nil, fmt.Errorf("%s requirement %s docs: %w", requirementsPath, qualifiedRequirementID, err)
			}
			docSet := map[string]struct{}{}
			for _, path := range docValues {
				canonicalPath, err := canonicalRequirementRepoPath(rootDir, path)
				if err != nil {
					return nil, fmt.Errorf("%s requirement %s docs path %s: %w", requirementsPath, qualifiedRequirementID, path, err)
				}
				docSet[canonicalPath] = struct{}{}
			}

			refs[qualifiedRequirementID] = requirementTraceability{
				Workflows: workflowSet,
				Docs:      docSet,
				Evidence:  evidenceSet,
			}
		}
	}
	return refs, nil
}

func mergePathSets(destination, source map[string]struct{}) {
	for path := range source {
		destination[path] = struct{}{}
	}
}

func validateWorkflowPathReferences(rootDir, contractPath, requirementsPath, workflowID, field string, paths []string, requirementPaths map[string]struct{}) error {
	for _, path := range paths {
		if err := validateWorkflowPath(rootDir, contractPath, workflowID, field, path); err != nil {
			return err
		}
		canonicalPath, err := canonicalRequirementRepoPath(rootDir, path)
		if err != nil {
			return fmt.Errorf("%s workflow %s %s path %s: %w", contractPath, workflowID, field, path, err)
		}
		if _, ok := requirementPaths[canonicalPath]; !ok {
			return fmt.Errorf("%s workflow %s %s path %s must be cited by one of its referenced requirements in %s", contractPath, workflowID, field, path, requirementsPath)
		}
	}
	return nil
}

func validateWorkflowPath(rootDir, contractPath, workflowID, field, path string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("%s workflow %s %s entries may not be empty", contractPath, workflowID, field)
	}
	if filepath.IsAbs(path) {
		return fmt.Errorf("%s workflow %s %s path %s must be repo-relative", contractPath, workflowID, field, path)
	}

	target, err := resolveRequirementPath(rootDir, path)
	if err != nil {
		return fmt.Errorf("%s workflow %s %s path %s: %w", contractPath, workflowID, field, path, err)
	}
	if err := rejectRequirementPathSymlinks(rootDir, target); err != nil {
		return fmt.Errorf("%s workflow %s %s path %s: %w", contractPath, workflowID, field, path, err)
	}
	info, err := os.Lstat(target)
	if err != nil {
		return fmt.Errorf("%s workflow %s %s path %s: %w", contractPath, workflowID, field, path, err)
	}
	if info.IsDir() {
		return fmt.Errorf("%s workflow %s %s path %s must point to a file", contractPath, workflowID, field, path)
	}
	return nil
}

func validateAliasProbes(rootDir, contractPath, workflowID string, workflow operatorWorkflow) error {
	for _, probe := range workflow.AliasProbes {
		args := strings.Fields(probe)
		if len(args) == 0 {
			return fmt.Errorf("%s workflow %s alias_probes entries may not be empty", contractPath, workflowID)
		}
		output, exitCode, err := runWorkcellCommand(rootDir, args...)
		if err != nil {
			return fmt.Errorf("%s workflow %s alias probe %q: %w", contractPath, workflowID, probe, err)
		}
		if exitCode != 0 {
			return fmt.Errorf("%s workflow %s alias probe %q exited %d\n%s", contractPath, workflowID, probe, exitCode, strings.TrimSpace(output))
		}
		if usagePrefix := expectedAliasProbeUsage(probe); usagePrefix != "" && !strings.Contains(output, usagePrefix) {
			return fmt.Errorf("%s workflow %s alias probe %q output missing alias usage %q", contractPath, workflowID, probe, usagePrefix)
		}
		if !strings.Contains(output, workflow.Canonical) {
			return fmt.Errorf("%s workflow %s alias probe %q output missing canonical syntax %q", contractPath, workflowID, probe, workflow.Canonical)
		}
	}
	return nil
}

func expectedAliasProbeUsage(probe string) string {
	args := strings.Fields(probe)
	if len(args) < 2 || args[len(args)-1] != "--help" {
		return ""
	}
	return "Usage: workcell " + strings.Join(args[:len(args)-1], " ")
}

func runWorkcellCommand(rootDir string, args ...string) (string, int, error) {
	scriptPath := workcellHelpBinary(rootDir)
	cmd := exec.Command(scriptPath, args...)
	cmd.Dir = rootDir
	cmd.Env = sanitizedScriptEnvironment(os.Environ())
	output, err := cmd.CombinedOutput()
	if err == nil {
		return string(output), 0, nil
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return string(output), exitErr.ExitCode(), nil
	}
	return "", -1, fmt.Errorf("running %s %s: %w", scriptPath, strings.Join(args, " "), err)
}

func workcellHelpBinary(rootDir string) string {
	if override := strings.TrimSpace(os.Getenv("WORKCELL_HELP_BIN")); override != "" {
		return override
	}
	return filepath.Join(rootDir, "scripts", "workcell")
}
