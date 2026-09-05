// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

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

const gitHubAPIVersion = "2026-03-10"

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

type ActionsPolicy struct {
	AllowOnlyPinnedVerifiedOrExplicitlyTrustedActions bool
	DefaultWorkflowTokenPermissions                   string
}

type ReleaseAssetsPolicy struct {
	ImmutableGitHubReleases bool
}

func GitHubActionsPolicy(policy map[string]any, policyPath string) (ActionsPolicy, error) {
	rawPolicy, ok := policy["actions_policy"].(map[string]any)
	if !ok {
		return ActionsPolicy{}, fmt.Errorf("%s must define actions_policy as an explicit hosted-control table", policyPath)
	}
	allowOnlyPinned, ok := rawPolicy["allow_only_pinned_verified_or_explicitly_trusted_actions"].(bool)
	if !ok || !allowOnlyPinned {
		return ActionsPolicy{}, fmt.Errorf("%s must set actions_policy.allow_only_pinned_verified_or_explicitly_trusted_actions = true", policyPath)
	}
	defaultWorkflowTokenPermissions, ok := rawPolicy["default_workflow_token_permissions"].(string)
	if !ok || defaultWorkflowTokenPermissions != "read" {
		return ActionsPolicy{}, fmt.Errorf("%s must set actions_policy.default_workflow_token_permissions = \"read\"", policyPath)
	}
	return ActionsPolicy{
		AllowOnlyPinnedVerifiedOrExplicitlyTrustedActions: allowOnlyPinned,
		DefaultWorkflowTokenPermissions:                   defaultWorkflowTokenPermissions,
	}, nil
}

func ReleaseAssets(policy map[string]any, policyPath string) (ReleaseAssetsPolicy, error) {
	rawPolicy, ok := policy["release_assets"].(map[string]any)
	if !ok {
		return ReleaseAssetsPolicy{}, fmt.Errorf("%s must define release_assets as an explicit hosted-control table", policyPath)
	}
	immutableReleases, ok := rawPolicy["immutable_github_releases"].(bool)
	if !ok || !immutableReleases {
		return ReleaseAssetsPolicy{}, fmt.Errorf("%s must set release_assets.immutable_github_releases = true", policyPath)
	}
	return ReleaseAssetsPolicy{ImmutableGitHubReleases: immutableReleases}, nil
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
			secrets, present, err := MustStringSlice(rawSecrets)
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
			branches, present, err := MustStringSlice(rawDeploymentBranches)
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
			tags, present, err := MustStringSlice(rawDeploymentTags)
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

	release, ok := environments["release"]
	if !ok {
		return errors.New("policy/github-hosted-controls.toml must declare workflow_environment.release")
	}
	if len(release.RequiredSecrets) != 0 {
		return errors.New("policy/github-hosted-controls.toml must not declare secrets for workflow_environment.release")
	}
	if len(release.Variables) != 0 {
		return errors.New("policy/github-hosted-controls.toml must not declare public variables for workflow_environment.release")
	}
	if !release.HasAllowAdminBypass || release.AllowAdminBypass {
		return errors.New("policy/github-hosted-controls.toml must set workflow_environment.release.allow_admin_bypass = false")
	}
	if release.HasDeploymentBranches || len(release.DeploymentBranches) != 0 {
		return errors.New("policy/github-hosted-controls.toml must not set workflow_environment.release.deployment_branches")
	}
	if !release.HasDeploymentTags || len(release.DeploymentTags) != 1 || release.DeploymentTags[0] != "v*" {
		return errors.New("policy/github-hosted-controls.toml must set workflow_environment.release.deployment_tags = [\"v*\"]")
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
	summary, err := readRulesetSummary(filepath.Join(tmpDir, "rulesets-summary.json"))
	if err != nil {
		return err
	}

	details := make([]any, 0, len(summary))
	for _, raw := range summary {
		detail, err := fetchRuleset(repo, raw)
		if err != nil {
			return err
		}
		details = append(details, detail)
	}
	return writeJSONFile(filepath.Join(tmpDir, "rulesets.json"), details)
}

func readRulesetSummary(path string) ([]any, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	values, err := decodeHostedControlPages(bytes.NewReader(content))
	if err != nil {
		return nil, err
	}
	if len(values) != 1 {
		return nil, errors.New("ruleset summary must contain exactly one JSON value")
	}
	summary, ok := values[0].([]any)
	if !ok {
		return nil, errors.New("ruleset summary must be an array")
	}
	return summary, nil
}

func fetchRuleset(repo string, raw any) (any, error) {
	id, err := rulesetSummaryID(raw)
	if err != nil {
		return nil, err
	}
	rulesetID := strconv.FormatInt(id, 10)
	endpoint := fmt.Sprintf("repos/%s/rulesets/%s", repo, rulesetID)
	cmd := exec.Command("gh", "api", "--hostname", "github.com", "-H", "X-GitHub-Api-Version: "+gitHubAPIVersion, endpoint)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("gh api %s: %w (stderr: %s)", endpoint, err, strings.TrimSpace(stderr.String()))
	}
	var detail any
	if err := json.Unmarshal(output, &detail); err != nil {
		return nil, fmt.Errorf("gh api %s: parse JSON: %w", endpoint, err)
	}
	return detail, nil
}

