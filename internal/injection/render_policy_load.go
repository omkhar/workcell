// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package injection

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/omkhar/workcell/internal/tomlsubset"
)

func loadPolicyBundle(policyPath Path) (map[string]any, []PolicySource, error) {
	resolvedPolicyPath, err := resolveAbsPath(policyPath.String())
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
	resolvedPolicyPath, err := resolveAbsPath(policyPath.String())
	if err != nil {
		return nil, nil, err
	}
	resolved := Path(resolvedPolicyPath)
	if slices.Contains(activeStack, resolved) {
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

	currentPolicy := maps.Clone(loaded)
	delete(currentPolicy, "includes")
	if len(activeStack) > 0 {
		currentPolicy = rebasePolicyFragment(currentPolicy, resolved.Parent())
	}
	if err := mergePolicyFragment(merged, currentPolicy, resolved); err != nil {
		return nil, nil, err
	}
	sourceSHA, err := policySHA256(resolved.String())
	if err != nil {
		return nil, nil, err
	}
	sources = append(sources, PolicySource{
		Path:   logicalPolicyPath(resolved, entrypointRoot),
		Sha256: sourceSHA,
	})
	return merged, sources, nil
}

func loadPolicyMetadataOverride(rawPath string) (string, []PolicySource, error) {
	resolved, err := resolveAbsPath(rawPath)
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

// parseTOMLSubset parses an injection-policy TOML file via the shared
// tomlsubset.ParseDocument API and reshapes the result into the
// map[string]any tree the rest of injection consumes.  Injection policy
// allows one specific array-of-tables construct ([[copies]]) which the
// shared strict parser rejects, so we strip those blocks out and parse
// each entry separately through tomlsubset.Parse before reassembling.
//
// This mirrors the adapter pattern PR 37 introduced for authpolicy and
// authresolve; the only injection-specific divergence is the allowed
// credential-table whitelist (credentialContainerPaths) and the local
// table whitelist (documents/ssh/credentials).
func parseTOMLSubset(content string, policyPath Path) (map[string]any, error) {
	policy := policyPath.String()
	subsetContent, copiesEntries, err := extractCopiesBlocks(content, policy)
	if err != nil {
		return nil, err
	}
	doc, err := tomlsubset.ParseDocument(subsetContent, policy)
	if err != nil {
		return nil, err
	}
	root, err := documentToInjectionMap(doc, policy)
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
// message matches the legacy injection parser.
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
		return nil, end, err
	}
	return parsed, end, nil
}

// documentToInjectionMap converts a tomlsubset.Document into the
// nested-map shape the rest of injection expects.  Only the
// `documents`, `ssh`, `credentials`, and `credentials.<name>` tables get
// special structural treatment; any other table name is rejected to
// preserve the strict subset semantics of the legacy injection parser.
// The credential whitelist is credentialContainerPaths (vs authpolicy's
// CredentialKeys) — these two maps stay in sync but live in different
// packages so each can evolve independently.
func documentToInjectionMap(doc *tomlsubset.Document, policyPath string) (map[string]any, error) {
	root := map[string]any{}
	for _, pair := range doc.TopLevel.Pairs {
		root[pair.Key] = pair.Value
	}
	for _, table := range doc.Tables {
		name := table.Name
		if strings.HasPrefix(name, "credentials.") {
			credentialKey := strings.SplitN(name, ".", 2)[1]
			if _, ok := credentialContainerPaths[credentialKey]; !ok {
				return nil, fmt.Errorf("%s:%d: unsupported credentials table [%s]", policyPath, table.Line, name)
			}
			credentialsRaw, exists := root["credentials"]
			credentials, _ := credentialsRaw.(map[string]any)
			switch {
			case !exists:
				credentials = map[string]any{}
				root["credentials"] = credentials
			case credentials == nil:
				return nil, fmt.Errorf("%s:%d: credentials table conflicts with scalar key credentials", policyPath, table.Line)
			case credentials[credentialKey] != nil:
				return nil, fmt.Errorf("%s:%d: duplicate credentials entry: %s", policyPath, table.Line, credentialKey)
			}
			entry := map[string]any{}
			credentials[credentialKey] = entry
			for _, pair := range table.Pairs {
				entry[pair.Key] = pair.Value
			}
			continue
		}
		if name != "documents" && name != "ssh" && name != "credentials" {
			return nil, fmt.Errorf("%s:%d: unsupported table [%s]", policyPath, table.Line, name)
		}
		targetRaw, exists := root[name]
		target, _ := targetRaw.(map[string]any)
		switch {
		case !exists:
			target = map[string]any{}
			root[name] = target
		case target == nil:
			return nil, fmt.Errorf("%s:%d: table [%s] conflicts with scalar key %s", policyPath, table.Line, name, name)
		}
		for _, pair := range table.Pairs {
			if _, exists := target[pair.Key]; exists {
				return nil, fmt.Errorf("%s:%d: duplicate key across table forms: %s.%s", policyPath, pair.Line, name, pair.Key)
			}
			target[pair.Key] = pair.Value
		}
	}
	return root, nil
}

func policySHA256(policyPath string) (string, error) {
	data, err := os.ReadFile(policyPath)
	if err != nil {
		return "", fmt.Errorf("read policy %s: %w", policyPath, err)
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
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
						rebasedEntry := maps.Clone(entryMap)
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
				rebasedSSH := maps.Clone(ssh)
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
						rebasedCredential := maps.Clone(credentialMap)
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
