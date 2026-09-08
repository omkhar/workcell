// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package testkit

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestHostedControlsRunnerRelaysTokenOnlyThroughStdin(t *testing.T) {
	root := t.TempDir()
	scriptsDir := filepath.Join(root, "scripts")
	copyHostedControlFixture(t, "scripts/run-hosted-controls-audit.sh", filepath.Join(scriptsDir, "run-hosted-controls-audit.sh"))
	verifier := filepath.Join(scriptsDir, "verify-github-hosted-controls.sh")
	writeCanonicalFixture(t, verifier, []byte(hostedControlChildProbe), 0o755)

	stdinPath := filepath.Join(root, "stdin")
	argsPath := filepath.Join(root, "args")
	envPath := filepath.Join(root, "environment")
	cmd := exec.Command(filepath.Join(scriptsDir, "run-hosted-controls-audit.sh"), "owner/repo")
	cmd.Env = hostedControlProbeEnvironment(stdinPath, argsPath, envPath, "37")
	output, err := cmd.CombinedOutput()
	requireExitStatus(t, err, 37, output)
	requireHostedControlProbe(t, stdinPath, argsPath, envPath, "dummy-token\n")
}

func TestHostedControlsRunnerRequiresNamespacedTokenInGitHubActions(t *testing.T) {
	root := t.TempDir()
	scriptsDir := filepath.Join(root, "scripts")
	runner := filepath.Join(scriptsDir, "run-hosted-controls-audit.sh")
	copyHostedControlFixture(t, "scripts/run-hosted-controls-audit.sh", runner)
	writeCanonicalFixture(t, filepath.Join(scriptsDir, "verify-github-hosted-controls.sh"), []byte(hostedControlChildProbe), 0o755)
	environment := hostedControlProbeEnvironment(filepath.Join(root, "stdin"), filepath.Join(root, "args"), filepath.Join(root, "environment"), "0")
	environment = environmentWithout(environment, "WORKCELL_HOSTED_CONTROLS_TOKEN")
	cmd := exec.Command(runner, "owner/repo")
	cmd.Env = environment
	output, err := cmd.CombinedOutput()
	requireExitStatus(t, err, 1, output)
	if !strings.Contains(string(output), "requires WORKCELL_HOSTED_CONTROLS_TOKEN") {
		t.Fatalf("runner output = %q, want namespaced-token requirement", output)
	}
}

func TestHostedControlsRunnerUsesGitHubTokenOutsideActions(t *testing.T) {
	root := t.TempDir()
	scriptsDir := filepath.Join(root, "scripts")
	runner := filepath.Join(scriptsDir, "run-hosted-controls-audit.sh")
	copyHostedControlFixture(t, "scripts/run-hosted-controls-audit.sh", runner)
	writeCanonicalFixture(t, filepath.Join(scriptsDir, "verify-github-hosted-controls.sh"), []byte(hostedControlChildProbe), 0o755)
	stdinPath := filepath.Join(root, "stdin")
	argsPath := filepath.Join(root, "args")
	envPath := filepath.Join(root, "environment")
	environment := hostedControlProbeEnvironment(stdinPath, argsPath, envPath, "31")
	environment = environmentWithout(environment, "GITHUB_ACTIONS")
	cmd := exec.Command(runner, "owner/repo")
	cmd.Env = environment
	output, err := cmd.CombinedOutput()
	requireExitStatus(t, err, 31, output)
	requireHostedControlProbe(t, stdinPath, argsPath, envPath, "wrong-gh-token\n")
}

