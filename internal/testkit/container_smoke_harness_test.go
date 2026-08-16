// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package testkit

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const containerSmokeInputCommand = "bash -c 'exec 3<&0; exec </dev/null; source /dev/fd/3' <<'SCRIPT'"

type containerSmokeProvider struct {
	name  string
	label string
}

var containerSmokeProviders = []containerSmokeProvider{
	{name: "codex", label: "Codex"},
	{name: "claude", label: "Claude"},
	{name: "gemini", label: "Gemini"},
}

func containerSmokeFailureTrap(label string) string {
	return "trap 'failure_status=$?; if [[ $- == *e* ]]; then trap - ERR; echo \"" + label + " smoke block failed at input line ${LINENO}.\" >&2; exit \"${failure_status}\"; fi' ERR"
}

func containerSmokeProviderBody(source string, provider containerSmokeProvider) (string, error) {
	marker := "run_container_stdin " + provider.name + " " + containerSmokeInputCommand
	if count := strings.Count(source, marker); count != 1 {
		return "", fmt.Errorf("%s direct-stdin block count = %d, want 1", provider.name, count)
	}

	remainder := strings.SplitN(source, marker, 2)[1]
	remainder = strings.TrimPrefix(remainder, "\n")
	end := strings.Index(remainder, "\nSCRIPT\n")
	if end < 0 {
		return "", fmt.Errorf("%s direct-stdin block has no closing SCRIPT delimiter", provider.name)
	}
	return remainder[:end] + "\n", nil
}

func containerSmokeLiteralHeredoc(source string, delimiter string) (string, error) {
	opener := "<<'" + delimiter + "'\n"
	if count := strings.Count(source, opener); count != 1 {
		return "", fmt.Errorf("%s heredoc opener count = %d, want 1", delimiter, count)
	}

	remainder := strings.SplitN(source, opener, 2)[1]
	end := strings.Index(remainder, "\n"+delimiter+"\n")
	if end < 0 {
		return "", fmt.Errorf("%s heredoc has no closing delimiter", delimiter)
	}
	return remainder[:end] + "\n", nil
}

func containerSmokeBashSyntax(body string) error {
	return containerSmokeBashSyntaxWithPath("/bin/bash", body)
}

func containerSmokeBashSyntaxWithPath(bashPath string, body string) error {
	cmd := exec.Command(bashPath, "--noprofile", "--norc", "-n")
	cmd.Stdin = strings.NewReader(body)
	output, err := cmd.CombinedOutput()
	return containerSmokeBashParseResult(err, output)
}

