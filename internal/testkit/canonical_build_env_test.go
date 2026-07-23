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

const canonicalGuardBlock = `# shellcheck source=scripts/lib/canonical-build-env.sh
source "${ROOT_DIR}/scripts/lib/canonical-build-env.sh"
workcell_require_modern_privileged_bash "$@"
workcell_require_canonical_build_environment
`

var canonicalEntrypoints = []struct {
	relative       string
	firstOperation string
	variable       string
	value          string
}{
	{"scripts/dev-quick-check.sh", "require_tool()", "GOAUTH", "workcell-entrypoint-auth"},
	{"scripts/validate-repo.sh", "SKIP_HEAVY_HOST_SHELLCHECK=", "CGO_CFLAGS", "-DWORKCELL_ENTRYPOINT"},
	{"scripts/verify-github-hosted-controls.sh", "POLICY_PATH=", "CC", "/tmp/workcell-entrypoint-cc"},
}

func canonicalBuildEnvVariable(name string) bool {
	if strings.HasPrefix(name, "GO") ||
		strings.HasPrefix(name, "CGO") ||
		strings.HasPrefix(name, "GIT_") ||
		strings.HasPrefix(name, "BASH_FUNC_") {
		return true
	}
	switch name {
	case "CC", "CXX", "FC", "AR", "GCCGO", "GCCGOTOOLDIR", "PKG_CONFIG",
		"NETRC", "GCM_INTERACTIVE", "BASH_ENV", "ENV",
		"WORKCELL_CANONICAL_BUILD_ENV", "WORKCELL_SANITIZED_ENTRYPOINT":
		return true
	default:
		return false
	}
}

func canonicalBuildEnvProbe(
	tb testing.TB,
	script string,
	extraEnv map[string]string,
	args ...string,
) (int, string) {
	tb.Helper()

	helper := filepath.Join(repoRoot(tb), "scripts", "lib", "canonical-build-env.sh")
	return canonicalBuildEnvProbeWithHelper(tb, helper, script, extraEnv, args...)
}

func canonicalBuildEnvProbeWithHelper(
	tb testing.TB,
	helper string,
	script string,
	extraEnv map[string]string,
	args ...string,
) (int, string) {
	tb.Helper()

	argv := []string{"--noprofile", "--norc", "-p", "-c", script, "canonical-build-env-probe", helper}
	argv = append(argv, args...)
	cmd := exec.Command("/bin/bash", argv...)
	cmd.Env = canonicalBuildEnv(extraEnv)

	output, err := cmd.CombinedOutput()
	if err == nil {
		return 0, string(output)
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode(), string(output)
	}
	tb.Fatalf("canonical build environment probe failed: %v", err)
	return 0, ""
}

func canonicalBuildEnv(extraEnv map[string]string) []string {
	env := make([]string, 0, len(os.Environ())+len(extraEnv)+2)
	for _, entry := range os.Environ() {
		name := strings.SplitN(entry, "=", 2)[0]
		_, overridden := extraEnv[name]
		if !canonicalBuildEnvVariable(name) && !overridden {
			env = append(env, entry)
		}
	}
	for _, name := range []string{"BASH_ENV", "ENV"} {
		if _, overridden := extraEnv[name]; !overridden {
			env = append(env, name+"=")
		}
	}
	for name, value := range extraEnv {
		env = append(env, name+"="+value)
	}
	return env
}

func writeCanonicalFixture(tb testing.TB, path string, content []byte, mode os.FileMode) {
	tb.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		tb.Fatal(err)
	}
	if err := os.WriteFile(path, content, mode); err != nil {
		tb.Fatal(err)
	}
}

func copyCanonicalFixture(tb testing.TB, source string, destination string) {
	tb.Helper()
	content, err := os.ReadFile(source)
	if err != nil {
		tb.Fatal(err)
	}
	writeCanonicalFixture(tb, destination, content, 0o755)
}

func writeCanonicalMutation(
	tb testing.TB,
	source string,
	destination string,
	original string,
	replacement string,
) {
	tb.Helper()
	content, err := os.ReadFile(source)
	if err != nil {
		tb.Fatal(err)
	}
	if count := strings.Count(string(content), original); count != 1 {
		tb.Fatalf("mutation anchor count in %s = %d, want 1: %q", source, count, original)
	}
	mutated := strings.Replace(string(content), original, replacement, 1)
	writeCanonicalFixture(tb, destination, []byte(mutated), 0o755)
}

