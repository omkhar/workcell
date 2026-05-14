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
	"github.com/omkhar/workcell/internal/host/hoststate"
	"github.com/omkhar/workcell/internal/host/launcher"
	"github.com/omkhar/workcell/internal/host/release"
	"github.com/omkhar/workcell/internal/host/sessions"
	"github.com/omkhar/workcell/internal/host/supportmatrix"
	"github.com/omkhar/workcell/internal/injection"
	"github.com/omkhar/workcell/internal/publishpr"
	"github.com/omkhar/workcell/internal/sessionctl"
	"github.com/omkhar/workcell/internal/transcript"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		// Both authpolicy and publishpr translations carry their bash
		// exit-code contract through a typed ExitCodeError. The two
		// types are intentionally distinct because their messages also
		// differ (auth/policy v. publish-pr), but the dispatch here
		// stays uniform: surface the embedded message (if any) on
		// stderr and exit with the typed Code.
		var authEC *authpolicy.ExitCodeError
		if errors.As(err, &authEC) {
			if msg := authEC.Error(); msg != "" {
				fmt.Fprintln(os.Stderr, msg)
			}
			os.Exit(authEC.Code)
		}
		var publishEC *publishpr.ExitCodeError
		if errors.As(err, &publishEC) {
			if msg := publishEC.Error(); msg != "" {
				fmt.Fprintln(os.Stderr, msg)
			}
			os.Exit(publishEC.Code)
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
	case "path":
		return runPath(args[1:])
	case "release":
		return runRelease(args[1:])
	case "launcher":
		return runLauncher(args[1:])
	case "policy":
		return runHostutilPolicy(args[1:])
	case "resolve-credentials":
		return runHostutilResolveCredentials(args[1:])
	case "pty-transcript":
		return runHostutilPTYTranscript(args[1:])
	default:
		return usage()
	}
}

// runHostutilPolicy dispatches the absorbed workcell-manage-injection-policy
// CLI surface. The previous standalone binary deferred to
// authpolicy.Run; we keep the same contract here so callers
// (scripts/lib/manage_injection_policy) see identical stdout/stderr and
// exit codes.
func runHostutilPolicy(args []string) error {
	code := authpolicy.Run("workcell-hostutil policy", args, os.Stdout, os.Stderr)
	if code == 0 {
		return nil
	}
	return &authpolicy.ExitCodeError{Code: code, Message: ""}
}

// runHostutilResolveCredentials dispatches the absorbed
// workcell-resolve-credential-sources CLI surface.  authresolve.Run
// already writes stdout/stderr and returns the bash-shaped exit code;
// we wrap it in an ExitCodeError so main()'s typed handler can
// preserve the contract.
func runHostutilResolveCredentials(args []string) error {
	code := authresolve.Run(args, os.Stdout, os.Stderr)
	if code == 0 {
		return nil
	}
	return &authpolicy.ExitCodeError{Code: code, Message: ""}
}

