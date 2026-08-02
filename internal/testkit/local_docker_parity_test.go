// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package testkit

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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
			mountSpec := `validator_workspace_mount="$(workcell_ci_workspace_mount_spec "${` + tc.workspace + `}" false)"`
			mount := `--mount "${validator_workspace_mount}"`
			afterPreflight := string(content)[preflight+len(call):]
			if !strings.Contains(afterPreflight, mountSpec) || !strings.Contains(afterPreflight, mount) {
				t.Fatalf("%s must use the non-creating comma-safe bind mechanism for its validator workload", tc.rel)
			}
			if tc.workspace == "ROOT_DIR" &&
				!strings.Contains(string(content), `ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"`) {
				t.Fatalf("%s must canonicalize ROOT_DIR before both preflight and workload binds", tc.rel)
			}
		})
	}
}

func TestRequireWorkcellCIWorkspaceMountDispatchesGoPolicy(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	binDir := t.TempDir()
	docker := writeExecutable(t, binDir, "docker", "#!/bin/bash\nexit 0\n")
	goBin := writeExecutable(t, binDir, "go", `#!/bin/bash
set -euo pipefail
printf '%s\0' "$@" >"${WORKCELL_TEST_GO_ARGS}"
`)
	argsLog := filepath.Join(t.TempDir(), "go.args")
	workspace := filepath.Join(t.TempDir(), "workspace, with spaces")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	helper := filepath.Join(root, "scripts", "ci", "lib", "local-docker-parity.sh")
	probe := writeExecutable(t, t.TempDir(), "probe", `#!/bin/bash
set -euo pipefail
ROOT_DIR="$1"
WORKCELL_GO_BIN="$2"
PATH="$3:${PATH}"
DOCKER_CONTEXT_NAME="fixture-context"
WORKCELL_DOCKER_CONTEXT="fixture-context"
export ROOT_DIR WORKCELL_GO_BIN PATH DOCKER_CONTEXT_NAME WORKCELL_DOCKER_CONTEXT
source "$4"
require_workcell_ci_workspace_mount fixture-image "$5"
`)
	command := exec.Command("/bin/bash", probe, root, goBin, binDir, helper, workspace)
	command.Env = append(os.Environ(), "WORKCELL_TEST_GO_ARGS="+argsLog)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("Go policy dispatch failed: %v\n%s", err, output)
	}
	data, err := os.ReadFile(argsLog)
	if err != nil {
		t.Fatal(err)
	}
	got := stringsFromNUL(data)
	want := []string{
		"run",
		"./cmd/workcell-citools",
		"validate-docker-workspace-bind",
		docker,
		"fixture-image",
		workspace,
		"fixture-context",
		"true",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("Go policy args = %q, want %q", got, want)
	}
}

func writeExecutable(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func stringsFromNUL(data []byte) []string {
	records := bytes.Split(data, []byte{0})
	if len(records) > 0 && len(records[len(records)-1]) == 0 {
		records = records[:len(records)-1]
	}
	result := make([]string, len(records))
	for i, record := range records {
		result[i] = string(record)
	}
	return result
}