func TestCanonicalBuildEnvironmentRejectsBuildAndShellInputs(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		variable string
		value    string
	}{
		{"bash-startup", "BASH_ENV", "/tmp/workcell-bash-env"},
		{"posix-shell-startup", "ENV", "/tmp/workcell-env"},
		{"exported-bash-function", "BASH_FUNC_workcell_probe%%", "() { printf workcell-hostile; }"},
		{"flags-tags-equals", "GOFLAGS", "-tags=workcell_dormant"},
		{"flags-tags-split", "GOFLAGS", "-tags workcell_dormant"},
		{"flags-overlay-equals", "GOFLAGS", "-overlay=/tmp/workcell-overlay.json"},
		{"flags-overlay-split", "GOFLAGS", "-overlay /tmp/workcell-overlay.json"},
		{"persisted-env", "GOENV", "/tmp/workcell-goenv"},
		{"workspace", "GOWORK", "/tmp/workcell.work"},
		{"target-os", "GOOS", "linux"},
		{"target-architecture", "GOARCH", "amd64"},
		{"experiment", "GOEXPERIMENT", "workcell-hostile"},
		{"module-mode", "GO111MODULE", "off"},
		{"toolchain", "GOTOOLCHAIN", "local"},
		{"toolchain-root", "GOROOT", "/tmp/workcell-goroot"},
		{"toolcache-alias-missing-minor", "GOROOT_1_X64", "/tmp/workcell-goroot"},
		{"toolcache-alias-unknown-architecture", "GOROOT_1_24_X86_64", "/tmp/workcell-goroot"},
		{"toolcache-alias-lowercase-architecture", "GOROOT_1_24_x64", "/tmp/workcell-goroot"},
		{"toolcache-alias-suffixed", "GOROOT_1_24_X64_EXTRA", "/tmp/workcell-goroot"},
		{"debug-semantics", "GODEBUG", "gotypesalias=0"},
		{"fips-selection", "GOFIPS140", "off"},
		{"external-link-selection", "GO_EXTLINK_ENABLED", "0"},
		{"auth-command", "GOAUTH", "workcell-hostile"},
		{"vcs-policy", "GOVCS", "*:all"},
		{"module-proxy", "GOPROXY", "direct"},
		{"checksum-policy", "GOSUMDB", "off"},
		{"internal-version", "GOTOOLCHAIN_INTERNAL_SWITCH_VERSION", "go999.0"},
		{"internal-count", "GOTOOLCHAIN_INTERNAL_SWITCH_COUNT", "1"},
		{"cache-program", "GOCACHEPROG", "/tmp/workcell-cacheprog"},
		{"future-go-input", "GO_WORKCELL_FUTURE", "workcell-hostile"},
		{"runtime-scheduling", "GOMAXPROCS", "1"},
		{"cgo-enabled", "CGO_ENABLED", "0"},
		{"cgo-cflags", "CGO_CFLAGS", "-DWORKCELL_HOSTILE"},
		{"cgo-cppflags", "CGO_CPPFLAGS", "-I/tmp/workcell-hostile"},
		{"cgo-cxxflags", "CGO_CXXFLAGS", "-DWORKCELL_HOSTILE"},
		{"cgo-fflags", "CGO_FFLAGS", "-fworkcell-hostile"},
		{"cgo-ldflags", "CGO_LDFLAGS", "-L/tmp/workcell-hostile"},
		{"cgo-allow", "CGO_CFLAGS_ALLOW", ".*"},
		{"cgo-disallow", "CGO_LDFLAGS_DISALLOW", "^$"},
		{"future-cgo-input", "CGO_WORKCELL_FUTURE", "workcell-hostile"},
		{"future-cgo-input-no-separator", "CGOWORKCELL_FUTURE", "workcell-hostile"},
		{"c-compiler", "CC", "/tmp/workcell-cc"},
		{"cxx-compiler", "CXX", "/tmp/workcell-cxx"},
		{"fortran-compiler", "FC", "/tmp/workcell-fc"},
		{"archiver", "AR", "/tmp/workcell-ar"},
		{"gccgo", "GCCGO", "/tmp/workcell-gccgo"},
		{"gccgo-tool-dir", "GCCGOTOOLDIR", "/tmp/workcell-gccgo-tools"},
		{"pkg-config", "PKG_CONFIG", "/tmp/workcell-pkg-config"},
		{"netrc-path", "NETRC", "/tmp/workcell-netrc"},
		{"credential-manager-interactive", "GCM_INTERACTIVE", "always"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			code, output := canonicalBuildEnvProbe(t, `
set -euo pipefail
source "$1"
workcell_require_canonical_build_environment
echo should-not-run
`, map[string]string{tc.variable: tc.value})
			if code != 2 {
				t.Fatalf("exit code = %d, want 2; output=%q", code, output)
			}
			if strings.HasPrefix(tc.variable, "BASH_FUNC_") {
				if !strings.Contains(output, "rejects ambient exported Bash functions") ||
					strings.Contains(output, tc.variable) {
					t.Fatalf("output disclosed or did not classify an exported Bash function: %q", output)
				}
			} else if !strings.Contains(output, tc.variable) {
				t.Fatalf("output %q does not name rejected variable %s", output, tc.variable)
			}
			if strings.Contains(output, tc.value) {
				t.Fatalf("output leaked rejected %s value: %q", tc.variable, output)
			}
		})
	}
}

