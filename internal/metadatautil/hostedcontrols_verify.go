// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/omkhar/workcell/internal/tomlsubset"
)

type hostedControlInputs struct {
	repoMeta            map[string]any
	actionsPermissions  map[string]any
	selectedActions     map[string]any
	workflowPermissions map[string]any
	immutableReleases   map[string]any
	actionsVariables    map[string]any
	environmentsIndex   map[string]any
	directCollaborators []any
	rulesets            []any
	releaseEnvironment  map[string]any
	policy              map[string]any
}

type hostedRulesetControls struct {
	branchIntegrity    map[string]any
	branchReview       map[string]any
	branchStatusChecks map[string]any
	tagRelease         map[string]any
}

func loadHostedControlInputs(tmpDir, policyPath string) (hostedControlInputs, error) {
	var inputs hostedControlInputs
	artifacts := []struct {
		name   string
		target any
	}{
		{"repo.json", &inputs.repoMeta},
		{"actions-permissions.json", &inputs.actionsPermissions},
		{"actions-selected-actions.json", &inputs.selectedActions},
		{"actions-workflow-permissions.json", &inputs.workflowPermissions},
		{"immutable-releases.json", &inputs.immutableReleases},
		{"actions-variables.json", &inputs.actionsVariables},
		{"environments.json", &inputs.environmentsIndex},
		{"collaborators-direct.json", &inputs.directCollaborators},
		{"rulesets.json", &inputs.rulesets},
		{"environment-release.json", &inputs.releaseEnvironment},
	}
	for _, artifact := range artifacts {
		if err := readJSONFile(filepath.Join(tmpDir, artifact.name), artifact.target); err != nil {
			return hostedControlInputs{}, err
		}
	}
	content, err := os.ReadFile(policyPath)
	if err != nil {
		return hostedControlInputs{}, err
	}
	inputs.policy, err = tomlsubset.Parse(string(content), policyPath)
	return inputs, err
}

func verifyGitHubActionsControls(inputs hostedControlInputs, actionsPolicy ActionsPolicy, releasePolicy ReleaseAssetsPolicy, repo string) error {
	if err := verifyActionsAvailability(inputs.actionsPermissions, repo); err != nil {
		return err
	}
	if err := verifySelectedActions(inputs.actionsPermissions, inputs.selectedActions, actionsPolicy, repo); err != nil {
		return err
	}
	if err := verifyWorkflowPermissions(inputs.workflowPermissions, actionsPolicy, repo); err != nil {
		return err
	}
	return verifyImmutableReleases(inputs.immutableReleases, releasePolicy, repo)
}

func verifyActionsAvailability(actual map[string]any, repo string) error {
	if enabled, _ := actual["enabled"].(bool); !enabled {
		return fmt.Errorf("GitHub Actions must be enabled on %s", repo)
	}
	if required, _ := actual["sha_pinning_required"].(bool); !required {
		return fmt.Errorf("GitHub Actions SHA pinning must be required on %s", repo)
	}
	return nil
}

func verifySelectedActions(actual, selected map[string]any, policy ActionsPolicy, repo string) error {
	if !policy.AllowOnlyPinnedVerifiedOrExplicitlyTrustedActions {
		return nil
	}
	if allowed, _ := actual["allowed_actions"].(string); allowed != "selected" {
		return fmt.Errorf("GitHub Actions on %s must restrict allowed_actions to selected", repo)
	}
	if allowed, _ := selected["github_owned_allowed"].(bool); !allowed {
		return fmt.Errorf("GitHub Actions selected policy on %s must allow GitHub-owned actions", repo)
	}
	if allowed, _ := selected["verified_allowed"].(bool); !allowed {
		return fmt.Errorf("GitHub Actions selected policy on %s must allow verified creator actions", repo)
	}
	return rejectSelectedActionPatterns(selected, repo)
}

