// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package hostutil

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/omkhar/workcell/internal/host/launcher"
	"github.com/omkhar/workcell/internal/host/sessions"
)

var codexVersionPattern = regexp.MustCompile(`(?m)^\s*ARG CODEX_VERSION=(.+)$`)

func CleanupStaleLatestLogPointers(stateRoot string) error {
	stateDirs, err := sessions.StateDirs(stateRoot)
	if err != nil {
		return err
	}
	uid, uidOK := currentUID()
	pointerNames := []string{
		"workcell.latest-debug-log",
		"workcell.latest-file-trace-log",
		"workcell.latest-transcript-log",
	}
	for _, profileDir := range stateDirs {
		for _, pointerName := range pointerNames {
			pointerPath := filepath.Join(profileDir, pointerName)
			info, err := os.Lstat(pointerPath)
			if err != nil {
				continue
			}
			if info.Mode()&os.ModeSymlink != 0 {
				continue
			}
			fileUID, ok := statUID(info)
			if !uidOK || !ok || fileUID != uid {
				continue
			}
			content, err := os.ReadFile(pointerPath)
			if err != nil {
				continue
			}
			lines := strings.Split(strings.TrimRight(string(content), "\n"), "\n")
			if len(lines) == 0 || lines[0] == "" {
				_ = os.Remove(pointerPath)
				continue
			}
			target := strings.TrimSpace(expandUserPathForLauncher(lines[0]))
			if target == "" {
				_ = os.Remove(pointerPath)
				continue
			}
			if _, err := os.Stat(target); err != nil {
				if errors.Is(err, os.ErrNotExist) {
					_ = os.Remove(pointerPath)
				}
			}
		}
	}
	return nil
}

func CleanupStaleSessionAuditDirs(stateRoot string) error {
	stateDirs, err := sessions.StateDirs(stateRoot)
	if err != nil {
		return err
	}
	cutoff := time.Now().Add(-12 * time.Hour)
	uid, uidOK := currentUID()
	if !uidOK {
		return nil
	}
	for _, profileDir := range stateDirs {
		candidates, err := os.ReadDir(profileDir)
		if err != nil {
			continue
		}
		for _, candidate := range candidates {
			if !strings.HasPrefix(candidate.Name(), "session-audit.") {
				continue
			}
			path := filepath.Join(profileDir, candidate.Name())
			info, err := os.Lstat(path)
			if err != nil {
				continue
			}
			fileUID, ok := statUID(info)
			if !ok || fileUID != uid || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				continue
			}
			if info.ModTime().After(cutoff) {
				continue
			}
			_ = os.RemoveAll(path)
		}
	}
	return nil
}

func AuditRecordDigest(prevDigest, timestamp string, args []string) string {
	values := append([]string{prevDigest, timestamp}, args...)
	sum := sha256.Sum256([]byte(strings.Join(values, "\x00")))
	return hex.EncodeToString(sum[:])
}

func DirectMountCacheKey(hostSource, mountPath string) string {
	sum := sha256.Sum256([]byte(hostSource + "\x00" + mountPath + "\x00"))
	return hex.EncodeToString(sum[:8])
}

func resolveHostOutputCandidate(raw string, allowExistingDir bool) (string, error) {
	if raw == "" {
		return "", errors.New("host output path is required")
	}
	expanded, err := expandUser(raw)
	if err != nil {
		return "", err
	}
	target, err := filepath.Abs(expanded)
	if err != nil {
		return "", err
	}
	target = filepath.Clean(target)
	allowedSymlinkRoots := map[string]struct{}{}
	if runtime.GOOS == "darwin" {
		allowedSymlinkRoots["/var"] = struct{}{}
		allowedSymlinkRoots["/tmp"] = struct{}{}
	}

	current := target
	for {
		info, err := os.Lstat(current)
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			if _, ok := allowedSymlinkRoots[current]; !ok {
				return "", fmt.Errorf("refusing symlinked host output path component: %s", current)
			}
		}
		if current == filepath.Dir(current) {
			break
		}
		current = filepath.Dir(current)
	}

	info, err := os.Stat(target)
	if err == nil {
		if allowExistingDir {
			if info.IsDir() {
				return target, nil
			}
			return "", fmt.Errorf("host output path must be a directory or a new directory path: %s", target)
		}
		if info.Mode().IsRegular() {
			return target, nil
		}
		return "", fmt.Errorf("host output path must be a regular file or a new file path: %s", target)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("stat host output candidate %s: %w", target, err)
	}
	return target, nil
}

