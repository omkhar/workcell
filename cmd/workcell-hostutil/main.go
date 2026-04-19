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

	"github.com/omkhar/workcell/internal/hostutil"
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
		home, err := hostutil.RealHome()
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
		resolved, err := hostutil.CanonicalizePathFrom(pathArgs[0], base)
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
		return hostutil.WriteGitHubReleaseCreatePayload(args[1], args[2])
	case "metadata":
		if len(args) < 4 {
			return releaseUsage()
		}
		return hostutil.WriteGitHubReleaseMetadata(args[1], args[3:], args[2])
	case "encode-name":
		if len(args) != 2 {
			return releaseUsage()
		}
		fmt.Println(hostutil.EncodeReleaseAssetName(args[1]))
		return nil
	case "bundle-manifest":
		if len(args) != 8 {
			return releaseUsage()
		}
		sourceDateEpoch, err := strconv.ParseInt(args[5], 10, 64)
		if err != nil {
			return fmt.Errorf("parse source_date_epoch: %w", err)
		}
		return hostutil.WriteReleaseBundleManifest(args[1], args[2], args[3], args[4], sourceDateEpoch, args[6], args[7])
	default:
		return releaseUsage()
	}
}

func runLauncher(args []string) error {
	if len(args) == 0 {
		return launcherUsage()
	}

	switch args[0] {
	case "session-suffix":
		if len(args) != 1 {
			return launcherUsage()
		}
		value, err := hostutil.RandomHex(4)
		if err != nil {
			return err
		}
		fmt.Println(value)
		return nil
	case "colima-status":
		if len(args) != 2 {
			return launcherUsage()
		}
		input, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}
		status, statusErr := hostutil.ColimaProfileStatus(input, args[1])
		if statusErr != nil {
			if hostutil.IsNoMatch(statusErr) {
				fmt.Fprintln(os.Stderr, statusErr)
				os.Exit(3)
			}
			return statusErr
		}
		fmt.Println(status)
		return nil
	case "cleanup-stale-log-pointers":
		if len(args) != 2 {
			return launcherUsage()
		}
		return hostutil.CleanupStaleLatestLogPointers(args[1])
	case "profile-lock-is-stale":
		if len(args) != 2 {
			return launcherUsage()
		}
		stale, err := hostutil.ProfileLockIsStale(args[1])
		if err != nil {
			return err
		}
		if stale {
			fmt.Println("1")
		} else {
			fmt.Println("0")
		}
		return nil
	case "acquire-profile-lock":
		if len(args) != 3 {
			return launcherUsage()
		}
		pid, err := strconv.Atoi(args[2])
		if err != nil {
			return fmt.Errorf("parse pid: %w", err)
		}
		acquired, err := hostutil.AcquireProfileLock(args[1], pid)
		if err != nil {
			return err
		}
		if acquired {
			fmt.Println("1")
		} else {
			fmt.Println("0")
		}
		return nil
	case "write-profile-owner":
		if len(args) != 3 {
			return launcherUsage()
		}
		pid, err := strconv.Atoi(args[2])
		if err != nil {
			return fmt.Errorf("parse pid: %w", err)
		}
		return hostutil.WriteProfileOwner(args[1], pid)
	case "cleanup-stale-session-audit-dirs":
		if len(args) != 2 {
			return launcherUsage()
		}
		return hostutil.CleanupStaleSessionAuditDirs(args[1])
	case "session-record-write":
		if len(args) < 3 {
			return launcherUsage()
		}
		updates := map[string]string{}
		for _, pair := range args[2:] {
			key, value, ok := strings.Cut(pair, "=")
			if !ok || key == "" {
				return fmt.Errorf("invalid session record update %q", pair)
			}
			updates[key] = value
		}
		return hostutil.WriteSessionRecord(args[1], updates)
	case "session-list":
		return runLauncherSessionList(args[1:])
	case "session-show":
		return runLauncherSessionShow(args[1:])
	case "session-export":
		return runLauncherSessionExport(args[1:])
	case "session-diff-metadata":
		return runLauncherSessionDiffMetadata(args[1:])
	case "session-runtime-metadata":
		return runLauncherSessionRuntimeMetadata(args[1:])
	case "session-timeline":
		return runLauncherSessionTimeline(args[1:])
	case "audit-digest":
		if len(args) < 3 {
			return launcherUsage()
		}
		fmt.Println(hostutil.AuditRecordDigest(args[1], args[2], args[3:]))
		return nil
	case "direct-mount-cache-key":
		if len(args) != 3 {
			return launcherUsage()
		}
		fmt.Println(hostutil.DirectMountCacheKey(args[1], args[2]))
		return nil
	case "resolve-host-output-candidate":
		if len(args) != 2 {
			return launcherUsage()
		}
		value, err := hostutil.ResolveHostOutputCandidate(args[1])
		if err != nil {
			return err
		}
		fmt.Println(value)
		return nil
	case "resolve-host-output-directory-candidate":
		if len(args) != 2 {
			return launcherUsage()
		}
		value, err := hostutil.ResolveHostOutputDirectoryCandidate(args[1])
		if err != nil {
			return err
		}
		fmt.Println(value)
		return nil
	case "cleanup-stale-injection-bundles":
		if len(args) != 2 {
			return launcherUsage()
		}
		return hostutil.CleanupStaleInjectionBundles(args[1])
	case "manifest-metadata":
		if len(args) != 2 {
			return launcherUsage()
		}
		lines, err := hostutil.ManifestMetadataLines(args[1])
		if err != nil {
			return err
		}
		for _, line := range lines {
			fmt.Println(line)
		}
		return nil
	case "resolver-metadata":
		if len(args) != 2 {
			return launcherUsage()
		}
		lines, err := hostutil.ResolverMetadataLines(args[1])
		if err != nil {
			return err
		}
		for _, line := range lines {
			fmt.Println(line)
		}
		return nil
	case "workspace-cache-key":
		if len(args) != 2 {
			return launcherUsage()
		}
		value, err := hostutil.WorkspaceCacheKey(args[1])
		if err != nil {
			return err
		}
		fmt.Println(value)
		return nil
	case "extract-codex-version":
		if len(args) != 2 {
			return launcherUsage()
		}
		value, err := hostutil.ExtractCodexVersion(args[1])
		if err != nil {
			return err
		}
		fmt.Println(value)
		return nil
	case "validate-security-options":
		if len(args) != 2 {
			return launcherUsage()
		}
		return hostutil.ValidateSecurityOptions(args[1])
	case "canonicalize-tool-path":
		if len(args) != 2 {
			return launcherUsage()
		}
		value, err := hostutil.CanonicalizeToolPath(args[1])
		if err != nil {
			return err
		}
		fmt.Println(value)
		return nil
	case "dedupe-endpoints":
		if len(args) != 2 {
			return launcherUsage()
		}
		fmt.Println(hostutil.DedupeEndpointList(args[1]))
		return nil
	case "resolve-endpoints":
		if len(args) != 2 {
			return launcherUsage()
		}
		lines, err := hostutil.ResolveEndpoints(args[1])
		if err != nil {
			return err
		}
		for _, line := range lines {
			fmt.Println(line)
		}
		return nil
	default:
		return launcherUsage()
	}
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
	return fmt.Errorf("usage: workcell-hostutil launcher <session-suffix|colima-status|cleanup-stale-log-pointers|profile-lock-is-stale|acquire-profile-lock|write-profile-owner|cleanup-stale-session-audit-dirs|session-record-write|session-list|session-show|session-export|session-diff-metadata|session-runtime-metadata|session-timeline|audit-digest|direct-mount-cache-key|resolve-host-output-candidate|resolve-host-output-directory-candidate|cleanup-stale-injection-bundles|manifest-metadata|resolver-metadata|workspace-cache-key|extract-codex-version|validate-security-options|canonicalize-tool-path|dedupe-endpoints|resolve-endpoints> [args...]")
}

