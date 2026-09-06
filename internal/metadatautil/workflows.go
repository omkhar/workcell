// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

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
	Name        string    `yaml:"name"`
	Needs       yaml.Node `yaml:"needs"`
	Environment struct {
		Name string `yaml:"name"`
	} `yaml:"environment"`
	Permissions map[string]string `yaml:"permissions"`
	Steps       []workflowStep    `yaml:"steps"`
}

type workflowStep struct {
	Name string            `yaml:"name"`
	If   yaml.Node         `yaml:"if"`
	Uses string            `yaml:"uses"`
	Env  map[string]string `yaml:"env"`
	Run  string            `yaml:"run"`
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

// ValidateReleaseWorkflowPublicationGate keeps the privileged hosted-controls
// credential in a minimal final job and requires its fresh check to complete
// immediately before the default-token publisher runs.
func ValidateReleaseWorkflowPublicationGate(workflowText string) error {
	if strings.Contains(workflowText, "WORKCELL_GITHUB_HOSTED_CONTROLS_POLICY_PATH") {
		return errors.New("release workflow must not override the reviewed GitHub hosted-controls policy path")
	}
	var document workflowDocument
	if err := yaml.Unmarshal([]byte(workflowText), &document); err != nil {
		return fmt.Errorf("parse release publication gate: %w", err)
	}
	releaseJob, ok := document.Jobs["release"]
	if !ok {
		return errors.New("release workflow must define the release artifact job")
	}
	if releaseJob.Permissions["contents"] != "read" {
		return errors.New("release artifact job must keep contents permission read-only")
	}
	verifyJob, ok := document.Jobs["verify-release-outputs"]
	if !ok {
		return errors.New("release workflow must define the independent verify-release-outputs job")
	}
	if verifyJob.Needs.Kind != yaml.SequenceNode || len(verifyJob.Needs.Content) != 2 ||
		verifyJob.Needs.Content[0].Value != "tag-policy" || verifyJob.Needs.Content[1].Value != "release" {
		return errors.New("release output verification job must depend directly on tag-policy and the release artifact job")
	}
	if len(verifyJob.Permissions) != 4 || verifyJob.Permissions["actions"] != "read" ||
		verifyJob.Permissions["attestations"] != "read" || verifyJob.Permissions["contents"] != "read" ||
		verifyJob.Permissions["packages"] != "read" {
		return errors.New("release output verification job must grant only read permissions for artifacts, attestations, contents, and packages")
	}
	verificationFound := false
	for _, step := range verifyJob.Steps {
		if strings.Contains(step.Run, "./scripts/verify-release-outputs.sh") {
			verificationFound = true
			break
		}
	}
	if !verificationFound {
		return errors.New("release output verification job must run verify-release-outputs.sh")
	}
	publishJob, ok := document.Jobs["publish-github-release"]
	if !ok {
		return errors.New("release workflow must define the final publish-github-release job")
	}
	if publishJob.Needs.Kind != yaml.SequenceNode || len(publishJob.Needs.Content) != 3 ||
		publishJob.Needs.Content[0].Value != "tag-policy" || publishJob.Needs.Content[1].Value != "release" ||
		publishJob.Needs.Content[2].Value != "verify-release-outputs" {
		return errors.New("final GitHub release publication job must depend directly on tag-policy, the release artifact job, and output verification")
	}
	if publishJob.Environment.Name != "hosted-controls-audit" {
		return errors.New("final GitHub release publication job must run in hosted-controls-audit")
	}
	if len(publishJob.Permissions) != 4 || publishJob.Permissions["actions"] != "read" ||
		publishJob.Permissions["attestations"] != "read" || publishJob.Permissions["contents"] != "write" ||
		publishJob.Permissions["packages"] != "read" {
		return errors.New("final GitHub release publication job must grant only read verification permissions and contents: write")
	}
	for _, step := range publishJob.Steps {
		if step.Name != "Recheck hosted controls and publish GitHub release assets" {
			continue
		}
		if step.Env["WORKCELL_HOSTED_CONTROLS_REQUIRED"] != "1" ||
			step.Env["WORKCELL_HOSTED_CONTROLS_TOKEN"] != "${{ secrets.WORKCELL_HOSTED_CONTROLS_TOKEN }}" ||
			step.Env["GITHUB_TOKEN"] != "${{ github.token }}" {
			return errors.New("final GitHub release publication step must receive the required hosted-controls token and separate default mutation token")
		}
		auditIndex := strings.Index(step.Run, `./scripts/run-hosted-controls-audit.sh "${GITHUB_REPOSITORY}"`)
		unsetIndex := strings.Index(step.Run, "unset WORKCELL_HOSTED_CONTROLS_TOKEN")
		publishIndex := strings.Index(step.Run, `./scripts/publish-github-release.sh "${RELEASE_TAG}"`)
		if auditIndex < 0 || unsetIndex <= auditIndex || publishIndex <= unsetIndex ||
			!strings.Contains(step.Run[publishIndex:], `--expected-tag-object "${RELEASE_TAG_OBJECT}"`) ||
			!strings.Contains(step.Run[publishIndex:], "--immutable-releases-preverified-by-hosted-controls") {
			return errors.New("final GitHub release publication step must recheck hosted controls, unset its credential, then invoke the explicit preverified publisher")
		}
		return nil
	}
	return errors.New("release workflow must combine the fresh hosted-controls check and GitHub release publication in one reviewed step")
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
	if strings.Contains(releaseWorkflow, "RELEASE_NO_ATTEST") || strings.Contains(releaseWorkflow, "WORKCELL_ENABLE_PRIVATE_GITHUB_ATTESTATIONS") ||
		strings.Contains(releaseWorkflow, "ENABLE_GITHUB_ATTESTATIONS_SUPPORTED") {
		return errors.New(".github/workflows/release.yml must not use mutable repository variables to skip GitHub attestations")
	}
	var document workflowDocument
	if err := yaml.Unmarshal([]byte(releaseWorkflow), &document); err != nil {
		return fmt.Errorf("parse release attestation flow: %w", err)
	}
	releaseJob, ok := document.Jobs["release"]
	if !ok {
		return errors.New("release workflow must define the release artifact job")
	}
	guardIndex := -1
	firstAttestIndex := -1
	releaseAttestSteps := 0
	for index, step := range releaseJob.Steps {
		if step.Name == "Confirm attestation environment policy" {
			if guardIndex >= 0 {
				return errors.New("release artifact job must contain exactly one attestation environment policy step")
			}
			guardIndex = index
			if step.If.Kind != 0 && strings.TrimSpace(step.If.Value) != "" {
				return errors.New("release artifact job must run the attestation environment policy step unconditionally")
			}
			if len(step.Env) != 1 || step.Env["REPOSITORY_VISIBILITY"] != "${{ github.event.repository.visibility }}" {
				return errors.New("release artifact job must bind repository visibility from the GitHub event in the attestation environment policy step")
			}
			if strings.TrimSpace(step.Run) != releaseAttestationEnvironmentPolicyScript {
				return errors.New("release artifact job must use the exact fail-closed public repository visibility check")
			}
		}
		if !strings.HasPrefix(step.Uses, "actions/attest@") {
			continue
		}
		if firstAttestIndex < 0 {
			firstAttestIndex = index
		}
		releaseAttestSteps++
		if step.If.Kind != 0 && strings.TrimSpace(step.If.Value) != "" {
			return errors.New(".github/workflows/release.yml must run every reviewed actions/attest step without a mutable condition")
		}
	}
	if guardIndex < 0 {
		return errors.New("release artifact job must contain exactly one attestation environment policy step")
	}
	if firstAttestIndex < 0 || guardIndex >= firstAttestIndex {
		return errors.New("release artifact job must check attestation support before its first attestation step")
	}
	for jobName, job := range document.Jobs {
		if jobName == "release" {
			continue
		}
		for _, step := range job.Steps {
			if !strings.HasPrefix(step.Uses, "actions/attest@") {
				continue
			}
			return fmt.Errorf("release workflow must keep every actions/attest step in the release artifact job, found one in %q", jobName)
		}
	}
	if releaseAttestSteps != 10 {
		return errors.New(".github/workflows/release.yml must keep exactly ten reviewed GitHub attestation steps")
	}
	for _, jobName := range []string{"verify-release-outputs", "publish-github-release"} {
		job, ok := document.Jobs[jobName]
		if !ok {
			return fmt.Errorf("release workflow must define the %s job", jobName)
		}
		if err := validateReleaseAttestationVerifier(job, jobName); err != nil {
			return err
		}
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

const releaseAttestationEnvironmentPolicyScript = `if [[ "${REPOSITORY_VISIBILITY}" != "public" ]]; then
  echo "::error::Release requires GitHub attestations but they are not supported for this repository." >&2
  echo "::error::Publish the repository, or review a fork-specific workflow and hosted-control policy change." >&2
  exit 1
fi`

const releaseAttestationVerificationScript = `set -euo pipefail
docker_config="$(mktemp -d "${RUNNER_TEMP}/workcell-docker-config.XXXXXX")"
chmod 0700 "${docker_config}"
trap 'rm -rf -- "${docker_config}"' EXIT
export DOCKER_CONFIG="${docker_config}"
printf '%s' "${GITHUB_TOKEN}" | docker login ghcr.io \
  --username "${GITHUB_REPOSITORY_OWNER}" \
  --password-stdin
verify_args=(
  --assets-dir dist
  --repo "${GITHUB_REPOSITORY}"
  --tag "${RELEASE_TAG}"
  --image-repository "${IMAGE_NAME}"
  --source-digest "${RELEASE_COMMIT}"
  --workflow-digest "${GITHUB_WORKFLOW_SHA}"
  --attestations
)
./scripts/verify-release-outputs.sh "${verify_args[@]}"`

func validateReleaseAttestationVerifier(job workflowJob, jobName string) error {
	verificationSteps := 0
	for _, step := range job.Steps {
		if !strings.Contains(step.Run, "./scripts/verify-release-outputs.sh") {
			continue
		}
		verificationSteps++
		if step.If.Kind != 0 && strings.TrimSpace(step.If.Value) != "" {
			return fmt.Errorf("%s job must run release-output attestation verification unconditionally", jobName)
		}
		if strings.TrimSpace(step.Run) != releaseAttestationVerificationScript {
			return fmt.Errorf("%s job must run the exact unconditional release-output attestation verification", jobName)
		}
	}
	if verificationSteps != 1 {
		return fmt.Errorf("%s job must contain exactly one release-output attestation verification step", jobName)
	}
	return nil
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
		"GITHUB_TOKEN: ${{ github.token }}",
		`token_file="$(mktemp "${RUNNER_TEMP}/workcell-github-api-token.XXXXXX")"`,
		`(umask 077 && printf '%s' "${GITHUB_TOKEN}" >"${token_file}")`,
		"unset GITHUB_TOKEN GH_TOKEN",
		`trap 'rm -f "${token_file}"' EXIT`,
		`WORKCELL_GITHUB_API_TOKEN_FILE="${token_file}" ./scripts/update-upstream-pins.sh --apply`,
		`WORKCELL_GITHUB_API_TOKEN_FILE="${token_file}" ./scripts/update-upstream-pins.sh --check`,
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

// readText lives in core.go.
// requireStringSliceTable lives in hostedcontrols.go.
