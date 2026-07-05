// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package authresolve

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/omkhar/workcell/internal/adapters"
	"github.com/omkhar/workcell/internal/cliexit"
	"github.com/omkhar/workcell/internal/injectionpolicy"
	"github.com/omkhar/workcell/internal/providerid"
	"github.com/omkhar/workcell/internal/rootio"
)

var (
	supportedAgents = providerid.AllProviderSet()
	supportedModes  = map[string]struct{}{
		"strict":      {},
		"development": {},
		"build":       {},
		"breakglass":  {},
	}
	supportedMaterialization = map[string]struct{}{
		"ephemeral":  {},
		"persistent": {},
	}
	allowedResolvers = map[string]map[string]struct{}{
		"codex_auth": {
			"codex-home-auth-file": {},
		},
		"claude_auth": {
			"claude-macos-keychain": {},
		},
	}
	sharedCredentialKeys      = adapters.SharedCredentialKeys()
	agentScopedCredentialKeys = adapters.AgentScopedCredentialKeysForProviders(providerid.AllProviders)
	allCredentialKeys         = credentialKeyUnion(agentScopedCredentialKeys, sharedCredentialKeys)
	rootPolicyKeys            = map[string]struct{}{
		"version":     {},
		"includes":    {},
		"documents":   {},
		"ssh":         {},
		"copies":      {},
		"credentials": {},
		"network":     {},
	}
	documentKeys = providerid.DocumentKeySet()
	// CredentialEntryKeys is the set of keys accepted in a
	// `[credentials.<name>]` table before credential resolution — the
	// resolver form.  It is the exact set validateAllowedKeys enforces in
	// run() for the table-shaped credential entry, and it is exported read-only
	// so the injection-policy schema drift check
	// (internal/injection/schema_doc_drift_test.go) can cross-check the
	// documented resolver-form key table against this parser set.
	CredentialEntryKeys = map[string]struct{}{
		"source":          {},
		"resolver":        {},
		"materialization": {},
		"providers":       {},
		"modes":           {},
	}
	systemSymlinkAllowlist = func() map[string]struct{} {
		if runtime.GOOS == "darwin" {
			return map[string]struct{}{
				"/var": {},
				"/tmp": {},
			}
		}
		return map[string]struct{}{}
	}()
)

// validateNetworkPolicy fails closed on an invalid [network] table before any
// credential is resolved, matching the render-time check in
// internal/injection.renderNetwork: only allow_endpoints/deny_endpoints are
// accepted (a mode-shaped key such as network_policy is rejected), and every
// endpoint must match the shared enforcement-helper grammar.
func validateNetworkPolicy(policy map[string]any) error {
	raw, ok := policy["network"]
	if !ok || raw == nil {
		return nil
	}
	network, ok := raw.(map[string]any)
	if !ok {
		return errors.New("network must be a TOML table")
	}
	if err := validateAllowedKeys(network, map[string]struct{}{"allow_endpoints": {}, "deny_endpoints": {}}, "network"); err != nil {
		return err
	}
	for _, key := range []string{"allow_endpoints", "deny_endpoints"} {
		value, ok := network[key]
		if !ok || value == nil {
			continue
		}
		items, ok := value.([]any)
		if !ok {
			return fmt.Errorf("network.%s must be an array of host:port strings", key)
		}
		for _, item := range items {
			endpoint, ok := item.(string)
			if !ok {
				return fmt.Errorf("network.%s must be an array of host:port strings; found non-string element: %v", key, item)
			}
			if err := injectionpolicy.ValidateEgressEndpoint(endpoint, "network."+key); err != nil {
				return err
			}
		}
	}
	return nil
}

func credentialKeyUnion(scoped map[string]map[string]struct{}, shared map[string]struct{}) map[string]struct{} {
	out := make(map[string]struct{})
	for key := range shared {
		out[key] = struct{}{}
	}
	for _, keys := range scoped {
		for key := range keys {
			out[key] = struct{}{}
		}
	}
	return out
}

// PolicySource is an alias for injectionpolicy.PolicySource — the
// canonical cross-package type.  Kept exported here for callers that
// have always imported it as authresolve.PolicySource.
type PolicySource = injectionpolicy.PolicySource

type config struct {
	policyPath     string
	agent          string
	mode           string
	resolutionMode string
	outputPolicy   string
	outputMetadata string
	outputRoot     string
}

// Run executes the resolve-credential-sources workflow.  Diagnostics
// land on stderr exactly as the bash predecessor wrote them, and the
// returned error is either nil or a *cliexit.ExitCodeError carrying
// the bash exit-code contract.  The hostutil wrapper recovers Code via
// errors.As and propagates it to os.Exit, keeping a single typed
// channel for every translated bash main in this repo.
func Run(args []string, stdout, stderr io.Writer) error {
	cfg, err := parseArgs(args)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return &cliexit.ExitCodeError{Code: 2}
	}
	if err := run(cfg); err != nil {
		fmt.Fprintln(stderr, err)
		return &cliexit.ExitCodeError{Code: 1}
	}
	return nil
}

