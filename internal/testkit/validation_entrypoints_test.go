// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package testkit

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestC3CertificationEntrypointSanitizesBuildControl(t *testing.T) {
	t.Parallel()

	script := filepath.Join(repoRoot(t), "scripts", "certify-c3-parallel-sessions.sh")
	bashEnvMarker := filepath.Join(t.TempDir(), "bash-env-ran")
	functionMarker := filepath.Join(t.TempDir(), "function-ran")
	bashEnv := filepath.Join(t.TempDir(), "bash-env")
	if err := os.WriteFile(bashEnv, []byte(`printf injected >"`+bashEnvMarker+`"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	injectedFunction := `() { printf injected >"` + functionMarker + `"; }`
	cmd := exec.Command(script, "--self-entrypoint-probe")
	cmd.Env = canonicalBuildEnv(map[string]string{
		"BASH_ENV":                         bashEnv,
		"BASH_FUNC_exec%%":                 injectedFunction,
		"BASH_FUNC_source%%":               injectedFunction,
		"GOENV":                            filepath.Join(t.TempDir(), "go-env"),
		"GOFLAGS":                          "-overlay=/tmp/workcell-hostile-overlay.json",
		"GOTOOLCHAIN":                      "auto",
		"GOWORK":                           filepath.Join(t.TempDir(), "go.work"),
		"WORKCELL_C3_SANITIZED_ENTRYPOINT": "1",
		"WORKCELL_GO_BIN":                  "/definitely-not-go",
	})
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("entrypoint probe: %v: %s", err, output)
	}
	// Go reports an empty GOENV path when GOENV=off, then the other exact
	// process-level controls.
	if string(output) != "\noff\n-mod=readonly\nlocal\n" {
		t.Fatalf("Go build control = %q", output)
	}
	for _, marker := range []string{bashEnvMarker, functionMarker} {
		if _, err := os.Stat(marker); !os.IsNotExist(err) {
			t.Fatalf("hostile shell control executed: %s: %v", marker, err)
		}
	}

	const usage = "Usage: certify-c3-parallel-sessions.sh --workspace PATH [--precommit-control-tree SHA]\n"
	cmd = exec.Command(script, "--help")
	cmd.Env = canonicalBuildEnv(nil)
	output, err = cmd.CombinedOutput()
	if err != nil || string(output) != usage {
		t.Fatalf("wrapper help = %q, %v", output, err)
	}
	cmd = exec.Command(script)
	cmd.Env = canonicalBuildEnv(nil)
	output, err = cmd.CombinedOutput()
	exitErr, ok := err.(*exec.ExitError)
	if !ok || exitErr.ExitCode() != 2 || string(output) != usage {
		t.Fatalf("wrapper usage failure = %q, %v", output, err)
	}

	cmd = exec.Command(script,
		"--workspace", "/definitely-not-a-workcell-workspace",
		"--root", "/definitely-not-workcell")
	cmd.Env = canonicalBuildEnv(nil)
	output, err = cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "/definitely-not-a-workcell-workspace") ||
		strings.Contains(string(output), "resolve control-plane root") {
		t.Fatalf("wrapper-owned root was not pinned: %v: %s", err, output)
	}
}

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
			"scripts/certify-c3-parallel-sessions.sh",
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
		`${ROOT_DIR}/scripts/ci/job-release-asset-acl.sh`,
		`${ROOT_DIR}/scripts/install.sh`,
		`${ROOT_DIR}/scripts/build-and-test.sh`,
		`${ROOT_DIR}/scripts/install-dev-tools.sh`,
		`${ROOT_DIR}/scripts/update-upstream-pins.sh`,
		`${ROOT_DIR}/scripts/update-provider-pins.sh`,
		`${ROOT_DIR}/scripts/publish-provider-bump-pr.sh`,
		`${ROOT_DIR}/scripts/publish-upstream-refresh-pr.sh`,
		`${ROOT_DIR}/scripts/verify-github-macos-release-test-runners.sh`,
		`${ROOT_DIR}/scripts/verify-upstream-copilot-release.sh`,
		`${ROOT_DIR}/scripts/verify-upstream-gemini-release.sh`,
	} {
		if !strings.Contains(string(validateRepo), want) {
			t.Fatalf("%s must lint and format %s", validateRepoPath, want)
		}
	}
}

func TestValidateRepoExecutesPublicContractHistoryGate(t *testing.T) {
	t.Parallel()

	validateRepoPath := filepath.Join(repoRoot(t), "scripts", "validate-repo.sh")
	content, err := os.ReadFile(validateRepoPath)
	if err != nil {
		t.Fatal(err)
	}

	const directInvocation = `
"${ROOT_DIR}/scripts/check-public-contract.sh"
`
	if !strings.Contains(string(content), directInvocation) {
		t.Fatalf("%s must execute check-public-contract.sh without the self-entrypoint probe", validateRepoPath)
	}
}

func TestPublicContractHistoryGateEstablishesCanonicalGitEnvironment(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	marker := filepath.Join(tempDir, "canonical-git-env")
	goWrapper := filepath.Join(tempDir, "go")
	wrapper := `#!/bin/bash -p
set -euo pipefail
[[ "${GIT_NO_REPLACE_OBJECTS:-}" == "1" ]]
[[ "${GIT_CONFIG_NOSYSTEM:-}" == "1" ]]
[[ "${GIT_CONFIG_SYSTEM:-}" == "/dev/null" ]]
[[ "${GIT_CONFIG_GLOBAL:-}" == "/dev/null" ]]
[[ "${GIT_CONFIG_COUNT:-}" == "1" ]]
[[ "${GIT_CONFIG_KEY_0:-}" == "core.attributesFile" ]]
[[ "${GIT_CONFIG_VALUE_0:-}" == "/dev/null" ]]
[[ -z "${GIT_REPLACE_REF_BASE:-}" ]]
printf 'canonical\n' >>"${WORKCELL_CANONICAL_GIT_MARKER:?}"
`
	if err := os.WriteFile(goWrapper, []byte(wrapper), 0o755); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(tempDir, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".gitconfig"), []byte("[core]\n\tattributesFile = /tmp/workcell-hostile-attributes\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	scriptPath := filepath.Join(repoRoot(t), "scripts", "check-public-contract.sh")
	cmd := exec.Command(scriptPath)
	cmd.Env = canonicalBuildEnv(map[string]string{
		"GIT_CONFIG_GLOBAL":             filepath.Join(home, ".gitconfig"),
		"GIT_NO_REPLACE_OBJECTS":        "0",
		"GIT_REPLACE_REF_BASE":          "refs/workcell-hostile/",
		"HOME":                          home,
		"WORKCELL_CANONICAL_GIT_MARKER": marker,
		"WORKCELL_GO_BIN":               goWrapper,
	})
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("public contract history gate: %v: %s", err, output)
	}
	content, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(content), "canonical\n"); got != 2 {
		t.Fatalf("canonical Git environment checks = %d, want 2", got)
	}
}

func TestVerifyOperatorContractIgnoresAmbientHelpOverride(t *testing.T) {
	t.Parallel()

	scriptPath := filepath.Join(repoRoot(t), "scripts", "verify-operator-contract.sh")
	marker := filepath.Join(t.TempDir(), "hostile-help-ran")
	helpBin := filepath.Join(t.TempDir(), "hostile-workcell")
	if err := os.WriteFile(helpBin, []byte("#!/bin/sh\n: >\"${WORKCELL_HELP_MARKER:?}\"\nexit 97\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(scriptPath)
	cmd.Env = canonicalBuildEnv(map[string]string{
		"WORKCELL_HELP_BIN":    helpBin,
		"WORKCELL_HELP_MARKER": marker,
	})
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("verify operator contract with hostile help override: %v\n%s", err, output)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("hostile help override executed: %v", err)
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
		`if [[ "${host_os}" == "Linux" ]]; then`,
		`require_markdownlint_node`,
		`require_markdownlint_npm`,
		`npm ci --prefix "${MARKDOWNLINT_DIR}" --ignore-scripts --omit=dev`,
		`install -m 0444 "${MARKDOWNLINT_DIR}/package-lock.json" "${MARKDOWNLINT_LOCK_STAMP}"`,
		`"${MARKDOWNLINT_BIN}" --version`,
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

func TestValidateRepoUsesLockedMarkdownlint(t *testing.T) {
	t.Parallel()

	scriptPath := filepath.Join(repoRoot(t), "scripts", "validate-repo.sh")
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	script := string(content)
	for _, want := range []string{
		`MARKDOWNLINT_LOCKFILE="${ROOT_DIR}/tools/markdownlint/package-lock.json"`,
		`"${ROOT_DIR}/tools/markdownlint"`,
		`"/usr/local/lib/workcell-markdownlint"`,
		`cmp -s "${MARKDOWNLINT_LOCKFILE}" "${install_dir}/node_modules/.workcell-package-lock.json"`,
		`if ! MARKDOWNLINT_BIN="$(resolve_markdownlint_bin)"; then`,
		`"${MARKDOWNLINT_BIN}" "${markdown_files[@]}"`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("%s does not contain %q", scriptPath, want)
		}
	}
	if strings.Contains(script, "require_tool markdownlint") {
		t.Fatalf("%s should not trust a PATH-resolved markdownlint", scriptPath)
	}
	if count := strings.Count(script, `-path "${ROOT_DIR}/tools/markdownlint/node_modules" -prune -o`); count < 4 {
		t.Fatalf("%s should prune locked markdownlint dependencies from repository scans; found %d guards", scriptPath, count)
	}
}

func TestValidateRepoRejectsStaleMarkdownlintInstall(t *testing.T) {
	t.Parallel()

	scriptPath := filepath.Join(repoRoot(t), "scripts", "validate-repo.sh")
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	script := string(content)
	start := strings.Index(script, `MARKDOWNLINT_LOCKFILE=`)
	if start < 0 {
		t.Fatalf("%s is missing the markdownlint resolver variables", scriptPath)
	}
	end := strings.Index(script[start:], "\n}\n\nHOME=")
	if end < 0 {
		t.Fatalf("%s is missing the end of resolve_markdownlint_bin", scriptPath)
	}
	resolver := script[start : start+end+3]

	fixtureRoot := t.TempDir()
	installDir := filepath.Join(fixtureRoot, "tools", "markdownlint")
	binPath := filepath.Join(installDir, "node_modules", ".bin", "markdownlint")
	stampPath := filepath.Join(installDir, "node_modules", ".workcell-package-lock.json")
	lockPath := filepath.Join(installDir, "package-lock.json")
	if err := os.MkdirAll(filepath.Dir(binPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lockPath, []byte("current lock\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stampPath, []byte("stale lock\n"), 0o444); err != nil {
		t.Fatal(err)
	}

	probePath := filepath.Join(t.TempDir(), "markdownlint-resolver-probe.sh")
	probe := "#!/bin/bash\nset -euo pipefail\nROOT_DIR=\"$1\"\n" + resolver + "\nresolve_markdownlint_bin\n"
	if err := os.WriteFile(probePath, []byte(probe), 0o700); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("/bin/bash", probePath, fixtureRoot).CombinedOutput(); err == nil {
		t.Fatalf("resolve_markdownlint_bin accepted a stale lock stamp: %s", output)
	}
	if err := os.Chmod(stampPath, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stampPath, []byte("current lock\n"), 0o444); err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command("/bin/bash", probePath, fixtureRoot).CombinedOutput()
	if err != nil {
		t.Fatalf("resolve_markdownlint_bin rejected a current lock stamp: %v\n%s", err, output)
	}
	if got, want := strings.TrimSpace(string(output)), binPath; got != want {
		t.Fatalf("resolve_markdownlint_bin = %q, want %q", got, want)
	}
}

func TestDocsValidatorPrunesLockedMarkdownlintDependencies(t *testing.T) {
	t.Parallel()

	scriptPath := filepath.Join(repoRoot(t), "scripts", "ci", "run-docs-in-validator.sh")
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), `-path /workspace/tools/markdownlint/node_modules -prune -o`) {
		t.Fatalf("%s should prune locked markdownlint dependencies from documentation scans", scriptPath)
	}
}

func TestInstallDevToolsEnforcesLockedMarkdownlintNodeRanges(t *testing.T) {
	t.Parallel()

	scriptPath := filepath.Join(repoRoot(t), "scripts", "install-dev-tools.sh")
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	prefix, _, ok := strings.Cut(string(content), "markdownlint_node_install_hint() {")
	if !ok {
		t.Fatalf("%s is missing markdownlint_node_install_hint", scriptPath)
	}
	probe := filepath.Join(t.TempDir(), "node-range-probe.sh")
	if err := os.WriteFile(probe, []byte(prefix+"version=\"$(node_version)\"\nmarkdownlint_node_compatible \"${version}\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		version string
		accept  bool
	}{
		{version: "v22.22.1"},
		{version: "v22.22.2", accept: true},
		{version: "v22.99.0", accept: true},
		{version: "v23.99.0"},
		{version: "v24.14.99"},
		{version: "v24.15.0", accept: true},
		{version: "v24.15.0-rc.1"},
		{version: "v25.99.0"},
		{version: "v26.0.0", accept: true},
		{version: "v26.0.0-nightly"},
		{version: "v27.0.0", accept: true},
		{version: "v26"},
		{version: "v26.bad.0"},
		{version: "not-a-version"},
	} {
		t.Run(tc.version, func(t *testing.T) {
			binDir := t.TempDir()
			node := filepath.Join(binDir, "node")
			if err := os.WriteFile(node, []byte("#!/bin/sh\nprintf '%s\\n' '"+tc.version+"'\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			command := exec.Command("/bin/bash", probe)
			command.Env = []string{"PATH=" + binDir + ":/usr/bin:/bin"}
			err := command.Run()
			if tc.accept && err != nil {
				t.Fatalf("markdownlint_node_compatible(%q) rejected a supported version: %v", tc.version, err)
			}
			if !tc.accept && err == nil {
				t.Fatalf("markdownlint_node_compatible(%q) accepted an unsupported version", tc.version)
			}
		})
	}
}

func TestGenerateHomebrewFormulaPinsExplicitVersion(t *testing.T) {
	t.Parallel()

	scriptPath := filepath.Join(repoRoot(t), "scripts", "generate-homebrew-formula.sh")
	formulaPath := filepath.Join(t.TempDir(), "Formula", "workcell.rb")
	const version = "v1.2.3"
	const checksum = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	cmd := exec.Command(scriptPath, version, checksum, formulaPath, "--repository", "omkhar/workcell")
	cmd.Env = canonicalBuildEnv(map[string]string{
		"GITHUB_REPOSITORY": "hostile/example",
	})
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generate Homebrew formula: %v\n%s", err, output)
	}
	formula, err := os.ReadFile(formulaPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`url "https://github.com/omkhar/workcell/releases/download/v1.2.3/workcell-v1.2.3.tar.gz"`,
		`version "1.2.3"`,
		`sha256 "` + checksum + `"`,
	} {
		if !strings.Contains(string(formula), want) {
			t.Fatalf("generated formula does not contain %q\n%s", want, formula)
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
		// The support-bundle diagnostics command emits clean JSON on stdout
		// and must not have --debug-log/--rebuild injected by the wrapper.
		"support-bundle)",
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
		"Preserved ~/.config/workcell, shared host packages, and unrelated Colima profiles.",
		"The uninstaller removes logs directly under /tmp or \\$TMPDIR if the current user owns them and their names match Workcell cleanup patterns.",
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

func TestGitHubWorkflowsVerifyHostedInstallEvidence(t *testing.T) {
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

		var parsed struct {
			Defaults struct {
				Run struct {
					Shell string `yaml:"shell"`
				} `yaml:"run"`
			} `yaml:"defaults"`
			Jobs map[string]struct {
				If              string     `yaml:"if"`
				RunsOn          string     `yaml:"runs-on"`
				Needs           yaml.Node  `yaml:"needs"`
				ContinueOnError *yaml.Node `yaml:"continue-on-error"`
				Defaults        struct {
					Run struct {
						Shell string `yaml:"shell"`
					} `yaml:"run"`
				} `yaml:"defaults"`
				Strategy struct {
					Matrix struct {
						Include []struct {
							Runner      string `yaml:"runner"`
							RunnerLabel string `yaml:"runner_label"`
						} `yaml:"include"`
					} `yaml:"matrix"`
				} `yaml:"strategy"`
				Steps []struct {
					Name            string     `yaml:"name"`
					Run             string     `yaml:"run"`
					If              string     `yaml:"if"`
					ContinueOnError *yaml.Node `yaml:"continue-on-error"`
					Shell           string     `yaml:"shell"`
				} `yaml:"steps"`
			} `yaml:"jobs"`
		}
		if err := yaml.Unmarshal(content, &parsed); err != nil {
			t.Fatalf("parse %s: %v", workflowPath, err)
		}
		if parsed.Defaults.Run.Shell != "bash --noprofile --norc -euo pipefail {0}" {
			t.Fatalf("%s must use the strict Bash workflow default", workflowPath)
		}
		installJob, ok := parsed.Jobs["install-verification"]
		if !ok {
			t.Fatalf("%s must contain jobs.install-verification", workflowPath)
		}
		wantJobIf := ""
		if workflowName == "ci.yml" {
			wantJobIf = "${{ github.event_name != 'pull_request' || contains(github.event.pull_request.labels.*.name, 'approved-heavy-ci') }}"
		}
		if installJob.If != wantJobIf ||
			installJob.RunsOn != "${{ matrix.runner }}" ||
			installJob.ContinueOnError != nil ||
			installJob.Defaults.Run.Shell != "" {
			t.Fatalf("%s install-verification job metadata does not match the hosted evidence contract", workflowPath)
		}
		if workflowName == "ci.yml" {
			if installJob.Needs.Kind != yaml.ScalarNode || installJob.Needs.Value != "validate" {
				t.Fatalf("%s install-verification job must require validate", workflowPath)
			}
		} else if installJob.Needs.Kind != yaml.SequenceNode ||
			len(installJob.Needs.Content) != 2 ||
			installJob.Needs.Content[0].Value != "tag-policy" ||
			installJob.Needs.Content[1].Value != "preflight" {
			t.Fatalf("%s install-verification job must require tag-policy and preflight", workflowPath)
		}
		include := installJob.Strategy.Matrix.Include
		if len(include) != 2 ||
			include[0].Runner != "macos-26" || include[0].RunnerLabel != "macos-26" ||
			include[1].Runner != "macos-15" || include[1].RunnerLabel != "macos-15" {
			t.Fatalf("%s install-verification job must use the macos-26 and macos-15 matrix", workflowPath)
		}
		installerRun := ""
		installerStepCount := 0
		for _, step := range installJob.Steps {
			if step.Name == "Verify release bundle installer" {
				if step.If != "" || step.ContinueOnError != nil || step.Shell != "" {
					t.Fatalf("%s release bundle installer step must use required default execution", workflowPath)
				}
				installerRun = step.Run
				installerStepCount++
			}
		}
		if installerStepCount != 1 {
			t.Fatalf("%s must contain one release bundle installer step; got %d", workflowPath, installerStepCount)
		}
		executableLines := make([]string, 0)
		for _, line := range strings.Split(installerRun, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			executableLines = append(executableLines, line)
		}
		missingBundleMessage := "Missing CI release bundle artifact"
		if workflowName == "release.yml" {
			missingBundleMessage = "Missing preflight release bundle artifact"
		}
		wantInstallerLines := []string{
			`set -euo pipefail`,
			`mkdir -p "${HOME}"`,
			`bundle_path="$(find dist/install -maxdepth 1 -type f -name 'workcell-*.tar.gz' -print -quit)"`,
			`if [[ -z "${bundle_path}" ]]; then`,
			`echo "` + missingBundleMessage + `" >&2`,
			`exit 1`,
			`fi`,
			`extract_root="${RUNNER_TEMP}/bundle-install"`,
			`rm -rf "${extract_root}"`,
			`mkdir -p "${extract_root}"`,
			`tar -xzf "${bundle_path}" -C "${extract_root}"`,
			`bundle_dir="$(find "${extract_root}" -mindepth 1 -maxdepth 1 -type d -name 'workcell-*' -print -quit)"`,
			`if [[ -z "${bundle_dir}" ]]; then`,
			`echo "Unable to locate extracted bundle root" >&2`,
			`exit 1`,
			`fi`,
			`test ! -e "${HOME}/.local/bin/workcell"`,
			`test ! -L "${HOME}/.local/bin/workcell"`,
			`test ! -e "${HOME}/.local/share/man/man1/workcell.1"`,
			`test ! -L "${HOME}/.local/share/man/man1/workcell.1"`,
			`"${bundle_dir}/scripts/install.sh"`,
			`"${HOME}/.local/bin/workcell" --help >/dev/null`,
			`test -e "${HOME}/.local/bin/workcell"`,
			`test -L "${HOME}/.local/bin/workcell"`,
			`test -e "${HOME}/.local/share/man/man1/workcell.1"`,
			`test -L "${HOME}/.local/share/man/man1/workcell.1"`,
			`HOME="${HOME}" "${bundle_dir}/scripts/uninstall.sh"`,
			`test ! -e "${HOME}/.local/bin/workcell"`,
			`test ! -L "${HOME}/.local/bin/workcell"`,
			`test ! -e "${HOME}/.local/share/man/man1/workcell.1"`,
			`test ! -L "${HOME}/.local/share/man/man1/workcell.1"`,
		}
		gotInstallerRun := strings.Join(executableLines, "\n")
		wantInstallerRun := strings.Join(wantInstallerLines, "\n")
		if gotInstallerRun != wantInstallerRun {
			t.Fatalf("%s must prove clean install, link creation, and complete link removal\nwant:\n%s\ngot:\n%s", workflowPath, wantInstallerRun, gotInstallerRun)
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

func TestSafePathDocDocumentsRepoPublishWrapperBeforeLowerLevelHelper(t *testing.T) {
	t.Parallel()

	// The safe-path publication guidance moved out of README.md into this
	// dedicated operator doc when the README was tiered into entry points.
	docPath := filepath.Join(repoRoot(t), "docs", "safe-path-expectations.md")
	content, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatal(err)
	}
	doc := string(content)

	wrapper := "./scripts/repo-publish-pr.sh --workspace /path/to/repo"
	lowerLevel := "workcell publish-pr --workspace /path/to/repo --branch feature/name"
	wrapperIndex := strings.Index(doc, wrapper)
	lowerLevelIndex := strings.Index(doc, lowerLevel)
	if wrapperIndex < 0 {
		t.Fatalf("%s must document the repo-local publish wrapper", docPath)
	}
	if lowerLevelIndex < 0 {
		t.Fatalf("%s must document the lower-level publish-pr helper", docPath)
	}
	if wrapperIndex > lowerLevelIndex {
		t.Fatalf("%s must introduce the repo-local wrapper before the lower-level helper", docPath)
	}
	for _, want := range []string{
		"./scripts/pre-merge.sh --profile pr-parity",
		"`workcell publish-pr` is the lower-level host-side helper",
		"operator repositories that do not carry Workcell's repo-local parity wrapper",
		"explicitly lower-assurance non-`main` draft path",
	} {
		if !strings.Contains(doc, want) {
			t.Fatalf("%s does not contain %q", docPath, want)
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

	const commitTemplateStart = `cat >"${commit_file}" <<'EOF'
^F Refresh pinned upstreams (pr-parity passed; runtime/provider maintenance)`
	if !strings.Contains(script, commitTemplateStart) {
		t.Fatalf("%s must generate the reviewed Risk-Aware upstream-refresh commit subject", scriptPath)
	}
}

func TestUpdateProviderPinsStagesCodexNamespaceAndLockfileBeforePublishingBump(t *testing.T) {
	t.Parallel()

	path := filepath.Join(repoRoot(t), "scripts", "update-provider-pins.sh")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	script := string(content)
	prepare := strings.Index(script, "prepare-codex-subcommand-fixture")
	apply := strings.Index(script, "apply-provider-bump-plan")
	publishDockerfile := strings.Index(script, `mv -f -- "${dockerfile_candidate}" "${DOCKERFILE_PATH}"`)
	publishPackageJSON := strings.Index(script, `mv -f -- "${providers_candidate_dir}/package.json" "${PROVIDERS_PACKAGE_JSON_PATH}"`)
	publishPackageLock := strings.Index(script, `mv -f -- "${providers_candidate_dir}/package-lock.json" "${PROVIDERS_DIR}/package-lock.json"`)
	publishCodexFixture := strings.Index(script, `mv -f -- "${codex_fixture_candidate}" "${CODEX_SUBCOMMAND_FIXTURE_PATH}"`)
	lockfile := strings.Index(script, `"${NPM_BIN}" install --package-lock-only`)
	verify := strings.LastIndex(script, "\nverify_provider_releases\n")
	if prepare < 0 || apply < 0 || publishDockerfile < 0 || publishPackageJSON < 0 || publishPackageLock < 0 || publishCodexFixture < 0 || lockfile < 0 || verify < 0 {
		t.Fatalf("%s must prepare, apply, publish, refresh the lockfile, and verify the Codex fixture update", path)
	}
	if !(prepare < apply && apply < lockfile &&
		lockfile < publishDockerfile && lockfile < publishPackageJSON && lockfile < publishPackageLock && lockfile < publishCodexFixture &&
		publishPackageLock < publishPackageJSON && publishCodexFixture < publishDockerfile &&
		publishDockerfile < verify && publishPackageJSON < verify && publishPackageLock < verify && publishCodexFixture < verify) {
		t.Fatalf("%s must stage every fallible refresh and publish dependent artifacts before their plan-driving pins", path)
	}
	for _, want := range []string{
		`CODEX_SUBCOMMAND_FIXTURE_PATH="${ROOT_DIR}/tests/fixtures/codex-subcommands.txt"`,
		`codex_fixture_candidate="$(mktemp "${CODEX_SUBCOMMAND_FIXTURE_PATH}.XXXXXX")"`,
		`[[ -z "${codex_fixture_candidate}" ]] || rm -f "${codex_fixture_candidate}"`,
		`[[ -z "${dockerfile_candidate}" ]] || rm -f "${dockerfile_candidate}"`,
		`"${PROVIDERS_DIR}"/.workcell-provider-bump.*) rm -rf -- "${providers_candidate_dir}" ;;`,
		`dockerfile_candidate="$(mktemp "${DOCKERFILE_PATH}.XXXXXX")"`,
		`providers_candidate_dir="$(mktemp -d "${PROVIDERS_DIR}/.workcell-provider-bump.XXXXXX")"`,
		`"${dockerfile_candidate}"`,
		`"${providers_candidate_dir}/package.json"`,
		`cd "${providers_candidate_dir}"`,
		`if [[ ! -f "${providers_candidate_dir}/package.json" || -L "${providers_candidate_dir}/package.json" ||`,
		`! -f "${providers_candidate_dir}/package-lock.json" || -L "${providers_candidate_dir}/package-lock.json" ]]`,
		`unexpected_provider_candidate="$(find "${providers_candidate_dir}"`,
		"verify_provider_releases() {",
		"  verify_provider_releases\n  print_summary\n  echo \"No eligible stable provider pin updates found.\"",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("%s is missing fail-closed Codex fixture handling %q", path, want)
		}
	}
}

