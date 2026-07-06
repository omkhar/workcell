// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package applecontainer

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestAppleContainerTargetPassesConformance(t *testing.T) {
	t.Parallel()

	stateRoot := t.TempDir()
	sourceWorkspace := writeSampleWorkspace(t)

	target, err := NewAppleContainerTarget(Contract{})
	if err != nil {
		t.Fatalf("NewAppleContainerTarget() error = %v", err)
	}
	contract := DefaultContract()
	c := DefaultConformanceCase(stateRoot, sourceWorkspace)

	result, err := RunConformance(context.Background(), target, contract, c)
	if err != nil {
		t.Fatalf("RunConformance() error = %v", err)
	}

	if result.Exported.Session.Status != contract.Session.FinalStatus {
		t.Fatalf("final status = %q, want %q", result.Exported.Session.Status, contract.Session.FinalStatus)
	}
	if got, want := len(result.Exported.AuditRecords), len(contract.RequiredAuditEvents); got != want {
		t.Fatalf("audit records = %d, want %d", got, want)
	}
	if result.Started.Record.TargetProvider != Provider {
		t.Fatalf("session target_provider = %q, want %q", result.Started.Record.TargetProvider, Provider)
	}
	if result.Finished.Record.FinalAssurance != contract.Session.Assurance {
		t.Fatalf("final_assurance = %q, want %q", result.Finished.Record.FinalAssurance, contract.Session.Assurance)
	}
	// The materialized workspace must not carry .git (local-materialization audit).
	if _, err := os.Stat(filepath.Join(result.Materialization.MaterializedWorkspace, ".git")); !os.IsNotExist(err) {
		t.Fatalf("materialized workspace unexpectedly contains .git")
	}
}

func TestRunConformanceRejectsRemoteContract(t *testing.T) {
	t.Parallel()

	stateRoot := t.TempDir()
	sourceWorkspace := writeSampleWorkspace(t)
	target, err := NewAppleContainerTarget(Contract{})
	if err != nil {
		t.Fatalf("NewAppleContainerTarget() error = %v", err)
	}
	bad := DefaultContract()
	bad.AccessModel = "brokered"

	if _, err := RunConformance(context.Background(), target, bad, DefaultConformanceCase(stateRoot, sourceWorkspace)); err == nil {
		t.Fatalf("RunConformance() with brokered access = nil, want error")
	}
}

func writeSampleWorkspace(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# sample\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}
