// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
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

type workflowNodeDocument struct {
	Env      map[string]string    `yaml:"env"`
	Jobs     map[string]yaml.Node `yaml:"jobs"`
	Defaults struct {
		Run struct {
			Shell string `yaml:"shell"`
		} `yaml:"run"`
	} `yaml:"defaults"`
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
	With map[string]string `yaml:"with"`
}

// This digest covers the complete parsed sign-release job plus the workflow-level
// ORAS pins, the registry it publishes to, and the shell its run steps inherit. It
// rejects unknown fields, reordered steps, changed commands, changed action inputs,
// a swapped publisher, a redirected registry, and a weakened shell default.
const releaseSignerContractSHA256 = "cf3446f349da655bf92d702b53708397797b73040c810d6830bfe7cb4f311692"

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

// verifyReleaseOutputsScript is the release output verifier the publication
// gate requires the independent verification job to execute.
const verifyReleaseOutputsScript = "./scripts/verify-release-outputs.sh"

// auditHostedControlsScript and publishGitHubReleaseScript are the two commands
// the final publication step must run, in that order, around the credential
// unset that separates them.
const (
	auditHostedControlsScript  = "./scripts/run-hosted-controls-audit.sh"
	publishGitHubReleaseScript = "./scripts/publish-github-release.sh"
)

// ValidateReleaseWorkflowPublicationGate keeps the privileged hosted-controls
// credential in a minimal final job and requires its fresh check to complete
// immediately before the default-token publisher runs.
func ValidateReleaseWorkflowPublicationGate(workflowText string) error {
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
		verifyJob.Needs.Content[0].Value != "tag-policy" || verifyJob.Needs.Content[1].Value != "sign-release" {
		return errors.New("release output verification job must depend directly on tag-policy and the signing job")
	}
	if len(verifyJob.Permissions) != 4 || verifyJob.Permissions["actions"] != "read" ||
		verifyJob.Permissions["attestations"] != "read" || verifyJob.Permissions["contents"] != "read" ||
		verifyJob.Permissions["packages"] != "read" {
		return errors.New("release output verification job must grant only read permissions for artifacts, attestations, contents, and packages")
	}
	// Read the invocation the shell really runs. A statement-start scan still
	// accepts the script inside a heredoc body, an unrun branch or a function
	// body, because each of those keeps the line that starts with it.
	verificationFound := slices.ContainsFunc(verifyJob.Steps, func(step workflowStep) bool {
		return len(ShellInvocations(step.Run, verifyReleaseOutputsScript)) > 0
	})
	if !verificationFound {
		return errors.New("release output verification job must run verify-release-outputs.sh")
	}
	publishJob, ok := document.Jobs["publish-github-release"]
	if !ok {
		return errors.New("release workflow must define the final publish-github-release job")
	}
	if publishJob.Needs.Kind != yaml.SequenceNode || len(publishJob.Needs.Content) != 3 ||
		publishJob.Needs.Content[0].Value != "tag-policy" || publishJob.Needs.Content[1].Value != "sign-release" ||
		publishJob.Needs.Content[2].Value != "verify-release-outputs" {
		return errors.New("final GitHub release publication job must depend directly on tag-policy, the signing job, and output verification")
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
		// Each of the three must be an invocation the shell runs, not text that
		// names it: a heredoc body, an unrun branch or a longer option leaves
		// the text in place while the credential stays live. The argument
		// checks compare whole words, so a longer name is a different command.
		audit := ShellInvocations(step.Run, auditHostedControlsScript)
		unset := ShellInvocations(step.Run, "unset WORKCELL_HOSTED_CONTROLS_TOKEN")
		publish := ShellInvocations(step.Run, publishGitHubReleaseScript)
		if len(audit) == 0 || len(unset) == 0 || len(publish) == 0 ||
			// The audit takes the repository and nothing else. It rejects a
			// second argument before it audits anything, and a || true after
			// it would let the step publish on that refusal, so a membership
			// test over its arguments is not enough.
			len(audit[0].Args) != 1 || audit[0].Args[0] != "${GITHUB_REPOSITORY}" ||
			// The publisher reads the preverification flag only as the word
			// straight after the tag. Later it is an asset name instead, and
			// the publisher is told the release was not preverified.
			len(publish[0].Args) < 2 || publish[0].Args[0] != "${GITHUB_REF_NAME}" ||
			publish[0].Args[1] != "--immutable-releases-preverified-by-hosted-controls" {
			return errors.New("final GitHub release publication step must recheck hosted controls, unset its credential, then invoke the explicit preverified publisher")
		}
		// All three run, so compare the positions the parser proves rather
		// than where the three names first appear in the text: a comment can
		// name them in this order while the step mutates the release first.
		if unset[0].Position <= audit[0].Position || publish[0].Position <= unset[0].Position {
			return errors.New("final GitHub release publication step must recheck hosted controls, unset its credential, then invoke the explicit preverified publisher")
		}
		return nil
	}
	return errors.New("release workflow must combine the fresh hosted-controls check and GitHub release publication in one reviewed step")
}