func parseArgs(args []string) (config, error) {
	var cfg config
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "--") {
			return config{}, fmt.Errorf("unexpected argument: %s", arg)
		}
		if i+1 >= len(args) {
			return config{}, fmt.Errorf("missing value for %s", arg)
		}
		value := args[i+1]
		i++
		switch arg {
		case "--policy":
			cfg.policyPath = value
		case "--agent":
			cfg.agent = value
		case "--mode":
			cfg.mode = value
		case "--resolution-mode":
			cfg.resolutionMode = value
		case "--output-policy":
			cfg.outputPolicy = value
		case "--output-metadata":
			cfg.outputMetadata = value
		case "--output-root":
			cfg.outputRoot = value
		default:
			return config{}, fmt.Errorf("unsupported flag: %s", arg)
		}
	}

	if cfg.policyPath == "" {
		return config{}, errors.New("--policy is required")
	}
	if cfg.agent == "" {
		return config{}, errors.New("--agent is required")
	}
	if cfg.mode == "" {
		return config{}, errors.New("--mode is required")
	}
	if cfg.resolutionMode == "" {
		return config{}, errors.New("--resolution-mode is required")
	}
	if cfg.outputPolicy == "" {
		return config{}, errors.New("--output-policy is required")
	}
	if cfg.outputMetadata == "" {
		return config{}, errors.New("--output-metadata is required")
	}
	if cfg.outputRoot == "" {
		return config{}, errors.New("--output-root is required")
	}
	if _, ok := supportedAgents[cfg.agent]; !ok {
		return config{}, fmt.Errorf("unsupported value for --agent: %s", cfg.agent)
	}
	if _, ok := supportedModes[cfg.mode]; !ok {
		return config{}, fmt.Errorf("unsupported value for --mode: %s", cfg.mode)
	}
	if cfg.resolutionMode != "launch" && cfg.resolutionMode != "metadata" {
		return config{}, fmt.Errorf("unsupported value for --resolution-mode: %s", cfg.resolutionMode)
	}
	return cfg, nil
}

