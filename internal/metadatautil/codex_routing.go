// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"fmt"
	"strings"

	"github.com/BurntSushi/toml"
)

var requiredCodexRoutingBindings = []string{
	"gpt-5.6-luna with low reasoning",
	"gpt-5.6-terra with medium reasoning",
	"gpt-5.6-sol with high reasoning",
	"gpt-6-astra with high reasoning",
}

// ValidateCodexRoutingConfigs verifies that both Codex baselines retain the
// required routing bindings and decode to the same whitespace-normalized value.
func ValidateCodexRoutingConfigs(repoConfigPath, managedConfigPath string) error {
	repoInstructions, err := validatedCodexRoutingInstructions(repoConfigPath)
	if err != nil {
		return err
	}
	managedInstructions, err := validatedCodexRoutingInstructions(managedConfigPath)
	if err != nil {
		return err
	}
	if repoInstructions != managedInstructions {
		return fmt.Errorf("Codex developer_instructions differ after whitespace normalization: %s and %s", repoConfigPath, managedConfigPath)
	}
	return nil
}

func validatedCodexRoutingInstructions(path string) (string, error) {
	var config map[string]any
	if _, err := toml.DecodeFile(path, &config); err != nil {
		return "", fmt.Errorf("decode Codex config %s: %w", path, err)
	}

	developerInstructions, ok := config["developer_instructions"].(string)
	if !ok {
		return "", fmt.Errorf("%s: developer_instructions must be a top-level string", path)
	}
	instructions := strings.Join(strings.Fields(developerInstructions), " ")
	if instructions == "" {
		return "", fmt.Errorf("%s: developer_instructions must be nonblank", path)
	}
	for _, binding := range requiredCodexRoutingBindings {
		if !strings.Contains(instructions, binding) {
			return "", fmt.Errorf("%s: developer_instructions must bind %s", path, binding)
		}
	}
	return instructions, nil
}
