// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package testkit

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
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

func TestValidatorImageOwnersCleanOnlyTheirExactTags(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	temp := t.TempDir()
	logPath := filepath.Join(temp, "docker.log")
	probe := writeExecutable(t, temp, "probe", `#!/bin/bash
set -euo pipefail
ROOT_DIR="$1"
TMPDIR="$2"
WORKCELL_TEST_DOCKER_LOG="$3"
export ROOT_DIR TMPDIR WORKCELL_TEST_DOCKER_LOG
source "${ROOT_DIR}/scripts/ci/lib/local-docker-parity.sh"
setup_workcell_ci_docker() { :; }
workcell_ci_docker() { printf '%s\n' "$*" >>"${WORKCELL_TEST_DOCKER_LOG}"; }
claim_workcell_validator_image "${ROOT_DIR}" image_a reservation_a
claim_workcell_validator_image "${ROOT_DIR}" image_b reservation_b
[[ "${image_a}" != "${image_b}" && -d "${reservation_a}" && -d "${reservation_b}" ]]
cleanup_workcell_owned_validator_image "${image_a}" "${reservation_a}"
[[ ! -e "${reservation_a}" && -d "${reservation_b}" ]]
cleanup_workcell_owned_validator_image "${image_b}" "${reservation_b}"
[[ ! -e "${reservation_b}" ]]
printf '%s\n%s\n' "${image_a}" "${image_b}"
`)
	command := exec.Command("/bin/bash", probe, root, temp, logPath)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("ownership probe failed: %v\n%s", err, output)
	}
	assertValidatorOwnershipProbe(t, root, logPath, string(output))
}

func TestValidatorImageDefaultTagBindsBothInputs(t *testing.T) {
	t.Parallel()

	repo := t.TempDir()
	dockerfile := filepath.Join(repo, "tools", "validator", "Dockerfile")
	bootstrap := filepath.Join(repo, "runtime", "container", "debian-bootstrap.env")
	writeValidatorTagInput(t, dockerfile, "initial\n")
	writeValidatorTagInput(t, bootstrap, "initial\n")
	initial := validatorImageDefaultTag(t, repoRoot(t), repo)
	writeValidatorTagInput(t, dockerfile, "changed Dockerfile\n")
	dockerfileChanged := validatorImageDefaultTag(t, repoRoot(t), repo)
	writeValidatorTagInput(t, bootstrap, "changed bootstrap\n")
	bootstrapChanged := validatorImageDefaultTag(t, repoRoot(t), repo)
	if initial == dockerfileChanged || dockerfileChanged == bootstrapChanged {
		t.Fatalf("validator tags did not bind both inputs: %q, %q, %q", initial, dockerfileChanged, bootstrapChanged)
	}
}

func writeValidatorTagInput(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func validatorImageDefaultTag(t *testing.T, sourceRoot, inputRoot string) string {
	t.Helper()
	helper := filepath.Join(sourceRoot, "scripts", "ci", "lib", "local-docker-parity.sh")
	command := exec.Command("/bin/bash", "-c", `ROOT_DIR="$1"; source "$2"; workcell_validator_image_default_tag "$3"`, "probe", sourceRoot, helper, inputRoot)
	output, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(output))
}

func TestValidatorImageOwnershipHonorsKeep(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	temp := t.TempDir()
	logPath := filepath.Join(temp, "docker.log")
	probe := writeExecutable(t, temp, "probe", `#!/bin/bash
set -euo pipefail
ROOT_DIR="$1"
TMPDIR="$2"
WORKCELL_TEST_DOCKER_LOG="$3"
WORKCELL_KEEP_VALIDATOR_IMAGE=1
export ROOT_DIR TMPDIR WORKCELL_TEST_DOCKER_LOG WORKCELL_KEEP_VALIDATOR_IMAGE
source "${ROOT_DIR}/scripts/ci/lib/local-docker-parity.sh"
workcell_ci_docker() { printf '%s\n' "$*" >>"${WORKCELL_TEST_DOCKER_LOG}"; }
claim_workcell_validator_image "${ROOT_DIR}" image owned_reservation
cleanup_workcell_owned_validator_image "${image}" "${owned_reservation}"
[[ ! -e "${owned_reservation}" ]]
`)
	command := exec.Command("/bin/bash", probe, root, temp, logPath)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("keep probe failed: %v\n%s", err, output)
	}
	if content, err := os.ReadFile(logPath); err == nil || !os.IsNotExist(err) {
		t.Fatalf("keep probe Docker log = %q, %v; want absent", content, err)
	}
}

