// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package authresolve

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/omkhar/workcell/internal/providerid"
	"github.com/omkhar/workcell/internal/tomlsubset"
)

func loadPolicyBundle(policyPath string) (map[string]any, []PolicySource, error) {
	resolvedPolicyPath := filepath.Clean(policyPath)
	entrypointRoot := filepath.Dir(resolvedPolicyPath)
	return loadPolicyBundleRecursive(resolvedPolicyPath, entrypointRoot, nil, map[string]struct{}{})
}

func loadPolicyBundleRecursive(policyPath, entrypointRoot string, activeStack []string, loadedPaths map[string]struct{}) (map[string]any, []PolicySource, error) {
	if slices.Contains(activeStack, policyPath) {
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

	currentPolicy := maps.Clone(loaded)
	delete(currentPolicy, "includes")
	if len(activeStack) > 0 {
		currentPolicy = rebasePolicyFragment(currentPolicy, filepath.Dir(policyPath))
	}
	if err := mergePolicyFragment(merged, currentPolicy, policyPath); err != nil {
		return nil, nil, err
	}
	sourceSHA, err := policySHA256(policyPath)
	if err != nil {
		return nil, nil, err
	}
	policySources = append(policySources, PolicySource{
		Path:   logicalPolicyPath(policyPath, entrypointRoot),
		Sha256: sourceSHA,
	})
	return merged, policySources, nil
}

func policySHA256(policyPath string) (string, error) {
	data, err := os.ReadFile(policyPath)
	if err != nil {
		return "", fmt.Errorf("read policy %s: %w", policyPath, err)
	}
	sum := sha256.Sum256(data)
	return "sha256:" + fmt.Sprintf("%x", sum[:]), nil
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
					rebasedEntry := maps.Clone(entryMap)
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
				rebasedSSH := maps.Clone(table)
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
						rebasedCred := maps.Clone(credMap)
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

// parseTOMLSubset parses an injection-policy TOML file via the shared
// tomlsubset.ParseDocument API and reshapes the result into the
// map[string]any tree the rest of authresolve consumes.  Injection policy
// allows one specific array-of-tables construct ([[copies]]) which the
// shared strict parser rejects, so we strip those blocks out and parse
// each entry separately through tomlsubset.Parse before reassembling.
func parseTOMLSubset(content, policyPath string) (map[string]any, error) {
	subsetContent, copiesEntries, err := extractCopiesBlocks(content, policyPath)
	if err != nil {
		return nil, err
	}
	doc, err := tomlsubset.ParseDocument(subsetContent, policyPath)
	if err != nil {
		return nil, err
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
				return "", nil, fmt.Errorf("%s:%d: unsupported array-of-table [%s]", policyPath, idx+1, tableName)
			}
			block, consumed, err := readCopiesBlock(lines, idx+1, policyPath)
			if err != nil {
				return "", nil, err
			}
			entries = append(entries, block)
			idx = consumed - 1
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
		return nil, end, err
	}
	return parsed, end, nil
}

// documentToPolicyMap converts a tomlsubset.Document into the
// nested-map shape the rest of authresolve expects.  Only the
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
			if _, ok := allCredentialKeys[credentialKey]; !ok {
				return nil, fmt.Errorf("%s:%d: unsupported credentials table [%s]", policyPath, table.Line, name)
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
			for _, pair := range table.Pairs {
				entry[pair.Key] = pair.Value
			}
			continue
		}
		if name != "documents" && name != "ssh" && name != "credentials" {
			return nil, fmt.Errorf("%s:%d: unsupported table [%s]", policyPath, table.Line, name)
		}
		target, _ := root[name].(map[string]any)
		if target == nil {
			target = map[string]any{}
			root[name] = target
		}
		for _, pair := range table.Pairs {
			target[pair.Key] = pair.Value
		}
	}
	if err := validateDocumentKeys(root); err != nil {
		return nil, err
	}
	return root, nil
}

func validateDocumentKeys(policy map[string]any) error {
	documents, _ := policy["documents"].(map[string]any)
	return validateAllowedKeys(documents, map[string]struct{}{"common": {}, providerid.Codex: {}, providerid.Claude: {}, providerid.Gemini: {}}, "documents")
}

func validateAllowedKeys(table map[string]any, allowed map[string]struct{}, label string) error {
	unknown := make([]string, 0)
	for key := range table {
		if _, ok := allowed[key]; !ok {
			unknown = append(unknown, key)
		}
	}
	slices.Sort(unknown)
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
