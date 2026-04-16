// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var (
	hexDigestPattern      = regexp.MustCompile(`^[0-9a-f]{64}$`)
	workflowPermissionsRE = regexp.MustCompile(`(?m)^permissions:\s+\{\}$`)
	aptInstallPattern     = regexp.MustCompile(`apt-get install -y --no-install-recommends(?s:(.*?))&&`)
)

type ControlPlaneArtifact struct {
	Kind        string
	RepoPath    string
	RuntimePath string
}

type PinnedInputsConfig struct {
	RuntimeDockerfilePath         string
	ValidatorDockerfilePath       string
	RemoteValidatorDockerfilePath string
	ProvidersPackageJSONPath      string
	ProvidersPackageLockPath      string
	WorkflowsDir                  string
	CIWorkflowPath                string
	ReleaseWorkflowPath           string
	PinHygieneWorkflowPath        string
	CodeownersPath                string
	CodexRequirementsPath         string
	CodexMCPConfigPath            string
	HostedControlsPolicyPath      string
	HostedControlsScriptPath      string
	ProviderBumpPolicyPath        string
	MaxDebianSnapshotAgeDays      int
}

func readText(path string) (string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(content), nil
}

func readJSONFile(path string, target any) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(content, target)
}

func writeJSONFile(path string, value any) error {
	content, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	content = append(content, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, content, 0o644)
}

func sha256HexFile(path string) (string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:]), nil
}

func isHexDigest(value string) bool {
	return hexDigestPattern.MatchString(value)
}

func resolveRepoRootFromDockerfile(dockerfilePath string) string {
	return filepath.Clean(filepath.Join(filepath.Dir(dockerfilePath), "..", ".."))
}

func ensureRegularFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("control-plane artifact must be a regular file: %s", path)
	}
	return nil
}

func ensureNoSymlinkPrefix(rootDir, repoPath string) error {
	return ensureNoSymlinkRelativePath(rootDir, repoPath, "control-plane artifact")
}

func ensureNoSymlinkRelativePath(rootDir, relativePath, label string) error {
	relativeParts := strings.Split(filepath.Clean(relativePath), string(filepath.Separator))
	current := rootDir
	for _, part := range relativeParts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s must not be a symlink: %s", label, relativePath)
		}
	}
	return nil
}

func ensureTrackedRegularFile(rootDir, repoPath string) error {
	path := filepath.Join(rootDir, repoPath)
	if err := ensureNoSymlinkPrefix(rootDir, repoPath); err != nil {
		return err
	}
	if err := ensureRegularFile(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("missing control-plane artifact: %s", repoPath)
		}
		return fmt.Errorf("control-plane artifact must be a regular file: %s", repoPath)
	}
	return nil
}

func gitOutput(rootDir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", rootDir}, args...)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", err
	}
	return string(output), nil
}

func gitInsideWorkTree(rootDir string) bool {
	output, err := gitOutput(rootDir, "rev-parse", "--is-inside-work-tree")
	if err != nil {
		return false
	}
	return strings.TrimSpace(output) == "true"
}

