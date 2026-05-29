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

func VerifyGitHubHostedControls(tmpDir, repo, policyPath string) error {
	var repoMeta map[string]any
	if err := readJSONFile(filepath.Join(tmpDir, "repo.json"), &repoMeta); err != nil {
		return err
	}
	var actionsPermissions map[string]any
	if err := readJSONFile(filepath.Join(tmpDir, "actions-permissions.json"), &actionsPermissions); err != nil {
		return err
	}
	var actionsVariables map[string]any
	if err := readJSONFile(filepath.Join(tmpDir, "actions-variables.json"), &actionsVariables); err != nil {
		return err
	}
	var environmentsIndex map[string]any
	if err := readJSONFile(filepath.Join(tmpDir, "environments.json"), &environmentsIndex); err != nil {
		return err
	}
	var directCollaborators []any
	if err := readJSONFile(filepath.Join(tmpDir, "collaborators-direct.json"), &directCollaborators); err != nil {
		return err
	}
	var rulesets []any
	if err := readJSONFile(filepath.Join(tmpDir, "rulesets.json"), &rulesets); err != nil {
		return err
	}
	var releaseEnv map[string]any
	if err := readJSONFile(filepath.Join(tmpDir, "environment-release.json"), &releaseEnv); err != nil {
		return err
	}
	policyContent, err := os.ReadFile(policyPath)
	if err != nil {
		return err
	}
	policy, err := tomlsubset.Parse(string(policyContent), policyPath)
	if err != nil {
		return err
	}

	owner, _ := repoMeta["owner"].(map[string]any)
	ownerLogin, _ := owner["login"].(string)
	ownerType, _ := owner["type"].(string)
	requireSingleOwnerCollaborator := func(mode string) error {
		if len(directCollaborators) != 1 {
			return fmt.Errorf("%s on %s requires exactly one direct collaborator", mode, repo)
		}
		collaborator, _ := directCollaborators[0].(map[string]any)
		if login, _ := collaborator["login"].(string); login != ownerLogin {
			return fmt.Errorf("%s on %s requires the owner to be the only direct collaborator", mode, repo)
		}
		permissions, _ := collaborator["permissions"].(map[string]any)
		if admin, _ := permissions["admin"].(bool); !admin {
			return fmt.Errorf("%s on %s requires the owner to retain admin permission", mode, repo)
		}
		return nil
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

	if enabled, _ := actionsPermissions["enabled"].(bool); !enabled {
		return fmt.Errorf("GitHub Actions must be enabled on %s", repo)
	}
	if required, _ := actionsPermissions["sha_pinning_required"].(bool); !required {
		return fmt.Errorf("GitHub Actions SHA pinning must be required on %s", repo)
	}

	activeRulesets := make([]map[string]any, 0)
	for _, raw := range rulesets {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if enforcement, _ := entry["enforcement"].(string); enforcement == "active" {
			activeRulesets = append(activeRulesets, entry)
		}
	}
	if len(activeRulesets) == 0 {
		return fmt.Errorf("no active rulesets found on %s", repo)
	}

	hasRefInclude := func(ruleset map[string]any, expected string) bool {
		conditions, _ := ruleset["conditions"].(map[string]any)
		refName, _ := conditions["ref_name"].(map[string]any)
		include, _ := refName["include"].([]any)
		for _, raw := range include {
			if s, _ := raw.(string); s == expected {
				return true
			}
		}
		return false
	}
	hasRule := func(ruleset map[string]any, ruleType string) map[string]any {
		rules, _ := ruleset["rules"].([]any)
		for _, raw := range rules {
			entry, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if typ, _ := entry["type"].(string); typ == ruleType {
				return entry
			}
		}
		return nil
	}
	requireBypassShape := func(ruleset map[string]any, actorType, bypassMode string, requireNonEmpty bool) error {
		actors, _ := ruleset["bypass_actors"].([]any)
		if requireNonEmpty && len(actors) == 0 {
			return fmt.Errorf("ruleset %v on %s must declare an explicit bypass actor", ruleset["name"], repo)
		}
		for _, raw := range actors {
			entry, ok := raw.(map[string]any)
			if !ok {
				return fmt.Errorf("ruleset %v on %s must only use %s/%s bypass actors", ruleset["name"], repo, actorType, bypassMode)
			}
			if at, _ := entry["actor_type"].(string); at != actorType {
				return fmt.Errorf("ruleset %v on %s must only use %s/%s bypass actors", ruleset["name"], repo, actorType, bypassMode)
			}
			if bm, _ := entry["bypass_mode"].(string); bm != bypassMode {
				return fmt.Errorf("ruleset %v on %s must only use %s/%s bypass actors", ruleset["name"], repo, actorType, bypassMode)
			}
		}
		return nil
	}

	var branchIntegrity, branchReview, branchStatusChecks, tagRelease map[string]any
	for _, ruleset := range activeRulesets {
		target, _ := ruleset["target"].(string)
		if target == "branch" && hasRefInclude(ruleset, "~DEFAULT_BRANCH") {
			if hasRule(ruleset, "required_signatures") != nil && hasRule(ruleset, "non_fast_forward") != nil && hasRule(ruleset, "deletion") != nil {
				branchIntegrity = ruleset
			}
			if hasRule(ruleset, "pull_request") != nil {
				branchReview = ruleset
			}
			if hasRule(ruleset, "required_status_checks") != nil {
				branchStatusChecks = ruleset
			}
		}
		if target == "tag" && hasRefInclude(ruleset, "refs/tags/v*") {
			if hasRule(ruleset, "creation") != nil && hasRule(ruleset, "update") != nil && hasRule(ruleset, "deletion") != nil {
				tagRelease = ruleset
			}
		}
	}
	if branchIntegrity == nil {
		return fmt.Errorf("missing active default-branch integrity ruleset on %s with required_signatures, non_fast_forward, and deletion", repo)
	}
	if branchReview == nil {
		return fmt.Errorf("missing active default-branch review ruleset on %s with a pull_request rule", repo)
	}
	if branchStatusChecks == nil {
		return fmt.Errorf("missing active default-branch status-check ruleset on %s with a required_status_checks rule", repo)
	}
	if actors, _ := branchIntegrity["bypass_actors"].([]any); len(actors) > 0 {
		return fmt.Errorf("default-branch integrity ruleset on %s must not declare bypass actors", repo)
	}
	if err := requireBypassShape(branchReview, "RepositoryRole", "pull_request", false); err != nil {
		return err
	}
	if tagRelease == nil {
		return fmt.Errorf("missing active release-tag ruleset on %s for refs/tags/v* with creation/update/deletion protection", repo)
	}
	if err := requireBypassShape(tagRelease, "RepositoryRole", "always", true); err != nil {
		return err
	}

	pullRequestRule := hasRule(branchReview, "pull_request")
	parameters, _ := pullRequestRule["parameters"].(map[string]any)
	if branchReviewMode == "review-gated" {
		if count, _ := parameters["required_approving_review_count"].(float64); count < 1 {
			return fmt.Errorf("default-branch review ruleset on %s must require at least one approving review", repo)
		}
		if required, _ := parameters["require_code_owner_review"].(bool); !required {
			return fmt.Errorf("default-branch review ruleset on %s must require code owner review", repo)
		}
		if resolved, _ := parameters["required_review_thread_resolution"].(bool); !resolved {
			return fmt.Errorf("default-branch review ruleset on %s must require resolved review threads", repo)
		}
	} else {
		if branchReviewMode == "single-owner-private-pr" {
			if private, _ := repoMeta["private"].(bool); !private {
				return fmt.Errorf("branch review mode 'single-owner-private-pr' on %s is only valid for private repositories", repo)
			}
			if ownerType != "User" {
				return fmt.Errorf("branch review mode 'single-owner-private-pr' on %s is only valid for user-owned repositories", repo)
			}
			if err := requireSingleOwnerCollaborator("branch review mode 'single-owner-private-pr'"); err != nil {
				return err
			}
		} else {
			if private, _ := repoMeta["private"].(bool); private {
				return fmt.Errorf("branch review mode 'single-owner-public-pr' on %s is only valid for public repositories", repo)
			}
			if ownerType != "User" {
				return fmt.Errorf("branch review mode 'single-owner-public-pr' on %s is only valid for user-owned repositories", repo)
			}
			if err := requireSingleOwnerCollaborator("branch review mode 'single-owner-public-pr'"); err != nil {
				return err
			}
		}
		if count, _ := parameters["required_approving_review_count"].(float64); count != 0 {
			return fmt.Errorf("default-branch review ruleset on %s must require zero approving reviews in %s mode", repo, branchReviewMode)
		}
		if required, _ := parameters["require_code_owner_review"].(bool); required {
			return fmt.Errorf("default-branch review ruleset on %s must not require code owner review in %s mode", repo, branchReviewMode)
		}
		if lastPushApproval, _ := parameters["require_last_push_approval"].(bool); lastPushApproval {
			return fmt.Errorf("default-branch review ruleset on %s must not require last-push approval in %s mode", repo, branchReviewMode)
		}
		resolved, _ := parameters["required_review_thread_resolution"].(bool)
		if branchReviewMode == "single-owner-public-pr" && !resolved {
			return fmt.Errorf("default-branch review ruleset on %s must require resolved review threads in single-owner-public-pr mode", repo)
		}
		if branchReviewMode == "single-owner-private-pr" && resolved {
			return fmt.Errorf("default-branch review ruleset on %s must not require resolved review threads in single-owner-private-pr mode", repo)
		}
	}

	statusRule := hasRule(branchStatusChecks, "required_status_checks")
	statusParameters, _ := statusRule["parameters"].(map[string]any)
	if strict, _ := statusParameters["strict_required_status_checks_policy"].(bool); !strict {
		return fmt.Errorf("default-branch status-check ruleset on %s must require strict status checks", repo)
	}
	requiredStatusChecks, _ := statusParameters["required_status_checks"].([]any)
	actualStatus := map[string]struct{}{}
	for _, raw := range requiredStatusChecks {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if context, _ := entry["context"].(string); context != "" {
			actualStatus[context] = struct{}{}
		}
	}
	missingStatus := make([]string, 0)
	for _, expected := range expectedContexts {
		if _, ok := actualStatus[expected]; !ok {
			missingStatus = append(missingStatus, expected)
		}
	}
	slices.Sort(missingStatus)
	if len(missingStatus) > 0 {
		return fmt.Errorf("default-branch status-check ruleset on %s is missing required contexts: %s", repo, strings.Join(missingStatus, ", "))
	}

	actualRepoVariables := map[string]any{}
	if variables, ok := actionsVariables["variables"].([]any); ok {
		for _, raw := range variables {
			entry, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			name, _ := entry["name"].(string)
			if name != "" {
				actualRepoVariables[name] = entry["value"]
			}
		}
	}
	missingRepoVariables := make([]string, 0)
	for name := range expectedRepoVariables {
		if _, ok := actualRepoVariables[name]; !ok {
			missingRepoVariables = append(missingRepoVariables, name)
		}
	}
	slices.Sort(missingRepoVariables)
	if len(missingRepoVariables) > 0 {
		return fmt.Errorf("repository variables missing on %s: %s", repo, strings.Join(missingRepoVariables, ", "))
	}
	wrongRepoVariables := make([]string, 0)
	for name, expectedValue := range expectedRepoVariables {
		if actualRepoVariables[name] != expectedValue {
			wrongRepoVariables = append(wrongRepoVariables, fmt.Sprintf("%s=%#v (expected %#v)", name, actualRepoVariables[name], expectedValue))
		}
	}
	slices.Sort(wrongRepoVariables)
	if len(wrongRepoVariables) > 0 {
		return fmt.Errorf("repository variables on %s do not match policy: %s", repo, strings.Join(wrongRepoVariables, ", "))
	}

	actualEnvironmentNames := map[string]struct{}{}
	if environments, ok := environmentsIndex["environments"].([]any); ok {
		for _, raw := range environments {
			entry, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if name, _ := entry["name"].(string); name != "" {
				actualEnvironmentNames[name] = struct{}{}
			}
		}
	}
	missingWorkflowEnvironments := make([]string, 0)
	for environmentName := range expectedWorkflowEnvironments {
		if _, ok := actualEnvironmentNames[environmentName]; !ok {
			missingWorkflowEnvironments = append(missingWorkflowEnvironments, environmentName)
		}
	}
	slices.Sort(missingWorkflowEnvironments)
	if len(missingWorkflowEnvironments) > 0 {
		return fmt.Errorf("workflow environments missing on %s: %s", repo, strings.Join(missingWorkflowEnvironments, ", "))
	}
	for environmentName, environmentPolicy := range expectedWorkflowEnvironments {
		artifactName := EnvironmentArtifactName(environmentName)
		var environmentMeta map[string]any
		if err := readJSONFile(filepath.Join(tmpDir, fmt.Sprintf("environment-%s.json", artifactName)), &environmentMeta); err != nil {
			return fmt.Errorf("read %s environment metadata: %w", environmentName, err)
		}
		if actualName, ok := environmentMeta["name"].(string); ok && actualName != "" && actualName != environmentName {
			return fmt.Errorf("workflow environment metadata for %s on %s resolved to %s", environmentName, repo, actualName)
		}

		var environmentVariables map[string]any
		if err := readJSONFile(filepath.Join(tmpDir, fmt.Sprintf("environment-%s-variables.json", artifactName)), &environmentVariables); err != nil {
			return fmt.Errorf("read %s environment variables: %w", environmentName, err)
		}
		actualEnvironmentVariables := map[string]any{}
		if variables, ok := environmentVariables["variables"].([]any); ok {
			for _, raw := range variables {
				entry, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				name, _ := entry["name"].(string)
				if name != "" {
					actualEnvironmentVariables[name] = entry["value"]
				}
			}
		}
		missingEnvironmentVariables := make([]string, 0)
		for name := range environmentPolicy.Variables {
			if _, ok := actualEnvironmentVariables[name]; !ok {
				missingEnvironmentVariables = append(missingEnvironmentVariables, name)
			}
		}
		slices.Sort(missingEnvironmentVariables)
		if len(missingEnvironmentVariables) > 0 {
			return fmt.Errorf("workflow environment variables missing on %s/%s: %s", repo, environmentName, strings.Join(missingEnvironmentVariables, ", "))
		}
		wrongEnvironmentVariables := make([]string, 0)
		for name, expectedValue := range environmentPolicy.Variables {
			if actualEnvironmentVariables[name] != expectedValue {
				wrongEnvironmentVariables = append(wrongEnvironmentVariables, fmt.Sprintf("%s=%#v (expected %#v)", name, actualEnvironmentVariables[name], expectedValue))
			}
		}
		slices.Sort(wrongEnvironmentVariables)
		if len(wrongEnvironmentVariables) > 0 {
			return fmt.Errorf("workflow environment variables on %s/%s do not match policy: %s", repo, environmentName, strings.Join(wrongEnvironmentVariables, ", "))
		}
		unexpectedEnvironmentVariables := UnexpectedEnvironmentVariableNames(actualEnvironmentVariables, environmentPolicy.Variables)
		if len(unexpectedEnvironmentVariables) > 0 {
			return fmt.Errorf("workflow environment variables on %s/%s include unexpected entries: %s", repo, environmentName, strings.Join(unexpectedEnvironmentVariables, ", "))
		}

		var environmentSecrets map[string]any
		if err := readJSONFile(filepath.Join(tmpDir, fmt.Sprintf("environment-%s-secrets.json", artifactName)), &environmentSecrets); err != nil {
			return fmt.Errorf("read %s environment secrets: %w", environmentName, err)
		}
		actualEnvironmentSecrets := map[string]struct{}{}
		if secrets, ok := environmentSecrets["secrets"].([]any); ok {
			for _, raw := range secrets {
				entry, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				if name, _ := entry["name"].(string); name != "" {
					actualEnvironmentSecrets[name] = struct{}{}
				}
			}
		}
		missingEnvironmentSecrets := make([]string, 0)
		for _, name := range environmentPolicy.RequiredSecrets {
			if _, ok := actualEnvironmentSecrets[name]; !ok {
				missingEnvironmentSecrets = append(missingEnvironmentSecrets, name)
			}
		}
		slices.Sort(missingEnvironmentSecrets)
		if len(missingEnvironmentSecrets) > 0 {
			return fmt.Errorf("workflow environment secrets missing on %s/%s: %s", repo, environmentName, strings.Join(missingEnvironmentSecrets, ", "))
		}
		unexpectedEnvironmentSecrets := UnexpectedEnvironmentSecretNames(actualEnvironmentSecrets, environmentPolicy.RequiredSecrets)
		if len(unexpectedEnvironmentSecrets) > 0 {
			return fmt.Errorf("workflow environment secrets on %s/%s include unexpected entries: %s", repo, environmentName, strings.Join(unexpectedEnvironmentSecrets, ", "))
		}

		var environmentBranchPolicies map[string]any
		if err := readJSONFile(filepath.Join(tmpDir, fmt.Sprintf("environment-%s-deployment-branch-policies.json", artifactName)), &environmentBranchPolicies); err != nil {
			return fmt.Errorf("read %s deployment branch policies: %w", environmentName, err)
		}
		if err := verifyWorkflowEnvironmentDeploymentPolicy(repo, environmentName, environmentPolicy, environmentMeta, environmentBranchPolicies); err != nil {
			return err
		}
	}

	protectionRules, _ := releaseEnv["protection_rules"].([]any)
	reviewerRules := make([]map[string]any, 0)
	var adminBypassRule map[string]any
	for _, raw := range protectionRules {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if typ, _ := entry["type"].(string); typ == "required_reviewers" {
			reviewerRules = append(reviewerRules, entry)
		}
		if typ, _ := entry["type"].(string); typ == "admin_bypass" {
			adminBypassRule = entry
		}
	}
	switch releaseMode {
	case "review-gated":
		if len(reviewerRules) == 0 {
			return fmt.Errorf("release environment on %s must require a human reviewer", repo)
		}
		hasReviewer := false
		for _, rule := range reviewerRules {
			if reviewers, _ := rule["reviewers"].([]any); len(reviewers) > 0 {
				hasReviewer = true
				break
			}
		}
		if !hasReviewer {
			return fmt.Errorf("release environment on %s must define at least one reviewer", repo)
		}
		if bypass, _ := releaseEnv["can_admins_bypass"].(bool); bypass {
			return fmt.Errorf("release environment on %s must not allow administrator bypass", repo)
		}
		if adminBypassRule != nil {
			if enabled, _ := adminBypassRule["enabled"].(bool); enabled {
				return fmt.Errorf("release environment on %s must not allow administrator bypass", repo)
			}
		}
	case "plan-limited-private":
		if private, _ := repoMeta["private"].(bool); !private {
			return fmt.Errorf("release environment mode 'plan-limited-private' on %s is only valid for private repositories", repo)
		}
		if len(reviewerRules) > 0 {
			return fmt.Errorf("release environment on %s must not define reviewer gates in plan-limited-private mode", repo)
		}
	case "single-owner-public":
		if private, _ := repoMeta["private"].(bool); private {
			return fmt.Errorf("release environment mode 'single-owner-public' on %s is only valid for public repositories", repo)
		}
		if ownerType != "User" {
			return fmt.Errorf("release environment mode 'single-owner-public' on %s is only valid for user-owned repositories", repo)
		}
		if err := requireSingleOwnerCollaborator("release environment mode 'single-owner-public'"); err != nil {
			return err
		}
		if len(reviewerRules) == 0 {
			return fmt.Errorf("release environment on %s must define a reviewer gate in single-owner-public mode", repo)
		}
		hasReviewer := false
		for _, rule := range reviewerRules {
			if reviewers, _ := rule["reviewers"].([]any); len(reviewers) > 0 {
				hasReviewer = true
			}
			if preventSelfReview, _ := rule["prevent_self_review"].(bool); preventSelfReview {
				return fmt.Errorf("release environment on %s must allow self-review in single-owner-public mode", repo)
			}
		}
		if !hasReviewer {
			return fmt.Errorf("release environment on %s must define at least one reviewer in single-owner-public mode", repo)
		}
		if bypass, _ := releaseEnv["can_admins_bypass"].(bool); bypass {
			return fmt.Errorf("release environment on %s must not allow administrator bypass", repo)
		}
		if adminBypassRule != nil {
			if enabled, _ := adminBypassRule["enabled"].(bool); enabled {
				return fmt.Errorf("release environment on %s must not allow administrator bypass", repo)
			}
		}
	default:
		if private, _ := repoMeta["private"].(bool); !private {
			return fmt.Errorf("release environment mode 'single-owner-private' on %s is only valid for private repositories", repo)
		}
		if ownerType != "User" {
			return fmt.Errorf("release environment mode 'single-owner-private' on %s is only valid for user-owned repositories", repo)
		}
		if err := requireSingleOwnerCollaborator("release environment mode 'single-owner-private'"); err != nil {
			return err
		}
	}

	return nil
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
