// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	// pinnedReleaseTagPattern matches an exact vMAJOR.MINOR.PATCH release tag.
	pinnedReleaseTagPattern = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)
	// workflowPermissionsRE, actionRefPattern, commitShaPattern, and the
	// workflow-parsing helpers live in pinnedinputs_workflows.go.
)

type PinnedInputsConfig struct {
	RuntimeDockerfilePath    string
	ValidatorDockerfilePath  string
	ProvidersPackageJSONPath string
	ProvidersPackageLockPath string
	WorkflowsDir             string
	CIWorkflowPath           string
	ReleaseWorkflowPath      string
	PinHygieneWorkflowPath   string
	CodeownersPath           string
	CodexRequirementsPath    string
	CodexMCPConfigPath       string
	HostedControlsPolicyPath string
	HostedControlsScriptPath string
	ProviderBumpPolicyPath   string
	MaxDebianSnapshotAgeDays int
}

// NewPinnedInputsConfig derives the repo-standard pinned-input paths
// from repoRoot. The path layout is fixed by the repository; only the
// root and the snapshot-age budget vary between the live tree and test
// fixtures.
func NewPinnedInputsConfig(repoRoot string, maxDebianSnapshotAgeDays int) PinnedInputsConfig {
	return PinnedInputsConfig{
		RuntimeDockerfilePath:    filepath.Join(repoRoot, "runtime", "container", "Dockerfile"),
		ValidatorDockerfilePath:  filepath.Join(repoRoot, "tools", "validator", "Dockerfile"),
		ProvidersPackageJSONPath: filepath.Join(repoRoot, "runtime", "container", "providers", "package.json"),
		ProvidersPackageLockPath: filepath.Join(repoRoot, "runtime", "container", "providers", "package-lock.json"),
		WorkflowsDir:             filepath.Join(repoRoot, ".github", "workflows"),
		CIWorkflowPath:           filepath.Join(repoRoot, ".github", "workflows", "ci.yml"),
		ReleaseWorkflowPath:      filepath.Join(repoRoot, ".github", "workflows", "release.yml"),
		PinHygieneWorkflowPath:   filepath.Join(repoRoot, ".github", "workflows", "pin-hygiene.yml"),
		CodeownersPath:           filepath.Join(repoRoot, ".github", "CODEOWNERS"),
		CodexRequirementsPath:    filepath.Join(repoRoot, "adapters", "codex", "requirements.toml"),
		CodexMCPConfigPath:       filepath.Join(repoRoot, "adapters", "codex", "mcp", "config.toml"),
		HostedControlsPolicyPath: filepath.Join(repoRoot, "policy", "github-hosted-controls.toml"),
		HostedControlsScriptPath: filepath.Join(repoRoot, "scripts", "verify-github-hosted-controls.sh"),
		ProviderBumpPolicyPath:   filepath.Join(repoRoot, "policy", "provider-bumps.toml"),
		MaxDebianSnapshotAgeDays: maxDebianSnapshotAgeDays,
	}
}

type markdownlintPackageJSON struct {
	Dependencies map[string]string `json:"dependencies"`
}

type markdownlintPackageLock struct {
	Packages map[string]markdownlintPackageLockEntry `json:"packages"`
}

type markdownlintPackageLockEntry struct {
	Version      string            `json:"version"`
	Dependencies map[string]string `json:"dependencies"`
	Engines      map[string]string `json:"engines"`
}

// readText, isHexDigest, hexDigestPattern live in core.go.
// requireStringSliceTable lives in hostedcontrols.go
// (canonical post-collapse; same package-internal symbols all consumers share).
// The GitHub Actions workflow format — uses-scan types, extractWorkflowUses,
// toolPins/loadToolPins/parseToolPins, loadAllowedActions, and the
// pull_request_target and YAML helpers — lives in pinnedinputs_workflows.go.

func CheckPinnedInputs(cfg PinnedInputsConfig) error {
	check := newPinnedInputsCheck(cfg)
	return firstPinnedInputError(
		check.load,
		check.validateFoundationalPins,
		check.loadRustManifestPins,
		check.loadAndValidateRustImagePins,
		check.validateCargoRustVersion,
		check.validateRustupVersion,
		check.validateRustupDigests,
		check.validateProviderLock,
		check.validateBuildxPins,
		check.validateQEMUPins,
		check.validateBuildkitPins,
		check.validateWorkflowBuilderPins,
		check.validateValidatorBuildkit,
		check.validateCosignVersions,
		check.validateCosignReleaseBindings,
		check.validateCosignRefsAndBuildxSetup,
		check.loadCIReproBuildJob,
		check.validateCIReproStrategy,
		check.validateCIReproEntriesAndTail,
		check.validateReleaseBuildAndSyft,
		check.loadSecurityWorkflow,
		check.validateActionlintPins,
		check.validateZizmorPins,
		check.validateToolPinPolicy,
		check.validateSecurityToolDownloads,
		check.validateSecurityWorkflowDispatch,
		check.validateReleaseManifestAndRuntimeSources,
		check.validateRuntimeCargoBuild,
		check.validateReleaseRequiredSteps,
		check.validateReleaseArtifactFlows,
		check.validateReleaseLegacyReferences,
		check.validateReleaseHostedControls,
		check.validateHostedControlsWorkflow,
		check.validateReleaseVerificationJobs,
		check.validateReleasePublishingInputs,
		check.validateWorkflowPolicies,
		check.validateCodeownersAndHostedPolicy,
	)
}

func requireArg(text, name, path string) (string, error) {
	match := regexp.MustCompile(`(?m)^ARG ` + regexp.QuoteMeta(name) + `=(.+)$`).FindStringSubmatch(text)
	if match == nil {
		return "", fmt.Errorf("unable to extract %s from %s", name, path)
	}
	return strings.TrimSpace(match[1]), nil
}

func requirePinnedBaseImage(image, label, path string) error {
	if !regexp.MustCompile(`^[^@]+@sha256:[0-9a-f]{64}$`).MatchString(image) {
		return fmt.Errorf("%s in %s must be pinned by immutable digest, found %q", label, path, image)
	}
	return nil
}

func requireRegex(text, pattern, label, path string) (*regexp.Regexp, []string, error) {
	re := regexp.MustCompile(pattern)
	match := re.FindStringSubmatch(text)
	if match == nil {
		return nil, nil, fmt.Errorf("%s in %s must match %q", label, path, pattern)
	}
	return re, match, nil
}

func requireEqual(label, left, leftPath, right, rightPath string) error {
	if left != right {
		return fmt.Errorf("%s must match between %s (%q) and %s (%q)", label, leftPath, left, rightPath, right)
	}
	return nil
}

func requireDelimitedText(text, start, end, label, path string) (string, error) {
	startIndex := strings.Index(text, start)
	if startIndex < 0 {
		return "", fmt.Errorf("%s in %s must start with %q", label, path, start)
	}
	bodyStart := startIndex + len(start)
	endIndex := strings.Index(text[bodyStart:], end)
	if endIndex < 0 {
		return "", fmt.Errorf("%s in %s must end with %q", label, path, end)
	}
	return text[bodyStart : bodyStart+endIndex], nil
}

func requireOrderedText(text, label, path string, needles []string) error {
	offset := 0
	for _, needle := range needles {
		index := strings.Index(text[offset:], needle)
		if index < 0 {
			return fmt.Errorf("%s in %s must contain %q after the previous required step", label, path, needle)
		}
		offset += index + len(needle)
	}
	return nil
}