func gitTopLevel(rootDir string) (string, error) {
	output, err := gitOutput(rootDir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	return filepath.Clean(strings.TrimSpace(output)), nil
}

func gitTrackedFiles(rootDir string) ([]string, bool, error) {
	if !gitInsideWorkTree(rootDir) {
		return nil, false, nil
	}
	topLevel, err := gitTopLevel(rootDir)
	if err != nil {
		return nil, false, nil
	}
	if filepath.Clean(topLevel) != filepath.Clean(rootDir) {
		return nil, false, nil
	}

	output, err := gitOutput(rootDir, "ls-files", "-z", "--cached", "--modified", "--deduplicate")
	if err != nil {
		return nil, false, err
	}
	parts := strings.Split(output, "\x00")
	tracked := make([]string, 0, len(parts))
	for _, path := range parts {
		if path == "" {
			continue
		}
		if err := ensureNoSymlinkRelativePath(rootDir, path, "tracked release input"); err != nil {
			return nil, true, err
		}
		info, err := os.Lstat(filepath.Join(rootDir, path))
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		if path != "" {
			tracked = append(tracked, path)
		}
	}
	sort.Strings(tracked)
	return tracked, true, nil
}

func gitTrackedSubset(rootDir string, paths []string, requireTracked bool) ([]string, error) {
	unique := make([]string, 0, len(paths))
	seen := map[string]struct{}{}
	for _, path := range paths {
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		unique = append(unique, path)
	}
	sort.Strings(unique)

	if !gitInsideWorkTree(rootDir) {
		return unique, nil
	}
	topLevel, err := gitTopLevel(rootDir)
	if err != nil {
		return unique, nil
	}
	if filepath.Clean(topLevel) != filepath.Clean(rootDir) {
		return unique, nil
	}

	args := append([]string{"ls-files", "--"}, unique...)
	output, err := gitOutput(rootDir, args...)
	if err != nil {
		return nil, err
	}
	tracked := map[string]struct{}{}
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if line != "" {
			tracked[line] = struct{}{}
		}
	}
	omitted := make([]string, 0)
	rendered := make([]string, 0, len(unique))
	for _, path := range unique {
		if _, ok := tracked[path]; ok {
			rendered = append(rendered, path)
			continue
		}
		omitted = append(omitted, path)
	}
	if requireTracked && len(omitted) > 0 {
		var builder strings.Builder
		builder.WriteString("Release-critical inputs must be tracked before generating a verified build input manifest:\n")
		for _, path := range omitted {
			builder.WriteString("  - ")
			builder.WriteString(path)
			builder.WriteByte('\n')
		}
		return nil, errors.New(strings.TrimSuffix(builder.String(), "\n"))
	}
	return rendered, nil
}

