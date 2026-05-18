// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/omkhar/workcell/internal/authpolicy"
	"github.com/omkhar/workcell/internal/authresolve"
	"github.com/omkhar/workcell/internal/cliexit"
	"github.com/omkhar/workcell/internal/host/hoststate"
	"github.com/omkhar/workcell/internal/host/launcher"
	"github.com/omkhar/workcell/internal/host/release"
	"github.com/omkhar/workcell/internal/host/sessions"
	"github.com/omkhar/workcell/internal/host/stateroot"
	"github.com/omkhar/workcell/internal/host/supportmatrix"
	"github.com/omkhar/workcell/internal/injection"
	"github.com/omkhar/workcell/internal/publishpr"
	"github.com/omkhar/workcell/internal/sessionctl"
	"github.com/omkhar/workcell/internal/transcript"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		// All Go CLI translations (authpolicy, publishpr, sessionctl,
		// etc.) funnel their bash exit-code contract through the
		// canonical cliexit.ExitCodeError so a single errors.As is
		// enough to recover {Code, Message}.
		var ec *cliexit.ExitCodeError
		if errors.As(err, &ec) {
			if msg := ec.Error(); msg != "" {
				fmt.Fprintln(os.Stderr, msg)
			}
			os.Exit(ec.Code)
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return usage()
	}

	switch args[0] {
	case "launcher":
		return runLauncherCompat(args[1:])
	case "path":
		return runPath(args[1:])
	case "release":
		return runRelease(args[1:])
	case "helper":
		return runHelper(args[1:])
	case "policy":
		return runHostutilPolicy(args[1:])
	case "resolve-credentials":
		return runHostutilResolveCredentials(args[1:])
	case "pty-transcript":
		return runHostutilPTYTranscript(args[1:])
	// Top-level *-cli and *-usage entry points promoted out of the
	// former `launcher` umbrella so the dispatch table only retains
	// stateless helpers.  Each case delegates to the same handler the
	// helper table used to call; no behavioural change.
	case "auth-cli":
		return cmdHelperAuthCli(args[1:])
	case "auth-usage":
		return cmdHelperAuthUsage(args[1:])
	case "policy-cli":
		return cmdHelperPolicyCli(args[1:])
	case "policy-usage":
		return cmdHelperPolicyUsage(args[1:])
	case "publish-pr-cli":
		return cmdHelperPublishPRCli(args[1:])
	case "publish-pr-usage":
		return cmdHelperPublishPRUsage(args[1:])
	case "session-usage":
		return cmdHelperSessionUsage(args[1:])
	case "session-attach-cli":
		return cmdHelperSessionAttachCli(args[1:])
	case "session-delete-cli":
		return cmdHelperSessionDeleteCli(args[1:])
	case "session-dispatch-cli":
		return cmdHelperSessionDispatchCli(args[1:])
	case "session-logs-cli":
		return cmdHelperSessionLogsCli(args[1:])
	case "session-monitor-cli":
		return cmdHelperSessionMonitorCli(args[1:])
	case "session-send-cli":
		return cmdHelperSessionSendCli(args[1:])
	case "session-stop-cli":
		return cmdHelperSessionStopCli(args[1:])
	case "session-timeline-cli":
		return cmdHelperSessionTimelineCli(args[1:])
	default:
		return usage()
	}
}

func runLauncherCompat(args []string) error {
	if len(args) == 0 {
		return launcherUsage()
	}
	if args[0] == "helper" {
		return runHelper(args[1:])
	}
	switch args[0] {
	case "auth-cli":
		return cmdHelperAuthCli(args[1:])
	case "auth-usage":
		return cmdHelperAuthUsage(args[1:])
	case "policy-cli":
		return cmdHelperPolicyCli(args[1:])
	case "policy-usage":
		return cmdHelperPolicyUsage(args[1:])
	case "publish-pr-cli":
		return cmdHelperPublishPRCli(args[1:])
	case "publish-pr-usage":
		return cmdHelperPublishPRUsage(args[1:])
	case "session-usage":
		return cmdHelperSessionUsage(args[1:])
	case "session-attach-cli":
		return cmdHelperSessionAttachCli(args[1:])
	case "session-delete-cli":
		return cmdHelperSessionDeleteCli(args[1:])
	case "session-dispatch-cli":
		return cmdHelperSessionDispatchCli(args[1:])
	case "session-logs-cli":
		return cmdHelperSessionLogsCli(args[1:])
	case "session-monitor-cli":
		return cmdHelperSessionMonitorCli(args[1:])
	case "session-send-cli":
		return cmdHelperSessionSendCli(args[1:])
	case "session-stop-cli":
		return cmdHelperSessionStopCli(args[1:])
	case "session-timeline-cli":
		return cmdHelperSessionTimelineCli(args[1:])
	default:
		return runHelper(args)
	}
}

// runHostutilPolicy dispatches the absorbed workcell-manage-injection-policy
// CLI surface (scripts/lib/manage_injection_policy callers). This is the
// **manage injection-policy** path: it edits the on-disk injection-policy
// TOML files. Not to be confused with `workcell-hostutil policy-cli`
// below, which is the **`workcell policy <subcommand>`** Go translation
// of the bash policy_main user-shell command (init/show/...).
// authpolicy.Run returns a *cliexit.ExitCodeError directly, so we can
// propagate it without re-wrapping; main()'s typed handler preserves
// the stdout/stderr/exit-code contract.
func runHostutilPolicy(args []string) error {
	return authpolicy.Run("workcell-hostutil policy", args, os.Stdout, os.Stderr)
}