func TestHostedControlsVerifierRelaysAmbientTokenOnce(t *testing.T) {
	root := t.TempDir()
	verifier := filepath.Join(root, "verify-github-hosted-controls.sh")
	sourcePath := filepath.Join(repoRoot(t), "scripts", "verify-github-hosted-controls.sh")
	content, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	anchor := `if [[ -n "${WORKCELL_GO_BIN:-}" ]]; then`
	probe := hostedControlDirectRelayProbe + "\n" + anchor
	mutant := strings.Replace(string(content), anchor, probe, 1)
	if mutant == string(content) {
		t.Fatal("direct-relay probe anchor is missing")
	}
	writeCanonicalFixture(t, verifier, []byte(mutant), 0o755)

	stdinPath := filepath.Join(root, "stdin")
	argsPath := filepath.Join(root, "args")
	envPath := filepath.Join(root, "environment")
	cmd := exec.Command(verifier, "owner/repo")
	cmd.Env = hostedControlProbeEnvironment(stdinPath, argsPath, envPath, "29")
	output, err := cmd.CombinedOutput()
	requireExitStatus(t, err, 29, output)
	requireHostedControlProbe(t, stdinPath, argsPath, envPath, "wrong-gh-token\n")
}

func TestHostedControlsVerifierRejectsAmbientGoBinaryBeforeExecution(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "executed")
	goWrapper := filepath.Join(t.TempDir(), "go")
	writeCanonicalFixture(t, goWrapper, []byte("#!/bin/sh\nprintf executed >\"$WORKCELL_TEST_MARKER\"\n"), 0o755)
	verifier := filepath.Join(repoRoot(t), "scripts", "verify-github-hosted-controls.sh")
	cmd := exec.Command(verifier, "owner/repo")
	cmd.Env = append(os.Environ(),
		"GH_TOKEN=dummy-token",
		"WORKCELL_GO_BIN="+goWrapper,
		"WORKCELL_TEST_MARKER="+marker,
	)
	output, err := cmd.CombinedOutput()
	requireExitStatus(t, err, 2, output)
	if !strings.Contains(string(output), "reject ambient WORKCELL_GO_BIN") {
		t.Fatalf("verifier output = %q, want WORKCELL_GO_BIN rejection", output)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ambient Go binary executed: %v", err)
	}
}

func TestHostedControlsVerifierRejectsMalformedTokenInput(t *testing.T) {
	verifier := filepath.Join(repoRoot(t), "scripts", "verify-github-hosted-controls.sh")
	for _, input := range []string{"", "token", "token\nextra", "token\n\n", "white space\n", strings.Repeat("a", 4097) + "\n"} {
		cmd := exec.Command(verifier, "--token-stdin", "owner/repo")
		cmd.Env = canonicalBuildEnv(nil)
		cmd.Stdin = strings.NewReader(input)
		output, err := cmd.CombinedOutput()
		requireExitStatus(t, err, 2, output)
	}
}

func TestHostedControlsVerifierScopesDummyTokenToGitHubCommands(t *testing.T) {
	root := t.TempDir()
	prepareHostedControlCommandGraphFixture(t, root)
	verifier := filepath.Join(root, "scripts", "verify-github-hosted-controls.sh")
	cmd := exec.Command(verifier, "--token-stdin", "owner/repo")
	cmd.Env = append(canonicalBuildEnv(nil),
		"WORKCELL_TEST_LOG_DIR="+filepath.Join(root, "logs"),
		"WORKCELL_TEST_CITOOLS_TEMPLATE="+filepath.Join(root, "citools-template"),
	)
	cmd.Stdin = strings.NewReader("dummy-token\n")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("command graph fixture: %v: %s", err, output)
	}
	requireHostedControlFile(t, filepath.Join(root, "logs", "go"), "clean\n")
	requireHostedControlFile(t, filepath.Join(root, "logs", "jq"), "clean\n")
	requireHostedControlFile(t, filepath.Join(root, "logs", "citools"), "clean\n")
	requireHostedControlGitHubLog(t, filepath.Join(root, "logs", "gh"))
	requireHostedControlFile(t, filepath.Join(root, "logs", "citools-commands"), "merge-hosted-control-object-pages\nmerge-hosted-control-array-pages\nlist-hosted-control-ruleset-ids\nnormalize-hosted-control-ruleset\nassemble-hosted-control-rulesets\nmerge-hosted-control-object-pages\nlist-hosted-control-environments\nmerge-hosted-control-object-pages\nmerge-hosted-control-object-pages\nmerge-hosted-control-object-pages\nverify-github-hosted-controls\n")
}

