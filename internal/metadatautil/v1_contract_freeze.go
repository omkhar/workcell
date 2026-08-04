// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

type v1ContractFreeze struct {
	Public            publicContract
	OperatorWorkflows map[string]string
}

type namedContractSet struct {
	name   string
	values []string
}

var commitOIDPattern = regexp.MustCompile(`^[0-9a-f]{40,64}$`)

// CheckV1ContractFreezeHistory requires every entry in an earlier v1 freeze
// snapshot to remain in the current snapshot. The caller supplies snapshots
// from Git history; checking all versions makes the floor append-only even when
// a change edits the live inventory and current freeze together.
func CheckV1ContractFreezeHistory(priorFreezePath, currentFreezePath string) error {
	prior, err := loadV1ContractFreeze(priorFreezePath)
	if err != nil {
		return err
	}
	current, err := loadV1ContractFreeze(currentFreezePath)
	if err != nil {
		return err
	}

	return checkV1ContractFreezeSnapshots(prior, current, priorFreezePath, currentFreezePath)
}

// CheckV1ContractFreezeGitHistory validates the current floor against every
// version of that path reachable from HEAD. --full-history prevents merge
// simplification from hiding a commitment that a later merge removed.
func CheckV1ContractFreezeGitHistory(rootDir, currentFreezePath string) error {
	inside, err := gitOutput(rootDir, "rev-parse", "--is-inside-work-tree")
	if err != nil || strings.TrimSpace(inside) != "true" {
		return fmt.Errorf("v1 contract history validation requires a readable Git worktree at %s", rootDir)
	}
	shallow, err := gitOutput(rootDir, "rev-parse", "--is-shallow-repository")
	if err != nil {
		return fmt.Errorf("checking whether Git history is shallow: %w", err)
	}
	if strings.TrimSpace(shallow) == "true" {
		return fmt.Errorf("v1 contract history validation requires complete Git history")
	}
	if _, err := gitOutput(rootDir, "rev-parse", "--verify", "HEAD"); err != nil {
		return fmt.Errorf("resolving HEAD for v1 contract history validation: %w", err)
	}

	current, err := loadV1ContractFreeze(currentFreezePath)
	if err != nil {
		return err
	}
	relativePath, err := filepath.Rel(rootDir, currentFreezePath)
	if err != nil || relativePath == "." || strings.HasPrefix(relativePath, ".."+string(filepath.Separator)) || filepath.IsAbs(relativePath) {
		return fmt.Errorf("v1 contract freeze must be inside the repository: %s", currentFreezePath)
	}
	relativePath = filepath.ToSlash(relativePath)

	history, err := gitOutput(rootDir, v1ContractHistoryLogArgs(relativePath)...)
	if err != nil {
		return fmt.Errorf("reading v1 contract Git history: %w", err)
	}
	commitOIDs := strings.Fields(history)
	if len(commitOIDs) == 0 {
		headPaths, err := gitOutput(rootDir, "ls-tree", "-r", "--name-only", "HEAD", "--", relativePath)
		if err != nil {
			return fmt.Errorf("checking v1 contract bootstrap state: %w", err)
		}
		if strings.TrimSpace(headPaths) != "" {
			return fmt.Errorf("v1 contract freeze exists at HEAD but has no readable Git history")
		}
		return nil
	}

	historyErrors := make([]error, 0)
	for _, commitOID := range commitOIDs {
		if !commitOIDPattern.MatchString(commitOID) {
			return fmt.Errorf("Git history returned malformed commit OID %q for %s", commitOID, relativePath)
		}
		label := commitOID + ":" + relativePath
		priorText, err := gitOutput(rootDir, "show", label)
		if err != nil {
			return fmt.Errorf("reading historical v1 contract freeze %s: %w", label, err)
		}
		prior, err := loadV1ContractFreezeText(priorText, label)
		if err != nil {
			return err
		}
		if err := checkV1ContractFreezeSnapshots(prior, current, label, currentFreezePath); err != nil {
			historyErrors = append(historyErrors, err)
		}
	}
	return errors.Join(historyErrors...)
}

