// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package adapters

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
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

// TestCommittedRuntimeShellAdapterTier asks each committed generated shell for
// the tier of every manifest id: a planned or unknown id has none.
func TestCommittedRuntimeShellAdapterTier(t *testing.T) {
	for _, script := range []string{"runtime/container/generated-adapters.sh", "scripts/lib/launcher/generated-adapters.sh"} {
		for _, m := range append(loadRepoManifests(t), Manifest{ID: "no-such-agent", Tier: "planned"}) {
			cmd := exec.Command("bash", "--noprofile", "--norc", "-c", `source "$1" && workcell_adapter_tier "$2"`, "bash", filepath.Join(repoRoot, script), m.ID)
			cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
			out, err := cmd.Output()
			want := m.Tier + "\n"
			if m.Tier == "planned" {
				want = ""
			}
			if string(out) != want || (err == nil) != (m.Tier != "planned") {
				t.Errorf("%s: workcell_adapter_tier %s = %q (%v), want %q", script, m.ID, out, err, want)
			}
		}
	}
}

// TestAdapterAssuranceMapping runs workcell_adapter_assurance in
// runtime/container/assurance.sh over each tier and option value.
func TestAdapterAssuranceMapping(t *testing.T) {
	for _, tc := range []struct{ tier, allow, out, refusal string }{
		{"certified", "0", "managed-certified-adapter\n", ""},
		{"uncertified", "1", "lower-assurance-uncertified-adapter\n", ""},
		{"uncertified", "0", "", "Workcell adapter demo is uncertified: no review certified its CLI. Add --allow-uncertified-agent"},
		{"certified", "1", "", "--allow-uncertified-agent applies only to an uncertified adapter; demo is certified."},
		{"planned", "1", "", "Unsupported Workcell adapter tier for assurance mapping: demo tier=planned"},
		{"", "0", "", "Unsupported Workcell adapter tier for assurance mapping: demo tier= allow=0"},
		{"uncertified", "", "", "Unsupported Workcell adapter tier for assurance mapping: demo tier=uncertified allow="},
	} {
		cmd := exec.Command("bash", "--noprofile", "--norc", "-c", `source "$1"; shift; workcell_adapter_assurance "$@"`, "bash", filepath.Join(repoRoot, "runtime", "container", "assurance.sh"), "demo", tc.tier, tc.allow)
		cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if string(out) != tc.out || (err == nil) != (tc.refusal == "") || !strings.Contains(stderr.String(), tc.refusal) {
			t.Errorf("workcell_adapter_assurance demo %q %q = %q, %v, %q; want %q and refusal %q", tc.tier, tc.allow, out, err, stderr.String(), tc.out, tc.refusal)
		}
	}
}

// launcherRun runs scripts/workcell in root with a scratch HOME and a scratch
// git workspace, and returns the exit code, stdout, and stderr.
func launcherRun(t *testing.T, root string, args ...string) (int, string, string) {
	t.Helper()
	home, workspace := t.TempDir(), t.TempDir()
	if out, err := exec.Command("git", "-C", workspace, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	cmd := exec.Command("bash", append([]string{"--noprofile", "--norc", "scripts/workcell"}, append(args, "--workspace", workspace, "--no-default-injection-policy")...)...)
	cmd.Dir = root
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "XDG_CONFIG_HOME=" + filepath.Join(home, ".config")}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) {
		t.Fatal(err)
	}
	return cmd.ProcessState.ExitCode(), stdout.String(), stderr.String()
}

// repoCopyWithUncertifiedFixture copies the tracked and new files of the
// repository, adds the testdata uncertified manifest to adapters/, and
// regenerates every adapter file, as a new uncertified agent would.
func repoCopyWithUncertifiedFixture(t *testing.T) string {
	t.Helper()
	listing, err := exec.Command("git", "-C", repoRoot, "ls-files", "-z", "--cached", "--others", "--exclude-standard").Output()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	for _, rel := range strings.Split(strings.TrimSuffix(string(listing), "\x00"), "\x00") {
		copyRepoEntry(t, rel, root)
	}
	fixture, err := os.ReadFile(filepath.Join("testdata", "uncertified", "zz-fixture", "adapter.toml"))
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(root, "adapters", "zz-fixture", "adapter.toml"), fixture, 0o644)
	manifests, err := LoadManifests(filepath.Join(root, "adapters"))
	if err != nil {
		t.Fatal(err)
	}
	for target, path := range map[string]string{
		"data":           "internal/adapters/data_gen.go",
		"providerid":     "internal/providerid/providerid_gen.go",
		"launcher-shell": "scripts/lib/launcher/generated-adapters.sh",
		"runtime-shell":  "runtime/container/generated-adapters.sh",
		"launcher-rust":  "runtime/container/rust/src/generated_targets.rs",
	} {
		rendered, err := GenTargets[target](manifests)
		if err != nil {
			t.Fatal(err)
		}
		writeTestFile(t, filepath.Join(root, path), rendered, 0o644)
	}
	return root
}

