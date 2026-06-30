// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package testkit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifyInvariantsUsesDedicatedSanitizedEntrypoint(t *testing.T) {
	t.Parallel()

	scriptPath := filepath.Join(repoRoot(t), "scripts", "verify-invariants.sh")
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	script := string(content)

	for _, want := range []string{
		"#!/bin/bash -p",
		"WORKCELL_VERIFY_INVARIANTS_SANITIZED_ENTRYPOINT",
		`exec /usr/bin/env -i \`,
		`/bin/bash -p "$0" "$@"`,
		"unset BASH_ENV ENV",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("%s does not contain %q", scriptPath, want)
		}
	}
}

func TestDevQuickCheckStaysBoundedToFastLocalWork(t *testing.T) {
	t.Parallel()

	scriptPath := filepath.Join(repoRoot(t), "scripts", "dev-quick-check.sh")
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	script := string(content)

	for _, want := range []string{
		"scripts/check-dead-code.sh",
		"scripts/check-public-repo-hygiene.sh",
		"scripts/check-pr-shape.sh",
		"gofmt -l",
		"go vet ./...",
		"go test ./...",
		"cargo test --locked --offline",
		`scripts/lint-dockerfiles.sh`,
		`scripts/go-port-validate.sh`,
		`find "${ROOT_DIR}/tests/scenarios" -type f -name 'test-*.sh' -print | sort`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("%s does not contain %q", scriptPath, want)
		}
	}

	for _, unwanted := range []string{
		"container-smoke.sh",
		"verify-invariants.sh",
		"verify-go-python-parity.sh",
		"verify-reproducible-build.sh",
		"verify-release-bundle.sh",
		"pre-merge.sh",
		"run-mutation-tests.sh",
		"verify-coverage.sh",
		"tests/python",
	} {
		if strings.Contains(script, unwanted) {
			t.Fatalf("%s unexpectedly contains %q", scriptPath, unwanted)
		}
	}
}

func TestValidationGatesLintAllScenarioShellScripts(t *testing.T) {
	t.Parallel()

	expectedProbe := `find "${ROOT_DIR}/tests/scenarios" -type f -name 'test-*.sh' -print | sort`

	quickCheckPath := filepath.Join(repoRoot(t), "scripts", "dev-quick-check.sh")
	quickCheck, err := os.ReadFile(quickCheckPath)
	if err != nil {
		t.Fatal(err)
	}

	validateRepoPath := filepath.Join(repoRoot(t), "scripts", "validate-repo.sh")
	validateRepo, err := os.ReadFile(validateRepoPath)
	if err != nil {
		t.Fatal(err)
	}

	for _, content := range []string{string(quickCheck), string(validateRepo)} {
		if !strings.Contains(content, expectedProbe) {
			t.Fatalf("validation scripts must include %q", expectedProbe)
		}
		if !strings.Contains(content, "scripts/go-port-validate.sh") {
			t.Fatalf("validation scripts must include scripts/go-port-validate.sh")
		}
		if !strings.Contains(content, "scripts/check-dead-code.sh") {
			t.Fatalf("validation scripts must include scripts/check-dead-code.sh")
		}
		if !strings.Contains(content, "scripts/check-public-repo-hygiene.sh") {
			t.Fatalf("validation scripts must include scripts/check-public-repo-hygiene.sh")
		}
		if !strings.Contains(content, "scripts/check-pr-shape.sh") {
			t.Fatalf("validation scripts must include scripts/check-pr-shape.sh")
		}
		if !strings.Contains(content, "scripts/lint-dockerfiles.sh") {
			t.Fatalf("validation scripts must include scripts/lint-dockerfiles.sh")
		}
		if !strings.Contains(content, "scripts/verify-requirements-coverage.sh") {
			t.Fatalf("validation scripts must include scripts/verify-requirements-coverage.sh")
		}
		if !strings.Contains(content, "scripts/verify-operator-contract.sh") {
			t.Fatalf("validation scripts must include scripts/verify-operator-contract.sh")
		}
		for _, want := range []string{
			"scripts/bootstrap-dev.sh",
			"scripts/check-dead-code.sh",
			"scripts/check-public-repo-hygiene.sh",
			"scripts/check-pr-shape.sh",
			"scripts/generate-homebrew-formula.sh",
			"scripts/install-workcell.sh",
			"scripts/install.sh",
			"scripts/lib/go-run-env.sh",
			"scripts/provider-e2e.sh",
			"scripts/uninstall.sh",
			"scripts/update-upstream-pins.sh",
			"scripts/verify-github-macos-release-test-runners.sh",
			"scripts/verify-operator-contract.sh",
		} {
			if !strings.Contains(content, want) {
				t.Fatalf("validation scripts must include %s", want)
			}
		}
		if !strings.Contains(content, "gofmt -l") {
			t.Fatalf("validation scripts must include gofmt formatting checks")
		}
		if !strings.Contains(content, "go vet ./...") {
			t.Fatalf("validation scripts must include go vet")
		}
	}

	if strings.Contains(string(quickCheck), "scripts/verify-go-python-parity.sh") {
		t.Fatalf("%s must not include scripts/verify-go-python-parity.sh", quickCheckPath)
	}
	if strings.Contains(string(validateRepo), "scripts/verify-go-python-parity.sh") {
		t.Fatalf("%s must not include scripts/verify-go-python-parity.sh", validateRepoPath)
	}
	if !strings.Contains(string(validateRepo), `scripts/run-scenario-tests.sh" --repo-required`) {
		t.Fatalf("%s must run the repo-required scenario tier", validateRepoPath)
	}
	if strings.Contains(string(validateRepo), `scripts/run-scenario-tests.sh" --secretless-only`) {
		t.Fatalf("%s must not depend on the broader secretless scenario lane", validateRepoPath)
	}
	for _, want := range []string{
		`${ROOT_DIR}/.githooks/pre-commit`,
		`${ROOT_DIR}/scripts/check-dead-code.sh`,
		`${ROOT_DIR}/scripts/check-public-repo-hygiene.sh`,
		`${ROOT_DIR}/scripts/check-pr-shape.sh`,
		`${ROOT_DIR}/scripts/install.sh`,
		`${ROOT_DIR}/scripts/build-and-test.sh`,
		`${ROOT_DIR}/scripts/install-dev-tools.sh`,
		`${ROOT_DIR}/scripts/update-upstream-pins.sh`,
		`${ROOT_DIR}/scripts/update-provider-pins.sh`,
		`${ROOT_DIR}/scripts/publish-provider-bump-pr.sh`,
		`${ROOT_DIR}/scripts/publish-upstream-refresh-pr.sh`,
		`${ROOT_DIR}/scripts/verify-github-macos-release-test-runners.sh`,
		`${ROOT_DIR}/scripts/verify-upstream-gemini-release.sh`,
	} {
		if !strings.Contains(string(validateRepo), want) {
			t.Fatalf("%s must lint and format %s", validateRepoPath, want)
		}
	}
}

