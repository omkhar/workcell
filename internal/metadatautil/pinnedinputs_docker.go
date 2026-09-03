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

func validateDockerPinnedInputs(cfg PinnedInputsConfig, repoRoot, runtimeDockerfile, validatorDockerfile string, debianBootstrapManifest DebianBootstrapManifest, debianBootstrapManifestPath, goModText, codexRequirementsText, codexMCPConfigText string) error {
	validator := dockerPinnedInputValidator{
		cfg:                         cfg,
		runtimeDockerfile:           runtimeDockerfile,
		validatorDockerfile:         validatorDockerfile,
		debianBootstrapManifest:     debianBootstrapManifest,
		debianBootstrapManifestPath: debianBootstrapManifestPath,
		goModText:                   goModText,
		goModPath:                   filepath.Join(repoRoot, "go.mod"),
		codexRequirementsText:       codexRequirementsText,
		codexMCPConfigText:          codexMCPConfigText,
	}
	if err := validator.load(); err != nil {
		return err
	}
	if err := validator.validateBaseAndBootstrapInputs(); err != nil {
		return err
	}
	if err := validator.validateRuntimeProviderInputs(); err != nil {
		return err
	}
	if err := validator.validateDockerPackageSets(); err != nil {
		return err
	}
	return validator.validateValidatorToolInputs()
}

type dockerPinnedInputValidator struct {
	cfg                         PinnedInputsConfig
	runtimeDockerfile           string
	validatorDockerfile         string
	debianBootstrapManifest     DebianBootstrapManifest
	debianBootstrapManifestPath string
	goModText                   string
	goModPath                   string
	codexRequirementsText       string
	codexMCPConfigText          string
	runtimeBaseImage            string
	validatorBaseImage          string
	codexVersion                string
	claudeVersion               string
	copilotVersion              string
	runtimeInstallBlocks        [][]string
	validatorInstallBlocks      [][]string
	goLanguageVersion           string
	goToolchainVersion          string
	validatorGoVersion          string
}

func (validator *dockerPinnedInputValidator) load() error {
	if err := validator.loadBaseImages(); err != nil {
		return err
	}
	if err := validator.loadProviderVersions(); err != nil {
		return err
	}
	return validator.loadInstallBlocks()
}

func (validator *dockerPinnedInputValidator) loadBaseImages() error {
	var err error
	validator.runtimeBaseImage, err = requireArg(validator.runtimeDockerfile, "NODE_BASE_IMAGE", validator.cfg.RuntimeDockerfilePath)
	if err != nil {
		return err
	}
	validator.validatorBaseImage, err = requireArg(validator.validatorDockerfile, "VALIDATOR_BASE_IMAGE", validator.cfg.ValidatorDockerfilePath)
	return err
}

func (validator *dockerPinnedInputValidator) loadProviderVersions() error {
	var err error
	validator.codexVersion, err = requireArg(validator.runtimeDockerfile, "CODEX_VERSION", validator.cfg.RuntimeDockerfilePath)
	if err != nil {
		return err
	}
	validator.claudeVersion, err = requireArg(validator.runtimeDockerfile, "CLAUDE_VERSION", validator.cfg.RuntimeDockerfilePath)
	if err != nil {
		return err
	}
	validator.copilotVersion, err = requireArg(validator.runtimeDockerfile, "COPILOT_VERSION", validator.cfg.RuntimeDockerfilePath)
	return err
}

func (validator *dockerPinnedInputValidator) loadInstallBlocks() error {
	var err error
	validator.runtimeInstallBlocks, err = extractInstallBlocks(validator.runtimeDockerfile, validator.cfg.RuntimeDockerfilePath)
	if err != nil {
		return err
	}
	validator.validatorInstallBlocks, err = extractInstallBlocks(validator.validatorDockerfile, validator.cfg.ValidatorDockerfilePath)
	return err
}

func (validator *dockerPinnedInputValidator) validateBaseAndBootstrapInputs() error {
	if err := validator.validateBaseImages(); err != nil {
		return err
	}
	if err := validator.validateBootstrapAndMCPInputs(); err != nil {
		return err
	}
	return CheckProviderBumpPolicy(validator.cfg.ProviderBumpPolicyPath, validator.cfg.RuntimeDockerfilePath, validator.cfg.ProvidersPackageJSONPath)
}

func (validator *dockerPinnedInputValidator) validateBaseImages() error {
	if err := requirePinnedBaseImage(validator.runtimeBaseImage, "NODE_BASE_IMAGE", validator.cfg.RuntimeDockerfilePath); err != nil {
		return err
	}
	return requirePinnedBaseImage(validator.validatorBaseImage, "VALIDATOR_BASE_IMAGE", validator.cfg.ValidatorDockerfilePath)
}