func TestCanonicalBuildEnvironmentScrubsPassiveHostedGoAliases(t *testing.T) {
	t.Parallel()

	realHelper := filepath.Join(repoRoot(t), "scripts", "lib", "canonical-build-env.sh")
	aliases := map[string]string{
		"GOROOT_1_24_X64":    "/opt/hostedtoolcache/go/1.24.0/x64",
		"GOROOT_999_1_ARM64": "/opt/hostedtoolcache/go/999.1.0/arm64",
	}
	aliasNames := []string{"GOROOT_1_24_X64", "GOROOT_999_1_ARM64"}
	probe := `
set -euo pipefail
source "$1"
shift
workcell_require_canonical_build_environment
workcell_require_canonical_build_environment
for name in "$@"; do
  if declare -p "$name" >/dev/null 2>&1; then
    printf 'retained:%s\n' "$name"
    exit 3
  fi
done
printf 'aliases-scrubbed\n'
`
	code, output := canonicalBuildEnvProbeWithHelper(t, realHelper, probe, aliases, aliasNames...)
	if code != 0 || output != "aliases-scrubbed\n" {
		t.Fatalf("passive hosted Go aliases were not scrubbed: code=%d output=%q", code, output)
	}

	readonlyValue := "/tmp/workcell-readonly-hosted-alias-secret"
	readonlyProbe := `
set -uo pipefail
source "$1"
readonly GOROOT_1_24_X64
rc=0
workcell_require_canonical_build_environment || rc=$?
exit "$rc"
`
	code, output = canonicalBuildEnvProbeWithHelper(
		t,
		realHelper,
		readonlyProbe,
		map[string]string{"GOROOT_1_24_X64": readonlyValue},
	)
	if code != 2 ||
		!strings.Contains(output, "could not scrub ambient GOROOT_1_24_X64") ||
		strings.Contains(output, readonlyValue) {
		t.Fatalf("readonly hosted alias did not fail closed without caller errexit: code=%d output=%q", code, output)
	}

	classifierMutant := filepath.Join(t.TempDir(), "canonical-build-env.sh")
	writeCanonicalMutation(
		t,
		realHelper,
		classifierMutant,
		"    if _workcell_canonical_env_is_passive_go_toolcache_alias \"${name}\"; then\n",
		"    if false; then\n",
	)
	if code, output = canonicalBuildEnvProbeWithHelper(t, classifierMutant, probe, aliases, aliasNames...); code != 2 {
		t.Fatalf("tool-cache classifier-removal mutant was not killed: code=%d output=%q", code, output)
	}

	scrubMutant := filepath.Join(t.TempDir(), "canonical-build-env.sh")
	writeCanonicalMutation(
		t,
		realHelper,
		scrubMutant,
		"    if _workcell_canonical_env_is_passive_go_toolcache_alias \"${name}\"; then\n      if ! unset \"${name}\" 2>/dev/null; then\n        printf 'Canonical build environment could not scrub ambient %s.\\n' \"${name}\" >&2\n        return 2\n      fi\n      continue\n    fi\n",
		"    if _workcell_canonical_env_is_passive_go_toolcache_alias \"${name}\"; then\n      :\n      continue\n    fi\n",
	)
	if code, output = canonicalBuildEnvProbeWithHelper(t, scrubMutant, probe, aliases, aliasNames...); code != 3 ||
		!strings.Contains(output, "retained:") {
		t.Fatalf("tool-cache scrub-removal mutant was not killed: code=%d output=%q", code, output)
	}

	unsetFailureMutant := filepath.Join(t.TempDir(), "canonical-build-env.sh")
	writeCanonicalMutation(
		t,
		realHelper,
		unsetFailureMutant,
		"      if ! unset \"${name}\" 2>/dev/null; then\n        printf 'Canonical build environment could not scrub ambient %s.\\n' \"${name}\" >&2\n        return 2\n      fi\n",
		"      unset \"${name}\" 2>/dev/null\n",
	)
	if code, output = canonicalBuildEnvProbeWithHelper(
		t,
		unsetFailureMutant,
		readonlyProbe,
		map[string]string{"GOROOT_1_24_X64": readonlyValue},
	); code != 0 {
		t.Fatalf("unset-failure mutant was not killed: code=%d output=%q", code, output)
	}
}

func TestCanonicalBuildEnvironmentRejectsUnsafeIdentifierBeforeExpansion(t *testing.T) {
	t.Parallel()

	marker := filepath.Join(t.TempDir(), "identifier-expanded")
	variable := "GO[$(: >'" + marker + "')]"
	directProbe := `
set -euo pipefail
source "$1"
_workcell_canonical_env_reject_nonempty "$2"
`
	env := map[string]string{variable: "workcell-unsafe-name"}
	code, output := canonicalBuildEnvProbe(t, directProbe, env, variable)
	if code != 2 ||
		!strings.Contains(output, "unsafe identifier") ||
		strings.Contains(output, variable) ||
		strings.Contains(output, env[variable]) {
		t.Fatalf("unsafe identifier was not rejected generically: code=%d output=%q", code, output)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("unsafe identifier executed before rejection; stat error=%v", err)
	}
	if exec.Command("/bin/bash", "-c", "((BASH_VERSINFO[0] < 4))").Run() == nil {
		code, output = canonicalBuildEnvProbe(t, `
set -euo pipefail
source "$1"
workcell_require_canonical_build_environment
`, env)
		if code != 2 || !strings.Contains(output, "unsafe identifier") {
			t.Fatalf("Bash 3 prefix discovery did not reject unsafe identifier: code=%d output=%q", code, output)
		}
	}

	realHelper := filepath.Join(repoRoot(t), "scripts", "lib", "canonical-build-env.sh")
	mutant := filepath.Join(t.TempDir(), "canonical-build-env.sh")
	writeCanonicalMutation(
		t,
		realHelper,
		mutant,
		"_workcell_canonical_env_reject_nonempty() {\n  local name=\"$1\"\n\n  case \"${name}\" in\n",
		"_workcell_canonical_env_reject_nonempty() {\n  local name=\"$1\"\n\n  case WORKCELL_SAFE_NAME in\n",
	)
	_, _ = canonicalBuildEnvProbeWithHelper(t, mutant, directProbe, env, variable)
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("identifier-validation mutant was not killed: %v", err)
	}
}

