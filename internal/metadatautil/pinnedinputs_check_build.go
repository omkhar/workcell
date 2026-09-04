// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

type workflowPinSpec struct {
	name, expected, expectedPath string
	require                      func(string, string) error
}

func (check *pinnedInputsCheck) validateFoundationalPins() error {
	if err := validateDockerPinnedInputs(
		check.cfg, check.repoRoot, check.runtimeDockerfile, check.validatorDockerfile,
		check.debianBootstrapManifest, check.paths.debianBootstrap, check.goModText,
		check.codexRequirementsText, check.codexMCPConfigText,
	); err != nil {
		return err
	}
	return validateNodeMarkdownlintPinnedInputs(
		check.cfg, check.validatorDockerfile, check.installDevToolsScript,
		check.markdownlintPackageJSON, check.paths.markdownlintJSON,
		check.markdownlintPackageLock, check.paths.markdownlintLock,
		check.paths.installDevTools, check.validateRepoScript, check.paths.validateRepo,
	)
}

func (check *pinnedInputsCheck) loadRustManifestPins() error {
	edition, err := requireTOMLString(check.cargoManifestText, "edition", check.paths.cargoManifest)
	if err != nil {
		return err
	}
	if edition != "2024" {
		return fmt.Errorf("%s must use edition 2024, found %q", check.paths.cargoManifest, edition)
	}
	values := &check.rustPins
	return loadPinnedStrings(requireTOMLString, []pinnedStringInput{
		{&values.cargoVersion, check.cargoManifestText, "rust-version", check.paths.cargoManifest},
		{&values.toolchainVersion, check.rustToolchainText, "channel", check.paths.rustToolchain},
	})
}

func (check *pinnedInputsCheck) loadAndValidateRustImagePins() error {
	values := &check.rustPins
	if err := loadPinnedStrings(requireArg, []pinnedStringInput{
		{&values.runtimeVersion, check.runtimeDockerfile, "RUST_VERSION", check.cfg.RuntimeDockerfilePath},
		{&values.runtimeImage, check.runtimeDockerfile, "RUST_TOOLCHAIN_IMAGE", check.cfg.RuntimeDockerfilePath},
		{&values.validatorVersion, check.validatorDockerfile, "RUST_VERSION", check.cfg.ValidatorDockerfilePath},
	}); err != nil {
		return err
	}
	expected := fmt.Sprintf("rust:%s-slim-trixie@", values.runtimeVersion)
	return firstPinnedInputError(
		func() error {
			return requireEqual("RUST_VERSION", values.runtimeVersion, check.cfg.RuntimeDockerfilePath, values.validatorVersion, check.cfg.ValidatorDockerfilePath)
		},
		func() error {
			return requireEqual("Rust toolchain channel", values.toolchainVersion, check.paths.rustToolchain, values.runtimeVersion, check.cfg.RuntimeDockerfilePath)
		},
		func() error {
			return requirePinnedBaseImage(values.runtimeImage, "RUST_TOOLCHAIN_IMAGE", check.cfg.RuntimeDockerfilePath)
		},
		func() error {
			return requireTextRequirements(textRequirement{
				text: values.runtimeImage, needle: expected,
				err: fmt.Errorf("RUST_TOOLCHAIN_IMAGE in %s must pin the official rust:%s-slim-trixie image, found %q", check.cfg.RuntimeDockerfilePath, values.runtimeVersion, values.runtimeImage),
			})
		},
	)
}

func (check *pinnedInputsCheck) validateCargoRustVersion() error {
	values := check.rustPins
	match := regexp.MustCompile(`^([0-9]+\.[0-9]+)\.[0-9]+$`).FindStringSubmatch(values.toolchainVersion)
	if match == nil {
		return fmt.Errorf("expected a semantic version in %s, found %q", check.paths.rustToolchain, values.toolchainVersion)
	}
	expected := match[1]
	if values.cargoVersion != expected {
		return fmt.Errorf("rust-version in %s must match the pinned toolchain major/minor, expected %q, found %q", check.paths.cargoManifest, expected, values.cargoVersion)
	}
	return nil
}

