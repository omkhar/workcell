// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package adapters

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// loadUncertifiedFixture returns the repository manifests plus the
// testdata uncertified adapter zz-fixture, which no build ships.
func loadUncertifiedFixture(t *testing.T) []Manifest {
	t.Helper()
	fixture, err := LoadManifests(filepath.Join("testdata", "uncertified"))
	if err != nil {
		t.Fatal(err)
	}
	if len(fixture) != 1 || fixture[0].Tier != "uncertified" {
		t.Fatalf("testdata/uncertified must hold one uncertified manifest, got %+v", fixture)
	}
	return append(loadRepoManifests(t), fixture...)
}

// runRuntimePolicy sources the runtime shell rendered from manifests and
// runtime/container/provider-policy.sh, then runs fn with args. It returns the
// exit code and stderr.
func runRuntimePolicy(t *testing.T, manifests []Manifest, fn string, args ...string) (int, string) {
	t.Helper()
	rendered, err := RenderRuntimeShell(manifests)
	if err != nil {
		t.Fatal(err)
	}
	generated := filepath.Join(t.TempDir(), "generated-adapters.sh")
	if err := os.WriteFile(generated, rendered, 0o600); err != nil {
		t.Fatal(err)
	}
	const script = `source "$1"; source "$2"; fn="$3"; shift 3; "${fn}" "$@"`
	cmd := exec.Command("bash", append([]string{"--noprofile", "--norc", "-c", script, "bash", generated, filepath.Join(repoRoot, "runtime", "container", "provider-policy.sh"), fn}, args...)...)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	err = cmd.Run()
	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) {
		t.Fatal(err)
	}
	return cmd.ProcessState.ExitCode(), stderr.String()
}

// TestRejectManifestArgsAllowsOnlyTheManifestAllowlist runs provider args of
// the uncertified fixture through reject_manifest_args: an option passes only
// when [flags].allow names it, and a word that names other_commands fails.
func TestRejectManifestArgsAllowsOnlyTheManifestAllowlist(t *testing.T) {
	manifests := loadUncertifiedFixture(t)
	for _, tc := range []struct {
		args []string
		want string // "" passes; otherwise the refusal text
	}{
		{nil, ""},
		{[]string{"--model", "x"}, ""},
		{[]string{"--model=x"}, ""},
		{[]string{"-m", "x", "-h"}, ""},
		{[]string{"run", "fix the test"}, ""},
		{[]string{"--yolo"}, "Workcell blocked a denied zz-fixture option: --yolo"},
		{[]string{"--yolo=1"}, "denied zz-fixture option: --yolo"},
		{[]string{"-y"}, "denied zz-fixture option: -y"},
		{[]string{"--model", "x", "--yolo"}, "denied zz-fixture option: --yolo"},
		{[]string{"--unlisted"}, "Workcell blocked a zz-fixture option that its manifest does not allow: --unlisted"},
		{[]string{"--Model", "x"}, "does not allow: --Model"},
		{[]string{"-mx"}, "does not allow: -mx"},
		{[]string{"-my"}, "does not allow: -my"},
		{[]string{"--model x"}, "does not allow: --model x"},
		{[]string{"--"}, "does not allow: --"},
		{[]string{"-"}, "does not allow: -"},
		{[]string{"serve"}, "Workcell blocked a zz-fixture command that its manifest does not allow: serve"},
		{[]string{"--model", "mcp"}, "command that its manifest does not allow: mcp"},
	} {
		code, stderr := runRuntimePolicy(t, manifests, "reject_manifest_args", append([]string{"zz-fixture"}, tc.args...)...)
		if tc.want == "" {
			if code != 0 {
				t.Errorf("reject_manifest_args %q exited %d, want 0: %s", tc.args, code, stderr)
			}
		} else if code != 2 || !strings.Contains(stderr, tc.want) {
			t.Errorf("reject_manifest_args %q exited %d with %q, want exit 2 with %q", tc.args, code, stderr, tc.want)
		}
	}
}

