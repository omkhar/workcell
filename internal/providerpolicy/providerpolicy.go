// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

// Package providerpolicy holds every provider argument policy of the runtime:
// the reviewed policies of the certified adapters (codex, claude, gemini and
// copilot) and the manifest allowlist of each uncertified adapter. The
// container entrypoint and the provider wrapper run it through
// workcell-provider-policy before they start a provider.
//
// The policies compare bytes. Lowercasing folds ASCII letters only, and a
// character class means its C-locale (POSIX) set, because the runtime image
// sets no locale.
package providerpolicy

import (
	"slices"
	"strings"
)

// Session is the launch context that the policies read.
type Session struct {
	// Mode is WORKCELL_MODE. An empty mode is strict.
	Mode string
	// AgentUI is AGENT_UI. An empty value is cli.
	AgentUI string
	// WrapperContext is WORKCELL_WRAPPER_CONTEXT. The provider wrapper sets 1.
	WrapperContext string
	// CallerIsPID1 is true when the caller is the container init process.
	CallerIsPID1 bool
}

func (s Session) mode() string {
	if s.Mode == "" {
		return "strict"
	}
	return s.Mode
}

// allowsBreakglass reports whether the reviewed policies stand down. Only the
// PID 1 entrypoint may honor breakglass. The provider wrapper sets
// WORKCELL_WRAPPER_CONTEXT=1, so a nested launch cannot enable it again.
func (s Session) allowsBreakglass() bool {
	return s.WrapperContext != "1" && s.CallerIsPID1 && s.mode() == "breakglass"
}

// Adapter is the generated policy data of one supported adapter.
type Adapter struct {
	// Tier is the manifest tier: certified or uncertified.
	Tier string
	// Allow, Deny and OtherCommands are the [flags] lists of an uncertified
	// adapter.
	Allow, Deny, OtherCommands []string
}

// Refusal is a policy decision to stop the launch. Its message is the
// operator text.
type Refusal struct{ Message string }

func (r *Refusal) Error() string { return r.Message }

func refuse(message string) error { return &Refusal{Message: message} }

// ValidateCommand checks the full command of the entrypoint: argv[0] must be
// expected, and the policy of expected checks the remaining arguments. An
// empty argv starts the default command.
func ValidateCommand(expected string, argv []string, s Session, adapters map[string]Adapter) error {
	if len(argv) == 0 {
		return nil
	}
	if argv[0] != expected {
		return refuse("Workcell blocked non-provider command: " + argv[0] + " (use the host launcher --allow-arbitrary-command path only for lower-assurance boundary debugging).")
	}
	args := argv[1:]
	switch expected {
	case "codex", "claude", "copilot", "gemini":
		return Check(expected, args, s, adapters)
	}
	// A provider without a reviewed policy runs only as an uncertified
	// adapter, behind its manifest allowlist.
	if a, ok := adapters[expected]; !ok || a.Tier != "uncertified" {
		return refuse("Workcell has no provider policy for: " + expected)
	}
	return ManifestArgs(expected, args, adapters)
}

// Check runs the provider policy of id over the provider arguments args. The
// provider wrapper calls it after it fixes the launch target.
func Check(id string, args []string, s Session, adapters map[string]Adapter) error {
	switch id {
	case "codex":
		return Codex(args, s)
	case "claude":
		return Claude(args, s)
	case "gemini":
		return Gemini(args, s)
	case "copilot":
		return Copilot(args, s)
	}
	return ManifestArgs(id, args, adapters)
}

// ManifestArgs passes the provider arguments of uncertified adapter id only
// through its manifest [flags]. An option, compared before any "=value", must
// be in allow and not in deny. A word that names one of other_commands is
// refused. This is the only policy for a CLI that no review certified, so it
// applies in every mode, breakglass included.
func ManifestArgs(id string, args []string, adapters map[string]Adapter) error {
	a, ok := adapters[id]
	if !ok || a.Tier != "uncertified" || len(a.Allow) == 0 {
		return refuse("Workcell has no manifest [flags] allow list for adapter: " + id)
	}
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			name := beforeEqual(arg)
			if slices.Contains(a.Deny, name) {
				return refuse("Workcell blocked a denied " + id + " option: " + name)
			}
			if !slices.Contains(a.Allow, name) {
				return refuse("Workcell blocked a " + id + " option that its manifest does not allow: " + name)
			}
		} else if slices.Contains(a.OtherCommands, arg) {
			return refuse("Workcell blocked a " + id + " command that its manifest does not allow: " + arg)
		}
	}
	return nil
}