func rejectSelectedActionPatterns(selected map[string]any, repo string) error {
	patterns, _ := selected["patterns_allowed"].([]any)
	unexpected := make([]string, 0)
	for _, raw := range patterns {
		if pattern, _ := raw.(string); pattern != "" {
			unexpected = append(unexpected, pattern)
		}
	}
	slices.Sort(unexpected)
	if len(unexpected) > 0 {
		return fmt.Errorf("GitHub Actions selected policy on %s must not allow unreviewed action patterns: %s", repo, strings.Join(unexpected, ", "))
	}
	return nil
}

func verifyWorkflowPermissions(actual map[string]any, policy ActionsPolicy, repo string) error {
	if value, _ := actual["default_workflow_permissions"].(string); value != policy.DefaultWorkflowTokenPermissions {
		return fmt.Errorf("GitHub Actions default workflow token permissions on %s must be %q", repo, policy.DefaultWorkflowTokenPermissions)
	}
	if canApprove, _ := actual["can_approve_pull_request_reviews"].(bool); canApprove {
		return fmt.Errorf("GitHub Actions workflow token on %s must not be allowed to approve pull requests", repo)
	}
	return nil
}

func verifyImmutableReleases(actual map[string]any, policy ReleaseAssetsPolicy, repo string) error {
	if policy.ImmutableGitHubReleases {
		if enabled, _ := actual["enabled"].(bool); !enabled {
			return fmt.Errorf("immutable GitHub releases must be enabled on %s", repo)
		}
	}
	return nil
}

func verifyHostedRepositoryVariables(payload, expected map[string]any, repo string) error {
	actual := hostedNamedValues(payload)
	if missing := missingHostedRepositoryVariableNames(actual, expected); len(missing) > 0 {
		return fmt.Errorf("repository variables missing on %s: %s", repo, strings.Join(missing, ", "))
	}
	if wrong := mismatchedHostedRepositoryVariableValues(actual, expected); len(wrong) > 0 {
		return fmt.Errorf("repository variables on %s do not match policy: %s", repo, strings.Join(wrong, ", "))
	}
	return nil
}

func hostedNamedValues(payload map[string]any) map[string]any {
	actual := map[string]any{}
	values, _ := payload["variables"].([]any)
	for _, raw := range values {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if name, _ := entry["name"].(string); name != "" {
			actual[name] = entry["value"]
		}
	}
	return actual
}

func missingHostedRepositoryVariableNames(actual, expected map[string]any) []string {
	missing := make([]string, 0)
	for name := range expected {
		if _, ok := actual[name]; !ok {
			missing = append(missing, name)
		}
	}
	slices.Sort(missing)
	return missing
}

func mismatchedHostedRepositoryVariableValues(actual, expected map[string]any) []string {
	wrong := make([]string, 0)
	for name, expectedValue := range expected {
		if actual[name] != expectedValue {
			wrong = append(wrong, fmt.Sprintf("%s=%#v (expected %#v)", name, actual[name], expectedValue))
		}
	}
	slices.Sort(wrong)
	return wrong
}

func missingHostedVariableNames(actual map[string]any, expected map[string]string) []string {
	missing := make([]string, 0)
	for name := range expected {
		if _, ok := actual[name]; !ok {
			missing = append(missing, name)
		}
	}
	slices.Sort(missing)
	return missing
}

func mismatchedHostedVariableValues(actual map[string]any, expected map[string]string) []string {
	wrong := make([]string, 0)
	for name, expectedValue := range expected {
		if actual[name] != expectedValue {
			wrong = append(wrong, fmt.Sprintf("%s=%#v (expected %#v)", name, actual[name], expectedValue))
		}
	}
	slices.Sort(wrong)
	return wrong
}

func verifyHostedWorkflowEnvironments(tmpDir string, index map[string]any, expected map[string]WorkflowEnvironmentPolicy, repo string) error {
	missing := missingHostedEnvironmentNames(hostedEnvironmentNames(index), expected)
	if len(missing) > 0 {
		return fmt.Errorf("workflow environments missing on %s: %s", repo, strings.Join(missing, ", "))
	}
	for name, policy := range expected {
		if err := verifyHostedWorkflowEnvironment(tmpDir, name, policy, repo); err != nil {
			return err
		}
	}
	return nil
}