// runHostutilResolveCredentials dispatches the absorbed
// workcell-resolve-credential-sources CLI surface.  authresolve.Run
// writes diagnostics to stderr and returns a *cliexit.ExitCodeError
// directly, so we can propagate it untouched and let main()'s typed
// handler preserve the bash exit-code contract.
func runHostutilResolveCredentials(args []string) error {
	return authresolve.Run(args, os.Stdout, os.Stderr)
}

// runHostutilPTYTranscript dispatches the absorbed
// workcell-pty-transcript CLI surface.  transcript.Run wants the
// raw *os.File for stdin/stdout (it tees PTY output and stamps
// timestamps), so we forward os.Stdin/os.Stdout directly.  Run returns
// a *cliexit.ExitCodeError directly, so we propagate it untouched.
func runHostutilPTYTranscript(args []string) error {
	return transcript.Run("workcell-hostutil pty-transcript", os.Stdin, os.Stdout, os.Stderr, args)
}

func runPath(args []string) error {
	if len(args) == 0 {
		return pathUsage()
	}

	switch args[0] {
	case "home":
		if len(args) != 1 {
			return pathUsage()
		}
		home, err := launcher.RealHome()
		if err != nil {
			return err
		}
		fmt.Println(home)
		return nil
	case "resolve":
		base := ""
		pathArgs := args[1:]
		if len(pathArgs) == 3 && pathArgs[0] == "--base" {
			base = pathArgs[1]
			pathArgs = pathArgs[2:]
		}
		if len(pathArgs) != 1 {
			return pathUsage()
		}
		resolved, err := launcher.CanonicalizePathFrom(pathArgs[0], base)
		if err != nil {
			return err
		}
		fmt.Println(resolved)
		return nil
	default:
		return pathUsage()
	}
}

func runRelease(args []string) error {
	if len(args) == 0 {
		return releaseUsage()
	}

	switch args[0] {
	case "create-payload":
		if len(args) != 3 {
			return releaseUsage()
		}
		return release.WriteGitHubReleaseCreatePayload(args[1], args[2])
	case "metadata":
		if len(args) < 4 {
			return releaseUsage()
		}
		return release.WriteGitHubReleaseMetadata(args[1], args[3:], args[2])
	case "encode-name":
		if len(args) != 2 {
			return releaseUsage()
		}
		fmt.Println(release.EncodeReleaseAssetName(args[1]))
		return nil
	case "bundle-manifest":
		if len(args) != 8 {
			return releaseUsage()
		}
		sourceDateEpoch, err := strconv.ParseInt(args[5], 10, 64)
		if err != nil {
			return fmt.Errorf("parse source_date_epoch: %w", err)
		}
		return release.WriteReleaseBundleManifest(args[1], args[2], args[3], args[4], sourceDateEpoch, args[6], args[7])
	default:
		return releaseUsage()
	}
}

// helperSubcommand describes one workcell-hostutil helper subcommand.
// minArgs and maxArgs count only the args that follow the subcommand
// name; a maxArgs of -1 means unbounded.
//
// PR-FIX-11 renamed this from launcherSubcommand to helperSubcommand
// after every `*-cli`/`*-usage` row was promoted to a top-level
// workcell-hostutil subcommand.  The helper table now holds only
// stateless helpers (cache keys, path math, validators, etc.).
type helperSubcommand struct {
	name    string
	minArgs int
	maxArgs int
	handler func(args []string) error
}

