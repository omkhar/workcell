// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/omkhar/workcell/internal/tomlsubset"
)

type pinnedInputsCheck struct {
	cfg      PinnedInputsConfig
	repoRoot string
	paths    pinnedInputPaths

	allowedActions map[string]bool
	pins           toolPins

	runtimeDockerfile           string
	validatorDockerfile         string
	providersPackageJSONText    string
	providersPackageLockText    string
	markdownlintPackageJSONText string
	markdownlintPackageLockText string
	installDevToolsScript       string
	validateRepoScript          string
	ciWorkflow                  string
	releaseWorkflow             string
	pinHygieneWorkflow          string
	upstreamRefreshWorkflow     string
	validatorImageScript        string
	codeowners                  string
	hostedControlsPolicyText    string
	hostedControlsScript        string
	codexRequirementsText       string
	codexMCPConfigText          string
	goModText                   string
	cargoManifestText           string
	rustToolchainText           string
	securityWorkflow            string

	debianBootstrapManifest DebianBootstrapManifest
	providersPackageJSON    map[string]any
	providersPackageLock    map[string]any
	markdownlintPackageJSON markdownlintPackageJSON
	markdownlintPackageLock markdownlintPackageLock
	hostedControlsPolicy    map[string]any

	rustPins rustPinValues
	toolPins validatedToolPins
	ciRepro  ciReproValues
}

type pinnedInputPaths struct {
	goMod                string
	cargoManifest        string
	installDevTools      string
	validateRepo         string
	markdownlintJSON     string
	markdownlintLock     string
	rustToolchain        string
	debianBootstrap      string
	upstreamRefresh      string
	validatorImageScript string
}

type rustPinValues struct {
	cargoVersion, toolchainVersion, runtimeVersion string
	runtimeImage, validatorVersion                 string
}

type validatedToolPins struct {
	buildx, qemu, buildkit, cosign, syft string
	actionlintVersion, actionlintSHA     string
	zizmorVersion, zizmorSHA             string
}

type ciReproValues struct{ job, strategy string }

type pinnedTextInput struct {
	path   string
	target *string
}

type releaseDownload struct{ label, url string }

type pinnedStringInput struct {
	target           *string
	text, name, path string
}
type workflowEnvInput struct {
	target                          *string
	text, key, pattern, label, path string
}

type textRequirement struct {
	text, needle string
	forbidden    bool
	err          error
}

type pinnedStringExtractor func(string, string, string) (string, error)

func newPinnedInputsCheck(cfg PinnedInputsConfig) *pinnedInputsCheck {
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(cfg.RuntimeDockerfilePath), "..", ".."))
	return &pinnedInputsCheck{
		cfg:      cfg,
		repoRoot: repoRoot,
		paths: pinnedInputPaths{
			goMod:                filepath.Join(repoRoot, "go.mod"),
			cargoManifest:        filepath.Join(repoRoot, "runtime", "container", "rust", "Cargo.toml"),
			installDevTools:      filepath.Join(repoRoot, "scripts", "install-dev-tools.sh"),
			validateRepo:         filepath.Join(repoRoot, "scripts", "validate-repo.sh"),
			markdownlintJSON:     filepath.Join(repoRoot, "tools", "markdownlint", "package.json"),
			markdownlintLock:     filepath.Join(repoRoot, "tools", "markdownlint", "package-lock.json"),
			rustToolchain:        filepath.Join(repoRoot, "runtime", "container", "rust", "rust-toolchain.toml"),
			debianBootstrap:      filepath.Join(repoRoot, filepath.FromSlash(DebianBootstrapManifestRelPath)),
			upstreamRefresh:      filepath.Join(cfg.WorkflowsDir, "upstream-refresh.yml"),
			validatorImageScript: filepath.Join(repoRoot, "scripts", "ci", "build-validator-image.sh"),
		},
	}
}

func (check *pinnedInputsCheck) load() error {
	return firstPinnedInputError(
		func() error {
			path := filepath.Join(check.repoRoot, "policy", "allowed-actions.toml")
			var err error
			check.allowedActions, err = loadAllowedActions(path)
			return err
		},
		func() error {
			path := filepath.Join(check.repoRoot, "policy", "tool-pins.toml")
			var err error
			check.pins, err = loadToolPins(path)
			return err
		},
		func() error {
			return loadPinnedTextInputs([]pinnedTextInput{
				{check.cfg.RuntimeDockerfilePath, &check.runtimeDockerfile},
				{check.cfg.ValidatorDockerfilePath, &check.validatorDockerfile},
			})
		},
		func() error {
			var err error
			check.debianBootstrapManifest, err = ReadDebianBootstrapManifest(check.paths.debianBootstrap)
			return err
		},
		func() error {
			return loadPinnedTextInputs([]pinnedTextInput{
				{check.cfg.ProvidersPackageJSONPath, &check.providersPackageJSONText},
				{check.cfg.ProvidersPackageLockPath, &check.providersPackageLockText},
				{check.paths.markdownlintJSON, &check.markdownlintPackageJSONText},
				{check.paths.markdownlintLock, &check.markdownlintPackageLockText},
				{check.paths.installDevTools, &check.installDevToolsScript},
				{check.paths.validateRepo, &check.validateRepoScript},
				{check.cfg.CIWorkflowPath, &check.ciWorkflow},
				{check.cfg.ReleaseWorkflowPath, &check.releaseWorkflow},
				{check.cfg.PinHygieneWorkflowPath, &check.pinHygieneWorkflow},
				{check.paths.upstreamRefresh, &check.upstreamRefreshWorkflow},
				{check.paths.validatorImageScript, &check.validatorImageScript},
				{check.cfg.CodeownersPath, &check.codeowners},
				{check.cfg.HostedControlsPolicyPath, &check.hostedControlsPolicyText},
				{check.cfg.HostedControlsScriptPath, &check.hostedControlsScript},
				{check.cfg.CodexRequirementsPath, &check.codexRequirementsText},
				{check.cfg.CodexMCPConfigPath, &check.codexMCPConfigText},
				{check.paths.goMod, &check.goModText},
				{check.paths.cargoManifest, &check.cargoManifestText},
				{check.paths.rustToolchain, &check.rustToolchainText},
			})
		},
		func() error {
			return firstPinnedInputError(
				func() error {
					return json.Unmarshal([]byte(check.providersPackageJSONText), &check.providersPackageJSON)
				},
				func() error {
					return json.Unmarshal([]byte(check.providersPackageLockText), &check.providersPackageLock)
				},
				func() error {
					return json.Unmarshal([]byte(check.markdownlintPackageJSONText), &check.markdownlintPackageJSON)
				},
				func() error {
					return json.Unmarshal([]byte(check.markdownlintPackageLockText), &check.markdownlintPackageLock)
				},
				func() error {
					var err error
					check.hostedControlsPolicy, err = tomlsubset.Parse(check.hostedControlsPolicyText, check.cfg.HostedControlsPolicyPath)
					return err
				},
			)
		},
	)
}

