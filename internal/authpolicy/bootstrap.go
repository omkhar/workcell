// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package authpolicy

import (
	"fmt"
	"io"

	"github.com/omkhar/workcell/internal/providerid"
)

// summarizeBootstrap returns the per-agent bootstrap-summary record
// that `workcell auth status` emits for the host operator.  The
// `bootstrap_*` lines downstream are byte-identical to what the bash
// predecessor produced; the per-provider switch arms mirror the
// authz contract published in docs/examples/quickstart-<provider>.md.
func summarizeBootstrap(agent string, selected map[string]any, inputKinds, resolvers, resolutionStates, providerReadyStates map[string]string) bootstrapSummary {
	switch agent {
	case providerid.Codex:
		if readiness, ok := providerReadyStates["codex_auth"]; ok {
			if credentialStateIsReady(readiness) {
				if inputKinds["codex_auth"] == "resolver" {
					return bootstrapSummary{
						state:    "ready",
						path:     "host-resolver",
						support:  "repo-required",
						handoff:  "none",
						doc:      "docs/examples/quickstart-codex.md",
						nextStep: "none",
					}
				}
				return bootstrapSummary{
					state:    "ready",
					path:     "direct-staged",
					support:  "repo-required",
					handoff:  "none",
					doc:      "docs/examples/quickstart-codex.md",
					nextStep: "none",
				}
			}
			if readiness == "configured-only" && resolvers["codex_auth"] == "codex-home-auth-file" {
				return bootstrapSummary{
					state:    "configured-only",
					path:     "host-resolver",
					support:  "repo-required",
					handoff:  "host-provider-cache",
					doc:      "docs/examples/quickstart-codex.md",
					nextStep: "stage-reviewed-codex-auth",
				}
			}
		}
		return defaultBootstrapSummary(agent)
	case providerid.Claude:
		for _, key := range []string{"claude_api_key", "claude_auth"} {
			if credentialStateIsReady(providerReadyStates[key]) {
				return bootstrapSummary{
					state:    "ready",
					path:     "direct-staged",
					support:  "repo-required",
					handoff:  "none",
					doc:      "docs/examples/quickstart-claude.md",
					nextStep: "none",
				}
			}
		}
		if resolutionStates["claude_auth"] == "configured-only" && resolvers["claude_auth"] == "claude-macos-keychain" {
			return bootstrapSummary{
				state:    "configured-only",
				path:     "host-export-scaffold",
				support:  "manual",
				handoff:  "host-export",
				doc:      "docs/examples/quickstart-claude.md",
				nextStep: "stage-reviewed-claude-auth-or-api-key",
			}
		}
		return defaultBootstrapSummary(agent)
	case providerid.Copilot:
		return bootstrapSummary{}
	case providerid.Gemini:
		for _, key := range []string{"gemini_env", "gemini_oauth"} {
			if credentialStateIsReady(providerReadyStates[key]) {
				return bootstrapSummary{
					state:    "ready",
					path:     "direct-staged",
					support:  "repo-required",
					handoff:  "none",
					doc:      "docs/examples/quickstart-gemini.md",
					nextStep: "none",
				}
			}
		}
		if credentialStateIsReady(providerReadyStates["gemini_projects"]) {
			return bootstrapSummary{
				state:    "supplemental-only",
				path:     "project-registry-supplement",
				support:  "manual",
				handoff:  "host-stage-file",
				doc:      "docs/examples/quickstart-gemini.md",
				nextStep: "stage-reviewed-gemini-env-or-oauth",
			}
		}
		if credentialStateIsReady(providerReadyStates["gcloud_adc"]) {
			return bootstrapSummary{
				state:    "supplemental-only",
				path:     "vertex-supplement",
				support:  "manual",
				handoff:  "host-stage-file",
				doc:      "docs/examples/gemini-vertex-setup.md",
				nextStep: "stage-reviewed-gemini-env-or-oauth",
			}
		}
		return defaultBootstrapSummary(agent)
	default:
		return bootstrapSummary{}
	}
}

func defaultBootstrapSummary(agent string) bootstrapSummary {
	switch agent {
	case providerid.Codex:
		return bootstrapSummary{
			state:    "not-configured",
			path:     "direct-staged",
			support:  "repo-required",
			handoff:  "host-stage-file",
			doc:      "docs/examples/quickstart-codex.md",
			nextStep: "stage-reviewed-codex-auth",
		}
	case providerid.Claude:
		return bootstrapSummary{
			state:    "not-configured",
			path:     "direct-staged",
			support:  "repo-required",
			handoff:  "host-stage-file",
			doc:      "docs/examples/quickstart-claude.md",
			nextStep: "stage-reviewed-claude-auth-or-api-key",
		}
	case providerid.Copilot:
		return bootstrapSummary{}
	case providerid.Gemini:
		return bootstrapSummary{
			state:    "not-configured",
			path:     "direct-staged",
			support:  "repo-required",
			handoff:  "host-stage-file",
			doc:      "docs/examples/quickstart-gemini.md",
			nextStep: "stage-reviewed-gemini-env-or-oauth",
		}
	default:
		return bootstrapSummary{}
	}
}