// helperSubcommands returns the dispatch table.  It is a function (not
// a package-level var) because several of the session handlers below
// call back into helperUsage, which itself reads this table; using a
// function defers the table's construction past package-init time and
// avoids a Go initialization cycle.
func helperSubcommands() []helperSubcommand {
	return []helperSubcommand{
		{"session-suffix", 0, 0, cmdHelperSessionSuffix},
		{"colima-status", 1, 1, cmdHelperColimaStatus},
		{"validate-colima-status", 1, 1, cmdHelperValidateColimaStatus},
		{"run-host-colima-with-timeout", 1, -1, cmdHelperRunHostColimaWithTimeout},
		{"docker-desktop-context-name", 0, 0, cmdHelperDockerDesktopContextName},
		{"route-profile-docker-command", 1, -1, cmdHelperRouteProfileDockerCommand},
		{"prepare-current-docker-client-plan", 1, -1, cmdHelperPrepareCurrentDockerClientPlan},
		{"cleanup-stale-log-pointers", 1, 1, cmdHelperCleanupStaleLogPointers},
		{"profile-lock-is-stale", 1, 1, cmdHelperProfileLockIsStale},
		{"acquire-profile-lock", 2, 2, cmdHelperAcquireProfileLock},
		{"write-profile-owner", 2, 2, cmdHelperWriteProfileOwner},
		{"cleanup-stale-session-audit-dirs", 1, 1, cmdHelperCleanupStaleSessionAuditDirs},
		{"session-record-write", 2, -1, cmdHelperSessionRecordWrite},
		{"session-list", 0, -1, runHelperSessionList},
		{"session-show", 0, -1, runHelperSessionShow},
		{"session-export", 0, -1, runHelperSessionExport},
		{"session-diff-metadata", 0, -1, runHelperSessionDiffMetadata},
		{"session-runtime-metadata", 0, -1, runHelperSessionRuntimeMetadata},
		{"session-timeline", 0, -1, runHelperSessionTimeline},
		{"audit-digest", 2, -1, cmdHelperAuditDigest},
		{"direct-mount-cache-key", 2, 2, cmdHelperDirectMountCacheKey},
		{"resolve-host-output-candidate", 1, 1, cmdHelperResolveHostOutputCandidate},
		{"resolve-host-output-directory-candidate", 1, 1, cmdHelperResolveHostOutputDirectoryCandidate},
		{"cleanup-stale-injection-bundles", 1, 1, cmdHelperCleanupStaleInjectionBundles},
		{"manifest-metadata", 1, 1, cmdHelperManifestMetadata},
		{"resolver-metadata", 1, 1, cmdHelperResolverMetadata},
		{"workspace-cache-key", 1, 1, cmdHelperWorkspaceCacheKey},
		{"extract-codex-version", 1, 1, cmdHelperExtractCodexVersion},
		{"validate-security-options", 1, 1, cmdHelperValidateSecurityOptions},
		{"validate-compat-security-options", 1, 1, cmdHelperValidateCompatSecurityOptions},
		{"validate-container-security-options", 1, 1, cmdHelperValidateContainerSecurityOptions},
		{"canonicalize-tool-path", 1, 1, cmdHelperCanonicalizeToolPath},
		{"dedupe-endpoints", 1, 1, cmdHelperDedupeEndpoints},
		{"resolve-endpoints", 1, 1, cmdHelperResolveEndpoints},
		{"support-matrix-eval", 6, 6, cmdHelperSupportMatrixEval},
		{"profile-path", 1, -1, cmdHelperProfilePath},
		// PR 23.4 — injection bundle preparation moved into Go.
		{"injection-stage-direct-mounts", 2, 2, cmdHelperInjectionStageDirectMounts},
		{"injection-prepare-bundle", 0, -1, cmdHelperInjectionPrepareBundle},
		// lookup-state-roots takes the two state-root values as
		// positional args (in WORKCELL,COLIMA order) so the bash shim
		// does not need to leak them through env -i to the cleaned
		// host process; bash forwards `${WORKCELL_STATE_ROOT:-} ${COLIMA_STATE_ROOT:-}`
		// directly on the command line.
		{"lookup-state-roots", 2, 2, cmdHelperLookupStateRoots},
	}
}

func runHelper(args []string) error {
	if len(args) == 0 {
		return helperUsage()
	}
	rest := args[1:]
	for _, sub := range helperSubcommands() {
		if sub.name != args[0] {
			continue
		}
		if len(rest) < sub.minArgs {
			return helperUsage()
		}
		if sub.maxArgs >= 0 && len(rest) > sub.maxArgs {
			return helperUsage()
		}
		return sub.handler(rest)
	}
	return helperUsage()
}

func cmdHelperSessionUsage(_ []string) error {
	fmt.Print(sessionctl.UsageText())
	return nil
}

func cmdHelperAuthUsage(_ []string) error {
	fmt.Print(authpolicy.AuthUsageText())
	return nil
}

func cmdHelperAuthCli(args []string) error {
	return authpolicy.AuthMain(args)
}

func cmdHelperPolicyUsage(_ []string) error {
	fmt.Print(authpolicy.PolicyUsageText())
	return nil
}

func cmdHelperPublishPRUsage(_ []string) error {
	fmt.Print(publishpr.UsageText())
	return nil
}

// cmdHelperPolicyCli is the top-level `workcell-hostutil policy-cli`
// entry point for the Go translation of scripts/workcell's policy_main
// bash function — the **user-facing `workcell policy <subcommand>`**
// surface (init, show, etc.). Not to be confused with `workcell-hostutil
// policy` (above), which is the **manage-injection-policy** TOML-editing
// surface absorbed from the former workcell-manage-injection-policy
// binary.  PR-FIX-11 promoted this entry point out of the helper
// dispatch table.
// Usage errors (missing/unknown subcommand, unknown option) exit with
// code 2 to match the bash CLI surface; all other errors propagate to
// main() for the default exit-1 path.
func cmdHelperPolicyCli(args []string) error {
	err := authpolicy.PolicyMain(args)
	if authpolicy.IsPolicyMainUsageError(err) {
		return &cliexit.ExitCodeError{Code: 2}
	}
	return err
}

func cmdHelperPublishPRCli(args []string) error {
	return publishpr.PublishPRMain(args, os.Stdin, os.Stdout, os.Stderr)
}

func cmdHelperSessionTimelineCli(args []string) error {
	return sessionctl.TimelineMain(args)
}

func cmdHelperSessionLogsCli(args []string) error {
	return sessionctl.LogsMain(args)
}

func cmdHelperSessionAttachCli(args []string) error {
	return sessionctl.AttachMain(args)
}

func cmdHelperSessionDeleteCli(args []string) error {
	return sessionctl.DeleteMain(args)
}

func cmdHelperSessionDispatchCli(args []string) error {
	return sessionctl.DispatchMain(args)
}

func cmdHelperSessionMonitorCli(args []string) error {
	return sessionctl.MonitorMain(args)
}

func cmdHelperSessionSendCli(args []string) error {
	return sessionctl.SendMain(args)
}