func requireHostedControlGitHubLog(t *testing.T, path string) {
	t.Helper()
	ghLog, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(ghLog)), "\n")
	if len(lines) != 15 {
		t.Fatalf("gh calls = %d, want 15: %q", len(lines), ghLog)
	}
	requiredPrefix := "token=dummy-token aliases=clean args=api --hostname github.com -H X-GitHub-Api-Version: 2026-03-10"
	for _, line := range lines {
		if !strings.HasPrefix(line, requiredPrefix) {
			t.Fatalf("gh log contains an unscoped or unpinned API call: %q", line)
		}
	}
}

func prepareHostedControlCommandGraphFixture(t *testing.T, root string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "logs"), 0o755); err != nil {
		t.Fatal(err)
	}
	copyHostedControlFixture(t, "scripts/lib/canonical-build-env.sh", filepath.Join(root, "scripts", "lib", "canonical-build-env.sh"))
	copyHostedControlFixture(t, "scripts/lib/go-run-env.sh", filepath.Join(root, "scripts", "lib", "go-run-env.sh"))
	copyHostedControlFixture(t, "go.mod", filepath.Join(root, "go.mod"))
	// GO_BIN, GH_BIN, and JQ_BIN below are spliced as literal text into the
	// generated shell script, so their path has to come from ExecFixtureDir
	// rather than root: root is the hostile TMPDIR-derived t.TempDir() under
	// the hostile-env lane. ExecFixtureDir's own path is shell-safe by
	// construction, but the splice below still uses ShellQuote as defense in
	// depth rather than trusting that invariant to hold forever.
	binDir := ExecFixtureDir(t)
	writeCanonicalFixture(t, filepath.Join(binDir, "go"), []byte(hostedControlFakeGo), 0o755)
	writeCanonicalFixture(t, filepath.Join(binDir, "gh"), []byte(hostedControlFakeGH), 0o755)
	jqPath, err := exec.LookPath("jq")
	if err != nil {
		t.Fatal(err)
	}
	jqFixture := strings.Replace(hostedControlFakeJQ, "@JQ_PATH@", jqPath, 1)
	writeCanonicalFixture(t, filepath.Join(binDir, "jq"), []byte(jqFixture), 0o755)
	writeCanonicalFixture(t, filepath.Join(root, "citools-template"), []byte(hostedControlFakeCITools), 0o755)

	source, err := os.ReadFile(filepath.Join(repoRoot(t), "scripts", "verify-github-hosted-controls.sh"))
	if err != nil {
		t.Fatal(err)
	}
	fixture := string(source)
	fixture = strings.Replace(fixture, `GO_BIN="$(resolve_trusted_go_bin)"`, `GO_BIN=`+ShellQuote(filepath.Join(binDir, "go")), 1)
	fixture = strings.Replace(fixture, `GH_BIN="$(resolve_trusted_tool /opt/homebrew/bin/gh /usr/local/bin/gh /usr/bin/gh)"`, `GH_BIN=`+ShellQuote(filepath.Join(binDir, "gh")), 1)
	fixture = strings.Replace(fixture, `JQ_BIN="$(resolve_trusted_tool /opt/homebrew/bin/jq /usr/local/bin/jq /usr/bin/jq)"`, `JQ_BIN=`+ShellQuote(filepath.Join(binDir, "jq")), 1)
	writeCanonicalFixture(t, filepath.Join(root, "scripts", "verify-github-hosted-controls.sh"), []byte(fixture), 0o755)
}

