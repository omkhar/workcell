// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package injection

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/omkhar/workcell/internal/adapters"
	"github.com/omkhar/workcell/internal/tomlsubset"
)

// allowedCredentialEntryKeys is the parser-accepted key set for a table-form
// credential entry (`[credentials.<name>]`), shared as a single source of truth
// between renderCredentials and the schema-doc drift test.
var allowedCredentialEntryKeys = mapKeysSet([]string{"source", "providers", "modes"})

func renderCredentials(policy map[string]any, policyDir Path, agent, mode string) (map[string]map[string]string, error) {
	raw := policy["credentials"]
	if raw == nil {
		return map[string]map[string]string{}, nil
	}
	credentials, ok := raw.(map[string]any)
	if !ok {
		return nil, errors.New("credentials must be a TOML table")
	}
	if err := validateAllowedKeys(credentials, mapKeysSet(sortedKeys(credentialContainerPaths)), "credentials"); err != nil {
		return nil, err
	}

	relevant := map[string]struct{}{}
	if adapters.SharedCredentialsApplyToAgent(agent) {
		for key := range sharedCredentialKeys {
			relevant[key] = struct{}{}
		}
	}
	if scoped, ok := agentScopedCredentialKeys[agent]; ok {
		for key := range scoped {
			relevant[key] = struct{}{}
		}
	}

	rendered := map[string]map[string]string{}
	for _, key := range sortedKeys(relevant) {
		rawValue, ok := credentials[key]
		if !ok || rawValue == nil {
			continue
		}

		var providers any
		var modes any
		sourceRaw := rawValue
		entry, isTable := rawValue.(map[string]any)
		if isTable {
			if err := validateAllowedKeys(entry, allowedCredentialEntryKeys, "credentials."+key); err != nil {
				return nil, err
			}
			sourceRaw = entry["source"]
			providers = entry["providers"]
			modes = entry["modes"]
		} else if _, ok := rawValue.(string); !ok {
			return nil, fmt.Errorf("credentials.%s must be a string path or a table", key)
		}

		if _, shared := sharedCredentialKeys[key]; shared {
			if !isTable || providers == nil {
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

func validateGeminiEnvFile(source Path) (map[string]any, error) {
	values, err := parseSimpleEnvFile(source)
	if err != nil {
		return nil, err
	}
	for key, value := range values {
		if _, ok := geminiSupportedEnvKeys[key]; !ok {
			return nil, fmt.Errorf("gemini auth env file %s contains an unsupported key", source)
		}
		if key != "GOOGLE_GENAI_USE_GCA" && key != "GOOGLE_GENAI_USE_VERTEXAI" && strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf("gemini auth env file %s sets %s but leaves it empty", source, key)
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
		return nil, fmt.Errorf("gemini auth env file %s enables both GOOGLE_GENAI_USE_GCA and GOOGLE_GENAI_USE_VERTEXAI; choose exactly one auth selector", source)
	}
	if hasLocation && !hasProject {
		return nil, fmt.Errorf("gemini auth env file %s sets a Google Cloud location without a project", source)
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
				"extra_endpoints":    sortedKeys(endpoints),
			}, nil
		}
		return nil, fmt.Errorf("gemini auth env file %s enables GOOGLE_GENAI_USE_VERTEXAI=true without either GOOGLE_API_KEY or both GOOGLE_CLOUD_PROJECT and GOOGLE_CLOUD_LOCATION", source)
	}
	if googleAPIKey != "" {
		return nil, fmt.Errorf("gemini auth env file %s sets GOOGLE_API_KEY without GOOGLE_GENAI_USE_VERTEXAI=true", source)
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
	return nil, fmt.Errorf("gemini auth env file %s does not configure a supported Gemini auth mode; use GEMINI_API_KEY, GOOGLE_GENAI_USE_GCA=true, or GOOGLE_GENAI_USE_VERTEXAI=true with GOOGLE_API_KEY or both GOOGLE_CLOUD_PROJECT and GOOGLE_CLOUD_LOCATION", source)
}

func parseSimpleEnvFile(source Path) (map[string]string, error) {
	values := map[string]string{}
	data, err := os.ReadFile(source.String())
	if err != nil {
		return nil, err
	}
	for lineNumber, rawLine := range strings.Split(string(data), "\n") {
		lineNumber++
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "export ") {
			line = strings.TrimSpace(line[len("export "):])
		}
		idx := strings.Index(line, "=")
		if idx < 0 {
			return nil, fmt.Errorf("malformed Gemini auth env file %s at line %d: use KEY=value assignments", source, lineNumber)
		}
		key := strings.TrimSpace(line[:idx])
		if !validEnvName(key) {
			return nil, fmt.Errorf("malformed Gemini auth env file %s at line %d: use a valid environment variable name", source, lineNumber)
		}
		value := tomlsubset.StripComment(line[idx+1:])
		if _, exists := values[key]; exists {
			return nil, fmt.Errorf("gemini auth env file %s repeats a key at line %d", source, lineNumber)
		}
		if value != "" && (value[0] == '\'' || value[0] == '"') {
			if len(value) < 2 || value[len(value)-1] != value[0] {
				return nil, fmt.Errorf("malformed Gemini auth env file %s at line %d: quoted value is not terminated", source, lineNumber)
			}
			if value[0] == '"' {
				parsed, err := strconv.Unquote(value)
				if err != nil {
					return nil, fmt.Errorf("malformed Gemini auth env file %s at line %d: double-quoted value is invalid", source, lineNumber)
				}
				value = parsed
			} else {
				value = value[1 : len(value)-1]
			}
		} else if value != "" && (strings.HasSuffix(value, "'") || strings.HasSuffix(value, "\"")) {
			return nil, fmt.Errorf("malformed Gemini auth env file %s at line %d: value has an unmatched trailing quote", source, lineNumber)
		}
		values[key] = value
	}
	return values, nil
}

func validEnvName(value string) bool {
	if value == "" {
		return false
	}
	for index, char := range value {
		if char == '_' || char >= 'A' && char <= 'Z' || char >= 'a' && char <= 'z' || index > 0 && char >= '0' && char <= '9' {
			continue
		}
		return false
	}
	return true
}

func parseEnvBooleanValue(values map[string]string, source Path, key string) (bool, error) {
	raw, ok := values[key]
	if !ok {
		return false, nil
	}
	normalized := strings.ToLower(strings.TrimSpace(raw))
	if normalized != "true" && normalized != "false" {
		return false, fmt.Errorf("invalid boolean in Gemini auth env file %s: %s must be true or false", source, key)
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
	return sortedKeys(endpoints)
}
