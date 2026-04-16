// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package testkit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodexManagedConfigKeepsWorkspaceWriteOutsideBreakglass(t *testing.T) {
	t.Parallel()

	configPath := filepath.Join(repoRoot(t), "adapters", "codex", ".codex", "config.toml")
	managedConfigPath := filepath.Join(repoRoot(t), "adapters", "codex", "managed_config.toml")
	requirementsPath := filepath.Join(repoRoot(t), "adapters", "codex", "requirements.toml")
	config, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	managedConfig, err := os.ReadFile(managedConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	requirements, err := os.ReadFile(requirementsPath)
	if err != nil {
		t.Fatal(err)
	}

	for path, raw := range map[string]string{
		configPath:        string(config),
		managedConfigPath: string(managedConfig),
	} {
		for _, want := range []string{
			"[profiles.strict]\nsandbox_mode = \"workspace-write\"",
			"[profiles.development]\nsandbox_mode = \"workspace-write\"",
			"[profiles.build]\nsandbox_mode = \"workspace-write\"",
			"[profiles.breakglass]\nsandbox_mode = \"danger-full-access\"",
		} {
			if !strings.Contains(raw, want) {
				t.Fatalf("%s does not contain %q", path, want)
			}
		}
	}

	requirementsText := string(requirements)
	if !strings.Contains(requirementsText, `allowed_sandbox_modes = ["workspace-write", "danger-full-access"]`) {
		t.Fatalf("%s does not allow managed workspace-write mode", requirementsPath)
	}
}
