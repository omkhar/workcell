// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package providerpolicy

import (
	"slices"
	"strings"
)

// CodexAllowedSubcommands is the read-only and session surface of Codex,
// verified against the pinned `codex --help`. `execpolicy` is hidden but
// real: a read-only command classifier that the managed autonomy path and
// verify-invariants invoke. `features` is allowed here, but its enable and
// disable actions are refused.
var CodexAllowedSubcommands = []string{
	"exec", "e", "review", "login", "logout", "completion", "doctor",
	"apply", "a", "resume", "fork", "archive", "unarchive", "delete",
	"help", "execpolicy", "features",
}

// CodexGUISubcommands is the managed GUI backend class, allowed only when
// AGENT_UI is gui and then only with no further argument.
var CodexGUISubcommands = []string{"app", "app-server"}

// CodexDeniedSubcommands is every known control-plane, daemon, marketplace,
// sandbox-escape and self-update subcommand, refused on every UI.
// cloud-tasks is the alias of cloud. responses-api-proxy and stdio-to-uds are
// hidden daemon and bridge subcommands. agents, queue and migrate-rollouts
// reach the shared app-server, change an existing session, or rewrite session
// history. debug reaches the app-server test client and mutates local memory.
var CodexDeniedSubcommands = []string{
	"agents", "queue", "migrate-rollouts", "plugin", "remote-control", "exec-server",
	"mcp", "mcp-server", "tcp-tunnel", "cloud", "cloud-tasks", "responses-api-proxy",
	"stdio-to-uds", "sandbox", "update", "debug",
}

// codexUnsafeOptions die on sight, with the argument in the message.
// --yolo is the hidden alias of --dangerously-bypass-approvals-and-sandbox.
// --approve-for-me and --not-so-yolo pick auto review.
var codexUnsafeOptions = []string{
	"--yolo", "--approve-for-me", "--not-so-yolo", "--ignore-rules", "--search", "--add-dir",
	"--remote", "--remote-auth-token-env", "--full-auto", "-a", "--ask-for-approval", "-s",
	"--sandbox", "--enable", "--disable",
}

// codexUnsafeAttached die in their --name=value form, with the name in the
// message.
var codexUnsafeAttached = []string{
	"--add-dir", "--remote", "--remote-auth-token-env", "--enable", "--disable", "--full-auto",
	"--search", "--ignore-rules",
}

const codexMalformedKey = "__workcell_malformed__"