func ResolveHostOutputCandidate(raw string) (string, error) {
	return resolveHostOutputCandidate(raw, false)
}

func ResolveHostOutputDirectoryCandidate(raw string) (string, error) {
	return resolveHostOutputCandidate(raw, true)
}

func CleanupStaleInjectionBundles(bundleParent string) error {
	root := filepath.Clean(bundleParent)
	info, err := os.Stat(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", root)
	}

	cutoff := time.Now().Add(-12 * time.Hour)
	uid, uidOK := currentUID()
	if !uidOK {
		return nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		path := filepath.Join(root, name)
		if strings.HasPrefix(name, "workcell-injections.") && !strings.HasSuffix(name, ".mounts.json") {
			info, err := os.Lstat(path)
			if err != nil {
				continue
			}
			fileUID, ok := statUID(info)
			if !ok || fileUID != uid || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				continue
			}
			// Only remove on confirmed not-live. Surface other errors
			// (corrupted owner.json, transient ps failure) by keeping
			// the bundle - safer than wiping an active session.
			live, liveErr := injectionBundleIsLive(path, cutoff)
			if liveErr != nil || live {
				continue
			}
			_ = os.RemoveAll(path)
			sidecar := filepath.Join(root, name+".mounts.json")
			_ = os.Remove(sidecar)
		}
	}

	entries, err = os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, "workcell-injections.") || !strings.HasSuffix(name, ".mounts.json") {
			continue
		}
		path := filepath.Join(root, name)
		info, err := os.Lstat(path)
		if err != nil {
			continue
		}
		fileUID, ok := statUID(info)
		if !ok || fileUID != uid || info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		bundleDir := filepath.Join(root, strings.TrimSuffix(name, ".mounts.json"))
		if _, err := os.Stat(bundleDir); err == nil {
			continue
		}
		_ = os.Remove(path)
	}
	return nil
}

func ManifestMetadataLines(manifestPath string) ([]string, error) {
	var manifest map[string]any
	if err := readJSONFile(manifestPath, &manifest); err != nil {
		return nil, err
	}
	metadata, _ := manifest["metadata"].(map[string]any)
	return []string{
		stringOrEmpty(metadata["policy_sha256"]),
		strings.Join(stringSlice(metadata["credential_keys"]), ","),
		strings.Join(stringSlice(metadata["extra_endpoints"]), " "),
		boolTo01(metadata["ssh_enabled"]),
		stringOrDefault(metadata["ssh_config_assurance"], "off"),
		strings.Join(stringSlice(metadata["secret_copy_targets"]), ","),
	}, nil
}

func ResolverMetadataLines(metadataPath string) ([]string, error) {
	var metadata map[string]any
	if err := readJSONFile(metadataPath, &metadata); err != nil {
		return nil, err
	}
	return []string{
		renderStringMap(metadata["credential_input_kinds"]),
		renderStringMap(metadata["credential_resolvers"]),
		renderStringMap(metadata["credential_materialization"]),
		renderStringMap(metadata["credential_resolution_states"]),
		renderStringMap(metadata["provider_auth_ready_states"]),
		renderStringMap(metadata["shared_auth_ready_states"]),
	}, nil
}

func WorkspaceCacheKey(workspace string) (string, error) {
	canonical, err := CanonicalizePath(workspace)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:8]), nil
}

func ExtractCodexVersion(dockerfilePath string) (string, error) {
	content, err := os.ReadFile(dockerfilePath)
	if err != nil {
		return "", err
	}
	match := codexVersionPattern.FindStringSubmatch(string(content))
	if match == nil {
		return "", errors.New("unable to extract CODEX_VERSION from Dockerfile")
	}
	return strings.TrimSpace(match[1]), nil
}

func ValidateSecurityOptions(raw string) error {
	var options []any
	if err := json.Unmarshal([]byte(raw), &options); err != nil {
		return err
	}
	for _, option := range options {
		if s, ok := option.(string); ok && strings.HasPrefix(s, "name=seccomp") {
			return nil
		}
	}
	return errors.New("managed runtime requires Docker seccomp support to stay active")
}

func CanonicalizeToolPath(candidate string) (string, error) {
	if candidate == "" {
		return "", nil
	}
	return CanonicalizePath(candidate)
}

