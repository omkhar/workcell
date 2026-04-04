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
		if !strings.Contains(content, "scripts/lint-dockerfiles.sh") {
			t.Fatalf("validation scripts must include scripts/lint-dockerfiles.sh")
		}
		if !strings.Contains(content, "gofmt -l") {
			t.Fatalf("validation scripts must include gofmt formatting checks")
		}
		if !strings.Contains(content, "go vet ./...") {
			t.Fatalf("validation scripts must include go vet")
		}
	}

	if !strings.Contains(string(quickCheck), "scripts/verify-go-python-parity.sh") {
		t.Fatalf("%s must include scripts/verify-go-python-parity.sh", quickCheckPath)
	}
	if !strings.Contains(string(validateRepo), "scripts/verify-go-python-parity.sh") {
		t.Fatalf("%s must include scripts/verify-go-python-parity.sh", validateRepoPath)
	}
	for _, want := range []string{
		`${ROOT_DIR}/install.sh`,
		`${ROOT_DIR}/scripts/build-and-test.sh`,
		`${ROOT_DIR}/scripts/install-dev-tools.sh`,
	} {
		if !strings.Contains(string(validateRepo), want) {
			t.Fatalf("%s must lint and format %s", validateRepoPath, want)
		}
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
}

func TestInstallDevToolsBootstrapsNodeAndPythonVenvPrereqs(t *testing.T) {
	t.Parallel()

	scriptPath := filepath.Join(repoRoot(t), "scripts", "install-dev-tools.sh")
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	script := string(content)

	for _, want := range []string{
		`command -v npm`,
		`python3 -m venv --help`,
		`append_unique_brew node`,
		`append_unique_apt nodejs npm`,
		`append_unique_brew python`,
		`append_unique_apt python3 python3-venv python3-pip`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("%s does not contain %q", scriptPath, want)
		}
	}
}
