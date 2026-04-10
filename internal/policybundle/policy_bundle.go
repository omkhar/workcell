// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package policybundle

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"unicode/utf8"
)

var SupportedAgents = map[string]struct{}{
	"codex":  {},
	"claude": {},
	"gemini": {},
}

var SupportedModes = map[string]struct{}{
	"strict":      {},
	"development": {},
	"build":       {},
	"breakglass":  {},
}

var CredentialKeys = map[string]struct{}{
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

var AgentScopedCredentialKeys = map[string]map[string]struct{}{
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

var SharedCredentialKeys = map[string]struct{}{
	"github_hosts":  {},
	"github_config": {},
}

var AllowedRootPolicyKeys = map[string]struct{}{
	"version":     {},
	"includes":    {},
	"documents":   {},
	"ssh":         {},
	"copies":      {},
	"credentials": {},
}

var systemSymlinkAllowlist = map[string]struct{}{}

var getUID = os.Getuid

func init() {
	if runtime.GOOS == "darwin" {
		systemSymlinkAllowlist["/var"] = struct{}{}
		systemSymlinkAllowlist["/tmp"] = struct{}{}
	}
}

type PolicySource struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

func die(message string) error {
	return errors.New(message)
}

func ExpandHostPath(raw string, base string) (string, error) {
	expanded, err := expandUserPath(raw)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(expanded) {
		expanded = filepath.Join(base, expanded)
	}
	return filepath.Abs(expanded)
}

func RequirePathWithin(root string, candidate string, label string) error {
	resolvedRoot, err := resolveExistingPath(root)
	if err != nil {
		return err
	}
	resolvedCandidate, err := resolveExistingPath(candidate)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(resolvedRoot, resolvedCandidate)
	if err != nil {
		return err
	}
	if rel == "." {
		return nil
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return die(fmt.Sprintf("%s must stay within %s: %s", label, resolvedRoot, resolvedCandidate))
	}
	return nil
}

func RequireNoSymlinkInPathChain(path string, label string) error {
	current := filepath.Clean(path)
	for {
		info, err := os.Lstat(current)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 && !systemSymlinkAllowed(current) {
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

func ValidateSourcePath(raw any, label string, base string) (string, error) {
	text, ok := raw.(string)
	if !ok || text == "" {
		return "", die(fmt.Sprintf("%s must be a non-empty string path", label))
	}
	source, err := ExpandHostPath(text, base)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(source); err != nil {
		return "", die(fmt.Sprintf("%s does not exist: %s", label, source))
	}
	if err := RequireNoSymlinkInPathChain(source, label); err != nil {
		return "", err
	}
	return source, nil
}

func ValidateAllowedKeys(table map[string]any, allowedKeys map[string]struct{}, label string) error {
	unknown := make([]string, 0)
	for key := range table {
		if _, ok := allowedKeys[key]; !ok {
			unknown = append(unknown, key)
		}
	}
	sort.Strings(unknown)
	if len(unknown) > 0 {
		return die(fmt.Sprintf("%s contains unsupported keys: %s", label, strings.Join(unknown, ", ")))
	}
	return nil
}

func SelectedFor(values any, current string, label string, allowedValues map[string]struct{}) (bool, error) {
	if values == nil {
		return true, nil
	}
	items, err := stringList(values)
	if err != nil {
		if err.Error() == "values must be strings" {
			return false, err
		}
		return false, die(fmt.Sprintf("%s must be a non-empty array when specified", label))
	}
	if len(items) == 0 {
		return false, die(fmt.Sprintf("%s must be a non-empty array when specified", label))
	}
	for _, value := range items {
		if _, ok := allowedValues[value]; !ok {
			return false, die(fmt.Sprintf("%s contains unsupported value: %s", label, value))
		}
	}
	return contains(items, current), nil
}

func StripComment(line string) string {
	escaped := false
	quoteChar := byte(0)
	result := make([]byte, 0, len(line))
	for i := 0; i < len(line); i++ {
		char := line[i]
		if escaped {
			result = append(result, char)
			escaped = false
			continue
		}
		if char == '\\' && quoteChar == '"' {
			result = append(result, char)
			escaped = true
			continue
		}
		if char == '"' || char == '\'' {
			if quoteChar == 0 {
				quoteChar = char
			} else if quoteChar == char {
				quoteChar = 0
			}
			result = append(result, char)
			continue
		}
		if char == '#' && quoteChar == 0 {
			break
		}
		result = append(result, char)
	}
	return strings.TrimSpace(string(result))
}

func ParseValue(raw string, policyPath string, lineno int) (any, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return nil, die(fmt.Sprintf("%s:%d: expected a value", policyPath, lineno))
	}
	if value == "true" || value == "false" {
		return value == "true", nil
	}
	if strings.HasPrefix(value, "\"") && strings.HasSuffix(value, "\"") {
		return unquoteDoubleQuoted(value, policyPath, lineno)
	}
	if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
		return parseArrayOfStrings(value, policyPath, lineno)
	}
	if digitsOnly(value) {
		n, err := strconv.Atoi(value)
		if err != nil {
			return nil, err
		}
		return n, nil
	}
	return nil, die(fmt.Sprintf(
		"%s:%d: unsupported TOML value; use quoted strings, booleans, integers, or arrays of strings",
		policyPath, lineno,
	))
}

func ParseTOMLSubset(content string, policyPath string) (map[string]any, error) {
	root := map[string]any{}
	current := root
	seenTables := map[string]struct{}{}

	scanner := bufio.NewScanner(strings.NewReader(content))
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := StripComment(scanner.Text())
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "[[") && strings.HasSuffix(line, "]]") {
			tableName := strings.TrimSpace(line[2 : len(line)-2])
			if tableName != "copies" {
				return nil, die(fmt.Sprintf("%s:%d: unsupported array-of-table [%s]", policyPath, lineNo, tableName))
			}
			copies, ok := root["copies"].([]map[string]any)
			if !ok && root["copies"] != nil {
				return nil, die(fmt.Sprintf("%s:%d: copies must remain an array of tables", policyPath, lineNo))
			}
			entry := map[string]any{}
			copies = append(copies, entry)
			root["copies"] = copies
			current = entry
			continue
		}

		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			tableName := strings.TrimSpace(line[1 : len(line)-1])
			if _, ok := seenTables[tableName]; ok {
				return nil, die(fmt.Sprintf("%s:%d: duplicate table [%s]", policyPath, lineNo, tableName))
			}
			seenTables[tableName] = struct{}{}
			if strings.HasPrefix(tableName, "credentials.") {
				credentialKey := strings.SplitN(tableName, ".", 2)[1]
				if _, ok := CredentialKeys[credentialKey]; !ok {
					return nil, die(fmt.Sprintf("%s:%d: unsupported credentials table [%s]", policyPath, lineNo, tableName))
				}
				credentials, ok := root["credentials"].(map[string]any)
				if !ok {
					credentials = map[string]any{}
					root["credentials"] = credentials
				}
				entry, ok := credentials[credentialKey].(map[string]any)
				if !ok {
					entry = map[string]any{}
					credentials[credentialKey] = entry
				}
				current = entry
				continue
			}
			if tableName != "documents" && tableName != "ssh" && tableName != "credentials" {
				return nil, die(fmt.Sprintf("%s:%d: unsupported table [%s]", policyPath, lineNo, tableName))
			}
			table, ok := root[tableName].(map[string]any)
			if !ok {
				table = map[string]any{}
				root[tableName] = table
			}
			current = table
			continue
		}

		if !strings.Contains(line, "=") {
			return nil, die(fmt.Sprintf("%s:%d: expected key = value", policyPath, lineNo))
		}
		parts := strings.SplitN(line, "=", 2)
		key := strings.TrimSpace(parts[0])
		value := parts[1]
		if key == "" {
			return nil, die(fmt.Sprintf("%s:%d: empty key", policyPath, lineNo))
		}
		if strings.Contains(key, ".") {
			return nil, die(fmt.Sprintf(
				"%s:%d: dotted TOML keys are not supported; use explicit [table] headers instead",
				policyPath, lineNo,
			))
		}
		if _, ok := current[key]; ok {
			return nil, die(fmt.Sprintf("%s:%d: duplicate key: %s", policyPath, lineNo, key))
		}
		parsed, err := ParseValue(value, policyPath, lineNo)
		if err != nil {
			return nil, err
		}
		current[key] = parsed
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return root, nil
}

func PolicySHA256(policyPath string) (string, error) {
	data, err := os.ReadFile(policyPath)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func CompositePolicySHA256(policySources []PolicySource) string {
	sources := make([]PolicySource, len(policySources))
	copy(sources, policySources)
	sort.SliceStable(sources, func(i, j int) bool {
		return sources[i].Path < sources[j].Path
	})
	var b strings.Builder
	b.WriteByte('[')
	for i, source := range sources {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString("{'path': ")
		b.WriteString(pythonReprString(source.Path))
		b.WriteString(", 'sha256': ")
		b.WriteString(pythonReprString(source.SHA256))
		b.WriteByte('}')
	}
	b.WriteByte(']')
	sum := sha256.Sum256([]byte(b.String()))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func LogicalPolicyPath(policyPath string, entrypointRoot string) (string, error) {
	rel, err := filepath.Rel(entrypointRoot, policyPath)
	if err != nil {
		return "", err
	}
	return filepath.ToSlash(rel), nil
}

func RebaseFragmentPath(raw any, fragmentDir string) any {
	text, ok := raw.(string)
	if !ok || text == "" {
		return raw
	}
	rebased, err := ExpandHostPath(text, fragmentDir)
	if err != nil {
		return raw
	}
	return rebased
}

func RebasePolicyFragment(policy map[string]any, fragmentDir string) map[string]any {
	rebased := map[string]any{}
	for key, value := range policy {
		switch key {
		case "documents":
			if documents, ok := asStringMap(value); ok {
				next := map[string]any{}
				for documentKey, documentValue := range documents {
					next[documentKey] = RebaseFragmentPath(documentValue, fragmentDir)
				}
				rebased[key] = next
				continue
			}
		case "copies":
			if copies, ok := value.([]map[string]any); ok {
				next := make([]map[string]any, 0, len(copies))
				for _, entry := range copies {
					if entry == nil {
						next = append(next, nil)
						continue
					}
					rebasedEntry := cloneStringAnyMap(entry)
					if source, ok := rebasedEntry["source"]; ok {
						rebasedEntry["source"] = RebaseFragmentPath(source, fragmentDir)
					}
					next = append(next, rebasedEntry)
				}
				rebased[key] = next
				continue
			}
		case "ssh":
			if ssh, ok := asStringMap(value); ok {
				next := cloneAnyMap(ssh)
				for _, sshKey := range []string{"config", "known_hosts"} {
					if rawValue, ok := next[sshKey]; ok {
						next[sshKey] = RebaseFragmentPath(rawValue, fragmentDir)
					}
				}
				if identities, ok := next["identities"].([]string); ok {
					nextIdentities := make([]string, len(identities))
					for i, identity := range identities {
						rebasedIdentity, _ := RebaseFragmentPath(identity, fragmentDir).(string)
						nextIdentities[i] = rebasedIdentity
					}
					next["identities"] = nextIdentities
				}
				if identities, ok := next["identities"].([]any); ok {
					nextIdentities := make([]any, len(identities))
					for i, identity := range identities {
						nextIdentities[i] = RebaseFragmentPath(identity, fragmentDir)
					}
					next["identities"] = nextIdentities
				}
				rebased[key] = next
				continue
			}
		case "credentials":
			if credentials, ok := asStringMap(value); ok {
				next := map[string]any{}
				for credentialKey, credentialValue := range credentials {
					if entry, ok := asStringMap(credentialValue); ok {
						rebasedEntry := cloneAnyMap(entry)
						if source, ok := rebasedEntry["source"]; ok {
							rebasedEntry["source"] = RebaseFragmentPath(source, fragmentDir)
						}
						next[credentialKey] = rebasedEntry
					} else {
						next[credentialKey] = RebaseFragmentPath(credentialValue, fragmentDir)
					}
				}
				rebased[key] = next
				continue
			}
		}
		rebased[key] = value
	}
	return rebased
}

func ValidatePolicyInclude(raw any, label string, base string, entrypointRoot string) (string, error) {
	source, err := ValidateSourcePath(raw, label, base)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(source)
	if err != nil {
		return "", err
	}
	if !info.IsDir() && !info.Mode().IsRegular() {
		return "", die(fmt.Sprintf("%s must point at a file: %s", label, source))
	}
	if info.IsDir() {
		return "", die(fmt.Sprintf("%s must point at a file: %s", label, source))
	}
	resolvedSource, err := resolveExistingPath(source)
	if err != nil {
		return "", err
	}
	if err := RequirePathWithin(entrypointRoot, resolvedSource, label); err != nil {
		return "", err
	}
	return resolvedSource, nil
}

func MergePolicyFragment(base map[string]any, addition map[string]any, sourcePath string) error {
	version := 1
	if raw, ok := addition["version"]; ok {
		if v, ok := raw.(int); ok {
			version = v
		} else {
			return die(fmt.Sprintf("unsupported injection policy version: %v", raw))
		}
	}
	if version != 1 {
		return die(fmt.Sprintf("unsupported injection policy version: %d", version))
	}

	for _, tableName := range []string{"documents", "ssh", "credentials"} {
		table := addition[tableName]
		if table == nil {
			continue
		}
		tableMap, ok := table.(map[string]any)
		if !ok {
			return die(fmt.Sprintf("injection policy fragment must keep %s as a table: %s", tableName, sourcePath))
		}
		destination, ok := base[tableName].(map[string]any)
		if !ok {
			if base[tableName] != nil {
				return die(fmt.Sprintf("injection policy merge corrupted %s: %s", tableName, sourcePath))
			}
			destination = map[string]any{}
			base[tableName] = destination
		}
		for key, value := range tableMap {
			if _, exists := destination[key]; exists {
				return die(fmt.Sprintf(
					"injection policy fragments declare the same setting more than once: %s.%s (%s)",
					tableName, key, sourcePath,
				))
			}
			destination[key] = value
		}
	}

	copies := addition["copies"]
	if copies == nil {
		return nil
	}
	copyList, ok := copies.([]map[string]any)
	if !ok {
		if _, ok := copies.([]any); ok {
			return die(fmt.Sprintf("injection policy fragment must keep copies as an array of tables: %s", sourcePath))
		}
		return die(fmt.Sprintf("injection policy fragment must keep copies as an array of tables: %s", sourcePath))
	}
	destinationCopies, ok := base["copies"].([]map[string]any)
	if !ok && base["copies"] != nil {
		return die(fmt.Sprintf("injection policy merge corrupted copies: %s", sourcePath))
	}
	destinationCopies = append(destinationCopies, copyList...)
	base["copies"] = destinationCopies
	return nil
}

func LoadPolicyBundle(policyPath string) (map[string]any, []PolicySource, error) {
	return loadPolicyBundle(policyPath, "", nil, map[string]struct{}{})
}

func LoadPolicyBundleAt(policyPath string, entrypointRoot string) (map[string]any, []PolicySource, error) {
	return loadPolicyBundle(policyPath, entrypointRoot, nil, map[string]struct{}{})
}

func loadPolicyBundle(policyPath string, entrypointRoot string, activeStack []string, loadedPaths map[string]struct{}) (map[string]any, []PolicySource, error) {
	resolvedPolicyPath, err := resolveExistingPath(policyPath)
	if err != nil {
		return nil, nil, err
	}
	if entrypointRoot == "" {
		entrypointRoot = filepath.Dir(resolvedPolicyPath)
	} else {
		entrypointRoot, err = resolveExistingPath(entrypointRoot)
		if err != nil {
			return nil, nil, err
		}
	}

	if contains(activeStack, resolvedPolicyPath) {
		cycle := append(append([]string{}, activeStack...), resolvedPolicyPath)
		return nil, nil, die(fmt.Sprintf("injection policy include cycle detected: %s", strings.Join(cycle, " -> ")))
	}
	if _, ok := loadedPaths[resolvedPolicyPath]; ok {
		return nil, nil, die(fmt.Sprintf("injection policy includes the same file more than once: %s", resolvedPolicyPath))
	}
	loadedPaths[resolvedPolicyPath] = struct{}{}

	data, err := os.ReadFile(resolvedPolicyPath)
	if err != nil {
		return nil, nil, err
	}
	loaded, err := ParseTOMLSubset(string(data), resolvedPolicyPath)
	if err != nil {
		return nil, nil, err
	}
	if loaded == nil {
		return nil, nil, die(fmt.Sprintf("injection policy must decode to a TOML table: %s", resolvedPolicyPath))
	}
	if err := ValidateAllowedKeys(loaded, AllowedRootPolicyKeys, "root policy"); err != nil {
		return nil, nil, err
	}

	version := 1
	if rawVersion, ok := loaded["version"]; ok {
		if v, ok := rawVersion.(int); ok {
			version = v
		} else {
			return nil, nil, die(fmt.Sprintf("unsupported injection policy version: %v", rawVersion))
		}
	}
	if version != 1 {
		return nil, nil, die(fmt.Sprintf("unsupported injection policy version: %d", version))
	}

	includes := []string{}
	if rawIncludes, ok := loaded["includes"]; ok {
		if rawIncludes == nil {
			includes = []string{}
		} else {
			var ok bool
			includes, ok = rawIncludes.([]string)
			if !ok {
				return nil, nil, die("includes must be an array of strings when specified")
			}
		}
	}

	merged := map[string]any{"version": 1}
	sources := make([]PolicySource, 0)
	nextStack := append(append([]string{}, activeStack...), resolvedPolicyPath)
	for index, include := range includes {
		includePath, err := ValidatePolicyInclude(include, fmt.Sprintf("includes[%d]", index), filepath.Dir(resolvedPolicyPath), entrypointRoot)
		if err != nil {
			return nil, nil, err
		}
		includedPolicy, includedSources, err := loadPolicyBundle(includePath, entrypointRoot, nextStack, loadedPaths)
		if err != nil {
			return nil, nil, err
		}
		if err := MergePolicyFragment(merged, includedPolicy, includePath); err != nil {
			return nil, nil, err
		}
		sources = append(sources, includedSources...)
	}

	currentPolicy := cloneAnyMap(loaded)
	delete(currentPolicy, "includes")
	if len(activeStack) > 0 {
		currentPolicy = RebasePolicyFragment(currentPolicy, filepath.Dir(resolvedPolicyPath))
	}
	if err := MergePolicyFragment(merged, currentPolicy, resolvedPolicyPath); err != nil {
		return nil, nil, err
	}
	logicalPath, err := LogicalPolicyPath(resolvedPolicyPath, entrypointRoot)
	if err != nil {
		return nil, nil, err
	}
	policySHA, err := PolicySHA256(resolvedPolicyPath)
	if err != nil {
		return nil, nil, err
	}
	sources = append(sources, PolicySource{Path: logicalPath, SHA256: policySHA})
	return merged, sources, nil
}

func LoadRawPolicy(policyPath string) (map[string]any, error) {
	if _, err := os.Stat(policyPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]any{"version": 1}, nil
		}
		return nil, err
	}
	data, err := os.ReadFile(policyPath)
	if err != nil {
		return nil, err
	}
	loaded, err := ParseTOMLSubset(string(data), policyPath)
	if err != nil {
		return nil, err
	}
	if loaded == nil {
		return nil, die(fmt.Sprintf("injection policy must decode to a TOML table: %s", policyPath))
	}
	if err := ValidateAllowedKeys(loaded, AllowedRootPolicyKeys, "root policy"); err != nil {
		return nil, err
	}
	if _, ok := loaded["version"]; !ok {
		loaded["version"] = 1
		return loaded, nil
	}
	version, ok := loaded["version"].(int)
	if !ok {
		return nil, die(fmt.Sprintf("unsupported injection policy version: %v", loaded["version"]))
	}
	if version != 1 {
		return nil, die(fmt.Sprintf("unsupported injection policy version: %d", version))
	}
	return loaded, nil
}

func QuoteString(value string) string {
	return JSONQuote(value)
}

func JSONQuote(value string) string {
	escaped := strings.ReplaceAll(value, "\\", "\\\\")
	escaped = strings.ReplaceAll(escaped, "\"", "\\\"")
	escaped = strings.ReplaceAll(escaped, "\n", "\\n")
	return "\"" + escaped + "\""
}

func RenderTOMLValue(value any) (string, error) {
	switch typed := value.(type) {
	case bool:
		if typed {
			return "true", nil
		}
		return "false", nil
	case int:
		return strconv.Itoa(typed), nil
	case string:
		return JSONQuote(typed), nil
	case []string:
		items := make([]string, len(typed))
		for i, item := range typed {
			items[i] = JSONQuote(item)
		}
		return "[" + strings.Join(items, ", ") + "]", nil
	case []any:
		items := make([]string, len(typed))
		for i, item := range typed {
			text, ok := item.(string)
			if !ok {
				return "", die("only arrays of strings are supported in rendered policy output")
			}
			items[i] = JSONQuote(text)
		}
		return "[" + strings.Join(items, ", ") + "]", nil
	default:
		return "", die(fmt.Sprintf("unsupported TOML value type: %T", value))
	}
}

func RenderPolicyTOML(policy map[string]any) (string, error) {
	lines := make([]string, 0)
	version := 1
	if rawVersion, ok := policy["version"]; ok {
		if typed, ok := rawVersion.(int); ok {
			version = typed
		} else {
			return "", die(fmt.Sprintf("unsupported TOML value type: %T", rawVersion))
		}
	}
	renderedVersion, err := RenderTOMLValue(version)
	if err != nil {
		return "", err
	}
	lines = append(lines, "version = "+renderedVersion)

	if includes, ok := policy["includes"].([]string); ok && len(includes) > 0 {
		rendered, err := RenderTOMLValue(includes)
		if err != nil {
			return "", err
		}
		lines = append(lines, "includes = "+rendered)
	}

	if documents, ok := asStringMap(policy["documents"]); ok && len(documents) > 0 {
		lines = append(lines, "")
		lines = append(lines, "[documents]")
		for _, key := range []string{"common", "codex", "claude", "gemini"} {
			if value, ok := documents[key]; ok {
				rendered, err := RenderTOMLValue(value)
				if err != nil {
					return "", err
				}
				lines = append(lines, fmt.Sprintf("%s = %s", key, rendered))
			}
		}
	}

	if credentials, ok := asStringMap(policy["credentials"]); ok && len(credentials) > 0 {
		scalarEntries := map[string]any{}
		for key, value := range credentials {
			if _, ok := value.(map[string]any); !ok {
				scalarEntries[key] = value
			}
		}
		if len(scalarEntries) > 0 {
			lines = append(lines, "")
			lines = append(lines, "[credentials]")
			keys := sortedAnyMapKeys(scalarEntries)
			for _, key := range keys {
				rendered, err := RenderTOMLValue(scalarEntries[key])
				if err != nil {
					return "", err
				}
				lines = append(lines, fmt.Sprintf("%s = %s", key, rendered))
			}
		}
		keys := sortedAnyMapKeys(credentials)
		for _, key := range keys {
			value := credentials[key]
			entry, ok := value.(map[string]any)
			if !ok {
				continue
			}
			lines = append(lines, "")
			lines = append(lines, fmt.Sprintf("[credentials.%s]", key))
			for _, field := range sortedAnyMapKeys(entry) {
				rendered, err := RenderTOMLValue(entry[field])
				if err != nil {
					return "", err
				}
				lines = append(lines, fmt.Sprintf("%s = %s", field, rendered))
			}
		}
	}

	if ssh, ok := asStringMap(policy["ssh"]); ok && len(ssh) > 0 {
		lines = append(lines, "")
		lines = append(lines, "[ssh]")
		ordered := []string{"enabled", "config", "known_hosts", "identities", "providers", "modes", "allow_unsafe_config"}
		seen := map[string]struct{}{}
		for _, key := range ordered {
			if value, ok := ssh[key]; ok {
				rendered, err := RenderTOMLValue(value)
				if err != nil {
					return "", err
				}
				lines = append(lines, fmt.Sprintf("%s = %s", key, rendered))
				seen[key] = struct{}{}
			}
		}
		extras := make([]string, 0)
		for key := range ssh {
			if _, ok := seen[key]; !ok {
				extras = append(extras, key)
			}
		}
		sort.Strings(extras)
		for _, key := range extras {
			rendered, err := RenderTOMLValue(ssh[key])
			if err != nil {
				return "", err
			}
			lines = append(lines, fmt.Sprintf("%s = %s", key, rendered))
		}
	}

	if copies, ok := policy["copies"].([]map[string]any); ok && len(copies) > 0 {
		for _, entry := range copies {
			if entry == nil {
				return "", die("copies entries must be TOML tables when rendering policy output")
			}
			lines = append(lines, "")
			lines = append(lines, "[[copies]]")
			ordered := []string{"source", "target", "classification", "providers", "modes"}
			seen := map[string]struct{}{}
			for _, key := range ordered {
				if value, ok := entry[key]; ok {
					rendered, err := RenderTOMLValue(value)
					if err != nil {
						return "", err
					}
					lines = append(lines, fmt.Sprintf("%s = %s", key, rendered))
					seen[key] = struct{}{}
				}
			}
			extras := make([]string, 0)
			for key := range entry {
				if _, ok := seen[key]; !ok {
					extras = append(extras, key)
				}
			}
			sort.Strings(extras)
			for _, key := range extras {
				rendered, err := RenderTOMLValue(entry[key])
				if err != nil {
					return "", err
				}
				lines = append(lines, fmt.Sprintf("%s = %s", key, rendered))
			}
		}
	}

	return strings.Join(lines, "\n") + "\n", nil
}

func WritePolicyFile(policyPath string, policy map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(policyPath), 0o755); err != nil {
		return err
	}
	rendered, err := RenderPolicyTOML(policy)
	if err != nil {
		return err
	}
	if err := os.WriteFile(policyPath, []byte(rendered), 0o600); err != nil {
		return err
	}
	return os.Chmod(policyPath, 0o600)
}

