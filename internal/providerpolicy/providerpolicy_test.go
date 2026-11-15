// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package providerpolicy

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

type policyCase struct {
	args []string
	want string // "" passes; otherwise the exact refusal message
}

func runPolicyCases(t *testing.T, id string, s Session, cases []policyCase) {
	t.Helper()
	for _, tc := range cases {
		err := Check(id, tc.args, s, Adapters)
		got := ""
		if err != nil {
			got = err.Error()
		}
		if got != tc.want {
			t.Errorf("%s %q (%+v) = %q, want %q", id, tc.args, s, got, tc.want)
		}
	}
}

const (
	codexUnsafe = "Workcell blocked unsafe Codex override: "
	codexConfig = "Workcell blocked unsafe Codex config override: "
	codexSub    = "Workcell blocked unsupported Codex CLI subcommand: "
)

func TestCodexPolicy(t *testing.T) {
	runPolicyCases(t, "codex", Session{}, []policyCase{
		{nil, ""},
		{[]string{"fix the tests", "--version"}, ""},
		{[]string{"--model", "gpt-5", "exec", "--help"}, ""},
		{[]string{"--local-provider", "ollama", "exec", "--help"}, ""},
		{[]string{"-mgpt-5-codex", "--version"}, ""},
		{[]string{"-iImage", "--version"}, ""},
		{[]string{"--image", "a.png", "exec", "hi"}, ""},
		{[]string{"execpolicy", "check", "--dangerously-skip-permissions"}, ""},
		{[]string{"features"}, ""},
		{[]string{"features", "list"}, ""},
		{[]string{"features", "--json", "list"}, ""},
		{[]string{"--", "plugin"}, ""},
		{[]string{"--", "--yolo"}, ""},
		{[]string{"plugin-like prompt", "plugin"}, ""},
		// A passing profile or config check allows the call. Under errexit
		// the shell policy stopped these calls with exit 1 and no message.
		{[]string{"--profile", "strict"}, ""},
		{[]string{"-pstrict"}, ""},
		{[]string{"-p=strict"}, ""},
		{[]string{"--profile=strict"}, ""},
		{[]string{"--config=model=gpt-5"}, ""},
		{[]string{"-cmodel=gpt-5"}, ""},
		{[]string{"-c", `model="gpt-5"`}, ""},

		{[]string{"--yolo"}, codexUnsafe + "--yolo"},
		{[]string{"--yolo=true"}, codexUnsafe + "--yolo=true"},
		{[]string{"--dangerously-bypass-approvals-and-sandbox"}, codexUnsafe + "--dangerously-bypass-approvals-and-sandbox"},
		{[]string{"--dangerously-bypass-hook-trust=1"}, codexUnsafe + "--dangerously-bypass-hook-trust=1"},
		{[]string{"--profile", "breakglass", "--version"}, codexUnsafe + "--profile"},
		{[]string{"-pbreakglass", "--version"}, codexUnsafe + "--profile"},
		{[]string{"-p=breakglass"}, codexUnsafe + "--profile"},
		{[]string{"--profile=breakglass"}, codexUnsafe + "--profile"},
		// Only the attached forms -p=VALUE and --profile=VALUE drop one "=".
		// A separate value that starts with "=" is the literal profile name,
		// as clap passes it on, so "=strict" is not the managed profile.
		{[]string{"--profile", "=strict"}, codexUnsafe + "--profile"},
		{[]string{"-p", "=strict"}, codexUnsafe + "--profile"},
		{[]string{"--profile==strict"}, codexUnsafe + "--profile"},
		{[]string{"-p==strict"}, codexUnsafe + "--profile"},
		{[]string{"--profile="}, codexUnsafe + "--profile"},
		{[]string{"--cd", "/state"}, codexUnsafe + "--cd"},
		{[]string{"--cd=/x"}, codexUnsafe + "--cd"},
		{[]string{"-C/state"}, codexUnsafe + "--cd"},
		{[]string{"-s", "danger-full-access"}, codexUnsafe + "-s"},
		{[]string{"-sdanger-full-access"}, codexUnsafe + "--sandbox"},
		{[]string{"-s=danger-full-access"}, codexUnsafe + "--sandbox"},
		{[]string{"--sandbox=workspace-write"}, codexUnsafe + "--sandbox"},
		{[]string{"-anever"}, codexUnsafe + "-anever"},
		{[]string{"--ask-for-approval=never"}, codexUnsafe + "--ask-for-approval"},
		{[]string{"--remote-auth-token-env", "FOO"}, codexUnsafe + "--remote-auth-token-env"},
		{[]string{"--remote-auth-token-env=FOO"}, codexUnsafe + "--remote-auth-token-env"},
		{[]string{"--add-dir=/state"}, codexUnsafe + "--add-dir"},
		{[]string{"--model", "gpt-5", "plugin", "list"}, codexSub + "plugin"},
		{[]string{"--local-provider", "ollama", "plugin"}, codexSub + "plugin"},
		// -i and --image take a value, so the next word is the subcommand.
		{[]string{"--image", "a.png", "plugin"}, codexSub + "plugin"},
		{[]string{"-i", "a.png", "plugin"}, codexSub + "plugin"},
		{[]string{"--image=a.png", "plugin"}, codexSub + "plugin"},
		{[]string{"-m", "--", "plugin"}, codexSub + "plugin"},
		{[]string{"tcp-tunnel"}, codexSub + "tcp-tunnel"},
		{[]string{"mcp-server"}, codexSub + "mcp-server"},
		{[]string{"debug", "app-server"}, codexSub + "debug"},
		{[]string{"app"}, "Workcell blocked unsupported Codex CLI subcommand outside the managed GUI path: app"},
		{[]string{"features", "enable", "x"}, codexSub + "features enable"},
		{[]string{"features", "--", "disable"}, codexSub + "features disable"},
		{[]string{"--config", "profile=breakglass"}, codexConfig + "profile"},
		{[]string{"--config", "sandbox_workspace_write.network_access=true"}, codexConfig + "sandbox_workspace_write.network_access"},
		{[]string{"--config", `plugins."evil@rogue".enabled=true`}, codexConfig + `plugins."evil@rogue".enabled`},
		{[]string{"--config", `features."remote_plugin"=true`}, codexConfig + `features."remote_plugin"`},
		{[]string{"--config", `"mcp_servers".rogue.command=/bin/sh`}, codexConfig + `"mcp_servers".rogue.command`},
		{[]string{"-c", "features . plugins=true"}, codexConfig + "features . plugins"},
		{[]string{"--config=profiles.x.approvals_reviewer=auto_review"}, codexConfig + "profiles.x.approvals_reviewer=auto_review"},
		{[]string{"-capprovals_reviewer=auto_review"}, codexConfig + "approvals_reviewer=auto_review"},
		{[]string{"-c=approvals_reviewer=auto_review"}, codexConfig + "approvals_reviewer=auto_review"},
		// -c=VALUE drops one "=" like -p=VALUE. The rest is the literal value,
		// here with an empty key, which Codex rejects.
		{[]string{"-c==profile=breakglass"}, ""},
		{[]string{"-c", "features={remote_plugin=true}"}, codexConfig + "features"},
		{[]string{"-c", "k\\x=1"}, codexConfig + "k\\x"},
		{[]string{"-c", "__workcell_malformed__=1"}, codexConfig + "__workcell_malformed__"},
	})
	// Every spelling of a projects trust override stays refused: one marks a
	// directory trusted, so Codex loads a workspace .codex/config.toml.
	for _, args := range [][]string{
		{"-c", `projects./workspace/sub.trust_level="trusted"`},
		{"-c", `projects."/workspace".trust_level="trusted"`},
		{"-c", `projects={"/workspace/sub"={trust_level="trusted"}}`},
		{"--config", `projects={"/workspace/sub"={trust_level="trusted"}}`},
		{`--config=projects.x.trust_level="trusted"`},
		{`-cprojects.x.trust_level="trusted"`},
		{"-c", "\nprojects.\"/workspace\".trust_level=\"trusted\""},
		{"-c", "model\n.x=1\nprojects.x.trust_level=\"trusted\""},
		{"-c", "\u00a0projects={\"/workspace\"={trust_level=\"trusted\"}}"},
		{"-c", "\u2003projects.x.trust_level=\"trusted\""},
		{"-c", "\vprojects.x.trust_level=\"trusted\""},
		{"-c", "profiles=\u00a0{strict={projects={\"/workspace\"={trust_level=\"trusted\"}}}}"},
		{"-c", "features=\u2003{remote_plugin=true}"},
	} {
		if err := Codex(append(args, "exec", "hi"), Session{}); err == nil {
			t.Errorf("Codex %q passed, want a refusal", args)
		}
	}
}