func TestValidatorImageClaimFailureLeavesNoReservation(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	for _, failure := range []string{"cksum", "mktemp"} {
		failure := failure
		t.Run(failure, func(t *testing.T) {
			t.Parallel()
			assertValidatorImageClaimFailure(t, root, failure)
		})
	}
}

func assertValidatorImageClaimFailure(t *testing.T, root, failure string) {
	t.Helper()
	temp := t.TempDir()
	probe := writeExecutable(t, temp, "probe", `#!/bin/bash
set -uo pipefail
ROOT_DIR="$1"
TMPDIR="$2"
failure="$3"
export ROOT_DIR TMPDIR
source "${ROOT_DIR}/scripts/ci/lib/local-docker-parity.sh"
if [[ "${failure}" == cksum ]]; then cksum() { return 70; }; fi
if [[ "${failure}" == mktemp ]]; then mktemp() { return 71; }; fi
if claim_workcell_validator_image "${ROOT_DIR}" image reservation; then exit 1; fi
if compgen -G "${TMPDIR}/workcell-validator-owner.*" >/dev/null; then exit 2; fi
exit 0
`)
	if output, err := exec.Command("/bin/bash", probe, root, temp, failure).CombinedOutput(); err != nil {
		t.Fatalf("%s failure probe failed: %v\n%s", failure, err, output)
	}
}

func TestValidatorImageOwnershipPreservesFailureStatus(t *testing.T) {
	t.Parallel()

	const status = 41
	root := repoRoot(t)
	temp := t.TempDir()
	logPath := filepath.Join(temp, "docker.log")
	probe := writeExecutable(t, temp, "probe", `#!/bin/bash
set -euo pipefail
ROOT_DIR="$1"
TMPDIR="$2"
WORKCELL_TEST_DOCKER_LOG="$3"
status="$4"
export ROOT_DIR TMPDIR WORKCELL_TEST_DOCKER_LOG
source "${ROOT_DIR}/scripts/ci/lib/local-docker-parity.sh"
setup_workcell_ci_docker() { :; }
workcell_ci_docker() { printf '%s\n' "$*" >>"${WORKCELL_TEST_DOCKER_LOG}"; }
image=""
reservation=""
cleanup() {
  local saved_status=$?
  cleanup_workcell_owned_validator_image "${image}" "${reservation}"
  return "${saved_status}"
}
trap cleanup EXIT
claim_workcell_validator_image "${ROOT_DIR}" image reservation
exit "${status}"
`)
	command := exec.Command("/bin/bash", probe, root, temp, logPath, fmt.Sprint(status))
	err := command.Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != status {
		t.Fatalf("failure status = %v, want %d", err, status)
	}
	content, err := os.ReadFile(logPath)
	if err != nil || !strings.HasPrefix(string(content), "image rm -f workcell-validator:local-") {
		t.Fatalf("failure cleanup log = %q, %v", content, err)
	}
}

func TestValidatorJobsClaimOnlyImplicitImages(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	for _, relative := range []string{"scripts/ci/job-docs.sh", "scripts/ci/job-validate.sh"} {
		assertValidatorJobImageOwnership(t, root, relative)
	}
}

type validatorJobOwnershipCase struct {
	name           string
	explicitImage  string
	buildStatus    int
	workloadStatus int
	wrongReference bool
	wantStatus     int
	wantCleanup    bool
}

func TestValidatorJobOwnershipLifecycle(t *testing.T) {
	t.Parallel()

	for _, job := range []string{"scripts/ci/job-docs.sh", "scripts/ci/job-validate.sh"} {
		for _, test := range []validatorJobOwnershipCase{
			{name: "success", wantCleanup: true},
			{name: "build failure", buildStatus: 41, wantStatus: 41, wantCleanup: true},
			{name: "workload failure", workloadStatus: 42, wantStatus: 42, wantCleanup: true},
			{name: "unexpected builder reference", wrongReference: true, wantStatus: 1, wantCleanup: true},
			{name: "explicit caller image", explicitImage: "caller/image:keep"},
		} {
			job, test := job, test
			t.Run(path.Base(job)+"/"+test.name, func(t *testing.T) {
				t.Parallel()
				runValidatorJobOwnershipFixture(t, job, test)
			})
		}
	}
}

