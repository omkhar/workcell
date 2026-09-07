// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil_test

import (
	"strings"
	"testing"

	"github.com/omkhar/workcell/internal/metadatautil"
)

func TestCheckPinnedInputsAcceptsRootOwnedCodexArtifacts(t *testing.T) {
	if err := metadatautil.CheckPinnedInputs(writePinnedInputsFixture(t)); err != nil {
		t.Fatal(err)
	}
}

func TestCheckPinnedInputsRequiresRootOwnedCodexArtifacts(t *testing.T) {
	artifacts := []struct {
		name  string
		label string
	}{
		{"codex", "root-owned Codex runtime artifact install"},
		{"codex-code-mode-host", "root-owned Codex code-mode host runtime artifact install"},
	}
	for _, artifact := range artifacts {
		for _, command := range []string{"mv", "install -m 0755", "install -o 1001 -g 0 -m 0755", "install -o 0 -g 1001 -m 0755", "install -o 0 -g 0 -m 0775", "# install -o 0 -g 0 -m 0755", "echo install -o 0 -g 0 -m 0755"} {
			t.Run(artifact.name+"/"+command, func(t *testing.T) {
				source := `"/tmp/` + artifact.name + `-${CODEX_ARCH}"`
				original := "install -o 0 -g 0 -m 0755 " + source
				cfg := rewritePinnedInputsFixtureFile(t, "runtime/container/Dockerfile", func(content string) string {
					if strings.Count(content, original) != 1 {
						t.Fatalf("expected one installation of %s", artifact.name)
					}
					return strings.Replace(content, original, command+" "+source, 1)
				})
				requirePinnedInputsErrorContains(t, cfg, artifact.label)
			})
		}
	}
}
