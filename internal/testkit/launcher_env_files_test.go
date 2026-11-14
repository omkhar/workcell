// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package testkit

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// launcherFunctionsProbe defines every function of scripts/workcell.
const launcherFunctionsProbe = `set -euo pipefail
eval "$(sed -n -e '/^[a-z_][a-z0-9_]*() {$/,/^}$/p' "${PROBE_SCRIPT}")"
`

func runLauncherFunctionsProbe(t *testing.T, body string, env map[string]string) (int, string) {
	t.Helper()
	if env == nil {
		env = map[string]string{}
	}
	env["PROBE_SCRIPT"] = filepath.Join(repoRoot(t), "scripts", "workcell")
	return runBashProbe(t, launcherFunctionsProbe+body, env)
}

// TestSessionMonitorStateFileRoundTrips writes the detached monitor state with
// hostile values and reads it back: each value comes back the same, and none
// runs as code.
func TestSessionMonitorStateFileRoundTrips(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	code, output := runLauncherFunctionsProbe(t, `cd "${PROBE_DIR}"
values=("plain" "with space" "quote'd \"double\"" '$(touch pwned-a)' '`+"`touch pwned-b`"+`' $'new\nline' $'tab\tand\001ctl' "é ü" "" "~home" "#hash" "a=b;c" '\back\slash' "trailing'")
names=(SESSION_ID WORKSPACE CONTAINER_NAME COLIMA_PROFILE SESSION_AUDIT_DIR ALLOW_ENDPOINTS DEBUG_LOG_PATH WORKCELL_STATE_ROOT MODE UI XDG_STATE_HOME EXECUTION_PATH AGENT LOG_LEVEL)
for i in "${!names[@]}"; do printf -v "${names[i]}" '%s' "${values[i]}"; done
LC_ALL=C.UTF-8 write_session_monitor_env_file "${PROBE_DIR}/state.env"
for name in "${names[@]}"; do unset "${name}"; done
read_session_monitor_env_file "${PROBE_DIR}/state.env"
for i in "${!names[@]}"; do
  [[ "${!names[i]}" == "${values[i]}" ]] || printf 'mismatch %s: [%s]\n' "${names[i]}" "${!names[i]}"
done
ls pwned-* 2>/dev/null || true
echo done
`, map[string]string{"PROBE_DIR": dir})
	if code != 0 || strings.TrimSpace(output) != "done" {
		t.Fatalf("exit %d:\n%s", code, output)
	}
}

// TestSessionMonitorStateFileRefusesOtherText covers a state file that holds
// more than the writer writes: code, an unknown or repeated name, a word that
// printf %q does not give back, and a cut last line. Each stops the read, and
// no code runs.
func TestSessionMonitorStateFileRefusesOtherText(t *testing.T) {
	t.Parallel()

	for name, text := range map[string]string{
		"a command":                "SESSION_ID=a\ntouch pwned\n",
		"a substitution":           "SESSION_ID=$(touch pwned)\n",
		"a second command":         "SESSION_ID=a;touch pwned\n",
		"a function":               "go_hostutil() { touch pwned; }\n",
		"an unknown name":          "PATH=/tmp\n",
		"the launcher root":        "ROOT_DIR=/tmp/evil\n",
		"a repeated name":          "SESSION_ID=a\nSESSION_ID=b\n",
		"an unquoted space":        "SESSION_ID=a b\n",
		"a cut last line":          "SESSION_ID=a\nMODE=strict",
		"a blank line":             "SESSION_ID=a\n\n",
		"an open ANSI-C quote":     "SESSION_ID=$'a\n",
		"an ANSI-C stop":           "SESSION_ID=$'a\\cb'\n",
		"a lower-case name":        "session_id=a\n",
		"a name with an index":     "SESSION_ID[0]=a\n",
		"a double-quoted value":    "SESSION_ID=\"a\"\n",
		"a single-quoted value":    "SESSION_ID='a b'\n",
		"a name with an expansion": "${X}=a\n",
	} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "state.env"), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		code, output := runLauncherFunctionsProbe(t, `cd "${PROBE_DIR}"
if read_session_monitor_env_file "${PROBE_DIR}/state.env"; then echo accepted; fi
ls pwned 2>/dev/null || true
`, map[string]string{"PROBE_DIR": dir})
		if code != 0 || strings.TrimSpace(output) != "" {
			t.Errorf("%s: exit %d:\n%s", name, code, output)
		}
	}
}