// runHostutilPTYTranscript dispatches the absorbed
// workcell-pty-transcript CLI surface.  transcript.Run wants the
// raw *os.File for stdin/stdout (it tees PTY output and stamps
// timestamps), so we forward os.Stdin/os.Stdout directly.
func runHostutilPTYTranscript(args []string) error {
	code := transcript.Run("workcell-hostutil pty-transcript", os.Stdin, os.Stdout, os.Stderr, args)
	if code == 0 {
		return nil
	}
	return &authpolicy.ExitCodeError{Code: code, Message: ""}
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

// launcherSubcommand describes one workcell-hostutil launcher subcommand.
// minArgs and maxArgs count only the args that follow the subcommand
// name; a maxArgs of -1 means unbounded.
type launcherSubcommand struct {
	name    string
	minArgs int
	maxArgs int
	handler func(args []string) error
}

// launcherSubcommands returns the dispatch table.  It is a function (not
// a package-level var) because several of the session handlers below
// call back into launcherUsage, which itself reads this table; using a
// function defers the table's construction past package-init time and
// avoids a Go initialization cycle.
func launcherSubcommands() []launcherSubcommand {
	return []launcherSubcommand{
		{"session-usage", 0, 0, cmdLauncherSessionUsage},
		{"auth-cli", 0, -1, cmdLauncherAuthCli},
		{"auth-usage", 0, 0, cmdLauncherAuthUsage},
		{"policy-usage", 0, 0, cmdLauncherPolicyUsage},
		{"policy-cli", 0, -1, cmdLauncherPolicyCli},
		{"session-timeline-cli", 0, -1, cmdLauncherSessionTimelineCli},
		{"session-logs-cli", 0, -1, cmdLauncherSessionLogsCli},
		{"session-attach-cli", 0, -1, cmdLauncherSessionAttachCli},
		{"session-delete-cli", 0, -1, cmdLauncherSessionDeleteCli},
		{"session-dispatcher-cli", 0, -1, cmdLauncherSessionDispatcherCli},
		{"session-monitor-cli", 0, -1, cmdLauncherSessionMonitorCli},
		{"session-send-cli", 0, -1, cmdLauncherSessionSendCli},
		{"session-stop-cli", 0, -1, cmdLauncherSessionStopCli},
		{"session-suffix", 0, 0, cmdLauncherSessionSuffix},
		{"colima-status", 1, 1, cmdLauncherColimaStatus},
		{"validate-colima-status", 1, 1, cmdLauncherValidateColimaStatus},
		{"run-host-colima-with-timeout", 1, -1, cmdLauncherRunHostColimaWithTimeout},
		{"docker-desktop-context-name", 0, 0, cmdLauncherDockerDesktopContextName},
		{"route-profile-docker-command", 1, -1, cmdLauncherRouteProfileDockerCommand},
		{"prepare-current-docker-client-plan", 1, -1, cmdLauncherPrepareCurrentDockerClientPlan},
		{"cleanup-stale-log-pointers", 1, 1, cmdLauncherCleanupStaleLogPointers},
		{"profile-lock-is-stale", 1, 1, cmdLauncherProfileLockIsStale},
		{"acquire-profile-lock", 2, 2, cmdLauncherAcquireProfileLock},
		{"write-profile-owner", 2, 2, cmdLauncherWriteProfileOwner},
		{"cleanup-stale-session-audit-dirs", 1, 1, cmdLauncherCleanupStaleSessionAuditDirs},
		{"session-record-write", 2, -1, cmdLauncherSessionRecordWrite},
		{"session-list", 0, -1, runLauncherSessionList},
		{"session-show", 0, -1, runLauncherSessionShow},
		{"session-export", 0, -1, runLauncherSessionExport},
		{"session-diff-metadata", 0, -1, runLauncherSessionDiffMetadata},
		{"session-runtime-metadata", 0, -1, runLauncherSessionRuntimeMetadata},
		{"session-timeline", 0, -1, runLauncherSessionTimeline},
		{"audit-digest", 2, -1, cmdLauncherAuditDigest},
		{"direct-mount-cache-key", 2, 2, cmdLauncherDirectMountCacheKey},
		{"resolve-host-output-candidate", 1, 1, cmdLauncherResolveHostOutputCandidate},
		{"resolve-host-output-directory-candidate", 1, 1, cmdLauncherResolveHostOutputDirectoryCandidate},
		{"cleanup-stale-injection-bundles", 1, 1, cmdLauncherCleanupStaleInjectionBundles},
		{"manifest-metadata", 1, 1, cmdLauncherManifestMetadata},
		{"resolver-metadata", 1, 1, cmdLauncherResolverMetadata},
		{"workspace-cache-key", 1, 1, cmdLauncherWorkspaceCacheKey},
		{"extract-codex-version", 1, 1, cmdLauncherExtractCodexVersion},
		{"validate-security-options", 1, 1, cmdLauncherValidateSecurityOptions},
		{"validate-container-security-options", 1, 1, cmdLauncherValidateContainerSecurityOptions},
		{"canonicalize-tool-path", 1, 1, cmdLauncherCanonicalizeToolPath},
		{"dedupe-endpoints", 1, 1, cmdLauncherDedupeEndpoints},
		{"resolve-endpoints", 1, 1, cmdLauncherResolveEndpoints},
		{"support-matrix-eval", 6, 6, cmdLauncherSupportMatrixEval},
		{"publish-pr-usage", 0, 0, cmdLauncherPublishPRUsage},
		{"profile-path", 1, -1, cmdLauncherProfilePath},
		// PR 23.4 — injection bundle preparation moved into Go.
		{"injection-stage-direct-mounts", 2, 2, cmdLauncherInjectionStageDirectMounts},
		{"injection-prepare-bundle", 0, -1, cmdLauncherInjectionPrepareBundle},
		{"publish-pr-cli", 0, -1, cmdLauncherPublishPRCli},
	}
}

func runLauncher(args []string) error {
	if len(args) == 0 {
		return launcherUsage()
	}
	rest := args[1:]
	for _, sub := range launcherSubcommands() {
		if sub.name != args[0] {
			continue
		}
		if len(rest) < sub.minArgs {
			return launcherUsage()
		}
		if sub.maxArgs >= 0 && len(rest) > sub.maxArgs {
			return launcherUsage()
		}
		return sub.handler(rest)
	}
	return launcherUsage()
}

func cmdLauncherSessionUsage(_ []string) error {
	fmt.Print(sessionctl.UsageText())
	return nil
}

func cmdLauncherAuthUsage(_ []string) error {
	fmt.Print(authpolicy.AuthUsageText())
	return nil
}

func cmdLauncherAuthCli(args []string) error {
	return authpolicy.AuthMain(args)
}

func cmdLauncherPolicyUsage(_ []string) error {
	fmt.Print(authpolicy.PolicyUsageText())
	return nil
}

func cmdLauncherPublishPRUsage(_ []string) error {
	fmt.Print(publishpr.UsageText())
	return nil
}

// cmdLauncherPolicyCli is the launcher entry point for the Go
// translation of scripts/workcell's policy_main bash function.  Usage
// errors (missing/unknown subcommand, unknown option) exit with code 2
// to match the bash CLI surface; all other errors propagate to main()
// for the default exit-1 path.
func cmdLauncherPolicyCli(args []string) error {
	err := authpolicy.PolicyMain(args)
	if authpolicy.IsPolicyMainUsageError(err) {
		os.Exit(2)
	}
	return err
}

func cmdLauncherPublishPRCli(args []string) error {
	return publishpr.PublishPRMain(args, os.Stdin, os.Stdout, os.Stderr)
}

func cmdLauncherSessionTimelineCli(args []string) error {
	return sessionctl.TimelineMain(args)
}

func cmdLauncherSessionLogsCli(args []string) error {
	return sessionctl.LogsMain(args)
}

func cmdLauncherSessionAttachCli(args []string) error {
	return sessionctl.AttachMain(args)
}

func cmdLauncherSessionDeleteCli(args []string) error {
	return sessionctl.DeleteMain(args)
}

func cmdLauncherSessionDispatcherCli(args []string) error {
	return sessionctl.DispatchMain(args)
}

func cmdLauncherSessionMonitorCli(args []string) error {
	return sessionctl.MonitorMain(args)
}

func cmdLauncherSessionSendCli(args []string) error {
	return sessionctl.SendMain(args)
}

func cmdLauncherSessionStopCli(args []string) error {
	return sessionctl.StopMain(args)
}

func cmdLauncherSessionSuffix(_ []string) error {
	value, err := launcher.RandomHex(4)
	if err != nil {
		return err
	}
	fmt.Println(value)
	return nil
}

func cmdLauncherColimaStatus(args []string) error {
	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		return err
	}
	status, statusErr := launcher.ColimaProfileStatus(input, args[0])
	if statusErr != nil {
		if launcher.IsNoMatch(statusErr) {
			fmt.Fprintln(os.Stderr, statusErr)
			os.Exit(3)
		}
		return statusErr
	}
	fmt.Println(status)
	return nil
}