// TestCodexGuardedConfigKeys gives every guarded key, guarded prefix and
// inline-table parent of the Codex -c policy a refused case and an allowed
// near miss, so a dropped or misspelled entry fails a test.
// TestIsUnsafeByte pins the edges of the C-locale class [^[:print:]\t].
func TestIsUnsafeByte(t *testing.T) {
	for c := 0; c < 256; c++ {
		want := c != '\t' && (c < 0x20 || c == 0x7f || c >= 0x80)
		if got := isUnsafeByte(byte(c)); got != want {
			t.Errorf("isUnsafeByte(%#x) = %t, want %t", c, got, want)
		}
	}
}

func TestCodexGuardedConfigKeys(t *testing.T) {
	var cases []policyCase
	for _, key := range []string{
		"profile", "sandbox", "sandbox_mode", "sandbox_permissions", "web_search", "approval_policy",
		"approvals_reviewer", "project_doc_fallback_filenames", "project_root_markers", "projects",
		"plugins", "marketplaces", "hooks", "features.plugins", "features.plugin_sharing",
		"features.plugin_hooks", "features.remote_plugin", "features.remote_control",
		"shell_environment_policy", "sandbox_workspace_write",
	} {
		cases = append(cases,
			policyCase{[]string{"-c", key + "=1"}, codexConfig + key},
			policyCase{[]string{"-c", "profiles.x." + key + "=1"}, codexConfig + "profiles.x." + key},
			policyCase{[]string{"-c", key + "_x=1"}, ""},
			policyCase{[]string{"-c", "x." + key + "=1"}, ""})
	}
	for _, key := range []string{
		"projects.x", "mcp_servers.x.command", "mcpx", "plugins.x", "marketplaces.x", "hooks.x",
		"shell_environment_policy.set.x", "sandbox_workspace_write.network_access",
	} {
		cases = append(cases,
			policyCase{[]string{"-c", key + "=1"}, codexConfig + key},
			policyCase{[]string{"-c", "profiles.x." + key + "=1"}, codexConfig + "profiles.x." + key})
	}
	for _, key := range []string{"projectsx.y", "pluginsx.y", "marketplacesx.y", "hooksx.y", "shell_environment_policyx.y", "sandbox_workspace_writex.y", "xmcp"} {
		cases = append(cases, policyCase{[]string{"-c", key + "=1"}, ""})
	}
	// An inline table under a guarded parent is refused. A scalar value of
	// an unguarded parent and an inline table of another parent are not.
	for _, key := range []string{
		"features", "plugins", "marketplaces", "projects", "mcp_servers", "hooks", "profiles",
		"profiles.x", "shell_environment_policy", "sandbox_workspace_write",
	} {
		cases = append(cases, policyCase{[]string{"-c", key + "={a=1}"}, codexConfig + key})
	}
	cases = append(cases,
		policyCase{[]string{"-c", "features=1"}, ""},
		policyCase{[]string{"-c", "profiles=1"}, ""},
		policyCase{[]string{"-c", "profiles.x=1"}, ""},
		policyCase{[]string{"-c", "model_providers={a=1}"}, ""},
		policyCase{[]string{"-c", "features= {remote_plugin=true}"}, codexConfig + "features"},
		policyCase{[]string{"-c", "PROFILE=breakglass"}, codexConfig + "PROFILE"},
		// A trailing dot is an empty key segment, and a quote inside a bare
		// segment is malformed; neither is the unguarded key it looks like.
		policyCase{[]string{"-c", "profile.=breakglass"}, codexConfig + "profile."},
		policyCase{[]string{"-c", `fea"tures.remote_plugin=true`}, codexConfig + `fea"tures.remote_plugin`},
		policyCase{[]string{"-c", `"model"="gpt-5"`}, ""},
		policyCase{[]string{"-c", `'features'.'other'=true`}, ""},
	)
	runPolicyCases(t, "codex", Session{}, cases)
}

