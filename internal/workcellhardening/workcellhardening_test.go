// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package workcellhardening

import (
	"os"
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