func (validator *dockerPinnedInputValidator) validateBootstrapAndMCPInputs() error {
	if err := verifySnapshotFreshness(validator.debianBootstrapManifest.Snapshot, validator.debianBootstrapManifestPath, validator.cfg.MaxDebianSnapshotAgeDays); err != nil {
		return err
	}
	if err := validateDebianBootstrapDockerfilePins(validator.runtimeDockerfile, validator.cfg.RuntimeDockerfilePath, validator.validatorDockerfile, validator.cfg.ValidatorDockerfilePath); err != nil {
		return err
	}
	if err := requireNoRegistryBootstrapMCP(validator.codexRequirementsText, validator.cfg.CodexRequirementsPath); err != nil {
		return err
	}
	return requireNoRegistryBootstrapMCP(validator.codexMCPConfigText, validator.cfg.CodexMCPConfigPath)
}

func (validator *dockerPinnedInputValidator) validateRuntimeProviderInputs() error {
	if err := validator.validateProviderInstallCommands(); err != nil {
		return err
	}
	if err := validator.validateClaudeArchitectureMappings(); err != nil {
		return err
	}
	if err := validator.validateProviderVersions(); err != nil {
		return err
	}
	if err := validator.validateCodexArchitectureMappings(); err != nil {
		return err
	}
	return validator.validateCopilotArchitectureMappings()
}

type dockerRegexRequirement struct {
	pattern string
	label   string
}

func (validator *dockerPinnedInputValidator) validateProviderInstallCommands() error {
	requirements := []dockerRegexRequirement{
		{`curl --ipv4 -fsSL "https://storage\.googleapis\.com/claude-code-dist-86c565f3-f756-42ad-8dfa-d59b1c096819/claude-code-releases/\$\{CLAUDE_VERSION\}/\$\{CLAUDE_PLATFORM\}/claude"`, "Claude native release download URL"},
		{`curl --ipv4 -fsSL "https://github\.com/github/copilot-cli/releases/download/v\$\{COPILOT_VERSION\}/copilot-\$\{COPILOT_PLATFORM\}\.tar\.gz"`, "Copilot native release download URL"},
		{`echo "\$\{COPILOT_SHA256\}  /tmp/copilot\.tar\.gz" \| sha256sum -c -`, "Copilot native archive checksum verification"},
		{`install -m 0755 /tmp/copilot /usr/local/libexec/workcell/real/copilot`, "executable Copilot runtime artifact install"},
	}
	for _, requirement := range requirements {
		if _, _, err := requireRegex(validator.runtimeDockerfile, requirement.pattern, requirement.label, validator.cfg.RuntimeDockerfilePath); err != nil {
			return err
		}
	}
	return nil
}

type dockerArchitectureMapping struct {
	pattern string
	label   string
	want    string
}

func validateDockerArchitectureMappings(text, path string, mappings []dockerArchitectureMapping) error {
	for _, mapping := range mappings {
		_, match, err := requireRegex(text, mapping.pattern, mapping.label, path)
		if err != nil {
			return err
		}
		if match[1] != mapping.want {
			return fmt.Errorf("%s in %s must use %s", mapping.label, path, mapping.want)
		}
	}
	return nil
}

func (validator *dockerPinnedInputValidator) validateClaudeArchitectureMappings() error {
	return validateDockerArchitectureMappings(validator.runtimeDockerfile, validator.cfg.RuntimeDockerfilePath, []dockerArchitectureMapping{
		{`(?m)^\s*arm64\)\s+\\\s*CLAUDE_PLATFORM="([^"]+)";\s+\\\s*CLAUDE_SHA256="([0-9a-f]{64})";`, "arm64 Claude mapping", "linux-arm64"},
		{`(?m)^\s*amd64\)\s+\\\s*CLAUDE_PLATFORM="([^"]+)";\s+\\\s*CLAUDE_SHA256="([0-9a-f]{64})";`, "amd64 Claude mapping", "linux-x64"},
	})
}

type providerVersionRequirement struct {
	version     string
	pattern     *regexp.Regexp
	errorPrefix string
}