func TestVerifyOperatorContractIgnoresAmbientHelpOverride(t *testing.T) {
	t.Parallel()

	scriptPath := filepath.Join(repoRoot(t), "scripts", "verify-operator-contract.sh")
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	script := string(content)

	if !strings.Contains(script, "env -u WORKCELL_HELP_BIN") {
		t.Fatalf("%s must clear WORKCELL_HELP_BIN so normal validation probes the repo script", scriptPath)
	}
}

func TestBuildAndTestDockerModeUsesSnapshotBackedValidatorRun(t *testing.T) {
	t.Parallel()

	scriptPath := filepath.Join(repoRoot(t), "scripts", "build-and-test.sh")
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	script := string(content)

	for _, want := range []string{
		"--docker",
		`"${ROOT_DIR}/scripts/with-validation-snapshot.sh"`,
		"--mode worktree",
		"--include-untracked",
		`./scripts/validate-repo.sh`,
		`./scripts/verify-invariants.sh`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("%s does not contain %q", scriptPath, want)
		}
	}

	if strings.Contains(script, `-v "${ROOT_DIR}:/workspace"`) {
		t.Fatalf("%s should mount a disposable snapshot into the validator container, not the live worktree", scriptPath)
	}
	if strings.Contains(script, ".venv/bin/activate") {
		t.Fatalf("%s should not depend on a repo-local Python virtualenv", scriptPath)
	}
}

