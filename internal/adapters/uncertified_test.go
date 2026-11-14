// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package adapters

import (
	"bytes"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/omkhar/workcell/internal/providerpolicy"
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

// runRuntimePolicy runs the runtime provider policy over the policy data that
// the provider-policy gen target renders from manifests. Kind "manifest" runs
// providerpolicy.ManifestArgs with args[0] as the adapter id, and kind
// "validate" runs providerpolicy.ValidateCommand, the entrypoint check, with
// args[0] as the expected command. It returns the exit code and stderr that
// workcell-provider-policy gives for the call. The session is empty, so
// CallerIsPID1 is false: that field is the binary's os.Getppid() == 1, the
// exec'd-binary equivalent of the shell policy's $$ == 1 check, and
// breakglass never applies here.
func runRuntimePolicy(t *testing.T, manifests []Manifest, kind string, args ...string) (int, string) {
	t.Helper()
	data := map[string]providerpolicy.Adapter{}
	for _, m := range manifests {
		switch m.Tier {
		case "certified":
			data[m.ID] = providerpolicy.Adapter{Tier: m.Tier}
		case "uncertified":
			data[m.ID] = providerpolicy.Adapter{Tier: m.Tier, Allow: m.Flags.Allow, Deny: m.Flags.Deny, OtherCommands: m.Flags.OtherCommands}
		}
	}
	var err error
	switch kind {
	case "manifest":
		err = providerpolicy.ManifestArgs(args[0], args[1:], data)
	case "validate":
		err = providerpolicy.ValidateCommand(args[0], args[1:], providerpolicy.Session{}, data)
	default:
		t.Fatalf("unknown policy kind %q", kind)
	}
	if err != nil {
		return 2, err.Error() + "\n"
	}
	return 0, ""
}

// TestRejectManifestArgsAllowsOnlyTheManifestAllowlist runs provider args of
// the uncertified fixture through the manifest policy: an option passes only
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
		code, stderr := runRuntimePolicy(t, manifests, "manifest", append([]string{"zz-fixture"}, tc.args...)...)
		if tc.want == "" {
			if code != 0 {
				t.Errorf("manifest policy %q exited %d, want 0: %s", tc.args, code, stderr)
			}
		} else if code != 2 || !strings.Contains(stderr, tc.want) {
			t.Errorf("manifest policy %q exited %d with %q, want exit 2 with %q", tc.args, code, stderr, tc.want)
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
		{"manifest", []string{"codex"}, "Workcell has no manifest [flags] allow list for adapter: codex"},
		{"manifest", []string{"no-such-agent", "--help"}, "no manifest [flags] allow list for adapter: no-such-agent"},
		{"validate", []string{"no-such-agent", "no-such-agent", "--help"}, "Workcell has no provider policy for: no-such-agent"},
		{"validate", []string{"zz-fixture", "zz-fixture", "--yolo"}, "denied zz-fixture option: --yolo"},
		{"validate", []string{"zz-fixture", "zz-fixture", "serve"}, "zz-fixture command that its manifest does not allow: serve"},
	} {
		if code, stderr := runRuntimePolicy(t, manifests, tc.fn, tc.args...); code != 2 || !strings.Contains(stderr, tc.want) {
			t.Errorf("%s %q exited %d with %q, want exit 2 with %q", tc.fn, tc.args, code, stderr, tc.want)
		}
	}
	if code, stderr := runRuntimePolicy(t, manifests, "validate", "zz-fixture", "zz-fixture", "run", "--model", "x"); code != 0 {
		t.Errorf("the entrypoint check refused allowed uncertified args (exit %d): %s", code, stderr)
	}
	// A manifest list that the generated data lacks stops the launch.
	broken := loadRepoManifests(t)
	if code, stderr := runRuntimePolicy(t, broken, "manifest", "zz-fixture", "--help"); code != 2 || !strings.Contains(stderr, "no manifest [flags] allow list for adapter: zz-fixture") {
		t.Errorf("manifest policy without generated data exited %d with %q", code, stderr)
	}
}

// TestRenderProviderPolicyUncertifiedTables parses the rendered provider
// policy data and pins the tier and [flags] lists of an uncertified manifest,
// and the tier alone for a certified one.
func TestRenderProviderPolicyUncertifiedTables(t *testing.T) {
	rendered, err := RenderProviderPolicyGo(loadUncertifiedFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	got := parseProviderPolicyAdapters(t, rendered)
	want := map[string]map[string][]string{
		"codex": {"Tier": {"certified"}},
		"zz-fixture": {
			"Tier":          {"uncertified"},
			"Allow":         {"-h", "--help", "-m", "--model"},
			"Deny":          {"-y", "--yolo"},
			"OtherCommands": {"serve", "mcp"},
		},
	}
	for id, fields := range want {
		if !reflect.DeepEqual(got[id], fields) {
			t.Errorf("provider policy data for %s = %v, want %v", id, got[id], fields)
		}
	}
}

// parseProviderPolicyAdapters decodes the Adapters map literal of a rendered
// internal/providerpolicy/adapters_gen.go: adapter id to field name to the
// string values of that field.
func parseProviderPolicyAdapters(t *testing.T, src []byte) map[string]map[string][]string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "adapters_gen.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	unquote := func(e ast.Expr) string {
		lit, ok := e.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			t.Fatalf("want a string literal, got %T", e)
		}
		v, err := strconv.Unquote(lit.Value)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	out := map[string]map[string][]string{}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.VAR {
			continue
		}
		for _, spec := range gen.Specs {
			vs := spec.(*ast.ValueSpec)
			if len(vs.Names) != 1 || vs.Names[0].Name != "Adapters" || len(vs.Values) != 1 {
				continue
			}
			table := vs.Values[0].(*ast.CompositeLit)
			for _, elt := range table.Elts {
				entry := elt.(*ast.KeyValueExpr)
				fields := map[string][]string{}
				for _, f := range entry.Value.(*ast.CompositeLit).Elts {
					field := f.(*ast.KeyValueExpr)
					name := field.Key.(*ast.Ident).Name
					if list, ok := field.Value.(*ast.CompositeLit); ok {
						values := []string{}
						for _, v := range list.Elts {
							values = append(values, unquote(v))
						}
						fields[name] = values
					} else {
						fields[name] = []string{unquote(field.Value)}
					}
				}
				out[unquote(entry.Key)] = fields
			}
		}
	}
	if len(out) == 0 {
		t.Fatal("rendered provider policy data has no Adapters entries")
	}
	return out
}

