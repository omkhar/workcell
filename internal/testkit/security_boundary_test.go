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

func runBashProbe(tb testing.TB, script string, env map[string]string) (int, string) {
	tb.Helper()

	cmd := exec.Command("bash", "-p", "-lc", script)
	cmd.Env = append(os.Environ(), "BASH_ENV=", "ENV=")
	for key, value := range env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	output, err := cmd.CombinedOutput()
	if err == nil {
		return 0, string(output)
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode(), string(output)
	}
	tb.Fatalf("bash probe failed: %v", err)
	return 0, ""
}

func TestHomeControlPlaneIgnoresManifestEnvOverride(t *testing.T) {
	t.Parallel()

	scriptPath := filepath.Join(repoRoot(t), "runtime", "container", "home-control-plane.sh")
	code, output := runBashProbe(t, `set -euo pipefail
source() {
  if [[ "$1" == "/usr/local/libexec/workcell/assurance.sh" ]]; then
    return 0
  fi
  builtin source "$@"
}
builtin source "`+scriptPath+`"
printf '%s\n' "$(workcell_control_plane_manifest_path)"
`, map[string]string{
		"WORKCELL_CONTROL_PLANE_MANIFEST": "/tmp/workcell-attacker-manifest.json",
	})
	if code != 0 {
		t.Fatalf("probe exit code = %d output=%q", code, output)
	}
	if strings.TrimSpace(output) != "/usr/local/libexec/workcell/control-plane-manifest.json" {
		t.Fatalf("control-plane manifest path = %q", strings.TrimSpace(output))
	}
}

func TestHomeControlPlaneRejectsManagedDirectoryTargets(t *testing.T) {
	t.Parallel()

	scriptPath := filepath.Join(repoRoot(t), "runtime", "container", "home-control-plane.sh")
	for _, target := range []string{
		"/state/agent-home/.claude",
		"/state/agent-home/.codex",
		"/state/agent-home/.gemini",
		"/state/agent-home/.config/gh",
		"/state/agent-home/.config/gcloud",
		"/state/agent-home/.config/claude-code",
	} {
		target := target
		t.Run(filepath.Base(target), func(t *testing.T) {
			t.Parallel()

			probe := fmt.Sprintf(
				"set -euo pipefail\n"+
					"source() {\n"+
					"  if [[ \"$1\" == \"/usr/local/libexec/workcell/assurance.sh\" ]]; then\n"+
					"    return 0\n"+
					"  fi\n"+
					"  builtin source \"$@\"\n"+
					"}\n"+
					"builtin source %q\n"+
					"if workcell_target_is_allowed %q; then\n"+
					"  printf 'allowed\\n'\n"+
					"else\n"+
					"  printf 'blocked\\n'\n"+
					"fi\n",
				scriptPath,
				target,
			)
			code, output := runBashProbe(t, probe, nil)
			if code != 0 {
				t.Fatalf("probe exit code = %d output=%q", code, output)
			}
			if strings.TrimSpace(output) != "blocked" {
				t.Fatalf("target %s unexpectedly allowed: %q", target, output)
			}
		})
	}
}

func TestHomeControlPlaneRejectsTraversalTargets(t *testing.T) {
	t.Parallel()

	scriptPath := filepath.Join(repoRoot(t), "runtime", "container", "home-control-plane.sh")
	for _, target := range []string{
		"/state/injected/../../state/agent-home/.codex/config.toml",
		"/state/agent-home/../agent-home/.claude.json",
		"/state/injected/./copy.txt",
	} {
		target := target
		t.Run(strings.ReplaceAll(target, "/", "_"), func(t *testing.T) {
			t.Parallel()

			probe := fmt.Sprintf(
				"set -euo pipefail\n"+
					"source() {\n"+
					"  if [[ \"$1\" == \"/usr/local/libexec/workcell/assurance.sh\" ]]; then\n"+
					"    return 0\n"+
					"  fi\n"+
					"  builtin source \"$@\"\n"+
					"}\n"+
					"builtin source %q\n"+
					"if workcell_target_is_allowed %q; then\n"+
					"  printf 'allowed\\n'\n"+
					"else\n"+
					"  printf 'blocked\\n'\n"+
					"fi\n",
				scriptPath,
				target,
			)
			code, output := runBashProbe(t, probe, nil)
			if code != 0 {
				t.Fatalf("probe exit code = %d output=%q", code, output)
			}
			if strings.TrimSpace(output) != "blocked" {
				t.Fatalf("target %s unexpectedly allowed: %q", target, output)
			}
		})
	}
}
func TestResolveWorkcellRealHomeRejectsWorkspaceOverride(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	overrideHome := filepath.Join(workspace, "attacker-home")
	if err := os.MkdirAll(overrideHome, 0o755); err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(repoRoot(t), "scripts", "lib", "trusted-docker-client.sh")
	probe := fmt.Sprintf("set -euo pipefail\nROOT_DIR=%q\nsource %q\nresolve_workcell_real_home\n", workspace, scriptPath)
	code, output := runBashProbe(t, probe, map[string]string{
		"HOME":                      t.TempDir(),
		"WORKCELL_DOCKER_REAL_HOME": overrideHome,
	})
	if code == 0 {
		t.Fatalf("resolve_workcell_real_home unexpectedly accepted workspace override: %q", output)
	}
}