func TestDebianSnapshotFreshnessDefaultsToSixtyDays(t *testing.T) {
	t.Parallel()

	for _, rel := range []string{"scripts/update-upstream-pins.sh", "scripts/check-pinned-inputs.sh"} {
		path := filepath.Join(repoRoot(t), filepath.FromSlash(rel))
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		want := `MAX_DEBIAN_SNAPSHOT_AGE_DAYS="${WORKCELL_MAX_DEBIAN_SNAPSHOT_AGE_DAYS:-60}"`
		if !strings.Contains(string(content), want) {
			t.Fatalf("%s must default Debian snapshot freshness to exactly 60 days", path)
		}
	}
}

func TestLatestDebianSnapshotFallsBackWhenNewestBootstrapPlanIsUnsuitable(t *testing.T) {
	t.Parallel()

	scriptPath := filepath.Join(repoRoot(t), "scripts", "update-upstream-pins.sh")
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	script := string(content)

	code, output := runBashProbe(t, `set -euo pipefail
`+extractShellFunction(t, script, "latest_debian_bootstrap_plan")+`

date_stamp_for_offset() {
  [[ "$1" == "0" ]] && printf '%s\n' 20260526T000000Z || printf '%s\n' 20260525T000000Z
}
curl() { return 0; }
resolve_debian_bootstrap_pins() {
  [[ "$1" != "20260526T000000Z" ]] || return 1
  printf '%s\n' '{"snapshot":"20260525T000000Z"}'
}

DEBIAN_SNAPSHOT_LOOKBACK_DAYS=1
MAX_DEBIAN_SNAPSHOT_AGE_DAYS=1
latest_debian_bootstrap_plan
`, nil)
	if code != 0 {
		t.Fatalf("probe exit code = %d output=%q", code, output)
	}
	if !strings.Contains(output, `"snapshot":"20260525T000000Z"`) {
		t.Fatalf("latest_debian_bootstrap_plan did not fall back: %q", output)
	}
}

