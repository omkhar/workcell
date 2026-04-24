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

			code, output := runBashProbe(t, `set -euo pipefail
source() {
  if [[ "$1" == "/usr/local/libexec/workcell/assurance.sh" ]]; then
    return 0
  fi
  builtin source "$@"
}
builtin source "`+scriptPath+`"
if workcell_target_is_allowed "`+target+`"; then
  printf 'allowed\n'
else
  printf 'blocked\n'
fi
`, nil)
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
	code, output := runBashProbe(t, `set -euo pipefail
ROOT_DIR="`+workspace+`"
source "`+scriptPath+`"
resolve_workcell_real_home
`, map[string]string{
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

	goHostutilIndex := strings.Index(script, `go_hostutil() {`)
	if goHostutilIndex == -1 {
		t.Fatalf("%s must define go_hostutil", scriptPath)
	}
	if realHomeIndex > goHostutilIndex {
		t.Fatalf("%s resolves REAL_HOME after go_hostutil is defined; want bootstrap home resolved first", scriptPath)
	}
	if cacheRootIndex > goHostutilIndex {
		t.Fatalf("%s exports WORKCELL_GO_CACHE_ROOT after go_hostutil is defined; want cache root fixed first", scriptPath)
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