func cmdLauncherValidateColimaStatus(args []string) error {
	statusBytes, err := io.ReadAll(os.Stdin)
	if err != nil {
		return err
	}
	return launcher.ValidateColimaStatusOutput(string(statusBytes), args[0])
}

func cmdLauncherRunHostColimaWithTimeout(args []string) error {
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
		os.Exit(code)
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
		return 0, launcher.HostColimaInvocation{}, nil, launcherUsage()
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
			return 0, launcher.HostColimaInvocation{}, nil, launcherUsage()
		}
		rest = rest[1:]
	}
	return timeoutSeconds, inv, rest, nil
}

func cmdLauncherDockerDesktopContextName(_ []string) error {
	fmt.Println(launcher.DockerDesktopContextName)
	return nil
}

// cmdLauncherRouteProfileDockerCommand implements the
// `route-profile-docker-command` launcher subcommand.  The bash caller
// passes the provider via --provider and, depending on the provider,
// either --socket-path (colima) or --context-name (docker-desktop).
// On success the env-prefix tokens are emitted to stdout one per line
// so the bash shim can capture them into an array with `mapfile`.  An
// unsupported provider exits with status 1 after printing the bash
// "Unsupported target provider for Docker command routing" diagnostic
// to stderr.
func cmdLauncherRouteProfileDockerCommand(args []string) error {
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
			return launcherUsage()
		}
	}
	route, err := launcher.RouteProfileDockerCommand(provider, socketPath, contextName)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
	for _, token := range route.EnvPrefix {
		fmt.Println(token)
	}
	return nil
}