func TestWorkcellBootstrapResolvesRealHomeBeforeGoHostutil(t *testing.T) {
	t.Parallel()

	scriptPath := filepath.Join(repoRoot(t), "scripts", "workcell")
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	script := string(content)

	if strings.Contains(script, `REAL_HOME="$(go_hostutil path home)"`) {
		t.Fatalf("%s must not resolve REAL_HOME through go_hostutil during early bootstrap", scriptPath)
	}

	realHomeIndex := strings.Index(script, `REAL_HOME="$(resolve_workcell_real_home)"`)
	if realHomeIndex == -1 {
		t.Fatalf("%s must resolve REAL_HOME via resolve_workcell_real_home", scriptPath)
	}

	cacheRootIndex := strings.Index(script, `export WORKCELL_GO_CACHE_ROOT=`)
	if cacheRootIndex == -1 {
		t.Fatalf("%s must export WORKCELL_GO_CACHE_ROOT before early go_hostutil use", scriptPath)
	}
	if cacheRootIndex < realHomeIndex {
		t.Fatalf("%s exports WORKCELL_GO_CACHE_ROOT before REAL_HOME is resolved", scriptPath)
	}

	// go_hostutil now lives in scripts/lib/launcher/go-hostutil.sh, so it
	// becomes defined at the point scripts/workcell sources that module. The
	// ordering invariant therefore targets the source line: the module must be
	// sourced only after REAL_HOME is resolved and WORKCELL_GO_CACHE_ROOT is
	// exported, so the go_hostutil/go_colimautil wrappers can never run with an
	// unsanitised host home or Go cache root.
	goHostutilSourceIndex := strings.Index(script, `source "${ROOT_DIR}/scripts/lib/launcher/go-hostutil.sh"`)
	if goHostutilSourceIndex == -1 {
		t.Fatalf("%s must source scripts/lib/launcher/go-hostutil.sh", scriptPath)
	}
	if realHomeIndex > goHostutilSourceIndex {
		t.Fatalf("%s resolves REAL_HOME after go-hostutil.sh is sourced; want bootstrap home resolved first", scriptPath)
	}
	if cacheRootIndex > goHostutilSourceIndex {
		t.Fatalf("%s exports WORKCELL_GO_CACHE_ROOT after go-hostutil.sh is sourced; want cache root fixed first", scriptPath)
	}

	modulePath := filepath.Join(repoRoot(t), "scripts", "lib", "launcher", "go-hostutil.sh")
	moduleContent, err := os.ReadFile(modulePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(moduleContent), `go_hostutil() {`) {
		t.Fatalf("%s must define go_hostutil", modulePath)
	}
}

func TestWorkcellRejectsHarnessOnlyCredentialResolverEnvBeforeBundlePreparation(t *testing.T) {
	t.Parallel()

	scriptPath := filepath.Join(repoRoot(t), "scripts", "workcell")
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	script := string(content)
	prepareIndex := strings.LastIndex(script, `prepare_injection_bundle "${AGENT}" "${MODE}"`)
	if prepareIndex == -1 {
		t.Fatalf("%s must prepare the injection bundle through prepare_injection_bundle", scriptPath)
	}
	for _, name := range []string{
		"WORKCELL_TEST_CODEX_AUTH_FILE",
		"WORKCELL_TEST_CLAUDE_KEYCHAIN_EXPORT_FILE",
	} {
		checkIndex := strings.Index(script, `if [[ -n "${`+name+`:-}" ]]; then`)
		if checkIndex == -1 {
			t.Fatalf("%s must reject harness-only env var %s", scriptPath, name)
		}
		if checkIndex > prepareIndex {
			t.Fatalf("%s must reject harness-only env var %s before preparing the injection bundle", scriptPath, name)
		}
		if strings.Contains(script, name+"=${"+name+"}") {
			t.Fatalf("%s must not forward caller-controlled %s into host credential resolution", scriptPath, name)
		}
	}
}