func runValidatorJobOwnershipFixture(t *testing.T, job string, test validatorJobOwnershipCase) {
	t.Helper()
	sourceRoot := repoRoot(t)
	root := t.TempDir()
	prepareValidatorOwnershipFixture(t, sourceRoot, root, job)
	arguments := []string{filepath.Join(root, filepath.FromSlash(job))}
	if path.Base(job) == "job-validate.sh" {
		writeValidatorValidateFixtureFiles(t, root)
		arguments = append(arguments, "--profile", "pr-parity")
	}
	logPath := filepath.Join(root, "docker.log")
	builderInputPath := filepath.Join(root, "builder-input.log")
	command := exec.Command("/bin/bash", arguments...)
	command.Env = append(os.Environ(),
		"TMPDIR="+root,
		"PATH="+filepath.Join(root, "bin")+":"+os.Getenv("PATH"),
		"WORKCELL_TEST_DOCKER_LOG="+logPath,
		"WORKCELL_TEST_BUILDER_INPUT_LOG="+builderInputPath,
		"WORKCELL_TEST_BUILD_STATUS="+fmt.Sprint(test.buildStatus),
		"WORKCELL_TEST_WORKLOAD_STATUS="+fmt.Sprint(test.workloadStatus),
		"WORKCELL_TEST_WRONG_REFERENCE="+fmt.Sprint(test.wrongReference),
		"WORKCELL_VALIDATOR_IMAGE="+test.explicitImage,
	)
	output, err := command.CombinedOutput()
	assertCommandExitStatus(t, err, test.wantStatus, output)
	assertValidatorJobOwnershipResult(t, root, logPath, builderInputPath, test.wantCleanup)
}

func writeValidatorValidateFixtureFiles(t *testing.T, root string) {
	t.Helper()
	for _, name := range []string{"verify-github-macos-release-test-runners.sh", "verify-build-input-manifest.sh", "verify-upstream-codex-release.sh", "verify-upstream-claude-release.sh", "verify-upstream-copilot-release.sh", "verify-upstream-gemini-release.sh", "verify-invariants.sh"} {
		writeExecutable(t, filepath.Join(root, "scripts"), name, "#!/bin/bash\nexit 0\n")
	}
	writeExecutable(t, filepath.Join(root, "scripts", "ci"), "run-validate-in-validator.sh", "#!/bin/bash\nexit \"${WORKCELL_TEST_WORKLOAD_STATUS}\"\n")
	writeExecutable(t, filepath.Join(root, "scripts"), "generate-homebrew-formula.sh", "#!/bin/bash\nprintf 'fixture\\n' >\"$3\"\n")
}

