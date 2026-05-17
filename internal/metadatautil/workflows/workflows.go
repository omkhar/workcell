// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package workflows

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/omkhar/workcell/internal/tomlsubset"
	"gopkg.in/yaml.v3"
)

type workflowDocument struct {
	Jobs map[string]workflowJob `yaml:"jobs"`
}

type workflowJob struct {
	Name string `yaml:"name"`
}

func CollectWorkflowJobNames(content []byte) ([]string, error) {
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
	if err := EnsureWorkflowTools(rootDir); err != nil {
		return err
	}

	policyText, err := readText(policyPath)
	if err != nil {
		return err
	}
	policy, err := tomlsubset.Parse(policyText, policyPath)
	if err != nil {
		return err
	}

	contexts, err := requireStringSliceTable(policy, "required_status_checks", "contexts", policyPath)
	if err != nil {
		return err
	}
	requiredJobNames := append([]string{}, contexts...)
	if len(requiredJobNames) == 0 {
		return fmt.Errorf("%s must define at least one required status-check context", policyPath)
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
		names, err := CollectWorkflowJobNames(content)
		if err != nil {
			return fmt.Errorf("%s: parse workflow job names: %w", path, err)
		}
		for _, name := range names {
			jobNames[name] = struct{}{}
		}
	}

	missing := make([]string, 0)
	for _, expected := range requiredJobNames {
		if _, ok := jobNames[expected]; !ok {
			missing = append(missing, expected)
		}
	}
	slices.Sort(missing)
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
	if err := ValidateCodeQLWorkflow(codeqlWorkflow, codeqlWorkflowPath); err != nil {
		return err
	}

	releaseWorkflowPath := filepath.Join(workflowDir, "release.yml")
	releaseWorkflow, err := readText(releaseWorkflowPath)
	if err != nil {
		return err
	}
	if err := ValidateReleaseWorkflowCodeQLFlow(releaseWorkflow); err != nil {
		return err
	}
	return nil
}

