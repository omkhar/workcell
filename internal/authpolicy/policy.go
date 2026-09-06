// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package authpolicy

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/omkhar/workcell/internal/adapters"
	"github.com/omkhar/workcell/internal/injectionpolicy"
	"github.com/omkhar/workcell/internal/pathutil"
	"github.com/omkhar/workcell/internal/providerid"
	"github.com/omkhar/workcell/internal/rootio"
	"github.com/omkhar/workcell/internal/secretfile"
)

var (
	SupportedAgents = providerid.AllProviderSet()
	SupportedModes  = map[string]struct{}{
		"strict":      {},
		"development": {},
		"build":       {},
		"breakglass":  {},
	}
	CredentialKeys = credentialKeyUnion(
		adapters.AgentScopedCredentialKeysForProviders(providerid.AllProviders),
		adapters.SharedCredentialKeys(),
	)
	DocumentKeySet            = providerid.DocumentKeySet()
	AgentScopedCredentialKeys = adapters.AgentScopedCredentialKeysForProviders(providerid.AllProviders)
	SharedCredentialKeys      = adapters.SharedCredentialKeys()
	AllowedRootPolicyKeys     = map[string]struct{}{
		"version":     {},
		"includes":    {},
		"documents":   {},
		"ssh":         {},
		"copies":      {},
		"credentials": {},
		"network":     {},
	}
	managedRootMarker = ".workcell-managed-root"
)

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

var systemSymlinkAllowlist = map[string]struct{}{}

func init() {
	if runtime.GOOS == "darwin" {
		systemSymlinkAllowlist[filepath.Clean("/var")] = struct{}{}
		systemSymlinkAllowlist[filepath.Clean("/tmp")] = struct{}{}
	}
}

// PolicySource is an alias for injectionpolicy.PolicySource — the
// canonical cross-package type.  The injectionpolicy form carries
// json tags (path/sha256); this package was using the bare uppercase
// SHA256 form before unification.  Call sites that used `.SHA256`
// must be renamed to `.Sha256` for the alias to compile.
type PolicySource = injectionpolicy.PolicySource

func die(message string) error {
	return fmt.Errorf("%s", message)
}

func expandHostPath(raw string, base string) (string, error) {
	expanded, err := pathutil.ExpandUserPathStrictRequireNonEmpty(raw)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(expanded) {
		expanded = filepath.Join(base, expanded)
	}
	return filepath.Abs(expanded)
}

func requirePathWithin(root string, candidate string, label string) error {
	resolvedRoot, err := resolveAbsPath(root)
	if err != nil {
		return err
	}
	resolvedCandidate, err := resolveAbsPath(candidate)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(resolvedRoot, resolvedCandidate)
	if err != nil {
		return die(fmt.Sprintf("%s must stay within %s: %s", label, resolvedRoot, resolvedCandidate))
	}
	if rel == "." || !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".." {
		return nil
	}
	return die(fmt.Sprintf("%s must stay within %s: %s", label, resolvedRoot, resolvedCandidate))
}

func requireNoSymlinkInPathChain(path string, label string) error {
	current := filepath.Clean(path)
	for {
		info, err := os.Lstat(current)
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			if _, ok := systemSymlinkAllowlist[current]; !ok {
				return die(fmt.Sprintf("%s must not be a symlink: %s", label, current))
			}
		}
		parent := filepath.Dir(current)
		if parent == current {
			return nil
		}
		current = parent
	}
}

func validateSourcePath(raw any, label string, base string) (string, error) {
	rawString, ok := raw.(string)
	if !ok || rawString == "" {
		return "", die(fmt.Sprintf("%s must be a non-empty string path", label))
	}
	source, err := expandHostPath(rawString, base)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(source); err != nil {
		if os.IsNotExist(err) {
			return "", die(fmt.Sprintf("%s does not exist: %s", label, source))
		}
		return "", err
	}
	if err := requireNoSymlinkInPathChain(source, label); err != nil {
		return "", err
	}
	return source, nil
}

func validateAllowedKeys(table map[string]any, allowedKeys map[string]struct{}, label string) error {
	if table == nil {
		return nil
	}
	unknown := make([]string, 0)
	for key := range table {
		if _, ok := allowedKeys[key]; !ok {
			unknown = append(unknown, key)
		}
	}
	slices.Sort(unknown)
	if len(unknown) > 0 {
		return die(fmt.Sprintf("%s contains unsupported keys: %s", label, strings.Join(unknown, ", ")))
	}
	return nil
}

