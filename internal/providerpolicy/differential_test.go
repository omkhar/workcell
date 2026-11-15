// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package providerpolicy

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/omkhar/workcell/internal/adapters"
)

// The differential test runs runtime/container/provider-policy.sh, the shell
// policy that this package replaces, and the Go policy over one generated
// corpus of argument vectors, and requires the same exit status and the same
// stderr for each one. The driver runs the shell with LC_ALL=C. The launcher
// passes no locale to the container and the image sets none, so the policy
// runs there in the default POSIX (C) locale. Under a UTF-8 locale bash folds
// the case of some non-ASCII letters, so the equivalence is a C-locale claim.

// diffCase is one policy call: kind "check" runs the per-provider function
// that provider-wrapper.sh calls, and kind "validate" runs
// validate_command_args, the entrypoint check.
type diffCase struct {
	session  Session
	kind     string
	id       string
	args     []string
	adapters map[string]Adapter
}

type diffResult struct {
	code   int
	stderr string
}

var certifiedIDs = []string{"codex", "claude", "gemini", "copilot"}

func (c diffCase) goResult() diffResult {
	var err error
	if c.kind == "check" {
		err = Check(c.id, c.args, c.session, c.adapters)
	} else {
		err = ValidateCommand(c.id, c.args, c.session, c.adapters)
	}
	if err == nil {
		return diffResult{}
	}
	var refusal *Refusal
	if !errors.As(err, &refusal) {
		panic(err)
	}
	return diffResult{code: 2, stderr: refusal.Message + "\n"}
}

// shellCall is the function and the arguments of the shell policy call.
func (c diffCase) shellCall() []string {
	switch {
	case c.kind == "validate":
		return append([]string{"validate_command_args", c.id}, c.args...)
	case slices.Contains(certifiedIDs, c.id):
		return append([]string{"reject_unsafe_" + c.id + "_args"}, c.args...)
	default:
		return append([]string{"reject_manifest_args", c.id}, c.args...)
	}
}

func (c diffCase) String() string {
	return fmt.Sprintf("%s %s %q mode=%q ui=%q wrapper=%q pid1=%t", c.kind, c.id, c.args,
		c.session.Mode, c.session.AgentUI, c.session.WrapperContext, c.session.CallerIsPID1)
}

// shellDriver reads NUL-separated records (mode, ui, wrapper context, word
// count, words) and runs each call in a subshell, so $$ stays the driver's
// PID. It writes the exit status and stderr of each call, NUL-separated.
const shellDriver = `
source "$1"
source "$2"
errexit_mode="$3"
exec 3<"$4" 4>"$5"
err="$6"
while IFS= read -r -d '' -u 3 mode; do
  IFS= read -r -d '' -u 3 ui
  IFS= read -r -d '' -u 3 wrapper
  IFS= read -r -d '' -u 3 n
  words=()
  for ((i = 0; i < n; i++)); do
    IFS= read -r -d '' -u 3 word
    words+=("${word}")
  done
  # The subshell is a plain command, not part of a || list, so set -e
  # inside it is in effect, as in the callers. Its status is read from $?.
  (
    if [[ "${errexit_mode}" == errexit ]]; then
      set -euo pipefail
    fi
    WORKCELL_MODE="${mode}"
    AGENT_UI="${ui}"
    WORKCELL_WRAPPER_CONTEXT="${wrapper}"
    "${words[@]}"
  ) 2>"${err}" >/dev/null
  code=$?
  msg=""
  IFS= read -r -d '' msg <"${err}" || true
  printf '%s\0%s\0' "${code}" "${msg}" >&4
done
`

