// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package remotevm

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/omkhar/workcell/internal/hostutil"
)

func TestRunConformanceWithFakeTarget(t *testing.T) {
	t.Parallel()

	target, err := NewFakeTarget(DefaultContract())
	if err != nil {
		t.Fatal(err)
	}
	caseSpec := DefaultConformanceCase(t.TempDir(), filepath.Join("testdata", "source-workspace"))
	result, err := RunConformance(context.Background(), target, DefaultContract(), caseSpec)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := result.Exported.Session.TargetKind, "remote_vm"; got != want {
		t.Fatalf("exported session target_kind = %q, want %q", got, want)
	}
	if got, want := result.Exported.Session.WorkspaceTransport, "remote-materialization"; got != want {
		t.Fatalf("exported session workspace_transport = %q, want %q", got, want)
	}
	if got, want := hostutil.SessionTargetSummary(result.Exported.Session), "remote_vm/fake-remote/"+caseSpec.TargetID; got != want {
		t.Fatalf("target summary = %q, want %q", got, want)
	}
}
