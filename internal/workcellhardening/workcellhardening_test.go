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
