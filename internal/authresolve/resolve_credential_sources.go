// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package authresolve

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/omkhar/workcell/internal/cliexit"
	"github.com/omkhar/workcell/internal/injectionpolicy"
	"github.com/omkhar/workcell/internal/providerid"
	"github.com/omkhar/workcell/internal/rootio"
	"github.com/omkhar/workcell/internal/secretfile"
)

const testClaudeExportEnv = "WORKCELL_TEST_CLAUDE_KEYCHAIN_EXPORT_FILE"

var (
	supportedAgents = map[string]struct{}{
		providerid.Codex:  {},
		providerid.Claude: {},
		providerid.Gemini: {},
	}
	supportedModes = map[string]struct{}{
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
	sharedCredentialKeys = map[string]struct{}{
		"github_hosts":  {},
		"github_config": {},
	}
	agentScopedCredentialKeys = map[string]map[string]struct{}{
		providerid.Codex: {
			"codex_auth": {},
		},
		providerid.Claude: {
			"claude_api_key": {},
			"claude_auth":    {},
			"claude_mcp":     {},
		},
		providerid.Gemini: {
			"gemini_env":      {},
			"gemini_oauth":    {},
			"gemini_projects": {},
			"gcloud_adc":      {},
		},
	}
	allCredentialKeys = map[string]struct{}{
		"codex_auth":      {},
		"claude_auth":     {},
		"claude_api_key":  {},
		"claude_mcp":      {},
		"gemini_env":      {},
		"gemini_oauth":    {},
		"gemini_projects": {},
		"gcloud_adc":      {},
		"github_hosts":    {},
		"github_config":   {},
	}
	rootPolicyKeys = map[string]struct{}{
		"version":     {},
		"includes":    {},
		"documents":   {},
		"ssh":         {},
		"copies":      {},
		"credentials": {},
	}
	credentialEntryKeys = map[string]struct{}{
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

		if err := validateAllowedKeys(rawMap, credentialEntryKeys, "credentials."+key); err != nil {
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
	for key := range sharedCredentialKeys {
		keys = append(keys, key)
	}
	for key := range agentScopedCredentialKeys[agent] {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func resolveCredential(key, resolverName string, outputRoot *os.Root, relativeDestination, resolutionMode string) (string, error) {
	if key == "codex_auth" && resolverName == "codex-home-auth-file" {
		return resolveCodexHomeAuthFile(outputRoot, relativeDestination, resolutionMode)
	}
	if key == "claude_auth" && resolverName == "claude-macos-keychain" {
		return resolveClaudeMacosKeychain(outputRoot, relativeDestination, resolutionMode)
	}
	return "", fmt.Errorf("unsupported credential resolver: %s", resolverName)
}

// ResolverSupported reports whether a credential key supports the named built-in resolver.
func ResolverSupported(key, resolverName string) bool {
	resolverSet, ok := allowedResolvers[key]
	if !ok {
		return false
	}
	_, ok = resolverSet[resolverName]
	return ok
}

// ProbeResolverReadiness reports whether a configured resolver is launch-ready on the host.
func ProbeResolverReadiness(key, resolverName string) (string, error) {
	switch {
	case key == "codex_auth" && resolverName == "codex-home-auth-file":
		return probeCodexHomeAuthFile()
	case key == "claude_auth" && resolverName == "claude-macos-keychain":
		return "configured-only", nil
	default:
		return "", fmt.Errorf("unsupported credential resolver: %s", resolverName)
	}
}

func resolveCodexHomeAuthFile(outputRoot *os.Root, relativeDestination, resolutionMode string) (string, error) {
	source, found, err := findCodexHomeAuthFile()
	if err != nil {
		return "", err
	}
	if found {
		if resolutionMode == "metadata" {
			if err := writePlaceholderUnderRoot(outputRoot, relativeDestination, "codex-home-auth-file"); err != nil {
				return "", err
			}
			return "host-source", nil
		}
		if err := materializeFileUnderRoot(source, "credentials.codex_auth", outputRoot, relativeDestination); err != nil {
			return "", err
		}
		return "resolved", nil
	}
	if resolutionMode == "metadata" {
		if err := writePlaceholderUnderRoot(outputRoot, relativeDestination, "codex-home-auth-file"); err != nil {
			return "", err
		}
		return "configured-only", nil
	}
	return "", fmt.Errorf(
		"codex host auth reuse is configured but no supported auth file is available at %s; stage codex_auth directly or remove credentials.codex_auth",
		codexHomeAuthFilePath(),
	)
}

func probeCodexHomeAuthFile() (string, error) {
	_, found, err := findCodexHomeAuthFile()
	if err != nil {
		return "", err
	}
	if found {
		return "host-source", nil
	}
	return "configured-only", nil
}

func findCodexHomeAuthFile() (string, bool, error) {
	source := codexHomeAuthFilePath()
	info, err := os.Stat(source)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, err
	}
	if info.IsDir() {
		return "", false, fmt.Errorf("credentials.codex_auth resolver path must be a file: %s", source)
	}
	if err := requireNoSymlinkInPathChain(source, "credentials.codex_auth"); err != nil {
		return "", false, err
	}
	validated, err := requireSecretFile(source, "credentials.codex_auth")
	if err != nil {
		return "", false, err
	}
	return validated, true, nil
}

func codexHomeAuthFilePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".codex", "auth.json")
	}
	return filepath.Join(home, ".codex", "auth.json")
}

func resolveClaudeMacosKeychain(outputRoot *os.Root, relativeDestination, resolutionMode string) (string, error) {
	if exportPath := os.Getenv(testClaudeExportEnv); exportPath != "" {
		source, err := validateSourcePath(exportPath, testClaudeExportEnv, cwd())
		if err != nil {
			return "", err
		}
		source, err = requireSecretFile(source, testClaudeExportEnv)
		if err != nil {
			return "", err
		}
		if err := materializeFileUnderRoot(source, testClaudeExportEnv, outputRoot, relativeDestination); err != nil {
			return "", err
		}
		return "resolved", nil
	}
	if resolutionMode == "metadata" {
		if err := writePlaceholderUnderRoot(outputRoot, relativeDestination, "claude-macos-keychain"); err != nil {
			return "", err
		}
		return "configured-only", nil
	}
	return "", errors.New("claude macOS login reuse is configured but no supported export path is available; use claude_api_key or remove credentials.claude_auth")
}

func materializeFileUnderRoot(source, label string, outputRoot *os.Root, relativeDestination string) error {
	sourceHandle, err := secretfile.Open(source, label, os.Getuid())
	if err != nil {
		return err
	}
	defer sourceHandle.Close()
	return rootio.WriteFileAtomicFromReader(outputRoot, relativeDestination, sourceHandle, 0o600, ".workcell-resolve-")
}

func writePlaceholderUnderRoot(outputRoot *os.Root, relativeDestination, resolver string) error {
	content := fmt.Sprintf(`{"resolver": %q, "workcell": %q}`+"\n", resolver, "metadata-only")
	return rootio.WriteFileAtomic(outputRoot, relativeDestination, []byte(content), 0o600, ".workcell-write-")
}

func writeAtomicText(outputRoot *os.Root, destination, content string) error {
	return rootio.WriteFileAtomic(outputRoot, destination, []byte(content), 0o600, ".workcell-write-")
}

func renderPolicyTOML(policy map[string]any) (string, error) {
	lines := []string{}

	version := policy["version"]
	if version == nil {
		version = 1
	}
	renderedVersion, err := renderTOMLValue(version)
	if err != nil {
		return "", err
	}
	lines = append(lines, "version = "+renderedVersion)

	if includes := policy["includes"]; includes != nil {
		if nonEmpty, ok := includesNonEmpty(includes); !ok || !nonEmpty {
			goto documents
		}
		renderedIncludes, err := renderTOMLValue(includes)
		if err != nil {
			return "", err
		}
		lines = append(lines, "includes = "+renderedIncludes)
	}

documents:
	if documents, ok := policy["documents"].(map[string]any); ok && len(documents) > 0 {
		lines = append(lines, "")
		lines = append(lines, "[documents]")
		for _, key := range []string{"common", providerid.Codex, providerid.Claude, providerid.Gemini} {
			if value, ok := documents[key]; ok {
				rendered, err := renderTOMLValue(value)
				if err != nil {
					return "", err
				}
				lines = append(lines, key+" = "+rendered)
			}
		}
	}

	if credentials, ok := policy["credentials"].(map[string]any); ok && len(credentials) > 0 {
		scalarEntries := map[string]any{}
		for key, value := range credentials {
			if _, ok := value.(map[string]any); !ok {
				scalarEntries[key] = value
			}
		}
		if len(scalarEntries) > 0 {
			lines = append(lines, "")
			lines = append(lines, "[credentials]")
			keys := sortedKeys(scalarEntries)
			for _, key := range keys {
				rendered, err := renderTOMLValue(scalarEntries[key])
				if err != nil {
					return "", err
				}
				lines = append(lines, key+" = "+rendered)
			}
		}
		keys := sortedKeys(credentials)
		for _, key := range keys {
			value := credentials[key]
			valueMap, ok := value.(map[string]any)
			if !ok {
				continue
			}
			lines = append(lines, "")
			lines = append(lines, "[credentials."+key+"]")
			for _, field := range sortedKeys(valueMap) {
				rendered, err := renderTOMLValue(valueMap[field])
				if err != nil {
					return "", err
				}
				lines = append(lines, field+" = "+rendered)
			}
		}
	}

	if ssh, ok := policy["ssh"].(map[string]any); ok && len(ssh) > 0 {
		lines = append(lines, "")
		lines = append(lines, "[ssh]")
		ordered := []string{"enabled", "config", "known_hosts", "identities", "providers", "modes", "allow_unsafe_config"}
		seen := map[string]struct{}{}
		for _, key := range ordered {
			if value, ok := ssh[key]; ok {
				rendered, err := renderTOMLValue(value)
				if err != nil {
					return "", err
				}
				lines = append(lines, key+" = "+rendered)
				seen[key] = struct{}{}
			}
		}
		for _, key := range sortedKeys(ssh) {
			if _, ok := seen[key]; ok {
				continue
			}
			rendered, err := renderTOMLValue(ssh[key])
			if err != nil {
				return "", err
			}
			lines = append(lines, key+" = "+rendered)
		}
	}

	if copies, ok := policy["copies"].([]any); ok && len(copies) > 0 {
		for _, entry := range copies {
			entryMap, ok := entry.(map[string]any)
			if !ok {
				return "", errors.New("copies entries must be TOML tables when rendering policy output")
			}
			lines = append(lines, "")
			lines = append(lines, "[[copies]]")
			ordered := []string{"source", "target", "classification", "providers", "modes"}
			seen := map[string]struct{}{}
			for _, key := range ordered {
				if value, ok := entryMap[key]; ok {
					rendered, err := renderTOMLValue(value)
					if err != nil {
						return "", err
					}
					lines = append(lines, key+" = "+rendered)
					seen[key] = struct{}{}
				}
			}
			for _, key := range sortedKeys(entryMap) {
				if _, ok := seen[key]; ok {
					continue
				}
				rendered, err := renderTOMLValue(entryMap[key])
				if err != nil {
					return "", err
				}
				lines = append(lines, key+" = "+rendered)
			}
		}
	}

	return strings.Join(lines, "\n") + "\n", nil
}

func renderTOMLValue(value any) (string, error) {
	switch v := value.(type) {
	case bool:
		if v {
			return "true", nil
		}
		return "false", nil
	case int:
		return strconv.Itoa(v), nil
	case string:
		return jsonQuote(v), nil
	case []string:
		items := make([]string, 0, len(v))
		for _, item := range v {
			items = append(items, jsonQuote(item))
		}
		return "[" + strings.Join(items, ", ") + "]", nil
	case []any:
		items := make([]string, 0, len(v))
		for _, item := range v {
			str, ok := item.(string)
			if !ok {
				return "", errors.New("only arrays of strings are supported in rendered policy output")
			}
			items = append(items, jsonQuote(str))
		}
		return "[" + strings.Join(items, ", ") + "]", nil
	default:
		return "", fmt.Errorf("unsupported TOML value type: %T", value)
	}
}

func jsonQuote(value string) string {
	escaped := strings.ReplaceAll(value, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `"`, `\"`)
	escaped = strings.ReplaceAll(escaped, "\n", `\n`)
	return `"` + escaped + `"`
}

func includesNonEmpty(value any) (bool, bool) {
	switch v := value.(type) {
	case []string:
		return len(v) > 0, true
	case []any:
		return len(v) > 0, true
	default:
		return false, false
	}
}

func sortedKeys(value map[string]any) []string {
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func validateSourcePath(raw any, label, base string) (string, error) {
	str, ok := raw.(string)
	if !ok || str == "" {
		return "", fmt.Errorf("%s must be a non-empty string path", label)
	}
	source := expandHostPath(str, base)
	if _, err := os.Stat(source); err != nil {
		return "", fmt.Errorf("%s does not exist: %s", label, source)
	}
	if err := requireNoSymlinkInPathChain(source, label); err != nil {
		return "", err
	}
	return source, nil
}

func requirePathWithin(root, candidate, label string) error {
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		resolvedRoot, err = filepath.Abs(root)
		if err != nil {
			return err
		}
	}
	resolvedCandidate, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		resolvedCandidate, err = filepath.Abs(candidate)
		if err != nil {
			return err
		}
	}
	rel, err := filepath.Rel(resolvedRoot, resolvedCandidate)
	if err != nil {
		return err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%s must stay within %s: %s", label, resolvedRoot, resolvedCandidate)
	}
	return nil
}