func ValidateReleaseWorkflowAuthoritySplit(workflowText string) error {
	var document workflowDocument
	if err := yaml.Unmarshal([]byte(workflowText), &document); err != nil {
		return fmt.Errorf("parse release authority split: %w", err)
	}
	if err := validateUnprivilegedReleaseJobs(document); err != nil {
		return err
	}
	if err := validateReleaseSigner(document); err != nil {
		return err
	}
	if err := validateReleaseAssembly(document); err != nil {
		return err
	}
	return validateReleaseSignerContract(workflowText)
}

// validateReleaseAssembly reads the parsed release job so that a comment or an
// unrelated job naming the commands cannot satisfy the assembly requirements.
func validateReleaseAssembly(document workflowDocument) error {
	steps := document.Jobs["release"].Steps
	if !slices.ContainsFunc(steps, func(step workflowStep) bool {
		var targets []string
		for _, invocation := range ShellInvocations(step.Run, "oras cp --recursive --from-oci-layout") {
			if at := slices.Index(invocation.Args, "--to-oci-layout"); at >= 0 && at+1 < len(invocation.Args) {
				targets = append(targets, invocation.Args[at+1])
			}
		}
		return slices.Contains(targets, "dist/release-image:amd64") &&
			slices.Contains(targets, "dist/release-image:arm64")
	}) {
		return errors.New("release job must copy both platform images into the release OCI layout it indexes")
	}
	if !slices.ContainsFunc(steps, func(step workflowStep) bool {
		return len(ShellInvocations(step.Run, "oras manifest index create --oci-layout")) > 0
	}) {
		return errors.New("release job must assemble the multi-arch index in an OCI layout")
	}
	return nil
}

func validateUnprivilegedReleaseJobs(document workflowDocument) error {
	for _, name := range []string{"build-amd64-image", "build-arm64-image", "bind-release-subjects", "release"} {
		job, ok := document.Jobs[name]
		if !ok || len(job.Permissions) != 1 || job.Permissions["contents"] != "read" {
			return fmt.Errorf("release job %s must grant only contents: read", name)
		}
	}
	return nil
}

func validateReleaseSigner(document workflowDocument) error {
	signer, ok := document.Jobs["sign-release"]
	if !ok {
		return errors.New("release workflow must define sign-release")
	}
	expected := map[string]string{"artifact-metadata": "write", "attestations": "write", "contents": "read", "id-token": "write", "packages": "write"}
	if signer.Environment.Name != "release" || !maps.Equal(signer.Permissions, expected) {
		return errors.New("sign-release must use the release environment and exact publication permissions")
	}
	if !needsExactly(signer.Needs, []string{"tag-policy", "preflight", "bind-release-subjects", "preflight-amd64-repro", "preflight-arm64-repro", "release"}) {
		return errors.New("sign-release must depend directly on policy, both platform preflights, and assembly")
	}
	return validateReleaseSignerInputs(signer)
}

// validateReleaseSignerInputs reads the parsed signer steps so that a comment or
// an unrelated job cannot satisfy the bound-input requirement.
func validateReleaseSignerInputs(signer workflowJob) error {
	var downloaded []string
	validates := false
	for _, step := range signer.Steps {
		if id, ok := step.With["artifact-ids"]; ok {
			downloaded = append(downloaded, id)
		}
		validates = validates || step.Name == "Validate privileged handoff"
	}
	if !slices.Equal(downloaded, []string{"${{ needs.release.outputs.artifact_id }}", "${{ needs.bind-release-subjects.outputs.artifact_id }}"}) {
		return errors.New("sign-release must download exactly the bound release and subject artifacts by immutable id")
	}
	if !validates {
		return errors.New("sign-release must validate the privileged handoff before it publishes")
	}
	return nil
}