func TestCanonicalBuildEnvironmentFailsClosedWhenFunctionDetectorFails(t *testing.T) {
	t.Parallel()

	realHelper := filepath.Join(repoRoot(t), "scripts", "lib", "canonical-build-env.sh")
	mutant := filepath.Join(t.TempDir(), "canonical-build-env.sh")
	writeCanonicalMutation(
		t,
		realHelper,
		mutant,
		"  if /usr/bin/env | /usr/bin/env -i LC_ALL=C /usr/bin/grep '^BASH_FUNC_' >/dev/null; then\n",
		"  if /usr/bin/false | /usr/bin/env -i LC_ALL=C /usr/bin/grep '^BASH_FUNC_' >/dev/null; then\n",
	)
	code, output := canonicalBuildEnvProbeWithHelper(t, mutant, `
set -euo pipefail
source "$1"
workcell_require_canonical_build_environment
`, nil)
	if code != 2 || !strings.Contains(output, "could not inspect ambient exported Bash functions") {
		t.Fatalf("function-detector failure did not fail closed: code=%d output=%q", code, output)
	}
}

func TestCanonicalBuildEnvironmentAllowsOnlyGoStoragePaths(t *testing.T) {
	t.Parallel()

	goCache := filepath.Join(t.TempDir(), "go-build")
	moduleCache := filepath.Join(t.TempDir(), "go-mod")
	goPath := filepath.Join(t.TempDir(), "go-path")
	safeEnv := map[string]string{
		"GOFLAGS":                "",
		"GOENV":                  "off",
		"GOWORK":                 "off",
		"GOPATH":                 goPath,
		"GOCACHE":                goCache,
		"GOMODCACHE":             moduleCache,
		"GIT_NO_REPLACE_OBJECTS": "1",
		"GIT_CONFIG_NOSYSTEM":    "1",
		"GIT_CONFIG_SYSTEM":      "/dev/null",
		"GIT_CONFIG_GLOBAL":      "/dev/null",
		"GIT_CONFIG_COUNT":       "1",
		"GIT_CONFIG_KEY_0":       "core.attributesFile",
		"GIT_CONFIG_VALUE_0":     "/dev/null",
		"GIT_ATTR_NOSYSTEM":      "1",
		"GIT_ATTR_SYSTEM":        "/dev/null",
		"GIT_ATTR_GLOBAL":        "/dev/null",
	}
	code, output := canonicalBuildEnvProbe(t, `
set -euo pipefail
source "$1"
workcell_require_canonical_build_environment
workcell_require_canonical_build_environment
printf 'GOFLAGS=<%s>\n' "$GOFLAGS"
printf 'GOENV=%s\n' "$GOENV"
printf 'GOWORK=%s\n' "$GOWORK"
printf 'GOPATH=%s\n' "$GOPATH"
printf 'GOCACHE=%s\n' "$GOCACHE"
printf 'GOMODCACHE=%s\n' "$GOMODCACHE"
printf 'GIT_CONFIG_GLOBAL=%s\n' "$GIT_CONFIG_GLOBAL"
printf 'GIT_ATTR_GLOBAL=%s\n' "$GIT_ATTR_GLOBAL"
printf 'BASH_ENV_SET=%s\n' "${BASH_ENV+x}"
printf 'ENV_SET=%s\n' "${ENV+x}"
`, safeEnv)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; output=%q", code, output)
	}
	for _, want := range []string{
		"GOFLAGS=<>",
		"GOENV=off",
		"GOWORK=off",
		"GOPATH=" + goPath,
		"GOCACHE=" + goCache,
		"GOMODCACHE=" + moduleCache,
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_ATTR_GLOBAL=/dev/null",
		"BASH_ENV_SET=",
		"ENV_SET=",
	} {
		if !strings.Contains(output, want+"\n") {
			t.Fatalf("canonical output %q does not contain %q", output, want)
		}
	}
}

