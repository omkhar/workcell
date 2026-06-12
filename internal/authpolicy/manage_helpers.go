// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package authpolicy

import (
	"fmt"
	"slices"
	"strings"

	"github.com/omkhar/workcell/internal/authresolve"
)

func allowedCredentialsForAgent(agent string) map[string]struct{} {
	allowed := map[string]struct{}{}
	for key := range SharedCredentialKeys {
		allowed[key] = struct{}{}
	}
	for key := range AgentScopedCredentialKeys[agent] {
		allowed[key] = struct{}{}
	}
	return allowed
}

func credentialAllowedForAgent(agent string, credential string) bool {
	_, ok := allowedCredentialsForAgent(agent)[credential]
	return ok
}

func validateSelectorValues(values any, label string, allowedValues map[string]struct{}) error {
	_, err := selectorStrings(values, label, allowedValues)
	return err
}

func validateStatusCredentialEntry(key string, raw any) error {
	rawMap, ok := raw.(map[string]any)
	if !ok {
		if _, shared := SharedCredentialKeys[key]; shared {
			return die(fmt.Sprintf("credentials.%s.providers is required so shared GitHub credentials stay least-privilege", key))
		}
		return nil
	}
	if err := validateAllowedKeys(rawMap, entryAllowedKeys, "credentials."+key); err != nil {
		return err
	}
	sourceRaw := rawMap["source"]
	resolver, _ := rawMap["resolver"].(string)
	providers := rawMap["providers"]
	materialization, hasMaterialization := rawMap["materialization"]
	if _, ok := SharedCredentialKeys[key]; ok && providers == nil {
		return die(fmt.Sprintf("credentials.%s.providers is required so shared GitHub credentials stay least-privilege", key))
	}
	if sourceRaw != nil && resolver != "" {
		return die(fmt.Sprintf("credentials.%s must not declare both source and resolver", key))
	}
	if resolver == "" {
		if hasMaterialization {
			return die(fmt.Sprintf("credentials.%s.materialization is only valid for resolver-backed auth", key))
		}
		if sourceRaw == nil {
			return die(fmt.Sprintf("credentials.%s must declare source or resolver", key))
		}
		return nil
	}
	if !authresolve.ResolverSupported(key, resolver) {
		return die(fmt.Sprintf("credentials.%s.resolver is unsupported: %s", key, resolver))
	}
	if hasMaterialization {
		if mat, ok := materialization.(string); !ok || mat != "ephemeral" {
			return die(fmt.Sprintf("credentials.%s.materialization must stay ephemeral for resolver-backed auth", key))
		}
	}
	return nil
}

func validateStatusCredentialSource(key string, raw any, policyBase string) error {
	sourceRaw := raw
	if rawMap, ok := raw.(map[string]any); ok {
		sourceRaw = rawMap["source"]
	}
	if sourceRaw == nil {
		return nil
	}
	source, err := validateSourcePath(sourceRaw, "credentials."+key, policyBase)
	if err != nil {
		return err
	}
	_, err = requireSecretFile(source, "credentials."+key)
	return err
}

func renderMap(value map[string]string) string {
	if len(value) == 0 {
		return "none"
	}
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+":"+value[key])
	}
	return strings.Join(parts, ",")
}

func parseRenderedMap(raw string) map[string]string {
	parsed := map[string]string{}
	if raw == "" || raw == "none" {
		return parsed
	}
	for _, item := range strings.Split(raw, ",") {
		if item == "" {
			continue
		}
		key, value, ok := strings.Cut(item, ":")
		if !ok || key == "" {
			continue
		}
		parsed[key] = value
	}
	return parsed
}

func renderModes(keys []string) string {
	if len(keys) == 0 {
		return "none"
	}
	return strings.Join(keys, ",")
}

func selectorStrings(values any, label string, allowedValues map[string]struct{}) ([]string, error) {
	if values == nil {
		return nil, nil
	}
	rawValues, ok := values.([]any)
	if !ok || len(rawValues) == 0 {
		return nil, die(fmt.Sprintf("%s must be a non-empty array when specified", label))
	}
	parsed := make([]string, 0, len(rawValues))
	for _, value := range rawValues {
		s, ok := value.(string)
		if !ok {
			return nil, die(fmt.Sprintf("%s values must be strings", label))
		}
		if _, ok := allowedValues[s]; !ok {
			return nil, die(fmt.Sprintf("%s contains unsupported value: %s", label, s))
		}
		parsed = append(parsed, s)
	}
	return parsed, nil
}

func credentialInputKind(raw any) string {
	if rawMap, ok := raw.(map[string]any); ok {
		if rawMap["resolver"] != nil {
			return "resolver"
		}
	}
	return "source"
}

func selectedCredentials(policy map[string]any, agent string, mode string) (map[string]any, error) {
	credentials, _ := policy["credentials"].(map[string]any)
	if credentials == nil {
		return map[string]any{}, nil
	}
	if agent == "" {
		selected := map[string]any{}
		for key, raw := range credentials {
			if err := validateStatusCredentialEntry(key, raw); err != nil {
				return nil, err
			}
			if rawMap, ok := raw.(map[string]any); ok {
				if err := validateSelectorValues(rawMap["providers"], "credentials."+key+".providers", SupportedAgents); err != nil {
					return nil, err
				}
				ok, err := selectedFor(rawMap["modes"], mode, "credentials."+key+".modes", SupportedModes)
				if err != nil {
					return nil, err
				}
				if !ok {
					continue
				}
			}
			selected[key] = raw
		}
		return selected, nil
	}
	relevant := map[string]struct{}{}
	for key := range SharedCredentialKeys {
		relevant[key] = struct{}{}
	}
	for key := range AgentScopedCredentialKeys[agent] {
		relevant[key] = struct{}{}
	}
	selected := map[string]any{}
	keys := make([]string, 0, len(relevant))
	for key := range relevant {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		raw, ok := credentials[key]
		if !ok {
			continue
		}
		if err := validateStatusCredentialEntry(key, raw); err != nil {
			return nil, err
		}
		if rawMap, ok := raw.(map[string]any); ok {
			ok, err := selectedFor(rawMap["providers"], agent, "credentials."+key+".providers", SupportedAgents)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
			ok, err = selectedFor(rawMap["modes"], mode, "credentials."+key+".modes", SupportedModes)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
		}
		selected[key] = raw
	}
	return selected, nil
}
