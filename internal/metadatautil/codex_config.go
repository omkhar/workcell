// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// This file ports the Codex TOML invariants out of
// scripts/verify-invariants.sh (the former toml_section_assignments /
// toml_section_names awk parser and its require_toml_* helpers).
//
// Value comparisons operate on DECODED TOML values rather than the raw
// assignment text the bash compared. Decoded comparison accepts benign
// reformatting the raw-text compare would have rejected (for example
// `enabled=true` versus `enabled = true`, or `'medium'` versus
// `"medium"`); it never accepts a different decoded value, so
// enforcement is not weakened. Parse errors still fail closed: an
// undecodable config rejects, matching the awk parser's exit-2 path.

// codexManagedForbiddenTopLevelKeys are the top-level keys the managed
// Codex baseline must not define: profile selection and sandbox or
// approval widening belong to the per-profile layer files, and the
// coordinator model stays operator-selected.
var codexManagedForbiddenTopLevelKeys = []string{
	"profile",
	"sandbox",
	"sandbox_mode",
	"sandbox_permissions",
	"approval_policy",
	"model",
	"model_reasoning_effort",
}

var codexFeaturesExactKeys = []string{
	"unified_exec",
	"code_mode_host",
	"plugins",
	"plugin_sharing",
	"remote_plugin",
}

// codexRequirement is one required section key/value binding, checked
// in declaration order to preserve the bash first-failure message.
type codexRequirement struct {
	section  string
	key      string
	expected any
}

var codexManagedRequirements = []codexRequirement{
	{"sandbox_workspace_write", "exclude_slash_tmp", true},
	{"sandbox_workspace_write", "exclude_tmpdir_env_var", false},
	{"sandbox_workspace_write", "network_access", false},
	{"features", "unified_exec", true},
	{"features", "code_mode_host", true},
	{"features", "plugins", false},
	{"features", "plugin_sharing", false},
	{"features", "remote_plugin", false},
	{"agents", "enabled", true},
	{"agents", "max_concurrent_threads_per_session", int64(3)},
	{"agents", "default_subagent_model", "gpt-5.6-terra"},
	{"agents", "default_subagent_reasoning_effort", "medium"},
}

var codexFeatureRequirements = []codexRequirement{
	{"features", "unified_exec", true},
	{"features", "code_mode_host", true},
	{"features", "plugins", false},
	{"features", "plugin_sharing", false},
	{"features", "remote_plugin", false},
}

// ValidateCodexManagedConfig verifies one Codex base config (repo-local
// or admin-managed) against the profile-v2 invariants: no top-level
// profile/sandbox/approval/model selection, the exact reviewed
// [sandbox_workspace_write] and [features] key sets, the reviewed
// sandbox/feature/agents values, and no inline [profiles...] table.
// Tolerated-but-unasserted tables (for example [shell_environment_policy]
// and its subtables, [[rules.prefix_rules]], analytics.*) pass through
// untouched.
func ValidateCodexManagedConfig(path string) error {
	config, err := decodeCodexTOML(path)
	if err != nil {
		return err
	}

	// Codex 0.134+ profile-v2: the base config must not select or inline a
	// profile. Profile selection is supplied by the runtime wrapper via
	// `--profile`, and the per-profile layers live in separate
	// `<name>.config.toml` files validated by ValidateCodexProfileLayer.
	// BurntSushi decodes a quoted key (`"approval_policy"`) and an escaped
	// key (`"approval\u005fpolicy"`) to the same map key as the bare
	// spelling, so quoting cannot smuggle a forbidden key past this loop.
	for _, key := range codexManagedForbiddenTopLevelKeys {
		if _, present := config[key]; present {
			return fmt.Errorf("Expected %s section [top-level] not to define %s", path, key)
		}
	}

	if err := requireCodexExactKeys(path, "sandbox_workspace_write", codexSubtable(config, "sandbox_workspace_write"), []string{
		"exclude_slash_tmp",
		"exclude_tmpdir_env_var",
		"network_access",
	}); err != nil {
		return err
	}
	if err := requireCodexExactKeys(path, "features", codexSubtable(config, "features"), codexFeaturesExactKeys); err != nil {
		return err
	}
	for _, requirement := range codexManagedRequirements {
		if err := requireCodexAssignment(path, requirement.section, codexSubtable(config, requirement.section), requirement.key, requirement.expected); err != nil {
			return err
		}
	}

	// The base config must carry no inline `[profiles...]` tables. A dotted
	// section header ([profiles.strict.x]) and its quoted-segment spelling
	// (["profiles" . "strict" . "x"]) both decode to a nested table under the
	// top-level key `profiles`, as does a dotted assignment (profiles.x = 1).
	// A quoted SINGLE-SEGMENT section name with literal dots
	// (["profiles.strict.x"]) decodes to a distinct top-level key containing
	// the literal dots, so it does not trip this guard — verified by unit test.
	if _, present := config["profiles"]; present {
		return fmt.Errorf("Expected %s not to define any [profiles...] section; profile-v2 uses separate <name>.config.toml layers", path)
	}
	return nil
}