// Claude is the reviewed Claude Code argument policy.
func Claude(args []string, s Session) error {
	if s.allowsBreakglass() {
		return nil
	}
	sawCommand := false
	afterBareOption := false
	for _, arg := range args {
		// Claude reads everything after a bare -- as prompt text, unless the
		// option before it takes a value: `--debug-file -- --permission-mode x`
		// names the file "--" and then applies the mode. Arity is unknown
		// here, so a -- right after a bare option never ends the scan.
		if arg == "--" && !afterBareOption {
			break
		}
		afterBareOption = strings.HasPrefix(arg, "-") && !strings.Contains(arg, "=")
		if !sawCommand && !strings.HasPrefix(arg, "-") {
			sawCommand = true
			switch arg {
			case "install", "update", "upgrade", "purge":
				return refuse("Workcell blocked Claude lifecycle command: " + arg)
			}
			continue
		}
		name, _, attached := strings.Cut(arg, "=")
		switch {
		case slices.Contains(claudeUnsafeOptions, name):
			if attached {
				return refuse("Workcell blocked unsafe Claude override: " + name)
			}
			return refuse("Workcell blocked unsafe Claude override: " + arg)
		case name == "--permission-mode":
			return refuse("Workcell blocked Claude autonomy override: use the host workcell --agent-autonomy option instead.")
		case name == "--cloud", name == "--remote", name == "--environment":
			// Cloud sessions leave the sandbox.
			return refuse("Workcell blocked Claude cloud execution: " + name)
		case name == "--discard-unpushed", name == "--force-remove-worktree", name == "--trust-workspace":
			// These delete unpushed work or skip the workspace trust prompt.
			return refuse("Workcell blocked unsafe Claude override: " + name)
		}
	}
	return nil
}

var claudeUnsafeOptions = []string{
	"--dangerously-skip-permissions", "--allow-dangerously-skip-permissions", "--add-dir",
	"--allowedTools", "--allowed-tools", "--mcp-config", "--plugin-dir", "--plugin-url",
	"--settings", "--setting-sources", "--system-prompt", "--system-prompt-file",
	"--append-system-prompt", "--append-system-prompt-file", "--append-subagent-system-prompt",
	"--append-subagent-system-prompt-file", "--agents", "--permission-prompt-tool",
}

// Gemini is the reviewed Gemini CLI argument policy.
func Gemini(args []string, s Session) error {
	if s.allowsBreakglass() {
		return nil
	}
	const autonomy = "Workcell blocked Gemini autonomy override: use the host workcell --agent-autonomy option instead."
	expectApprovalMode := false
	for _, arg := range args {
		if expectApprovalMode {
			return refuse(autonomy)
		}
		if arg == "--" { // yargs: a bare -- ends options; --- is a positional
			break
		}
		lower := asciiLower(arg)
		// yargs also accepts the camel-case spelling of a dashed option
		// (--allowedTools for --allowed-tools). Drop the dashes after the
		// leading -- so each spelling reaches the same key.
		key := lower
		if strings.HasPrefix(lower, "--") {
			key = "--" + strings.ReplaceAll(lower[2:], "-", "")
		}
		switch {
		case geminiUnsafeKey(key):
			return refuse("Workcell blocked unsafe Gemini override: " + arg)
		case len(key) >= 2 && key[0] == '-' && key[1] != '-':
			// yargs reads -dy as -d and -y. Scan the short group up to the
			// first option that takes a value; the rest of the group is
			// that value.
		group:
			for i := 1; i < len(lower); i++ {
				switch lower[i] {
				case 'y', 's':
					return refuse("Workcell blocked unsafe Gemini override: " + arg)
				case 'm', 'p', 'i', 'w', 'e', 'r', 'o':
					break group
				}
			}
		case key == "--approvalmode":
			expectApprovalMode = true
		case strings.HasPrefix(key, "--approvalmode="):
			return refuse(autonomy)
		}
	}
	return nil
}

