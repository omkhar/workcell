package hostutil

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var codexVersionPattern = regexp.MustCompile(`(?m)^\s*ARG CODEX_VERSION=(.+)$`)

func RandomHex(n int) (string, error) {
	if n <= 0 {
		return "", errors.New("random hex size must be positive")
	}
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func ColimaProfileStatus(listJSON []byte, profile string) (string, error) {
	records, err := decodeJSONObjectSequence(listJSON)
	if err != nil {
		return "", err
	}
	for _, record := range records {
		name, _ := record["name"].(string)
		if name != profile {
			continue
		}
		status, _ := record["status"].(string)
		if status == "" {
			return "", errors.New("profile status missing status field")
		}
		return status, nil
	}
	return "", errNoMatch
}

func CleanupStaleLatestLogPointers(colimaRoot string) error {
	root := filepath.Clean(colimaRoot)
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

	uid := uint32(os.Getuid())
	pointerNames := []string{
		"workcell.latest-debug-log",
		"workcell.latest-file-trace-log",
		"workcell.latest-transcript-log",
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		profileDir := filepath.Join(root, entry.Name())
		if isSymlink(profileDir) {
			continue
		}
		for _, pointerName := range pointerNames {
			pointerPath := filepath.Join(profileDir, pointerName)
			info, err := os.Lstat(pointerPath)
			if err != nil {
				continue
			}
			if info.Mode()&os.ModeSymlink != 0 {
				continue
			}
			if statUID(info) != uid {
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
				_ = os.Remove(pointerPath)
			}
		}
	}
	return nil
}

func ProfileLockIsStale(lockDir string) (bool, error) {
	ownerPath := filepath.Join(lockDir, "owner.json")
	content, err := os.ReadFile(ownerPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return true, nil
		}
		return true, nil
	}

	var owner struct {
		PID     int    `json:"pid"`
		Started string `json:"started"`
	}
	if err := json.Unmarshal(content, &owner); err != nil {
		return true, nil
	}
	if owner.PID <= 0 || owner.Started == "" {
		return true, nil
	}

	if err := syscall.Kill(owner.PID, 0); err != nil {
		return true, nil
	}

	observed, err := processStartTime(owner.PID)
	if err != nil {
		return true, nil
	}
	return observed != owner.Started, nil
}

func WriteProfileOwner(ownerPath string, pid int) error {
	started, err := processStartTime(pid)
	if err != nil {
		return err
	}
	payload := map[string]any{
		"pid":     pid,
		"started": started,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(ownerPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(ownerPath, data, 0o600); err != nil {
		return err
	}
	return os.Chmod(ownerPath, 0o600)
}

func CleanupStaleSessionAuditDirs(colimaRoot string) error {
	root := filepath.Clean(colimaRoot)
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
	uid := uint32(os.Getuid())
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		profileDir := filepath.Join(root, entry.Name())
		if isSymlink(profileDir) {
			continue
		}
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
			if statUID(info) != uid || info.IsDir() == false || info.Mode()&os.ModeSymlink != 0 {
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

func ResolveHostOutputCandidate(raw string) (string, error) {
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
				return "", fmt.Errorf("Refusing symlinked host output path component: %s", current)
			}
		}
		if current == filepath.Dir(current) {
			break
		}
		current = filepath.Dir(current)
	}

	if info, err := os.Stat(target); err == nil && !info.Mode().IsRegular() {
		return "", fmt.Errorf("Host output path must be a regular file or a new file path: %s", target)
	}
	return target, nil
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
	uid := uint32(os.Getuid())
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
			if statUID(info) != uid || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				continue
			}
			if live, err := injectionBundleIsLive(path, cutoff); err == nil && live {
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
		if statUID(info) != uid || info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
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
		return "", errors.New("Unable to extract CODEX_VERSION from Dockerfile")
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
	return errors.New("Managed runtime requires Docker seccomp support to stay active.")
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
		addrs, err := net.DefaultResolver.LookupIPAddr(nilContext{}, host)
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

var errNoMatch = errors.New("not found")

func decodeJSONObjectSequence(raw []byte) ([]map[string]any, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, errNoMatch
	}

	if trimmed[0] == '[' {
		var records []map[string]any
		if err := json.Unmarshal(trimmed, &records); err != nil {
			return nil, err
		}
		return records, nil
	}

	var records []map[string]any
	for _, line := range bytes.Split(trimmed, []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal(line, &record); err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, nil
}

func processStartTime(pid int) (string, error) {
	cmd := exec.Command("ps", "-o", "lstart=", "-p", strconv.Itoa(pid))
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
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
		PID     any    `json:"pid"`
		Started string `json:"started"`
	}
	if err := json.Unmarshal(content, &owner); err != nil {
		return false, nil
	}
	pid, ok := owner.PID.(float64)
	if !ok || owner.Started == "" {
		return false, nil
	}
	started, err := processStartTime(int(pid))
	if err != nil {
		return false, nil
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
	sort.Strings(keys)
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

type nilContext struct{}

func (nilContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (nilContext) Done() <-chan struct{}       { return nil }
func (nilContext) Err() error                  { return nil }
func (nilContext) Value(key any) any           { return nil }

func statUID(info os.FileInfo) uint32 {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		return stat.Uid
	}
	return ^uint32(0)
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
