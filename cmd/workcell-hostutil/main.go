// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/omkhar/workcell/internal/host/hoststate"
	"github.com/omkhar/workcell/internal/host/launcher"
	"github.com/omkhar/workcell/internal/host/release"
	"github.com/omkhar/workcell/internal/host/sessions"
	"github.com/omkhar/workcell/internal/host/supportmatrix"
	"github.com/omkhar/workcell/internal/publishpr"
	"github.com/omkhar/workcell/internal/sessionctl"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
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
	default:
		return usage()
	}
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
		{"session-timeline-cli", 0, -1, cmdLauncherSessionTimelineCli},
		{"session-logs-cli", 0, -1, cmdLauncherSessionLogsCli},
		{"session-suffix", 0, 0, cmdLauncherSessionSuffix},
		{"colima-status", 1, 1, cmdLauncherColimaStatus},
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

func cmdLauncherPublishPRUsage(_ []string) error {
	fmt.Print(publishpr.UsageText())
	return nil
}

func cmdLauncherSessionTimelineCli(args []string) error {
	return sessionctl.TimelineMain(args)
}

func cmdLauncherSessionLogsCli(args []string) error {
	return sessionctl.LogsMain(args)
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

func usage() error {
	return fmt.Errorf("usage: workcell-hostutil <path|release> [args...]")
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