func TestCanonicalBuildEnvironmentRejectsGitInputs(t *testing.T) {
	t.Parallel()

	cases := []struct {
		variable string
		value    string
	}{
		{"GIT_INDEX_FILE", "/tmp/workcell-index"},
		{"GIT_DIR", "/tmp/workcell-git-dir"},
		{"GIT_WORK_TREE", "/tmp/workcell-worktree"},
		{"GIT_COMMON_DIR", "/tmp/workcell-common-dir"},
		{"GIT_OBJECT_DIRECTORY", "/tmp/workcell-objects"},
		{"GIT_ALTERNATE_OBJECT_DIRECTORIES", "/tmp/workcell-alternates"},
		{"GIT_REPLACE_REF_BASE", "refs/workcell/replace"},
		{"GIT_GRAFT_FILE", "/tmp/workcell-grafts"},
		{"GIT_SHALLOW_FILE", "/tmp/workcell-shallow"},
		{"GIT_EXEC_PATH", "/tmp/workcell-exec"},
		{"GIT_CONFIG", "/tmp/workcell-config"},
		{"GIT_CONFIG_PARAMETERS", "'core.worktree'='/tmp/workcell'"},
		{"GIT_CONFIG_COUNT", "2"},
		{"GIT_CONFIG_KEY_0", "core.worktree"},
		{"GIT_CONFIG_VALUE_0", "/tmp/workcell"},
		{"GIT_ATTR_SOURCE", "HEAD"},
		{"GIT_EXTERNAL_DIFF", "/tmp/workcell-diff"},
		{"GIT_TEMPLATE_DIR", "/tmp/workcell-template"},
		{"GIT_TEST_ASSUME_DIFFERENT_OWNER", "1"},
		{"GIT_WORKCELL_FUTURE", "workcell-hostile"},
		{"GIT_NO_REPLACE_OBJECTS", "0"},
		{"GIT_CONFIG_NOSYSTEM", "0"},
		{"GIT_CONFIG_SYSTEM", "/tmp/workcell-system-config"},
		{"GIT_CONFIG_GLOBAL", "/tmp/workcell-global-config"},
		{"GIT_ATTR_NOSYSTEM", "0"},
		{"GIT_ATTR_SYSTEM", "/tmp/workcell-system-attributes"},
		{"GIT_ATTR_GLOBAL", "/tmp/workcell-global-attributes"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(strings.ToLower(tc.variable), func(t *testing.T) {
			t.Parallel()
			code, output := canonicalBuildEnvProbe(t, `
set -euo pipefail
source "$1"
workcell_require_canonical_build_environment
echo should-not-run
`, map[string]string{tc.variable: tc.value})
			if code != 2 {
				t.Fatalf("exit code = %d, want 2; output=%q", code, output)
			}
			if !strings.Contains(output, tc.variable) {
				t.Fatalf("output %q does not name rejected variable %s", output, tc.variable)
			}
			if strings.Contains(output, tc.value) {
				t.Fatalf("output leaked rejected %s value: %q", tc.variable, output)
			}
		})
	}
}

func TestCanonicalBuildEnvironmentGitAttributeBoundary(t *testing.T) {
	t.Parallel()

	fixtureRoot := t.TempDir()
	fakeHome := filepath.Join(fixtureRoot, "home")
	globalAttributes := filepath.Join(fakeHome, ".config", "git", "attributes")
	writeCanonicalFixture(t, globalAttributes, []byte("*.txt export-ignore\n"), 0o600)
	writeCanonicalFixture(
		t,
		filepath.Join(fakeHome, ".gitconfig"),
		[]byte("[credential]\n\thelper = !exit 97\n"),
		0o600,
	)
	repo := createSnapshotFixtureRepo(t, fixtureRoot)
	probePath := filepath.Join(repo, "attribute-probe.txt")
	writeCanonicalFixture(t, probePath, []byte("probe\n"), 0o600)

	checkAttribute := func() (int, string) {
		return canonicalBuildEnvProbe(t, `
set -euo pipefail
source "$1"
workcell_require_canonical_build_environment
if git config --global --get credential.helper >/dev/null 2>&1; then
  echo unexpected-global-credential-helper
  exit 1
fi
git -C "$2" check-attr export-ignore -- attribute-probe.txt
`, map[string]string{"HOME": fakeHome}, repo)
	}

	code, output := checkAttribute()
	if code != 0 || output != "attribute-probe.txt: export-ignore: unspecified\n" {
		t.Fatalf("global Git state affected canonical attributes: code=%d output=%q", code, output)
	}

	writeCanonicalFixture(
		t,
		filepath.Join(repo, ".git", "info", "attributes"),
		[]byte("attribute-probe.txt export-ignore\n"),
		0o600,
	)
	code, output = checkAttribute()
	if code != 0 || output != "attribute-probe.txt: export-ignore: set\n" {
		t.Fatalf("local administrative attributes limitation not observed: code=%d output=%q", code, output)
	}
}

func TestCanonicalBuildEnvironmentBlocksCacheProgramExecution(t *testing.T) {
	t.Parallel()

	fixtureRoot := t.TempDir()
	marker := filepath.Join(fixtureRoot, "cache-program-ran")
	cacheProgram := filepath.Join(fixtureRoot, "cache-program")
	writeCanonicalFixture(t, cacheProgram, []byte(`#!/bin/sh
: >"${WORKCELL_CACHEPROG_MARKER:?}"
exit 1
`), 0o755)

	runGo := exec.Command("go", "list", "./internal/testkit")
	runGo.Dir = repoRoot(t)
	runGo.Env = canonicalBuildEnv(map[string]string{
		"GOENV":                     "off",
		"GOWORK":                    "off",
		"GOFLAGS":                   "",
		"GOCACHE":                   filepath.Join(fixtureRoot, "go-cache"),
		"GOCACHEPROG":               cacheProgram,
		"WORKCELL_CACHEPROG_MARKER": marker,
	})
	_ = runGo.Run()
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("control did not prove GOCACHEPROG execution: %v", err)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}

	probe := `
set -euo pipefail
source "$1"
workcell_require_canonical_build_environment
cd "$2"
go list ./internal/testkit
`
	hostileEnv := map[string]string{
		"GOCACHE":                   filepath.Join(fixtureRoot, "guarded-cache"),
		"GOCACHEPROG":               cacheProgram,
		"WORKCELL_CACHEPROG_MARKER": marker,
	}
	code, output := canonicalBuildEnvProbe(t, probe, hostileEnv, repoRoot(t))
	if code != 2 || !strings.Contains(output, "GOCACHEPROG") {
		t.Fatalf("guard did not reject GOCACHEPROG: code=%d output=%q", code, output)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("guarded GOCACHEPROG executed; stat error=%v", err)
	}

	realHelper := filepath.Join(repoRoot(t), "scripts", "lib", "canonical-build-env.sh")
	mutant := filepath.Join(fixtureRoot, "canonical-build-env-mutant.sh")
	writeCanonicalMutation(
		t,
		realHelper,
		mutant,
		"      GOENV | GOWORK | GOPATH | GOCACHE | GOMODCACHE)\n",
		"      GOENV | GOWORK | GOPATH | GOCACHE | GOMODCACHE | GOCACHEPROG)\n",
	)
	_, _ = canonicalBuildEnvProbeWithHelper(t, mutant, probe, hostileEnv, repoRoot(t))
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("GOCACHEPROG allowlist mutant was not killed: %v", err)
	}
}