func TestInstallDevToolsBootstrapsCommonHostPrereqs(t *testing.T) {
	t.Parallel()

	scriptPath := filepath.Join(repoRoot(t), "scripts", "install-dev-tools.sh")
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	script := string(content)

	for _, want := range []string{
		`command -v npm`,
		`append_unique_brew node`,
		`if [[ "${host_os}" == "Linux" ]] && markdownlint_needs_install; then`,
		`require_markdownlint_node`,
		`require_markdownlint_npm`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("%s does not contain %q", scriptPath, want)
		}
	}
	for _, unwanted := range []string{
		"append_unique_apt nodejs npm",
		"python3 -m venv",
		"python3-venv",
		"pytest",
	} {
		if strings.Contains(script, unwanted) {
			t.Fatalf("%s unexpectedly contains %q", scriptPath, unwanted)
		}
	}
}

func TestGenerateHomebrewFormulaPinsExplicitVersion(t *testing.T) {
	t.Parallel()

	scriptPath := filepath.Join(repoRoot(t), "scripts", "generate-homebrew-formula.sh")
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	script := string(content)

	for _, want := range []string{
		`FORMULA_VERSION="${VERSION}"`,
		`FORMULA_VERSION="${FORMULA_VERSION#v}"`,
		`version "${FORMULA_VERSION}"`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("%s does not contain %q", scriptPath, want)
		}
	}
}

func TestInstallWorkcellBootstrapsRequiredHostDependencies(t *testing.T) {
	t.Parallel()

	scriptPath := filepath.Join(repoRoot(t), "scripts", "install-workcell.sh")
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	script := string(content)

	for _, want := range []string{
		"--no-install-deps",
		"Installing required host packages via Homebrew",
		"Missing required host packages:",
		"brew install",
		"colima",
		"docker",
		"gh",
		"git",
		"go",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("%s does not contain %q", scriptPath, want)
		}
	}

	if strings.Contains(script, "Missing required tool: go") {
		t.Fatalf("%s should not hard-fail on missing go during install anymore", scriptPath)
	}
}

func TestInstallWorkcellDebugWrapperSkipsSessionCommands(t *testing.T) {
	t.Parallel()

	scriptPath := filepath.Join(repoRoot(t), "scripts", "install-workcell.sh")
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	script := string(content)

	for _, want := range []string{
		"session)",
		"SKIP_AUTO_DEBUG=1",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("%s does not contain %q", scriptPath, want)
		}
	}
}

func TestUninstallRemovesWorkcellStateWithoutRequiringGo(t *testing.T) {
	t.Parallel()

	scriptPath := filepath.Join(repoRoot(t), "scripts", "uninstall.sh")
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	script := string(content)

	for _, want := range []string{
		"resolve_real_home",
		"Preserved shared host packages installed outside Workcell.",
		"shared host packages such as colima, docker, gh, git, and go",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("%s does not contain %q", scriptPath, want)
		}
	}

	for _, unwanted := range []string{
		"Missing required tool: go",
		"WORKCELL_GO_BIN",
	} {
		if strings.Contains(script, unwanted) {
			t.Fatalf("%s unexpectedly contains %q", scriptPath, unwanted)
		}
	}
}