func RequireSecretFile(source string, label string) (string, error) {
	if err := requireNoSymlink(source, label); err != nil {
		return "", err
	}
	info, err := os.Stat(source)
	if err != nil {
		return "", die(fmt.Sprintf("%s must point at a file: %s", label, source))
	}
	if !info.Mode().IsRegular() {
		return "", die(fmt.Sprintf("%s must point at a file: %s", label, source))
	}
	pathStat, err := os.Lstat(source)
	if err != nil {
		return "", err
	}
	sysStat, ok := pathStat.Sys().(*syscall.Stat_t)
	if !ok {
		return "", errors.New("unsupported file stat type")
	}
	if int(sysStat.Uid) != getUID() {
		return "", die(fmt.Sprintf("%s must be owned by uid %d: %s", label, getUID(), source))
	}
	if pathStat.Mode().Perm()&0o077 != 0 {
		return "", die(fmt.Sprintf("%s must not be group/world-accessible: %s", label, source))
	}
	return source, nil
}

func requireNoSymlink(source string, label string) error {
	if info, err := os.Lstat(source); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return die(fmt.Sprintf("%s must not be a symlink: %s", label, source))
		}
	}
	return nil
}

func expandUserPath(raw string) (string, error) {
	if raw == "" {
		return "", errors.New("empty path")
	}
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
	if strings.HasPrefix(raw, "~") {
		slash := strings.IndexByte(raw, '/')
		userName := raw[1:]
		remainder := ""
		if slash >= 0 {
			userName = raw[1:slash]
			remainder = raw[slash+1:]
		}
		usr, err := user.Lookup(userName)
		if err != nil || usr.HomeDir == "" {
			return raw, nil
		}
		if remainder == "" {
			return usr.HomeDir, nil
		}
		return filepath.Join(usr.HomeDir, remainder), nil
	}
	return raw, nil
}