// Codex is the reviewed Codex CLI argument policy. Value-taking global
// options consume their value before first-subcommand detection, so a value
// is never read as the command (`--model gpt-5 plugin`). Keep the
// value-taking set in lockstep with codex_first_subcommand in
// runtime/container/provider-wrapper.sh.
func Codex(args []string, s Session) error {
	if s.allowsBreakglass() {
		return nil
	}
	const unsafe = "Workcell blocked unsafe Codex override: "
	allowedProfile := s.mode()
	switch allowedProfile {
	case "strict", "development", "build":
	default:
		allowedProfile = "strict"
	}
	profile := func(value string) error {
		// clap accepts the short form with "=" (-p=VALUE).
		value = strings.TrimPrefix(value, "=")
		if value != allowedProfile {
			return refuse(unsafe + "--profile")
		}
		return nil
	}
	config := func(value, shown string) error {
		if codexConfigOverrideIsBlocked(value) {
			return refuse("Workcell blocked unsafe Codex config override: " + shown)
		}
		return nil
	}

	expect := ""
	sawCommand := false
	expectFeaturesAction := false
	sawAppServer := false
	for _, arg := range args {
		if expect != "" {
			var err error
			switch expect {
			case "profile":
				err = profile(arg)
			case "cd":
				err = refuse(unsafe + "--cd")
			case "config":
				err = config(arg, beforeEqual(arg))
			}
			if err != nil {
				return err
			}
			expect = ""
			continue
		}

		// The managed GUI launch is exactly bare `codex app-server`. Any
		// token after it, including one after --, is refused.
		if sawAppServer {
			return refuse("Workcell blocked unsupported Codex app-server argument: " + arg + " (only the managed no-arg app-server launch is permitted)")
		}

		// After `features`, the next bare token is its action. enable and
		// disable write features.<name> into the writable config.toml, so
		// both are refused; `features list` stays allowed. A -- while armed
		// is skipped and does not end the scan. An option leaves the arm in
		// place.
		if expectFeaturesAction {
			if arg == "--" {
				continue
			}
			if !strings.HasPrefix(arg, "-") {
				expectFeaturesAction = false
				if arg == "enable" || arg == "disable" {
					return refuse("Workcell blocked unsupported Codex CLI subcommand: features " + arg)
				}
				continue
			}
		}

		// A bare -- ends option and subcommand parsing: Codex forwards every
		// later token as prompt text.
		if arg == "--" {
			break
		}

		if !sawCommand && !strings.HasPrefix(arg, "-") {
			sawCommand = true
			// Codex dispatches on the first bare token: a known subcommand
			// name, or else prompt text. The fixture check keeps every real
			// subcommand in one of the three sets.
			switch {
			case arg == "features":
				expectFeaturesAction = true
			case slices.Contains(CodexAllowedSubcommands, arg):
			case slices.Contains(CodexGUISubcommands, arg):
				ui := s.AgentUI
				if ui == "" {
					ui = "cli"
				}
				if ui != "gui" {
					return refuse("Workcell blocked unsupported Codex CLI subcommand outside the managed GUI path: " + arg)
				}
				sawAppServer = true
			case slices.Contains(CodexDeniedSubcommands, arg):
				return refuse("Workcell blocked unsupported Codex CLI subcommand: " + arg)
			}
			continue
		}

		name, _, attached := strings.Cut(arg, "=")
		switch {
		// Every --dangerously-bypass-* option is unsafe, so a future bypass
		// option needs no code change. The scope is not --dangerously-*, so
		// Claude's --dangerously-skip-permissions stays data for
		// `codex execpolicy check`.
		case strings.HasPrefix(arg, "--dangerously-bypass-"),
			slices.Contains(codexUnsafeOptions, arg),
			attached && (name == "--yolo" || name == "--approve-for-me" || name == "--not-so-yolo"):
			return refuse(unsafe + arg)
		// clap also takes a short option glued to its value (-pVALUE).
		case strings.HasPrefix(arg, "-a") && len(arg) > 2:
			return refuse(unsafe + arg)
		case strings.HasPrefix(arg, "-C") && len(arg) > 2:
			return refuse(unsafe + "--cd")
		case strings.HasPrefix(arg, "-p") && len(arg) > 2:
			if err := profile(arg[2:]); err != nil {
				return err
			}
		case strings.HasPrefix(arg, "-s") && len(arg) > 2, strings.HasPrefix(arg, "--sandbox="):
			return refuse(unsafe + "--sandbox")
		case arg == "-p", arg == "--profile":
			expect = "profile"
		case arg == "-C", arg == "--cd":
			expect = "cd"
		case arg == "-m", arg == "--model", arg == "-i", arg == "--image", arg == "--local-provider":
			// Allowed value-taking globals: consume the value so it is never
			// read as the first subcommand.
			expect = "safe"
		case strings.HasPrefix(arg, "--ask-for-approval="):
			return refuse(unsafe + "--ask-for-approval")
		case attached && slices.Contains(codexUnsafeAttached, name):
			return refuse(unsafe + name)
		case strings.HasPrefix(arg, "--cd="):
			return refuse(unsafe + "--cd")
		case strings.HasPrefix(arg, "--profile="):
			if err := profile(arg[len("--profile="):]); err != nil {
				return err
			}
		case arg == "-c", arg == "--config":
			expect = "config"
		case strings.HasPrefix(arg, "--config="):
			v := arg[len("--config="):]
			if err := config(v, v); err != nil {
				return err
			}
		case strings.HasPrefix(arg, "-c") && len(arg) > 2:
			// -cKEY=VALUE and clap's -c=KEY=VALUE.
			v := strings.TrimPrefix(arg[2:], "=")
			if err := config(v, v); err != nil {
				return err
			}
		}
	}
	return nil
}