// runShell runs the cases through the shell policy in parallel bash drivers.
// errexit runs each call under set -euo pipefail, as the entrypoint and the
// wrapper do. pid1 runs the drivers as PID 1 of a new PID namespace.
func runShell(t *testing.T, cases []diffCase, errexit, pid1 bool) []diffResult {
	t.Helper()
	root := filepath.Join("..", "..")
	generated := filepath.Join(root, "runtime", "container", "generated-adapters.sh")
	policy := filepath.Join(root, "runtime", "container", "provider-policy.sh")
	workers := min(runtime.NumCPU(), 8)
	results := make([]diffResult, len(cases))
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for w := range workers {
		var idx []int
		for i := w; i < len(cases); i += workers {
			idx = append(idx, i)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			dir := t.TempDir()
			in := filepath.Join(dir, "in")
			var buf bytes.Buffer
			for _, i := range idx {
				c := cases[i]
				words := c.shellCall()
				for _, field := range append([]string{c.session.Mode, c.session.AgentUI, c.session.WrapperContext, strconv.Itoa(len(words))}, words...) {
					buf.WriteString(field)
					buf.WriteByte(0)
				}
			}
			if err := os.WriteFile(in, buf.Bytes(), 0o600); err != nil {
				errs <- err
				return
			}
			mode := "plain"
			if errexit {
				mode = "errexit"
			}
			argv := []string{"bash", "--noprofile", "--norc", "-c", shellDriver, "bash", generated, policy, mode, in, filepath.Join(dir, "out"), filepath.Join(dir, "err")}
			if pid1 {
				argv = append([]string{"unshare", "--user", "--map-root-user", "--pid", "--fork"}, argv...)
			}
			cmd := exec.Command(argv[0], argv[1:]...)
			cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "LC_ALL=C"}
			if out, err := cmd.CombinedOutput(); err != nil {
				errs <- fmt.Errorf("shell driver: %v: %s", err, out)
				return
			}
			out, err := os.ReadFile(filepath.Join(dir, "out"))
			if err != nil {
				errs <- err
				return
			}
			fields := bytes.Split(out, []byte{0})
			if len(fields) != 2*len(idx)+1 {
				errs <- fmt.Errorf("shell driver wrote %d fields for %d cases", len(fields), len(idx))
				return
			}
			for k, i := range idx {
				code, err := strconv.Atoi(string(fields[2*k]))
				if err != nil {
					errs <- err
					return
				}
				results[i] = diffResult{code: code, stderr: string(fields[2*k+1])}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	return results
}

// corpusTokens returns the token pool for provider id: every inventory option
// and its other spellings, the manifest commands, and edge tokens.
func corpusTokens(t *testing.T, id string, manifest adapters.Manifest) []string {
	t.Helper()
	inventory, err := os.ReadFile(filepath.Join("..", "..", "tests", "fixtures", "flags", id+".txt"))
	if err != nil {
		t.Fatal(err)
	}
	var flags []string
	sc := bufio.NewScanner(bytes.NewReader(inventory))
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" && !strings.HasPrefix(line, "#") {
			flags = append(flags, line)
		}
	}
	flags = append(flags, manifest.Flags.Allow...)
	flags = append(flags, manifest.Flags.Deny...)
	flags = append(flags, extraFlags...)

	var tokens []string
	add := func(s ...string) { tokens = append(tokens, s...) }
	for _, f := range flags {
		add(f, f+"=", f+"="+probeValue, f+probeValue, f+"=strict", strings.ToUpper(f), strings.ToUpper(f)+"="+probeValue)
		if strings.HasPrefix(f, "--") && len(f) > 2 {
			camel := camelCase(f)
			add(camel, camel+"="+probeValue, "--"+strings.ReplaceAll(f[2:], "-", ""), "-"+f[2:], f+"-", "---"+f[2:])
		}
		if len(f) == 2 && f[0] == '-' {
			add(f+"=", f+"=strict", f+"strict", f+"breakglass", f+"-x", f+"--yolo", f+"y", "-d"+f[1:], "-m"+f[1:])
		}
	}
	add(manifest.Flags.Subcommands...)
	add(manifest.Flags.OtherCommands...)
	add(CodexAllowedSubcommands...)
	add(CodexGUISubcommands...)
	add(CodexDeniedSubcommands...)
	add(edgeTokens...)
	add(configValues()...)
	for _, c := range shortLetters {
		for _, d := range shortLetters {
			add("-" + string(c) + string(d))
		}
	}
	for _, a := range []string{"y", "s", "m", "p", "d", "i", "x"} {
		for _, b := range []string{"y", "s", "m", "d", "l"} {
			for _, c := range []string{"y", "s", "p", "q"} {
				add("-" + a + b + c)
			}
		}
	}
	slices.Sort(tokens)
	return slices.Compact(tokens)
}

const probeValue = "workcell-flag-probe"

