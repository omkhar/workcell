// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package workcellhardening

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// happyLauncher is a minimal but structurally faithful scripts/workcell:
// it satisfies all eleven invariants (the correct shebang on line 1, the
// run_host_colima HOME restore inside the function body, every scrub /
// unset / source substring, and no Perl-backed shasum).  Individual
// negative cases mutate one property of this baseline.
const happyLauncher = `#!/usr/bin/env -S -i PATH=/opt/homebrew/bin:/usr/bin:/bin BASH_ENV= ENV= /bin/bash
set -euo pipefail

scrub_host_process_env() {
  unset PERL5OPT PERL5LIB PERLLIB PERL_MB_OPT PERL_MM_OPT
  for var in $(compgen -v); do
    case "${var}" in
      DYLD_*) unset "${var}" ;;
    esac
  done
  unset DOCKER_CONTEXT
  unset DOCKER_CLI_PLUGIN_EXTRA_DIRS
}

source "${ROOT_DIR}/scripts/lib/trusted-docker-client.sh"
source "${ROOT_DIR}/scripts/lib/shellproto.sh"
source "${ROOT_DIR}/scripts/lib/sessionctl-shim.sh"

run_host_colima() {
  HOME="${REAL_HOME}" COLIMA_HOME="${COLIMA_STATE_ROOT}" "${HOST_COLIMA_BIN}" "$@"
}
`

// writeLauncher materializes a fake repo with scripts/workcell set to the
// given body; a body of "" means "do not create the file" (unreadable
// launcher case).
func writeLauncher(t *testing.T, body string) string {
	t.Helper()
	root := t.TempDir()
	if body == "" {
		return root
	}
	path := filepath.Join(root, launcherRelPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return root
}

func TestCheck(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr string // "" means expect success
	}{
		{
			name: "happy path all invariants hold",
			body: happyLauncher,
		},
		{
			// kindFunctionBlock: run_host_colima body lacks the HOME restore.
			name:    "run_host_colima missing HOME restore",
			body:    strings.Replace(happyLauncher, `HOME="${REAL_HOME}" `, "", 1),
			wantErr: "Expected run_host_colima to restore the real host HOME instead of the Docker client sandbox home",
		},
		{
			// kindFunctionBlock scoping: the HOME restore text exists in the
			// file but OUTSIDE the run_host_colima body, so the block-scoped
			// check must still fail (guards against the whole-file match the
			// migration was designed to prevent).
			name: "HOME restore only outside run_host_colima body",
			body: strings.Replace(happyLauncher, `HOME="${REAL_HOME}" `, "", 1) +
				"\ndecoy() {\n  HOME=\"${REAL_HOME}\" true\n}\n",
			wantErr: "Expected run_host_colima to restore the real host HOME instead of the Docker client sandbox home",
		},
		{
			// kindFirstLineRegex: wrong shebang on line 1.
			name:    "wrong shebang",
			body:    strings.Replace(happyLauncher, "#!/usr/bin/env -S -i PATH=/opt/homebrew/bin:/usr/bin:/bin BASH_ENV= ENV= /bin/bash", "#!/bin/bash", 1),
			wantErr: "Expected scripts/workcell to use env -S -i with an absolute /bin/bash and cleared host environment",
		},
		{
			// kindFirstLineRegex anchoring: the correct shebang appears, but
			// not on the FIRST line, so the anchored line-1 check must fail.
			name:    "correct shebang not on first line",
			body:    "#!/bin/bash\n" + happyLauncher,
			wantErr: "Expected scripts/workcell to use env -S -i with an absolute /bin/bash and cleared host environment",
		},
		{
			// kindPresent: scrub_host_process_env removed.
			name:    "missing scrub_host_process_env",
			body:    strings.Replace(happyLauncher, "scrub_host_process_env", "some_other_name", 1),
			wantErr: "Expected scripts/workcell to scrub hostile host process environment before host tool lookup",
		},
		{
			// kindPresent: Perl unset line removed.
			name:    "missing Perl scrub",
			body:    strings.Replace(happyLauncher, "unset PERL5OPT PERL5LIB PERLLIB PERL_MB_OPT PERL_MM_OPT\n", "", 1),
			wantErr: "Expected scripts/workcell to scrub hostile Perl environment before host tool lookup",
		},
		{
			// kindPresent: literal DYLD_* removed.
			name:    "missing DYLD scrub",
			body:    strings.Replace(happyLauncher, "DYLD_*", "OTHER_", 1),
			wantErr: "Expected scripts/workcell to scrub DYLD_* variables before host tool lookup",
		},
		{
			// kindAbsent: Perl-backed shasum present is a violation.
			name:    "shasum present",
			body:    happyLauncher + "\nprofile_hash() { shasum -a 256 \"$1\"; }\n",
			wantErr: "scripts/workcell still uses Perl-backed shasum for profile hashing",
		},
		{
			// kindPresent: DOCKER_CONTEXT unset removed.
			name:    "missing DOCKER_CONTEXT scrub",
			body:    strings.Replace(happyLauncher, "unset DOCKER_CONTEXT\n", "", 1),
			wantErr: "Expected scripts/workcell to scrub caller Docker context overrides before binding the managed daemon",
		},
		{
			// kindPresent: DOCKER_CLI_PLUGIN_EXTRA_DIRS unset removed.
			name:    "missing DOCKER_CLI_PLUGIN_EXTRA_DIRS scrub",
			body:    strings.Replace(happyLauncher, "unset DOCKER_CLI_PLUGIN_EXTRA_DIRS\n", "", 1),
			wantErr: "Expected scripts/workcell to scrub caller Docker CLI plugin overrides",
		},
		{
			// kindPresent (source line): trusted-docker-client source removed.
			name:    "missing trusted-docker-client source",
			body:    strings.Replace(happyLauncher, `source "${ROOT_DIR}/scripts/lib/trusted-docker-client.sh"`+"\n", "", 1),
			wantErr: "Expected scripts/workcell to source the trusted Docker client helper",
		},
		{
			// kindPresent (source line): shellproto source removed.
			name:    "missing shellproto source",
			body:    strings.Replace(happyLauncher, `source "${ROOT_DIR}/scripts/lib/shellproto.sh"`+"\n", "", 1),
			wantErr: "Expected scripts/workcell to source the shellproto helper",
		},
		{
			// kindPresent (source line): sessionctl-shim source removed.
			name:    "missing sessionctl-shim source",
			body:    strings.Replace(happyLauncher, `source "${ROOT_DIR}/scripts/lib/sessionctl-shim.sh"`+"\n", "", 1),
			wantErr: "Expected scripts/workcell to source the sessionctl shim helper",
		},
		{
			// A missing launcher is treated as empty content, so the first
			// affirmative check (run_host_colima HOME restore) fires.
			name:    "unreadable launcher",
			body:    "",
			wantErr: "Expected run_host_colima to restore the real host HOME instead of the Docker client sandbox home",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := writeLauncher(t, tc.body)
			err := Check(root)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Check() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Check() = nil, want error %q", tc.wantErr)
			}
			if err.Error() != tc.wantErr {
				t.Fatalf("Check() error = %q, want %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// configSafetyHappyLauncher is a minimal scripts/workcell that satisfies
// all four config-safety invariants: no test-harness host tool override
// support, no unsafe YAML.load_file parsing, both the COLIMA_STATE_ROOT
// assignment and the COLIMA_HOME pin, and a REAL_HOME assignment.
// Individual negative cases mutate one property of this baseline.
const configSafetyHappyLauncher = `#!/bin/bash
set -euo pipefail
REAL_HOME="$(host_real_home)"
COLIMA_STATE_ROOT="${REAL_HOME}/.workcell/colima"
run_host_colima() {
  COLIMA_HOME="${COLIMA_STATE_ROOT}" "${HOST_COLIMA_BIN}" "$@"
}
`

func TestCheckConfigSafety(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr string // "" means expect success
	}{
		{
			name: "happy path all invariants hold",
			body: configSafetyHappyLauncher,
		},
		{
			// kindRegexAbsent: WORKCELL_DOCKER_BIN= present trips check #1.
			name:    "WORKCELL_DOCKER_BIN override present",
			body:    configSafetyHappyLauncher + "\nWORKCELL_DOCKER_BIN=/usr/bin/docker\n",
			wantErr: "Unexpected test-harness host tool override support remains in scripts/workcell",
		},
		{
			// kindRegexAbsent: bare WORKCELL_TEST_HARNESS (no `=`) also trips.
			name:    "WORKCELL_TEST_HARNESS present",
			body:    configSafetyHappyLauncher + "\nif [ -n \"${WORKCELL_TEST_HARNESS:-}\" ]; then :; fi\n",
			wantErr: "Unexpected test-harness host tool override support remains in scripts/workcell",
		},
		{
			// kindRegexAbsent: WORKCELL_GIT_BIN= (alternation branch) trips.
			name:    "WORKCELL_GIT_BIN override present",
			body:    configSafetyHappyLauncher + "\nWORKCELL_GIT_BIN=/usr/bin/git\n",
			wantErr: "Unexpected test-harness host tool override support remains in scripts/workcell",
		},
		{
			// kindRegexAbsent negative: an unrelated WORKCELL_*_BIN= that is
			// NOT one of GIT/COLIMA/DOCKER/RUBY must NOT trip the regex, so
			// the check still passes.  This pins that the alternation group is
			// respected rather than matching any WORKCELL_*_BIN=.
			name: "unrelated WORKCELL_FOO_BIN does not trip regex",
			body: configSafetyHappyLauncher + "\nWORKCELL_FOO_BIN=/usr/bin/foo\n",
		},
		{
			// kindAbsent: unsafe YAML.load_file present trips check #2.
			name:    "YAML.load_file present",
			body:    configSafetyHappyLauncher + "\nprofile = YAML.load_file(path)\n",
			wantErr: "scripts/workcell still uses unsafe YAML.load_file parsing for managed profile validation",
		},
		{
			// kindPresent: the COLIMA_HOME pin removed (COLIMA_STATE_ROOT
			// assignment still present) fails the second half of the guard
			// with the shared message.
			name:    "missing COLIMA_HOME pin",
			body:    strings.Replace(configSafetyHappyLauncher, `COLIMA_HOME="${COLIMA_STATE_ROOT}" `, "", 1),
			wantErr: "Expected scripts/workcell to pin Colima state operations to one COLIMA_HOME root",
		},
		{
			// kindPresent: the COLIMA_STATE_ROOT assignment removed fails the
			// first half of the guard with the same shared message.
			name:    "missing COLIMA_STATE_ROOT assignment",
			body:    strings.Replace(configSafetyHappyLauncher, "COLIMA_STATE_ROOT=", "STATE_ROOT=", 1),
			wantErr: "Expected scripts/workcell to pin Colima state operations to one COLIMA_HOME root",
		},
		{
			// kindPresent: the REAL_HOME assignment removed fails check #4.
			name:    "missing REAL_HOME",
			body:    strings.Replace(configSafetyHappyLauncher, "REAL_HOME=", "HOME_DIR=", 1),
			wantErr: "Expected scripts/workcell to derive the real host home independently of caller HOME",
		},
		{
			// A missing launcher is empty content: the negative checks pass
			// and the first affirmative check (COLIMA_STATE_ROOT pin) fires,
			// mirroring `rg -q` returning non-zero on a missing file.
			name:    "missing launcher",
			body:    "",
			wantErr: "Expected scripts/workcell to pin Colima state operations to one COLIMA_HOME root",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := writeLauncher(t, tc.body)
			err := CheckConfigSafety(root)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("CheckConfigSafety() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("CheckConfigSafety() = nil, want error %q", tc.wantErr)
			}
			if err.Error() != tc.wantErr {
				t.Fatalf("CheckConfigSafety() error = %q, want %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestCheckConfigSafetyRealRepo(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	if _, err := os.Stat(filepath.Join(repoRoot, launcherRelPath)); err != nil {
		t.Skipf("real scripts/workcell not found at %s: %v", repoRoot, err)
	}
	if err := CheckConfigSafety(repoRoot); err != nil {
		t.Fatalf("CheckConfigSafety(real repo) = %v, want nil", err)
	}
}

// runtimeHappyLauncher is a minimal scripts/workcell that satisfies all
// ten runtime/gc invariants: the trusted Docker client seed, no
// DOCKER_CONFIG pin to the real host home, the buildx_cmd invocation, a
// runtime_build_codex_arch body resolving both musl assets (and no gnu
// asset), both hidden probes, both --gc cleanup helpers, the strict-mode
// rebuild rejection, and both go_colimautil validators.  Individual
// negative cases mutate one property of this baseline.
const runtimeHappyLauncher = `#!/bin/bash
set -euo pipefail

setup_workcell_trusted_docker_client() {
  DOCKER_CONFIG="${TRUSTED_DOCKER_CONFIG}"
}

runtime_build_image() {
  buildx_cmd build --tag workcell/runtime .
}

runtime_build_codex_arch() {
  case "${server_arch}" in
    arm64 | aarch64) printf 'aarch64-unknown-linux-musl\n' ;;
    amd64 | x86_64) printf 'x86_64-unknown-linux-musl\n' ;;
    *) return 1 ;;
  esac
}

handle_flags() {
  case "$1" in
    --self-docker-probe) run_self_docker_probe ;;
    --self-staging-probe) run_self_staging_probe ;;
  esac
}

gc() {
  prune_runtime_image_cache_dir
  cleanup_workcell_temp_root
}

reject_rebuild() {
  die "strict mode requires --prepare when you explicitly request --rebuild."
}

validate_managed_config() {
  go_colimautil validate-profile-config "${config}"
  go_colimautil validate-runtime-mounts "${config}"
}
`

func TestCheckRuntimeInvariants(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr string // "" means expect success
	}{
		{
			name: "happy path all invariants hold",
			body: runtimeHappyLauncher,
		},
		{
			// kindPresent: trusted Docker client seed removed.
			name:    "missing trusted Docker client seed",
			body:    strings.Replace(runtimeHappyLauncher, "setup_workcell_trusted_docker_client", "setup_other", 1),
			wantErr: "Expected scripts/workcell to seed a trusted Docker client state before host Docker use",
		},
		{
			// kindAbsent: pinning DOCKER_CONFIG to the real host home is a
			// violation.
			name:    "DOCKER_CONFIG pinned to real home present",
			body:    runtimeHappyLauncher + "\nDOCKER_CONFIG=\"${REAL_HOME}/.docker\"\n",
			wantErr: "scripts/workcell still pins DOCKER_CONFIG to the real host home",
		},
		{
			// kindPresent: buildx_cmd build removed.
			name:    "missing buildx_cmd build",
			body:    strings.Replace(runtimeHappyLauncher, "buildx_cmd build", "buildx build", 1),
			wantErr: "Expected scripts/workcell to invoke buildx through the trusted absolute plugin path",
		},
		{
			// kindFunctionBlock: aarch64 musl asset removed from the block.
			name:    "missing aarch64 musl asset",
			body:    strings.Replace(runtimeHappyLauncher, "aarch64-unknown-linux-musl", "aarch64-unknown-linux-foo", 1),
			wantErr: "Expected scripts/workcell Codex release probe to resolve musl release assets",
		},
		{
			// kindFunctionBlock: x86_64 musl asset removed from the block.
			name:    "missing x86_64 musl asset",
			body:    strings.Replace(runtimeHappyLauncher, "x86_64-unknown-linux-musl", "x86_64-unknown-linux-foo", 1),
			wantErr: "Expected scripts/workcell Codex release probe to resolve musl release assets",
		},
		{
			// kindFunctionBlockAbsent (the NEGATED sub-condition): a gnu asset
			// inside the block is a violation even though both musl assets
			// remain present.
			name:    "gnu asset present in codex arch block",
			body:    strings.Replace(runtimeHappyLauncher, "*) return 1 ;;", "*) printf 'x86_64-unknown-linux-gnu\\n'; return 1 ;;", 1),
			wantErr: "Expected scripts/workcell Codex release probe to resolve musl release assets",
		},
		{
			// kindFunctionBlockAbsent scoping: a gnu asset OUTSIDE the
			// runtime_build_codex_arch body must NOT trip the block-scoped
			// negative check, so the invariant still holds.
			name: "gnu asset only outside codex arch block",
			body: runtimeHappyLauncher + "\ncomment_note() { : 'x86_64-unknown-linux-gnu'; }\n",
		},
		{
			// kindPresent: hidden self-docker probe removed.
			name:    "missing self-docker probe",
			body:    strings.Replace(runtimeHappyLauncher, "--self-docker-probe", "--self-docker-off", 1),
			wantErr: "Expected scripts/workcell to expose a hidden self-docker probe for invariant testing",
		},
		{
			// kindPresent (first half of --gc guard): runtime-image cache prune
			// helper removed.
			name:    "missing runtime image cache prune",
			body:    strings.Replace(runtimeHappyLauncher, "prune_runtime_image_cache_dir", "prune_other", 1),
			wantErr: "Expected scripts/workcell --gc to cover bounded runtime-image cache and Workcell-owned temp cleanup",
		},
		{
			// kindPresent (second half of --gc guard): temp-root cleanup helper
			// removed.
			name:    "missing temp root cleanup",
			body:    strings.Replace(runtimeHappyLauncher, "cleanup_workcell_temp_root", "cleanup_other", 1),
			wantErr: "Expected scripts/workcell --gc to cover bounded runtime-image cache and Workcell-owned temp cleanup",
		},
		{
			// kindPresent: hidden self-staging probe removed.
			name:    "missing self-staging probe",
			body:    strings.Replace(runtimeHappyLauncher, "--self-staging-probe", "--self-staging-off", 1),
			wantErr: "Expected scripts/workcell to expose a hidden staging probe for invariant testing",
		},
		{
			// kindPresent: strict-mode rebuild rejection message removed.
			name:    "missing strict-mode rebuild rejection",
			body:    strings.Replace(runtimeHappyLauncher, "strict mode requires --prepare when you explicitly request --rebuild.", "strict mode message changed", 1),
			wantErr: "Expected scripts/workcell to reject explicit strict-mode image rebuild requests",
		},
		{
			// kindPresent: profile-config validator removed.
			name:    "missing validate-profile-config",
			body:    strings.Replace(runtimeHappyLauncher, "go_colimautil validate-profile-config", "go_colimautil validate-other", 1),
			wantErr: "Expected scripts/workcell to validate managed Colima config through the dedicated Go helper",
		},
		{
			// kindPresent: runtime-mounts validator removed.
			name:    "missing validate-runtime-mounts",
			body:    strings.Replace(runtimeHappyLauncher, "go_colimautil validate-runtime-mounts", "go_colimautil validate-other-mounts", 1),
			wantErr: "Expected scripts/workcell to validate managed Lima mounts through the dedicated Go helper",
		},
		{
			// A missing launcher is empty content: the negative checks pass and
			// the first affirmative check (trusted Docker client seed) fires,
			// mirroring `rg -q` returning non-zero on a missing file.
			name:    "missing launcher",
			body:    "",
			wantErr: "Expected scripts/workcell to seed a trusted Docker client state before host Docker use",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := writeLauncher(t, tc.body)
			err := CheckRuntimeInvariants(root)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("CheckRuntimeInvariants() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("CheckRuntimeInvariants() = nil, want error %q", tc.wantErr)
			}
			if err.Error() != tc.wantErr {
				t.Fatalf("CheckRuntimeInvariants() error = %q, want %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestCheckRuntimeInvariantsRealRepo(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	if _, err := os.Stat(filepath.Join(repoRoot, launcherRelPath)); err != nil {
		t.Skipf("real scripts/workcell not found at %s: %v", repoRoot, err)
	}
	if err := CheckRuntimeInvariants(repoRoot); err != nil {
		t.Fatalf("CheckRuntimeInvariants(real repo) = %v, want nil", err)
	}
}

// managedProfileStagingHappyLauncher is a minimal scripts/workcell that
// satisfies all three managed-profile staging/cleanup invariants: a
// start_managed_profile body mounting all three staging cache roots (with
// the staging-cache-root reject call), a top-level
// reject_symlinked_colima_staging_cache_roots definition, the
// prepare_colima_staging_cache_roots call inside the three preparing
// functions, no bare unbraced default-parent cleanup call, and a
// cleanup_default_injection_bundles body that captures both parents
// fail-closed before cleaning them.  Individual negative cases mutate one
// property of this baseline.
const managedProfileStagingHappyLauncher = `#!/bin/bash
set -euo pipefail

reject_symlinked_colima_staging_cache_roots() {
  : reject symlinks
}

prepare_injection_bundle() {
  prepare_colima_staging_cache_roots
}

prepare_workspace_control_plane_shadow() {
  prepare_colima_staging_cache_roots
}

start_managed_profile() {
  prepare_colima_staging_cache_roots
  host_inputs_root="${cache}/workcell-host-inputs"
  shadow_root="${cache}/workcell-shadow"
  token_handoff_root="${cache}/workcell-token-handoff"
  colima start \
    --mount "${host_inputs_root}" \
    --mount "${shadow_root}" \
    --mount "${token_handoff_root}:w"
}

cleanup_default_injection_bundles() {
  local bundle_parent token_handoff_parent
  bundle_parent="$(default_injection_bundle_parent)" || return $?
  token_handoff_parent="$(default_copilot_token_handoff_parent)" || return $?
  cleanup_stale_injection_bundles "${bundle_parent}"
  cleanup_stale_injection_bundles "${token_handoff_parent}"
}
`

func TestCheckManagedProfileStaging(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr string // "" means expect success
	}{
		{
			name: "happy path all invariants hold",
			body: managedProfileStagingHappyLauncher,
		},
		{
			// kindFunctionBlock: host-inputs mount root removed from the
			// start_managed_profile body (guard 1).
			name:    "missing host-inputs mount root",
			body:    strings.Replace(managedProfileStagingHappyLauncher, `--mount "${host_inputs_root}" \`+"\n", "", 1),
			wantErr: "Expected managed Colima launch to mount Workcell staging cache roots with reviewed access modes",
		},
		{
			// kindFunctionBlock: token-handoff write mount removed (guard 1).
			name:    "missing token-handoff write mount",
			body:    strings.Replace(managedProfileStagingHappyLauncher, `--mount "${token_handoff_root}:w"`, `--mount "${token_handoff_root}"`, 1),
			wantErr: "Expected managed Colima launch to mount Workcell staging cache roots with reviewed access modes",
		},
		{
			// kindFunctionBlock scoping: the workcell-shadow cache-root name
			// exists in the file but OUTSIDE start_managed_profile, so the
			// block-scoped guard must still fail.
			name: "shadow root only outside start_managed_profile body",
			body: strings.Replace(managedProfileStagingHappyLauncher, `  shadow_root="${cache}/workcell-shadow"`+"\n", "", 1) +
				"\ndecoy() {\n  echo workcell-shadow\n}\n",
			wantErr: "Expected managed Colima launch to mount Workcell staging cache roots with reviewed access modes",
		},
		{
			// kindPresent: the reject_symlinked_colima_staging_cache_roots
			// helper removed entirely (guard 2, whole-file probe).
			name:    "missing reject_symlinked helper",
			body:    strings.Replace(managedProfileStagingHappyLauncher, "reject_symlinked_colima_staging_cache_roots", "reject_other", 2),
			wantErr: "Expected Workcell staging cache roots to reject symlinked host components before staging or mounting",
		},
		{
			// kindFunctionBlock: prepare_colima_staging_cache_roots call removed
			// from prepare_injection_bundle (guard 2), while the other functions
			// keep theirs.
			name: "missing staging-cache-root call in prepare_injection_bundle",
			body: strings.Replace(managedProfileStagingHappyLauncher,
				"prepare_injection_bundle() {\n  prepare_colima_staging_cache_roots\n}",
				"prepare_injection_bundle() {\n  :\n}", 1),
			wantErr: "Expected Workcell staging cache roots to reject symlinked host components before staging or mounting",
		},
		{
			// kindAbsent: a bare unbraced default-parent cleanup call is a
			// violation (guard 3), even though every fail-closed probe remains.
			name:    "bare default-parent cleanup call present",
			body:    managedProfileStagingHappyLauncher + "\ncleanup_stale_injection_bundles \"$(default_injection_bundle_parent)\"\n",
			wantErr: "Expected stale injection cleanup to fail closed when the default bundle parent is rejected",
		},
		{
			// kindFunctionBlock: the fail-closed bundle_parent capture removed
			// from cleanup_default_injection_bundles (guard 3).
			name:    "missing fail-closed bundle_parent capture",
			body:    strings.Replace(managedProfileStagingHappyLauncher, `  bundle_parent="$(default_injection_bundle_parent)" || return $?`+"\n", "", 1),
			wantErr: "Expected stale injection cleanup to fail closed when the default bundle parent is rejected",
		},
		{
			// kindFunctionBlock scoping: the fail-closed token-handoff capture
			// exists OUTSIDE cleanup_default_injection_bundles, so the
			// block-scoped guard must still fail.
			name: "token-handoff capture only outside cleanup function",
			body: strings.Replace(managedProfileStagingHappyLauncher,
				`  token_handoff_parent="$(default_copilot_token_handoff_parent)" || return $?`+"\n", "", 1) +
				"\ndecoy() {\n  token_handoff_parent=\"$(default_copilot_token_handoff_parent)\" || return $?\n}\n",
			wantErr: "Expected stale injection cleanup to fail closed when the default bundle parent is rejected",
		},
		{
			// A missing launcher is empty content: the negative (kindAbsent)
			// guard-3 probe passes but the first affirmative check (guard 1's
			// host-inputs mount) fires, mirroring the shell's first `if`.
			name:    "missing launcher",
			body:    "",
			wantErr: "Expected managed Colima launch to mount Workcell staging cache roots with reviewed access modes",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := writeLauncher(t, tc.body)
			err := CheckManagedProfileStaging(root)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("CheckManagedProfileStaging() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("CheckManagedProfileStaging() = nil, want error %q", tc.wantErr)
			}
			if err.Error() != tc.wantErr {
				t.Fatalf("CheckManagedProfileStaging() error = %q, want %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestCheckManagedProfileStagingRealRepo(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	if _, err := os.Stat(filepath.Join(repoRoot, launcherRelPath)); err != nil {
		t.Skipf("real scripts/workcell not found at %s: %v", repoRoot, err)
	}
	if err := CheckManagedProfileStaging(repoRoot); err != nil {
		t.Fatalf("CheckManagedProfileStaging(real repo) = %v, want nil", err)
	}
}

// bootstrapEgressHappyLauncher is a minimal scripts/workcell that
// satisfies the eight launcher-scoped bootstrap egress invariants: both
// Debian snapshot mirrors and both Docker blob-storage CDNs on :443, no
// unused static.rust-lang.org:443 or snapshot.debian.org:80 egress, the
// resolve_copilot_release_url helper, and the --build-arg pass-through.
// Individual negative cases mutate one property of this baseline.
const bootstrapEgressHappyLauncher = `#!/bin/bash
set -euo pipefail

bootstrap_egress_endpoints() {
  cat <<'EOF'
snapshot.debian.org:443
snapshot-cloudflare.debian.org:443
docker-images-prod.abc123.r2.cloudflarestorage.com:443
production.cloudfront.docker.com:443
EOF
}

resolve_copilot_release_url() {
  : resolve on host
}

runtime_build() {
  buildx_cmd build --build-arg "COPILOT_RELEASE_URL=${copilot_release_url}" .
}
`

// bootstrapEgressHappyDockerfile is a minimal runtime/container/Dockerfile
// that satisfies the one Dockerfile-scoped invariant: a line-anchored
// `ARG COPILOT_RELEASE_URL=` override.
const bootstrapEgressHappyDockerfile = `# syntax=docker/dockerfile:1
FROM debian:trixie-slim
ARG COPILOT_RELEASE_URL=https://example.invalid/copilot.tar.gz
RUN : install
`

// writeBootstrapRepo materializes a fake repo with scripts/workcell set to
// launcher and runtime/container/Dockerfile set to dockerfile; a body of ""
// means "do not create that file" (unreadable-target case).
func writeBootstrapRepo(t *testing.T, launcher, dockerfile string) string {
	t.Helper()
	root := t.TempDir()
	write := func(rel, body string) {
		if body == "" {
			return
		}
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	write(launcherRelPath, launcher)
	write(dockerfileRelPath, dockerfile)
	return root
}

func TestCheckBootstrapEgress(t *testing.T) {
	tests := []struct {
		name       string
		launcher   string
		dockerfile string
		wantErr    string // "" means expect success
	}{
		{
			name:       "happy path all invariants hold",
			launcher:   bootstrapEgressHappyLauncher,
			dockerfile: bootstrapEgressHappyDockerfile,
		},
		{
			// kindPresent: the snapshot.debian.org:443 endpoint removed.
			name:       "missing snapshot.debian.org endpoint",
			launcher:   strings.Replace(bootstrapEgressHappyLauncher, "snapshot.debian.org:443\n", "", 1),
			dockerfile: bootstrapEgressHappyDockerfile,
			wantErr:    "Expected scripts/workcell bootstrap endpoints to allow snapshot.debian.org",
		},
		{
			// kindPresent: the snapshot-cloudflare mirror removed.
			name:       "missing snapshot-cloudflare mirror",
			launcher:   strings.Replace(bootstrapEgressHappyLauncher, "snapshot-cloudflare.debian.org:443\n", "", 1),
			dockerfile: bootstrapEgressHappyDockerfile,
			wantErr:    "Expected scripts/workcell bootstrap endpoints to allow the snapshot-cloudflare.debian.org CDN mirror",
		},
		{
			// kindAbsent: an unused static.rust-lang.org:443 egress entry is a
			// violation.
			name:       "static.rust-lang.org egress present",
			launcher:   bootstrapEgressHappyLauncher + "static.rust-lang.org:443\n",
			dockerfile: bootstrapEgressHappyDockerfile,
			wantErr:    "Expected scripts/workcell bootstrap endpoints to avoid unused static.rust-lang.org egress",
		},
		{
			// kindRegexPresent: the R2 host removed entirely.
			name:       "missing R2 blob-storage host",
			launcher:   strings.Replace(bootstrapEgressHappyLauncher, "docker-images-prod.abc123.r2.cloudflarestorage.com:443\n", "", 1),
			dockerfile: bootstrapEgressHappyDockerfile,
			wantErr:    "Expected scripts/workcell bootstrap endpoints to allow Docker blob storage on Cloudflare R2",
		},
		{
			// kindRegexPresent semantics: the `[^.]+` subdomain wildcard spans
			// exactly one dotless segment, so a host with an extra dotted
			// segment (docker-images-prod.a.b.r2...) does NOT match and the
			// check fails, pinning that the wildcard is not `.*`.
			name:       "R2 host with dotted subdomain does not match",
			launcher:   strings.Replace(bootstrapEgressHappyLauncher, "docker-images-prod.abc123.r2.cloudflarestorage.com:443", "docker-images-prod.a.b.r2.cloudflarestorage.com:443", 1),
			dockerfile: bootstrapEgressHappyDockerfile,
			wantErr:    "Expected scripts/workcell bootstrap endpoints to allow Docker blob storage on Cloudflare R2",
		},
		{
			// kindRegexPresent negative control: a single dotless subdomain
			// segment other than abc123 still matches, so the invariant holds.
			name:       "R2 host with different single subdomain still matches",
			launcher:   strings.Replace(bootstrapEgressHappyLauncher, "docker-images-prod.abc123.r2.cloudflarestorage.com:443", "docker-images-prod.xyz789.r2.cloudflarestorage.com:443", 1),
			dockerfile: bootstrapEgressHappyDockerfile,
		},
		{
			// kindPresent: the CloudFront blob-storage host removed.
			name:       "missing CloudFront blob-storage host",
			launcher:   strings.Replace(bootstrapEgressHappyLauncher, "production.cloudfront.docker.com:443\n", "", 1),
			dockerfile: bootstrapEgressHappyDockerfile,
			wantErr:    "Expected scripts/workcell bootstrap endpoints to allow Docker blob storage on CloudFront",
		},
		{
			// kindRegexPresent against the Dockerfile: the ARG override line
			// removed.
			name:       "missing Dockerfile ARG override",
			launcher:   bootstrapEgressHappyLauncher,
			dockerfile: strings.Replace(bootstrapEgressHappyDockerfile, "ARG COPILOT_RELEASE_URL=https://example.invalid/copilot.tar.gz\n", "", 1),
			wantErr:    "Expected runtime Dockerfile to accept a host-resolved Copilot release URL override",
		},
		{
			// kindRegexPresent multiline anchoring: the ARG text appears in the
			// Dockerfile but NOT at a line start (embedded in a RUN echo), so
			// the `(?m)^ARG ...` anchor must still fail.
			name:     "Dockerfile ARG text not at line start",
			launcher: bootstrapEgressHappyLauncher,
			dockerfile: "# syntax=docker/dockerfile:1\nFROM debian:trixie-slim\n" +
				"RUN echo \"ARG COPILOT_RELEASE_URL=set-at-runtime\"\n",
			wantErr: "Expected runtime Dockerfile to accept a host-resolved Copilot release URL override",
		},
		{
			// A missing Dockerfile is empty content: the launcher-scoped checks
			// pass but the Dockerfile-scoped ARG regex fails, mirroring `rg -q`
			// returning non-zero on a missing file.  Exercises the per-check
			// target-file read against a distinct absent file.
			name:       "missing Dockerfile",
			launcher:   bootstrapEgressHappyLauncher,
			dockerfile: "",
			wantErr:    "Expected runtime Dockerfile to accept a host-resolved Copilot release URL override",
		},
		{
			// kindPresent: the resolve_copilot_release_url helper removed.
			name:       "missing resolve_copilot_release_url helper",
			launcher:   strings.Replace(bootstrapEgressHappyLauncher, "resolve_copilot_release_url()", "resolve_other()", 1),
			dockerfile: bootstrapEgressHappyDockerfile,
			wantErr:    "Expected scripts/workcell to resolve Copilot release URLs on the host before runtime builds",
		},
		{
			// kindPresent: the --build-arg Copilot release URL pass-through
			// removed.
			name:       "missing Copilot release URL build-arg",
			launcher:   strings.Replace(bootstrapEgressHappyLauncher, `--build-arg "COPILOT_RELEASE_URL=${copilot_release_url}"`, `--build-arg "OTHER=${copilot_release_url}"`, 1),
			dockerfile: bootstrapEgressHappyDockerfile,
			wantErr:    "Expected scripts/workcell runtime builds to pass host-resolved Copilot release URLs into Docker",
		},
		{
			// kindAbsent: an unused snapshot.debian.org:80 (plaintext) egress
			// entry is a violation, even though snapshot.debian.org:443 remains.
			name:       "snapshot.debian.org:80 egress present",
			launcher:   bootstrapEgressHappyLauncher + "snapshot.debian.org:80\n",
			dockerfile: bootstrapEgressHappyDockerfile,
			wantErr:    "Expected scripts/workcell bootstrap endpoints to avoid unused snapshot.debian.org:80 egress",
		},
		{
			// A missing launcher is empty content: the negative checks pass and
			// the first affirmative check (snapshot.debian.org:443) fires,
			// mirroring `rg -q` returning non-zero on a missing file.
			name:       "missing launcher",
			launcher:   "",
			dockerfile: bootstrapEgressHappyDockerfile,
			wantErr:    "Expected scripts/workcell bootstrap endpoints to allow snapshot.debian.org",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := writeBootstrapRepo(t, tc.launcher, tc.dockerfile)
			err := CheckBootstrapEgress(root)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("CheckBootstrapEgress() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("CheckBootstrapEgress() = nil, want error %q", tc.wantErr)
			}
			if err.Error() != tc.wantErr {
				t.Fatalf("CheckBootstrapEgress() error = %q, want %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestCheckBootstrapEgressRealRepo(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	if _, err := os.Stat(filepath.Join(repoRoot, launcherRelPath)); err != nil {
		t.Skipf("real scripts/workcell not found at %s: %v", repoRoot, err)
	}
	if err := CheckBootstrapEgress(repoRoot); err != nil {
		t.Fatalf("CheckBootstrapEgress(real repo) = %v, want nil", err)
	}
}

// bootstrapAuditHappyLauncher is a minimal scripts/workcell that satisfies
// all three bootstrap-audit-metadata invariants: the two audit-record
// fields (bootstrap_applied and bootstrap_endpoints) and the temporary
// bootstrap network policy activation announcement.  Individual negative
// cases mutate one property of this baseline.  Literal B (the
// bootstrap_endpoints field) is reproduced here byte-for-byte from
// scripts/workcell so a mis-transcription is caught by both the negative
// cases and TestCheckBootstrapAuditMetadataRealRepo.
const bootstrapAuditHappyLauncher = `#!/bin/bash
set -euo pipefail

write_audit_record() {
  emit_record \
    "bootstrap_applied=${BOOTSTRAP_APPLIED}" \
    "bootstrap_endpoints=$([[ "${BOOTSTRAP_APPLIED}" -eq 1 ]] && printf '%s' "${BOOTSTRAP_ENDPOINTS}" || printf '')"
}

announce_bootstrap_policy() {
  printf 'bootstrap_policy=allowlist endpoints=%s\n' "${BOOTSTRAP_ENDPOINTS}" >&2
}
`

func TestCheckBootstrapAuditMetadata(t *testing.T) {
	tests := []struct {
		name     string
		launcher string
		wantErr  string // "" means expect success
	}{
		{
			name:     "happy path all invariants hold",
			launcher: bootstrapAuditHappyLauncher,
		},
		{
			// kindPresent (Literal A): the bootstrap_applied audit field removed.
			name:     "missing bootstrap_applied field",
			launcher: strings.Replace(bootstrapAuditHappyLauncher, `"bootstrap_applied=${BOOTSTRAP_APPLIED}"`, `"other_applied=${BOOTSTRAP_APPLIED}"`, 1),
			wantErr:  "Expected scripts/workcell audit records to include bootstrap network metadata",
		},
		{
			// kindPresent (Literal B): the bootstrap_endpoints audit field removed.
			name:     "missing bootstrap_endpoints field",
			launcher: strings.Replace(bootstrapAuditHappyLauncher, `bootstrap_endpoints=$([[ "${BOOTSTRAP_APPLIED}" -eq 1 ]] && printf '%s' "${BOOTSTRAP_ENDPOINTS}" || printf '')`, `bootstrap_endpoints=`, 1),
			wantErr:  "Expected scripts/workcell audit records to include bootstrap network metadata",
		},
		{
			// The audit field must be QUOTED: dropping the leading `"` (which the
			// old `rg` pattern required) leaves an unquoted command substitution
			// that word-splits the endpoint list — the check must still fail.
			name:     "unquoted bootstrap_endpoints field",
			launcher: strings.Replace(bootstrapAuditHappyLauncher, `"bootstrap_endpoints=$(`, `bootstrap_endpoints=$(`, 1),
			wantErr:  "Expected scripts/workcell audit records to include bootstrap network metadata",
		},
		{
			// kindPresent: the bootstrap-policy activation announcement removed.
			name:     "missing bootstrap policy announcement",
			launcher: strings.Replace(bootstrapAuditHappyLauncher, "bootstrap_policy=allowlist endpoints=%s", "bootstrap_policy=off endpoints=%s", 1),
			wantErr:  "Expected scripts/workcell to announce temporary bootstrap network policy activation",
		},
		{
			// A missing launcher is empty content: the first affirmative check
			// (bootstrap_applied) fires, mirroring `rg -q` returning non-zero on a
			// missing file.
			name:     "missing launcher",
			launcher: "",
			wantErr:  "Expected scripts/workcell audit records to include bootstrap network metadata",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := writeBootstrapRepo(t, tc.launcher, "")
			err := CheckBootstrapAuditMetadata(root)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("CheckBootstrapAuditMetadata() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("CheckBootstrapAuditMetadata() = nil, want error %q", tc.wantErr)
			}
			if err.Error() != tc.wantErr {
				t.Fatalf("CheckBootstrapAuditMetadata() error = %q, want %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// TestCheckBootstrapAuditMetadataRealRepo asserts that the real
// scripts/workcell in this repository satisfies all three
// bootstrap-audit-metadata invariants.  This is the key guard against a
// mis-transcribed Literal B: if the Go pattern is not a byte-exact
// substring of the actual audit record, this test fails with the guard's
// stderr message.
func TestCheckBootstrapAuditMetadataRealRepo(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	if _, err := os.Stat(filepath.Join(repoRoot, launcherRelPath)); err != nil {
		t.Skipf("real scripts/workcell not found at %s: %v", repoRoot, err)
	}
	if err := CheckBootstrapAuditMetadata(repoRoot); err != nil {
		t.Fatalf("CheckBootstrapAuditMetadata(real repo) = %v, want nil", err)
	}
}

// gitIndexShadowHappyLauncher is a minimal scripts/workcell that satisfies
// all five git-index shadow invariants: the two regex checks and the
// partial-file cleanup inside git_index_materialize_regular_file, the unsafe
// index-path rejection inside git_index_populate_shadow_dir, and the shared
// blocked-key matcher reuse inside sanitize_shadowed_git_config.  Individual
// negative cases mutate one property of this baseline.
const gitIndexShadowHappyLauncher = `#!/bin/bash
set -euo pipefail

git_index_materialize_regular_file() {
  local workspace="$1" oid="$2" destination_path="$3" relative_path="$4"
  if ! run_clean_host_command git -C "${workspace}" cat-file blob "${oid}" >"${destination_path}"; then
    rm -f "${destination_path}"
    echo "Workcell blocked shadow materialization: failed to read tracked blob ${oid} for ${relative_path}" >&2
    return 1
  fi
}

git_index_populate_shadow_dir() {
  local index_path="$1"
  case "${index_path}" in
    '' | /* | */../* | ../* | */..)
      return 1
      ;;
  esac
}

sanitize_shadowed_git_config() {
  local key="$1"
  if git_config_key_is_blocked "${key}"; then
    return 1
  fi
}
`

func TestCheckGitIndexShadow(t *testing.T) {
	tests := []struct {
		name     string
		launcher string
		wantErr  string // "" means expect success
	}{
		{
			name:     "happy path all invariants hold",
			launcher: gitIndexShadowHappyLauncher,
		},
		{
			// kindFunctionBlockRegex: the cat-file blob materialization removed.
			name:     "missing cat-file blob",
			launcher: strings.Replace(gitIndexShadowHappyLauncher, "cat-file blob", "checkout-index", 1),
			wantErr:  "Expected git_index_materialize_regular_file to materialize tracked blobs without checkout-index",
		},
		{
			// kindFunctionBlockRegex: the fail-closed message removed.
			name:     "missing failed to read tracked blob",
			launcher: strings.Replace(gitIndexShadowHappyLauncher, "failed to read tracked blob", "read tracked blob", 1),
			wantErr:  "Expected git_index_materialize_regular_file to fail closed when a tracked control-plane blob is unreadable",
		},
		{
			// kindFunctionBlock: the partial-file cleanup removed.
			name:     "missing partial-file cleanup",
			launcher: strings.Replace(gitIndexShadowHappyLauncher, `rm -f "${destination_path}"`, `true`, 1),
			wantErr:  "Expected git_index_materialize_regular_file to remove partially materialized files after blob read failures",
		},
		{
			// kindFunctionBlock: the unsafe index-path rejection removed.
			name:     "missing unsafe index path rejection",
			launcher: strings.Replace(gitIndexShadowHappyLauncher, `*/../*`, `*/ok/*`, 1),
			wantErr:  "Expected git_index_populate_shadow_dir to reject unsafe index paths before shadow materialization",
		},
		{
			// kindFunctionBlockRegex: the shared blocked-key matcher reuse removed.
			name:     "missing shared blocked-key matcher",
			launcher: strings.Replace(gitIndexShadowHappyLauncher, "git_config_key_is_blocked", "always_false", 2),
			wantErr:  "Expected sanitize_shadowed_git_config to reuse the shared blocked git-config key matcher",
		},
		{
			// Scoping proof: the cat-file blob needle present in a DIFFERENT
			// function body does not satisfy the git_index_materialize_regular_file
			// check, because extract_named_function_block scopes to the named
			// function.  Here the needle is moved out of
			// git_index_materialize_regular_file into an unrelated helper.
			name: "cat-file blob only in a different function",
			launcher: strings.Replace(
				strings.Replace(gitIndexShadowHappyLauncher, "cat-file blob", "checkout-index", 1),
				"set -euo pipefail",
				"set -euo pipefail\n\nunrelated_helper() {\n  git cat-file blob HEAD\n}",
				1,
			),
			wantErr: "Expected git_index_materialize_regular_file to materialize tracked blobs without checkout-index",
		},
		{
			// A missing launcher is empty content: the first affirmative check
			// (cat-file blob) fires, mirroring function_block_contains_regex
			// returning non-zero on a missing file.
			name:     "missing launcher",
			launcher: "",
			wantErr:  "Expected git_index_materialize_regular_file to materialize tracked blobs without checkout-index",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := writeLauncher(t, tc.launcher)
			err := CheckGitIndexShadow(root)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("CheckGitIndexShadow() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("CheckGitIndexShadow() = nil, want error %q", tc.wantErr)
			}
			if err.Error() != tc.wantErr {
				t.Fatalf("CheckGitIndexShadow() error = %q, want %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// TestCheckGitIndexShadowRealRepo asserts that the real scripts/workcell in
// this repository satisfies all five git-index shadow invariants, guarding
// against a mis-transcribed needle or function name.
func TestCheckGitIndexShadowRealRepo(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	if _, err := os.Stat(filepath.Join(repoRoot, launcherRelPath)); err != nil {
		t.Skipf("real scripts/workcell not found at %s: %v", repoRoot, err)
	}
	if err := CheckGitIndexShadow(repoRoot); err != nil {
		t.Fatalf("CheckGitIndexShadow(real repo) = %v, want nil", err)
	}
}

// publishPrShadowHappyLauncher is a minimal scripts/workcell that satisfies
// all four publish-PR / shadow-mount invariants: the core.hooksPath and
// --no-verify regex checks inside publish_pr_main, the symlink-free copy helper
// inside add_shadow_git_hooks_mount, and the symlink guard inside
// add_shadow_git_config_mount.  Individual negative cases mutate one property
// of this baseline.
const publishPrShadowHappyLauncher = `#!/bin/bash
set -euo pipefail

publish_pr_main() {
  git -c core.hooksPath=/dev/null commit --no-verify -m "publish"
  git -c core.hooksPath=/dev/null push --no-verify
}

add_shadow_git_hooks_mount() {
  local source_path="$1" source="$2"
  copy_tree_without_symlinks "${source_path}" "${source}"
}

add_shadow_git_config_mount() {
  local source_path="$1"
  if [[ -f "${source_path}" && ! -L "${source_path}" ]]; then
    return 0
  fi
}
`

func TestCheckPublishPrShadowMounts(t *testing.T) {
	tests := []struct {
		name     string
		launcher string
		wantErr  string // "" means expect success
	}{
		{
			name:     "happy path all invariants hold",
			launcher: publishPrShadowHappyLauncher,
		},
		{
			// kindFunctionBlockRegex: the repo-hooks disable removed.
			name:     "missing core.hooksPath disable",
			launcher: strings.Replace(publishPrShadowHappyLauncher, "core.hooksPath=/dev/null", "core.editor=true", 2),
			wantErr:  "Expected publish_pr_main to disable repo hooks for host-side publish git commands",
		},
		{
			// kindFunctionBlockRegex: the explicit hook bypass removed.
			name:     "missing --no-verify bypass",
			launcher: strings.Replace(publishPrShadowHappyLauncher, "--no-verify", "--verbose", 2),
			wantErr:  "Expected publish_pr_main to bypass repo hooks explicitly on host-side commit and push",
		},
		{
			// kindFunctionBlock: the symlink-free copy helper removed.
			name:     "missing symlink-free hooks copy",
			launcher: strings.Replace(publishPrShadowHappyLauncher, "copy_tree_without_symlinks", "cp -r", 1),
			wantErr:  "Expected add_shadow_git_hooks_mount to avoid copying symlinked hook content into the readonly shadow",
		},
		{
			// kindFunctionBlock: the symlinked-config guard removed.
			name:     "missing symlinked git config guard",
			launcher: strings.Replace(publishPrShadowHappyLauncher, `! -L "${source_path}"`, `-e "${source_path}"`, 1),
			wantErr:  "Expected add_shadow_git_config_mount to ignore symlinked git config files",
		},
		{
			// Scoping proof: the core.hooksPath needle present in a DIFFERENT
			// function body does not satisfy the publish_pr_main check, because
			// extract_named_function_block scopes to the named function.  Here the
			// needle is removed from publish_pr_main and reintroduced in an
			// unrelated helper.
			name: "core.hooksPath only in a different function",
			launcher: strings.Replace(
				strings.Replace(publishPrShadowHappyLauncher, "core.hooksPath=/dev/null", "core.editor=true", 2),
				"set -euo pipefail",
				"set -euo pipefail\n\nunrelated_helper() {\n  git -c core.hooksPath=/dev/null status\n}",
				1,
			),
			wantErr: "Expected publish_pr_main to disable repo hooks for host-side publish git commands",
		},
		{
			// A missing launcher is empty content: the first affirmative check
			// (core.hooksPath disable) fires, mirroring function_block_contains_regex
			// returning non-zero on a missing file.
			name:     "missing launcher",
			launcher: "",
			wantErr:  "Expected publish_pr_main to disable repo hooks for host-side publish git commands",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := writeLauncher(t, tc.launcher)
			err := CheckPublishPrShadowMounts(root)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("CheckPublishPrShadowMounts() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("CheckPublishPrShadowMounts() = nil, want error %q", tc.wantErr)
			}
			if err.Error() != tc.wantErr {
				t.Fatalf("CheckPublishPrShadowMounts() error = %q, want %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// TestCheckPublishPrShadowMountsRealRepo asserts that the real scripts/workcell
// in this repository satisfies all four publish-PR / shadow-mount invariants,
// guarding against a mis-transcribed needle or function name.
func TestCheckPublishPrShadowMountsRealRepo(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	if _, err := os.Stat(filepath.Join(repoRoot, launcherRelPath)); err != nil {
		t.Skipf("real scripts/workcell not found at %s: %v", repoRoot, err)
	}
	if err := CheckPublishPrShadowMounts(repoRoot); err != nil {
		t.Fatalf("CheckPublishPrShadowMounts(real repo) = %v, want nil", err)
	}
}

// shadowEnumEgressHappyLauncher is a minimal scripts/workcell that satisfies
// the five launcher-scoped shadow-enumeration invariants: the whole-file .git
// enumeration and all four submodule find-snippet needles.  Individual
// negative cases mutate one property of this baseline.  The needles are
// reproduced here byte-for-byte from scripts/workcell so a mis-transcription
// is caught by both the negative cases and TestCheckShadowEnumEgressRealRepo.
const shadowEnumEgressHappyLauncher = `#!/bin/bash
set -euo pipefail

prepare_workspace_control_plane_shadow() {
  find "${workspace}" -type d -name .git -prune -print0
  find "${workspace}/${git_rel}/modules" \
    \( -type f -o -type l \) -name hooks \
    -o \( -type f -o -type l \) \( -name config -o -name config.worktree \) \
    -o \( -type f -o -type l \) -name worktrees
}
`

// shadowEnumEgressHappyColima is a minimal scripts/colima-egress-allowlist.sh
// that satisfies the two IPv6-egress invariants: it does not silently disable
// IPv6 and it emits the ip6tables fail-closed message.  Individual negative
// cases mutate one property of this baseline.
const shadowEnumEgressHappyColima = `#!/bin/bash
set -euo pipefail

enforce_allowlist() {
  if ! have_ip6tables; then
    echo "requires ip6tables support to enforce dual-stack allowlist egress policy" >&2
    return 1
  fi
}
`

// writeShadowEnumEgressRepo materializes a fake repo with scripts/workcell set
// to launcher and scripts/colima-egress-allowlist.sh set to colima; a body of
// "" means "do not create that file" (unreadable-target case).
func writeShadowEnumEgressRepo(t *testing.T, launcher, colima string) string {
	t.Helper()
	root := t.TempDir()
	write := func(rel, body string) {
		if body == "" {
			return
		}
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	write(launcherRelPath, launcher)
	write(colimaEgressAllowlistRelPath, colima)
	return root
}

func TestCheckShadowEnumEgress(t *testing.T) {
	tests := []struct {
		name     string
		launcher string
		colima   string
		wantErr  string // "" means expect success
	}{
		{
			name:     "happy path all invariants hold",
			launcher: shadowEnumEgressHappyLauncher,
			colima:   shadowEnumEgressHappyColima,
		},
		{
			// kindPresent: the whole-file .git enumeration removed.
			name:     "missing git enumeration",
			launcher: strings.Replace(shadowEnumEgressHappyLauncher, `find "${workspace}" -type d -name .git -prune -print0`, `find "${workspace}" -print0`, 1),
			colima:   shadowEnumEgressHappyColima,
			wantErr:  "Expected prepare_workspace_control_plane_shadow to enumerate only real .git directories",
		},
		{
			// kindPresent (needle 1): the modules find snippet removed.  The
			// wantErr proves the dynamic loop message text is preserved verbatim.
			name:     "missing modules find needle",
			launcher: strings.Replace(shadowEnumEgressHappyLauncher, `find "${workspace}/${git_rel}/modules" \`, `find "${workspace}/other" \`, 1),
			colima:   shadowEnumEgressHappyColima,
			wantErr:  `Expected prepare_workspace_control_plane_shadow to match snippet: find "${workspace}/${git_rel}/modules" \`,
		},
		{
			// kindPresent (needle 2): the hooks find snippet removed.
			name:     "missing hooks needle",
			launcher: strings.Replace(shadowEnumEgressHappyLauncher, `-type l \) -name hooks`, `-type l \) -name other`, 1),
			colima:   shadowEnumEgressHappyColima,
			wantErr:  `Expected prepare_workspace_control_plane_shadow to match snippet: -type l \) -name hooks`,
		},
		{
			// kindPresent (needle 3): the config/config.worktree find snippet
			// removed.
			name:     "missing config needle",
			launcher: strings.Replace(shadowEnumEgressHappyLauncher, `-type l \) \( -name config -o -name config.worktree \)`, `-type l \) \( -name other \)`, 1),
			colima:   shadowEnumEgressHappyColima,
			wantErr:  `Expected prepare_workspace_control_plane_shadow to match snippet: -type l \) \( -name config -o -name config.worktree \)`,
		},
		{
			// kindPresent (needle 4): the worktrees find snippet removed.
			name:     "missing worktrees needle",
			launcher: strings.Replace(shadowEnumEgressHappyLauncher, `-type l \) -name worktrees`, `-type l \) -name other`, 1),
			colima:   shadowEnumEgressHappyColima,
			wantErr:  `Expected prepare_workspace_control_plane_shadow to match snippet: -type l \) -name worktrees`,
		},
		{
			// kindAbsent against the colima helper: silently disabling IPv6 as a
			// fallback is a violation (present → exit 1).
			name:     "disable_ipv6 fallback present",
			launcher: shadowEnumEgressHappyLauncher,
			colima:   shadowEnumEgressHappyColima + "\ndisable_ipv6=1\n",
			wantErr:  "Workcell should not silently disable IPv6 as a fallback for allowlist enforcement",
		},
		{
			// kindPresent against the colima helper: the ip6tables fail-closed
			// message removed.
			name:     "missing ip6tables fail-closed message",
			launcher: shadowEnumEgressHappyLauncher,
			colima:   strings.Replace(shadowEnumEgressHappyColima, "requires ip6tables support to enforce dual-stack allowlist egress policy", "requires something else", 1),
			wantErr:  "Expected allowlist egress helper to fail closed when dual-stack allowlist enforcement is unavailable",
		},
		{
			// A missing launcher is empty content: the four whole-file needles
			// (and the .git enumeration) fail, so the first affirmative check
			// fires, mirroring `grep -Fq` returning non-zero on a missing file.
			name:     "missing launcher",
			launcher: "",
			colima:   shadowEnumEgressHappyColima,
			wantErr:  "Expected prepare_workspace_control_plane_shadow to enumerate only real .git directories",
		},
		{
			// A missing colima helper is empty content: the launcher-scoped
			// checks pass, the kindAbsent disable_ipv6 probe passes, but the
			// affirmative ip6tables message probe fails, mirroring `rg -q`
			// returning non-zero on a missing file.  Exercises the per-check
			// target-file read against a distinct absent file.
			name:     "missing colima helper",
			launcher: shadowEnumEgressHappyLauncher,
			colima:   "",
			wantErr:  "Expected allowlist egress helper to fail closed when dual-stack allowlist enforcement is unavailable",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := writeShadowEnumEgressRepo(t, tc.launcher, tc.colima)
			err := CheckShadowEnumEgress(root)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("CheckShadowEnumEgress() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("CheckShadowEnumEgress() = nil, want error %q", tc.wantErr)
			}
			if err.Error() != tc.wantErr {
				t.Fatalf("CheckShadowEnumEgress() error = %q, want %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// TestCheckShadowEnumEgressRealRepo asserts that the real scripts/workcell and
// scripts/colima-egress-allowlist.sh in this repository satisfy all seven
// shadow-enumeration / IPv6-egress invariants.  This is the key guard against a
// mis-transcribed find-snippet needle or colima-helper path: if any Go pattern
// is not a byte-exact substring of the actual file, this test fails with the
// guard's stderr message.
func TestCheckShadowEnumEgressRealRepo(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	if _, err := os.Stat(filepath.Join(repoRoot, launcherRelPath)); err != nil {
		t.Skipf("real scripts/workcell not found at %s: %v", repoRoot, err)
	}
	if err := CheckShadowEnumEgress(repoRoot); err != nil {
		t.Fatalf("CheckShadowEnumEgress(real repo) = %v, want nil", err)
	}
}

// TestExtractNamedFunctionBlock pins the sed-range extraction semantics
// the run_host_colima check depends on: the block runs from the `NAME()`
// opening line through the first line beginning with `}` (inclusive), and
// text after the closing brace is excluded.
func TestExtractNamedFunctionBlock(t *testing.T) {
	src := "before\nfoo() {\n  body_line\n}\nafter_foo() {\n  other\n}\n"
	got := extractNamedFunctionBlock(src, "foo")
	want := "foo() {\n  body_line\n}"
	if got != want {
		t.Fatalf("extractNamedFunctionBlock = %q, want %q", got, want)
	}
	if strings.Contains(got, "other") {
		t.Fatalf("extractNamedFunctionBlock leaked text past the closing brace: %q", got)
	}
	if extractNamedFunctionBlock(src, "absent") != "" {
		t.Fatalf("extractNamedFunctionBlock for a missing function should be empty")
	}
}

func TestRegexMatchesAnyLineIsLineBounded(t *testing.T) {
	// A negated char class must not consume a newline, mirroring ripgrep's
	// default (non-multiline) behaviour — otherwise a broken cross-line R2
	// endpoint would spuriously match.
	pat := `docker-images-prod\.[^.]+\.r2\.cloudflarestorage\.com:443`
	if !regexMatchesAnyLine(pat, "docker-images-prod.abc123.r2.cloudflarestorage.com:443") {
		t.Fatalf("expected a valid single-line R2 endpoint to match")
	}
	if regexMatchesAnyLine(pat, "docker-images-prod.\n.r2.cloudflarestorage.com:443") {
		t.Fatalf("a cross-newline R2 endpoint must NOT match (rg is line-oriented)")
	}
}

// homeSeedHappyHomeControlPlane is a minimal runtime/container/home-control-plane.sh
// that satisfies all six leading home-seeding invariants.
const homeSeedHappyHomeControlPlane = `#!/usr/bin/env bash
set -euo pipefail
workcell_seed_gemini_home() {
  : "${HOME}/.gemini/trustedFolders.json"
  workcell_reset_session_target "${HOME}/.gemini/settings.json" "Gemini settings"
  workcell_set_gemini_tool_sandbox "${HOME}/.gemini/settings.json" false
}
workcell_seed_claude_home() {
  workcell_copy_manifest_credential_file claude_auth "${HOME}/.claude/.credentials.json" || true
  workcell_copy_manifest_credential_file claude_auth "${HOME}/.claude/.claude.json" || true
  workcell_copy_manifest_credential_file claude_auth "${HOME}/.claude.json" || true
}
`

// homeSeedHappyProviderWrapper returns a minimal
// runtime/container/provider-wrapper.sh satisfying the two affirmative unset
// probes, the negated export probe (the export line is absent), and every
// copilot_env knob.  The knobs come from the package var so the fixture cannot
// drift out of sync with the checks.
func homeSeedHappyProviderWrapper() string {
	return "#!/usr/bin/env bash\nset -euo pipefail\nunset CLAUDE_CONFIG_DIR\nunset DISABLE_AUTOUPDATER\n" +
		strings.Join(copilotAmbientEnvKnobs, "\n") + "\n"
}

// homeSeedHappyDevelopmentWrapper returns a minimal
// runtime/container/development-wrapper.sh satisfying every copilot_env knob
// (the only invariants that read this second wrapper).
func homeSeedHappyDevelopmentWrapper() string {
	return "#!/usr/bin/env bash\nset -euo pipefail\n" +
		strings.Join(copilotAmbientEnvKnobs, "\n") + "\n"
}

// writeHomeSeedProviderWrapperRepo materializes a fake repo with the three
// wrapper files set to the given bodies; a body of "" means "do not create
// that file" (unreadable-target case).
func writeHomeSeedProviderWrapperRepo(t *testing.T, home, provider, development string) string {
	t.Helper()
	root := t.TempDir()
	write := func(rel, body string) {
		if body == "" {
			return
		}
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	write(homeControlPlaneRelPath, home)
	write(providerWrapperRelPath, provider)
	write(developmentWrapperRelPath, development)
	return root
}

func TestCheckHomeSeedProviderWrapper(t *testing.T) {
	firstKnob := copilotAmbientEnvKnobs[0]                            // unset GH_CONFIG_DIR
	lastKnob := copilotAmbientEnvKnobs[len(copilotAmbientEnvKnobs)-1] // unset OTEL_RESOURCE_ATTRIBUTES
	exportLine := "export HOME CODEX_HOME CLAUDE_CONFIG_DIR TMPDIR WORKCELL_MODE CODEX_PROFILE WORKCELL_AGENT_AUTONOMY WORKCELL_CONTAINER_MUTABILITY"

	tests := []struct {
		name        string
		home        string
		provider    string
		development string
		wantErr     string // "" means expect success
	}{
		{
			name:        "happy path all invariants hold",
			home:        homeSeedHappyHomeControlPlane,
			provider:    homeSeedHappyProviderWrapper(),
			development: homeSeedHappyDevelopmentWrapper(),
		},
		{
			// kindPresent (home-control-plane): trustedFolders.json removed.
			name:        "missing trustedFolders provisioning",
			home:        strings.Replace(homeSeedHappyHomeControlPlane, "trustedFolders.json", "otherFolders.json", 1),
			provider:    homeSeedHappyProviderWrapper(),
			development: homeSeedHappyDevelopmentWrapper(),
			wantErr:     "Expected Gemini home seeding to provision trustedFolders.json",
		},
		{
			// kindPresent (home-control-plane): the settings reset needle removed.
			name:        "missing settings reset",
			home:        strings.Replace(homeSeedHappyHomeControlPlane, `workcell_reset_session_target "${HOME}/.gemini/settings.json" "Gemini settings"`, "true", 1),
			provider:    homeSeedHappyProviderWrapper(),
			development: homeSeedHappyDevelopmentWrapper(),
			wantErr:     "Expected Gemini home seeding to reset settings.json through workcell_reset_session_target",
		},
		{
			// kindPresent (home-control-plane): the .credentials.json copy removed.
			name:        "missing claude credentials copy",
			home:        strings.Replace(homeSeedHappyHomeControlPlane, `workcell_copy_manifest_credential_file claude_auth "${HOME}/.claude/.credentials.json" || true`, "true", 1),
			provider:    homeSeedHappyProviderWrapper(),
			development: homeSeedHappyDevelopmentWrapper(),
			wantErr:     "Expected Claude home seeding to copy auth into .claude/.credentials.json",
		},
		{
			// kindPresent (provider-wrapper): the CLAUDE_CONFIG_DIR scrub removed.
			name:        "missing CLAUDE_CONFIG_DIR scrub",
			home:        homeSeedHappyHomeControlPlane,
			provider:    strings.Replace(homeSeedHappyProviderWrapper(), "unset CLAUDE_CONFIG_DIR", "true", 1),
			development: homeSeedHappyDevelopmentWrapper(),
			wantErr:     "Expected provider wrapper to discard caller-supplied CLAUDE_CONFIG_DIR",
		},
		{
			// kindAbsent (provider-wrapper): exporting CLAUDE_CONFIG_DIR for
			// non-Claude launches is a violation (present → exit 1).
			name:        "forbidden CLAUDE_CONFIG_DIR export present",
			home:        homeSeedHappyHomeControlPlane,
			provider:    homeSeedHappyProviderWrapper() + exportLine + "\n",
			development: homeSeedHappyDevelopmentWrapper(),
			wantErr:     "Provider wrapper should not export CLAUDE_CONFIG_DIR for non-Claude launches",
		},
		{
			// kindPresent (provider-wrapper): the DISABLE_AUTOUPDATER scrub removed.
			name:        "missing DISABLE_AUTOUPDATER scrub",
			home:        homeSeedHappyHomeControlPlane,
			provider:    strings.Replace(homeSeedHappyProviderWrapper(), "unset DISABLE_AUTOUPDATER", "true", 1),
			development: homeSeedHappyDevelopmentWrapper(),
			wantErr:     "Expected provider wrapper to discard caller-supplied DISABLE_AUTOUPDATER",
		},
		{
			// copilot_env loop, provider-wrapper side: the first knob removed from
			// provider-wrapper.sh; the dynamic message names that wrapper + knob.
			name:        "first knob missing from provider wrapper",
			home:        homeSeedHappyHomeControlPlane,
			provider:    strings.Replace(homeSeedHappyProviderWrapper(), firstKnob+"\n", "", 1),
			development: homeSeedHappyDevelopmentWrapper(),
			wantErr:     "Expected provider-wrapper.sh to scrub Copilot/GitHub ambient env knob: " + firstKnob,
		},
		{
			// copilot_env loop, development-wrapper side: the first knob removed
			// from development-wrapper.sh only; the provider probe for that knob
			// passes first, then the development probe fails, proving the inner
			// wrapper ordering (provider before development).
			name:        "first knob missing from development wrapper",
			home:        homeSeedHappyHomeControlPlane,
			provider:    homeSeedHappyProviderWrapper(),
			development: strings.Replace(homeSeedHappyDevelopmentWrapper(), firstKnob+"\n", "", 1),
			wantErr:     "Expected development-wrapper.sh to scrub Copilot/GitHub ambient env knob: " + firstKnob,
		},
		{
			// copilot_env loop: a trailing knob removed from provider-wrapper.sh,
			// proving the loop covers the full list, not just the head.
			name:        "last knob missing from provider wrapper",
			home:        homeSeedHappyHomeControlPlane,
			provider:    strings.Replace(homeSeedHappyProviderWrapper(), lastKnob+"\n", "", 1),
			development: homeSeedHappyDevelopmentWrapper(),
			wantErr:     "Expected provider-wrapper.sh to scrub Copilot/GitHub ambient env knob: " + lastKnob,
		},
		{
			// A missing home-control-plane.sh is empty content: the first
			// affirmative check (trustedFolders) fires, mirroring `rg -q`
			// returning non-zero on a missing file.
			name:        "missing home control plane",
			home:        "",
			provider:    homeSeedHappyProviderWrapper(),
			development: homeSeedHappyDevelopmentWrapper(),
			wantErr:     "Expected Gemini home seeding to provision trustedFolders.json",
		},
		{
			// A missing provider-wrapper.sh is empty content: the six
			// home-control-plane checks pass, then the first provider probe
			// (CLAUDE_CONFIG_DIR scrub) fails.
			name:        "missing provider wrapper",
			home:        homeSeedHappyHomeControlPlane,
			provider:    "",
			development: homeSeedHappyDevelopmentWrapper(),
			wantErr:     "Expected provider wrapper to discard caller-supplied CLAUDE_CONFIG_DIR",
		},
		{
			// A missing development-wrapper.sh is empty content: every single
			// probe and the provider side of the first knob pass, then the
			// development side of the first knob fails.
			name:        "missing development wrapper",
			home:        homeSeedHappyHomeControlPlane,
			provider:    homeSeedHappyProviderWrapper(),
			development: "",
			wantErr:     "Expected development-wrapper.sh to scrub Copilot/GitHub ambient env knob: " + firstKnob,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := writeHomeSeedProviderWrapperRepo(t, tc.home, tc.provider, tc.development)
			err := CheckHomeSeedProviderWrapper(root)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("CheckHomeSeedProviderWrapper() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("CheckHomeSeedProviderWrapper() = nil, want error %q", tc.wantErr)
			}
			if err.Error() != tc.wantErr {
				t.Fatalf("CheckHomeSeedProviderWrapper() error = %q, want %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// TestCheckHomeSeedProviderWrapperCoversAllKnobs asserts the generated check
// list contains exactly nine leading probes plus two checks per copilot_env
// knob (one per wrapper), guarding against an accidentally truncated loop.
func TestCheckHomeSeedProviderWrapperCoversAllKnobs(t *testing.T) {
	got := len(homeSeedProviderWrapperChecks())
	want := 9 + 2*len(copilotAmbientEnvKnobs)
	if got != want {
		t.Fatalf("homeSeedProviderWrapperChecks() has %d checks, want %d", got, want)
	}
}

// TestCheckHomeSeedProviderWrapperRealRepo asserts that the real
// home-control-plane.sh, provider-wrapper.sh, and development-wrapper.sh in
// this repository satisfy all fifty-seven home-seeding / provider-wrapper
// invariants.  This is the key guard against a mis-transcribed needle or a
// mis-typed knob: if any Go pattern is not a byte-exact substring of the
// actual file, this test fails with the guard's stderr message.
func TestCheckHomeSeedProviderWrapperRealRepo(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	if _, err := os.Stat(filepath.Join(repoRoot, homeControlPlaneRelPath)); err != nil {
		t.Skipf("real home-control-plane.sh not found at %s: %v", repoRoot, err)
	}
	if err := CheckHomeSeedProviderWrapper(repoRoot); err != nil {
		t.Fatalf("CheckHomeSeedProviderWrapper(real repo) = %v, want nil", err)
	}
}

// copilotHandoffHappyLauncher is a minimal but structurally faithful
// scripts/workcell satisfying every scripts/workcell-targeted Copilot
// token-handoff invariant: the three no-auth classifier function blocks, the
// prepare_copilot_token_handoff_mount write-site re-check + leaf-permission +
// staged-copy-removal probes, the whole-file handoff-dir mktemp + token-handoff
// marker probes, and the DIRECT_SOURCE_MOUNTS / prepare "$@" launcher wiring.
const copilotHandoffHappyLauncher = `#!/usr/bin/env bash
set -euo pipefail
copilot_no_auth_invocation() {
  case "$1" in
  -h | --help | -v | --version | help | version | completion)
    return 0 ;;
  esac
  return 1
}
copilot_host_invocation_requires_auth() {
  if copilot_no_auth_invocation "$@"; then
    return 1
  fi
}
fail_fast_for_missing_copilot_auth() {
  if copilot_no_auth_invocation "$@"; then
    return 0
  fi
}
prepare_copilot_token_handoff_mount() {
  token_handoff_parent="$(default_copilot_token_handoff_parent)" || exit $?
  chmod 0700 "${token_handoff_parent}"
  reject_symlinked_colima_staging_cache_roots || exit $?
  COPILOT_TOKEN_HANDOFF_DIR="$(mktemp -d "${token_handoff_parent}/copilot-token-handoff.XXXXXX")"
  chmod 0733 "${COPILOT_TOKEN_HANDOFF_DIR}"
  chmod 0444 "${COPILOT_TOKEN_HANDOFF_FILE}"
  rm -f -- "${source_path}"
}
main() {
  prepare_copilot_token_handoff_mount "$@"
  DIRECT_SOURCE_MOUNTS=("${filtered_mounts[@]}")
  : "workcell-token-handoff"
  COPILOT_TOKEN_HANDOFF_CONSUMED_FILE="${COPILOT_TOKEN_HANDOFF_DIR}/copilot-token-consumed"
  while [[ ! -e "${COPILOT_TOKEN_HANDOFF_CONSUMED_FILE}" ]]; do
    sleep 1
  done
}
`

// copilotHandoffHappyProvider is a minimal runtime/container/provider-wrapper.sh
// satisfying the four provider prefix-scrub probes (two present, two absent),
// the negated COPILOT_HOME token-copy guard, and the two shared no-auth
// classifier function blocks.
const copilotHandoffHappyProvider = `#!/usr/bin/env bash
set -euo pipefail
unset "${!COPILOT_@}"
unset "${!GITHUB_COPILOT_@}"
copilot_no_auth_invocation() {
  case "$1" in
  -h | --help | -v | --version | help | version | completion)
    return 0 ;;
  esac
  return 1
}
copilot_invocation_requires_auth() {
  if copilot_no_auth_invocation "$@"; then
    return 0
  fi
}
`

// copilotHandoffHappyDevelopment is a minimal
// runtime/container/development-wrapper.sh satisfying the four development
// prefix-scrub probes (the only invariants that read this second wrapper).
const copilotHandoffHappyDevelopment = `#!/usr/bin/env bash
set -euo pipefail
unset "${!COPILOT_@}"
unset "${!GITHUB_COPILOT_@}"
`

// copilotHandoffHappyHome is a minimal runtime/container/home-control-plane.sh
// that satisfies the negated COPILOT_HOME token-copy guard (the needle is
// absent).
const copilotHandoffHappyHome = `#!/usr/bin/env bash
set -euo pipefail
: "home control plane"
`

// copilotHandoffHappySmoke is a minimal scripts/container-smoke.sh satisfying
// the two stage_copilot_token_handoff_dir leaf-permission probes.
const copilotHandoffHappySmoke = `#!/usr/bin/env bash
set -euo pipefail
stage_copilot_token_handoff_dir() {
  chmod 0733 "${token_handoff_dir}"
  chmod 0444 "${token_handoff_file}"
}
`

// writeCopilotHandoffRepo materializes a fake repo with the five files this
// group reads set to the given bodies; a body of "" means "do not create that
// file" (unreadable-target case).
func writeCopilotHandoffRepo(t *testing.T, launcher, provider, development, home, smoke string) string {
	t.Helper()
	root := t.TempDir()
	write := func(rel, body string) {
		if body == "" {
			return
		}
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	write(launcherRelPath, launcher)
	write(providerWrapperRelPath, provider)
	write(developmentWrapperRelPath, development)
	write(homeControlPlaneRelPath, home)
	write(containerSmokeRelPath, smoke)
	return root
}

func TestCheckCopilotTokenHandoff(t *testing.T) {
	tests := []struct {
		name        string
		launcher    string
		provider    string
		development string
		home        string
		smoke       string
		wantErr     string // "" means expect success
	}{
		{
			name:        "happy path all invariants hold",
			launcher:    copilotHandoffHappyLauncher,
			provider:    copilotHandoffHappyProvider,
			development: copilotHandoffHappyDevelopment,
			home:        copilotHandoffHappyHome,
			smoke:       copilotHandoffHappySmoke,
		},
		{
			// Guard 1, provider probe A (kindPresent): the COPILOT_ prefix scrub
			// removed from provider-wrapper.sh; message names that wrapper.
			name:        "provider missing COPILOT prefix scrub",
			launcher:    copilotHandoffHappyLauncher,
			provider:    strings.Replace(copilotHandoffHappyProvider, `unset "${!COPILOT_@}"`, "true", 1),
			development: copilotHandoffHappyDevelopment,
			home:        copilotHandoffHappyHome,
			smoke:       copilotHandoffHappySmoke,
			wantErr:     "Expected provider-wrapper.sh to scrub unknown future Copilot env variables by prefix",
		},
		{
			// Guard 1, provider probe C (kindAbsent): a duplicate OIDC token loop
			// present in provider-wrapper.sh is a violation.
			name:        "provider reintroduces duplicate OIDC loop",
			launcher:    copilotHandoffHappyLauncher,
			provider:    copilotHandoffHappyProvider + "unset \"${!GITHUB_COPILOT_OIDC_MCP_TOKEN@}\"\n",
			development: copilotHandoffHappyDevelopment,
			home:        copilotHandoffHappyHome,
			smoke:       copilotHandoffHappySmoke,
			wantErr:     "provider-wrapper.sh must rely on the GITHUB_COPILOT_ prefix scrub instead of a duplicate OIDC token loop",
		},
		{
			// Guard 1, development side: the GitHub Copilot prefix scrub removed
			// from development-wrapper.sh only; the four provider probes and the
			// provider side of the loop pass first, proving wrapper-major ordering.
			name:        "development missing GitHub Copilot prefix scrub",
			launcher:    copilotHandoffHappyLauncher,
			provider:    copilotHandoffHappyProvider,
			development: strings.Replace(copilotHandoffHappyDevelopment, `unset "${!GITHUB_COPILOT_@}"`, "true", 1),
			home:        copilotHandoffHappyHome,
			smoke:       copilotHandoffHappySmoke,
			wantErr:     "Expected development-wrapper.sh to scrub unknown future GitHub Copilot env variables by prefix",
		},
		{
			// Guard 2, first file (provider-wrapper.sh): the token-copy needle
			// present is a violation.
			name:        "provider copies token into COPILOT_HOME",
			launcher:    copilotHandoffHappyLauncher,
			provider:    copilotHandoffHappyProvider + "cp token \"${COPILOT_HOME}/workcell-token\"\n",
			development: copilotHandoffHappyDevelopment,
			home:        copilotHandoffHappyHome,
			smoke:       copilotHandoffHappySmoke,
			wantErr:     "Copilot auth token must not be copied into COPILOT_HOME",
		},
		{
			// Guard 2, second file (home-control-plane.sh): provider is clean, so
			// the first Guard-2 check passes and the home-control-plane check fires,
			// proving the two-file ordering (provider before home).
			name:        "home control plane copies token into COPILOT_HOME",
			launcher:    copilotHandoffHappyLauncher,
			provider:    copilotHandoffHappyProvider,
			development: copilotHandoffHappyDevelopment,
			home:        copilotHandoffHappyHome + "cp token \"${COPILOT_HOME}/workcell-token\"\n",
			smoke:       copilotHandoffHappySmoke,
			wantErr:     "Copilot auth token must not be copied into COPILOT_HOME",
		},
		{
			// Guard 3 (kindPresent, scripts/workcell): the prepare "$@" call removed.
			name:        "launcher missing prepare token handoff call",
			launcher:    strings.Replace(copilotHandoffHappyLauncher, `prepare_copilot_token_handoff_mount "$@"`, "true", 1),
			provider:    copilotHandoffHappyProvider,
			development: copilotHandoffHappyDevelopment,
			home:        copilotHandoffHappyHome,
			smoke:       copilotHandoffHappySmoke,
			wantErr:     "Expected launcher to prepare a host-mounted Copilot token handoff before docker run",
		},
		{
			// Guard 5, scripts/workcell function-block probe: the no-auth subcommand
			// case removed from copilot_no_auth_invocation.
			name:        "launcher no-auth classifier missing shared helper",
			launcher:    strings.Replace(copilotHandoffHappyLauncher, `-h | --help | -v | --version | help | version | completion)`, `-h)`, 1),
			provider:    copilotHandoffHappyProvider,
			development: copilotHandoffHappyDevelopment,
			home:        copilotHandoffHappyHome,
			smoke:       copilotHandoffHappySmoke,
			wantErr:     "Expected host and runtime Copilot auth classifiers to share the no-auth subcommand helper",
		},
		{
			// Guard 5, provider-wrapper.sh function-block probe: the shared helper
			// call removed from copilot_invocation_requires_auth.
			name:        "provider auth classifier missing shared helper",
			launcher:    copilotHandoffHappyLauncher,
			provider:    strings.Replace(copilotHandoffHappyProvider, `if copilot_no_auth_invocation "$@"; then`, "if false; then", 1),
			development: copilotHandoffHappyDevelopment,
			home:        copilotHandoffHappyHome,
			smoke:       copilotHandoffHappySmoke,
			wantErr:     "Expected host and runtime Copilot auth classifiers to share the no-auth subcommand helper",
		},
		{
			// Guard 7, prepare_copilot_token_handoff_mount function-block probe: the
			// write-site symlink re-check removed.
			name:        "launcher missing write-site staging re-check",
			launcher:    strings.Replace(copilotHandoffHappyLauncher, "reject_symlinked_colima_staging_cache_roots || exit $?", "true", 1),
			provider:    copilotHandoffHappyProvider,
			development: copilotHandoffHappyDevelopment,
			home:        copilotHandoffHappyHome,
			smoke:       copilotHandoffHappySmoke,
			wantErr:     "Expected Copilot token handoff writes to re-check the guarded Colima handoff root at the write site",
		},
		{
			// Guard 8, container-smoke.sh function-block probe (a different target
			// file): the leaf-file chmod removed from stage_copilot_token_handoff_dir.
			name:        "container smoke missing token leaf permission",
			launcher:    copilotHandoffHappyLauncher,
			provider:    copilotHandoffHappyProvider,
			development: copilotHandoffHappyDevelopment,
			home:        copilotHandoffHappyHome,
			smoke:       strings.Replace(copilotHandoffHappySmoke, `chmod 0444 "${token_handoff_file}"`, "true", 1),
			wantErr:     "Expected Copilot token handoff leaf permissions to support cap-dropped container root without exposing parent traversal",
		},
		{
			// Guard 9 (kindPresent, scripts/workcell): the consumed-marker wait loop
			// removed.
			name:        "launcher missing consumed marker wait",
			launcher:    strings.Replace(copilotHandoffHappyLauncher, `while [[ ! -e "${COPILOT_TOKEN_HANDOFF_CONSUMED_FILE}" ]]; do`, "while false; do", 1),
			provider:    copilotHandoffHappyProvider,
			development: copilotHandoffHappyDevelopment,
			home:        copilotHandoffHappyHome,
			smoke:       copilotHandoffHappySmoke,
			wantErr:     "Expected detached Copilot launches to wait for the wrapper-side token consumed marker",
		},
		{
			// Guard 10, prepare_copilot_token_handoff_mount function-block probe: the
			// staged-copy removal removed.
			name:        "launcher missing staged token copy removal",
			launcher:    strings.Replace(copilotHandoffHappyLauncher, `rm -f -- "${source_path}"`, "true", 1),
			provider:    copilotHandoffHappyProvider,
			development: copilotHandoffHappyDevelopment,
			home:        copilotHandoffHappyHome,
			smoke:       copilotHandoffHappySmoke,
			wantErr:     "Expected Copilot token handoff to remove the staged token copy from the mounted injection bundle",
		},
		{
			// A missing scripts/workcell is empty content: Guard 1 (wrappers) and
			// Guard 2 pass, then Guard 3 (the first scripts/workcell probe) fails.
			name:        "missing launcher",
			launcher:    "",
			provider:    copilotHandoffHappyProvider,
			development: copilotHandoffHappyDevelopment,
			home:        copilotHandoffHappyHome,
			smoke:       copilotHandoffHappySmoke,
			wantErr:     "Expected launcher to prepare a host-mounted Copilot token handoff before docker run",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := writeCopilotHandoffRepo(t, tc.launcher, tc.provider, tc.development, tc.home, tc.smoke)
			err := CheckCopilotTokenHandoff(root)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("CheckCopilotTokenHandoff() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("CheckCopilotTokenHandoff() = nil, want error %q", tc.wantErr)
			}
			if err.Error() != tc.wantErr {
				t.Fatalf("CheckCopilotTokenHandoff() error = %q, want %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// TestCheckCopilotTokenHandoffCount asserts the generated check list contains
// exactly twenty-nine invariants (eight prefix-scrub loop checks + twenty-one
// remaining probes), guarding against an accidentally truncated loop or block.
func TestCheckCopilotTokenHandoffCount(t *testing.T) {
	got := len(copilotTokenHandoffChecks())
	const want = 29
	if got != want {
		t.Fatalf("copilotTokenHandoffChecks() has %d checks, want %d", got, want)
	}
}

// TestCheckCopilotTokenHandoffRealRepo asserts that the real scripts/workcell,
// provider-wrapper.sh, development-wrapper.sh, home-control-plane.sh, and
// container-smoke.sh in this repository satisfy all twenty-nine Copilot
// token-handoff invariants.  This is the key guard against a mis-transcribed
// needle or target file: if any Go pattern is not a byte-exact substring of the
// actual file (or the wrong file), this test fails with the guard's stderr
// message.
func TestCheckCopilotTokenHandoffRealRepo(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	if _, err := os.Stat(filepath.Join(repoRoot, launcherRelPath)); err != nil {
		t.Skipf("real scripts/workcell not found at %s: %v", repoRoot, err)
	}
	if err := CheckCopilotTokenHandoff(repoRoot); err != nil {
		t.Fatalf("CheckCopilotTokenHandoff(real repo) = %v, want nil", err)
	}
}

// copilotDockerRunHappyLauncher is a minimal but structurally faithful
// scripts/workcell satisfying every launcher-targeted Copilot / docker-run
// invariant: no Docker --env-file for the token, the PID-1 wiring, the
// token-handoff bind mount, the two host-computed auth-metadata env exports
// (the two resolved variable needles), the consumed-marker wait, and the
// mapped-user /run/workcell tmpfs.
const copilotDockerRunHappyLauncher = `#!/usr/bin/env bash
set -euo pipefail
main() {
  if [[ -z "${COPILOT_TOKEN_HANDOFF_DIR}" ]]; then
    return 1
  fi
  DOCKER_RUN_BASE+=(--init)
  DOCKER_RUN_PREFIX_LEN=2
  DOCKER_RUN_BASE+=(--mount "type=bind,source=${COPILOT_TOKEN_HANDOFF_DIR}:${COPILOT_TOKEN_HANDOFF_CONTAINER_DIR}:rw")
  copilot_container_dir_env='WORKCELL_COPILOT_TOKEN_HANDOFF_CONTAINER_DIR="${COPILOT_TOKEN_HANDOFF_CONTAINER_DIR}"'
  copilot_auth_required_env='WORKCELL_COPILOT_AUTH_REQUIRED="${COPILOT_AUTH_REQUIRED}"'
  DOCKER_RUN_BASE+=(--env "${copilot_container_dir_env}" --env "${copilot_auth_required_env}")
  DOCKER_RUN_BASE+=(--tmpfs "/run/workcell:nosuid,nodev,size=4m,mode=755,uid=${HOST_UID},gid=${HOST_GID}")
  wait_for_copilot_token_handoff_consumed
}
`

// copilotDockerRunHappyHostState is a minimal internal/host/hoststate/hoststate.go
// satisfying the legacy stale-env-file cleanup probe.
const copilotDockerRunHappyHostState = `package hoststate

func staleEnvFile(suffix string) bool {
	return strings.HasPrefix(suffix, "env.")
}
`

// copilotDockerRunHappyLauncherCommon is a minimal launcher_common.rs satisfying
// the WORKCELL_COPILOT_AUTH_REQUIRED sanitization probe.
const copilotDockerRunHappyLauncherCommon = `pub fn sanitize(env: &mut Env) {
    env.remove("WORKCELL_COPILOT_AUTH_REQUIRED");
}
`

// copilotDockerRunHappyWorkcellLauncher is a minimal workcell-launcher.rs
// satisfying the copilot_auth_required_for_pid1 probe.
const copilotDockerRunHappyWorkcellLauncher = `fn build(request: &Request) {
    let required = copilot_auth_required_for_pid1(request.target_name);
    let _ = required;
}
`

// copilotDockerRunHappyEntrypoint is a minimal runtime/container/entrypoint.sh
// satisfying the entrypoint-targeted probes (staging, container-dir env, host
// token read/unlink, runtime-state record, self-reexec, mapped-user creation)
// and NONE of the three negated guards (no caller-token source, no chown, no
// re-exported token when launching the child).
const copilotDockerRunHappyEntrypoint = `#!/usr/bin/env bash
set -euo pipefail
stage_copilot_token_handoff_file "$@"
WORKCELL_COPILOT_TOKEN_HANDOFF_CONTAINER_DIR="${WORKCELL_COPILOT_TOKEN_HANDOFF_CONTAINER_DIR:-/opt/workcell/copilot-token-handoff}"
WORKCELL_COPILOT_HOST_TOKEN_FILE="${WORKCELL_COPILOT_TOKEN_HANDOFF_CONTAINER_DIR}/copilot-github-token.txt"
host_token_file="${WORKCELL_COPILOT_HOST_TOKEN_FILE}"
setpriv --reuid "${uid}" --regid "${gid}" --init-groups /bin/bash -c 'cat "${host_token_file}"'
rm -f -- "${host_token_file}"
workcell_write_readonly_state_file "${WORKCELL_RUNTIME_COPILOT_TOKEN_FILE_PATH}" "${token_file}"
exec env -u WORKCELL_COPILOT_GITHUB_TOKEN /usr/local/bin/workcell-launcher "$@"
`

// copilotDockerRunHappySmoke is a minimal scripts/container-smoke.sh satisfying
// the two Docker-inspect metadata-leak proof probes.
const copilotDockerRunHappySmoke = `#!/usr/bin/env bash
set -euo pipefail
assert_no_metadata_leak() {
  COPILOT_METADATA_ENV="$(docker_cmd inspect --format '{{json .Config.Env}}' "${container}")"
  if grep -q "${token}" <<<"${COPILOT_METADATA_ENV}"; then
    echo "Copilot token leaked into Docker container metadata" >&2
    return 1
  fi
}
`

// copilotDockerRunHappyRuntimeUser is a minimal runtime/container/runtime-user.sh
// satisfying the runtime-state token-path probe.
const copilotDockerRunHappyRuntimeUser = `#!/usr/bin/env bash
WORKCELL_RUNTIME_COPILOT_TOKEN_FILE_PATH="${WORKCELL_RUNTIME_STATE_DIR}/copilot-token-file"
`

// writeCopilotDockerRunRepo materializes a fake repo with the seven files this
// group reads set to the given bodies; a body of "" means "do not create that
// file" (unreadable-target case).
func writeCopilotDockerRunRepo(t *testing.T, launcher, hostState, launcherCommon, workcellLauncher, entrypoint, smoke, runtimeUser string) string {
	t.Helper()
	root := t.TempDir()
	write := func(rel, body string) {
		if body == "" {
			return
		}
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	write(launcherRelPath, launcher)
	write(hoststateRelPath, hostState)
	write(launcherCommonRustRelPath, launcherCommon)
	write(workcellLauncherRustRelPath, workcellLauncher)
	write(entrypointRelPath, entrypoint)
	write(containerSmokeRelPath, smoke)
	write(runtimeUserRelPath, runtimeUser)
	return root
}

func TestCheckCopilotDockerRun(t *testing.T) {
	tests := []struct {
		name             string
		launcher         string
		hostState        string
		launcherCommon   string
		workcellLauncher string
		entrypoint       string
		smoke            string
		runtimeUser      string
		wantErr          string // "" means expect success
	}{
		{
			name:             "happy path all invariants hold",
			launcher:         copilotDockerRunHappyLauncher,
			hostState:        copilotDockerRunHappyHostState,
			launcherCommon:   copilotDockerRunHappyLauncherCommon,
			workcellLauncher: copilotDockerRunHappyWorkcellLauncher,
			entrypoint:       copilotDockerRunHappyEntrypoint,
			smoke:            copilotDockerRunHappySmoke,
			runtimeUser:      copilotDockerRunHappyRuntimeUser,
		},
		{
			// Check 1 (kindPresent, hoststate.go — a non-launcher file): the
			// legacy env-file cleanup probe removed.
			name:             "hoststate missing legacy env-file cleanup",
			launcher:         copilotDockerRunHappyLauncher,
			hostState:        strings.Replace(copilotDockerRunHappyHostState, `strings.HasPrefix(suffix, "env.")`, `strings.HasPrefix(suffix, "other.")`, 1),
			launcherCommon:   copilotDockerRunHappyLauncherCommon,
			workcellLauncher: copilotDockerRunHappyWorkcellLauncher,
			entrypoint:       copilotDockerRunHappyEntrypoint,
			smoke:            copilotDockerRunHappySmoke,
			runtimeUser:      copilotDockerRunHappyRuntimeUser,
			wantErr:          "Expected legacy stale Copilot token env-file cleanup to cover production mktemp suffixes",
		},
		{
			// Check 2 (kindAbsent, launcher): a Docker --env-file for the Copilot
			// token present is a violation.
			name:             "launcher uses docker env-file for token",
			launcher:         copilotDockerRunHappyLauncher + "\nDOCKER_RUN_BASE+=(--env-file \"${COPILOT_TOKEN_ENV_FILE}\")\n",
			hostState:        copilotDockerRunHappyHostState,
			launcherCommon:   copilotDockerRunHappyLauncherCommon,
			workcellLauncher: copilotDockerRunHappyWorkcellLauncher,
			entrypoint:       copilotDockerRunHappyEntrypoint,
			smoke:            copilotDockerRunHappySmoke,
			runtimeUser:      copilotDockerRunHappyRuntimeUser,
			wantErr:          "Copilot auth must not use Docker env-files because Docker stores them in container metadata",
		},
		{
			// Check 3 (kindPresent, launcher — first probe of the PID-1 guard).
			name:             "launcher missing token-handoff-dir guard",
			launcher:         strings.Replace(copilotDockerRunHappyLauncher, `if [[ -z "${COPILOT_TOKEN_HANDOFF_DIR}" ]]; then`, "if false; then", 1),
			hostState:        copilotDockerRunHappyHostState,
			launcherCommon:   copilotDockerRunHappyLauncherCommon,
			workcellLauncher: copilotDockerRunHappyWorkcellLauncher,
			entrypoint:       copilotDockerRunHappyEntrypoint,
			smoke:            copilotDockerRunHappySmoke,
			runtimeUser:      copilotDockerRunHappyRuntimeUser,
			wantErr:          "Expected Copilot token handoff launches to keep the Workcell entrypoint as PID 1",
		},
		{
			// Check 5 (kindPresent, launcher — last probe of the PID-1 guard):
			// proves the three ordered probes share one message.
			name:             "launcher missing docker-run prefix len",
			launcher:         strings.Replace(copilotDockerRunHappyLauncher, "DOCKER_RUN_PREFIX_LEN=2", "DOCKER_RUN_PREFIX_LEN=1", 1),
			hostState:        copilotDockerRunHappyHostState,
			launcherCommon:   copilotDockerRunHappyLauncherCommon,
			workcellLauncher: copilotDockerRunHappyWorkcellLauncher,
			entrypoint:       copilotDockerRunHappyEntrypoint,
			smoke:            copilotDockerRunHappySmoke,
			runtimeUser:      copilotDockerRunHappyRuntimeUser,
			wantErr:          "Expected Copilot token handoff launches to keep the Workcell entrypoint as PID 1",
		},
		{
			// Check 6 (kindPresent, launcher): the read-write handoff bind mount
			// spec removed.
			name:             "launcher missing token-handoff bind mount",
			launcher:         strings.Replace(copilotDockerRunHappyLauncher, "${COPILOT_TOKEN_HANDOFF_DIR}:${COPILOT_TOKEN_HANDOFF_CONTAINER_DIR}:rw", "${SOME_OTHER_DIR}:/mnt:ro", 1),
			hostState:        copilotDockerRunHappyHostState,
			launcherCommon:   copilotDockerRunHappyLauncherCommon,
			workcellLauncher: copilotDockerRunHappyWorkcellLauncher,
			entrypoint:       copilotDockerRunHappyEntrypoint,
			smoke:            copilotDockerRunHappySmoke,
			runtimeUser:      copilotDockerRunHappyRuntimeUser,
			wantErr:          "Expected docker run to mount only the Copilot token handoff directory, not the original token source",
		},
		{
			// Check 7 (kindPresent, launcher — resolved variable needle 1): the
			// container-dir auth-metadata env export removed.
			name:             "launcher missing container-dir auth metadata env",
			launcher:         strings.Replace(copilotDockerRunHappyLauncher, `WORKCELL_COPILOT_TOKEN_HANDOFF_CONTAINER_DIR="${COPILOT_TOKEN_HANDOFF_CONTAINER_DIR}"`, `WORKCELL_OTHER="${x}"`, 1),
			hostState:        copilotDockerRunHappyHostState,
			launcherCommon:   copilotDockerRunHappyLauncherCommon,
			workcellLauncher: copilotDockerRunHappyWorkcellLauncher,
			entrypoint:       copilotDockerRunHappyEntrypoint,
			smoke:            copilotDockerRunHappySmoke,
			runtimeUser:      copilotDockerRunHappyRuntimeUser,
			wantErr:          "Expected Copilot launches to pass validated host-computed auth metadata through PID 1 and scrub caller-supplied metadata before provider wrapper exec",
		},
		{
			// Check 8 (kindPresent, launcher — resolved variable needle 2): the
			// auth-required env export removed.
			name:             "launcher missing auth-required env",
			launcher:         strings.Replace(copilotDockerRunHappyLauncher, `WORKCELL_COPILOT_AUTH_REQUIRED="${COPILOT_AUTH_REQUIRED}"`, `WORKCELL_OTHER_REQUIRED="${x}"`, 1),
			hostState:        copilotDockerRunHappyHostState,
			launcherCommon:   copilotDockerRunHappyLauncherCommon,
			workcellLauncher: copilotDockerRunHappyWorkcellLauncher,
			entrypoint:       copilotDockerRunHappyEntrypoint,
			smoke:            copilotDockerRunHappySmoke,
			runtimeUser:      copilotDockerRunHappyRuntimeUser,
			wantErr:          "Expected Copilot launches to pass validated host-computed auth metadata through PID 1 and scrub caller-supplied metadata before provider wrapper exec",
		},
		{
			// Check 9 (kindPresent, launcher_common.rs — a non-launcher file):
			// the auth-required sanitization probe removed.
			name:             "launcher_common missing auth-required scrub",
			launcher:         copilotDockerRunHappyLauncher,
			hostState:        copilotDockerRunHappyHostState,
			launcherCommon:   strings.Replace(copilotDockerRunHappyLauncherCommon, "WORKCELL_COPILOT_AUTH_REQUIRED", "WORKCELL_OTHER", 1),
			workcellLauncher: copilotDockerRunHappyWorkcellLauncher,
			entrypoint:       copilotDockerRunHappyEntrypoint,
			smoke:            copilotDockerRunHappySmoke,
			runtimeUser:      copilotDockerRunHappyRuntimeUser,
			wantErr:          "Expected Copilot launches to pass validated host-computed auth metadata through PID 1 and scrub caller-supplied metadata before provider wrapper exec",
		},
		{
			// Check 10 (kindPresent, workcell-launcher.rs — a non-launcher file):
			// the PID-1 auth classifier call removed.
			name:             "workcell-launcher missing pid1 auth classifier",
			launcher:         copilotDockerRunHappyLauncher,
			hostState:        copilotDockerRunHappyHostState,
			launcherCommon:   copilotDockerRunHappyLauncherCommon,
			workcellLauncher: strings.Replace(copilotDockerRunHappyWorkcellLauncher, "copilot_auth_required_for_pid1(request.target_name)", "copilot_auth_required_for_pid1(other)", 1),
			entrypoint:       copilotDockerRunHappyEntrypoint,
			smoke:            copilotDockerRunHappySmoke,
			runtimeUser:      copilotDockerRunHappyRuntimeUser,
			wantErr:          "Expected Copilot launches to pass validated host-computed auth metadata through PID 1 and scrub caller-supplied metadata before provider wrapper exec",
		},
		{
			// Check 11 (kindPresent, launcher): the consumed-marker wait removed.
			name:             "launcher missing consumed-marker wait",
			launcher:         strings.Replace(copilotDockerRunHappyLauncher, "wait_for_copilot_token_handoff_consumed", "wait_for_other", 1),
			hostState:        copilotDockerRunHappyHostState,
			launcherCommon:   copilotDockerRunHappyLauncherCommon,
			workcellLauncher: copilotDockerRunHappyWorkcellLauncher,
			entrypoint:       copilotDockerRunHappyEntrypoint,
			smoke:            copilotDockerRunHappySmoke,
			runtimeUser:      copilotDockerRunHappyRuntimeUser,
			wantErr:          "Expected detached Copilot launches to wait until the managed wrapper consumes the token handoff",
		},
		{
			// Check 12 (kindPresent, launcher): the mapped-user /run/workcell
			// tmpfs spec removed.
			name:             "launcher missing run-workcell tmpfs",
			launcher:         strings.Replace(copilotDockerRunHappyLauncher, "/run/workcell:nosuid,nodev,size=4m,mode=755,uid=${HOST_UID},gid=${HOST_GID}", "/run/workcell:rw", 1),
			hostState:        copilotDockerRunHappyHostState,
			launcherCommon:   copilotDockerRunHappyLauncherCommon,
			workcellLauncher: copilotDockerRunHappyWorkcellLauncher,
			entrypoint:       copilotDockerRunHappyEntrypoint,
			smoke:            copilotDockerRunHappySmoke,
			runtimeUser:      copilotDockerRunHappyRuntimeUser,
			wantErr:          "Expected readonly Copilot token handoff state to use a mapped-user writable /run/workcell tmpfs",
		},
		{
			// Check 13 (kindPresent, entrypoint.sh): the token staging call removed.
			name:             "entrypoint missing token staging call",
			launcher:         copilotDockerRunHappyLauncher,
			hostState:        copilotDockerRunHappyHostState,
			launcherCommon:   copilotDockerRunHappyLauncherCommon,
			workcellLauncher: copilotDockerRunHappyWorkcellLauncher,
			entrypoint:       strings.Replace(copilotDockerRunHappyEntrypoint, `stage_copilot_token_handoff_file "$@"`, "true", 1),
			smoke:            copilotDockerRunHappySmoke,
			runtimeUser:      copilotDockerRunHappyRuntimeUser,
			wantErr:          "Expected runtime entrypoint to stage the Copilot host handoff token into a transient runtime file",
		},
		{
			// Check 16 (kindPresent, entrypoint.sh — last probe of the read-and-
			// unlink guard): the host token unlink removed.
			name:             "entrypoint missing host token unlink",
			launcher:         copilotDockerRunHappyLauncher,
			hostState:        copilotDockerRunHappyHostState,
			launcherCommon:   copilotDockerRunHappyLauncherCommon,
			workcellLauncher: copilotDockerRunHappyWorkcellLauncher,
			entrypoint:       strings.Replace(copilotDockerRunHappyEntrypoint, `rm -f -- "${host_token_file}"`, "true", 1),
			smoke:            copilotDockerRunHappySmoke,
			runtimeUser:      copilotDockerRunHappyRuntimeUser,
			wantErr:          "Expected runtime entrypoint to read and unlink the mounted Copilot token handoff file",
		},
		{
			// Check 18 (kindPresent, container-smoke.sh — last probe of the
			// metadata-leak guard): the leak assertion message removed.
			name:             "container smoke missing metadata leak assertion",
			launcher:         copilotDockerRunHappyLauncher,
			hostState:        copilotDockerRunHappyHostState,
			launcherCommon:   copilotDockerRunHappyLauncherCommon,
			workcellLauncher: copilotDockerRunHappyWorkcellLauncher,
			entrypoint:       copilotDockerRunHappyEntrypoint,
			smoke:            strings.Replace(copilotDockerRunHappySmoke, "Copilot token leaked into Docker container metadata", "token leaked", 1),
			runtimeUser:      copilotDockerRunHappyRuntimeUser,
			wantErr:          "Expected container smoke to prove Copilot token material is absent from Docker inspect metadata",
		},
		{
			// Check 19 (kindPresent, runtime-user.sh — first probe of the
			// runtime-state guard): the runtime token-path variable removed.
			name:             "runtime-user missing token file path",
			launcher:         copilotDockerRunHappyLauncher,
			hostState:        copilotDockerRunHappyHostState,
			launcherCommon:   copilotDockerRunHappyLauncherCommon,
			workcellLauncher: copilotDockerRunHappyWorkcellLauncher,
			entrypoint:       copilotDockerRunHappyEntrypoint,
			smoke:            copilotDockerRunHappySmoke,
			runtimeUser:      strings.Replace(copilotDockerRunHappyRuntimeUser, "WORKCELL_RUNTIME_COPILOT_TOKEN_FILE_PATH", "WORKCELL_OTHER", 1),
			wantErr:          "Expected runtime entrypoint to record the staged Copilot token path in root-controlled runtime state",
		},
		{
			// Check 23 (kindAbsent, entrypoint.sh): accepting a caller-supplied
			// token source is a violation.
			name:             "entrypoint accepts caller-supplied token",
			launcher:         copilotDockerRunHappyLauncher,
			hostState:        copilotDockerRunHappyHostState,
			launcherCommon:   copilotDockerRunHappyLauncherCommon,
			workcellLauncher: copilotDockerRunHappyWorkcellLauncher,
			entrypoint:       copilotDockerRunHappyEntrypoint + "\ntoken=\"${WORKCELL_COPILOT_GITHUB_TOKEN:-}\"\n",
			smoke:            copilotDockerRunHappySmoke,
			runtimeUser:      copilotDockerRunHappyRuntimeUser,
			wantErr:          "Runtime entrypoint must not accept caller-supplied WORKCELL_COPILOT_GITHUB_TOKEN as a Copilot auth source",
		},
		{
			// Check 25 (kindRegexAbsent, entrypoint.sh): reintroducing the token
			// env variable on the provider-child launch line is a violation.  The
			// `.*"$@"` regex matches only when the assignment precedes a `"$@"`
			// on the same line, exercising the genuine-regex semantics.
			name:             "entrypoint re-exports token launching child",
			launcher:         copilotDockerRunHappyLauncher,
			hostState:        copilotDockerRunHappyHostState,
			launcherCommon:   copilotDockerRunHappyLauncherCommon,
			workcellLauncher: copilotDockerRunHappyWorkcellLauncher,
			entrypoint:       copilotDockerRunHappyEntrypoint + "\nWORKCELL_COPILOT_GITHUB_TOKEN=\"${token}\" exec provider-wrapper \"$@\"\n",
			smoke:            copilotDockerRunHappySmoke,
			runtimeUser:      copilotDockerRunHappyRuntimeUser,
			wantErr:          "Runtime entrypoint must not reintroduce the Copilot token env variable when launching the provider child",
		},
		{
			// kindRegexAbsent negative control: the token variable name appears in
			// entrypoint.sh (the check-21 `exec env -u` line) but NOT as an
			// assignment before a `"$@"`, so the regex must NOT trip — the
			// invariant still holds.  Pins that the check matches the assignment
			// form rather than any mention of the variable.
			name:             "token variable mention without re-export still holds",
			launcher:         copilotDockerRunHappyLauncher,
			hostState:        copilotDockerRunHappyHostState,
			launcherCommon:   copilotDockerRunHappyLauncherCommon,
			workcellLauncher: copilotDockerRunHappyWorkcellLauncher,
			entrypoint:       copilotDockerRunHappyEntrypoint + "\n# note: WORKCELL_COPILOT_GITHUB_TOKEN is scrubbed\n",
			smoke:            copilotDockerRunHappySmoke,
			runtimeUser:      copilotDockerRunHappyRuntimeUser,
		},
		{
			// A missing scripts/workcell is empty content: check 1 (hoststate)
			// passes, check 2 (kindAbsent on the empty launcher) passes, then
			// check 3 (the first affirmative launcher probe) fails.
			name:             "missing launcher",
			launcher:         "",
			hostState:        copilotDockerRunHappyHostState,
			launcherCommon:   copilotDockerRunHappyLauncherCommon,
			workcellLauncher: copilotDockerRunHappyWorkcellLauncher,
			entrypoint:       copilotDockerRunHappyEntrypoint,
			smoke:            copilotDockerRunHappySmoke,
			runtimeUser:      copilotDockerRunHappyRuntimeUser,
			wantErr:          "Expected Copilot token handoff launches to keep the Workcell entrypoint as PID 1",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := writeCopilotDockerRunRepo(t, tc.launcher, tc.hostState, tc.launcherCommon, tc.workcellLauncher, tc.entrypoint, tc.smoke, tc.runtimeUser)
			err := CheckCopilotDockerRun(root)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("CheckCopilotDockerRun() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("CheckCopilotDockerRun() = nil, want error %q", tc.wantErr)
			}
			if err.Error() != tc.wantErr {
				t.Fatalf("CheckCopilotDockerRun() error = %q, want %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// TestCheckCopilotDockerRunCount asserts the check list contains exactly
// twenty-five invariants, guarding against an accidentally truncated or
// duplicated migration of the shell block.
func TestCheckCopilotDockerRunCount(t *testing.T) {
	got := len(copilotDockerRunChecks)
	const want = 25
	if got != want {
		t.Fatalf("copilotDockerRunChecks has %d checks, want %d", got, want)
	}
}

// TestCheckCopilotDockerRunRealRepo asserts that the real hoststate.go,
// scripts/workcell, launcher_common.rs, workcell-launcher.rs, entrypoint.sh,
// container-smoke.sh, and runtime-user.sh in this repository satisfy all
// twenty-five Copilot / docker-run invariants.  This is the key guard against a
// mis-transcribed needle, a mis-resolved variable needle, or a wrong target
// file: if any Go pattern is not a byte-exact substring of the actual file (or
// the wrong file), this test fails with the guard's stderr message.
func TestCheckCopilotDockerRunRealRepo(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	if _, err := os.Stat(filepath.Join(repoRoot, launcherRelPath)); err != nil {
		t.Skipf("real scripts/workcell not found at %s: %v", repoRoot, err)
	}
	if err := CheckCopilotDockerRun(repoRoot); err != nil {
		t.Fatalf("CheckCopilotDockerRun(real repo) = %v, want nil", err)
	}
}

// providerLauncherAuthorityHappyProviderWrapper is a minimal but structurally
// faithful runtime/container/provider-wrapper.sh: it contains every affirmative
// provider-wrapper needle (the authority marker, the parent-verification pair,
// all twelve Gemini sandbox knobs, the pinned Claude/Gemini exec lines with their
// trailing backslash, the Copilot token-handoff needles, and the staged-token
// requirement) and NEITHER of the two negated needles (no caller-supplied
// WORKCELL_COPILOT_TOKEN_FILE default, no WORKCELL_COPILOT_GITHUB_TOKEN:- env
// fallback).
const providerLauncherAuthorityHappyProviderWrapper = `#!/usr/bin/env bash
set -euo pipefail
export WORKCELL_PROVIDER_LAUNCHER_AUTHORITY=1
workcell_provider_parent_is_launcher() {
  local parent_exe
  parent_exe="$(readlink "/proc/${PPID}/exe")"
}
unset GEMINI_SANDBOX
unset GEMINI_SANDBOX_IMAGE
unset GEMINI_SANDBOX_IMAGE_DEFAULT
unset GEMINI_SANDBOX_PROXY_COMMAND
unset BUILD_SANDBOX
unset SANDBOX
unset SANDBOX_FLAGS
unset SANDBOX_MOUNTS
unset SANDBOX_ENV
unset SANDBOX_PORTS
unset SANDBOX_SET_UID_GID
unset SEATBELT_PROFILE
unset GH_CONFIG_DIR
copilot_github_token="$(workcell_load_copilot_github_token)"
token_file="$(head -n1 "${WORKCELL_RUNTIME_COPILOT_TOKEN_FILE_PATH}")"
copilot_token_handoff_consumed_file="${WORKCELL_COPILOT_TOKEN_HANDOFF_CONTAINER_DIR}/copilot-token-consumed"
: >"${copilot_token_handoff_consumed_file}"
[[ -n "${token_file}" ]] || workcell_die "Copilot auth token handoff file is required."
DISABLE_AUTOUPDATER=1 CLAUDE_CONFIG_DIR="${HOME}/.claude" exec /usr/local/libexec/workcell/real/claude \
  --managed
GEMINI_CLI_NO_RELAUNCH=1 GEMINI_SANDBOX=false exec /usr/local/libexec/workcell/real/node \
  /usr/local/libexec/workcell/real/gemini
`

// providerLauncherAuthorityHappyWorkcellLauncher is a minimal
// workcell-launcher.rs satisfying the authority-marker and
// spawn_and_wait_request probes.
const providerLauncherAuthorityHappyWorkcellLauncher = `fn main() {
    std::env::set_var("WORKCELL_PROVIDER_LAUNCHER_AUTHORITY", "1");
    spawn_and_wait_request(&request);
}
`

// providerLauncherAuthorityHappyLauncherCommon is a minimal launcher_common.rs
// satisfying the authority-marker sanitization probe.
const providerLauncherAuthorityHappyLauncherCommon = `pub fn sanitize(env: &mut Env) {
    env.remove("WORKCELL_PROVIDER_LAUNCHER_AUTHORITY");
}
`

// providerLauncherAuthorityHappyLib is a minimal lib.rs satisfying the two
// exec-guard probes.
const providerLauncherAuthorityHappyLib = `fn guard() -> bool {
    if !current_process_parent_is_approved_native_launcher() {
        return false;
    }
    approved_wrapper_requires_native_launcher_parent()
}
`

// writeProviderLauncherAuthorityRepo materializes a fake repo with the four
// files this group reads set to the given bodies; a body of "" means "do not
// create that file" (unreadable-target case).
func writeProviderLauncherAuthorityRepo(t *testing.T, providerWrapper, workcellLauncher, launcherCommon, rustLib string) string {
	t.Helper()
	root := t.TempDir()
	write := func(rel, body string) {
		if body == "" {
			return
		}
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	write(providerWrapperRelPath, providerWrapper)
	write(workcellLauncherRustRelPath, workcellLauncher)
	write(launcherCommonRustRelPath, launcherCommon)
	write(rustLibRelPath, rustLib)
	return root
}

func TestCheckProviderLauncherAuthority(t *testing.T) {
	tests := []struct {
		name             string
		providerWrapper  string
		workcellLauncher string
		launcherCommon   string
		rustLib          string
		wantErr          string // "" means expect success
	}{
		{
			name:             "happy path all invariants hold",
			providerWrapper:  providerLauncherAuthorityHappyProviderWrapper,
			workcellLauncher: providerLauncherAuthorityHappyWorkcellLauncher,
			launcherCommon:   providerLauncherAuthorityHappyLauncherCommon,
			rustLib:          providerLauncherAuthorityHappyLib,
		},
		{
			// Check 1 (kindPresent, provider-wrapper.sh): the authority marker.
			name:             "provider wrapper missing authority marker",
			providerWrapper:  strings.Replace(providerLauncherAuthorityHappyProviderWrapper, "WORKCELL_PROVIDER_LAUNCHER_AUTHORITY", "WORKCELL_OTHER", 1),
			workcellLauncher: providerLauncherAuthorityHappyWorkcellLauncher,
			launcherCommon:   providerLauncherAuthorityHappyLauncherCommon,
			rustLib:          providerLauncherAuthorityHappyLib,
			wantErr:          "Expected provider wrapper to require the managed launcher authority marker",
		},
		{
			// Check 2 (kindPresent, workcell-launcher.rs — a Rust-file target).
			name:             "workcell-launcher missing authority marker",
			providerWrapper:  providerLauncherAuthorityHappyProviderWrapper,
			workcellLauncher: strings.Replace(providerLauncherAuthorityHappyWorkcellLauncher, "WORKCELL_PROVIDER_LAUNCHER_AUTHORITY", "WORKCELL_OTHER", 1),
			launcherCommon:   providerLauncherAuthorityHappyLauncherCommon,
			rustLib:          providerLauncherAuthorityHappyLib,
			wantErr:          "Expected workcell-launcher to set the provider-wrapper authority marker",
		},
		{
			// Check 3 (kindPresent, launcher_common.rs — a Rust-file target).
			name:             "launcher_common missing authority marker",
			providerWrapper:  providerLauncherAuthorityHappyProviderWrapper,
			workcellLauncher: providerLauncherAuthorityHappyWorkcellLauncher,
			launcherCommon:   strings.Replace(providerLauncherAuthorityHappyLauncherCommon, "WORKCELL_PROVIDER_LAUNCHER_AUTHORITY", "WORKCELL_OTHER", 1),
			rustLib:          providerLauncherAuthorityHappyLib,
			wantErr:          "Expected workcell-launcher env sanitization to discard caller-supplied provider authority markers",
		},
		{
			// Check 4 (kindPresent, workcell-launcher.rs).
			name:             "workcell-launcher missing spawn_and_wait_request",
			providerWrapper:  providerLauncherAuthorityHappyProviderWrapper,
			workcellLauncher: strings.Replace(providerLauncherAuthorityHappyWorkcellLauncher, "spawn_and_wait_request", "spawn_other", 1),
			launcherCommon:   providerLauncherAuthorityHappyLauncherCommon,
			rustLib:          providerLauncherAuthorityHappyLib,
			wantErr:          "Expected workcell-launcher to keep a native parent supervising shell wrappers",
		},
		{
			// Check 5b (kindPresent, provider-wrapper.sh — second probe of the
			// parent-verification multi-probe guard): proves the two ordered
			// probes share one message.
			name:             "provider wrapper missing readlink parent probe",
			providerWrapper:  strings.Replace(providerLauncherAuthorityHappyProviderWrapper, `readlink "/proc/${PPID}/exe"`, `readlink "/proc/self/exe"`, 1),
			workcellLauncher: providerLauncherAuthorityHappyWorkcellLauncher,
			launcherCommon:   providerLauncherAuthorityHappyLauncherCommon,
			rustLib:          providerLauncherAuthorityHappyLib,
			wantErr:          "Expected provider wrapper to require a native Workcell launcher parent before managed provider launch",
		},
		{
			// Check 6b (kindPresent, lib.rs — second probe of the exec-guard
			// multi-probe pair on a Rust-file target): proves the two ordered
			// probes share one message.
			name:             "lib.rs missing approved-wrapper exec guard",
			providerWrapper:  providerLauncherAuthorityHappyProviderWrapper,
			workcellLauncher: providerLauncherAuthorityHappyWorkcellLauncher,
			launcherCommon:   providerLauncherAuthorityHappyLauncherCommon,
			rustLib:          strings.Replace(providerLauncherAuthorityHappyLib, "approved_wrapper_requires_native_launcher_parent", "other_guard", 1),
			wantErr:          "Expected exec guard to reject protected runtime wrapper approval without a native launcher parent",
		},
		{
			// One of the twelve Gemini sandbox scrub checks (resolved variable
			// needle): the last knob removed proves the interpolated message.
			name:             "provider wrapper missing gemini sandbox knob",
			providerWrapper:  strings.Replace(providerLauncherAuthorityHappyProviderWrapper, "unset SEATBELT_PROFILE", "unset OTHER_PROFILE", 1),
			workcellLauncher: providerLauncherAuthorityHappyWorkcellLauncher,
			launcherCommon:   providerLauncherAuthorityHappyLauncherCommon,
			rustLib:          providerLauncherAuthorityHappyLib,
			wantErr:          "Expected provider wrapper to scrub Gemini sandbox env knob: unset SEATBELT_PROFILE",
		},
		{
			// Escaped-exec needle (pinned Claude line, trailing backslash): drop
			// the trailing backslash so the fixed-string containment fails.
			name:             "provider wrapper missing pinned claude exec",
			providerWrapper:  strings.Replace(providerLauncherAuthorityHappyProviderWrapper, `real/claude \`, `real/claude`, 1),
			workcellLauncher: providerLauncherAuthorityHappyWorkcellLauncher,
			launcherCommon:   providerLauncherAuthorityHappyLauncherCommon,
			rustLib:          providerLauncherAuthorityHappyLib,
			wantErr:          "Expected provider wrapper to launch the pinned native Claude binary with managed env",
		},
		{
			// Escaped-exec needle (pinned Gemini node line, trailing backslash).
			name:             "provider wrapper missing pinned gemini exec",
			providerWrapper:  strings.Replace(providerLauncherAuthorityHappyProviderWrapper, `real/node \`, `real/node`, 1),
			workcellLauncher: providerLauncherAuthorityHappyWorkcellLauncher,
			launcherCommon:   providerLauncherAuthorityHappyLauncherCommon,
			rustLib:          providerLauncherAuthorityHappyLib,
			wantErr:          "Expected provider wrapper to pin Gemini native sandbox off on the managed path",
		},
		{
			// Consumed-marker guard (second probe): the `: >"${...}"` write
			// removed proves the two ordered probes share one message.
			name:             "provider wrapper missing consumed-marker write",
			providerWrapper:  strings.Replace(providerLauncherAuthorityHappyProviderWrapper, `: >"${copilot_token_handoff_consumed_file}"`, `: >/dev/null`, 1),
			workcellLauncher: providerLauncherAuthorityHappyWorkcellLauncher,
			launcherCommon:   providerLauncherAuthorityHappyLauncherCommon,
			rustLib:          providerLauncherAuthorityHappyLib,
			wantErr:          "Expected provider wrapper to write a host-visible Copilot token consumed marker",
		},
		{
			// kindAbsent: a caller-supplied WORKCELL_COPILOT_TOKEN_FILE default
			// present is a violation.
			name:             "provider wrapper trusts caller token file",
			providerWrapper:  providerLauncherAuthorityHappyProviderWrapper + "\nlocal token_file=\"${WORKCELL_COPILOT_TOKEN_FILE:-}\"\n",
			workcellLauncher: providerLauncherAuthorityHappyWorkcellLauncher,
			launcherCommon:   providerLauncherAuthorityHappyLauncherCommon,
			rustLib:          providerLauncherAuthorityHappyLib,
			wantErr:          "Provider wrapper must not trust caller-supplied WORKCELL_COPILOT_TOKEN_FILE",
		},
		{
			// Staged-token guard (second probe, kindAbsent): a
			// WORKCELL_COPILOT_GITHUB_TOKEN:- env fallback present is a violation.
			// The first probe still holds, so this proves the mixed
			// present/absent pair shares one message.
			name:             "provider wrapper allows github token env fallback",
			providerWrapper:  providerLauncherAuthorityHappyProviderWrapper + "\ncopilot_github_token=\"${WORKCELL_COPILOT_GITHUB_TOKEN:-}\"\n",
			workcellLauncher: providerLauncherAuthorityHappyWorkcellLauncher,
			launcherCommon:   providerLauncherAuthorityHappyLauncherCommon,
			rustLib:          providerLauncherAuthorityHappyLib,
			wantErr:          "Expected provider wrapper to require staged Copilot token files instead of caller-supplied token env fallbacks",
		},
		{
			// A missing provider-wrapper.sh is empty content: the first
			// affirmative provider-wrapper probe (the authority marker) fails.
			name:             "missing provider wrapper",
			providerWrapper:  "",
			workcellLauncher: providerLauncherAuthorityHappyWorkcellLauncher,
			launcherCommon:   providerLauncherAuthorityHappyLauncherCommon,
			rustLib:          providerLauncherAuthorityHappyLib,
			wantErr:          "Expected provider wrapper to require the managed launcher authority marker",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := writeProviderLauncherAuthorityRepo(t, tc.providerWrapper, tc.workcellLauncher, tc.launcherCommon, tc.rustLib)
			err := CheckProviderLauncherAuthority(root)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("CheckProviderLauncherAuthority() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("CheckProviderLauncherAuthority() = nil, want error %q", tc.wantErr)
			}
			if err.Error() != tc.wantErr {
				t.Fatalf("CheckProviderLauncherAuthority() error = %q, want %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// TestCheckProviderLauncherAuthorityCount asserts the check list contains
// exactly thirty invariants, guarding against an accidentally truncated or
// duplicated migration of the shell block.
func TestCheckProviderLauncherAuthorityCount(t *testing.T) {
	got := len(providerLauncherAuthorityChecks())
	const want = 30
	if got != want {
		t.Fatalf("providerLauncherAuthorityChecks() has %d checks, want %d", got, want)
	}
}

// TestCheckProviderLauncherAuthorityRealRepo asserts that the real
// provider-wrapper.sh, workcell-launcher.rs, launcher_common.rs, and lib.rs in
// this repository satisfy all thirty provider-launcher-authority invariants.
// This is the key guard against a mis-transcribed needle, a mis-resolved
// variable needle, or a wrong target file: if any Go pattern is not a byte-exact
// substring of the actual file (or the wrong file), this test fails with the
// guard's stderr message.
func TestCheckProviderLauncherAuthorityRealRepo(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	if _, err := os.Stat(filepath.Join(repoRoot, providerWrapperRelPath)); err != nil {
		t.Skipf("real provider-wrapper.sh not found at %s: %v", repoRoot, err)
	}
	if err := CheckProviderLauncherAuthority(repoRoot); err != nil {
		t.Fatalf("CheckProviderLauncherAuthority(real repo) = %v, want nil", err)
	}
}

// copilotPolicyWrapperHappyProviderWrapper is a minimal but structurally
// faithful runtime/container/provider-wrapper.sh: it contains every affirmative
// provider-wrapper needle (the two token-handoff env scrubs, the managed
// COPILOT_GITHUB_TOKEN export, the HTTP/2 pin, the secret-env/temp-dir/available
// tools flags, and the pinned Copilot exec line with its trailing backslash) and
// NEITHER the two negated fixed-string needles (no --allow-all-tools, no
// --allow-all-paths) NOR any shell-like --available-tools grant (so the negated
// grep -Eq guard also holds).
const copilotPolicyWrapperHappyProviderWrapper = `#!/usr/bin/env bash
set -euo pipefail
unset WORKCELL_COPILOT_GITHUB_TOKEN
unset WORKCELL_COPILOT_TOKEN_FILE
COPILOT_GITHUB_TOKEN="${copilot_github_token}" \
COPILOT_ENABLE_HTTP2=false \
exec /usr/local/libexec/workcell/real/copilot \
  --secret-env-vars=GH_TOKEN,GITHUB_TOKEN,COPILOT_GITHUB_TOKEN \
  --disallow-temp-dir \
  "--available-tools=view,create,edit,apply_patch,grep,glob"
`

// copilotPolicyWrapperHappyProviderPolicy is a minimal but structurally faithful
// runtime/container/provider-policy.sh: it contains every affirmative policy
// needle (the two blocked-lifecycle messages, the -p/--prompt case label, the two
// attached-prompt value extractions, the bundled-short-option case label, and the
// bundled-short-option blocked message) and NOT the -i/--interactive prompt-alias
// case label.
const copilotPolicyWrapperHappyProviderPolicy = `#!/usr/bin/env bash
copilot_policy() {
  case "${arg}" in
  -p | --prompt)
    attached_prompt_value="${arg:2}"
    attached_prompt_value="${arg#--prompt=}"
    ;;
  -[!-]?*)
    workcell_die "Workcell blocked bundled Copilot short options: ${arg}"
    ;;
  esac
  workcell_die "Workcell blocked Claude lifecycle command: ${arg}"
  workcell_die "Workcell blocked Copilot lifecycle/control-plane command: ${arg}"
}
`

// copilotPolicyWrapperHappySmoke is a minimal but structurally faithful
// scripts/container-smoke.sh: it contains the three container-smoke needles for
// the attached-prompt and bundled-short-option guards.
const copilotPolicyWrapperHappySmoke = `#!/usr/bin/env bash
run_case workcell-copilot-policy-attached-short-prompt-allow-tool.out
run_case workcell-copilot-policy-attached-long-prompt-allow-tool.out
run_case workcell-copilot-policy-bundled-short-options.out
`

// writeCopilotPolicyWrapperRepo materializes a fake repo with the three files
// this group reads set to the given bodies; a body of "" means "do not create
// that file" (unreadable-target case).
func writeCopilotPolicyWrapperRepo(t *testing.T, providerWrapper, providerPolicy, smoke string) string {
	t.Helper()
	root := t.TempDir()
	write := func(rel, body string) {
		if body == "" {
			return
		}
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	write(providerWrapperRelPath, providerWrapper)
	write(providerPolicyRelPath, providerPolicy)
	write(containerSmokeRelPath, smoke)
	return root
}

func TestCheckCopilotPolicyWrapper(t *testing.T) {
	tests := []struct {
		name            string
		providerWrapper string
		providerPolicy  string
		smoke           string
		wantErr         string // "" means expect success
	}{
		{
			name:            "happy path all invariants hold",
			providerWrapper: copilotPolicyWrapperHappyProviderWrapper,
			providerPolicy:  copilotPolicyWrapperHappyProviderPolicy,
			smoke:           copilotPolicyWrapperHappySmoke,
		},
		{
			// Check 1 (kindPresent, provider-wrapper.sh).
			name:            "provider wrapper keeps host-side github token env",
			providerWrapper: strings.Replace(copilotPolicyWrapperHappyProviderWrapper, "unset WORKCELL_COPILOT_GITHUB_TOKEN", "unset OTHER", 1),
			providerPolicy:  copilotPolicyWrapperHappyProviderPolicy,
			smoke:           copilotPolicyWrapperHappySmoke,
			wantErr:         "Expected provider wrapper to discard the host-side Copilot token handoff variable before exec",
		},
		{
			// Check 2 (kindPresent, provider-wrapper.sh).
			name:            "provider wrapper keeps token file path env",
			providerWrapper: strings.Replace(copilotPolicyWrapperHappyProviderWrapper, "unset WORKCELL_COPILOT_TOKEN_FILE", "unset OTHER", 1),
			providerPolicy:  copilotPolicyWrapperHappyProviderPolicy,
			smoke:           copilotPolicyWrapperHappySmoke,
			wantErr:         "Expected provider wrapper to discard the Copilot token handoff path before exec",
		},
		{
			// Check 3 (kindPresent, provider-wrapper.sh — escaped ${...} needle).
			name:            "provider wrapper missing managed COPILOT_GITHUB_TOKEN export",
			providerWrapper: strings.Replace(copilotPolicyWrapperHappyProviderWrapper, `COPILOT_GITHUB_TOKEN="${copilot_github_token}"`, `COPILOT_GITHUB_TOKEN="x"`, 1),
			providerPolicy:  copilotPolicyWrapperHappyProviderPolicy,
			smoke:           copilotPolicyWrapperHappySmoke,
			wantErr:         "Expected provider wrapper to expose Copilot auth only as COPILOT_GITHUB_TOKEN to the managed child",
		},
		{
			// Check 4 (kindPresent, provider-wrapper.sh).
			name:            "provider wrapper missing http2 pin",
			providerWrapper: strings.Replace(copilotPolicyWrapperHappyProviderWrapper, "COPILOT_ENABLE_HTTP2=false", "COPILOT_ENABLE_HTTP2=true", 1),
			providerPolicy:  copilotPolicyWrapperHappyProviderPolicy,
			smoke:           copilotPolicyWrapperHappySmoke,
			wantErr:         "Expected provider wrapper to pin Copilot HTTP/2 off on the managed path",
		},
		{
			// Check 5 (kindPresent, provider-wrapper.sh).
			name:            "provider wrapper missing secret-env-vars",
			providerWrapper: strings.Replace(copilotPolicyWrapperHappyProviderWrapper, "--secret-env-vars=GH_TOKEN,GITHUB_TOKEN,COPILOT_GITHUB_TOKEN", "--secret-env-vars=NONE", 1),
			providerPolicy:  copilotPolicyWrapperHappyProviderPolicy,
			smoke:           copilotPolicyWrapperHappySmoke,
			wantErr:         "Expected provider wrapper to declare Copilot/GitHub token env as provider secrets",
		},
		{
			// Check 6 (kindPresent, provider-wrapper.sh).
			name:            "provider wrapper missing disallow-temp-dir",
			providerWrapper: strings.Replace(copilotPolicyWrapperHappyProviderWrapper, "--disallow-temp-dir", "--allow-temp-dir", 1),
			providerPolicy:  copilotPolicyWrapperHappyProviderPolicy,
			smoke:           copilotPolicyWrapperHappySmoke,
			wantErr:         "Expected provider wrapper to deny Copilot temp-dir access on the managed path",
		},
		{
			// Check 7 (kindPresent, provider-wrapper.sh — quoted needle).
			name:            "provider wrapper missing shell-free available-tools grant",
			providerWrapper: strings.Replace(copilotPolicyWrapperHappyProviderWrapper, `"--available-tools=view,create,edit,apply_patch,grep,glob"`, `"--available-tools=view"`, 1),
			providerPolicy:  copilotPolicyWrapperHappyProviderPolicy,
			smoke:           copilotPolicyWrapperHappySmoke,
			wantErr:         "Expected provider wrapper to keep Copilot prompt/yolo tool grants shell-free",
		},
		{
			// Check 8 (kindRegexAbsent, provider-wrapper.sh): an unquoted
			// available-tools value granting a shell-like tool is a violation.
			name:            "provider wrapper grants shell-like available tool",
			providerWrapper: copilotPolicyWrapperHappyProviderWrapper + "\n--available-tools=view,shell\n",
			providerPolicy:  copilotPolicyWrapperHappyProviderPolicy,
			smoke:           copilotPolicyWrapperHappySmoke,
			wantErr:         "Provider wrapper must not grant Copilot shell-like tools on the safe path",
		},
		{
			// Check 9a (kindAbsent, provider-wrapper.sh — first probe of the
			// all-tools/all-paths guard).
			name:            "provider wrapper grants all tools",
			providerWrapper: copilotPolicyWrapperHappyProviderWrapper + "\n  --allow-all-tools\n",
			providerPolicy:  copilotPolicyWrapperHappyProviderPolicy,
			smoke:           copilotPolicyWrapperHappySmoke,
			wantErr:         "Provider wrapper must not grant Copilot all tools or all paths on the safe path",
		},
		{
			// Check 9b (kindAbsent, provider-wrapper.sh — second probe): proves the
			// two ordered probes share one message.
			name:            "provider wrapper grants all paths",
			providerWrapper: copilotPolicyWrapperHappyProviderWrapper + "\n  --allow-all-paths\n",
			providerPolicy:  copilotPolicyWrapperHappyProviderPolicy,
			smoke:           copilotPolicyWrapperHappySmoke,
			wantErr:         "Provider wrapper must not grant Copilot all tools or all paths on the safe path",
		},
		{
			// Check 10 (kindPresent, provider-wrapper.sh — trailing backslash):
			// drop the trailing backslash so the fixed-string containment fails.
			name:            "provider wrapper missing pinned copilot exec",
			providerWrapper: strings.Replace(copilotPolicyWrapperHappyProviderWrapper, `real/copilot \`, `real/copilot`, 1),
			providerPolicy:  copilotPolicyWrapperHappyProviderPolicy,
			smoke:           copilotPolicyWrapperHappySmoke,
			wantErr:         "Expected provider wrapper to launch the pinned native Copilot binary",
		},
		{
			// Check 11 (kindPresent, provider-policy.sh — ${arg} needle).
			name:            "provider policy missing claude lifecycle block",
			providerWrapper: copilotPolicyWrapperHappyProviderWrapper,
			providerPolicy:  strings.Replace(copilotPolicyWrapperHappyProviderPolicy, "Workcell blocked Claude lifecycle command: ${arg}", "blocked", 1),
			smoke:           copilotPolicyWrapperHappySmoke,
			wantErr:         "Expected provider policy to reject native Claude lifecycle commands that bypass the pinned image",
		},
		{
			// Check 12 (kindPresent, provider-policy.sh).
			name:            "provider policy missing copilot lifecycle block",
			providerWrapper: copilotPolicyWrapperHappyProviderWrapper,
			providerPolicy:  strings.Replace(copilotPolicyWrapperHappyProviderPolicy, "Workcell blocked Copilot lifecycle/control-plane command: ${arg}", "blocked", 1),
			smoke:           copilotPolicyWrapperHappySmoke,
			wantErr:         "Expected provider policy to reject native Copilot lifecycle/control-plane commands",
		},
		{
			// Check 13 (kindPresent, provider-policy.sh).
			name:            "provider policy missing prompt case label",
			providerWrapper: copilotPolicyWrapperHappyProviderWrapper,
			providerPolicy:  strings.Replace(copilotPolicyWrapperHappyProviderPolicy, "-p | --prompt)", "-x)", 1),
			smoke:           copilotPolicyWrapperHappySmoke,
			wantErr:         "Expected provider policy to treat only Copilot -p/--prompt as value-taking prompt flags",
		},
		{
			// Check 14a (kindPresent, provider-policy.sh — first probe of the
			// attached-prompt guard).
			name:            "provider policy missing short attached-prompt extraction",
			providerWrapper: copilotPolicyWrapperHappyProviderWrapper,
			providerPolicy:  strings.Replace(copilotPolicyWrapperHappyProviderPolicy, `attached_prompt_value="${arg:2}"`, `attached_prompt_value="x"`, 1),
			smoke:           copilotPolicyWrapperHappySmoke,
			wantErr:         "Expected provider policy and smoke coverage to reject attached dash-prefixed Copilot prompt values",
		},
		{
			// Check 14b (kindPresent, provider-policy.sh — second probe).
			name:            "provider policy missing long attached-prompt extraction",
			providerWrapper: copilotPolicyWrapperHappyProviderWrapper,
			providerPolicy:  strings.Replace(copilotPolicyWrapperHappyProviderPolicy, `attached_prompt_value="${arg#--prompt=}"`, `attached_prompt_value="y"`, 1),
			smoke:           copilotPolicyWrapperHappySmoke,
			wantErr:         "Expected provider policy and smoke coverage to reject attached dash-prefixed Copilot prompt values",
		},
		{
			// Check 14c (kindPresent, container-smoke.sh — third probe): proves the
			// guard reads the smoke harness and shares one message.
			name:            "smoke missing short attached-prompt coverage",
			providerWrapper: copilotPolicyWrapperHappyProviderWrapper,
			providerPolicy:  copilotPolicyWrapperHappyProviderPolicy,
			smoke:           strings.Replace(copilotPolicyWrapperHappySmoke, "workcell-copilot-policy-attached-short-prompt-allow-tool.out", "other.out", 1),
			wantErr:         "Expected provider policy and smoke coverage to reject attached dash-prefixed Copilot prompt values",
		},
		{
			// Check 14d (kindPresent, container-smoke.sh — fourth probe).
			name:            "smoke missing long attached-prompt coverage",
			providerWrapper: copilotPolicyWrapperHappyProviderWrapper,
			providerPolicy:  copilotPolicyWrapperHappyProviderPolicy,
			smoke:           strings.Replace(copilotPolicyWrapperHappySmoke, "workcell-copilot-policy-attached-long-prompt-allow-tool.out", "other.out", 1),
			wantErr:         "Expected provider policy and smoke coverage to reject attached dash-prefixed Copilot prompt values",
		},
		{
			// Check 15a (kindPresent, provider-policy.sh — first probe of the
			// bundled-short-option guard).
			name:            "provider policy missing bundled short-option case label",
			providerWrapper: copilotPolicyWrapperHappyProviderWrapper,
			providerPolicy:  strings.Replace(copilotPolicyWrapperHappyProviderPolicy, "-[!-]?*)", "-z)", 1),
			smoke:           copilotPolicyWrapperHappySmoke,
			wantErr:         "Expected provider policy and smoke coverage to reject bundled Copilot short options",
		},
		{
			// Check 15b (kindPresent, provider-policy.sh — second probe).
			name:            "provider policy missing bundled short-option block",
			providerWrapper: copilotPolicyWrapperHappyProviderWrapper,
			providerPolicy:  strings.Replace(copilotPolicyWrapperHappyProviderPolicy, "Workcell blocked bundled Copilot short options: ${arg}", "blocked", 1),
			smoke:           copilotPolicyWrapperHappySmoke,
			wantErr:         "Expected provider policy and smoke coverage to reject bundled Copilot short options",
		},
		{
			// Check 15c (kindPresent, container-smoke.sh — third probe).
			name:            "smoke missing bundled short-option coverage",
			providerWrapper: copilotPolicyWrapperHappyProviderWrapper,
			providerPolicy:  copilotPolicyWrapperHappyProviderPolicy,
			smoke:           strings.Replace(copilotPolicyWrapperHappySmoke, "workcell-copilot-policy-bundled-short-options.out", "other.out", 1),
			wantErr:         "Expected provider policy and smoke coverage to reject bundled Copilot short options",
		},
		{
			// Check 16 (kindAbsent, provider-policy.sh): treating -i/--interactive
			// as a prompt alias present is a violation.
			name:            "provider policy treats interactive as prompt alias",
			providerWrapper: copilotPolicyWrapperHappyProviderWrapper,
			providerPolicy:  copilotPolicyWrapperHappyProviderPolicy + "\n  -p | --prompt | -i | --interactive)\n",
			smoke:           copilotPolicyWrapperHappySmoke,
			wantErr:         "Expected provider policy not to treat Copilot -i/--interactive as prompt aliases",
		},
		{
			// A missing provider-wrapper.sh is empty content: the first affirmative
			// provider-wrapper probe (the github-token env scrub) fails.
			name:            "missing provider wrapper",
			providerWrapper: "",
			providerPolicy:  copilotPolicyWrapperHappyProviderPolicy,
			smoke:           copilotPolicyWrapperHappySmoke,
			wantErr:         "Expected provider wrapper to discard the host-side Copilot token handoff variable before exec",
		},
		{
			// A missing provider-policy.sh is empty content: the first policy probe
			// (the Claude lifecycle block) fails after every provider-wrapper probe
			// holds.
			name:            "missing provider policy",
			providerWrapper: copilotPolicyWrapperHappyProviderWrapper,
			providerPolicy:  "",
			smoke:           copilotPolicyWrapperHappySmoke,
			wantErr:         "Expected provider policy to reject native Claude lifecycle commands that bypass the pinned image",
		},
		{
			// A missing container-smoke.sh is empty content: the first smoke probe
			// (the attached short-prompt coverage) fails after the wrapper and the
			// two policy attached-prompt probes hold.
			name:            "missing container smoke",
			providerWrapper: copilotPolicyWrapperHappyProviderWrapper,
			providerPolicy:  copilotPolicyWrapperHappyProviderPolicy,
			smoke:           "",
			wantErr:         "Expected provider policy and smoke coverage to reject attached dash-prefixed Copilot prompt values",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := writeCopilotPolicyWrapperRepo(t, tc.providerWrapper, tc.providerPolicy, tc.smoke)
			err := CheckCopilotPolicyWrapper(root)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("CheckCopilotPolicyWrapper() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("CheckCopilotPolicyWrapper() = nil, want error %q", tc.wantErr)
			}
			if err.Error() != tc.wantErr {
				t.Fatalf("CheckCopilotPolicyWrapper() error = %q, want %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// TestCheckCopilotPolicyWrapperCount asserts the check list contains exactly
// twenty-two invariants, guarding against an accidentally truncated or
// duplicated migration of the shell block.
func TestCheckCopilotPolicyWrapperCount(t *testing.T) {
	got := len(copilotPolicyWrapperChecks)
	const want = 22
	if got != want {
		t.Fatalf("copilotPolicyWrapperChecks has %d checks, want %d", got, want)
	}
}

// TestCheckCopilotPolicyWrapperRealRepo asserts that the real provider-wrapper.sh,
// provider-policy.sh, and container-smoke.sh in this repository satisfy all
// twenty-two Copilot-policy-wrapper invariants.  This is the key guard against a
// mis-transcribed needle or a wrong target file: if any Go pattern is not a
// byte-exact substring of the actual file (or the wrong file), this test fails
// with the guard's stderr message.
func TestCheckCopilotPolicyWrapperRealRepo(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	if _, err := os.Stat(filepath.Join(repoRoot, providerWrapperRelPath)); err != nil {
		t.Skipf("real provider-wrapper.sh not found at %s: %v", repoRoot, err)
	}
	if err := CheckCopilotPolicyWrapper(repoRoot); err != nil {
		t.Fatalf("CheckCopilotPolicyWrapper(real repo) = %v, want nil", err)
	}
}

// copilotUnsafeFlagsHappyProviderPolicy is a minimal but structurally faithful
// runtime/container/provider-policy.sh: it contains all sixteen unsafe long
// flags, all five attached short-flag forms (with the `?*` glob characters as
// literal text, matched by grep -Fq), and the second bare short-flag snippet
// `-C | -i | -n | -r | -w)` (the FIRST bare snippet lives in the smoke harness,
// so the two loop-3 items are deliberately split across the two files to prove
// the OR across them is real).
const copilotUnsafeFlagsHappyProviderPolicy = `#!/usr/bin/env bash
copilot_policy() {
  case "${arg}" in
  --config-dir | --allow-tool | --allow-all-tools | --allow-all-mcp-server-instructions) ;;
  --available-tools | --secret-env-vars | --no-auto-update | --no-remote | --no-remote-export) ;;
  --disable-builtin-mcps | --disallow-temp-dir | --dynamic-retrieval | --interactive) ;;
  --no-bash-env | --plan | --worktree) ;;
  -c?* | -i?* | -a?* | -A?* | -w?*) ;;
  -C | -i | -n | -r | -w) ;;
  esac
}
`

// copilotUnsafeFlagsHappySmoke is a minimal but structurally faithful
// scripts/container-smoke.sh: it contains the FIRST bare short-flag snippet
// `copilot_short_flag in -C -i -n -r -w` (its only home; provider-policy.sh has
// the second), the development-wrapper loader coverage needles, and the
// forged-auth smoke needle.
const copilotUnsafeFlagsHappySmoke = `#!/usr/bin/env bash
# copilot_short_flag in -C -i -n -r -w
run_case development-wrapper-copilot-loader.out
run_case workcell-copilot-real-copy.out
run_case forged-copilot-token.out
`

// copilotUnsafeFlagsHappyProviderWrapper is a minimal but structurally faithful
// runtime/container/provider-wrapper.sh containing the argv re-check needle.
const copilotUnsafeFlagsHappyProviderWrapper = `#!/usr/bin/env bash
reject_unsafe_copilot_args "$@"
`

// copilotUnsafeFlagsHappyDevelopmentWrapper is a minimal but structurally
// faithful runtime/container/development-wrapper.sh containing the protected
// runtime target rejection needle.
const copilotUnsafeFlagsHappyDevelopmentWrapper = `#!/usr/bin/env bash
reject_protected_runtime_arguments "$@"
`

// copilotUnsafeFlagsHappyRustLib is a minimal but structurally faithful
// runtime/container/rust/src/lib.rs containing both exec-guard needles.
const copilotUnsafeFlagsHappyRustLib = `pub fn approved_wrapper_allows_runtime(w: ApprovedWrapper) -> bool {
    match w {
        ApprovedWrapper::Development | ApprovedWrapper::None => false,
        _ => true,
    }
}
`

// copilotUnsafeFlagsHappyLauncherCommon is a minimal but structurally faithful
// runtime/container/rust/src/bin/common/launcher_common.rs containing the
// forged-auth env needle.
const copilotUnsafeFlagsHappyLauncherCommon = `pub const COPILOT_TOKEN_ENV: &str = "WORKCELL_COPILOT_GITHUB_TOKEN";
`

// writeCopilotUnsafeFlagsRepo materializes a fake repo with the six files this
// group reads set to the given bodies; a body of "" means "do not create that
// file" (unreadable-target case).
func writeCopilotUnsafeFlagsRepo(t *testing.T, providerPolicy, smoke, providerWrapper, developmentWrapper, rustLib, launcherCommon string) string {
	t.Helper()
	root := t.TempDir()
	write := func(rel, body string) {
		if body == "" {
			return
		}
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	write(providerPolicyRelPath, providerPolicy)
	write(containerSmokeRelPath, smoke)
	write(providerWrapperRelPath, providerWrapper)
	write(developmentWrapperRelPath, developmentWrapper)
	write(rustLibRelPath, rustLib)
	write(launcherCommonRustRelPath, launcherCommon)
	return root
}

func TestCheckCopilotUnsafeFlags(t *testing.T) {
	tests := []struct {
		name               string
		providerPolicy     string
		smoke              string
		providerWrapper    string
		developmentWrapper string
		rustLib            string
		launcherCommon     string
		wantErr            string // "" means expect success
	}{
		{
			name:               "happy path all invariants hold",
			providerPolicy:     copilotUnsafeFlagsHappyProviderPolicy,
			smoke:              copilotUnsafeFlagsHappySmoke,
			providerWrapper:    copilotUnsafeFlagsHappyProviderWrapper,
			developmentWrapper: copilotUnsafeFlagsHappyDevelopmentWrapper,
			rustLib:            copilotUnsafeFlagsHappyRustLib,
			launcherCommon:     copilotUnsafeFlagsHappyLauncherCommon,
		},
		{
			// Loop 1 (kindPresent, provider-policy.sh — first flag).
			name:               "provider policy missing first unsafe long flag",
			providerPolicy:     strings.Replace(copilotUnsafeFlagsHappyProviderPolicy, "--config-dir", "--other", 1),
			smoke:              copilotUnsafeFlagsHappySmoke,
			providerWrapper:    copilotUnsafeFlagsHappyProviderWrapper,
			developmentWrapper: copilotUnsafeFlagsHappyDevelopmentWrapper,
			rustLib:            copilotUnsafeFlagsHappyRustLib,
			launcherCommon:     copilotUnsafeFlagsHappyLauncherCommon,
			wantErr:            "Expected provider policy to reject Copilot unsafe flag: --config-dir",
		},
		{
			// Loop 1 (kindPresent, provider-policy.sh — last flag).
			name:               "provider policy missing last unsafe long flag",
			providerPolicy:     strings.Replace(copilotUnsafeFlagsHappyProviderPolicy, "--worktree", "--other", 1),
			smoke:              copilotUnsafeFlagsHappySmoke,
			providerWrapper:    copilotUnsafeFlagsHappyProviderWrapper,
			developmentWrapper: copilotUnsafeFlagsHappyDevelopmentWrapper,
			rustLib:            copilotUnsafeFlagsHappyRustLib,
			launcherCommon:     copilotUnsafeFlagsHappyLauncherCommon,
			wantErr:            "Expected provider policy to reject Copilot unsafe flag: --worktree",
		},
		{
			// Loop 2 (kindPresent, provider-policy.sh — literal `?*` short form).
			name:               "provider policy missing attached short form",
			providerPolicy:     strings.Replace(copilotUnsafeFlagsHappyProviderPolicy, "-a?*", "-a", 1),
			smoke:              copilotUnsafeFlagsHappySmoke,
			providerWrapper:    copilotUnsafeFlagsHappyProviderWrapper,
			developmentWrapper: copilotUnsafeFlagsHappyDevelopmentWrapper,
			rustLib:            copilotUnsafeFlagsHappyRustLib,
			launcherCommon:     copilotUnsafeFlagsHappyLauncherCommon,
			wantErr:            "Expected provider policy to reject Copilot attached unsafe short flag: -a?*",
		},
		{
			// Loop 3 (kindPresentInAnyFile): the first bare snippet lives only in the
			// smoke harness, so removing it there (it is absent from the policy) makes
			// it absent from BOTH files → violation.
			name:               "bare short flag absent from both files",
			providerPolicy:     copilotUnsafeFlagsHappyProviderPolicy,
			smoke:              strings.Replace(copilotUnsafeFlagsHappySmoke, "copilot_short_flag in -C -i -n -r -w", "other", 1),
			providerWrapper:    copilotUnsafeFlagsHappyProviderWrapper,
			developmentWrapper: copilotUnsafeFlagsHappyDevelopmentWrapper,
			rustLib:            copilotUnsafeFlagsHappyRustLib,
			launcherCommon:     copilotUnsafeFlagsHappyLauncherCommon,
			wantErr:            "Expected Copilot bare unsafe short flags to be rejected and smoke-tested: copilot_short_flag in -C -i -n -r -w",
		},
		{
			// Guard (kindPresent, provider-wrapper.sh).
			name:               "provider wrapper missing argv re-check",
			providerPolicy:     copilotUnsafeFlagsHappyProviderPolicy,
			smoke:              copilotUnsafeFlagsHappySmoke,
			providerWrapper:    strings.Replace(copilotUnsafeFlagsHappyProviderWrapper, "reject_unsafe_copilot_args", "other", 1),
			developmentWrapper: copilotUnsafeFlagsHappyDevelopmentWrapper,
			rustLib:            copilotUnsafeFlagsHappyRustLib,
			launcherCommon:     copilotUnsafeFlagsHappyLauncherCommon,
			wantErr:            "Expected provider wrapper to re-check Copilot argv before launch",
		},
		{
			// Guard (kindPresent, development-wrapper.sh).
			name:               "development wrapper missing protected target rejection",
			providerPolicy:     copilotUnsafeFlagsHappyProviderPolicy,
			smoke:              copilotUnsafeFlagsHappySmoke,
			providerWrapper:    copilotUnsafeFlagsHappyProviderWrapper,
			developmentWrapper: strings.Replace(copilotUnsafeFlagsHappyDevelopmentWrapper, `reject_protected_runtime_arguments "$@"`, "other", 1),
			rustLib:            copilotUnsafeFlagsHappyRustLib,
			launcherCommon:     copilotUnsafeFlagsHappyLauncherCommon,
			wantErr:            "Expected development wrapper to reject loader-mediated protected runtime targets before exec",
		},
		{
			// Guard (kindPresent, container-smoke.sh).
			name:               "smoke missing development-wrapper loader coverage",
			providerPolicy:     copilotUnsafeFlagsHappyProviderPolicy,
			smoke:              strings.Replace(copilotUnsafeFlagsHappySmoke, "development-wrapper-copilot-loader", "other", 1),
			providerWrapper:    copilotUnsafeFlagsHappyProviderWrapper,
			developmentWrapper: copilotUnsafeFlagsHappyDevelopmentWrapper,
			rustLib:            copilotUnsafeFlagsHappyRustLib,
			launcherCommon:     copilotUnsafeFlagsHappyLauncherCommon,
			wantErr:            "Expected container smoke to cover development-wrapper loader-mediated Copilot execution",
		},
		{
			// Multi-probe guard (first probe): exec-guard wrapper-specific pair,
			// rust/src/lib.rs.
			name:               "exec guard missing wrapper-specific match arm",
			providerPolicy:     copilotUnsafeFlagsHappyProviderPolicy,
			smoke:              copilotUnsafeFlagsHappySmoke,
			providerWrapper:    copilotUnsafeFlagsHappyProviderWrapper,
			developmentWrapper: copilotUnsafeFlagsHappyDevelopmentWrapper,
			rustLib:            strings.Replace(copilotUnsafeFlagsHappyRustLib, "ApprovedWrapper::Development | ApprovedWrapper::None => false", "_ => false", 1),
			launcherCommon:     copilotUnsafeFlagsHappyLauncherCommon,
			wantErr:            "Expected exec guard to keep protected runtime authorization wrapper-specific",
		},
		{
			// Multi-probe guard (second probe): proves the two ordered probes share
			// one message.
			name:               "exec guard missing runtime authorization helper",
			providerPolicy:     copilotUnsafeFlagsHappyProviderPolicy,
			smoke:              copilotUnsafeFlagsHappySmoke,
			providerWrapper:    copilotUnsafeFlagsHappyProviderWrapper,
			developmentWrapper: copilotUnsafeFlagsHappyDevelopmentWrapper,
			rustLib:            strings.Replace(copilotUnsafeFlagsHappyRustLib, "approved_wrapper_allows_runtime", "other_helper", 1),
			launcherCommon:     copilotUnsafeFlagsHappyLauncherCommon,
			wantErr:            "Expected exec guard to keep protected runtime authorization wrapper-specific",
		},
		{
			// Multi-probe guard (first probe): forged-auth pair, launcher_common.rs.
			name:               "launcher missing forged auth env token",
			providerPolicy:     copilotUnsafeFlagsHappyProviderPolicy,
			smoke:              copilotUnsafeFlagsHappySmoke,
			providerWrapper:    copilotUnsafeFlagsHappyProviderWrapper,
			developmentWrapper: copilotUnsafeFlagsHappyDevelopmentWrapper,
			rustLib:            copilotUnsafeFlagsHappyRustLib,
			launcherCommon:     strings.Replace(copilotUnsafeFlagsHappyLauncherCommon, "WORKCELL_COPILOT_GITHUB_TOKEN", "OTHER_TOKEN", 1),
			wantErr:            "Expected launcher and smoke coverage to reject forged Copilot auth env",
		},
		{
			// Multi-probe guard (second probe, container-smoke.sh): shares the message.
			name:               "smoke missing forged auth coverage",
			providerPolicy:     copilotUnsafeFlagsHappyProviderPolicy,
			smoke:              strings.Replace(copilotUnsafeFlagsHappySmoke, "forged-copilot-token", "other", 1),
			providerWrapper:    copilotUnsafeFlagsHappyProviderWrapper,
			developmentWrapper: copilotUnsafeFlagsHappyDevelopmentWrapper,
			rustLib:            copilotUnsafeFlagsHappyRustLib,
			launcherCommon:     copilotUnsafeFlagsHappyLauncherCommon,
			wantErr:            "Expected launcher and smoke coverage to reject forged Copilot auth env",
		},
		{
			// A missing provider-policy.sh is empty content: the first loop-1 probe
			// fails (the --config-dir flag).
			name:               "missing provider policy",
			providerPolicy:     "",
			smoke:              copilotUnsafeFlagsHappySmoke,
			providerWrapper:    copilotUnsafeFlagsHappyProviderWrapper,
			developmentWrapper: copilotUnsafeFlagsHappyDevelopmentWrapper,
			rustLib:            copilotUnsafeFlagsHappyRustLib,
			launcherCommon:     copilotUnsafeFlagsHappyLauncherCommon,
			wantErr:            "Expected provider policy to reject Copilot unsafe flag: --config-dir",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := writeCopilotUnsafeFlagsRepo(t, tc.providerPolicy, tc.smoke, tc.providerWrapper, tc.developmentWrapper, tc.rustLib, tc.launcherCommon)
			err := CheckCopilotUnsafeFlags(root)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("CheckCopilotUnsafeFlags() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("CheckCopilotUnsafeFlags() = nil, want error %q", tc.wantErr)
			}
			if err.Error() != tc.wantErr {
				t.Fatalf("CheckCopilotUnsafeFlags() error = %q, want %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// TestCheckCopilotUnsafeFlagsBareShortFormOrSemantics exercises the new
// kindPresentInAnyFile OR semantics directly against the loop-3 first bare
// short-flag snippet: it must hold when the snippet is present in the smoke
// harness only, when present in the provider policy only, and must fail only
// when absent from BOTH files — mirroring `grep -Fq -- NEEDLE f1 f2`.
func TestCheckCopilotUnsafeFlagsBareShortFormOrSemantics(t *testing.T) {
	const snippet = "copilot_short_flag in -C -i -n -r -w"
	// A policy body that still satisfies every non-loop-3 policy probe but does
	// NOT contain the first bare snippet by default (the happy policy already
	// omits it; the second bare snippet stays present so loop-3 item 2 holds).
	policyWithout := copilotUnsafeFlagsHappyProviderPolicy
	policyWith := copilotUnsafeFlagsHappyProviderPolicy + "\n  # " + snippet + "\n"
	// A smoke body that satisfies every non-loop-3 smoke probe but omits the
	// first bare snippet.
	smokeWithout := strings.Replace(copilotUnsafeFlagsHappySmoke, snippet, "placeholder", 1)
	smokeWith := copilotUnsafeFlagsHappySmoke

	cases := []struct {
		name    string
		policy  string
		smoke   string
		wantErr bool
	}{
		{name: "present in smoke (file1) only", policy: policyWithout, smoke: smokeWith, wantErr: false},
		{name: "present in policy (file2) only", policy: policyWith, smoke: smokeWithout, wantErr: false},
		{name: "absent from both", policy: policyWithout, smoke: smokeWithout, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := writeCopilotUnsafeFlagsRepo(t, tc.policy, tc.smoke, copilotUnsafeFlagsHappyProviderWrapper, copilotUnsafeFlagsHappyDevelopmentWrapper, copilotUnsafeFlagsHappyRustLib, copilotUnsafeFlagsHappyLauncherCommon)
			err := CheckCopilotUnsafeFlags(root)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("CheckCopilotUnsafeFlags() = nil, want OR-semantics violation")
				}
				want := "Expected Copilot bare unsafe short flags to be rejected and smoke-tested: " + snippet
				if err.Error() != want {
					t.Fatalf("CheckCopilotUnsafeFlags() error = %q, want %q", err.Error(), want)
				}
				return
			}
			if err != nil {
				t.Fatalf("CheckCopilotUnsafeFlags() = %v, want nil (needle present in one file)", err)
			}
		})
	}
}

// TestCheckCopilotUnsafeFlagsCount asserts the check list contains exactly
// thirty-one invariants, guarding against an accidentally truncated or
// duplicated migration of the shell block.
func TestCheckCopilotUnsafeFlagsCount(t *testing.T) {
	got := len(copilotUnsafeFlagsChecks())
	const want = 31
	if got != want {
		t.Fatalf("copilotUnsafeFlagsChecks() has %d checks, want %d", got, want)
	}
}

// TestCheckCopilotUnsafeFlagsRealRepo asserts that the real provider-policy.sh,
// container-smoke.sh, provider-wrapper.sh, development-wrapper.sh, lib.rs, and
// launcher_common.rs in this repository satisfy all thirty-one
// Copilot-unsafe-flag invariants.  This is the key guard against a
// mis-transcribed needle or a wrong target file: if any Go pattern is not a
// byte-exact substring of the actual file (or the wrong file), this test fails
// with the guard's stderr message.
func TestCheckCopilotUnsafeFlagsRealRepo(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	if _, err := os.Stat(filepath.Join(repoRoot, providerPolicyRelPath)); err != nil {
		t.Skipf("real provider-policy.sh not found at %s: %v", repoRoot, err)
	}
	if err := CheckCopilotUnsafeFlags(repoRoot); err != nil {
		t.Fatalf("CheckCopilotUnsafeFlags(real repo) = %v, want nil", err)
	}
}

// copilotReleaseVerifyHappyVerifier is a minimal but structurally faithful
// scripts/verify-upstream-copilot-release.sh: it contains all six help-mode
// needles (including the escaped whole-flag `grep -Eq` matcher, matched here as
// a fixed string) and all eleven managed flags.
const copilotReleaseVerifyHappyVerifier = `#!/usr/bin/env bash
COPILOT_HELP_MODE="${WORKCELL_COPILOT_RELEASE_HELP_MODE:-auto}"
COPILOT_NATIVE_HELP_DONE=0
COPILOT_DOCKER_HELP_DONE=0
copilot_help_mode() {
  case "${COPILOT_HELP_MODE}" in
  auto | native | docker | checksum) ;;
  esac
  [[ "${COPILOT_HELP_MODE}" == "checksum" ]] && return 0
}
copilot_flag_present() {
  grep -Eq -- "(^|[^[:alnum:]_-])${flag}([^[:alnum:]_-]|$)" <<<"${help_output}"
}
MANAGED_FLAGS=(
  --allow-tool
  --available-tools
  --disable-builtin-mcps
  --disallow-temp-dir
  --log-dir
  --no-ask-user
  --no-auto-update
  --no-custom-instructions
  --no-remote
  --no-remote-export
  --secret-env-vars
)
`

// copilotReleaseVerifyHappyUpdatePins is a minimal but structurally faithful
// scripts/update-provider-pins.sh containing the checksum-only verify needle.
const copilotReleaseVerifyHappyUpdatePins = `#!/usr/bin/env bash
WORKCELL_COPILOT_RELEASE_HELP_MODE=checksum "${ROOT_DIR}/scripts/verify-upstream-copilot-release.sh"
`

// copilotReleaseVerifyHappyJobValidate is a minimal but structurally faithful
// scripts/ci/job-validate.sh containing the checksum-only verify needle.
const copilotReleaseVerifyHappyJobValidate = `#!/usr/bin/env bash
WORKCELL_COPILOT_RELEASE_HELP_MODE=checksum "${ROOT_DIR}/scripts/verify-upstream-copilot-release.sh"
`

// copilotReleaseVerifyHappyReleaseYML is a minimal but structurally faithful
// .github/workflows/release.yml containing the docker/smoke and arm64 release-
// help needles.
const copilotReleaseVerifyHappyReleaseYML = `name: release
jobs:
  container-smoke:
    env:
      WORKCELL_COPILOT_RELEASE_HELP_MODE: docker
      WORKCELL_COPILOT_RELEASE_HELP_IMAGE: workcell:smoke
  preflight-arm64-copilot-runtime:
    env:
      WORKCELL_COPILOT_RELEASE_HELP_IMAGE: workcell:copilot-arm64-smoke
`

// writeCopilotReleaseVerifyRepo materializes a fake repo with the four files
// this group reads set to the given bodies; a body of "" means "do not create
// that file" (unreadable-target case).
func writeCopilotReleaseVerifyRepo(t *testing.T, verifier, updatePins, jobValidate, releaseYML string) string {
	t.Helper()
	root := t.TempDir()
	write := func(rel, body string) {
		if body == "" {
			return
		}
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	write(verifyUpstreamCopilotReleaseRelPath, verifier)
	write(updateProviderPinsRelPath, updatePins)
	write(jobValidateRelPath, jobValidate)
	write(releaseWorkflowRelPath, releaseYML)
	return root
}

func TestCheckCopilotReleaseVerify(t *testing.T) {
	tests := []struct {
		name        string
		verifier    string
		updatePins  string
		jobValidate string
		releaseYML  string
		wantErr     string // "" means expect success
	}{
		{
			name:        "happy path all invariants hold",
			verifier:    copilotReleaseVerifyHappyVerifier,
			updatePins:  copilotReleaseVerifyHappyUpdatePins,
			jobValidate: copilotReleaseVerifyHappyJobValidate,
			releaseYML:  copilotReleaseVerifyHappyReleaseYML,
		},
		{
			// Help-mode guard (first probe): shares the group message.
			name:        "verifier missing help-mode default",
			verifier:    strings.Replace(copilotReleaseVerifyHappyVerifier, `COPILOT_HELP_MODE="${WORKCELL_COPILOT_RELEASE_HELP_MODE:-auto}"`, "COPILOT_HELP_MODE=auto", 1),
			updatePins:  copilotReleaseVerifyHappyUpdatePins,
			jobValidate: copilotReleaseVerifyHappyJobValidate,
			releaseYML:  copilotReleaseVerifyHappyReleaseYML,
			wantErr:     "Expected Copilot upstream release verifier to track native/Docker help probes separately, support checksum-only paths, and match whole safety flags",
		},
		{
			// Help-mode guard (sixth probe): the escaped whole-flag `grep -Eq`
			// matcher, matched as a fixed string.
			name:        "verifier missing whole-flag grep -Eq matcher",
			verifier:    strings.Replace(copilotReleaseVerifyHappyVerifier, `grep -Eq -- "(^|[^[:alnum:]_-])${flag}([^[:alnum:]_-]|$)"`, "grep -Fq -- \"${flag}\"", 1),
			updatePins:  copilotReleaseVerifyHappyUpdatePins,
			jobValidate: copilotReleaseVerifyHappyJobValidate,
			releaseYML:  copilotReleaseVerifyHappyReleaseYML,
			wantErr:     "Expected Copilot upstream release verifier to track native/Docker help probes separately, support checksum-only paths, and match whole safety flags",
		},
		{
			// Managed-flag loop item (kindPresent, interpolated message).
			name:        "verifier missing managed flag",
			verifier:    strings.Replace(copilotReleaseVerifyHappyVerifier, "--allow-tool", "--other-flag", 1),
			updatePins:  copilotReleaseVerifyHappyUpdatePins,
			jobValidate: copilotReleaseVerifyHappyJobValidate,
			releaseYML:  copilotReleaseVerifyHappyReleaseYML,
			wantErr:     "Expected Copilot upstream release verifier to require managed flag: --allow-tool",
		},
		{
			// Checksum-only guard (first probe, update-provider-pins.sh).
			name:        "update-provider-pins missing checksum needle",
			verifier:    copilotReleaseVerifyHappyVerifier,
			updatePins:  strings.Replace(copilotReleaseVerifyHappyUpdatePins, "WORKCELL_COPILOT_RELEASE_HELP_MODE=checksum", "WORKCELL_COPILOT_RELEASE_HELP_MODE=docker", 1),
			jobValidate: copilotReleaseVerifyHappyJobValidate,
			releaseYML:  copilotReleaseVerifyHappyReleaseYML,
			wantErr:     "Expected provider bump and routine validate paths to use checksum-only Copilot release verification before smoke images exist",
		},
		{
			// Checksum-only guard (second probe, job-validate.sh): proves the two
			// ordered probes share one message.
			name:        "job-validate missing checksum needle",
			verifier:    copilotReleaseVerifyHappyVerifier,
			updatePins:  copilotReleaseVerifyHappyUpdatePins,
			jobValidate: strings.Replace(copilotReleaseVerifyHappyJobValidate, "WORKCELL_COPILOT_RELEASE_HELP_MODE=checksum", "WORKCELL_COPILOT_RELEASE_HELP_MODE=docker", 1),
			releaseYML:  copilotReleaseVerifyHappyReleaseYML,
			wantErr:     "Expected provider bump and routine validate paths to use checksum-only Copilot release verification before smoke images exist",
		},
		{
			// Container-smoke guard (first probe, release.yml).
			name:        "release yml missing docker help mode",
			verifier:    copilotReleaseVerifyHappyVerifier,
			updatePins:  copilotReleaseVerifyHappyUpdatePins,
			jobValidate: copilotReleaseVerifyHappyJobValidate,
			releaseYML:  strings.Replace(copilotReleaseVerifyHappyReleaseYML, "WORKCELL_COPILOT_RELEASE_HELP_MODE: docker", "WORKCELL_COPILOT_RELEASE_HELP_MODE: native", 1),
			wantErr:     "Expected release container-smoke job to force Copilot release help verification inside the runtime image",
		},
		{
			// Container-smoke guard (second probe, release.yml): shares the message.
			name:        "release yml missing smoke help image",
			verifier:    copilotReleaseVerifyHappyVerifier,
			updatePins:  copilotReleaseVerifyHappyUpdatePins,
			jobValidate: copilotReleaseVerifyHappyJobValidate,
			releaseYML:  strings.Replace(copilotReleaseVerifyHappyReleaseYML, "WORKCELL_COPILOT_RELEASE_HELP_IMAGE: workcell:smoke", "WORKCELL_COPILOT_RELEASE_HELP_IMAGE: workcell:other", 1),
			wantErr:     "Expected release container-smoke job to force Copilot release help verification inside the runtime image",
		},
		{
			// Arm64 guard (second probe, release.yml): the arm64 smoke image.
			name:        "release yml missing arm64 help image",
			verifier:    copilotReleaseVerifyHappyVerifier,
			updatePins:  copilotReleaseVerifyHappyUpdatePins,
			jobValidate: copilotReleaseVerifyHappyJobValidate,
			releaseYML:  strings.Replace(copilotReleaseVerifyHappyReleaseYML, "WORKCELL_COPILOT_RELEASE_HELP_IMAGE: workcell:copilot-arm64-smoke", "WORKCELL_COPILOT_RELEASE_HELP_IMAGE: workcell:other-arm64", 1),
			wantErr:     "Expected release workflow to verify Copilot release help inside an arm64 runtime image before publication",
		},
		{
			// A missing verifier file is empty content: the first help-mode probe
			// fails.
			name:        "missing verifier file",
			verifier:    "",
			updatePins:  copilotReleaseVerifyHappyUpdatePins,
			jobValidate: copilotReleaseVerifyHappyJobValidate,
			releaseYML:  copilotReleaseVerifyHappyReleaseYML,
			wantErr:     "Expected Copilot upstream release verifier to track native/Docker help probes separately, support checksum-only paths, and match whole safety flags",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := writeCopilotReleaseVerifyRepo(t, tc.verifier, tc.updatePins, tc.jobValidate, tc.releaseYML)
			err := CheckCopilotReleaseVerify(root)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("CheckCopilotReleaseVerify() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("CheckCopilotReleaseVerify() = nil, want error %q", tc.wantErr)
			}
			if err.Error() != tc.wantErr {
				t.Fatalf("CheckCopilotReleaseVerify() error = %q, want %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// TestCheckCopilotReleaseVerifyCount asserts the check list contains exactly
// twenty-four invariants, guarding against an accidentally truncated or
// duplicated migration of the shell block.
func TestCheckCopilotReleaseVerifyCount(t *testing.T) {
	got := len(copilotReleaseVerifyChecks())
	const want = 24
	if got != want {
		t.Fatalf("copilotReleaseVerifyChecks() has %d checks, want %d", got, want)
	}
}

// TestCheckCopilotReleaseVerifyRealRepo asserts that the real
// verify-upstream-copilot-release.sh, update-provider-pins.sh, job-validate.sh,
// and release.yml in this repository satisfy all twenty-four Copilot
// upstream-release verifier invariants.  This is the key guard against a
// mis-transcribed needle or a wrong target file: if any Go pattern is not a
// byte-exact substring of the actual file (or the wrong file), this test fails
// with the guard's stderr message.
func TestCheckCopilotReleaseVerifyRealRepo(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	if _, err := os.Stat(filepath.Join(repoRoot, verifyUpstreamCopilotReleaseRelPath)); err != nil {
		t.Skipf("real verify-upstream-copilot-release.sh not found at %s: %v", repoRoot, err)
	}
	if err := CheckCopilotReleaseVerify(repoRoot); err != nil {
		t.Fatalf("CheckCopilotReleaseVerify(real repo) = %v, want nil", err)
	}
}

// adapterRuleGuardBashHappyReleaseYML is a minimal .github/workflows/release.yml
// with the native help-mode needle on exactly two lines (the amd64 and arm64
// lanes), satisfying the kindCountAtLeast(2) guard.
const adapterRuleGuardBashHappyReleaseYML = `name: release
jobs:
  preflight-amd64:
    env:
      WORKCELL_COPILOT_RELEASE_HELP_MODE: native
  preflight-arm64:
    env:
      WORKCELL_COPILOT_RELEASE_HELP_MODE: native
`

// adapterRuleGuardBashHappyCodexRule is a minimal Codex rule file (used for both
// managed_config.toml and requirements.toml) containing the four required
// bypass-path needles and NOT the removed npm entrypoint.
const adapterRuleGuardBashHappyCodexRule = `# codex rule
deny = [
  "/usr/local/libexec/workcell/provider-wrapper.sh",
  "/usr/local/libexec/workcell/real/claude",
  "/usr/local/libexec/workcell/core/copilot",
  "/usr/local/libexec/workcell/real/copilot",
]
`

// adapterRuleGuardBashHappyGuard is a minimal adapters/claude/hooks/guard-bash.sh
// containing the regex-escaped provider-wrapper needle (literal backslash-dot),
// the two Copilot provider paths, the escaped home-control-plane needles
// (`\\.copilot`, `copilot\.md`), and NOT the removed npm entrypoint.
const adapterRuleGuardBashHappyGuard = `#!/usr/bin/env bash
blocked_regex="/usr/local/libexec/workcell/provider-wrapper\.sh"
blocked_regex+="|/usr/local/libexec/workcell/real/claude"
blocked_regex+="|/usr/local/libexec/workcell/core/copilot"
blocked_regex+="|/usr/local/libexec/workcell/real/copilot"
home_control_regex="(~|/state/agent-home)/(\\.claude|\\.copilot|\\.gemini)"
doc_regex="copilot\.md"
`

// writeAdapterRuleGuardBashRepo materializes a fake repo with the four files
// this group reads set to the given bodies; a body of "" means "do not create
// that file" (unreadable-target case).
func writeAdapterRuleGuardBashRepo(t *testing.T, releaseYML, managedConfig, requirements, guardBash string) string {
	t.Helper()
	root := t.TempDir()
	write := func(rel, body string) {
		if body == "" {
			return
		}
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	write(releaseWorkflowRelPath, releaseYML)
	write(codexManagedConfigRelPath, managedConfig)
	write(codexRequirementsRelPath, requirements)
	write(claudeGuardBashRelPath, guardBash)
	return root
}

func TestCheckAdapterRuleGuardBash(t *testing.T) {
	tests := []struct {
		name          string
		releaseYML    string
		managedConfig string
		requirements  string
		guardBash     string
		wantErr       string // "" means expect success
	}{
		{
			name:          "happy path all invariants hold",
			releaseYML:    adapterRuleGuardBashHappyReleaseYML,
			managedConfig: adapterRuleGuardBashHappyCodexRule,
			requirements:  adapterRuleGuardBashHappyCodexRule,
			guardBash:     adapterRuleGuardBashHappyGuard,
		},
		{
			// kindCountAtLeast: zero native lines fails the count guard.
			name:          "release yml zero native lines fails count",
			releaseYML:    strings.ReplaceAll(adapterRuleGuardBashHappyReleaseYML, "WORKCELL_COPILOT_RELEASE_HELP_MODE: native", "WORKCELL_COPILOT_RELEASE_HELP_MODE: docker"),
			managedConfig: adapterRuleGuardBashHappyCodexRule,
			requirements:  adapterRuleGuardBashHappyCodexRule,
			guardBash:     adapterRuleGuardBashHappyGuard,
			wantErr:       "Expected release workflow to force native Copilot release help verification for amd64 and arm64 lanes",
		},
		{
			// kindCountAtLeast: one native line fails the count guard (< 2).
			name:          "release yml one native line fails count",
			releaseYML:    strings.Replace(adapterRuleGuardBashHappyReleaseYML, "WORKCELL_COPILOT_RELEASE_HELP_MODE: native", "WORKCELL_COPILOT_RELEASE_HELP_MODE: docker", 1),
			managedConfig: adapterRuleGuardBashHappyCodexRule,
			requirements:  adapterRuleGuardBashHappyCodexRule,
			guardBash:     adapterRuleGuardBashHappyGuard,
			wantErr:       "Expected release workflow to force native Copilot release help verification for amd64 and arm64 lanes",
		},
		{
			// kindCountAtLeast line semantics: two occurrences on ONE line count
			// as one matching line (grep -Fc counts LINES, not occurrences), so
			// this still fails the minCount(2) guard.
			name:          "release yml two occurrences on one line fails count",
			releaseYML:    "name: release\nenv: WORKCELL_COPILOT_RELEASE_HELP_MODE: native WORKCELL_COPILOT_RELEASE_HELP_MODE: native\n",
			managedConfig: adapterRuleGuardBashHappyCodexRule,
			requirements:  adapterRuleGuardBashHappyCodexRule,
			guardBash:     adapterRuleGuardBashHappyGuard,
			wantErr:       "Expected release workflow to force native Copilot release help verification for amd64 and arm64 lanes",
		},
		{
			// kindCountAtLeast: three native lines satisfies the count guard.
			name:          "release yml three native lines passes count",
			releaseYML:    adapterRuleGuardBashHappyReleaseYML + "      WORKCELL_COPILOT_RELEASE_HELP_MODE: native\n",
			managedConfig: adapterRuleGuardBashHappyCodexRule,
			requirements:  adapterRuleGuardBashHappyCodexRule,
			guardBash:     adapterRuleGuardBashHappyGuard,
		},
		{
			// codex_rule_file loop, managed_config.toml, probe 1.
			name:          "managed_config missing provider-wrapper path",
			releaseYML:    adapterRuleGuardBashHappyReleaseYML,
			managedConfig: strings.Replace(adapterRuleGuardBashHappyCodexRule, "/usr/local/libexec/workcell/provider-wrapper.sh", "/other/path", 1),
			requirements:  adapterRuleGuardBashHappyCodexRule,
			guardBash:     adapterRuleGuardBashHappyGuard,
			wantErr:       "Expected managed_config.toml to block direct provider-wrapper launches",
		},
		{
			// codex_rule_file loop, managed_config.toml, probe 3 (second needle):
			// proves the two-needle probe shares one message.
			name:          "managed_config missing real copilot path",
			releaseYML:    adapterRuleGuardBashHappyReleaseYML,
			managedConfig: strings.Replace(adapterRuleGuardBashHappyCodexRule, "/usr/local/libexec/workcell/real/copilot", "/other/copilot", 1),
			requirements:  adapterRuleGuardBashHappyCodexRule,
			guardBash:     adapterRuleGuardBashHappyGuard,
			wantErr:       "Expected managed_config.toml to block Copilot provider mediation bypass paths",
		},
		{
			// codex_rule_file loop, managed_config.toml, probe 4 (negated cli.js).
			name:          "managed_config references removed npm entrypoint",
			releaseYML:    adapterRuleGuardBashHappyReleaseYML,
			managedConfig: adapterRuleGuardBashHappyCodexRule + "allow = [\"@anthropic-ai/claude-code/cli.js\"]\n",
			requirements:  adapterRuleGuardBashHappyCodexRule,
			guardBash:     adapterRuleGuardBashHappyGuard,
			wantErr:       "managed_config.toml should not reference the removed Claude npm entrypoint",
		},
		{
			// codex_rule_file loop, requirements.toml, probe 2: proves the loop
			// runs the same probes against the second file with its basename.
			name:          "requirements missing native claude path",
			releaseYML:    adapterRuleGuardBashHappyReleaseYML,
			managedConfig: adapterRuleGuardBashHappyCodexRule,
			requirements:  strings.Replace(adapterRuleGuardBashHappyCodexRule, "/usr/local/libexec/workcell/real/claude", "/other/claude", 1),
			guardBash:     adapterRuleGuardBashHappyGuard,
			wantErr:       "Expected requirements.toml to block the native Claude binary path",
		},
		{
			// guard-bash.sh: the regex-escaped provider-wrapper needle carries a
			// literal backslash-dot; removing it fails this probe.
			name:          "guard missing escaped provider-wrapper needle",
			releaseYML:    adapterRuleGuardBashHappyReleaseYML,
			managedConfig: adapterRuleGuardBashHappyCodexRule,
			requirements:  adapterRuleGuardBashHappyCodexRule,
			guardBash:     strings.Replace(adapterRuleGuardBashHappyGuard, `/usr/local/libexec/workcell/provider-wrapper\.sh`, "/other/wrapper", 1),
			wantErr:       "Expected Claude Bash guard to block direct provider-wrapper launches",
		},
		{
			// guard-bash.sh multi-path probe: the `\\.copilot` home-control needle
			// (two literal backslashes) shares guardBypassMessage.
			name:          "guard missing double-backslash copilot needle",
			releaseYML:    adapterRuleGuardBashHappyReleaseYML,
			managedConfig: adapterRuleGuardBashHappyCodexRule,
			requirements:  adapterRuleGuardBashHappyCodexRule,
			guardBash:     strings.Replace(adapterRuleGuardBashHappyGuard, `\\.copilot`, `\\.other`, 1),
			wantErr:       "Expected Claude Bash guard to block Copilot provider and home control-plane bypass paths",
		},
		{
			// guard-bash.sh multi-path probe: the `copilot\.md` needle shares the
			// same message.
			name:          "guard missing copilot md needle",
			releaseYML:    adapterRuleGuardBashHappyReleaseYML,
			managedConfig: adapterRuleGuardBashHappyCodexRule,
			requirements:  adapterRuleGuardBashHappyCodexRule,
			guardBash:     strings.Replace(adapterRuleGuardBashHappyGuard, `copilot\.md`, `other\.md`, 1),
			wantErr:       "Expected Claude Bash guard to block Copilot provider and home control-plane bypass paths",
		},
		{
			// guard-bash.sh: negated cli.js check (present is a violation).
			name:          "guard references removed npm entrypoint",
			releaseYML:    adapterRuleGuardBashHappyReleaseYML,
			managedConfig: adapterRuleGuardBashHappyCodexRule,
			requirements:  adapterRuleGuardBashHappyCodexRule,
			guardBash:     adapterRuleGuardBashHappyGuard + "legacy=\"@anthropic-ai/claude-code/cli.js\"\n",
			wantErr:       "Claude Bash guard should not reference the removed Claude npm entrypoint",
		},
		{
			// A missing release.yml is empty content: zero native lines, so the
			// first (count) check fails.
			name:          "missing release yml",
			releaseYML:    "",
			managedConfig: adapterRuleGuardBashHappyCodexRule,
			requirements:  adapterRuleGuardBashHappyCodexRule,
			guardBash:     adapterRuleGuardBashHappyGuard,
			wantErr:       "Expected release workflow to force native Copilot release help verification for amd64 and arm64 lanes",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := writeAdapterRuleGuardBashRepo(t, tc.releaseYML, tc.managedConfig, tc.requirements, tc.guardBash)
			err := CheckAdapterRuleGuardBash(root)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("CheckAdapterRuleGuardBash() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("CheckAdapterRuleGuardBash() = nil, want error %q", tc.wantErr)
			}
			if err.Error() != tc.wantErr {
				t.Fatalf("CheckAdapterRuleGuardBash() error = %q, want %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// TestCheckAdapterRuleGuardBashCount asserts the check list contains exactly
// eighteen invariants, guarding against an accidentally truncated or duplicated
// migration of the shell block.
func TestCheckAdapterRuleGuardBashCount(t *testing.T) {
	got := len(adapterRuleGuardBashChecks())
	const want = 18
	if got != want {
		t.Fatalf("adapterRuleGuardBashChecks() has %d checks, want %d", got, want)
	}
}

// TestCheckAdapterRuleGuardBashRealRepo asserts that the real release.yml, the
// two Codex rule files, and guard-bash.sh in this repository satisfy all
// eighteen adapter-rule / Bash-guard invariants.  This is the key guard against
// a mis-transcribed needle or a wrong target file: if any Go pattern is not a
// byte-exact substring of the actual file (or the wrong file), this test fails
// with the guard's stderr message.
func TestCheckAdapterRuleGuardBashRealRepo(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	if _, err := os.Stat(filepath.Join(repoRoot, claudeGuardBashRelPath)); err != nil {
		t.Skipf("real guard-bash.sh not found at %s: %v", repoRoot, err)
	}
	if err := CheckAdapterRuleGuardBash(repoRoot); err != nil {
		t.Fatalf("CheckAdapterRuleGuardBash(real repo) = %v, want nil", err)
	}
}

// inspectAssuranceHappyWorkcell is a minimal scripts/workcell containing every
// needle the mount-view loop, the --inspect contract-token loop, and the
// audit-log-field loop require, so all three pass with an empty assurance.sh.
const inspectAssuranceHappyWorkcell = `#!/usr/bin/env bash
workspace_runtime_probe_path() { :; }
validate_colima_runtime_workspace_view() { :; }
validate_colima_runtime_workspace_view "${profile}" "${workspace}"
# Refreshing managed Colima profile ${COLIMA_PROFILE} because the running VM is not exposing the expected workspace contents.
# Refreshing managed Colima profile ${COLIMA_PROFILE} because the started VM did not expose the expected workspace view.
workcell --inspect
print_inspect_state
provider_native_sandbox_configured
provider_native_sandbox_effective
provider_native_sandbox_reason
codex claude gemini
network_policy
session_assurance_initial
`

// inspectAssuranceHappyColima is a minimal scripts/colima-egress-allowlist.sh
// containing the three safe-cwd snippets the second loop requires.
const inspectAssuranceHappyColima = `#!/usr/bin/env bash
cd "${home}" &&
cd / &&
LIMA_WORKDIR=/
`

// writeInspectAssuranceRepo materializes a fake repo with the three files this
// group reads set to the given bodies; a body of "" means "do not create that
// file" (unreadable-target case).
func writeInspectAssuranceRepo(t *testing.T, workcell, colima, assurance string) string {
	t.Helper()
	root := t.TempDir()
	write := func(rel, body string) {
		if body == "" {
			return
		}
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	write(launcherRelPath, workcell)
	write(colimaEgressAllowlistRelPath, colima)
	write(assuranceRelPath, assurance)
	return root
}

func TestCheckInspectAssuranceLoops(t *testing.T) {
	tests := []struct {
		name      string
		workcell  string
		colima    string
		assurance string
		wantErr   string // "" means expect success
	}{
		{
			name:      "happy path all invariants hold",
			workcell:  inspectAssuranceHappyWorkcell,
			colima:    inspectAssuranceHappyColima,
			assurance: "#!/usr/bin/env bash\n",
		},
		{
			// Loop 1 (mount-view): a removed snippet fails with the interpolated
			// needle message.
			name:      "workcell missing mount-view snippet",
			workcell:  strings.Replace(inspectAssuranceHappyWorkcell, "workspace_runtime_probe_path()", "", 1),
			colima:    inspectAssuranceHappyColima,
			assurance: "#!/usr/bin/env bash\n",
			wantErr:   "Expected workcell mount-view validation snippet missing: workspace_runtime_probe_path()",
		},
		{
			// Loop 2 (egress safe-cwd): a removed snippet in the colima helper
			// fails; the item carries `/` matched literally by grep -F.
			name:      "colima helper missing safe-cwd snippet",
			workcell:  inspectAssuranceHappyWorkcell,
			colima:    strings.Replace(inspectAssuranceHappyColima, "LIMA_WORKDIR=/", "", 1),
			assurance: "#!/usr/bin/env bash\n",
			wantErr:   "Expected egress helper safe-cwd snippet missing: LIMA_WORKDIR=/",
		},
		{
			// Loop 3 (--inspect contract tokens): a removed token in workcell fails.
			name:      "workcell missing --inspect contract token",
			workcell:  strings.Replace(inspectAssuranceHappyWorkcell, "print_inspect_state", "", 1),
			colima:    inspectAssuranceHappyColima,
			assurance: "#!/usr/bin/env bash\n",
			wantErr:   "Expected workcell to contain --inspect contract token: print_inspect_state",
		},
		{
			// Loop 4 (audit-log field) present-in-any: present in workcell only →
			// pass (assurance.sh does not mention it).
			name:      "audit field present in workcell only passes",
			workcell:  inspectAssuranceHappyWorkcell,
			colima:    inspectAssuranceHappyColima,
			assurance: "#!/usr/bin/env bash\n",
		},
		{
			// Loop 4 present-in-any: present in assurance.sh only → pass (the
			// field is removed from workcell but the OR is satisfied by assurance).
			name:      "audit field present in assurance only passes",
			workcell:  strings.Replace(inspectAssuranceHappyWorkcell, "network_policy", "", 1),
			colima:    inspectAssuranceHappyColima,
			assurance: "#!/usr/bin/env bash\nnetwork_policy\n",
		},
		{
			// Loop 4 present-in-any: absent from BOTH files → fail with the
			// interpolated field message.
			name:      "audit field absent from both files fails",
			workcell:  strings.Replace(inspectAssuranceHappyWorkcell, "network_policy", "", 1),
			colima:    inspectAssuranceHappyColima,
			assurance: "#!/usr/bin/env bash\n",
			wantErr:   "Expected audit log field referenced in control scripts: network_policy",
		},
		{
			// A missing scripts/workcell is empty content: the first mount-view
			// kindPresent check fails.
			name:      "missing workcell file",
			workcell:  "",
			colima:    inspectAssuranceHappyColima,
			assurance: "#!/usr/bin/env bash\n",
			wantErr:   "Expected workcell mount-view validation snippet missing: workspace_runtime_probe_path()",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := writeInspectAssuranceRepo(t, tc.workcell, tc.colima, tc.assurance)
			err := CheckInspectAssuranceLoops(root)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("CheckInspectAssuranceLoops() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("CheckInspectAssuranceLoops() = nil, want error %q", tc.wantErr)
			}
			if err.Error() != tc.wantErr {
				t.Fatalf("CheckInspectAssuranceLoops() error = %q, want %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// TestCheckInspectAssuranceLoopsCount asserts the check list contains exactly
// twenty-five invariants, guarding against an accidentally truncated or
// duplicated migration of the four shell loops.
func TestCheckInspectAssuranceLoopsCount(t *testing.T) {
	got := len(inspectAssuranceLoopsChecks())
	const want = 25
	if got != want {
		t.Fatalf("inspectAssuranceLoopsChecks() has %d checks, want %d", got, want)
	}
}

// TestCheckInspectAssuranceLoopsRealRepo asserts that the real scripts/workcell,
// scripts/colima-egress-allowlist.sh, and runtime/container/assurance.sh in this
// repository satisfy all twenty-five --inspect / session-assurance invariants.
// This is the key guard against a mis-transcribed needle or a wrong target file:
// if any Go pattern is not a byte-exact substring of the actual file(s), this
// test fails with the guard's stderr message.
func TestCheckInspectAssuranceLoopsRealRepo(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	if _, err := os.Stat(filepath.Join(repoRoot, launcherRelPath)); err != nil {
		t.Skipf("real scripts/workcell not found at %s: %v", repoRoot, err)
	}
	if err := CheckInspectAssuranceLoops(repoRoot); err != nil {
		t.Fatalf("CheckInspectAssuranceLoops(real repo) = %v, want nil", err)
	}
}

// writeValidatorWritableStateRepo materializes a fake repo populated with the
// six target files the validator-writable-state block reads.  A body of ""
// means "do not create the file" (unreadable-file case).
func writeValidatorWritableStateRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, body := range files {
		if body == "" {
			continue
		}
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	return root
}

// validatorWritableStateHappyFiles returns the six-file fixture map that
// satisfies all twenty-three invariants: each file that carries affirmative
// grep -Fq needles contains every one of them, and none of the four files
// contains its forbidden ${ROOT_DIR}/tmp/workcell-* temp-root snippet.  The
// needle bodies are built from the exported needle slices so the fixture stays
// in lockstep with the migration.
func validatorWritableStateHappyFiles() map[string]string {
	return map[string]string{
		buildAndTestRelPath:               "#!/usr/bin/env bash\n" + strings.Join(buildAndTestValidatorIsolationNeedles, "\n") + "\n",
		trustedDockerClientRelPath:        "#!/usr/bin/env bash\n" + `fallback_home="${fallback_parent%/}/workcell-home-${uid}"` + "\n",
		verifyReleaseBundleRelPath:        "#!/usr/bin/env bash\n" + strings.Join(releaseBundleValidatorIsolationNeedles, "\n") + "\n",
		verifyBuildInputManifestRelPath:   "#!/usr/bin/env bash\n",
		verifyControlPlaneManifestRelPath: "#!/usr/bin/env bash\n",
		verifyReproducibleBuildRelPath:    "#!/usr/bin/env bash\n",
	}
}

func TestCheckValidatorWritableState(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(files map[string]string)
		wantErr string // "" means expect success
	}{
		{
			name:   "happy path all invariants hold",
			mutate: func(map[string]string) {},
		},
		{
			// build-and-test loop: a removed needle fails with the interpolated
			// per-needle message (first needle).
			name: "build-and-test missing UID needle",
			mutate: func(files map[string]string) {
				files[buildAndTestRelPath] = strings.Replace(files[buildAndTestRelPath], "WORKCELL_BUILD_AND_TEST_VALIDATOR_UID=\n", "", 1)
			},
			wantErr: "Expected scripts/build-and-test.sh --docker to launch validator work under an explicit caller UID/GID with isolated writable state (WORKCELL_BUILD_AND_TEST_VALIDATOR_UID=)",
		},
		{
			// build-and-test loop: the last needle (the mkdir line) removed fails
			// with its interpolated message.
			name: "build-and-test missing mkdir needle",
			mutate: func(files map[string]string) {
				files[buildAndTestRelPath] = strings.Replace(files[buildAndTestRelPath], `mkdir -p "${HOME}" "${XDG_CACHE_HOME}" "${GOCACHE}" "${GOMODCACHE}" "${CARGO_TARGET_DIR}" "${TMPDIR}"`, "", 1)
			},
			wantErr: `Expected scripts/build-and-test.sh --docker to launch validator work under an explicit caller UID/GID with isolated writable state (mkdir -p "${HOME}" "${XDG_CACHE_HOME}" "${GOCACHE}" "${GOMODCACHE}" "${CARGO_TARGET_DIR}" "${TMPDIR}")`,
		},
		{
			// A missing scripts/build-and-test.sh is empty content: the first
			// affirmative needle fails.
			name: "build-and-test file missing",
			mutate: func(files map[string]string) {
				files[buildAndTestRelPath] = ""
			},
			wantErr: "Expected scripts/build-and-test.sh --docker to launch validator work under an explicit caller UID/GID with isolated writable state (WORKCELL_BUILD_AND_TEST_VALIDATOR_UID=)",
		},
		{
			// trusted-docker-client isolated-home probe: removed needle fails with
			// the fixed message.
			name: "trusted-docker-client missing isolated home",
			mutate: func(files map[string]string) {
				files[trustedDockerClientRelPath] = "#!/usr/bin/env bash\n"
			},
			wantErr: "Expected trusted-docker-client.sh to synthesize an isolated home for passwd-less caller UIDs",
		},
		{
			// verify-release-bundle loop: a removed needle fails with the
			// interpolated per-needle message.
			name: "verify-release-bundle missing validator user needle",
			mutate: func(files map[string]string) {
				files[verifyReleaseBundleRelPath] = strings.Replace(files[verifyReleaseBundleRelPath], `--user "${validator_uid}:${validator_gid}"`, "", 1)
			},
			wantErr: `Expected scripts/verify-release-bundle.sh to build bundles in the validator under an explicit caller UID/GID with isolated writable state (--user "${validator_uid}:${validator_gid}")`,
		},
		{
			// build-input-manifest mounted-repo-write guard: the forbidden
			// temp-root snippet present is a violation.
			name: "verify-build-input-manifest writes under mounted repo",
			mutate: func(files map[string]string) {
				files[verifyBuildInputManifestRelPath] += "tmp=\"${ROOT_DIR}/tmp/workcell-build-input-nested\"\n"
			},
			wantErr: "Expected verify-build-input-manifest.sh nested-source checks to avoid writing under the mounted repo",
		},
		{
			// control-plane-manifest mounted-repo-write guard.
			name: "verify-control-plane-manifest writes under mounted repo",
			mutate: func(files map[string]string) {
				files[verifyControlPlaneManifestRelPath] += "tmp=\"${ROOT_DIR}/tmp/workcell-control-plane-nested\"\n"
			},
			wantErr: "Expected verify-control-plane-manifest.sh nested-source checks to avoid writing under the mounted repo",
		},
		{
			// release-bundle mounted-repo-write guard: the same file also carries
			// the eight affirmative needles, which remain satisfied, so the
			// negated temp-root guard is what fires.
			name: "verify-release-bundle writes under mounted repo",
			mutate: func(files map[string]string) {
				files[verifyReleaseBundleRelPath] += "tmp=\"${ROOT_DIR}/tmp/workcell-release-bundle\"\n"
			},
			wantErr: "Expected verify-release-bundle.sh temp roots to avoid writing under the mounted repo",
		},
		{
			// reproducible-build mounted-repo-write guard.
			name: "verify-reproducible-build writes under mounted repo",
			mutate: func(files map[string]string) {
				files[verifyReproducibleBuildRelPath] += "tmp=\"${ROOT_DIR}/tmp/workcell-repro\"\n"
			},
			wantErr: "Expected verify-reproducible-build.sh OCI exports to avoid writing under the mounted repo",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			files := validatorWritableStateHappyFiles()
			tc.mutate(files)
			root := writeValidatorWritableStateRepo(t, files)
			err := CheckValidatorWritableState(root)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("CheckValidatorWritableState() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("CheckValidatorWritableState() = nil, want error %q", tc.wantErr)
			}
			if err.Error() != tc.wantErr {
				t.Fatalf("CheckValidatorWritableState() error = %q, want %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// TestCheckValidatorWritableStateCount asserts the generated check list contains
// exactly twenty-three invariants, guarding against an accidentally truncated or
// duplicated migration of the two loops, the isolated-home probe, and the four
// mounted-repo-write guards.
func TestCheckValidatorWritableStateCount(t *testing.T) {
	got := len(validatorWritableStateChecks())
	const want = 23
	if got != want {
		t.Fatalf("validatorWritableStateChecks() has %d checks, want %d", got, want)
	}
}

// TestCheckValidatorWritableStateRealRepo asserts that the real
// scripts/build-and-test.sh, scripts/lib/trusted-docker-client.sh,
// scripts/verify-release-bundle.sh, scripts/verify-build-input-manifest.sh,
// scripts/verify-control-plane-manifest.sh, and scripts/verify-reproducible-build.sh
// in this repository satisfy all twenty-three validator writable-state
// invariants.  This is the key guard against a mis-transcribed needle or a wrong
// target file: if any Go pattern diverges from the actual file contents, this
// test fails with the guard's stderr message.
func TestCheckValidatorWritableStateRealRepo(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	if _, err := os.Stat(filepath.Join(repoRoot, buildAndTestRelPath)); err != nil {
		t.Skipf("real scripts/build-and-test.sh not found at %s: %v", repoRoot, err)
	}
	if err := CheckValidatorWritableState(repoRoot); err != nil {
		t.Fatalf("CheckValidatorWritableState(real repo) = %v, want nil", err)
	}
}

// hostutilEgressRgHappyGoHostutil is a minimal scripts/lib/launcher/go-hostutil.sh
// satisfying all five bootstrap-Go invariants: the escaped-literal patterns match
// the literal ${ROOT_DIR}/${GOPATH}/${HOST_GO_BIN}/"$@" tokens.
const hostutilEgressRgHappyGoHostutil = `#!/bin/bash
set -euo pipefail
run_clean_host_command_in_dir "${ROOT_DIR}" env \
  GOPATH="${GOPATH}" \
  GOMODCACHE="${GOMODCACHE}" \
  GOCACHE="${GOCACHE}" \
  "${HOST_GO_BIN}" run ./cmd/workcell-hostutil "$@"
`

// hostutilEgressRgHappyEntrypoint is a minimal runtime/container/entrypoint.sh
// satisfying the four entrypoint invariants: it does NOT inject a default Codex
// --cd override or default AGENT_NAME to codex, and it traps INT/TERM.
const hostutilEgressRgHappyEntrypoint = `#!/bin/bash
set -euo pipefail
trap 'workcell_run_command_with_file_trace_signal INT' INT
trap 'workcell_run_command_with_file_trace_signal TERM' TERM
`

// hostutilEgressRgHappyColima is a minimal scripts/colima-egress-allowlist.sh
// satisfying all twelve colima-egress invariants: the first line is the cleared
// env -S -i shebang, it does not trust PATH (no command -v/type -P/which ), and
// it scrubs the environment before invoking the Go runtime helper.
const hostutilEgressRgHappyColima = `#!/usr/bin/env -S -i PATH=/usr/bin BASH_ENV= ENV= /bin/bash
set -euo pipefail
REAL_HOME=/Users/real
scrub_host_process_env
unset PERL5OPT PERL5LIB PERLLIB PERL_MB_OPT PERL_MM_OPT
unset DYLD_*
is_trusted_host_tool_path
run_clean_repo_command env \
  GOPATH="${GOPATH}" \
  GOMODCACHE="${GOMODCACHE}" \
  GOCACHE="${GOCACHE}" \
  "${GO_BIN}" run ./cmd/workcell-runtimeutil "$@"
`

// writeHostutilEgressRgRepo materializes a fake repo with the three target files
// set to their respective bodies; a body of "" means "do not create that file"
// (unreadable-target case).
func writeHostutilEgressRgRepo(t *testing.T, goHostutil, entrypoint, colima string) string {
	t.Helper()
	root := t.TempDir()
	write := func(rel, body string) {
		if body == "" {
			return
		}
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	write(goHostutilRelPath, goHostutil)
	write(entrypointRelPath, entrypoint)
	write(colimaEgressAllowlistRelPath, colima)
	return root
}

func TestCheckHostutilEgressRg(t *testing.T) {
	tests := []struct {
		name       string
		goHostutil string
		entrypoint string
		colima     string
		wantErr    string // "" means expect success
	}{
		{
			name:       "happy path all invariants hold",
			goHostutil: hostutilEgressRgHappyGoHostutil,
			entrypoint: hostutilEgressRgHappyEntrypoint,
			colima:     hostutilEgressRgHappyColima,
		},
		{
			// kindRegexPresent, escaped-literal ${ROOT_DIR} pattern: the bootstrap
			// invocation removed.
			name:       "go-hostutil missing scrubbed bootstrap invocation",
			goHostutil: strings.Replace(hostutilEgressRgHappyGoHostutil, `run_clean_host_command_in_dir "${ROOT_DIR}" env`, `run_clean_host_command_in_dir env`, 1),
			entrypoint: hostutilEgressRgHappyEntrypoint,
			colima:     hostutilEgressRgHappyColima,
			wantErr:    "Expected scripts/lib/launcher/go-hostutil.sh to invoke the bootstrap Go helper from the repo root under a scrubbed environment with explicit Go caches",
		},
		{
			// kindRegexPresent, escaped-literal "$@" pattern (fifth probe of the
			// shared-message guard): the HOST_GO_BIN run line removed.
			name:       "go-hostutil missing HOST_GO_BIN run",
			goHostutil: strings.Replace(hostutilEgressRgHappyGoHostutil, `"${HOST_GO_BIN}" run ./cmd/workcell-hostutil "$@"`, `go run ./cmd/workcell-hostutil "$@"`, 1),
			entrypoint: hostutilEgressRgHappyEntrypoint,
			colima:     hostutilEgressRgHappyColima,
			wantErr:    "Expected scripts/lib/launcher/go-hostutil.sh to invoke the bootstrap Go helper from the repo root under a scrubbed environment with explicit Go caches",
		},
		{
			// kindRegexAbsent: a default Codex --cd override present is a violation.
			name:       "entrypoint injects default codex --cd override",
			goHostutil: hostutilEgressRgHappyGoHostutil,
			entrypoint: hostutilEgressRgHappyEntrypoint + "set -- codex --cd /work\n",
			colima:     hostutilEgressRgHappyColima,
			wantErr:    "runtime/container/entrypoint.sh still injects a blocked default Codex --cd override",
		},
		{
			// kindRegexAbsent, escaped-literal pattern: AGENT_NAME defaulting to
			// codex present is a violation.
			name:       "entrypoint defaults AGENT_NAME to codex",
			goHostutil: hostutilEgressRgHappyGoHostutil,
			entrypoint: hostutilEgressRgHappyEntrypoint + `AGENT_NAME="${AGENT_NAME:-codex}"` + "\n",
			colima:     hostutilEgressRgHappyColima,
			wantErr:    "runtime/container/entrypoint.sh still defaults AGENT_NAME to codex",
		},
		{
			// kindRegexPresent (first probe of the trap guard): INT trap removed.
			name:       "entrypoint missing INT trap",
			goHostutil: hostutilEgressRgHappyGoHostutil,
			entrypoint: strings.Replace(hostutilEgressRgHappyEntrypoint, `trap 'workcell_run_command_with_file_trace_signal INT' INT`, `trap - INT`, 1),
			colima:     hostutilEgressRgHappyColima,
			wantErr:    "Expected runtime/container/entrypoint.sh to trap INT/TERM and finalize file-trace shutdown before exit",
		},
		{
			// kindRegexAbsent, GENUINE `|` alternation: any of command -v / type -P
			// / which present means PATH is trusted (a violation).
			name:       "colima trusts PATH via command -v",
			goHostutil: hostutilEgressRgHappyGoHostutil,
			entrypoint: hostutilEgressRgHappyEntrypoint,
			colima:     hostutilEgressRgHappyColima + "tool=$(command -v git)\n",
			wantErr:    "scripts/colima-egress-allowlist.sh still trusts PATH for executed host tools",
		},
		{
			// kindRegexAbsent, third alternative of the `|` guard: `which ` present
			// is likewise a violation, proving the alternation is a real regex.
			name:       "colima trusts PATH via which",
			goHostutil: hostutilEgressRgHappyGoHostutil,
			entrypoint: hostutilEgressRgHappyEntrypoint,
			colima:     hostutilEgressRgHappyColima + "tool=$(which git)\n",
			wantErr:    "scripts/colima-egress-allowlist.sh still trusts PATH for executed host tools",
		},
		{
			// kindFirstLineRegex: a non-cleared shebang fails the anchored
			// first-line probe.
			name:       "colima non-cleared shebang",
			goHostutil: hostutilEgressRgHappyGoHostutil,
			entrypoint: hostutilEgressRgHappyEntrypoint,
			colima:     strings.Replace(hostutilEgressRgHappyColima, "#!/usr/bin/env -S -i PATH=/usr/bin BASH_ENV= ENV= /bin/bash", "#!/bin/bash", 1),
			wantErr:    "Expected scripts/colima-egress-allowlist.sh to use env -S -i with an absolute /bin/bash and cleared host environment",
		},
		{
			// kindRegexPresent, escaped-literal DYLD_\* pattern: the DYLD_* scrub
			// removed.
			name:       "colima missing DYLD scrub",
			goHostutil: hostutilEgressRgHappyGoHostutil,
			entrypoint: hostutilEgressRgHappyEntrypoint,
			colima:     strings.Replace(hostutilEgressRgHappyColima, "unset DYLD_*", "unset OTHER", 1),
			wantErr:    "Expected scripts/colima-egress-allowlist.sh to scrub DYLD_* variables before host tool lookup",
		},
		{
			// kindRegexPresent (fifth probe of the Go-runtime guard): the runtimeutil
			// run line removed.
			name:       "colima missing Go runtime invocation",
			goHostutil: hostutilEgressRgHappyGoHostutil,
			entrypoint: hostutilEgressRgHappyEntrypoint,
			colima:     strings.Replace(hostutilEgressRgHappyColima, `"${GO_BIN}" run ./cmd/workcell-runtimeutil "$@"`, `go run ./cmd/workcell-runtimeutil "$@"`, 1),
			wantErr:    "Expected scripts/colima-egress-allowlist.sh to invoke Go runtime helpers under a scrubbed environment with explicit Go caches",
		},
		{
			// A missing go-hostutil.sh is empty content: the first affirmative probe
			// fails, mirroring `rg -q` returning non-zero on a missing file.
			name:       "missing go-hostutil",
			goHostutil: "",
			entrypoint: hostutilEgressRgHappyEntrypoint,
			colima:     hostutilEgressRgHappyColima,
			wantErr:    "Expected scripts/lib/launcher/go-hostutil.sh to invoke the bootstrap Go helper from the repo root under a scrubbed environment with explicit Go caches",
		},
		{
			// A missing entrypoint.sh is empty content: the two kindRegexAbsent
			// probes pass on empty content, but the affirmative trap probe fails,
			// mirroring `rg -q` returning non-zero on a missing file.
			name:       "missing entrypoint",
			goHostutil: hostutilEgressRgHappyGoHostutil,
			entrypoint: "",
			colima:     hostutilEgressRgHappyColima,
			wantErr:    "Expected runtime/container/entrypoint.sh to trap INT/TERM and finalize file-trace shutdown before exit",
		},
		{
			// A missing colima helper is empty content: the launcher/entrypoint
			// probes pass, the kindRegexAbsent PATH-trust probe passes, but the
			// affirmative REAL_HOME probe fails.
			name:       "missing colima helper",
			goHostutil: hostutilEgressRgHappyGoHostutil,
			entrypoint: hostutilEgressRgHappyEntrypoint,
			colima:     "",
			wantErr:    "Expected scripts/colima-egress-allowlist.sh to derive the real host home independently of caller HOME",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := writeHostutilEgressRgRepo(t, tc.goHostutil, tc.entrypoint, tc.colima)
			err := CheckHostutilEgressRg(root)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("CheckHostutilEgressRg() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("CheckHostutilEgressRg() = nil, want error %q", tc.wantErr)
			}
			if err.Error() != tc.wantErr {
				t.Fatalf("CheckHostutilEgressRg() error = %q, want %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestCheckHostutilEgressRgCount(t *testing.T) {
	got := len(hostutilEgressRgChecks)
	const want = 21
	if got != want {
		t.Fatalf("hostutilEgressRgChecks has %d checks, want %d", got, want)
	}
}

// TestCheckHostutilEgressRgLineParity proves the per-line evaluator does not let
// an escaped-literal pattern match across a newline: the HOST_GO_BIN run pattern
// matches its intact single line but must NOT match when split by a newline,
// mirroring ripgrep's default (non-multiline) behaviour.
func TestCheckHostutilEgressRgLineParity(t *testing.T) {
	pat := `"\$\{HOST_GO_BIN\}" run ./cmd/workcell-hostutil "\$@"`
	if !regexMatchesAnyLine(pat, `"${HOST_GO_BIN}" run ./cmd/workcell-hostutil "$@"`) {
		t.Fatalf("expected the intact HOST_GO_BIN run line to match")
	}
	if regexMatchesAnyLine(pat, "\"${HOST_GO_BIN}\" run ./cmd/workcell-hostutil\n\"$@\"") {
		t.Fatalf("a HOST_GO_BIN run split across a newline must NOT match (rg is line-oriented)")
	}
}

// TestCheckHostutilEgressRgRealRepo asserts that the real
// scripts/lib/launcher/go-hostutil.sh, runtime/container/entrypoint.sh, and
// scripts/colima-egress-allowlist.sh in this repository satisfy all twenty-one
// hostutil / entrypoint / colima-egress invariants.  This is the key guard
// against a mis-transcribed regex or a wrong target file: if any Go pattern
// diverges from the actual file contents, this test fails with the guard's
// stderr message.
func TestCheckHostutilEgressRgRealRepo(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	if _, err := os.Stat(filepath.Join(repoRoot, goHostutilRelPath)); err != nil {
		t.Skipf("real scripts/lib/launcher/go-hostutil.sh not found at %s: %v", repoRoot, err)
	}
	if err := CheckHostutilEgressRg(repoRoot); err != nil {
		t.Fatalf("CheckHostutilEgressRg(real repo) = %v, want nil", err)
	}
}

// dockerfilePinsHappyBody is a minimal but structurally faithful Dockerfile
// that satisfies all fifteen per-Dockerfile dockerfile-pin invariants: it pins
// the snapshot CA bundle / amd64+arm64 OpenSSL bootstrap packages, the apt
// retry/timeout settings, the retry-and-discard TLS bootstrap download loop, the
// fail-closed download/checksum/dpkg chain, and the unprivileged `USER workcell`
// default.  Each snippet sits on its own physical line so the per-line evaluator
// (regexMatchesAnyLine, `rg` parity) matches it exactly as ripgrep would.  Both
// fixture Dockerfiles use this baseline; individual negative cases mutate one
// property of one file.
const dockerfilePinsHappyBody = `FROM debian:trixie-slim
ARG CA_DEB=ca-certificates_20250419_all.deb
ARG OPENSSL_AMD64=openssl_3.5.5-1~deb13u1_amd64.deb
ARG OPENSSL_ARM64=openssl_3.5.5-1~deb13u1_arm64.deb
RUN echo 'Acquire::Retries "5";' >>/etc/apt/apt.conf
RUN echo 'Acquire::http::Timeout "30";' >>/etc/apt/apt.conf
RUN echo 'Acquire::https::Timeout "30";' >>/etc/apt/apt.conf
RUN for attempt in 1 2 3; do \
      rm -f "${output}"; \
      sleep "$((attempt * 5))"; \
    done
RUN fetch_snapshot_bootstrap_package "${openssl_url}" /tmp/workcell-bootstrap-openssl.deb \
    && echo "${openssl_sha256}  /tmp/workcell-bootstrap-openssl.deb" | sha256sum -c - \
    && fetch_snapshot_bootstrap_package "${ca_url}" /tmp/workcell-bootstrap-ca-certificates.deb \
    && echo "${ca_sha256}  /tmp/workcell-bootstrap-ca-certificates.deb" | sha256sum -c - \
    && dpkg -i /tmp/workcell-bootstrap-openssl.deb /tmp/workcell-bootstrap-ca-certificates.deb
USER workcell
`

func writeDockerfilePinsRepo(t *testing.T, runtimeDF, validatorDF string) string {
	t.Helper()
	root := t.TempDir()
	write := func(rel, body string) {
		if body == "" {
			return
		}
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	write(dockerfileRelPath, runtimeDF)
	write(validatorDockerfileRelPath, validatorDF)
	return root
}

func TestCheckDockerfilePins(t *testing.T) {
	tests := []struct {
		name        string
		runtimeDF   string
		validatorDF string
		wantRel     string // "" means expect success
		wantSuffix  string
	}{
		{
			name:        "happy path all invariants hold",
			runtimeDF:   dockerfilePinsHappyBody,
			validatorDF: dockerfilePinsHappyBody,
		},
		{
			// CA-pin probe missing from the runtime Dockerfile (first probe of
			// the first Dockerfile → first violation).
			name:        "runtime missing CA bundle pin",
			runtimeDF:   strings.Replace(dockerfilePinsHappyBody, "ca-certificates_20250419_all.deb", "ca-certificates_OLD_all.deb", 1),
			validatorDF: dockerfilePinsHappyBody,
			wantRel:     dockerfileRelPath,
			wantSuffix:  "to pin a snapshot CA bundle bootstrap package before HTTPS apt",
		},
		{
			// arm64 OpenSSL pin missing from the validator Dockerfile: the runtime
			// Dockerfile passes entirely first, then the validator's third probe
			// fires, proving the Dockerfile-outer order.
			name:        "validator missing arm64 OpenSSL pin",
			runtimeDF:   dockerfilePinsHappyBody,
			validatorDF: strings.Replace(dockerfilePinsHappyBody, "openssl_3.5.5-1~deb13u1_arm64.deb", "openssl_OLD_arm64.deb", 1),
			wantRel:     validatorDockerfileRelPath,
			wantSuffix:  "to pin the arm64 snapshot OpenSSL bootstrap package before HTTPS apt",
		},
		{
			// apt HTTPS timeout pin missing from the runtime Dockerfile.
			name:        "runtime missing apt HTTPS timeout",
			runtimeDF:   strings.Replace(dockerfilePinsHappyBody, `Acquire::https::Timeout "30";`, "", 1),
			validatorDF: dockerfilePinsHappyBody,
			wantRel:     dockerfileRelPath,
			wantSuffix:  "to pin apt HTTPS timeout for snapshot fetch resilience",
		},
		{
			// retry/discard shared-message guard: the sleep probe removed from the
			// validator Dockerfile yields the guard's shared message.
			name:        "validator missing retry sleep",
			runtimeDF:   dockerfilePinsHappyBody,
			validatorDF: strings.Replace(dockerfilePinsHappyBody, `sleep "$((attempt * 5))";`, "", 1),
			wantRel:     validatorDockerfileRelPath,
			wantSuffix:  "snapshot TLS bootstrap downloads to retry and discard partial packages",
		},
		{
			// fail-closed shared-message guard: the dpkg probe broken in the
			// runtime Dockerfile yields the guard's shared message.
			name:        "runtime broken fail-closed dpkg step",
			runtimeDF:   strings.Replace(dockerfilePinsHappyBody, "dpkg -i /tmp/workcell-bootstrap-openssl.deb", "dpkgBROKEN -i /tmp/workcell-bootstrap-openssl.deb", 1),
			validatorDF: dockerfilePinsHappyBody,
			wantRel:     dockerfileRelPath,
			wantSuffix:  "snapshot TLS bootstrap to fail closed across download, checksum, and dpkg steps",
		},
		{
			// USER-default probe (second loop) missing from the validator
			// Dockerfile: every pin probe passes for both Dockerfiles, then the
			// validator's USER probe (last check) fires.
			name:        "validator missing unprivileged USER default",
			runtimeDF:   dockerfilePinsHappyBody,
			validatorDF: strings.Replace(dockerfilePinsHappyBody, "USER workcell", "USER root", 1),
			wantRel:     validatorDockerfileRelPath,
			wantSuffix:  "to default to the named unprivileged workcell user",
		},
		{
			// A missing runtime Dockerfile is empty content: its first probe fails,
			// mirroring `rg -q` returning non-zero on a missing file.
			name:        "runtime Dockerfile missing",
			runtimeDF:   "",
			validatorDF: dockerfilePinsHappyBody,
			wantRel:     dockerfileRelPath,
			wantSuffix:  "to pin a snapshot CA bundle bootstrap package before HTTPS apt",
		},
		{
			// Both Dockerfiles broken: the runtime CA pin (first check overall)
			// wins over the validator USER pin, proving the runtime-before-validator
			// ordering.
			name:        "both broken runtime wins",
			runtimeDF:   strings.Replace(dockerfilePinsHappyBody, "ca-certificates_20250419_all.deb", "ca-certificates_OLD_all.deb", 1),
			validatorDF: strings.Replace(dockerfilePinsHappyBody, "USER workcell", "USER root", 1),
			wantRel:     dockerfileRelPath,
			wantSuffix:  "to pin a snapshot CA bundle bootstrap package before HTTPS apt",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := writeDockerfilePinsRepo(t, tc.runtimeDF, tc.validatorDF)
			err := CheckDockerfilePins(root)
			if tc.wantRel == "" {
				if err != nil {
					t.Fatalf("CheckDockerfilePins() = %v, want nil", err)
				}
				return
			}
			want := "Expected " + root + "/" + tc.wantRel + " " + tc.wantSuffix
			if err == nil {
				t.Fatalf("CheckDockerfilePins() = nil, want error %q", want)
			}
			if err.Error() != want {
				t.Fatalf("CheckDockerfilePins() error = %q, want %q", err.Error(), want)
			}
		})
	}
}

// TestCheckDockerfilePinsCount asserts the generated check list contains exactly
// thirty checks: fourteen snapshot-TLS-bootstrap pins plus one USER-default per
// Dockerfile, across two Dockerfiles.
func TestCheckDockerfilePinsCount(t *testing.T) {
	got := len(dockerfilePinsChecks("/repo"))
	const want = 30
	if got != want {
		t.Fatalf("dockerfilePinsChecks(...) has %d checks, want %d", got, want)
	}
}

// TestCheckDockerfilePinsLineParity proves the per-line evaluator does not let an
// escaped-literal fail-closed pattern match across a newline: the dpkg probe
// matches its intact single line but must NOT match when split by a newline,
// mirroring ripgrep's default (non-multiline) behaviour.
func TestCheckDockerfilePinsLineParity(t *testing.T) {
	pat := `&& dpkg -i /tmp/workcell-bootstrap-openssl\.deb /tmp/workcell-bootstrap-ca-certificates\.deb`
	if !regexMatchesAnyLine(pat, "&& dpkg -i /tmp/workcell-bootstrap-openssl.deb /tmp/workcell-bootstrap-ca-certificates.deb") {
		t.Fatalf("expected the intact dpkg fail-closed line to match")
	}
	if regexMatchesAnyLine(pat, "&& dpkg -i /tmp/workcell-bootstrap-openssl.deb\n/tmp/workcell-bootstrap-ca-certificates.deb") {
		t.Fatalf("a dpkg step split across a newline must NOT match (rg is line-oriented)")
	}
}

// TestCheckDockerfilePinsRealRepo asserts that the real
// runtime/container/Dockerfile and tools/validator/Dockerfile in this repository
// satisfy all thirty dockerfile-pin invariants.  This is the key guard against a
// mis-transcribed regex or a wrong target file: if any Go pattern diverges from
// the actual Dockerfile contents, this test fails with the guard's stderr
// message.
func TestCheckDockerfilePinsRealRepo(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	if _, err := os.Stat(filepath.Join(repoRoot, dockerfileRelPath)); err != nil {
		t.Skipf("real runtime/container/Dockerfile not found at %s: %v", repoRoot, err)
	}
	if err := CheckDockerfilePins(repoRoot); err != nil {
		t.Fatalf("CheckDockerfilePins(real repo) = %v, want nil", err)
	}
}

// validatorDispatchLoopsHappyFiles returns the six-file fixture map that
// satisfies all thirteen validator-dispatch invariants: the validator Dockerfile
// carries every ENV pin, scripts/validate-repo.sh carries both Cargo-target
// externalization needles, and each dispatch target file carries its needle
// (scripts/pre-merge.sh carries both of its needles).  Needle bodies are built
// from the exported slices so the fixture stays in lockstep with the migration.
func validatorDispatchLoopsHappyFiles() map[string]string {
	return map[string]string{
		validatorDockerfileRelPath: "FROM debian\n" + strings.Join(validatorEnvPinNeedles, "\n") + "\n",
		validateRepoRelPath: "#!/usr/bin/env bash\n" +
			`WORKCELL_VALIDATE_CACHE_HOME="${WORKCELL_VALIDATE_CACHE_HOME:-${XDG_CACHE_HOME}/workcell/validate}"` + "\n" +
			`CARGO_TARGET_DIR="${CARGO_TARGET_DIR:-${WORKCELL_VALIDATE_CACHE_HOME}/cargo-target}"` + "\n",
		ciWorkflowRelPath:       "steps:\n  - run: ./scripts/ci/job-validate.sh --profile pr-parity\n",
		docsWorkflowRelPath:     "steps:\n  - run: ./scripts/ci/job-docs.sh\n",
		mutationWorkflowRelPath: "steps:\n  - run: ./scripts/ci/job-mutation.sh\n",
		preMergeRelPath:         "#!/usr/bin/env bash\nscripts/ci/job-validate.sh\nscripts/ci/job-docs.sh\n",
	}
}

func TestCheckValidatorDispatchLoops(t *testing.T) {
	root := t.TempDir()

	tests := []struct {
		name    string
		mutate  func(files map[string]string)
		wantErr string // "" means expect success; absolute-path messages use the shared root
	}{
		{
			name:   "happy path all invariants hold",
			mutate: func(map[string]string) {},
		},
		{
			// ENV-pin loop: the first needle removed from the validator
			// Dockerfile fails with the interpolated absolute-path message.
			name: "validator Dockerfile missing HOME pin",
			mutate: func(files map[string]string) {
				files[validatorDockerfileRelPath] = strings.Replace(files[validatorDockerfileRelPath], "ENV HOME=/home/workcell\n", "", 1)
			},
			wantErr: "Expected " + root + "/" + validatorDockerfileRelPath + " to pin its default nonroot writable state under /home/workcell (ENV HOME=/home/workcell)",
		},
		{
			// ENV-pin loop: a middle needle removed fires with its own message,
			// proving the per-needle interpolation.
			name: "validator Dockerfile missing GOMODCACHE pin",
			mutate: func(files map[string]string) {
				files[validatorDockerfileRelPath] = strings.Replace(files[validatorDockerfileRelPath], "ENV GOMODCACHE=/home/workcell/.cache/go-mod\n", "", 1)
			},
			wantErr: "Expected " + root + "/" + validatorDockerfileRelPath + " to pin its default nonroot writable state under /home/workcell (ENV GOMODCACHE=/home/workcell/.cache/go-mod)",
		},
		{
			// A missing validator Dockerfile is empty content: the first
			// affirmative ENV pin fails, mirroring grep on a missing file.
			name: "validator Dockerfile missing",
			mutate: func(files map[string]string) {
				files[validatorDockerfileRelPath] = ""
			},
			wantErr: "Expected " + root + "/" + validatorDockerfileRelPath + " to pin its default nonroot writable state under /home/workcell (ENV HOME=/home/workcell)",
		},
		{
			// validate-repo || guard: the first probe (cache-home) removed fires
			// the shared message.
			name: "validate-repo missing cache-home probe",
			mutate: func(files map[string]string) {
				files[validateRepoRelPath] = strings.Replace(files[validateRepoRelPath], `WORKCELL_VALIDATE_CACHE_HOME="${WORKCELL_VALIDATE_CACHE_HOME:-${XDG_CACHE_HOME}/workcell/validate}"`+"\n", "", 1)
			},
			wantErr: "Expected scripts/validate-repo.sh to externalize Cargo target writes under the Workcell-owned validation cache",
		},
		{
			// validate-repo || guard: the second probe (cargo-target) removed
			// fires the same shared message, proving either missing probe yields
			// identical stderr.
			name: "validate-repo missing cargo-target probe",
			mutate: func(files map[string]string) {
				files[validateRepoRelPath] = strings.Replace(files[validateRepoRelPath], `CARGO_TARGET_DIR="${CARGO_TARGET_DIR:-${WORKCELL_VALIDATE_CACHE_HOME}/cargo-target}"`+"\n", "", 1)
			},
			wantErr: "Expected scripts/validate-repo.sh to externalize Cargo target writes under the Workcell-owned validation cache",
		},
		{
			// dispatch loop: the ci.yml needle removed fires with the
			// interpolated absolute-path + needle message.
			name: "ci workflow missing dispatch needle",
			mutate: func(files map[string]string) {
				files[ciWorkflowRelPath] = "steps:\n  - run: echo noop\n"
			},
			wantErr: "Expected " + root + "/" + ciWorkflowRelPath + " to dispatch validator parity through the shared CI entrypoints (./scripts/ci/job-validate.sh --profile pr-parity)",
		},
		{
			// dispatch loop: the second pre-merge needle (job-docs) removed while
			// the first (job-validate) stays fires the job-docs message, proving
			// both same-file probes are distinct checks.
			name: "pre-merge missing job-docs dispatch needle",
			mutate: func(files map[string]string) {
				files[preMergeRelPath] = "#!/usr/bin/env bash\nscripts/ci/job-validate.sh\n"
			},
			wantErr: "Expected " + root + "/" + preMergeRelPath + " to dispatch validator parity through the shared CI entrypoints (scripts/ci/job-docs.sh)",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			files := validatorDispatchLoopsHappyFiles()
			tc.mutate(files)
			for rel, body := range files {
				path := filepath.Join(root, rel)
				if body == "" {
					os.Remove(path)
					continue
				}
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
				}
				if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
					t.Fatalf("write %s: %v", path, err)
				}
			}
			err := CheckValidatorDispatchLoops(root)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("CheckValidatorDispatchLoops() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("CheckValidatorDispatchLoops() = nil, want error %q", tc.wantErr)
			}
			if err.Error() != tc.wantErr {
				t.Fatalf("CheckValidatorDispatchLoops() error = %q, want %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// TestCheckValidatorDispatchLoopsCount asserts the generated check list contains
// exactly thirteen invariants: six validator-Dockerfile ENV pins, two
// validate-repo Cargo-target probes, and five CI-dispatch probes.
func TestCheckValidatorDispatchLoopsCount(t *testing.T) {
	got := len(validatorDispatchLoopsChecks("/repo"))
	const want = 13
	if got != want {
		t.Fatalf("validatorDispatchLoopsChecks(...) has %d checks, want %d", got, want)
	}
}

// TestCheckValidatorDispatchLoopsRealRepo asserts that the real
// tools/validator/Dockerfile, scripts/validate-repo.sh, the three lane
// workflows, and scripts/pre-merge.sh in this repository satisfy all thirteen
// validator-dispatch invariants.  This is the key guard against a
// mis-transcribed needle or a wrong target file: if any Go pattern diverges from
// the actual file contents, this test fails with the guard's stderr message.
func TestCheckValidatorDispatchLoopsRealRepo(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	if _, err := os.Stat(filepath.Join(repoRoot, validatorDockerfileRelPath)); err != nil {
		t.Skipf("real tools/validator/Dockerfile not found at %s: %v", repoRoot, err)
	}
	if err := CheckValidatorDispatchLoops(repoRoot); err != nil {
		t.Fatalf("CheckValidatorDispatchLoops(real repo) = %v, want nil", err)
	}
}

// callerRequiredContractsHappyFiles returns the five-file fixture map that
// satisfies all fifty caller-required invariants: every caller file carries all
// ten required needles (joined by newlines so grep-style containment holds).
// Needle bodies are built from the exported slices so the fixture stays in
// lockstep with the migration.
func callerRequiredContractsHappyFiles() map[string]string {
	files := make(map[string]string, len(callerRequiredContractsCallers))
	body := "#!/usr/bin/env bash\n" + strings.Join(callerRequiredContractsNeedles, "\n") + "\n"
	for _, rel := range callerRequiredContractsCallers {
		files[rel] = body
	}
	return files
}

func TestCheckCallerRequiredContracts(t *testing.T) {
	root := t.TempDir()

	tests := []struct {
		name    string
		mutate  func(files map[string]string)
		wantErr string // "" means expect success; absolute-path messages use the shared root
	}{
		{
			name:   "happy path all invariants hold",
			mutate: func(map[string]string) {},
		},
		{
			// Outer=first caller, inner=first needle: removing the UID needle from
			// the first caller fires the interpolated absolute-path + needle
			// message, proving caller-outer/needle-inner interpolation.
			name: "first caller missing UID needle",
			mutate: func(files map[string]string) {
				files[runValidateInValidatorRelPath] = strings.Replace(files[runValidateInValidatorRelPath], `validator_uid="$(id -u)"`+"\n", "", 1)
			},
			wantErr: "Expected " + root + "/" + runValidateInValidatorRelPath + " to launch validator work under an explicit caller UID/GID with isolated writable state (validator_uid=\"$(id -u)\")",
		},
		{
			// A middle caller missing a middle needle: proves the loop advances
			// through earlier callers/needles before firing.
			name: "third caller missing GOCACHE needle",
			mutate: func(files map[string]string) {
				files[runMutationInValidatorRelPath] = strings.Replace(files[runMutationInValidatorRelPath], `-e GOCACHE="${validator_cache}/go-build"`+"\n", "", 1)
			},
			wantErr: "Expected " + root + "/" + runMutationInValidatorRelPath + " to launch validator work under an explicit caller UID/GID with isolated writable state (-e GOCACHE=\"${validator_cache}/go-build\")",
		},
		{
			// The last caller (release.yml) missing the last needle (mkdir -p):
			// the final (caller, required) pair fires, proving the full traversal.
			name: "release workflow missing mkdir needle",
			mutate: func(files map[string]string) {
				files[releaseWorkflowRelPath] = strings.Replace(files[releaseWorkflowRelPath], `mkdir -p "${HOME}" "${XDG_CACHE_HOME}" "${GOCACHE}" "${GOMODCACHE}" "${CARGO_TARGET_DIR}" "${TMPDIR}"`+"\n", "", 1)
			},
			wantErr: "Expected " + root + "/" + releaseWorkflowRelPath + " to launch validator work under an explicit caller UID/GID with isolated writable state (mkdir -p \"${HOME}\" \"${XDG_CACHE_HOME}\" \"${GOCACHE}\" \"${GOMODCACHE}\" \"${CARGO_TARGET_DIR}\" \"${TMPDIR}\")",
		},
		{
			// caller-outer ordering: when the FIRST caller is entirely missing
			// (empty content) its first needle fires before any later caller runs,
			// even though a later caller is also broken.
			name: "first caller missing wins over later caller",
			mutate: func(files map[string]string) {
				files[runValidateInValidatorRelPath] = ""
				files[jobValidateRelPath] = "#!/usr/bin/env bash\n"
			},
			wantErr: "Expected " + root + "/" + runValidateInValidatorRelPath + " to launch validator work under an explicit caller UID/GID with isolated writable state (validator_uid=\"$(id -u)\")",
		},
		{
			// needle-inner ordering within a single caller: an EARLIER needle
			// missing wins over a later one in the same file.
			name: "earlier needle wins within a caller",
			mutate: func(files map[string]string) {
				files[runDocsInValidatorRelPath] = strings.Replace(files[runDocsInValidatorRelPath], `validator_gid="$(id -g)"`+"\n", "", 1)
				files[runDocsInValidatorRelPath] = strings.Replace(files[runDocsInValidatorRelPath], `-e TMPDIR="${validator_tmp}"`+"\n", "", 1)
			},
			wantErr: "Expected " + root + "/" + runDocsInValidatorRelPath + " to launch validator work under an explicit caller UID/GID with isolated writable state (validator_gid=\"$(id -g)\")",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			files := callerRequiredContractsHappyFiles()
			tc.mutate(files)
			for rel, body := range files {
				path := filepath.Join(root, rel)
				if body == "" {
					os.Remove(path)
					continue
				}
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
				}
				if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
					t.Fatalf("write %s: %v", path, err)
				}
			}
			err := CheckCallerRequiredContracts(root)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("CheckCallerRequiredContracts() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("CheckCallerRequiredContracts() = nil, want error %q", tc.wantErr)
			}
			if err.Error() != tc.wantErr {
				t.Fatalf("CheckCallerRequiredContracts() error = %q, want %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// TestCheckCallerRequiredContractsCount asserts the generated check list contains
// exactly fifty invariants: five caller files × ten required needles.
func TestCheckCallerRequiredContractsCount(t *testing.T) {
	got := len(callerRequiredContractsChecks("/repo"))
	const want = 50
	if got != want {
		t.Fatalf("callerRequiredContractsChecks(...) has %d checks, want %d", got, want)
	}
}

// TestCheckCallerRequiredContractsRealRepo asserts that the five real caller
// files in this repository satisfy all fifty caller-required invariants.  This is
// the key guard against a mis-transcribed needle or a wrong target file: if any
// Go needle diverges from the actual file contents, this test fails with the
// guard's stderr message.
func TestCheckCallerRequiredContractsRealRepo(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	if _, err := os.Stat(filepath.Join(repoRoot, runValidateInValidatorRelPath)); err != nil {
		t.Skipf("real scripts/ci/run-validate-in-validator.sh not found at %s: %v", repoRoot, err)
	}
	if err := CheckCallerRequiredContracts(repoRoot); err != nil {
		t.Fatalf("CheckCallerRequiredContracts(real repo) = %v, want nil", err)
	}
}

// writeFnBlockGoBlockGitEnvRepo materializes a repo from a rel-path->body map,
// skipping empty bodies (so a test can simulate a missing target file).
func writeFnBlockGoBlockGitEnvRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, body := range files {
		if body == "" {
			continue
		}
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	return root
}

// fnBlockGoBlockGitEnvHappyFiles returns the five-file fixture map that
// satisfies all six fnblock/goblock/gitenv invariants: the bash launcher
// carries both function-block regex needles inside their named functions, the
// Go host-exec file carries the fixed needle inside ResolveExistingExecutableOrDie,
// and the git shim carries all three git-env literals.
func fnBlockGoBlockGitEnvHappyFiles() map[string]string {
	launcher := "#!/usr/bin/env bash\n" +
		"validate_colima_profile() {\n" +
		"  validate_colima_profile_config \"$@\"\n" +
		"}\n" +
		"git_alias_value_is_blocked() {\n" +
		"  git_commit_short_arg_is_no_verify \"${token}\"\n" +
		"}\n"
	hostExec := "package publishpr\n\n" +
		"func ResolveExistingExecutableOrDie(ctx *BashContext, rawPath, label string) (string, error) {\n" +
		"\tcanonical := CanonicalizeHostToolPath(rawPath)\n" +
		"\tif canonical == \"\" || !IsTrustedHostToolPath(rawPath, ctx) || !IsTrustedHostToolPath(canonical, ctx) {\n" +
		"\t\treturn \"\", errUntrusted\n" +
		"\t}\n" +
		"\treturn canonical, nil\n" +
		"}\n"
	gitShim := "#!/usr/bin/env bash\n"
	for _, v := range fnBlockGoBlockGitEnvGitEnvVars {
		gitShim += "[[ -n \"${" + v + ":-}\" ]] && exit 1\n"
	}
	return map[string]string{
		launcherRelPath:        launcher,
		hostExecRelPath:        hostExec,
		containerBinGitRelPath: gitShim,
	}
}

func TestCheckFnBlockGoBlockGitEnv(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(files map[string]string)
		wantErr string // "" means expect success
	}{
		{
			name:   "happy path all invariants hold",
			mutate: func(map[string]string) {},
		},
		{
			// function-block regex #1: rename the needle inside
			// validate_colima_profile so grep -q no longer matches in-block.
			name: "colima function-block needle missing",
			mutate: func(files map[string]string) {
				files[launcherRelPath] = strings.Replace(files[launcherRelPath], "validate_colima_profile_config \"$@\"", "renamed_helper \"$@\"", 1)
			},
			wantErr: "Expected validate_colima_profile to re-check the managed Colima config before reusing a running profile",
		},
		{
			// function-block regex #2.
			name: "git-alias function-block needle missing",
			mutate: func(files map[string]string) {
				files[launcherRelPath] = strings.Replace(files[launcherRelPath], "git_commit_short_arg_is_no_verify", "renamed_parser", 1)
			},
			wantErr: "Expected git_alias_value_is_blocked to reuse the precise short-option no-verify parser",
		},
		{
			// go-function-block: needle absent entirely.
			name: "goblock needle absent",
			mutate: func(files map[string]string) {
				files[hostExecRelPath] = strings.Replace(files[hostExecRelPath], "!IsTrustedHostToolPath(rawPath, ctx) || !IsTrustedHostToolPath(canonical, ctx)", "false", 1)
			},
			wantErr: "Expected publishpr.ResolveExistingExecutableOrDie to reject untrusted host executable paths",
		},
		{
			// go-function-block SCOPING: needle present, but only inside a
			// DIFFERENT top-level function, so it must not satisfy the check.
			name: "goblock needle only in a different function",
			mutate: func(files map[string]string) {
				files[hostExecRelPath] = "package publishpr\n\n" +
					"func ResolveExistingExecutableOrDie(ctx *BashContext, rawPath, label string) (string, error) {\n" +
					"\treturn canonical, nil\n" +
					"}\n\n" +
					"func other() {\n" +
					"\t_ = !IsTrustedHostToolPath(rawPath, ctx) || !IsTrustedHostToolPath(canonical, ctx)\n" +
					"}\n"
			},
			wantErr: "Expected publishpr.ResolveExistingExecutableOrDie to reject untrusted host executable paths",
		},
		{
			// go-function-block COMMENT-STRIPPING: needle present inside the
			// right function but only in a // line comment, so it must not
			// satisfy the check.
			name: "goblock needle only in a line comment",
			mutate: func(files map[string]string) {
				files[hostExecRelPath] = "package publishpr\n\n" +
					"func ResolveExistingExecutableOrDie(ctx *BashContext, rawPath, label string) (string, error) {\n" +
					"\t// !IsTrustedHostToolPath(rawPath, ctx) || !IsTrustedHostToolPath(canonical, ctx)\n" +
					"\treturn canonical, nil\n" +
					"}\n"
			},
			wantErr: "Expected publishpr.ResolveExistingExecutableOrDie to reject untrusted host executable paths",
		},
		{
			name: "git-env first var missing",
			mutate: func(files map[string]string) {
				files[containerBinGitRelPath] = strings.Replace(files[containerBinGitRelPath], `"${GIT_OBJECT_DIRECTORY:-}"`, `"${GIT_OBJECT_DIRECTORY_X:-}"`, 1)
			},
			wantErr: "Expected runtime/container/bin/git to block GIT_OBJECT_DIRECTORY to prevent object-store redirection",
		},
		{
			name: "git-env middle var missing",
			mutate: func(files map[string]string) {
				files[containerBinGitRelPath] = strings.Replace(files[containerBinGitRelPath], `"${GIT_ALTERNATE_OBJECT_DIRECTORIES:-}"`, `"${GIT_ALTERNATE_OBJECT_DIRECTORIES_X:-}"`, 1)
			},
			wantErr: "Expected runtime/container/bin/git to block GIT_ALTERNATE_OBJECT_DIRECTORIES to prevent object-store redirection",
		},
		{
			name: "git-env last var missing",
			mutate: func(files map[string]string) {
				files[containerBinGitRelPath] = strings.Replace(files[containerBinGitRelPath], `"${GIT_INDEX_FILE:-}"`, `"${GIT_INDEX_FILE_X:-}"`, 1)
			},
			wantErr: "Expected runtime/container/bin/git to block GIT_INDEX_FILE to prevent object-store redirection",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files := fnBlockGoBlockGitEnvHappyFiles()
			tt.mutate(files)
			root := writeFnBlockGoBlockGitEnvRepo(t, files)
			err := CheckFnBlockGoBlockGitEnv(root)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("CheckFnBlockGoBlockGitEnv() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("CheckFnBlockGoBlockGitEnv() = nil, want error %q", tt.wantErr)
			}
			if err.Error() != tt.wantErr {
				t.Fatalf("CheckFnBlockGoBlockGitEnv() = %q, want %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestCheckFnBlockGoBlockGitEnvCount(t *testing.T) {
	got := len(fnBlockGoBlockGitEnvChecks())
	const want = 6
	if got != want {
		t.Fatalf("fnBlockGoBlockGitEnvChecks() has %d checks, want %d", got, want)
	}
}

// TestCheckFnBlockGoBlockGitEnvRealRepo asserts that the five real target files
// in this repository satisfy all eight fnblock/goblock/gitenv invariants.  This
// is the key guard against a mis-transcribed needle or wrong target file: if any
// Go needle diverges from the actual file contents, this test fails with the
// guard's stderr message.
func TestCheckFnBlockGoBlockGitEnvRealRepo(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	if _, err := os.Stat(filepath.Join(repoRoot, hostExecRelPath)); err != nil {
		t.Skipf("real internal/publishpr/host_exec.go not found at %s: %v", repoRoot, err)
	}
	if err := CheckFnBlockGoBlockGitEnv(repoRoot); err != nil {
		t.Fatalf("CheckFnBlockGoBlockGitEnv(real repo) = %v, want nil", err)
	}
}

// TestExtractGoFunctionBlock exercises extractGoFunctionBlock's parity with the
// awk in go_function_block_contains_fixed: func-scoped extraction, exact `}`
// termination, and // / /* */ (including multi-line) comment stripping.
func TestExtractGoFunctionBlock(t *testing.T) {
	tests := []struct {
		name        string
		text        string
		fn          string
		wantContain string
		notContain  string
	}{
		{
			name:        "scopes to the named function and stops at exact closing brace",
			text:        "func A() {\n\tkeep\n}\n\nfunc B() {\n\tother\n}\n",
			fn:          "A",
			wantContain: "keep",
			notContain:  "other",
		},
		{
			name:       "does not include a needle from a later function",
			text:       "func A() {\n\treturn\n}\n\nfunc B() {\n\tNEEDLE\n}\n",
			fn:         "A",
			notContain: "NEEDLE",
		},
		{
			name:        "strips a // line comment",
			text:        "func A() {\n\t// NEEDLE\n\tcode\n}\n",
			fn:          "A",
			wantContain: "code",
			notContain:  "NEEDLE",
		},
		{
			name:        "strips a multi-line /* */ block comment",
			text:        "func A() {\n\t/* NEEDLE\n\tstill in comment NEEDLE2 */\n\tcode\n}\n",
			fn:          "A",
			wantContain: "code",
			notContain:  "NEEDLE",
		},
		{
			name:        "keeps code sharing a line after a closed block comment",
			text:        "func A() {\n\t/* c */ keep\n}\n",
			fn:          "A",
			wantContain: "keep",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractGoFunctionBlock(tt.text, tt.fn)
			if tt.wantContain != "" && !strings.Contains(got, tt.wantContain) {
				t.Fatalf("extractGoFunctionBlock(...) = %q, want it to contain %q", got, tt.wantContain)
			}
			if tt.notContain != "" && strings.Contains(got, tt.notContain) {
				t.Fatalf("extractGoFunctionBlock(...) = %q, want it NOT to contain %q", got, tt.notContain)
			}
		})
	}
}

// --- D3 simple-clusters sweep: buildx-builder-trust ---

// buildxBuilderTrustHappyFiles returns the six-file fixture map that satisfies
// all eight buildx-builder-trust invariants.
func buildxBuilderTrustHappyFiles() map[string]string {
	return map[string]string{
		verifyReleaseBundleRelPath:     "#!/usr/bin/env bash\nBUILDX_BUILDER=\"workcell-release-${ctx}\"\n",
		buildAndTestRelPath:            "#!/usr/bin/env bash\n: \"${WORKCELL_KEEP_VALIDATOR_IMAGE:-}\"\n",
		jobValidateRelPath:             "#!/usr/bin/env bash\ncleanup_workcell_validator_image\n",
		jobDocsRelPath:                 "#!/usr/bin/env bash\ncleanup_workcell_validator_image\n",
		verifyReproducibleBuildRelPath: "#!/usr/bin/env bash\n: \"${WORKCELL_REPRO_OWNS_BUILDER:-}\"\n",
		trustedDockerClientRelPath:     "#!/usr/bin/env bash\nbuildx_expected_endpoints() { :; }\ndocker context inspect \"${DOCKER_CONTEXT_NAME}\" --format '{{.x}}'\n",
		colimaEgressAllowlistRelPath:   "#!/usr/bin/env bash\nCOLIMA_HOME=\"${colima_home}\"\n",
	}
}

func TestCheckBuildxBuilderTrust(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(files map[string]string)
		wantErr string
	}{
		{name: "happy path all invariants hold", mutate: func(map[string]string) {}},
		{
			name: "release builder needle missing",
			mutate: func(f map[string]string) {
				f[verifyReleaseBundleRelPath] = strings.Replace(f[verifyReleaseBundleRelPath], `BUILDX_BUILDER="workcell-release-`, `BUILDX_BUILDER="other-`, 1)
			},
			wantErr: "Expected verify-release-bundle.sh to choose a deterministic context-scoped Buildx builder by default",
		},
		{
			name: "keep-validator-image needle missing",
			mutate: func(f map[string]string) {
				f[buildAndTestRelPath] = strings.Replace(f[buildAndTestRelPath], "WORKCELL_KEEP_VALIDATOR_IMAGE", "X", 1)
			},
			wantErr: "Expected local validator lanes to remove disposable validator images unless explicitly retained",
		},
		{
			name: "job-validate cleanup needle missing",
			mutate: func(f map[string]string) {
				f[jobValidateRelPath] = strings.Replace(f[jobValidateRelPath], "cleanup_workcell_validator_image", "X", 1)
			},
			wantErr: "Expected local validator lanes to remove disposable validator images unless explicitly retained",
		},
		{
			name: "job-docs cleanup needle missing",
			mutate: func(f map[string]string) {
				f[jobDocsRelPath] = strings.Replace(f[jobDocsRelPath], "cleanup_workcell_validator_image", "X", 1)
			},
			wantErr: "Expected local validator lanes to remove disposable validator images unless explicitly retained",
		},
		{
			name: "repro-owns-builder needle missing",
			mutate: func(f map[string]string) {
				f[verifyReproducibleBuildRelPath] = strings.Replace(f[verifyReproducibleBuildRelPath], "WORKCELL_REPRO_OWNS_BUILDER", "X", 1)
			},
			wantErr: "Expected reproducible-build validation to remove its default Workcell-owned Buildx builder on exit",
		},
		{
			name: "buildx-expected-endpoints needle missing",
			mutate: func(f map[string]string) {
				f[trustedDockerClientRelPath] = strings.Replace(f[trustedDockerClientRelPath], "buildx_expected_endpoints()", "renamed()", 1)
			},
			wantErr: "Expected trusted-docker-client.sh to compute accepted Buildx endpoints from the active Docker context or host",
		},
		{
			name: "docker-context-inspect needle missing",
			mutate: func(f map[string]string) {
				f[trustedDockerClientRelPath] = strings.Replace(f[trustedDockerClientRelPath], `docker context inspect "${DOCKER_CONTEXT_NAME}" --format`, "X", 1)
			},
			wantErr: "Expected trusted-docker-client.sh to resolve Docker context host URIs when validating existing Buildx builders",
		},
		{
			name: "colima-home pin needle missing",
			mutate: func(f map[string]string) {
				f[colimaEgressAllowlistRelPath] = strings.Replace(f[colimaEgressAllowlistRelPath], `COLIMA_HOME="${colima_home}"`, "X", 1)
			},
			wantErr: "Expected scripts/colima-egress-allowlist.sh to pin COLIMA_HOME while operating on Lima state",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files := buildxBuilderTrustHappyFiles()
			tt.mutate(files)
			root := writeFnBlockGoBlockGitEnvRepo(t, files)
			err := CheckBuildxBuilderTrust(root)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("CheckBuildxBuilderTrust() = %v, want nil", err)
				}
				return
			}
			if err == nil || err.Error() != tt.wantErr {
				t.Fatalf("CheckBuildxBuilderTrust() = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestCheckBuildxBuilderTrustCount(t *testing.T) {
	if got, want := len(buildxBuilderTrustChecks), 8; got != want {
		t.Fatalf("buildxBuilderTrustChecks has %d checks, want %d", got, want)
	}
}

func TestCheckBuildxBuilderTrustRealRepo(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	if _, err := os.Stat(filepath.Join(repoRoot, verifyReleaseBundleRelPath)); err != nil {
		t.Skipf("real scripts/verify-release-bundle.sh not found at %s: %v", repoRoot, err)
	}
	if err := CheckBuildxBuilderTrust(repoRoot); err != nil {
		t.Fatalf("CheckBuildxBuilderTrust(real repo) = %v, want nil", err)
	}
}

// --- D3 simple-clusters sweep: doc-scan / go-vcs ---

func docScanGoVcsHappyFiles() map[string]string {
	return map[string]string{
		validateRepoRelPath: "#!/usr/bin/env bash\nfind . -path \"${ROOT_DIR}/.venv\" -prune -o -print\n",
		goRunEnvRelPath:     "#!/usr/bin/env bash\ngo build -buildvcs=false -o \"${output_path}\" ./cmd\n",
	}
}

func TestCheckDocScanGoVcs(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(files map[string]string)
		wantErr string
	}{
		{name: "happy path all invariants hold", mutate: func(map[string]string) {}},
		{
			name: "venv-prune needle missing",
			mutate: func(f map[string]string) {
				f[validateRepoRelPath] = strings.Replace(f[validateRepoRelPath], `-path "${ROOT_DIR}/.venv" -prune -o`, "-print", 1)
			},
			wantErr: "Expected validate-repo.sh to prune repo-local virtualenv content from documentation scans",
		},
		{
			name: "buildvcs needle missing",
			mutate: func(f map[string]string) {
				f[goRunEnvRelPath] = strings.Replace(f[goRunEnvRelPath], `build -buildvcs=false -o "${output_path}"`, "build -o out", 1)
			},
			wantErr: "Expected build_go_tool_in_repo to disable Go VCS stamping in untrusted repos",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files := docScanGoVcsHappyFiles()
			tt.mutate(files)
			root := writeFnBlockGoBlockGitEnvRepo(t, files)
			err := CheckDocScanGoVcs(root)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("CheckDocScanGoVcs() = %v, want nil", err)
				}
				return
			}
			if err == nil || err.Error() != tt.wantErr {
				t.Fatalf("CheckDocScanGoVcs() = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestCheckDocScanGoVcsCount(t *testing.T) {
	if got, want := len(docScanGoVcsChecks), 2; got != want {
		t.Fatalf("docScanGoVcsChecks has %d checks, want %d", got, want)
	}
}

func TestCheckDocScanGoVcsRealRepo(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	if _, err := os.Stat(filepath.Join(repoRoot, validateRepoRelPath)); err != nil {
		t.Skipf("real scripts/validate-repo.sh not found at %s: %v", repoRoot, err)
	}
	if err := CheckDocScanGoVcs(repoRoot); err != nil {
		t.Fatalf("CheckDocScanGoVcs(real repo) = %v, want nil", err)
	}
}

// --- D3 simple-clusters sweep: container-smoke chown/tar ---

// smokeChownTarHappyFiles returns a container-smoke.sh fixture that contains
// NONE of the three forbidden constructs, so all three absence invariants hold.
func smokeChownTarHappyFiles() map[string]string {
	return map[string]string{
		containerSmokeRelPath: "#!/usr/bin/env bash\nstage_workspace_via_cp\n",
	}
}

func TestCheckSmokeChownTar(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(files map[string]string)
		wantErr string
	}{
		{name: "happy path all invariants hold", mutate: func(map[string]string) {}},
		{
			name: "raw recursive chown present",
			mutate: func(f map[string]string) {
				f[containerSmokeRelPath] += "chown -R \"${HOST_UID}:${HOST_GID}\" \"${target_path}\"\n"
			},
			wantErr: "Expected scripts/container-smoke.sh to avoid raw recursive chown on host-managed paths",
		},
		{
			name: "tar-based staging present",
			mutate: func(f map[string]string) {
				f[containerSmokeRelPath] += "tar --null -T \"${path_list_filtered}\" -cf -\n"
			},
			wantErr: "Expected scripts/container-smoke.sh to avoid tar-based smoke workspace staging",
		},
		{
			name: "tar-based extraction present",
			mutate: func(f map[string]string) {
				f[containerSmokeRelPath] += "tar -xf -\n"
			},
			wantErr: "Expected scripts/container-smoke.sh to avoid tar-based extraction for smoke workspace staging",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files := smokeChownTarHappyFiles()
			tt.mutate(files)
			root := writeFnBlockGoBlockGitEnvRepo(t, files)
			err := CheckSmokeChownTar(root)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("CheckSmokeChownTar() = %v, want nil", err)
				}
				return
			}
			if err == nil || err.Error() != tt.wantErr {
				t.Fatalf("CheckSmokeChownTar() = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestCheckSmokeChownTarCount(t *testing.T) {
	if got, want := len(smokeChownTarChecks), 3; got != want {
		t.Fatalf("smokeChownTarChecks has %d checks, want %d", got, want)
	}
}

func TestCheckSmokeChownTarRealRepo(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	if _, err := os.Stat(filepath.Join(repoRoot, containerSmokeRelPath)); err != nil {
		t.Skipf("real scripts/container-smoke.sh not found at %s: %v", repoRoot, err)
	}
	if err := CheckSmokeChownTar(repoRoot); err != nil {
		t.Fatalf("CheckSmokeChownTar(real repo) = %v, want nil", err)
	}
}

// --- D3 simple-clusters sweep: dual-stack allowlist apply plan ---

// dualStackApplyPlanHappyFiles returns a colima-egress-allowlist.sh fixture that
// satisfies all seven dual-stack allowlist-apply-plan invariants: a line-anchored
// render_clear_plan definition, a render_allowlist_apply_plan block that renders
// the clear plan, resolves endpoint IPs in the VM (getent ahosts) and never calls
// render_allowlist_plan, an ip6tables preflight, and the guarded run_in_vm apply.
func dualStackApplyPlanHappyFiles() map[string]string {
	body := "#!/usr/bin/env bash\n" +
		"render_clear_plan() {\n" +
		"  echo clear\n" +
		"}\n" +
		"render_allowlist_apply_plan() {\n" +
		"  local plan\n" +
		"  plan=\"$(render_clear_plan)\"\n" +
		"  resolve_vm_endpoint_ips \"${endpoints}\"\n" +
		"  getent ahosts \"${host}\"\n" +
		"}\n" +
		"apply_allowlist() {\n" +
		"  if ! type ip6tables >/dev/null 2>&1; then\n" +
		"    return 1\n" +
		"  fi\n" +
		"  run_in_vm \"$(render_allowlist_apply_plan)\"\n" +
		"}\n"
	return map[string]string{colimaEgressAllowlistRelPath: body}
}

func TestCheckDualStackApplyPlan(t *testing.T) {
	rel := colimaEgressAllowlistRelPath
	tests := []struct {
		name    string
		mutate  func(files map[string]string)
		wantErr string
	}{
		{name: "happy path all invariants hold", mutate: func(map[string]string) {}},
		{
			name: "guarded apply path missing",
			mutate: func(f map[string]string) {
				f[rel] = strings.Replace(f[rel], `run_in_vm "$(render_allowlist_apply_plan)"`, "direct_apply", 1)
			},
			wantErr: "Expected dual-stack allowlist apply path to use the guarded apply plan",
		},
		{
			name: "ip6tables preflight missing",
			mutate: func(f map[string]string) {
				f[rel] = strings.Replace(f[rel], "if ! type ip6tables >/dev/null 2>&1; then", "if false; then", 1)
			},
			wantErr: "Expected dual-stack allowlist apply plan to preflight ip6tables before rewriting rules",
		},
		{
			name: "render_clear_plan definition missing",
			mutate: func(f map[string]string) {
				f[rel] = strings.Replace(f[rel], "render_clear_plan() {", "xrender_clear_plan() {", 1)
			},
			wantErr: "Expected dual-stack allowlist helper to render clear rules in the VM apply plan",
		},
		{
			name: "in-block render_clear_plan call missing",
			mutate: func(f map[string]string) {
				f[rel] = strings.Replace(f[rel], `plan="$(render_clear_plan)"`, `plan="$(render_empty)"`, 1)
			},
			wantErr: "Expected dual-stack allowlist apply plan to include render_clear_plan",
		},
		{
			name: "resolve_vm_endpoint_ips missing",
			mutate: func(f map[string]string) {
				f[rel] = strings.Replace(f[rel], `resolve_vm_endpoint_ips "${endpoints}"`, "noop", 1)
			},
			wantErr: "Expected dual-stack allowlist apply plan to resolve hostnames inside the VM before applying rules",
		},
		{
			name: "getent ahosts missing",
			mutate: func(f map[string]string) {
				f[rel] = strings.Replace(f[rel], `getent ahosts "${host}"`, "noop", 1)
			},
			wantErr: "Expected dual-stack allowlist apply plan to use VM DNS results for hostname endpoints",
		},
		{
			name: "host-resolved render_allowlist_plan present in block",
			mutate: func(f map[string]string) {
				f[rel] = strings.Replace(f[rel], `getent ahosts "${host}"`, "getent ahosts \"${host}\"\n  render_allowlist_plan", 1)
			},
			wantErr: "Expected dual-stack allowlist apply plan to avoid host-resolved endpoint rules",
		},
		{
			name: "bare clear_rules present in render block",
			mutate: func(f map[string]string) {
				f[rel] = strings.Replace(f[rel], `getent ahosts "${host}"`, "getent ahosts \"${host}\"\n  clear_rules", 1)
			},
			wantErr: "Expected dual-stack allowlist apply plan to avoid invoking clear_rules during render",
		},
		{
			name: "clear_rules only in a different function passes",
			mutate: func(f map[string]string) {
				f[rel] = strings.Replace(f[rel], "apply_allowlist() {\n", "apply_allowlist() {\n  clear_rules\n", 1)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files := dualStackApplyPlanHappyFiles()
			tt.mutate(files)
			root := writeFnBlockGoBlockGitEnvRepo(t, files)
			err := CheckDualStackApplyPlan(root)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("CheckDualStackApplyPlan() = %v, want nil", err)
				}
				return
			}
			if err == nil || err.Error() != tt.wantErr {
				t.Fatalf("CheckDualStackApplyPlan() = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestCheckDualStackApplyPlanCount(t *testing.T) {
	if got, want := len(dualStackApplyPlanChecks), 8; got != want {
		t.Fatalf("dualStackApplyPlanChecks has %d checks, want %d", got, want)
	}
}

func TestCheckDualStackApplyPlanRealRepo(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	if _, err := os.Stat(filepath.Join(repoRoot, colimaEgressAllowlistRelPath)); err != nil {
		t.Skipf("real scripts/colima-egress-allowlist.sh not found at %s: %v", repoRoot, err)
	}
	if err := CheckDualStackApplyPlan(repoRoot); err != nil {
		t.Fatalf("CheckDualStackApplyPlan(real repo) = %v, want nil", err)
	}
}

// --- D3 final simple sweep: publish-pr base-name refcheck (goblock) ---

// publishBaseRefcheckHappyFiles returns a publish_pr_main.go fixture whose
// ValidateBaseName body carries the !checkRefFormat(base) needle, so the single
// goblock invariant holds.
func publishBaseRefcheckHappyFiles() map[string]string {
	return map[string]string{
		publishPrMainRelPath: "package publishpr\n\n" +
			"func ValidateBaseName(base string, allowNonMainBase bool, checkRefFormat CheckRefFormatFunc) error {\n" +
			"\tif checkRefFormat != nil && !checkRefFormat(base) {\n" +
			"\t\treturn errInvalidBase\n" +
			"\t}\n" +
			"\treturn nil\n" +
			"}\n",
	}
}

func TestCheckPublishBaseRefcheck(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(files map[string]string)
		wantErr string
	}{
		{name: "happy path invariant holds", mutate: func(map[string]string) {}},
		{
			name: "checkRefFormat call missing from body",
			mutate: func(f map[string]string) {
				f[publishPrMainRelPath] = strings.Replace(f[publishPrMainRelPath], "!checkRefFormat(base)", "false", 1)
			},
			wantErr: "Expected publishpr.ValidateBaseName to validate the publish-pr --base branch name through checkRefFormat",
		},
		{
			name: "needle only in a comment inside the body",
			mutate: func(f map[string]string) {
				f[publishPrMainRelPath] = strings.Replace(f[publishPrMainRelPath], "\tif checkRefFormat != nil && !checkRefFormat(base) {", "\t// !checkRefFormat(base)\n\tif false {", 1)
			},
			wantErr: "Expected publishpr.ValidateBaseName to validate the publish-pr --base branch name through checkRefFormat",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files := publishBaseRefcheckHappyFiles()
			tt.mutate(files)
			root := writeFnBlockGoBlockGitEnvRepo(t, files)
			err := CheckPublishBaseRefcheck(root)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("CheckPublishBaseRefcheck() = %v, want nil", err)
				}
				return
			}
			if err == nil || err.Error() != tt.wantErr {
				t.Fatalf("CheckPublishBaseRefcheck() = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestCheckPublishBaseRefcheckCount(t *testing.T) {
	if got, want := len(publishBaseRefcheckChecks), 1; got != want {
		t.Fatalf("publishBaseRefcheckChecks has %d checks, want %d", got, want)
	}
}

func TestCheckPublishBaseRefcheckRealRepo(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	if _, err := os.Stat(filepath.Join(repoRoot, publishPrMainRelPath)); err != nil {
		t.Skipf("real internal/publishpr/publish_pr_main.go not found at %s: %v", repoRoot, err)
	}
	if err := CheckPublishBaseRefcheck(repoRoot); err != nil {
		t.Fatalf("CheckPublishBaseRefcheck(real repo) = %v, want nil", err)
	}
}

// --- D3 final simple sweep: validate_runtime_security_posture (fnblock) ---

// runtimeSecurityPostureHappyFiles returns a scripts/workcell fixture whose
// validate_runtime_security_posture body carries both go_hostutil helper needles,
// so the two fnblock invariants hold.
func runtimeSecurityPostureHappyFiles() map[string]string {
	return map[string]string{
		launcherRelPath: "#!/usr/bin/env bash\n" +
			"validate_runtime_security_posture() {\n" +
			"  go_hostutil helper validate-security-options \"$@\"\n" +
			"  go_hostutil helper validate-compat-security-options \"$@\"\n" +
			"}\n",
	}
}

func TestCheckRuntimeSecurityPosture(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(files map[string]string)
		wantErr string
	}{
		{name: "happy path all invariants hold", mutate: func(map[string]string) {}},
		{
			name: "security-options needle missing",
			mutate: func(f map[string]string) {
				f[launcherRelPath] = strings.Replace(f[launcherRelPath], "  go_hostutil helper validate-security-options \"$@\"\n", "", 1)
			},
			wantErr: "Expected validate_runtime_security_posture to validate daemon SecurityOptions through the helper subcommand",
		},
		{
			name: "compat-security-options needle missing",
			mutate: func(f map[string]string) {
				f[launcherRelPath] = strings.Replace(f[launcherRelPath], "  go_hostutil helper validate-compat-security-options \"$@\"\n", "", 1)
			},
			wantErr: "Expected validate_runtime_security_posture to validate Docker Desktop compat daemon SecurityOptions through the compat helper subcommand",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files := runtimeSecurityPostureHappyFiles()
			tt.mutate(files)
			root := writeFnBlockGoBlockGitEnvRepo(t, files)
			err := CheckRuntimeSecurityPosture(root)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("CheckRuntimeSecurityPosture() = %v, want nil", err)
				}
				return
			}
			if err == nil || err.Error() != tt.wantErr {
				t.Fatalf("CheckRuntimeSecurityPosture() = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestCheckRuntimeSecurityPostureCount(t *testing.T) {
	if got, want := len(runtimeSecurityPostureChecks), 2; got != want {
		t.Fatalf("runtimeSecurityPostureChecks has %d checks, want %d", got, want)
	}
}

func TestCheckRuntimeSecurityPostureRealRepo(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	if _, err := os.Stat(filepath.Join(repoRoot, launcherRelPath)); err != nil {
		t.Skipf("real scripts/workcell not found at %s: %v", repoRoot, err)
	}
	if err := CheckRuntimeSecurityPosture(repoRoot); err != nil {
		t.Fatalf("CheckRuntimeSecurityPosture(real repo) = %v, want nil", err)
	}
}

// --- D3 final simple sweep: container-smoke apt-broker slow-wait probe ---

// smokeAptBrokerProbeHappyFiles returns a container-smoke.sh fixture that carries
// all six apt-broker slow-wait probe strings, so all six invariants hold.
func smokeAptBrokerProbeHappyFiles() map[string]string {
	return map[string]string{
		containerSmokeRelPath: "#!/usr/bin/env bash\n" +
			"slow_apt_helper=/state/tmp/workcell-slow-apt-helper.sh\n" +
			"/bin/bash /usr/local/libexec/workcell/apt-broker.sh\n" +
			"sudo -n /usr/local/libexec/workcell/apt-helper.sh apt-get update\n" +
			"echo slow-apt-helper-ok\n" +
			"# expected sudo-wrapper to wait for a slow apt broker request by default\n" +
			"# expected default apt broker waits to avoid timing out slow requests\n",
	}
}

func TestCheckSmokeAptBrokerProbe(t *testing.T) {
	tests := []struct {
		name    string
		needle  string
		wantErr string
	}{
		{name: "happy path all invariants hold"},
		{
			name:    "slow-apt-helper path probe missing",
			needle:  "slow_apt_helper=/state/tmp/workcell-slow-apt-helper.sh",
			wantErr: "Expected scripts/container-smoke.sh to keep the Linux runtime apt-broker slow-wait probe (slow_apt_helper=/state/tmp/workcell-slow-apt-helper.sh)",
		},
		{
			name:    "apt-broker invocation probe missing",
			needle:  "/bin/bash /usr/local/libexec/workcell/apt-broker.sh",
			wantErr: "Expected scripts/container-smoke.sh to keep the Linux runtime apt-broker slow-wait probe (/bin/bash /usr/local/libexec/workcell/apt-broker.sh)",
		},
		{
			name:    "apt-helper update probe missing",
			needle:  "sudo -n /usr/local/libexec/workcell/apt-helper.sh apt-get update",
			wantErr: "Expected scripts/container-smoke.sh to keep the Linux runtime apt-broker slow-wait probe (sudo -n /usr/local/libexec/workcell/apt-helper.sh apt-get update)",
		},
		{
			name:    "slow-apt-helper-ok probe missing",
			needle:  "slow-apt-helper-ok",
			wantErr: "Expected scripts/container-smoke.sh to keep the Linux runtime apt-broker slow-wait probe (slow-apt-helper-ok)",
		},
		{
			name:    "sudo-wrapper wait probe missing",
			needle:  "expected sudo-wrapper to wait for a slow apt broker request by default",
			wantErr: "Expected scripts/container-smoke.sh to keep the Linux runtime apt-broker slow-wait probe (expected sudo-wrapper to wait for a slow apt broker request by default)",
		},
		{
			name:    "default broker wait probe missing",
			needle:  "expected default apt broker waits to avoid timing out slow requests",
			wantErr: "Expected scripts/container-smoke.sh to keep the Linux runtime apt-broker slow-wait probe (expected default apt broker waits to avoid timing out slow requests)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files := smokeAptBrokerProbeHappyFiles()
			if tt.needle != "" {
				files[containerSmokeRelPath] = strings.Replace(files[containerSmokeRelPath], tt.needle, "", 1)
			}
			root := writeFnBlockGoBlockGitEnvRepo(t, files)
			err := CheckSmokeAptBrokerProbe(root)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("CheckSmokeAptBrokerProbe() = %v, want nil", err)
				}
				return
			}
			if err == nil || err.Error() != tt.wantErr {
				t.Fatalf("CheckSmokeAptBrokerProbe() = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestCheckSmokeAptBrokerProbeCount(t *testing.T) {
	if got, want := len(smokeAptBrokerProbeChecks), 6; got != want {
		t.Fatalf("smokeAptBrokerProbeChecks has %d checks, want %d", got, want)
	}
}

func TestCheckSmokeAptBrokerProbeRealRepo(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	if _, err := os.Stat(filepath.Join(repoRoot, containerSmokeRelPath)); err != nil {
		t.Skipf("real scripts/container-smoke.sh not found at %s: %v", repoRoot, err)
	}
	if err := CheckSmokeAptBrokerProbe(repoRoot); err != nil {
		t.Fatalf("CheckSmokeAptBrokerProbe(real repo) = %v, want nil", err)
	}
}

// --- D3 final simple sweep: Copilot token-handoff cleanup (hoststate) ---

// copilotTokenHandoffCleanupHappyFiles returns a hoststate.go fixture carrying
// all three Copilot token-handoff cleanup identifiers, so all three invariants
// hold.
func copilotTokenHandoffCleanupHappyFiles() map[string]string {
	return map[string]string{
		hoststateRelPath: "package hoststate\n\n" +
			"const copilotStandaloneTokenHandoffName = \"copilot-standalone-token-handoff\"\n" +
			"const copilotTokenHandoffBundleName = \"copilot-token-handoff\"\n" +
			"func removeCopilotTokenHandoffArtifacts() error { return nil }\n",
	}
}

func TestCheckCopilotTokenHandoffCleanup(t *testing.T) {
	tests := []struct {
		name    string
		needle  string
		wantErr string
	}{
		{name: "happy path all invariants hold"},
		{name: "standalone handoff name missing", needle: "copilotStandaloneTokenHandoffName", wantErr: "Expected stale Copilot token handoff directories to be covered by host cleanup"},
		{name: "bundle name missing", needle: "copilotTokenHandoffBundleName", wantErr: "Expected stale Copilot token handoff directories to be covered by host cleanup"},
		{name: "removal helper missing", needle: "removeCopilotTokenHandoffArtifacts", wantErr: "Expected stale Copilot token handoff directories to be covered by host cleanup"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files := copilotTokenHandoffCleanupHappyFiles()
			if tt.needle != "" {
				files[hoststateRelPath] = strings.Replace(files[hoststateRelPath], tt.needle, "renamed", 1)
			}
			root := writeFnBlockGoBlockGitEnvRepo(t, files)
			err := CheckCopilotTokenHandoffCleanup(root)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("CheckCopilotTokenHandoffCleanup() = %v, want nil", err)
				}
				return
			}
			if err == nil || err.Error() != tt.wantErr {
				t.Fatalf("CheckCopilotTokenHandoffCleanup() = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestCheckCopilotTokenHandoffCleanupCount(t *testing.T) {
	if got, want := len(copilotTokenHandoffCleanupChecks), 3; got != want {
		t.Fatalf("copilotTokenHandoffCleanupChecks has %d checks, want %d", got, want)
	}
}

func TestCheckCopilotTokenHandoffCleanupRealRepo(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	if _, err := os.Stat(filepath.Join(repoRoot, hoststateRelPath)); err != nil {
		t.Skipf("real internal/host/hoststate/hoststate.go not found at %s: %v", repoRoot, err)
	}
	if err := CheckCopilotTokenHandoffCleanup(repoRoot); err != nil {
		t.Fatalf("CheckCopilotTokenHandoffCleanup(real repo) = %v, want nil", err)
	}
}

// --- D3 final simple sweep: provider-wrapper token unlink ---

// providerTokenUnlinkHappyFiles returns a provider-wrapper.sh fixture carrying
// the token-unlink needle, so the single invariant holds.
func providerTokenUnlinkHappyFiles() map[string]string {
	return map[string]string{
		providerWrapperRelPath: "#!/usr/bin/env bash\n" +
			"rm -f -- \"${token_file}\" || workcell_die \"could not remove\"\n",
	}
}

func TestCheckProviderTokenUnlink(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(files map[string]string)
		wantErr string
	}{
		{name: "happy path invariant holds", mutate: func(map[string]string) {}},
		{
			name: "token unlink needle missing",
			mutate: func(f map[string]string) {
				f[providerWrapperRelPath] = strings.Replace(f[providerWrapperRelPath], "rm -f -- \"${token_file}\"", "true", 1)
			},
			wantErr: "Expected provider wrapper to unlink the runtime Copilot token handoff file before managed exec",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files := providerTokenUnlinkHappyFiles()
			tt.mutate(files)
			root := writeFnBlockGoBlockGitEnvRepo(t, files)
			err := CheckProviderTokenUnlink(root)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("CheckProviderTokenUnlink() = %v, want nil", err)
				}
				return
			}
			if err == nil || err.Error() != tt.wantErr {
				t.Fatalf("CheckProviderTokenUnlink() = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestCheckProviderTokenUnlinkCount(t *testing.T) {
	if got, want := len(providerTokenUnlinkChecks), 1; got != want {
		t.Fatalf("providerTokenUnlinkChecks has %d checks, want %d", got, want)
	}
}

func TestCheckProviderTokenUnlinkRealRepo(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	if _, err := os.Stat(filepath.Join(repoRoot, providerWrapperRelPath)); err != nil {
		t.Skipf("real runtime/container/provider-wrapper.sh not found at %s: %v", repoRoot, err)
	}
	if err := CheckProviderTokenUnlink(repoRoot); err != nil {
		t.Fatalf("CheckProviderTokenUnlink(real repo) = %v, want nil", err)
	}
}

// --- D3 final simple sweep: validate-repo scenario-script references ---

// validateRepoScenarioRefsHappyFiles returns a validate-repo.sh fixture that
// references all three scenario scripts, so all three invariants hold.
func validateRepoScenarioRefsHappyFiles() map[string]string {
	return map[string]string{
		validateRepoRelPath: "#!/usr/bin/env bash\n" +
			"run-scenario-tests.sh\n" +
			"verify-scenario-coverage.sh\n" +
			"verify-control-plane-parity.sh\n",
	}
}

func TestCheckValidateRepoScenarioRefs(t *testing.T) {
	tests := []struct {
		name    string
		needle  string
		wantErr string
	}{
		{name: "happy path all invariants hold"},
		{name: "run-scenario-tests reference missing", needle: "run-scenario-tests.sh", wantErr: "validate-repo.sh must reference run-scenario-tests.sh"},
		{name: "verify-scenario-coverage reference missing", needle: "verify-scenario-coverage.sh", wantErr: "validate-repo.sh must reference verify-scenario-coverage.sh"},
		{name: "verify-control-plane-parity reference missing", needle: "verify-control-plane-parity.sh", wantErr: "validate-repo.sh must reference verify-control-plane-parity.sh"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files := validateRepoScenarioRefsHappyFiles()
			if tt.needle != "" {
				files[validateRepoRelPath] = strings.Replace(files[validateRepoRelPath], tt.needle, "", 1)
			}
			root := writeFnBlockGoBlockGitEnvRepo(t, files)
			err := CheckValidateRepoScenarioRefs(root)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("CheckValidateRepoScenarioRefs() = %v, want nil", err)
				}
				return
			}
			if err == nil || err.Error() != tt.wantErr {
				t.Fatalf("CheckValidateRepoScenarioRefs() = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestCheckValidateRepoScenarioRefsCount(t *testing.T) {
	if got, want := len(validateRepoScenarioRefsChecks), 3; got != want {
		t.Fatalf("validateRepoScenarioRefsChecks has %d checks, want %d", got, want)
	}
}

func TestCheckValidateRepoScenarioRefsRealRepo(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	if _, err := os.Stat(filepath.Join(repoRoot, validateRepoRelPath)); err != nil {
		t.Skipf("real scripts/validate-repo.sh not found at %s: %v", repoRoot, err)
	}
	if err := CheckValidateRepoScenarioRefs(repoRoot); err != nil {
		t.Fatalf("CheckValidateRepoScenarioRefs(real repo) = %v, want nil", err)
	}
}

// --- D3 dir/exec/clear_rules sweep: new-kind direct tests ---

// TestKindFunctionBlockRegexAbsent exercises kindFunctionBlockRegexAbsent's
// holds() directly: a regex match on any line of the named function block is a
// violation (holds=false); a pattern absent from the block, or present only in a
// DIFFERENT function, holds (true).
func TestKindFunctionBlockRegexAbsent(t *testing.T) {
	c := check{
		kind:         kindFunctionBlockRegexAbsent,
		functionName: "render_allowlist_apply_plan",
		regex:        `^[[:space:]]*clear_rules$`,
	}
	inBlock := "render_allowlist_apply_plan() {\n  clear_rules\n}\n"
	if c.holds(inBlock, "") {
		t.Fatalf("holds()=true for pattern present in block, want false (violation)")
	}
	absent := "render_allowlist_apply_plan() {\n  render_clear_plan\n}\n"
	if !c.holds(absent, "") {
		t.Fatalf("holds()=false for pattern absent from block, want true")
	}
	otherFunc := "render_allowlist_apply_plan() {\n  render_clear_plan\n}\n" +
		"other_func() {\n  clear_rules\n}\n"
	if !c.holds(otherFunc, "") {
		t.Fatalf("holds()=false for pattern present only in a different function, want true")
	}
}

// TestKindDirExists exercises kindDirExists's holds() directly: an existing
// directory holds (true); a missing path or a path that is a regular file (not a
// directory) is a violation (holds=false).
func TestKindDirExists(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "present"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "afile"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cases := []struct {
		name string
		path string
		want bool
	}{
		{"directory present", "present", true},
		{"path missing", "missing", false},
		{"path is a file not a dir", "afile", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := check{kind: kindDirExists, targetPath: tc.path}
			if got := c.holds("", root); got != tc.want {
				t.Fatalf("holds()=%v, want %v", got, tc.want)
			}
		})
	}
}

// TestKindExecutable exercises kindExecutable's holds() directly: an existing
// file with an execute bit holds (true); a file with no execute bit, or a
// missing path, is a violation (holds=false).
func TestKindExecutable(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "exec"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write exec: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "plain"), []byte("data\n"), 0o644); err != nil {
		t.Fatalf("write plain: %v", err)
	}
	cases := []struct {
		name string
		path string
		want bool
	}{
		{"executable file", "exec", true},
		{"non-executable file", "plain", false},
		{"path missing", "missing", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := check{kind: kindExecutable, targetPath: tc.path}
			if got := c.holds("", root); got != tc.want {
				t.Fatalf("holds()=%v, want %v", got, tc.want)
			}
		})
	}
}

// --- D3 dir/exec sweep: pre-commit hook executable subcommand ---

func TestCheckPrecommitHookExec(t *testing.T) {
	hookRel := ".githooks/pre-commit"
	tests := []struct {
		name    string
		mode    os.FileMode
		write   bool
		wantErr bool
	}{
		{name: "executable hook holds", mode: 0o755, write: true},
		{name: "non-executable hook fails", mode: 0o644, write: true, wantErr: true},
		{name: "missing hook fails", write: false, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			if tt.write {
				path := filepath.Join(root, hookRel)
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
				if err := os.WriteFile(path, []byte("#!/bin/bash\n"), tt.mode); err != nil {
					t.Fatalf("write: %v", err)
				}
			}
			err := CheckPrecommitHookExec(root)
			if !tt.wantErr {
				if err != nil {
					t.Fatalf("CheckPrecommitHookExec() = %v, want nil", err)
				}
				return
			}
			wantMsg := "Expected executable repo pre-commit hook: " + root + "/" + hookRel
			if err == nil || err.Error() != wantMsg {
				t.Fatalf("CheckPrecommitHookExec() = %v, want %q", err, wantMsg)
			}
		})
	}
}

func TestCheckPrecommitHookExecCount(t *testing.T) {
	if got, want := len(precommitHookExecChecks), 1; got != want {
		t.Fatalf("precommitHookExecChecks has %d checks, want %d", got, want)
	}
}

func TestCheckPrecommitHookExecRealRepo(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	if _, err := os.Stat(filepath.Join(repoRoot, ".githooks/pre-commit")); err != nil {
		t.Skipf("real .githooks/pre-commit not found at %s: %v", repoRoot, err)
	}
	if err := CheckPrecommitHookExec(repoRoot); err != nil {
		t.Fatalf("CheckPrecommitHookExec(real repo) = %v, want nil", err)
	}
}

// --- D3 dir sweep: docs/examples directory subcommand ---

func TestCheckDocsExamplesDir(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(root string) error
		wantErr bool
	}{
		{
			name:  "directory present holds",
			setup: func(root string) error { return os.MkdirAll(filepath.Join(root, "docs", "examples"), 0o755) },
		},
		{
			name:    "directory missing fails",
			setup:   func(string) error { return nil },
			wantErr: true,
		},
		{
			name: "path is a file not a dir fails",
			setup: func(root string) error {
				if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
					return err
				}
				return os.WriteFile(filepath.Join(root, "docs", "examples"), []byte("x"), 0o644)
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			if err := tt.setup(root); err != nil {
				t.Fatalf("setup: %v", err)
			}
			err := CheckDocsExamplesDir(root)
			if !tt.wantErr {
				if err != nil {
					t.Fatalf("CheckDocsExamplesDir() = %v, want nil", err)
				}
				return
			}
			if err == nil || err.Error() != "docs/examples/ must exist" {
				t.Fatalf("CheckDocsExamplesDir() = %v, want %q", err, "docs/examples/ must exist")
			}
		})
	}
}

func TestCheckDocsExamplesDirCount(t *testing.T) {
	if got, want := len(docsExamplesDirChecks), 1; got != want {
		t.Fatalf("docsExamplesDirChecks has %d checks, want %d", got, want)
	}
}

func TestCheckDocsExamplesDirRealRepo(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	if _, err := os.Stat(filepath.Join(repoRoot, "docs/examples")); err != nil {
		t.Skipf("real docs/examples not found at %s: %v", repoRoot, err)
	}
	if err := CheckDocsExamplesDir(repoRoot); err != nil {
		t.Fatalf("CheckDocsExamplesDir(real repo) = %v, want nil", err)
	}
}

// --- D3 file sweep: scenario-scripts-present subcommand ---

// TestKindFileExists exercises kindFileExists's holds() directly: an existing
// regular file holds (true); a missing path or a directory (not a regular file)
// is a violation (holds=false), mirroring Bash `[[ -f ]]`.
func TestKindFileExists(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "regular"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "adir"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	cases := []struct {
		name string
		path string
		want bool
	}{
		{"regular file present", "regular", true},
		{"path missing", "missing", false},
		{"path is a directory not a regular file", "adir", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := check{kind: kindFileExists, targetPath: tc.path}
			if got := c.holds("", root); got != tc.want {
				t.Fatalf("holds()=%v, want %v", got, tc.want)
			}
		})
	}
}

// scenarioScriptsRelPaths are the four repo-relative paths the group checks, in
// the shell's original order (manifest first, then the three scenario scripts).
var scenarioScriptsRelPaths = []string{
	"tests/scenarios/manifest.json",
	"scripts/run-scenario-tests.sh",
	"scripts/verify-scenario-coverage.sh",
	"scripts/verify-control-plane-parity.sh",
}

// writeScenarioHarness populates root with a valid scenario harness: a regular
// manifest.json plus the three executable scenario scripts.
func writeScenarioHarness(t *testing.T, root string) {
	t.Helper()
	manifest := filepath.Join(root, "tests/scenarios/manifest.json")
	if err := os.MkdirAll(filepath.Dir(manifest), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(manifest, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "scripts"), 0o755); err != nil {
		t.Fatalf("mkdir scripts: %v", err)
	}
	for _, rel := range scenarioScriptsRelPaths[1:] {
		if err := os.WriteFile(filepath.Join(root, rel), []byte("#!/bin/bash\n"), 0o755); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
}

func TestCheckScenarioScriptsPresent(t *testing.T) {
	t.Run("full harness holds", func(t *testing.T) {
		root := t.TempDir()
		writeScenarioHarness(t, root)
		if err := CheckScenarioScriptsPresent(root); err != nil {
			t.Fatalf("CheckScenarioScriptsPresent() = %v, want nil", err)
		}
	})
	t.Run("missing manifest fails with static message", func(t *testing.T) {
		root := t.TempDir()
		writeScenarioHarness(t, root)
		if err := os.Remove(filepath.Join(root, "tests/scenarios/manifest.json")); err != nil {
			t.Fatalf("remove: %v", err)
		}
		err := CheckScenarioScriptsPresent(root)
		if err == nil || err.Error() != "tests/scenarios/manifest.json must exist" {
			t.Fatalf("CheckScenarioScriptsPresent() = %v, want %q", err, "tests/scenarios/manifest.json must exist")
		}
	})
	t.Run("manifest that is a directory fails", func(t *testing.T) {
		root := t.TempDir()
		writeScenarioHarness(t, root)
		manifest := filepath.Join(root, "tests/scenarios/manifest.json")
		if err := os.Remove(manifest); err != nil {
			t.Fatalf("remove: %v", err)
		}
		if err := os.MkdirAll(manifest, 0o755); err != nil {
			t.Fatalf("mkdir manifest dir: %v", err)
		}
		err := CheckScenarioScriptsPresent(root)
		if err == nil || err.Error() != "tests/scenarios/manifest.json must exist" {
			t.Fatalf("CheckScenarioScriptsPresent() = %v, want %q", err, "tests/scenarios/manifest.json must exist")
		}
	})
	// Each scenario script, when non-executable or missing, must fail with the
	// prefix plus the ABSOLUTE ${ROOT_DIR}/<path> script path, byte-for-byte.
	for _, rel := range scenarioScriptsRelPaths[1:] {
		rel := rel
		t.Run("non-executable "+rel+" fails with absolute path", func(t *testing.T) {
			root := t.TempDir()
			writeScenarioHarness(t, root)
			if err := os.Chmod(filepath.Join(root, rel), 0o644); err != nil {
				t.Fatalf("chmod: %v", err)
			}
			wantMsg := "Expected executable scenario script: " + root + "/" + rel
			err := CheckScenarioScriptsPresent(root)
			if err == nil || err.Error() != wantMsg {
				t.Fatalf("CheckScenarioScriptsPresent() = %v, want %q", err, wantMsg)
			}
		})
		t.Run("missing "+rel+" fails with absolute path", func(t *testing.T) {
			root := t.TempDir()
			writeScenarioHarness(t, root)
			if err := os.Remove(filepath.Join(root, rel)); err != nil {
				t.Fatalf("remove: %v", err)
			}
			wantMsg := "Expected executable scenario script: " + root + "/" + rel
			err := CheckScenarioScriptsPresent(root)
			if err == nil || err.Error() != wantMsg {
				t.Fatalf("CheckScenarioScriptsPresent() = %v, want %q", err, wantMsg)
			}
		})
	}
}

func TestCheckScenarioScriptsPresentCount(t *testing.T) {
	if got, want := len(scenarioScriptsPresentChecks), 4; got != want {
		t.Fatalf("scenarioScriptsPresentChecks has %d checks, want %d", got, want)
	}
}

func TestCheckScenarioScriptsPresentRealRepo(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	for _, rel := range scenarioScriptsRelPaths {
		if _, err := os.Stat(filepath.Join(repoRoot, rel)); err != nil {
			t.Skipf("real %s not found at %s: %v", rel, repoRoot, err)
		}
	}
	if err := CheckScenarioScriptsPresent(repoRoot); err != nil {
		t.Fatalf("CheckScenarioScriptsPresent(real repo) = %v, want nil", err)
	}
}

// --- kindJSONExprEval + jq -e adapter-settings migration (D3) ---

// jsonExprCheck builds a bare kindJSONExprEval check for the holds() unit tests.
func jsonExprCheck(path, expectedRaw string) check {
	return check{kind: kindJSONExprEval, jsonPath: path, jsonExpectedRaw: expectedRaw}
}

// TestJSONExprEvalHolds exercises kindJSONExprEval's jq-typed `==` semantics
// directly: string equality, boolean equality that must reject the string
// "false", numeric equality, nested paths, missing-key/explicit-null rendering
// as jq null (never equal to a scalar), and invalid-JSON / non-object-index
// error cases that mirror jq's non-zero exit.
func TestJSONExprEvalHolds(t *testing.T) {
	const doc = `{
		"str": "allow",
		"boolFalse": false,
		"boolTrue": true,
		"num": 5,
		"strFalse": "false",
		"nullField": null,
		"nested": {"deep": {"enabled": false}},
		"scalar": "x"
	}`
	tests := []struct {
		name     string
		path     string
		expected string
		want     bool
	}{
		{"string match", ".str", `"allow"`, true},
		{"string mismatch", ".str", `"deny"`, false},
		{"bool false match", ".boolFalse", "false", true},
		{"bool false does not match string false", ".strFalse", "false", false},
		{"string false vs string false matches", ".strFalse", `"false"`, true},
		{"bool true match", ".boolTrue", "true", true},
		{"bool true mismatch against false", ".boolTrue", "false", false},
		{"number match", ".num", "5", true},
		{"number match float form", ".num", "5.0", true},
		{"number mismatch", ".num", "6", false},
		{"nested path match", ".nested.deep.enabled", "false", true},
		{"missing key renders null not equal to false", ".missing", "false", false},
		{"missing key not equal to null literal string", ".missing", `"null"`, false},
		{"explicit null not equal to false", ".nullField", "false", false},
		{"missing nested under missing parent", ".missing.child", "false", false},
		{"indexing a scalar with a key is a jq error", ".scalar.child", `"x"`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := jsonExprCheck(tt.path, tt.expected).holds(doc, "")
			if got != tt.want {
				t.Fatalf("holds(%s == %s) = %v, want %v", tt.path, tt.expected, got, tt.want)
			}
		})
	}
}

// TestJSONExprEvalInvalidJSON asserts an unparseable target file is a violation,
// mirroring jq's error exit under the `if !` guard.
func TestJSONExprEvalInvalidJSON(t *testing.T) {
	if jsonExprCheck(".str", `"allow"`).holds("{not valid json", "") {
		t.Fatal("holds() on invalid JSON = true, want false (jq error → violation)")
	}
	if jsonExprCheck(".str", `"allow"`).holds("", "") {
		t.Fatal("holds() on empty file = true, want false (jq error → violation)")
	}
}

// TestJSONExprEvalDifferentialJQ pins kindJSONExprEval's holds() to the real
// `jq -e 'EXPR' FILE` exit code across a matrix of documents and expressions, so
// any drift from jq's typed `==` fails here.  Skipped if jq is unavailable.
func TestJSONExprEvalDifferentialJQ(t *testing.T) {
	jqPath, err := exec.LookPath("jq")
	if err != nil {
		t.Skip("jq not installed; skipping differential parity test")
	}
	docs := []string{
		`{"x":"allow"}`,
		`{"x":false}`,
		`{"x":"false"}`,
		`{"x":true}`,
		`{"x":5}`,
		`{"x":null}`,
		`{"y":1}`,
		`{"a":{"b":false}}`,
		// Array-index navigation (`[0]`) and empty-array RHS (`== []`) fixtures.
		`{"a":[{"b":"x"}]}`,
		`{"a":[{"b":"y"}]}`,
		`{"a":[]}`,
		`{"a":{}}`,
		`{"a":"scalar"}`,
		`{"x":[]}`,
		`{"x":[1]}`,
		`{"hooks":{"PreToolUse":[{"hooks":[{"command":"/opt/workcell/adapters/claude/hooks/guard-bash.sh"}]}]}}`,
	}
	exprs := []struct{ path, expectedRaw string }{
		{".x", `"allow"`},
		{".x", "false"},
		{".x", "true"},
		{".x", "5"},
		{".a.b", "false"},
		{".missing", "false"},
		// Array-index navigation and empty-array literal exprs.
		{".a[0].b", `"x"`},
		{".a[0]", `"x"`},
		{".a[1]", `"x"`},
		{".x", "[]"},
		{".tools.allowed", "[]"},
		{".hooks.PreToolUse[0].hooks[0].command", `"/opt/workcell/adapters/claude/hooks/guard-bash.sh"`},
	}
	for _, doc := range docs {
		for _, e := range exprs {
			name := doc + " | " + e.path + "==" + e.expectedRaw
			t.Run(name, func(t *testing.T) {
				f := filepath.Join(t.TempDir(), "d.json")
				if err := os.WriteFile(f, []byte(doc), 0o644); err != nil {
					t.Fatalf("write fixture: %v", err)
				}
				cmd := exec.Command(jqPath, "-e", e.path+" == "+e.expectedRaw, f)
				runErr := cmd.Run()
				jqTruthy := runErr == nil // jq -e exit 0 ⇔ expression true
				got := jsonExprCheck(e.path, e.expectedRaw).holds(doc, "")
				if got != jqTruthy {
					t.Fatalf("holds()=%v but jq -e truthy=%v for %s", got, jqTruthy, name)
				}
			})
		}
	}
}

// TestJSONPathTruthyHolds exercises kindJSONPathTruthy directly for both
// polarities: a bare-path guard holds (default polarity) iff the value is neither
// null nor false, and holds (inverted polarity) iff it is null/false/missing or a
// navigation/parse error.
func TestJSONPathTruthyHolds(t *testing.T) {
	const path = ".s.a.t"
	tests := []struct {
		name        string
		doc         string
		wantTruthy  bool // holds under default polarity (`if ! jq -e`)
		wantViolate bool // holds under inverted polarity (`if jq -e`)
	}{
		{"string truthy", `{"s":{"a":{"t":"oauth"}}}`, true, false},
		{"explicit null", `{"s":{"a":{"t":null}}}`, false, true},
		{"missing leaf", `{"s":{"a":{}}}`, false, true},
		{"missing root", `{}`, false, true},
		{"boolean false", `{"s":{"a":{"t":false}}}`, false, true},
		{"boolean true", `{"s":{"a":{"t":true}}}`, true, false},
		{"number zero is truthy", `{"s":{"a":{"t":0}}}`, true, false},
		{"empty array is truthy", `{"s":{"a":{"t":[]}}}`, true, false},
		{"navigation error passes inverted", `{"s":"scalar"}`, false, true},
		{"invalid json passes inverted", `{not json`, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := (check{kind: kindJSONPathTruthy, jsonPath: path}).holds(tt.doc, ""); got != tt.wantTruthy {
				t.Fatalf("default polarity holds = %v, want %v", got, tt.wantTruthy)
			}
			if got := (check{kind: kindJSONPathTruthy, jsonPath: path, jsonViolateWhenTruthy: true}).holds(tt.doc, ""); got != tt.wantViolate {
				t.Fatalf("inverted polarity holds = %v, want %v", got, tt.wantViolate)
			}
		})
	}
}

// TestJSONPathTruthyDifferentialJQ pins kindJSONPathTruthy's holds() (both
// polarities) to the real `jq -e '<path>' FILE` exit code: default polarity must
// equal jq-truthy, inverted polarity must equal NOT jq-truthy (so a falsy value
// AND a jq error both pass, exactly as `if jq -e` does).  Skipped if jq is
// unavailable.
func TestJSONPathTruthyDifferentialJQ(t *testing.T) {
	jqPath, err := exec.LookPath("jq")
	if err != nil {
		t.Skip("jq not installed; skipping differential parity test")
	}
	const path = ".s.a.t"
	docs := []string{
		`{"s":{"a":{"t":"oauth"}}}`,
		`{"s":{"a":{"t":null}}}`,
		`{"s":{"a":{}}}`,
		`{}`,
		`{"s":{"a":{"t":false}}}`,
		`{"s":{"a":{"t":true}}}`,
		`{"s":{"a":{"t":0}}}`,
		`{"s":{"a":{"t":[]}}}`,
		`{"s":"scalar"}`,
	}
	for _, doc := range docs {
		t.Run(doc, func(t *testing.T) {
			f := filepath.Join(t.TempDir(), "d.json")
			if err := os.WriteFile(f, []byte(doc), 0o644); err != nil {
				t.Fatalf("write fixture: %v", err)
			}
			jqTruthy := exec.Command(jqPath, "-e", path, f).Run() == nil
			if got := (check{kind: kindJSONPathTruthy, jsonPath: path}).holds(doc, ""); got != jqTruthy {
				t.Fatalf("default polarity holds=%v but jq -e truthy=%v for %s", got, jqTruthy, doc)
			}
			if got := (check{kind: kindJSONPathTruthy, jsonPath: path, jsonViolateWhenTruthy: true}).holds(doc, ""); got != !jqTruthy {
				t.Fatalf("inverted polarity holds=%v but !(jq -e truthy)=%v for %s", got, !jqTruthy, doc)
			}
		})
	}
}

// TestJSONTypeEqualsDifferentialJQ pins kindJSONTypeEquals's holds() to the real
// `jq -e '<path> | type == "<T>"' FILE` exit code across every jq type name and a
// matrix of leaf kinds (including a navigation error), so any drift from jq's
// `type` builtin fails here.  Skipped if jq is unavailable.
func TestJSONTypeEqualsDifferentialJQ(t *testing.T) {
	jqPath, err := exec.LookPath("jq")
	if err != nil {
		t.Skip("jq not installed; skipping differential parity test")
	}
	const path = ".adv.ex"
	docs := []string{
		`{"adv":{"ex":[]}}`,
		`{"adv":{"ex":["A"]}}`,
		`{"adv":{"ex":{}}}`,
		`{"adv":{}}`,
		`{}`,
		`{"adv":{"ex":null}}`,
		`{"adv":{"ex":"str"}}`,
		`{"adv":{"ex":5}}`,
		`{"adv":{"ex":true}}`,
		`{"adv":"scalar"}`,
	}
	types := []string{"array", "object", "string", "number", "boolean", "null"}
	for _, doc := range docs {
		for _, typ := range types {
			t.Run(doc+"|"+typ, func(t *testing.T) {
				f := filepath.Join(t.TempDir(), "d.json")
				if err := os.WriteFile(f, []byte(doc), 0o644); err != nil {
					t.Fatalf("write fixture: %v", err)
				}
				expr := path + ` | type == "` + typ + `"`
				jqTruthy := exec.Command(jqPath, "-e", expr, f).Run() == nil
				c := check{kind: kindJSONTypeEquals, jsonPath: path, jsonExpectedRaw: `"` + typ + `"`}
				if got := c.holds(doc, ""); got != jqTruthy {
					t.Fatalf("holds()=%v but jq -e truthy=%v for %s | type==%q", got, jqTruthy, doc, typ)
				}
			})
		}
	}
}

// TestJSONTypeEqualsInvalidJSON asserts an unparseable target file is a violation
// (holds false), mirroring jq's error exit under the `if !` guard.
func TestJSONTypeEqualsInvalidJSON(t *testing.T) {
	c := check{kind: kindJSONTypeEquals, jsonPath: ".adv.ex", jsonExpectedRaw: `"array"`}
	if c.holds("{not valid json", "") {
		t.Fatal("holds() on invalid JSON = true, want false")
	}
}

// TestCheckClaudeGuardBashHook exercises the per-file guard-bash-hook check,
// including the array-index navigation and the byte-exact basename-prefixed
// violation message.
func TestCheckClaudeGuardBashHook(t *testing.T) {
	const good = `{"hooks":{"PreToolUse":[{"hooks":[{"command":"/opt/workcell/adapters/claude/hooks/guard-bash.sh"}]}]}}`
	tests := []struct {
		name     string
		body     string
		fileName string
		wantErr  string
	}{
		{"happy managed", good, "managed-settings.json", ""},
		{"happy user settings", good, "settings.json", ""},
		{"wrong command", `{"hooks":{"PreToolUse":[{"hooks":[{"command":"/bin/sh"}]}]}}`, "settings.json", "settings.json settings must use the managed guard-bash.sh hook"},
		{"missing hooks", `{}`, "managed-settings.json", "managed-settings.json settings must use the managed guard-bash.sh hook"},
		{"empty PreToolUse array", `{"hooks":{"PreToolUse":[]}}`, "settings.json", "settings.json settings must use the managed guard-bash.sh hook"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, tt.fileName)
			if err := os.WriteFile(path, []byte(tt.body), 0o644); err != nil {
				t.Fatalf("write %s: %v", path, err)
			}
			assertCheckErr(t, "CheckClaudeGuardBashHook", CheckClaudeGuardBashHook(path), tt.wantErr)
		})
	}
}

// TestCheckClaudeGuardBashHookMissingFile asserts a missing file is a violation
// with the basename-prefixed message (jq errors on a missing file → non-zero exit
// → the `if !` guard fires).
func TestCheckClaudeGuardBashHookMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	assertCheckErr(t, "CheckClaudeGuardBashHook", CheckClaudeGuardBashHook(path),
		"settings.json settings must use the managed guard-bash.sh hook")
}

const happyGeminiBaseline = `{"tools":{"allowed":[]},"mcp":{"allowed":[]},"security":{"auth":{}}}`

func TestCheckGeminiSettingsBaseline(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr string
	}{
		{"happy path", happyGeminiBaseline, ""},
		{"selected auth type present rejected", `{"tools":{"allowed":[]},"mcp":{"allowed":[]},"security":{"auth":{"selectedType":"oauth"}}}`, "Gemini adapter baseline must not hardcode a selected auth type"},
		{"selected auth type false passes", `{"tools":{"allowed":[]},"mcp":{"allowed":[]},"security":{"auth":{"selectedType":false}}}`, ""},
		{"tools allowed non-empty", `{"tools":{"allowed":["Bash"]},"mcp":{"allowed":[]},"security":{"auth":{}}}`, "Gemini adapter must not seed allowed tools"},
		{"tools allowed missing renders null", `{"mcp":{"allowed":[]},"security":{"auth":{}}}`, "Gemini adapter must not seed allowed tools"},
		{"mcp allowed non-empty", `{"tools":{"allowed":[]},"mcp":{"allowed":["srv"]},"security":{"auth":{}}}`, "Gemini adapter must not seed allowed MCP servers"},
		{"tools allowed object not array", `{"tools":{"allowed":{}},"mcp":{"allowed":[]},"security":{"auth":{}}}`, "Gemini adapter must not seed allowed tools"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := writeGeminiRepo(t, tt.body)
			assertCheckErr(t, "CheckGeminiSettingsBaseline", CheckGeminiSettingsBaseline(root), tt.wantErr)
		})
	}
}

// TestCheckGeminiSettingsGuardsExcludedEnvVars covers the migrated
// `| type == "array"` guard now folded into the guards group.
func TestCheckGeminiSettingsGuardsExcludedEnvVars(t *testing.T) {
	base := `{"security":{"folderTrust":{"enabled":false}},"tools":{"shell":{"enableInteractiveShell":false}}`
	tests := []struct {
		name    string
		body    string
		wantErr string
	}{
		{"array holds", base + `,"advanced":{"excludedEnvVars":["A"]}}`, ""},
		{"empty array holds", base + `,"advanced":{"excludedEnvVars":[]}}`, ""},
		{"object rejected", base + `,"advanced":{"excludedEnvVars":{}}}`, "Gemini adapter must exclude sensitive environment variables"},
		{"missing rejected", base + `}`, "Gemini adapter must exclude sensitive environment variables"},
		{"null rejected", base + `,"advanced":{"excludedEnvVars":null}}`, "Gemini adapter must exclude sensitive environment variables"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := writeGeminiRepo(t, tt.body)
			assertCheckErr(t, "CheckGeminiSettingsGuards", CheckGeminiSettingsGuards(root), tt.wantErr)
		})
	}
}

// writeGeminiRepo writes a rootDir seeded only with the Gemini adapter settings
// file (the sole file the baseline/guards groups read for these tests).
func writeGeminiRepo(t *testing.T, gemini string) string {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, geminiSettingsRelPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(gemini), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return root
}

// writeAdapterSettingsRepo writes a rootDir seeded with the two adapter settings
// files the migrated jq -e groups read, from the provided JSON bodies.
func writeAdapterSettingsRepo(t *testing.T, claudeManaged, gemini string) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		claudeManagedSettingsRelPath: claudeManaged,
		geminiSettingsRelPath:        gemini,
	}
	for rel, body := range files {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	return root
}

const happyClaudeManaged = `{"disableBypassPermissionsMode":"allow"}`
const happyGeminiSettings = `{"security":{"folderTrust":{"enabled":false}},"tools":{"shell":{"enableInteractiveShell":false}},"advanced":{"excludedEnvVars":["AWS_SECRET_ACCESS_KEY"]}}`

func TestCheckClaudeManagedBypass(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr string
	}{
		{"happy path", happyClaudeManaged, ""},
		{"wrong string value", `{"disableBypassPermissionsMode":"deny"}`, "Claude managed settings must allow bypass-permissions mode under the external Workcell boundary"},
		{"missing field", `{}`, "Claude managed settings must allow bypass-permissions mode under the external Workcell boundary"},
		{"boolean not string", `{"disableBypassPermissionsMode":true}`, "Claude managed settings must allow bypass-permissions mode under the external Workcell boundary"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := writeAdapterSettingsRepo(t, tt.body, happyGeminiSettings)
			assertCheckErr(t, "CheckClaudeManagedBypass", CheckClaudeManagedBypass(root), tt.wantErr)
		})
	}
}

func TestCheckGeminiSettingsGuards(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr string
	}{
		{"happy path", happyGeminiSettings, ""},
		{"folder trust enabled", `{"security":{"folderTrust":{"enabled":true}},"tools":{"shell":{"enableInteractiveShell":false}}}`, "Gemini adapter must disable Gemini folder trust inside the managed runtime"},
		{"folder trust string false rejected", `{"security":{"folderTrust":{"enabled":"false"}},"tools":{"shell":{"enableInteractiveShell":false}}}`, "Gemini adapter must disable Gemini folder trust inside the managed runtime"},
		{"folder trust missing", `{"tools":{"shell":{"enableInteractiveShell":false}}}`, "Gemini adapter must disable Gemini folder trust inside the managed runtime"},
		{"interactive shell enabled", `{"security":{"folderTrust":{"enabled":false}},"tools":{"shell":{"enableInteractiveShell":true}}}`, "Gemini adapter must disable interactive shell mode"},
		{"interactive shell missing", `{"security":{"folderTrust":{"enabled":false}},"tools":{"shell":{}}}`, "Gemini adapter must disable interactive shell mode"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := writeAdapterSettingsRepo(t, happyClaudeManaged, tt.body)
			assertCheckErr(t, "CheckGeminiSettingsGuards", CheckGeminiSettingsGuards(root), tt.wantErr)
		})
	}
}

func TestCheckClaudeMcpProjectServers(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		fileName string
		wantErr  string
	}{
		{"happy managed", `{"enableAllProjectMcpServers":false}`, "managed-settings.json", ""},
		{"happy user settings", `{"enableAllProjectMcpServers":false}`, "settings.json", ""},
		{"true is a violation", `{"enableAllProjectMcpServers":true}`, "settings.json", "settings.json settings must disable auto-enabled project MCP servers"},
		{"missing field", `{}`, "managed-settings.json", "managed-settings.json settings must disable auto-enabled project MCP servers"},
		{"basename preserved for nested path", `{"enableAllProjectMcpServers":true}`, "managed-settings.json", "managed-settings.json settings must disable auto-enabled project MCP servers"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, tt.fileName)
			if err := os.WriteFile(path, []byte(tt.body), 0o644); err != nil {
				t.Fatalf("write %s: %v", path, err)
			}
			assertCheckErr(t, "CheckClaudeMcpProjectServers", CheckClaudeMcpProjectServers(path), tt.wantErr)
		})
	}
}

// TestCheckClaudeMcpProjectServersMissingFile asserts a missing file is a
// violation with the basename-prefixed message (jq errors on a missing file →
// non-zero exit → the `if !` guard fires).
func TestCheckClaudeMcpProjectServersMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	assertCheckErr(t, "CheckClaudeMcpProjectServers", CheckClaudeMcpProjectServers(path),
		"settings.json settings must disable auto-enabled project MCP servers")
}

// TestAdapterSettingsChecksCount guards the migrated check counts so an
// accidental add/drop is caught.
func TestAdapterSettingsChecksCount(t *testing.T) {
	if got := len(claudeManagedBypassChecks); got != 1 {
		t.Fatalf("claudeManagedBypassChecks has %d checks, want 1", got)
	}
	if got := len(geminiSettingsBaselineChecks); got != 3 {
		t.Fatalf("geminiSettingsBaselineChecks has %d checks, want 3", got)
	}
	if got := len(geminiSettingsGuardChecks); got != 3 {
		t.Fatalf("geminiSettingsGuardChecks has %d checks, want 3", got)
	}
}

// TestAdapterSettingsChecksRealRepo asserts the real adapter settings files in
// this repository satisfy all migrated jq -e invariants — the guard against a
// mis-transcribed path, message, or RHS literal.
func TestAdapterSettingsChecksRealRepo(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	if _, err := os.Stat(filepath.Join(repoRoot, claudeManagedSettingsRelPath)); err != nil {
		t.Skipf("real %s not found: %v", claudeManagedSettingsRelPath, err)
	}
	if err := CheckClaudeManagedBypass(repoRoot); err != nil {
		t.Fatalf("CheckClaudeManagedBypass(real repo) = %v, want nil", err)
	}
	if err := CheckGeminiSettingsBaseline(repoRoot); err != nil {
		t.Fatalf("CheckGeminiSettingsBaseline(real repo) = %v, want nil", err)
	}
	if err := CheckGeminiSettingsGuards(repoRoot); err != nil {
		t.Fatalf("CheckGeminiSettingsGuards(real repo) = %v, want nil", err)
	}
	for _, rel := range []string{"adapters/claude/.claude/settings.json", claudeManagedSettingsRelPath} {
		if err := CheckClaudeMcpProjectServers(filepath.Join(repoRoot, rel)); err != nil {
			t.Fatalf("CheckClaudeMcpProjectServers(real %s) = %v, want nil", rel, err)
		}
		if err := CheckClaudeGuardBashHook(filepath.Join(repoRoot, rel)); err != nil {
			t.Fatalf("CheckClaudeGuardBashHook(real %s) = %v, want nil", rel, err)
		}
	}
}

// assertCheckErr fails unless got matches the wantErr expectation ("" = nil).
func assertCheckErr(t *testing.T, label string, got error, wantErr string) {
	t.Helper()
	if wantErr == "" {
		if got != nil {
			t.Fatalf("%s = %v, want nil", label, got)
		}
		return
	}
	if got == nil {
		t.Fatalf("%s = nil, want error %q", label, wantErr)
	}
	if got.Error() != wantErr {
		t.Fatalf("%s = %q, want %q", label, got.Error(), wantErr)
	}
}

// hostGateSanitizeHappyBody is a minimal host-gate script body that satisfies
// both per-script invariants: an absolute privileged Bash shebang on line 1 and
// the entrypoint self-sanitize sentinel on a later line.
const hostGateSanitizeHappyBody = "#!/bin/bash -p\nWORKCELL_SANITIZED_ENTRYPOINT=1\n"

// writeHostGateEntrypointRepo writes every HOST_GATE_SCRIPTS path under a fresh
// temp root with the happy body, applying overrides by repo-relative path: a
// non-empty override replaces that script's body, and an empty-string override
// omits the file entirely (an empty-content / missing-file case).
func writeHostGateEntrypointRepo(t *testing.T, overrides map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for _, rel := range hostGateScriptRelPaths {
		body := hostGateSanitizeHappyBody
		if o, ok := overrides[rel]; ok {
			body = o
		}
		if body == "" {
			continue
		}
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	return root
}

func TestCheckHostGateEntrypointSanitize(t *testing.T) {
	const (
		shebangSuffix    = " to use an absolute privileged Bash shebang before self-sanitizing its host entrypoint"
		sanitizeSuffix   = " to self-sanitize its host entrypoint before running release or boundary checks"
		firstScript      = "scripts/build-and-test.sh"
		secondScript     = "scripts/check-pinned-inputs.sh"
		fourthScript     = "scripts/container-smoke.sh"
		lateScript       = "scripts/verify-release-bundle.sh"
		noShebangBody    = "#!/bin/bash\nWORKCELL_SANITIZED_ENTRYPOINT=1\n"
		noSentinelBody   = "#!/bin/bash -p\n# no sanitize sentinel here\n"
		bothBrokenBody   = "#!/bin/bash\n# no sanitize sentinel here\n"
		shebangSecondLn  = "# comment\n#!/bin/bash -p\nWORKCELL_SANITIZED_ENTRYPOINT=1\n"
		trustedEntryBody = "#!/bin/bash -p\nexec ./scripts/lib/trusted-entrypoint.sh\n"
	)
	tests := []struct {
		name       string
		overrides  map[string]string
		wantRel    string // "" means expect success
		wantSuffix string
	}{
		{name: "happy path all invariants hold"},
		{
			name:       "trusted-entrypoint alternative satisfies sentinel",
			overrides:  map[string]string{firstScript: trustedEntryBody},
			wantSuffix: "", // the trusted-entrypoint.sh alternation half holds
		},
		{
			// kindFirstLineRegex: a non-privileged shebang fails the anchored probe.
			name:       "first script non-privileged shebang",
			overrides:  map[string]string{firstScript: noShebangBody},
			wantRel:    firstScript,
			wantSuffix: shebangSuffix,
		},
		{
			// kindRegexPresent: a good shebang but no self-sanitize sentinel.
			name:       "first script missing entrypoint sentinel",
			overrides:  map[string]string{firstScript: noSentinelBody},
			wantRel:    firstScript,
			wantSuffix: sanitizeSuffix,
		},
		{
			// Per-script order: the shebang check precedes the sentinel check, so a
			// script failing both yields the shebang message.
			name:       "first script fails both shebang wins",
			overrides:  map[string]string{firstScript: bothBrokenBody},
			wantRel:    firstScript,
			wantSuffix: shebangSuffix,
		},
		{
			// firstLineRegex reads only line 1: a privileged shebang on line 2 does
			// not satisfy the probe.
			name:       "privileged shebang only on second line",
			overrides:  map[string]string{firstScript: shebangSecondLn},
			wantRel:    firstScript,
			wantSuffix: shebangSuffix,
		},
		{
			// Array order: an earlier bad script wins over a later bad one.
			name: "earlier script wins over later",
			overrides: map[string]string{
				secondScript: noShebangBody,
				lateScript:   noSentinelBody,
			},
			wantRel:    secondScript,
			wantSuffix: shebangSuffix,
		},
		{
			// A later script's sentinel failure fires once earlier scripts pass.
			name:       "fourth script missing sentinel",
			overrides:  map[string]string{fourthScript: noSentinelBody},
			wantRel:    fourthScript,
			wantSuffix: sanitizeSuffix,
		},
		{
			// A missing file is empty content: the shebang probe fails first.
			name:       "first script missing file",
			overrides:  map[string]string{firstScript: ""},
			wantRel:    firstScript,
			wantSuffix: shebangSuffix,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := writeHostGateEntrypointRepo(t, tc.overrides)
			err := CheckHostGateEntrypointSanitize(root)
			if tc.wantRel == "" {
				if err != nil {
					t.Fatalf("CheckHostGateEntrypointSanitize() = %v, want nil", err)
				}
				return
			}
			want := "Expected " + root + "/" + tc.wantRel + tc.wantSuffix
			if err == nil {
				t.Fatalf("CheckHostGateEntrypointSanitize() = nil, want error %q", want)
			}
			if err.Error() != want {
				t.Fatalf("CheckHostGateEntrypointSanitize() error = %q, want %q", err.Error(), want)
			}
		})
	}
}

// TestCheckHostGateEntrypointSanitizeCount asserts the generated list contains
// exactly forty-four checks: a shebang plus an entrypoint probe for each of the
// twenty-two HOST_GATE_SCRIPTS.
func TestCheckHostGateEntrypointSanitizeCount(t *testing.T) {
	got := len(hostGateEntrypointSanitizeChecks("/repo"))
	const want = 44
	if got != want {
		t.Fatalf("hostGateEntrypointSanitizeChecks(...) has %d checks, want %d", got, want)
	}
	if len(hostGateScriptRelPaths) != 22 {
		t.Fatalf("hostGateScriptRelPaths has %d paths, want 22", len(hostGateScriptRelPaths))
	}
}

// TestCheckHostGateEntrypointSanitizeLineParity proves the entrypoint alternation
// is a genuine regex matched per line: each alternative matches within a single
// line, and the `\.` matches a literal dot only (not an arbitrary character).
func TestCheckHostGateEntrypointSanitizeLineParity(t *testing.T) {
	pat := `WORKCELL_SANITIZED_ENTRYPOINT|trusted-entrypoint\.sh`
	if !regexMatchesAnyLine(pat, "export WORKCELL_SANITIZED_ENTRYPOINT=1") {
		t.Fatalf("expected the sentinel alternative to match")
	}
	if !regexMatchesAnyLine(pat, "exec ./scripts/lib/trusted-entrypoint.sh") {
		t.Fatalf("expected the trusted-entrypoint.sh alternative to match")
	}
	if regexMatchesAnyLine(pat, "exec ./scripts/lib/trusted-entrypointXsh") {
		t.Fatalf(`\. must match a literal dot only, not an arbitrary character`)
	}
}

// TestCheckHostGateEntrypointSanitizeRealRepo asserts every real HOST_GATE_SCRIPTS
// file in this repository satisfies both per-script invariants. This is the key
// guard against a mis-transcribed pattern or a wrong target file.
func TestCheckHostGateEntrypointSanitizeRealRepo(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	if _, err := os.Stat(filepath.Join(repoRoot, hostGateScriptRelPaths[0])); err != nil {
		t.Skipf("real %s not found at %s: %v", hostGateScriptRelPaths[0], repoRoot, err)
	}
	if err := CheckHostGateEntrypointSanitize(repoRoot); err != nil {
		t.Fatalf("CheckHostGateEntrypointSanitize(real repo) = %v, want nil", err)
	}
}

// writePrecommitUpstreamPinGateRepo writes .githooks/pre-commit with the given
// body under a fresh temp root; an empty body omits the file entirely.
func writePrecommitUpstreamPinGateRepo(t *testing.T, body string) string {
	t.Helper()
	root := t.TempDir()
	if body == "" {
		return root
	}
	path := filepath.Join(root, ".githooks", "pre-commit")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return root
}

func TestCheckPrecommitUpstreamPinGate(t *testing.T) {
	const wantErr = "Expected repo pre-commit hook to gate commits on pending pinned upstream updates"
	happy := "#!/bin/bash\n\"${ROOT_DIR}/scripts/update-upstream-pins.sh\" --check\n"
	tests := []struct {
		name    string
		body    string
		wantErr string
	}{
		{name: "happy path pattern present", body: happy},
		{
			name:    "missing --check gate",
			body:    "#!/bin/bash\n\"${ROOT_DIR}/scripts/update-upstream-pins.sh\" --apply\n",
			wantErr: wantErr,
		},
		{
			name:    "missing hook file",
			body:    "",
			wantErr: wantErr,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := writePrecommitUpstreamPinGateRepo(t, tc.body)
			err := CheckPrecommitUpstreamPinGate(root)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("CheckPrecommitUpstreamPinGate() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("CheckPrecommitUpstreamPinGate() = nil, want error %q", tc.wantErr)
			}
			if err.Error() != tc.wantErr {
				t.Fatalf("CheckPrecommitUpstreamPinGate() error = %q, want %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestCheckPrecommitUpstreamPinGateCount(t *testing.T) {
	got := len(precommitUpstreamPinGateChecks)
	const want = 1
	if got != want {
		t.Fatalf("precommitUpstreamPinGateChecks has %d checks, want %d", got, want)
	}
}

// TestCheckPrecommitUpstreamPinGateLineParity proves the pattern's `\.` is a
// literal dot and that the probe matches within a single line (rg parity): a
// pattern split across a newline must not match.
func TestCheckPrecommitUpstreamPinGateLineParity(t *testing.T) {
	pat := `scripts/update-upstream-pins\.sh" --check`
	if !regexMatchesAnyLine(pat, `run "scripts/update-upstream-pins.sh" --check now`) {
		t.Fatalf("expected the intact --check gate line to match")
	}
	if regexMatchesAnyLine(pat, "scripts/update-upstream-pins.sh\"\n--check") {
		t.Fatalf("a gate split across a newline must NOT match (rg is line-oriented)")
	}
	if regexMatchesAnyLine(pat, `scripts/update-upstream-pinsXsh" --check`) {
		t.Fatalf(`\. must match a literal dot only, not an arbitrary character`)
	}
}

func TestCheckPrecommitUpstreamPinGateRealRepo(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	if _, err := os.Stat(filepath.Join(repoRoot, ".githooks", "pre-commit")); err != nil {
		t.Skipf("real .githooks/pre-commit not found at %s: %v", repoRoot, err)
	}
	if err := CheckPrecommitUpstreamPinGate(repoRoot); err != nil {
		t.Fatalf("CheckPrecommitUpstreamPinGate(real repo) = %v, want nil", err)
	}
}

// trustedDockerClientHappyBody satisfies all four per-script probes: it sources
// the helper, seeds the client state, drops caller HOME, and invokes buildx via
// the trusted plugin path.
const trustedDockerClientHappyBody = "source \"${ROOT_DIR}/scripts/lib/trusted-docker-client.sh\"\n" +
	"setup_workcell_trusted_docker_client\n" +
	"HOME=/tmp exec ./sanitized-reexec\n" +
	"buildx_cmd build .\n"

// writeTrustedDockerClientRepo writes every trusted-Docker-client script under a
// fresh temp root with the happy body, applying overrides by repo-relative path:
// a non-empty override replaces the body, an empty-string override omits the file.
func writeTrustedDockerClientRepo(t *testing.T, overrides map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for _, rel := range trustedDockerClientScriptRelPaths {
		body := trustedDockerClientHappyBody
		if o, ok := overrides[rel]; ok {
			body = o
		}
		if body == "" {
			continue
		}
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	return root
}

func TestCheckTrustedDockerClientRg(t *testing.T) {
	const (
		sourceSuffix = " to source the trusted Docker client helper"
		setupSuffix  = " to seed a trusted Docker client state before using Docker"
		homeSuffix   = " to stop preserving caller HOME across its sanitized entrypoint re-exec"
		buildxSuffix = " to invoke buildx through the trusted absolute plugin path"
		firstScript  = "scripts/container-smoke.sh"
		secondScript = "scripts/generate-builder-environment-manifest.sh"
	)
	drop := func(rel, needle string) map[string]string {
		return map[string]string{rel: strings.Replace(trustedDockerClientHappyBody, needle, "# removed\n", 1)}
	}
	tests := []struct {
		name       string
		overrides  map[string]string
		wantRel    string
		wantSuffix string
	}{
		{name: "happy path all invariants hold"},
		{
			name:       "first script missing source helper",
			overrides:  drop(firstScript, "source \"${ROOT_DIR}/scripts/lib/trusted-docker-client.sh\"\n"),
			wantRel:    firstScript,
			wantSuffix: sourceSuffix,
		},
		{
			name:       "first script missing setup call",
			overrides:  drop(firstScript, "setup_workcell_trusted_docker_client\n"),
			wantRel:    firstScript,
			wantSuffix: setupSuffix,
		},
		{
			name:       "first script missing HOME drop",
			overrides:  drop(firstScript, "HOME=/tmp exec ./sanitized-reexec\n"),
			wantRel:    firstScript,
			wantSuffix: homeSuffix,
		},
		{
			name:       "first script missing buildx invocation",
			overrides:  drop(firstScript, "buildx_cmd build .\n"),
			wantRel:    firstScript,
			wantSuffix: buildxSuffix,
		},
		{
			// Loop order: the first loop's three probes run for every script before
			// the second loop's buildx probe, so a first-script buildx failure loses
			// to a second-script source failure.
			name: "loop1 across scripts precedes loop2",
			overrides: map[string]string{
				firstScript:  strings.Replace(trustedDockerClientHappyBody, "buildx_cmd build .\n", "# removed\n", 1),
				secondScript: strings.Replace(trustedDockerClientHappyBody, "source \"${ROOT_DIR}/scripts/lib/trusted-docker-client.sh\"\n", "# removed\n", 1),
			},
			wantRel:    secondScript,
			wantSuffix: sourceSuffix,
		},
		{
			// A missing file is empty content: the first (source) probe fails.
			name:       "first script missing file",
			overrides:  map[string]string{firstScript: ""},
			wantRel:    firstScript,
			wantSuffix: sourceSuffix,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := writeTrustedDockerClientRepo(t, tc.overrides)
			err := CheckTrustedDockerClientRg(root)
			if tc.wantRel == "" {
				if err != nil {
					t.Fatalf("CheckTrustedDockerClientRg() = %v, want nil", err)
				}
				return
			}
			want := "Expected " + root + "/" + tc.wantRel + tc.wantSuffix
			if err == nil {
				t.Fatalf("CheckTrustedDockerClientRg() = nil, want error %q", want)
			}
			if err.Error() != want {
				t.Fatalf("CheckTrustedDockerClientRg() error = %q, want %q", err.Error(), want)
			}
		})
	}
}

// TestCheckTrustedDockerClientRgCount asserts sixteen checks: three loop-one
// probes plus one loop-two buildx probe for each of the four scripts.
func TestCheckTrustedDockerClientRgCount(t *testing.T) {
	got := len(trustedDockerClientRgChecks("/repo"))
	const want = 16
	if got != want {
		t.Fatalf("trustedDockerClientRgChecks(...) has %d checks, want %d", got, want)
	}
	if len(trustedDockerClientScriptRelPaths) != 4 {
		t.Fatalf("trustedDockerClientScriptRelPaths has %d paths, want 4", len(trustedDockerClientScriptRelPaths))
	}
}

// TestCheckTrustedDockerClientRgLineParity proves the source-helper probe's
// escaped `\$ \{ \} \.` match the literal `$ { } .` within a single line, and
// that a match split across a newline does not hold (rg is line-oriented).
func TestCheckTrustedDockerClientRgLineParity(t *testing.T) {
	pat := `source "\$\{ROOT_DIR\}/scripts/lib/trusted-docker-client\.sh"`
	if !regexMatchesAnyLine(pat, `source "${ROOT_DIR}/scripts/lib/trusted-docker-client.sh"`) {
		t.Fatalf("expected the intact source-helper line to match")
	}
	if regexMatchesAnyLine(pat, "source \"${ROOT_DIR}/scripts/lib/\ntrusted-docker-client.sh\"") {
		t.Fatalf("a source-helper line split across a newline must NOT match (rg is line-oriented)")
	}
	if regexMatchesAnyLine(pat, `source "XYROOT_DIRZ/scripts/lib/trusted-docker-client.sh"`) {
		t.Fatalf(`escaped ${...} must match the literal $ { } characters only`)
	}
}

func TestCheckTrustedDockerClientRgRealRepo(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	if _, err := os.Stat(filepath.Join(repoRoot, trustedDockerClientScriptRelPaths[0])); err != nil {
		t.Skipf("real %s not found at %s: %v", trustedDockerClientScriptRelPaths[0], repoRoot, err)
	}
	if err := CheckTrustedDockerClientRg(repoRoot); err != nil {
		t.Fatalf("CheckTrustedDockerClientRg(real repo) = %v, want nil", err)
	}
}

// claudeUserSettingsRelPath is the repo-relative path to the shipped Claude
// adapter user settings file. verify-invariants.sh passes
// "${ROOT_DIR}/adapters/claude/.claude/settings.json" to the two Claude
// settings-file guards (CheckClaudeMcpProjectServers and
// CheckClaudeGuardBashHook); these real-repo assertions read the same file so a
// mistranscribed jq path or expected literal is caught against the shipped
// artifact, not just synthetic fixtures. This closes the D3 tail for the Claude
// and Gemini adapter-settings groups (see the "Parity discipline" note in
// workcellhardening.go).
const claudeUserSettingsRelPath = "adapters/claude/.claude/settings.json"

func TestCheckClaudeMcpProjectServersRealRepo(t *testing.T) {
	settingsPath := filepath.Join("..", "..", claudeUserSettingsRelPath)
	if _, err := os.Stat(settingsPath); err != nil {
		t.Skipf("real %s not found: %v", claudeUserSettingsRelPath, err)
	}
	if err := CheckClaudeMcpProjectServers(settingsPath); err != nil {
		t.Fatalf("CheckClaudeMcpProjectServers(real repo) = %v, want nil", err)
	}
}

func TestCheckClaudeGuardBashHookRealRepo(t *testing.T) {
	settingsPath := filepath.Join("..", "..", claudeUserSettingsRelPath)
	if _, err := os.Stat(settingsPath); err != nil {
		t.Skipf("real %s not found: %v", claudeUserSettingsRelPath, err)
	}
	if err := CheckClaudeGuardBashHook(settingsPath); err != nil {
		t.Fatalf("CheckClaudeGuardBashHook(real repo) = %v, want nil", err)
	}
}

func TestCheckClaudeManagedBypassRealRepo(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	if _, err := os.Stat(filepath.Join(repoRoot, claudeManagedSettingsRelPath)); err != nil {
		t.Skipf("real %s not found: %v", claudeManagedSettingsRelPath, err)
	}
	if err := CheckClaudeManagedBypass(repoRoot); err != nil {
		t.Fatalf("CheckClaudeManagedBypass(real repo) = %v, want nil", err)
	}
}

func TestCheckGeminiSettingsBaselineRealRepo(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	if _, err := os.Stat(filepath.Join(repoRoot, geminiSettingsRelPath)); err != nil {
		t.Skipf("real %s not found: %v", geminiSettingsRelPath, err)
	}
	if err := CheckGeminiSettingsBaseline(repoRoot); err != nil {
		t.Fatalf("CheckGeminiSettingsBaseline(real repo) = %v, want nil", err)
	}
}

func TestCheckGeminiSettingsGuardsRealRepo(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	if _, err := os.Stat(filepath.Join(repoRoot, geminiSettingsRelPath)); err != nil {
		t.Skipf("real %s not found: %v", geminiSettingsRelPath, err)
	}
	if err := CheckGeminiSettingsGuards(repoRoot); err != nil {
		t.Fatalf("CheckGeminiSettingsGuards(real repo) = %v, want nil", err)
	}
}