func EnsureWorkflowTools(rootDir string) error {
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

func ValidateCodeQLWorkflow(codeqlWorkflow, workflowPath string) error {
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

func ValidateReleaseWorkflowCodeQLFlow(releaseWorkflow string) error {
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

func ValidateCIWorkflowPRShapeFlow(ciWorkflow string) error {
	for _, needle := range []string{
		"name: Pull request shape",
		"fetch-depth: 0",
		"WORKCELL_PR_BASE_REF: ${{ github.event.pull_request.base.ref }}",
		"Check pull request shape",
		`./scripts/ci/job-pr-shape.sh --base "${WORKCELL_PR_BASE_REF}"`,
		"Skip outside pull requests",
		`PR shape gate applies only to pull requests.`,
	} {
		if !strings.Contains(ciWorkflow, needle) {
			return fmt.Errorf(".github/workflows/ci.yml must contain %q", needle)
		}
	}
	return nil
}

func ValidateReleaseWorkflowControlPlaneFlow(releaseWorkflow string) error {
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

func ValidateReleaseWorkflowGitHubAttestationFlow(releaseWorkflow string) error {
	if !strings.Contains(releaseWorkflow, "ENABLE_GITHUB_ATTESTATIONS_SUPPORTED: ${{ github.event.repository.visibility == 'public' || vars.WORKCELL_ENABLE_PRIVATE_GITHUB_ATTESTATIONS == 'true' }}") {
		return errors.New(".github/workflows/release.yml must gate GitHub attestations on public visibility or an explicit private-repo capability flag")
	}
	if !strings.Contains(releaseWorkflow, "RELEASE_NO_ATTEST: ${{ vars.WORKCELL_RELEASE_NO_ATTEST || 'false' }}") {
		return errors.New(".github/workflows/release.yml must expose RELEASE_NO_ATTEST as an explicit opt-out env var sourced from vars.WORKCELL_RELEASE_NO_ATTEST")
	}
	if !strings.Contains(releaseWorkflow, "name: Confirm attestation environment policy") {
		return errors.New(".github/workflows/release.yml must include the fail-closed attestation preflight step")
	}
	attestationPolicyStep := namedWorkflowStep(releaseWorkflow, "Confirm attestation environment policy")
	if !strings.Contains(attestationPolicyStep, `ENABLE_GITHUB_ATTESTATIONS_SUPPORTED`) ||
		!strings.Contains(attestationPolicyStep, `!= "true"`) ||
		!strings.Contains(attestationPolicyStep, `exit 1`) {
		return errors.New(".github/workflows/release.yml must keep the fail-closed attestation preflight script body (must `exit 1` when ENABLE_GITHUB_ATTESTATIONS_SUPPORTED is not 'true' and RELEASE_NO_ATTEST is not 'true')")
	}
	const attestGuard = "if: env.RELEASE_NO_ATTEST != 'true' && env.ENABLE_GITHUB_ATTESTATIONS_SUPPORTED == 'true'"
	attestStepRE := regexp.MustCompile(`(?m)^\s*-\s+uses:\s+actions/attest@`)
	guardedAttestStepRE := regexp.MustCompile(`(?ms)^\s*-\s+uses:\s+actions/attest@[^\n]+\n\s+if:\s+env\.RELEASE_NO_ATTEST != 'true' && env\.ENABLE_GITHUB_ATTESTATIONS_SUPPORTED == 'true'\n`)
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

func namedWorkflowStep(workflow, name string) string {
	lines := strings.Split(workflow, "\n")
	stepPrefix := regexp.MustCompile(`^(\s*)-\s+name:\s+` + regexp.QuoteMeta(name) + `\s*$`)
	for i, line := range lines {
		match := stepPrefix.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		stepIndent := match[1]
		end := len(lines)
		for j := i + 1; j < len(lines); j++ {
			if strings.HasPrefix(lines[j], stepIndent+"- ") {
				end = j
				break
			}
		}
		return strings.Join(lines[i:end], "\n")
	}
	return ""
}

func ValidateMacOSInstallVerificationFlow(workflowText, workflowPath, artifactName, jobName string) error {
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

func ValidateUpstreamRefreshWorkflow(workflowText string) error {
	for _, needle := range []string{
		"name: Upstream refresh",
		"workflow_dispatch:",
		"./scripts/update-upstream-pins.sh --apply",
		"./scripts/update-upstream-pins.sh --check",
		"./scripts/check-pinned-inputs.sh",
		"environment:\n      name: upstream-refresh",
		"actions/upload-artifact@",
		"name: upstream-refresh-candidate",
		"metadata.json",
		"gh issue",
		"persist-credentials: false",
		"fetch-depth: 0",
		"contents: read",
		"issues: write",
		"pull-requests: read",
		"WORKCELL_COSIGN_VERSION:",
		"sigstore/cosign-installer@",
		"cosign-release: ${{ env.WORKCELL_COSIGN_VERSION }}",
		`sudo install -m 0755 "$(command -v cosign)" /usr/local/bin/cosign`,
	} {
		if !strings.Contains(workflowText, needle) {
			return fmt.Errorf(".github/workflows/upstream-refresh.yml must contain %q", needle)
		}
	}
	for _, forbidden := range []string{
		"WORKCELL_UPSTREAM_REFRESH_GIT_NAME",
		"WORKCELL_UPSTREAM_REFRESH_GIT_EMAIL",
		"WORKCELL_UPSTREAM_REFRESH_GPG_FINGERPRINT",
		"WORKCELL_UPSTREAM_REFRESH_GPG_PRIVATE_KEY",
		"WORKCELL_UPSTREAM_REFRESH_GPG_KEY_ID",
		"gpg --batch --with-colons --list-secret-keys",
		"git commit -S",
		"gh pr create",
		`git push "https://x-access-token:`,
		"contents: write",
		"pull-requests: write",
	} {
		if strings.Contains(workflowText, forbidden) {
			return fmt.Errorf(".github/workflows/upstream-refresh.yml must not contain %q", forbidden)
		}
	}
	return nil
}

func readText(path string) (string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(content), nil
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

func requireStringSliceTable(root map[string]any, tableName, key, sourcePath string) ([]string, error) {
	table, ok := root[tableName].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s must define %s.%s as a non-empty array", sourcePath, tableName, key)
	}
	values, ok, err := mustStringSlice(table[key])
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
