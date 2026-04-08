// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package injection

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

const (
	sessionHomeRoot = "/state/agent-home"
	runInjectedRoot = "/state/injected"
	directMountRoot = "/opt/workcell/host-inputs"
)

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
	supportedClassifications = map[string]struct{}{
		"public": {},
		"secret": {},
	}
	reservedSSHFilnames = map[string]struct{}{
		"config":      {},
		"known_hosts": {},
	}
	riskySSHDirectives = map[string]struct{}{
		"controlmaster":       {},
		"controlpath":         {},
		"controlpersist":      {},
		"forwardagent":        {},
		"identityagent":       {},
		"include":             {},
		"knownhostscommand":   {},
		"localcommand":        {},
		"permitlocalcommand":  {},
		"pkcs11provider":      {},
		"proxycommand":        {},
		"securitykeyprovider": {},
		"sendenv":             {},
		"setenv":              {},
		"userknownhostsfile":  {},
	}
	reservedTargets = []string{
		"/state/agent-home/.codex/AGENTS.md",
		"/state/agent-home/.codex/auth.json",
		"/state/agent-home/.codex/config.toml",
		"/state/agent-home/.codex/managed_config.toml",
		"/state/agent-home/.codex/requirements.toml",
		"/state/agent-home/.codex/agents",
		"/state/agent-home/.codex/rules",
		"/state/agent-home/.codex/mcp",
		"/state/agent-home/.claude/settings.json",
		"/state/agent-home/.claude/CLAUDE.md",
		"/state/agent-home/.claude/.claude.json",
		"/state/agent-home/.claude.json",
		"/state/agent-home/.claude/.credentials.json",
		"/state/agent-home/.claude/workcell",
		"/state/agent-home/.config/claude-code/auth.json",
		"/state/agent-home/.mcp.json",
		"/state/agent-home/.gemini/settings.json",
		"/state/agent-home/.gemini/GEMINI.md",
		"/state/agent-home/.gemini/.env",
		"/state/agent-home/.gemini/oauth_creds.json",
		"/state/agent-home/.gemini/projects.json",
		"/state/agent-home/.gemini/trustedFolders.json",
		"/state/agent-home/.config/gcloud/application_default_credentials.json",
		"/state/agent-home/.config/gh/config.yml",
		"/state/agent-home/.config/gh/hosts.yml",
		"/state/agent-home/.ssh",
	}
	canonicalCredentialDestinations = map[string]string{
		"codex_auth":      "codex/auth.json",
		"claude_api_key":  "claude/api-key.txt",
		"claude_mcp":      "claude/mcp.json",
		"gemini_env":      "gemini/gemini.env",
		"gemini_oauth":    "gemini/oauth_creds.json",
		"gemini_projects": "gemini/projects.json",
		"gcloud_adc":      "gemini/gcloud-adc.json",
		"github_hosts":    "shared/github-hosts.yml",
		"github_config":   "shared/github-config.yml",
	}
	credentialContainerPaths = map[string]string{
		"codex_auth":      directMountRoot + "/credentials/codex-auth.json",
		"claude_auth":     directMountRoot + "/credentials/claude-auth.json",
		"claude_api_key":  directMountRoot + "/credentials/claude-api-key.txt",
		"claude_mcp":      directMountRoot + "/credentials/claude-mcp.json",
		"gemini_env":      directMountRoot + "/credentials/gemini.env",
		"gemini_oauth":    directMountRoot + "/credentials/gemini-oauth.json",
		"gemini_projects": directMountRoot + "/credentials/gemini-projects.json",
		"gcloud_adc":      directMountRoot + "/credentials/gcloud-adc.json",
		"github_hosts":    directMountRoot + "/credentials/github-hosts.yml",
		"github_config":   directMountRoot + "/credentials/github-config.yml",
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
	sharedCredentialKeys = map[string]struct{}{
		"github_hosts":  {},
		"github_config": {},
	}
	googleAuthEndpoints = []string{
		"accounts.google.com:443",
		"oauth2.googleapis.com:443",
		"sts.googleapis.com:443",
	}
	vertexEndpoint    = "aiplatform.googleapis.com:443"
	geminiProjectKeys = []string{
		"GOOGLE_CLOUD_PROJECT",
		"GOOGLE_CLOUD_PROJECT_ID",
	}
	geminiVertexLocationKeys = []string{
		"GOOGLE_CLOUD_LOCATION",
		"GOOGLE_CLOUD_REGION",
		"CLOUD_ML_REGION",
		"VERTEX_LOCATION",
		"VERTEX_AI_LOCATION",
	}
	geminiSupportedEnvKeys = map[string]struct{}{
		"GEMINI_API_KEY":            {},
		"GOOGLE_API_KEY":            {},
		"GOOGLE_GENAI_USE_GCA":      {},
		"GOOGLE_GENAI_USE_VERTEXAI": {},
		"GOOGLE_CLOUD_PROJECT":      {},
		"GOOGLE_CLOUD_PROJECT_ID":   {},
		"GOOGLE_CLOUD_LOCATION":     {},
		"GOOGLE_CLOUD_REGION":       {},
		"CLOUD_ML_REGION":           {},
		"VERTEX_LOCATION":           {},
		"VERTEX_AI_LOCATION":        {},
	}
	allowedRootPolicyKeys = map[string]struct{}{
		"version":     {},
		"includes":    {},
		"documents":   {},
		"ssh":         {},
		"copies":      {},
		"credentials": {},
	}
)

type PolicySource struct {
	Path   string `json:"path"`
	Sha256 string `json:"sha256"`
}

