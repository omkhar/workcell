// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package mutation

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMutationAnchorsResolve proves every registered mutant still names text
// that exists in the tree it mutates. A refactor that moves or rewrites a
// mutated line otherwise leaves the row pointing at nothing, and only the
// CI-gated lane reports it, as a harness failure rather than a survivor.
func TestMutationAnchorsResolve(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	for _, tc := range append(append([]mutationCase{}, goHelperMutations...), rustMutations...) {
		t.Run(tc.label, func(t *testing.T) {
			content, err := os.ReadFile(filepath.Join(root, tc.relativePath))
			if err != nil {
				t.Fatalf("read %s: %v", tc.relativePath, err)
			}
			if !strings.Contains(string(content), tc.original) {
				t.Fatalf("anchor absent from %s: %q", tc.relativePath, tc.original)
			}
		})
	}
}
