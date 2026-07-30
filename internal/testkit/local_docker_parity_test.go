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

func TestValidatorRunnersRequireWorkspaceMountPreflight(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	for _, tc := range []struct {
		rel       string
		workspace string
	}{
		{rel: "scripts/ci/job-validate.sh", workspace: "ROOT_DIR"},
		{rel: "scripts/ci/run-docs-in-validator.sh", workspace: "WORKSPACE"},
		{rel: "scripts/ci/run-fuzz-in-validator.sh", workspace: "WORKSPACE"},
		{rel: "scripts/ci/run-mutation-in-validator.sh", workspace: "WORKSPACE"},
		{rel: "scripts/ci/run-validate-in-validator.sh", workspace: "WORKSPACE"},
	} {
		tc := tc
		t.Run(tc.rel, func(t *testing.T) {
			t.Parallel()

			content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(tc.rel)))
			if err != nil {
				t.Fatal(err)
			}
			call := `require_workcell_ci_workspace_mount "${VALIDATOR_IMAGE}" "${` + tc.workspace + `}"`
			if strings.Count(string(content), call) != 1 {
				t.Fatalf("%s must invoke the shared workspace mount preflight exactly once", tc.rel)
			}
			setup := strings.Index(string(content), "setup_workcell_ci_docker")
			preflight := strings.Index(string(content), call)
			workload := strings.Index(string(content)[preflight+len(call):], "workcell_ci_docker run --rm")
			if setup < 0 || preflight < setup || workload < 0 {
				t.Fatalf("%s must order Docker setup, workspace preflight, then validator workload", tc.rel)
			}
			mount := `--mount "type=bind,src=${` + tc.workspace + `},dst=/workspace"`
			if !strings.Contains(string(content)[preflight+len(call):], mount) {
				t.Fatalf("%s must use the same canonical bind mechanism for its validator workload", tc.rel)
			}
			if tc.workspace == "ROOT_DIR" &&
				!strings.Contains(string(content), `ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"`) {
				t.Fatalf("%s must canonicalize ROOT_DIR before both preflight and workload binds", tc.rel)
			}
		})
	}
}