func validatePolicyDocuments(policy map[string]any) error {
	raw, ok := policy["documents"]
	if !ok || raw == nil {
		return nil
	}
	documents, ok := raw.(map[string]any)
	if !ok {
		return die("documents must be a TOML table")
	}
	return validateAllowedKeys(documents, DocumentKeySet, "documents")
}

// validatePolicyNetwork is the acceptance-time counterpart to renderNetwork:
// `workcell policy validate` accepts only allow_endpoints/deny_endpoints
// (network_policy etc. rejected), each matching the shared grammar.
func validatePolicyNetwork(policy map[string]any) error {
	raw, ok := policy["network"]
	if !ok || raw == nil {
		return nil
	}
	network, ok := raw.(map[string]any)
	if !ok {
		return die("network must be a TOML table")
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
			return die(fmt.Sprintf("network.%s must be an array of host:port strings", key))
		}
		for _, item := range items {
			endpoint, ok := item.(string)
			if !ok {
				return die(fmt.Sprintf("network.%s must be an array of host:port strings; found non-string element: %v", key, item))
			}
			if err := injectionpolicy.ValidateEgressEndpoint(endpoint, "network."+key); err != nil {
				return err
			}
		}
	}
	return nil
}

func validatePolicyCredentials(policy map[string]any) error {
	raw, ok := policy["credentials"]
	if !ok || raw == nil {
		return nil
	}
	credentials, ok := raw.(map[string]any)
	if !ok {
		return die("credentials must be a TOML table")
	}
	return validateAllowedKeys(credentials, CredentialKeys, "credentials")
}

func selectedFor(values any, current string, label string, allowedValues map[string]struct{}) (bool, error) {
	rawValues, err := selectorStrings(values, label, allowedValues)
	if err != nil {
		return false, err
	}
	if rawValues == nil {
		return true, nil
	}
	for _, value := range rawValues {
		if value == current {
			return true, nil
		}
	}
	return false, nil
}

// parseTOMLSubset parses an injection-policy TOML file via the shared
// injectionpolicy.ParsePolicyTOML seam and layers on the authpolicy
// document, credential, and network whitelists, preserving this
// package's error ordering.
func parseTOMLSubset(content string, policyPath string) (map[string]any, error) {
	root, err := injectionpolicy.ParsePolicyTOML(content, policyPath, CredentialKeys)
	if err != nil {
		return nil, err
	}
	if err := validatePolicyDocuments(root); err != nil {
		return nil, err
	}
	if err := validatePolicyCredentials(root); err != nil {
		return nil, err
	}
	if err := validatePolicyNetwork(root); err != nil {
		return nil, err
	}
	return root, nil
}