// ValidateCodexProfileLayer verifies one Codex profile-v2 layer file
// (strict/development/build/breakglass): no nested profile selection,
// exactly the flat keys approval_policy/sandbox_mode/web_search with the
// reviewed values, and no sections at all (a [sandbox_workspace_write]
// override inside a layer could widen network access, so it is rejected).
func ValidateCodexProfileLayer(path, sandboxMode, approvalPolicy string) error {
	config, err := decodeCodexTOML(path)
	if err != nil {
		return err
	}

	if _, present := config["profile"]; present {
		return fmt.Errorf("Expected %s section [top-level] not to define profile", path)
	}

	// Split the decoded top level into flat assignments and tables: the bash
	// exact-keys check counted only top-level assignments, and its separate
	// no-[sections] check rejected section headers with a distinct message.
	flat := map[string]any{}
	var tables []string
	for key, value := range config {
		if _, isTable := value.(map[string]any); isTable {
			tables = append(tables, key)
			continue
		}
		flat[key] = value
	}

	if err := requireCodexExactKeys(path, "top-level", flat, []string{
		"approval_policy",
		"sandbox_mode",
		"web_search",
	}); err != nil {
		return err
	}
	if err := requireCodexAssignment(path, "", flat, "sandbox_mode", sandboxMode); err != nil {
		return err
	}
	if err := requireCodexAssignment(path, "", flat, "approval_policy", approvalPolicy); err != nil {
		return err
	}
	if err := requireCodexAssignment(path, "", flat, "web_search", "disabled"); err != nil {
		return err
	}
	if len(tables) > 0 {
		return fmt.Errorf("Expected %s to contain no [sections]; a profile-v2 layer is a flat key set", path)
	}
	return nil
}

// ValidateCodexAdapterLockstep verifies that adapters/codex/requirements.toml
// and the managed provider wrapper stay in lockstep with the managed Codex
// baseline: the two reviewed sandbox modes, the wrapper lines that disable the
// incompatible native sandbox, and the reviewed [features] key set and values.
func ValidateCodexAdapterLockstep(rootDir string) error {
	requirementsPath := filepath.Join(rootDir, "adapters", "codex", "requirements.toml")
	requirements, err := decodeCodexTOML(requirementsPath)
	if err != nil {
		return err
	}
	if err := requireCodexAssignment(requirementsPath, "", requirements, "allowed_sandbox_modes", []any{"workspace-write", "danger-full-access"}); err != nil {
		return fmt.Errorf("%w\nExpected adapters/codex/requirements.toml to allow the two reviewed Codex sandbox values", err)
	}

	wrapperPath := filepath.Join(rootDir, "runtime", "container", "provider-wrapper.sh")
	wrapper, err := os.ReadFile(wrapperPath)
	if err != nil {
		return fmt.Errorf("read managed Codex wrapper: %w", err)
	}
	if !strings.Contains(string(wrapper), "MANAGED_CODEX_SANDBOX_ARGS=(--sandbox danger-full-access)") {
		return fmt.Errorf("Expected the managed Codex CLI wrapper to disable the incompatible native sandbox")
	}
	if !strings.Contains(string(wrapper), `MANAGED_CODEX_APP_SERVER_ARGS=(-c 'sandbox_mode="danger-full-access"')`) {
		return fmt.Errorf("Expected the managed Codex GUI wrapper to disable the incompatible native sandbox")
	}

	// The adapter AGENTS.md requires config.toml, managed_config.toml, and
	// requirements.toml to stay aligned on security-boundary config. Lock the
	// requirements [features] values in lockstep with the managed baseline.
	if err := requireCodexExactKeys(requirementsPath, "features", codexSubtable(requirements, "features"), codexFeaturesExactKeys); err != nil {
		return err
	}
	for _, requirement := range codexFeatureRequirements {
		if err := requireCodexAssignment(requirementsPath, requirement.section, codexSubtable(requirements, requirement.section), requirement.key, requirement.expected); err != nil {
			return err
		}
	}
	return nil
}