func cmdHelperSessionStopCli(args []string) error {
	return sessionctl.StopMain(args)
}

func cmdHelperSessionSuffix(_ []string) error {
	value, err := launcher.RandomHex(4)
	if err != nil {
		return err
	}
	fmt.Println(value)
	return nil
}

func cmdHelperColimaStatus(args []string) error {
	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		return err
	}
	status, statusErr := launcher.ColimaProfileStatus(input, args[0])
	if statusErr != nil {
		if launcher.IsNoMatch(statusErr) {
			return &cliexit.ExitCodeError{Code: 3, Message: statusErr.Error()}
		}
		return statusErr
	}
	fmt.Println(status)
	return nil
}

func cmdHelperValidateColimaStatus(args []string) error {
	statusBytes, err := io.ReadAll(os.Stdin)
	if err != nil {
		return err
	}
	return launcher.ValidateColimaStatusOutput(string(statusBytes), args[0])
}

func cmdHelperRunHostColimaWithTimeout(args []string) error {
	timeoutSeconds, inv, colimaArgs, err := parseColimaInvocationArgs(args)
	if err != nil {
		return err
	}
	inv.Args = colimaArgs
	code, runErr := launcher.RunHostColimaWithTimeout(timeoutSeconds, inv)
	if runErr != nil {
		return runErr
	}
	if code != 0 {
		return &cliexit.ExitCodeError{Code: code}
	}
	return nil
}

// parseColimaInvocationArgs decodes the `run-host-colima-with-timeout`
// argument vector.  The expected shape is:
//
//	<seconds> [--colima-bin=PATH] [--real-home=DIR] [--colima-home=DIR] [--cwd=DIR] -- COLIMA_ARGS...
//
// All flags are optional; values fall back to the matching environment
// variables (HOST_COLIMA_BIN, REAL_HOME, COLIMA_STATE_ROOT,
// WORKCELL_HOST_COMMAND_CWD) so callers can pick whichever style is
// most convenient.
func parseColimaInvocationArgs(args []string) (int, launcher.HostColimaInvocation, []string, error) {
	if len(args) == 0 {
		return 0, launcher.HostColimaInvocation{}, nil, helperUsage()
	}
	timeoutSeconds, err := strconv.Atoi(args[0])
	if err != nil {
		return 0, launcher.HostColimaInvocation{}, nil, fmt.Errorf("parse timeout seconds: %w", err)
	}
	rest := args[1:]
	inv := launcher.HostColimaInvocation{
		ColimaBin:  os.Getenv("HOST_COLIMA_BIN"),
		RealHome:   os.Getenv("REAL_HOME"),
		ColimaHome: os.Getenv("COLIMA_STATE_ROOT"),
		CWD:        os.Getenv("WORKCELL_HOST_COMMAND_CWD"),
	}
	for len(rest) > 0 {
		arg := rest[0]
		if arg == "--" {
			rest = rest[1:]
			break
		}
		switch {
		case strings.HasPrefix(arg, "--colima-bin="):
			inv.ColimaBin = strings.TrimPrefix(arg, "--colima-bin=")
		case strings.HasPrefix(arg, "--real-home="):
			inv.RealHome = strings.TrimPrefix(arg, "--real-home=")
		case strings.HasPrefix(arg, "--colima-home="):
			inv.ColimaHome = strings.TrimPrefix(arg, "--colima-home=")
		case strings.HasPrefix(arg, "--cwd="):
			inv.CWD = strings.TrimPrefix(arg, "--cwd=")
		default:
			return 0, launcher.HostColimaInvocation{}, nil, helperUsage()
		}
		rest = rest[1:]
	}
	return timeoutSeconds, inv, rest, nil
}

func cmdHelperDockerDesktopContextName(_ []string) error {
	fmt.Println(launcher.DockerDesktopContextName)
	return nil
}

// cmdHelperRouteProfileDockerCommand implements the
// `route-profile-docker-command` helper subcommand.  The bash caller
// passes the provider via --provider and, depending on the provider,
// either --socket-path (colima) or --context-name (docker-desktop).
// On success the env-prefix tokens are emitted to stdout one per line
// so the bash shim can capture them into an array with `mapfile`.  An
// unsupported provider exits with status 1 after printing the bash
// "Unsupported target provider for Docker command routing" diagnostic
// to stderr.
func cmdHelperRouteProfileDockerCommand(args []string) error {
	var provider, socketPath, contextName string
	for _, arg := range args {
		switch {
		case strings.HasPrefix(arg, "--provider="):
			provider = strings.TrimPrefix(arg, "--provider=")
		case strings.HasPrefix(arg, "--socket-path="):
			socketPath = strings.TrimPrefix(arg, "--socket-path=")
		case strings.HasPrefix(arg, "--context-name="):
			contextName = strings.TrimPrefix(arg, "--context-name=")
		default:
			return helperUsage()
		}
	}
	route, err := launcher.RouteProfileDockerCommand(provider, socketPath, contextName)
	if err != nil {
		// Return an ExitCodeError so main()'s single typed handler
		// emits the diagnostic and the right exit code; no direct
		// os.Exit calls outside main().
		return &cliexit.ExitCodeError{Code: 1, Message: err.Error()}
	}
	for _, token := range route.EnvPrefix {
		fmt.Println(token)
	}
	return nil
}