func geminiUnsafeKey(key string) bool {
	if !strings.HasPrefix(key, "--") {
		return false
	}
	rest := key[2:]
	if strings.Contains(rest, "dangerously") {
		return true
	}
	if i := strings.Index(rest, "bypass"); i >= 0 && strings.Contains(rest[i+len("bypass"):], "permission") {
		return true
	}
	name := beforeEqual(key)
	switch name {
	case "--sandbox", "--adddir", "--includedirectories", "--allowedtools", "--policy", "--adminpolicy", "--yolo":
		return true
	}
	return false
}

// Copilot is the reviewed GitHub Copilot CLI argument policy.
func Copilot(args []string, s Session) error {
	if s.allowsBreakglass() {
		return nil
	}
	const unsafe = "Workcell blocked unsafe Copilot override: "
	expectValue := false
	sawCommand := false
	for _, arg := range args {
		if expectValue {
			if strings.HasPrefix(arg, "-") {
				return refuse(unsafe + arg)
			}
			expectValue = false
			continue
		}
		if !sawCommand && !strings.HasPrefix(arg, "-") {
			sawCommand = true
			switch arg {
			case "config", "init", "login", "logout", "mcp", "plugin", "sandbox", "skill", "update":
				return refuse("Workcell blocked Copilot lifecycle/control-plane command: " + arg)
			}
			continue
		}
		lowerName, _, attached := strings.Cut(asciiLower(arg), "=")
		if slices.Contains(copilotUnsafeOptions, lowerName) {
			if attached {
				return refuse(unsafe + beforeEqual(arg))
			}
			return refuse(unsafe + arg)
		}
		switch {
		case arg == "-p", arg == "--prompt", arg == "--model":
			expectValue = true
		case strings.HasPrefix(arg, "-p") && len(arg) > 2:
			if v := arg[2:]; strings.HasPrefix(v, "-") {
				return refuse(unsafe + v)
			}
		case strings.HasPrefix(arg, "--prompt="):
			if v := arg[len("--prompt="):]; strings.HasPrefix(v, "-") {
				return refuse(unsafe + v)
			}
		case strings.HasPrefix(arg, "--model="):
		case arg == "-C", arg == "-i", arg == "-n", arg == "-r", arg == "-w", arg == "-A", arg == "-a":
			return refuse(unsafe + arg)
		case len(arg) > 2 && arg[0] == '-' && strings.Contains("CcinrwAa", arg[1:2]):
			return refuse(unsafe + arg[:2])
		case len(arg) > 2 && arg[0] == '-' && arg[1] != '-':
			return refuse("Workcell blocked bundled Copilot short options: " + arg)
		}
	}
	return nil
}

// copilotUnsafeOptions are compared lowercase, before any "=value".
var copilotUnsafeOptions = []string{
	"--acp", "--add-dir", "--add-github-mcp-tool", "--add-github-mcp-toolset", "--additional-mcp-config",
	"--agent", "--allow-all", "--allow-all-mcp-server-instructions", "--allow-all-paths",
	"--allow-all-tools", "--allow-all-urls", "--allow-tool", "--allow-url", "--assisted-approval",
	"--attachment", "--autopilot", "--available-tools", "--bash-env", "-c", "--config-dir", "--connect",
	"--continue", "--deny-tool", "--deny-url", "--disable-builtin-mcps", "--disable-mcp-server",
	"--disallow-temp-dir", "--dynamic-retrieval", "--enable-all-github-mcp-tools", "--enable-memory",
	"--excluded-tools", "--experimental", "--extension-sdk-path", "--host-compute-id",
	"--host-environment", "--interactive", "--log-dir", "--max-autopilot-continues", "--mcp-github-auth",
	"--mode", "--name", "--no-ask-user", "--no-auto-update", "--no-bash-env", "--no-custom-instructions",
	"--no-remote", "--no-remote-export", "--no-sandbox", "--output-format", "--plan", "--plugin-dir",
	"--remote", "--remote-export", "--resume", "--sandbox", "--secret-env-vars", "--session-id",
	"--share", "--share-gist", "--sync-session", "--trust-folder", "--worktree", "--yolo",
}