func RunRenderInjectionBundle(policyPath, agent, mode, outputRoot, policyMetadata string) error {
	resolvedPolicyPath, err := resolvePathLikePython(policyPath)
	if err != nil {
		return err
	}
	resolvedOutputRoot, err := resolvePathLikePython(outputRoot)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(resolvedOutputRoot, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(resolvedOutputRoot, 0o700); err != nil {
		return err
	}

	policy, policySources, err := loadPolicyBundle(Path(resolvedPolicyPath))
	if err != nil {
		return err
	}
	policyEntrypoint := logicalPolicyPath(Path(resolvedPolicyPath), Path(filepath.Dir(resolvedPolicyPath)))
	if policyMetadata != "" {
		policyEntrypoint, policySources, err = loadPolicyMetadataOverride(policyMetadata)
		if err != nil {
			return err
		}
	}

	renderedDocuments, err := renderDocuments(policy, Path(resolvedOutputRoot), Path(filepath.Dir(resolvedPolicyPath)))
	if err != nil {
		return err
	}
	renderedCopies, err := renderCopies(policy, Path(resolvedOutputRoot), Path(filepath.Dir(resolvedPolicyPath)), agent, mode)
	if err != nil {
		return err
	}
	renderedCredentials, err := renderCredentials(policy, Path(filepath.Dir(resolvedPolicyPath)), agent, mode)
	if err != nil {
		return err
	}
	renderedSSH, err := renderSSH(policy, Path(resolvedOutputRoot), Path(filepath.Dir(resolvedPolicyPath)), agent, mode)
	if err != nil {
		return err
	}

	manifest := map[string]any{
		"version": 1,
		"metadata": map[string]any{
			"policy_entrypoint":   policyEntrypoint,
			"policy_sha256":       effectivePolicySHA256(policySources, Path(resolvedOutputRoot), renderedDocuments, renderedCopies, renderedCredentials, renderedSSH),
			"policy_sources":      policySources,
			"credential_keys":     sortedStringKeysMap(renderedCredentials),
			"extra_endpoints":     deriveCredentialExtraEndpoints(renderedCredentials),
			"secret_copy_targets": secretCopyTargets(renderedCopies),
			"ssh_enabled":         len(renderedSSH) > 0,
			"ssh_config_assurance": func() string {
				if renderedSSH == nil {
					return "off"
				}
				if v, ok := renderedSSH["config_assurance"].(string); ok {
					return v
				}
				return "off"
			}(),
		},
		"documents":   renderedDocuments,
		"copies":      renderedCopies,
		"credentials": renderedCredentials,
		"ssh":         renderedSSH,
	}

	manifestPath := filepath.Join(resolvedOutputRoot, "manifest.json")
	if err := writeIndentedJSON(manifestPath, manifest, 0o600); err != nil {
		return err
	}
	fmt.Fprintln(os.Stdout, manifestPath)
	return nil
}

func loadPolicyBundle(policyPath Path) (map[string]any, []PolicySource, error) {
	resolvedPolicyPath, err := resolvePathLikePython(policyPath.String())
	if err != nil {
		return nil, nil, err
	}
	resolvedPolicy := Path(resolvedPolicyPath)
	loadedPaths := map[string]struct{}{}
	merged, sources, err := loadPolicyBundleRecursive(resolvedPolicy, resolvedPolicy.Parent(), nil, loadedPaths)
	if err != nil {
		return nil, nil, err
	}
	return merged, sources, nil
}

func loadPolicyBundleRecursive(policyPath, entrypointRoot Path, activeStack []Path, loadedPaths map[string]struct{}) (map[string]any, []PolicySource, error) {
	resolvedPolicyPath, err := resolvePathLikePython(policyPath.String())
	if err != nil {
		return nil, nil, err
	}
	resolved := Path(resolvedPolicyPath)
	if containsPath(activeStack, resolved) {
		cycle := append(append([]Path{}, activeStack...), resolved)
		parts := make([]string, 0, len(cycle))
		for _, p := range cycle {
			parts = append(parts, p.String())
		}
		return nil, nil, fmt.Errorf("injection policy include cycle detected: %s", strings.Join(parts, " -> "))
	}
	if _, ok := loadedPaths[resolved.String()]; ok {
		return nil, nil, fmt.Errorf("injection policy includes the same file more than once: %s", resolved)
	}
	loadedPaths[resolved.String()] = struct{}{}

	raw, err := os.ReadFile(resolved.String())
	if err != nil {
		return nil, nil, err
	}
	loaded, err := parseTOMLSubset(string(raw), resolved)
	if err != nil {
		return nil, nil, err
	}
	validateAllowedKeys(loaded, allowedRootPolicyKeys, "root policy")
	version := 1
	if rawVersion, ok := loaded["version"]; ok {
		if v, ok := rawVersion.(int); ok {
			version = v
		} else {
			return nil, nil, fmt.Errorf("unsupported injection policy version: %v", rawVersion)
		}
	}
	if version != 1 {
		return nil, nil, fmt.Errorf("unsupported injection policy version: %d", version)
	}

	includes, _ := loaded["includes"].([]any)
	if includes == nil {
		if rawIncludes, ok := loaded["includes"]; ok && rawIncludes != nil {
			var err error
			includes, err = anySlice(rawIncludes, "includes")
			if err != nil {
				return nil, nil, err
			}
		} else {
			includes = []any{}
		}
	}
	merged := map[string]any{"version": 1}
	sources := make([]PolicySource, 0)
	nextStack := append(append([]Path{}, activeStack...), resolved)
	for index, include := range includes {
		includePath, err := validatePolicyInclude(include, fmt.Sprintf("includes[%d]", index), resolved.Parent(), entrypointRoot)
		if err != nil {
			return nil, nil, err
		}
		included, includedSources, err := loadPolicyBundleRecursive(includePath, entrypointRoot, nextStack, loadedPaths)
		if err != nil {
			return nil, nil, err
		}
		if err := mergePolicyFragment(merged, included, includePath); err != nil {
			return nil, nil, err
		}
		sources = append(sources, includedSources...)
	}

	currentPolicy := cloneMap(loaded)
	delete(currentPolicy, "includes")
	if len(activeStack) > 0 {
		currentPolicy = rebasePolicyFragment(currentPolicy, resolved.Parent())
	}
	if err := mergePolicyFragment(merged, currentPolicy, resolved); err != nil {
		return nil, nil, err
	}
	sources = append(sources, PolicySource{
		Path:   logicalPolicyPath(resolved, entrypointRoot),
		Sha256: policySHA256(resolved.String()),
	})
	return merged, sources, nil
}

func loadPolicyMetadataOverride(rawPath string) (string, []PolicySource, error) {
	resolved, err := resolvePathLikePython(rawPath)
	if err != nil {
		return "", nil, err
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		return "", nil, err
	}
	var metadata map[string]any
	if err := json.Unmarshal(data, &metadata); err != nil {
		return "", nil, fmt.Errorf("policy metadata override must be valid JSON: %s (%s)", resolved, err.Error())
	}
	entrypoint, ok := metadata["policy_entrypoint"].(string)
	if !ok || entrypoint == "" {
		return "", nil, fmt.Errorf("policy metadata override must include policy_entrypoint: %s", resolved)
	}
	rawSources, ok := metadata["policy_sources"].([]any)
	if !ok || len(rawSources) == 0 {
		return "", nil, fmt.Errorf("policy metadata override must include policy_sources: %s", resolved)
	}
	sources := make([]PolicySource, 0, len(rawSources))
	for _, rawEntry := range rawSources {
		entry, ok := rawEntry.(map[string]any)
		if !ok {
			return "", nil, fmt.Errorf("policy metadata override sources must be objects: %s", resolved)
		}
		pathValue, ok := entry["path"].(string)
		if !ok || pathValue == "" {
			return "", nil, fmt.Errorf("policy metadata override source path must be a string: %s", resolved)
		}
		shaValue, ok := entry["sha256"].(string)
		if !ok || shaValue == "" {
			return "", nil, fmt.Errorf("policy metadata override source sha256 must be a string: %s", resolved)
		}
		sources = append(sources, PolicySource{Path: pathValue, Sha256: shaValue})
	}
	return entrypoint, sources, nil
}

func renderDocuments(policy map[string]any, outputRoot, policyDir Path) (map[string]string, error) {
	raw := policy["documents"]
	if raw == nil {
		return map[string]string{}, nil
	}
	documents, ok := raw.(map[string]any)
	if !ok {
		return nil, errors.New("documents must be a TOML table")
	}
	validateAllowedKeys(documents, mapKeysSet([]string{"common", "codex", "claude", "gemini"}), "documents")

	rendered := map[string]string{}
	ordered := []struct {
		key     string
		relpath string
	}{
		{"common", "documents/common.md"},
		{"codex", "documents/codex.md"},
		{"claude", "documents/claude.md"},
		{"gemini", "documents/gemini.md"},
	}
	for _, item := range ordered {
		rawValue, ok := documents[item.key]
		if !ok || rawValue == nil {
			continue
		}
		source, err := validateSourcePath(rawValue, "documents."+item.key, policyDir)
		if err != nil {
			return nil, err
		}
		if err := ensureIsFile(source, fmt.Sprintf("documents.%s", item.key)); err != nil {
			return nil, err
		}
		if err := stageFile(source, outputRoot, item.relpath); err != nil {
			return nil, err
		}
		rendered[item.key] = item.relpath
	}
	return rendered, nil
}

func renderCopies(policy map[string]any, outputRoot, policyDir Path, agent, mode string) ([]map[string]any, error) {
	raw := policy["copies"]
	if raw == nil {
		return []map[string]any{}, nil
	}
	copies, ok := raw.([]any)
	if !ok {
		return nil, errors.New("copies must be a TOML array of tables")
	}
	rendered := make([]map[string]any, 0, len(copies))
	copyIndex := 0
	for _, rawEntry := range copies {
		entry, ok := rawEntry.(map[string]any)
		if !ok {
			return nil, errors.New("each copies entry must be a table")
		}
		if err := validateAllowedKeys(entry, mapKeysSet([]string{"source", "target", "classification", "providers", "modes"}), "copies entry"); err != nil {
			return nil, err
		}
		ok, err := selectedFor(entry["providers"], agent, "copies.providers", supportedAgents)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		ok, err = selectedFor(entry["modes"], mode, "copies.modes", supportedModes)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		sourceValue, err := validateSourcePath(entry["source"], "copies.source", policyDir)
		if err != nil {
			return nil, err
		}
		targetRaw, ok := entry["target"]
		if !ok {
			targetRaw = ""
		}
		target, err := validateContainerTarget(normalizeContainerTarget(fmt.Sprint(targetRaw)))
		if err != nil {
			return nil, err
		}
		classification, ok := entry["classification"].(string)
		if !ok {
			return nil, errors.New("copies.classification is required")
		}
		kind := "file"
		relpath := fmt.Sprintf("copies/%d", copyIndex)
		mountPath := directMountRoot + "/copies/" + strconv.Itoa(copyIndex)
		copyIndex++
		fileMode, dirMode, err := classificationModes(classification, sourceValue.IsDir())
		if err != nil {
			return nil, err
		}

		var renderedSource any
		if classification == "secret" {
			if err := validateSecretTree(sourceValue, "copies.source"); err != nil {
				return nil, err
			}
			kind = func() string {
				if sourceValue.IsDir() {
					return "dir"
				}
				return "file"
			}()
			renderedSource = directMountEntry(sourceValue, mountPath)
		} else {
			kind, err = copySource(sourceValue, outputRoot.Join(relpath))
			if err != nil {
				return nil, err
			}
			renderedSource = relpath
		}

		rendered = append(rendered, map[string]any{
			"source":         renderedSource,
			"target":         target,
			"kind":           kind,
			"file_mode":      fileMode,
			"dir_mode":       dirMode,
			"classification": classification,
		})
	}
	return rendered, nil
}

func renderSSH(policy map[string]any, outputRoot, policyDir Path, agent, mode string) (map[string]any, error) {
	raw := policy["ssh"]
	if raw == nil {
		return map[string]any{}, nil
	}
	ssh, ok := raw.(map[string]any)
	if !ok {
		return nil, errors.New("ssh must be a TOML table")
	}
	if err := validateAllowedKeys(ssh, mapKeysSet([]string{"enabled", "config", "known_hosts", "identities", "providers", "modes", "allow_unsafe_config"}), "ssh"); err != nil {
		return nil, err
	}
	enabledRaw, hasEnabled := ssh["enabled"]
	hasMaterial := false
	for _, key := range []string{"config", "known_hosts", "identities"} {
		if _, ok := ssh[key]; ok {
			hasMaterial = true
		}
	}
	if hasEnabled {
		enabled, ok := enabledRaw.(bool)
		if !ok {
			return nil, errors.New("ssh.enabled must be a boolean when specified")
		}
		if !enabled {
			return map[string]any{}, nil
		}
	}
	if !hasEnabled && !hasMaterial {
		return map[string]any{}, nil
	}
	ok, err := selectedFor(ssh["providers"], agent, "ssh.providers", supportedAgents)
	if err != nil {
		return nil, err
	}
	if !ok {
		return map[string]any{}, nil
	}
	ok, err = selectedFor(ssh["modes"], mode, "ssh.modes", supportedModes)
	if err != nil {
		return nil, err
	}
	if !ok {
		return map[string]any{}, nil
	}
	rendered := map[string]any{
		"identities": []map[string]any{},
	}
	allowUnsafeRaw, hasAllowUnsafe := ssh["allow_unsafe_config"]
	allowUnsafeConfig := false
	if hasAllowUnsafe {
		val, ok := allowUnsafeRaw.(bool)
		if !ok {
			return nil, errors.New("ssh.allow_unsafe_config must be a boolean when specified")
		}
		allowUnsafeConfig = val
	}
	configRaw, hasConfig := ssh["config"]
	if !hasConfig || configRaw == nil {
		rendered["config_assurance"] = "no-config"
	} else if allowUnsafeConfig {
		rendered["config_assurance"] = "lower-assurance-unsafe-config"
	} else {
		rendered["config_assurance"] = "safe"
	}
	if hasConfig && configRaw != nil {
		source, err := validateSourcePath(configRaw, "ssh.config", policyDir)
		if err != nil {
			return nil, err
		}
		if _, err := validateSecretFile(source, "ssh.config"); err != nil {
			return nil, err
		}
		if err := validateSSHConfigSafety(source, allowUnsafeConfig); err != nil {
			return nil, err
		}
		rendered["config"] = directMountEntry(source, directMountRoot+"/ssh/config")
	}
	knownHostsRaw, hasKnownHosts := ssh["known_hosts"]
	if hasKnownHosts && knownHostsRaw != nil {
		source, err := validateSourcePath(knownHostsRaw, "ssh.known_hosts", policyDir)
		if err != nil {
			return nil, err
		}
		if _, err := validateKnownHostsFile(source, "ssh.known_hosts"); err != nil {
			return nil, err
		}
		rendered["known_hosts"] = directMountEntry(source, directMountRoot+"/ssh/known_hosts")
	}
	identitiesRaw, hasIdentities := ssh["identities"]
	identities := []any{}
	if hasIdentities {
		if identitiesRaw == nil {
			identities = []any{}
		} else {
			var err error
			identities, err = anySlice(identitiesRaw, "ssh.identities")
			if err != nil {
				return nil, err
			}
		}
	}
	renderedIdentities := make([]map[string]any, 0, len(identities))
	seenIdentityTargets := map[string]struct{}{}
	for index, rawIdentity := range identities {
		source, err := validateSourcePath(rawIdentity, fmt.Sprintf("ssh.identities[%d]", index), policyDir)
		if err != nil {
			return nil, err
		}
		if _, err := validateSecretFile(source, fmt.Sprintf("ssh.identities[%d]", index)); err != nil {
			return nil, err
		}
		if _, reserved := reservedSSHFilnames[source.Base()]; reserved {
			return nil, fmt.Errorf("ssh.identities[%d] basename collides with a reserved SSH file: %s", index, source.Base())
		}
		if _, exists := seenIdentityTargets[source.Base()]; exists {
			return nil, fmt.Errorf("ssh.identities contains duplicate target basename: %s", source.Base())
		}
		seenIdentityTargets[source.Base()] = struct{}{}
		renderedIdentities = append(renderedIdentities, map[string]any{
			"source":      source.String(),
			"mount_path":  directMountRoot + "/ssh/identity-" + strconv.Itoa(index),
			"target_name": source.Base(),
		})
	}
	rendered["identities"] = renderedIdentities
	return rendered, nil
}

func renderCredentials(policy map[string]any, policyDir Path, agent, mode string) (map[string]map[string]string, error) {
	raw := policy["credentials"]
	if raw == nil {
		return map[string]map[string]string{}, nil
	}
	credentials, ok := raw.(map[string]any)
	if !ok {
		return nil, errors.New("credentials must be a TOML table")
	}
	if err := validateAllowedKeys(credentials, mapKeysSet([]string{
		"codex_auth",
		"claude_auth",
		"claude_api_key",
		"claude_mcp",
		"gemini_env",
		"gemini_oauth",
		"gemini_projects",
		"gcloud_adc",
		"github_hosts",
		"github_config",
	}), "credentials"); err != nil {
		return nil, err
	}

	relevant := map[string]struct{}{}
	for key := range sharedCredentialKeys {
		relevant[key] = struct{}{}
	}
	if scoped, ok := agentScopedCredentialKeys[agent]; ok {
		for key := range scoped {
			relevant[key] = struct{}{}
		}
	}

	rendered := map[string]map[string]string{}
	for _, key := range sortedSetKeys(relevant) {
		rawValue, ok := credentials[key]
		if !ok || rawValue == nil {
			continue
		}

		var providers any
		var modes any
		sourceRaw := rawValue
		if entry, ok := rawValue.(map[string]any); ok {
			if err := validateAllowedKeys(entry, mapKeysSet([]string{"source", "providers", "modes"}), "credentials."+key); err != nil {
				return nil, err
			}
			sourceRaw = entry["source"]
			providers = entry["providers"]
			modes = entry["modes"]
		} else if _, ok := rawValue.(string); !ok {
			return nil, fmt.Errorf("credentials.%s must be a string path or a table", key)
		}

		if _, shared := sharedCredentialKeys[key]; shared {
			if _, isTable := rawValue.(map[string]any); isTable && providers == nil {
				return nil, fmt.Errorf("credentials.%s.providers is required so shared GitHub credentials stay least-privilege", key)
			}
		}
		ok, err := selectedFor(providers, agent, "credentials."+key+".providers", supportedAgents)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		ok, err = selectedFor(modes, mode, "credentials."+key+".modes", supportedModes)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}

		source, err := validateSourcePath(sourceRaw, "credentials."+key, policyDir)
		if err != nil {
			return nil, err
		}
		source, err = validateSecretFile(source, "credentials."+key)
		if err != nil {
			return nil, err
		}
		switch key {
		case "gemini_env":
			if _, err := validateGeminiEnvFile(source); err != nil {
				return nil, err
			}
		case "gemini_oauth":
			if _, err := validateJSONObjFile(source, "credentials.gemini_oauth"); err != nil {
				return nil, err
			}
		case "gcloud_adc":
			if err := validateGcloudADCFile(source, "credentials.gcloud_adc"); err != nil {
				return nil, err
			}
		case "gemini_projects":
			if err := validateGeminiProjectsFile(source, "credentials.gemini_projects"); err != nil {
				return nil, err
			}
		}
		rendered[key] = map[string]string{
			"source":     source.String(),
			"mount_path": credentialContainerPaths[key],
		}
	}
	return rendered, nil
}