func TestWorkcellResolvesLaunchWorkspaceBeforeBundlePreparation(t *testing.T) {
	t.Parallel()

	scriptPath := filepath.Join(repoRoot(t), "scripts", "workcell")
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	script := string(content)
	prepareIndex := strings.LastIndex(script, `prepare_injection_bundle "${AGENT}" "${MODE}"`)
	if prepareIndex == -1 {
		t.Fatalf("%s must prepare the injection bundle through prepare_injection_bundle", scriptPath)
	}
	resolveIndex := strings.LastIndex(script[:prepareIndex], "\nresolve_launch_workspace\n")
	if resolveIndex == -1 {
		t.Fatalf("%s must resolve the launch workspace before preparing the injection bundle", scriptPath)
	}
	for _, expected := range []string{
		`[[ "${AUTH_STATUS}" -eq 0 ]]`,
		`[[ "${DOCTOR}" -eq 0 ]]`,
		`[[ "${INSPECT}" -eq 0 ]]`,
		`[[ "${GC}" -eq 0 ]]`,
		`[[ -z "${LOGS_KIND}" ]]`,
	} {
		if !strings.Contains(script, expected) {
			t.Fatalf("%s launch workspace resolver must preserve diagnostic exception %s", scriptPath, expected)
		}
	}
}

func TestWorkcellRejectsSensitiveWorkspaceBeforeExistenceCheck(t *testing.T) {
	t.Parallel()

	scriptPath := filepath.Join(repoRoot(t), "scripts", "workcell")
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	script := string(content)
	rejectIndex := strings.Index(script, `reject_broad_or_sensitive_workspace "${resolved_path}"`)
	if rejectIndex == -1 {
		t.Fatalf("%s must reject broad/sensitive resolved workspaces before existence checks", scriptPath)
	}
	existenceIndex := strings.Index(script, `if [[ ! -e "${resolved_path}" ]]; then`)
	if existenceIndex == -1 {
		t.Fatalf("%s must check resolved workspace existence", scriptPath)
	}
	if rejectIndex > existenceIndex {
		t.Fatalf("%s must reject broad/sensitive workspaces before missing-path errors", scriptPath)
	}
	if !strings.Contains(script, `reject_broad_or_sensitive_workspace "${path}"`) {
		t.Fatalf("%s validate_workspace must share the broad/sensitive rejection helper", scriptPath)
	}
}

func TestWorkcellForwardsColimaStartTimeoutThroughDetachedSessionHandoff(t *testing.T) {
	t.Parallel()

	scriptPath := filepath.Join(repoRoot(t), "scripts", "workcell")
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	script := string(content)
	for _, expected := range []string{
		`WORKCELL_COLIMA_START_TIMEOUT_SECONDS`,
		`monitor_env+=("WORKCELL_COLIMA_START_TIMEOUT_SECONDS=${WORKCELL_COLIMA_START_TIMEOUT_SECONDS}")`,
		`session_start_env+=("WORKCELL_COLIMA_START_TIMEOUT_SECONDS=${WORKCELL_COLIMA_START_TIMEOUT_SECONDS}")`,
	} {
		if !strings.Contains(script, expected) {
			t.Fatalf("%s must preserve detached-session Colima start timeout setting %q", scriptPath, expected)
		}
	}
}

func TestWorkcellShadowsCopilotRepoHookControlPlane(t *testing.T) {
	t.Parallel()

	scriptPath := filepath.Join(repoRoot(t), "scripts", "workcell")
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	script := string(content)
	sharedDirList := `for rel in .github/instructions .github/copilot .github/hooks .github/agents .github/skills .agents/skills; do`
	for _, expected := range []string{
		`.github/hooks/*)`,
		`printf '%s\0' ".github/hooks"`,
		`*/.github/hooks/*)`,
		`"${path%%/.github/hooks/*}/.github/hooks"`,
		sharedDirList,
	} {
		if !strings.Contains(script, expected) {
			t.Fatalf("%s must include Copilot repo hook control-plane masking entry %q", scriptPath, expected)
		}
	}
	if strings.Count(script, sharedDirList) != 2 {
		t.Fatalf("%s must use the Copilot repo hook control-plane list for both readonly VCS and safe-path shadowing", scriptPath)
	}
}

