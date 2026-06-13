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

	codexDir := filepath.Join(repoRoot(t), "adapters", "codex")
	requirementsPath := filepath.Join(codexDir, "requirements.toml")

	// Codex 0.134+ profile-v2: each profile is a separate layer file. The
	// sandbox_mode floor must stay workspace-write everywhere except the
	// explicit breakglass layer.
	for name, wantSandbox := range map[string]string{
		"strict":      "workspace-write",
		"development": "workspace-write",
		"build":       "workspace-write",
		"breakglass":  "danger-full-access",
	} {
		layerPath := filepath.Join(codexDir, ".codex", name+".config.toml")
		layer, err := os.ReadFile(layerPath)
		if err != nil {
			t.Fatal(err)
		}
		want := "sandbox_mode = \"" + wantSandbox + "\""
		if !strings.Contains(string(layer), want) {
			t.Fatalf("%s does not contain %q", layerPath, want)
		}
	}

	// The base configs must not reintroduce inline profile tables; profile
	// selection and sandbox mode now come only from the layer files above.
	for _, base := range []string{
		filepath.Join(codexDir, ".codex", "config.toml"),
		filepath.Join(codexDir, "managed_config.toml"),
	} {
		raw, err := os.ReadFile(base)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "[profiles.") {
			t.Fatalf("%s must not inline [profiles.*] tables under profile-v2", base)
		}
	}

	requirements, err := os.ReadFile(requirementsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(requirements), `allowed_sandbox_modes = ["workspace-write", "danger-full-access"]`) {
		t.Fatalf("%s does not allow managed workspace-write mode", requirementsPath)
	}
}
