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

// controlPlaneShadowProbe defines every function of scripts/workcell, stubs
// the host steps that need Colima or Go, and runs
// prepare_workspace_control_plane_shadow on a fixture that the setup lines
// build in the workspace. It prints each masked or mounted destination.
const controlPlaneShadowProbe = `set -euo pipefail
eval "$(sed -n -e '/^[a-z_][a-z0-9_]*() {$/,/^}$/p' -e '/^WORKSPACE_CONTROL_PLANE_DIRS=($/,/^)$/p' -e '/^WORKSPACE_CONTROL_PLANE_FILES=($/,/^)$/p' -e '/^WORKSPACE_CONTROL_PLANE_NESTED_FILES=(/p' -e '/^WORKSPACE_MCP_FILE_SURFACES=(/p' -e '/^SESSION_WORKSPACES_GIT_ENTRY=/p' "${PROBE_SCRIPT}")"
run_clean_host_command() { "$@"; }
colima_staging_cache_root() { printf '%%s\n' "${PROBE_SHADOW}"; }
prepare_colima_staging_cache_roots() { mkdir -p "${PROBE_SHADOW}/workcell-shadow"; }
ensure_session_clone_dir() { :; }
HOST_GIT_BIN=git
ALLOW_CONTROL_PLANE_VCS="${PROBE_ACV}"
ALLOW_REPO_MCP=0
cd "${PROBE_WORKSPACE}"
git init -q
git config user.email probe@example.com
git config user.name probe
%s
prepare_workspace_control_plane_shadow "${PROBE_WORKSPACE}" strict
printf '%%s\n' "${SHADOW_DEST_PATHS[@]}"
`

func runControlPlaneShadowProbe(t *testing.T, setup string, acv bool) (int, string, string) {
	t.Helper()
	root := t.TempDir()
	// A space, a dollar sign and a backtick in the workspace path must not
	// change what the walks find.
	workspace := filepath.Join(root, "work space $x `y`")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "outside")
	if err := os.Mkdir(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	flag := "0"
	if acv {
		flag = "1"
	}
	code, output := runBashProbe(t, fmt.Sprintf(controlPlaneShadowProbe, setup), map[string]string{
		"PROBE_SCRIPT":    filepath.Join(repoRoot(t), "scripts", "workcell"),
		"PROBE_WORKSPACE": workspace,
		"PROBE_SHADOW":    filepath.Join(root, "shadow"),
		"PROBE_OUTSIDE":   outside,
		"PROBE_ACV":       flag,
	})
	return code, output, workspace
}

// TestWorkspaceControlPlaneSlashEntriesMaskAtAnyDepth covers the slash entries
// of WORKSPACE_CONTROL_PLANE_DIRS below the top: an untracked directory at
// any depth is masked, or mounted read-only with --allow-control-plane-vcs,
// as the top-level one is. The match ignores case, as APFS does, so
// c/.Agents/Plugins is the same directory as c/.agents/plugins there.
func TestWorkspaceControlPlaneSlashEntriesMaskAtAnyDepth(t *testing.T) {
	t.Parallel()

	const setup = `mkdir -p x/.agents/plugins/p a/b/.github/hooks .agents/skills/s
printf '{}\n' >x/.agents/plugins/p/hooks.json
printf 'x\n' >a/b/.github/hooks/h
printf 'x\n' >.agents/skills/s/SKILL.md
mkdir -p c/.Agents/Plugins/p d/.GITHUB/Agents
mkdir -p y/x.agents/plugins y/.agents/pluginsx`
	for _, acv := range []bool{false, true} {
		code, output, _ := runControlPlaneShadowProbe(t, setup, acv)
		if code != 0 {
			t.Fatalf("acv=%v: exit %d: %s", acv, code, output)
		}
		for _, want := range []string{"/workspace/x/.agents/plugins\n", "/workspace/a/b/.github/hooks\n", "/workspace/.agents/skills\n", "/workspace/c/.Agents/Plugins\n", "/workspace/d/.GITHUB/Agents\n"} {
			if !strings.Contains(output, want) {
				t.Errorf("acv=%v: %q not masked:\n%s", acv, strings.TrimSpace(want), output)
			}
		}
		for _, absent := range []string{"/workspace/y/x.agents/plugins\n", "/workspace/y/.agents/pluginsx\n"} {
			if strings.Contains(output, absent) {
				t.Errorf("acv=%v: look-alike %q masked:\n%s", acv, strings.TrimSpace(absent), output)
			}
		}
	}
}