// TestCommittedLauncherShellAdapterTier asks the committed launcher shell for
// the tier of every manifest id: a planned or unknown id has none.
func TestCommittedLauncherShellAdapterTier(t *testing.T) {
	script := "scripts/lib/launcher/generated-adapters.sh"
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

// TestOpencodeManifestPolicy runs the shipped opencode manifest through
// the entrypoint check of internal/providerpolicy: every allow entry passes, every
// deny and other_commands entry fails, and the yargs spellings of a refused
// option (an attached value, --no- negation, camel case) fail too.
func TestOpencodeManifestPolicy(t *testing.T) {
	checkShippedManifestPolicy(t, "opencode", "--model", "opencode/demo", [][]string{
		nil, {"run", "fix the failing test"}, {"--model", "opencode/demo", "run", "hi"},
		{"-m", "opencode/demo", "--continue"}, {"--prompt=--auto"}, {"subdir"},
	}, []string{
		// The managed autonomy, network listener, remote attach and share
		// options stay refused even when a manifest edit drops them from deny.
		"--auto", "--yolo", "--dangerously-skip-permissions", "--share", "--port", "--hostname", "--attach",
		"--no-auto", "--dangerouslySkipPermissions", "--printLogs", "--mdnsDomain", "--", "-mx",
	})
}

// TestAmpManifestPolicy runs the shipped amp manifest through
// the entrypoint check. A hidden command that the help does not list (review,
// sync) and a command alias (t, up) fail like a listed command.
func TestAmpManifestPolicy(t *testing.T) {
	checkShippedManifestPolicy(t, "amp", "--mode", "high", [][]string{
		nil, {"-x", "fix the failing test"}, {"--mode", "high", "-x", "hi"}, {"login"},
		{"logout", "--all"}, {"version"}, {"--execute=--dangerously-allow-all is text"},
	}, []string{
		// The managed autonomy, remote execution, runner and terminal access
		// options stay refused even when a manifest edit drops them from deny.
		"--dangerously-allow-all", "--executor", "-ox", "--orb-execute", "--no-tui", "--remote-control-terminal",
		"--settings-file", "--mcp-config", "--", "-xm", "review", "sync", "t", "up",
	})
}

// TestGooseManifestPolicy runs the shipped goose manifest through
// the entrypoint check. A hidden command that the help does not list
// (mcp-probe) and a command alias (gw, sched) fail like a listed command.
func TestGooseManifestPolicy(t *testing.T) {
	checkShippedManifestPolicy(t, "goose", "--model", "claude-demo", [][]string{
		nil, {"run", "-t", "fix the failing test"}, {"session", "--name", "demo"}, {"s", "-r"},
		{"configure"}, {"info", "-v"}, {"run", "--text=--with-extension is text"},
	}, []string{
		// The extension, container and recipe inputs stay refused even when a
		// manifest edit drops them from deny.
		"--with-extension", "--with-builtin", "--with-streamable-http-extension", "--container",
		"--recipe", "--", "-tq", "mcp", "mcp-probe", "gw", "sched", "serve",
	})
}

// checkShippedManifestPolicy runs the shipped uncertified manifest id through
// the entrypoint check: each accepted argv passes, every allow entry passes,
// and each refused word, every deny entry (also with an attached value) and
// every other_commands entry fails, alone and after the allowed option
// allowedFlag allowedValue.
func checkShippedManifestPolicy(t *testing.T, id, allowedFlag, allowedValue string, accepted [][]string, refused []string) {
	t.Helper()
	manifests := loadRepoManifests(t)
	i := slices.IndexFunc(manifests, func(m Manifest) bool { return m.ID == id })
	if i < 0 || manifests[i].Tier != "uncertified" {
		t.Fatalf("adapters/%s must be an uncertified manifest", id)
	}
	flags := manifests[i].Flags
	run := func(args ...string) (int, string) {
		return runRuntimePolicy(t, manifests, "validate", append([]string{id, id}, args...)...)
	}
	for _, args := range accepted {
		if code, stderr := run(args...); code != 0 {
			t.Errorf("%s %q exited %d, want 0: %s", id, args, code, stderr)
		}
	}
	for _, flag := range flags.Allow {
		if code, stderr := run(flag, "value"); code != 0 {
			t.Errorf("allowed %s option %s exited %d: %s", id, flag, code, stderr)
		}
	}
	for _, flag := range flags.Deny {
		refused = append(refused, flag)
		if len(flag) > 2 { // a long option
			refused = append(refused, flag+"=true")
		}
	}
	for _, arg := range append(refused, flags.OtherCommands...) {
		if code, _ := run(arg); code != 2 {
			t.Errorf("%s %q exited %d, want the policy refusal (2)", id, arg, code)
		}
		// A refused word after an allowed one still fails.
		if code, _ := run(allowedFlag, allowedValue, arg); code != 2 {
			t.Errorf("%s %s %s %q exited %d, want 2", id, allowedFlag, allowedValue, arg, code)
		}
	}
}