func TestAppleSiliconOnlyHostGuardsArePinned(t *testing.T) {
	t.Parallel()

	launcherPath := filepath.Join(repoRoot(t), "scripts", "workcell")
	launcherContent, err := os.ReadFile(launcherPath)
	if err != nil {
		t.Fatal(err)
	}
	launcher := string(launcherContent)

	for _, want := range []string{
		"support_matrix_launch_allowed",
		"Supported launch hosts today remain Apple Silicon macOS",
		"refresh_support_matrix_state",
	} {
		if !strings.Contains(launcher, want) {
			t.Fatalf("%s does not contain %q", launcherPath, want)
		}
	}

	installerPath := filepath.Join(repoRoot(t), "scripts", "install-workcell.sh")
	installerContent, err := os.ReadFile(installerPath)
	if err != nil {
		t.Fatal(err)
	}
	installer := string(installerContent)

	for _, want := range []string{
		"hw.optional.arm64",
		"Intel macOS is not supported",
		"require_supported_macos_host_arch",
	} {
		if !strings.Contains(installer, want) {
			t.Fatalf("%s does not contain %q", installerPath, want)
		}
	}

	formulaScriptPath := filepath.Join(repoRoot(t), "scripts", "generate-homebrew-formula.sh")
	formulaScriptContent, err := os.ReadFile(formulaScriptPath)
	if err != nil {
		t.Fatal(err)
	}
	formulaScript := string(formulaScriptContent)

	for _, want := range []string{
		`Hardware::CPU.arm?`,
		"Apple Silicon macOS hosts only",
		`depends_on "git"`,
	} {
		if !strings.Contains(formulaScript, want) {
			t.Fatalf("%s does not contain %q", formulaScriptPath, want)
		}
	}
}

func TestGitHubWorkflowsContinuouslyVerifyInstallAndUninstall(t *testing.T) {
	t.Parallel()

	for _, workflowName := range []string{"ci.yml", "release.yml"} {
		workflowPath := filepath.Join(repoRoot(t), ".github", "workflows", workflowName)
		content, err := os.ReadFile(workflowPath)
		if err != nil {
			t.Fatal(err)
		}
		workflow := string(content)

		for _, want := range []string{
			"macos-26",
			"macos-15",
			"brew tap-new",
			"brew --repo",
			"brew install \"${tap_name}/workcell\"",
			"brew uninstall --force \"${tap_name}/workcell\"",
			`"${bundle_dir}/scripts/install.sh"`,
			`"${bundle_dir}/scripts/uninstall.sh"`,
		} {
			if !strings.Contains(workflow, want) {
				t.Fatalf("%s does not contain %q", workflowPath, want)
			}
		}
	}

	ciWorkflowPath := filepath.Join(repoRoot(t), ".github", "workflows", "ci.yml")
	ciContent, err := os.ReadFile(ciWorkflowPath)
	if err != nil {
		t.Fatal(err)
	}
	ciWorkflow := string(ciContent)

	for _, want := range []string{
		"name: workcell-ci-install-candidate",
		"name: Install verification (${{ matrix.runner_label }})",
	} {
		if !strings.Contains(ciWorkflow, want) {
			t.Fatalf("%s does not contain %q", ciWorkflowPath, want)
		}
	}

	releaseWorkflowPath := filepath.Join(repoRoot(t), ".github", "workflows", "release.yml")
	releaseContent, err := os.ReadFile(releaseWorkflowPath)
	if err != nil {
		t.Fatal(err)
	}
	releaseWorkflow := string(releaseContent)

	if !strings.Contains(releaseWorkflow, "name: workcell-release-install-candidate") {
		t.Fatalf("%s does not contain the reviewed release install artifact upload name", releaseWorkflowPath)
	}
}

func TestPublishProviderBumpPRRequiresCleanWorktree(t *testing.T) {
	t.Parallel()

	scriptPath := filepath.Join(repoRoot(t), "scripts", "publish-provider-bump-pr.sh")
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	script := string(content)

	for _, want := range []string{
		`git -C "${ROOT_DIR}" status --short`,
		`git -C "${ROOT_DIR}" fetch origin "${BASE_BRANCH}"`,
		`refs/remotes/origin/${BASE_BRANCH}`,
		`worktree add --detach "${worktree_root}" "${base_ref}"`,
		`requires a clean worktree`,
		`Commit, stash, or discard local changes first`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("%s does not contain %q", scriptPath, want)
		}
	}
}

