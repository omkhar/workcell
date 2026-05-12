// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package hostedcontrols

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/omkhar/workcell/internal/tomlsubset"
)

type WorkflowEnvironmentPolicy struct {
	Variables             map[string]string
	RequiredSecrets       []string
	AllowAdminBypass      bool
	HasAllowAdminBypass   bool
	DeploymentBranches    []string
	HasDeploymentBranches bool
	DeploymentTags        []string
	HasDeploymentTags     bool
}

func RepositoryVariables(policy map[string]any, policyPath string) (map[string]any, error) {
	expectedRepoVariables, ok := policy["repository_variables"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s must define repository_variables as a table of exact expected values", policyPath)
	}
	for name, value := range expectedRepoVariables {
		if strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("%s repository_variables entries must map non-empty names to exact string values", policyPath)
		}
		if _, ok := value.(string); !ok {
			return nil, fmt.Errorf("%s repository_variables entries must map non-empty names to exact string values", policyPath)
		}
	}
	for _, requiredName := range []string{
		"WORKCELL_RELEASE_NO_ATTEST",
		"WORKCELL_ENABLE_PRIVATE_GITHUB_ATTESTATIONS",
	} {
		if _, ok := expectedRepoVariables[requiredName]; !ok {
			return nil, fmt.Errorf("%s must declare %s in repository_variables", policyPath, requiredName)
		}
	}
	return expectedRepoVariables, nil
}

func ValidateCanonicalRepositoryVariables(policy map[string]any, policyPath string) error {
	repositoryVariables, err := RepositoryVariables(policy, policyPath)
	if err != nil {
		return err
	}
	if value, _ := repositoryVariables["WORKCELL_RELEASE_NO_ATTEST"].(string); value != "false" {
		return errors.New("policy/github-hosted-controls.toml must require WORKCELL_RELEASE_NO_ATTEST = \"false\"")
	}
	if value, _ := repositoryVariables["WORKCELL_ENABLE_PRIVATE_GITHUB_ATTESTATIONS"].(string); value != "false" {
		return errors.New("policy/github-hosted-controls.toml must require WORKCELL_ENABLE_PRIVATE_GITHUB_ATTESTATIONS = \"false\"")
	}
	return nil
}