func loadPinnedTextInputs(inputs []pinnedTextInput) error {
	for _, input := range inputs {
		text, err := readText(input.path)
		if err != nil {
			return err
		}
		*input.target = text
	}
	return nil
}

func firstPinnedInputError(checks ...func() error) error {
	for _, check := range checks {
		if err := check(); err != nil {
			return err
		}
	}
	return nil
}

func requireYAMLKey(text, name, path string) (string, error) {
	match := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(name) + `:\s*(.+)$`).FindStringSubmatch(text)
	if match == nil {
		return "", fmt.Errorf("unable to extract %s from %s", name, path)
	}
	return strings.TrimSpace(match[1]), nil
}

func requireUniformWorkflowEnv(text, key, valuePattern, label, path string) (string, error) {
	lineRE := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(key) + `:\s*(\S+)\s*$`)
	valueRE := regexp.MustCompile(`^` + valuePattern + `$`)
	matches := lineRE.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return "", fmt.Errorf("%s in %s must define %s", label, path, key)
	}
	value := matches[0][1]
	for _, match := range matches {
		if !valueRE.MatchString(match[1]) {
			return "", fmt.Errorf("%s in %s must match %q", label, path, valuePattern)
		}
		if match[1] != value {
			return "", fmt.Errorf("%s in %s must use one reviewed value for %s", label, path, key)
		}
	}
	return value, nil
}

func requireCappedReleaseDownloads(text, path string, downloads []releaseDownload) error {
	for _, download := range downloads {
		if err := requireCappedReleaseDownload(text, path, download); err != nil {
			return err
		}
	}
	return nil
}

func requireCappedReleaseDownload(text, path string, download releaseDownload) error {
	count := strings.Count(text, download.url)
	if count == 0 {
		return fmt.Errorf("%s must derive the %s archive URL from its pinned version", path, download.label)
	}
	offset := 0
	for range count {
		relative := strings.Index(text[offset:], download.url)
		urlIndex := offset + relative
		curlIndex := strings.LastIndex(text[:urlIndex], "curl -fsSL")
		if curlIndex < 0 {
			return fmt.Errorf("%s must download %s with curl -fsSL", path, download.label)
		}
		block := text[curlIndex : urlIndex+len(download.url)]
		if err := requireDownloadBounds(block, path, download.label); err != nil {
			return err
		}
		offset = urlIndex + len(download.url)
	}
	return nil
}

func requireDownloadBounds(block, path, label string) error {
	for _, needle := range []string{"--max-time 60", "--connect-timeout 15", "--max-filesize 209715200"} {
		if !strings.Contains(block, needle) {
			return fmt.Errorf("%s must bound %s downloads with %s", path, label, needle)
		}
	}
	return nil
}

func requireActionRef(text, action, path string) (string, error) {
	re := regexp.MustCompile(regexp.QuoteMeta(action) + `@([0-9a-f]{40})`)
	matches := re.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return "", fmt.Errorf("%s must pin %s to an immutable commit SHA", path, action)
	}
	ref := matches[0][1]
	for _, match := range matches[1:] {
		if match[1] != ref {
			return "", fmt.Errorf("%s must use a single reviewed ref for %s", path, action)
		}
	}
	return ref, nil
}

func requireTOMLString(text, key, path string) (string, error) {
	match := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(key) + `\s*=\s*"([^"]+)"\s*$`).FindStringSubmatch(text)
	if match == nil {
		return "", fmt.Errorf("unable to extract %s from %s", key, path)
	}
	return match[1], nil
}

func loadPinnedStrings(extract pinnedStringExtractor, inputs []pinnedStringInput) error {
	for _, input := range inputs {
		value, err := extract(input.text, input.name, input.path)
		if err != nil {
			return err
		}
		*input.target = value
	}
	return nil
}

func loadWorkflowEnvs(inputs []workflowEnvInput) error {
	for _, input := range inputs {
		value, err := requireUniformWorkflowEnv(input.text, input.key, input.pattern, input.label, input.path)
		if err != nil {
			return err
		}
		*input.target = value
	}
	return nil
}

func requireTextRequirements(requirements ...textRequirement) error {
	for _, requirement := range requirements {
		if strings.Contains(requirement.text, requirement.needle) == requirement.forbidden {
			return requirement.err
		}
	}
	return nil
}

func requiredText(text, needle, path string) textRequirement {
	return textRequirement{text: text, needle: needle, err: fmt.Errorf("%s must contain %q", path, needle)}
}
