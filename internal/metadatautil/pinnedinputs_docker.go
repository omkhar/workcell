// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var aptInstallPattern = regexp.MustCompile(`apt-get install -y --no-install-recommends(?s:(.*?))&&`)

func validateDockerPinnedInputs(cfg PinnedInputsConfig, repoRoot, runtimeDockerfile, validatorDockerfile, goModText, codexRequirementsText, codexMCPConfigText string) error {
	goModPath := filepath.Join(repoRoot, "go.mod")

	runtimeBaseImage, err := requireArg(runtimeDockerfile, "NODE_BASE_IMAGE", cfg.RuntimeDockerfilePath)
	if err != nil {
		return err
	}
	validatorBaseImage, err := requireArg(validatorDockerfile, "VALIDATOR_BASE_IMAGE", cfg.ValidatorDockerfilePath)
	if err != nil {
		return err
	}
	runtimeSnapshot, err := requireArg(runtimeDockerfile, "DEBIAN_SNAPSHOT", cfg.RuntimeDockerfilePath)
	if err != nil {
		return err
	}
	validatorSnapshot, err := requireArg(validatorDockerfile, "DEBIAN_SNAPSHOT", cfg.ValidatorDockerfilePath)
	if err != nil {
		return err
	}
	codexVersion, err := requireArg(runtimeDockerfile, "CODEX_VERSION", cfg.RuntimeDockerfilePath)
	if err != nil {
		return err
	}
	claudeVersion, err := requireArg(runtimeDockerfile, "CLAUDE_VERSION", cfg.RuntimeDockerfilePath)
	if err != nil {
		return err
	}
	copilotVersion, err := requireArg(runtimeDockerfile, "COPILOT_VERSION", cfg.RuntimeDockerfilePath)
	if err != nil {
		return err
	}

	runtimeInstallBlocks, err := extractInstallBlocks(runtimeDockerfile, cfg.RuntimeDockerfilePath)
	if err != nil {
		return err
	}
	validatorInstallBlocks, err := extractInstallBlocks(validatorDockerfile, cfg.ValidatorDockerfilePath)
	if err != nil {
		return err
	}

	if err := requirePinnedBaseImage(runtimeBaseImage, "NODE_BASE_IMAGE", cfg.RuntimeDockerfilePath); err != nil {
		return err
	}
	if err := requirePinnedBaseImage(validatorBaseImage, "VALIDATOR_BASE_IMAGE", cfg.ValidatorDockerfilePath); err != nil {
		return err
	}
	if err := verifySnapshotFreshness(runtimeSnapshot, cfg.RuntimeDockerfilePath, cfg.MaxDebianSnapshotAgeDays); err != nil {
		return err
	}
	if err := verifySnapshotFreshness(validatorSnapshot, cfg.ValidatorDockerfilePath, cfg.MaxDebianSnapshotAgeDays); err != nil {
		return err
	}
	if err := requireNoRegistryBootstrapMCP(codexRequirementsText, cfg.CodexRequirementsPath); err != nil {
		return err
	}
	if err := requireNoRegistryBootstrapMCP(codexMCPConfigText, cfg.CodexMCPConfigPath); err != nil {
		return err
	}
	if err := CheckProviderBumpPolicy(cfg.ProviderBumpPolicyPath, cfg.RuntimeDockerfilePath, cfg.ProvidersPackageJSONPath); err != nil {
		return err
	}
	if _, _, err := requireRegex(runtimeDockerfile, `curl --ipv4 -fsSL "https://storage\.googleapis\.com/claude-code-dist-86c565f3-f756-42ad-8dfa-d59b1c096819/claude-code-releases/\$\{CLAUDE_VERSION\}/\$\{CLAUDE_PLATFORM\}/claude"`, "Claude native release download URL", cfg.RuntimeDockerfilePath); err != nil {
		return err
	}
	if _, _, err := requireRegex(runtimeDockerfile, `curl --ipv4 -fsSL "https://github\.com/github/copilot-cli/releases/download/v\$\{COPILOT_VERSION\}/copilot-\$\{COPILOT_PLATFORM\}\.tar\.gz"`, "Copilot native release download URL", cfg.RuntimeDockerfilePath); err != nil {
		return err
	}
	if _, _, err := requireRegex(runtimeDockerfile, `echo "\$\{COPILOT_SHA256\}  /tmp/copilot\.tar\.gz" \| sha256sum -c -`, "Copilot native archive checksum verification", cfg.RuntimeDockerfilePath); err != nil {
		return err
	}
	if _, _, err := requireRegex(runtimeDockerfile, `install -m 0755 /tmp/copilot /usr/local/libexec/workcell/real/copilot`, "executable Copilot runtime artifact install", cfg.RuntimeDockerfilePath); err != nil {
		return err
	}
	if _, match, err := requireRegex(runtimeDockerfile, `(?m)^\s*arm64\)\s+\\\s*CLAUDE_PLATFORM="([^"]+)";\s+\\\s*CLAUDE_SHA256="([0-9a-f]{64})";`, "arm64 Claude mapping", cfg.RuntimeDockerfilePath); err != nil {
		return err
	} else if match[1] != "linux-arm64" {
		return fmt.Errorf("arm64 Claude mapping in %s must use linux-arm64", cfg.RuntimeDockerfilePath)
	}
	if _, match, err := requireRegex(runtimeDockerfile, `(?m)^\s*amd64\)\s+\\\s*CLAUDE_PLATFORM="([^"]+)";\s+\\\s*CLAUDE_SHA256="([0-9a-f]{64})";`, "amd64 Claude mapping", cfg.RuntimeDockerfilePath); err != nil {
		return err
	} else if match[1] != "linux-x64" {
		return fmt.Errorf("amd64 Claude mapping in %s must use linux-x64", cfg.RuntimeDockerfilePath)
	}
	if !regexp.MustCompile(`^0\.[0-9]+\.[0-9]+(?:-[A-Za-z0-9.-]+)?$`).MatchString(codexVersion) {
		return fmt.Errorf("runtime/container/Dockerfile CODEX_VERSION must stay pinned to an explicit release, found %q", codexVersion)
	}
	if !regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[A-Za-z0-9.-]+)?$`).MatchString(claudeVersion) {
		return fmt.Errorf("runtime/container/Dockerfile CLAUDE_VERSION must stay pinned to an explicit release, found %q", claudeVersion)
	}
	if !regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(copilotVersion) {
		return fmt.Errorf("runtime/container/Dockerfile COPILOT_VERSION must stay pinned to a non-prerelease GitHub Copilot CLI release, found %q", copilotVersion)
	}
	if _, match, err := requireRegex(runtimeDockerfile, `(?m)^\s*arm64\)\s+\\(?:\s*CLAUDE_[A-Z0-9_]+="[^"]+";\s+\\)*\s*CODEX_ARCH="([^"]+)";\s+\\\s*CODEX_SHA256="([0-9a-f]{64})";`, "arm64 Codex mapping", cfg.RuntimeDockerfilePath); err != nil {
		return err
	} else if match[1] != "aarch64-unknown-linux-musl" {
		return fmt.Errorf("arm64 Codex mapping in %s must use aarch64-unknown-linux-musl", cfg.RuntimeDockerfilePath)
	}
	if _, match, err := requireRegex(runtimeDockerfile, `(?m)^\s*amd64\)\s+\\(?:\s*CLAUDE_[A-Z0-9_]+="[^"]+";\s+\\)*\s*CODEX_ARCH="([^"]+)";\s+\\\s*CODEX_SHA256="([0-9a-f]{64})";`, "amd64 Codex mapping", cfg.RuntimeDockerfilePath); err != nil {
		return err
	} else if match[1] != "x86_64-unknown-linux-musl" {
		return fmt.Errorf("amd64 Codex mapping in %s must use x86_64-unknown-linux-musl", cfg.RuntimeDockerfilePath)
	}
	if _, match, err := requireRegex(runtimeDockerfile, `(?m)^\s*arm64\)\s+\\\s*COPILOT_PLATFORM="([^"]+)";\s+\\\s*COPILOT_SHA256="([0-9a-f]{64})";`, "arm64 Copilot mapping", cfg.RuntimeDockerfilePath); err != nil {
		return err
	} else if match[1] != "linux-arm64" {
		return fmt.Errorf("arm64 Copilot mapping in %s must use linux-arm64", cfg.RuntimeDockerfilePath)
	}
	if _, match, err := requireRegex(runtimeDockerfile, `(?m)^\s*amd64\)\s+\\\s*COPILOT_PLATFORM="([^"]+)";\s+\\\s*COPILOT_SHA256="([0-9a-f]{64})";`, "amd64 Copilot mapping", cfg.RuntimeDockerfilePath); err != nil {
		return err
	} else if match[1] != "linux-x64" {
		return fmt.Errorf("amd64 Copilot mapping in %s must use linux-x64", cfg.RuntimeDockerfilePath)
	}
	if len(runtimeInstallBlocks) != 2 {
		return fmt.Errorf("runtime/container/Dockerfile must contain exactly two apt install blocks (runtime base and runtime builder)")
	}
	if len(validatorInstallBlocks) != 1 {
		return errors.New("tools/validator/Dockerfile must contain exactly one apt install block")
	}
	if err := requireExactPackages(runtimeInstallBlocks[0], []string{"bash", "bubblewrap", "ca-certificates", "curl", "fd-find", "git", "jq", "less", "openssh-client", "passwd", "procps", "ripgrep", "sudo", "unzip", "util-linux", "xz-utils"}, "Runtime base", cfg.RuntimeDockerfilePath); err != nil {
		return err
	}
	if err := requireExactPackages(runtimeInstallBlocks[1], []string{"gcc", "libc6-dev"}, "Runtime builder", cfg.RuntimeDockerfilePath); err != nil {
		return err
	}
	if err := requireExactPackages(validatorInstallBlocks[0], []string{"ca-certificates", "codespell", "curl", "gcc", "git", "groff-base", "jq", "libc6-dev", "llvm", "mandoc", "openssh-client", "procps", "shellcheck", "shfmt", "yamllint"}, "Validator", cfg.ValidatorDockerfilePath); err != nil {
		return err
	}
	goLanguageVersion, err := requireGoDirective(goModText, "go", goModPath)
	if err != nil {
		return err
	}
	goToolchainVersion, err := requireToolchainDirective(goModText, goModPath)
	if err != nil {
		return err
	}
	validatorGoVersion, err := requireArg(validatorDockerfile, "GO_VERSION", cfg.ValidatorDockerfilePath)
	if err != nil {
		return err
	}
	if err := requireEqual("Go toolchain version", goToolchainVersion, goModPath, validatorGoVersion, cfg.ValidatorDockerfilePath); err != nil {
		return err
	}
	expectedGoLanguageVersion, err := goLanguageVersionFromToolchain(goToolchainVersion, goModPath)
	if err != nil {
		return err
	}
	if goLanguageVersion != expectedGoLanguageVersion {
		return fmt.Errorf("go language version in %s must match the toolchain major/minor at patch zero, expected %q, found %q", goModPath, expectedGoLanguageVersion, goLanguageVersion)
	}
	validatorGoSHAx86_64, err := requireArg(validatorDockerfile, "GO_LINUX_X86_64_SHA256", cfg.ValidatorDockerfilePath)
	if err != nil {
		return err
	}
	if !isHexDigest(validatorGoSHAx86_64) {
		return fmt.Errorf("GO_LINUX_X86_64_SHA256 in %s must be a full SHA256 digest, found %q", cfg.ValidatorDockerfilePath, validatorGoSHAx86_64)
	}
	validatorGoSHAArm64, err := requireArg(validatorDockerfile, "GO_LINUX_ARM64_SHA256", cfg.ValidatorDockerfilePath)
	if err != nil {
		return err
	}
	if !isHexDigest(validatorGoSHAArm64) {
		return fmt.Errorf("GO_LINUX_ARM64_SHA256 in %s must be a full SHA256 digest, found %q", cfg.ValidatorDockerfilePath, validatorGoSHAArm64)
	}
	validatorHadolintVersion, err := requireArg(validatorDockerfile, "HADOLINT_VERSION", cfg.ValidatorDockerfilePath)
	if err != nil {
		return err
	}
	if !pinnedReleaseTagPattern.MatchString(validatorHadolintVersion) {
		return fmt.Errorf("HADOLINT_VERSION must be an exact pinned release, found %q", validatorHadolintVersion)
	}
	validatorHadolintSHAx86_64, err := requireArg(validatorDockerfile, "HADOLINT_LINUX_X86_64_SHA256", cfg.ValidatorDockerfilePath)
	if err != nil {
		return err
	}
	if !isHexDigest(validatorHadolintSHAx86_64) {
		return fmt.Errorf("HADOLINT_LINUX_X86_64_SHA256 in %s must be a full SHA256 digest, found %q", cfg.ValidatorDockerfilePath, validatorHadolintSHAx86_64)
	}
	validatorHadolintSHAArm64, err := requireArg(validatorDockerfile, "HADOLINT_LINUX_ARM64_SHA256", cfg.ValidatorDockerfilePath)
	if err != nil {
		return err
	}
	if !isHexDigest(validatorHadolintSHAArm64) {
		return fmt.Errorf("HADOLINT_LINUX_ARM64_SHA256 in %s must be a full SHA256 digest, found %q", cfg.ValidatorDockerfilePath, validatorHadolintSHAArm64)
	}
	validatorDeadcodeVersion, err := requireArg(validatorDockerfile, "DEADCODE_VERSION", cfg.ValidatorDockerfilePath)
	if err != nil {
		return err
	}
	if !pinnedReleaseTagPattern.MatchString(validatorDeadcodeVersion) {
		return fmt.Errorf("DEADCODE_VERSION must be an exact pinned release, found %q", validatorDeadcodeVersion)
	}
	return nil
}

