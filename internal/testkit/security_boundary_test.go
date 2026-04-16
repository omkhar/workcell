// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package testkit

import (
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
