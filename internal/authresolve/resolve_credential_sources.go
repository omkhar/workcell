// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package authresolve

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/omkhar/workcell/internal/rootio"
	"github.com/omkhar/workcell/internal/secretfile"
)

const testClaudeExportEnv = "WORKCELL_TEST_CLAUDE_KEYCHAIN_EXPORT_FILE"

var (
	supportedAgents = map[string]struct{}{
		"codex":  {},
		"claude": {},
		"gemini": {},
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
		"claude_auth": {
			"claude-macos-keychain": {},
		},
	}
	sharedCredentialKeys = map[string]struct{}{
		"github_hosts":  {},
		"github_config": {},
	}
	agentScopedCredentialKeys = map[string]map[string]struct{}{
		"codex": {
			"codex_auth": {},
		},
		"claude": {
			"claude_api_key": {},
			"claude_auth":    {},
			"claude_mcp":     {},
		},
		"gemini": {
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

// PolicySource mirrors the metadata emitted by the Python helper.
type PolicySource struct {
	Path   string `json:"path"`
	Sha256 string `json:"sha256"`
}

type config struct {
	policyPath     string
	agent          string
	mode           string
	resolutionMode string
	outputPolicy   string
	outputMetadata string
	outputRoot     string
}

// Run executes the resolve-credential-sources workflow.
func Run(args []string, stdout, stderr io.Writer) int {
	cfg, err := parseArgs(args)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if err := run(cfg); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
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
	}

	for _, key := range relevantKeys {
		raw, exists := credentialTable[key]
		if !exists || raw == nil {
			continue
		}

		rawMap, isMap := raw.(map[string]any)
		if !isMap {
			metadata["credential_input_kinds"].(map[string]string)[key] = "source"
			metadata["credential_resolution_states"].(map[string]string)[key] = "source"
			continue
		}

		if err := validateAllowedKeys(rawMap, credentialEntryKeys, "credentials."+key); err != nil {
			return err
		}
		resolverName, _ := rawMap["resolver"].(string)
		providers := rawMap["providers"]
		modes := rawMap["modes"]

		ok, err := selectedFor(providers, cfg.agent, "credentials."+key+".providers", supportedAgents)
		if err != nil {
			return err
		}
		if !ok {
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
			metadata["credential_input_kinds"].(map[string]string)[key] = "source"
			metadata["credential_resolution_states"].(map[string]string)[key] = "source"
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

func selectedCredentialKeys(agent string) []string {
	keys := make([]string, 0, len(sharedCredentialKeys)+len(agentScopedCredentialKeys[agent]))
	for key := range sharedCredentialKeys {
		keys = append(keys, key)
	}
	for key := range agentScopedCredentialKeys[agent] {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func resolveCredential(key, resolverName string, outputRoot *os.Root, relativeDestination, resolutionMode string) (string, error) {
	if key == "claude_auth" && resolverName == "claude-macos-keychain" {
		return resolveClaudeMacosKeychain(outputRoot, relativeDestination, resolutionMode)
	}
	return "", fmt.Errorf("unsupported credential resolver: %s", resolverName)
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
		if err := materializeFileUnderRoot(source, outputRoot, relativeDestination); err != nil {
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
	return "", errors.New("Claude macOS login reuse is configured but no supported export path is available. Use claude_api_key or remove credentials.claude_auth.")
}

func materializeFileUnderRoot(source string, outputRoot *os.Root, relativeDestination string) error {
	sourceHandle, err := secretfile.Open(source, testClaudeExportEnv, os.Getuid())
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
		for _, key := range []string{"common", "codex", "claude", "gemini"} {
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
	sort.Strings(keys)
	return keys
}

func loadPolicyBundle(policyPath string) (map[string]any, []PolicySource, error) {
	resolvedPolicyPath := filepath.Clean(policyPath)
	entrypointRoot := filepath.Dir(resolvedPolicyPath)
	return loadPolicyBundleRecursive(resolvedPolicyPath, entrypointRoot, nil, map[string]struct{}{})
}

func loadPolicyBundleRecursive(policyPath, entrypointRoot string, activeStack []string, loadedPaths map[string]struct{}) (map[string]any, []PolicySource, error) {
	if containsPath(activeStack, policyPath) {
		cycle := append(append([]string{}, activeStack...), policyPath)
		return nil, nil, fmt.Errorf("injection policy include cycle detected: %s", strings.Join(cycle, " -> "))
	}
	if _, ok := loadedPaths[policyPath]; ok {
		return nil, nil, fmt.Errorf("injection policy includes the same file more than once: %s", policyPath)
	}
	loadedPaths[policyPath] = struct{}{}

	content, err := os.ReadFile(policyPath)
	if err != nil {
		return nil, nil, err
	}
	loaded, err := parseTOMLSubset(string(content), policyPath)
	if err != nil {
		return nil, nil, err
	}
	if err := validateAllowedKeys(loaded, rootPolicyKeys, "root policy"); err != nil {
		return nil, nil, err
	}

	version := loaded["version"]
	if version == nil {
		version = 1
	}
	if version != 1 {
		return nil, nil, fmt.Errorf("unsupported injection policy version: %v", version)
	}

	includes := loaded["includes"]
	if includes == nil {
		includes = []any{}
	}
	includeList, ok := includes.([]any)
	if !ok {
		return nil, nil, errors.New("includes must be an array of strings when specified")
	}

	merged := map[string]any{"version": 1}
	var policySources []PolicySource
	nextStack := append(append([]string{}, activeStack...), policyPath)
	for idx, include := range includeList {
		includePath, err := validatePolicyInclude(include, fmt.Sprintf("includes[%d]", idx), filepath.Dir(policyPath), entrypointRoot)
		if err != nil {
			return nil, nil, err
		}
		includedPolicy, includedSources, err := loadPolicyBundleRecursive(includePath, entrypointRoot, nextStack, loadedPaths)
		if err != nil {
			return nil, nil, err
		}
		if err := mergePolicyFragment(merged, includedPolicy, includePath); err != nil {
			return nil, nil, err
		}
		policySources = append(policySources, includedSources...)
	}

	currentPolicy := cloneMap(loaded)
	delete(currentPolicy, "includes")
	if len(activeStack) > 0 {
		currentPolicy = rebasePolicyFragment(currentPolicy, filepath.Dir(policyPath))
	}
	if err := mergePolicyFragment(merged, currentPolicy, policyPath); err != nil {
		return nil, nil, err
	}
	policySources = append(policySources, PolicySource{
		Path:   logicalPolicyPath(policyPath, entrypointRoot),
		Sha256: policySHA256(policyPath),
	})
	return merged, policySources, nil
}

func policySHA256(policyPath string) string {
	data, err := os.ReadFile(policyPath)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return "sha256:" + fmt.Sprintf("%x", sum[:])
}

func validatePolicyInclude(raw any, label, base, entrypointRoot string) (string, error) {
	source, err := validateSourcePath(raw, label, base)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(source)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s must point at a file: %s", label, source)
	}
	if err := requirePathWithin(entrypointRoot, source, label); err != nil {
		return "", err
	}
	return source, nil
}

func mergePolicyFragment(base, addition map[string]any, sourcePath string) error {
	version := addition["version"]
	if version == nil {
		version = 1
	}
	if version != 1 {
		return fmt.Errorf("unsupported injection policy version: %v", version)
	}

	for _, tableName := range []string{"documents", "ssh", "credentials"} {
		table := addition[tableName]
		if table == nil {
			continue
		}
		tableMap, ok := table.(map[string]any)
		if !ok {
			return fmt.Errorf("injection policy fragment must keep %s as a table: %s", tableName, sourcePath)
		}
		dest, ok := base[tableName]
		if !ok {
			dest = map[string]any{}
			base[tableName] = dest
		}
		destMap, ok := dest.(map[string]any)
		if !ok {
			return fmt.Errorf("injection policy merge corrupted %s: %s", tableName, sourcePath)
		}
		for key, value := range tableMap {
			if _, exists := destMap[key]; exists {
				return fmt.Errorf(
					"injection policy fragments declare the same setting more than once: %s.%s (%s)",
					tableName, key, sourcePath,
				)
			}
			destMap[key] = value
		}
	}

	copies := addition["copies"]
	if copies == nil {
		return nil
	}
	copyList, ok := copies.([]any)
	if !ok {
		return fmt.Errorf("injection policy fragment must keep copies as an array of tables: %s", sourcePath)
	}
	dest, ok := base["copies"]
	if !ok {
		dest = []any{}
		base["copies"] = dest
	}
	destList, ok := dest.([]any)
	if !ok {
		return fmt.Errorf("injection policy merge corrupted copies: %s", sourcePath)
	}
	base["copies"] = append(destList, copyList...)
	return nil
}

func loadRawPolicy(policyPath string) (map[string]any, error) {
	if _, err := os.Stat(policyPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]any{"version": 1}, nil
		}
		return nil, err
	}
	content, err := os.ReadFile(policyPath)
	if err != nil {
		return nil, err
	}
	loaded, err := parseTOMLSubset(string(content), policyPath)
	if err != nil {
		return nil, err
	}
	if err := validateAllowedKeys(loaded, rootPolicyKeys, "root policy"); err != nil {
		return nil, err
	}
	version := loaded["version"]
	if version == nil {
		loaded["version"] = 1
		return loaded, nil
	}
	if version != 1 {
		return nil, fmt.Errorf("unsupported injection policy version: %v", version)
	}
	return loaded, nil
}

func rebasePolicyFragment(policy map[string]any, fragmentDir string) map[string]any {
	rebased := map[string]any{}
	for key, value := range policy {
		switch key {
		case "documents":
			if table, ok := value.(map[string]any); ok {
				rebasedDocs := map[string]any{}
				for docKey, docValue := range table {
					rebasedDocs[docKey] = rebaseFragmentPath(docValue, fragmentDir)
				}
				rebased[key] = rebasedDocs
				continue
			}
		case "copies":
			if copies, ok := value.([]any); ok {
				rebasedCopies := make([]any, 0, len(copies))
				for _, entry := range copies {
					entryMap, ok := entry.(map[string]any)
					if !ok {
						rebasedCopies = append(rebasedCopies, entry)
						continue
					}
					rebasedEntry := cloneMap(entryMap)
					if source, ok := rebasedEntry["source"]; ok {
						rebasedEntry["source"] = rebaseFragmentPath(source, fragmentDir)
					}
					rebasedCopies = append(rebasedCopies, rebasedEntry)
				}
				rebased[key] = rebasedCopies
				continue
			}
		case "ssh":
			if table, ok := value.(map[string]any); ok {
				rebasedSSH := cloneMap(table)
				for _, sshKey := range []string{"config", "known_hosts"} {
					if sshValue, ok := rebasedSSH[sshKey]; ok {
						rebasedSSH[sshKey] = rebaseFragmentPath(sshValue, fragmentDir)
					}
				}
				switch identities := rebasedSSH["identities"].(type) {
				case []any:
					rebasedIDs := make([]any, 0, len(identities))
					for _, identity := range identities {
						rebasedIDs = append(rebasedIDs, rebaseFragmentPath(identity, fragmentDir))
					}
					rebasedSSH["identities"] = rebasedIDs
				case []string:
					rebasedIDs := make([]string, len(identities))
					for i, identity := range identities {
						rebasedIdentity, _ := rebaseFragmentPath(identity, fragmentDir).(string)
						rebasedIDs[i] = rebasedIdentity
					}
					rebasedSSH["identities"] = rebasedIDs
				}
				rebased[key] = rebasedSSH
				continue
			}
		case "credentials":
			if table, ok := value.(map[string]any); ok {
				rebasedCreds := map[string]any{}
				for credKey, credValue := range table {
					if credMap, ok := credValue.(map[string]any); ok {
						rebasedCred := cloneMap(credMap)
						if source, ok := rebasedCred["source"]; ok {
							rebasedCred["source"] = rebaseFragmentPath(source, fragmentDir)
						}
						rebasedCreds[credKey] = rebasedCred
						continue
					}
					rebasedCreds[credKey] = rebaseFragmentPath(credValue, fragmentDir)
				}
				rebased[key] = rebasedCreds
				continue
			}
		}
		rebased[key] = value
	}
	return rebased
}

func rebaseFragmentPath(raw any, fragmentDir string) any {
	str, ok := raw.(string)
	if !ok || str == "" {
		return raw
	}
	return expandHostPath(str, fragmentDir)
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

func requireNoSymlink(path, label string) error {
	if _, err := os.Lstat(path); err != nil {
		return err
	}
	if fi, err := os.Lstat(path); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s must not be a symlink: %s", label, path)
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

func parseTOMLSubset(content, policyPath string) (map[string]any, error) {
	root := map[string]any{}
	current := root
	seenTables := map[string]struct{}{}

	for lineno, rawLine := range strings.Split(content, "\n") {
		line := stripComment(rawLine)
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "[[") && strings.HasSuffix(line, "]]") {
			tableName := strings.TrimSpace(line[2 : len(line)-2])
			if tableName != "copies" {
				return nil, fmt.Errorf("%s:%d: unsupported array-of-table [%s]", policyPath, lineno+1, tableName)
			}
			copies, _ := root["copies"].([]any)
			entry := map[string]any{}
			root["copies"] = append(copies, entry)
			current = entry
			continue
		}

		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			tableName := strings.TrimSpace(line[1 : len(line)-1])
			if _, exists := seenTables[tableName]; exists {
				return nil, fmt.Errorf("%s:%d: duplicate table [%s]", policyPath, lineno+1, tableName)
			}
			seenTables[tableName] = struct{}{}

			if strings.HasPrefix(tableName, "credentials.") {
				credentialKey := strings.SplitN(tableName, ".", 2)[1]
				if _, ok := allCredentialKeys[credentialKey]; !ok {
					return nil, fmt.Errorf("%s:%d: unsupported credentials table [%s]", policyPath, lineno+1, tableName)
				}
				credentials, _ := root["credentials"].(map[string]any)
				if credentials == nil {
					credentials = map[string]any{}
					root["credentials"] = credentials
				}
				entry, _ := credentials[credentialKey].(map[string]any)
				if entry == nil {
					entry = map[string]any{}
					credentials[credentialKey] = entry
				}
				current = entry
				continue
			}

			if tableName != "documents" && tableName != "ssh" && tableName != "credentials" {
				return nil, fmt.Errorf("%s:%d: unsupported table [%s]", policyPath, lineno+1, tableName)
			}
			table, _ := root[tableName].(map[string]any)
			if table == nil {
				table = map[string]any{}
				root[tableName] = table
			}
			current = table
			continue
		}

		if !strings.Contains(line, "=") {
			return nil, fmt.Errorf("%s:%d: expected key = value", policyPath, lineno+1)
		}

		key, value, _ := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if key == "" {
			return nil, fmt.Errorf("%s:%d: empty key", policyPath, lineno+1)
		}
		if strings.Contains(key, ".") {
			return nil, fmt.Errorf("%s:%d: dotted TOML keys are not supported; use explicit [table] headers instead", policyPath, lineno+1)
		}
		if _, exists := current[key]; exists {
			return nil, fmt.Errorf("%s:%d: duplicate key: %s", policyPath, lineno+1, key)
		}

		parsed, err := parseValue(value, policyPath, lineno+1)
		if err != nil {
			return nil, err
		}
		current[key] = parsed
	}

	return root, nil
}

func parseValue(raw, policyPath string, lineno int) (any, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return nil, fmt.Errorf("%s:%d: expected a value", policyPath, lineno)
	}
	if value == "true" || value == "false" {
		return value == "true", nil
	}
	if strings.HasPrefix(value, "\"") && strings.HasSuffix(value, "\"") {
		return strconv.Unquote(value)
	}
	if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
		parsed, err := parseStringArray(value)
		if err != nil {
			return nil, fmt.Errorf("%s:%d: %v", policyPath, lineno, err)
		}
		return parsed, nil
	}
	if isDigits(value) {
		n, err := strconv.Atoi(value)
		if err != nil {
			return nil, err
		}
		return n, nil
	}
	return nil, fmt.Errorf("%s:%d: unsupported TOML value; use quoted strings, booleans, integers, or arrays of strings", policyPath, lineno)
}

func parseStringArray(raw string) ([]string, error) {
	inner := strings.TrimSpace(raw[1 : len(raw)-1])
	if inner == "" {
		return []string{}, nil
	}
	values := []string{}
	for i := 0; i < len(inner); {
		for i < len(inner) && (inner[i] == ' ' || inner[i] == '\t' || inner[i] == ',') {
			i++
		}
		if i >= len(inner) {
			break
		}
		quote := inner[i]
		if quote != '\'' && quote != '"' {
			return nil, fmt.Errorf("only arrays of strings are supported")
		}
		j := i + 1
		escaped := false
		for j < len(inner) {
			c := inner[j]
			if escaped {
				escaped = false
				j++
				continue
			}
			if c == '\\' {
				escaped = true
				j++
				continue
			}
			if c == quote {
				break
			}
			j++
		}
		if j >= len(inner) {
			return nil, fmt.Errorf("unterminated string in array")
		}
		token := inner[i : j+1]
		var str string
		var err error
		if token[0] == '\'' {
			str, err = strconv.Unquote(`"` + strings.ReplaceAll(token[1:len(token)-1], `"`, `\"`) + `"`)
		} else {
			str, err = strconv.Unquote(token)
		}
		if err != nil {
			return nil, err
		}
		values = append(values, str)
		i = j + 1
		for i < len(inner) && (inner[i] == ' ' || inner[i] == '\t') {
			i++
		}
		if i < len(inner) {
			if inner[i] != ',' {
				return nil, fmt.Errorf("expected comma between array values")
			}
			i++
		}
	}
	return values, nil
}

func stripComment(line string) string {
	escaped := false
	quoteChar := byte(0)
	result := make([]byte, 0, len(line))
	for i := 0; i < len(line); i++ {
		ch := line[i]
		if escaped {
			result = append(result, ch)
			escaped = false
			continue
		}
		if ch == '\\' && quoteChar == '"' {
			result = append(result, ch)
			escaped = true
			continue
		}
		if ch == '"' || ch == '\'' {
			if quoteChar == 0 {
				quoteChar = ch
			} else if quoteChar == ch {
				quoteChar = 0
			}
			result = append(result, ch)
			continue
		}
		if ch == '#' && quoteChar == 0 {
			break
		}
		result = append(result, ch)
	}
	return strings.TrimSpace(string(result))
}

func validateAllowedKeys(table map[string]any, allowed map[string]struct{}, label string) error {
	unknown := make([]string, 0)
	for key := range table {
		if _, ok := allowed[key]; !ok {
			unknown = append(unknown, key)
		}
	}
	sort.Strings(unknown)
	if len(unknown) > 0 {
		return fmt.Errorf("%s contains unsupported keys: %s", label, strings.Join(unknown, ", "))
	}
	return nil
}

func selectedFor(values any, current, label string, allowed map[string]struct{}) (bool, error) {
	if values == nil {
		return true, nil
	}
	switch arr := values.(type) {
	case []string:
		if len(arr) == 0 {
			return false, fmt.Errorf("%s must be a non-empty array when specified", label)
		}
		for _, str := range arr {
			if _, ok := allowed[str]; !ok {
				return false, fmt.Errorf("%s contains unsupported value: %s", label, str)
			}
			if str == current {
				return true, nil
			}
		}
		return false, nil
	case []any:
		if len(arr) == 0 {
			return false, fmt.Errorf("%s must be a non-empty array when specified", label)
		}
		for _, raw := range arr {
			str, ok := raw.(string)
			if !ok {
				return false, fmt.Errorf("%s values must be strings", label)
			}
			if _, ok := allowed[str]; !ok {
				return false, fmt.Errorf("%s contains unsupported value: %s", label, str)
			}
			if str == current {
				return true, nil
			}
		}
		return false, nil
	default:
		return false, fmt.Errorf("%s must be a non-empty array when specified", label)
	}
}

func logicalPolicyPath(policyPath, entrypointRoot string) string {
	rel, err := filepath.Rel(entrypointRoot, policyPath)
	if err != nil {
		return policyPath
	}
	return filepath.ToSlash(rel)
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

func cloneMap(value map[string]any) map[string]any {
	clone := make(map[string]any, len(value))
	for key, val := range value {
		clone[key] = val
	}
	return clone
}

func containsPath(stack []string, value string) bool {
	for _, entry := range stack {
		if entry == value {
			return true
		}
	}
	return false
}

func isDigits(value string) bool {
	if value == "" {
		return false
	}
	for i := 0; i < len(value); i++ {
		if value[i] < '0' || value[i] > '9' {
			return false
		}
	}
	return true
}

func cwd() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}
