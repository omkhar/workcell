// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

// bundle_load.go is the single shared implementation of the recursive
// injection-policy bundle loader that internal/injection,
// internal/authresolve, and internal/authpolicy previously triplicated.
// The per-caller security postures (include validation, extra policy
// validation, UTF-8 guard, version strictness, path expansion) stay in
// the callers and reach this loader through LoadOptions hooks; the
// structural walk, merge, and TOML-subset parsing live only here.

package injectionpolicy

import (
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/omkhar/workcell/internal/tomlsubset"
)

// RootPolicyKeys is the closed set of keys a root policy document may
// declare. All three policy-bundle loaders enforce the same set.
var RootPolicyKeys = map[string]struct{}{
	"version":     {},
	"includes":    {},
	"documents":   {},
	"ssh":         {},
	"copies":      {},
	"credentials": {},
	"network":     {},
}

// LoadOptions carries the only per-caller divergences between the three
// policy-bundle loaders. Everything not expressed here is shared.
type LoadOptions struct {
	// Parse parses one policy file into a policy map. Callers wrap
	// ParsePolicyTOML to add package-specific validation (injection's
	// UTF-8 guard; authresolve/authpolicy document, credential, and
	// network checks) so error ordering is preserved exactly.
	Parse func(content, policyPath string) (map[string]any, error)
	// ValidateInclude resolves and validates one includes[] entry and
	// returns the absolute include path. The three include-security
	// postures differ deliberately and stay per-caller.
	ValidateInclude func(raw any, label, baseDir, entrypointRoot string) (string, error)
	// ValidateMerged, when non-nil, runs on the merged map after each
	// recursion level's merge.
	ValidateMerged func(policy map[string]any) error
	// RebasePath rebases one path-valued fragment entry against the
	// fragment directory. Tilde-expansion edge behavior differs per
	// caller, so the hook is required.
	RebasePath func(raw any, fragmentDir string) any
	// Clone clones the loaded fragment before include stripping and
	// rebasing. nil means maps.Clone (the injection/authresolve shallow
	// behavior); authpolicy passes its deep clone.
	Clone func(policy map[string]any) map[string]any
	// StrictVersion rejects a non-integer version value instead of
	// defaulting it to 1 (injection and authresolve are strict;
	// authpolicy historically defaults).
	StrictVersion bool
}

// LoadBundle loads policyPath (already resolved by the caller) and its
// includes recursively, returning the merged policy and the source list.
func LoadBundle(policyPath, entrypointRoot string, reader *BundleReader, opts LoadOptions) (map[string]any, []PolicySource, error) {
	return loadBundleRecursive(policyPath, entrypointRoot, nil, map[string]struct{}{}, reader, opts)
}

func loadBundleRecursive(policyPath, entrypointRoot string, activeStack []string, loadedPaths map[string]struct{}, reader *BundleReader, opts LoadOptions) (map[string]any, []PolicySource, error) {
	if slices.Contains(activeStack, policyPath) {
		cycle := append(append([]string{}, activeStack...), policyPath)
		return nil, nil, fmt.Errorf("injection policy include cycle detected: %s", strings.Join(cycle, " -> "))
	}
	if _, ok := loadedPaths[policyPath]; ok {
		return nil, nil, fmt.Errorf("injection policy includes the same file more than once: %s", policyPath)
	}
	loadedPaths[policyPath] = struct{}{}

	file, err := reader.Read(policyPath)
	if err != nil {
		return nil, nil, err
	}
	loaded, err := opts.Parse(string(file.Bytes), policyPath)
	if err != nil {
		return nil, nil, err
	}
	if err := validateRootKeys(loaded); err != nil {
		return nil, nil, err
	}
	if err := fragmentVersionError(loaded, opts.StrictVersion); err != nil {
		return nil, nil, err
	}

	includesRaw, ok := loaded["includes"]
	includes := []any{}
	if ok && includesRaw != nil {
		includes, ok = includesRaw.([]any)
		if !ok {
			return nil, nil, errors.New("includes must be an array of strings when specified")
		}
	}

	merged := map[string]any{"version": 1}
	sources := make([]PolicySource, 0)
	nextStack := append(append([]string{}, activeStack...), policyPath)
	for index, include := range includes {
		includePath, err := opts.ValidateInclude(include, fmt.Sprintf("includes[%d]", index), filepath.Dir(policyPath), entrypointRoot)
		if err != nil {
			return nil, nil, err
		}
		included, includedSources, err := loadBundleRecursive(includePath, entrypointRoot, nextStack, loadedPaths, reader, opts)
		if err != nil {
			return nil, nil, err
		}
		if err := mergePolicyFragment(merged, included, includePath); err != nil {
			return nil, nil, err
		}
		sources = append(sources, includedSources...)
	}

	clone := opts.Clone
	if clone == nil {
		clone = maps.Clone
	}
	currentPolicy := clone(loaded)
	delete(currentPolicy, "includes")
	if len(activeStack) > 0 {
		currentPolicy = RebasePolicyFragment(currentPolicy, filepath.Dir(policyPath), opts.RebasePath)
	}
	if err := mergePolicyFragment(merged, currentPolicy, policyPath); err != nil {
		return nil, nil, err
	}
	if opts.ValidateMerged != nil {
		if err := opts.ValidateMerged(merged); err != nil {
			return nil, nil, err
		}
	}
	sources = append(sources, PolicySource{
		Path:     LogicalPolicyPath(policyPath, entrypointRoot),
		Sha256:   file.Sha256,
		Fragment: loaded,
	})
	return merged, sources, nil
}