func TestLatestDebianSnapshotBoundsLookbackByConfiguredMaximumAge(t *testing.T) {
	t.Parallel()

	scriptPath := filepath.Join(repoRoot(t), "scripts", "update-upstream-pins.sh")
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	resolutionLog := filepath.Join(t.TempDir(), "resolution.log")
	code, output := runBashProbe(t, `set -euo pipefail
`+extractShellFunction(t, string(content), "latest_debian_bootstrap_plan")+`

date_stamp_for_offset() {
  printf '202605%02dT000000Z\n' "$((26 - $1))"
}
curl() { return 0; }
resolve_debian_bootstrap_pins() {
  printf '%s\n' "$1" >>"${WORKCELL_RESOLUTION_LOG}"
  return 1
}

DEBIAN_SNAPSHOT_LOOKBACK_DAYS=2
MAX_DEBIAN_SNAPSHOT_AGE_DAYS=1
latest_debian_bootstrap_plan
`, map[string]string{"WORKCELL_RESOLUTION_LOG": resolutionLog})
	if code != 1 {
		t.Fatalf("bounded lookback exit code = %d output=%q, want 1", code, output)
	}
	if want := "Unable to resolve a Debian snapshot within 1 days for trixie/trixie-updates/trixie-security with HTTPS-fetched and byte-verified bootstrap packages\n"; output != want {
		t.Fatalf("bounded lookback output = %q, want %q", output, want)
	}
	logContent, err := os.ReadFile(resolutionLog)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(logContent), "20260526T000000Z\n20260525T000000Z\n"; got != want {
		t.Fatalf("bounded lookback resolution order = %q, want %q", got, want)
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
