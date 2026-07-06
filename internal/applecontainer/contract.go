// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

// Package applecontainer holds the roadmap C1 evaluation spike for the Apple
// `container` runtime (macOS 26+). It is deliberately self-contained: it does
// NOT reuse internal/remotevm's remote-VM Contract, whose Validate() hardcodes
// brokered, remote-materialization semantics that are wrong for a local
// per-session VM. Apple `container` boots one lightweight Virtualization.framework
// VM per container (its own Linux kernel, hostname, NIC and block device), so the
// contract here describes a LOCAL VM with direct (non-brokered) access and local
// workspace materialization while still asserting the same session-lifecycle
// audit events and status transitions the remote-VM contract requires.
package applecontainer

import (
	"fmt"
	"slices"
	"strings"
)

const (
	// TargetKind classifies the Apple `container` target as a local VM, matching
	// the policy/host-support-matrix.tsv target_kind column.
	TargetKind = "local_vm"
	// Provider is the canonical support-matrix provider slug for Apple container.
	Provider = "apple-container"
	// AssuranceClass names the per-session-VM assurance tier: unlike Colima's one
	// shared VM kernel for all sessions, every session gets its own VM boundary.
	AssuranceClass = "per-session-vm"
	// SupportBoundary keeps C1 support-invisible: it is an evaluation preview.
	SupportBoundary = "preview-only"
	// RuntimeAPI is the local `container` CLI, not a brokered remote transport.
	RuntimeAPI = "local-cli"
	// WorkspaceTransport materializes the workspace on the local host filesystem.
	WorkspaceTransport = "local-materialization"
	// AccessModel is direct: the session talks to a local VM, not through a broker.
	AccessModel = "direct"

	MaterializationMode  = "explicit"
	MaterializationFile  = "materialization.json"
	MaterializedWorktree = "workspace"
	BootstrapFile        = "bootstrap.json"
	// WorkspaceControl records that the host drives the local workspace directly.
	WorkspaceControl   = "host-local"
	SessionStartStatus = "running"
	SessionFinalStatus = "exited"
	SessionAssurance   = "per-session-vm-preview-direct"
)

var requiredAuditEvents = []string{
	"workspace_materialized",
	"bootstrap_ready",
	"session_started",
	"session_finished",
}

// RequiredAuditEvents returns a copy of the audit events every conformant Apple
// container session must emit, in lifecycle order.
func RequiredAuditEvents() []string {
	return append([]string(nil), requiredAuditEvents...)
}

// Contract is the local-VM conformance contract for the Apple `container`
// backend. It is a distinct type from remotevm.Contract on purpose.
type Contract struct {
	Version                  int                          `json:"version"`
	TargetKind               string                       `json:"target_kind"`
	TargetProvider           string                       `json:"target_provider"`
	TargetAssuranceClass     string                       `json:"target_assurance_class"`
	SupportBoundary          string                       `json:"support_boundary"`
	RuntimeAPI               string                       `json:"runtime_api"`
	WorkspaceTransport       string                       `json:"workspace_transport"`
	AccessModel              string                       `json:"access_model"`
	WorkspaceMaterialization WorkspaceMaterializationSpec `json:"workspace_materialization"`
	Bootstrap                BootstrapSpec                `json:"bootstrap"`
	Session                  SessionSpec                  `json:"session"`
	RequiredAuditEvents      []string                     `json:"required_audit_events"`
}

type WorkspaceMaterializationSpec struct {
	Mode          string   `json:"mode"`
	ManifestName  string   `json:"manifest_name"`
	WorkspaceDir  string   `json:"workspace_dir"`
	ExcludedPaths []string `json:"excluded_paths"`
}

type BootstrapSpec struct {
	ManifestName string `json:"manifest_name"`
}

type SessionSpec struct {
	WorkspaceControlPlane string `json:"workspace_control_plane"`
	StartStatus           string `json:"start_status"`
	FinalStatus           string `json:"final_status"`
	Assurance             string `json:"assurance"`
}

// DefaultContract returns the canonical Apple-container local-VM contract.
func DefaultContract() Contract {
	return Contract{
		Version:              1,
		TargetKind:           TargetKind,
		TargetProvider:       Provider,
		TargetAssuranceClass: AssuranceClass,
		SupportBoundary:      SupportBoundary,
		RuntimeAPI:           RuntimeAPI,
		WorkspaceTransport:   WorkspaceTransport,
		AccessModel:          AccessModel,
		WorkspaceMaterialization: WorkspaceMaterializationSpec{
			Mode:          MaterializationMode,
			ManifestName:  MaterializationFile,
			WorkspaceDir:  MaterializedWorktree,
			ExcludedPaths: []string{".git"},
		},
		Bootstrap: BootstrapSpec{
			ManifestName: BootstrapFile,
		},
		Session: SessionSpec{
			WorkspaceControlPlane: WorkspaceControl,
			StartStatus:           SessionStartStatus,
			FinalStatus:           SessionFinalStatus,
			Assurance:             SessionAssurance,
		},
		RequiredAuditEvents: RequiredAuditEvents(),
	}
}

// Validate fails closed unless the contract exactly describes the local-VM
// Apple-container semantics. It intentionally rejects brokered/remote values so
// a remote-VM contract cannot masquerade as an Apple-container one.
func (c Contract) Validate() error {
	if c.Version != 1 {
		return fmt.Errorf("unsupported apple-container contract version %d", c.Version)
	}
	for _, field := range []struct {
		name  string
		value string
		want  string
	}{
		{name: "target_kind", value: c.TargetKind, want: TargetKind},
		{name: "target_provider", value: c.TargetProvider, want: Provider},
		{name: "target_assurance_class", value: c.TargetAssuranceClass, want: AssuranceClass},
		{name: "support_boundary", value: c.SupportBoundary, want: SupportBoundary},
		{name: "runtime_api", value: c.RuntimeAPI, want: RuntimeAPI},
		{name: "workspace_transport", value: c.WorkspaceTransport, want: WorkspaceTransport},
		{name: "access_model", value: c.AccessModel, want: AccessModel},
		{name: "workspace_materialization.mode", value: c.WorkspaceMaterialization.Mode, want: MaterializationMode},
		{name: "workspace_materialization.manifest_name", value: c.WorkspaceMaterialization.ManifestName, want: MaterializationFile},
		{name: "workspace_materialization.workspace_dir", value: c.WorkspaceMaterialization.WorkspaceDir, want: MaterializedWorktree},
		{name: "bootstrap.manifest_name", value: c.Bootstrap.ManifestName, want: BootstrapFile},
		{name: "session.workspace_control_plane", value: c.Session.WorkspaceControlPlane, want: WorkspaceControl},
		{name: "session.start_status", value: c.Session.StartStatus, want: SessionStartStatus},
		{name: "session.final_status", value: c.Session.FinalStatus, want: SessionFinalStatus},
		{name: "session.assurance", value: c.Session.Assurance, want: SessionAssurance},
	} {
		if strings.TrimSpace(field.value) == "" {
			return fmt.Errorf("%s may not be empty", field.name)
		}
		if field.value != field.want {
			return fmt.Errorf("%s must be %q, got %q", field.name, field.want, field.value)
		}
	}
	if !slices.Equal(c.WorkspaceMaterialization.ExcludedPaths, []string{".git"}) {
		return fmt.Errorf("workspace_materialization.excluded_paths must be [\".git\"]")
	}
	if !slices.Equal(c.RequiredAuditEvents, requiredAuditEvents) {
		return fmt.Errorf("required_audit_events must be %v", requiredAuditEvents)
	}
	return nil
}