func TestCanonicalBuildEnvironmentKillsGuardMutations(t *testing.T) {
	t.Parallel()

	realHelper := filepath.Join(repoRoot(t), "scripts", "lib", "canonical-build-env.sh")
	cases := []struct {
		name        string
		original    string
		replacement string
		variable    string
		value       string
	}{
		{
			"future-go-allowlist",
			"      GOENV | GOWORK | GOPATH | GOCACHE | GOMODCACHE)\n",
			"      GOENV | GOWORK | GOPATH | GOCACHE | GOMODCACHE | GO_WORKCELL_FUTURE)\n",
			"GO_WORKCELL_FUTURE",
			"workcell-mutant",
		},
		{
			"cgo-loop-removal",
			"  for name in \"${!CGO@}\"; do\n",
			"  for name in WORKCELL_NO_CGO_INPUTS; do\n",
			"CGOWORKCELL_FUTURE",
			"workcell-mutant",
		},
		{
			"external-tool-removal",
			"  for name in CC CXX FC AR GCCGO GCCGOTOOLDIR PKG_CONFIG NETRC GCM_INTERACTIVE; do\n",
			"  for name in CXX FC AR GCCGO GCCGOTOOLDIR PKG_CONFIG NETRC GCM_INTERACTIVE; do\n",
			"CC",
			"/tmp/workcell-mutant-cc",
		},
		{
			"git-index-allowlist",
			"      GIT_NO_REPLACE_OBJECTS | \\\n",
			"      GIT_INDEX_FILE | \\\n        GIT_NO_REPLACE_OBJECTS | \\\n",
			"GIT_INDEX_FILE",
			"/tmp/workcell-mutant-index",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			mutant := filepath.Join(t.TempDir(), "canonical-build-env.sh")
			writeCanonicalMutation(t, realHelper, mutant, tc.original, tc.replacement)
			probe := `
set -euo pipefail
source "$1"
workcell_require_canonical_build_environment
`
			env := map[string]string{tc.variable: tc.value}
			if code, output := canonicalBuildEnvProbeWithHelper(t, realHelper, probe, env); code != 2 {
				t.Fatalf("control did not reject %s: code=%d output=%q", tc.variable, code, output)
			}
			if code, output := canonicalBuildEnvProbeWithHelper(t, mutant, probe, env); code == 2 {
				t.Fatalf("mutant still rejected %s: code=%d output=%q", tc.variable, code, output)
			}
		})
	}
}

func stagedCanonicalEntrypoint(
	tb testing.TB,
	relative string,
	removeGuard bool,
) string {
	tb.Helper()

	root := tb.TempDir()
	realRoot := repoRoot(tb)
	realHelper := filepath.Join(realRoot, "scripts", "lib", "canonical-build-env.sh")
	copyCanonicalFixture(tb, realHelper, filepath.Join(root, "scripts", "lib", "canonical-build-env.sh"))

	content, err := os.ReadFile(filepath.Join(realRoot, filepath.FromSlash(relative)))
	if err != nil {
		tb.Fatal(err)
	}
	text := string(content)
	guardEnd := strings.Index(text, canonicalGuardBlock)
	if guardEnd < 0 {
		tb.Fatalf("%s does not contain the canonical guard block", relative)
	}
	header := text[:guardEnd+len(canonicalGuardBlock)]
	if removeGuard {
		header = strings.Replace(
			header,
			"workcell_require_canonical_build_environment\n",
			"",
			1,
		)
	}
	header += "/bin/bash -c 'if declare -F workcell_descendant >/dev/null; then workcell_descendant; fi'\n"
	header += "printf 'canonical-entrypoint-reached\\n'\n"
	staged := filepath.Join(root, filepath.FromSlash(relative))
	writeCanonicalFixture(tb, staged, []byte(header), 0o755)
	return staged
}