func parseTOMLSubset(content string, policyPath Path) (map[string]any, error) {
	root := map[string]any{}
	current := root
	seenTables := map[string]struct{}{}
	lines := strings.Split(content, "\n")
	for lineno, rawLine := range lines {
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
			copies = append(copies, entry)
			root["copies"] = copies
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
				if _, ok := credentialContainerPaths[credentialKey]; !ok {
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
		parts := strings.SplitN(line, "=", 2)
		key := strings.TrimSpace(parts[0])
		if key == "" {
			return nil, fmt.Errorf("%s:%d: empty key", policyPath, lineno+1)
		}
		if strings.Contains(key, ".") {
			return nil, fmt.Errorf("%s:%d: dotted TOML keys are not supported; use explicit [table] headers instead", policyPath, lineno+1)
		}
		if _, exists := current[key]; exists {
			return nil, fmt.Errorf("%s:%d: duplicate key: %s", policyPath, lineno+1, key)
		}
		value, err := parseTOMLValue(parts[1], policyPath, lineno+1)
		if err != nil {
			return nil, err
		}
		current[key] = value
	}
	return root, nil
}

func parseTOMLValue(raw string, policyPath Path, lineno int) (any, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return nil, fmt.Errorf("%s:%d: expected a value", policyPath, lineno)
	}
	if value == "true" {
		return true, nil
	}
	if value == "false" {
		return false, nil
	}
	if strings.HasPrefix(value, "\"") && strings.HasSuffix(value, "\"") {
		return strconv.Unquote(value)
	}
	if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
		rawArray := strings.TrimSpace(value[1 : len(value)-1])
		if rawArray == "" {
			return []any{}, nil
		}
		items := splitTOMLArray(rawArray)
		result := make([]any, 0, len(items))
		for _, item := range items {
			trimmed := strings.TrimSpace(item)
			if !(strings.HasPrefix(trimmed, "\"") && strings.HasSuffix(trimmed, "\"")) {
				return nil, fmt.Errorf("%s:%d: only arrays of strings are supported", policyPath, lineno)
			}
			parsed, err := strconv.Unquote(trimmed)
			if err != nil {
				return nil, err
			}
			result = append(result, parsed)
		}
		return result, nil
	}
	if digitsOnly(value) {
		i, err := strconv.Atoi(value)
		if err != nil {
			return nil, err
		}
		return i, nil
	}
	return nil, fmt.Errorf("%s:%d: unsupported TOML value; use quoted strings, booleans, integers, or arrays of strings", policyPath, lineno)
}