func resolveExistingPath(raw string) (string, error) {
	expanded, err := expandUserPath(raw)
	if err != nil {
		return "", err
	}
	absolute, err := filepath.Abs(expanded)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(absolute)
}

func parseArrayOfStrings(raw string, policyPath string, lineno int) ([]string, error) {
	if len(raw) < 2 {
		return nil, die(fmt.Sprintf("%s:%d: expected an array value", policyPath, lineno))
	}
	inner := strings.TrimSpace(raw[1 : len(raw)-1])
	if inner == "" {
		return []string{}, nil
	}
	values := make([]string, 0)
	i := 0
	for i < len(inner) {
		for i < len(inner) && isSpace(inner[i]) {
			i++
		}
		if i >= len(inner) {
			break
		}
		if inner[i] == ',' {
			return nil, die(fmt.Sprintf("%s:%d: only arrays of strings are supported", policyPath, lineno))
		}
		if inner[i] != '"' && inner[i] != '\'' {
			return nil, die(fmt.Sprintf("%s:%d: only arrays of strings are supported", policyPath, lineno))
		}
		value, next, err := parseQuotedLiteral(inner, i)
		if err != nil {
			return nil, die(fmt.Sprintf("%s:%d: %v", policyPath, lineno, err))
		}
		values = append(values, value)
		i = next
		for i < len(inner) && isSpace(inner[i]) {
			i++
		}
		if i >= len(inner) {
			break
		}
		if inner[i] != ',' {
			return nil, die(fmt.Sprintf("%s:%d: only arrays of strings are supported", policyPath, lineno))
		}
		i++
		for i < len(inner) && isSpace(inner[i]) {
			i++
		}
		if i >= len(inner) {
			break
		}
	}
	return values, nil
}

