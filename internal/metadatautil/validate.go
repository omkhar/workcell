// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type workflowDocument struct {
	Jobs map[string]workflowJob `yaml:"jobs"`
}

type workflowJob struct {
	Name string `yaml:"name"`
}

func collectWorkflowJobNames(content []byte) ([]string, error) {
	var document workflowDocument
	if err := yaml.Unmarshal(content, &document); err != nil {
		return nil, err
	}

	names := make([]string, 0, len(document.Jobs))
	for _, job := range document.Jobs {
		name := strings.TrimSpace(job.Name)
		if name == "" {
			continue
		}
		names = append(names, name)
	}
	return names, nil
}

func CheckWorkflows(rootDir, policyPath string) error {
	if err := ensureWorkflowTools(rootDir); err != nil {
		return err
	}

	policyText, err := readText(policyPath)
	if err != nil {
		return err
	}
	policy, err := ParseTOMLSubset(policyText, policyPath)
	if err != nil {
		return err
	}

	contexts, err := requireStringSliceTable(policy, "required_status_checks", "contexts", policyPath)
	if err != nil {
		return err
	}
	if len(contexts) == 0 {
		return fmt.Errorf("%s must define required_status_checks.contexts as a non-empty array", policyPath)
	}

	workflowDir := filepath.Join(rootDir, ".github", "workflows")
	workflowPaths, err := filepath.Glob(filepath.Join(workflowDir, "*.yml"))
	if err != nil {
		return err
	}

	jobNames := map[string]struct{}{}
	for _, path := range workflowPaths {
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		names, err := collectWorkflowJobNames(content)
		if err != nil {
			return fmt.Errorf("%s: parse workflow job names: %w", path, err)
		}
		for _, name := range names {
			jobNames[name] = struct{}{}
		}
	}

	missing := make([]string, 0)
	for _, expected := range contexts {
		if _, ok := jobNames[expected]; !ok {
			missing = append(missing, expected)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		return fmt.Errorf(
			"workflow jobs are missing required status-check names from %s: %s",
			policyPath,
			strings.Join(missing, ", "),
		)
	}

	codeqlWorkflowPath := filepath.Join(workflowDir, "codeql.yml")
	codeqlWorkflow, err := readText(codeqlWorkflowPath)
	if err != nil {
		return err
	}
	if err := validateCodeQLWorkflow(codeqlWorkflow, codeqlWorkflowPath); err != nil {
		return err
	}

	releaseWorkflowPath := filepath.Join(workflowDir, "release.yml")
	releaseWorkflow, err := readText(releaseWorkflowPath)
	if err != nil {
		return err
	}
	if err := validateReleaseWorkflowCodeQLFlow(releaseWorkflow); err != nil {
		return err
	}
	return nil
}

func ensureWorkflowTools(rootDir string) error {
	actionlintPath, err := exec.LookPath("actionlint")
	if err != nil {
		return fmt.Errorf("actionlint is required for workflow validation: %w", err)
	}
	zizmorPath, err := exec.LookPath("zizmor")
	if err != nil {
		return fmt.Errorf("zizmor is required for workflow validation: %w", err)
	}

	actionlintCmd := exec.Command(actionlintPath)
	actionlintCmd.Dir = rootDir
	if output, err := actionlintCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("actionlint failed: %w\n%s", err, strings.TrimSpace(string(output)))
	}

	workflowPaths, err := filepath.Glob(filepath.Join(rootDir, ".github", "workflows", "*.yml"))
	if err != nil {
		return err
	}
	zizmorArgs := []string{
		"--persona", "auditor",
		"--config", filepath.Join(rootDir, ".github", "zizmor.yml"),
	}
	zizmorArgs = append(zizmorArgs, workflowPaths...)
	zizmorCmd := exec.Command(
		zizmorPath,
		zizmorArgs...,
	)
	zizmorCmd.Dir = rootDir
	if output, err := zizmorCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("zizmor failed: %w\n%s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func validateCodeQLWorkflow(codeqlWorkflow, workflowPath string) error {
	if regexp.MustCompile(`(?s)- language: go\s+build-mode: none`).MatchString(codeqlWorkflow) {
		return fmt.Errorf("%s must not configure Go CodeQL with build-mode: none", workflowPath)
	}
	for _, needle := range []string{
		"- language: rust",
		"build-mode: none",
		"- language: javascript-typescript",
		"- language: go",
		"build-mode: autobuild",
		"github/codeql-action/init@",
		"github/codeql-action/autobuild@",
		"github/codeql-action/analyze@",
	} {
		if !strings.Contains(codeqlWorkflow, needle) {
			return fmt.Errorf("%s must contain %q", workflowPath, needle)
		}
	}
	return nil
}

func validateReleaseWorkflowCodeQLFlow(releaseWorkflow string) error {
	for _, needle := range []string{
		"name: Release CodeQL (${{ matrix.language }})",
		"- language: rust",
		"- language: javascript-typescript",
		"- language: go",
		"build-mode: autobuild",
		"github/codeql-action/autobuild@",
		"- codeql-preflight",
	} {
		if !strings.Contains(releaseWorkflow, needle) {
			return fmt.Errorf(".github/workflows/release.yml must contain %q", needle)
		}
	}
	if regexp.MustCompile(`(?s)- language: go\s+build-mode: none`).MatchString(releaseWorkflow) {
		return errors.New(".github/workflows/release.yml must not configure Go CodeQL with build-mode: none")
	}
	return nil
}

func validateReleaseWorkflowControlPlaneFlow(releaseWorkflow string) error {
	if !strings.Contains(releaseWorkflow, "dist/workcell-control-plane-preflight.json") {
		return errors.New(".github/workflows/release.yml must keep the reviewed control-plane manifest flow")
	}
	if !strings.Contains(releaseWorkflow, "run: ./scripts/generate-control-plane-manifest.sh dist/workcell-control-plane.json") {
		return errors.New(".github/workflows/release.yml must regenerate the published control-plane manifest under dist/workcell-control-plane.json")
	}
	if !regexp.MustCompile(`(?s)Verify control-plane manifest matches preflight.*?cmp -s \\\s+dist/workcell-control-plane\.json \\\s+dist/preflight/workcell-control-plane-preflight\.json`).MatchString(releaseWorkflow) {
		return errors.New(".github/workflows/release.yml must verify the published control-plane manifest against the preflight artifact")
	}
	return nil
}

func validateReleaseWorkflowGitHubAttestationFlow(releaseWorkflow string) error {
	if !strings.Contains(releaseWorkflow, "ENABLE_GITHUB_ATTESTATIONS_SUPPORTED: ${{ github.event.repository.visibility == 'public' || vars.WORKCELL_ENABLE_PRIVATE_GITHUB_ATTESTATIONS == 'true' }}") {
		return errors.New(".github/workflows/release.yml must gate GitHub attestations on public visibility or an explicit private-repo capability flag")
	}
	const attestGuard = "if: env.ENABLE_GITHUB_ATTESTATIONS == 'true' && env.ENABLE_GITHUB_ATTESTATIONS_SUPPORTED == 'true'"
	attestStepRE := regexp.MustCompile(`(?m)^\s*-\s+uses:\s+actions/attest@`)
	guardedAttestStepRE := regexp.MustCompile(`(?ms)^\s*-\s+uses:\s+actions/attest@[^\n]+\n\s+if:\s+env\.ENABLE_GITHUB_ATTESTATIONS == 'true' && env\.ENABLE_GITHUB_ATTESTATIONS_SUPPORTED == 'true'\n`)
	totalAttestSteps := len(attestStepRE.FindAllString(releaseWorkflow, -1))
	if totalAttestSteps != 10 {
		return errors.New(".github/workflows/release.yml must keep exactly ten reviewed GitHub attestation steps")
	}
	if len(guardedAttestStepRE.FindAllString(releaseWorkflow, -1)) != totalAttestSteps {
		return fmt.Errorf(".github/workflows/release.yml must guard every actions/attest step with %q", attestGuard)
	}
	for _, needle := range []string{
		"subject-name: ${{ env.IMAGE_NAME }}",
		"sbom-path: dist/workcell-image.spdx.json",
		"subject-path: dist/${{ env.BUNDLE_NAME }}",
		"sbom-path: dist/workcell-source.spdx.json",
		"subject-path: dist/workcell.rb",
		"subject-path: dist/workcell-image.digest",
		"subject-path: dist/workcell-build-inputs.json",
		"subject-path: dist/workcell-control-plane.json",
		"subject-path: dist/workcell-builder-environment.json",
		"subject-path: dist/SHA256SUMS",
	} {
		if !strings.Contains(releaseWorkflow, needle) {
			return fmt.Errorf(".github/workflows/release.yml must contain %q", needle)
		}
	}
	return nil
}

func validateMacOSInstallVerificationFlow(workflowText, workflowPath, artifactName, jobName string) error {
	for _, needle := range []string{
		fmt.Sprintf("name: %s", artifactName),
		jobName,
		"macos-26",
		"macos-15",
		"actions/upload-artifact@",
		"actions/download-artifact@",
		`"${bundle_dir}/scripts/install.sh"`,
		`"${bundle_dir}/scripts/uninstall.sh"`,
		"brew tap-new",
		"brew --repo",
		"brew install \"${tap_name}/workcell\"",
		"brew uninstall --force \"${tap_name}/workcell\"",
		"brew list --versions workcell",
	} {
		if !strings.Contains(workflowText, needle) {
			return fmt.Errorf("%s must contain %q", workflowPath, needle)
		}
	}

	if !strings.Contains(workflowText, "find dist/install -maxdepth 1 -type f -name 'workcell-*.tar.gz'") {
		return fmt.Errorf("%s must resolve the reviewed install-candidate bundle from the artifact download", workflowPath)
	}

	return nil
}

func validateUpstreamRefreshWorkflow(workflowText string) error {
	for _, needle := range []string{
		"name: Upstream refresh",
		"workflow_dispatch:",
		"WORKCELL_COSIGN_VERSION",
		"sigstore/cosign-installer@",
		"cosign-release: ${{ env.WORKCELL_COSIGN_VERSION }}",
		`sudo install -m 0755 "$(command -v cosign)" /usr/local/bin/cosign`,
		"./scripts/update-upstream-pins.sh --apply",
		"./scripts/update-upstream-pins.sh --check",
		"./scripts/check-pinned-inputs.sh",
		"WORKCELL_UPSTREAM_REFRESH_GPG_PRIVATE_KEY",
		"WORKCELL_UPSTREAM_REFRESH_GPG_KEY_ID",
		"git commit -S -F",
		"gh pr create",
		"--draft",
		"persist-credentials: false",
		"fetch-depth: 0",
		"contents: write",
		"pull-requests: write",
	} {
		if !strings.Contains(workflowText, needle) {
			return fmt.Errorf(".github/workflows/upstream-refresh.yml must contain %q", needle)
		}
	}
	return nil
}

func hostedControlsRepositoryVariables(policy map[string]any, policyPath string) (map[string]any, error) {
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
		"WORKCELL_ENABLE_GITHUB_ATTESTATIONS",
		"WORKCELL_ENABLE_PRIVATE_GITHUB_ATTESTATIONS",
	} {
		if _, ok := expectedRepoVariables[requiredName]; !ok {
			return nil, fmt.Errorf("%s must declare %s in repository_variables", policyPath, requiredName)
		}
	}
	return expectedRepoVariables, nil
}