func hostedEnvironmentNames(index map[string]any) map[string]struct{} {
	actual := map[string]struct{}{}
	environments, _ := index["environments"].([]any)
	for _, raw := range environments {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if name, _ := entry["name"].(string); name != "" {
			actual[name] = struct{}{}
		}
	}
	return actual
}

func missingHostedEnvironmentNames(actual map[string]struct{}, expected map[string]WorkflowEnvironmentPolicy) []string {
	missing := make([]string, 0)
	for name := range expected {
		if _, ok := actual[name]; !ok {
			missing = append(missing, name)
		}
	}
	slices.Sort(missing)
	return missing
}

func verifyHostedWorkflowEnvironment(tmpDir, name string, policy WorkflowEnvironmentPolicy, repo string) error {
	meta, err := readAndVerifyHostedEnvironmentMetadata(tmpDir, name, repo)
	if err != nil {
		return err
	}
	if err := verifyHostedEnvironmentVariables(tmpDir, name, policy.Variables, repo); err != nil {
		return err
	}
	if err := verifyHostedEnvironmentSecrets(tmpDir, name, policy.RequiredSecrets, repo); err != nil {
		return err
	}
	return verifyHostedEnvironmentDeployment(tmpDir, name, policy, meta, repo)
}

func readAndVerifyHostedEnvironmentMetadata(tmpDir, name, repo string) (map[string]any, error) {
	var meta map[string]any
	artifact := EnvironmentArtifactName(name)
	if err := readJSONFile(filepath.Join(tmpDir, fmt.Sprintf("environment-%s.json", artifact)), &meta); err != nil {
		return nil, fmt.Errorf("read %s environment metadata: %w", name, err)
	}
	if actualName, ok := meta["name"].(string); ok && actualName != "" && actualName != name {
		return nil, fmt.Errorf("workflow environment metadata for %s on %s resolved to %s", name, repo, actualName)
	}
	return meta, nil
}

func verifyHostedEnvironmentVariables(tmpDir, name string, expected map[string]string, repo string) error {
	var payload map[string]any
	artifact := EnvironmentArtifactName(name)
	if err := readJSONFile(filepath.Join(tmpDir, fmt.Sprintf("environment-%s-variables.json", artifact)), &payload); err != nil {
		return fmt.Errorf("read %s environment variables: %w", name, err)
	}
	actual := hostedNamedValues(payload)
	if missing := missingHostedVariableNames(actual, expected); len(missing) > 0 {
		return fmt.Errorf("workflow environment variables missing on %s/%s: %s", repo, name, strings.Join(missing, ", "))
	}
	if wrong := mismatchedHostedVariableValues(actual, expected); len(wrong) > 0 {
		return fmt.Errorf("workflow environment variables on %s/%s do not match policy: %s", repo, name, strings.Join(wrong, ", "))
	}
	if unexpected := UnexpectedEnvironmentVariableNames(actual, expected); len(unexpected) > 0 {
		return fmt.Errorf("workflow environment variables on %s/%s include unexpected entries: %s", repo, name, strings.Join(unexpected, ", "))
	}
	return nil
}

func verifyHostedEnvironmentSecrets(tmpDir, name string, expected []string, repo string) error {
	var payload map[string]any
	artifact := EnvironmentArtifactName(name)
	if err := readJSONFile(filepath.Join(tmpDir, fmt.Sprintf("environment-%s-secrets.json", artifact)), &payload); err != nil {
		return fmt.Errorf("read %s environment secrets: %w", name, err)
	}
	actual := hostedSecretNames(payload)
	if missing := missingHostedSecretNames(actual, expected); len(missing) > 0 {
		return fmt.Errorf("workflow environment secrets missing on %s/%s: %s", repo, name, strings.Join(missing, ", "))
	}
	if unexpected := UnexpectedEnvironmentSecretNames(actual, expected); len(unexpected) > 0 {
		return fmt.Errorf("workflow environment secrets on %s/%s include unexpected entries: %s", repo, name, strings.Join(unexpected, ", "))
	}
	return nil
}