// codexNormalizeConfigKey canonicalizes the key of a Codex `-c key=value`
// override, the text before the first "=", so equivalent TOML spellings meet
// one lowercase form: split on ".", trim whitespace from each segment
// (`features . plugins`), and strip one surrounding quote pair from each
// segment (`features."remote_plugin"`). A control byte other than tab, a
// non-ASCII byte, a residual quote, or a backslash (a TOML escape that the
// policy does not decode) gives codexMalformedKey, which fails closed. Real
// guarded keys never contain them.
func codexNormalizeConfigKey(value string) string {
	key := beforeEqual(value)
	if hasUnsafeByte(key) {
		return codexMalformedKey
	}
	if key == "" {
		return ""
	}
	segments := strings.Split(key, ".")
	// A trailing "." adds no empty segment.
	if segments[len(segments)-1] == "" {
		segments = segments[:len(segments)-1]
	}
	for i, segment := range segments {
		segment = strings.Trim(segment, cSpace)
		if len(segment) >= 2 && (segment[0] == '"' || segment[0] == '\'') && segment[len(segment)-1] == segment[0] {
			segment = segment[1 : len(segment)-1]
		}
		for j := 0; j < len(segment); j++ {
			if segment[j] == '"' || segment[j] == '\'' || segment[j] == '\\' {
				return codexMalformedKey
			}
		}
		segments[i] = segment
	}
	return asciiLower(strings.Join(segments, "."))
}

// codexConfigKeyIsGuarded matches a normalized key against the guarded
// namespaces. The profile-scoped path tests its remainder against the same
// set, because a profile layer can set any key.
func codexConfigKeyIsGuarded(key string) bool {
	switch key {
	case "profile", "sandbox", "sandbox_mode", "sandbox_permissions", "web_search", "approval_policy",
		"approvals_reviewer", "project_doc_fallback_filenames", "project_root_markers", "projects",
		"plugins", "marketplaces", "hooks", "features.plugins", "features.plugin_sharing",
		"features.plugin_hooks", "features.remote_plugin", "features.remote_control",
		"shell_environment_policy", "sandbox_workspace_write":
		return true
	}
	for _, prefix := range []string{"projects.", "mcp", "plugins.", "marketplaces.", "hooks.", "shell_environment_policy.", "sandbox_workspace_write."} {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}

// codexConfigOverrideIsBlocked reports whether a `-c key=value` override
// reaches a guarded namespace.
func codexConfigOverrideIsBlocked(value string) bool {
	key := codexNormalizeConfigKey(value)
	if key == codexMalformedKey || codexConfigKeyIsGuarded(key) {
		return true
	}
	// `profiles.<name>.<rest>` can set any key. The normalizer keeps <name>
	// one segment (a "." in a quoted name fails closed), so strip
	// `profiles.<name>.` and test the remainder.
	if rest, ok := strings.CutPrefix(key, "profiles."); ok && hasInnerDot(rest) {
		if _, tail, _ := strings.Cut(rest, "."); codexConfigKeyIsGuarded(tail) {
			return true
		}
	}
	// Codex parses the value as TOML, so an inline table hides guarded
	// children under an unguarded parent (`features={remote_plugin=true}`).
	// The managed baseline owns these tables, so a whole-table override of a
	// guarded parent is refused. A scalar value is not.
	if _, raw, ok := strings.Cut(value, "="); ok {
		raw = strings.TrimLeft(raw, cSpace)
		// Codex trims Unicode whitespace that the policy keeps, so a value
		// that starts with a control or non-ASCII byte fails closed.
		if raw != "" && isUnsafeByte(raw[0]) {
			return true
		}
		if strings.HasPrefix(raw, "{") {
			switch {
			case key == "features", key == "plugins", key == "marketplaces", key == "projects",
				strings.HasPrefix(key, "mcp"), key == "hooks", key == "profiles", strings.HasPrefix(key, "profiles."),
				key == "shell_environment_policy", key == "sandbox_workspace_write":
				return true
			}
		}
	}
	return false
}

// hasInnerDot reports whether s has a "." with at least one byte before it
// and one after it.
func hasInnerDot(s string) bool {
	for i := 1; i < len(s)-1; i++ {
		if s[i] == '.' {
			return true
		}
	}
	return false
}