func digitsOnly(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func splitTOMLArray(raw string) []string {
	items := []string{}
	start := 0
	depth := 0
	quote := rune(0)
	escaped := false
	for i, r := range raw {
		if escaped {
			escaped = false
			continue
		}
		if quote == '"' && r == '\\' {
			escaped = true
			continue
		}
		if r == '\'' || r == '"' {
			if quote == 0 {
				quote = r
			} else if quote == r {
				quote = 0
			}
			continue
		}
		if quote != 0 {
			continue
		}
		if r == '[' {
			depth++
			continue
		}
		if r == ']' {
			depth--
			continue
		}
		if r == ',' && depth == 0 {
			items = append(items, raw[start:i])
			start = i + 1
		}
	}
	items = append(items, raw[start:])
	return items
}

func stripComment(line string) string {
	escaped := false
	quoteChar := rune(0)
	var result strings.Builder
	for _, char := range line {
		if escaped {
			result.WriteRune(char)
			escaped = false
			continue
		}
		if char == '\\' && quoteChar == '"' {
			result.WriteRune(char)
			escaped = true
			continue
		}
		if char == '"' || char == '\'' {
			if quoteChar == 0 {
				quoteChar = char
			} else if quoteChar == char {
				quoteChar = 0
			}
			result.WriteRune(char)
			continue
		}
		if char == '#' && quoteChar == 0 {
			break
		}
		result.WriteRune(char)
	}
	return strings.TrimSpace(result.String())
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
	items, err := stringSlice(values, label)
	if err != nil {
		return false, err
	}
	if len(items) == 0 {
		return false, fmt.Errorf("%s must be a non-empty array when specified", label)
	}
	for _, s := range items {
		if _, ok := allowed[s]; !ok {
			return false, fmt.Errorf("%s contains unsupported value: %s", label, s)
		}
		if s == current {
			return true, nil
		}
	}
	return false, nil
}

func stringSlice(values any, label string) ([]string, error) {
	switch typed := values.(type) {
	case nil:
		return nil, nil
	case []string:
		return append([]string(nil), typed...), nil
	case []any:
		items := make([]string, 0, len(typed))
		for _, item := range typed {
			value, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("%s values must be strings", label)
			}
			items = append(items, value)
		}
		return items, nil
	default:
		return nil, fmt.Errorf("%s must be a non-empty array when specified", label)
	}
}

