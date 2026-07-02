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
	"github.com/omkhar/workcell/internal/tomlsubset"
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
		adapters.AgentScopedCredentialKeys(),
		adapters.SharedCredentialKeys(),
	)
	DocumentKeySet            = providerid.DocumentKeySet()
	AgentScopedCredentialKeys = adapters.AgentScopedCredentialKeys()
	SharedCredentialKeys      = adapters.SharedCredentialKeys()
	AllowedRootPolicyKeys     = map[string]struct{}{
		"version":     {},
		"includes":    {},
		"documents":   {},
		"ssh":         {},
		"copies":      {},
		"credentials": {},
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
// tomlsubset.ParseDocument API and reshapes the result into the
// map[string]any tree the rest of authpolicy consumes.  Injection policy
// allows one specific array-of-tables construct ([[copies]]) which the
// shared strict parser rejects, so we strip those blocks out and parse
// each entry separately through tomlsubset.Parse before reassembling.
func parseTOMLSubset(content string, policyPath string) (map[string]any, error) {
	subsetContent, copiesEntries, err := extractCopiesBlocks(content, policyPath)
	if err != nil {
		return nil, err
	}
	doc, err := tomlsubset.ParseDocument(subsetContent, policyPath)
	if err != nil {
		return nil, die(err.Error())
	}
	root, err := documentToPolicyMap(doc, policyPath)
	if err != nil {
		return nil, err
	}
	if len(copiesEntries) > 0 {
		copies := make([]any, 0, len(copiesEntries))
		for _, entry := range copiesEntries {
			copies = append(copies, entry)
		}
		root["copies"] = copies
	}
	return root, nil
}

// extractCopiesBlocks scans content for `[[copies]]` headers, parses each
// following key/value block as a single TOML subset table via
// tomlsubset.Parse, and returns the remaining content with those blocks
// elided plus the parsed entries in declaration order.  Any other
// [[array-of-table]] header is rejected here so the caller-visible error
// message matches the legacy parser.
func extractCopiesBlocks(content, policyPath string) (string, []map[string]any, error) {
	var (
		kept    strings.Builder
		entries []map[string]any
	)
	lines := strings.Split(content, "\n")
	for idx := 0; idx < len(lines); idx++ {
		rawLine := lines[idx]
		stripped := tomlsubset.StripComment(rawLine)
		if strings.HasPrefix(stripped, "[[") && strings.HasSuffix(stripped, "]]") {
			tableName := strings.TrimSpace(stripped[2 : len(stripped)-2])
			if tableName != "copies" {
				return "", nil, die(fmt.Sprintf("%s:%d: unsupported array-of-table [%s]", policyPath, idx+1, tableName))
			}
			block, consumed, err := readCopiesBlock(lines, idx+1, policyPath)
			if err != nil {
				return "", nil, err
			}
			entries = append(entries, block)
			idx = consumed - 1
			// Emit a blank line so downstream line numbers stay roughly
			// aligned for diagnostic output.
			kept.WriteByte('\n')
			continue
		}
		kept.WriteString(rawLine)
		kept.WriteByte('\n')
	}
	result := kept.String()
	if strings.HasSuffix(result, "\n") {
		result = result[:len(result)-1]
	}
	return result, entries, nil
}

// readCopiesBlock consumes lines starting at idx until the next [header]
// (single or double-bracketed) or end of input, parses the collected
// `key = value` pairs as a one-off TOML subset table, and returns the
// parsed entry plus the next unconsumed line index.
func readCopiesBlock(lines []string, idx int, policyPath string) (map[string]any, int, error) {
	var block strings.Builder
	end := idx
	for end < len(lines) {
		stripped := tomlsubset.StripComment(lines[end])
		if strings.HasPrefix(stripped, "[") {
			break
		}
		block.WriteString(lines[end])
		block.WriteByte('\n')
		end++
	}
	parsed, err := tomlsubset.Parse(block.String(), policyPath)
	if err != nil {
		return nil, end, die(err.Error())
	}
	return parsed, end, nil
}

// documentToPolicyMap converts a tomlsubset.Document into the
// nested-map shape the rest of authpolicy expects.  Only the
// `documents`, `ssh`, `credentials`, and `credentials.<name>` tables get
// special structural treatment; any other table name is rejected to
// preserve the strict subset semantics of the legacy parser.
func documentToPolicyMap(doc *tomlsubset.Document, policyPath string) (map[string]any, error) {
	root := map[string]any{}
	for _, pair := range doc.TopLevel.Pairs {
		root[pair.Key] = pair.Value
	}
	for _, table := range doc.Tables {
		name := table.Name
		if strings.HasPrefix(name, "credentials.") {
			credentialKey := strings.SplitN(name, ".", 2)[1]
			if _, ok := CredentialKeys[credentialKey]; !ok {
				return nil, die(fmt.Sprintf("%s:%d: unsupported credentials table [%s]", policyPath, table.Line, name))
			}
			credentialsRaw, exists := root["credentials"]
			credentials, _ := credentialsRaw.(map[string]any)
			switch {
			case !exists:
				credentials = map[string]any{}
				root["credentials"] = credentials
			case credentials == nil:
				return nil, die(fmt.Sprintf("%s:%d: credentials table conflicts with scalar key credentials", policyPath, table.Line))
			case credentials[credentialKey] != nil:
				return nil, die(fmt.Sprintf("%s:%d: duplicate credentials entry: %s", policyPath, table.Line, credentialKey))
			}
			entry := map[string]any{}
			credentials[credentialKey] = entry
			for _, pair := range table.Pairs {
				entry[pair.Key] = pair.Value
			}
			continue
		}
		if name != "documents" && name != "ssh" && name != "credentials" {
			return nil, die(fmt.Sprintf("%s:%d: unsupported table [%s]", policyPath, table.Line, name))
		}
		targetRaw, exists := root[name]
		target, _ := targetRaw.(map[string]any)
		switch {
		case !exists:
			target = map[string]any{}
			root[name] = target
		case target == nil:
			return nil, die(fmt.Sprintf("%s:%d: table [%s] conflicts with scalar key %s", policyPath, table.Line, name, name))
		}
		for _, pair := range table.Pairs {
			if _, exists := target[pair.Key]; exists {
				return nil, die(fmt.Sprintf("%s:%d: duplicate key across table forms: %s.%s", policyPath, pair.Line, name, pair.Key))
			}
			target[pair.Key] = pair.Value
		}
	}
	if err := validatePolicyDocuments(root); err != nil {
		return nil, err
	}
	return root, nil
}

func policySHA256(policyPath string) (string, error) {
	data, err := os.ReadFile(policyPath)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
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

func logicalPolicyPath(policyPath string, entrypointRoot string) (string, error) {
	relativePath, err := filepath.Rel(entrypointRoot, policyPath)
	if err != nil {
		return "", err
	}
	return filepath.ToSlash(relativePath), nil
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

func rebasePolicyFragment(policy map[string]any, fragmentDir string) map[string]any {
	rebased := map[string]any{}
	for key, value := range policy {
		switch key {
		case "documents":
			if table, ok := value.(map[string]any); ok {
				rebasedTable := map[string]any{}
				for documentKey, documentValue := range table {
					rebasedTable[documentKey] = rebaseFragmentPath(documentValue, fragmentDir)
				}
				rebased[key] = rebasedTable
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
					rebasedEntry := map[string]any{}
					for k, v := range entryMap {
						rebasedEntry[k] = v
					}
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
				rebasedSSH := map[string]any{}
				for k, v := range table {
					rebasedSSH[k] = v
				}
				for _, sshKey := range []string{"config", "known_hosts"} {
					if sshValue, ok := rebasedSSH[sshKey]; ok {
						rebasedSSH[sshKey] = rebaseFragmentPath(sshValue, fragmentDir)
					}
				}
				if identities, ok := rebasedSSH["identities"].([]any); ok {
					rebasedIdentities := make([]any, 0, len(identities))
					for _, identity := range identities {
						rebasedIdentities = append(rebasedIdentities, rebaseFragmentPath(identity, fragmentDir))
					}
					rebasedSSH["identities"] = rebasedIdentities
				}
				rebased[key] = rebasedSSH
				continue
			}
		case "credentials":
			if table, ok := value.(map[string]any); ok {
				rebasedCredentials := map[string]any{}
				for credentialKey, credentialValue := range table {
					if credentialMap, ok := credentialValue.(map[string]any); ok {
						rebasedCredential := map[string]any{}
						for k, v := range credentialMap {
							rebasedCredential[k] = v
						}
						if source, ok := rebasedCredential["source"]; ok {
							rebasedCredential["source"] = rebaseFragmentPath(source, fragmentDir)
						}
						rebasedCredentials[credentialKey] = rebasedCredential
					} else {
						rebasedCredentials[credentialKey] = rebaseFragmentPath(credentialValue, fragmentDir)
					}
				}
				rebased[key] = rebasedCredentials
				continue
			}
		}
		rebased[key] = value
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

func mergePolicyFragment(base map[string]any, addition map[string]any, sourcePath string) error {
	version := 1
	if rawVersion, ok := addition["version"]; ok {
		if value, ok := rawVersion.(int); ok {
			version = value
		}
	}
	if version != 1 {
		return die(fmt.Sprintf("unsupported injection policy version: %d", version))
	}
	for _, tableName := range []string{"documents", "ssh", "credentials"} {
		tableRaw, ok := addition[tableName]
		if !ok {
			continue
		}
		table, ok := tableRaw.(map[string]any)
		if !ok {
			return die(fmt.Sprintf("injection policy fragment must keep %s as a table: %s", tableName, sourcePath))
		}
		destination, _ := base[tableName].(map[string]any)
		if destination == nil {
			destination = map[string]any{}
			base[tableName] = destination
		}
		for key, value := range table {
			if _, exists := destination[key]; exists {
				return die(fmt.Sprintf("injection policy fragments declare the same setting more than once: %s.%s (%s)", tableName, key, sourcePath))
			}
			destination[key] = value
		}
	}
	if copiesRaw, ok := addition["copies"]; ok {
		copies, ok := copiesRaw.([]any)
		if !ok {
			return die(fmt.Sprintf("injection policy fragment must keep copies as an array of tables: %s", sourcePath))
		}
		destinationCopies, _ := base["copies"].([]any)
		destinationCopies = append(destinationCopies, copies...)
		base["copies"] = destinationCopies
	}
	return nil
}

func loadPolicyBundle(policyPath string) (map[string]any, []PolicySource, error) {
	resolvedPolicyPath, err := filepath.Abs(policyPath)
	if err != nil {
		return nil, nil, err
	}
	return loadPolicyBundleWithState(resolvedPolicyPath, "", nil, map[string]struct{}{})
}

func loadPolicyBundleWithState(policyPath string, entrypointRoot string, activeStack []string, loadedPaths map[string]struct{}) (map[string]any, []PolicySource, error) {
	resolvedPolicyPath, err := filepath.Abs(policyPath)
	if err != nil {
		return nil, nil, err
	}
	if entrypointRoot == "" {
		entrypointRoot = filepath.Dir(resolvedPolicyPath)
	}
	entrypointRoot, err = filepath.Abs(entrypointRoot)
	if err != nil {
		return nil, nil, err
	}
	for _, active := range activeStack {
		if active == resolvedPolicyPath {
			cycle := strings.Join(append(activeStack, resolvedPolicyPath), " -> ")
			return nil, nil, die(fmt.Sprintf("injection policy include cycle detected: %s", cycle))
		}
	}
	if _, ok := loadedPaths[resolvedPolicyPath]; ok {
		return nil, nil, die(fmt.Sprintf("injection policy includes the same file more than once: %s", resolvedPolicyPath))
	}
	loadedPaths[resolvedPolicyPath] = struct{}{}

	content, err := os.ReadFile(resolvedPolicyPath)
	if err != nil {
		return nil, nil, err
	}
	loaded, err := parseTOMLSubset(string(content), resolvedPolicyPath)
	if err != nil {
		return nil, nil, err
	}
	if err := validateAllowedKeys(loaded, AllowedRootPolicyKeys, "root policy"); err != nil {
		return nil, nil, err
	}
	version := 1
	if rawVersion, ok := loaded["version"]; ok {
		if value, ok := rawVersion.(int); ok {
			version = value
		}
	}
	if version != 1 {
		return nil, nil, die(fmt.Sprintf("unsupported injection policy version: %d", version))
	}
	includesRaw, ok := loaded["includes"]
	includes := []any{}
	if ok && includesRaw != nil {
		includes, ok = includesRaw.([]any)
		if !ok {
			return nil, nil, die("includes must be an array of strings when specified")
		}
	}
	merged := map[string]any{"version": 1}
	policySources := make([]PolicySource, 0)
	nextStack := append(append([]string(nil), activeStack...), resolvedPolicyPath)
	for index, include := range includes {
		includePath, err := validatePolicyInclude(include, fmt.Sprintf("includes[%d]", index), filepath.Dir(resolvedPolicyPath), entrypointRoot)
		if err != nil {
			return nil, nil, err
		}
		includedPolicy, includedSources, err := loadPolicyBundleWithState(includePath, entrypointRoot, nextStack, loadedPaths)
		if err != nil {
			return nil, nil, err
		}
		if err := mergePolicyFragment(merged, includedPolicy, includePath); err != nil {
			return nil, nil, err
		}
		policySources = append(policySources, includedSources...)
	}

	currentPolicy := clonePolicyMap(loaded)
	delete(currentPolicy, "includes")
	if len(activeStack) > 0 {
		currentPolicy = rebasePolicyFragment(currentPolicy, filepath.Dir(resolvedPolicyPath))
	}
	if err := mergePolicyFragment(merged, currentPolicy, resolvedPolicyPath); err != nil {
		return nil, nil, err
	}
	sourceSHA, err := policySHA256(resolvedPolicyPath)
	if err != nil {
		return nil, nil, err
	}
	logicalPath, err := logicalPolicyPath(resolvedPolicyPath, entrypointRoot)
	if err != nil {
		return nil, nil, err
	}
	policySources = append(policySources, PolicySource{
		Path:   logicalPath,
		Sha256: sourceSHA,
	})
	return merged, policySources, nil
}

func loadRawPolicy(policyPath string) (map[string]any, error) {
	if _, err := os.Stat(policyPath); os.IsNotExist(err) {
		return map[string]any{"version": 1}, nil
	}
	content, err := os.ReadFile(policyPath)
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