// cmdHelperPrepareCurrentDockerClientPlan implements the
// `prepare-current-docker-client-plan` helper subcommand.  Required
// arg: --backend=BACKEND.  Optional: --context-name=NAME,
// --context-exists=0|1, --context-healthy=0|1 (consulted only for
// docker-desktop).  On success it prints `mode=...` (and, for
// docker-desktop, `context=NAME`) on stdout.  When the docker-desktop
// context check fails the helper exits with status 2 after writing the
// two-line bash diagnostic to stderr, matching the original `exit 2`
// in scripts/workcell.
func cmdHelperPrepareCurrentDockerClientPlan(args []string) error {
	var backend, contextName string
	contextExists := false
	contextHealthy := false
	for _, arg := range args {
		switch {
		case strings.HasPrefix(arg, "--backend="):
			backend = strings.TrimPrefix(arg, "--backend=")
		case strings.HasPrefix(arg, "--context-name="):
			contextName = strings.TrimPrefix(arg, "--context-name=")
		case arg == "--context-exists=1":
			contextExists = true
		case arg == "--context-exists=0":
			contextExists = false
		case arg == "--context-healthy=1":
			contextHealthy = true
		case arg == "--context-healthy=0":
			contextHealthy = false
		default:
			return helperUsage()
		}
	}
	plan, err := launcher.PrepareCurrentDockerClientPlan(backend, contextName, contextExists, contextHealthy)
	if err != nil {
		var planErr *launcher.PrepareDockerClientPlanError
		if errors.As(err, &planErr) {
			// Bash original exits 2 on docker-desktop context
			// failures; route through ExitCodeError so main() can
			// own the exit while the rest of the helper stays in
			// Go-error idiom.
			return &cliexit.ExitCodeError{Code: 2, Message: planErr.Error()}
		}
		return err
	}
	fmt.Printf("mode=%s\n", plan.Mode)
	if plan.ContextName != "" {
		fmt.Printf("context=%s\n", plan.ContextName)
	}
	return nil
}

func cmdHelperCleanupStaleLogPointers(args []string) error {
	return hoststate.CleanupStaleLatestLogPointers(args[0])
}

func cmdHelperProfileLockIsStale(args []string) error {
	stale, err := launcher.ProfileLockIsStale(args[0])
	if err != nil {
		return err
	}
	if stale {
		fmt.Println("1")
	} else {
		fmt.Println("0")
	}
	return nil
}

func cmdHelperAcquireProfileLock(args []string) error {
	pid, err := strconv.Atoi(args[1])
	if err != nil {
		return fmt.Errorf("parse pid: %w", err)
	}
	acquired, err := launcher.AcquireProfileLock(args[0], pid)
	if err != nil {
		return err
	}
	if acquired {
		fmt.Println("1")
	} else {
		fmt.Println("0")
	}
	return nil
}

func cmdHelperWriteProfileOwner(args []string) error {
	pid, err := strconv.Atoi(args[1])
	if err != nil {
		return fmt.Errorf("parse pid: %w", err)
	}
	return launcher.WriteProfileOwner(args[0], pid)
}

func cmdHelperCleanupStaleSessionAuditDirs(args []string) error {
	return hoststate.CleanupStaleSessionAuditDirs(args[0])
}

func cmdHelperSessionRecordWrite(args []string) error {
	updates := map[string]string{}
	for _, pair := range args[1:] {
		key, value, ok := strings.Cut(pair, "=")
		if !ok || key == "" {
			return fmt.Errorf("invalid session record update %q", pair)
		}
		updates[key] = value
	}
	return sessions.WriteSessionRecord(args[0], updates)
}

func cmdHelperAuditDigest(args []string) error {
	fmt.Println(hoststate.AuditRecordDigest(args[0], args[1], args[2:]))
	return nil
}

func cmdHelperDirectMountCacheKey(args []string) error {
	fmt.Println(hoststate.DirectMountCacheKey(args[0], args[1]))
	return nil
}

func cmdHelperResolveHostOutputCandidate(args []string) error {
	value, err := hoststate.ResolveHostOutputCandidate(args[0])
	if err != nil {
		return err
	}
	fmt.Println(value)
	return nil
}

func cmdHelperResolveHostOutputDirectoryCandidate(args []string) error {
	value, err := hoststate.ResolveHostOutputDirectoryCandidate(args[0])
	if err != nil {
		return err
	}
	fmt.Println(value)
	return nil
}

func cmdHelperCleanupStaleInjectionBundles(args []string) error {
	return hoststate.CleanupStaleInjectionBundles(args[0])
}

func cmdHelperManifestMetadata(args []string) error {
	lines, err := hoststate.ManifestMetadataLines(args[0])
	if err != nil {
		return err
	}
	for _, line := range lines {
		fmt.Println(line)
	}
	return nil
}

func cmdHelperResolverMetadata(args []string) error {
	lines, err := hoststate.ResolverMetadataLines(args[0])
	if err != nil {
		return err
	}
	for _, line := range lines {
		fmt.Println(line)
	}
	return nil
}

func cmdHelperWorkspaceCacheKey(args []string) error {
	value, err := hoststate.WorkspaceCacheKey(args[0])
	if err != nil {
		return err
	}
	fmt.Println(value)
	return nil
}

func cmdHelperExtractCodexVersion(args []string) error {
	value, err := launcher.ExtractCodexVersion(args[0])
	if err != nil {
		return err
	}
	fmt.Println(value)
	return nil
}