func validateRootKeys(policy map[string]any) error {
	unknown := make([]string, 0)
	for key := range policy {
		if _, ok := RootPolicyKeys[key]; !ok {
			unknown = append(unknown, key)
		}
	}
	slices.Sort(unknown)
	if len(unknown) > 0 {
		return fmt.Errorf("root policy contains unsupported keys: %s", strings.Join(unknown, ", "))
	}
	return nil
}

func fragmentVersionError(policy map[string]any, strict bool) error {
	raw, ok := policy["version"]
	if !ok || raw == nil {
		return nil
	}
	if version, isInt := raw.(int); isInt {
		if version != 1 {
			return fmt.Errorf("unsupported injection policy version: %d", version)
		}
		return nil
	}
	if strict {
		return fmt.Errorf("unsupported injection policy version: %v", raw)
	}
	return nil
}

func mergePolicyFragment(base, addition map[string]any, sourcePath string) error {
	version := 1
	if raw, ok := addition["version"]; ok {
		if v, isInt := raw.(int); isInt {
			version = v
		}
	}
	if version != 1 {
		return fmt.Errorf("unsupported injection policy version: %d", version)
	}
	for _, tableName := range []string{"documents", "ssh", "credentials"} {
		tableRaw, ok := addition[tableName]
		if !ok || tableRaw == nil {
			continue
		}
		table, ok := tableRaw.(map[string]any)
		if !ok {
			return fmt.Errorf("injection policy fragment must keep %s as a table: %s", tableName, sourcePath)
		}
		destination, _ := base[tableName].(map[string]any)
		if destination == nil {
			destination = map[string]any{}
			base[tableName] = destination
		}
		for key, value := range table {
			if _, exists := destination[key]; exists {
				return fmt.Errorf("injection policy fragments declare the same setting more than once: %s.%s (%s)", tableName, key, sourcePath)
			}
			destination[key] = value
		}
	}
	if copiesRaw, ok := addition["copies"]; ok && copiesRaw != nil {
		copies, ok := copiesRaw.([]any)
		if !ok {
			return fmt.Errorf("injection policy fragment must keep copies as an array of tables: %s", sourcePath)
		}
		destinationCopies, _ := base["copies"].([]any)
		destinationCopies = append(destinationCopies, copies...)
		base["copies"] = destinationCopies
	}
	// [network] endpoint lists are unioned across fragments (unlike the
	// duplicate-rejecting tables above); this surface carries only endpoint
	// lists, never a mode, and the merged result is re-validated downstream.
	if networkRaw, ok := addition["network"]; ok && networkRaw != nil {
		network, ok := networkRaw.(map[string]any)
		if !ok {
			return fmt.Errorf("injection policy fragment must keep network as a table: %s", sourcePath)
		}
		destination, _ := base["network"].(map[string]any)
		if destination == nil {
			destination = map[string]any{}
			base["network"] = destination
		}
		for key, value := range network {
			existing, present := destination[key]
			if !present {
				destination[key] = value
				continue
			}
			existingList, existingOK := existing.([]any)
			additionList, additionOK := value.([]any)
			if !existingOK || !additionOK {
				return fmt.Errorf("injection policy fragments declare conflicting non-array network.%s: %s", key, sourcePath)
			}
			destination[key] = append(existingList, additionList...)
		}
	}
	return nil
}