func DedupeEndpointList(raw string) string {
	seen := make(map[string]struct{})
	ordered := make([]string, 0)
	for _, entry := range strings.Fields(raw) {
		if entry == "" {
			continue
		}
		if _, ok := seen[entry]; ok {
			continue
		}
		seen[entry] = struct{}{}
		ordered = append(ordered, entry)
	}
	return strings.Join(ordered, " ")
}

func ResolveEndpoints(raw string) ([]string, error) {
	endpoints := strings.Fields(raw)
	results := make([]string, 0)
	for _, endpoint := range endpoints {
		host, _, ok := strings.Cut(endpoint, ":")
		if !ok {
			continue
		}
		if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
			continue
		}
		if isNumericHost(host) {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		cancel()
		if err != nil {
			continue
		}
		seen := map[string]struct{}{}
		var ipv4Addrs []string
		var ipv6Addrs []string
		for _, addr := range addrs {
			ip := addr.IP.String()
			if ip == "" {
				continue
			}
			if _, ok := seen[ip]; ok {
				continue
			}
			seen[ip] = struct{}{}
			if addr.IP.To4() != nil {
				ipv4Addrs = append(ipv4Addrs, ip)
			} else {
				ipv6Addrs = append(ipv6Addrs, ip)
			}
		}
		for _, ip := range append(ipv4Addrs, ipv6Addrs...) {
			results = append(results, host+"\t"+ip)
		}
	}
	return results, nil
}

func injectionBundleIsLive(bundlePath string, cutoff time.Time) (bool, error) {
	ownerMetaPath := filepath.Join(bundlePath, "owner.json")
	content, err := os.ReadFile(ownerMetaPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			info, statErr := os.Lstat(bundlePath)
			if statErr != nil {
				return false, statErr
			}
			return info.ModTime().After(cutoff), nil
		}
		return false, err
	}

	var owner struct {
		PID     int    `json:"pid"`
		Started string `json:"started"`
	}
	if err := json.Unmarshal(content, &owner); err != nil {
		// Corrupted owner.json - safer to treat as live and refuse
		// deletion than risk wiping an active bundle on transient
		// metadata corruption.
		return true, fmt.Errorf("parse owner.json at %s: %w", ownerMetaPath, err)
	}
	if owner.PID <= 0 || owner.Started == "" {
		return false, nil
	}
	started, err := launcher.ProcessStartTime(owner.PID)
	if err != nil {
		// launcher.ProcessStartTime distinguishes ESRCH ("process gone")
		// from other errors via launcher.IsProcessGone. If the process
		// is definitively gone, the bundle is dead. Anything else is a
		// transient lookup failure - keep the bundle.
		if launcher.IsProcessGone(err) {
			return false, nil
		}
		return true, fmt.Errorf("process start time for pid %d: %w", owner.PID, err)
	}
	return started == owner.Started, nil
}

func renderStringMap(value any) string {
	table, ok := value.(map[string]any)
	if !ok || len(table) == 0 {
		return "none"
	}
	keys := make([]string, 0, len(table))
	for key := range table {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s:%v", key, table[key]))
	}
	return strings.Join(parts, ",")
}

func stringSlice(value any) []string {
	if value == nil {
		return nil
	}
	raw, ok := value.([]any)
	if !ok {
		if typed, ok := value.([]string); ok {
			return typed
		}
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func stringOrEmpty(value any) string {
	if s, ok := value.(string); ok {
		return s
	}
	return ""
}

func stringOrDefault(value any, fallback string) string {
	if s, ok := value.(string); ok && s != "" {
		return s
	}
	return fallback
}

func boolTo01(value any) string {
	if b, ok := value.(bool); ok && b {
		return "1"
	}
	return "0"
}

func statUID(info os.FileInfo) (uint32, bool) {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		return stat.Uid, true
	}
	return 0, false
}

func currentUID() (uint32, bool) {
	uid := os.Getuid()
	if uid < 0 {
		return 0, false
	}
	return uint32(uid), true
}

func isSymlink(path string) bool {
	info, err := os.Lstat(path)
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeSymlink != 0
}

func expandUserPathForLauncher(raw string) string {
	if raw == "" {
		return ""
	}
	if raw == "~" || strings.HasPrefix(raw, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return raw
		}
		if raw == "~" {
			return home
		}
		return filepath.Join(home, raw[2:])
	}
	return raw
}

func readJSONFile(path string, target any) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(content, target)
}

func isNumericHost(host string) bool {
	if host == "" {
		return false
	}
	return strings.Trim(host, ".") != "" && strings.IndexFunc(host, func(r rune) bool {
		return (r < '0' || r > '9') && r != '.'
	}) == -1
}