func rulesetSummaryID(raw any) (int64, error) {
	entry, ok := raw.(map[string]any)
	if !ok {
		return 0, fmt.Errorf("unexpected ruleset summary entry: %v", raw)
	}
	number, ok := entry["id"].(json.Number)
	if !ok {
		return 0, fmt.Errorf("unexpected ruleset summary id: %v", entry)
	}
	value, err := number.Int64()
	if err != nil {
		return 0, fmt.Errorf("unexpected ruleset summary id: %v", entry)
	}
	if value <= 0 {
		return 0, fmt.Errorf("unexpected ruleset summary id: %v", entry)
	}
	return value, nil
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

func rejectReleaseAdminBypass(releaseEnv, adminBypassRule map[string]any, repo string) error {
	if bypass, _ := releaseEnv["can_admins_bypass"].(bool); bypass {
		return fmt.Errorf("release environment on %s must not allow administrator bypass", repo)
	}
	if adminBypassRule != nil {
		if enabled, _ := adminBypassRule["enabled"].(bool); enabled {
			return fmt.Errorf("release environment on %s must not allow administrator bypass", repo)
		}
	}
	return nil
}

func VerifyGitHubHostedControls(tmpDir, repo, policyPath string) error {
	inputs, err := loadHostedControlInputs(tmpDir, policyPath)
	if err != nil {
		return err
	}
	repoMeta := inputs.repoMeta
	directCollaborators := inputs.directCollaborators
	policy := inputs.policy

	owner, _ := repoMeta["owner"].(map[string]any)
	ownerLogin, _ := owner["login"].(string)
	ownerType, _ := owner["type"].(string)
	requireSingleOwnerCollaborator := func(mode string) error {
		return verifySingleOwnerCollaborator(directCollaborators, ownerLogin, mode, repo)
	}

	branchIntegrityPolicy, ok := policy["branch_integrity"].(map[string]any)
	if !ok {
		return fmt.Errorf("%s must define branch_integrity as a table with explicit booleans", policyPath)
	}
	for _, key := range []string{"require_signed_commits", "block_force_pushes", "block_deletions"} {
		if value, ok := branchIntegrityPolicy[key].(bool); !ok || !value {
			return fmt.Errorf("%s must set branch_integrity.%s = true", policyPath, key)
		}
	}
	branchReviewPolicy, _ := policy["branch_review"].(map[string]any)
	branchReviewMode, _ := branchReviewPolicy["mode"].(string)
	if branchReviewMode == "" {
		branchReviewMode = "review-gated"
	}
	if branchReviewMode != "review-gated" && branchReviewMode != "single-owner-public-pr" && branchReviewMode != "single-owner-private-pr" {
		return fmt.Errorf("%s must set branch_review.mode to 'review-gated', 'single-owner-public-pr', or 'single-owner-private-pr'", policyPath)
	}
	releasePolicy, _ := policy["release_environment"].(map[string]any)
	releaseMode, _ := releasePolicy["mode"].(string)
	if releaseMode == "" {
		releaseMode = "review-gated"
	}
	if releaseMode != "review-gated" && releaseMode != "single-owner-public" && releaseMode != "single-owner-private" && releaseMode != "plan-limited-private" {
		return fmt.Errorf("%s must set release_environment.mode to 'review-gated', 'single-owner-public', 'single-owner-private', or 'plan-limited-private'", policyPath)
	}
	actionsPolicy, err := GitHubActionsPolicy(policy, policyPath)
	if err != nil {
		return err
	}
	releaseAssetsPolicy, err := ReleaseAssets(policy, policyPath)
	if err != nil {
		return err
	}
	expectedContexts, err := requireStringSliceTable(policy, "required_status_checks", "contexts", policyPath)
	if err != nil {
		return err
	}
	if len(expectedContexts) == 0 {
		return fmt.Errorf("%s must define required_status_checks.contexts as a non-empty array", policyPath)
	}
	expectedRepoVariables, err := RepositoryVariables(policy, policyPath)
	if err != nil {
		return err
	}
	expectedWorkflowEnvironments, err := WorkflowEnvironments(policy, policyPath)
	if err != nil {
		return err
	}

	if err := verifyGitHubActionsControls(inputs, actionsPolicy, releaseAssetsPolicy, repo); err != nil {
		return err
	}
	if err := verifyHostedRulesetControls(inputs, branchReviewMode, ownerType, expectedContexts, requireSingleOwnerCollaborator, repo); err != nil {
		return err
	}

	if err := verifyHostedRepositoryVariables(inputs.actionsVariables, expectedRepoVariables, repo); err != nil {
		return err
	}
	if err := verifyHostedWorkflowEnvironments(tmpDir, inputs.environmentsIndex, expectedWorkflowEnvironments, repo); err != nil {
		return err
	}

	return verifyReleaseEnvironmentControls(releaseMode, inputs, ownerLogin, ownerType, repo)
}

func verifyWorkflowEnvironmentDeploymentPolicy(repo, environmentName string, environmentPolicy WorkflowEnvironmentPolicy, environmentMeta, environmentBranchPolicies map[string]any) error {
	if environmentPolicy.HasAllowAdminBypass {
		bypass, ok := environmentMeta["can_admins_bypass"].(bool)
		if !ok || bypass != environmentPolicy.AllowAdminBypass {
			return fmt.Errorf("workflow environment %s/%s must set can_admins_bypass=%t", repo, environmentName, environmentPolicy.AllowAdminBypass)
		}
	}
	if !environmentPolicy.HasDeploymentBranches && !environmentPolicy.HasDeploymentTags {
		return nil
	}

	deploymentBranchPolicy, ok := environmentMeta["deployment_branch_policy"].(map[string]any)
	if !ok {
		return fmt.Errorf("workflow environment %s/%s must define deployment branch policies", repo, environmentName)
	}
	if protectedBranches, _ := deploymentBranchPolicy["protected_branches"].(bool); protectedBranches {
		return fmt.Errorf("workflow environment %s/%s must not rely on protected-branch deployment policy", repo, environmentName)
	}
	if customBranchPolicies, _ := deploymentBranchPolicy["custom_branch_policies"].(bool); !customBranchPolicies {
		return fmt.Errorf("workflow environment %s/%s must use explicit deployment branch policies", repo, environmentName)
	}

	actualBranches := make([]string, 0)
	actualTags := make([]string, 0)
	if branchPolicies, ok := environmentBranchPolicies["branch_policies"].([]any); ok {
		for _, raw := range branchPolicies {
			entry, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if name, _ := entry["name"].(string); name != "" {
				switch typ, _ := entry["type"].(string); typ {
				case "", "branch":
					actualBranches = append(actualBranches, name)
				case "tag":
					actualTags = append(actualTags, name)
				}
			}
		}
	}
	slices.Sort(actualBranches)
	slices.Sort(actualTags)
	if !slices.Equal(actualBranches, environmentPolicy.DeploymentBranches) {
		return fmt.Errorf("workflow environment %s/%s must restrict deployment branches to %s", repo, environmentName, strings.Join(environmentPolicy.DeploymentBranches, ", "))
	}
	if !slices.Equal(actualTags, environmentPolicy.DeploymentTags) {
		return fmt.Errorf("workflow environment %s/%s must restrict deployment tags to %s", repo, environmentName, strings.Join(environmentPolicy.DeploymentTags, ", "))
	}
	return nil
}

func requireStringSliceTable(root map[string]any, tableName, key, sourcePath string) ([]string, error) {
	table, ok := root[tableName].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s must define %s.%s as a non-empty array", sourcePath, tableName, key)
	}
	values, ok, err := MustStringSlice(table[key])
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("%s must define %s.%s as a non-empty array", sourcePath, tableName, key)
	}
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf("%s must define %s.%s as a non-empty array", sourcePath, tableName, key)
		}
	}
	return values, nil
}

// readJSONFile / writeJSONFile live in core.go.
