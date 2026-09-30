// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package testkit

import (
	"regexp"
	"strings"
	"testing"
)

var ciJobHeader = regexp.MustCompile(`(?m)^  [a-z][a-z-]*:$`)

// jobBlock returns the text of one top-level job in ci.yml, from its "  id:"
// line up to the next top-level job.
func jobBlock(t *testing.T, workflow, id string) string {
	t.Helper()
	headers := ciJobHeader.FindAllStringIndex(workflow, -1)
	for i, h := range headers {
		if workflow[h[0]:h[1]] != "  "+id+":" {
			continue
		}
		if i+1 < len(headers) {
			return workflow[h[0]:headers[i+1][0]]
		}
		return workflow[h[0]:]
	}
	t.Fatalf("ci.yml has no job %q", id)
	return ""
}

// The host launcher invariants run in their own required job, in parallel with
// validate, and hosted validate must not run them a second time.
func TestHostInvariantsRunInTheirOwnRequiredJob(t *testing.T) {
	t.Parallel()

	workflow := readRepoFile(t, ".github", "workflows", "ci.yml")
	invariants := jobBlock(t, workflow, "invariants")
	for _, want := range []string{
		"name: Host launcher invariants",
		"needs: pr-shape",
		"run: ./scripts/verify-invariants.sh",
	} {
		if !strings.Contains(invariants, want) {
			t.Fatalf("invariants job lacks %q", want)
		}
	}
	if strings.Contains(invariants, "\n    if:") || strings.Contains(invariants, "needs: validate") {
		t.Fatal("invariants job must run on every event and not wait for validate")
	}
	if !strings.Contains(jobBlock(t, workflow, "validate"), `WORKCELL_CI_VALIDATE_SKIP_HOST_INVARIANTS: "1"`) {
		t.Fatal("hosted validate must skip the invariants that their own job runs")
	}
	if !strings.Contains(readRepoFile(t, "policy", "github-hosted-controls.toml"), `"Host launcher invariants",`) {
		t.Fatal("Host launcher invariants is not a required status check")
	}
	validate := readRepoFile(t, "scripts", "ci", "job-validate.sh")
	if !strings.Contains(validate, "if [[ \"${SKIP_HOST_INVARIANTS}\" != \"1\" ]]; then\n  echo \"[ci/validate] host launcher invariants\"\n  \"${ROOT_DIR}/scripts/verify-invariants.sh\"\nfi") {
		t.Fatal("job-validate.sh must keep running verify-invariants.sh unless the skip flag is 1")
	}
}

// Caches are restored on every run and saved only by a push to main, so a pull
// request can never write into the scope that main and later runs read.
func TestValidatorCachesAreSavedOnlyOnMain(t *testing.T) {
	t.Parallel()

	workflow := readRepoFile(t, ".github", "workflows", "ci.yml")
	if strings.Count(workflow, "actions/cache/save@") != 1 {
		t.Fatal("ci.yml must have exactly one cache save step")
	}
	if strings.Contains(workflow, "uses: actions/cache@") {
		t.Fatal("ci.yml must use the split restore and save actions, not the combined action")
	}
	save := workflow[strings.Index(workflow, "- name: Save validator caches"):]
	save = save[:strings.Index(save, "\n\n")]
	if !strings.Contains(save, "github.event_name == 'push' && github.ref == 'refs/heads/main'") {
		t.Fatalf("cache save is not limited to pushes to main:\n%s", save)
	}
	for _, line := range strings.Split(workflow, "\n") {
		if strings.Contains(line, "actions/cache/") && !strings.Contains(line, "@55cc8345863c7cc4c66a329aec7e433d2d1c52a9 # v") {
			t.Fatalf("cache action is not pinned by SHA: %s", line)
		}
	}
	for _, id := range []string{"validate", "hostile-env"} {
		block := jobBlock(t, workflow, id)
		for _, want := range []string{"go.sum", "Cargo.lock", "tools/validator/Dockerfile", "WORKCELL_VALIDATOR_CACHE_DIR"} {
			if !strings.Contains(block, want) {
				t.Fatalf("%s job lacks %q", id, want)
			}
		}
	}
}

// The cache directory is mounted only when the container uid is the host uid,
// so the root and uidmap axes keep the in-container cache.
func TestValidatorCacheMountIsGatedOnHostUID(t *testing.T) {
	t.Parallel()

	lane := readRepoFile(t, "scripts", "ci", "run-validate-in-validator.sh")
	for _, want := range []string{
		`[[ -n "${WORKCELL_VALIDATOR_CACHE_DIR:-}" && "${validator_uid}" == "$(id -u)" ]]`,
		`WORKCELL_VALIDATOR_CACHE_DIR must be an absolute path`,
		`WORKCELL_VALIDATOR_CACHE_DIR must not be a symlink`,
		`! -O "${WORKCELL_VALIDATOR_CACHE_DIR}"`,
		`|| cache_unsafe_mode="find-failed"`,
		`validator_cache="/workcell-validator-cache"`,
		`workcell_ci_workspace_mount_spec "${WORKCELL_VALIDATOR_CACHE_DIR}" false "${validator_cache}"`,
		`${cache_mount_args[@]+"${cache_mount_args[@]}"} \`,
	} {
		if !strings.Contains(lane, want) {
			t.Fatalf("run-validate-in-validator.sh lacks %q", want)
		}
	}
}