func compositePolicySHA256(policySources []PolicySource) string {
	sortedSources := append([]PolicySource(nil), policySources...)
	sort.Slice(sortedSources, func(i, j int) bool {
		return sortedSources[i].Path < sortedSources[j].Path
	})
	var b strings.Builder
	b.WriteByte('[')
	for i, source := range sortedSources {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString("{'path': ")
		b.WriteString(pythonReprString(source.Path))
		b.WriteString(", 'sha256': ")
		b.WriteString(pythonReprString(source.Sha256))
		b.WriteByte('}')
	}
	b.WriteByte(']')
	sum := sha256.Sum256([]byte(b.String()))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func pythonReprString(value string) string {
	escaped := strings.ReplaceAll(value, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `'`, `\'`)
	return "'" + escaped + "'"
}

func rebaseFragmentPath(raw any, fragmentDir string) any {
	rawString, ok := raw.(string)
	if !ok || rawString == "" {
		return raw
	}
	rebased, err := expandHostPath(rawString, fragmentDir)
	if err != nil {
		return raw
	}
	return rebased
}

func validatePolicyInclude(raw any, label string, base string, entrypointRoot string) (string, error) {
	source, err := validateSourcePath(raw, label, base)
	if err != nil {
		return "", err
	}
	if info, err := os.Stat(source); err != nil || !info.Mode().IsRegular() {
		return "", die(fmt.Sprintf("%s must point at a file: %s", label, source))
	}
	if err := requirePathWithin(entrypointRoot, source, label); err != nil {
		return "", err
	}
	return source, nil
}

func loadPolicyBundle(policyPath string) (map[string]any, []PolicySource, error) {
	return loadPolicyBundleWithReader(policyPath, injectionpolicy.NewBundleReader())
}

func loadPolicyBundleWithReader(policyPath string, reader *injectionpolicy.BundleReader) (map[string]any, []PolicySource, error) {
	resolvedPolicyPath, err := filepath.Abs(policyPath)
	if err != nil {
		return nil, nil, err
	}
	return injectionpolicy.LoadBundle(resolvedPolicyPath, filepath.Dir(resolvedPolicyPath), reader, injectionpolicy.LoadOptions{
		Parse:           parseTOMLSubset,
		ValidateInclude: validatePolicyInclude,
		ValidateMerged: func(policy map[string]any) error {
			if err := validatePolicyCredentials(policy); err != nil {
				return err
			}
			return validatePolicyNetwork(policy)
		},
		RebasePath: rebaseFragmentPath,
		Clone:      clonePolicyMap,
	})
}

func loadRawPolicy(policyPath string) (map[string]any, error) {
	content, err := injectionpolicy.ReadFile(policyPath)
	if os.IsNotExist(err) {
		return map[string]any{"version": 1}, nil
	}
	if err != nil {
		return nil, err
	}
	loaded, err := parseTOMLSubset(string(content), policyPath)
	if err != nil {
		return nil, err
	}
	if err := validateAllowedKeys(loaded, AllowedRootPolicyKeys, "root policy"); err != nil {
		return nil, err
	}
	if err := validatePolicyCredentials(loaded); err != nil {
		return nil, err
	}
	if err := validatePolicyNetwork(loaded); err != nil {
		return nil, err
	}
	version := 1
	if rawVersion, ok := loaded["version"]; ok {
		if value, ok := rawVersion.(int); ok {
			version = value
		}
	}
	if version != 1 {
		return nil, die(fmt.Sprintf("unsupported injection policy version: %d", version))
	}
	if _, ok := loaded["version"]; !ok {
		loaded["version"] = 1
	}
	return loaded, nil
}

func clonePolicyMap(policy map[string]any) map[string]any {
	cloned := map[string]any{}
	for key, value := range policy {
		cloned[key] = cloneValue(value)
	}
	return cloned
}

func cloneValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		cloned := map[string]any{}
		for key, child := range typed {
			cloned[key] = cloneValue(child)
		}
		return cloned
	case []any:
		cloned := make([]any, 0, len(typed))
		for _, child := range typed {
			cloned = append(cloned, cloneValue(child))
		}
		return cloned
	default:
		return typed
	}
}

func renderTOMLValue(value any) (string, error) {
	switch typed := value.(type) {
	case bool:
		if typed {
			return "true", nil
		}
		return "false", nil
	case int:
		return strconv.Itoa(typed), nil
	case string:
		return jsonQuote(typed), nil
	case []any:
		items := make([]string, 0, len(typed))
		for _, item := range typed {
			s, ok := item.(string)
			if !ok {
				return "", die("only arrays of strings are supported in rendered policy output")
			}
			items = append(items, jsonQuote(s))
		}
		return "[" + strings.Join(items, ", ") + "]", nil
	case []string:
		items := make([]string, 0, len(typed))
		for _, item := range typed {
			items = append(items, jsonQuote(item))
		}
		return "[" + strings.Join(items, ", ") + "]", nil
	default:
		return "", die(fmt.Sprintf("unsupported TOML value type: %T", value))
	}
}

func jsonQuote(value string) string {
	escaped := strings.ReplaceAll(value, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `"`, `\"`)
	escaped = strings.ReplaceAll(escaped, "\n", `\n`)
	return `"` + escaped + `"`
}