func verifySnapshotFreshness(snapshot, path string, maxAgeDays int) error {
	ts, err := time.Parse("20060102T150405Z", snapshot)
	if err != nil {
		return fmt.Errorf("debian snapshot %s in %s is not valid", snapshot, path)
	}
	now := time.Now().UTC()
	ageDays := int(now.Sub(ts).Hours() / 24)
	if ageDays > maxAgeDays {
		return fmt.Errorf(
			"debian snapshot %s in %s is %d days old; refresh it or raise WORKCELL_MAX_DEBIAN_SNAPSHOT_AGE_DAYS",
			snapshot,
			path,
			ageDays,
		)
	}
	return nil
}

func extractInstallBlocks(text, path string) ([][]string, error) {
	matches := aptInstallPattern.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return nil, fmt.Errorf("unable to find apt install blocks in %s", path)
	}
	blocks := make([][]string, 0, len(matches))
	for _, match := range matches {
		body := strings.ReplaceAll(match[1], "\\", " ")
		fields := strings.Fields(body)
		if len(fields) == 0 {
			return nil, fmt.Errorf("unable to extract package list from install block in %s", path)
		}
		blocks = append(blocks, fields)
	}
	return blocks, nil
}

func requireExactPackages(actual, expected []string, label, path string) error {
	if len(actual) != len(expected) {
		return fmt.Errorf("%s package set in %s changed.\nexpected: %v\nactual:   %v", label, path, expected, actual)
	}
	for i := range actual {
		if actual[i] != expected[i] {
			return fmt.Errorf("%s package set in %s changed.\nexpected: %v\nactual:   %v", label, path, expected, actual)
		}
	}
	return nil
}

func requireGoDirective(text, directive, path string) (string, error) {
	match := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(directive) + ` ([0-9]+\.[0-9]+\.[0-9]+)$`).FindStringSubmatch(text)
	if match == nil {
		return "", fmt.Errorf("unable to extract %s from %s", directive, path)
	}
	return match[1], nil
}

func requireToolchainDirective(text, path string) (string, error) {
	match := regexp.MustCompile(`(?m)^toolchain go([0-9]+\.[0-9]+\.[0-9]+)$`).FindStringSubmatch(text)
	if match == nil {
		return "", fmt.Errorf("unable to extract toolchain from %s", path)
	}
	return match[1], nil
}

func goLanguageVersionFromToolchain(version, path string) (string, error) {
	match := regexp.MustCompile(`^([0-9]+\.[0-9]+)\.[0-9]+$`).FindStringSubmatch(version)
	if match == nil {
		return "", fmt.Errorf("expected a semantic Go toolchain version in %s, found %q", path, version)
	}
	return match[1] + ".0", nil
}