// bootstrapSummaryForCredential mirrors summarizeBootstrap but at the
// per-credential granularity used by `workcell auth status --credential
// <key>`.  Keep the two functions side-by-side: every per-provider
// branch above must have a corresponding per-credential arm here.
func bootstrapSummaryForCredential(agent, credential string, report credentialSelectionReport) bootstrapSummary {
	switch credential {
	case "codex_auth":
		if report.inputKind == "resolver" {
			if credentialStateIsReady(report.readiness) {
				return bootstrapSummary{
					state:    "ready",
					path:     "host-resolver",
					support:  "repo-required",
					handoff:  "none",
					doc:      "docs/examples/quickstart-codex.md",
					nextStep: "none",
				}
			}
			return bootstrapSummary{
				state:    report.readiness,
				path:     "host-resolver",
				support:  "repo-required",
				handoff:  "host-provider-cache",
				doc:      "docs/examples/quickstart-codex.md",
				nextStep: "stage-reviewed-codex-auth",
			}
		}
		return bootstrapSummary{
			state:    report.readiness,
			path:     "direct-staged",
			support:  "repo-required",
			handoff:  bootstrapHandoffForReadiness(report.readiness),
			doc:      "docs/examples/quickstart-codex.md",
			nextStep: bootstrapNextStepForReadiness(report.readiness, "stage-reviewed-codex-auth"),
		}
	case "claude_auth":
		if report.inputKind == "resolver" {
			if credentialStateIsReady(report.readiness) {
				return bootstrapSummary{
					state:    "ready",
					path:     "host-export-scaffold",
					support:  "manual",
					handoff:  "none",
					doc:      "docs/examples/quickstart-claude.md",
					nextStep: "none",
				}
			}
			return bootstrapSummary{
				state:    report.readiness,
				path:     "host-export-scaffold",
				support:  "manual",
				handoff:  "host-export",
				doc:      "docs/examples/quickstart-claude.md",
				nextStep: "stage-reviewed-claude-auth-or-api-key",
			}
		}
		return bootstrapSummary{
			state:    report.readiness,
			path:     "direct-staged",
			support:  "repo-required",
			handoff:  bootstrapHandoffForReadiness(report.readiness),
			doc:      "docs/examples/quickstart-claude.md",
			nextStep: bootstrapNextStepForReadiness(report.readiness, "stage-reviewed-claude-auth-or-api-key"),
		}
	case "claude_api_key", "claude_mcp":
		return bootstrapSummary{
			state:    report.readiness,
			path:     "direct-staged",
			support:  "repo-required",
			handoff:  bootstrapHandoffForReadiness(report.readiness),
			doc:      "docs/examples/quickstart-claude.md",
			nextStep: bootstrapNextStepForReadiness(report.readiness, "stage-reviewed-claude-auth-or-api-key"),
		}
	case "copilot_github_token":
		return bootstrapSummary{}
	case "gemini_env", "gemini_oauth", "gemini_projects":
		return bootstrapSummary{
			state:    report.readiness,
			path:     "direct-staged",
			support:  "repo-required",
			handoff:  bootstrapHandoffForReadiness(report.readiness),
			doc:      "docs/examples/quickstart-gemini.md",
			nextStep: bootstrapNextStepForReadiness(report.readiness, "stage-reviewed-gemini-env-or-oauth"),
		}
	case "gcloud_adc":
		return bootstrapSummary{
			state:    report.readiness,
			path:     "vertex-supplement",
			support:  "manual",
			handoff:  bootstrapHandoffForReadiness(report.readiness),
			doc:      "docs/examples/gemini-vertex-setup.md",
			nextStep: bootstrapNextStepForReadiness(report.readiness, "stage-reviewed-gemini-env-or-oauth"),
		}
	default:
		return defaultBootstrapSummary(agent)
	}
}

func bootstrapHandoffForReadiness(readiness string) string {
	if credentialStateIsReady(readiness) {
		return "none"
	}
	return "host-stage-file"
}

func bootstrapNextStepForReadiness(readiness, nextStep string) string {
	if credentialStateIsReady(readiness) {
		return "none"
	}
	return nextStep
}

func printBootstrapSummaryWithPrefix(stdout io.Writer, summary bootstrapSummary, prefix string) {
	if summary.path == "" {
		return
	}
	fmt.Fprintln(stdout, prefix+"state="+summary.state)
	fmt.Fprintln(stdout, prefix+"path="+summary.path)
	fmt.Fprintln(stdout, prefix+"support="+summary.support)
	fmt.Fprintln(stdout, prefix+"handoff="+summary.handoff)
	fmt.Fprintln(stdout, prefix+"doc="+summary.doc)
	fmt.Fprintln(stdout, prefix+"next_step="+summary.nextStep)
}

func printBootstrapSummary(stdout io.Writer, summary bootstrapSummary) {
	printBootstrapSummaryWithPrefix(stdout, summary, "provider_bootstrap_")
}

func printCredentialBootstrapSummary(stdout io.Writer, summary bootstrapSummary) {
	printBootstrapSummaryWithPrefix(stdout, summary, "bootstrap_")
}