func validateCanonicalHostedControlsRepositoryVariables(policy map[string]any, policyPath string) error {
	repositoryVariables, err := hostedControlsRepositoryVariables(policy, policyPath)
	if err != nil {
		return err
	}
	if value, _ := repositoryVariables["WORKCELL_ENABLE_GITHUB_ATTESTATIONS"].(string); value != "true" {
		return errors.New("policy/github-hosted-controls.toml must require WORKCELL_ENABLE_GITHUB_ATTESTATIONS = \"true\"")
	}
	if value, _ := repositoryVariables["WORKCELL_ENABLE_PRIVATE_GITHUB_ATTESTATIONS"].(string); value != "false" {
		return errors.New("policy/github-hosted-controls.toml must require WORKCELL_ENABLE_PRIVATE_GITHUB_ATTESTATIONS = \"false\"")
	}
	return nil
}

func FetchGitHubHostedControlsRulesets(tmpDir, repo string) error {
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
		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("gh api repos/%s/rulesets/%s: %w", repo, rulesetID, err)
		}
		var detail any
		if err := json.Unmarshal(output, &detail); err != nil {
			return err
		}
		details = append(details, detail)
	}
	return writeJSONFile(filepath.Join(tmpDir, "rulesets.json"), details)
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
	policyText, err := readText(policyPath)
	if err != nil {
		return err
	}
	policy, err := ParseTOMLSubset(policyText, policyPath)
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
	if branchReviewMode != "review-gated" && branchReviewMode != "approval-gated" && branchReviewMode != "single-owner-public-pr" && branchReviewMode != "single-owner-private-pr" {
		return fmt.Errorf("%s must set branch_review.mode to 'review-gated', 'approval-gated', 'single-owner-public-pr', or 'single-owner-private-pr'", policyPath)
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
	expectedRepoVariables, err := hostedControlsRepositoryVariables(policy, policyPath)
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
	if branchReviewMode == "review-gated" || branchReviewMode == "approval-gated" {
		if count, _ := parameters["required_approving_review_count"].(float64); count < 1 {
			return fmt.Errorf("default-branch review ruleset on %s must require at least one approving review", repo)
		}
		requireCodeOwnerReview, _ := parameters["require_code_owner_review"].(bool)
		if branchReviewMode == "review-gated" && !requireCodeOwnerReview {
			return fmt.Errorf("default-branch review ruleset on %s must require code owner review", repo)
		}
		if branchReviewMode == "approval-gated" && requireCodeOwnerReview {
			return fmt.Errorf("default-branch review ruleset on %s must not require code owner review in approval-gated mode", repo)
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
	sort.Strings(missingStatus)
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
	sort.Strings(missingRepoVariables)
	if len(missingRepoVariables) > 0 {
		return fmt.Errorf("repository variables missing on %s: %s", repo, strings.Join(missingRepoVariables, ", "))
	}
	wrongRepoVariables := make([]string, 0)
	for name, expectedValue := range expectedRepoVariables {
		if actualRepoVariables[name] != expectedValue {
			wrongRepoVariables = append(wrongRepoVariables, fmt.Sprintf("%s=%#v (expected %#v)", name, actualRepoVariables[name], expectedValue))
		}
	}
	sort.Strings(wrongRepoVariables)
	if len(wrongRepoVariables) > 0 {
		return fmt.Errorf("repository variables on %s do not match policy: %s", repo, strings.Join(wrongRepoVariables, ", "))
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

func CheckPinnedInputs(cfg PinnedInputsConfig) error {
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(cfg.RuntimeDockerfilePath), "..", ".."))
	goModPath := filepath.Join(repoRoot, "go.mod")
	cargoManifestPath := filepath.Join(repoRoot, "runtime", "container", "rust", "Cargo.toml")
	rustToolchainPath := filepath.Join(repoRoot, "runtime", "container", "rust", "rust-toolchain.toml")

	runtimeDockerfile, err := readText(cfg.RuntimeDockerfilePath)
	if err != nil {
		return err
	}
	validatorDockerfile, err := readText(cfg.ValidatorDockerfilePath)
	if err != nil {
		return err
	}
	providersPackageJSONText, err := readText(cfg.ProvidersPackageJSONPath)
	if err != nil {
		return err
	}
	providersPackageLockText, err := readText(cfg.ProvidersPackageLockPath)
	if err != nil {
		return err
	}
	ciWorkflow, err := readText(cfg.CIWorkflowPath)
	if err != nil {
		return err
	}
	releaseWorkflow, err := readText(cfg.ReleaseWorkflowPath)
	if err != nil {
		return err
	}
	pinHygieneWorkflow, err := readText(cfg.PinHygieneWorkflowPath)
	if err != nil {
		return err
	}
	upstreamRefreshWorkflow, err := readText(filepath.Join(cfg.WorkflowsDir, "upstream-refresh.yml"))
	if err != nil {
		return err
	}
	codeowners, err := readText(cfg.CodeownersPath)
	if err != nil {
		return err
	}
	hostedControlsPolicyText, err := readText(cfg.HostedControlsPolicyPath)
	if err != nil {
		return err
	}
	hostedControlsScript, err := readText(cfg.HostedControlsScriptPath)
	if err != nil {
		return err
	}
	codexRequirementsText, err := readText(cfg.CodexRequirementsPath)
	if err != nil {
		return err
	}
	codexMCPConfigText, err := readText(cfg.CodexMCPConfigPath)
	if err != nil {
		return err
	}
	goModText, err := readText(goModPath)
	if err != nil {
		return err
	}
	cargoManifestText, err := readText(cargoManifestPath)
	if err != nil {
		return err
	}
	rustToolchainText, err := readText(rustToolchainPath)
	if err != nil {
		return err
	}

	var providersPackageJSON map[string]any
	if err := json.Unmarshal([]byte(providersPackageJSONText), &providersPackageJSON); err != nil {
		return err
	}
	var providersPackageLock map[string]any
	if err := json.Unmarshal([]byte(providersPackageLockText), &providersPackageLock); err != nil {
		return err
	}
	hostedControlsPolicy, err := ParseTOMLSubset(hostedControlsPolicyText, cfg.HostedControlsPolicyPath)
	if err != nil {
		return err
	}

	requireArg := func(text, name, path string) (string, error) {
		match := regexp.MustCompile(`(?m)^ARG ` + regexp.QuoteMeta(name) + `=(.+)$`).FindStringSubmatch(text)
		if match == nil {
			return "", fmt.Errorf("unable to extract %s from %s", name, path)
		}
		return strings.TrimSpace(match[1]), nil
	}
	requireYAMLKey := func(text, name, path string) (string, error) {
		match := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(name) + `:\s*(.+)$`).FindStringSubmatch(text)
		if match == nil {
			return "", fmt.Errorf("unable to extract %s from %s", name, path)
		}
		return strings.TrimSpace(match[1]), nil
	}
	requirePinnedBaseImage := func(image, label, path string) error {
		if !regexp.MustCompile(`^[^@]+@sha256:[0-9a-f]{64}$`).MatchString(image) {
			return fmt.Errorf("%s in %s must be pinned by immutable digest, found %q", label, path, image)
		}
		return nil
	}
	verifySnapshotFreshness := func(snapshot, path string) error {
		ts, err := time.Parse("20060102T150405Z", snapshot)
		if err != nil {
			return fmt.Errorf("debian snapshot %s in %s is not valid", snapshot, path)
		}
		now := time.Now().UTC()
		ageDays := int(now.Sub(ts).Hours() / 24)
		if ageDays > cfg.MaxDebianSnapshotAgeDays {
			return fmt.Errorf(
				"debian snapshot %s in %s is %d days old; refresh it or raise WORKCELL_MAX_DEBIAN_SNAPSHOT_AGE_DAYS",
				snapshot,
				path,
				ageDays,
			)
		}
		return nil
	}
	extractInstallBlocks := func(text, path string) ([][]string, error) {
		matches := aptInstallPattern.FindAllStringSubmatch(text, -1)
		if len(matches) == 0 {
			return nil, fmt.Errorf("unable to find apt install blocks in %s", path)
		}
		blocks := make([][]string, 0, len(matches))
		for _, match := range matches {
			body := strings.ReplaceAll(match[1], "\\", " ")
			fields := strings.Fields(body)
			if len(fields) == 0 {
				return nil, fmt.Errorf("unable to extract package list from install block in %s", path)
			}
			blocks = append(blocks, fields)
		}
		return blocks, nil
	}
	requireExactPackages := func(actual, expected []string, label, path string) error {
		if len(actual) != len(expected) {
			return fmt.Errorf("%s package set in %s changed.\nexpected: %v\nactual:   %v", label, path, expected, actual)
		}
		for i := range actual {
			if actual[i] != expected[i] {
				return fmt.Errorf("%s package set in %s changed.\nexpected: %v\nactual:   %v", label, path, expected, actual)
			}
		}
		return nil
	}
	requireRegex := func(text, pattern, label, path string) (*regexp.Regexp, []string, error) {
		re := regexp.MustCompile(pattern)
		match := re.FindStringSubmatch(text)
		if match == nil {
			return nil, nil, fmt.Errorf("%s in %s must match %q", label, path, pattern)
		}
		return re, match, nil
	}
	requireActionRef := func(text, action, path string) (string, error) {
		re := regexp.MustCompile(regexp.QuoteMeta(action) + `@([0-9a-f]{40})`)
		matches := re.FindAllStringSubmatch(text, -1)
		if len(matches) == 0 {
			return "", fmt.Errorf("%s must pin %s to an immutable commit SHA", path, action)
		}
		refs := map[string]struct{}{}
		for _, match := range matches {
			refs[match[1]] = struct{}{}
		}
		if len(refs) != 1 {
			return "", fmt.Errorf("%s must use a single reviewed ref for %s", path, action)
		}
		for ref := range refs {
			return ref, nil
		}
		return "", nil
	}
	requireGoDirective := func(text, directive, path string) (string, error) {
		match := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(directive) + ` ([0-9]+\.[0-9]+\.[0-9]+)$`).FindStringSubmatch(text)
		if match == nil {
			return "", fmt.Errorf("unable to extract %s from %s", directive, path)
		}
		return match[1], nil
	}
	requireToolchainDirective := func(text, path string) (string, error) {
		match := regexp.MustCompile(`(?m)^toolchain go([0-9]+\.[0-9]+\.[0-9]+)$`).FindStringSubmatch(text)
		if match == nil {
			return "", fmt.Errorf("unable to extract toolchain from %s", path)
		}
		return match[1], nil
	}
	requireTOMLString := func(text, key, path string) (string, error) {
		match := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(key) + `\s*=\s*"([^"]+)"\s*$`).FindStringSubmatch(text)
		if match == nil {
			return "", fmt.Errorf("unable to extract %s from %s", key, path)
		}
		return match[1], nil
	}
	requireEqual := func(label, left, leftPath, right, rightPath string) error {
		if left != right {
			return fmt.Errorf("%s must match between %s (%q) and %s (%q)", label, leftPath, left, rightPath, right)
		}
		return nil
	}
	majorMinor := func(version, path string) (string, error) {
		match := regexp.MustCompile(`^([0-9]+\.[0-9]+)\.[0-9]+$`).FindStringSubmatch(version)
		if match == nil {
			return "", fmt.Errorf("expected a semantic version in %s, found %q", path, version)
		}
		return match[1], nil
	}
	goLanguageVersionFromToolchain := func(version, path string) (string, error) {
		match := regexp.MustCompile(`^([0-9]+\.[0-9]+)\.[0-9]+$`).FindStringSubmatch(version)
		if match == nil {
			return "", fmt.Errorf("expected a semantic Go toolchain version in %s, found %q", path, version)
		}
		return match[1] + ".0", nil
	}
	extractReproMatrixEntries := func(strategyBlock, path string) ([][3]string, error) {
		re := regexp.MustCompile(`(?m)^\s{10}- platform:\s*(\S+)\n^\s{12}platform_name:\s*(\S+)\n^\s{12}runner:\s*(\S+)$`)
		matches := re.FindAllStringSubmatch(strategyBlock, -1)
		if len(matches) == 0 {
			return nil, fmt.Errorf("unable to extract reproducible-build matrix entries from %s", path)
		}
		result := make([][3]string, 0, len(matches))
		for _, match := range matches {
			result = append(result, [3]string{match[1], match[2], match[3]})
		}
		return result, nil
	}
	requireNoRegistryBootstrapMCP := func(text, path string) error {
		disallowedFragments := []string{
			"npx",
			"npm exec",
			"pnpm dlx",
			"yarn dlx",
			"bunx",
			"@upstash/context7-mcp",
			"exa-mcp-server",
		}
		lines := strings.Split(text, "\n")
		for _, line := range lines {
			line = stripComment(line)
			lower := strings.ToLower(line)
			for _, fragment := range disallowedFragments {
				if strings.Contains(lower, fragment) {
					return fmt.Errorf("%s must not seed mutable registry-backed MCP commands; found %q", path, line)
				}
			}
		}
		return nil
	}

	runtimeBaseImage, err := requireArg(runtimeDockerfile, "NODE_BASE_IMAGE", cfg.RuntimeDockerfilePath)
	if err != nil {
		return err
	}
	validatorBaseImage, err := requireArg(validatorDockerfile, "VALIDATOR_BASE_IMAGE", cfg.ValidatorDockerfilePath)
	if err != nil {
		return err
	}
	runtimeSnapshot, err := requireArg(runtimeDockerfile, "DEBIAN_SNAPSHOT", cfg.RuntimeDockerfilePath)
	if err != nil {
		return err
	}
	validatorSnapshot, err := requireArg(validatorDockerfile, "DEBIAN_SNAPSHOT", cfg.ValidatorDockerfilePath)
	if err != nil {
		return err
	}
	codexVersion, err := requireArg(runtimeDockerfile, "CODEX_VERSION", cfg.RuntimeDockerfilePath)
	if err != nil {
		return err
	}
	claudeVersion, err := requireArg(runtimeDockerfile, "CLAUDE_VERSION", cfg.RuntimeDockerfilePath)
	if err != nil {
		return err
	}

	runtimeInstallBlocks, err := extractInstallBlocks(runtimeDockerfile, cfg.RuntimeDockerfilePath)
	if err != nil {
		return err
	}
	validatorInstallBlocks, err := extractInstallBlocks(validatorDockerfile, cfg.ValidatorDockerfilePath)
	if err != nil {
		return err
	}

	if err := requirePinnedBaseImage(runtimeBaseImage, "NODE_BASE_IMAGE", cfg.RuntimeDockerfilePath); err != nil {
		return err
	}
	if err := requirePinnedBaseImage(validatorBaseImage, "VALIDATOR_BASE_IMAGE", cfg.ValidatorDockerfilePath); err != nil {
		return err
	}
	if err := verifySnapshotFreshness(runtimeSnapshot, cfg.RuntimeDockerfilePath); err != nil {
		return err
	}
	if err := verifySnapshotFreshness(validatorSnapshot, cfg.ValidatorDockerfilePath); err != nil {
		return err
	}
	if err := requireNoRegistryBootstrapMCP(codexRequirementsText, cfg.CodexRequirementsPath); err != nil {
		return err
	}
	if err := requireNoRegistryBootstrapMCP(codexMCPConfigText, cfg.CodexMCPConfigPath); err != nil {
		return err
	}
	if err := CheckProviderBumpPolicy(cfg.ProviderBumpPolicyPath, cfg.RuntimeDockerfilePath, cfg.ProvidersPackageJSONPath); err != nil {
		return err
	}
	if _, _, err := requireRegex(runtimeDockerfile, `curl -fsSL "https://storage\.googleapis\.com/claude-code-dist-86c565f3-f756-42ad-8dfa-d59b1c096819/claude-code-releases/\$\{CLAUDE_VERSION\}/\$\{CLAUDE_PLATFORM\}/claude"`, "Claude native release download URL", cfg.RuntimeDockerfilePath); err != nil {
		return err
	}
	if _, match, err := requireRegex(runtimeDockerfile, `(?m)^\s*arm64\)\s+\\\s*CLAUDE_PLATFORM="([^"]+)";\s+\\\s*CLAUDE_SHA256="([0-9a-f]{64})";`, "arm64 Claude mapping", cfg.RuntimeDockerfilePath); err != nil {
		return err
	} else if match[1] != "linux-arm64" {
		return fmt.Errorf("arm64 Claude mapping in %s must use linux-arm64", cfg.RuntimeDockerfilePath)
	}
	if _, match, err := requireRegex(runtimeDockerfile, `(?m)^\s*amd64\)\s+\\\s*CLAUDE_PLATFORM="([^"]+)";\s+\\\s*CLAUDE_SHA256="([0-9a-f]{64})";`, "amd64 Claude mapping", cfg.RuntimeDockerfilePath); err != nil {
		return err
	} else if match[1] != "linux-x64" {
		return fmt.Errorf("amd64 Claude mapping in %s must use linux-x64", cfg.RuntimeDockerfilePath)
	}
	if !regexp.MustCompile(`^0\.[0-9]+\.[0-9]+(?:-[A-Za-z0-9.-]+)?$`).MatchString(codexVersion) {
		return fmt.Errorf("runtime/container/Dockerfile CODEX_VERSION must stay pinned to an explicit release, found %q", codexVersion)
	}
	if !regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[A-Za-z0-9.-]+)?$`).MatchString(claudeVersion) {
		return fmt.Errorf("runtime/container/Dockerfile CLAUDE_VERSION must stay pinned to an explicit release, found %q", claudeVersion)
	}
	if _, match, err := requireRegex(runtimeDockerfile, `(?m)^\s*arm64\)\s+\\(?:\s*CLAUDE_[A-Z0-9_]+="[^"]+";\s+\\)*\s*CODEX_ARCH="([^"]+)";\s+\\\s*CODEX_SHA256="([0-9a-f]{64})";`, "arm64 Codex mapping", cfg.RuntimeDockerfilePath); err != nil {
		return err
	} else if match[1] != "aarch64-unknown-linux-gnu" {
		return fmt.Errorf("arm64 Codex mapping in %s must use aarch64-unknown-linux-gnu", cfg.RuntimeDockerfilePath)
	}
	if _, match, err := requireRegex(runtimeDockerfile, `(?m)^\s*amd64\)\s+\\(?:\s*CLAUDE_[A-Z0-9_]+="[^"]+";\s+\\)*\s*CODEX_ARCH="([^"]+)";\s+\\\s*CODEX_SHA256="([0-9a-f]{64})";`, "amd64 Codex mapping", cfg.RuntimeDockerfilePath); err != nil {
		return err
	} else if match[1] != "x86_64-unknown-linux-gnu" {
		return fmt.Errorf("amd64 Codex mapping in %s must use x86_64-unknown-linux-gnu", cfg.RuntimeDockerfilePath)
	}
	if len(runtimeInstallBlocks) != 2 {
		return fmt.Errorf("runtime/container/Dockerfile must contain exactly two apt install blocks (runtime base and runtime builder)")
	}
	if len(validatorInstallBlocks) != 1 {
		return errors.New("tools/validator/Dockerfile must contain exactly one apt install block")
	}
	if err := requireExactPackages(runtimeInstallBlocks[0], []string{"bash", "bubblewrap", "ca-certificates", "curl", "fd-find", "git", "jq", "less", "openssh-client", "passwd", "procps", "ripgrep", "sudo", "unzip", "util-linux", "xz-utils"}, "Runtime base", cfg.RuntimeDockerfilePath); err != nil {
		return err
	}
	if err := requireExactPackages(runtimeInstallBlocks[1], []string{"gcc", "libc6-dev"}, "Runtime builder", cfg.RuntimeDockerfilePath); err != nil {
		return err
	}
	if err := requireExactPackages(validatorInstallBlocks[0], []string{"ca-certificates", "codespell", "curl", "gcc", "git", "groff-base", "jq", "libc6-dev", "llvm", "mandoc", "openssh-client", "procps", "shellcheck", "shfmt", "yamllint"}, "Validator", cfg.ValidatorDockerfilePath); err != nil {
		return err
	}
	goLanguageVersion, err := requireGoDirective(goModText, "go", goModPath)
	if err != nil {
		return err
	}
	goToolchainVersion, err := requireToolchainDirective(goModText, goModPath)
	if err != nil {
		return err
	}
	validatorGoVersion, err := requireArg(validatorDockerfile, "GO_VERSION", cfg.ValidatorDockerfilePath)
	if err != nil {
		return err
	}
	if err := requireEqual("Go toolchain version", goToolchainVersion, goModPath, validatorGoVersion, cfg.ValidatorDockerfilePath); err != nil {
		return err
	}
		expectedGoLanguageVersion, err := goLanguageVersionFromToolchain(goToolchainVersion, goModPath)
		if err != nil {
			return err
		}
		if goLanguageVersion != expectedGoLanguageVersion {
			return fmt.Errorf("go language version in %s must match the toolchain major/minor at patch zero, expected %q, found %q", goModPath, expectedGoLanguageVersion, goLanguageVersion)
		}
		validatorGoSHAx86_64, err := requireArg(validatorDockerfile, "GO_LINUX_X86_64_SHA256", cfg.ValidatorDockerfilePath)
		if err != nil {
			return err
		}
		if !isHexDigest(validatorGoSHAx86_64) {
			return fmt.Errorf("GO_LINUX_X86_64_SHA256 in %s must be a full SHA256 digest, found %q", cfg.ValidatorDockerfilePath, validatorGoSHAx86_64)
		}
		validatorGoSHAArm64, err := requireArg(validatorDockerfile, "GO_LINUX_ARM64_SHA256", cfg.ValidatorDockerfilePath)
		if err != nil {
			return err
		}
		if !isHexDigest(validatorGoSHAArm64) {
			return fmt.Errorf("GO_LINUX_ARM64_SHA256 in %s must be a full SHA256 digest, found %q", cfg.ValidatorDockerfilePath, validatorGoSHAArm64)
		}
		validatorHadolintVersion, err := requireArg(validatorDockerfile, "HADOLINT_VERSION", cfg.ValidatorDockerfilePath)
		if err != nil {
			return err
		}
		if !regexp.MustCompile(`^v\d+\.\d+\.\d+$`).MatchString(validatorHadolintVersion) {
			return fmt.Errorf("HADOLINT_VERSION must be an exact pinned release, found %q", validatorHadolintVersion)
		}
		validatorHadolintSHAx86_64, err := requireArg(validatorDockerfile, "HADOLINT_LINUX_X86_64_SHA256", cfg.ValidatorDockerfilePath)
		if err != nil {
			return err
		}
		if !isHexDigest(validatorHadolintSHAx86_64) {
			return fmt.Errorf("HADOLINT_LINUX_X86_64_SHA256 in %s must be a full SHA256 digest, found %q", cfg.ValidatorDockerfilePath, validatorHadolintSHAx86_64)
		}
		validatorHadolintSHAArm64, err := requireArg(validatorDockerfile, "HADOLINT_LINUX_ARM64_SHA256", cfg.ValidatorDockerfilePath)
		if err != nil {
			return err
		}
		if !isHexDigest(validatorHadolintSHAArm64) {
			return fmt.Errorf("HADOLINT_LINUX_ARM64_SHA256 in %s must be a full SHA256 digest, found %q", cfg.ValidatorDockerfilePath, validatorHadolintSHAArm64)
		}
		validatorMarkdownlintVersion, err := requireArg(validatorDockerfile, "MARKDOWNLINT_VERSION", cfg.ValidatorDockerfilePath)
		if err != nil {
			return err
		}
		if !regexp.MustCompile(`^0\.\d+\.\d+$`).MatchString(validatorMarkdownlintVersion) {
			return fmt.Errorf("MARKDOWNLINT_VERSION must be an exact pinned release, found %q", validatorMarkdownlintVersion)
		}
	for _, needle := range []string{
		`COPY tools/markdownlint/package.json tools/markdownlint/package-lock.json /usr/local/lib/workcell-markdownlint/`,
		`npm ci --prefix /usr/local/lib/workcell-markdownlint --ignore-scripts --omit=dev`,
		`ln -sf /usr/local/lib/workcell-markdownlint/node_modules/.bin/markdownlint /usr/local/bin/markdownlint`,
		`markdownlint --version | grep -F "${MARKDOWNLINT_VERSION}" >/dev/null`,
	} {
		if !strings.Contains(validatorDockerfile, needle) {
			return fmt.Errorf("%s must contain %q", cfg.ValidatorDockerfilePath, needle)
		}
	}
	cargoEdition, err := requireTOMLString(cargoManifestText, "edition", cargoManifestPath)
	if err != nil {
		return err
	}
	if cargoEdition != "2024" {
		return fmt.Errorf("%s must use edition 2024, found %q", cargoManifestPath, cargoEdition)
	}
	cargoRustVersion, err := requireTOMLString(cargoManifestText, "rust-version", cargoManifestPath)
	if err != nil {
		return err
	}
	rustToolchainVersion, err := requireTOMLString(rustToolchainText, "channel", rustToolchainPath)
	if err != nil {
		return err
	}
	runtimeRustVersion, err := requireArg(runtimeDockerfile, "RUST_VERSION", cfg.RuntimeDockerfilePath)
	if err != nil {
		return err
	}
	runtimeRustToolchainImage, err := requireArg(runtimeDockerfile, "RUST_TOOLCHAIN_IMAGE", cfg.RuntimeDockerfilePath)
	if err != nil {
		return err
	}
	validatorRustVersion, err := requireArg(validatorDockerfile, "RUST_VERSION", cfg.ValidatorDockerfilePath)
	if err != nil {
		return err
	}
	if err := requireEqual("RUST_VERSION", runtimeRustVersion, cfg.RuntimeDockerfilePath, validatorRustVersion, cfg.ValidatorDockerfilePath); err != nil {
		return err
	}
	if err := requireEqual("Rust toolchain channel", rustToolchainVersion, rustToolchainPath, runtimeRustVersion, cfg.RuntimeDockerfilePath); err != nil {
		return err
	}
	if err := requirePinnedBaseImage(runtimeRustToolchainImage, "RUST_TOOLCHAIN_IMAGE", cfg.RuntimeDockerfilePath); err != nil {
		return err
	}
	expectedRustToolchainTrack := fmt.Sprintf("rust:%s-slim-trixie@", runtimeRustVersion)
	if !strings.Contains(runtimeRustToolchainImage, expectedRustToolchainTrack) {
		return fmt.Errorf("RUST_TOOLCHAIN_IMAGE in %s must pin the official rust:%s-slim-trixie image, found %q", cfg.RuntimeDockerfilePath, runtimeRustVersion, runtimeRustToolchainImage)
	}
	expectedCargoRustVersion, err := majorMinor(rustToolchainVersion, rustToolchainPath)
	if err != nil {
		return err
	}
	if cargoRustVersion != expectedCargoRustVersion {
		return fmt.Errorf("rust-version in %s must match the pinned toolchain major/minor, expected %q, found %q", cargoManifestPath, expectedCargoRustVersion, cargoRustVersion)
	}
	validatorRustupVersion, err := requireArg(validatorDockerfile, "RUSTUP_VERSION", cfg.ValidatorDockerfilePath)
	if err != nil {
		return err
	}
	if !regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[A-Za-z0-9.-]+)?$`).MatchString(validatorRustupVersion) {
		return fmt.Errorf("RUSTUP_VERSION must be an exact pinned release, found %q", validatorRustupVersion)
	}
	validatorRustupSHAx86_64, err := requireArg(validatorDockerfile, "RUSTUP_INIT_LINUX_X86_64_SHA256", cfg.ValidatorDockerfilePath)
	if err != nil {
		return err
	}
	if !isHexDigest(validatorRustupSHAx86_64) {
		return fmt.Errorf("RUSTUP_INIT_LINUX_X86_64_SHA256 in %s must be a full SHA256 digest, found %q", cfg.ValidatorDockerfilePath, validatorRustupSHAx86_64)
	}
	validatorRustupSHAArm64, err := requireArg(validatorDockerfile, "RUSTUP_INIT_LINUX_ARM64_SHA256", cfg.ValidatorDockerfilePath)
	if err != nil {
		return err
	}
	if !isHexDigest(validatorRustupSHAArm64) {
		return fmt.Errorf("RUSTUP_INIT_LINUX_ARM64_SHA256 in %s must be a full SHA256 digest, found %q", cfg.ValidatorDockerfilePath, validatorRustupSHAArm64)
	}

	rootPackage, _ := providersPackageLock["packages"].(map[string]any)
	rootDependencies, _ := rootPackage[""].(map[string]any)
	expectedDependencies, _ := providersPackageJSON["dependencies"].(map[string]any)
	actualDependencies, _ := rootDependencies["dependencies"].(map[string]any)
	if len(actualDependencies) != len(expectedDependencies) {
		return errors.New("runtime/container/providers/package-lock.json root dependencies do not match package.json")
	}
	for name, expected := range expectedDependencies {
		if actualDependencies[name] != expected {
			return errors.New("runtime/container/providers/package-lock.json root dependencies do not match package.json")
		}
	}
	for packageName, expectedVersionAny := range expectedDependencies {
		expectedVersion, _ := expectedVersionAny.(string)
		pkgEntry, ok := rootPackage["node_modules/"+packageName].(map[string]any)
		if !ok {
			return fmt.Errorf("missing pinned provider package entry for %s", packageName)
		}
		if version, _ := pkgEntry["version"].(string); version != expectedVersion {
			return fmt.Errorf("pinned provider package %s is %s, expected %s", packageName, version, expectedVersion)
		}
		if integrity, _ := pkgEntry["integrity"].(string); integrity == "" {
			return fmt.Errorf("pinned provider package %s is missing an integrity hash", packageName)
		}
		if resolved, _ := pkgEntry["resolved"].(string); !strings.HasPrefix(resolved, "https://registry.npmjs.org/") {
			return fmt.Errorf("pinned provider package %s uses an unexpected source: %q", packageName, resolved)
		}
	}
	for packagePath, rawEntry := range rootPackage {
		if packagePath == "" {
			continue
		}
		entry, _ := rawEntry.(map[string]any)
		if link, _ := entry["link"].(bool); link {
			return fmt.Errorf("linked npm dependencies are not allowed in the provider lockfile: %s", packagePath)
		}
		if integrity, _ := entry["integrity"].(string); integrity == "" {
			return fmt.Errorf("provider lockfile entry is missing integrity data: %s", packagePath)
		}
		if resolved, _ := entry["resolved"].(string); !strings.HasPrefix(resolved, "https://registry.npmjs.org/") {
			return fmt.Errorf("provider lockfile entry uses an unexpected source (%s): %q", packagePath, resolved)
		}
	}

	ciBuildxVersion, err := requireYAMLKey(ciWorkflow, "WORKCELL_BUILDX_VERSION", ".github/workflows/ci.yml")
	if err != nil {
		return err
	}
	releaseBuildxVersion, err := requireYAMLKey(releaseWorkflow, "WORKCELL_BUILDX_VERSION", ".github/workflows/release.yml")
	if err != nil {
		return err
	}
	if ciBuildxVersion != releaseBuildxVersion {
		return errors.New("WORKCELL_BUILDX_VERSION must match between .github/workflows/ci.yml and .github/workflows/release.yml")
	}
	if !regexp.MustCompile(`^v\d+\.\d+\.\d+$`).MatchString(ciBuildxVersion) {
		return fmt.Errorf("WORKCELL_BUILDX_VERSION must be an exact pinned release (for example v0.32.1), found %q", ciBuildxVersion)
	}

	ciQEMUImage, err := requireYAMLKey(ciWorkflow, "WORKCELL_QEMU_IMAGE", ".github/workflows/ci.yml")
	if err != nil {
		return err
	}
	releaseQEMUImage, err := requireYAMLKey(releaseWorkflow, "WORKCELL_QEMU_IMAGE", ".github/workflows/release.yml")
	if err != nil {
		return err
	}
	if ciQEMUImage != releaseQEMUImage {
		return errors.New("WORKCELL_QEMU_IMAGE must match between .github/workflows/ci.yml and .github/workflows/release.yml")
	}
	if err := requirePinnedBaseImage(ciQEMUImage, "WORKCELL_QEMU_IMAGE", ".github/workflows/ci.yml"); err != nil {
		return err
	}
	ciBuildkitImage, err := requireYAMLKey(ciWorkflow, "WORKCELL_BUILDKIT_IMAGE", ".github/workflows/ci.yml")
	if err != nil {
		return err
	}
	releaseBuildkitImage, err := requireYAMLKey(releaseWorkflow, "WORKCELL_BUILDKIT_IMAGE", ".github/workflows/release.yml")
	if err != nil {
		return err
	}
	if ciBuildkitImage != releaseBuildkitImage {
		return errors.New("WORKCELL_BUILDKIT_IMAGE must match between .github/workflows/ci.yml and .github/workflows/release.yml")
	}
	if err := requirePinnedBaseImage(ciBuildkitImage, "WORKCELL_BUILDKIT_IMAGE", ".github/workflows/ci.yml"); err != nil {
		return err
	}

	ciCosignVersion, err := requireYAMLKey(ciWorkflow, "WORKCELL_COSIGN_VERSION", ".github/workflows/ci.yml")
	if err != nil {
		return err
	}
	releaseCosignVersion, err := requireYAMLKey(releaseWorkflow, "WORKCELL_COSIGN_VERSION", ".github/workflows/release.yml")
	if err != nil {
		return err
	}
	pinHygieneCosignVersion, err := requireYAMLKey(pinHygieneWorkflow, "WORKCELL_COSIGN_VERSION", ".github/workflows/pin-hygiene.yml")
	if err != nil {
		return err
	}
	upstreamRefreshCosignVersion, err := requireYAMLKey(upstreamRefreshWorkflow, "WORKCELL_COSIGN_VERSION", ".github/workflows/upstream-refresh.yml")
	if err != nil {
		return err
	}
	if len(map[string]struct{}{ciCosignVersion: {}, releaseCosignVersion: {}, pinHygieneCosignVersion: {}, upstreamRefreshCosignVersion: {}}) != 1 {
		return errors.New("WORKCELL_COSIGN_VERSION must match between .github/workflows/ci.yml, .github/workflows/release.yml, .github/workflows/pin-hygiene.yml, and .github/workflows/upstream-refresh.yml")
	}
	if !regexp.MustCompile(`^v\d+\.\d+\.\d+$`).MatchString(ciCosignVersion) {
		return fmt.Errorf("WORKCELL_COSIGN_VERSION must be an exact pinned release, found %q", ciCosignVersion)
	}
	for _, workflow := range []struct {
		text string
		path string
	}{{ciWorkflow, ".github/workflows/ci.yml"}, {releaseWorkflow, ".github/workflows/release.yml"}, {pinHygieneWorkflow, ".github/workflows/pin-hygiene.yml"}, {upstreamRefreshWorkflow, ".github/workflows/upstream-refresh.yml"}} {
		if !strings.Contains(workflow.text, "cosign-release: ${{ env.WORKCELL_COSIGN_VERSION }}") {
			return fmt.Errorf("%s must pin the installed cosign binary release", workflow.path)
		}
	}
	ciCosignInstallerRef, err := requireActionRef(ciWorkflow, "sigstore/cosign-installer", ".github/workflows/ci.yml")
	if err != nil {
		return err
	}
	releaseCosignInstallerRef, err := requireActionRef(releaseWorkflow, "sigstore/cosign-installer", ".github/workflows/release.yml")
	if err != nil {
		return err
	}
	pinHygieneCosignInstallerRef, err := requireActionRef(pinHygieneWorkflow, "sigstore/cosign-installer", ".github/workflows/pin-hygiene.yml")
	if err != nil {
		return err
	}
	upstreamRefreshCosignInstallerRef, err := requireActionRef(upstreamRefreshWorkflow, "sigstore/cosign-installer", ".github/workflows/upstream-refresh.yml")
	if err != nil {
		return err
	}
	if len(map[string]struct{}{ciCosignInstallerRef: {}, releaseCosignInstallerRef: {}, pinHygieneCosignInstallerRef: {}, upstreamRefreshCosignInstallerRef: {}}) != 1 {
		return errors.New("sigstore/cosign-installer must use the same reviewed commit SHA in .github/workflows/ci.yml, .github/workflows/release.yml, .github/workflows/pin-hygiene.yml, and .github/workflows/upstream-refresh.yml")
	}
	if !strings.Contains(ciWorkflow, "driver-opts: image=${{ env.WORKCELL_BUILDKIT_IMAGE }}") {
		return errors.New(".github/workflows/ci.yml must pin the BuildKit daemon image used by setup-buildx-action")
	}
	if !strings.Contains(releaseWorkflow, "driver-opts: image=${{ env.WORKCELL_BUILDKIT_IMAGE }}") {
		return errors.New(".github/workflows/release.yml must pin the BuildKit daemon image used by setup-buildx-action")
	}
	if !strings.Contains(ciWorkflow, "cache-binary: true") {
		return errors.New("pinned buildx binary caching must stay enabled in .github/workflows/ci.yml")
	}
	extractBetween := func(text, startMarker, endMarker, label string) (string, error) {
		start := strings.Index(text, startMarker)
		if start < 0 {
			return "", fmt.Errorf("unable to extract %s from .github/workflows/ci.yml", label)
		}
		remaining := text[start:]
		end := strings.Index(remaining, endMarker)
		if end < 0 {
			return "", fmt.Errorf("unable to extract %s from .github/workflows/ci.yml", label)
		}
		return remaining[:end+1], nil
	}
	ciReproBuildJob := ""
	if start := strings.Index(ciWorkflow, "  reproducible-build-platform:\n"); start >= 0 {
		remaining := ciWorkflow[start:]
		if end := strings.Index(remaining, "\n  reproducible-build:\n"); end >= 0 {
			ciReproBuildJob = remaining[:end+1]
		} else {
			ciReproBuildJob = remaining
		}
	}
	if ciReproBuildJob == "" {
		return errors.New("unable to extract reproducible-build-platform job from .github/workflows/ci.yml")
	}
	if !regexp.MustCompile(`(?m)^\s{4}runs-on:\s*\$\{\{\s*matrix\.runner\s*\}\}$`).MatchString(ciReproBuildJob) {
		return errors.New(".github/workflows/ci.yml must route reproducible-build-platform through runs-on: ${{ matrix.runner }}")
	}
	ciReproStrategyBlock, err := extractBetween(ciReproBuildJob, "    strategy:\n", "\n    steps:\n", "reproducible-build-platform strategy block")
	if err != nil {
		return errors.New("unable to extract reproducible-build-platform strategy block from .github/workflows/ci.yml")
	}
	expectedCiReproStrategyBlock := "    strategy:\n" +
		"      fail-fast: false\n" +
		"      matrix:\n" +
		"        include:\n" +
		"          - platform: linux/amd64\n" +
		"            platform_name: amd64\n" +
		"            runner: ubuntu-latest\n" +
		"          - platform: linux/arm64\n" +
		"            platform_name: arm64\n" +
		"            runner: ubuntu-24.04-arm\n"
	if ciReproStrategyBlock != expectedCiReproStrategyBlock {
		return errors.New(".github/workflows/ci.yml must keep the reviewed reproducible-build matrix structure, including a single native ubuntu-24.04-arm lane for linux/arm64")
	}
	entries, err := extractReproMatrixEntries(ciReproStrategyBlock, ".github/workflows/ci.yml")
	if err != nil {
		return err
	}
	arm64Entries := make([][3]string, 0)
	for _, entry := range entries {
		if entry[0] == "linux/arm64" {
			arm64Entries = append(arm64Entries, entry)
		}
	}
	if len(arm64Entries) != 1 || arm64Entries[0] != [3]string{"linux/arm64", "arm64", "ubuntu-24.04-arm"} {
		return errors.New(".github/workflows/ci.yml must define exactly one linux/arm64 reproducible-build matrix entry and it must use runner ubuntu-24.04-arm")
	}
	if strings.Contains(ciWorkflow, "docker/setup-qemu-action@") {
		return errors.New(".github/workflows/ci.yml must not configure QEMU in CI now that arm64 reproducible builds use a native runner")
	}
	if err := validateMacOSInstallVerificationFlow(ciWorkflow, ".github/workflows/ci.yml", "workcell-ci-install-candidate", "name: Install verification (${{ matrix.runner_label }})"); err != nil {
		return err
	}
	if !strings.Contains(releaseWorkflow, "cache-binary: false") {
		return errors.New("the publishing release workflow must not cache the Buildx binary")
	}
	if !strings.Contains(releaseWorkflow, "cache-image: false") {
		return errors.New("the publishing release workflow must not cache the QEMU helper image")
	}
	releaseSyftVersion, err := requireYAMLKey(releaseWorkflow, "WORKCELL_SYFT_VERSION", ".github/workflows/release.yml")
	if err != nil {
		return err
	}
	if !regexp.MustCompile(`^v\d+\.\d+\.\d+$`).MatchString(releaseSyftVersion) {
		return fmt.Errorf("WORKCELL_SYFT_VERSION must be an exact pinned release, found %q", releaseSyftVersion)
	}
	if !strings.Contains(releaseWorkflow, "syft-version: ${{ env.WORKCELL_SYFT_VERSION }}") {
		return errors.New(".github/workflows/release.yml must pin the Syft version used for release SBOM generation")
	}
	if !strings.Contains(releaseWorkflow, "anchore/sbom-action/download-syft@") {
		return errors.New(".github/workflows/release.yml must install the pinned Syft CLI before generating the builder environment manifest")
	}
	securityWorkflow, err := readText(filepath.Join(cfg.WorkflowsDir, "security.yml"))
	if err != nil {
		return err
	}
	_, securityActionlintVersionMatch, err := requireRegex(securityWorkflow, `(?m)^\s*ACTIONLINT_VERSION:\s*([0-9]+\.[0-9]+\.[0-9]+)\s*$`, "security actionlint version", ".github/workflows/security.yml")
	if err != nil {
		return err
	}
	_, releaseActionlintVersionMatch, err := requireRegex(releaseWorkflow, `(?m)^\s*ACTIONLINT_VERSION:\s*([0-9]+\.[0-9]+\.[0-9]+)\s*$`, "release actionlint version", ".github/workflows/release.yml")
	if err != nil {
		return err
	}
	if securityActionlintVersionMatch[1] != releaseActionlintVersionMatch[1] {
		return errors.New("ACTIONLINT_VERSION must match between .github/workflows/security.yml and .github/workflows/release.yml")
	}
	_, securityActionlintSHAMatch, err := requireRegex(securityWorkflow, `(?m)^\s*ACTIONLINT_SHA256:\s*([0-9a-f]{64})\s*$`, "security actionlint sha", ".github/workflows/security.yml")
	if err != nil {
		return err
	}
	_, releaseActionlintSHAMatch, err := requireRegex(releaseWorkflow, `(?m)^\s*ACTIONLINT_SHA256:\s*([0-9a-f]{64})\s*$`, "release actionlint sha", ".github/workflows/release.yml")
	if err != nil {
		return err
	}
	if securityActionlintSHAMatch[1] != releaseActionlintSHAMatch[1] {
		return errors.New("ACTIONLINT_SHA256 must match between .github/workflows/security.yml and .github/workflows/release.yml")
	}
	for _, workflow := range []struct {
		text string
		path string
	}{
		{text: securityWorkflow, path: ".github/workflows/security.yml"},
		{text: releaseWorkflow, path: ".github/workflows/release.yml"},
	} {
		if !strings.Contains(workflow.text, "https://github.com/rhysd/actionlint/releases/download/v${ACTIONLINT_VERSION}/actionlint_${ACTIONLINT_VERSION}_linux_amd64.tar.gz") {
			return fmt.Errorf("%s must derive the actionlint archive URL from ACTIONLINT_VERSION", workflow.path)
		}
		if !strings.Contains(workflow.text, "--max-time 60") {
			return fmt.Errorf("%s must bound actionlint download wall-clock time", workflow.path)
		}
		if !strings.Contains(workflow.text, "--connect-timeout 15") {
			return fmt.Errorf("%s must bound actionlint download connect time", workflow.path)
		}
	}
	for _, needle := range []string{
		"github.event_name == 'workflow_dispatch' && github.ref_name != 'main'",
		"base-ref: ${{ github.event_name == 'workflow_dispatch' && 'refs/heads/main' || '' }}",
		"head-ref: ${{ github.event_name == 'workflow_dispatch' && github.ref || '' }}",
	} {
		if !strings.Contains(securityWorkflow, needle) {
			return fmt.Errorf(".github/workflows/security.yml must contain %q", needle)
		}
	}
	if !strings.Contains(releaseWorkflow, "docker buildx imagetools create") {
		return errors.New(".github/workflows/release.yml must assemble the published multi-arch manifest with docker buildx imagetools create")
	}
	if regexp.MustCompile(`docker/build-push-action@.*?platforms:\s*linux/amd64,linux/arm64`).MatchString(releaseWorkflow) {
		return errors.New(".github/workflows/release.yml must not publish the final multi-arch image through one opaque multi-platform build-push step")
	}
	if !strings.Contains(runtimeDockerfile, "COPY runtime/container/rust /workcell-rust") {
		return errors.New("runtime/container/Dockerfile must vendor the reviewed Rust runtime sources into the builder stage")
	}
	for _, needle := range []string{
		"COPY --from=rust-toolchain /usr/local/cargo /usr/local/cargo",
		"COPY --from=rust-toolchain /usr/local/rustup /usr/local/rustup",
	} {
		if !strings.Contains(runtimeDockerfile, needle) {
			return fmt.Errorf("runtime/container/Dockerfile must copy the pinned Rust toolchain through %q", needle)
		}
	}
	if !strings.Contains(runtimeDockerfile, "COPY runtime/container/control-plane-manifest.json /usr/local/libexec/workcell/control-plane-manifest.json") {
		return errors.New("runtime/container/Dockerfile must copy the reviewed control-plane manifest into the runtime image")
	}
	hasOfflineCargoBuild := strings.Contains(runtimeDockerfile, "cargo build \\") ||
		strings.Contains(runtimeDockerfile, "\"${toolchain_bin}/cargo\" build \\")
	if !hasOfflineCargoBuild || !strings.Contains(runtimeDockerfile, "--locked \\") || !strings.Contains(runtimeDockerfile, "--offline \\") {
		return errors.New("runtime/container/Dockerfile must build the shipped Rust launcher artifacts with cargo --locked --offline")
	}
	if !strings.Contains(runtimeDockerfile, "CARGO_HOME=/workcell-rust/cargo-home") {
		return errors.New("runtime/container/Dockerfile must isolate Cargo home inside the vendored runtime source tree")
	}
	for _, needle := range []string{
		"name: workcell-release-preflight",
		"name: workcell-release-install-candidate",
		"name: Release install verification (${{ matrix.runner_label }})",
		"brew tap-new",
		"brew --repo",
		"brew install \"${tap_name}/workcell\"",
		"macos-26",
		"macos-15",
		"actions/download-artifact@",
		"context: dist/release-source",
		"name: Re-verify pinned upstreams from archived source tree",
		"name: Verify GitHub macOS release test runners",
		"working-directory: dist/release-source",
		"WORKCELL_BUILD_INPUT_ROOT: ${{ github.workspace }}/dist/release-source",
		"WORKCELL_CONTROL_PLANE_ROOT: ${{ github.workspace }}/dist/release-source",
		"Verify published platform digests match preflight",
		"docker buildx imagetools inspect --raw",
		"{{json .Manifest}}",
		"vnd.docker.reference.type",
		"ENABLE_GITHUB_ATTESTATIONS: ${{ vars.WORKCELL_ENABLE_GITHUB_ATTESTATIONS || 'false' }}",
		"actions/attest@",
		"Verify release bundle matches preflight",
		"Verify control-plane manifest matches preflight",
		"github/codeql-action/init@",
		"github/codeql-action/analyze@",
		"./scripts/publish-github-release.sh",
	} {
		if !strings.Contains(releaseWorkflow, needle) {
			return fmt.Errorf(".github/workflows/release.yml must contain %q", needle)
		}
	}
	if strings.Contains(releaseWorkflow, "{{json .manifest}}") {
		return errors.New(".github/workflows/release.yml must not use the unsupported lowercase Buildx .manifest template field")
	}
	if !strings.Contains(releaseWorkflow, "dist/${{ env.BUNDLE_NAME }}.sigstore.json") ||
		!strings.Contains(releaseWorkflow, "dist/workcell-control-plane.sigstore.json") ||
		!strings.Contains(releaseWorkflow, "dist/workcell-image.digest.sigstore.json") ||
		!strings.Contains(releaseWorkflow, "dist/workcell-source.spdx.sigstore.json") ||
		!strings.Contains(releaseWorkflow, "dist/workcell-image.spdx.sigstore.json") {
		return errors.New(".github/workflows/release.yml must publish direct signature bundles for release artifacts")
	}
	if err := validateReleaseWorkflowControlPlaneFlow(releaseWorkflow); err != nil {
		return err
	}
	if err := validateMacOSInstallVerificationFlow(releaseWorkflow, ".github/workflows/release.yml", "workcell-release-install-candidate", "name: Release install verification (${{ matrix.runner_label }})"); err != nil {
		return err
	}
	if err := validateReleaseWorkflowGitHubAttestationFlow(releaseWorkflow); err != nil {
		return err
	}
	if strings.Contains(releaseWorkflow, "steps.build.outputs.digest") {
		return errors.New(".github/workflows/release.yml must not keep referencing the old single-step multi-platform digest output")
	}
	if strings.Contains(releaseWorkflow, "gh release ") {
		return errors.New(".github/workflows/release.yml must not depend on an ambient gh CLI; use a pinned release-publish action")
	}
	if !strings.Contains(releaseWorkflow, "./scripts/publish-github-release.sh") {
		return errors.New(".github/workflows/release.yml must publish assets through the reviewed repo-local GitHub Release API script")
	}
	for _, needle := range []string{
		`run: ./scripts/run-hosted-controls-audit.sh "${GITHUB_REPOSITORY}"`,
		`WORKCELL_HOSTED_CONTROLS_REQUIRED: "1"`,
	} {
		if !strings.Contains(releaseWorkflow, needle) {
			return fmt.Errorf(".github/workflows/release.yml must contain %q", needle)
		}
	}
	if err := validateUpstreamRefreshWorkflow(upstreamRefreshWorkflow); err != nil {
		return err
	}
	hostedControlsWorkflow, err := readText(filepath.Join(cfg.WorkflowsDir, "hosted-controls.yml"))
	if err != nil {
		return err
	}
	for _, needle := range []string{
		`run: ./scripts/run-hosted-controls-audit.sh "${GITHUB_REPOSITORY}"`,
		`WORKCELL_HOSTED_CONTROLS_TOKEN: ${{ secrets.WORKCELL_HOSTED_CONTROLS_TOKEN }}`,
		`WORKCELL_HOSTED_CONTROLS_REQUIRED: "0"`,
	} {
		if !strings.Contains(hostedControlsWorkflow, needle) {
			return fmt.Errorf(".github/workflows/hosted-controls.yml must contain %q", needle)
		}
	}
	for _, needle := range []string{
		"./scripts/verify-github-macos-release-test-runners.sh",
		"./scripts/verify-upstream-gemini-release.sh",
		"./scripts/verify-upstream-claude-release.sh",
	} {
		if !strings.Contains(ciWorkflow, needle) {
			return fmt.Errorf(".github/workflows/ci.yml must contain %q", needle)
		}
		if !strings.Contains(releaseWorkflow, needle) {
			return fmt.Errorf(".github/workflows/release.yml must contain %q", needle)
		}
	}
	for _, needle := range []string{
		"./scripts/verify-github-macos-release-test-runners.sh",
		"./scripts/verify-upstream-codex-release.sh",
		"./scripts/verify-upstream-claude-release.sh",
		"./scripts/verify-upstream-gemini-release.sh",
		"./scripts/update-upstream-pins.sh --check",
	} {
		if !strings.Contains(pinHygieneWorkflow, needle) {
			return fmt.Errorf(".github/workflows/pin-hygiene.yml must contain %q", needle)
		}
	}
	for _, needle := range []string{
		"./scripts/update-upstream-pins.sh --check",
	} {
		if !strings.Contains(releaseWorkflow, needle) {
			return fmt.Errorf(".github/workflows/release.yml must contain %q", needle)
		}
	}
	for _, needle := range []string{
		"environment:\n      name: release",
		`sudo install -m 0755 "$(command -v cosign)" /usr/local/bin/cosign`,
		`sudo install -m 0755 "$(command -v syft)" /usr/local/bin/syft`,
		`actionlint_archive="${RUNNER_TEMP}/actionlint.tar.gz"`,
		`tar -xzf "${actionlint_archive}" -C "${RUNNER_TEMP}" actionlint`,
		"Reclaim runner space before reproducible image check",
		`docker image rm -f "workcell-validator:${GITHUB_SHA}" >/dev/null 2>&1 || true`,
		"git -c safe.directory=/workspace archive \\",
		"docker buildx prune -af || true",
	} {
		if !strings.Contains(releaseWorkflow, needle) {
			return fmt.Errorf(".github/workflows/release.yml must contain %q", needle)
		}
	}
	for _, workflowPath := range mustGlob(filepath.Join(cfg.WorkflowsDir, "*.yml")) {
		workflowText, err := readText(workflowPath)
		if err != nil {
			return err
		}
		if !workflowPermissionsRE.MatchString(workflowText) {
			return fmt.Errorf("workflow-level empty permissions declaration missing in %s", workflowPath)
		}
		if strings.Contains(workflowText, "pull_request_target") {
			return fmt.Errorf("%s must not contain pull_request_target triggers", workflowPath)
		}
		if regexp.MustCompile(`secrets\.[A-Z0-9_]*(?:PAT|PERSONAL_ACCESS_TOKEN)\b|GH_PAT\b|PERSONAL_ACCESS_TOKEN\b`).MatchString(workflowText) {
			return fmt.Errorf("%s must not contain long-lived personal access tokens", workflowPath)
		}
		for _, match := range regexp.MustCompile(`(?m)^\s*-\s+uses:\s+([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)@([^\s#]+)`).FindAllStringSubmatch(workflowText, -1) {
			if !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(match[2]) {
				return fmt.Errorf("%s must pin GitHub Actions by full commit SHA; found %s@%s", workflowPath, match[1], match[2])
			}
		}
	}
	for _, required := range []string{
		"/.github/workflows/ @omkhar",
		"/scripts/ @omkhar",
		"/runtime/container/ @omkhar",
		"/docs/provenance.md @omkhar",
	} {
		if !strings.Contains(codeowners, required) {
			return fmt.Errorf(".github/CODEOWNERS must declare high-risk ownership for %q", required)
		}
	}
	releaseEnvironment, _ := hostedControlsPolicy["release_environment"].(map[string]any)
	releaseMode, _ := releaseEnvironment["mode"].(string)
	if releaseMode != "review-gated" && releaseMode != "single-owner-public" && releaseMode != "single-owner-private" && releaseMode != "plan-limited-private" {
		return errors.New("policy/github-hosted-controls.toml must set release_environment.mode to 'review-gated', 'single-owner-public', 'single-owner-private', or 'plan-limited-private'")
	}
	if err := validateCanonicalHostedControlsRepositoryVariables(hostedControlsPolicy, "policy/github-hosted-controls.toml"); err != nil {
		return err
	}
	for _, needle := range []string{
		"gh api --paginate \"repos/${REPO}/actions/variables?per_page=100\"",
		"jq -s '{total_count: (map(.total_count // 0) | max // 0), variables: (map(.variables // []) | add)}'",
		`verify-github-hosted-controls "${TMP_DIR}" "${REPO}" "${POLICY_PATH}"`,
	} {
		if !strings.Contains(hostedControlsScript, needle) {
			return fmt.Errorf("scripts/verify-github-hosted-controls.sh must contain %q", needle)
		}
	}
	if err := requireNoRegistryBootstrapMCP(codexRequirementsText, cfg.CodexRequirementsPath); err != nil {
		return err
	}
	if err := requireNoRegistryBootstrapMCP(codexMCPConfigText, cfg.CodexMCPConfigPath); err != nil {
		return err
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

func mustGlob(pattern string) []string {
	matches, err := filepath.Glob(pattern)
	if err != nil {
		panic(err)
	}
	return matches
}
