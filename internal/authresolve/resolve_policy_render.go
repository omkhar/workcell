// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package authresolve

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/omkhar/workcell/internal/providerid"
)

func renderPolicyTOML(policy map[string]any) (string, error) {
	if err := validatePolicyCredentials(policy); err != nil {
		return "", err
	}

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
		var renderErr error
		lines, renderErr = renderOrderedThenSorted(lines, ssh,
			[]string{"enabled", "config", "known_hosts", "identities", "providers", "modes", "allow_unsafe_config"})
		if renderErr != nil {
			return "", renderErr
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
			var renderErr error
			lines, renderErr = renderOrderedThenSorted(lines, entryMap,
				[]string{"source", "target", "classification", "providers", "modes"})
			if renderErr != nil {
				return "", renderErr
			}
		}
	}

	// [network] survives the resolver unchanged so the injection layer sees the
	// merged allow/deny endpoint lists and applies the authoritative
	// host:port validation.  The resolver only passes the lists through.
	if network, ok := policy["network"].(map[string]any); ok && len(network) > 0 {
		lines = append(lines, "")
		lines = append(lines, "[network]")
		var renderErr error
		lines, renderErr = renderOrderedThenSorted(lines, network,
			[]string{"allow_endpoints", "deny_endpoints"})
		if renderErr != nil {
			return "", renderErr
		}
	}

	return strings.Join(lines, "\n") + "\n", nil
}

// renderOrderedThenSorted appends "key = value" TOML lines for m: first the
// keys listed in ordered (when present in m), then any remaining keys in
// sorted order. It returns the extended lines slice.
func renderOrderedThenSorted(lines []string, m map[string]any, ordered []string) ([]string, error) {
	seen := map[string]struct{}{}
	for _, key := range ordered {
		value, ok := m[key]
		if !ok {
			continue
		}
		rendered, err := renderTOMLValue(value)
		if err != nil {
			return nil, err
		}
		lines = append(lines, key+" = "+rendered)
		seen[key] = struct{}{}
	}
	for _, key := range sortedKeys(m) {
		if _, ok := seen[key]; ok {
			continue
		}
		rendered, err := renderTOMLValue(m[key])
		if err != nil {
			return nil, err
		}
		lines = append(lines, key+" = "+rendered)
	}
	return lines, nil
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