func TestReadmeDocumentsRepoPublishWrapperBeforeLowerLevelHelper(t *testing.T) {
	t.Parallel()

	readmePath := filepath.Join(repoRoot(t), "README.md")
	content, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatal(err)
	}
	readme := string(content)

	wrapper := "./scripts/repo-publish-pr.sh --workspace /path/to/repo"
	lowerLevel := "workcell publish-pr --workspace /path/to/repo --branch feature/name"
	wrapperIndex := strings.Index(readme, wrapper)
	lowerLevelIndex := strings.Index(readme, lowerLevel)
	if wrapperIndex < 0 {
		t.Fatalf("%s must document the repo-local publish wrapper", readmePath)
	}
	if lowerLevelIndex < 0 {
		t.Fatalf("%s must document the lower-level publish-pr helper", readmePath)
	}
	if wrapperIndex > lowerLevelIndex {
		t.Fatalf("%s must introduce the repo-local wrapper before the lower-level helper", readmePath)
	}
	for _, want := range []string{
		"./scripts/pre-merge.sh --profile pr-parity",
		"`workcell publish-pr` is the lower-level host-side helper",
		"operator repositories that do not carry Workcell's repo-local parity wrapper",
		"explicitly lower-assurance non-`main` draft path",
	} {
		if !strings.Contains(readme, want) {
			t.Fatalf("%s does not contain %q", readmePath, want)
		}
	}
}

func TestPublishUpstreamRefreshPRRequiresCleanWorktree(t *testing.T) {
	t.Parallel()

	scriptPath := filepath.Join(repoRoot(t), "scripts", "publish-upstream-refresh-pr.sh")
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	script := string(content)

	for _, want := range []string{
		`git -C "${ROOT_DIR}" status --short`,
		`git -C "${ROOT_DIR}" fetch origin "${BASE_BRANCH}"`,
		`refs/remotes/origin/${BASE_BRANCH}`,
		`gh run download "${RUN_ID}" --repo "${REPO}" --name upstream-refresh-candidate`,
		`Candidate patch digest mismatch`,
		`Candidate tree OID mismatch`,
		`requires a clean worktree`,
		`Commit, stash, or discard local changes first`,
		`requires an origin remote`,
		`rm -rf "${worktree_root}"`,
		`git clone --no-hardlinks --no-checkout "${ROOT_DIR}" "${worktree_root}"`,
		`git -C "${worktree_root}" remote set-url origin "${origin_url}"`,
		`git -C "${worktree_root}" fetch --no-tags origin "${BASE_BRANCH}"`,
		`git -C "${worktree_root}" checkout --detach "${base_sha}"`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("%s does not contain %q", scriptPath, want)
		}
	}
}