func TestCodexSessionRules(t *testing.T) {
	for _, tc := range []struct {
		s    Session
		args []string
		want string
	}{
		{Session{Mode: "development"}, []string{"-p", "development"}, ""},
		{Session{Mode: "development"}, []string{"-p", "strict"}, codexUnsafe + "--profile"},
		{Session{Mode: "build"}, []string{"--profile=build"}, ""},
		// An unknown mode allows only the strict profile.
		{Session{Mode: "breakglass"}, []string{"-p", "breakglass"}, codexUnsafe + "--profile"},
		{Session{Mode: "breakglass"}, []string{"-p", "strict"}, ""},
		// Breakglass stands down only for the PID 1 caller outside the wrapper.
		{Session{Mode: "breakglass", CallerIsPID1: true}, []string{"--yolo"}, ""},
		{Session{Mode: "breakglass", CallerIsPID1: true, WrapperContext: "1"}, []string{"--yolo"}, codexUnsafe + "--yolo"},
		{Session{Mode: "breakglass"}, []string{"--yolo"}, codexUnsafe + "--yolo"},
		{Session{Mode: "Breakglass", CallerIsPID1: true}, []string{"--yolo"}, codexUnsafe + "--yolo"},
		// app-server only under AGENT_UI=gui, and then with no argument.
		{Session{AgentUI: "gui"}, []string{"app-server"}, ""},
		{Session{AgentUI: "gui"}, []string{"app"}, ""},
		{Session{AgentUI: "gui"}, []string{"app-server", "--listen", "ws://127.0.0.1:1"}, "Workcell blocked unsupported Codex app-server argument: --listen (only the managed no-arg app-server launch is permitted)"},
		{Session{AgentUI: "gui"}, []string{"app-server", "--"}, "Workcell blocked unsupported Codex app-server argument: -- (only the managed no-arg app-server launch is permitted)"},
		{Session{AgentUI: "GUI"}, []string{"app-server"}, "Workcell blocked unsupported Codex CLI subcommand outside the managed GUI path: app-server"},
		{Session{AgentUI: "gui"}, []string{"plugin"}, codexSub + "plugin"},
	} {
		got := ""
		if err := Codex(tc.args, tc.s); err != nil {
			got = err.Error()
		}
		if got != tc.want {
			t.Errorf("Codex %q (%+v) = %q, want %q", tc.args, tc.s, got, tc.want)
		}
	}
}