// TestWorkspaceControlPlaneRefusesRedirectedSlashEntries covers a parent
// symlink of a slash control path. The launch stops when the path can show in
// the session: present on the host or in the index, or present where the
// container resolves the parent, including a link that dangles on the host.
// An absent path below a link that resolves on both sides stays legal.
func TestWorkspaceControlPlaneRefusesRedirectedSlashEntries(t *testing.T) {
	t.Parallel()

	const refused = "parent component is a symlink"
	for name, test := range map[string]struct {
		setup  string
		refuse bool
	}{
		"top link that dangles on the host":        {"mkdir -p evil/plugins/q\nln -s /workspace/evil .agents", true},
		"nested link that dangles on the host":     {"mkdir -p x evil/skills\nln -s /workspace/evil x/.agents", true},
		"nested link in another case":              {"mkdir -p x evil/plugins\nln -s /workspace/evil x/.AGENTS", true},
		"github link to a hooks directory":         {"mkdir -p gh/hooks\nln -s /workspace/gh .github", true},
		"github link to an instructions file":      {"mkdir -p gh\nprintf 'x\\n' >gh/copilot-instructions.md\nln -s /workspace/gh .github", true},
		"github link to an MCP file":               {"mkdir -p gh\nprintf '{}\\n' >gh/mcp.json\nln -s /workspace/gh .github", true},
		"link to the workspace root":               {"mkdir -p plugins\nln -s /workspace .agents", true},
		"two links, the second absolute":           {"mkdir -p evil/skills\nln -s /workspace/evil a\nln -s a .agents", true},
		"relative link that resolves inside":       {"mkdir -p real/plugins\nln -s real .agents", true},
		"link through proc":                        {"ln -s /proc/self/cwd/evil .agents", true},
		"link that climbs back into the workspace": {"mkdir -p evil/plugins\nln -s /tmp/../workspace/evil .agents", true},
		"newline in a parent name":                 {"mkdir -p evil/plugins $'a\\nb'\nln -s /workspace/evil $'a\\nb/.agents'", true},
		"newline that ends a link target":          {"mkdir -p evil $'evil\\n/plugins'\nln -s $'/workspace/evil\\n' .agents", true},
		"link loop":                                {"ln -s .agents .agents", true},
		"tracked path under a later link": {
			"mkdir -p x/.agents/plugins\nprintf 'x\\n' >x/.agents/plugins/f\ngit add -A\nrm -rf x/.agents\nmkdir -p empty\nln -s /workspace/empty x/.agents", true,
		},
		"leaf link under a link":             {"mkdir -p evil\nln -s /etc evil/plugins\nln -s /workspace/evil .agents", true},
		"absent path under a dangling link":  {"mkdir -p evil\nln -s /workspace/evil .agents", false},
		"absent path under an outside link":  {`ln -s "${PROBE_OUTSIDE}" .github`, false},
		"present path under an outside link": {`printf '{}\n' >"${PROBE_OUTSIDE}/mcp.json"` + "\n" + `ln -s "${PROBE_OUTSIDE}" .github`, true},
		"absent path under an inside link":   {"mkdir -p real\nln -s real .agents", false},
		"link that leaves the workspace":     {"ln -s /etc .agents", false},
	} {
		for _, acv := range []bool{false, true} {
			code, output, _ := runControlPlaneShadowProbe(t, test.setup, acv)
			switch {
			case test.refuse && (code != 2 || !strings.Contains(output, refused)):
				t.Errorf("%s (acv=%v): exit %d, want a refusal:\n%s", name, acv, code, output)
			case !test.refuse && code != 0:
				t.Errorf("%s (acv=%v): exit %d, want a launch:\n%s", name, acv, code, output)
			}
		}
	}
}

// TestWorkspaceControlPlaneRefusesDanglingControlDirLinkWithVCS covers a
// control directory that is itself a link which dangles on the host: with
// --allow-control-plane-vcs the session would read the link target writable.
func TestWorkspaceControlPlaneRefusesDanglingControlDirLinkWithVCS(t *testing.T) {
	t.Parallel()

	for _, setup := range []string{"mkdir -p evil\nln -s /workspace/evil .codex", "mkdir -p evil x\nln -s /workspace/evil x/.claude", "mkdir -p .agents evil\nln -s /workspace/evil .agents/plugins"} {
		code, output, _ := runControlPlaneShadowProbe(t, setup, true)
		if code != 2 || !strings.Contains(output, "refuses symlinked workspace control directories") {
			t.Errorf("%q: exit %d, want a refusal:\n%s", setup, code, output)
		}
	}
}

// TestWorkspaceControlPlaneSlashDirWalkFailsClosed covers the walk status: a
// workspace that cannot be read is not an empty inventory.
func TestWorkspaceControlPlaneSlashDirWalkFailsClosed(t *testing.T) {
	t.Parallel()

	probe := fmt.Sprintf(`set -euo pipefail
eval "$(sed -n -e '/^WORKSPACE_CONTROL_PLANE_DIRS=($/,/^)$/p' -e '/^workspace_control_plane_slash_dir_paths() {$/,/^}$/p' %s)"
if workspace_control_plane_slash_dir_paths %s >/dev/null 2>&1; then
  echo "a failed walk returned 0"
fi
`, ShellQuote(filepath.Join(repoRoot(t), "scripts", "workcell")), ShellQuote(filepath.Join(t.TempDir(), "missing")))
	if code, output := runBashProbe(t, probe, nil); code != 0 || output != "" {
		t.Fatalf("exit %d: %s", code, output)
	}
}

// TestWorkspaceControlPlaneSlashDirWalkIsOneFind covers the cost of the slash
// list: one walk of the workspace finds the entries and the links at their
// leading parts, whatever the number of slash entries.
func TestWorkspaceControlPlaneSlashDirWalkIsOneFind(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	probe := fmt.Sprintf(`set -euo pipefail
eval "$(sed -n -e '/^WORKSPACE_CONTROL_PLANE_DIRS=($/,/^)$/p' -e '/^workspace_control_plane_slash_dir_paths() {$/,/^}$/p' %s)"
cd %s
find() { echo walk >>walks; command find "$@"; }
mkdir -p a/.Github/Hooks evil/plugins
ln -s ../evil a/.agents
workspace_control_plane_slash_dir_paths "$PWD" >paths
tr '\0' '\n' <paths | LC_ALL=C sort
echo "walks=$(wc -l <walks | tr -d ' ')"
`, ShellQuote(filepath.Join(repoRoot(t), "scripts", "workcell")), ShellQuote(workspace))
	code, output := runBashProbe(t, probe, nil)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, output)
	}
	for _, want := range []string{"a/.Github/Hooks\n", "a/.agents/plugins\n", "a/.agents/skills\n", "walks=1\n"} {
		if !strings.Contains(output, want) {
			t.Errorf("missing %q:\n%s", strings.TrimSpace(want), output)
		}
	}
}
