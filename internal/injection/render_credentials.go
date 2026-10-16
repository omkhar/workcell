// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package injection

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/omkhar/workcell/internal/adapters"
	"github.com/omkhar/workcell/internal/authresolve"
	"github.com/omkhar/workcell/internal/credbroker"
	"github.com/omkhar/workcell/internal/jsonstrict"
	"github.com/omkhar/workcell/internal/rootio"
	"github.com/omkhar/workcell/internal/tomlsubset"
)

// EgressCACertName is the bundle file that holds the session CA of a session
// that brokers credentials.
const EgressCACertName = "egress-ca.pem"

// EgressCABundleName is the bundle file that holds the system roots and the
// session CA, for a client whose CA file replaces its system roots.
const EgressCABundleName = "egress-ca-bundle.pem"

// credentialBroker is what the resolver metadata says about the credential
// broker: on reports a session that brokers credentials, and brokered names
// each key that the broker serves.
type credentialBroker struct {
	on       bool
	brokered map[string]bool
}

// loadCredentialBroker reads the broker state from the resolver metadata.
func loadCredentialBroker(path string) (credentialBroker, error) {
	data, err := rootio.ReadFileNoFollow(path, "resolver metadata", rootio.MaxManifestBytes)
	if err != nil {
		return credentialBroker{}, err
	}
	var metadata map[string]any
	if err := json.Unmarshal(data, &metadata); err != nil {
		return credentialBroker{}, fmt.Errorf("resolver metadata %s: %w", path, err)
	}
	broker, err := credentialBrokerFromMetadata(metadata)
	if err != nil {
		return broker, fmt.Errorf("resolver metadata %s: %w", path, err)
	}
	return broker, nil
}

func credentialBrokerFromMetadata(metadata map[string]any) (credentialBroker, error) {
	broker := credentialBroker{brokered: map[string]bool{}}
	if raw, ok := metadata["credential_broker"]; ok {
		if broker.on, ok = raw.(bool); !ok {
			return broker, errors.New("credential_broker must be a boolean")
		}
	}
	resolvers, ok := metadata["credential_resolvers"].(map[string]any)
	if !ok && metadata["credential_resolvers"] != nil {
		return broker, errors.New("credential_resolvers must be an object")
	}
	for key, name := range resolvers {
		broker.brokered[key] = name == authresolve.BrokerResolver
	}
	return broker, nil
}

// check refuses an API key that would reach the agent in a session that
// brokers credentials: there the agent gets only a placeholder for each key
// the broker can serve. Gemini OAuth, Vertex settings and Codex OAuth tokens
// stay direct.
func (b credentialBroker) check(rendered map[string]map[string]string) error {
	if !b.on {
		return nil
	}
	for _, key := range sortedKeys(rendered) {
		if b.brokered[key] || !credbroker.Brokers(key) {
			continue
		}
		if key == "gemini_env" {
			metadata, err := validateGeminiEnvFile(Path(rendered[key]["source"]))
			if err != nil {
				return err
			}
			if metadata["selected_auth_type"] != "gemini-api-key" {
				continue
			}
		}
		if key == "codex_auth" {
			apiKey, err := codexAuthHoldsAPIKey(Path(rendered[key]["source"]))
			if err != nil {
				return err
			}
			if !apiKey {
				continue
			}
		}
		return fmt.Errorf("--egress-proxy brokers credentials.%s: store the key in the macOS login Keychain item %q and set resolver = %q in place of source, or launch without --egress-proxy", key, credbroker.KeychainItem(key), authresolve.BrokerResolver)
	}
	return nil
}

// maxCodexAuthJSONDepth bounds the nesting of a Codex auth.json, which nests
// two levels.
const maxCodexAuthJSONDepth = 16

// codexAuthHoldsAPIKey reports whether a Codex auth.json sets OPENAI_API_KEY,
// the API key of API-key mode; ChatGPT OAuth leaves it null. A repeated key
// is refused: encoding/json reads only the last copy, and the file with every
// copy would reach the agent.
func codexAuthHoldsAPIKey(source Path) (bool, error) {
	data, err := readInjectionPath(source, nil)
	if err != nil {
		return false, err
	}
	if err := jsonstrict.RejectDuplicateKeys(data, maxCodexAuthJSONDepth); err != nil {
		return false, fmt.Errorf("credentials.codex_auth %s: %w", source, err)
	}
	var auth map[string]any
	if err := json.Unmarshal(data, &auth); err != nil {
		return false, fmt.Errorf("credentials.codex_auth must contain a JSON object: %s", source)
	}
	value, ok := auth["OPENAI_API_KEY"]
	return ok && value != nil, nil
}

// brokersAny reports whether the broker serves a credential. The resolver
// records a resolver only for a credential it selected, so the renderer
// renders every key that the metadata names.
func (b credentialBroker) brokersAny() bool {
	for _, brokered := range b.brokered {
		if brokered {
			return true
		}
	}
	return false
}

// allowedCredentialEntryKeys is the parser-accepted key set for a table-form
// credential entry (`[credentials.<name>]`), shared as a single source of truth
// between renderCredentials and the schema-doc drift test.
var allowedCredentialEntryKeys = mapKeysSet([]string{"source", "providers", "modes"})

func renderCredentials(policy map[string]any, policyDir Path, agent, mode string) (map[string]map[string]string, error) {
	return renderCredentialsWithBudget(policy, policyDir, agent, mode, newInjectionTreeBudget())
}

func renderCredentialsWithBudget(
	policy map[string]any,
	policyDir Path,
	agent, mode string,
	budget *injectionTreeBudget,
) (map[string]map[string]string, error) {
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
		if err := accountInjectionSourceSize(source, budget); err != nil {
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
	data, err := readInjectionPath(source, nil)
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
	data, err := readInjectionPath(source, nil)
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