func TestClaudePolicy(t *testing.T) {
	const unsafe = "Workcell blocked unsafe Claude override: "
	runPolicyCases(t, "claude", Session{}, []policyCase{
		{nil, ""},
		{[]string{"-p", "fix the tests"}, ""},
		{[]string{"--", "--remote", "task"}, ""},
		{[]string{"--model=x", "--", "--remote"}, ""},
		{[]string{"doctor", "install"}, ""},
		{[]string{"install"}, "Workcell blocked Claude lifecycle command: install"},
		{[]string{"update"}, "Workcell blocked Claude lifecycle command: update"},
		{[]string{"--remote", "--", "task"}, "Workcell blocked Claude cloud execution: --remote"},
		{[]string{"--cloud=x"}, "Workcell blocked Claude cloud execution: --cloud"},
		{[]string{"--debug-file", "--", "--permission-mode", "bypassPermissions"}, "Workcell blocked Claude autonomy override: use the host workcell --agent-autonomy option instead."},
		{[]string{"--permission-mode=plan"}, "Workcell blocked Claude autonomy override: use the host workcell --agent-autonomy option instead."},
		{[]string{"--dangerously-skip-permissions"}, unsafe + "--dangerously-skip-permissions"},
		{[]string{"--allowedTools=Bash"}, unsafe + "--allowedTools"},
		{[]string{"--trust-workspace"}, unsafe + "--trust-workspace"},
		{[]string{"--discard-unpushed=1"}, unsafe + "--discard-unpushed"},
	})
}