// cmdLauncherPrepareCurrentDockerClientPlan implements the
// `prepare-current-docker-client-plan` launcher subcommand.  Required
// arg: --backend=BACKEND.  Optional: --context-name=NAME,
// --context-exists=0|1, --context-healthy=0|1 (consulted only for
// docker-desktop).  On success it prints `mode=...` (and, for
// docker-desktop, `context=NAME`) on stdout.  When the docker-desktop
// context check fails the helper exits with status 2 after writing the
// two-line bash diagnostic to stderr, matching the original `exit 2`
// in scripts/workcell.
func cmdLauncherPrepareCurrentDockerClientPlan(args []string) error {
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
			return launcherUsage()
		}
	}
	plan, err := launcher.PrepareCurrentDockerClientPlan(backend, contextName, contextExists, contextHealthy)
	if err != nil {
		var planErr *launcher.PrepareDockerClientPlanError
		if errors.As(err, &planErr) {
			fmt.Fprintln(os.Stderr, planErr.Error())
			os.Exit(2)
		}
		return err
	}
	fmt.Printf("mode=%s\n", plan.Mode)
	if plan.ContextName != "" {
		fmt.Printf("context=%s\n", plan.ContextName)
	}
	return nil
}

func cmdLauncherCleanupStaleLogPointers(args []string) error {
	return hoststate.CleanupStaleLatestLogPointers(args[0])
}