// TestRejectManifestArgsFailsClosed refuses an adapter without manifest flag
// data, and routes a provider without a reviewed policy to the manifest
// allowlist only when its tier is uncertified.
func TestRejectManifestArgsFailsClosed(t *testing.T) {
	manifests := loadUncertifiedFixture(t)
	for _, tc := range []struct {
		fn   string
		args []string
		want string
	}{
		{"reject_manifest_args", []string{"codex"}, "Workcell has no manifest [flags] allow list for adapter: codex"},
		{"reject_manifest_args", []string{"no-such-agent", "--help"}, "no manifest [flags] allow list for adapter: no-such-agent"},
		{"validate_command_args", []string{"no-such-agent", "no-such-agent", "--help"}, "Workcell has no provider policy for: no-such-agent"},
		{"validate_command_args", []string{"zz-fixture", "zz-fixture", "--yolo"}, "denied zz-fixture option: --yolo"},
		{"validate_command_args", []string{"zz-fixture", "zz-fixture", "serve"}, "zz-fixture command that its manifest does not allow: serve"},
	} {
		if code, stderr := runRuntimePolicy(t, manifests, tc.fn, tc.args...); code != 2 || !strings.Contains(stderr, tc.want) {
			t.Errorf("%s %q exited %d with %q, want exit 2 with %q", tc.fn, tc.args, code, stderr, tc.want)
		}
	}
	if code, stderr := runRuntimePolicy(t, manifests, "validate_command_args", "zz-fixture", "zz-fixture", "run", "--model", "x"); code != 0 {
		t.Errorf("validate_command_args refused allowed uncertified args (exit %d): %s", code, stderr)
	}
	// A manifest list that the generated data lacks stops the launch.
	broken := loadRepoManifests(t)
	if code, stderr := runRuntimePolicy(t, broken, "reject_manifest_args", "zz-fixture", "--help"); code != 2 || !strings.Contains(stderr, "no manifest [flags] allow list for adapter: zz-fixture") {
		t.Errorf("reject_manifest_args without generated data exited %d with %q", code, stderr)
	}
}

// TestRenderRuntimeShellUncertifiedTables pins the tier and [flags] tables of
// the runtime shell for an uncertified manifest, and their absence for a
// certified one.
func TestRenderRuntimeShellUncertifiedTables(t *testing.T) {
	rendered, err := RenderRuntimeShell(loadUncertifiedFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"    claude | codex | copilot | gemini)\n      echo certified\n",
		"    zz-fixture)\n      echo uncertified\n",
		"    zz-fixture:allow)\n      printf '%s\\n' '-h' '--help' '-m' '--model'\n",
		"    zz-fixture:deny)\n      printf '%s\\n' '-y' '--yolo'\n",
		"    zz-fixture:other_commands)\n      printf '%s\\n' 'serve' 'mcp'\n",
	} {
		if !strings.Contains(string(rendered), want) {
			t.Errorf("runtime shell lacks %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(string(rendered), "codex:allow") {
		t.Error("runtime shell must not render [flags] lists of a certified adapter")
	}
}

// TestCommittedRuntimeShellAdapterTier asks the committed runtime shell for
// the tier of every manifest id: a planned or unknown id has none.
func TestCommittedRuntimeShellAdapterTier(t *testing.T) {
	for _, m := range loadRepoManifests(t) {
		cmd := exec.Command("bash", "--noprofile", "--norc", "-c", `source "$1" && workcell_adapter_tier "$2"`, "bash", filepath.Join(repoRoot, "runtime", "container", "generated-adapters.sh"), m.ID)
		cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
		out, err := cmd.Output()
		want := m.Tier + "\n"
		if m.Tier == "planned" {
			want = ""
		}
		if string(out) != want || (err == nil) != (m.Tier != "planned") {
			t.Errorf("%s: workcell_adapter_tier = %q (%v), want %q", m.ID, out, err, want)
		}
	}
}
