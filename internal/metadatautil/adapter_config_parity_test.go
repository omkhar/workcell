// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/BurntSushi/toml"
)

// sharedCodexSections are the sections and keys that the repo-local Codex
// base config and the managed baseline must keep byte-equal so the local
// and managed deployments do not drift. Repo-local-only keys
// (project_doc_fallback_filenames, project_root_markers) stay outside this
// list on purpose.
var sharedCodexSections = []string{
	"analytics",
	"history",
	"web_search",
	"developer_instructions",
	"agents",
	"shell_environment_policy",
	"sandbox_workspace_write",
	"features",
}

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

	for _, key := range sharedCodexSections {
		if !reflect.DeepEqual(base[key], managed[key]) {
			t.Errorf("adapters/codex/.codex/config.toml and managed_config.toml disagree on %q:\nbase:    %#v\nmanaged: %#v", key, base[key], managed[key])
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

func loadJSONDocument(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	document := map[string]any{}
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return document
}

func TestClaudeSettingsParity(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	managed := loadJSONDocument(t, filepath.Join(root, "adapters", "claude", "managed-settings.json"))
	session := loadJSONDocument(t, filepath.Join(root, "adapters", "claude", ".claude", "settings.json"))

	managedPermissions, ok := managed["permissions"].(map[string]any)
	if !ok {
		t.Fatal("managed-settings.json lacks a permissions object")
	}
	sessionPermissions, ok := session["permissions"].(map[string]any)
	if !ok {
		t.Fatal(".claude/settings.json lacks a permissions object")
	}
	if !reflect.DeepEqual(managedPermissions["deny"], sessionPermissions["deny"]) {
		t.Error("managed-settings.json and .claude/settings.json deny lists differ; keep the two baselines identical")
	}
	if !reflect.DeepEqual(managed["hooks"], session["hooks"]) {
		t.Error("managed-settings.json and .claude/settings.json hooks differ; keep the two baselines identical")
	}
	if mode := managedPermissions["disableBypassPermissionsMode"]; mode != "disable" {
		t.Errorf("managed-settings.json permissions.disableBypassPermissionsMode = %v, want \"disable\"", mode)
	}
	if _, exists := managed["disableBypassPermissionsMode"]; exists {
		t.Error("managed-settings.json declares disableBypassPermissionsMode at the top level; the documented key lives under permissions")
	}
}
