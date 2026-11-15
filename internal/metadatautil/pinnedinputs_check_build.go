// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

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
	return firstPinnedInputError(
		func() error {
			return validateNodeProviderLock("runtime/container/providers/package-lock.json", check.providersPackageJSON, check.providersPackageLock)
		},
		func() error {
			return validateNodeProviderLock("runtime/container/providers-uncertified/package-lock.json", check.uncertifiedPackageJSON, check.uncertifiedPackageLock)
		},
	)
}

func (check *pinnedInputsCheck) validateValidatorBuildkit() error {
	match := regexp.MustCompile(`(?m)^BUILDKIT_IMAGE="\$\{WORKCELL_BUILDKIT_IMAGE:-([^}]+)\}"$`).FindStringSubmatch(check.validatorImageScript)
	if match == nil {
		return errors.New("scripts/ci/build-validator-image.sh must default BUILDKIT_IMAGE from WORKCELL_BUILDKIT_IMAGE with a pinned fallback")
	}
	if err := requireEqual("BuildKit image", check.pins.Buildkit, "policy/tool-pins.toml", match[1], "scripts/ci/build-validator-image.sh"); err != nil {
		return err
	}
	for _, needle := range []string{
		`source "${ROOT_DIR}/scripts/ci/lib/local-docker-parity.sh"`,
		`VALIDATOR_IMAGE_DEFAULT_TAG="$(workcell_validator_image_default_tag "${ROOT_DIR}")"`,
	} {
		if !strings.Contains(check.validatorImageScript, needle) {
			return fmt.Errorf("scripts/ci/build-validator-image.sh must include the Debian bootstrap manifest in validator image identity: missing %s", needle)
		}
	}
	return validateValidatorImageIdentityScript(check.localDockerParityScript)
}

func validateValidatorImageIdentityScript(script string) error {
	for _, needle := range []string{
		`cksum "${root}/tools/validator/Dockerfile"`,
		`cksum "${root}/runtime/container/debian-bootstrap.env"`,
		`printf 'workcell-validator:local-%s-%s\n' "${dockerfile_cksum}" "${bootstrap_cksum}"`,
	} {
		if !strings.Contains(script, needle) {
			return fmt.Errorf("scripts/ci/lib/local-docker-parity.sh must bind the validator image identity: missing %s", needle)
		}
	}
	return nil
}