func cmdHelperValidateSecurityOptions(args []string) error {
	return launcher.ValidateSecurityOptions(args[0])
}

func cmdHelperValidateCompatSecurityOptions(args []string) error {
	return launcher.ValidateCompatSecurityOptions(args[0])
}

func cmdHelperValidateContainerSecurityOptions(args []string) error {
	return launcher.ValidateContainerSecurityOptions(args[0])
}

func cmdHelperCanonicalizeToolPath(args []string) error {
	value, err := launcher.CanonicalizeToolPath(args[0])
	if err != nil {
		return err
	}
	fmt.Println(value)
	return nil
}

func cmdHelperDedupeEndpoints(args []string) error {
	fmt.Println(launcher.DedupeEndpointList(args[0]))
	return nil
}

func cmdHelperResolveEndpoints(args []string) error {
	lines, err := launcher.ResolveEndpoints(args[0])
	if err != nil {
		return err
	}
	for _, line := range lines {
		fmt.Println(line)
	}
	return nil
}

func cmdHelperSupportMatrixEval(args []string) error {
	result, err := supportmatrix.Evaluate(args[0], supportmatrix.Query{
		HostOS:               args[1],
		HostArch:             args[2],
		TargetKind:           args[3],
		TargetProvider:       args[4],
		TargetAssuranceClass: args[5],
	})
	if err != nil {
		return err
	}
	for _, line := range supportmatrix.MetadataLines(result) {
		fmt.Println(line)
	}
	return nil
}

// cmdHelperProfilePath dispatches the profile-path umbrella
// subcommand.  Each kind exposes a thin, byte-identical replacement for
// the matching scripts/workcell profile_* helper.
//
// Usage forms:
//
//	profile-path dir          STATE_ROOT PROFILE
//	profile-path lima-dir     STATE_ROOT PROFILE
//	profile-path disk-dir     STATE_ROOT PROFILE
//	profile-path target-state-dir          TARGET_STATE_ROOT TARGET_KIND TARGET_PROVIDER PROFILE
//	profile-path audit-log                 TARGET_STATE_ROOT TARGET_KIND TARGET_PROVIDER PROFILE
//	profile-path legacy-audit-log          STATE_ROOT PROFILE
//	profile-path sessions-dir              TARGET_STATE_ROOT TARGET_KIND TARGET_PROVIDER PROFILE
//	profile-path legacy-sessions-dir       STATE_ROOT PROFILE
//	profile-path lock-dir                  WORKCELL_STATE_ROOT TARGET_KIND TARGET_PROVIDER PROFILE
//	profile-path latest-log-pointer        TARGET_STATE_ROOT TARGET_KIND TARGET_PROVIDER PROFILE KIND
//	profile-path legacy-latest-log-pointer STATE_ROOT PROFILE KIND
//	profile-path colima-config             STATE_ROOT PROFILE
func cmdHelperProfilePath(args []string) error {
	kind := args[0]
	rest := args[1:]
	value, err := dispatchProfilePath(kind, rest)
	if err != nil {
		if errors.Is(err, hoststate.ErrUnsupportedLogPointerKind) {
			return &cliexit.ExitCodeError{Code: 2, Message: err.Error()}
		}
		var keyErr *hoststate.InvalidStateKeyError
		if errors.As(err, &keyErr) {
			msg := keyErr.Error()
			if keyErr.Hint != "" {
				msg = fmt.Sprintf("%s\n%s", msg, keyErr.Hint)
			}
			return &cliexit.ExitCodeError{Code: 2, Message: msg}
		}
		return err
	}
	fmt.Println(value)
	return nil
}

func dispatchProfilePath(kind string, args []string) (string, error) {
	expect := func(n int) error {
		if len(args) != n {
			return fmt.Errorf("profile-path %s expects %d args, got %d", kind, n, len(args))
		}
		return nil
	}
	switch kind {
	case "dir":
		if err := expect(2); err != nil {
			return "", err
		}
		return hoststate.ProfileDir(args[0], args[1])
	case "lima-dir":
		if err := expect(2); err != nil {
			return "", err
		}
		return hoststate.ProfileLimaDir(args[0], args[1])
	case "disk-dir":
		if err := expect(2); err != nil {
			return "", err
		}
		return hoststate.ProfileDiskDir(args[0], args[1])
	case "target-state-dir":
		if err := expect(4); err != nil {
			return "", err
		}
		return hoststate.ProfileTargetStateDir(args[0], args[1], args[2], args[3])
	case "audit-log":
		if err := expect(4); err != nil {
			return "", err
		}
		return hoststate.ProfileAuditLogPath(args[0], args[1], args[2], args[3])
	case "legacy-audit-log":
		if err := expect(2); err != nil {
			return "", err
		}
		return hoststate.LegacyProfileAuditLogPath(args[0], args[1])
	case "sessions-dir":
		if err := expect(4); err != nil {
			return "", err
		}
		return hoststate.ProfileSessionsDirPath(args[0], args[1], args[2], args[3])
	case "legacy-sessions-dir":
		if err := expect(2); err != nil {
			return "", err
		}
		return hoststate.LegacyProfileSessionsDirPath(args[0], args[1])
	case "lock-dir":
		if err := expect(4); err != nil {
			return "", err
		}
		return hoststate.ProfileLockDirPath(args[0], args[1], args[2], args[3])
	case "latest-log-pointer":
		if err := expect(5); err != nil {
			return "", err
		}
		return hoststate.ProfileLatestLogPointerPath(args[0], args[1], args[2], args[3], args[4])
	case "legacy-latest-log-pointer":
		if err := expect(3); err != nil {
			return "", err
		}
		return hoststate.LegacyProfileLatestLogPointerPath(args[0], args[1], args[2])
	case "colima-config":
		if err := expect(2); err != nil {
			return "", err
		}
		return hoststate.ProfileColimaConfigPath(args[0], args[1])
	default:
		return "", fmt.Errorf("unknown profile-path kind: %s", kind)
	}
}