func TestRequireWorkcellCIWorkspaceMount(t *testing.T) {
	t.Parallel()

	workspace := createValidatorWorkspaceFixture(t)
	staleWorkspace := createValidatorWorkspaceFixture(t)
	visibleMount := filepath.Join(t.TempDir(), "visible")
	staleMount := filepath.Join(t.TempDir(), "stale")
	if err := os.Symlink(workspace, visibleMount); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(staleWorkspace, staleMount); err != nil {
		t.Fatal(err)
	}
	helper := filepath.Join(repoRoot(t), "scripts", "ci", "lib", "local-docker-parity.sh")
	probe := filepath.Join(t.TempDir(), "workspace-mount-probe.sh")
	probeScript := `#!/bin/bash
set -euo pipefail
source "$1"
workspace="$(cd "$2" && pwd -P)"
mode="$3"
stale_workspace="$(cd "$4" && pwd -P)"
visible_mount="$5"
stale_mount="$6"
WORKCELL_DOCKER_CONTEXT="fixture-context"
setup_workcell_trusted_docker_client() {
  :
}
select_workcell_docker_context() {
  [[ "${DOCKER_CONTEXT_NAME}" == "fixture-context" ]]
}
workcell_ci_docker() {
  mount_spec=""
  challenge_name=""
  challenge_value=""
  payload=""
  image_seen=0
  while [[ "$#" -gt 0 ]]; do
    case "$1" in
      --mount)
        mount_spec="${2:-}"
        shift 2
        ;;
      -e)
        case "${2:-}" in
          WORKCELL_VALIDATOR_BIND_CHALLENGE_NAME=*)
            challenge_name="${2#*=}"
            ;;
          WORKCELL_VALIDATOR_BIND_CHALLENGE_VALUE=*)
            challenge_value="${2#*=}"
            ;;
        esac
        shift 2
        ;;
      -c)
        payload="${2:-}"
        shift 2
        ;;
      fixture-image)
        image_seen=1
        shift
        ;;
      *)
        shift
        ;;
    esac
  done
  [[ "${mount_spec}" == "type=bind,src=${workspace},dst=/workspace,readonly" ]] || return 64
  [[ "${image_seen}" -eq 1 ]] || return 64
  [[ "${challenge_name}" == .workcell-validator-bind.* ]] || return 64
  [[ -n "${challenge_value}" ]] || return 64
  [[ -n "${payload}" ]] || return 64
  case "${mode}" in
    visible)
      daemon_workspace="${visible_mount}"
      ;;
    missing)
      return 125
      ;;
    stale)
      daemon_workspace="${stale_mount}"
      ;;
  esac
  translated_payload="${payload//\/workspace/${daemon_workspace}}"
  env \
    "WORKCELL_VALIDATOR_BIND_CHALLENGE_NAME=${challenge_name}" \
    "WORKCELL_VALIDATOR_BIND_CHALLENGE_VALUE=${challenge_value}" \
    /bin/bash -c "${translated_payload}"
}
setup_workcell_ci_docker
require_workcell_ci_workspace_mount fixture-image "${workspace}"
`
	if err := os.WriteFile(probe, []byte(probeScript), 0o700); err != nil {
		t.Fatal(err)
	}

	t.Run("visible", func(t *testing.T) {
		output, err := exec.Command("/bin/bash", probe, helper, workspace, "visible", staleWorkspace, visibleMount, staleMount).CombinedOutput()
		if err != nil {
			t.Fatalf("visible workspace rejected: %v\n%s", err, output)
		}
		assertNoValidatorBindChallenges(t, workspace)
	})

	t.Run("missing", func(t *testing.T) {
		output, err := exec.Command("/bin/bash", probe, helper, workspace, "missing", staleWorkspace, visibleMount, staleMount).CombinedOutput()
		if err == nil {
			t.Fatalf("missing workspace accepted: %s", output)
		}
		for _, want := range []string{
			"Validator workspace is not visible through Docker context fixture-context",
			"Configured WORKCELL_DOCKER_CONTEXT=fixture-context cannot bind this checkout",
		} {
			if !strings.Contains(string(output), want) {
				t.Fatalf("missing-workspace output does not contain %q:\n%s", want, output)
			}
		}
		assertNoValidatorBindChallenges(t, workspace)
	})

	t.Run("stale", func(t *testing.T) {
		output, err := exec.Command("/bin/bash", probe, helper, workspace, "stale", staleWorkspace, visibleMount, staleMount).CombinedOutput()
		if err == nil {
			t.Fatalf("stale workspace accepted: %s", output)
		}
		if !strings.Contains(string(output), "Validator workspace is not visible through Docker context fixture-context") {
			t.Fatalf("stale-workspace output is not actionable:\n%s", output)
		}
		assertNoValidatorBindChallenges(t, workspace)
	})
}

func createValidatorWorkspaceFixture(t *testing.T) string {
	t.Helper()

	workspace := filepath.Join(t.TempDir(), "workspace with spaces")
	if err := os.MkdirAll(filepath.Join(workspace, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string]string{
		"go.mod":                   "module example.com/workcell-fixture\n",
		"scripts/validate-repo.sh": "#!/bin/bash\nexit 0\n",
	} {
		if err := os.WriteFile(filepath.Join(workspace, filepath.FromSlash(path)), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(filepath.Join(workspace, "scripts", "validate-repo.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	return workspace
}

func assertNoValidatorBindChallenges(t *testing.T, workspace string) {
	t.Helper()

	matches, err := filepath.Glob(filepath.Join(workspace, ".workcell-validator-bind.*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("validator bind challenge residue remains: %v", matches)
	}
}