const shortLetters = "yYsSmpPiIwWerRoOCcnNAadhvVlqtuxfz-=1"

// extraFlags are options outside the inventories: removed, hidden, or
// policy-named options, and near misses of them.
var extraFlags = []string{
	"--yolo", "--approve-for-me", "--not-so-yolo", "--dangerously-bypass-approvals-and-sandbox",
	"--dangerously-bypass-hook-trust", "--dangerously-bypass-x", "--dangerously-skip-permissions",
	"--full-auto", "--ignore-rules", "--search", "--remote-auth-token-env", "--local-provider",
	"--enable", "--disable", "--ask-for-approval", "--sandbox", "--cd", "--profile", "--config",
	"--image", "--model", "--prompt", "--approval-mode", "--approvalMode", "--approval_mode",
	"--admin-policy", "--include-directories", "--allowed-tools", "--allowedTools", "--add-dir",
	"--permission-mode", "--cloud", "--environment", "--discard-unpushed", "--force-remove-worktree",
	"--trust-workspace", "--policy", "--x-bypass-y-permission-z", "--no-dangerously", "--bypasspermission",
	"--debug-file", "--interactive", "--acp", "--secret-env-vars", "--log-dir", "-p", "-c", "-C", "-a",
	"-s", "-m", "-i", "-y", "-n", "-r", "-w", "-A", "-e", "-o", "-d",
}

var edgeTokens = []string{
	"", " ", "-", "--", "---", "----", "=", "-=", "--=", "-=y", "x", "hi", "exec", "features", "enable",
	"disable", "list", "app-server", "app", "plugin", "mcp", "install", "update", "upgrade", "purge",
	"login", "logout", "config", "init", "skill", "sandbox", "serve", "run", "help", "strict",
	"development", "build", "breakglass", "=strict", "=breakglass", "==strict", "-p", "--prompt",
	"-safe", "-yellow", "--yolo\u00a0", "--\u0130nclude-directories", "-\u00ff", "\u212a",
	"bypassPermissions", "--debug-file", "/state", "ws://127.0.0.1:1", "--listen", "--stdio", "x\ny",
	"\t", "-\t", "--yolo\n", "-y\n", "--model=dangerously", "--model=x-bypass-permission",
}

