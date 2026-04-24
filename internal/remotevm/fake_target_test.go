// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package remotevm

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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

func TestFakeTargetUsesProviderSpecificStateRoots(t *testing.T) {
	t.Parallel()

	target, err := NewAWSEC2SSMTarget()
	if err != nil {
		t.Fatal(err)
	}
	result, err := target.MaterializeWorkspace(context.Background(), MaterializeRequest{
		StateRoot:         t.TempDir(),
		TargetID:          "i-1234567890abcdef0",
		MaterializationID: "fixture-materialization",
		SourceWorkspace:   filepath.Join("testdata", "source-workspace"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := filepath.Base(filepath.Dir(result.TargetRoot)); got != AWSEC2SSMProvider {
		t.Fatalf("provider state root = %q, want %q", got, AWSEC2SSMProvider)
	}
	if got := filepath.Base(result.TargetRoot); got != "i-1234567890abcdef0" {
		t.Fatalf("target root leaf = %q, want %q", got, "i-1234567890abcdef0")
	}
}

func TestFakeTargetRejectsPathTraversalIdentifiers(t *testing.T) {
	t.Parallel()

	target, err := NewFakeTarget(DefaultContract())
	if err != nil {
		t.Fatal(err)
	}
	sourceRoot := filepath.Join("testdata", "source-workspace")
	for _, tc := range []struct {
		name string
		req  MaterializeRequest
		want string
	}{
		{
			name: "target",
			req: MaterializeRequest{
				StateRoot:         t.TempDir(),
				TargetID:          "../escape",
				MaterializationID: "fixture-materialization",
				SourceWorkspace:   sourceRoot,
			},
			want: "target id must be a single path segment",
		},
		{
			name: "materialization",
			req: MaterializeRequest{
				StateRoot:         t.TempDir(),
				TargetID:          "fake-remote-target",
				MaterializationID: "../escape",
				SourceWorkspace:   sourceRoot,
			},
			want: "materialization id must be a single path segment",
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := target.MaterializeWorkspace(context.Background(), tc.req); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("MaterializeWorkspace() error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestFakeTargetRejectsPathTraversalProvider(t *testing.T) {
	t.Parallel()

	contract := DefaultContractForProvider("../provider")
	target, err := NewFakeTarget(contract)
	if err != nil {
		t.Fatal(err)
	}
	_, err = target.MaterializeWorkspace(context.Background(), MaterializeRequest{
		StateRoot:         t.TempDir(),
		TargetID:          "fake-remote-target",
		MaterializationID: "fixture-materialization",
		SourceWorkspace:   filepath.Join("testdata", "source-workspace"),
	})
	if err == nil || !strings.Contains(err.Error(), "target provider must be a single path segment") {
		t.Fatalf("MaterializeWorkspace() error = %v, want target provider rejection", err)
	}
}

func TestFakeTargetRejectsPathTraversalSessionID(t *testing.T) {
	t.Parallel()

	target, err := NewFakeTarget(DefaultContract())
	if err != nil {
		t.Fatal(err)
	}
	stateRoot := t.TempDir()
	materialized, err := target.MaterializeWorkspace(context.Background(), MaterializeRequest{
		StateRoot:         stateRoot,
		TargetID:          "fake-remote-target",
		MaterializationID: "fixture-materialization",
		SourceWorkspace:   filepath.Join("testdata", "source-workspace"),
	})
	if err != nil {
		t.Fatal(err)
	}
	bootstrapped, err := target.BootstrapTarget(context.Background(), BootstrapRequest{
		StateRoot:   stateRoot,
		TargetID:    "fake-remote-target",
		BootstrapID: "fixture-bootstrap",
		ImageRef:    "workcell:local",
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = target.StartSession(context.Background(), StartSessionRequest{
		SessionID:       "../escape",
		Agent:           "codex",
		Mode:            "strict",
		StartedAt:       "2026-04-24T00:00:00Z",
		Materialization: materialized,
		Bootstrap:       bootstrapped,
	})
	if err == nil || !strings.Contains(err.Error(), "session id must be a single path segment") {
		t.Fatalf("StartSession() error = %v, want session id rejection", err)
	}
}