func copyRepoEntry(t *testing.T, rel, root string) {
	t.Helper()
	src := filepath.Join(repoRoot, rel)
	info, err := os.Lstat(src)
	if errors.Is(err, fs.ErrNotExist) {
		return // deleted in the working tree
	}
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		target, err := os.Readlink(src)
		if err == nil {
			err = os.Symlink(target, dst)
		}
		if err != nil {
			t.Fatal(err)
		}
		return
	}
	content, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, dst, content, info.Mode().Perm())
}

func writeTestFile(t *testing.T, path string, content []byte, perm fs.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, perm); err != nil {
		t.Fatal(err)
	}
}

// TestLauncherGatesUncertifiedAdapter runs the launcher on a repository copy
// with an uncertified adapter: it refuses the adapter without
// --allow-uncertified-agent, and with it labels the session lower assurance and
// runs the runtime-uncertified image tag.
func TestLauncherGatesUncertifiedAdapter(t *testing.T) {
	if testing.Short() {
		t.Skip("copies the repository and runs the launcher")
	}
	root := repoCopyWithUncertifiedFixture(t)
	const refusal = "Workcell adapter zz-fixture is uncertified: no review certified its CLI. Add --allow-uncertified-agent"
	if code, _, stderr := launcherRun(t, root, "--agent", "zz-fixture", "--allow-uncertified-agent", "--allow-uncertified-agent", "--dry-run"); code != 2 || !strings.Contains(stderr, "--allow-uncertified-agent may be given only once.") {
		t.Errorf("a repeated --allow-uncertified-agent exited %d, want 2 with the once-only refusal: %s", code, stderr)
	}
	for _, args := range [][]string{
		{"--agent", "zz-fixture", "--dry-run"},
		{"--agent", "zz-fixture", "--inspect"},
		{"--agent", "zz-fixture", "--auth-status"},
	} {
		if code, _, stderr := launcherRun(t, root, args...); code != 2 || !strings.Contains(stderr, refusal) {
			t.Errorf("launcher %q exited %d, want 2 with the uncertified refusal: %s", args, code, stderr)
		}
	}

	// The launch support check refuses a host other than the reviewed launch
	// host (Apple Silicon macOS) before a dry run, for example the Linux
	// validator without /dev/kvm. The refusals above ran on every host; the
	// suite on the macOS host runs the dry runs below.
	if runtime.GOOS != "darwin" {
		t.Skip("the dry runs need the reviewed launch host, macOS")
	}
	code, stdout, stderr := launcherRun(t, root, "--agent", "zz-fixture", "--allow-uncertified-agent", "--dry-run")
	if code != 0 {
		t.Fatalf("uncertified dry run exited %d: %s", code, stderr)
	}
	for _, want := range []string{
		"\nsession_assurance_initial=lower-assurance-uncertified-adapter\n",
		"\nexecution_path=lower-assurance-uncertified-adapter audit_log=",
	} {
		if !strings.Contains("\n"+stderr, want) {
			t.Errorf("uncertified dry run lacks %q:\n%s", want, stderr)
		}
	}
	if !strings.Contains(stdout, " workcell:local-uncertified") || strings.Contains(stdout, " workcell:local ") {
		t.Errorf("uncertified dry run must run the workcell:local-uncertified image:\n%s", stdout)
	}

	code, stdout, stderr = launcherRun(t, root, "--agent", "zz-fixture", "--allow-uncertified-agent", "--allow-repo-mcp", "--ack-repo-mcp="+time.Now().UTC().Format("2006-01-02"), "--dry-run")
	if code != 0 || !strings.Contains(stderr, "\nsession_assurance_initial=lower-assurance-repo-mcp+uncertified-adapter\n") || !strings.Contains(stderr, "\nexecution_path=lower-assurance-repo-mcp+uncertified-adapter ") {
		t.Errorf("stacked dry run (exit %d) lacks the stacked uncertified reason:\n%s%s", code, stdout, stderr)
	}

	code, stdout, stderr = launcherRun(t, root, "--agent", "zz-fixture", "--allow-uncertified-agent", "--inspect")
	if code != 0 || !strings.Contains("\n"+stdout, "\nsession_assurance_initial=lower-assurance-uncertified-adapter\n") {
		t.Errorf("uncertified inspect (exit %d) lacks the assurance label:\n%s%s", code, stdout, stderr)
	}
}

// TestLauncherRefusesUncertifiedOptionMisuse runs the shipped launcher: the
// option needs --agent, and a certified adapter refuses it.
func TestLauncherRefusesUncertifiedOptionMisuse(t *testing.T) {
	for _, tc := range []struct {
		args    []string
		refusal string
	}{
		{[]string{"--agent", "codex", "--allow-uncertified-agent", "--auth-status"}, "--allow-uncertified-agent applies only to an uncertified adapter; codex is certified."},
		{[]string{"--logs", "audit", "--allow-uncertified-agent"}, "--allow-uncertified-agent requires --agent."},
	} {
		if code, _, stderr := launcherRun(t, repoRoot, tc.args...); code != 2 || !strings.Contains(stderr, tc.refusal) {
			t.Errorf("launcher %q exited %d, want 2 with %q: %s", tc.args, code, tc.refusal, stderr)
		}
	}
	if code, _, stderr := launcherRun(t, repoRoot, "--agent", "codex", "--auth-status"); code != 0 {
		t.Errorf("certified launcher probe exited %d: %s", code, stderr)
	}
}
