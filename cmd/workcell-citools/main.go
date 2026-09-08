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
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/omkhar/workcell/internal/cliexit"
	"github.com/omkhar/workcell/internal/gitconfigblocklist"
	"github.com/omkhar/workcell/internal/hardeningprofile"
	"github.com/omkhar/workcell/internal/host/validatorbind"
	"github.com/omkhar/workcell/internal/metadatautil"
	"github.com/omkhar/workcell/internal/mutation"
	"github.com/omkhar/workcell/internal/paritytree"
	"github.com/omkhar/workcell/internal/pathutil"
	"github.com/omkhar/workcell/internal/scenarios"
	"github.com/omkhar/workcell/internal/startupbench"
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

// hardeningCheck adapts a workcellhardening check taking one path
// argument to the subcommand handler signature.
func hardeningCheck(check func(string) error) func(args []string) error {
	return func(args []string) error { return check(args[0]) }
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
		{"merge-hosted-control-array-pages", "", 0, 0, cmdMergeHostedControlArrayPages},
		{"merge-hosted-control-object-pages", "FIELD", 1, 1, cmdMergeHostedControlObjectPages},
		{"list-hosted-control-ruleset-ids", "SUMMARY_PATH", 1, 1, cmdListHostedControlRulesetIDs},
		{"normalize-hosted-control-ruleset", "EXPECTED_ID", 1, 1, cmdNormalizeHostedControlRuleset},
		{"assemble-hosted-control-rulesets", "SUMMARY_PATH DETAILS_PATH OUTPUT_PATH", 3, 3, cmdAssembleHostedControlRulesets},
		{"list-hosted-control-environments", "POLICY_PATH", 1, 1, cmdListHostedControlEnvironments},
		{"verify-github-hosted-controls", "TMP_DIR REPO POLICY_PATH", 3, 3, cmdVerifyGitHubHostedControls},
		{"extract-dockerfile-arg", "DOCKERFILE_PATH ARG_NAME", 2, 2, cmdExtractDockerfileArg},
		{"extract-claude-sha", "DOCKERFILE_PATH TARGET_ARCH", 2, 2, cmdExtractClaudeSHA},
		{"extract-codex-sha", "DOCKERFILE_PATH TARGET_ARCH", 2, 2, cmdExtractCodexSHA},
		{"extract-codex-code-mode-host-sha", "DOCKERFILE_PATH TARGET_ARCH", 2, 2, cmdExtractCodexCodeModeHostSHA},
		{"extract-copilot-sha", "DOCKERFILE_PATH TARGET_ARCH", 2, 2, cmdExtractCopilotSHA},
		{"github-api-get", "URL", 1, 1, cmdGitHubAPIGet},
		{"github-release-asset", "REPOSITORY ASSET_NAME CLASS", 3, 3, cmdGitHubReleaseAsset},
		{"hadolint-manifest-checksum", "ASSET_NAME", 1, 1, cmdHadolintManifestChecksum},
		{"select-buildx-version", "CURRENT_VERSION CANDIDATE_VERSION", 2, 2, cmdSelectBuildxVersion},
		{"manifest-checksum", "MANIFEST_PATH PLATFORM", 2, 2, cmdManifestChecksum},
		{"manifest-version", "MANIFEST_PATH EXPECTED_VERSION", 2, 2, cmdManifestVersion},
		{"check-provider-bump-policy", "POLICY_PATH DOCKERFILE PROVIDERS_PACKAGE_JSON", 3, 3, cmdCheckProviderBumpPolicy},
		{"provider-bump-plan", "POLICY_PATH DOCKERFILE PROVIDERS_PACKAGE_JSON [NOW_RFC3339]", 3, 4, cmdProviderBumpPlan},
		{"apply-provider-bump-plan", "PLAN_PATH POLICY_PATH DOCKERFILE PROVIDERS_PACKAGE_JSON", 4, 4, cmdApplyProviderBumpPlan},
		{"prepare-codex-subcommand-fixture", "VERSION FIXTURE_PATH OUTPUT_PATH", 3, 3, cmdPrepareCodexSubcommandFixture},
		{"resolve-debian-bootstrap", "SNAPSHOT", 1, 1, cmdResolveDebianBootstrap},
		{"inspect-debian-bootstrap", "MANIFEST_PATH", 1, 1, cmdInspectDebianBootstrap},
		{"apply-debian-bootstrap", "PLAN_PATH REPO_ROOT", 2, 2, cmdApplyDebianBootstrap},
		{"generate-build-input-manifest", "DOCKERFILE PACKAGE_JSON PACKAGE_LOCK OUTPUT BUILD_REF SOURCE_DATE_EPOCH REQUIRE_TRACKED", 7, 7, cmdGenerateBuildInputManifest},
		{"generate-builder-environment-manifest", "OUTPUT BUILDKIT_IMAGE BUILDX_VERSION_TARGET COSIGN_VERSION_TARGET QEMU_IMAGE SYFT_VERSION_TARGET BUILDX_VERSION BUILDX_INSPECT DOCKER_VERSION_JSON QEMU_VERSION COSIGN_VERSION CURL_VERSION GIT_VERSION GZIP_VERSION SYFT_VERSION TAR_VERSION", 16, 16, cmdGenerateBuilderEnvironmentManifest},
		{"create-release-image-handoff", "ARCHIVE OUTPUT REPOSITORY RUN_ID TAG COMMIT PLATFORM IMAGE_DIGEST MANIFEST_DIGEST CONFIG_DIGEST", 10, 10, cmdCreateReleaseImageHandoff},
		{"check-pinned-inputs", "REPO_ROOT MAX_DEBIAN_SNAPSHOT_AGE_DAYS", 2, 2, cmdCheckPinnedInputs},
		{"check-validator-anchoring", "REPO_ROOT", 1, 1, cmdCheckValidatorAnchoring},
		{"check-doc-language", "REPO_ROOT", 1, 1, cmdCheckDocLanguage},
		{"check-generated-artifacts", "REPO_ROOT", 1, 1, cmdCheckGeneratedArtifacts},
		{"verify-reproducible-build", "OCI_EXPORT_A OCI_EXPORT_B REPRO_PLATFORMS REPRO_MANIFEST_PATH SOURCE_DATE_EPOCH", 5, 5, cmdVerifyReproducibleBuild},
		{"generate-reproducible-build-manifest", "OCI_EXPORT REPRO_PLATFORMS OUTPUT_PATH SOURCE_DATE_EPOCH", 4, 4, cmdGenerateReproducibleBuildManifest},
		{"verify-reproducible-build-manifest", "OCI_EXPORT REPRO_PLATFORMS MANIFEST_PATH", 3, 3, cmdVerifyReproducibleBuildManifest},
		{"canonicalize-path", "PATH", 1, 1, cmdCanonicalizePath},
		{"validate-docker-workspace-bind", "DOCKER_BIN IMAGE WORKSPACE CONTEXT CONTEXT_EXPLICIT", 5, 5, cmdValidateDockerWorkspaceBind},
		{"docker-workspace-bind-mount", "SOURCE READONLY [TARGET]", 2, 3, cmdDockerWorkspaceBindMount},
		{"coverage-percent", "REPORT_PATH MINIMUM LABEL", 3, 3, cmdCoveragePercent},
		{"coverage-executables", "MESSAGE_PATH", 1, 1, cmdCoverageExecutables},
		{"validate-json", "FILE [FILE...]", 1, -1, cmdValidateJSON},
		{"validate-toml", "FILE [FILE...]", 1, -1, cmdValidateTOML},
		{"validate-codex-routing-configs", "REPO_CONFIG MANAGED_CONFIG", 2, 2, cmdValidateCodexRoutingConfigs},
		{"workcell-codex-toml-invariants", "ROOT_DIR", 1, 1, cmdWorkcellCodexTomlInvariants},
		{"validate-requirements", "ROOT_DIR REQUIREMENTS_PATH", 2, 2, cmdValidateRequirements},
		{"validate-operator-contract", "ROOT_DIR CONTRACT_PATH REQUIREMENTS_PATH", 3, 3, cmdValidateOperatorContract},
		{"validate-public-contract", "ROOT_DIR CONTRACT_PATH", 2, 2, cmdValidatePublicContract},
		{"validate-v1-contract-freeze-git-history", "ROOT_DIR CURRENT_FREEZE_PATH", 2, 2, cmdValidateV1ContractFreezeGitHistory},
		{"scan-credential-patterns", "ROOT_DIR", 1, 1, cmdScanCredentialPatterns},
		{"run-mutation-tests", "", 0, 0, cmdRunMutationTests},
		{"mutation-score", "POLICY_PATH", 1, 1, cmdMutationScore},
		{"tree-compare", "LEFT_ROOT RIGHT_ROOT", 2, 2, cmdTreeCompare},
		{"upstream-get", "PROFILE [VERSION TARGET]", 1, 3, cmdUpstreamGet},
		{"git-config-blocklist-parity", "ROOT_DIR", 1, 1, cmdGitConfigBlocklistParity},
		{"workcell-hardening-invariants", "ROOT_DIR", 1, 1, hardeningCheck(workcellhardening.Check)},
		{"workcell-config-safety", "ROOT_DIR", 1, 1, hardeningCheck(workcellhardening.CheckConfigSafety)},
		{"workcell-runtime-invariants", "ROOT_DIR", 1, 1, hardeningCheck(workcellhardening.CheckRuntimeInvariants)},
		{"workcell-managed-profile-staging", "ROOT_DIR", 1, 1, hardeningCheck(workcellhardening.CheckManagedProfileStaging)},
		{"workcell-bootstrap-egress", "ROOT_DIR", 1, 1, hardeningCheck(workcellhardening.CheckBootstrapEgress)},
		{"workcell-bootstrap-audit", "ROOT_DIR", 1, 1, hardeningCheck(workcellhardening.CheckBootstrapAuditMetadata)},
		{"workcell-git-index-shadow", "ROOT_DIR", 1, 1, hardeningCheck(workcellhardening.CheckGitIndexShadow)},
		{"workcell-publish-pr-shadow", "ROOT_DIR", 1, 1, hardeningCheck(workcellhardening.CheckPublishPrShadowMounts)},
		{"workcell-shadow-enum-egress", "ROOT_DIR", 1, 1, hardeningCheck(workcellhardening.CheckShadowEnumEgress)},
		{"workcell-home-seed-provider-wrapper", "ROOT_DIR", 1, 1, hardeningCheck(workcellhardening.CheckHomeSeedProviderWrapper)},
		{"workcell-copilot-token-handoff", "ROOT_DIR", 1, 1, hardeningCheck(workcellhardening.CheckCopilotTokenHandoff)},
		{"workcell-copilot-docker-run", "ROOT_DIR", 1, 1, hardeningCheck(workcellhardening.CheckCopilotDockerRun)},
		{"workcell-provider-launcher-authority", "ROOT_DIR", 1, 1, hardeningCheck(workcellhardening.CheckProviderLauncherAuthority)},
		{"workcell-copilot-policy-wrapper", "ROOT_DIR", 1, 1, hardeningCheck(workcellhardening.CheckCopilotPolicyWrapper)},
		{"workcell-copilot-unsafe-flags", "ROOT_DIR", 1, 1, hardeningCheck(workcellhardening.CheckCopilotUnsafeFlags)},
		{"workcell-copilot-release-verify", "ROOT_DIR", 1, 1, hardeningCheck(workcellhardening.CheckCopilotReleaseVerify)},
		{"workcell-adapter-rule-guard-bash", "ROOT_DIR", 1, 1, hardeningCheck(workcellhardening.CheckAdapterRuleGuardBash)},
		{"workcell-inspect-assurance-loops", "ROOT_DIR", 1, 1, hardeningCheck(workcellhardening.CheckInspectAssuranceLoops)},
		{"workcell-validator-writable-state", "ROOT_DIR", 1, 1, hardeningCheck(workcellhardening.CheckValidatorWritableState)},
		{"workcell-hostutil-egress-rg", "ROOT_DIR", 1, 1, hardeningCheck(workcellhardening.CheckHostutilEgressRg)},
		{"workcell-dockerfile-pins", "ROOT_DIR", 1, 1, hardeningCheck(workcellhardening.CheckDockerfilePins)},
		{"workcell-validator-dispatch-loops", "ROOT_DIR", 1, 1, hardeningCheck(workcellhardening.CheckValidatorDispatchLoops)},
		{"workcell-caller-required-contracts", "ROOT_DIR", 1, 1, hardeningCheck(workcellhardening.CheckCallerRequiredContracts)},
		{"workcell-fnblock-goblock-gitenv", "ROOT_DIR", 1, 1, hardeningCheck(workcellhardening.CheckFnBlockGoBlockGitEnv)},
		{"workcell-buildx-builder-trust", "ROOT_DIR", 1, 1, hardeningCheck(workcellhardening.CheckBuildxBuilderTrust)},
		{"workcell-doc-scan-go-vcs", "ROOT_DIR", 1, 1, hardeningCheck(workcellhardening.CheckDocScanGoVcs)},
		{"workcell-smoke-chown-tar", "ROOT_DIR", 1, 1, hardeningCheck(workcellhardening.CheckSmokeChownTar)},
		{"workcell-dualstack-apply-plan", "ROOT_DIR", 1, 1, hardeningCheck(workcellhardening.CheckDualStackApplyPlan)},
		{"workcell-publish-base-refcheck", "ROOT_DIR", 1, 1, hardeningCheck(workcellhardening.CheckPublishBaseRefcheck)},
		{"workcell-runtime-security-posture", "ROOT_DIR", 1, 1, hardeningCheck(workcellhardening.CheckRuntimeSecurityPosture)},
		{"hardening-profile-conformance", "ROOT_DIR", 1, 1, cmdHardeningProfileConformance},
		{"workcell-smoke-apt-broker-probe", "ROOT_DIR", 1, 1, hardeningCheck(workcellhardening.CheckSmokeAptBrokerProbe)},
		{"workcell-copilot-token-handoff-cleanup", "ROOT_DIR", 1, 1, hardeningCheck(workcellhardening.CheckCopilotTokenHandoffCleanup)},
		{"workcell-provider-token-unlink", "ROOT_DIR", 1, 1, hardeningCheck(workcellhardening.CheckProviderTokenUnlink)},
		{"workcell-validate-repo-scenario-refs", "ROOT_DIR", 1, 1, hardeningCheck(workcellhardening.CheckValidateRepoScenarioRefs)},
		{"workcell-precommit-hook-exec", "ROOT_DIR", 1, 1, hardeningCheck(workcellhardening.CheckPrecommitHookExec)},
		{"workcell-docs-examples-dir", "ROOT_DIR", 1, 1, hardeningCheck(workcellhardening.CheckDocsExamplesDir)},
		{"workcell-scenario-scripts-present", "ROOT_DIR", 1, 1, hardeningCheck(workcellhardening.CheckScenarioScriptsPresent)},
		{"workcell-claude-mcp-project-servers", "SETTINGS_PATH", 1, 1, hardeningCheck(workcellhardening.CheckClaudeMcpProjectServers)},
		{"workcell-claude-guard-bash-hook", "SETTINGS_PATH", 1, 1, hardeningCheck(workcellhardening.CheckClaudeGuardBashHook)},
		{"workcell-claude-managed-bypass", "ROOT_DIR", 1, 1, hardeningCheck(workcellhardening.CheckClaudeManagedBypass)},
		{"workcell-gemini-settings-baseline", "ROOT_DIR", 1, 1, hardeningCheck(workcellhardening.CheckGeminiSettingsBaseline)},
		{"workcell-gemini-settings-guards", "ROOT_DIR", 1, 1, hardeningCheck(workcellhardening.CheckGeminiSettingsGuards)},
		{"workcell-hostgate-entrypoint-sanitize", "ROOT_DIR", 1, 1, hardeningCheck(workcellhardening.CheckHostGateEntrypointSanitize)},
		{"workcell-precommit-upstream-pin-gate", "ROOT_DIR", 1, 1, hardeningCheck(workcellhardening.CheckPrecommitUpstreamPinGate)},
		{"workcell-trusted-docker-client-rg", "ROOT_DIR", 1, 1, hardeningCheck(workcellhardening.CheckTrustedDockerClientRg)},
		{"workcell-check-batch", "ROOT_DIR CHECK[=ARG] [CHECK...]", 2, -1, cmdWorkcellCheckBatch},
	}
}