func TestCanonicalBuildEnvironmentDirectEntrypoints(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	realHelper := filepath.Join(root, "scripts", "lib", "canonical-build-env.sh")
	functionDetectorMutant := filepath.Join(t.TempDir(), "canonical-build-env.sh")
	writeCanonicalMutation(
		t,
		realHelper,
		functionDetectorMutant,
		"  if /usr/bin/env | /usr/bin/env -i LC_ALL=C /usr/bin/grep '^BASH_FUNC_' >/dev/null; then\n",
		"  if /usr/bin/true | /usr/bin/false; then\n",
	)
	for _, tc := range canonicalEntrypoints {
		tc := tc
		t.Run(filepath.Base(tc.relative), func(t *testing.T) {
			t.Parallel()
			content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(tc.relative)))
			if err != nil {
				t.Fatal(err)
			}
			text := string(content)
			rootAt := strings.Index(text, `ROOT_DIR="$(CDPATH='' cd -- `)
			guardAt := strings.Index(text, canonicalGuardBlock)
			riskAt := strings.Index(text, tc.firstOperation)
			if !strings.HasPrefix(text, "#!/bin/bash -p\n") ||
				rootAt < 0 ||
				guardAt < rootAt ||
				riskAt < guardAt+len(canonicalGuardBlock) {
				t.Fatalf("%s must run the privileged canonical guard before %q", tc.relative, tc.firstOperation)
			}

			control := stagedCanonicalEntrypoint(t, tc.relative, false)
			mutant := stagedCanonicalEntrypoint(t, tc.relative, true)
			env := map[string]string{tc.variable: tc.value}
			probe := `exec "$2"`
			code, output := canonicalBuildEnvProbe(t, probe, env, control)
			if code != 2 ||
				!strings.Contains(output, tc.variable) ||
				strings.Contains(output, "canonical-entrypoint-reached") {
				t.Fatalf("control %s did not reject %s: code=%d output=%q", tc.relative, tc.variable, code, output)
			}
			code, output = canonicalBuildEnvProbe(t, probe, env, mutant)
			if code != 0 || !strings.Contains(output, "canonical-entrypoint-reached") {
				t.Fatalf("guard-removal mutant for %s was not killed: code=%d output=%q", tc.relative, code, output)
			}

			startupMarker := filepath.Join(t.TempDir(), "startup-ran")
			functionMarker := filepath.Join(t.TempDir(), "function-ran")
			startupFile := filepath.Join(t.TempDir(), "bash-env")
			writeCanonicalFixture(t, startupFile, []byte(`: >"${WORKCELL_DESCENDANT_STARTUP_MARKER:?}"
`), 0o600)
			descendantEnv := map[string]string{
				"BASH_ENV":                            startupFile,
				"ENV":                                 startupFile,
				"BASH_FUNC_workcell_descendant%%":     `() { : >"${WORKCELL_DESCENDANT_FUNCTION_MARKER:?}"; }`,
				"WORKCELL_DESCENDANT_FUNCTION_MARKER": functionMarker,
				"WORKCELL_DESCENDANT_STARTUP_MARKER":  startupMarker,
			}
			if code, output = canonicalBuildEnvProbe(t, probe, descendantEnv, control); code != 2 {
				t.Fatalf("control %s admitted descendant startup code: code=%d output=%q", tc.relative, code, output)
			}
			for _, marker := range []string{startupMarker, functionMarker} {
				if _, err := os.Stat(marker); !os.IsNotExist(err) {
					t.Fatalf("control %s executed descendant marker: %s", tc.relative, marker)
				}
			}
			if code, output = canonicalBuildEnvProbe(t, probe, descendantEnv, mutant); code != 0 {
				t.Fatalf("guard mutant %s did not reach descendant: code=%d output=%q", tc.relative, code, output)
			}
			for _, marker := range []string{startupMarker, functionMarker} {
				if _, err := os.Stat(marker); err != nil {
					t.Fatalf("guard mutant %s did not execute descendant marker: %v", tc.relative, err)
				}
			}

			if err := os.Remove(functionMarker); err != nil {
				t.Fatal(err)
			}
			functionEnv := map[string]string{
				"BASH_FUNC_workcell_descendant%%":     descendantEnv["BASH_FUNC_workcell_descendant%%"],
				"WORKCELL_DESCENDANT_FUNCTION_MARKER": functionMarker,
			}
			if code, output = canonicalBuildEnvProbe(t, probe, functionEnv, control); code != 2 {
				t.Fatalf("control %s admitted a raw exported function: code=%d output=%q", tc.relative, code, output)
			}
			if _, err := os.Stat(functionMarker); !os.IsNotExist(err) {
				t.Fatalf("control %s executed a raw exported function; stat error=%v", tc.relative, err)
			}
			detectorMutant := stagedCanonicalEntrypoint(t, tc.relative, false)
			copyCanonicalFixture(t, functionDetectorMutant, filepath.Join(filepath.Dir(detectorMutant), "lib", "canonical-build-env.sh"))
			if code, output = canonicalBuildEnvProbe(t, probe, functionEnv, detectorMutant); code != 0 {
				t.Fatalf("function-detector mutant %s did not reach descendant: code=%d output=%q", tc.relative, code, output)
			}
			if _, err := os.Stat(functionMarker); err != nil {
				t.Fatalf("function-detector mutant %s was not killed: %v", tc.relative, err)
			}
		})
	}

	for _, relative := range []string{"scripts/dev-quick-check.sh", "scripts/validate-repo.sh"} {
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(content), `${ROOT_DIR}/scripts/lib/canonical-build-env.sh`) {
			t.Fatalf("%s must lint and format the canonical helper", relative)
		}
	}

	// A direct bare-name invocation goes through PATH resolution before the
	// kernel applies the privileged shebang. Reaching the guard with the helper
	// found at the staged root proves BASH_SOURCE retained that resolved path.
	staged := stagedCanonicalEntrypoint(t, "scripts/dev-quick-check.sh", false)
	path := filepath.Dir(staged) + string(os.PathListSeparator) + os.Getenv("PATH")
	code, output := canonicalBuildEnvProbe(
		t,
		`exec dev-quick-check.sh`,
		map[string]string{
			"PATH":    path,
			"GOFLAGS": "-overlay=/tmp/workcell-bare-name-overlay.json",
		},
	)
	if code != 2 || !strings.Contains(output, "GOFLAGS") {
		t.Fatalf("bare-name direct entrypoint did not resolve the canonical root: code=%d output=%q", code, output)
	}
}

