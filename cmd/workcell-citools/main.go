// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

// Package main is the workcell-citools umbrella binary — a CI
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
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/omkhar/workcell/internal/cliexit"
	"github.com/omkhar/workcell/internal/gitconfigblocklist"
	"github.com/omkhar/workcell/internal/metadatautil"
	"github.com/omkhar/workcell/internal/mutation"
	"github.com/omkhar/workcell/internal/paritytree"
	"github.com/omkhar/workcell/internal/scenarios"
	"github.com/omkhar/workcell/internal/workcellhardening"
)

func die(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

func dieUsage(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(2)
}

// subcommand describes one workcell-citools subcommand.  minArgs
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
		{"check-retention-policy", "ROOT_DIR POLICY_PATH", 2, 2, cmdCheckRetentionPolicy},
		{"generate-workflow-lane-manifest", "ROOT_DIR POLICY_PATH OUTPUT_PATH", 3, 3, cmdGenerateWorkflowLaneManifest},
		{"verify-workflow-lane-manifest", "ROOT_DIR POLICY_PATH MANIFEST_PATH", 3, 3, cmdVerifyWorkflowLaneManifest},
		{"plan-workflow-lanes", "MANIFEST_PATH CONFIG_JSON_PATH", 2, 2, cmdPlanWorkflowLanes},
		{"fetch-rulesets", "TMP_DIR REPO", 2, 2, cmdFetchRulesets},
		{"list-hosted-control-environments", "POLICY_PATH", 1, 1, cmdListHostedControlEnvironments},
		{"verify-github-hosted-controls", "TMP_DIR REPO POLICY_PATH", 3, 3, cmdVerifyGitHubHostedControls},
		{"extract-dockerfile-arg", "DOCKERFILE_PATH ARG_NAME", 2, 2, cmdExtractDockerfileArg},
		{"extract-claude-sha", "DOCKERFILE_PATH TARGET_ARCH", 2, 2, cmdExtractClaudeSHA},
		{"extract-codex-sha", "DOCKERFILE_PATH TARGET_ARCH", 2, 2, cmdExtractCodexSHA},
		{"extract-copilot-sha", "DOCKERFILE_PATH TARGET_ARCH", 2, 2, cmdExtractCopilotSHA},
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
		{"validate-public-contract", "ROOT_DIR CONTRACT_PATH", 2, 2, cmdValidatePublicContract},
		{"scan-credential-patterns", "ROOT_DIR", 1, 1, cmdScanCredentialPatterns},
		{"run-mutation-tests", "", 0, 0, cmdRunMutationTests},
		{"mutation-score", "POLICY_PATH", 1, 1, cmdMutationScore},
		{"tree-compare", "LEFT_ROOT RIGHT_ROOT", 2, 2, cmdTreeCompare},
		{"git-config-blocklist-parity", "ROOT_DIR", 1, 1, cmdGitConfigBlocklistParity},
		{"workcell-hardening-invariants", "ROOT_DIR", 1, 1, cmdWorkcellHardeningInvariants},
		{"workcell-config-safety", "ROOT_DIR", 1, 1, cmdWorkcellConfigSafety},
		{"workcell-runtime-invariants", "ROOT_DIR", 1, 1, cmdWorkcellRuntimeInvariants},
		{"workcell-managed-profile-staging", "ROOT_DIR", 1, 1, cmdWorkcellManagedProfileStaging},
		{"workcell-bootstrap-egress", "ROOT_DIR", 1, 1, cmdWorkcellBootstrapEgress},
		{"workcell-bootstrap-audit", "ROOT_DIR", 1, 1, cmdWorkcellBootstrapAudit},
		{"workcell-git-index-shadow", "ROOT_DIR", 1, 1, cmdWorkcellGitIndexShadow},
		{"workcell-publish-pr-shadow", "ROOT_DIR", 1, 1, cmdWorkcellPublishPrShadow},
		{"workcell-shadow-enum-egress", "ROOT_DIR", 1, 1, cmdWorkcellShadowEnumEgress},
		{"workcell-home-seed-provider-wrapper", "ROOT_DIR", 1, 1, cmdWorkcellHomeSeedProviderWrapper},
		{"workcell-copilot-token-handoff", "ROOT_DIR", 1, 1, cmdWorkcellCopilotTokenHandoff},
		{"workcell-copilot-docker-run", "ROOT_DIR", 1, 1, cmdWorkcellCopilotDockerRun},
		{"workcell-provider-launcher-authority", "ROOT_DIR", 1, 1, cmdWorkcellProviderLauncherAuthority},
		{"workcell-copilot-policy-wrapper", "ROOT_DIR", 1, 1, cmdWorkcellCopilotPolicyWrapper},
		{"workcell-copilot-unsafe-flags", "ROOT_DIR", 1, 1, cmdWorkcellCopilotUnsafeFlags},
		{"workcell-copilot-release-verify", "ROOT_DIR", 1, 1, cmdWorkcellCopilotReleaseVerify},
		{"workcell-adapter-rule-guard-bash", "ROOT_DIR", 1, 1, cmdWorkcellAdapterRuleGuardBash},
		{"workcell-inspect-assurance-loops", "ROOT_DIR", 1, 1, cmdWorkcellInspectAssuranceLoops},
		{"workcell-validator-writable-state", "ROOT_DIR", 1, 1, cmdWorkcellValidatorWritableState},
		{"workcell-hostutil-egress-rg", "ROOT_DIR", 1, 1, cmdWorkcellHostutilEgressRg},
		{"workcell-dockerfile-pins", "ROOT_DIR", 1, 1, cmdWorkcellDockerfilePins},
		{"workcell-validator-dispatch-loops", "ROOT_DIR", 1, 1, cmdWorkcellValidatorDispatchLoops},
		{"workcell-caller-required-contracts", "ROOT_DIR", 1, 1, cmdWorkcellCallerRequiredContracts},
		{"workcell-fnblock-goblock-gitenv", "ROOT_DIR", 1, 1, cmdWorkcellFnBlockGoBlockGitEnv},
		{"workcell-buildx-builder-trust", "ROOT_DIR", 1, 1, cmdWorkcellBuildxBuilderTrust},
		{"workcell-doc-scan-go-vcs", "ROOT_DIR", 1, 1, cmdWorkcellDocScanGoVcs},
		{"workcell-smoke-chown-tar", "ROOT_DIR", 1, 1, cmdWorkcellSmokeChownTar},
		{"workcell-dualstack-apply-plan", "ROOT_DIR", 1, 1, cmdWorkcellDualStackApplyPlan},
	}
}