func hostedSecretNames(payload map[string]any) map[string]struct{} {
	actual := map[string]struct{}{}
	secrets, _ := payload["secrets"].([]any)
	for _, raw := range secrets {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if name, _ := entry["name"].(string); name != "" {
			actual[name] = struct{}{}
		}
	}
	return actual
}

func missingHostedSecretNames(actual map[string]struct{}, expected []string) []string {
	missing := make([]string, 0)
	for _, name := range expected {
		if _, ok := actual[name]; !ok {
			missing = append(missing, name)
		}
	}
	slices.Sort(missing)
	return missing
}

func verifyHostedEnvironmentDeployment(tmpDir, name string, policy WorkflowEnvironmentPolicy, meta map[string]any, repo string) error {
	var branchPolicies map[string]any
	artifact := EnvironmentArtifactName(name)
	if err := readJSONFile(filepath.Join(tmpDir, fmt.Sprintf("environment-%s-deployment-branch-policies.json", artifact)), &branchPolicies); err != nil {
		return fmt.Errorf("read %s deployment branch policies: %w", name, err)
	}
	return verifyWorkflowEnvironmentDeploymentPolicy(repo, name, policy, meta, branchPolicies)
}

func verifyHostedRulesetControls(inputs hostedControlInputs, reviewMode, ownerType string, expectedContexts []string, requireOwner func(string) error, repo string) error {
	active := activeHostedRulesets(inputs.rulesets)
	if len(active) == 0 {
		return fmt.Errorf("no active rulesets found on %s", repo)
	}
	controls := classifyHostedRulesets(active)
	if err := verifyHostedRulesetShape(controls, repo); err != nil {
		return err
	}
	if err := verifyBranchReviewRuleset(controls.branchReview, inputs.repoMeta, reviewMode, ownerType, requireOwner, repo); err != nil {
		return err
	}
	return verifyRequiredStatusRuleset(controls.branchStatusChecks, expectedContexts, repo)
}

func activeHostedRulesets(rawRulesets []any) []map[string]any {
	active := make([]map[string]any, 0)
	for _, raw := range rawRulesets {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if enforcement, _ := entry["enforcement"].(string); enforcement == "active" {
			active = append(active, entry)
		}
	}
	return active
}

func classifyHostedRulesets(rulesets []map[string]any) hostedRulesetControls {
	var controls hostedRulesetControls
	for _, ruleset := range rulesets {
		if isDefaultBranchRuleset(ruleset) {
			classifyDefaultBranchRuleset(&controls, ruleset)
		}
		if isReleaseTagRuleset(ruleset) {
			controls.tagRelease = ruleset
		}
	}
	return controls
}

func isDefaultBranchRuleset(ruleset map[string]any) bool {
	target, _ := ruleset["target"].(string)
	return target == "branch" && hostedRulesetHasRef(ruleset, "~DEFAULT_BRANCH")
}

func isReleaseTagRuleset(ruleset map[string]any) bool {
	target, _ := ruleset["target"].(string)
	return target == "tag" && hostedRulesetHasRef(ruleset, "refs/tags/v*") && hasReleaseTagRules(ruleset)
}

func classifyDefaultBranchRuleset(controls *hostedRulesetControls, ruleset map[string]any) {
	if hasIntegrityRules(ruleset) {
		controls.branchIntegrity = ruleset
	}
	if hostedRulesetRule(ruleset, "pull_request") != nil {
		controls.branchReview = ruleset
	}
	if hostedRulesetRule(ruleset, "required_status_checks") != nil {
		controls.branchStatusChecks = ruleset
	}
}

func hasIntegrityRules(ruleset map[string]any) bool {
	return hostedRulesetRule(ruleset, "required_signatures") != nil &&
		hostedRulesetRule(ruleset, "non_fast_forward") != nil &&
		hostedRulesetRule(ruleset, "deletion") != nil
}