func cmdLauncherProfileLockIsStale(args []string) error {
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

func cmdLauncherAcquireProfileLock(args []string) error {
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

func cmdLauncherWriteProfileOwner(args []string) error {
	pid, err := strconv.Atoi(args[1])
	if err != nil {
		return fmt.Errorf("parse pid: %w", err)
	}
	return launcher.WriteProfileOwner(args[0], pid)
}

func cmdLauncherCleanupStaleSessionAuditDirs(args []string) error {
	return hoststate.CleanupStaleSessionAuditDirs(args[0])
}

func cmdLauncherSessionRecordWrite(args []string) error {
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

func cmdLauncherAuditDigest(args []string) error {
	fmt.Println(hoststate.AuditRecordDigest(args[0], args[1], args[2:]))
	return nil
}

func cmdLauncherDirectMountCacheKey(args []string) error {
	fmt.Println(hoststate.DirectMountCacheKey(args[0], args[1]))
	return nil
}

func cmdLauncherResolveHostOutputCandidate(args []string) error {
	value, err := hoststate.ResolveHostOutputCandidate(args[0])
	if err != nil {
		return err
	}
	fmt.Println(value)
	return nil
}

func cmdLauncherResolveHostOutputDirectoryCandidate(args []string) error {
	value, err := hoststate.ResolveHostOutputDirectoryCandidate(args[0])
	if err != nil {
		return err
	}
	fmt.Println(value)
	return nil
}

func cmdLauncherCleanupStaleInjectionBundles(args []string) error {
	return hoststate.CleanupStaleInjectionBundles(args[0])
}

func cmdLauncherManifestMetadata(args []string) error {
	lines, err := hoststate.ManifestMetadataLines(args[0])
	if err != nil {
		return err
	}
	for _, line := range lines {
		fmt.Println(line)
	}
	return nil
}

func cmdLauncherResolverMetadata(args []string) error {
	lines, err := hoststate.ResolverMetadataLines(args[0])
	if err != nil {
		return err
	}
	for _, line := range lines {
		fmt.Println(line)
	}
	return nil
}

func cmdLauncherWorkspaceCacheKey(args []string) error {
	value, err := hoststate.WorkspaceCacheKey(args[0])
	if err != nil {
		return err
	}
	fmt.Println(value)
	return nil
}

func cmdLauncherExtractCodexVersion(args []string) error {
	value, err := launcher.ExtractCodexVersion(args[0])
	if err != nil {
		return err
	}
	fmt.Println(value)
	return nil
}

func cmdLauncherValidateSecurityOptions(args []string) error {
	return launcher.ValidateSecurityOptions(args[0])
}

func cmdLauncherValidateContainerSecurityOptions(args []string) error {
	return launcher.ValidateContainerSecurityOptions(args[0])
}

func cmdLauncherCanonicalizeToolPath(args []string) error {
	value, err := launcher.CanonicalizeToolPath(args[0])
	if err != nil {
		return err
	}
	fmt.Println(value)
	return nil
}

func cmdLauncherDedupeEndpoints(args []string) error {
	fmt.Println(launcher.DedupeEndpointList(args[0]))
	return nil
}

func cmdLauncherResolveEndpoints(args []string) error {
	lines, err := launcher.ResolveEndpoints(args[0])
	if err != nil {
		return err
	}
	for _, line := range lines {
		fmt.Println(line)
	}
	return nil
}

func cmdLauncherSupportMatrixEval(args []string) error {
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

// cmdLauncherProfilePath dispatches the profile-path umbrella
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
func cmdLauncherProfilePath(args []string) error {
	kind := args[0]
	rest := args[1:]
	value, err := dispatchProfilePath(kind, rest)
	if err != nil {
		if errors.Is(err, hoststate.ErrUnsupportedLogPointerKind) {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		var keyErr *hoststate.InvalidStateKeyError
		if errors.As(err, &keyErr) {
			fmt.Fprintln(os.Stderr, keyErr.Error())
			if keyErr.Hint != "" {
				fmt.Fprintln(os.Stderr, keyErr.Hint)
			}
			os.Exit(2)
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

// cmdLauncherInjectionStageDirectMounts is the launcher subcommand entry
// point for the PR 23.4 Go translation of prepare_injection_direct_mounts.
// It accepts BUNDLE_ROOT and MOUNT_SPEC_PATH and prints one "-v" argument
// pair per output line so bash can split it back into the DIRECT_SOURCE_MOUNTS
// array.
func cmdLauncherInjectionStageDirectMounts(args []string) error {
	dockerArgs, err := injection.StageDirectMounts(args[0], args[1])
	if err != nil {
		return err
	}
	for _, arg := range dockerArgs {
		fmt.Println(arg)
	}
	return nil
}

// cmdLauncherInjectionPrepareBundle is the launcher subcommand entry point
// for the PR 23.4 Go translation of prepare_injection_bundle.  It reads the
// bash globals from CLI flags, executes the full pipeline in-process, and
// prints KEY=VALUE lines (one per bash global) so the shim can re-export
// them via a read loop.
func cmdLauncherInjectionPrepareBundle(args []string) error {
	opts, err := parsePrepareBundleArgs(args)
	if err != nil {
		return err
	}
	result, err := injection.PrepareBundle(*opts)
	if err != nil {
		return err
	}
	fmt.Print(injection.FormatBundleResultForShell(result))
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
	return fmt.Errorf("usage: workcell-hostutil <path|release|launcher|policy|resolve-credentials|pty-transcript> [args...]")
}

func pathUsage() error {
	return fmt.Errorf("usage: workcell-hostutil path <home|resolve> [--base DIR] [PATH]")
}

func releaseUsage() error {
	return fmt.Errorf("usage: workcell-hostutil release <create-payload|metadata|encode-name|bundle-manifest> [args...]")
}

func launcherUsage() error {
	subs := launcherSubcommands()
	names := make([]string, 0, len(subs))
	for _, sub := range subs {
		names = append(names, sub.name)
	}
	return fmt.Errorf("usage: workcell-hostutil launcher <%s> [args...]", strings.Join(names, "|"))
}

func parseSessionRoots(args []string) ([]string, []string, error) {
	if len(args) == 0 {
		return nil, nil, launcherUsage()
	}

	if strings.HasPrefix(args[0], "--root=") {
		roots := make([]string, 0, len(args))
		for len(args) > 0 && strings.HasPrefix(args[0], "--root=") {
			root := strings.TrimPrefix(args[0], "--root=")
			if root == "" {
				return nil, nil, launcherUsage()
			}
			roots = append(roots, root)
			args = args[1:]
		}
		if len(roots) == 0 {
			return nil, nil, launcherUsage()
		}
		return roots, args, nil
	}

	return []string{args[0]}, args[1:], nil
}

func runLauncherSessionList(args []string) error {
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
			return launcherUsage()
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

func runLauncherSessionShow(args []string) error {
	roots, rest, err := parseSessionRoots(args)
	if err != nil {
		return err
	}
	if len(rest) < 1 || len(rest) > 2 {
		return launcherUsage()
	}

	format := "json"
	for _, arg := range rest[1:] {
		if arg != "--text" {
			return launcherUsage()
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

func runLauncherSessionExport(args []string) error {
	roots, rest, err := parseSessionRoots(args)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return launcherUsage()
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

func runLauncherSessionDiffMetadata(args []string) error {
	roots, rest, err := parseSessionRoots(args)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return launcherUsage()
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

func runLauncherSessionRuntimeMetadata(args []string) error {
	roots, rest, err := parseSessionRoots(args)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return launcherUsage()
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

func runLauncherSessionTimeline(args []string) error {
	roots, rest, err := parseSessionRoots(args)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return launcherUsage()
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