func TestCanonicalBuildEnvironmentPrivilegedEntrypoint(t *testing.T) {
	t.Parallel()

	script := filepath.Join(repoRoot(t), "scripts", "validate-repo.sh")
	bashEnvMarker := filepath.Join(t.TempDir(), "bash-env-ran")
	bashEnv := filepath.Join(t.TempDir(), "bash-env")
	writeCanonicalFixture(t, bashEnv, []byte(`: >"${WORKCELL_BASH_ENV_MARKER:?}"
`), 0o600)

	code, output := canonicalBuildEnvProbe(
		t,
		`exec "$2" --help`,
		map[string]string{
			"BASH_ENV":                 bashEnv,
			"WORKCELL_BASH_ENV_MARKER": bashEnvMarker,
			"GIT_INDEX_FILE":           "/tmp/workcell-hostile-index",
			"BASH_FUNC_source%%":       "() { :; }",
			"BASH_FUNC_workcell_require_canonical_build_environment%%": "() { :; }",
		},
		script,
	)
	if code != 2 || !strings.Contains(output, "BASH_ENV") {
		t.Fatalf("privileged entrypoint did not reject before help: code=%d output=%q", code, output)
	}
	if _, err := os.Stat(bashEnvMarker); !os.IsNotExist(err) {
		t.Fatalf("privileged entrypoint executed BASH_ENV; stat error=%v", err)
	}

	if code, output := canonicalBuildEnvProbe(t, `exec /bin/bash "$2" --help`, nil, script); code != 2 ||
		!strings.Contains(output, "execute the script directly") {
		t.Fatalf("nonprivileged interpreter did not fail closed: code=%d output=%q", code, output)
	}
}

func TestCanonicalBuildEnvironmentScopeAndReachability(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	checks := map[string][]string{
		".github/workflows/release.yml": {"./scripts/validate-repo.sh"},
		"scripts/bootstrap-dev.sh":      {`"${ROOT_DIR}/scripts/dev-quick-check.sh"`},
		"scripts/run-hosted-controls-audit.sh": {
			`"${ROOT_DIR}/scripts/verify-github-hosted-controls.sh"`,
		},
	}
	for relative, needles := range checks {
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
		if err != nil {
			t.Fatal(err)
		}
		for _, needle := range needles {
			if !strings.Contains(string(content), needle) {
				t.Fatalf("%s must reach the canonical root through %q", relative, needle)
			}
		}
	}

	content, err := os.ReadFile(filepath.Join(root, "docs", "validation-scenarios.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	for _, want := range []string{
		"G0a1a1 canonical-build-environment",
		"three roots",
		"`scripts/lib/canonical-build-env.sh`",
		"exact `GOENV=off`",
		"exact `GOWORK=off`",
		"`GOPATH`, `GOCACHE`, and `GOMODCACHE`",
		"`GOROOT_<major>_<minor>_{X64,ARM64}`",
		"removed before any descendant runs",
		"near-matches still fail closed",
		"explicitly lower assurance",
		"G0a1a2",
		"G0a1b",
		"G0a1c",
		"G0a2a",
		"G0a2",
		"G0b",
		"arbitrary direct `go` invocations",
		"whole local administrative plane",
		"`.git/info/attributes`",
		"credential files",
		"`HOME` (including `.netrc`)",
		"`PATH`",
		"tool binaries",
		"`BASH_ENV`",
		"clears `CDPATH`",
		"`CGO*`",
		"`BASH_FUNC_*`",
		"`SHELLOPTS`",
		"`BASHOPTS`",
		"`BASH_XTRACEFD`",
		"descendant `CDPATH`",
		"resident-only base selection",
		"Release certification stays blocked",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("validation scenario documentation must contain %q", want)
		}
	}
}