func unquoteDoubleQuoted(raw string, policyPath string, lineno int) (string, error) {
	if len(raw) < 2 || raw[0] != '"' || raw[len(raw)-1] != '"' {
		return "", die(fmt.Sprintf("%s:%d: unsupported TOML value; use quoted strings, booleans, integers, or arrays of strings", policyPath, lineno))
	}
	value, _, err := parseQuotedLiteral(raw, 0)
	if err != nil {
		return "", die(fmt.Sprintf("%s:%d: %v", policyPath, lineno, err))
	}
	return value, nil
}

func parseQuotedLiteral(text string, start int) (string, int, error) {
	quote := text[start]
	if quote != '"' && quote != '\'' {
		return "", start, errors.New("expected quoted string")
	}
	var b strings.Builder
	i := start + 1
	for i < len(text) {
		char := text[i]
		if char == quote {
			return b.String(), i + 1, nil
		}
		if char == '\\' {
			i++
			if i >= len(text) {
				return "", start, errors.New("unterminated quoted value")
			}
			esc := text[i]
			switch esc {
			case '\\', '"', '\'':
				b.WriteByte(esc)
			case 'n':
				b.WriteByte('\n')
			case 'r':
				b.WriteByte('\r')
			case 't':
				b.WriteByte('\t')
			case 'b':
				b.WriteByte('\b')
			case 'f':
				b.WriteByte('\f')
			case 'a':
				b.WriteByte('\a')
			case 'v':
				b.WriteByte('\v')
			case 'x':
				if i+2 >= len(text) {
					return "", start, errors.New("invalid hex escape")
				}
				value, err := parseHexByte(text[i+1 : i+3])
				if err != nil {
					return "", start, err
				}
				b.WriteByte(value)
				i += 2
			case 'u':
				if i+4 >= len(text) {
					return "", start, errors.New("invalid unicode escape")
				}
				value, err := parseHexRune(text[i+1:i+5], 16)
				if err != nil {
					return "", start, err
				}
				b.WriteRune(value)
				i += 4
			case 'U':
				if i+8 >= len(text) {
					return "", start, errors.New("invalid unicode escape")
				}
				value, err := parseHexRune(text[i+1:i+9], 32)
				if err != nil {
					return "", start, err
				}
				b.WriteRune(value)
				i += 8
			default:
				return "", start, fmt.Errorf("unknown escape sequence \\%c", esc)
			}
			i++
			continue
		}
		b.WriteByte(char)
		i++
	}
	return "", start, errors.New("unterminated quoted value")
}