func prepareValidatorOwnershipFixture(t *testing.T, sourceRoot, root, job string) {
	t.Helper()
	for _, directory := range []string{"scripts/ci/lib", "scripts/lib", "tools/validator", "runtime/container", "bin"} {
		if err := os.MkdirAll(filepath.Join(root, directory), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	copyValidatorFixtureFile(t, sourceRoot, root, job, 0o755)
	copyValidatorFixtureFile(t, sourceRoot, root, "scripts/ci/lib/local-docker-parity.sh", 0o644)
	writeValidatorFixtureFiles(t, root)
}

func writeValidatorFixtureFiles(t *testing.T, root string) {
	t.Helper()
	writeExecutable(t, filepath.Join(root, "scripts", "lib"), "trusted-docker-client.sh", "#!/bin/bash\nsetup_workcell_trusted_docker_client() { :; }\ncleanup_workcell_trusted_docker_client() { :; }\nselect_workcell_docker_context() { DOCKER_CONTEXT_NAME=fixture; }\n")
	writeExecutable(t, filepath.Join(root, "scripts", "lib"), "go-run-env.sh", "#!/bin/bash\nrun_go_in_repo() { :; }\n")
	for _, name := range []string{"check-pinned-inputs.sh", "check-doc-support-matrix-fields.sh", "check-public-contract.sh", "check-doc-links.sh"} {
		writeExecutable(t, filepath.Join(root, "scripts"), name, "#!/bin/bash\nexit 0\n")
	}
	writeExecutable(t, filepath.Join(root, "scripts", "ci"), "build-validator-image.sh", "#!/bin/bash\nprintf '%s\\n' \"${WORKCELL_VALIDATOR_IMAGE}\" >\"${WORKCELL_TEST_BUILDER_INPUT_LOG}\"\n[[ \"${WORKCELL_TEST_BUILD_STATUS}\" -eq 0 ]] || exit \"${WORKCELL_TEST_BUILD_STATUS}\"\nif [[ \"${WORKCELL_TEST_WRONG_REFERENCE}\" == true ]]; then echo wrong/reference:latest; else printf '%s\\n' \"${WORKCELL_VALIDATOR_IMAGE}\"; fi\n")
	writeExecutable(t, filepath.Join(root, "scripts", "ci"), "run-docs-in-validator.sh", "#!/bin/bash\nexit \"${WORKCELL_TEST_WORKLOAD_STATUS}\"\n")
	writeExecutable(t, filepath.Join(root, "bin"), "docker", "#!/bin/bash\nprintf '%s\\n' \"$*\" >>\"${WORKCELL_TEST_DOCKER_LOG}\"\n")
	if err := os.WriteFile(filepath.Join(root, "tools", "validator", "Dockerfile"), []byte("fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "runtime", "container", "debian-bootstrap.env"), []byte("fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func copyValidatorFixtureFile(t *testing.T, sourceRoot, root, relative string, mode os.FileMode) {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(sourceRoot, relative))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, relative), content, mode); err != nil {
		t.Fatal(err)
	}
}

func assertCommandExitStatus(t *testing.T, err error, want int, output []byte) {
	t.Helper()
	if want == 0 && err == nil {
		return
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != want {
		t.Fatalf("command status = %v, want %d\n%s", err, want, output)
	}
}

func assertValidatorJobOwnershipResult(t *testing.T, root, logPath, builderInputPath string, owned bool) {
	t.Helper()
	assertNoValidatorReservations(t, root)
	builderInput, err := os.ReadFile(builderInputPath)
	if err != nil {
		t.Fatal(err)
	}
	cleanupLines := validatorCleanupLines(t, logPath)
	want := []string{"image rm -f " + strings.TrimSpace(string(builderInput))}
	if !owned {
		want = nil
	}
	if !slices.Equal(cleanupLines, want) {
		t.Fatalf("cleanup lines = %q, want exact %q", cleanupLines, want)
	}
}

func validatorCleanupLines(t *testing.T, logPath string) []string {
	t.Helper()
	content, err := os.ReadFile(logPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var cleanup []string
	for _, line := range strings.Split(strings.TrimSpace(string(content)), "\n") {
		line = strings.TrimPrefix(line, "--context fixture ")
		if strings.HasPrefix(line, "image rm -f ") {
			cleanup = append(cleanup, line)
		}
	}
	return cleanup
}

func assertNoValidatorReservations(t *testing.T, root string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(root, "workcell-validator-owner.*"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("owner reservations = %q, %v", matches, err)
	}
}

func assertValidatorJobImageOwnership(t *testing.T, root, relative string) {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(root, relative))
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	claim := `claim_workcell_validator_image "${ROOT_DIR}" VALIDATOR_IMAGE VALIDATOR_IMAGE_RESERVATION`
	if strings.Count(text, claim) != 1 || strings.Count(text, `trap cleanup EXIT`) != 1 ||
		!strings.Contains(text, `if [[ -z "${VALIDATOR_IMAGE_INPUT}" ]]; then`) {
		t.Fatalf("%s does not make implicit image ownership explicit", relative)
	}
	if strings.Index(text, `trap cleanup EXIT`) > strings.Index(text, claim) {
		t.Fatalf("%s claims its image before cleanup is armed", relative)
	}
}

func assertValidatorOwnershipProbe(t *testing.T, root, logPath, output string) {
	t.Helper()
	images := strings.Fields(output)
	if len(images) != 2 {
		t.Fatalf("owned image output = %q, want two references", output)
	}
	prefixCommand := exec.Command("/bin/bash", "-c", `source "$1"; workcell_validator_image_default_tag "$2"`, "probe", filepath.Join(root, "scripts", "ci", "lib", "local-docker-parity.sh"), root)
	prefix, err := prefixCommand.Output()
	if err != nil {
		t.Fatal(err)
	}
	assertValidatorImagePrefixes(t, images, string(prefix))
	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	want := "image rm -f " + images[0] + "\nimage rm -f " + images[1] + "\n"
	if string(content) != want {
		t.Fatalf("Docker cleanup log = %q, want %q with no image ID", content, want)
	}
}

func assertValidatorImagePrefixes(t *testing.T, images []string, prefix string) {
	t.Helper()
	for _, image := range images {
		if !strings.HasPrefix(image, strings.TrimSpace(prefix)+"-workcell-validator-owner.") {
			t.Fatalf("owned image %q does not use checksum prefix %q", image, prefix)
		}
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

// Every validator lane runs its workload as a uid the image has no /etc/passwd
// record for, so a lane without the synthesized entry loses ssh-keygen, and
// with it git's ssh signing and verification.  The synthesis lives in one
// library precisely so a lane cannot half-adopt it: this test fails when a lane
// stops sourcing it, calls it more than once, or drops the read-only mount.
func TestValidatorLanesMountSynthesizedPasswd(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	validatorMounts := []string{
		`workcell_ci_workspace_mount_spec "${validator_passwd}" true /etc/passwd`,
		`--mount "${validator_passwd_mount}"`,
	}
	for rel, mounts := range map[string][]string{
		"scripts/ci/run-docs-in-validator.sh":     validatorMounts,
		"scripts/ci/run-fuzz-in-validator.sh":     validatorMounts,
		"scripts/ci/run-mutation-in-validator.sh": validatorMounts,
		"scripts/ci/run-validate-in-validator.sh": validatorMounts,
		"scripts/build-and-test.sh":               {`-v "${passwd_file}:/etc/passwd:ro"`},
	} {
		rel, mounts := rel, mounts
		t.Run(rel, func(t *testing.T) {
			t.Parallel()

			content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
			if err != nil {
				t.Fatal(err)
			}
			lane := activeShellLines(string(content))
			if !strings.Contains(lane, "scripts/ci/lib/validator-passwd.sh") {
				t.Fatalf("%s must use the shared passwd synthesis library", rel)
			}
			if got := strings.Count(lane, "workcell_ci_validator_passwd_file"); got != 1 {
				t.Fatalf("%s calls the passwd synthesis %d times, want 1", rel, got)
			}
			for _, mount := range mounts {
				if !strings.Contains(lane, mount) {
					t.Fatalf("%s must mount the synthesized passwd file: %s", rel, mount)
				}
			}
		})
	}
}

// The passwd artifact has to land inside the workspace the caller preflighted.
// mkdir -p and mktemp both follow a symlinked tmp without complaint, which
// would move the create, the mode change and the removal somewhere else, so the
// library gates on the canonical path.  The symlinked case is its negative
// fixture.
func TestValidatorPasswdRefusesSymlinkedWorkspaceTmp(t *testing.T) {
	t.Parallel()

	library := filepath.Join(repoRoot(t), "scripts", "ci", "lib", "validator-passwd.sh")
	binDir := t.TempDir()
	writeExecutable(t, binDir, "docker", "#!/bin/bash\nprintf 'root:x:0:0:root:/root:/bin/bash\\n'\n")
	probe := writeExecutable(t, t.TempDir(), "probe", `#!/bin/bash
set -euo pipefail
PATH="$1:${PATH}"
export PATH
source "$2"
workcell_ci_validator_passwd_file docker fixture-image 1000 1000 /home/fixture "$3"
`)

	for _, symlinked := range []bool{false, true} {
		symlinked := symlinked
		name := "plain tmp"
		if symlinked {
			name = "symlinked tmp"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			workspace := t.TempDir()
			if symlinked {
				if err := os.Symlink(t.TempDir(), filepath.Join(workspace, "tmp")); err != nil {
					t.Fatal(err)
				}
			}
			output, err := exec.Command("/bin/bash", probe, binDir, library, workspace).CombinedOutput()
			if symlinked {
				if err == nil || !strings.Contains(string(output), "is not the canonical") {
					t.Fatalf("symlinked tmp accepted: %v\n%s", err, output)
				}
				return
			}
			if err != nil {
				t.Fatalf("plain tmp refused: %v\n%s", err, output)
			}
			canonical, err := filepath.EvalSymlinks(workspace)
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.TrimSpace(string(output)); !strings.HasPrefix(got, filepath.Join(canonical, "tmp")+"/") {
				t.Fatalf("passwd artifact = %q, want a file under %q", got, canonical)
			}
		})
	}
}

// The hostile lane earns its runtime only while each axis keeps the shapes that
// reproduced a finding by hand, so the derivations are executed here rather
// than pattern-matched: a dropped backslash that lets bash expand $HOME, a
// shortened padding component, or a comma quietly removed from the bind source
// leaves the lane green for the wrong reason.
func TestHostileAxesKeepTheShapesThatReproducedFindings(t *testing.T) {
	t.Parallel()

	tmpdir := runHostileDerivation(t, `validator_tmp="${validator_tmp}/`, `validator_tmp="/tmp/workcell-home-1000/.tmp"`, "validator_tmp")
	if !strings.Contains(tmpdir, " ") {
		t.Fatalf("hostile TMPDIR %q has no whitespace component", tmpdir)
	}
	if !strings.Contains(tmpdir, "$") {
		t.Fatalf("hostile TMPDIR %q has no unexpanded dollar sign", tmpdir)
	}
	if !hasOptionToken(tmpdir) {
		t.Fatalf("hostile TMPDIR %q has no --prefixed token", tmpdir)
	}
	padded := false
	for _, component := range strings.Split(tmpdir, "/") {
		padded = padded || len(component) >= 80
	}
	if !padded {
		t.Fatalf("hostile TMPDIR %q has no ~80-character padding component", tmpdir)
	}

	workspace := runHostileDerivation(t, `hostile_workspace="${hostile_root}/`, `hostile_root="/tmp/workcell-hostile.fixture"`, "hostile_workspace")
	if !strings.Contains(workspace, " ") {
		t.Fatalf("hostile workspace %q has no whitespace component", workspace)
	}
	if !strings.Contains(workspace, ",") {
		t.Fatalf("hostile workspace %q has no comma, so it never exercises the CSV mount record", workspace)
	}
	if !hasOptionToken(workspace) {
		t.Fatalf("hostile workspace %q has no --prefixed token", workspace)
	}
}

// An unknown axis must fail closed.  A typo that fell through to the ordinary
// validation would report a green advisory lane that tested nothing hostile.
func TestHostileAxisRejectsAnUnknownValue(t *testing.T) {
	t.Parallel()

	lane := filepath.Join(repoRoot(t), "scripts", "ci", "run-validate-in-validator.sh")
	command := exec.Command(lane)
	command.Env = append(os.Environ(), "WORKCELL_HOSTILE_ENV=not-an-axis")
	output, err := command.CombinedOutput()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 2 {
		t.Fatalf("unknown axis status = %v, want exit 2\n%s", err, output)
	}
	if !strings.Contains(string(output), "Unsupported WORKCELL_HOSTILE_ENV: not-an-axis") {
		t.Fatalf("unknown axis output = %q", output)
	}
}

func hasOptionToken(path string) bool {
	for _, word := range strings.Fields(path) {
		if strings.HasPrefix(word, "--") {
			return true
		}
	}
	return false
}

// runHostileDerivation executes the single assignment the lane derives an axis
// with, so the test reads the value bash produces instead of the source text.
func runHostileDerivation(t *testing.T, prefix, preset, variable string) string {
	t.Helper()

	lane := filepath.Join(repoRoot(t), "scripts", "ci", "run-validate-in-validator.sh")
	content, err := os.ReadFile(lane)
	if err != nil {
		t.Fatal(err)
	}
	derivation := ""
	for _, line := range strings.Split(activeShellLines(string(content)), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), prefix) {
			derivation = strings.TrimSpace(line)
			break
		}
	}
	if derivation == "" {
		t.Fatalf("run-validate-in-validator.sh no longer derives %s", variable)
	}
	script := preset + "; " + derivation + `; printf '%s' "${` + variable + `}"`
	command := exec.Command("/bin/bash", "-c", script)
	command.Env = append(os.Environ(), "HOME=/hostile-home-must-not-expand")
	output, err := command.Output()
	if err != nil {
		t.Fatalf("%s derivation failed: %v", variable, err)
	}
	return string(output)
}

// activeShellLines drops whole-line comments so a lane cannot satisfy a check
// with the statement it commented out.  A trailing comment is left alone: it
// cannot carry a statement, and cutting at the first "#" would corrupt a
// pathname or a parameter expansion that contains one.
func activeShellLines(script string) string {
	active := make([]string, 0, strings.Count(script, "\n")+1)
	for _, line := range strings.Split(script, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		active = append(active, line)
	}
	return strings.Join(active, "\n")
}

// The negative fixture for activeShellLines: a commented-out source statement
// must not read as coverage.
func TestActiveShellLinesDropsCommentedStatements(t *testing.T) {
	t.Parallel()

	commented := "  # source \"${ROOT_DIR}/scripts/ci/lib/validator-passwd.sh\"\n#!/bin/bash\n"
	if got := activeShellLines(commented); strings.Contains(got, "validator-passwd.sh") {
		t.Fatalf("commented statement survived comment stripping: %q", got)
	}
	live := "source \"${ROOT_DIR}/scripts/ci/lib/validator-passwd.sh\" # keep the trailing comment\n"
	if got := activeShellLines(live); !strings.Contains(got, "# keep the trailing comment") {
		t.Fatalf("live statement lost its trailing comment: %q", got)
	}
}