// cmdHelperInjectionStageDirectMounts is the helper subcommand entry
// point for the PR 23.4 Go translation of prepare_injection_direct_mounts.
// It accepts BUNDLE_ROOT and MOUNT_SPEC_PATH and prints one "-v" argument
// pair per output line so bash can split it back into the DIRECT_SOURCE_MOUNTS
// array.
func cmdHelperInjectionStageDirectMounts(args []string) error {
	dockerArgs, err := injection.StageDirectMounts(args[0], args[1])
	if err != nil {
		return err
	}
	for _, arg := range dockerArgs {
		fmt.Println(arg)
	}
	return nil
}

// cmdHelperInjectionPrepareBundle is the helper subcommand entry point
// for the PR 23.4 Go translation of prepare_injection_bundle.  It reads the
// bash globals from CLI flags, executes the full pipeline in-process, and
// prints KEY=VALUE lines (one per bash global) so the shim can re-export
// them via a read loop.  FormatBundleResultForShell is fail-closed: if
// any value carries a forbidden control char we propagate the error and
// emit nothing so bash never re-imports a partial plan.
func cmdHelperInjectionPrepareBundle(args []string) error {
	opts, err := parsePrepareBundleArgs(args)
	if err != nil {
		return err
	}
	result, err := injection.PrepareBundle(*opts)
	if err != nil {
		return err
	}
	formatted, err := injection.FormatBundleResultForShell(result)
	if err != nil {
		return err
	}
	fmt.Print(formatted)
	return nil
}

// cmdHelperLookupStateRoots is the helper subcommand entry point
// that scripts/workcell::session_lookup_root_args shells out to so the
// bash↔Go state-root contract has a single Go owner.  The two roots
// arrive as positional args in (workcell, colima) order rather than as
// env vars because go_hostutil routes through run_clean_host_command,
// which strips the process env via env -i.  Each non-empty value is
// emitted on its own line as a ready-to-consume `--root=PATH` token.
func cmdHelperLookupStateRoots(args []string) error {
	lines, err := stateroot.FormatRootArgs(args[0], args[1])
	if err != nil {
		return &cliexit.ExitCodeError{Code: 2, Message: err.Error()}
	}
	for _, line := range lines {
		fmt.Println(line)
	}
	return nil
}

func parsePrepareBundleArgs(args []string) (*injection.PrepareBundleOptions, error) {
	opts := &injection.PrepareBundleOptions{ProcessPID: os.Getpid()}
	for _, arg := range args {
		key, value, ok := strings.Cut(arg, "=")
		if !ok {
			return nil, fmt.Errorf("expected KEY=VALUE, got %q", arg)
		}
		switch key {
		case "--agent":
			opts.Agent = value
		case "--mode":
			opts.Mode = value
		case "--policy":
			opts.PolicyPath = value
		case "--use-default-policy":
			opts.UseDefaultPolicy = value == "1" || value == "true"
		case "--auth-status":
			opts.AuthStatus = value == "1" || value == "true"
		case "--doctor":
			opts.Doctor = value == "1" || value == "true"
		case "--inspect":
			opts.Inspect = value == "1" || value == "true"
		case "--dry-run":
			opts.DryRun = value == "1" || value == "true"
		case "--synthetic-codex-auth":
			opts.SelfStagingProbeSyntheticCodexAuth = value == "1" || value == "true"
		case "--synthetic-claude-export":
			opts.SelfStagingProbeSyntheticClaudeExport = value == "1" || value == "true"
		case "--real-home":
			opts.RealHome = value
		case "--bundle-parent":
			opts.BundleParentOverride = value
		case "--pid":
			pid, err := strconv.Atoi(value)
			if err != nil {
				return nil, fmt.Errorf("invalid --pid: %v", err)
			}
			opts.ProcessPID = pid
		default:
			return nil, fmt.Errorf("unknown flag: %s", key)
		}
	}
	return opts, nil
}

func usage() error {
	return fmt.Errorf("usage: workcell-hostutil <path|release|helper|launcher|policy|resolve-credentials|pty-transcript|auth-cli|auth-usage|policy-cli|policy-usage|publish-pr-cli|publish-pr-usage|session-usage|session-attach-cli|session-delete-cli|session-dispatch-cli|session-logs-cli|session-monitor-cli|session-send-cli|session-stop-cli|session-timeline-cli> [args...]")
}

func pathUsage() error {
	return fmt.Errorf("usage: workcell-hostutil path <home|resolve> [--base DIR] [PATH]")
}

func releaseUsage() error {
	return fmt.Errorf("usage: workcell-hostutil release <create-payload|metadata|encode-name|bundle-manifest> [args...]")
}