// configValues are Codex -c values: guarded keys in every spelling, inline
// tables, and malformed keys.
func configValues() []string {
	keys := []string{
		"profile", "sandbox", "sandbox_mode", "sandbox_permissions", "web_search", "approval_policy",
		"approvals_reviewer", "project_doc_fallback_filenames", "project_root_markers", "projects",
		"projects./workspace/sub.trust_level", "mcp", "mcp_servers.x.command", "mcpx", "plugins",
		"plugins.x", "marketplaces", "marketplaces.y", "hooks", "hooks.z", "features", "features.plugins",
		"features.plugin_sharing", "features.plugin_hooks", "features.remote_plugin",
		"features.remote_control", "features.other", "shell_environment_policy",
		"shell_environment_policy.set.X", "sandbox_workspace_write", "sandbox_workspace_write.network_access",
		"model", "model_provider", "profiles", "profiles.x", "profiles.x.approvals_reviewer",
		"profiles.x.model", "profiles.x.mcp_servers.a", "profiles..mcp", "profiles.x.", "profiles.a.b.hooks",
		"__workcell_malformed__", "__WORKCELL_MALFORMED__", "", ".", "..", "a.", ".a", "a..b",
	}
	var values []string
	for _, k := range keys {
		spellings := []string{k, strings.ToUpper(k), " " + k + " ", "\t" + k}
		if parts := strings.Split(k, "."); len(parts) > 1 {
			spellings = append(spellings,
				`"`+parts[0]+`".`+strings.Join(parts[1:], "."),
				strings.Join(parts, " . "),
				parts[0]+`.'`+strings.Join(parts[1:], ".")+`'`,
				parts[0]+`."`+strings.Join(parts[1:], `"."`)+`"`)
		} else {
			spellings = append(spellings, `"`+k+`"`, `'`+k+`'`, `"`+k, k+`\`, `"`+k+`'`)
		}
		for _, s := range spellings {
			values = append(values, s, s+"=1", s+"=true", s+"={a=1}", s+"= {a=1}", s+"=\t{", s+"=\u00a0{x=1}",
				s+"=\u2003{", s+"=\v{", s+"=\x01", s+"==", s+"=\"{\"")
		}
	}
	values = append(values, "model\n.x=1", "\nprojects.x=1", "\u00a0projects=1", "pro\u00a0jects=1", "a\x7f=1", "k\\x=1", "a.\"b.c\".d=1")
	return values
}

// camelCase returns --allowedTools for --allowed-tools.
func camelCase(flag string) string {
	parts := strings.Split(strings.TrimPrefix(flag, "--"), "-")
	for i := 1; i < len(parts); i++ {
		if parts[i] != "" {
			parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
		}
	}
	return "--" + strings.Join(parts, "")
}

var sessions = []Session{
	{},
	{Mode: "strict", AgentUI: "cli", WrapperContext: "0"},
	{Mode: "development", AgentUI: "gui", WrapperContext: "1"},
	{Mode: "build", AgentUI: "gui"},
	{Mode: "breakglass", AgentUI: "Gui", WrapperContext: "1"},
	{Mode: "breakglass"},
	{Mode: "Strict", AgentUI: "tui", WrapperContext: "yes"},
}

// buildCorpus returns systematic cases (each token alone, after a leading
// option, before a value, and after --) plus seeded random argument vectors.
func buildCorpus(t *testing.T, randomPerID int) []diffCase {
	t.Helper()
	manifests, err := adapters.LoadManifests(filepath.Join("..", "..", "adapters"))
	if err != nil {
		t.Fatal(err)
	}
	// The cases carry policy data built from the manifests, so the corpus
	// does not depend on a generated table.
	data := map[string]Adapter{}
	for _, m := range manifests {
		if m.Tier != "planned" {
			data[m.ID] = Adapter{Tier: m.Tier, Allow: m.Flags.Allow, Deny: m.Flags.Deny, OtherCommands: m.Flags.OtherCommands}
		}
	}
	rng := rand.New(rand.NewPCG(901, 2026))
	var cases []diffCase
	for _, m := range manifests {
		if m.Tier == "planned" {
			continue
		}
		tokens := corpusTokens(t, m.ID, m)
		lead := "--help"
		if len(m.Flags.Allow) > 0 {
			lead = m.Flags.Allow[0]
		}
		for i, tok := range tokens {
			s := sessions[i%len(sessions)]
			for _, args := range [][]string{
				{tok}, {tok, probeValue}, {tok, "--yolo"}, {tok, "-y"}, {tok, "strict"}, {tok, "enable"},
				{tok, "--", "plugin"}, {lead, tok}, {"--model", "x", tok}, {"--", tok}, {"-d", "--", tok},
				{"exec", tok}, {"features", tok}, {tok, "-p", "--allow-tool=x"}, {"-c", tok}, {"--config", tok},
				{"--config=" + tok}, {"-c" + tok}, {"-p", tok}, {"--debug-file", "--", tok},
			} {
				cases = append(cases, diffCase{session: s, kind: "check", id: m.ID, args: args})
			}
			cases = append(cases, diffCase{session: sessions[0], kind: "check", id: m.ID, args: []string{tok}})
		}
		for range randomPerID {
			n := rng.IntN(6)
			args := make([]string, n)
			for j := range args {
				args[j] = tokens[rng.IntN(len(tokens))]
			}
			s := sessions[rng.IntN(len(sessions))]
			cases = append(cases, diffCase{session: s, kind: "check", id: m.ID, args: args})
			argv0 := m.ID
			switch rng.IntN(8) {
			case 0:
				argv0 = "bash"
			case 1:
				argv0 = "codex"
			}
			cases = append(cases, diffCase{session: s, kind: "validate", id: m.ID, args: append([]string{argv0}, args...)})
		}
	}
	for _, id := range []string{"", "no-such-agent", "zz", "planned", "-", "--"} {
		for _, args := range [][]string{nil, {id}, {id, "--help"}, {id, "x"}, {"codex", "--yolo"}} {
			cases = append(cases, diffCase{kind: "validate", id: id, args: args}, diffCase{kind: "check", id: id, args: args})
		}
	}
	for i := range cases {
		cases[i].adapters = data
	}
	return cases
}

// sampleCorpus keeps a seeded sample of n cases, or every case when
// WORKCELL_PROVIDER_POLICY_DIFF_FULL=1 asks for the full corpus.
func sampleCorpus(cases []diffCase, n int) []diffCase {
	if os.Getenv("WORKCELL_PROVIDER_POLICY_DIFF_FULL") == "1" || len(cases) <= n {
		return cases
	}
	rng := rand.New(rand.NewPCG(14, 11))
	rng.Shuffle(len(cases), func(i, j int) { cases[i], cases[j] = cases[j], cases[i] })
	return cases[:n]
}

func requireBash4(t *testing.T) {
	t.Helper()
	if out, err := exec.Command("bash", "-c", "((BASH_VERSINFO[0] >= 4))").CombinedOutput(); err != nil {
		t.Skipf("the shell policy needs bash 4 or later: %v %s", err, out)
	}
}

// shellStatusLeak reports whether the shell result is the known status leak
// of the shell Codex policy, and the Go result agrees with it. A passing
// profile check, or a passing attached config check that is the last word,
// ends in a false `[[ ... ]] &&` test, so the shell function returns status
// 1 with no message although no check refused. The callers run under
// set -euo pipefail, so there the first passing profile check also stops the
// launch with exit 1. The Go policy allows these calls, as the shell checks
// decide. Without errexit the shell scanned every word, so the Go policy must
// allow the call. With errexit the shell stopped early, so a later refusal
// is also correct.
func shellStatusLeak(c diffCase, goR, shellR diffResult, errexit bool) bool {
	if shellR != (diffResult{code: 1}) || c.id != "codex" || !codexLeakWord(c.args) {
		return false
	}
	return goR.code == 0 || (errexit && goR.code == 2)
}

// codexLeakWord reports whether args hold a Codex profile option or an
// attached config option.
func codexLeakWord(args []string) bool {
	for _, a := range args {
		if a == "-p" || a == "--profile" || strings.HasPrefix(a, "-p") || strings.HasPrefix(a, "--profile=") ||
			strings.HasPrefix(a, "--config=") || (strings.HasPrefix(a, "-c") && len(a) > 2) {
			return true
		}
	}
	return false
}

// goStricter reports whether a Go refusal that the shell policy lacks is one
// of the reviewed tightenings of the port. A separate Codex profile value
// that starts with "=" (`--profile =strict`, `--profile==strict`) is the
// literal profile name, which the shell stripped to the managed profile. A
// Gemini or Copilot long option name with a control or non-ASCII byte is
// refused, because bash under a UTF-8 locale folded U+0130 and U+212A to
// ASCII letters. The refusal must name the word that it is about.
// goStricter returns the index of that word. The shell may refuse too: then
// it refused a later word, and compareCorpus checks that the shell allows
// every word before the tightened one.
func goStricter(c diffCase, goR diffResult) (int, bool) {
	if goR.code != 2 {
		return 0, false
	}
	for i, a := range c.args {
		switch c.id {
		case "codex":
			equalValue := strings.HasPrefix(a, "--profile==") ||
				((a == "-p" || a == "--profile") && i+1 < len(c.args) && strings.HasPrefix(c.args[i+1], "="))
			if equalValue && goR.stderr == "Workcell blocked unsafe Codex override: --profile\n" {
				return i, true
			}
		case "gemini", "copilot":
			if !strings.HasPrefix(a, "--") || !hasUnsafeByte(beforeEqual(a)) {
				continue
			}
			prefix := "Workcell blocked unsafe Gemini override: "
			if c.id == "copilot" {
				prefix = "Workcell blocked unsafe Copilot override: "
			}
			if goR.stderr == prefix+a+"\n" || goR.stderr == prefix+beforeEqual(a)+"\n" {
				return i, true
			}
		}
	}
	return 0, false
}

// compareCorpus runs cases through both policies and fails on any result
// that differs, except the shell status leak and the reviewed tightenings.
// It returns the number of shell status leaks.
func compareCorpus(t *testing.T, cases []diffCase, errexit, pid1 bool) int {
	t.Helper()
	shell := runShell(t, cases, errexit, pid1)
	denied, leaks, stricter := 0, 0, 0
	var prefixes []diffCase
	for i, c := range cases {
		got := c.goResult()
		switch {
		case got == shell[i]:
			if got.code != 0 {
				denied++
			}
		case shellStatusLeak(c, got, shell[i], errexit):
			leaks++
		default:
			at, ok := goStricter(c, got)
			if !ok {
				t.Errorf("%s: go %+v, shell %+v", c, got, shell[i])
				break
			}
			stricter++
			if shell[i].code == 2 {
				before := c
				before.args = c.args[:at]
				prefixes = append(prefixes, before)
			}
		}
	}
	// Both refuse, the Go policy at a tightened word and the shell at a
	// later one. The shell must allow every word before the tightened word.
	if len(prefixes) > 0 {
		for k, r := range runShell(t, prefixes, errexit, pid1) {
			if r.code == 2 {
				t.Errorf("%s: the shell refuses a word before the Go tightening: %+v", prefixes[k], r)
			}
		}
	}
	t.Logf("%d cases: %d refused by both, %d shell status leaks, %d Go tightenings", len(cases), denied, leaks, stricter)
	if denied == 0 || denied == len(cases) {
		t.Fatalf("degenerate corpus: %d of %d refused", denied, len(cases))
	}
	return leaks
}

// TestDifferentialAgainstShellPolicy is the parity gate of the port: over the
// corpus the Go policy and the shell policy give the same exit status and the
// same stderr. Set WORKCELL_PROVIDER_POLICY_DIFF_FULL=1 to run every case.
func TestDifferentialAgainstShellPolicy(t *testing.T) {
	requireBash4(t)
	n := 20000
	if testing.Short() {
		n = 2000
	}
	compareCorpus(t, sampleCorpus(buildCorpus(t, 3000), n), false, false)
}

// TestDifferentialUnderErrexit runs the shell policy under set -euo pipefail,
// as entrypoint.sh and provider-wrapper.sh call it.
func TestDifferentialUnderErrexit(t *testing.T) {
	requireBash4(t)
	n := 5000
	if testing.Short() {
		n = 500
	}
	cases := sampleCorpus(buildCorpus(t, 1000), n)
	// The known leak: under set -e the shell stops at the first passing
	// profile check with status 1, before it reaches --yolo, and a trailing
	// attached config check ends the call with status 1. Without set -e the
	// shell scans on and refuses --yolo. These cases show that set -e is in
	// effect in the driver, and they must be classified as leaks.
	leak := []diffCase{
		{kind: "check", id: "codex", args: []string{"--profile", "strict", "--yolo"}, adapters: cases[0].adapters},
		{kind: "check", id: "codex", args: []string{"--config=model=x"}, adapters: cases[0].adapters},
	}
	if got := runShell(t, leak, true, false); got[0] != (diffResult{code: 1}) || got[1] != (diffResult{code: 1}) {
		t.Fatalf("set -e is not in effect in the shell driver: %+v", got)
	}
	if got := runShell(t, leak[:1], false, false); got[0].code != 2 {
		t.Fatalf("without set -e the shell must scan on and refuse --yolo: %+v", got)
	}
	if leaks := compareCorpus(t, append(cases, leak...), true, false); leaks < len(leak) {
		t.Fatalf("%d shell status leaks under set -e, want at least %d", leaks, len(leak))
	}
}

// TestDifferentialBreakglassAsPID1 runs the shell policy as PID 1 of a new
// PID namespace, the only place where breakglass applies.
func TestDifferentialBreakglassAsPID1(t *testing.T) {
	requireBash4(t)
	if err := exec.Command("unshare", "--user", "--map-root-user", "--pid", "--fork", "true").Run(); err != nil {
		t.Skipf("no unprivileged PID namespace here: %v", err)
	}
	n := 1000
	if testing.Short() {
		n = 100
	}
	var cases []diffCase
	for _, c := range sampleCorpus(buildCorpus(t, 200), n) {
		for _, s := range []Session{
			{Mode: "breakglass"}, {Mode: "breakglass", WrapperContext: "1"}, {Mode: "breakglass", WrapperContext: "0"},
			{Mode: "strict"}, {Mode: "breakglass", AgentUI: "gui"},
		} {
			s.CallerIsPID1 = true
			c.session = s
			cases = append(cases, c)
		}
	}
	compareCorpus(t, cases, false, true)
}
