// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil_test

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckPinnedInputsRejectsMissingAptBrokerContextInput(t *testing.T) {
	for _, line := range []string{
		"!go.mod", "!go.sum", "!internal/", "!internal/aptbroker/", "!internal/aptbroker/**",
		"!cmd/", "!cmd/workcell-apt-broker-client/", "!cmd/workcell-apt-broker-client/**",
		"!cmd/workcell-apt-broker-server/", "!cmd/workcell-apt-broker-server/**",
	} {
		t.Run(line, func(t *testing.T) {
			cfg := writePinnedInputsFixture(t)
			root := filepath.Join(filepath.Dir(cfg.RuntimeDockerfilePath), "..", "..")
			rewriteFile(t, filepath.Join(root, ".dockerignore"), func(content string) string {
				if !strings.Contains(content, line+"\n") {
					t.Fatalf("fixture missing context rule %q", line)
				}
				return strings.Replace(content, line+"\n", "", 1)
			})
			requirePinnedInputsErrorContains(t, cfg, ".dockerignore must match the reviewed runtime build context")
		})
	}
}

func TestCheckPinnedInputsRejectsAppendedContextRules(t *testing.T) {
	for _, line := range []string{"go.mod", "internal/aptbroker/**", "cmd/**", "*", "!**"} {
		t.Run(line, func(t *testing.T) {
			cfg := writePinnedInputsFixture(t)
			root := filepath.Join(filepath.Dir(cfg.RuntimeDockerfilePath), "..", "..")
			rewriteFile(t, filepath.Join(root, ".dockerignore"), func(content string) string {
				return content + line + "\n"
			})
			requirePinnedInputsErrorContains(t, cfg, ".dockerignore must match the reviewed runtime build context")
		})
	}
}
