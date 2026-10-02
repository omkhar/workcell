// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omkhar/workcell/internal/metadatautil"
)

// TestValidateContainerSmokeBuildEpochRejectsEvasions runs the shared evasion
// corpus against the real epoch assignment.
func TestValidateContainerSmokeBuildEpochRejectsEvasions(t *testing.T) {
	script, err := os.ReadFile(filepath.Join("..", "..", "scripts", "container-smoke.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if err := metadatautil.ValidateContainerSmokeBuildEpoch(string(script)); err != nil {
		t.Fatalf("real script rejected: %v", err)
	}
	anchor := `SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH:-0}"`
	RequireRejectsAllEvasions(t, string(script), anchor, "smoke build epoch",
		metadatautil.ValidateContainerSmokeBuildEpoch)

	for name, replacement := range map[string]string{
		"head commit time": `SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH:-$(git -C "${ROOT_DIR}" log -1 --pretty=%ct 2>/dev/null || printf '0')}"`,
		"command prefix":   anchor + " true",
		"second default":   anchor + "\n" + anchor,
		"other default":    `SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH:-1}"`,
	} {
		t.Run(name, func(t *testing.T) {
			mutated := strings.Replace(string(script), anchor, replacement, 1)
			if err := metadatautil.ValidateContainerSmokeBuildEpoch(mutated); err == nil {
				t.Fatalf("validator accepted %s", name)
			}
		})
	}
}