// scenario-manifest is intentionally dispatched in main() rather than
// listed here because it preserves the bash contract of distinct
// usage (2) vs. runtime (1) exit codes that scenarios.Run encodes.
// Help/error output mentions it via the special-case branches in
// main() and rootUsageError.

func main() {
	if len(os.Args) < 2 {
		// A missing top-level command is a usage error (exit 2), matching
		// the wrong-arity path below and the other workcell Go CLIs (D8).
		dieUsage(rootUsageError(""))
	}
	// scenario-manifest preserves the bash contract of distinct exit
	// codes for usage (2) vs. runtime (1) errors that
	// scenarios.Run encodes; scenarios.Run already wrote the
	// diagnostic to stderr and returns a *cliexit.ExitCodeError, so we
	// forward Code straight through to os.Exit instead of routing it
	// through die() (which would double-print the message).
	if os.Args[1] == "scenario-manifest" {
		if err := scenarios.Run("workcell-citools scenario-manifest", os.Args[2:], os.Stdout, os.Stderr); err != nil {
			if ec, ok := cliexit.IsExitCodeError(err); ok {
				os.Exit(ec.Code)
			}
			die(err)
		}
		return
	}
	for _, sub := range subcommands() {
		if sub.name != os.Args[1] {
			continue
		}
		args := os.Args[2:]
		if len(args) < sub.minArgs || (sub.maxArgs >= 0 && len(args) > sub.maxArgs) {
			dieUsage(fmt.Errorf("usage: %s %s %s", os.Args[0], sub.name, sub.usage))
		}
		if err := sub.handler(args); err != nil {
			die(err)
		}
		return
	}
	// An unknown top-level command is a usage error (exit 2).
	dieUsage(rootUsageError(os.Args[1]))
}

