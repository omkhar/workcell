package main

import (
	"fmt"
	"io"
	"os"
	"strconv"

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
	return fmt.Errorf("usage: workcell-hostutil <path|release> ...")
}

func pathUsage() error {
	return fmt.Errorf("usage: workcell-hostutil path <home|resolve> [--base DIR] [PATH]")
}

func releaseUsage() error {
	return fmt.Errorf("usage: workcell-hostutil release <create-payload|metadata|encode-name|bundle-manifest> ...")
}

func launcherUsage() error {
	return fmt.Errorf("usage: workcell-hostutil launcher <session-suffix|colima-status|cleanup-stale-log-pointers|profile-lock-is-stale|write-profile-owner|cleanup-stale-session-audit-dirs|audit-digest|direct-mount-cache-key|resolve-host-output-candidate|cleanup-stale-injection-bundles|manifest-metadata|resolver-metadata|workspace-cache-key|extract-codex-version|validate-security-options|canonicalize-tool-path|dedupe-endpoints|resolve-endpoints> ...")
}