func hasReleaseTagRules(ruleset map[string]any) bool {
	return hostedRulesetRule(ruleset, "creation") != nil &&
		hostedRulesetRule(ruleset, "update") != nil &&
		hostedRulesetRule(ruleset, "deletion") != nil
}

func hostedRulesetHasRef(ruleset map[string]any, expected string) bool {
	conditions, _ := ruleset["conditions"].(map[string]any)
	refName, _ := conditions["ref_name"].(map[string]any)
	include, _ := refName["include"].([]any)
	for _, raw := range include {
		if value, _ := raw.(string); value == expected {
			return true
		}
	}
	return false
}

func hostedRulesetRule(ruleset map[string]any, ruleType string) map[string]any {
	rules, _ := ruleset["rules"].([]any)
	for _, raw := range rules {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if value, _ := entry["type"].(string); value == ruleType {
			return entry
		}
	}
	return nil
}

func verifyHostedRulesetShape(controls hostedRulesetControls, repo string) error {
	if controls.branchIntegrity == nil {
		return fmt.Errorf("missing active default-branch integrity ruleset on %s with required_signatures, non_fast_forward, and deletion", repo)
	}
	if controls.branchReview == nil {
		return fmt.Errorf("missing active default-branch review ruleset on %s with a pull_request rule", repo)
	}
	if controls.branchStatusChecks == nil {
		return fmt.Errorf("missing active default-branch status-check ruleset on %s with a required_status_checks rule", repo)
	}
	return verifyHostedRulesetBypasses(controls, repo)
}

func verifyHostedRulesetBypasses(controls hostedRulesetControls, repo string) error {
	if actors, _ := controls.branchIntegrity["bypass_actors"].([]any); len(actors) > 0 {
		return fmt.Errorf("default-branch integrity ruleset on %s must not declare bypass actors", repo)
	}
	if err := requireHostedBypassShape(controls.branchReview, "RepositoryRole", "pull_request", false, repo); err != nil {
		return err
	}
	if controls.tagRelease == nil {
		return fmt.Errorf("missing active release-tag ruleset on %s for refs/tags/v* with creation/update/deletion protection", repo)
	}
	return requireHostedBypassShape(controls.tagRelease, "RepositoryRole", "always", true, repo)
}

func requireHostedBypassShape(ruleset map[string]any, actorType, bypassMode string, requireNonEmpty bool, repo string) error {
	actors, _ := ruleset["bypass_actors"].([]any)
	if requireNonEmpty && len(actors) == 0 {
		return fmt.Errorf("ruleset %v on %s must declare an explicit bypass actor", ruleset["name"], repo)
	}
	for _, raw := range actors {
		if !hostedBypassActorMatches(raw, actorType, bypassMode) {
			return fmt.Errorf("ruleset %v on %s must only use %s/%s bypass actors", ruleset["name"], repo, actorType, bypassMode)
		}
	}
	return nil
}

func hostedBypassActorMatches(raw any, actorType, bypassMode string) bool {
	entry, ok := raw.(map[string]any)
	if !ok {
		return false
	}
	actualType, _ := entry["actor_type"].(string)
	actualMode, _ := entry["bypass_mode"].(string)
	return actualType == actorType && actualMode == bypassMode
}

func verifyBranchReviewRuleset(ruleset, repoMeta map[string]any, mode, ownerType string, requireOwner func(string) error, repo string) error {
	rule := hostedRulesetRule(ruleset, "pull_request")
	parameters, _ := rule["parameters"].(map[string]any)
	if mode == "review-gated" {
		return verifyReviewGatedParameters(parameters, repo)
	}
	if err := verifySingleOwnerReviewMode(repoMeta, mode, ownerType, requireOwner, repo); err != nil {
		return err
	}
	return verifySingleOwnerReviewParameters(parameters, mode, repo)
}