func TestUpdateUpstreamPinsRefreshesReviewedSources(t *testing.T) {
	t.Parallel()

	scriptPath := filepath.Join(repoRoot(t), "scripts", "update-upstream-pins.sh")
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	script := string(content)

	for _, want := range []string{
		"--apply",
		"--check",
		"scripts/update-provider-pins.sh",
		"https://go.dev/dl/?mode=json",
		"https://static.rust-lang.org/dist/channel-rust-stable.toml",
		"https://static.rust-lang.org/rustup/release-stable.toml",
		"https://api.github.com/repos/docker/buildx/releases/latest",
		"https://api.github.com/repos/sigstore/cosign/releases/latest",
		"https://api.github.com/repos/anchore/syft/releases/latest",
		"https://api.github.com/repos/rhysd/actionlint/releases/latest",
		"--config -",
		`header = "Authorization: Bearer ${token}"`,
		"Accept: application/octet-stream",
		"github_release_asset_api_url",
		`-D "${headers_file}"`,
		`curl -fsSL "${CURL_CHECKSUM_GUARDS[@]}" "${location}"`,
		"https://api.github.com/repos/hadolint/hadolint/releases/latest",
		"hub.docker.com/v2/repositories/tonistiigi/binfmt/tags",
		"docker buildx imagetools inspect",
		"https://snapshot.debian.org/archive/debian/",
		"https://snapshot.debian.org/archive/debian-security/",
		"debian_snapshot_has_bootstrap_packages",
		"openssl_3.5.5-1~deb13u1_amd64.deb",
		"openssl_3.5.5-1~deb13u1_arm64.deb",
		"ca-certificates_20250419_all.deb",
		"scripts/check-pinned-inputs.sh",
		"UPSTREAM_REFRESH_WORKFLOW_PATH",
		"current_upstream_refresh_cosign_version",
		"upstream-refresh-cosign-version",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("%s does not contain %q", scriptPath, want)
		}
	}
	if strings.Contains(script, "--oauth2-bearer") {
		t.Fatalf("%s still passes GitHub tokens through curl argv", scriptPath)
	}

	for _, want := range []string{
		`actionlint_checksums_url="$(github_release_asset_api_url "${actionlint_release_json}" "actionlint_${target_actionlint_version}_checksums.txt")"`,
		`github_release_asset_get "${actionlint_checksums_url}" |`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("actionlint checksum path in %s does not contain %q", scriptPath, want)
		}
	}

	githubAPIGet := extractShellFunction(t, script, "github_api_get")
	if strings.Contains(githubAPIGet, `-H "Authorization: Bearer ${token}"`) {
		t.Fatalf("github_api_get in %s must not follow redirects with a custom GitHub Authorization header", scriptPath)
	}
}

func TestLatestDebianSnapshotRequiresBootstrapPackages(t *testing.T) {
	t.Parallel()

	scriptPath := filepath.Join(repoRoot(t), "scripts", "update-upstream-pins.sh")
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	script := string(content)

	curlLogPath := filepath.Join(t.TempDir(), "curl.log")
	code, output := runBashProbe(t, `set -euo pipefail
`+extractShellFunction(t, script, "latest_debian_snapshot")+`
`+extractShellFunction(t, script, "debian_snapshot_has_bootstrap_packages")+`

date_stamp_for_offset() {
  case "$1" in
    0) printf '%s\n' 20260526T000000Z ;;
    1) printf '%s\n' 20260525T000000Z ;;
    2) printf '%s\n' 20260524T000000Z ;;
    3) printf '%s\n' 20260523T000000Z ;;
    4) printf '%s\n' 20260522T000000Z ;;
    5) printf '%s\n' 20260521T000000Z ;;
    6) printf '%s\n' 20260520T000000Z ;;
    7) printf '%s\n' 20260519T000000Z ;;
    8) printf '%s\n' 20260518T000000Z ;;
    *) printf '%s\n' 20260517T000000Z ;;
  esac
}

curl() {
  local url="${*: -1}"
  printf '%s\n' "${url}" >>"${WORKCELL_CURL_LOG}"
  case "${url}" in
    */dists/trixie/Release | */dists/trixie-updates/Release | */dists/trixie-security/Release)
      return 0
      ;;
    */20260518T000000Z/pool/main/o/openssl/openssl_3.5.5-1~deb13u1_amd64.deb | \
    */20260518T000000Z/pool/main/o/openssl/openssl_3.5.5-1~deb13u1_arm64.deb | \
    */20260518T000000Z/pool/main/c/ca-certificates/ca-certificates_20250419_all.deb)
      return 0
      ;;
    */pool/main/o/openssl/openssl_3.5.5-1~deb13u1_amd64.deb | \
    */pool/main/o/openssl/openssl_3.5.5-1~deb13u1_arm64.deb | \
    */pool/main/c/ca-certificates/ca-certificates_20250419_all.deb)
      return 22
      ;;
  esac
  return 1
}

latest_debian_snapshot
`, map[string]string{"WORKCELL_CURL_LOG": curlLogPath})
	if code != 0 {
		t.Fatalf("probe exit code = %d output=%q", code, output)
	}
	if got := strings.TrimSpace(output); got != "20260518T000000Z" {
		t.Fatalf("latest_debian_snapshot = %q, want 20260518T000000Z", got)
	}

	curlLog, err := os.ReadFile(curlLogPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"https://snapshot.debian.org/archive/debian/20260526T000000Z/pool/main/o/openssl/openssl_3.5.5-1~deb13u1_amd64.deb",
		"https://snapshot.debian.org/archive/debian/20260518T000000Z/pool/main/o/openssl/openssl_3.5.5-1~deb13u1_amd64.deb",
		"https://snapshot.debian.org/archive/debian/20260518T000000Z/pool/main/o/openssl/openssl_3.5.5-1~deb13u1_arm64.deb",
		"https://snapshot.debian.org/archive/debian/20260518T000000Z/pool/main/c/ca-certificates/ca-certificates_20250419_all.deb",
	} {
		if !strings.Contains(string(curlLog), want) {
			t.Fatalf("curl log does not contain %q; log=%s", want, curlLog)
		}
	}
}