func parseHexByte(text string) (byte, error) {
	value, err := strconv.ParseUint(text, 16, 8)
	if err != nil {
		return 0, errors.New("invalid escape sequence")
	}
	return byte(value), nil
}

func parseHexRune(text string, bitSize int) (rune, error) {
	value, err := strconv.ParseUint(text, 16, bitSize)
	if err != nil {
		return 0, errors.New("invalid escape sequence")
	}
	if value > utf8.MaxRune {
		return 0, errors.New("invalid unicode escape")
	}
	return rune(value), nil
}

func systemSymlinkAllowed(path string) bool {
	_, ok := systemSymlinkAllowlist[path]
	return ok
}

func contains(values []string, current string) bool {
	for _, value := range values {
		if value == current {
			return true
		}
	}
	return false
}

func digitsOnly(value string) bool {
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

func isSpace(b byte) bool {
	switch b {
	case ' ', '\t', '\n', '\r', '\f', '\v':
		return true
	default:
		return false
	}
}

func stringList(value any) ([]string, error) {
	switch typed := value.(type) {
	case []string:
		return typed, nil
	case []any:
		items := make([]string, len(typed))
		for i, item := range typed {
			text, ok := item.(string)
			if !ok {
				return nil, die("values must be strings")
			}
			items[i] = text
		}
		return items, nil
	default:
		return nil, errors.New("not a string slice")
	}
}

func asStringMap(value any) (map[string]any, bool) {
	typed, ok := value.(map[string]any)
	return typed, ok
}

func cloneAnyMap(src map[string]any) map[string]any {
	dst := make(map[string]any, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func cloneStringAnyMap(src map[string]any) map[string]any {
	return cloneAnyMap(src)
}

func sortedAnyMapKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func pythonReprString(value string) string {
	var b strings.Builder
	b.WriteByte('\'')
	for i := 0; i < len(value); i++ {
		switch value[i] {
		case '\\':
			b.WriteString("\\\\")
		case '\'':
			b.WriteString("\\'")
		case '\n':
			b.WriteString("\\n")
		case '\r':
			b.WriteString("\\r")
		case '\t':
			b.WriteString("\\t")
		default:
			b.WriteByte(value[i])
		}
	}
	b.WriteByte('\'')
	return b.String()
}