func walkFiles(rootDir, relativeRoot string, excludeParts ...string) ([]string, error) {
	base := filepath.Join(rootDir, relativeRoot)
	excluded := map[string]struct{}{}
	for _, part := range excludeParts {
		excluded[part] = struct{}{}
	}

	paths := make([]string, 0)
	err := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(rootDir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		parts := strings.Split(rel, string(filepath.Separator))
		for _, part := range parts {
			if _, ok := excluded[part]; ok {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}
		isRegular, err := dirEntryIsRegular(d)
		if err != nil {
			return err
		}
		if isRegular {
			paths = append(paths, rel)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	return paths, nil
}

func walkRepoFiles(rootDir string) ([]string, error) {
	excluded := map[string]struct{}{
		".git":         {},
		"dist":         {},
		"tmp":          {},
		"node_modules": {},
		"target":       {},
	}
	paths := make([]string, 0)
	err := filepath.WalkDir(rootDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(rootDir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		parts := strings.Split(rel, string(filepath.Separator))
		for _, part := range parts {
			if _, ok := excluded[part]; ok {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}
		if strings.Contains(rel, "__pycache__") || strings.HasSuffix(rel, ".pyc") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		isRegular, err := dirEntryIsRegular(d)
		if err != nil {
			return err
		}
		if isRegular {
			paths = append(paths, rel)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	return paths, nil
}

func dirEntryIsRegular(d fs.DirEntry) (bool, error) {
	mode := d.Type()
	if mode.IsRegular() {
		return true, nil
	}
	if mode&fs.ModeType != 0 {
		return false, nil
	}
	info, err := d.Info()
	if err != nil {
		return false, err
	}
	return info.Mode().IsRegular(), nil
}

func digestMap(rootDir string, paths []string) (map[string]string, error) {
	result := make(map[string]string, len(paths))
	for _, relativePath := range paths {
		candidate := filepath.Join(rootDir, relativePath)
		if err := ensureNoSymlinkRelativePath(rootDir, relativePath, "tracked release input"); err != nil {
			return nil, err
		}
		info, err := os.Lstat(candidate)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil, fmt.Errorf(
					"tracked release input is missing from the working tree; stage the deletion or restore the file before generating a verified build input manifest: %s",
					relativePath,
				)
			}
			return nil, err
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("tracked release input is missing from the working tree; stage the deletion or restore the file before generating a verified build input manifest: %s", relativePath)
		}
		sum, err := sha256HexFile(candidate)
		if err != nil {
			return nil, err
		}
		result[relativePath] = sum
	}
	return result, nil
}

func ExtractDockerfileArg(dockerfilePath, argName string) (string, error) {
	text, err := readText(dockerfilePath)
	if err != nil {
		return "", err
	}
	pattern := regexp.MustCompile(`(?m)^ARG ` + regexp.QuoteMeta(argName) + `=(.+)$`)
	match := pattern.FindStringSubmatch(text)
	if match == nil {
		return "", fmt.Errorf("unable to extract %s from %s", argName, dockerfilePath)
	}
	return strings.TrimSpace(match[1]), nil
}

func ExtractClaudeSHA(dockerfilePath, targetArch string) (string, error) {
	text, err := readText(dockerfilePath)
	if err != nil {
		return "", err
	}
	pattern := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(targetArch) + `\)\s+\\\s*CLAUDE_PLATFORM="[^"]+";\s+\\\s*CLAUDE_SHA256="([0-9a-f]{64})";`)
	match := pattern.FindStringSubmatch(text)
	if match == nil {
		return "", fmt.Errorf("unable to extract CLAUDE_SHA256 for %s", targetArch)
	}
	return match[1], nil
}

func ExtractCodexSHA(dockerfilePath, targetArch string) (string, error) {
	text, err := readText(dockerfilePath)
	if err != nil {
		return "", err
	}
	pattern := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(targetArch) + `\)\s+\\(?:\s*CLAUDE_[A-Z0-9_]+="[^"]+";\s+\\)*\s*CODEX_ARCH="[^"]+";\s+\\\s*CODEX_SHA256="([0-9a-f]{64})";`)
	match := pattern.FindStringSubmatch(text)
	if match == nil {
		return "", fmt.Errorf("unable to extract CODEX_SHA256 for %s", targetArch)
	}
	return match[1], nil
}

func ManifestChecksum(manifestPath, platform string) (string, error) {
	var manifest map[string]any
	if err := readJSONFile(manifestPath, &manifest); err != nil {
		return "", err
	}
	platforms, ok := manifest["platforms"].(map[string]any)
	if !ok {
		return "", fmt.Errorf("missing checksum for %s in Claude release manifest", platform)
	}
	entry, ok := platforms[platform].(map[string]any)
	if !ok {
		return "", fmt.Errorf("missing checksum for %s in Claude release manifest", platform)
	}
	checksum, ok := entry["checksum"].(string)
	if !ok || checksum == "" {
		return "", fmt.Errorf("missing checksum for %s in Claude release manifest", platform)
	}
	return checksum, nil
}

func ManifestVersion(manifestPath, expectedVersion string) error {
	var manifest map[string]any
	if err := readJSONFile(manifestPath, &manifest); err != nil {
		return err
	}
	version, _ := manifest["version"].(string)
	if version != expectedVersion {
		return fmt.Errorf(
			"claude release manifest version %q does not match pinned %q",
			version,
			expectedVersion,
		)
	}
	return nil
}

func GenerateControlPlaneManifest(rootDir, outputPath string) error {
	hostArtifacts := []string{
		"scripts/workcell",
		"scripts/lib/extract_direct_mounts",
		"scripts/lib/render_injection_bundle",
		"scripts/lib/trusted-docker-client.sh",
	}
	runtimeArtifacts := []ControlPlaneArtifact{
		{Kind: "adapter-baseline", RepoPath: "adapters/claude/.claude/settings.json", RuntimePath: "/opt/workcell/adapters/claude/.claude/settings.json"},
		{Kind: "adapter-baseline", RepoPath: "adapters/claude/CLAUDE.md", RuntimePath: "/opt/workcell/adapters/claude/CLAUDE.md"},
		{Kind: "adapter-baseline", RepoPath: "adapters/claude/hooks/guard-bash.sh", RuntimePath: "/opt/workcell/adapters/claude/hooks/guard-bash.sh"},
		{Kind: "adapter-baseline", RepoPath: "adapters/claude/managed-settings.json", RuntimePath: "/opt/workcell/adapters/claude/managed-settings.json"},
		{Kind: "adapter-baseline", RepoPath: "adapters/claude/mcp-template.json", RuntimePath: "/opt/workcell/adapters/claude/mcp-template.json"},
		{Kind: "adapter-baseline", RepoPath: "adapters/codex/.codex/AGENTS.md", RuntimePath: "/opt/workcell/adapters/codex/.codex/AGENTS.md"},
		{Kind: "adapter-baseline", RepoPath: "adapters/codex/.codex/agents/anthropic_claude_compat.md", RuntimePath: "/opt/workcell/adapters/codex/.codex/agents/anthropic_claude_compat.md"},
		{Kind: "adapter-baseline", RepoPath: "adapters/codex/.codex/agents/apple_platform_boundary.md", RuntimePath: "/opt/workcell/adapters/codex/.codex/agents/apple_platform_boundary.md"},
		{Kind: "adapter-baseline", RepoPath: "adapters/codex/.codex/agents/distinguished_security.md", RuntimePath: "/opt/workcell/adapters/codex/.codex/agents/distinguished_security.md"},
		{Kind: "adapter-baseline", RepoPath: "adapters/codex/.codex/agents/openai_codex_platform.md", RuntimePath: "/opt/workcell/adapters/codex/.codex/agents/openai_codex_platform.md"},
		{Kind: "adapter-baseline", RepoPath: "adapters/codex/.codex/config.toml", RuntimePath: "/opt/workcell/adapters/codex/.codex/config.toml"},
		{Kind: "adapter-baseline", RepoPath: "adapters/codex/.codex/rules/default.rules", RuntimePath: "/opt/workcell/adapters/codex/.codex/rules/default.rules"},
		{Kind: "adapter-baseline", RepoPath: "adapters/codex/managed_config.toml", RuntimePath: "/opt/workcell/adapters/codex/managed_config.toml"},
		{Kind: "adapter-baseline", RepoPath: "adapters/codex/mcp/config.toml", RuntimePath: "/opt/workcell/adapters/codex/mcp/config.toml"},
		{Kind: "adapter-baseline", RepoPath: "adapters/codex/requirements.toml", RuntimePath: "/opt/workcell/adapters/codex/requirements.toml"},
		{Kind: "adapter-baseline", RepoPath: "adapters/gemini/.gemini/settings.json", RuntimePath: "/opt/workcell/adapters/gemini/.gemini/settings.json"},
		{Kind: "adapter-baseline", RepoPath: "adapters/gemini/GEMINI.md", RuntimePath: "/opt/workcell/adapters/gemini/GEMINI.md"},
		{Kind: "runtime-control-plane", RepoPath: "runtime/container/assurance.sh", RuntimePath: "/usr/local/libexec/workcell/assurance.sh"},
		{Kind: "runtime-control-plane", RepoPath: "runtime/container/bin/apt-helper.sh", RuntimePath: "/usr/local/libexec/workcell/apt-helper.sh"},
		{Kind: "runtime-control-plane", RepoPath: "runtime/container/bin/apt-wrapper.sh", RuntimePath: "/usr/local/libexec/workcell/apt-wrapper.sh"},
		{Kind: "runtime-control-plane", RepoPath: "runtime/container/development-wrapper.sh", RuntimePath: "/usr/local/libexec/workcell/development-wrapper.sh"},
		{Kind: "runtime-control-plane", RepoPath: "runtime/container/entrypoint.sh", RuntimePath: "/usr/local/libexec/workcell/entrypoint.sh"},
		{Kind: "runtime-control-plane", RepoPath: "runtime/container/bin/git", RuntimePath: "/usr/local/libexec/workcell/git-wrapper.sh"},
		{Kind: "runtime-control-plane", RepoPath: "runtime/container/home-control-plane.sh", RuntimePath: "/usr/local/libexec/workcell/home-control-plane.sh"},
		{Kind: "runtime-control-plane", RepoPath: "runtime/container/bin/node", RuntimePath: "/usr/local/libexec/workcell/node-wrapper.sh"},
		{Kind: "runtime-control-plane", RepoPath: "runtime/container/provider-policy.sh", RuntimePath: "/usr/local/libexec/workcell/provider-policy.sh"},
		{Kind: "runtime-control-plane", RepoPath: "runtime/container/provider-wrapper.sh", RuntimePath: "/usr/local/libexec/workcell/provider-wrapper.sh"},
		{Kind: "runtime-control-plane", RepoPath: "runtime/container/public-node-guard.mjs", RuntimePath: "/usr/local/libexec/workcell/public-node-guard.mjs"},
		{Kind: "runtime-control-plane", RepoPath: "runtime/container/runtime-user.sh", RuntimePath: "/usr/local/libexec/workcell/runtime-user.sh"},
		{Kind: "runtime-control-plane", RepoPath: "adapters/claude/managed-settings.json", RuntimePath: "/etc/claude-code/managed-settings.json"},
	}

	renderedHost := make([]map[string]any, 0, len(hostArtifacts))
	for _, repoPath := range hostArtifacts {
		if err := ensureTrackedRegularFile(rootDir, repoPath); err != nil {
			return err
		}
		sum, err := sha256HexFile(filepath.Join(rootDir, repoPath))
		if err != nil {
			return err
		}
		renderedHost = append(renderedHost, map[string]any{
			"repo_path": repoPath,
			"sha256":    sum,
		})
	}

	renderedRuntime := make([]map[string]any, 0, len(runtimeArtifacts))
	for _, artifact := range runtimeArtifacts {
		if err := ensureTrackedRegularFile(rootDir, artifact.RepoPath); err != nil {
			return err
		}
		sum, err := sha256HexFile(filepath.Join(rootDir, artifact.RepoPath))
		if err != nil {
			return err
		}
		renderedRuntime = append(renderedRuntime, map[string]any{
			"kind":         artifact.Kind,
			"repo_path":    artifact.RepoPath,
			"runtime_path": artifact.RuntimePath,
			"sha256":       sum,
		})
	}

	manifest := map[string]any{
		"schema_version":    2,
		"host_artifacts":    renderedHost,
		"runtime_artifacts": renderedRuntime,
	}
	return writeJSONFile(outputPath, manifest)
}

func ValidateControlPlaneManifest(manifestPath string) error {
	var manifest map[string]any
	if err := readJSONFile(manifestPath, &manifest); err != nil {
		return err
	}
	schemaVersion, ok := manifest["schema_version"].(float64)
	if !ok || schemaVersion != 2 {
		return errors.New("control-plane manifest must use schema_version 2")
	}

	hostArtifacts, ok := manifest["host_artifacts"].([]any)
	if !ok || len(hostArtifacts) == 0 {
		return errors.New("control-plane manifest must include non-empty host_artifacts")
	}
	runtimeArtifacts, ok := manifest["runtime_artifacts"].([]any)
	if !ok || len(runtimeArtifacts) == 0 {
		return errors.New("control-plane manifest must include non-empty runtime_artifacts")
	}

	seenHost := map[string]struct{}{}
	for _, raw := range hostArtifacts {
		entry, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("unexpected host artifact shape: %v", raw)
		}
		if len(entry) != 2 {
			return fmt.Errorf("unexpected host artifact shape: %v", entry)
		}
		repoPath, ok := entry["repo_path"].(string)
		if !ok || repoPath == "" {
			return fmt.Errorf("unexpected host artifact shape: %v", entry)
		}
		if _, exists := seenHost[repoPath]; exists {
			return fmt.Errorf("duplicate host artifact path: %s", repoPath)
		}
		seenHost[repoPath] = struct{}{}
		sha256Value, ok := entry["sha256"].(string)
		if !ok || !isHexDigest(sha256Value) {
			return fmt.Errorf("invalid host artifact digest: %v", entry)
		}
	}

	seenRuntime := map[string]struct{}{}
	for _, raw := range runtimeArtifacts {
		entry, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("runtime artifact is missing data: %v", raw)
		}
		for _, key := range []string{"kind", "repo_path", "runtime_path", "sha256"} {
			if _, exists := entry[key]; !exists {
				return fmt.Errorf("runtime artifact is missing %s: %v", key, entry)
			}
		}
		runtimePath, ok := entry["runtime_path"].(string)
		if !ok || !strings.HasPrefix(runtimePath, "/") {
			return fmt.Errorf("runtime artifact path must be absolute: %v", entry)
		}
		if _, exists := seenRuntime[runtimePath]; exists {
			return fmt.Errorf("duplicate runtime artifact path: %s", runtimePath)
		}
		seenRuntime[runtimePath] = struct{}{}
		sha256Value, ok := entry["sha256"].(string)
		if !ok || !isHexDigest(sha256Value) {
			return fmt.Errorf("invalid runtime artifact digest: %v", entry)
		}
	}
	return nil
}

func ControlPlaneParityRows(manifestPath string) ([]string, error) {
	var manifest map[string]any
	if err := readJSONFile(manifestPath, &manifest); err != nil {
		return nil, err
	}
	runtimeArtifacts, ok := manifest["runtime_artifacts"].([]any)
	if !ok {
		return nil, errors.New("control-plane manifest must include runtime_artifacts")
	}
	runtimePaths := map[string]struct{}{}
	for _, raw := range runtimeArtifacts {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		runtimePath, _ := entry["runtime_path"].(string)
		if runtimePath != "" {
			runtimePaths[runtimePath] = struct{}{}
		}
	}

	rows := make([]string, 0, 4)
	for _, provider := range []string{"codex", "claude", "gemini"} {
		prefix := "/opt/workcell/adapters/" + provider + "/"
		for runtimePath := range runtimePaths {
			if strings.HasPrefix(runtimePath, prefix) {
				rows = append(rows, "prefix\t"+provider+"\t"+prefix)
				break
			}
		}
	}
	if _, ok := runtimePaths["/etc/claude-code/managed-settings.json"]; ok {
		rows = append(rows, "path\tclaude-managed-settings\t/etc/claude-code/managed-settings.json")
	}
	return rows, nil
}

func GenerateBuilderEnvironmentManifest(
	outputPath, buildkitImage, buildxVersionTarget, cosignVersionTarget, qemuImage, syftVersionTarget, buildxVersion, buildxInspect, dockerVersionJSON, qemuVersion, cosignVersion, curlVersion, gitVersion, gzipVersion, syftVersion, tarVersion string,
) error {
	var dockerVersion any
	if err := json.Unmarshal([]byte(dockerVersionJSON), &dockerVersion); err != nil {
		return err
	}

	builder := map[string]any{
		"buildkit_image":        buildkitImage,
		"buildx_inspect":        buildxInspect,
		"buildx_version_target": buildxVersionTarget,
		"buildx_version":        buildxVersion,
		"cosign_version_target": cosignVersionTarget,
		"cosign_version":        cosignVersion,
		"curl_version":          curlVersion,
		"docker_version":        dockerVersion,
		"git_version":           gitVersion,
		"gzip_version":          gzipVersion,
		"syft_version_target":   syftVersionTarget,
		"syft_version":          syftVersion,
		"tar_version":           tarVersion,
	}
	if qemuImage != "" {
		builder["qemu"] = map[string]any{
			"image":   qemuImage,
			"version": qemuVersion,
		}
	}

	manifest := map[string]any{
		"schema_version": 1,
		"builder":        builder,
	}
	return writeJSONFile(outputPath, manifest)
}

func GenerateBuildInputManifest(
	dockerfilePath, packageJSONPath, packageLockPath, outputPath, buildRef string, sourceDateEpoch int64, requireTracked bool,
) error {
	rootDir := resolveRepoRootFromDockerfile(dockerfilePath)
	dockerfile, err := readText(dockerfilePath)
	if err != nil {
		return err
	}
	packageJSONText, err := readText(packageJSONPath)
	if err != nil {
		return err
	}
	packageLockText, err := readText(packageLockPath)
	if err != nil {
		return err
	}

	nodeBaseImage, err := ExtractDockerfileArg(dockerfilePath, "NODE_BASE_IMAGE")
	if err != nil {
		return err
	}
	debianSnapshot, err := ExtractDockerfileArg(dockerfilePath, "DEBIAN_SNAPSHOT")
	if err != nil {
		return err
	}
	claudeVersion, err := ExtractDockerfileArg(dockerfilePath, "CLAUDE_VERSION")
	if err != nil {
		return err
	}
	codexVersion, err := ExtractDockerfileArg(dockerfilePath, "CODEX_VERSION")
	if err != nil {
		return err
	}

	claudeAssets := map[string]any{}
	for _, target := range []struct {
		arch     string
		platform string
	}{
		{arch: "arm64", platform: "linux-arm64"},
		{arch: "amd64", platform: "linux-x64"},
	} {
		sha, err := ExtractClaudeSHA(dockerfilePath, target.arch)
		if err != nil {
			return err
		}
		claudeAssets[target.arch] = map[string]any{
			"platform": target.platform,
			"sha256":   sha,
			"url": "https://storage.googleapis.com/claude-code-dist-86c565f3-f756-42ad-8dfa-d59b1c096819/claude-code-releases/" +
				claudeVersion + "/" + target.platform + "/claude",
		}
	}

	codexAssets := map[string]any{}
	for _, target := range []struct {
		arch string
		name string
	}{
		{arch: "arm64", name: "aarch64-unknown-linux-gnu"},
		{arch: "amd64", name: "x86_64-unknown-linux-gnu"},
	} {
		sha, err := ExtractCodexSHA(dockerfilePath, target.arch)
		if err != nil {
			return err
		}
		codexAssets[target.arch] = map[string]any{
			"arch":   target.name,
			"sha256": sha,
			"url":    "https://github.com/openai/codex/releases/download/rust-v" + codexVersion + "/codex-" + target.name + ".tar.gz",
		}
	}

	var packageJSON map[string]any
	if err := json.Unmarshal([]byte(packageJSONText), &packageJSON); err != nil {
		return err
	}
	var packageLock map[string]any
	if err := json.Unmarshal([]byte(packageLockText), &packageLock); err != nil {
		return err
	}

	dependencies := map[string]any{}
	rawDependencies, _ := packageJSON["dependencies"].(map[string]any)
	lockPackages, _ := packageLock["packages"].(map[string]any)
	for _, name := range sortedKeys(rawDependencies) {
		pkgEntry, ok := lockPackages["node_modules/"+name].(map[string]any)
		if !ok {
			return fmt.Errorf("missing pinned package entry for %s", name)
		}
		version, _ := pkgEntry["version"].(string)
		resolved, _ := pkgEntry["resolved"].(string)
		integrity, _ := pkgEntry["integrity"].(string)
		dependencies[name] = map[string]any{
			"version":   version,
			"resolved":  resolved,
			"integrity": integrity,
		}
	}

	adapterContextPaths, err := walkFiles(rootDir, "adapters", "node_modules", "target")
	if err != nil {
		return err
	}
	runtimeContainerContextPaths, err := walkFiles(rootDir, filepath.Join("runtime", "container"), "node_modules", "target")
	if err != nil {
		return err
	}
	runtimeContextPaths, err := gitTrackedSubset(rootDir, append(append([]string{".dockerignore"}, adapterContextPaths...), runtimeContainerContextPaths...), requireTracked)
	if err != nil {
		return err
	}
	runtimeContextInputs, err := digestMap(rootDir, runtimeContextPaths)
	if err != nil {
		return err
	}

	trackedFiles, tracked, err := gitTrackedFiles(rootDir)
	if err != nil {
		return err
	}
	excludedPrefixes := []string{
		"dist/",
		"tmp/",
		"runtime/container/providers/node_modules/",
		"runtime/container/rust/target/",
	}
	runtimeContextSet := map[string]struct{}{}
	for _, path := range runtimeContextPaths {
		runtimeContextSet[path] = struct{}{}
	}

	var verificationPaths []string
	if !tracked {
		verificationPaths, err = walkRepoFiles(rootDir)
		if err != nil {
			return err
		}
		verificationPaths = filterVerificationPaths(verificationPaths, runtimeContextSet, excludedPrefixes)
	} else {
		verificationPaths = filterVerificationPaths(trackedFiles, runtimeContextSet, excludedPrefixes)
	}
	verificationInputs, err := digestMap(rootDir, verificationPaths)
	if err != nil {
		return err
	}

	manifest := map[string]any{
		"schema_version": 1,
		"build": map[string]any{
			"ref":               buildRef,
			"source_date_epoch": sourceDateEpoch,
		},
		"runtime": map[string]any{
			"dockerfile_sha256": sha256HexString(dockerfile),
			"node_base_image":   nodeBaseImage,
			"debian_snapshot":   debianSnapshot,
			"claude": map[string]any{
				"version": claudeVersion,
				"assets":  claudeAssets,
			},
			"codex": map[string]any{
				"version": codexVersion,
				"assets":  codexAssets,
			},
			"providers": map[string]any{
				"package_json_sha256": sha256HexString(packageJSONText),
				"package_lock_sha256": sha256HexString(packageLockText),
				"dependencies":        dependencies,
			},
			"context_inputs": runtimeContextInputs,
		},
		"verification": map[string]any{
			"inputs": verificationInputs,
		},
	}
	return writeJSONFile(outputPath, manifest)
}

func sha256HexString(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func filterVerificationPaths(paths []string, runtimeContextSet map[string]struct{}, excludedPrefixes []string) []string {
	filtered := make([]string, 0, len(paths))
	for _, path := range paths {
		if _, ok := runtimeContextSet[path]; ok {
			continue
		}
		skip := false
		for _, prefix := range excludedPrefixes {
			if strings.HasPrefix(path, prefix) {
				skip = true
				break
			}
		}
		if !skip {
			filtered = append(filtered, path)
		}
	}
	return filtered
}

func sortedKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