func verifyReviewGatedParameters(parameters map[string]any, repo string) error {
	if count, _ := parameters["required_approving_review_count"].(float64); count < 1 {
		return fmt.Errorf("default-branch review ruleset on %s must require at least one approving review", repo)
	}
	if required, _ := parameters["require_code_owner_review"].(bool); !required {
		return fmt.Errorf("default-branch review ruleset on %s must require code owner review", repo)
	}
	if resolved, _ := parameters["required_review_thread_resolution"].(bool); !resolved {
		return fmt.Errorf("default-branch review ruleset on %s must require resolved review threads", repo)
	}
	return nil
}

func verifySingleOwnerReviewMode(repoMeta map[string]any, mode, ownerType string, requireOwner func(string) error, repo string) error {
	private, _ := repoMeta["private"].(bool)
	if mode == "single-owner-private-pr" {
		return verifySingleOwnerMode(private, true, ownerType, mode, requireOwner, repo)
	}
	return verifySingleOwnerMode(private, false, ownerType, mode, requireOwner, repo)
}

func verifySingleOwnerMode(private, expectedPrivate bool, ownerType, mode string, requireOwner func(string) error, repo string) error {
	modeLabel := "branch review mode '" + mode + "'"
	if private != expectedPrivate {
		visibility := "public"
		if expectedPrivate {
			visibility = "private"
		}
		return fmt.Errorf("%s on %s is only valid for %s repositories", modeLabel, repo, visibility)
	}
	if ownerType != "User" {
		return fmt.Errorf("%s on %s is only valid for user-owned repositories", modeLabel, repo)
	}
	return requireOwner(modeLabel)
}

func verifySingleOwnerReviewParameters(parameters map[string]any, mode, repo string) error {
	if count, _ := parameters["required_approving_review_count"].(float64); count != 0 {
		return fmt.Errorf("default-branch review ruleset on %s must require zero approving reviews in %s mode", repo, mode)
	}
	if required, _ := parameters["require_code_owner_review"].(bool); required {
		return fmt.Errorf("default-branch review ruleset on %s must not require code owner review in %s mode", repo, mode)
	}
	if required, _ := parameters["require_last_push_approval"].(bool); required {
		return fmt.Errorf("default-branch review ruleset on %s must not require last-push approval in %s mode", repo, mode)
	}
	return verifySingleOwnerThreadResolution(parameters, mode, repo)
}

func verifySingleOwnerThreadResolution(parameters map[string]any, mode, repo string) error {
	resolved, _ := parameters["required_review_thread_resolution"].(bool)
	if mode == "single-owner-public-pr" && !resolved {
		return fmt.Errorf("default-branch review ruleset on %s must require resolved review threads in single-owner-public-pr mode", repo)
	}
	if mode == "single-owner-private-pr" && resolved {
		return fmt.Errorf("default-branch review ruleset on %s must not require resolved review threads in single-owner-private-pr mode", repo)
	}
	return nil
}

func verifyRequiredStatusRuleset(ruleset map[string]any, expected []string, repo string) error {
	rule := hostedRulesetRule(ruleset, "required_status_checks")
	parameters, _ := rule["parameters"].(map[string]any)
	if strict, _ := parameters["strict_required_status_checks_policy"].(bool); !strict {
		return fmt.Errorf("default-branch status-check ruleset on %s must require strict status checks", repo)
	}
	missing := missingHostedStatusContexts(hostedStatusContexts(parameters), expected)
	if len(missing) > 0 {
		return fmt.Errorf("default-branch status-check ruleset on %s is missing required contexts: %s", repo, strings.Join(missing, ", "))
	}
	return nil
}

func hostedStatusContexts(parameters map[string]any) map[string]struct{} {
	rawChecks, _ := parameters["required_status_checks"].([]any)
	actual := map[string]struct{}{}
	for _, raw := range rawChecks {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if context, _ := entry["context"].(string); context != "" {
			actual[context] = struct{}{}
		}
	}
	return actual
}

func missingHostedStatusContexts(actual map[string]struct{}, expected []string) []string {
	missing := make([]string, 0)
	for _, context := range expected {
		if _, ok := actual[context]; !ok {
			missing = append(missing, context)
		}
	}
	slices.Sort(missing)
	return missing
}