func rootUsageError(badCommand string) error {
	names := make([]string, 0, len(subcommands())+1)
	for _, sub := range subcommands() {
		names = append(names, sub.name)
	}
	// scenario-manifest is dispatched directly in main() and so is not
	// part of the subcommands() table, but it is still a known command
	// for help/error output purposes.
	names = append(names, "scenario-manifest")
	sort.Strings(names)
	var lines strings.Builder
	for _, name := range names {
		lines.WriteString("  ")
		lines.WriteString(name)
		lines.WriteString("\n")
	}
	if badCommand == "" {
		return fmt.Errorf("usage: %s <command> [args...]\n\nCommands:\n%s", os.Args[0], lines.String())
	}
	return fmt.Errorf("unknown command: %s\n\nKnown commands:\n%s", badCommand, lines.String())
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
	return metadatautil.CheckWorkflows(args[0], args[1])
}

func cmdCheckRetentionPolicy(args []string) error {
	return metadatautil.CheckRetentionPolicy(args[0], args[1])
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
	return metadatautil.FetchRulesets(args[0], args[1])
}

func cmdListHostedControlEnvironments(args []string) error {
	environments, err := metadatautil.EnvironmentNames(args[0])
	if err != nil {
		return err
	}
	for _, environmentName := range environments {
		fmt.Println(environmentName)
	}
	return nil
}