func containerSmokeBashParseResult(err error, output []byte) error {
	if err != nil {
		return fmt.Errorf("bash -n failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	if warning := strings.TrimSpace(string(output)); warning != "" {
		return fmt.Errorf("bash -n emitted parser output: %s", warning)
	}
	return nil
}

func validateContainerSmokeHarness(source string) error {
	var problems []string
	require := func(scope string, source string, fragments ...string) {
		for _, fragment := range fragments {
			if !strings.Contains(source, fragment) {
				problems = append(problems, scope+" is missing "+fmt.Sprintf("%q", fragment))
			}
		}
	}

	for _, provider := range containerSmokeProviders {
		legacy := "run_container " + provider.name + " bash -lc \"$("
		if strings.Contains(source, legacy) {
			problems = append(problems, provider.name+" still generates its smoke block through command substitution")
		}

		body, err := containerSmokeProviderBody(source, provider)
		if err != nil {
			problems = append(problems, err.Error())
			continue
		}
		require(provider.name, body,
			"set -Eeuo pipefail",
			containerSmokeFailureTrap(provider.label),
		)
		if err := containerSmokeBashSyntax(body); err != nil {
			problems = append(problems, provider.name+" block "+err.Error())
		}
	}

	codex, err := containerSmokeProviderBody(source, containerSmokeProviders[0])
	if err == nil {
		require("codex", codex,
			`codex_user_script=/run/workcell/container-smoke-codex-user.sh`,
			"cat >\"${codex_user_script}\" <<'CODEX_USER_SCRIPT'\n    set -Eeuo pipefail",
			`trap 'failure_status=$?; if [[ $- == *e* ]]; then trap - ERR; echo "Codex user smoke block failed at input line ${LINENO}." >&2; exit "${failure_status}"; fi' ERR`,
			"\nCODEX_USER_SCRIPT\n  chmod 0444",
			`chmod 0444 "${codex_user_script}"`,
			"setpriv --reuid \"$WORKCELL_HOST_UID\" --regid \"$WORKCELL_HOST_GID\" --init-groups \\\n    bash -c 'source \"$1\"' workcell-codex-user-smoke \"${codex_user_script}\" </dev/null",
		)
		staged, stageErr := containerSmokeLiteralHeredoc(codex, "CODEX_USER_SCRIPT")
		if stageErr != nil {
			problems = append(problems, stageErr.Error())
		} else {
			require("staged mapped-user codex", staged,
				`test "$(id -u)" = "$WORKCELL_HOST_UID"`,
				`test "$(id -u)" != 0`,
				`raw_execveat_number=322`,
				`raw_execveat_number=281`,
				`raw_execveat_empty_path_flag=4096`,
				`$n=0+shift`,
				`$flags=0+shift`,
				`syscall($n, $fd, $empty, $zero, $env, $flags)`,
				`"$raw_execveat_number" "$EXEC_TMP/workcell-raw-execveat-script" "$raw_execveat_empty_path_flag"`,
				`grep -qx "raw-execveat-fd-script-allowed" /tmp/workcell-raw-execveat-fd.out`,
			)
			if count := strings.Count(staged, "raw-execveat-fd-script-allowed"); count != 2 {
				problems = append(problems, fmt.Sprintf("staged mapped-user codex exact raw execveat marker count = %d, want 2", count))
			}
			if syntaxErr := containerSmokeBashSyntax(staged); syntaxErr != nil {
				problems = append(problems, "staged codex user block "+syntaxErr.Error())
			}
		}
	}

	for _, provider := range containerSmokeProviders[1:] {
		body, bodyErr := containerSmokeProviderBody(source, provider)
		if bodyErr != nil {
			continue
		}
		require(provider.name, body,
			`run_as_runtime_user() {`,
			`setpriv --reuid "$WORKCELL_HOST_UID" --regid "$WORKCELL_HOST_GID" --init-groups "$@"`,
			`runtime_uid="$(run_as_runtime_user id -u)"`,
			`test "${runtime_uid}" = "$WORKCELL_HOST_UID"`,
			`test "${runtime_uid}" != 0`,
		)
		expectedCalls := map[string]int{"claude": 6, "gemini": 9}[provider.name]
		if count := strings.Count(body, "run_as_runtime_user "+provider.name); count != expectedCalls {
			problems = append(problems, fmt.Sprintf("%s mapped provider policy call count = %d, want %d", provider.name, count, expectedCalls))
		}
		if provider.name == "claude" {
			require("claude writable glob fixture", body,
				`claude_glob_fixture="$(mktemp -d /tmp/workcell-claude-hook-glob.XXXXXX)"`,
				`trap 'rm -rf -- "${claude_glob_fixture}"' EXIT`,
				`test -O "${claude_glob_fixture}"`,
				`test -w "${claude_glob_fixture}"`,
				`touch "${claude_glob_fixture}/claude"`,
				`claude_glob_previous_dir="${PWD}"`,
				"\n  cd \"${claude_glob_fixture}\"\n",
				"\n  cd \"${claude_glob_previous_dir}\"\n",
			)
			if strings.Contains(body, "touch ./claude") {
				problems = append(problems, "claude glob fixture still depends on a writable workspace")
			}
		}
	}

	if len(problems) != 0 {
		return fmt.Errorf("container smoke harness validation failed:\n- %s", strings.Join(problems, "\n- "))
	}
	return nil
}

func readContainerSmokeHarness(tb testing.TB) string {
	tb.Helper()
	path := filepath.Join(repoRoot(tb), "scripts", "container-smoke.sh")
	content, err := os.ReadFile(path)
	if err != nil {
		tb.Fatal(err)
	}
	return string(content)
}

func mutateContainerSmokeHarness(tb testing.TB, source string, old string, replacement string) string {
	tb.Helper()
	if count := strings.Count(source, old); count != 1 {
		tb.Fatalf("container smoke mutation anchor count = %d, want 1: %q", count, old)
	}
	return strings.Replace(source, old, replacement, 1)
}

func TestContainerSmokeHarness(t *testing.T) {
	t.Parallel()
	if err := validateContainerSmokeHarness(readContainerSmokeHarness(t)); err != nil {
		t.Fatal(err)
	}
}

func TestContainerSmokeBashSyntaxRejectsStatusZeroParserWarnings(t *testing.T) {
	t.Parallel()
	parserPath := filepath.Join(t.TempDir(), "bash")
	parser := []byte("#!/bin/sh\ncat >/dev/null\nprintf '%s\\n' 'warning: here-document delimited by end-of-file' >&2\n")
	if err := os.WriteFile(parserPath, parser, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := containerSmokeBashSyntaxWithPath(parserPath, "cat <<'EOF'\n"); err == nil {
		t.Fatal("bash parser warning passed validation with a successful status")
	}
}

func TestContainerSmokeFailureTrapHonorsErrexitState(t *testing.T) {
	trap := containerSmokeFailureTrap("Probe")
	script := strings.Join([]string{
		"set -Eeuo pipefail",
		trap,
		"set +e",
		"/bin/bash -c 'exit 126'",
		"status=$?",
		"set -e",
		`test "${status}" -eq 126`,
		`printf 'expected-status=%s\n' "${status}"`,
		"/bin/bash -c 'exit 7'",
	}, "\n")
	cmd := exec.Command("/bin/bash", "--noprofile", "--norc", "-c", "exec 3<&0; exec </dev/null; source /dev/fd/3")
	cmd.Stdin = strings.NewReader(script)
	output, err := cmd.CombinedOutput()
	exitErr, ok := err.(*exec.ExitError)
	if !ok || exitErr.ExitCode() != 7 {
		t.Fatalf("failure sequence exit = %v, want status 7: %s", err, output)
	}
	if !strings.Contains(string(output), "expected-status=126\n") {
		t.Fatalf("expected failure status was not preserved: %s", output)
	}
	if !strings.Contains(string(output), "Probe smoke block failed at input line ") {
		t.Fatalf("unexpected failure did not report its input line: %s", output)
	}
}

func TestContainerSmokeHarnessRejectsNonWritableClaudeWorkspaceDependency(t *testing.T) {
	t.Parallel()
	source := readContainerSmokeHarness(t)
	mutated := mutateContainerSmokeHarness(t, source,
		"  touch \"${claude_glob_fixture}/claude\"\n  claude_glob_previous_dir=\"${PWD}\"\n  cd \"${claude_glob_fixture}\"",
		"  touch ./claude\n  claude_glob_previous_dir=\"${PWD}\"\n  :",
	)
	err := validateContainerSmokeHarness(mutated)
	if err == nil {
		t.Fatal("non-writable Claude workspace dependency passed validation")
	}
	if !strings.Contains(err.Error(), "claude writable glob fixture") ||
		!strings.Contains(err.Error(), "writable workspace") {
		t.Fatalf("non-writable workspace mutation error = %q, want fixture and workspace diagnostics", err)
	}
}

func TestContainerSmokeHarnessRejectsMutations(t *testing.T) {
	t.Parallel()
	source := readContainerSmokeHarness(t)
	cases := []struct {
		name        string
		anchor      string
		replacement string
	}{
		{
			name:        "generated-command-substitution",
			anchor:      "run_container_stdin codex " + containerSmokeInputCommand,
			replacement: `run_container codex bash -lc "$(cat <<'SCRIPT'`,
		},
		{
			name:        "stdin-descriptor-not-preserved",
			anchor:      "run_container_stdin claude " + containerSmokeInputCommand,
			replacement: "run_container_stdin claude bash -c 'exec </dev/null; source /dev/fd/3' <<'SCRIPT'",
		},
		{
			name:        "provider-stdin-not-closed",
			anchor:      "run_container_stdin gemini " + containerSmokeInputCommand,
			replacement: "run_container_stdin gemini bash -c 'exec 3<&0; source /dev/fd/3' <<'SCRIPT'",
		},
		{
			name:        "failure-trap-removed",
			anchor:      "  " + containerSmokeFailureTrap("Gemini") + "\n",
			replacement: "",
		},
		{
			name:        "failure-trap-errexit-guard-removed",
			anchor:      "  " + containerSmokeFailureTrap("Codex") + "\n",
			replacement: `  trap 'failure_status=$?; trap - ERR; echo "Codex smoke block failed at input line ${LINENO}." >&2; exit "${failure_status}"' ERR` + "\n",
		},
		{
			name:        "failure-status-not-preserved",
			anchor:      `exit "${failure_status}"; fi' ERR` + "\n  /usr/local/bin/workcell-entrypoint claude",
			replacement: `true; fi' ERR` + "\n  /usr/local/bin/workcell-entrypoint claude",
		},
		{
			name:        "late-syntax-error",
			anchor:      `    codex features -- list >/tmp/codex-features-dd-list-permit.out 2>&1 || true`,
			replacement: "    if then",
		},
		{
			name:        "unterminated-staged-heredoc-warning",
			anchor:      "CODEX_USER_SCRIPT\n  chmod 0444",
			replacement: "CODEX_USER_SCRIP\n  chmod 0444",
		},
		{
			name:        "mapped-user-invariant-removed",
			anchor:      "  runtime_uid=\"$(run_as_runtime_user id -u)\"\n  test \"${runtime_uid}\" = \"$WORKCELL_HOST_UID\"\n  test \"${runtime_uid}\" != 0\n  if run_as_runtime_user claude",
			replacement: "  if run_as_runtime_user claude",
		},
		{
			name:        "codex-setpriv-mapping-changed",
			anchor:      `setpriv --reuid "$WORKCELL_HOST_UID" --regid "$WORKCELL_HOST_GID" --init-groups \` + "\n    bash -c 'source \"$1\"' workcell-codex-user-smoke",
			replacement: `setpriv --reuid 65534 --regid "$WORKCELL_HOST_GID" --init-groups \` + "\n    bash -c 'source \"$1\"' workcell-codex-user-smoke",
		},
		{
			name:        "execveat-mapped-uid-equality-removed",
			anchor:      `    test "$(id -u)" = "$WORKCELL_HOST_UID"` + "\n",
			replacement: "",
		},
		{
			name:        "execveat-mapped-user-invariant-removed",
			anchor:      `    test "$(id -u)" != 0` + "\n    test \"$WORKCELL_RUNTIME\" = \"1\"",
			replacement: `    test "$WORKCELL_RUNTIME" = "1"`,
		},
		{
			name:        "provider-policy-command-runs-as-root",
			anchor:      `if run_as_runtime_user claude update`,
			replacement: `if claude update`,
		},
		{
			name:        "claude-glob-fixture-cleanup-trap-removed",
			anchor:      `  trap 'rm -rf -- "${claude_glob_fixture}"' EXIT` + "\n",
			replacement: "",
		},
		{
			name:        "claude-glob-fixture-cd-check-bypassed",
			anchor:      `  cd "${claude_glob_fixture}"` + "\n",
			replacement: `  cd "${claude_glob_fixture}" || true` + "\n",
		},
		{
			name:        "execveat-flags-left-as-string",
			anchor:      `$flags=0+shift`,
			replacement: `$flags=shift`,
		},
		{
			name:        "execveat-syscall-removed",
			anchor:      `syscall($n, $fd, $empty, $zero, $env, $flags)`,
			replacement: `$zero`,
		},
		{
			name:        "execveat-empty-path-flag-not-passed",
			anchor:      `"$raw_execveat_number" "$EXEC_TMP/workcell-raw-execveat-script" "$raw_execveat_empty_path_flag"`,
			replacement: `"$raw_execveat_number" "$EXEC_TMP/workcell-raw-execveat-script" 0`,
		},
		{
			name:        "exact-success-marker-removed",
			anchor:      `printf 'raw-execveat-fd-script-allowed\n'`,
			replacement: `printf 'raw-execveat-fd-script-ran\n'`,
		},
		{
			name:        "generic-einval-accepted",
			anchor:      `grep -qx "raw-execveat-fd-script-allowed" /tmp/workcell-raw-execveat-fd.out`,
			replacement: `grep -Eq "raw-execveat-fd-script-allowed|Invalid argument" /tmp/workcell-raw-execveat-fd.out`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			mutated := mutateContainerSmokeHarness(t, source, tc.anchor, tc.replacement)
			if err := validateContainerSmokeHarness(mutated); err == nil {
				t.Fatal("container smoke harness mutation passed validation")
			}
		})
	}
}
