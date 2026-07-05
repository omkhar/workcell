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