func TestGeminiPolicy(t *testing.T) {
	const unsafe = "Workcell blocked unsafe Gemini override: "
	const autonomy = "Workcell blocked Gemini autonomy override: use the host workcell --agent-autonomy option instead."
	runPolicyCases(t, "gemini", Session{}, []policyCase{
		{nil, ""},
		{[]string{"-pyes"}, ""},
		{[]string{"-dl"}, ""},
		{[]string{"--", "-yellow"}, ""},
		{[]string{"-d", "--", "-yellow"}, ""},
		{[]string{"--approval-mode"}, ""},
		{[]string{"-y", "--", "text"}, unsafe + "-y"},
		{[]string{"---", "-y"}, unsafe + "-y"},
		{[]string{"-dy"}, unsafe + "-dy"},
		{[]string{"-DS"}, unsafe + "-DS"},
		{[]string{"--YOLO"}, unsafe + "--YOLO"},
		{[]string{"--allowedTools=x"}, unsafe + "--allowedTools=x"},
		{[]string{"--include-directories", "/"}, unsafe + "--include-directories"},
		// Bash under a UTF-8 locale folded U+0130 to i and U+212A to k. A long
		// option name with a control or non-ASCII byte is refused.
		{[]string{"--\u0130nclude-directories=/"}, unsafe + "--\u0130nclude-directories=/"},
		{[]string{"--yolo\u00a0"}, unsafe + "--yolo\u00a0"},
		{[]string{"--yolo\x7f"}, unsafe + "--yolo\x7f"},
		{[]string{"--model=\u00e9t\u00e9", "-p", "\u00e9t\u00e9"}, ""},
		{[]string{"--model=x-bypass-permission"}, unsafe + "--model=x-bypass-permission"},
		{[]string{"--dangerously-allow-all"}, unsafe + "--dangerously-allow-all"},
		{[]string{"--model=dangerously"}, unsafe + "--model=dangerously"},
		{[]string{"--approval-mode", "yolo"}, autonomy},
		{[]string{"--approvalMode=yolo"}, autonomy},
	})
}