func renderPolicyTOML(policy map[string]any) (string, error) {
	if err := validatePolicyDocuments(policy); err != nil {
		return "", err
	}
	if err := validatePolicyCredentials(policy); err != nil {
		return "", err
	}
	if err := validatePolicyNetwork(policy); err != nil {
		return "", err
	}

	lines := make([]string, 0)
	version := 1
	if rawVersion, ok := policy["version"]; ok {
		if value, ok := rawVersion.(int); ok {
			version = value
		}
	}
	renderedVersion, err := renderTOMLValue(version)
	if err != nil {
		return "", err
	}
	lines = append(lines, "version = "+renderedVersion)

	if includes, ok := policy["includes"].([]any); ok && len(includes) > 0 {
		renderedIncludes, err := renderTOMLValue(includes)
		if err != nil {
			return "", err
		}
		lines = append(lines, "includes = "+renderedIncludes)
	}

	if documents, ok := policy["documents"].(map[string]any); ok && len(documents) > 0 {
		lines = append(lines, "")
		lines = append(lines, "[documents]")
		for _, key := range providerid.DocumentKeys {
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
		orderedSet := map[string]struct{}{}
		for _, key := range ordered {
			orderedSet[key] = struct{}{}
			if value, ok := ssh[key]; ok {
				rendered, err := renderTOMLValue(value)
				if err != nil {
					return "", err
				}
				lines = append(lines, key+" = "+rendered)
			}
		}
		extras := make([]string, 0)
		for key := range ssh {
			if _, ok := orderedSet[key]; !ok {
				extras = append(extras, key)
			}
		}
		slices.Sort(extras)
		for _, key := range extras {
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
				return "", die("copies entries must be TOML tables when rendering policy output")
			}
			lines = append(lines, "")
			lines = append(lines, "[[copies]]")
			for _, key := range []string{"source", "target", "classification", "providers", "modes"} {
				if value, ok := entryMap[key]; ok {
					rendered, err := renderTOMLValue(value)
					if err != nil {
						return "", err
					}
					lines = append(lines, key+" = "+rendered)
				}
			}
			extras := make([]string, 0)
			for key := range entryMap {
				if key == "source" || key == "target" || key == "classification" || key == "providers" || key == "modes" {
					continue
				}
				extras = append(extras, key)
			}
			slices.Sort(extras)
			for _, key := range extras {
				rendered, err := renderTOMLValue(entryMap[key])
				if err != nil {
					return "", err
				}
				lines = append(lines, key+" = "+rendered)
			}
		}
	}

	if network, ok := policy["network"].(map[string]any); ok && len(network) > 0 {
		lines = append(lines, "")
		lines = append(lines, "[network]")
		orderedSet := map[string]struct{}{}
		for _, key := range []string{"allow_endpoints", "deny_endpoints"} {
			orderedSet[key] = struct{}{}
			if value, ok := network[key]; ok {
				rendered, err := renderTOMLValue(value)
				if err != nil {
					return "", err
				}
				lines = append(lines, key+" = "+rendered)
			}
		}
		extras := make([]string, 0)
		for key := range network {
			if _, ok := orderedSet[key]; !ok {
				extras = append(extras, key)
			}
		}
		slices.Sort(extras)
		for _, key := range extras {
			rendered, err := renderTOMLValue(network[key])
			if err != nil {
				return "", err
			}
			lines = append(lines, key+" = "+rendered)
		}
	}

	return strings.Join(lines, "\n") + "\n", nil
}

func writePolicyFile(policyPath string, policy map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(policyPath), 0o755); err != nil {
		return err
	}
	rendered, err := renderPolicyTOML(policy)
	if err != nil {
		return err
	}
	parentRoot, err := os.OpenRoot(filepath.Dir(policyPath))
	if err != nil {
		return err
	}
	defer parentRoot.Close()
	return rootio.WriteFileAtomic(parentRoot, filepath.Base(policyPath), []byte(rendered), 0o600, ".workcell-policy-")
}

func requireSecretFile(source string, label string) (string, error) {
	handle, err := secretfile.Open(source, label, os.Getuid())
	if err != nil {
		return "", die(err.Error())
	}
	defer handle.Close()
	return source, nil
}

func resolveAbsPath(raw string) (string, error) {
	expanded, err := pathutil.ExpandUserPathStrictRequireNonEmpty(raw)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(expanded) {
		expanded, err = filepath.Abs(expanded)
		if err != nil {
			return "", err
		}
	}
	clean := filepath.Clean(expanded)
	if clean == string(filepath.Separator) {
		return clean, nil
	}
	existing := clean
	suffix := make([]string, 0)
	for {
		if _, err := os.Lstat(existing); err == nil {
			break
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return clean, nil
		}
		suffix = append([]string{filepath.Base(existing)}, suffix...)
		existing = parent
	}
	resolvedExisting, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return "", err
	}
	if len(suffix) == 0 {
		return filepath.Clean(resolvedExisting), nil
	}
	parts := append([]string{resolvedExisting}, suffix...)
	return filepath.Clean(filepath.Join(parts...)), nil
}

func pathsEquivalent(left string, right string) bool {
	leftResolved, errLeft := resolveAbsPath(left)
	rightResolved, errRight := resolveAbsPath(right)
	if errLeft != nil || errRight != nil {
		return left == right
	}
	return leftResolved == rightResolved
}

func sortedKeys(value map[string]any) []string {
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}
