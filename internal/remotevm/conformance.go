// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package remotevm

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/omkhar/workcell/internal/hostutil"
)

type ConformanceCase struct {
	StateRoot         string
	TargetID          string
	MaterializationID string
	BootstrapID       string
	SessionID         string
	Agent             string
	Mode              string
	ImageRef          string
	StartedAt         string
	FinishedAt        string
	ExitStatus        string
	SourceWorkspace   string
}

type ConformanceResult struct {
	Materialization MaterializeResult
	Bootstrap       BootstrapResult
	Started         SessionResult
	Finished        SessionResult
	Exported        hostutil.SessionExport
}

func DefaultConformanceCase(stateRoot, sourceWorkspace string) ConformanceCase {
	return ConformanceCase{
		StateRoot:         stateRoot,
		TargetID:          "fake-remote-target",
		MaterializationID: "fixture-materialization",
		BootstrapID:       "fixture-bootstrap",
		SessionID:         "fixture-session",
		Agent:             "codex",
		Mode:              "strict",
		ImageRef:          "workcell-remotevm:test",
		StartedAt:         "2026-04-21T00:00:00Z",
		FinishedAt:        "2026-04-21T00:15:00Z",
		ExitStatus:        "0",
		SourceWorkspace:   sourceWorkspace,
	}
}

func RunConformance(ctx context.Context, target ConformanceTarget, contract Contract, c ConformanceCase) (ConformanceResult, error) {
	if err := contract.Validate(); err != nil {
		return ConformanceResult{}, err
	}
	materialization, err := target.MaterializeWorkspace(ctx, MaterializeRequest{
		StateRoot:         c.StateRoot,
		TargetID:          c.TargetID,
		MaterializationID: c.MaterializationID,
		SourceWorkspace:   c.SourceWorkspace,
	})
	if err != nil {
		return ConformanceResult{}, err
	}
	if err := validateMaterialization(contract, materialization, c.SourceWorkspace); err != nil {
		return ConformanceResult{}, err
	}
	bootstrap, err := target.BootstrapTarget(ctx, BootstrapRequest{
		StateRoot:   c.StateRoot,
		TargetID:    c.TargetID,
		BootstrapID: c.BootstrapID,
		ImageRef:    c.ImageRef,
	})
	if err != nil {
		return ConformanceResult{}, err
	}
	if err := validateBootstrap(contract, bootstrap, c); err != nil {
		return ConformanceResult{}, err
	}
	started, err := target.StartSession(ctx, StartSessionRequest{
		SessionID:       c.SessionID,
		Agent:           c.Agent,
		Mode:            c.Mode,
		StartedAt:       c.StartedAt,
		Materialization: materialization,
		Bootstrap:       bootstrap,
	})
	if err != nil {
		return ConformanceResult{}, err
	}
	if err := validateStartedSession(contract, started, materialization, bootstrap, c); err != nil {
		return ConformanceResult{}, err
	}
	finished, err := target.FinishSession(ctx, FinishSessionRequest{
		Started:    started,
		FinishedAt: c.FinishedAt,
		ExitStatus: c.ExitStatus,
	})
	if err != nil {
		return ConformanceResult{}, err
	}
	if err := validateFinishedSession(contract, finished, c); err != nil {
		return ConformanceResult{}, err
	}
	records, err := hostutil.ListSessionRecordsInRoots([]string{c.StateRoot}, hostutil.SessionListOptions{})
	if err != nil {
		return ConformanceResult{}, err
	}
	if len(records) != 1 {
		return ConformanceResult{}, fmt.Errorf("ListSessionRecordsInRoots() len = %d, want 1", len(records))
	}
	if got := hostutil.SessionTargetSummary(records[0]); got != "remote_vm/fake-remote/"+c.TargetID {
		return ConformanceResult{}, fmt.Errorf("SessionTargetSummary() = %q", got)
	}
	exported, err := hostutil.ExportSessionRecordInRoots([]string{c.StateRoot}, c.SessionID)
	if err != nil {
		return ConformanceResult{}, err
	}
	if exported.Session.Status != contract.Session.FinalStatus {
		return ConformanceResult{}, fmt.Errorf("exported final status = %q, want %q", exported.Session.Status, contract.Session.FinalStatus)
	}
	if len(exported.AuditRecords) != len(contract.RequiredAuditEvents) {
		return ConformanceResult{}, fmt.Errorf("exported audit record len = %d, want %d", len(exported.AuditRecords), len(contract.RequiredAuditEvents))
	}
	for idx, want := range contract.RequiredAuditEvents {
		if !strings.Contains(exported.AuditRecords[idx], "event="+want) {
			return ConformanceResult{}, fmt.Errorf("audit record %d = %q, want event=%s", idx, exported.AuditRecords[idx], want)
		}
	}
	return ConformanceResult{
		Materialization: materialization,
		Bootstrap:       bootstrap,
		Started:         started,
		Finished:        finished,
		Exported:        exported,
	}, nil
}