// RebasePolicyFragment rebases the path-valued entries of an included
// fragment against the fragment's directory. rebasePath supplies the
// caller's path-expansion posture.
func RebasePolicyFragment(policy map[string]any, fragmentDir string, rebasePath func(raw any, fragmentDir string) any) map[string]any {
	rebased := map[string]any{}
	for key, value := range policy {
		switch key {
		case "documents":
			if table, ok := value.(map[string]any); ok {
				rebasedDocs := map[string]any{}
				for docKey, docValue := range table {
					rebasedDocs[docKey] = rebasePath(docValue, fragmentDir)
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
						rebasedEntry["source"] = rebasePath(source, fragmentDir)
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
						rebasedSSH[sshKey] = rebasePath(sshValue, fragmentDir)
					}
				}
				if identities, ok := rebasedSSH["identities"].([]any); ok {
					rebasedIDs := make([]any, 0, len(identities))
					for _, identity := range identities {
						rebasedIDs = append(rebasedIDs, rebasePath(identity, fragmentDir))
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
							rebasedCred["source"] = rebasePath(source, fragmentDir)
						}
						rebasedCreds[credKey] = rebasedCred
						continue
					}
					rebasedCreds[credKey] = rebasePath(credValue, fragmentDir)
				}
				rebased[key] = rebasedCreds
				continue
			}
		}
		rebased[key] = value
	}
	return rebased
}

// LogicalPolicyPath renders policyPath relative to the bundle entrypoint
// root, falling back to the input path when no relative form exists.
func LogicalPolicyPath(policyPath, entrypointRoot string) string {
	rel, err := filepath.Rel(entrypointRoot, policyPath)
	if err != nil {
		return policyPath
	}
	return filepath.ToSlash(rel)
}

// ParsePolicyTOML parses an injection-policy TOML file via the shared
// tomlsubset API and reshapes the result into the nested-map policy
// shape. Injection policy allows one specific array-of-tables construct
// ([[copies]]) which the strict subset parser rejects, so those blocks
// are stripped and parsed separately before reassembly. This function is
// THE parser seam: tomlsubset is referenced only here and in its
// unexported helpers, so a future parser swap happens in one place.
// credentialTableKeys is the caller's [credentials.<name>] whitelist.
func ParsePolicyTOML(content, policyPath string, credentialTableKeys map[string]struct{}) (map[string]any, error) {
	subsetContent, copiesEntries, err := extractCopiesBlocks(content, policyPath)
	if err != nil {
		return nil, err
	}
	doc, err := tomlsubset.ParseDocument(subsetContent, policyPath)
	if err != nil {
		return nil, err
	}
	root, err := documentToPolicyMap(doc, policyPath, credentialTableKeys)
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
// following key/value block as a single TOML subset table, and returns
// the remaining content with those blocks elided plus the parsed entries
// in declaration order. Any other [[array-of-table]] header is rejected
// here so the caller-visible error message matches the legacy parsers.
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
	result := strings.TrimSuffix(kept.String(), "\n")
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

// documentToPolicyMap converts a tomlsubset.Document into the nested-map
// policy shape. Only the `documents`, `ssh`, `credentials`, `network`,
// and `credentials.<name>` tables get structural treatment; any other
// table name is rejected to preserve the strict subset semantics.
func documentToPolicyMap(doc *tomlsubset.Document, policyPath string, credentialTableKeys map[string]struct{}) (map[string]any, error) {
	root := map[string]any{}
	for _, pair := range doc.TopLevel.Pairs {
		root[pair.Key] = pair.Value
	}
	for _, table := range doc.Tables {
		name := table.Name
		if strings.HasPrefix(name, "credentials.") {
			credentialKey := strings.SplitN(name, ".", 2)[1]
			if _, ok := credentialTableKeys[credentialKey]; !ok {
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
		if name != "documents" && name != "ssh" && name != "credentials" && name != "network" {
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