func TestEnsureGoRunEnvFallsBackToPerUserCacheWithoutHome(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	scriptPath := filepath.Join(repoRoot(t), "scripts", "lib", "go-run-env.sh")
	expected := filepath.Join(tempDir, fmt.Sprintf("workcell-go-%d", os.Getuid()))

	code, output := runBashProbe(t, `set -euo pipefail
unset HOME XDG_CACHE_HOME WORKCELL_GO_CACHE_ROOT
source "`+scriptPath+`"
ensure_go_run_env
printf '%s\n' "${WORKCELL_GO_CACHE_ROOT}"
`, map[string]string{
		"HOME":                   "",
		"TMPDIR":                 tempDir,
		"XDG_CACHE_HOME":         "",
		"WORKCELL_GO_CACHE_ROOT": "",
	})
	if code != 0 {
		t.Fatalf("probe exit code = %d output=%q", code, output)
	}
	if strings.TrimSpace(output) != expected {
		t.Fatalf("WORKCELL_GO_CACHE_ROOT = %q, want %q", strings.TrimSpace(output), expected)
	}
}

func TestCheckPRShapeIgnoresAmbientGitConfig(t *testing.T) {
	t.Parallel()

	repo := t.TempDir()
	origin := filepath.Join(t.TempDir(), "origin.git")
	if output, err := exec.Command("git", "init", "--bare", origin).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v output=%q", err, string(output))
	}
	if output, err := exec.Command("git", "init", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v output=%q", err, string(output))
	}
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v output=%q", args, err, string(output))
		}
	}

	runGit("config", "user.name", "Workcell Test")
	runGit("config", "user.email", "workcell-test@example.com")
	runGit("remote", "add", "origin", origin)
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "tracked.txt")
	runGit("commit", "-m", "init")
	runGit("branch", "-M", "main")
	runGit("push", "-u", "origin", "main")
	runGit("switch", "-c", "feature/pr-shape-safe")
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("commit", "-am", "feature change")

	maliciousHome := t.TempDir()
	maliciousMarker := filepath.Join(t.TempDir(), "diff.marker")
	maliciousDiff := filepath.Join(t.TempDir(), "malicious-diff.sh")
	if err := os.WriteFile(maliciousDiff, []byte("#!/bin/sh\nprintf 'unexpected diff.external invocation\\n' >\""+maliciousMarker+"\"\nexit 99\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(maliciousHome, ".gitconfig"), []byte("[diff]\n\texternal = "+maliciousDiff+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	scriptPath := filepath.Join(repoRoot(t), "scripts", "check-pr-shape.sh")
	code, output := runBashProbe(t, `set -euo pipefail
"`+scriptPath+`" --repo-root "`+repo+`" --base-ref refs/remotes/origin/main --head-ref HEAD --max-files 25 --max-lines 1200 --max-areas 8 --max-binaries 0
`, map[string]string{
		"HOME": maliciousHome,
	})
	if code != 0 {
		t.Fatalf("probe exit code = %d output=%q", code, output)
	}
	if strings.Contains(output, "failed") {
		t.Fatalf("check-pr-shape reported failure under ambient git config: %q", output)
	}
	if _, err := os.Stat(maliciousMarker); !os.IsNotExist(err) {
		t.Fatalf("malicious diff.external hook unexpectedly ran; marker error=%v", err)
	}
}

func TestCheckPRShapeCountsRenameDestinationArea(t *testing.T) {
	t.Parallel()

	repo := t.TempDir()
	origin := filepath.Join(t.TempDir(), "origin.git")
	if output, err := exec.Command("git", "init", "--bare", origin).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v output=%q", err, string(output))
	}
	if output, err := exec.Command("git", "init", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v output=%q", err, string(output))
	}
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v output=%q", args, err, string(output))
		}
	}

	runGit("config", "user.name", "Workcell Test")
	runGit("config", "user.email", "workcell-test@example.com")
	runGit("remote", "add", "origin", origin)
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "tracked.txt")
	runGit("commit", "-m", "init")
	runGit("branch", "-M", "main")
	runGit("push", "-u", "origin", "main")
	runGit("switch", "-c", "feature/rename-area")
	if err := os.MkdirAll(filepath.Join(repo, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	runGit("mv", "tracked.txt", "docs/renamed.txt")
	runGit("commit", "-m", "rename into docs")

	scriptPath := filepath.Join(repoRoot(t), "scripts", "check-pr-shape.sh")
	code, output := runBashProbe(t, `set -euo pipefail
"`+scriptPath+`" --repo-root "`+repo+`" --base-ref refs/remotes/origin/main --head-ref HEAD --max-files 25 --max-lines 1200 --max-areas 1 --max-binaries 0
`, nil)
	if code == 0 {
		t.Fatalf("check-pr-shape unexpectedly accepted a rename into a second top-level area: %q", output)
	}
	if !strings.Contains(output, "changed_areas=2") {
		t.Fatalf("check-pr-shape did not count both rename areas: %q", output)
	}
}