func anySlice(values any, label string) ([]any, error) {
	switch typed := values.(type) {
	case nil:
		return nil, nil
	case []any:
		return append([]any(nil), typed...), nil
	case []string:
		items := make([]any, 0, len(typed))
		for _, item := range typed {
			items = append(items, item)
		}
		return items, nil
	default:
		return nil, fmt.Errorf("%s must be an array of strings when specified", label)
	}
}

func ensureNoSymlinksWithin(root Path) error {
	return filepath.WalkDir(root.String(), func(current string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if current == root.String() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("directory injections must not contain symlinks: %s", current)
		}
		return nil
	})
}

func copySource(source, destination Path) (string, error) {
	info, err := os.Stat(source.String())
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		if err := ensureNoSymlinksWithin(source); err != nil {
			return "", err
		}
		if err := os.MkdirAll(destination.String(), 0o700); err != nil {
			return "", err
		}
		destinationRoot, err := os.OpenRoot(destination.String())
		if err != nil {
			return "", err
		}
		defer destinationRoot.Close()
		if err := filepath.Walk(source.String(), func(current string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(source.String(), current)
			if err != nil {
				return err
			}
			if rel == "." {
				return nil
			}
			rel = filepath.Clean(rel)
			if info.IsDir() {
				if err := destinationRoot.MkdirAll(rel, 0o755); err != nil {
					return err
				}
				return destinationRoot.Chmod(rel, 0o700)
			}
			data, err := os.ReadFile(current)
			if err != nil {
				return err
			}
			if parent := filepath.Dir(rel); parent != "." {
				if err := destinationRoot.MkdirAll(parent, 0o755); err != nil {
					return err
				}
			}
			if err := destinationRoot.WriteFile(rel, data, 0o600); err != nil {
				return err
			}
			return destinationRoot.Chmod(rel, 0o600)
		}); err != nil {
			return "", err
		}
		if err := os.Chmod(destination.String(), 0o700); err != nil {
			return "", err
		}
		return "dir", nil
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("injection source must be a file or directory: %s", source)
	}
	if err := os.MkdirAll(destination.Parent().String(), 0o755); err != nil {
		return "", err
	}
	parentRoot, err := os.OpenRoot(destination.Parent().String())
	if err != nil {
		return "", err
	}
	defer parentRoot.Close()
	data, err := os.ReadFile(source.String())
	if err != nil {
		return "", err
	}
	if err := parentRoot.WriteFile(destination.Base(), data, 0o600); err != nil {
		return "", err
	}
	if err := parentRoot.Chmod(destination.Base(), 0o600); err != nil {
		return "", err
	}
	return "file", nil
}

func stageFile(source, outputRoot Path, relpath string) error {
	root, err := os.OpenRoot(outputRoot.String())
	if err != nil {
		return err
	}
	defer root.Close()
	relpath = filepath.Clean(relpath)
	if parent := filepath.Dir(relpath); parent != "." {
		if err := root.MkdirAll(parent, 0o755); err != nil {
			return err
		}
	}
	data, err := os.ReadFile(source.String())
	if err != nil {
		return err
	}
	if err := root.WriteFile(relpath, data, 0o600); err != nil {
		return err
	}
	return root.Chmod(relpath, 0o600)
}

func directMountEntry(source Path, mountPath string) map[string]string {
	return map[string]string{
		"source":     source.String(),
		"mount_path": mountPath,
	}
}

func validateSourcePath(raw any, label string, base Path) (Path, error) {
	rawStr, ok := raw.(string)
	if !ok || rawStr == "" {
		return Path(""), fmt.Errorf("%s must be a non-empty string path", label)
	}
	source, err := expandHostPath(rawStr, base)
	if err != nil {
		return Path(""), err
	}
	if _, err := os.Stat(source.String()); err != nil {
		return Path(""), fmt.Errorf("%s does not exist: %s", label, source)
	}
	if err := requireNoSymlinkInPathChain(source, label); err != nil {
		return Path(""), err
	}
	return source, nil
}

func expandHostPath(raw string, base Path) (Path, error) {
	expanded, err := expandUserPath(raw)
	if err != nil {
		return Path(""), err
	}
	if !filepath.IsAbs(expanded) {
		expanded = filepath.Join(base.String(), expanded)
	}
	abs, err := filepath.Abs(expanded)
	if err != nil {
		return Path(""), err
	}
	return Path(abs), nil
}

func requirePathWithin(root, candidate Path, label string) error {
	resolvedRoot, err := filepath.EvalSymlinks(root.String())
	if err != nil {
		return err
	}
	resolvedCandidate, err := filepath.EvalSymlinks(candidate.String())
	if err != nil {
		return err
	}
	if resolvedCandidate != resolvedRoot && !strings.HasPrefix(resolvedCandidate, resolvedRoot+string(filepath.Separator)) {
		return fmt.Errorf("%s must stay within %s: %s", label, resolvedRoot, resolvedCandidate)
	}
	return nil
}

func requireNoSymlink(path Path, label string) error {
	if info, err := os.Lstat(path.String()); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s must not be a symlink: %s", label, path)
	}
	return nil
}

