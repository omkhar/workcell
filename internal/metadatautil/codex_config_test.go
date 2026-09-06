// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validCodexProfileLayerFixture = `sandbox_mode = "workspace-write"
approval_policy = "on-request"
web_search = "disabled"
`

const validCodexRequirementsFixture = `allowed_sandbox_modes = ["workspace-write", "danger-full-access"]

[features]
unified_exec = true
code_mode_host = true
plugins = false
plugin_sharing = false
remote_plugin = false
`

const validCodexWrapperFixture = `MANAGED_CODEX_SANDBOX_ARGS=(--sandbox danger-full-access)
MANAGED_CODEX_APP_SERVER_ARGS=(-c 'sandbox_mode="danger-full-access"')
`

func TestValidateCodexManagedConfigShippedBaselines(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	for _, path := range []string{
		filepath.Join(repoRoot, "adapters", "codex", ".codex", "config.toml"),
		filepath.Join(repoRoot, "adapters", "codex", "managed_config.toml"),
	} {
		if err := ValidateCodexManagedConfig(path); err != nil {
			t.Errorf("ValidateCodexManagedConfig(%s) error = %v", path, err)
		}
	}
}

func TestValidateCodexProfileLayerShippedLayers(t *testing.T) {
	profileDir := filepath.Join("..", "..", "adapters", "codex", ".codex")
	layers := []struct {
		name           string
		sandboxMode    string
		approvalPolicy string
	}{
		{"strict", "workspace-write", "on-request"},
		{"development", "workspace-write", "on-request"},
		{"build", "workspace-write", "never"},
		{"breakglass", "danger-full-access", "never"},
	}
	for _, layer := range layers {
		path := filepath.Join(profileDir, layer.name+".config.toml")
		if err := ValidateCodexProfileLayer(path, layer.sandboxMode, layer.approvalPolicy); err != nil {
			t.Errorf("ValidateCodexProfileLayer(%s) error = %v", path, err)
		}
	}
}

func TestValidateCodexAdapterLockstepShippedRepo(t *testing.T) {
	if err := ValidateCodexAdapterLockstep(filepath.Join("..", "..")); err != nil {
		t.Fatalf("ValidateCodexAdapterLockstep() error = %v", err)
	}
}

// TestValidateCodexManagedConfigMutants replicates every mutant fixture the
// former bash region built from managed_config.toml, plus the positive
// quoted single-segment control. wantMessage "" means the mutant must be
// ACCEPTED.
func TestValidateCodexManagedConfigMutants(t *testing.T) {
	managed := readCodexManagedConfigFixture(t)
	tests := []struct {
		name        string
		mutate      func(t *testing.T, content string) string
		wantMessage string
	}{
		{
			"missing agents.enabled",
			func(t *testing.T, content string) string {
				return replaceExactLine(t, content, "enabled = true", "")
			},
			"section [agents] to define enabled",
		},
		{
			"changed agents.max_concurrent_threads_per_session",
			func(t *testing.T, content string) string {
				return replaceExactLine(t, content, "max_concurrent_threads_per_session = 3", "max_concurrent_threads_per_session = 4")
			},
			"to set max_concurrent_threads_per_session=3, got 4",
		},
		{
			"changed agents.default_subagent_model",
			func(t *testing.T, content string) string {
				return replaceExactLine(t, content, `default_subagent_model = "gpt-5.6-terra"`, `default_subagent_model = "gpt-5.6-luna"`)
			},
			`to set default_subagent_model="gpt-5.6-terra", got "gpt-5.6-luna"`,
		},
		{
			"changed agents.default_subagent_reasoning_effort",
			func(t *testing.T, content string) string {
				return replaceExactLine(t, content, `default_subagent_reasoning_effort = "medium"`, `default_subagent_reasoning_effort = "high"`)
			},
			`to set default_subagent_reasoning_effort="medium", got "high"`,
		},
		{
			"quoted top-level approval_policy override",
			func(t *testing.T, content string) string {
				return insertAfterExactLine(t, content, `web_search = "disabled"`, `"approval_policy" = "never"`)
			},
			"not to define approval_policy",
		},
		{
			"escaped top-level approval_policy override",
			func(t *testing.T, content string) string {
				return insertAfterExactLine(t, content, `web_search = "disabled"`, `"approval\u005fpolicy" = "never"`)
			},
			"not to define approval_policy",
		},
		{
			"whitespace-padded strict sandbox override section",
			func(_ *testing.T, content string) string {
				return content + "\n[ profiles.strict.sandbox_workspace_write ]\nnetwork_access = true\n"
			},
			"not to define any [profiles...] section",
		},
		{
			"quoted strict segment sandbox override section",
			func(_ *testing.T, content string) string {
				return content + "\n[ \"profiles\" . \"strict\" . \"sandbox_workspace_write\" ]\nnetwork_access = true\n"
			},
			"not to define any [profiles...] section",
		},
		{
			"malformed escaped approval_policy override fails closed",
			func(t *testing.T, content string) string {
				return insertAfterExactLine(t, content, `web_search = "disabled"`, `"approval\u00ZZpolicy" = "never"`)
			},
			"decode Codex config",
		},
		{
			// POSITIVE control: BurntSushi decodes a quoted single-segment
			// section name with literal dots to a distinct top-level key, so
			// it must NOT trip the [profiles...] guard.
			"quoted single-segment literal-dot section stays distinct",
			func(_ *testing.T, content string) string {
				return content + "\n[\"profiles.strict.sandbox_workspace_write\"]\nnetwork_access = true\n"
			},
			"",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := writeCodexTempTOML(t, test.mutate(t, managed))
			err := ValidateCodexManagedConfig(path)
			if test.wantMessage == "" {
				if err != nil {
					t.Fatalf("ValidateCodexManagedConfig() error = %v, want acceptance", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantMessage) {
				t.Fatalf("ValidateCodexManagedConfig() error = %v, want message containing %q", err, test.wantMessage)
			}
		})
	}
}

func TestValidateCodexProfileLayerRejectsViolations(t *testing.T) {
	tests := []struct {
		name        string
		layer       string
		wantMessage string
	}{
		{"profile key present", validCodexProfileLayerFixture + "profile = \"strict\"\n", "not to define profile"},
		{"extra flat key", validCodexProfileLayerFixture + "model = \"gpt-5.6-terra\"\n", "to contain the exact reviewed key set"},
		{"missing flat key", strings.Replace(validCodexProfileLayerFixture, "web_search = \"disabled\"\n", "", 1), "to contain the exact reviewed key set"},
		{"wrong sandbox_mode", strings.Replace(validCodexProfileLayerFixture, "\"workspace-write\"", "\"danger-full-access\"", 1), `to set sandbox_mode="workspace-write", got "danger-full-access"`},
		{"wrong approval_policy", strings.Replace(validCodexProfileLayerFixture, "\"on-request\"", "\"never\"", 1), `to set approval_policy="on-request", got "never"`},
		{"wrong web_search", strings.Replace(validCodexProfileLayerFixture, "\"disabled\"", "\"enabled\"", 1), `to set web_search="disabled", got "enabled"`},
		{"section present", validCodexProfileLayerFixture + "[sandbox_workspace_write]\nnetwork_access = true\n", "to contain no [sections]; a profile-v2 layer is a flat key set"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := writeCodexTempTOML(t, test.layer)
			err := ValidateCodexProfileLayer(path, "workspace-write", "on-request")
			if err == nil || !strings.Contains(err.Error(), test.wantMessage) {
				t.Fatalf("ValidateCodexProfileLayer() error = %v, want message containing %q", err, test.wantMessage)
			}
		})
	}
}

func TestValidateCodexProfileLayerAcceptsFlatLayer(t *testing.T) {
	path := writeCodexTempTOML(t, validCodexProfileLayerFixture)
	if err := ValidateCodexProfileLayer(path, "workspace-write", "on-request"); err != nil {
		t.Fatalf("ValidateCodexProfileLayer() error = %v", err)
	}
}

func TestValidateCodexAdapterLockstep(t *testing.T) {
	tests := []struct {
		name         string
		requirements string
		wrapper      string
		wantMessage  string
	}{
		{"reviewed fixture accepted", validCodexRequirementsFixture, validCodexWrapperFixture, ""},
		{
			"wrong allowed_sandbox_modes",
			strings.Replace(validCodexRequirementsFixture, "\"danger-full-access\"", "\"read-only\"", 1),
			validCodexWrapperFixture,
			"Expected adapters/codex/requirements.toml to allow the two reviewed Codex sandbox values",
		},
		{
			"missing allowed_sandbox_modes",
			strings.Replace(validCodexRequirementsFixture, "allowed_sandbox_modes = [\"workspace-write\", \"danger-full-access\"]\n", "", 1),
			validCodexWrapperFixture,
			"to define allowed_sandbox_modes",
		},
		{
			"missing feature key",
			strings.Replace(validCodexRequirementsFixture, "plugins = false\n", "", 1),
			validCodexWrapperFixture,
			"section [features] to contain the exact reviewed key set",
		},
		{
			"wrong feature value",
			strings.Replace(validCodexRequirementsFixture, "plugins = false", "plugins = true", 1),
			validCodexWrapperFixture,
			"to set plugins=false, got true",
		},
		{
			"missing CLI wrapper line",
			validCodexRequirementsFixture,
			strings.Replace(validCodexWrapperFixture, "MANAGED_CODEX_SANDBOX_ARGS=(--sandbox danger-full-access)\n", "", 1),
			"Expected the managed Codex CLI wrapper to disable the incompatible native sandbox",
		},
		{
			"missing GUI wrapper line",
			validCodexRequirementsFixture,
			strings.Replace(validCodexWrapperFixture, "MANAGED_CODEX_APP_SERVER_ARGS", "OTHER_ARGS", 1),
			"Expected the managed Codex GUI wrapper to disable the incompatible native sandbox",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rootDir := writeCodexLockstepFixture(t, test.requirements, test.wrapper)
			err := ValidateCodexAdapterLockstep(rootDir)
			if test.wantMessage == "" {
				if err != nil {
					t.Fatalf("ValidateCodexAdapterLockstep() error = %v, want acceptance", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantMessage) {
				t.Fatalf("ValidateCodexAdapterLockstep() error = %v, want message containing %q", err, test.wantMessage)
			}
		})
	}
}

func readCodexManagedConfigFixture(t *testing.T) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join("..", "..", "adapters", "codex", "managed_config.toml"))
	if err != nil {
		t.Fatalf("read managed_config.toml fixture: %v", err)
	}
	return string(content)
}

// replaceExactLine replaces the whole-line match with replacement, deleting
// the line when replacement is empty; the line must exist (mirroring the bash
// mutant generator's failure when the fixture drifts).
func replaceExactLine(t *testing.T, content, line, replacement string) string {
	t.Helper()
	lines := strings.Split(content, "\n")
	result := make([]string, 0, len(lines))
	replaced := false
	for _, current := range lines {
		if current == line {
			replaced = true
			if replacement != "" {
				result = append(result, replacement)
			}
			continue
		}
		result = append(result, current)
	}
	if !replaced {
		t.Fatalf("expected managed Codex config fixture to contain %q", line)
	}
	return strings.Join(result, "\n")
}

// insertAfterExactLine inserts the extra line after each whole-line match,
// mirroring the bash awk mutant generators.
func insertAfterExactLine(t *testing.T, content, line, insert string) string {
	t.Helper()
	lines := strings.Split(content, "\n")
	result := make([]string, 0, len(lines)+1)
	found := false
	for _, current := range lines {
		result = append(result, current)
		if current == line {
			found = true
			result = append(result, insert)
		}
	}
	if !found {
		t.Fatalf("expected managed Codex config fixture to contain %q", line)
	}
	return strings.Join(result, "\n")
}

func writeCodexTempTOML(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func writeCodexLockstepFixture(t *testing.T, requirements, wrapper string) string {
	t.Helper()
	rootDir := t.TempDir()
	adapterDir := filepath.Join(rootDir, "adapters", "codex")
	containerDir := filepath.Join(rootDir, "runtime", "container")
	for _, dir := range []string{adapterDir, containerDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir fixture dir: %v", err)
		}
	}
	if err := os.WriteFile(filepath.Join(adapterDir, "requirements.toml"), []byte(requirements), 0o600); err != nil {
		t.Fatalf("write requirements fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(containerDir, "provider-wrapper.sh"), []byte(wrapper), 0o600); err != nil {
		t.Fatalf("write wrapper fixture: %v", err)
	}
	return rootDir
}
