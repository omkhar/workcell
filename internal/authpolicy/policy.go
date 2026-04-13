// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package authpolicy

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
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

var (
	SupportedAgents = map[string]struct{}{
		"codex":  {},
		"claude": {},
		"gemini": {},
	}
	SupportedModes = map[string]struct{}{
		"strict":      {},
		"development": {},
		"build":       {},
		"breakglass":  {},
	}
	CredentialKeys = map[string]struct{}{
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
	AgentScopedCredentialKeys = map[string]map[string]struct{}{
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
	SharedCredentialKeys = map[string]struct{}{
		"github_hosts":  {},
		"github_config": {},
	}
	AllowedRootPolicyKeys = map[string]struct{}{
		"version":     {},
		"includes":    {},
		"documents":   {},
		"ssh":         {},
		"copies":      {},
		"credentials": {},
	}
	managedRootMarker = ".workcell-managed-root"
)

var systemSymlinkAllowlist = map[string]struct{}{}

func init() {
	if runtime.GOOS == "darwin" {
		systemSymlinkAllowlist[filepath.Clean("/var")] = struct{}{}
		systemSymlinkAllowlist[filepath.Clean("/tmp")] = struct{}{}
	}
}

type PolicySource struct {
	Path   string
	SHA256 string
}

func die(message string) error {
	return fmt.Errorf("%s", message)
}

func expandHostPath(raw string, base string) (string, error) {
	expanded, err := expandUserPath(raw)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(expanded) {
		expanded = filepath.Join(base, expanded)
	}
	return filepath.Abs(expanded)
}

func expandUserPath(raw string) (string, error) {
	switch {
	case raw == "":
		return "", fmt.Errorf("empty path")
	case raw == "~":
		return os.UserHomeDir()
	case strings.HasPrefix(raw, "~/"):
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, raw[2:]), nil
	case strings.HasPrefix(raw, "~"):
		slash := strings.IndexByte(raw, '/')
		userName := raw[1:]
		remainder := ""
		if slash >= 0 {
			userName = raw[1:slash]
			remainder = raw[slash+1:]
		}
		if userName == "" {
			return os.UserHomeDir()
		}
		usr, err := user.Lookup(userName)
		if err != nil {
			return "", err
		}
		if remainder == "" {
			return usr.HomeDir, nil
		}
		return filepath.Join(usr.HomeDir, remainder), nil
	default:
		return raw, nil
	}
}

func requirePathWithin(root string, candidate string, label string) error {
	resolvedRoot, err := resolvePathLikePython(root)
	if err != nil {
		return err
	}
	resolvedCandidate, err := resolvePathLikePython(candidate)
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
	sort.Strings(unknown)
	if len(unknown) > 0 {
		return die(fmt.Sprintf("%s contains unsupported keys: %s", label, strings.Join(unknown, ", ")))
	}
	return nil
}

func selectedFor(values any, current string, label string, allowedValues map[string]struct{}) (bool, error) {
	if values == nil {
		return true, nil
	}
	rawValues, ok := values.([]any)
	if !ok || len(rawValues) == 0 {
		return false, die(fmt.Sprintf("%s must be a non-empty array when specified", label))
	}
	for _, value := range rawValues {
		s, ok := value.(string)
		if !ok {
			return false, die(fmt.Sprintf("%s values must be strings", label))
		}
		if _, ok := allowedValues[s]; !ok {
			return false, die(fmt.Sprintf("%s contains unsupported value: %s", label, s))
		}
	}
	for _, value := range rawValues {
		if value.(string) == current {
			return true, nil
		}
	}
	return false, nil
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

func parseValue(raw string, policyPath string, lineno int) (any, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return nil, die(fmt.Sprintf("%s:%d: expected a value", policyPath, lineno))
	}
	switch value {
	case "true":
		return true, nil
	case "false":
		return false, nil
	}
	if strings.HasPrefix(value, "\"") && strings.HasSuffix(value, "\"") {
		parsed, err := strconv.Unquote(value)
		if err != nil {
			return nil, err
		}
		return parsed, nil
	}
	if strings.HasPrefix(value, "'") && strings.HasSuffix(value, "'") {
		parsed, err := parseSingleQuoted(value)
		if err != nil {
			return nil, err
		}
		return parsed, nil
	}
	if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
		parsed, err := parseStringArray(value, policyPath, lineno)
		if err != nil {
			return nil, err
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
	return nil, die(fmt.Sprintf("%s:%d: unsupported TOML value; use quoted strings, booleans, integers, or arrays of strings", policyPath, lineno))
}

func isDigits(value string) bool {
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

func parseSingleQuoted(value string) (string, error) {
	if len(value) < 2 || value[0] != '\'' || value[len(value)-1] != '\'' {
		return "", fmt.Errorf("invalid quoted string: %s", value)
	}
	inner := value[1 : len(value)-1]
	replacer := strings.NewReplacer(`\\`, `\`, `\'`, `'`, `\n`, "\n", `\t`, "\t", `\r`, "\r")
	return replacer.Replace(inner), nil
}

func parseStringArray(value string, policyPath string, lineno int) ([]any, error) {
	inner := strings.TrimSpace(value[1 : len(value)-1])
	if inner == "" {
		return []any{}, nil
	}
	items := make([]string, 0)
	var current strings.Builder
	quoteChar := byte(0)
	escaped := false
	for i := 0; i < len(inner); i++ {
		ch := inner[i]
		if quoteChar != 0 {
			current.WriteByte(ch)
			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' && quoteChar == '"' {
				escaped = true
				continue
			}
			if ch == quoteChar {
				quoteChar = 0
			}
			continue
		}
		if ch == '\'' || ch == '"' {
			quoteChar = ch
			current.WriteByte(ch)
			continue
		}
		if ch == ',' {
			token := strings.TrimSpace(current.String())
			current.Reset()
			if token == "" {
				continue
			}
			parsed, err := parseValue(token, policyPath, lineno)
			if err != nil {
				return nil, err
			}
			s, ok := parsed.(string)
			if !ok {
				return nil, die(fmt.Sprintf("%s:%d: only arrays of strings are supported", policyPath, lineno))
			}
			items = append(items, s)
			continue
		}
		current.WriteByte(ch)
	}
	if quoteChar != 0 {
		return nil, die(fmt.Sprintf("%s:%d: only arrays of strings are supported", policyPath, lineno))
	}
	token := strings.TrimSpace(current.String())
	if token != "" {
		parsed, err := parseValue(token, policyPath, lineno)
		if err != nil {
			return nil, err
		}
		s, ok := parsed.(string)
		if !ok {
			return nil, die(fmt.Sprintf("%s:%d: only arrays of strings are supported", policyPath, lineno))
		}
		items = append(items, s)
	}
	for _, item := range items {
		if item == "" {
			return nil, die(fmt.Sprintf("%s:%d: only arrays of strings are supported", policyPath, lineno))
		}
	}
	result := make([]any, 0, len(items))
	for _, item := range items {
		result = append(result, item)
	}
	return result, nil
}

func parseTOMLSubset(content string, policyPath string) (map[string]any, error) {
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
				return nil, die(fmt.Sprintf("%s:%d: unsupported array-of-table [%s]", policyPath, lineno+1, tableName))
			}
			rawCopies, _ := root["copies"].([]any)
			entry := map[string]any{}
			rawCopies = append(rawCopies, entry)
			root["copies"] = rawCopies
			current = entry
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			tableName := strings.TrimSpace(line[1 : len(line)-1])
			if _, seen := seenTables[tableName]; seen {
				return nil, die(fmt.Sprintf("%s:%d: duplicate table [%s]", policyPath, lineno+1, tableName))
			}
			seenTables[tableName] = struct{}{}
			if strings.HasPrefix(tableName, "credentials.") {
				credentialKey := strings.SplitN(tableName, ".", 2)[1]
				if _, ok := CredentialKeys[credentialKey]; !ok {
					return nil, die(fmt.Sprintf("%s:%d: unsupported credentials table [%s]", policyPath, lineno+1, tableName))
				}
				rawCredentials, _ := root["credentials"].(map[string]any)
				if rawCredentials == nil {
					rawCredentials = map[string]any{}
					root["credentials"] = rawCredentials
				}
				entry, _ := rawCredentials[credentialKey].(map[string]any)
				if entry == nil {
					entry = map[string]any{}
					rawCredentials[credentialKey] = entry
				}
				current = entry
				continue
			}
			if tableName != "documents" && tableName != "ssh" && tableName != "credentials" {
				return nil, die(fmt.Sprintf("%s:%d: unsupported table [%s]", policyPath, lineno+1, tableName))
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
			return nil, die(fmt.Sprintf("%s:%d: expected key = value", policyPath, lineno+1))
		}
		parts := strings.SplitN(line, "=", 2)
		key := strings.TrimSpace(parts[0])
		value := parts[1]
		if key == "" {
			return nil, die(fmt.Sprintf("%s:%d: empty key", policyPath, lineno+1))
		}
		if strings.Contains(key, ".") {
			return nil, die(fmt.Sprintf("%s:%d: dotted TOML keys are not supported; use explicit [table] headers instead", policyPath, lineno+1))
		}
		if _, exists := current[key]; exists {
			return nil, die(fmt.Sprintf("%s:%d: duplicate key: %s", policyPath, lineno+1, key))
		}
		parsed, err := parseValue(value, policyPath, lineno+1)
		if err != nil {
			return nil, err
		}
		current[key] = parsed
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
		b.WriteString(pythonReprString(source.SHA256))
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
		SHA256: sourceSHA,
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
		sort.Strings(extras)
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
			sort.Strings(extras)
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

func resolvePathLikePython(raw string) (string, error) {
	expanded, err := expandUserPath(raw)
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
	leftResolved, errLeft := resolvePathLikePython(left)
	rightResolved, errRight := resolvePathLikePython(right)
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
	sort.Strings(keys)
	return keys
}