func (validator *dockerPinnedInputValidator) validateProviderVersions() error {
	requirements := []providerVersionRequirement{
		{validator.codexVersion, regexp.MustCompile(`^0\.[0-9]+\.[0-9]+(?:-[A-Za-z0-9.-]+)?$`), "runtime/container/Dockerfile CODEX_VERSION must stay pinned to an explicit release"},
		{validator.claudeVersion, regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[A-Za-z0-9.-]+)?$`), "runtime/container/Dockerfile CLAUDE_VERSION must stay pinned to an explicit release"},
		{validator.copilotVersion, regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`), "runtime/container/Dockerfile COPILOT_VERSION must stay pinned to a non-prerelease GitHub Copilot CLI release"},
	}
	for _, requirement := range requirements {
		if !requirement.pattern.MatchString(requirement.version) {
			return fmt.Errorf("%s, found %q", requirement.errorPrefix, requirement.version)
		}
	}
	return nil
}

func (validator *dockerPinnedInputValidator) validateCodexArchitectureMappings() error {
	return validateDockerArchitectureMappings(validator.runtimeDockerfile, validator.cfg.RuntimeDockerfilePath, []dockerArchitectureMapping{
		{`(?m)^\s*arm64\)\s+\\(?:\s*CLAUDE_[A-Z0-9_]+="[^"]+";\s+\\)*\s*CODEX_ARCH="([^"]+)";\s+\\\s*CODEX_SHA256="([0-9a-f]{64})";`, "arm64 Codex mapping", "aarch64-unknown-linux-musl"},
		{`(?m)^\s*amd64\)\s+\\(?:\s*CLAUDE_[A-Z0-9_]+="[^"]+";\s+\\)*\s*CODEX_ARCH="([^"]+)";\s+\\\s*CODEX_SHA256="([0-9a-f]{64})";`, "amd64 Codex mapping", "x86_64-unknown-linux-musl"},
	})
}

func (validator *dockerPinnedInputValidator) validateCopilotArchitectureMappings() error {
	return validateDockerArchitectureMappings(validator.runtimeDockerfile, validator.cfg.RuntimeDockerfilePath, []dockerArchitectureMapping{
		{`(?m)^\s*arm64\)\s+\\\s*COPILOT_PLATFORM="([^"]+)";\s+\\\s*COPILOT_SHA256="([0-9a-f]{64})";`, "arm64 Copilot mapping", "linux-arm64"},
		{`(?m)^\s*amd64\)\s+\\\s*COPILOT_PLATFORM="([^"]+)";\s+\\\s*COPILOT_SHA256="([0-9a-f]{64})";`, "amd64 Copilot mapping", "linux-x64"},
	})
}

func (validator *dockerPinnedInputValidator) validateDockerPackageSets() error {
	if err := validator.validateInstallBlockShape(); err != nil {
		return err
	}
	return validator.validateExactDockerPackages()
}

func (validator *dockerPinnedInputValidator) validateInstallBlockShape() error {
	if len(validator.runtimeInstallBlocks) != 2 {
		return errors.New("runtime/container/Dockerfile must contain exactly two apt install blocks (runtime base and runtime builder)")
	}
	if len(validator.validatorInstallBlocks) != 1 {
		return errors.New("tools/validator/Dockerfile must contain exactly one apt install block")
	}
	return nil
}

type dockerPackageRequirement struct {
	actual   []string
	expected []string
	label    string
	path     string
}

func (validator *dockerPinnedInputValidator) validateExactDockerPackages() error {
	requirements := []dockerPackageRequirement{
		{validator.runtimeInstallBlocks[0], []string{"bash", "bubblewrap", "ca-certificates", "curl", "fd-find", "git", "jq", "less", "openssh-client", "passwd", "procps", "ripgrep", "sudo", "unzip", "util-linux", "xz-utils"}, "Runtime base", validator.cfg.RuntimeDockerfilePath},
		{validator.runtimeInstallBlocks[1], []string{"gcc", "libc6-dev"}, "Runtime builder", validator.cfg.RuntimeDockerfilePath},
		{validator.validatorInstallBlocks[0], []string{"acl", "ca-certificates", "codespell", "curl", "gcc", "git", "groff-base", "jq", "libc6-dev", "llvm", "mandoc", "openssh-client", "procps", "shellcheck", "shfmt", "yamllint"}, "Validator", validator.cfg.ValidatorDockerfilePath},
	}
	for _, requirement := range requirements {
		if err := requireExactPackages(requirement.actual, requirement.expected, requirement.label, requirement.path); err != nil {
			return err
		}
	}
	return nil
}

func (validator *dockerPinnedInputValidator) validateValidatorToolInputs() error {
	if err := validator.validateValidatorGoVersion(); err != nil {
		return err
	}
	if err := validator.validateValidatorGoDigests(); err != nil {
		return err
	}
	if err := validator.validateValidatorHadolint(); err != nil {
		return err
	}
	return validator.validateValidatorDeadcode()
}