func TestCopilotPolicy(t *testing.T) {
	const unsafe = "Workcell blocked unsafe Copilot override: "
	const lifecycle = "Workcell blocked Copilot lifecycle/control-plane command: "
	runPolicyCases(t, "copilot", Session{}, []policyCase{
		{[]string{"-p", "mcp"}, ""},
		{[]string{"--prompt=mcp"}, ""},
		{[]string{"--model", "gpt-4.1", "-p", "smoke"}, ""},
		{[]string{"--model=gpt-4.1", "--prompt=smoke"}, ""},
		{[]string{"mcp"}, lifecycle + "mcp"},
		{[]string{"logout"}, lifecycle + "logout"},
		{[]string{"--model", "gpt-4.1", "mcp"}, lifecycle + "mcp"},
		{[]string{"--prompt", "smoke", "mcp"}, lifecycle + "mcp"},
		{[]string{"--model", "--yolo"}, unsafe + "--yolo"},
		{[]string{"-p", "--allow-tool=write"}, unsafe + "--allow-tool=write"},
		{[]string{"-p--allow-tool=write"}, unsafe + "--allow-tool=write"},
		{[]string{"--prompt=--allow-tool=write"}, unsafe + "--allow-tool=write"},
		{[]string{"-si"}, "Workcell blocked bundled Copilot short options: -si"},
		{[]string{"-i", "--allow-all-tools"}, unsafe + "-i"},
		{[]string{"--interactive", "--add-dir"}, unsafe + "--interactive"},
		{[]string{"--no-sandbox", "-p", "smoke"}, unsafe + "--no-sandbox"},
		{[]string{"--sandbox", "-p", "smoke"}, unsafe + "--sandbox"},
		{[]string{"--YOLO=1"}, unsafe + "--YOLO"},
		{[]string{"-C"}, unsafe + "-C"},
		{[]string{"-cx"}, unsafe + "-c"},
		{[]string{"-Ax"}, unsafe + "-A"},
		{[]string{"--no-as\u212a-user"}, unsafe + "--no-as\u212a-user"},
		{[]string{"--\u0130nteractive=1"}, unsafe + "--\u0130nteractive"},
		{[]string{"--model\n"}, unsafe + "--model\n"},
		{[]string{"--model\x7f"}, unsafe + "--model\x7f"},
		{[]string{"--prompt=\u00e9t\u00e9"}, ""},
	})
}

func TestManifestPolicy(t *testing.T) {
	fixture := map[string]Adapter{
		"zz-fixture": {Tier: "uncertified", Allow: []string{"-h", "--help", "-m", "--model"}, Deny: []string{"-y", "--yolo"}, OtherCommands: []string{"serve", "mcp"}},
		"zz-empty":   {Tier: "uncertified"},
		"zz-cert":    {Tier: "certified", Allow: []string{"--help"}},
		"codex":      {Tier: "certified"},
	}
	for _, tc := range []struct {
		id   string
		args []string
		want string
	}{
		{"zz-fixture", nil, ""},
		{"zz-fixture", []string{"--model", "x"}, ""},
		{"zz-fixture", []string{"--model=x"}, ""},
		{"zz-fixture", []string{"-m", "x", "-h"}, ""},
		{"zz-fixture", []string{"run", "fix the test"}, ""},
		{"zz-fixture", []string{"--yolo"}, "Workcell blocked a denied zz-fixture option: --yolo"},
		{"zz-fixture", []string{"--yolo=1"}, "Workcell blocked a denied zz-fixture option: --yolo"},
		{"zz-fixture", []string{"--model", "x", "-y"}, "Workcell blocked a denied zz-fixture option: -y"},
		{"zz-fixture", []string{"--Model", "x"}, "Workcell blocked a zz-fixture option that its manifest does not allow: --Model"},
		{"zz-fixture", []string{"-mx"}, "Workcell blocked a zz-fixture option that its manifest does not allow: -mx"},
		{"zz-fixture", []string{"--"}, "Workcell blocked a zz-fixture option that its manifest does not allow: --"},
		{"zz-fixture", []string{"--model", "mcp"}, "Workcell blocked a zz-fixture command that its manifest does not allow: mcp"},
		{"zz-empty", nil, "Workcell has no manifest [flags] allow list for adapter: zz-empty"},
		{"zz-cert", []string{"--help"}, "Workcell has no manifest [flags] allow list for adapter: zz-cert"},
		{"codex", nil, "Workcell has no manifest [flags] allow list for adapter: codex"},
		{"no-such-agent", []string{"--help"}, "Workcell has no manifest [flags] allow list for adapter: no-such-agent"},
	} {
		got := ""
		if err := ManifestArgs(tc.id, tc.args, fixture); err != nil {
			got = err.Error()
		}
		if got != tc.want {
			t.Errorf("ManifestArgs %s %q = %q, want %q", tc.id, tc.args, got, tc.want)
		}
	}
	// Breakglass does not reach an uncertified adapter.
	s := Session{Mode: "breakglass", CallerIsPID1: true}
	if err := Check("zz-fixture", []string{"--yolo"}, s, fixture); err == nil {
		t.Error("breakglass passed a denied uncertified option")
	}
}