// TestRuntimeProfilesAreReadNotSourced reads each runtime profile and refuses
// a line that sourcing would run as code.
func TestRuntimeProfilesAreReadNotSourced(t *testing.T) {
	t.Parallel()

	profiles, err := filepath.Glob(filepath.Join(repoRoot(t), "runtime", "profiles", "*.env"))
	if err != nil || len(profiles) == 0 {
		t.Fatalf("profiles = %v, %v", profiles, err)
	}
	for _, profile := range profiles {
		data, err := os.ReadFile(profile)
		if err != nil {
			t.Fatal(err)
		}
		want := strings.ReplaceAll(string(data), `"`, "")
		code, output := runLauncherFunctionsProbe(t, `read_runtime_profile_env "${PROBE_PROFILE}"
for name in COLIMA_CPU COLIMA_MEMORY COLIMA_DISK CONTAINER_MEMORY NETWORK_POLICY EXTRA_ENDPOINTS; do
  printf '%s=%s\n' "${name}" "${!name}"
done
`, map[string]string{"PROBE_PROFILE": profile})
		if code != 0 || output != want {
			t.Errorf("%s: exit %d:\n%s\nwant:\n%s", filepath.Base(profile), code, output, want)
		}
	}

	for name, text := range map[string]string{
		"a command":             "COLIMA_CPU=4\ntouch pwned\n",
		"a substitution":        "COLIMA_CPU=$(touch pwned)\n",
		"a quoted substitution": "EXTRA_ENDPOINTS=\"$(touch pwned)\"\n",
		"a backtick":            "EXTRA_ENDPOINTS=\"`touch pwned`\"\n",
		"an unquoted space":     "EXTRA_ENDPOINTS=a touch\n",
		"a second command":      "COLIMA_CPU=4;touch pwned\n",
		"an unknown name":       "PATH=/tmp\n",
		"a function":            "go_hostutil() { touch pwned; }\n",
		"a cut last line":       "COLIMA_CPU=4",
		"a stray quote":         "EXTRA_ENDPOINTS=\"a\"b\"\n",
		"a repeated name":       "COLIMA_CPU=4\nCOLIMA_CPU=8\n",
	} {
		dir := t.TempDir()
		profile := filepath.Join(dir, "hostile.env")
		if err := os.WriteFile(profile, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		code, output := runLauncherFunctionsProbe(t, fmt.Sprintf(`cd %s
if read_runtime_profile_env "${PROBE_PROFILE}"; then echo accepted; fi
ls pwned 2>/dev/null || true
`, ShellQuote(dir)), map[string]string{"PROBE_PROFILE": profile})
		if code != 0 || strings.TrimSpace(output) != "" {
			t.Errorf("%s: exit %d:\n%s", name, code, output)
		}
	}
}

// TestOSReleaseValueMatchesTheFile reads the host's /etc/os-release with the
// launcher's parser and with a sourcing subshell: the values agree.
func TestOSReleaseValueMatchesTheFile(t *testing.T) {
	t.Parallel()

	if _, err := os.Stat("/etc/os-release"); err != nil {
		t.Skip("no /etc/os-release on this host")
	}
	code, output := runBashProbe(t, `set -euo pipefail
eval "$(sed -n -e '/^os_release_value() {$/,/^}$/p' "${PROBE_LIBRARY}")"
for key in ID VERSION_ID VERSION_CODENAME NAME; do
  want="$(unset ID VERSION_ID VERSION_CODENAME NAME; . /etc/os-release; printf '%s' "${!key-}")"
  got="$(os_release_value "${key}")"
  [[ "${got}" == "${want}" ]] || printf '%s: got [%s], want [%s]\n' "${key}" "${got}" "${want}"
done
`, map[string]string{"PROBE_LIBRARY": filepath.Join(repoRoot(t), "scripts", "lib", "launcher", "host-detect.sh")})
	if code != 0 || output != "" {
		t.Fatalf("exit %d:\n%s", code, output)
	}
}