func checkV1ContractFreeze(currentPublicContractPath, currentOperatorContractPath, freezePath string) error {
	freeze, err := loadV1ContractFreeze(freezePath)
	if err != nil {
		return err
	}
	currentPublic, err := loadPublicContract(currentPublicContractPath)
	if err != nil {
		return err
	}
	currentOperator, err := loadOperatorContract(currentOperatorContractPath)
	if err != nil {
		return err
	}

	frozenSets := publicContractSets(freeze.Public)
	currentSets := publicContractSets(currentPublic)
	for index, frozenSet := range frozenSets {
		currentSet := currentSets[index]
		if missing := missingFrozenValues(frozenSet.values, currentSet.values); len(missing) > 0 {
			return fmt.Errorf("%s v1 %s missing from %s: %s", freezePath, frozenSet.name, currentPublicContractPath, strings.Join(missing, ", "))
		}
		if unfrozen := missingFrozenValues(currentSet.values, frozenSet.values); len(unfrozen) > 0 {
			return fmt.Errorf("%s must append current %s from %s: %s", freezePath, frozenSet.name, currentPublicContractPath, strings.Join(unfrozen, ", "))
		}
	}
	if !slices.Equal(freeze.Public.ScenarioManifestTSVColumns, currentPublic.ScenarioManifestTSVColumns) {
		return fmt.Errorf(
			"%s v1 scenario-manifest TSV columns changed in %s: got %q, want %q",
			freezePath,
			currentPublicContractPath,
			currentPublic.ScenarioManifestTSVColumns,
			freeze.Public.ScenarioManifestTSVColumns,
		)
	}

	workflowIDs := make([]string, 0, len(freeze.OperatorWorkflows))
	for workflowID := range freeze.OperatorWorkflows {
		workflowIDs = append(workflowIDs, workflowID)
	}
	slices.Sort(workflowIDs)
	for _, workflowID := range workflowIDs {
		frozenCanonical := freeze.OperatorWorkflows[workflowID]
		current, ok := currentOperator.Workflows[workflowID]
		if !ok {
			return fmt.Errorf("%s v1 workflow %s missing from %s", freezePath, workflowID, currentOperatorContractPath)
		}
		if current.Canonical != frozenCanonical {
			return fmt.Errorf("%s v1 workflow %s canonical syntax changed in %s: got %q, want %q", freezePath, workflowID, currentOperatorContractPath, current.Canonical, frozenCanonical)
		}
		if !isPublicWorkflowTier(current.Support) || current.TargetState == "remove" {
			return fmt.Errorf("%s v1 workflow %s is no longer retained as a public workflow in %s", freezePath, workflowID, currentOperatorContractPath)
		}
	}
	for workflowID, current := range currentOperator.Workflows {
		if current.Support != "supported" || current.TargetState != "retain" {
			continue
		}
		if _, ok := freeze.OperatorWorkflows[workflowID]; !ok {
			return fmt.Errorf("%s must append current supported workflow %s from %s", freezePath, workflowID, currentOperatorContractPath)
		}
	}
	return nil
}

func loadV1ContractFreeze(freezePath string) (v1ContractFreeze, error) {
	text, err := readText(freezePath)
	if err != nil {
		return v1ContractFreeze{}, err
	}
	return loadV1ContractFreezeText(text, freezePath)
}

func loadV1ContractFreezeText(text, label string) (v1ContractFreeze, error) {
	document, err := ParseTOMLSubset(text, label)
	if err != nil {
		return v1ContractFreeze{}, err
	}

	meta, ok := document["meta"].(map[string]any)
	if !ok {
		return v1ContractFreeze{}, fmt.Errorf("%s must define a [meta] table", label)
	}
	version, ok := MustString(meta["version"])
	if !ok || version != "1" {
		return v1ContractFreeze{}, fmt.Errorf(`%s must set [meta] version = "1"`, label)
	}

	public := publicContract{}
	publicFields := []struct {
		key    string
		target *[]string
	}{
		{"exit_codes", &public.ExitCodes},
		{"output_line_prefixes", &public.OutputLinePrefixes},
		{"session_show_prefixes", &public.SessionShowPrefixes},
		{"session_record_fields", &public.SessionRecordFields},
		{"session_export_fields", &public.SessionExportFields},
		{"injection_tables", &public.InjectionTables},
		{"injection_scalar_root_keys", &public.InjectionScalarRootKeys},
	}
	for _, field := range publicFields {
		values, err := requiredStringSliceTable(document, label, "public_contract", field.key)
		if err != nil {
			return v1ContractFreeze{}, err
		}
		*field.target = values
	}
	scenarioManifestTSVColumns, err := optionalStringSliceTable(document, label, "public_contract", "scenario_manifest_tsv_columns")
	if err != nil {
		return v1ContractFreeze{}, err
	}
	public.ScenarioManifestTSVColumns = scenarioManifestTSVColumns

	rawWorkflows, ok := document["operator_workflows"].(map[string]any)
	if !ok || len(rawWorkflows) == 0 {
		return v1ContractFreeze{}, fmt.Errorf("%s must define a non-empty [operator_workflows] table", label)
	}
	workflows := make(map[string]string, len(rawWorkflows))
	for workflowID, rawCanonical := range rawWorkflows {
		if !isValidWorkflowID(workflowID) {
			return v1ContractFreeze{}, fmt.Errorf("%s has invalid v1 workflow id %q", label, workflowID)
		}
		canonical, ok := MustString(rawCanonical)
		if !ok || strings.TrimSpace(canonical) == "" {
			return v1ContractFreeze{}, fmt.Errorf("%s v1 workflow %s must define non-empty canonical syntax", label, workflowID)
		}
		workflows[workflowID] = canonical
	}

	return v1ContractFreeze{Public: public, OperatorWorkflows: workflows}, nil
}