func cmdCreateReleaseImageHandoff(args []string) error {
	return metadatautil.CreateReleaseImageHandoff(args[0], args[1], args[2], args[3], args[4], args[5], args[6], args[7], args[8], args[9])
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
	if os.Args[1] == "startup-bench" {
		os.Exit(startupbench.Run(os.Args[2:], os.Stdout, os.Stderr))
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
	names := make([]string, 0, len(subcommands())+2)
	for _, sub := range subcommands() {
		names = append(names, sub.name)
	}
	// scenario-manifest is dispatched directly in main() and so is not
	// part of the subcommands() table, but it is still a known command
	// for help/error output purposes.
	names = append(names, "scenario-manifest", "startup-bench")
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

func cmdListHostedControlRulesetIDs(args []string) error {
	return metadatautil.ListHostedControlRulesetIDs(args[0], os.Stdout)
}

func cmdNormalizeHostedControlRuleset(args []string) error {
	return metadatautil.NormalizeHostedControlRuleset(os.Stdin, os.Stdout, args[0])
}

func cmdAssembleHostedControlRulesets(args []string) error {
	return metadatautil.AssembleHostedControlRulesets(args[0], args[1], args[2])
}

func cmdMergeHostedControlArrayPages(_ []string) error {
	return metadatautil.MergeHostedControlArrayPages(os.Stdin, os.Stdout)
}

func cmdMergeHostedControlObjectPages(args []string) error {
	return metadatautil.MergeHostedControlObjectPages(os.Stdin, os.Stdout, args[0])
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

func cmdExtractCodexCodeModeHostSHA(args []string) error {
	value, err := metadatautil.ExtractCodexCodeModeHostSHA(args[0], args[1])
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

func cmdHadolintManifestChecksum(args []string) error {
	manifest, err := io.ReadAll(io.LimitReader(os.Stdin, metadatautil.HadolintChecksumManifestMaxBytes+1))
	if err != nil {
		return err
	}
	value, err := metadatautil.HadolintManifestChecksum(manifest, args[0])
	if err != nil {
		return err
	}
	fmt.Println(value)
	return nil
}

func cmdGitHubReleaseAsset(args []string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	content, err := metadatautil.FetchGitHubReleaseAssetClass(ctx, os.Stdin, args[0], args[1], args[2])
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(content)
	return err
}

func cmdGitHubAPIGet(args []string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	content, err := metadatautil.FetchGitHubAPI(ctx, args[0])
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(content)
	return err
}

func cmdUpstreamGet(args []string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	content, err := metadatautil.FetchUpstream(ctx, args)
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(content)
	return err
}

func cmdResolveDebianBootstrap(args []string) error {
	pins, err := metadatautil.ResolveDefaultDebianBootstrapPins(args[0])
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(pins)
}

func cmdInspectDebianBootstrap(args []string) error {
	manifest, err := metadatautil.ReadDebianBootstrapManifest(args[0])
	if err != nil {
		return err
	}
	content, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("%s\n", content)
	return nil
}

func cmdSelectBuildxVersion(args []string) error {
	version, err := metadatautil.SelectInstallableBuildxVersion(os.Stdin, args[0], args[1])
	if err != nil {
		return err
	}
	fmt.Println(version)
	return nil
}

func cmdApplyDebianBootstrap(args []string) error {
	return metadatautil.ApplyDebianBootstrapPins(args[0], args[1])
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

func cmdPrepareCodexSubcommandFixture(args []string) error {
	return metadatautil.PrepareCodexSubcommandFixture(args[0], args[1], args[2])
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

func cmdCheckValidatorAnchoring(args []string) error {
	return metadatautil.CheckValidatorAnchoring(args[0])
}

func cmdCheckDocLanguage(args []string) error {
	return metadatautil.CheckDocLanguage(args[0])
}

func cmdCheckGeneratedArtifacts(args []string) error {
	return metadatautil.CheckGeneratedArtifacts(args[0])
}

func cmdCheckPinnedInputs(args []string) error {
	maxAge, err := strconv.Atoi(args[1])
	if err != nil {
		return err
	}
	return metadatautil.CheckPinnedInputs(metadatautil.NewPinnedInputsConfig(args[0], maxAge))
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

func cmdValidateDockerWorkspaceBind(args []string) error {
	contextExplicit, err := strconv.ParseBool(args[4])
	if err != nil {
		return fmt.Errorf("CONTEXT_EXPLICIT must be true or false")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return validatorbind.Require(ctx, validatorbind.Options{
		DockerBinary:    args[0],
		Image:           args[1],
		Workspace:       args[2],
		Context:         args[3],
		ContextExplicit: contextExplicit,
	})
}

func cmdDockerWorkspaceBindMount(args []string) error {
	readOnly, err := strconv.ParseBool(args[1])
	if err != nil {
		return fmt.Errorf("READONLY must be true or false")
	}
	target := "/workspace"
	if len(args) > 2 {
		target = args[2]
	}
	mount, err := validatorbind.MountSpec(args[0], target, readOnly)
	if err != nil {
		return err
	}
	fmt.Fprintln(os.Stdout, mount)
	return nil
}

func cmdCanonicalizePath(args []string) error {
	// Best-effort semantics with an explicit empty-input rejection,
	// preserved from the former metadatautil.CanonicalizePath wrapper.
	if args[0] == "" {
		return pathutil.ErrEmptyPath
	}
	value, err := pathutil.CanonicalizePath(args[0], pathutil.Options{})
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

func cmdValidateCodexRoutingConfigs(args []string) error {
	return metadatautil.ValidateCodexRoutingConfigs(args[0], args[1])
}

// cmdWorkcellCodexTomlInvariants runs the Codex TOML invariants migrated out
// of scripts/verify-invariants.sh in the original script order: both managed
// baselines, the routing-config parity check, the four profile-v2 layers, and
// the requirements/wrapper lockstep. The first failure returns (exit 1 via
// die()), matching the former region's `|| exit 1` semantics.
func cmdWorkcellCodexTomlInvariants(args []string) error {
	rootDir := args[0]
	codexConfig := filepath.Join(rootDir, "adapters", "codex", ".codex", "config.toml")
	managedConfig := filepath.Join(rootDir, "adapters", "codex", "managed_config.toml")
	if err := metadatautil.ValidateCodexManagedConfig(codexConfig); err != nil {
		return err
	}
	if err := metadatautil.ValidateCodexManagedConfig(managedConfig); err != nil {
		return err
	}
	if err := metadatautil.ValidateCodexRoutingConfigs(codexConfig, managedConfig); err != nil {
		return err
	}
	profileDir := filepath.Join(rootDir, "adapters", "codex", ".codex")
	layers := []struct {
		name           string
		sandboxMode    string
		approvalPolicy string
	}{
		{"strict", "workspace-write", "on-request"},
		{"development", "workspace-write", "on-request"},
		{"build", "workspace-write", "never"},
		{"breakglass", "danger-full-access", "never"},
	}
	for _, layer := range layers {
		if err := metadatautil.ValidateCodexProfileLayer(filepath.Join(profileDir, layer.name+".config.toml"), layer.sandboxMode, layer.approvalPolicy); err != nil {
			return err
		}
	}
	return metadatautil.ValidateCodexAdapterLockstep(rootDir)
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

func cmdValidateV1ContractFreezeGitHistory(args []string) error {
	return metadatautil.CheckV1ContractFreezeGitHistory(args[0], args[1])
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

// cmdHardeningProfileConformance runs the roadmap A6 hardening-profile
// conformance check: it asserts that scripts/workcell (and its egress helper)
// still apply every container-hardening and outbound-endpoint literal declared
// in the reviewed policy/hardening-profile.toml artifact, failing (exit 1 via
// die()) with a message identifying the first drifted section/literal.
func cmdHardeningProfileConformance(args []string) error {
	return hardeningprofile.Check(args[0])
}

// cmdWorkcellCheckBatch runs several migrated static checks in one
// workcell-citools process, in argv order, stopping at the first failure so
// that failure's exit code (1 via die()) and stderr message are byte-identical
// to running the failing check's individual subcommand. Batchable checks are
// exactly the table's single-argument checks: a ROOT_DIR check is named bare
// (it receives the shared ROOT_DIR), and a SETTINGS_PATH check is spelled
// CHECK=PATH. An unknown or non-batchable check name is a usage error (exit 2
// via dieUsage(), matching main's dispatch for a malformed invocation).
func cmdWorkcellCheckBatch(args []string) error {
	rootDir := args[0]
	for _, spec := range args[1:] {
		name, settingsPath, hasSettingsPath := strings.Cut(spec, "=")
		handler, argUsage := batchableCheck(name)
		switch {
		case handler == nil:
			dieUsage(fmt.Errorf("usage: %s workcell-check-batch ROOT_DIR CHECK[=ARG] [CHECK...] (unknown check %q)", os.Args[0], name))
		case hasSettingsPath && argUsage != "SETTINGS_PATH":
			dieUsage(fmt.Errorf("usage: %s workcell-check-batch ROOT_DIR CHECK[=ARG] [CHECK...] (check %q does not take =ARG)", os.Args[0], name))
		case !hasSettingsPath && argUsage == "SETTINGS_PATH":
			dieUsage(fmt.Errorf("usage: %s workcell-check-batch ROOT_DIR CHECK[=ARG] [CHECK...] (check %q requires =SETTINGS_PATH)", os.Args[0], name))
		}
		checkArg := rootDir
		if hasSettingsPath {
			checkArg = settingsPath
		}
		if err := handler([]string{checkArg}); err != nil {
			return err
		}
	}
	return nil
}

// batchableCheck resolves a check name to its handler when the subcommand
// table registers it as a single-argument ROOT_DIR or SETTINGS_PATH check —
// the only shapes workcell-check-batch can supply arguments for. The second
// return value is the matched entry's usage string ("ROOT_DIR" or
// "SETTINGS_PATH"); any other subcommand resolves to (nil, "").
func batchableCheck(name string) (func([]string) error, string) {
	for _, sub := range subcommands() {
		if sub.name != name {
			continue
		}
		if sub.minArgs == 1 && sub.maxArgs == 1 && (sub.usage == "ROOT_DIR" || sub.usage == "SETTINGS_PATH") {
			return sub.handler, sub.usage
		}
		return nil, ""
	}
	return nil, ""
}