func hostedControlProbeEnvironment(stdinPath, argsPath, envPath, status string) []string {
	environment := append([]string{}, os.Environ()...)
	environment = append(environment,
		"GITHUB_ACTIONS=true",
		"WORKCELL_HOSTED_CONTROLS_REQUIRED=1",
		"WORKCELL_HOSTED_CONTROLS_TOKEN=dummy-token",
		"GH_TOKEN=wrong-gh-token",
		"GITHUB_TOKEN=wrong-github-token",
		"GH_ENTERPRISE_TOKEN=wrong-enterprise-token",
		"GITHUB_ENTERPRISE_TOKEN=wrong-github-enterprise-token",
		"WORKCELL_GITHUB_API_TOKEN=wrong-api-token",
		"WORKCELL_GITHUB_API_TOKEN_FILE=/wrong/token-file",
		"WORKCELL_TEST_STDIN_PATH="+stdinPath,
		"WORKCELL_TEST_ARGS_PATH="+argsPath,
		"WORKCELL_TEST_ENV_PATH="+envPath,
		"WORKCELL_TEST_CHILD_STATUS="+status,
	)
	return environment
}

func environmentWithout(environment []string, name string) []string {
	prefix := name + "="
	filtered := make([]string, 0, len(environment))
	for _, entry := range environment {
		if !strings.HasPrefix(entry, prefix) {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

func requireHostedControlProbe(t *testing.T, stdinPath, argsPath, envPath, token string) {
	t.Helper()
	requireHostedControlFile(t, stdinPath, token)
	requireHostedControlFile(t, argsPath, "--token-stdin\nowner/repo\n")
	requireHostedControlFile(t, envPath, "clean\n")
}

func requireHostedControlFile(t *testing.T, path, want string) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(content); got != want {
		t.Fatalf("%s = %q, want %q", path, got, want)
	}
}

func requireExitStatus(t *testing.T, err error, want int, output []byte) {
	t.Helper()
	exitError, ok := err.(*exec.ExitError)
	if !ok || exitError.ExitCode() != want {
		t.Fatalf("exit = %v, want %d; output=%q", err, want, output)
	}
}

func copyHostedControlFixture(t *testing.T, source, destination string) {
	t.Helper()
	copyCanonicalFixture(t, filepath.Join(repoRoot(t), filepath.FromSlash(source)), destination)
}

const hostedControlChildProbe = `#!/bin/bash -p
set -euo pipefail
IFS= read -r token
printf '%s\n' "${token}" >"${WORKCELL_TEST_STDIN_PATH}"
printf '%s\n' "$@" >"${WORKCELL_TEST_ARGS_PATH}"
for name in GH_TOKEN GITHUB_TOKEN GH_ENTERPRISE_TOKEN GITHUB_ENTERPRISE_TOKEN WORKCELL_HOSTED_CONTROLS_TOKEN WORKCELL_GITHUB_API_TOKEN WORKCELL_GITHUB_API_TOKEN_FILE HOSTED_TOKEN LOCAL_TOKEN WORKFLOW_TOKEN; do
  if declare -p "${name}" >/dev/null 2>&1; then
    printf 'leaked %s\n' "${name}" >"${WORKCELL_TEST_ENV_PATH}"
    exit 90
  fi
done
printf 'clean\n' >"${WORKCELL_TEST_ENV_PATH}"
exit "${WORKCELL_TEST_CHILD_STATUS}"
`

const hostedControlDirectRelayProbe = `if [[ "${1:-}" == "--token-stdin" ]]; then
  IFS= read -r token
  printf '%s\n' "${token}" >"${WORKCELL_TEST_STDIN_PATH}"
  printf '%s\n' "$@" >"${WORKCELL_TEST_ARGS_PATH}"
  for name in GH_TOKEN GITHUB_TOKEN GH_ENTERPRISE_TOKEN GITHUB_ENTERPRISE_TOKEN WORKCELL_HOSTED_CONTROLS_TOKEN WORKCELL_GITHUB_API_TOKEN WORKCELL_GITHUB_API_TOKEN_FILE; do
    if declare -p "${name}" >/dev/null 2>&1; then
      printf 'leaked %s\n' "${name}" >"${WORKCELL_TEST_ENV_PATH}"
      exit 90
    fi
  done
  printf 'clean\n' >"${WORKCELL_TEST_ENV_PATH}"
  exit "${WORKCELL_TEST_CHILD_STATUS}"
fi`

const hostedControlFakeGo = `#!/bin/bash
set -euo pipefail
for name in GH_TOKEN GITHUB_TOKEN GH_ENTERPRISE_TOKEN GITHUB_ENTERPRISE_TOKEN WORKCELL_HOSTED_CONTROLS_TOKEN WORKCELL_GITHUB_API_TOKEN WORKCELL_GITHUB_API_TOKEN_FILE AUDIT_TOKEN; do
  declare -p "${name}" >/dev/null 2>&1 && exit 90
done
if IFS= read -r -n 1 _; then exit 92; fi
printf 'clean\n' >"${WORKCELL_TEST_LOG_DIR}/go"
while [[ $# -gt 0 ]]; do
  if [[ "$1" == "-o" ]]; then
    /bin/cp "${WORKCELL_TEST_CITOOLS_TEMPLATE}" "$2"
    /bin/chmod 0755 "$2"
    exit 0
  fi
  shift
done
exit 91
`

const hostedControlFakeGH = `#!/bin/bash
set -euo pipefail
aliases=clean
for name in GITHUB_TOKEN GH_ENTERPRISE_TOKEN GITHUB_ENTERPRISE_TOKEN WORKCELL_HOSTED_CONTROLS_TOKEN WORKCELL_GITHUB_API_TOKEN WORKCELL_GITHUB_API_TOKEN_FILE AUDIT_TOKEN; do
  declare -p "${name}" >/dev/null 2>&1 && aliases="leaked-${name}"
done
if IFS= read -r -n 1 _; then exit 92; fi
printf 'token=%s aliases=%s args=%s\n' "${GH_TOKEN:-}" "${aliases}" "$*" >>"${WORKCELL_TEST_LOG_DIR}/gh"
if [[ "$*" == *actions/permissions/selected-actions* ]]; then
  printf '{"status":409}\n'
  exit 1
fi
printf '{}\n'
`

const hostedControlFakeJQ = `#!/bin/bash
set -euo pipefail
for name in GH_TOKEN GITHUB_TOKEN GH_ENTERPRISE_TOKEN GITHUB_ENTERPRISE_TOKEN WORKCELL_HOSTED_CONTROLS_TOKEN WORKCELL_GITHUB_API_TOKEN WORKCELL_GITHUB_API_TOKEN_FILE AUDIT_TOKEN; do
  declare -p "${name}" >/dev/null 2>&1 && exit 90
done
if IFS= read -r -n 1 _; then exit 92; fi
printf 'clean\n' >"${WORKCELL_TEST_LOG_DIR}/jq"
exec @JQ_PATH@ "$@"
`

const hostedControlFakeCITools = `#!/bin/bash
set -euo pipefail
for name in GH_TOKEN GITHUB_TOKEN GH_ENTERPRISE_TOKEN GITHUB_ENTERPRISE_TOKEN WORKCELL_HOSTED_CONTROLS_TOKEN WORKCELL_GITHUB_API_TOKEN WORKCELL_GITHUB_API_TOKEN_FILE AUDIT_TOKEN; do
  declare -p "${name}" >/dev/null 2>&1 && exit 90
done
input="$(/bin/cat)"
[[ "${input}" != *dummy-token* ]] || exit 92
printf 'clean\n' >"${WORKCELL_TEST_LOG_DIR}/citools"
printf '%s\n' "$1" >>"${WORKCELL_TEST_LOG_DIR}/citools-commands"
case "$1" in
  merge-hosted-control-object-pages)
    printf '{"total_count":0,"%s":[]}\n' "$2"
    ;;
  merge-hosted-control-array-pages) printf '[{"id":42}]\n' ;;
  list-hosted-control-ruleset-ids) printf '42\n' ;;
  normalize-hosted-control-ruleset) printf '{"id":42}\n' ;;
  assemble-hosted-control-rulesets) printf '[{"id":42}]\n' >"$4" ;;
  list-hosted-control-environments) printf 'release\n' ;;
  verify-github-hosted-controls) : ;;
  *) exit 93 ;;
esac
`