func (check *pinnedInputsCheck) validateRustupVersion() error {
	value, err := requireArg(check.validatorDockerfile, "RUSTUP_VERSION", check.cfg.ValidatorDockerfilePath)
	if err != nil {
		return err
	}
	if !regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[A-Za-z0-9.-]+)?$`).MatchString(value) {
		return fmt.Errorf("RUSTUP_VERSION must be an exact pinned release, found %q", value)
	}
	return nil
}

func (check *pinnedInputsCheck) validateRustupDigests() error {
	x86Digest, err := requireArg(check.validatorDockerfile, "RUSTUP_INIT_LINUX_X86_64_SHA256", check.cfg.ValidatorDockerfilePath)
	if err != nil {
		return err
	}
	if !isHexDigest(x86Digest) {
		return fmt.Errorf("RUSTUP_INIT_LINUX_X86_64_SHA256 in %s must be a full SHA256 digest, found %q", check.cfg.ValidatorDockerfilePath, x86Digest)
	}
	armDigest, err := requireArg(check.validatorDockerfile, "RUSTUP_INIT_LINUX_ARM64_SHA256", check.cfg.ValidatorDockerfilePath)
	if err != nil {
		return err
	}
	if !isHexDigest(armDigest) {
		return fmt.Errorf("RUSTUP_INIT_LINUX_ARM64_SHA256 in %s must be a full SHA256 digest, found %q", check.cfg.ValidatorDockerfilePath, armDigest)
	}
	return nil
}

func (check *pinnedInputsCheck) validateProviderLock() error {
	return validateNodeProviderLock(check.providersPackageJSON, check.providersPackageLock)
}

func (check *pinnedInputsCheck) validateBuildxPins() error {
	var ciValue, releaseValue string
	if err := loadPinnedStrings(requireYAMLKey, []pinnedStringInput{
		{&ciValue, check.ciWorkflow, "WORKCELL_BUILDX_VERSION", ".github/workflows/ci.yml"},
		{&releaseValue, check.releaseWorkflow, "WORKCELL_BUILDX_VERSION", ".github/workflows/release.yml"},
	}); err != nil {
		return err
	}
	if ciValue != releaseValue {
		return errors.New("WORKCELL_BUILDX_VERSION must match between .github/workflows/ci.yml and .github/workflows/release.yml")
	}
	if !pinnedReleaseTagPattern.MatchString(ciValue) {
		return fmt.Errorf("WORKCELL_BUILDX_VERSION must be an exact pinned release (for example v0.32.1), found %q", ciValue)
	}
	check.toolPins.buildx = ciValue
	return nil
}

func (check *pinnedInputsCheck) validateQEMUPins() error {
	var ciValue, releaseValue string
	if err := loadPinnedStrings(requireYAMLKey, []pinnedStringInput{
		{&ciValue, check.ciWorkflow, "WORKCELL_QEMU_IMAGE", ".github/workflows/ci.yml"},
		{&releaseValue, check.releaseWorkflow, "WORKCELL_QEMU_IMAGE", ".github/workflows/release.yml"},
	}); err != nil {
		return err
	}
	if ciValue != releaseValue {
		return errors.New("WORKCELL_QEMU_IMAGE must match between .github/workflows/ci.yml and .github/workflows/release.yml")
	}
	if err := requirePinnedBaseImage(ciValue, "WORKCELL_QEMU_IMAGE", ".github/workflows/ci.yml"); err != nil {
		return err
	}
	check.toolPins.qemu = ciValue
	return nil
}

func (check *pinnedInputsCheck) validateBuildkitPins() error {
	var ciValue, releaseValue string
	if err := loadPinnedStrings(requireYAMLKey, []pinnedStringInput{
		{&ciValue, check.ciWorkflow, "WORKCELL_BUILDKIT_IMAGE", ".github/workflows/ci.yml"},
		{&releaseValue, check.releaseWorkflow, "WORKCELL_BUILDKIT_IMAGE", ".github/workflows/release.yml"},
	}); err != nil {
		return err
	}
	if ciValue != releaseValue {
		return errors.New("WORKCELL_BUILDKIT_IMAGE must match between .github/workflows/ci.yml and .github/workflows/release.yml")
	}
	if err := requirePinnedBaseImage(ciValue, "WORKCELL_BUILDKIT_IMAGE", ".github/workflows/ci.yml"); err != nil {
		return err
	}
	check.toolPins.buildkit = ciValue
	return nil
}

func (check *pinnedInputsCheck) validateWorkflowBuilderPins() error {
	specs := []workflowPinSpec{
		{
			name: "WORKCELL_BUILDX_VERSION", expected: check.toolPins.buildx, expectedPath: ".github/workflows/ci.yml",
			require: func(string, string) error { return nil },
		},
		{
			name: "WORKCELL_BUILDKIT_IMAGE", expected: check.toolPins.buildkit, expectedPath: ".github/workflows/ci.yml",
			require: func(text, path string) error {
				return requireTextRequirements(textRequirement{
					text: text, needle: "driver-opts: image=${{ env.WORKCELL_BUILDKIT_IMAGE }}",
					err: fmt.Errorf("%s must pin the BuildKit daemon image used by setup-buildx-action", path),
				})
			},
		},
	}
	for _, workflowPath := range workflowYAMLFiles(check.cfg.WorkflowsDir) {
		text, err := readText(workflowPath)
		if err != nil {
			return err
		}
		path := ".github/workflows/" + filepath.Base(workflowPath)
		for _, spec := range specs {
			if err := validateOptionalWorkflowPin(text, path, spec); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateOptionalWorkflowPin(text, path string, spec workflowPinSpec) error {
	present := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(spec.name) + `:`).MatchString(text)
	if !present {
		return nil
	}
	value, err := requireYAMLKey(text, spec.name, path)
	if err != nil {
		return err
	}
	if err := requireEqual(spec.name, spec.expected, spec.expectedPath, value, path); err != nil {
		return err
	}
	return spec.require(text, path)
}

func (check *pinnedInputsCheck) validateValidatorBuildkit() error {
	match := regexp.MustCompile(`(?m)^BUILDKIT_IMAGE="\$\{WORKCELL_BUILDKIT_IMAGE:-([^}]+)\}"$`).FindStringSubmatch(check.validatorImageScript)
	if match == nil {
		return errors.New("scripts/ci/build-validator-image.sh must default BUILDKIT_IMAGE from WORKCELL_BUILDKIT_IMAGE with a pinned fallback")
	}
	if err := requireEqual("WORKCELL_BUILDKIT_IMAGE", check.toolPins.buildkit, ".github/workflows/ci.yml", match[1], "scripts/ci/build-validator-image.sh"); err != nil {
		return err
	}
	for _, needle := range []string{
		`DEBIAN_BOOTSTRAP_MANIFEST="${ROOT_DIR}/runtime/container/debian-bootstrap.env"`,
		`DEBIAN_BOOTSTRAP_CKSUM="$(cksum "${DEBIAN_BOOTSTRAP_MANIFEST}" | awk '{print $1}')"`,
		`VALIDATOR_IMAGE_DEFAULT_TAG="workcell-validator:local-${VALIDATOR_DOCKERFILE_CKSUM}-${DEBIAN_BOOTSTRAP_CKSUM}"`,
	} {
		if !strings.Contains(check.validatorImageScript, needle) {
			return fmt.Errorf("scripts/ci/build-validator-image.sh must include the Debian bootstrap manifest in validator image identity: missing %s", needle)
		}
	}
	return nil
}