func (validator *dockerPinnedInputValidator) validateValidatorGoVersion() error {
	if err := validator.loadValidatorGoVersions(); err != nil {
		return err
	}
	return validator.validateValidatorGoVersionParity()
}

func (validator *dockerPinnedInputValidator) loadValidatorGoVersions() error {
	var err error
	validator.goLanguageVersion, err = requireGoDirective(validator.goModText, "go", validator.goModPath)
	if err != nil {
		return err
	}
	validator.goToolchainVersion, err = requireToolchainDirective(validator.goModText, validator.goModPath)
	if err != nil {
		return err
	}
	validator.validatorGoVersion, err = requireArg(validator.validatorDockerfile, "GO_VERSION", validator.cfg.ValidatorDockerfilePath)
	return err
}

func (validator *dockerPinnedInputValidator) validateValidatorGoVersionParity() error {
	if err := requireEqual("Go toolchain version", validator.goToolchainVersion, validator.goModPath, validator.validatorGoVersion, validator.cfg.ValidatorDockerfilePath); err != nil {
		return err
	}
	expected, err := goLanguageVersionFromToolchain(validator.goToolchainVersion, validator.goModPath)
	if err != nil {
		return err
	}
	if validator.goLanguageVersion != expected {
		return fmt.Errorf("go language version in %s must match the toolchain major/minor at patch zero, expected %q, found %q", validator.goModPath, expected, validator.goLanguageVersion)
	}
	return nil
}

func (validator *dockerPinnedInputValidator) validateValidatorGoDigests() error {
	return requireDockerSHAArgs(validator.validatorDockerfile, validator.cfg.ValidatorDockerfilePath,
		"GO_LINUX_X86_64_SHA256", "GO_LINUX_ARM64_SHA256")
}

func requireDockerSHAArgs(dockerfile, path string, names ...string) error {
	for _, name := range names {
		value, err := requireArg(dockerfile, name, path)
		if err != nil {
			return err
		}
		if !isHexDigest(value) {
			return fmt.Errorf("%s in %s must be a full SHA256 digest, found %q", name, path, value)
		}
	}
	return nil
}

func (validator *dockerPinnedInputValidator) validateValidatorHadolint() error {
	version, err := requireArg(validator.validatorDockerfile, "HADOLINT_VERSION", validator.cfg.ValidatorDockerfilePath)
	if err != nil {
		return err
	}
	if !pinnedReleaseTagPattern.MatchString(version) {
		return fmt.Errorf("HADOLINT_VERSION must be an exact pinned release, found %q", version)
	}
	return requireDockerSHAArgs(validator.validatorDockerfile, validator.cfg.ValidatorDockerfilePath,
		"HADOLINT_LINUX_X86_64_SHA256", "HADOLINT_LINUX_ARM64_SHA256")
}

func (validator *dockerPinnedInputValidator) validateValidatorDeadcode() error {
	version, err := requireArg(validator.validatorDockerfile, "DEADCODE_VERSION", validator.cfg.ValidatorDockerfilePath)
	if err != nil {
		return err
	}
	if !pinnedReleaseTagPattern.MatchString(version) {
		return fmt.Errorf("DEADCODE_VERSION must be an exact pinned release, found %q", version)
	}
	return nil
}