func WorkflowEnvironments(policy map[string]any, policyPath string) (map[string]WorkflowEnvironmentPolicy, error) {
	rawEnvironments, ok := policy["workflow_environment"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s must define workflow_environment as a table of named environment contracts", policyPath)
	}

	environments := make(map[string]WorkflowEnvironmentPolicy, len(rawEnvironments))
	for environmentName, rawEntry := range rawEnvironments {
		if strings.TrimSpace(environmentName) == "" {
			return nil, fmt.Errorf("%s workflow_environment entries must use non-empty environment names", policyPath)
		}
		entry, ok := rawEntry.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s workflow_environment.%s must be a table", policyPath, environmentName)
		}

		variables := map[string]string{}
		if rawVariables, ok := entry["variables"]; ok {
			variableTable, ok := rawVariables.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("%s workflow_environment.%s.variables must be a table of exact expected values", policyPath, environmentName)
			}
			for name, rawValue := range variableTable {
				value, ok := rawValue.(string)
				if strings.TrimSpace(name) == "" || !ok || strings.TrimSpace(value) == "" {
					return nil, fmt.Errorf("%s workflow_environment.%s.variables must map non-empty names to exact string values", policyPath, environmentName)
				}
				variables[name] = value
			}
		}

		requiredSecrets := []string{}
		if rawSecrets, ok := entry["required_secrets"]; ok {
			secrets, present, err := mustStringSlice(rawSecrets)
			if err != nil {
				return nil, fmt.Errorf("%s workflow_environment.%s.required_secrets: %w", policyPath, environmentName, err)
			}
			if !present {
				return nil, fmt.Errorf("%s workflow_environment.%s.required_secrets must be an array of secret names", policyPath, environmentName)
			}
			for _, secretName := range secrets {
				if strings.TrimSpace(secretName) == "" {
					return nil, fmt.Errorf("%s workflow_environment.%s.required_secrets must be an array of non-empty secret names", policyPath, environmentName)
				}
			}
			requiredSecrets = append(requiredSecrets, secrets...)
			slices.Sort(requiredSecrets)
		}

		allowAdminBypass := false
		hasAllowAdminBypass := false
		if rawAllowAdminBypass, ok := entry["allow_admin_bypass"]; ok {
			value, ok := rawAllowAdminBypass.(bool)
			if !ok {
				return nil, fmt.Errorf("%s workflow_environment.%s.allow_admin_bypass must be a boolean", policyPath, environmentName)
			}
			allowAdminBypass = value
			hasAllowAdminBypass = true
		}

		deploymentBranches := []string{}
		hasDeploymentBranches := false
		if rawDeploymentBranches, ok := entry["deployment_branches"]; ok {
			branches, present, err := mustStringSlice(rawDeploymentBranches)
			if err != nil {
				return nil, fmt.Errorf("%s workflow_environment.%s.deployment_branches: %w", policyPath, environmentName, err)
			}
			if !present {
				return nil, fmt.Errorf("%s workflow_environment.%s.deployment_branches must be an array of branch names", policyPath, environmentName)
			}
			for _, branchName := range branches {
				if strings.TrimSpace(branchName) == "" {
					return nil, fmt.Errorf("%s workflow_environment.%s.deployment_branches must be an array of non-empty branch names", policyPath, environmentName)
				}
			}
			deploymentBranches = append(deploymentBranches, branches...)
			slices.Sort(deploymentBranches)
			hasDeploymentBranches = true
		}
		deploymentTags := []string{}
		hasDeploymentTags := false
		if rawDeploymentTags, ok := entry["deployment_tags"]; ok {
			tags, present, err := mustStringSlice(rawDeploymentTags)
			if err != nil {
				return nil, fmt.Errorf("%s workflow_environment.%s.deployment_tags: %w", policyPath, environmentName, err)
			}
			if !present {
				return nil, fmt.Errorf("%s workflow_environment.%s.deployment_tags must be an array of tag patterns", policyPath, environmentName)
			}
			for _, tagPattern := range tags {
				if strings.TrimSpace(tagPattern) == "" {
					return nil, fmt.Errorf("%s workflow_environment.%s.deployment_tags must be an array of non-empty tag patterns", policyPath, environmentName)
				}
			}
			deploymentTags = append(deploymentTags, tags...)
			slices.Sort(deploymentTags)
			hasDeploymentTags = true
		}

		environments[environmentName] = WorkflowEnvironmentPolicy{
			Variables:             variables,
			RequiredSecrets:       requiredSecrets,
			AllowAdminBypass:      allowAdminBypass,
			HasAllowAdminBypass:   hasAllowAdminBypass,
			DeploymentBranches:    deploymentBranches,
			HasDeploymentBranches: hasDeploymentBranches,
			DeploymentTags:        deploymentTags,
			HasDeploymentTags:     hasDeploymentTags,
		}
	}
	return environments, nil
}

func ValidateCanonicalWorkflowEnvironments(policy map[string]any, policyPath string) error {
	environments, err := WorkflowEnvironments(policy, policyPath)
	if err != nil {
		return err
	}

	hostedControlsAudit, ok := environments["hosted-controls-audit"]
	if !ok {
		return errors.New("policy/github-hosted-controls.toml must declare workflow_environment.hosted-controls-audit")
	}
	if len(hostedControlsAudit.Variables) != 0 {
		return errors.New("policy/github-hosted-controls.toml must not declare public variables for workflow_environment.hosted-controls-audit")
	}
	if len(hostedControlsAudit.RequiredSecrets) != 1 || hostedControlsAudit.RequiredSecrets[0] != "WORKCELL_HOSTED_CONTROLS_TOKEN" {
		return errors.New("policy/github-hosted-controls.toml must require only WORKCELL_HOSTED_CONTROLS_TOKEN for workflow_environment.hosted-controls-audit")
	}
	if !hostedControlsAudit.HasAllowAdminBypass || hostedControlsAudit.AllowAdminBypass {
		return errors.New("policy/github-hosted-controls.toml must set workflow_environment.hosted-controls-audit.allow_admin_bypass = false")
	}
	if !hostedControlsAudit.HasDeploymentBranches || len(hostedControlsAudit.DeploymentBranches) != 1 || hostedControlsAudit.DeploymentBranches[0] != "main" {
		return errors.New("policy/github-hosted-controls.toml must set workflow_environment.hosted-controls-audit.deployment_branches = [\"main\"]")
	}
	if !hostedControlsAudit.HasDeploymentTags || len(hostedControlsAudit.DeploymentTags) != 1 || hostedControlsAudit.DeploymentTags[0] != "v*" {
		return errors.New("policy/github-hosted-controls.toml must set workflow_environment.hosted-controls-audit.deployment_tags = [\"v*\"]")
	}

	upstreamRefresh, ok := environments["upstream-refresh"]
	if !ok {
		return errors.New("policy/github-hosted-controls.toml must declare workflow_environment.upstream-refresh")
	}
	if len(upstreamRefresh.RequiredSecrets) != 0 {
		return errors.New("policy/github-hosted-controls.toml must not declare secrets for workflow_environment.upstream-refresh")
	}
	if len(upstreamRefresh.Variables) != 0 {
		return errors.New("policy/github-hosted-controls.toml must not declare public variables for workflow_environment.upstream-refresh")
	}
	if !upstreamRefresh.HasAllowAdminBypass || upstreamRefresh.AllowAdminBypass {
		return errors.New("policy/github-hosted-controls.toml must set workflow_environment.upstream-refresh.allow_admin_bypass = false")
	}
	if !upstreamRefresh.HasDeploymentBranches || len(upstreamRefresh.DeploymentBranches) != 1 || upstreamRefresh.DeploymentBranches[0] != "main" {
		return errors.New("policy/github-hosted-controls.toml must set workflow_environment.upstream-refresh.deployment_branches = [\"main\"]")
	}
	if upstreamRefresh.HasDeploymentTags || len(upstreamRefresh.DeploymentTags) != 0 {
		return errors.New("policy/github-hosted-controls.toml must not set workflow_environment.upstream-refresh.deployment_tags")
	}
	return nil
}