func cmdVerifyGitHubHostedControls(args []string) error {
	return metadatautil.VerifyGitHubHostedControls(args[0], args[1], args[2])
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

func cmdExtractCopilotSHA(args []string) error {
	value, err := metadatautil.ExtractCopilotSHA(args[0], args[1])
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
	return metadatautil.CheckPinnedInputs(metadatautil.PinnedInputsConfig{
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

func cmdValidatePublicContract(args []string) error {
	return metadatautil.CheckPublicContract(args[0], args[1])
}

func cmdScanCredentialPatterns(args []string) error {
	return metadatautil.ScanCredentialPatterns(args[0])
}

// cmdRunMutationTests absorbs the former workcell-run-mutation-tests
// binary.  Like that binary, the repo root is recovered from the
// source path of this file via runtime.Caller — the resulting path
// (cmd/workcell-citools/main.go → cmd/workcell-citools →
// cmd → repo root) is two `..` segments up, identical to the
// original.
func cmdRunMutationTests(_ []string) error {
	root, err := citoolsRepoRoot()
	if err != nil {
		return err
	}
	return mutation.Run(root)
}

// cmdMutationScore runs the mutation harness, prints the score (so a wrapper can
// surface it in a CI job summary), and fails when the score drops below the
// reviewed baseline in POLICY_PATH.
func cmdMutationScore(args []string) error {
	root, err := citoolsRepoRoot()
	if err != nil {
		return err
	}
	policy, err := mutation.LoadScorePolicy(args[0])
	if err != nil {
		return err
	}
	result, err := mutation.RunScored(root)
	if err != nil {
		return err
	}
	fmt.Printf("mutation score: %.2f%% (%d/%d killed)\n", result.Score(), result.Killed, result.Total)
	if len(result.Survivors) > 0 {
		fmt.Printf("surviving mutants: %s\n", strings.Join(result.Survivors, ", "))
	}
	return mutation.CheckScore(result, policy)
}

// citoolsRepoRoot returns the repo root by walking two `..`
// segments up from this source file's path. This ties correctness to
// the source-tree layout: moving cmd/workcell-citools/main.go
// (or building+installing the binary outside the repo) breaks it.
// The original workcell-run-mutation-tests standalone binary had the
// same shape; preserved here for parity. A more robust fix would
// take repo-root as an explicit argv arg.
//
// TODO(workcell-citools-repo-root): take repo-root as an explicit
// argv arg so this helper stops depending on runtime.Caller layout.
func citoolsRepoRoot() (string, error) {
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

// cmdGitConfigBlocklistParity runs the git-config blocklist parity
// invariant migrated out of scripts/verify-invariants.sh; it fails
// (exit 1 via die()) with the shell's original stderr messages when a
// TOML key or prefix/suffix pattern is missing from any enforcer.
func cmdGitConfigBlocklistParity(args []string) error {
	return gitconfigblocklist.Check(args[0])
}

// cmdWorkcellHardeningInvariants runs the eleven scripts/workcell
// hardening-invariant checks migrated out of
// scripts/verify-invariants.sh; it fails (exit 1 via die()) with the
// shell's original stderr message for the first violated invariant.
func cmdWorkcellHardeningInvariants(args []string) error {
	return workcellhardening.Check(args[0])
}

// cmdWorkcellConfigSafety runs the four scripts/workcell config-safety
// checks migrated out of scripts/verify-invariants.sh; it fails (exit 1
// via die()) with the shell's original stderr message for the first
// violated invariant.
func cmdWorkcellConfigSafety(args []string) error {
	return workcellhardening.CheckConfigSafety(args[0])
}

// cmdWorkcellRuntimeInvariants runs the ten scripts/workcell runtime/gc
// checks migrated out of scripts/verify-invariants.sh; it fails (exit 1
// via die()) with the shell's original stderr message for the first
// violated invariant.
func cmdWorkcellRuntimeInvariants(args []string) error {
	return workcellhardening.CheckRuntimeInvariants(args[0])
}

// cmdWorkcellManagedProfileStaging runs the three scripts/workcell
// managed-profile staging/cleanup checks migrated out of
// scripts/verify-invariants.sh; it fails (exit 1 via die()) with the
// shell's original stderr message for the first violated invariant.
func cmdWorkcellManagedProfileStaging(args []string) error {
	return workcellhardening.CheckManagedProfileStaging(args[0])
}

// cmdWorkcellBootstrapEgress runs the nine bootstrap egress-endpoint
// checks migrated out of scripts/verify-invariants.sh; it fails (exit 1
// via die()) with the shell's original stderr message for the first
// violated invariant.
func cmdWorkcellBootstrapEgress(args []string) error {
	return workcellhardening.CheckBootstrapEgress(args[0])
}

// cmdWorkcellBootstrapAudit runs the two scripts/workcell
// bootstrap-audit-metadata checks migrated out of
// scripts/verify-invariants.sh; it fails (exit 1 via die()) with the
// shell's original stderr message for the first violated invariant.
func cmdWorkcellBootstrapAudit(args []string) error {
	return workcellhardening.CheckBootstrapAuditMetadata(args[0])
}

// cmdWorkcellGitIndexShadow runs the five scripts/workcell git-index shadow
// checks migrated out of scripts/verify-invariants.sh; it fails (exit 1 via
// die()) with the shell's original stderr message for the first violated
// invariant.
func cmdWorkcellGitIndexShadow(args []string) error {
	return workcellhardening.CheckGitIndexShadow(args[0])
}

// cmdWorkcellPublishPrShadow runs the four scripts/workcell publish-PR /
// shadow-mount checks migrated out of scripts/verify-invariants.sh; it fails
// (exit 1 via die()) with the shell's original stderr message for the first
// violated invariant.
func cmdWorkcellPublishPrShadow(args []string) error {
	return workcellhardening.CheckPublishPrShadowMounts(args[0])
}

// cmdWorkcellShadowEnumEgress runs the seven scripts/workcell shadow-enumeration
// / IPv6-egress checks migrated out of scripts/verify-invariants.sh; it fails
// (exit 1 via die()) with the shell's original stderr message for the first
// violated invariant.
func cmdWorkcellShadowEnumEgress(args []string) error {
	return workcellhardening.CheckShadowEnumEgress(args[0])
}

// cmdWorkcellHomeSeedProviderWrapper runs the fifty-seven home-seeding /
// provider-wrapper env-scrub checks migrated out of
// scripts/verify-invariants.sh; it fails (exit 1 via die()) with the shell's
// original stderr message for the first violated invariant.
func cmdWorkcellHomeSeedProviderWrapper(args []string) error {
	return workcellhardening.CheckHomeSeedProviderWrapper(args[0])
}

// cmdWorkcellCopilotTokenHandoff runs the twenty-nine Copilot prefix-scrub /
// token-handoff checks migrated out of scripts/verify-invariants.sh; it fails
// (exit 1 via die()) with the shell's original stderr message for the first
// violated invariant.
func cmdWorkcellCopilotTokenHandoff(args []string) error {
	return workcellhardening.CheckCopilotTokenHandoff(args[0])
}

// cmdWorkcellCopilotDockerRun runs the twenty-five Copilot / docker-run
// checks migrated out of scripts/verify-invariants.sh; it fails (exit 1 via
// die()) with the shell's original stderr message for the first violated
// invariant.
func cmdWorkcellCopilotDockerRun(args []string) error {
	return workcellhardening.CheckCopilotDockerRun(args[0])
}

// cmdWorkcellProviderLauncherAuthority runs the thirty provider-launcher-authority
// checks migrated out of scripts/verify-invariants.sh; it fails (exit 1 via
// die()) with the shell's original stderr message for the first violated
// invariant.
func cmdWorkcellProviderLauncherAuthority(args []string) error {
	return workcellhardening.CheckProviderLauncherAuthority(args[0])
}

// cmdWorkcellCopilotPolicyWrapper runs the twenty-two Copilot-policy-wrapper
// checks migrated out of scripts/verify-invariants.sh; it fails (exit 1 via
// die()) with the shell's original stderr message for the first violated
// invariant.
func cmdWorkcellCopilotPolicyWrapper(args []string) error {
	return workcellhardening.CheckCopilotPolicyWrapper(args[0])
}

// cmdWorkcellCopilotUnsafeFlags runs the thirty-one Copilot-unsafe-flag checks
// migrated out of scripts/verify-invariants.sh; it fails (exit 1 via die())
// with the shell's original stderr message for the first violated invariant.
func cmdWorkcellCopilotUnsafeFlags(args []string) error {
	return workcellhardening.CheckCopilotUnsafeFlags(args[0])
}

// cmdWorkcellCopilotReleaseVerify runs the twenty-four Copilot upstream-release
// verifier checks migrated out of scripts/verify-invariants.sh; it fails (exit 1
// via die()) with the shell's original stderr message for the first violated
// invariant.
func cmdWorkcellCopilotReleaseVerify(args []string) error {
	return workcellhardening.CheckCopilotReleaseVerify(args[0])
}

// cmdWorkcellAdapterRuleGuardBash runs the eighteen adapter-rule / Bash-guard
// checks migrated out of scripts/verify-invariants.sh; it fails (exit 1 via
// die()) with the shell's original stderr message for the first violated
// invariant.
func cmdWorkcellAdapterRuleGuardBash(args []string) error {
	return workcellhardening.CheckAdapterRuleGuardBash(args[0])
}

// cmdWorkcellInspectAssuranceLoops runs the twenty-five --inspect /
// session-assurance checks migrated out of scripts/verify-invariants.sh; it
// fails (exit 1 via die()) with the shell's original stderr message for the
// first violated invariant.
func cmdWorkcellInspectAssuranceLoops(args []string) error {
	return workcellhardening.CheckInspectAssuranceLoops(args[0])
}

// cmdWorkcellValidatorWritableState runs the twenty-three validator
// writable-state isolation checks migrated out of
// scripts/verify-invariants.sh; it fails (exit 1 via die()) with the shell's
// original stderr message for the first violated invariant.
func cmdWorkcellValidatorWritableState(args []string) error {
	return workcellhardening.CheckValidatorWritableState(args[0])
}

// cmdWorkcellHostutilEgressRg runs the twenty-one hostutil / entrypoint /
// colima-egress `rg` checks migrated out of scripts/verify-invariants.sh; it
// fails (exit 1 via die()) with the shell's original stderr message for the
// first violated invariant.
func cmdWorkcellHostutilEgressRg(args []string) error {
	return workcellhardening.CheckHostutilEgressRg(args[0])
}

// cmdWorkcellDockerfilePins runs the thirty dockerfile-pin checks
// (snapshot-TLS-bootstrap package/apt pins and unprivileged-USER defaults across
// runtime/container/Dockerfile and tools/validator/Dockerfile) migrated out of
// scripts/verify-invariants.sh; it fails (exit 1 via die()) with the shell's
// original stderr message for the first violated invariant.
func cmdWorkcellDockerfilePins(args []string) error {
	return workcellhardening.CheckDockerfilePins(args[0])
}

// cmdWorkcellValidatorDispatchLoops runs the thirteen validator-dispatch checks
// (validator Dockerfile ENV pins, validate-repo Cargo-target externalization,
// and CI-dispatch entrypoint wiring) migrated out of
// scripts/verify-invariants.sh; it fails (exit 1 via die()) with the shell's
// original stderr message for the first violated invariant.
func cmdWorkcellValidatorDispatchLoops(args []string) error {
	return workcellhardening.CheckValidatorDispatchLoops(args[0])
}

// cmdWorkcellCallerRequiredContracts runs the fifty caller-required checks (five
// CI caller files × ten UID/GID-and-isolated-writable-state needles) migrated
// out of scripts/verify-invariants.sh; it fails (exit 1 via die()) with the
// shell's original stderr message for the first violated (caller, required)
// pair.
func cmdWorkcellCallerRequiredContracts(args []string) error {
	return workcellhardening.CheckCallerRequiredContracts(args[0])
}

// cmdWorkcellFnBlockGoBlockGitEnv runs the six fnblock/goblock/gitenv checks
// (two bash function-block regex probes, one Go function-block fixed-string
// probe, and three git-env object-store-redirection pins) migrated out of
// scripts/verify-invariants.sh; it fails (exit 1 via die()) with the shell's
// original stderr message for the first violated invariant.
func cmdWorkcellFnBlockGoBlockGitEnv(args []string) error {
	return workcellhardening.CheckFnBlockGoBlockGitEnv(args[0])
}

// cmdWorkcellBuildxBuilderTrust runs the eight buildx-builder-trust checks
// (deterministic release builder, disposable validator-image cleanup across the
// local lanes, reproducible-build builder teardown, trusted Buildx endpoint /
// Docker-context resolution, and the colima-egress COLIMA_HOME pin) migrated out
// of scripts/verify-invariants.sh; it fails (exit 1 via die()) with the shell's
// original stderr message for the first violated invariant.
func cmdWorkcellBuildxBuilderTrust(args []string) error {
	return workcellhardening.CheckBuildxBuilderTrust(args[0])
}

// cmdWorkcellDocScanGoVcs runs the two doc-scan / Go-VCS-stamping checks
// (validate-repo venv-prune and go-run-env buildvcs disablement) migrated out of
// scripts/verify-invariants.sh; it fails (exit 1 via die()) with the shell's
// original stderr message for the first violated invariant.
func cmdWorkcellDocScanGoVcs(args []string) error {
	return workcellhardening.CheckDocScanGoVcs(args[0])
}

// cmdWorkcellSmokeChownTar runs the three container-smoke chown/tar checks (no
// raw recursive chown, no tar-based staging or extraction) migrated out of
// scripts/verify-invariants.sh; it fails (exit 1 via die()) with the shell's
// original stderr message for the first violated invariant.
func cmdWorkcellSmokeChownTar(args []string) error {
	return workcellhardening.CheckSmokeChownTar(args[0])
}

// cmdWorkcellDualStackApplyPlan runs the seven dual-stack allowlist-apply-plan
// checks (guarded apply path, ip6tables preflight, clear-plan render helper, and
// the render_allowlist_apply_plan clear-plan/VM-resolution/no-host-resolution
// function-block invariants) migrated out of scripts/verify-invariants.sh; it
// fails (exit 1 via die()) with the shell's original stderr message for the first
// violated invariant.
func cmdWorkcellDualStackApplyPlan(args []string) error {
	return workcellhardening.CheckDualStackApplyPlan(args[0])
}