func requireNoSymlinkInPathChain(path Path, label string) error {
	current := path.String()
	for {
		info, err := os.Lstat(current)
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			if runtime.GOOS != "darwin" || (current != "/var" && current != "/tmp") {
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

func requireSecretOwnerOnly(path Path, label string) error {
	info, err := os.Lstat(path.String())
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return errors.New("unsupported file stat type")
	}
	if int(stat.Uid) != os.Getuid() {
		return fmt.Errorf("%s must be owned by uid %d: %s", label, os.Getuid(), path)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%s must not be group/world-accessible: %s", label, path)
	}
	return nil
}

func validateSecretFile(source Path, label string) (Path, error) {
	if err := requireNoSymlink(source, label); err != nil {
		return Path(""), err
	}
	info, err := os.Stat(source.String())
	if err != nil {
		return Path(""), err
	}
	if !info.Mode().IsRegular() {
		return Path(""), fmt.Errorf("%s must point at a file: %s", label, source)
	}
	if err := requireSecretOwnerOnly(source, label); err != nil {
		return Path(""), err
	}
	return source, nil
}

func validateSecretTree(source Path, label string) error {
	if err := requireNoSymlink(source, label); err != nil {
		return err
	}
	info, err := os.Stat(source.String())
	if err != nil {
		return err
	}
	if info.Mode().IsRegular() {
		_, err = validateSecretFile(source, label)
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s must point at a file or directory: %s", label, source)
	}
	if err := requireSecretOwnerOnly(source, label); err != nil {
		return err
	}
	if err := ensureNoSymlinksWithin(source); err != nil {
		return err
	}
	return filepath.WalkDir(source.String(), func(current string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if current == source.String() {
			return nil
		}
		child := Path(current)
		if err := requireNoSymlink(child, label); err != nil {
			return err
		}
		return requireSecretOwnerOnly(child, label)
	})
}

func validateKnownHostsFile(source Path, label string) (Path, error) {
	if err := requireNoSymlink(source, label); err != nil {
		return Path(""), err
	}
	info, err := os.Stat(source.String())
	if err != nil {
		return Path(""), err
	}
	if !info.Mode().IsRegular() {
		return Path(""), fmt.Errorf("%s must point at a file: %s", label, source)
	}
	if info.Mode().Perm()&0o022 != 0 {
		return Path(""), fmt.Errorf("%s must not be group/world-writable: %s", label, source)
	}
	return source, nil
}

func parseSSHDirective(line string) (string, string, bool) {
	stripped := strings.TrimSpace(line)
	if stripped == "" || strings.HasPrefix(stripped, "#") {
		return "", "", false
	}
	parts := strings.Fields(stripped)
	directive := strings.ToLower(parts[0])
	remainder := ""
	if len(parts) > 1 {
		remainder = strings.Join(parts[1:], " ")
	}
	return directive, remainder, true
}

func validateSSHConfigSafety(source Path, allowUnsafe bool) error {
	if allowUnsafe {
		return nil
	}
	data, err := os.ReadFile(source.String())
	if err != nil {
		return err
	}
	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		directive, remainder, ok := parseSSHDirective(line)
		if !ok {
			continue
		}
		if _, risky := riskySSHDirectives[directive]; risky {
			return fmt.Errorf("ssh.config contains unsafe directive %q at line %d; set ssh.allow_unsafe_config = true only when you explicitly accept lower assurance", directive, i+1)
		}
		if directive == "match" && strings.Contains(" "+strings.ToLower(remainder)+" ", " exec ") {
			return fmt.Errorf("ssh.config contains unsafe Match exec at line %d; set ssh.allow_unsafe_config = true only when you explicitly accept lower assurance", i+1)
		}
	}
	return nil
}

func validateGeminiEnvFile(source Path) (map[string]any, error) {
	values, err := parseSimpleEnvFile(source)
	if err != nil {
		return nil, err
	}
	for key, value := range values {
		if _, ok := geminiSupportedEnvKeys[key]; !ok {
			return nil, fmt.Errorf("Unsupported key in Gemini auth env file %s: %s.", source, key)
		}
		if key != "GOOGLE_GENAI_USE_GCA" && key != "GOOGLE_GENAI_USE_VERTEXAI" && strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf("Gemini auth env file %s sets %s but leaves it empty.", source, key)
		}
	}

	gcaEnabled, err := parseEnvBooleanValue(values, source, "GOOGLE_GENAI_USE_GCA")
	if err != nil {
		return nil, err
	}
	vertexEnabled, err := parseEnvBooleanValue(values, source, "GOOGLE_GENAI_USE_VERTEXAI")
	if err != nil {
		return nil, err
	}
	geminiAPIKey := strings.TrimSpace(values["GEMINI_API_KEY"])
	googleAPIKey := strings.TrimSpace(values["GOOGLE_API_KEY"])
	hasProject := false
	for _, key := range geminiProjectKeys {
		if strings.TrimSpace(values[key]) != "" {
			hasProject = true
			break
		}
	}
	hasLocation := false
	for _, key := range geminiVertexLocationKeys {
		if strings.TrimSpace(values[key]) != "" {
			hasLocation = true
			break
		}
	}
	if gcaEnabled && vertexEnabled {
		return nil, fmt.Errorf("Gemini auth env file %s enables both GOOGLE_GENAI_USE_GCA and GOOGLE_GENAI_USE_VERTEXAI. Choose exactly one auth selector.", source)
	}
	if hasLocation && !hasProject {
		return nil, fmt.Errorf("Gemini auth env file %s sets a Google Cloud location without a project.", source)
	}
	endpoints := map[string]struct{}{}
	if vertexEnabled {
		if googleAPIKey != "" || (hasProject && hasLocation) {
			endpoints[vertexEndpoint] = struct{}{}
			for _, key := range geminiVertexLocationKeys {
				location := normalizeVertexLocation(values[key])
				if location != "" {
					endpoints[location+"-aiplatform.googleapis.com:443"] = struct{}{}
				}
			}
			return map[string]any{
				"selected_auth_type": "vertex-ai",
				"extra_endpoints":    sortedSetKeys(endpoints),
			}, nil
		}
		return nil, fmt.Errorf("Gemini auth env file %s enables GOOGLE_GENAI_USE_VERTEXAI=true without either GOOGLE_API_KEY or both GOOGLE_CLOUD_PROJECT and GOOGLE_CLOUD_LOCATION.", source)
	}
	if googleAPIKey != "" {
		return nil, fmt.Errorf("Gemini auth env file %s sets GOOGLE_API_KEY without GOOGLE_GENAI_USE_VERTEXAI=true.", source)
	}
	if gcaEnabled {
		return map[string]any{
			"selected_auth_type": "oauth-personal",
			"extra_endpoints":    append([]string{}, googleAuthEndpoints...),
		}, nil
	}
	if geminiAPIKey != "" {
		return map[string]any{
			"selected_auth_type": "gemini-api-key",
			"extra_endpoints":    []string{},
		}, nil
	}
	return nil, fmt.Errorf("Gemini auth env file %s does not configure a supported Gemini auth mode. Use GEMINI_API_KEY, GOOGLE_GENAI_USE_GCA=true, or GOOGLE_GENAI_USE_VERTEXAI=true with GOOGLE_API_KEY or both GOOGLE_CLOUD_PROJECT and GOOGLE_CLOUD_LOCATION.", source)
}

func parseSimpleEnvFile(source Path) (map[string]string, error) {
	values := map[string]string{}
	data, err := os.ReadFile(source.String())
	if err != nil {
		return nil, err
	}
	for _, rawLine := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "export ") {
			line = strings.TrimSpace(line[len("export "):])
		}
		idx := strings.Index(line, "=")
		if idx < 0 {
			return nil, fmt.Errorf("Malformed Gemini auth env file %s: %s. Use KEY=value assignments.", source, strings.TrimSpace(rawLine))
		}
		key := strings.TrimSpace(line[:idx])
		value := stripComment(line[idx+1:])
		if _, exists := values[key]; exists {
			return nil, fmt.Errorf("Gemini auth env file %s configures %s more than once.", source, key)
		}
		if value != "" && (value[0] == '\'' || value[0] == '"') {
			if len(value) < 2 || value[len(value)-1] != value[0] {
				return nil, fmt.Errorf("Malformed Gemini auth env file %s: %s has an unterminated quoted value.", source, key)
			}
			if value[0] == '"' {
				parsed, err := strconv.Unquote(value)
				if err != nil {
					return nil, fmt.Errorf("Malformed Gemini auth env file %s: %s has an invalid double-quoted value (%v).", source, key, err)
				}
				value = parsed
			} else {
				value = value[1 : len(value)-1]
			}
		} else if value != "" && (strings.HasSuffix(value, "'") || strings.HasSuffix(value, "\"")) {
			return nil, fmt.Errorf("Malformed Gemini auth env file %s: %s has an unmatched trailing quote.", source, key)
		}
		values[key] = value
	}
	return values, nil
}

func parseEnvBooleanValue(values map[string]string, source Path, key string) (bool, error) {
	raw, ok := values[key]
	if !ok {
		return false, nil
	}
	normalized := strings.ToLower(strings.TrimSpace(raw))
	if normalized != "true" && normalized != "false" {
		return false, fmt.Errorf("Invalid boolean in Gemini auth env file %s: %s=%s. Use true or false.", source, key, raw)
	}
	return normalized == "true", nil
}

func normalizeVertexLocation(value string) string {
	candidate := strings.ToLower(strings.TrimSpace(value))
	if candidate == "" {
		return ""
	}
	for _, r := range candidate {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
			return ""
		}
	}
	if strings.HasPrefix(candidate, "-") || strings.HasSuffix(candidate, "-") || strings.Contains(candidate, "--") {
		return ""
	}
	return candidate
}