func launcherUsage() error {
	return fmt.Errorf("usage: workcell-hostutil launcher <helper|*-cli|*-usage> [args...]")
}

func helperUsage() error {
	subs := helperSubcommands()
	names := make([]string, 0, len(subs))
	for _, sub := range subs {
		names = append(names, sub.name)
	}
	return fmt.Errorf("usage: workcell-hostutil helper <%s> [args...]", strings.Join(names, "|"))
}

func parseSessionRoots(args []string) ([]string, []string, error) {
	if len(args) == 0 {
		return nil, nil, helperUsage()
	}

	if strings.HasPrefix(args[0], "--root=") {
		roots := make([]string, 0, len(args))
		for len(args) > 0 && strings.HasPrefix(args[0], "--root=") {
			root := strings.TrimPrefix(args[0], "--root=")
			if root == "" {
				return nil, nil, helperUsage()
			}
			roots = append(roots, root)
			args = args[1:]
		}
		if len(roots) == 0 {
			return nil, nil, helperUsage()
		}
		return roots, args, nil
	}

	return []string{args[0]}, args[1:], nil
}

func runHelperSessionList(args []string) error {
	roots, rest, err := parseSessionRoots(args)
	if err != nil {
		return err
	}

	format := "lines"
	verbose := false
	opts := sessions.SessionListOptions{}
	for _, arg := range rest {
		switch {
		case arg == "--json":
			format = "json"
		case arg == "--verbose":
			verbose = true
		case strings.HasPrefix(arg, "--workspace="):
			opts.Workspace = strings.TrimPrefix(arg, "--workspace=")
		case strings.HasPrefix(arg, "--profile="):
			opts.Profile = strings.TrimPrefix(arg, "--profile=")
		default:
			return helperUsage()
		}
	}
	if format == "json" && verbose {
		return fmt.Errorf("session-list accepts either --json or --verbose, not both")
	}

	records, err := sessions.ListSessionRecordsInRoots(roots, opts)
	if err != nil {
		return err
	}
	if format == "json" {
		content, err := json.MarshalIndent(records, "", "  ")
		if err != nil {
			return err
		}
		fmt.Printf("%s\n", content)
		return nil
	}
	for _, record := range records {
		if verbose {
			fmt.Printf(
				"%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				record.SessionID,
				record.Status,
				sessions.SessionDisplayLiveStatus(record),
				sessions.SessionControlMode(record),
				record.Agent,
				record.Mode,
				record.Profile,
				sessions.SessionTargetSummary(record),
				record.TargetAssuranceClass,
				record.WorkspaceTransport,
				sessions.SessionDisplayGitBranch(record),
				sessions.SessionDisplayWorktree(record),
				record.StartedAt,
				sessions.SessionAssuranceSummary(record),
				sessions.SessionDisplayWorkspace(record),
			)
			continue
		}
		fmt.Printf(
			"%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			record.SessionID,
			record.Status,
			sessions.SessionDisplayLiveStatus(record),
			sessions.SessionControlMode(record),
			record.Agent,
			record.Mode,
			record.Profile,
			record.StartedAt,
			sessions.SessionAssuranceSummary(record),
			sessions.SessionDisplayWorkspace(record),
		)
	}
	return nil
}

func runHelperSessionShow(args []string) error {
	roots, rest, err := parseSessionRoots(args)
	if err != nil {
		return err
	}
	if len(rest) < 1 || len(rest) > 2 {
		return helperUsage()
	}

	format := "json"
	for _, arg := range rest[1:] {
		if arg != "--text" {
			return helperUsage()
		}
		format = "text"
	}
	record, err := sessions.FindSessionRecordInRoots(roots, rest[0])
	if err != nil {
		return err
	}
	if format == "text" {
		for _, line := range sessions.SessionShowLines(record) {
			fmt.Println(line)
		}
		return nil
	}
	content, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("%s\n", content)
	return nil
}

func runHelperSessionExport(args []string) error {
	roots, rest, err := parseSessionRoots(args)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return helperUsage()
	}

	exported, err := sessions.ExportSessionRecordInRoots(roots, rest[0])
	if err != nil {
		return err
	}
	content, err := json.MarshalIndent(exported, "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("%s\n", content)
	return nil
}

func runHelperSessionDiffMetadata(args []string) error {
	roots, rest, err := parseSessionRoots(args)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return helperUsage()
	}

	record, err := sessions.FindSessionRecordInRoots(roots, rest[0])
	if err != nil {
		return err
	}
	for _, line := range sessions.SessionDiffMetadataLines(record) {
		fmt.Println(line)
	}
	return nil
}

func runHelperSessionRuntimeMetadata(args []string) error {
	roots, rest, err := parseSessionRoots(args)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return helperUsage()
	}

	record, recordPath, err := sessions.FindSessionRecordWithPathInRoots(roots, rest[0])
	if err != nil {
		return err
	}
	for _, line := range sessions.SessionRuntimeMetadataLines(record) {
		fmt.Println(line)
	}
	fmt.Printf("record_path=%s\n", recordPath)
	return nil
}

func runHelperSessionTimeline(args []string) error {
	roots, rest, err := parseSessionRoots(args)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return helperUsage()
	}

	lines, err := sessions.SessionTimelineRecordsInRoots(roots, rest[0])
	if err != nil {
		return err
	}
	for _, line := range lines {
		fmt.Println(line)
	}
	return nil
}