func decodeCodexTOML(path string) (map[string]any, error) {
	var config map[string]any
	if _, err := toml.DecodeFile(path, &config); err != nil {
		return nil, fmt.Errorf("decode Codex config %s: %w", path, err)
	}
	return config, nil
}

// codexSubtable returns the named top-level table, or an empty map when the
// key is absent or not a table (the bash section scan likewise found no
// assignments there, so the exact-keys and assignment checks fail closed).
func codexSubtable(config map[string]any, section string) map[string]any {
	table, ok := config[section].(map[string]any)
	if !ok {
		return map[string]any{}
	}
	return table
}

// codexSectionLabel mirrors the bash "${section:-top-level}" spelling.
func codexSectionLabel(section string) string {
	if section == "" {
		return "top-level"
	}
	return section
}

// requireCodexAssignment checks one decoded key/value binding, preserving the
// bash require_toml_assignment message formats.
func requireCodexAssignment(path, section string, table map[string]any, key string, expected any) error {
	actual, present := table[key]
	if !present {
		return fmt.Errorf("Expected %s section [%s] to define %s", path, codexSectionLabel(section), key)
	}
	if !codexValueEqual(actual, expected) {
		return fmt.Errorf(
			"Expected %s section [%s] to set %s=%s, got %s",
			path, codexSectionLabel(section), key,
			formatCodexTOMLValue(expected), formatCodexTOMLValue(actual))
	}
	return nil
}

// requireCodexExactKeys checks that the table holds exactly the expected key
// set, preserving the bash require_toml_exact_keys first line and replacing
// its diff -u dump with sorted expected/actual key listings.
func requireCodexExactKeys(path, sectionLabel string, table map[string]any, expected []string) error {
	actual := make([]string, 0, len(table))
	for key := range table {
		actual = append(actual, key)
	}
	sort.Strings(actual)
	want := append([]string(nil), expected...)
	sort.Strings(want)
	if !slices.Equal(want, actual) {
		return fmt.Errorf(
			"Expected %s section [%s] to contain the exact reviewed key set\nexpected keys: %s\nactual keys: %s",
			path, sectionLabel, strings.Join(want, " "), strings.Join(actual, " "))
	}
	return nil
}

func codexValueEqual(actual, expected any) bool {
	expectedList, ok := expected.([]any)
	if !ok {
		return actual == expected
	}
	actualList, ok := actual.([]any)
	if !ok || len(actualList) != len(expectedList) {
		return false
	}
	for i := range expectedList {
		if actualList[i] != expectedList[i] {
			return false
		}
	}
	return true
}

// formatCodexTOMLValue renders a decoded value in TOML literal form so the
// failure messages match the raw-text spellings the bash printed (true,
// false, 3, "medium", ["workspace-write", "danger-full-access"]).
func formatCodexTOMLValue(value any) string {
	switch v := value.(type) {
	case string:
		return strconv.Quote(v)
	case bool:
		return strconv.FormatBool(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case []any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			parts = append(parts, formatCodexTOMLValue(item))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	default:
		return fmt.Sprintf("%v", v)
	}
}
