// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/BurntSushi/toml"
)

func loadTOMLDocument(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	document := map[string]any{}
	if err := toml.Unmarshal(data, &document); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return document
}

func TestCodexAdapterConfigParity(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	base := loadTOMLDocument(t, filepath.Join(root, "adapters", "codex", ".codex", "config.toml"))
	managed := loadTOMLDocument(t, filepath.Join(root, "adapters", "codex", "managed_config.toml"))

	// Every key the managed baseline declares must match the repo-local base
	// config, so the local and managed deployments do not drift.  The base
	// config may carry repo-local-only keys (project_doc_fallback_filenames,
	// project_root_markers) that the managed layer does not.
	for key, value := range managed {
		if !reflect.DeepEqual(base[key], value) {
			t.Errorf("adapters/codex/.codex/config.toml and managed_config.toml disagree on %q:\nbase:    %#v\nmanaged: %#v", key, base[key], value)
		}
	}

	// The config layer has no rules schema; prefix rules are enforced by
	// requirements.toml and .codex/rules/default.rules only.
	if _, exists := managed["rules"]; exists {
		t.Error("managed_config.toml must not declare a rules table; requirements.toml owns enforcement")
	}
	if _, exists := base["rules"]; exists {
		t.Error(".codex/config.toml must not declare a rules table; requirements.toml owns enforcement")
	}

	for name, document := range map[string]map[string]any{"base": base, "managed": managed} {
		policy, ok := document["shell_environment_policy"].(map[string]any)
		if !ok {
			t.Fatalf("%s config lacks a shell_environment_policy table", name)
		}
		for _, legacy := range []string{"exclude", "include_only"} {
			if _, exists := policy[legacy]; exists {
				t.Errorf("%s shell_environment_policy uses the legacy %q key; use the canonical filters table", name, legacy)
			}
		}
		filters, ok := policy["filters"].(map[string]any)
		if !ok || len(filters) == 0 {
			t.Fatalf("%s shell_environment_policy.filters must be a non-empty table", name)
		}
		for pattern, action := range filters {
			if action != "exclude" {
				t.Errorf("%s shell_environment_policy.filters[%q] = %v, want \"exclude\"", name, pattern, action)
			}
		}
	}
}