func checkV1ContractFreezeSnapshots(prior, current v1ContractFreeze, priorLabel, currentLabel string) error {
	priorSets := publicContractSets(prior.Public)
	currentSets := publicContractSets(current.Public)
	for index, priorSet := range priorSets {
		if missing := missingFrozenValues(priorSet.values, currentSets[index].values); len(missing) > 0 {
			return fmt.Errorf("%s removed historical v1 %s from %s: %s", currentLabel, priorSet.name, priorLabel, strings.Join(missing, ", "))
		}
	}
	if len(prior.Public.ScenarioManifestTSVColumns) > 0 && !slices.Equal(prior.Public.ScenarioManifestTSVColumns, current.Public.ScenarioManifestTSVColumns) {
		return fmt.Errorf(
			"%s rewrote historical v1 scenario-manifest TSV columns from %s: got %q, want %q",
			currentLabel,
			priorLabel,
			current.Public.ScenarioManifestTSVColumns,
			prior.Public.ScenarioManifestTSVColumns,
		)
	}

	workflowIDs := make([]string, 0, len(prior.OperatorWorkflows))
	for workflowID := range prior.OperatorWorkflows {
		workflowIDs = append(workflowIDs, workflowID)
	}
	slices.Sort(workflowIDs)
	for _, workflowID := range workflowIDs {
		priorCanonical := prior.OperatorWorkflows[workflowID]
		currentCanonical, ok := current.OperatorWorkflows[workflowID]
		if !ok {
			return fmt.Errorf("%s removed historical v1 workflow %s from %s", currentLabel, workflowID, priorLabel)
		}
		if currentCanonical != priorCanonical {
			return fmt.Errorf("%s rewrote historical v1 workflow %s from %s: got %q, want %q", currentLabel, workflowID, priorLabel, currentCanonical, priorCanonical)
		}
	}
	return nil
}

func publicContractSets(contract publicContract) []namedContractSet {
	return []namedContractSet{
		{"exit codes", contract.ExitCodes},
		{"output-line prefixes", contract.OutputLinePrefixes},
		{"session-show prefixes", contract.SessionShowPrefixes},
		{"session-record fields", contract.SessionRecordFields},
		{"session-export fields", contract.SessionExportFields},
		{"injection tables", contract.InjectionTables},
		{"injection scalar root keys", contract.InjectionScalarRootKeys},
	}
}

func optionalStringSliceTable(document map[string]any, label, table, key string) ([]string, error) {
	rawTable, ok := document[table].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s must define a [%s] table", label, table)
	}
	values, found, err := MustStringSlice(rawTable[key])
	if err != nil {
		return nil, fmt.Errorf("%s [%s] %s: %w", label, table, key, err)
	}
	if !found {
		return nil, nil
	}
	if len(values) == 0 {
		return nil, fmt.Errorf("%s [%s] must not define an empty %s array", label, table, key)
	}
	return values, nil
}

func v1ContractHistoryLogArgs(relativePath string) []string {
	return []string{
		"-c", "log.showSignature=false",
		"log", "--full-history", "--format=%H", "--", relativePath,
	}
}

func missingFrozenValues(frozen, current []string) []string {
	currentSet := make(map[string]struct{}, len(current))
	for _, value := range current {
		currentSet[value] = struct{}{}
	}
	missing := make([]string, 0)
	for _, value := range frozen {
		if _, ok := currentSet[value]; !ok {
			missing = append(missing, value)
		}
	}
	slices.Sort(missing)
	return missing
}

func defaultV1ContractFreezePath(rootDir string) string {
	return filepath.Join(rootDir, "policy", "v1-contract-freeze.toml")
}