func run(cfg config) error {
	policyPath, err := resolvePath(cfg.policyPath)
	if err != nil {
		return err
	}
	outputPolicyPath, err := resolvePath(cfg.outputPolicy)
	if err != nil {
		return err
	}
	outputMetadataPath, err := resolvePath(cfg.outputMetadata)
	if err != nil {
		return err
	}
	outputRoot, err := resolvePath(cfg.outputRoot)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(outputRoot, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(outputRoot, 0o700); err != nil {
		return err
	}
	outputPolicyRel, err := rootio.RelativePathWithin(outputRoot, outputPolicyPath, "--output-policy")
	if err != nil {
		return err
	}
	outputMetadataRel, err := rootio.RelativePathWithin(outputRoot, outputMetadataPath, "--output-metadata")
	if err != nil {
		return err
	}
	outputRootFS, err := os.OpenRoot(outputRoot)
	if err != nil {
		return err
	}
	defer outputRootFS.Close()

	policy, policySources, err := loadPolicyBundle(policyPath)
	if err != nil {
		return err
	}
	policy = rebasePolicyFragment(policy, filepath.Dir(policyPath))
	// Validate [network] fail-closed before resolving any credential; otherwise
	// an invalid [network] (on the resolver whitelist) could stage credentials
	// and write a resolved policy before the later render rejects it.
	if err := validateNetworkPolicy(policy); err != nil {
		return err
	}
	credentials, ok := policy["credentials"]
	if credentials == nil {
		credentials = map[string]any{}
		ok = true
	}
	if !ok {
		return errors.New("credentials must be a TOML table")
	}
	credentialTable, ok := credentials.(map[string]any)
	if !ok {
		return errors.New("credentials must be a TOML table")
	}

	relevantKeys := selectedCredentialKeys(cfg.agent)
	metadata := map[string]any{
		"policy_entrypoint":            logicalPolicyPath(policyPath, filepath.Dir(policyPath)),
		"policy_sources":               policySources,
		"credential_input_kinds":       map[string]string{},
		"credential_resolvers":         map[string]string{},
		"credential_materialization":   map[string]string{},
		"credential_resolution_states": map[string]string{},
		"provider_auth_ready_states":   map[string]string{},
		"shared_auth_ready_states":     map[string]string{},
	}

	for _, key := range relevantKeys {
		raw, exists := credentialTable[key]
		if !exists || raw == nil {
			continue
		}

		rawMap, isMap := raw.(map[string]any)
		if !isMap {
			if _, shared := sharedCredentialKeys[key]; shared {
				return fmt.Errorf("credentials.%s.providers is required so shared GitHub credentials stay least-privilege", key)
			}
			source, err := validateSourcePath(raw, "credentials."+key, cwd())
			if err != nil {
				return err
			}
			if _, err := requireSecretFile(source, "credentials."+key); err != nil {
				return err
			}
			metadata["credential_input_kinds"].(map[string]string)[key] = "source"
			metadata["credential_resolution_states"].(map[string]string)[key] = "source"
			recordAuthReadyState(metadata, key, "ready")
			continue
		}

		if err := validateAllowedKeys(rawMap, CredentialEntryKeys, "credentials."+key); err != nil {
			return err
		}
		resolverName, _ := rawMap["resolver"].(string)
		providers := rawMap["providers"]
		modes := rawMap["modes"]
		if _, shared := sharedCredentialKeys[key]; shared && providers == nil {
			return fmt.Errorf("credentials.%s.providers is required so shared GitHub credentials stay least-privilege", key)
		}

		ok, err := selectedFor(providers, cfg.agent, "credentials."+key+".providers", supportedAgents)
		if err != nil {
			return err
		}
		if !ok {
			recordAuthReadyState(metadata, key, "filtered-provider")
			if resolverName != "" {
				delete(credentialTable, key)
			}
			continue
		}
		ok, err = selectedFor(modes, cfg.mode, "credentials."+key+".modes", supportedModes)
		if err != nil {
			return err
		}
		if !ok {
			recordAuthReadyState(metadata, key, "filtered-mode")
			if resolverName != "" {
				delete(credentialTable, key)
			}
			continue
		}

		_, hasSource := rawMap["source"]
		if hasSource && resolverName != "" {
			return fmt.Errorf("credentials.%s must not declare both source and resolver", key)
		}
		if !hasSource && resolverName == "" {
			return fmt.Errorf("credentials.%s must declare source or resolver", key)
		}
		if resolverName == "" {
			source, err := validateSourcePath(rawMap["source"], "credentials."+key, cwd())
			if err != nil {
				return err
			}
			if _, err := requireSecretFile(source, "credentials."+key); err != nil {
				return err
			}
			metadata["credential_input_kinds"].(map[string]string)[key] = "source"
			metadata["credential_resolution_states"].(map[string]string)[key] = "source"
			recordAuthReadyState(metadata, key, "ready")
			continue
		}

		resolverSet, ok := allowedResolvers[key]
		if !ok {
			return fmt.Errorf("credentials.%s.resolver is unsupported: %s", key, resolverName)
		}
		if _, ok := resolverSet[resolverName]; !ok {
			return fmt.Errorf("credentials.%s.resolver is unsupported: %s", key, resolverName)
		}

		materialization := "ephemeral"
		if rawMaterialization, ok := rawMap["materialization"].(string); ok && rawMaterialization != "" {
			materialization = rawMaterialization
		}
		if _, ok := supportedMaterialization[materialization]; !ok {
			return fmt.Errorf(
				"credentials.%s.materialization must be one of: ephemeral, persistent",
				key,
			)
		}
		if materialization != "ephemeral" {
			return fmt.Errorf(
				"credentials.%s.materialization must stay ephemeral for resolver-backed auth",
				key,
			)
		}

		relativeDestination := filepath.Join("resolved", "credentials", key+".json")
		destination := filepath.Join(outputRoot, relativeDestination)
		state, err := resolveCredential(key, resolverName, outputRootFS, relativeDestination, cfg.resolutionMode)
		if err != nil {
			return err
		}

		rewritten := make(map[string]any, len(rawMap))
		for k, v := range rawMap {
			rewritten[k] = v
		}
		delete(rewritten, "resolver")
		delete(rewritten, "materialization")
		rewritten["source"] = destination
		credentialTable[key] = rewritten

		metadata["credential_input_kinds"].(map[string]string)[key] = "resolver"
		metadata["credential_resolvers"].(map[string]string)[key] = resolverName
		metadata["credential_materialization"].(map[string]string)[key] = materialization
		metadata["credential_resolution_states"].(map[string]string)[key] = state
		recordAuthReadyState(metadata, key, authReadyStateForResolution(state))
	}

	renderedPolicy, err := renderPolicyTOML(policy)
	if err != nil {
		return err
	}
	if err := writeAtomicText(outputRootFS, outputPolicyRel, renderedPolicy); err != nil {
		return err
	}
	metadataJSON, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return err
	}
	if err := writeAtomicText(outputRootFS, outputMetadataRel, string(metadataJSON)+"\n"); err != nil {
		return err
	}
	return nil
}

func recordAuthReadyState(metadata map[string]any, key, state string) {
	if _, ok := sharedCredentialKeys[key]; ok {
		metadata["shared_auth_ready_states"].(map[string]string)[key] = state
		return
	}
	metadata["provider_auth_ready_states"].(map[string]string)[key] = state
}

func authReadyStateForResolution(state string) string {
	switch state {
	case "resolved", "source", "host-source":
		return "ready"
	default:
		return state
	}
}

func selectedCredentialKeys(agent string) []string {
	keys := make([]string, 0, len(sharedCredentialKeys)+len(agentScopedCredentialKeys[agent]))
	if adapters.SharedCredentialsApplyToAgent(agent) {
		for key := range sharedCredentialKeys {
			keys = append(keys, key)
		}
	}
	for key := range agentScopedCredentialKeys[agent] {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}
