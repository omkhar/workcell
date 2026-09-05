// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validCodexRoutingFixture = `developer_instructions = """
Use gpt-5.6-luna with low reasoning for mechanical work,
gpt-5.6-terra with medium reasoning for ordinary work,
gpt-5.6-sol with high reasoning for complex work, and
gpt-6-astra with high reasoning for unresolved work, and
gpt-6-astra with xhigh reasoning for a direct exceptional task when evidence warrants it.
"""
`

const commentOnlyCodexRoutingFixture = `# gpt-5.6-luna with low reasoning
# gpt-5.6-terra with medium reasoning
# gpt-5.6-sol with high reasoning
# gpt-6-astra with high reasoning
# gpt-6-astra with xhigh reasoning
analytics.enabled = false
`

func TestValidateCodexRoutingConfigsShippedBaselines(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	err := ValidateCodexRoutingConfigs(
		filepath.Join(repoRoot, "adapters", "codex", ".codex", "config.toml"),
		filepath.Join(repoRoot, "adapters", "codex", "managed_config.toml"),
	)
	if err != nil {
		t.Fatalf("ValidateCodexRoutingConfigs() error = %v", err)
	}
}

func TestValidateCodexRoutingConfigsNormalizesWhitespace(t *testing.T) {
	managedFixture := strings.ReplaceAll(validCodexRoutingFixture, " for ", "\n\tfor ")
	repoPath, managedPath := writeCodexRoutingFixtures(t, validCodexRoutingFixture, managedFixture)
	if err := ValidateCodexRoutingConfigs(repoPath, managedPath); err != nil {
		t.Fatalf("ValidateCodexRoutingConfigs() error = %v", err)
	}
}

func TestValidateCodexRoutingConfigsRejectsInvalidRouting(t *testing.T) {
	missingBinding := strings.ReplaceAll(validCodexRoutingFixture, "gpt-5.6-sol with high reasoning", "gpt-5.6-sol")
	tests := []struct {
		name        string
		repoConfig  string
		managed     string
		wantMessage string
	}{
		{"missing from repo baseline", commentOnlyCodexRoutingFixture, validCodexRoutingFixture, "developer_instructions must be a top-level string"},
		{"missing from managed baseline", validCodexRoutingFixture, commentOnlyCodexRoutingFixture, "developer_instructions must be a top-level string"},
		{"case variant is not the top-level key", strings.Replace(validCodexRoutingFixture, "developer_instructions", "DEVELOPER_INSTRUCTIONS", 1), validCodexRoutingFixture, "developer_instructions must be a top-level string"},
		{"wrong value type", "developer_instructions = true\n", validCodexRoutingFixture, "developer_instructions must be a top-level string"},
		{"blank instructions", "developer_instructions = \"   \"\n", validCodexRoutingFixture, "developer_instructions must be nonblank"},
		{"required binding missing in both", missingBinding, missingBinding, "must bind gpt-5.6-sol with high reasoning"},
		{"exceptional xhigh binding missing in both", strings.Replace(validCodexRoutingFixture, "gpt-6-astra with xhigh reasoning", "gpt-6-astra", 1), strings.Replace(validCodexRoutingFixture, "gpt-6-astra with xhigh reasoning", "gpt-6-astra", 1), "must bind gpt-6-astra with xhigh reasoning"},
		{"divergent instructions", validCodexRoutingFixture, strings.Replace(validCodexRoutingFixture, "evidence warrants it.", "evidence warrants it. Keep evidence concise.", 1), "differ after whitespace normalization"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repoPath, managedPath := writeCodexRoutingFixtures(t, test.repoConfig, test.managed)
			err := ValidateCodexRoutingConfigs(repoPath, managedPath)
			if err == nil || !strings.Contains(err.Error(), test.wantMessage) {
				t.Fatalf("ValidateCodexRoutingConfigs() error = %v, want message containing %q", err, test.wantMessage)
			}
		})
	}
}

func writeCodexRoutingFixtures(t *testing.T, repoConfig, managedConfig string) (string, string) {
	t.Helper()
	tempDir := t.TempDir()
	repoPath := filepath.Join(tempDir, "config.toml")
	managedPath := filepath.Join(tempDir, "managed_config.toml")
	if err := os.WriteFile(repoPath, []byte(repoConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(managedPath, []byte(managedConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	return repoPath, managedPath
}