func EnvironmentNames(policyPath string) ([]string, error) {
	content, err := os.ReadFile(policyPath)
	if err != nil {
		return nil, err
	}
	policy, err := tomlsubset.Parse(string(content), policyPath)
	if err != nil {
		return nil, err
	}
	environments, err := WorkflowEnvironments(policy, policyPath)
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(environments))
	for environmentName := range environments {
		names = append(names, environmentName)
	}
	slices.Sort(names)
	return names, nil
}

func EnvironmentArtifactName(environmentName string) string {
	return strings.ReplaceAll(url.QueryEscape(environmentName), "+", "%20")
}

func FetchRulesets(tmpDir, repo string) error {
	var summary []any
	if err := readJSONFile(filepath.Join(tmpDir, "rulesets-summary.json"), &summary); err != nil {
		return err
	}

	details := make([]any, 0, len(summary))
	for _, raw := range summary {
		entry, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("unexpected ruleset summary entry: %v", raw)
		}
		idValue, ok := entry["id"].(float64)
		if !ok {
			return fmt.Errorf("unexpected ruleset summary id: %v", entry)
		}
		rulesetID := strconv.FormatInt(int64(idValue), 10)
		cmd := exec.Command("gh", "api", fmt.Sprintf("repos/%s/rulesets/%s", repo, rulesetID))
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		output, err := cmd.Output()
		if err != nil {
			return fmt.Errorf("gh api repos/%s/rulesets/%s: %w (stderr: %s)", repo, rulesetID, err, strings.TrimSpace(stderr.String()))
		}
		var detail any
		if err := json.Unmarshal(output, &detail); err != nil {
			return fmt.Errorf("gh api repos/%s/rulesets/%s: parse JSON: %w", repo, rulesetID, err)
		}
		details = append(details, detail)
	}
	return writeJSONFile(filepath.Join(tmpDir, "rulesets.json"), details)
}

func UnexpectedEnvironmentVariableNames(actual map[string]any, expected map[string]string) []string {
	unexpected := make([]string, 0)
	for name := range actual {
		if _, ok := expected[name]; !ok {
			unexpected = append(unexpected, name)
		}
	}
	slices.Sort(unexpected)
	return unexpected
}

func UnexpectedEnvironmentSecretNames(actual map[string]struct{}, expected []string) []string {
	expectedSet := make(map[string]struct{}, len(expected))
	for _, name := range expected {
		expectedSet[name] = struct{}{}
	}
	unexpected := make([]string, 0)
	for name := range actual {
		if _, ok := expectedSet[name]; !ok {
			unexpected = append(unexpected, name)
		}
	}
	slices.Sort(unexpected)
	return unexpected
}

func mustStringSlice(value any) ([]string, bool, error) {
	raw, ok := value.([]any)
	if !ok {
		return nil, false, nil
	}
	result := make([]string, 0, len(raw))
	for _, item := range raw {
		s, ok := item.(string)
		if !ok {
			return nil, false, errors.New("array value must contain only strings")
		}
		result = append(result, s)
	}
	return result, true, nil
}

func readJSONFile(path string, target any) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(content, target)
}

func writeJSONFile(path string, value any) error {
	content, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	content = append(content, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, content, 0o644)
}
