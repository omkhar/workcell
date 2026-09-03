// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"encoding/json"
	"path/filepath"

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

	debianBootstrapManifest DebianBootstrapManifest
	providersPackageJSON    map[string]any
	providersPackageLock    map[string]any
	markdownlintPackageJSON markdownlintPackageJSON
	markdownlintPackageLock markdownlintPackageLock
	hostedControlsPolicy    map[string]any
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

type pinnedTextInput struct {
	path   string
	target *string
}

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
		check.loadAllowedActions,
		check.loadToolPins,
		check.loadDockerfiles,
		check.loadDebianBootstrapManifest,
		check.loadInitialTextInputs,
		check.parseInitialTextInputs,
	)
}

func (check *pinnedInputsCheck) loadAllowedActions() error {
	path := filepath.Join(check.repoRoot, "policy", "allowed-actions.toml")
	var err error
	check.allowedActions, err = loadAllowedActions(path)
	return err
}

func (check *pinnedInputsCheck) loadToolPins() error {
	path := filepath.Join(check.repoRoot, "policy", "tool-pins.toml")
	var err error
	check.pins, err = loadToolPins(path)
	return err
}

func (check *pinnedInputsCheck) loadDockerfiles() error {
	return loadPinnedTextInputs([]pinnedTextInput{
		{check.cfg.RuntimeDockerfilePath, &check.runtimeDockerfile},
		{check.cfg.ValidatorDockerfilePath, &check.validatorDockerfile},
	})
}

func (check *pinnedInputsCheck) loadDebianBootstrapManifest() error {
	var err error
	check.debianBootstrapManifest, err = ReadDebianBootstrapManifest(check.paths.debianBootstrap)
	return err
}

func (check *pinnedInputsCheck) loadInitialTextInputs() error {
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
}

func (check *pinnedInputsCheck) parseInitialTextInputs() error {
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
		check.parseHostedControlsPolicy,
	)
}

func (check *pinnedInputsCheck) parseHostedControlsPolicy() error {
	var err error
	check.hostedControlsPolicy, err = tomlsubset.Parse(check.hostedControlsPolicyText, check.cfg.HostedControlsPolicyPath)
	return err
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