func validateJSONObjFile(source Path, label string) (map[string]any, error) {
	data, err := os.ReadFile(source.String())
	if err != nil {
		return nil, err
	}
	var parsed any
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("%s must contain valid JSON: %s (%s)", label, source, err.Error())
	}
	object, ok := parsed.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s must contain a JSON object: %s", label, source)
	}
	return object, nil
}

func validateGcloudADCFile(source Path, label string) error {
	parsed, err := validateJSONObjFile(source, label)
	if err != nil {
		return err
	}
	adcType, ok := parsed["type"].(string)
	if !ok || strings.TrimSpace(adcType) == "" {
		return fmt.Errorf("%s must contain a non-empty JSON string field: type", label)
	}
	return nil
}

func validateGeminiProjectsFile(source Path, label string) error {
	parsed, err := validateJSONObjFile(source, label)
	if err != nil {
		return err
	}
	if _, ok := parsed["projects"].(map[string]any); !ok {
		return fmt.Errorf("%s must contain a JSON object with an object-valued projects field", label)
	}
	return nil
}

func deriveCredentialExtraEndpoints(renderedCredentials map[string]map[string]string) []string {
	endpoints := map[string]struct{}{}
	if geminiEnv, ok := renderedCredentials["gemini_env"]; ok {
		metadata, err := validateGeminiEnvFile(Path(geminiEnv["source"]))
		if err == nil {
			for _, endpoint := range metadata["extra_endpoints"].([]string) {
				endpoints[endpoint] = struct{}{}
			}
		}
	}
	return sortedSetKeys(endpoints)
}