func runLauncherSessionList(args []string) error {
	if len(args) < 1 || len(args) > 5 {
		return launcherUsage()
	}

	colimaRoot := args[0]
	format := "lines"
	verbose := false
	opts := hostutil.SessionListOptions{}
	for _, arg := range args[1:] {
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

	records, err := hostutil.ListSessionRecords(colimaRoot, opts)
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
				hostutil.SessionDisplayLiveStatus(record),
				hostutil.SessionControlMode(record),
				record.Agent,
				record.Mode,
				record.Profile,
				hostutil.SessionTargetSummary(record),
				record.TargetAssuranceClass,
				record.WorkspaceTransport,
				hostutil.SessionDisplayGitBranch(record),
				hostutil.SessionDisplayWorktree(record),
				record.StartedAt,
				hostutil.SessionAssuranceSummary(record),
				hostutil.SessionDisplayWorkspace(record),
			)
			continue
		}
		fmt.Printf(
			"%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			record.SessionID,
			record.Status,
			hostutil.SessionDisplayLiveStatus(record),
			hostutil.SessionControlMode(record),
			record.Agent,
			record.Mode,
			record.Profile,
			record.StartedAt,
			hostutil.SessionAssuranceSummary(record),
			hostutil.SessionDisplayWorkspace(record),
		)
	}
	return nil
}

func runLauncherSessionShow(args []string) error {
	if len(args) < 2 || len(args) > 3 {
		return launcherUsage()
	}

	format := "json"
	for _, arg := range args[2:] {
		if arg != "--text" {
			return launcherUsage()
		}
		format = "text"
	}
	record, err := hostutil.FindSessionRecord(args[0], args[1])
	if err != nil {
		return err
	}
	if format == "text" {
		for _, line := range hostutil.SessionShowLines(record) {
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
	if len(args) != 2 {
		return launcherUsage()
	}

	exported, err := hostutil.ExportSessionRecord(args[0], args[1])
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
	if len(args) != 2 {
		return launcherUsage()
	}

	record, err := hostutil.FindSessionRecord(args[0], args[1])
	if err != nil {
		return err
	}
	for _, line := range hostutil.SessionDiffMetadataLines(record) {
		fmt.Println(line)
	}
	return nil
}

func runLauncherSessionRuntimeMetadata(args []string) error {
	if len(args) != 2 {
		return launcherUsage()
	}

	record, err := hostutil.FindSessionRecord(args[0], args[1])
	if err != nil {
		return err
	}
	for _, line := range hostutil.SessionRuntimeMetadataLines(record) {
		fmt.Println(line)
	}
	return nil
}

func runLauncherSessionTimeline(args []string) error {
	if len(args) != 2 {
		return launcherUsage()
	}

	lines, err := hostutil.SessionTimelineRecords(args[0], args[1])
	if err != nil {
		return err
	}
	for _, line := range lines {
		fmt.Println(line)
	}
	return nil
}