func validateReleaseSignerContract(workflowText string) error {
	var document workflowNodeDocument
	if err := yaml.Unmarshal([]byte(workflowText), &document); err != nil {
		return err
	}
	signer, ok := document.Jobs["sign-release"]
	if !ok {
		return errors.New("release workflow must define sign-release")
	}
	// The signer installs its publisher from the workflow-level ORAS pins and
	// publishes to the workflow-level IMAGE_NAME, so the contract covers those
	// too. Swapping the publisher or the registry destination for one the
	// maintainer did not review must break this digest. Privileged run steps also
	// inherit the workflow shell, so dropping -e there must break it too.
	content, err := yaml.Marshal(struct {
		Signer       yaml.Node `yaml:"sign-release"`
		OrasPins     []string  `yaml:"oras-pins"`
		RegistryName string    `yaml:"image-name"`
		ShellDefault string    `yaml:"shell-default"`
	}{
		Signer:       signer,
		OrasPins:     []string{document.Env["WORKCELL_ORAS_VERSION"], document.Env["WORKCELL_ORAS_LINUX_AMD64_SHA256"]},
		RegistryName: document.Env["IMAGE_NAME"],
		ShellDefault: document.Defaults.Run.Shell,
	})
	if err != nil {
		return err
	}
	sum := sha256.Sum256(content)
	digest := hex.EncodeToString(sum[:])
	if digest != releaseSignerContractSHA256 {
		return fmt.Errorf("sign-release must match the exact privileged step contract: got %s", digest)
	}
	return nil
}