func requireNoSymlinkInPathChain(path, label string) error {
	current := path
	for {
		fi, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			if _, allowed := systemSymlinkAllowlist[current]; !allowed {
				return fmt.Errorf("%s must not be a symlink: %s", label, current)
			}
		}
		parent := filepath.Dir(current)
		if parent == current {
			return nil
		}
		current = parent
	}
}

func requireSecretFile(source, label string) (string, error) {
	handle, err := secretfile.Open(source, label, os.Getuid())
	if err != nil {
		return "", err
	}
	defer handle.Close()
	return source, nil
}

func expandHostPath(raw, base string) string {
	expanded := raw
	if strings.HasPrefix(raw, "~") {
		if home, err := expandUser(raw); err == nil {
			expanded = home
		}
	}
	if !filepath.IsAbs(expanded) {
		expanded = filepath.Join(base, expanded)
	}
	abs, err := filepath.Abs(expanded)
	if err != nil {
		return filepath.Clean(expanded)
	}
	return abs
}

func expandUser(raw string) (string, error) {
	if raw == "~" || strings.HasPrefix(raw, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if raw == "~" {
			return home, nil
		}
		return filepath.Join(home, raw[2:]), nil
	}
	if !strings.HasPrefix(raw, "~") {
		return raw, nil
	}
	slash := strings.IndexByte(raw, '/')
	userName := raw[1:]
	remainder := ""
	if slash >= 0 {
		userName = raw[1:slash]
		remainder = raw[slash+1:]
	}
	var home string
	if userName == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		home = h
	} else {
		usr, err := user.Lookup(userName)
		if err != nil {
			return "", err
		}
		home = usr.HomeDir
	}
	if remainder == "" {
		return home, nil
	}
	return filepath.Join(home, remainder), nil
}

func resolvePath(raw string) (string, error) {
	expanded := raw
	if strings.HasPrefix(raw, "~") {
		userExpanded, err := expandUser(raw)
		if err == nil {
			expanded = userExpanded
		}
	}
	abs, err := filepath.Abs(expanded)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved, nil
	}
	parent := filepath.Dir(abs)
	resolvedParent, err := filepath.EvalSymlinks(parent)
	if err == nil {
		return filepath.Join(resolvedParent, filepath.Base(abs)), nil
	}
	return abs, nil
}

func cwd() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}
