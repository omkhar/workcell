// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package authresolve

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/omkhar/workcell/internal/injectionpolicy"
)

func loadPolicyBundle(policyPath string) (map[string]any, []PolicySource, error) {
	return loadPolicyBundleWithReader(policyPath, injectionpolicy.NewBundleReader())
}

func loadPolicyBundleWithReader(policyPath string, reader *injectionpolicy.BundleReader) (map[string]any, []PolicySource, error) {
	resolvedPolicyPath := filepath.Clean(policyPath)
	return injectionpolicy.LoadBundle(resolvedPolicyPath, filepath.Dir(resolvedPolicyPath), reader, injectionpolicy.LoadOptions{
		Parse:           parseTOMLSubset,
		ValidateInclude: validatePolicyInclude,
		ValidateMerged:  validatePolicyCredentials,
		RebasePath:      rebaseFragmentPath,
		StrictVersion:   true,
	})
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

func rebasePolicyFragment(policy map[string]any, fragmentDir string) map[string]any {
	return injectionpolicy.RebasePolicyFragment(policy, fragmentDir, rebaseFragmentPath)
}

func rebaseFragmentPath(raw any, fragmentDir string) any {
	str, ok := raw.(string)
	if !ok || str == "" {
		return raw
	}
	return expandHostPath(str, fragmentDir)
}

// parseTOMLSubset parses an injection-policy TOML file via the shared
// injectionpolicy.ParsePolicyTOML seam and layers on the authresolve
// document and credential whitelists, preserving this package's error
// ordering.
func parseTOMLSubset(content, policyPath string) (map[string]any, error) {
	root, err := injectionpolicy.ParsePolicyTOML(content, policyPath, allCredentialKeys)
	if err != nil {
		return nil, err
	}
	if err := validatePolicyDocuments(root); err != nil {
		return nil, err
	}
	if err := validatePolicyCredentials(root); err != nil {
		return nil, err
	}
	return root, nil
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

func validatePolicyDocuments(policy map[string]any) error {
	raw, ok := policy["documents"]
	if !ok {
		return nil
	}
	documents, ok := raw.(map[string]any)
	if !ok {
		return errors.New("documents must be a TOML table")
	}
	return validateAllowedKeys(documents, documentKeys, "documents")
}

func validatePolicyCredentials(policy map[string]any) error {
	raw, ok := policy["credentials"]
	if !ok || raw == nil {
		return nil
	}
	credentials, ok := raw.(map[string]any)
	if !ok {
		return errors.New("credentials must be a TOML table")
	}
	return validateAllowedKeys(credentials, allCredentialKeys, "credentials")
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
	return injectionpolicy.LogicalPolicyPath(policyPath, entrypointRoot)
}