func needsExactly(node yaml.Node, expected []string) bool {
	return node.Kind == yaml.SequenceNode &&
		slices.EqualFunc(node.Content, expected, func(actual *yaml.Node, name string) bool { return actual.Value == name })
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

// ValidateReleaseWorkflowGitHubAttestationFlow requires every reviewed release
// attestation to run unconditionally. A repository variable is mutable without
// review, so a workflow that reads one can be made to publish a release with no
// attestations at all; the only supported way to release without them is to
// review the change that removes them.
func ValidateReleaseWorkflowGitHubAttestationFlow(releaseWorkflow string) error {
	if strings.Contains(releaseWorkflow, "RELEASE_NO_ATTEST") || strings.Contains(releaseWorkflow, "WORKCELL_ENABLE_PRIVATE_GITHUB_ATTESTATIONS") ||
		strings.Contains(releaseWorkflow, "ENABLE_GITHUB_ATTESTATIONS_SUPPORTED") {
		return errors.New(".github/workflows/release.yml must not use mutable repository variables to skip GitHub attestations")
	}
	var document workflowDocument
	if err := yaml.Unmarshal([]byte(releaseWorkflow), &document); err != nil {
		return fmt.Errorf("parse release attestation flow: %w", err)
	}
	if err := validateReleaseAttestationGuard(document); err != nil {
		return err
	}
	if err := validateReleaseAttestationSteps(document); err != nil {
		return err
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

// releaseAttestationEnvironmentPolicyScript is the whole body the attestation
// environment policy step must run. Comparing the script rather than a set of
// substrings rejects an extra branch that exits 0 on an unsupported repository.
const releaseAttestationEnvironmentPolicyScript = `if [[ "${REPOSITORY_VISIBILITY}" != "public" ]]; then
  echo "::error::Release requires GitHub attestations but they are not supported for this repository." >&2
  echo "::error::Publish the repository, or review a fork-specific workflow and hosted-control policy change." >&2
  exit 1
fi`

// releaseAttestationVerificationScript is the whole body both release-output
// verification steps must run, including the --attestations argument that makes
// them check the attestations this flow requires the signer to publish.
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

// validateReleaseAttestationGuard requires the unprivileged release artifact job
// to refuse an environment that cannot attest, before it produces anything the
// signer would attest.
func validateReleaseAttestationGuard(document workflowDocument) error {
	guards := 0
	for _, step := range document.Jobs["release"].Steps {
		if step.Name != "Confirm attestation environment policy" {
			continue
		}
		guards++
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
	if guards != 1 {
		return errors.New("release artifact job must contain exactly one attestation environment policy step")
	}
	return nil
}

// validateReleaseAttestationSteps keeps every attestation in the one privileged
// signing job, and keeps that job downstream of the guard above: sign-release
// runs only after the release artifact job it needs has confirmed the
// environment can attest.
func validateReleaseAttestationSteps(document workflowDocument) error {
	signer, ok := document.Jobs["sign-release"]
	if !ok {
		return errors.New("release workflow must define sign-release")
	}
	attestSteps := 0
	for _, step := range signer.Steps {
		if !strings.HasPrefix(step.Uses, "actions/attest@") {
			continue
		}
		attestSteps++
		if step.If.Kind != 0 && strings.TrimSpace(step.If.Value) != "" {
			return errors.New(".github/workflows/release.yml must run every reviewed actions/attest step without a mutable condition")
		}
	}
	if attestSteps != 10 {
		return errors.New(".github/workflows/release.yml must keep exactly ten reviewed GitHub attestation steps")
	}
	for jobName, job := range document.Jobs {
		if jobName == "sign-release" {
			continue
		}
		if slices.ContainsFunc(job.Steps, func(step workflowStep) bool { return strings.HasPrefix(step.Uses, "actions/attest@") }) {
			return fmt.Errorf("release workflow must keep every actions/attest step in the signing job, found one in %q", jobName)
		}
	}
	if !slices.ContainsFunc(signer.Needs.Content, func(need *yaml.Node) bool { return need.Value == "release" }) {
		return errors.New("release workflow must check attestation support before its first attestation step: sign-release must need the release artifact job")
	}
	return nil
}

// validateReleaseAttestationVerifier requires the exact verification body, so a
// release cannot be published after a check that dropped --attestations or that
// only ran on some condition.
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
	return validateManualPrivilegedWorkflowRef(workflowText, ".github/workflows/upstream-refresh.yml", "refresh")
}

func ValidateHostedControlsWorkflow(workflowText string) error {
	for _, needle := range []string{
		`name: hosted-controls-audit`,
		`run: ./scripts/run-hosted-controls-audit.sh "${GITHUB_REPOSITORY}"`,
		`WORKCELL_HOSTED_CONTROLS_TOKEN: ${{ secrets.WORKCELL_HOSTED_CONTROLS_TOKEN }}`,
		`WORKCELL_HOSTED_CONTROLS_REQUIRED: "1"`,
	} {
		if !strings.Contains(workflowText, needle) {
			return fmt.Errorf(".github/workflows/hosted-controls.yml must contain %q", needle)
		}
	}
	return validateManualPrivilegedWorkflowRef(workflowText, ".github/workflows/hosted-controls.yml", "verify-hosted-controls")
}

func validateManualPrivilegedWorkflowRef(workflowText, workflowPath, jobName string) error {
	root, err := parseWorkflowRoot(workflowText, workflowPath)
	if err != nil {
		return err
	}
	jobs, err := requireWorkflowMapping(root, "jobs", workflowPath+" must define exactly one jobs mapping")
	if err != nil {
		return err
	}
	job, err := requireWorkflowMapping(jobs, jobName, workflowPath+" must define exactly one "+jobName+" job mapping")
	if err != nil {
		return err
	}
	guards := yamlMappingValues(job, "if")
	if len(guards) != 1 || guards[0].Tag != "!!str" || yamlScalarValue(guards[0]) != "github.ref == 'refs/heads/main'" {
		return fmt.Errorf("%s %s job must require github.ref == 'refs/heads/main'", workflowPath, jobName)
	}
	return nil
}

func requireWorkflowMapping(parent *yaml.Node, key, message string) (*yaml.Node, error) {
	values := yamlMappingValues(parent, key)
	if len(values) != 1 || values[0].Kind != yaml.MappingNode {
		return nil, errors.New(message)
	}
	return values[0], nil
}

// readText lives in core.go.
// requireStringSliceTable lives in hostedcontrols.go.
