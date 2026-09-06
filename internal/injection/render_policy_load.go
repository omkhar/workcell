// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package injection

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/omkhar/workcell/internal/injectionpolicy"
	"github.com/omkhar/workcell/internal/pathutil"
	"github.com/omkhar/workcell/internal/rootio"
)

func loadPolicyBundle(policyPath Path) (map[string]any, []PolicySource, error) {
	resolvedPolicyPath, err := resolveAbsPath(policyPath.String())
	if err != nil {
		return nil, nil, err
	}
	resolvedPolicy := Path(resolvedPolicyPath)
	return loadPolicyBundleWithReader(resolvedPolicy, injectionpolicy.NewBundleReader())
}

func loadPolicyBundleWithReader(policyPath Path, reader *injectionpolicy.BundleReader) (map[string]any, []PolicySource, error) {
	return injectionpolicy.LoadBundle(policyPath.String(), policyPath.Parent().String(), reader, injectionpolicy.LoadOptions{
		Parse: func(content, path string) (map[string]any, error) {
			return parseTOMLSubset(content, Path(path))
		},
		ValidateInclude: func(raw any, label, base, entrypointRoot string) (string, error) {
			source, err := validatePolicyInclude(raw, label, Path(base), Path(entrypointRoot))
			if err != nil {
				return "", err
			}
			return source.String(), nil
		},
		RebasePath:    rebaseFragmentPathString,
		StrictVersion: true,
	})
}

func loadPolicyMetadataOverride(rawPath string) (string, []PolicySource, error) {
	resolved, err := pathutil.ExpandUserPathStrictRequireNonEmpty(rawPath)
	if err != nil {
		return "", nil, err
	}
	data, err := rootio.ReadFileNoFollow(resolved, "policy metadata override", rootio.MaxManifestBytes)
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
// injectionpolicy.ParsePolicyTOML seam. The UTF-8 guard and the
// credential-table whitelist (credentialContainerPaths, vs authpolicy's
// CredentialKeys) are the injection-specific pieces.
func parseTOMLSubset(content string, policyPath Path) (map[string]any, error) {
	policy := policyPath.String()
	if !utf8.ValidString(content) {
		return nil, fmt.Errorf("%s must contain valid UTF-8", policy)
	}
	return injectionpolicy.ParsePolicyTOML(content, policy, credentialTableKeys)
}

// credentialTableKeys is the [credentials.<name>] whitelist view of
// credentialContainerPaths.
var credentialTableKeys = func() map[string]struct{} {
	keys := make(map[string]struct{}, len(credentialContainerPaths))
	for key := range credentialContainerPaths {
		keys[key] = struct{}{}
	}
	return keys
}()

func logicalPolicyPath(policyPath, entrypointRoot Path) string {
	return injectionpolicy.LogicalPolicyPath(policyPath.String(), entrypointRoot.String())
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

func rebaseFragmentPathString(raw any, fragmentDir string) any {
	return rebaseFragmentPath(raw, Path(fragmentDir))
}

func rebasePolicyFragment(policy map[string]any, fragmentDir Path) map[string]any {
	return injectionpolicy.RebasePolicyFragment(policy, fragmentDir.String(), rebaseFragmentPathString)
}

func validatePolicyInclude(raw any, label string, base, entrypointRoot Path) (Path, error) {
	rawPath, ok := raw.(string)
	if !ok || rawPath == "" {
		return Path(""), fmt.Errorf("%s must be a non-empty string path", label)
	}
	if err := validateManifestPathField(rawPath, label); err != nil {
		return Path(""), err
	}
	source, err := expandHostPath(rawPath, base)
	if err != nil {
		return Path(""), err
	}
	if err := validateManifestPathField(source.String(), label); err != nil {
		return Path(""), err
	}
	if err := requirePolicyIncludePathWithin(entrypointRoot, source, label); err != nil {
		return Path(""), err
	}
	return source, nil
}

func requirePolicyIncludePathWithin(root, candidate Path, label string) error {
	relative, err := filepath.Rel(root.String(), candidate.String())
	if err != nil {
		return err
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return fmt.Errorf("%s must stay within %s: %s", label, root, candidate)
	}
	return nil
}
