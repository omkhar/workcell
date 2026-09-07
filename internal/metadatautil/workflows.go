// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"cmp"
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
	Env  map[string]string `yaml:"env"`
	Run  string            `yaml:"run"`
	With map[string]string `yaml:"with"`
}

// This digest covers the complete parsed sign-release job plus the workflow-level
// ORAS pins, the registry it publishes to, and the shell its run steps inherit. It
// rejects unknown fields, reordered steps, changed commands, changed action inputs,
// a swapped publisher, a redirected registry, and a weakened shell default.
const releaseSignerContractSHA256 = "59d426ff05378de33e64dc11f715e25f21727eb07e9f33d7cead528856e84982"

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
	verificationFound := false
	for _, step := range verifyJob.Steps {
		// Match the script only as the start of a statement, so an inert
		// mention (a comment, or an argument to echo) cannot satisfy the gate.
		for _, line := range strings.Split(step.Run, "\n") {
			statement := strings.TrimLeft(line, " \t")
			if statement == verifyReleaseOutputsScript ||
				strings.HasPrefix(statement, verifyReleaseOutputsScript+" ") {
				verificationFound = true
				break
			}
		}
		if verificationFound {
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
		auditIndex := strings.Index(step.Run, `./scripts/run-hosted-controls-audit.sh "${GITHUB_REPOSITORY}"`)
		unsetIndex := strings.Index(step.Run, "unset WORKCELL_HOSTED_CONTROLS_TOKEN")
		publishIndex := strings.Index(step.Run, `./scripts/publish-github-release.sh "${GITHUB_REF_NAME}"`)
		if auditIndex < 0 || unsetIndex <= auditIndex || publishIndex <= unsetIndex ||
			!strings.Contains(step.Run[publishIndex:], "--immutable-releases-preverified-by-hosted-controls") {
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
		for _, args := range commandArgs(step.Run, "oras cp --recursive --from-oci-layout") {
			if at := slices.Index(args, "--to-oci-layout"); at >= 0 && at+1 < len(args) {
				targets = append(targets, args[at+1])
			}
		}
		return slices.Contains(targets, "dist/release-image:amd64") &&
			slices.Contains(targets, "dist/release-image:arm64")
	}) {
		return errors.New("release job must copy both platform images into the release OCI layout it indexes")
	}
	if !slices.ContainsFunc(steps, func(step workflowStep) bool {
		return len(commandArgs(step.Run, "oras manifest index create --oci-layout")) > 0
	}) {
		return errors.New("release job must assemble the multi-arch index in an OCI layout")
	}
	return nil
}

var heredocPattern = regexp.MustCompile(`<<-?\s*(?:'([^']*)'|"([^"]*)"|([A-Za-z_][A-Za-z0-9_]*))`)

var inlineComment = regexp.MustCompile(`(^|\s)#.*$`)

// commandArgs returns the arguments of each invocation of command in script. It
// joins continuations and drops comments, inline ones included, and heredoc bodies,
// so no decoy text counts as a command and one call cannot satisfy a two-call rule.
func commandArgs(script, command string) [][]string {
	var invocations [][]string
	var current strings.Builder
	var heredocs []string
	for line := range strings.Lines(script) {
		trimmed := strings.TrimSpace(line)
		if len(heredocs) > 0 {
			if trimmed == heredocs[0] {
				heredocs = heredocs[1:]
			}
			continue
		}
		if current.Len() > 0 {
			current.WriteString(" ")
		}
		current.WriteString(strings.TrimSpace(strings.TrimSuffix(trimmed, "\\")))
		if strings.HasSuffix(trimmed, "\\") {
			continue
		}
		logical := strings.TrimSpace(inlineComment.ReplaceAllString(current.String(), ""))
		current.Reset()
		// Here-strings are blanked first so that a redirection such as
		// <<<"${value}" is not read as a heredoc opening the delimiter ${value}.
		// Every delimiter on the line opens a body, and bash reads them in the
		// order they appear, so they are queued rather than overwritten.
		for _, match := range heredocPattern.FindAllStringSubmatch(strings.ReplaceAll(logical, "<<<", " "), -1) {
			heredocs = append(heredocs, cmp.Or(match[1], match[2], match[3]))
		}
		if rest, found := strings.CutPrefix(logical, command); found && (rest == "" || rest[0] == ' ' || rest[0] == '\t') {
			invocations = append(invocations, strings.Fields(rest))
		}
	}
	return invocations
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
