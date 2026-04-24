// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package remotevm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
)

const (
	TargetKind           = "remote_vm"
	CanonicalProvider    = "fake-remote"
	AssuranceClass       = "compat"
	SupportBoundary      = "preview-only"
	RuntimeAPI           = "brokered"
	WorkspaceTransport   = "remote-materialization"
	AccessModel          = "brokered"
	MaterializationMode  = "explicit"
	MaterializationFile  = "materialization.json"
	MaterializedWorktree = "workspace"
	BootstrapFile        = "bootstrap.json"
	WorkspaceControl     = "host-brokered"
	SessionStartStatus   = "running"
	SessionFinalStatus   = "exited"
	SessionAssurance     = "compat-preview-brokered"
)

var requiredAuditEvents = []string{
	"workspace_materialized",
	"bootstrap_ready",
	"session_started",
	"session_finished",
}

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

func DefaultContract() Contract {
	return DefaultContractForProvider(CanonicalProvider)
}

func DefaultContractForProvider(provider string) Contract {
	if strings.TrimSpace(provider) == "" {
		provider = CanonicalProvider
	}
	return Contract{
		Version:              1,
		TargetKind:           TargetKind,
		TargetProvider:       provider,
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
		RequiredAuditEvents: append([]string(nil), requiredAuditEvents...),
	}
}

func LoadContract(path string) (Contract, error) {
	var contract Contract
	content, err := os.ReadFile(path)
	if err != nil {
		return contract, err
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&contract); err != nil {
		return contract, err
	}
	if err := decoder.Decode(&struct{}{}); err != nil && err != io.EOF {
		return contract, fmt.Errorf("%s: unexpected trailing JSON content", path)
	}
	if err := contract.Validate(); err != nil {
		return contract, fmt.Errorf("%s: %w", path, err)
	}
	return contract, nil
}

func (c Contract) Validate() error {
	if c.Version != 1 {
		return fmt.Errorf("unsupported remote-vm contract version %d", c.Version)
	}
	if strings.TrimSpace(c.TargetProvider) == "" {
		return fmt.Errorf("target_provider may not be empty")
	}
	for _, field := range []struct {
		name  string
		value string
		want  string
	}{
		{name: "target_kind", value: c.TargetKind, want: TargetKind},
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