func TestValidateCommand(t *testing.T) {
	for _, tc := range []struct {
		expected string
		argv     []string
		want     string
	}{
		{"codex", nil, ""},
		{"codex", []string{"codex"}, ""},
		{"opencode", []string{"opencode", "run", "hi"}, ""},
		{"codex", []string{"bash", "-c", "x"}, "Workcell blocked non-provider command: bash (use the host launcher --allow-arbitrary-command path only for lower-assurance boundary debugging)."},
		{"codex", []string{"codex", "--yolo"}, codexUnsafe + "--yolo"},
		{"amp", []string{"amp", "--dangerously-allow-all"}, "Workcell blocked a denied amp option: --dangerously-allow-all"},
		{"no-such-agent", []string{"no-such-agent", "--help"}, "Workcell has no provider policy for: no-such-agent"},
		{"", []string{""}, "Workcell has no provider policy for: "},
	} {
		got := ""
		if err := ValidateCommand(tc.expected, tc.argv, Session{}, Adapters); err != nil {
			got = err.Error()
		}
		if got != tc.want {
			t.Errorf("ValidateCommand %s %q = %q, want %q", tc.expected, tc.argv, got, tc.want)
		}
	}
	// A certified adapter without a reviewed policy does not fall back to
	// its manifest [flags].
	certified := map[string]Adapter{"zz-cert": {Tier: "certified", Allow: []string{"--help"}}}
	err := ValidateCommand("zz-cert", []string{"zz-cert", "--help"}, Session{}, certified)
	if err == nil || err.Error() != "Workcell has no provider policy for: zz-cert" {
		t.Errorf("ValidateCommand zz-cert = %v, want the no provider policy refusal", err)
	}
}

// TestCodexSubcommandFixtureIsClassified keeps the subcommand gate deny by
// default: a first bare word that is not a known subcommand is prompt text,
// so every subcommand of the pinned Codex must be in the allowed, GUI or
// denied set. The fixture stamp must equal the pinned CODEX_VERSION, or the
// check passes on a stale fixture.
func TestCodexSubcommandFixtureIsClassified(t *testing.T) {
	root := filepath.Join("..", "..")
	fixture, err := os.ReadFile(filepath.Join(root, "tests", "fixtures", "codex-subcommands.txt"))
	if err != nil {
		t.Fatal(err)
	}
	dockerfile, err := os.ReadFile(filepath.Join(root, "runtime", "container", "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	pinned := regexp.MustCompile(`(?m)^ARG CODEX_VERSION=(\S+)`).FindSubmatch(dockerfile)
	stamp := regexp.MustCompile(`(?m)^#\s*codex-version:\s*(\S+)`).FindSubmatch(fixture)
	if pinned == nil || stamp == nil || !bytes.Equal(pinned[1], stamp[1]) {
		t.Fatalf("tests/fixtures/codex-subcommands.txt stamp %q must equal Dockerfile ARG CODEX_VERSION %q; regenerate the fixture from the pinned clap enum Subcommand source and classify each new name", stamp, pinned)
	}
	var names []string
	sc := bufio.NewScanner(bytes.NewReader(fixture))
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" && !strings.HasPrefix(line, "#") {
			names = append(names, line)
		}
	}
	if len(names) == 0 {
		t.Fatal("tests/fixtures/codex-subcommands.txt lists no subcommand")
	}
	sets := [][]string{CodexAllowedSubcommands, CodexGUISubcommands, CodexDeniedSubcommands}
	for _, name := range names {
		n := 0
		for _, set := range sets {
			if slices.Contains(set, name) {
				n++
			}
		}
		if n != 1 {
			t.Errorf("Codex subcommand %q is in %d of the allowed, GUI and denied sets, want exactly 1", name, n)
		}
	}
}
