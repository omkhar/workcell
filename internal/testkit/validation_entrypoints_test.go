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
		`append_unique_apt nodejs npm`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("%s does not contain %q", scriptPath, want)
		}
	}
	for _, unwanted := range []string{
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

func TestLauncherAndInstallerHostSupportGuardsStayPinned(t *testing.T) {
	t.Parallel()

	launcherPath := filepath.Join(repoRoot(t), "scripts", "workcell")
	launcherContent, err := os.ReadFile(launcherPath)
	if err != nil {
		t.Fatal(err)
	}
	launcher := string(launcherContent)

	for _, want := range []string{
		"detected_host_os",
		"detected_host_arch",
		"support-matrix-eval",
		"support_matrix_status",
		"support_matrix_launch",
		"fail_for_unsupported_launch_target",
	} {
		if !strings.Contains(launcher, want) {
			t.Fatalf("%s does not contain %q", launcherPath, want)
		}
	}
	for _, unwanted := range []string{
		"require_supported_macos_host_arch",
		"Intel macOS is not supported",
	} {
		if strings.Contains(launcher, unwanted) {
			t.Fatalf("%s unexpectedly contains %q", launcherPath, unwanted)
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
		"https://api.github.com/repos/hadolint/hadolint/releases/latest",
		"hub.docker.com/v2/repositories/tonistiigi/binfmt/tags",
		"docker buildx imagetools inspect",
		"https://snapshot.debian.org/archive/debian/",
		"https://snapshot.debian.org/archive/debian-security/",
		"scripts/check-pinned-inputs.sh",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("%s does not contain %q", scriptPath, want)
		}
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
		`echo "[pre-merge] pinned upstream refresh check"`,
		`"${ROOT_DIR}/scripts/update-upstream-pins.sh" --check`,
		`echo "[pre-merge] GitHub macOS release test runner verification"`,
		`"${ROOT_DIR}/scripts/verify-github-macos-release-test-runners.sh" macos-26 macos-15`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("%s does not contain %q", scriptPath, want)
		}
	}
}
