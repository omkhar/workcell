// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package remotevm

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestFakeTargetMaterializeWorkspaceCopiesFixtureAndExcludesDotGit(t *testing.T) {
	t.Parallel()

	contract := DefaultContract()
	target, err := NewFakeTarget(contract)
	if err != nil {
		t.Fatal(err)
	}
	sourceRoot := filepath.Join("testdata", "source-workspace")
	tempWorkspace := t.TempDir()
	if _, err := copyWorkspaceTree(sourceRoot, tempWorkspace, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(tempWorkspace, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tempWorkspace, ".git", "config"), []byte("[core]\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := target.MaterializeWorkspace(context.Background(), MaterializeRequest{
		StateRoot:         t.TempDir(),
		TargetID:          "fake-remote-target",
		MaterializationID: "fixture-materialization",
		SourceWorkspace:   tempWorkspace,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(result.MaterializedWorkspace, ".git")); !os.IsNotExist(err) {
		t.Fatalf(".git unexpectedly materialized: %v", err)
	}
	if got, want := len(result.Manifest.Entries), 3; got != want {
		t.Fatalf("len(result.Manifest.Entries) = %d, want %d", got, want)
	}
}
