// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package authpolicy

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"

	"github.com/omkhar/workcell/internal/authresolve"
)

func commandStatus(opts statusOptions, stdout io.Writer) error {
	if _, err := os.Stat(opts.policyPath); os.IsNotExist(err) {
		fmt.Fprintln(stdout, "injection_policy=none")
		fmt.Fprintln(stdout, "default_injection_policy_path="+opts.policyPath)
		fmt.Fprintln(stdout, "credential_keys=none")
		fmt.Fprintln(stdout, "credential_input_kinds=none")
		fmt.Fprintln(stdout, "credential_resolvers=none")
		fmt.Fprintln(stdout, "credential_materialization=none")
		fmt.Fprintln(stdout, "credential_resolution_states=none")
		fmt.Fprintln(stdout, "provider_auth_ready_states=none")
		fmt.Fprintln(stdout, "shared_auth_ready_states=none")
		if opts.agent != "" {
			fmt.Fprintln(stdout, "provider_auth_mode=none")
			fmt.Fprintln(stdout, "provider_auth_modes=none")
			fmt.Fprintln(stdout, "shared_auth_modes=none")
			fmt.Fprintln(stdout, "github_auth_present=0")
			printBootstrapSummary(stdout, defaultBootstrapSummary(opts.agent))
		}
		return nil
	}

	policy, policySources, err := loadPolicyBundle(opts.policyPath)
	if err != nil {
		return err
	}
	selected, err := selectedCredentials(policy, opts.agent, opts.mode)
	if err != nil {
		return err
	}
	for key, raw := range selected {
		if err := validateStatusCredentialSource(key, raw, filepath.Dir(opts.policyPath)); err != nil {
			return err
		}
	}
	inputKinds := map[string]string{}
	resolvers := map[string]string{}
	materialization := map[string]string{}
	resolutionStates := map[string]string{}
	providerReadyStates := map[string]string{}
	sharedReadyStates := map[string]string{}
	for key, raw := range selected {
		inputKinds[key] = credentialInputKind(raw)
		if rawMap, ok := raw.(map[string]any); ok {
			if resolver, ok := rawMap["resolver"].(string); ok {
				resolvers[key] = resolver
			}
			if mat, ok := rawMap["materialization"].(string); ok {
				materialization[key] = mat
			}
		}
		if inputKinds[key] == "resolver" {
			state, err := resolverReadinessForStatus(key, resolvers[key])
			if err != nil {
				return err
			}
			resolutionStates[key] = state
		} else {
			resolutionStates[key] = "source"
		}
	}
	if opts.agent != "" {
		providerReadyStates, sharedReadyStates, err = explainReadyStates(policy, filepath.Dir(opts.policyPath), opts.agent, opts.mode)
		if err != nil {
			return err
		}
	} else {
		for key := range selected {
			readyState := "ready"
			if resolutionStates[key] == "configured-only" {
				readyState = "configured-only"
			}
			if _, ok := SharedCredentialKeys[key]; ok {
				sharedReadyStates[key] = readyState
			} else {
				providerReadyStates[key] = readyState
			}
		}
	}

	fmt.Fprintln(stdout, "policy_source_sha256="+compositePolicySHA256(policySources))
	fmt.Fprintln(stdout, "credential_keys="+renderModes(sortedKeys(selected)))
	fmt.Fprintln(stdout, "credential_input_kinds="+renderMap(inputKinds))
	fmt.Fprintln(stdout, "credential_resolvers="+renderMap(resolvers))
	fmt.Fprintln(stdout, "credential_materialization="+renderMap(materialization))
	fmt.Fprintln(stdout, "credential_resolution_states="+renderMap(resolutionStates))
	fmt.Fprintln(stdout, "provider_auth_ready_states="+renderMap(providerReadyStates))
	fmt.Fprintln(stdout, "shared_auth_ready_states="+renderMap(sharedReadyStates))
	if opts.agent != "" {
		providerAuthModes := providerAuthModesForStatus(opts.agent, selected, resolutionStates)
		sharedAuthModes := sharedAuthModesForStatus(selected, resolutionStates)
		providerAuthMode := "none"
		if len(providerAuthModes) > 0 {
			providerAuthMode = providerAuthModes[0]
		}
		fmt.Fprintln(stdout, "provider_auth_mode="+providerAuthMode)
		fmt.Fprintln(stdout, "provider_auth_modes="+renderModes(providerAuthModes))
		fmt.Fprintln(stdout, "shared_auth_modes="+renderModes(sharedAuthModes))
		if len(sharedAuthModes) > 0 {
			fmt.Fprintln(stdout, "github_auth_present=1")
		} else {
			fmt.Fprintln(stdout, "github_auth_present=0")
		}
		printBootstrapSummary(stdout, summarizeBootstrap(opts.agent, selected, inputKinds, resolvers, resolutionStates, providerReadyStates))
	}
	return nil
}

func explainReadyStates(policy map[string]any, policyBase string, agent string, mode string) (map[string]string, map[string]string, error) {
	providerReadyStates := map[string]string{}
	sharedReadyStates := map[string]string{}
	credentials, _ := policy["credentials"].(map[string]any)
	if credentials == nil {
		return providerReadyStates, sharedReadyStates, nil
	}

	relevant := map[string]struct{}{}
	for key := range allowedCredentialsForAgent(agent) {
		relevant[key] = struct{}{}
	}
	keys := make([]string, 0, len(relevant))
	for key := range relevant {
		keys = append(keys, key)
	}
	slices.Sort(keys)

	for _, key := range keys {
		if _, ok := credentials[key]; !ok {
			continue
		}
		report, err := explainCredentialSelection(policy, policyBase, key, agent, mode)
		if err != nil {
			return nil, nil, err
		}
		if report.readiness == "" || report.readiness == "absent" {
			continue
		}
		readyState := report.readiness
		if credentialStateIsReady(report.readiness) {
			readyState = "ready"
		}
		if _, ok := SharedCredentialKeys[key]; ok {
			sharedReadyStates[key] = readyState
		} else {
			providerReadyStates[key] = readyState
		}
	}
	return providerReadyStates, sharedReadyStates, nil
}

func resolverReadinessForStatus(key, resolver string) (string, error) {
	if resolver == "" {
		return "configured-only", nil
	}
	return authresolve.ProbeResolverReadiness(key, resolver)
}

func providerAuthModesForStatus(agent string, selected map[string]any, resolutionStates map[string]string) []string {
	providerAuthModes := make([]string, 0)
	for _, key := range statusOrder[agent] {
		if _, ok := selected[key]; ok && credentialStateIsReady(resolutionStates[key]) {
			providerAuthModes = append(providerAuthModes, key)
		}
	}
	return providerAuthModes
}

func sharedAuthModesForStatus(selected map[string]any, resolutionStates map[string]string) []string {
	sharedAuthModes := make([]string, 0)
	for _, key := range []string{"github_hosts", "github_config"} {
		if _, ok := selected[key]; ok && credentialStateIsReady(resolutionStates[key]) {
			sharedAuthModes = append(sharedAuthModes, key)
		}
	}
	return sharedAuthModes
}

func credentialStateIsReady(state string) bool {
	switch state {
	case "source", "resolved", "host-source", "ready":
		return true
	default:
		return false
	}
}

// Bootstrap-summary helpers (summarizeBootstrap, defaultBootstrapSummary,
// bootstrapSummaryForCredential, bootstrapHandoffForReadiness,
// bootstrapNextStepForReadiness, printBootstrapSummary,
// printCredentialBootstrapSummary) live in bootstrap.go.