func validateMaterialization(contract Contract, result MaterializeResult, sourceWorkspace string) error {
	if result.Manifest.TargetKind != contract.TargetKind {
		return fmt.Errorf("materialization target_kind = %q, want %q", result.Manifest.TargetKind, contract.TargetKind)
	}
	if result.Manifest.WorkspaceTransport != contract.WorkspaceTransport {
		return fmt.Errorf("materialization workspace_transport = %q, want %q", result.Manifest.WorkspaceTransport, contract.WorkspaceTransport)
	}
	if result.Manifest.SourceWorkspace != sourceWorkspace {
		return fmt.Errorf("materialization source_workspace = %q, want %q", result.Manifest.SourceWorkspace, sourceWorkspace)
	}
	if _, err := os.Stat(filepath.Join(result.MaterializedWorkspace, ".git")); !os.IsNotExist(err) {
		return fmt.Errorf("materialized workspace must not contain .git")
	}
	mirrorRoot, err := os.MkdirTemp("", "workcell-remotevm-compare.")
	if err != nil {
		return err
	}
	defer os.RemoveAll(mirrorRoot)
	entries, err := copyWorkspaceTree(sourceWorkspace, filepath.Join(mirrorRoot, "workspace"), contract.WorkspaceMaterialization.ExcludedPaths)
	if err != nil {
		return err
	}
	if !slices.EqualFunc(entries, result.Manifest.Entries, func(a, b WorkspaceEntry) bool {
		return a.Path == b.Path && a.Kind == b.Kind && a.Mode == b.Mode && a.SHA256 == b.SHA256 && a.LinkTarget == b.LinkTarget
	}) {
		return fmt.Errorf("materialization manifest entries do not match copied workspace")
	}
	return nil
}

func validateBootstrap(contract Contract, bootstrap BootstrapResult, c ConformanceCase) error {
	if bootstrap.Manifest.TargetID != c.TargetID {
		return fmt.Errorf("bootstrap target_id = %q, want %q", bootstrap.Manifest.TargetID, c.TargetID)
	}
	if bootstrap.Manifest.TargetAssuranceClass != contract.TargetAssuranceClass {
		return fmt.Errorf("bootstrap target_assurance_class = %q, want %q", bootstrap.Manifest.TargetAssuranceClass, contract.TargetAssuranceClass)
	}
	if bootstrap.Manifest.SupportBoundary != contract.SupportBoundary {
		return fmt.Errorf("bootstrap support_boundary = %q, want %q", bootstrap.Manifest.SupportBoundary, contract.SupportBoundary)
	}
	if bootstrap.Manifest.ImageRef != c.ImageRef {
		return fmt.Errorf("bootstrap image_ref = %q, want %q", bootstrap.Manifest.ImageRef, c.ImageRef)
	}
	return nil
}

func validateStartedSession(contract Contract, session SessionResult, materialization MaterializeResult, bootstrap BootstrapResult, c ConformanceCase) error {
	record := session.Record
	for _, pair := range []struct {
		name string
		got  string
		want string
	}{
		{name: "target_kind", got: record.TargetKind, want: contract.TargetKind},
		{name: "target_provider", got: record.TargetProvider, want: contract.TargetProvider},
		{name: "target_id", got: record.TargetID, want: c.TargetID},
		{name: "target_assurance_class", got: record.TargetAssuranceClass, want: contract.TargetAssuranceClass},
		{name: "runtime_api", got: record.RuntimeAPI, want: contract.RuntimeAPI},
		{name: "workspace_transport", got: record.WorkspaceTransport, want: contract.WorkspaceTransport},
		{name: "workspace", got: record.Workspace, want: materialization.MaterializedWorkspace},
		{name: "workspace_origin", got: record.WorkspaceOrigin, want: c.SourceWorkspace},
		{name: "workspace_control_plane", got: record.WorkspaceControlPlane, want: contract.Session.WorkspaceControlPlane},
		{name: "audit_log_path", got: record.AuditLogPath, want: bootstrap.AuditLogPath},
		{name: "status", got: record.Status, want: contract.Session.StartStatus},
	} {
		if pair.got != pair.want {
			return fmt.Errorf("started session %s = %q, want %q", pair.name, pair.got, pair.want)
		}
	}
	return nil
}

func validateFinishedSession(contract Contract, session SessionResult, c ConformanceCase) error {
	record := session.Record
	if record.Status != contract.Session.FinalStatus {
		return fmt.Errorf("finished session status = %q, want %q", record.Status, contract.Session.FinalStatus)
	}
	if record.FinishedAt != c.FinishedAt {
		return fmt.Errorf("finished session finished_at = %q, want %q", record.FinishedAt, c.FinishedAt)
	}
	if record.ExitStatus != c.ExitStatus {
		return fmt.Errorf("finished session exit_status = %q, want %q", record.ExitStatus, c.ExitStatus)
	}
	if record.FinalAssurance != contract.Session.Assurance {
		return fmt.Errorf("finished session final_assurance = %q, want %q", record.FinalAssurance, contract.Session.Assurance)
	}
	return nil
}