func effectivePolicySHA256(
	policySources []PolicySource,
	outputRoot Path,
	renderedDocuments map[string]string,
	renderedCopies []map[string]any,
	renderedCredentials map[string]map[string]string,
	renderedSSH map[string]any,
) string {
	documents := map[string]string{}
	for _, key := range sortedStringKeys(renderedDocuments) {
		documents[key] = pathMaterialSHA256(outputRoot.Join(renderedDocuments[key]))
	}
	copies := make([]map[string]any, 0, len(renderedCopies))
	for _, entry := range renderedCopies {
		renderedSource := entry["source"]
		var sourcePath Path
		switch value := renderedSource.(type) {
		case string:
			sourcePath = outputRoot.Join(value)
		case map[string]any:
			if hostSource, ok := value["source"].(string); ok {
				sourcePath = Path(hostSource)
			}
		}
		copies = append(copies, map[string]any{
			"classification": entry["classification"],
			"dir_mode":       entry["dir_mode"],
			"file_mode":      entry["file_mode"],
			"kind":           entry["kind"],
			"sha256":         pathMaterialSHA256(sourcePath),
			"target":         entry["target"],
		})
	}
	credentials := map[string]map[string]any{}
	for _, key := range sortedStringKeysMap(renderedCredentials) {
		value := renderedCredentials[key]
		credentials[key] = map[string]any{
			"mount_path": value["mount_path"],
			"sha256":     pathMaterialSHA256(Path(value["source"])),
		}
	}
	ssh := map[string]any{}
	if len(renderedSSH) > 0 {
		if value, ok := renderedSSH["config_assurance"].(string); ok {
			ssh["config_assurance"] = value
		} else {
			ssh["config_assurance"] = "off"
		}
		if config, ok := renderedSSH["config"].(map[string]any); ok {
			if source, ok := config["source"].(string); ok {
				ssh["config"] = map[string]any{
					"mount_path": config["mount_path"],
					"sha256":     pathMaterialSHA256(Path(source)),
				}
			}
		}
		if knownHosts, ok := renderedSSH["known_hosts"].(map[string]any); ok {
			if source, ok := knownHosts["source"].(string); ok {
				ssh["known_hosts"] = map[string]any{
					"mount_path": knownHosts["mount_path"],
					"sha256":     pathMaterialSHA256(Path(source)),
				}
			}
		}
		if identities, ok := renderedSSH["identities"].([]map[string]any); ok {
			renderedIdentities := make([]map[string]any, 0, len(identities))
			for _, entry := range identities {
				renderedIdentities = append(renderedIdentities, map[string]any{
					"mount_path":  entry["mount_path"],
					"sha256":      pathMaterialSHA256(Path(entry["source"].(string))),
					"target_name": entry["target_name"],
				})
			}
			ssh["identities"] = renderedIdentities
		} else if identities, ok := renderedSSH["identities"].([]any); ok {
			renderedIdentities := make([]map[string]any, 0, len(identities))
			for _, rawEntry := range identities {
				entry := rawEntry.(map[string]any)
				renderedIdentities = append(renderedIdentities, map[string]any{
					"mount_path":  entry["mount_path"],
					"sha256":      pathMaterialSHA256(Path(entry["source"].(string))),
					"target_name": entry["target_name"],
				})
			}
			ssh["identities"] = renderedIdentities
		}
	}
	canonical, _ := json.Marshal(map[string]any{
		"credentials":    credentials,
		"copies":         copies,
		"documents":      documents,
		"policy_sources": policySources,
		"ssh":            ssh,
	})
	sum := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func pathMaterialSHA256(path Path) string {
	info, err := os.Lstat(path.String())
	if err != nil {
		return ""
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, _ := os.Readlink(path.String())
		sum := sha256.Sum256([]byte("symlink:" + target))
		return hex.EncodeToString(sum[:])
	}
	if info.Mode().IsRegular() {
		data, err := os.ReadFile(path.String())
		if err != nil {
			return ""
		}
		sum := sha256.Sum256(data)
		return hex.EncodeToString(sum[:])
	}
	if info.IsDir() {
		hasher := sha256.New()
		hasher.Write([]byte("dir\n"))
		children := []string{}
		_ = filepath.WalkDir(path.String(), func(current string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if current == path.String() {
				return nil
			}
			children = append(children, current)
			return nil
		})
		sort.Slice(children, func(i, j int) bool {
			return filepath.ToSlash(strings.TrimPrefix(children[i], path.String()+string(filepath.Separator))) < filepath.ToSlash(strings.TrimPrefix(children[j], path.String()+string(filepath.Separator)))
		})
		for _, child := range children {
			info, err := os.Lstat(child)
			if err != nil {
				return ""
			}
			relative, err := filepath.Rel(path.String(), child)
			if err != nil {
				return ""
			}
			relative = filepath.ToSlash(relative)
			switch {
			case info.Mode()&os.ModeSymlink != 0:
				target, _ := os.Readlink(child)
				hasher.Write([]byte("symlink:" + relative + ":" + target + "\n"))
			case info.IsDir():
				hasher.Write([]byte("dir:" + relative + "\n"))
			case info.Mode().IsRegular():
				hasher.Write([]byte("file:" + relative + "\n"))
				data, err := os.ReadFile(child)
				if err != nil {
					return ""
				}
				hasher.Write(data)
				hasher.Write([]byte("\n"))
			default:
				return ""
			}
		}
		return hex.EncodeToString(hasher.Sum(nil))
	}
	return ""
}

func policySHA256(policyPath string) string {
	data, err := os.ReadFile(policyPath)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func compositePolicySHA256(policySources []PolicySource) string {
	data, _ := json.Marshal(policySources)
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func logicalPolicyPath(policyPath, entrypointRoot Path) string {
	relative, err := filepath.Rel(entrypointRoot.String(), policyPath.String())
	if err != nil {
		return policyPath.String()
	}
	return filepath.ToSlash(relative)
}

func rebaseFragmentPath(raw any, fragmentDir Path) any {
	rawStr, ok := raw.(string)
	if !ok || rawStr == "" {
		return raw
	}
	expanded, err := expandHostPath(rawStr, fragmentDir)
	if err != nil {
		return raw
	}
	return expanded.String()
}

func rebasePolicyFragment(policy map[string]any, fragmentDir Path) map[string]any {
	rebased := map[string]any{}
	for key, value := range policy {
		switch key {
		case "documents":
			if documents, ok := value.(map[string]any); ok {
				rebasedDocs := map[string]any{}
				for documentKey, documentValue := range documents {
					rebasedDocs[documentKey] = rebaseFragmentPath(documentValue, fragmentDir)
				}
				rebased[key] = rebasedDocs
				continue
			}
		case "copies":
			if copies, ok := value.([]any); ok {
				rebasedCopies := make([]any, 0, len(copies))
				for _, entry := range copies {
					if entryMap, ok := entry.(map[string]any); ok {
						rebasedEntry := cloneMap(entryMap)
						if source, ok := rebasedEntry["source"]; ok {
							rebasedEntry["source"] = rebaseFragmentPath(source, fragmentDir)
						}
						rebasedCopies = append(rebasedCopies, rebasedEntry)
						continue
					}
					rebasedCopies = append(rebasedCopies, entry)
				}
				rebased[key] = rebasedCopies
				continue
			}
		case "ssh":
			if ssh, ok := value.(map[string]any); ok {
				rebasedSSH := cloneMap(ssh)
				for _, sshKey := range []string{"config", "known_hosts"} {
					if sshValue, ok := rebasedSSH[sshKey]; ok {
						rebasedSSH[sshKey] = rebaseFragmentPath(sshValue, fragmentDir)
					}
				}
				if identities, err := anySlice(rebasedSSH["identities"], "ssh.identities"); err == nil {
					next := make([]any, 0, len(identities))
					for _, identity := range identities {
						next = append(next, rebaseFragmentPath(identity, fragmentDir))
					}
					rebasedSSH["identities"] = next
				}
				rebased[key] = rebasedSSH
				continue
			}
		case "credentials":
			if credentials, ok := value.(map[string]any); ok {
				rebasedCredentials := map[string]any{}
				for credentialKey, credentialValue := range credentials {
					if credentialMap, ok := credentialValue.(map[string]any); ok {
						rebasedCredential := cloneMap(credentialMap)
						if source, ok := rebasedCredential["source"]; ok {
							rebasedCredential["source"] = rebaseFragmentPath(source, fragmentDir)
						}
						rebasedCredentials[credentialKey] = rebasedCredential
						continue
					}
					rebasedCredentials[credentialKey] = rebaseFragmentPath(credentialValue, fragmentDir)
				}
				rebased[key] = rebasedCredentials
				continue
			}
		}
		rebased[key] = value
	}
	return rebased
}

func validatePolicyInclude(raw any, label string, base, entrypointRoot Path) (Path, error) {
	source, err := validateSourcePath(raw, label, base)
	if err != nil {
		return Path(""), err
	}
	if err := ensureIsFile(source, label); err != nil {
		return Path(""), err
	}
	if err := requirePathWithin(entrypointRoot, source, label); err != nil {
		return Path(""), err
	}
	return source, nil
}

func mergePolicyFragment(base, addition map[string]any, sourcePath Path) error {
	version := 1
	if rawVersion, ok := addition["version"]; ok {
		if v, ok := rawVersion.(int); ok {
			version = v
		}
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
		destination, _ := base[tableName].(map[string]any)
		if destination == nil {
			destination = map[string]any{}
			base[tableName] = destination
		}
		for key, value := range tableMap {
			if _, exists := destination[key]; exists {
				return fmt.Errorf("injection policy fragments declare the same setting more than once: %s.%s (%s)", tableName, key, sourcePath)
			}
			destination[key] = value
		}
	}
	if copies, ok := addition["copies"]; ok {
		copyList, ok := copies.([]any)
		if !ok {
			return fmt.Errorf("injection policy fragment must keep copies as an array of tables: %s", sourcePath)
		}
		destinationCopies, _ := base["copies"].([]any)
		destinationCopies = append(destinationCopies, copyList...)
		base["copies"] = destinationCopies
	}
	return nil
}

func ensureIsFile(source Path, label string) error {
	info, err := os.Stat(source.String())
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s must point at a file: %s", label, source)
	}
	return nil
}

func writeIndentedJSON(pathname string, value any, mode os.FileMode) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.WriteFile(pathname, data, mode); err != nil {
		return err
	}
	return os.Chmod(pathname, mode)
}

func validateContainerTarget(candidate string) (string, error) {
	if !targetIsUnder(candidate, sessionHomeRoot) && !targetIsUnder(candidate, runInjectedRoot) {
		return "", fmt.Errorf("injection target must stay under /state/agent-home or /state/injected: %s", candidate)
	}
	if targetIsReserved(candidate) {
		return "", fmt.Errorf("injection target collides with a Workcell-managed control-plane path: %s", candidate)
	}
	return candidate, nil
}

func normalizeContainerTarget(raw string) string {
	if strings.HasPrefix(raw, "~/") {
		raw = path.Join(sessionHomeRoot, raw[2:])
	}
	candidate := path.Clean(raw)
	if !path.IsAbs(candidate) {
		return raw
	}
	if strings.Contains(candidate, "..") {
		return raw
	}
	return candidate
}

func targetIsUnder(candidate, root string) bool {
	candidate = path.Clean(candidate)
	root = path.Clean(root)
	return candidate == root || strings.HasPrefix(candidate, root+"/")
}

func targetIsReserved(candidate string) bool {
	candidate = path.Clean(candidate)
	for _, reserved := range reservedTargets {
		if candidate == reserved || strings.HasPrefix(candidate, reserved+"/") {
			return true
		}
	}
	return false
}

func classificationModes(classification string, isDir bool) (string, string, error) {
	if _, ok := supportedClassifications[classification]; !ok {
		return "", "", fmt.Errorf("unsupported injection classification: %s", classification)
	}
	if classification == "secret" {
		return "0600", "0700", nil
	}
	return "0644", "0755", nil
}

func secretCopyTargets(renderedCopies []map[string]any) []string {
	targets := []string{}
	for _, entry := range renderedCopies {
		if classification, _ := entry["classification"].(string); classification == "secret" {
			if target, ok := entry["target"].(string); ok {
				targets = append(targets, target)
			}
		}
	}
	sort.Strings(targets)
	return targets
}

func sortedStringKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedStringKeysMap(values map[string]map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedSetKeys(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func mapKeysSet(keys []string) map[string]struct{} {
	allowed := map[string]struct{}{}
	for _, key := range keys {
		allowed[key] = struct{}{}
	}
	return allowed
}

func cloneMap(values map[string]any) map[string]any {
	clone := map[string]any{}
	for key, value := range values {
		clone[key] = value
	}
	return clone
}

func containsPath(stack []Path, candidate Path) bool {
	for _, item := range stack {
		if item == candidate {
			return true
		}
	}
	return false
}

type Path string

func (p Path) String() string {
	return string(p)
}

func (p Path) Parent() Path {
	return Path(filepath.Dir(string(p)))
}

func (p Path) Join(rel string) Path {
	return Path(filepath.Join(string(p), rel))
}

func (p Path) Base() string {
	return filepath.Base(string(p))
}

func (p Path) IsDir() bool {
	info, err := os.Stat(string(p))
	return err == nil && info.IsDir()
}

func newPath(value string) Path {
	return Path(value)
}

func init() {
	// Keep the regex-free helpers isolated; no init-time side effects.
}