func validateDebianBootstrapDockerfilePins(runtimeDockerfile, runtimePath, validatorDockerfile, validatorPath string) error {
	requiredUses := []string{
		`COPY --chmod=0444 runtime/container/debian-bootstrap.env /usr/local/share/workcell/debian-bootstrap.env`,
		`mapfile -t debian_bootstrap_pins < /usr/local/share/workcell/debian-bootstrap.env`,
		`[[ "${#debian_bootstrap_pins[@]}" -eq 7 ]]`,
		`[[ "${debian_bootstrap_pins[0]}" =~ ^DEBIAN_SNAPSHOT=[0-9]{8}T[0-9]{6}Z$ ]]`,
		`[[ "${debian_bootstrap_pins[1]}" =~ ^DEBIAN_OPENSSL_AMD64_PATH=pool/main/o/openssl/openssl_[A-Za-z0-9.+~_-]+_amd64\.deb$ ]]`,
		`[[ "${debian_bootstrap_pins[2]}" =~ ^DEBIAN_OPENSSL_AMD64_SHA256=[0-9a-f]{64}$ ]]`,
		`[[ "${debian_bootstrap_pins[3]}" =~ ^DEBIAN_OPENSSL_ARM64_PATH=pool/main/o/openssl/openssl_[A-Za-z0-9.+~_-]+_arm64\.deb$ ]]`,
		`[[ "${debian_bootstrap_pins[4]}" =~ ^DEBIAN_OPENSSL_ARM64_SHA256=[0-9a-f]{64}$ ]]`,
		`[[ "${debian_bootstrap_pins[5]}" =~ ^DEBIAN_CA_CERTIFICATES_PATH=pool/main/c/ca-certificates/ca-certificates_[A-Za-z0-9.+~_-]+_all\.deb$ ]]`,
		`[[ "${debian_bootstrap_pins[6]}" =~ ^DEBIAN_CA_CERTIFICATES_SHA256=[0-9a-f]{64}$ ]]`,
		`DEBIAN_SNAPSHOT="${debian_bootstrap_pins[0]#*=}"`,
		`DEBIAN_OPENSSL_AMD64_PATH="${debian_bootstrap_pins[1]#*=}"`,
		`DEBIAN_OPENSSL_AMD64_SHA256="${debian_bootstrap_pins[2]#*=}"`,
		`DEBIAN_OPENSSL_ARM64_PATH="${debian_bootstrap_pins[3]#*=}"`,
		`DEBIAN_OPENSSL_ARM64_SHA256="${debian_bootstrap_pins[4]#*=}"`,
		`DEBIAN_CA_CERTIFICATES_PATH="${debian_bootstrap_pins[5]#*=}"`,
		`DEBIAN_CA_CERTIFICATES_SHA256="${debian_bootstrap_pins[6]#*=}"`,
		`[[ "${DEBIAN_OPENSSL_AMD64_PATH%_amd64.deb}" == "${DEBIAN_OPENSSL_ARM64_PATH%_arm64.deb}" ]]`,
		`openssl_path="${DEBIAN_OPENSSL_AMD64_PATH}"`,
		`openssl_sha256="${DEBIAN_OPENSSL_AMD64_SHA256}"`,
		`openssl_path="${DEBIAN_OPENSSL_ARM64_PATH}"`,
		`openssl_sha256="${DEBIAN_OPENSSL_ARM64_SHA256}"`,
		`openssl_url="archive/debian/${DEBIAN_SNAPSHOT}/${openssl_path}"`,
		`ca_url="archive/debian/${DEBIAN_SNAPSHOT}/${DEBIAN_CA_CERTIFICATES_PATH}"`,
		`ca_sha256="${DEBIAN_CA_CERTIFICATES_SHA256}"`,
		`echo "${openssl_sha256}  /tmp/workcell-bootstrap-openssl.deb" | sha256sum -c -`,
		`echo "${ca_sha256}  /tmp/workcell-bootstrap-ca-certificates.deb" | sha256sum -c -`,
		`dpkg -i /tmp/workcell-bootstrap-openssl.deb /tmp/workcell-bootstrap-ca-certificates.deb`,
	}
	for _, dockerfile := range []struct {
		content string
		path    string
	}{{runtimeDockerfile, runtimePath}, {validatorDockerfile, validatorPath}} {
		for _, required := range requiredUses {
			if !regexp.MustCompile(`(?m)^[\t ]*(?:RUN[\t ]+|&&[\t ]+)?` + regexp.QuoteMeta(required) + `(?:[\t ;\\]|$)`).MatchString(dockerfile.content) {
				return fmt.Errorf("%s must use reviewed Debian bootstrap pin %s", dockerfile.path, required)
			}
		}
		if regexp.MustCompile(`(?m)(^|&&\s+)(RUN\s+)?(source|\.)\s+[^\n]*debian-bootstrap\.env`).MatchString(dockerfile.content) {
			return fmt.Errorf("%s must not evaluate the Debian bootstrap manifest as shell code", dockerfile.path)
		}
	}
	return nil
}

func verifySnapshotFreshness(snapshot, path string, maxAgeDays int) error {
	if maxAgeDays < 0 || maxAgeDays > 60 {
		return fmt.Errorf("maximum Debian snapshot age must be between 0 and 60 days, found %d", maxAgeDays)
	}
	ts, err := time.Parse("20060102T150405Z", snapshot)
	if err != nil {
		return fmt.Errorf("debian snapshot %s in %s is not valid", snapshot, path)
	}
	now := time.Now().UTC()
	if ts.After(now.Add(5 * time.Minute)) {
		return fmt.Errorf("debian snapshot %s in %s is in the future", snapshot, path)
	}
	ageDays := int(now.Sub(ts).Hours() / 24)
	if ageDays > maxAgeDays {
		return fmt.Errorf(
			"debian snapshot %s in %s is %d days old; refresh it (the maximum accepted age is 60 days)",
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
