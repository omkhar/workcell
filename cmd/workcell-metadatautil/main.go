// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

// Package main is the workcell-metadatautil umbrella binary — a CI
// helper grab-bag (control-plane manifests, scenario-manifest, mutation
// tests, tree-compare, coverage tooling, JSON/TOML validation, etc.).
//
// Calling convention: subcommands here use positional argv that
// matches their bash predecessor's argv shape (most are 3-15
// positional args; usage is shown in the subcommand table). The
// scenario-manifest subcommand is special-cased in main() because it
// preserves the bash contract of distinct usage (exit 2) vs.
// runtime (exit 1) error codes that scenarios.Run encodes.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"time"

	"github.com/omkhar/workcell/internal/metadatautil"
	"github.com/omkhar/workcell/internal/metadatautil/hostedcontrols"
	"github.com/omkhar/workcell/internal/metadatautil/pinnedinputs"
	"github.com/omkhar/workcell/internal/metadatautil/workflows"
	"github.com/omkhar/workcell/internal/mutation"
	"github.com/omkhar/workcell/internal/paritytree"
	"github.com/omkhar/workcell/internal/scenarios"
)

func die(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

// subcommand describes one workcell-metadatautil subcommand.  minArgs
// and maxArgs count only the args that follow the subcommand name; a
// maxArgs of -1 means unbounded.
type subcommand struct {
	name    string
	usage   string
	minArgs int
	maxArgs int
	handler func(args []string) error
}

func subcommands() []subcommand {
	return []subcommand{
		{"generate-control-plane-manifest", "ROOT_DIR OUTPUT_PATH", 2, 2, cmdGenerateControlPlaneManifest},
		{"verify-control-plane-manifest", "MANIFEST_PATH", 1, 1, cmdVerifyControlPlaneManifest},
		{"verify-control-plane-parity", "MANIFEST_PATH", 1, 1, cmdVerifyControlPlaneParity},
		{"check-workflows", "ROOT_DIR POLICY_PATH", 2, 2, cmdCheckWorkflows},
		{"generate-workflow-lane-manifest", "ROOT_DIR POLICY_PATH OUTPUT_PATH", 3, 3, cmdGenerateWorkflowLaneManifest},
		{"verify-workflow-lane-manifest", "ROOT_DIR POLICY_PATH MANIFEST_PATH", 3, 3, cmdVerifyWorkflowLaneManifest},
		{"plan-workflow-lanes", "MANIFEST_PATH CONFIG_JSON_PATH", 2, 2, cmdPlanWorkflowLanes},
		{"fetch-rulesets", "TMP_DIR REPO", 2, 2, cmdFetchRulesets},
		{"list-hosted-control-environments", "POLICY_PATH", 1, 1, cmdListHostedControlEnvironments},
		{"verify-github-hosted-controls", "TMP_DIR REPO POLICY_PATH", 3, 3, cmdVerifyGitHubHostedControls},
		{"extract-dockerfile-arg", "DOCKERFILE_PATH ARG_NAME", 2, 2, cmdExtractDockerfileArg},
		{"extract-claude-sha", "DOCKERFILE_PATH TARGET_ARCH", 2, 2, cmdExtractClaudeSHA},
		{"extract-codex-sha", "DOCKERFILE_PATH TARGET_ARCH", 2, 2, cmdExtractCodexSHA},
		{"manifest-checksum", "MANIFEST_PATH PLATFORM", 2, 2, cmdManifestChecksum},
		{"manifest-version", "MANIFEST_PATH EXPECTED_VERSION", 2, 2, cmdManifestVersion},
		{"check-provider-bump-policy", "POLICY_PATH DOCKERFILE PROVIDERS_PACKAGE_JSON", 3, 3, cmdCheckProviderBumpPolicy},
		{"provider-bump-plan", "POLICY_PATH DOCKERFILE PROVIDERS_PACKAGE_JSON [NOW_RFC3339]", 3, 4, cmdProviderBumpPlan},
		{"apply-provider-bump-plan", "PLAN_PATH POLICY_PATH DOCKERFILE PROVIDERS_PACKAGE_JSON", 4, 4, cmdApplyProviderBumpPlan},
		{"generate-build-input-manifest", "DOCKERFILE PACKAGE_JSON PACKAGE_LOCK OUTPUT BUILD_REF SOURCE_DATE_EPOCH REQUIRE_TRACKED", 7, 7, cmdGenerateBuildInputManifest},
		{"generate-builder-environment-manifest", "OUTPUT BUILDKIT_IMAGE BUILDX_VERSION_TARGET COSIGN_VERSION_TARGET QEMU_IMAGE SYFT_VERSION_TARGET BUILDX_VERSION BUILDX_INSPECT DOCKER_VERSION_JSON QEMU_VERSION COSIGN_VERSION CURL_VERSION GIT_VERSION GZIP_VERSION SYFT_VERSION TAR_VERSION", 16, 16, cmdGenerateBuilderEnvironmentManifest},
		{"check-pinned-inputs", "DOCKERFILE VALIDATOR_DOCKERFILE PROVIDERS_PACKAGE_JSON PROVIDERS_PACKAGE_LOCK WORKFLOWS_DIR CI_WORKFLOW RELEASE_WORKFLOW PIN_HYGIENE_WORKFLOW CODEOWNERS CODEX_REQUIREMENTS CODEX_MCP_CONFIG HOSTED_CONTROLS_POLICY HOSTED_CONTROLS_SCRIPT PROVIDER_BUMP_POLICY MAX_DEBIAN_SNAPSHOT_AGE_DAYS", 15, 15, cmdCheckPinnedInputs},
		{"verify-reproducible-build", "OCI_EXPORT_A OCI_EXPORT_B REPRO_PLATFORMS REPRO_MANIFEST_PATH SOURCE_DATE_EPOCH", 5, 5, cmdVerifyReproducibleBuild},
		{"generate-reproducible-build-manifest", "OCI_EXPORT REPRO_PLATFORMS OUTPUT_PATH SOURCE_DATE_EPOCH", 4, 4, cmdGenerateReproducibleBuildManifest},
		{"verify-reproducible-build-manifest", "OCI_EXPORT REPRO_PLATFORMS MANIFEST_PATH", 3, 3, cmdVerifyReproducibleBuildManifest},
		{"canonicalize-path", "PATH", 1, 1, cmdCanonicalizePath},
		{"coverage-percent", "REPORT_PATH MINIMUM LABEL", 3, 3, cmdCoveragePercent},
		{"coverage-executables", "MESSAGE_PATH", 1, 1, cmdCoverageExecutables},
		{"validate-json", "FILE [FILE...]", 1, -1, cmdValidateJSON},
		{"validate-toml", "FILE [FILE...]", 1, -1, cmdValidateTOML},
		{"validate-requirements", "ROOT_DIR REQUIREMENTS_PATH", 2, 2, cmdValidateRequirements},
		{"validate-operator-contract", "ROOT_DIR CONTRACT_PATH REQUIREMENTS_PATH", 3, 3, cmdValidateOperatorContract},
		{"scan-credential-patterns", "ROOT_DIR", 1, 1, cmdScanCredentialPatterns},
		{"run-mutation-tests", "", 0, 0, cmdRunMutationTests},
		{"tree-compare", "LEFT_ROOT RIGHT_ROOT", 2, 2, cmdTreeCompare},
	}
}

// scenario-manifest is intentionally dispatched in main() rather than
// listed here because it preserves the bash contract of distinct
// usage (2) vs. runtime (1) exit codes that scenarios.Run encodes.
// Help/error output mentions it via the special-case branches in
// main() and rootUsageError.

func main() {
	if len(os.Args) < 2 {
		die(rootUsageError(""))
	}
	// scenario-manifest preserves the bash contract of distinct exit
	// codes for usage (2) vs. runtime (1) errors that
	// scenarios.Run encodes; we forward through it directly rather
	// than collapsing into the generic error-then-die path used by
	// the other subcommands.
	if os.Args[1] == "scenario-manifest" {
		os.Exit(scenarios.Run("workcell-metadatautil scenario-manifest", os.Args[2:], os.Stdout, os.Stderr))
	}
	for _, sub := range subcommands() {
		if sub.name != os.Args[1] {
			continue
		}
		args := os.Args[2:]
		if len(args) < sub.minArgs || (sub.maxArgs >= 0 && len(args) > sub.maxArgs) {
			die(fmt.Errorf("usage: %s %s %s", os.Args[0], sub.name, sub.usage))
		}
		if err := sub.handler(args); err != nil {
			die(err)
		}
		return
	}
	die(rootUsageError(os.Args[1]))
}

func rootUsageError(badCommand string) error {
	names := make([]string, 0, len(subcommands()))
	for _, sub := range subcommands() {
		names = append(names, sub.name)
	}
	if badCommand == "" {
		return fmt.Errorf("usage: %s <command> [args...]", os.Args[0])
	}
	return fmt.Errorf("unknown command: %s", badCommand)
}

func cmdGenerateControlPlaneManifest(args []string) error {
	return metadatautil.GenerateControlPlaneManifest(args[0], args[1])
}

func cmdVerifyControlPlaneManifest(args []string) error {
	return metadatautil.ValidateControlPlaneManifest(args[0])
}

func cmdVerifyControlPlaneParity(args []string) error {
	rows, err := metadatautil.ControlPlaneParityRows(args[0])
	if err != nil {
		return err
	}
	for _, row := range rows {
		fmt.Println(row)
	}
	return nil
}

func cmdCheckWorkflows(args []string) error {
	return workflows.CheckWorkflows(args[0], args[1])
}

func cmdGenerateWorkflowLaneManifest(args []string) error {
	return metadatautil.GenerateWorkflowLaneManifest(args[0], args[1], args[2])
}

func cmdVerifyWorkflowLaneManifest(args []string) error {
	return metadatautil.VerifyWorkflowLaneManifest(args[0], args[1], args[2])
}

func cmdPlanWorkflowLanes(args []string) error {
	var cfg metadatautil.WorkflowLanePlannerConfig
	if err := metadatautil.LoadJSONFile(args[1], &cfg); err != nil {
		return err
	}
	plan, err := metadatautil.PlanWorkflowLanes(args[0], cfg)
	if err != nil {
		return err
	}
	content, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("%s\n", content)
	return nil
}

func cmdFetchRulesets(args []string) error {
	return hostedcontrols.FetchRulesets(args[0], args[1])
}

func cmdListHostedControlEnvironments(args []string) error {
	environments, err := hostedcontrols.EnvironmentNames(args[0])
	if err != nil {
		return err
	}
	for _, environmentName := range environments {
		fmt.Println(environmentName)
	}
	return nil
}

func cmdVerifyGitHubHostedControls(args []string) error {
	return hostedcontrols.VerifyGitHubHostedControls(args[0], args[1], args[2])
}

func cmdExtractDockerfileArg(args []string) error {
	value, err := metadatautil.ExtractDockerfileArg(args[0], args[1])
	if err != nil {
		return err
	}
	fmt.Println(value)
	return nil
}

func cmdExtractClaudeSHA(args []string) error {
	value, err := metadatautil.ExtractClaudeSHA(args[0], args[1])
	if err != nil {
		return err
	}
	fmt.Println(value)
	return nil
}

func cmdExtractCodexSHA(args []string) error {
	value, err := metadatautil.ExtractCodexSHA(args[0], args[1])
	if err != nil {
		return err
	}
	fmt.Println(value)
	return nil
}

func cmdManifestChecksum(args []string) error {
	value, err := metadatautil.ManifestChecksum(args[0], args[1])
	if err != nil {
		return err
	}
	fmt.Println(value)
	return nil
}

func cmdManifestVersion(args []string) error {
	return metadatautil.ManifestVersion(args[0], args[1])
}

func cmdCheckProviderBumpPolicy(args []string) error {
	return metadatautil.CheckProviderBumpPolicy(args[0], args[1], args[2])
}

func cmdProviderBumpPlan(args []string) error {
	now := time.Now().UTC()
	if len(args) == 4 {
		parsed, err := time.Parse(time.RFC3339, args[3])
		if err != nil {
			return err
		}
		now = parsed.UTC()
	}
	plan, err := metadatautil.PlanProviderBumps(args[0], args[1], args[2], now, metadatautil.DefaultProviderBumpSources(), nil)
	if err != nil {
		return err
	}
	content, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("%s\n", content)
	return nil
}

func cmdApplyProviderBumpPlan(args []string) error {
	return metadatautil.ApplyProviderBumpPlan(args[0], args[1], args[2], args[3])
}

func cmdGenerateBuildInputManifest(args []string) error {
	sourceEpoch, err := strconv.ParseInt(args[5], 10, 64)
	if err != nil {
		return err
	}
	requireTracked := args[6] == "1"
	return metadatautil.GenerateBuildInputManifest(args[0], args[1], args[2], args[3], args[4], sourceEpoch, requireTracked)
}

func cmdGenerateBuilderEnvironmentManifest(args []string) error {
	return metadatautil.GenerateBuilderEnvironmentManifest(
		args[0], args[1], args[2], args[3], args[4], args[5], args[6], args[7],
		args[8], args[9], args[10], args[11], args[12], args[13], args[14], args[15],
	)
}

func cmdCheckPinnedInputs(args []string) error {
	maxAge, err := strconv.Atoi(args[14])
	if err != nil {
		return err
	}
	return pinnedinputs.CheckPinnedInputs(pinnedinputs.PinnedInputsConfig{
		RuntimeDockerfilePath:    args[0],
		ValidatorDockerfilePath:  args[1],
		ProvidersPackageJSONPath: args[2],
		ProvidersPackageLockPath: args[3],
		WorkflowsDir:             args[4],
		CIWorkflowPath:           args[5],
		ReleaseWorkflowPath:      args[6],
		PinHygieneWorkflowPath:   args[7],
		CodeownersPath:           args[8],
		CodexRequirementsPath:    args[9],
		CodexMCPConfigPath:       args[10],
		HostedControlsPolicyPath: args[11],
		HostedControlsScriptPath: args[12],
		ProviderBumpPolicyPath:   args[13],
		MaxDebianSnapshotAgeDays: maxAge,
	})
}

func cmdVerifyReproducibleBuild(args []string) error {
	sourceEpoch, err := strconv.ParseInt(args[4], 10, 64)
	if err != nil {
		return err
	}
	return metadatautil.VerifyReproducibleBuild(args[0], args[1], args[2], args[3], sourceEpoch)
}

func cmdGenerateReproducibleBuildManifest(args []string) error {
	sourceEpoch, err := strconv.ParseInt(args[3], 10, 64)
	if err != nil {
		return err
	}
	return metadatautil.GenerateReproducibleBuildManifest(args[0], args[1], args[2], sourceEpoch)
}

func cmdVerifyReproducibleBuildManifest(args []string) error {
	return metadatautil.VerifyReproducibleBuildManifest(args[0], args[1], args[2])
}

func cmdCanonicalizePath(args []string) error {
	value, err := metadatautil.CanonicalizePath(args[0])
	if err != nil {
		return err
	}
	fmt.Println(value)
	return nil
}

func cmdCoveragePercent(args []string) error {
	minimum, err := strconv.ParseFloat(args[1], 64)
	if err != nil {
		return err
	}
	percent, err := metadatautil.CoveragePercent(args[0])
	if err != nil {
		return err
	}
	if percent < minimum {
		return fmt.Errorf("%s is %.2f%%, below the required %.2f%%", args[2], percent, minimum)
	}
	fmt.Printf("%s: %.2f%%\n", args[2], percent)
	return nil
}

func cmdCoverageExecutables(args []string) error {
	executables, err := metadatautil.CoverageExecutables(args[0])
	if err != nil {
		return err
	}
	for _, executable := range executables {
		fmt.Println(executable)
	}
	return nil
}

func cmdValidateJSON(args []string) error {
	return metadatautil.ValidateJSONFiles(args)
}

func cmdValidateTOML(args []string) error {
	return metadatautil.ValidateTOMLFiles(args)
}

func cmdValidateRequirements(args []string) error {
	return metadatautil.ValidateRequirements(args[0], args[1])
}

func cmdValidateOperatorContract(args []string) error {
	return metadatautil.ValidateOperatorContract(args[0], args[1], args[2])
}

func cmdScanCredentialPatterns(args []string) error {
	return metadatautil.ScanCredentialPatterns(args[0])
}

// cmdRunMutationTests absorbs the former workcell-run-mutation-tests
// binary.  Like that binary, the repo root is recovered from the
// source path of this file via runtime.Caller — the resulting path
// (cmd/workcell-metadatautil/main.go → cmd/workcell-metadatautil →
// cmd → repo root) is two `..` segments up, identical to the
// original.
func cmdRunMutationTests(_ []string) error {
	root, err := metadatautilRepoRoot()
	if err != nil {
		return err
	}
	return mutation.Run(root)
}

// metadatautilRepoRoot returns the repo root by walking two `..`
// segments up from this source file's path. This ties correctness to
// the source-tree layout: moving cmd/workcell-metadatautil/main.go
// (or building+installing the binary outside the repo) breaks it.
// The original workcell-run-mutation-tests standalone binary had the
// same shape; preserved here for parity. A more robust fix would
// take repo-root as an explicit argv arg.
func metadatautilRepoRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("unable to locate repo root")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..")), nil
}

// cmdTreeCompare absorbs the former workcell-tree-compare binary.
func cmdTreeCompare(args []string) error {
	return paritytree.CompareDirectoryTrees(args[0], args[1])
}