func TestLatestDebianSnapshotUsesFreshnessLookback(t *testing.T) {
	t.Parallel()

	scriptPath := filepath.Join(repoRoot(t), "scripts", "update-upstream-pins.sh")
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	script := string(content)

	code, output := runBashProbe(t, `set -euo pipefail
`+extractShellFunction(t, script, "latest_debian_snapshot")+`
`+extractShellFunction(t, script, "debian_snapshot_has_bootstrap_packages")+`

date_stamp_for_offset() {
  if [[ "$1" == "22" ]]; then
    printf '%s\n' 20260518T000000Z
    return
  fi
  printf '202605%02dT000000Z\n' "$((40 - $1))"
}

curl() {
  local url="${*: -1}"
  case "${url}" in
    */dists/trixie/Release | */dists/trixie-updates/Release | */dists/trixie-security/Release)
      return 0
      ;;
    */20260518T000000Z/pool/main/o/openssl/openssl_3.5.5-1~deb13u1_amd64.deb | \
    */20260518T000000Z/pool/main/o/openssl/openssl_3.5.5-1~deb13u1_arm64.deb | \
    */20260518T000000Z/pool/main/c/ca-certificates/ca-certificates_20250419_all.deb)
      return 0
      ;;
    */pool/main/o/openssl/openssl_3.5.5-1~deb13u1_amd64.deb | \
    */pool/main/o/openssl/openssl_3.5.5-1~deb13u1_arm64.deb | \
    */pool/main/c/ca-certificates/ca-certificates_20250419_all.deb)
      return 22
      ;;
  esac
  return 1
}

latest_debian_snapshot
`, nil)
	if code != 0 {
		t.Fatalf("probe exit code = %d output=%q", code, output)
	}
	if got := strings.TrimSpace(output); got != "20260518T000000Z" {
		t.Fatalf("latest_debian_snapshot = %q, want 20260518T000000Z", got)
	}
}

func TestPreMergeChecksPinnedUpstreams(t *testing.T) {
	t.Parallel()

	scriptPath := filepath.Join(repoRoot(t), "scripts", "pre-merge.sh")
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	script := string(content)

	for _, want := range []string{
		`--profile repo-core|pr-parity|release-preflight`,
		`"${ROOT_DIR}/scripts/ci-plan.sh" "${plan_args[@]}" --format json`,
		`echo "[pre-merge] release pin hygiene"`,
		`scripts/ci/job-pin-hygiene.sh)`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("%s does not contain %q", scriptPath, want)
		}
	}
}

func extractShellFunction(tb testing.TB, script, name string) string {
	tb.Helper()

	start := strings.Index(script, name+"() {")
	if start < 0 {
		tb.Fatalf("script does not contain shell function %s", name)
	}
	lines := strings.Split(script[start:], "\n")
	var extracted []string
	for i, line := range lines {
		extracted = append(extracted, line)
		if i > 0 && strings.TrimSpace(line) == "}" {
			return strings.Join(extracted, "\n")
		}
	}
	tb.Fatalf("script shell function %s has no closing brace", name)
	return ""
}
