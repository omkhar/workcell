// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/omkhar/workcell/internal/metadatautil"
)

func die(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

func main() {
	if len(os.Args) < 2 {
		die(fmt.Errorf("usage: %s <command> [args...]", os.Args[0]))
	}

	var err error
	switch os.Args[1] {
	case "generate-control-plane-manifest":
		if len(os.Args) != 4 {
			die(fmt.Errorf("usage: %s generate-control-plane-manifest ROOT_DIR OUTPUT_PATH", os.Args[0]))
		}
		err = metadatautil.GenerateControlPlaneManifest(os.Args[2], os.Args[3])
	case "verify-control-plane-manifest":
		if len(os.Args) != 3 {
			die(fmt.Errorf("usage: %s verify-control-plane-manifest MANIFEST_PATH", os.Args[0]))
		}
		err = metadatautil.ValidateControlPlaneManifest(os.Args[2])
	case "verify-control-plane-parity":
		if len(os.Args) != 3 {
			die(fmt.Errorf("usage: %s verify-control-plane-parity MANIFEST_PATH", os.Args[0]))
		}
		rows, rowErr := metadatautil.ControlPlaneParityRows(os.Args[2])
		if rowErr != nil {
			die(rowErr)
		}
		for _, row := range rows {
			fmt.Println(row)
		}
		return
	case "check-workflows":
		if len(os.Args) != 4 {
			die(fmt.Errorf("usage: %s check-workflows ROOT_DIR POLICY_PATH", os.Args[0]))
		}
		err = metadatautil.CheckWorkflows(os.Args[2], os.Args[3])
	case "fetch-rulesets":
		if len(os.Args) != 4 {
			die(fmt.Errorf("usage: %s fetch-rulesets TMP_DIR REPO", os.Args[0]))
		}
		err = metadatautil.FetchGitHubHostedControlsRulesets(os.Args[2], os.Args[3])
	case "verify-github-hosted-controls":
		if len(os.Args) != 5 {
			die(fmt.Errorf("usage: %s verify-github-hosted-controls TMP_DIR REPO POLICY_PATH", os.Args[0]))
		}
		err = metadatautil.VerifyGitHubHostedControls(os.Args[2], os.Args[3], os.Args[4])
	case "extract-dockerfile-arg":
		if len(os.Args) != 4 {
			die(fmt.Errorf("usage: %s extract-dockerfile-arg DOCKERFILE_PATH ARG_NAME", os.Args[0]))
		}
		value, extractErr := metadatautil.ExtractDockerfileArg(os.Args[2], os.Args[3])
		if extractErr != nil {
			die(extractErr)
		}
		fmt.Println(value)
		return
	case "extract-claude-sha":
		if len(os.Args) != 4 {
			die(fmt.Errorf("usage: %s extract-claude-sha DOCKERFILE_PATH TARGET_ARCH", os.Args[0]))
		}
		value, extractErr := metadatautil.ExtractClaudeSHA(os.Args[2], os.Args[3])
		if extractErr != nil {
			die(extractErr)
		}
		fmt.Println(value)
		return
	case "extract-codex-sha":
		if len(os.Args) != 4 {
			die(fmt.Errorf("usage: %s extract-codex-sha DOCKERFILE_PATH TARGET_ARCH", os.Args[0]))
		}
		value, extractErr := metadatautil.ExtractCodexSHA(os.Args[2], os.Args[3])
		if extractErr != nil {
			die(extractErr)
		}
		fmt.Println(value)
		return
	case "manifest-checksum":
		if len(os.Args) != 4 {
			die(fmt.Errorf("usage: %s manifest-checksum MANIFEST_PATH PLATFORM", os.Args[0]))
		}
		value, extractErr := metadatautil.ManifestChecksum(os.Args[2], os.Args[3])
		if extractErr != nil {
			die(extractErr)
		}
		fmt.Println(value)
		return
	case "manifest-version":
		if len(os.Args) != 4 {
			die(fmt.Errorf("usage: %s manifest-version MANIFEST_PATH EXPECTED_VERSION", os.Args[0]))
		}
		err = metadatautil.ManifestVersion(os.Args[2], os.Args[3])
	case "check-provider-bump-policy":
		if len(os.Args) != 5 {
			die(fmt.Errorf("usage: %s check-provider-bump-policy POLICY_PATH DOCKERFILE PROVIDERS_PACKAGE_JSON", os.Args[0]))
		}
		err = metadatautil.CheckProviderBumpPolicy(os.Args[2], os.Args[3], os.Args[4])
	case "provider-bump-plan":
		if len(os.Args) != 5 && len(os.Args) != 6 {
			die(fmt.Errorf("usage: %s provider-bump-plan POLICY_PATH DOCKERFILE PROVIDERS_PACKAGE_JSON [NOW_RFC3339]", os.Args[0]))
		}
		now := time.Now().UTC()
		if len(os.Args) == 6 {
			parsedNow, parseErr := time.Parse(time.RFC3339, os.Args[5])
			if parseErr != nil {
				die(parseErr)
			}
			now = parsedNow.UTC()
		}
		plan, planErr := metadatautil.PlanProviderBumps(os.Args[2], os.Args[3], os.Args[4], now, metadatautil.DefaultProviderBumpSources(), nil)
		if planErr != nil {
			die(planErr)
		}
		content, marshalErr := json.MarshalIndent(plan, "", "  ")
		if marshalErr != nil {
			die(marshalErr)
		}
		fmt.Printf("%s\n", content)
		return
	case "apply-provider-bump-plan":
		if len(os.Args) != 5 {
			die(fmt.Errorf("usage: %s apply-provider-bump-plan PLAN_PATH DOCKERFILE PROVIDERS_PACKAGE_JSON", os.Args[0]))
		}
		err = metadatautil.ApplyProviderBumpPlan(os.Args[2], os.Args[3], os.Args[4])
	case "generate-build-input-manifest":
		if len(os.Args) != 9 {
			die(fmt.Errorf("usage: %s generate-build-input-manifest DOCKERFILE PACKAGE_JSON PACKAGE_LOCK OUTPUT BUILD_REF SOURCE_DATE_EPOCH REQUIRE_TRACKED", os.Args[0]))
		}
		sourceEpoch, convErr := strconv.ParseInt(os.Args[7], 10, 64)
		if convErr != nil {
			die(convErr)
		}
		requireTracked := os.Args[8] == "1"
		err = metadatautil.GenerateBuildInputManifest(os.Args[2], os.Args[3], os.Args[4], os.Args[5], os.Args[6], sourceEpoch, requireTracked)
	case "generate-builder-environment-manifest":
		if len(os.Args) != 18 {
			die(fmt.Errorf("usage: %s generate-builder-environment-manifest OUTPUT BUILDKIT_IMAGE BUILDX_VERSION_TARGET COSIGN_VERSION_TARGET QEMU_IMAGE SYFT_VERSION_TARGET BUILDX_VERSION BUILDX_INSPECT DOCKER_VERSION_JSON QEMU_VERSION COSIGN_VERSION CURL_VERSION GIT_VERSION GZIP_VERSION SYFT_VERSION TAR_VERSION", os.Args[0]))
		}
		err = metadatautil.GenerateBuilderEnvironmentManifest(
			os.Args[2],
			os.Args[3],
			os.Args[4],
			os.Args[5],
			os.Args[6],
			os.Args[7],
			os.Args[8],
			os.Args[9],
			os.Args[10],
			os.Args[11],
			os.Args[12],
			os.Args[13],
			os.Args[14],
			os.Args[15],
			os.Args[16],
			os.Args[17],
		)
	case "check-pinned-inputs":
		if len(os.Args) != 17 {
			die(fmt.Errorf("usage: %s check-pinned-inputs DOCKERFILE VALIDATOR_DOCKERFILE PROVIDERS_PACKAGE_JSON PROVIDERS_PACKAGE_LOCK WORKFLOWS_DIR CI_WORKFLOW RELEASE_WORKFLOW PIN_HYGIENE_WORKFLOW CODEOWNERS CODEX_REQUIREMENTS CODEX_MCP_CONFIG HOSTED_CONTROLS_POLICY HOSTED_CONTROLS_SCRIPT PROVIDER_BUMP_POLICY MAX_DEBIAN_SNAPSHOT_AGE_DAYS", os.Args[0]))
		}
		maxAge, convErr := strconv.Atoi(os.Args[16])
		if convErr != nil {
			die(convErr)
		}
		err = metadatautil.CheckPinnedInputs(metadatautil.PinnedInputsConfig{
			RuntimeDockerfilePath:         os.Args[2],
			ValidatorDockerfilePath:       os.Args[3],
			ProvidersPackageJSONPath:      os.Args[4],
			ProvidersPackageLockPath:      os.Args[5],
			WorkflowsDir:                  os.Args[6],
			CIWorkflowPath:                os.Args[7],
			ReleaseWorkflowPath:           os.Args[8],
			PinHygieneWorkflowPath:        os.Args[9],
			CodeownersPath:                os.Args[10],
			CodexRequirementsPath:         os.Args[11],
			CodexMCPConfigPath:            os.Args[12],
			HostedControlsPolicyPath:      os.Args[13],
			HostedControlsScriptPath:      os.Args[14],
			ProviderBumpPolicyPath:        os.Args[15],
			MaxDebianSnapshotAgeDays:      maxAge,
		})
	case "verify-reproducible-build":
		if len(os.Args) != 7 {
			die(fmt.Errorf("usage: %s verify-reproducible-build OCI_EXPORT_A OCI_EXPORT_B REPRO_PLATFORMS REPRO_MANIFEST_PATH SOURCE_DATE_EPOCH", os.Args[0]))
		}
		sourceEpoch, convErr := strconv.ParseInt(os.Args[6], 10, 64)
		if convErr != nil {
			die(convErr)
		}
		err = metadatautil.VerifyReproducibleBuild(os.Args[2], os.Args[3], os.Args[4], os.Args[5], sourceEpoch)
	case "generate-reproducible-build-manifest":
		if len(os.Args) != 6 {
			die(fmt.Errorf("usage: %s generate-reproducible-build-manifest OCI_EXPORT REPRO_PLATFORMS OUTPUT_PATH SOURCE_DATE_EPOCH", os.Args[0]))
		}
		sourceEpoch, convErr := strconv.ParseInt(os.Args[5], 10, 64)
		if convErr != nil {
			die(convErr)
		}
		err = metadatautil.GenerateReproducibleBuildManifest(os.Args[2], os.Args[3], os.Args[4], sourceEpoch)
	case "verify-reproducible-build-manifest":
		if len(os.Args) != 5 {
			die(fmt.Errorf("usage: %s verify-reproducible-build-manifest OCI_EXPORT REPRO_PLATFORMS MANIFEST_PATH", os.Args[0]))
		}
		err = metadatautil.VerifyReproducibleBuildManifest(os.Args[2], os.Args[3], os.Args[4])
	case "canonicalize-path":
		if len(os.Args) != 3 {
			die(fmt.Errorf("usage: %s canonicalize-path PATH", os.Args[0]))
		}
		value, canonErr := metadatautil.CanonicalizePath(os.Args[2])
		if canonErr != nil {
			die(canonErr)
		}
		fmt.Println(value)
		return
	case "coverage-percent":
		if len(os.Args) != 5 {
			die(fmt.Errorf("usage: %s coverage-percent REPORT_PATH MINIMUM LABEL", os.Args[0]))
		}
		minimum, convErr := strconv.ParseFloat(os.Args[3], 64)
		if convErr != nil {
			die(convErr)
		}
		percent, percentErr := metadatautil.CoveragePercent(os.Args[2])
		if percentErr != nil {
			die(percentErr)
		}
		if percent < minimum {
			die(fmt.Errorf("%s is %.2f%%, below the required %.2f%%", os.Args[4], percent, minimum))
		}
		fmt.Printf("%s: %.2f%%\n", os.Args[4], percent)
		return
	case "coverage-executables":
		if len(os.Args) != 3 {
			die(fmt.Errorf("usage: %s coverage-executables MESSAGE_PATH", os.Args[0]))
		}
		executables, execErr := metadatautil.CoverageExecutables(os.Args[2])
		if execErr != nil {
			die(execErr)
		}
		for _, executable := range executables {
			fmt.Println(executable)
		}
		return
	case "validate-json":
		if len(os.Args) < 3 {
			die(fmt.Errorf("usage: %s validate-json FILE [FILE...]", os.Args[0]))
		}
		err = metadatautil.ValidateJSONFiles(os.Args[2:])
	case "validate-toml":
		if len(os.Args) < 3 {
			die(fmt.Errorf("usage: %s validate-toml FILE [FILE...]", os.Args[0]))
		}
		err = metadatautil.ValidateTOMLFiles(os.Args[2:])
	case "validate-requirements":
		if len(os.Args) != 4 {
			die(fmt.Errorf("usage: %s validate-requirements ROOT_DIR REQUIREMENTS_PATH", os.Args[0]))
		}
		err = metadatautil.ValidateRequirements(os.Args[2], os.Args[3])
	case "scan-credential-patterns":
		if len(os.Args) != 3 {
			die(fmt.Errorf("usage: %s scan-credential-patterns ROOT_DIR", os.Args[0]))
		}
		err = metadatautil.ScanCredentialPatterns(os.Args[2])
	default:
		die(fmt.Errorf("unknown command: %s", os.Args[1]))
	}

	if err != nil {
		die(err)
	}
}
