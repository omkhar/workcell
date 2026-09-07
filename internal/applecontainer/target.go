// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package applecontainer

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// WorkspaceEntry is one materialized workspace path recorded in the manifest.
type WorkspaceEntry struct {
	Path       string      `json:"path"`
	Kind       string      `json:"kind"`
	Mode       fs.FileMode `json:"mode"`
	SHA256     string      `json:"sha256,omitempty"`
	LinkTarget string      `json:"link_target,omitempty"`
}

// WorkspaceManifest records a local materialization of the source workspace.
type WorkspaceManifest struct {
	Version               int              `json:"version"`
	TargetKind            string           `json:"target_kind"`
	TargetProvider        string           `json:"target_provider"`
	TargetID              string           `json:"target_id"`
	WorkspaceTransport    string           `json:"workspace_transport"`
	SourceWorkspace       string           `json:"source_workspace"`
	MaterializationID     string           `json:"materialization_id"`
	MaterializedWorkspace string           `json:"materialized_workspace"`
	ExcludedPaths         []string         `json:"excluded_paths"`
	Entries               []WorkspaceEntry `json:"entries"`
}

// BootstrapManifest records the per-session VM bootstrap parameters.
type BootstrapManifest struct {
	Version              int    `json:"version"`
	TargetKind           string `json:"target_kind"`
	TargetProvider       string `json:"target_provider"`
	TargetID             string `json:"target_id"`
	TargetAssuranceClass string `json:"target_assurance_class"`
	SupportBoundary      string `json:"support_boundary"`
	RuntimeAPI           string `json:"runtime_api"`
	AccessModel          string `json:"access_model"`
	BootstrapID          string `json:"bootstrap_id"`
	ImageRef             string `json:"image_ref"`
}

type MaterializeRequest struct {
	StateRoot         string
	TargetID          string
	MaterializationID string
	SourceWorkspace   string
}

type MaterializeResult struct {
	TargetRoot            string
	MaterializationRoot   string
	ManifestPath          string
	MaterializedWorkspace string
	Manifest              WorkspaceManifest
}

type BootstrapRequest struct {
	StateRoot   string
	TargetID    string
	BootstrapID string
	ImageRef    string
}

type BootstrapResult struct {
	TargetRoot   string
	ManifestPath string
	AuditLogPath string
	Manifest     BootstrapManifest
}

// AppleContainerTarget is a deterministic, filesystem-backed implementation of
// the lifecycle used to prove contract conformance without booting a live VM.
// This change provides the materialization and bootstrap methods; the
// session-lifecycle methods (StartSession/FinishSession) are added in a
// follow-up change.
type AppleContainerTarget struct {
	Contract Contract
}

// NewAppleContainerTarget builds a deterministic target from the given contract,
// defaulting to DefaultContract() for the zero value.
func NewAppleContainerTarget(contract Contract) (AppleContainerTarget, error) {
	if contract.Version == 0 &&
		contract.TargetKind == "" &&
		contract.TargetProvider == "" &&
		contract.TargetAssuranceClass == "" {
		contract = DefaultContract()
	}
	if err := contract.Validate(); err != nil {
		return AppleContainerTarget{}, err
	}
	return AppleContainerTarget{Contract: contract}, nil
}

func (t AppleContainerTarget) MaterializeWorkspace(_ context.Context, req MaterializeRequest) (MaterializeResult, error) {
	return t.materializeWorkspaceWithOps(req, systemWorkspaceMaterializeOps())
}

func (t AppleContainerTarget) materializeWorkspaceWithOps(req MaterializeRequest, ops workspaceMaterializeOps) (MaterializeResult, error) {
	if strings.TrimSpace(req.StateRoot) == "" {
		return MaterializeResult{}, fmt.Errorf("state root is required")
	}
	targetProvider, err := statePathSegment("target provider", t.Contract.TargetProvider)
	if err != nil {
		return MaterializeResult{}, err
	}
	targetID, err := statePathSegment("target id", req.TargetID)
	if err != nil {
		return MaterializeResult{}, err
	}
	materializationID, err := statePathSegment("materialization id", req.MaterializationID)
	if err != nil {
		return MaterializeResult{}, err
	}
	if strings.TrimSpace(req.SourceWorkspace) == "" {
		return MaterializeResult{}, fmt.Errorf("source workspace is required")
	}
	root := targetRoot(req.StateRoot, t.Contract.TargetKind, targetProvider, targetID)
	materializationRoot := filepath.Join(root, "materializations", materializationID)
	workspaceRoot := filepath.Join(materializationRoot, t.Contract.WorkspaceMaterialization.WorkspaceDir)
	manifestPath := filepath.Join(materializationRoot, t.Contract.WorkspaceMaterialization.ManifestName)
	manifest := WorkspaceManifest{
		Version:               1,
		TargetKind:            t.Contract.TargetKind,
		TargetProvider:        t.Contract.TargetProvider,
		TargetID:              targetID,
		WorkspaceTransport:    t.Contract.WorkspaceTransport,
		SourceWorkspace:       req.SourceWorkspace,
		MaterializationID:     materializationID,
		MaterializedWorkspace: workspaceRoot,
		ExcludedPaths:         append([]string(nil), t.Contract.WorkspaceMaterialization.ExcludedPaths...),
		Entries:               nil,
	}
	manifest, err = publishWorkspaceMaterialization(
		req.StateRoot,
		[]string{"targets", t.Contract.TargetKind, targetProvider, targetID},
		materializationID,
		t.Contract.WorkspaceMaterialization.WorkspaceDir,
		t.Contract.WorkspaceMaterialization.ManifestName,
		req.SourceWorkspace,
		t.Contract.WorkspaceMaterialization.ExcludedPaths,
		manifest,
		ops,
	)
	if err != nil {
		return MaterializeResult{}, err
	}
	return MaterializeResult{
		TargetRoot:            root,
		MaterializationRoot:   materializationRoot,
		ManifestPath:          manifestPath,
		MaterializedWorkspace: workspaceRoot,
		Manifest:              manifest,
	}, nil
}

func (t AppleContainerTarget) BootstrapTarget(_ context.Context, req BootstrapRequest) (BootstrapResult, error) {
	if strings.TrimSpace(req.StateRoot) == "" {
		return BootstrapResult{}, fmt.Errorf("state root is required")
	}
	targetProvider, err := statePathSegment("target provider", t.Contract.TargetProvider)
	if err != nil {
		return BootstrapResult{}, err
	}
	targetID, err := statePathSegment("target id", req.TargetID)
	if err != nil {
		return BootstrapResult{}, err
	}
	// bootstrap_id scopes the manifest path (so re-bootstrapping a target with a
	// new id does not overwrite an earlier manifest) and is an audit token, so it
	// must be a safe single path segment.
	bootstrapID, err := statePathSegment("bootstrap id", req.BootstrapID)
	if err != nil {
		return BootstrapResult{}, err
	}
	if strings.TrimSpace(req.ImageRef) == "" {
		return BootstrapResult{}, fmt.Errorf("image ref is required")
	}
	if err := validateAuditToken("image ref", req.ImageRef); err != nil {
		return BootstrapResult{}, err
	}
	root := targetRoot(req.StateRoot, t.Contract.TargetKind, targetProvider, targetID)
	bootstrapRoot := filepath.Join(root, "bootstrap", bootstrapID)
	if err := os.MkdirAll(bootstrapRoot, 0o755); err != nil {
		return BootstrapResult{}, err
	}
	manifestPath := filepath.Join(bootstrapRoot, t.Contract.Bootstrap.ManifestName)
	manifest := BootstrapManifest{
		Version:              1,
		TargetKind:           t.Contract.TargetKind,
		TargetProvider:       t.Contract.TargetProvider,
		TargetID:             targetID,
		TargetAssuranceClass: t.Contract.TargetAssuranceClass,
		SupportBoundary:      t.Contract.SupportBoundary,
		RuntimeAPI:           t.Contract.RuntimeAPI,
		AccessModel:          t.Contract.AccessModel,
		BootstrapID:          bootstrapID,
		ImageRef:             req.ImageRef,
	}
	if err := writeJSON(manifestPath, manifest); err != nil {
		return BootstrapResult{}, err
	}
	return BootstrapResult{
		TargetRoot:   root,
		ManifestPath: manifestPath,
		AuditLogPath: filepath.Join(root, "workcell.audit.log"),
		Manifest:     manifest,
	}, nil
}

func targetRoot(stateRoot, targetKind, targetProvider, targetID string) string {
	return filepath.Join(stateRoot, "targets", targetKind, targetProvider, targetID)
}

func statePathSegment(label, value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("%s is required", label)
	}
	if err := validateAuditToken(label, value); err != nil {
		return "", err
	}
	if value == "." || value == ".." || filepath.IsAbs(value) || strings.ContainsAny(value, `/\`) {
		return "", fmt.Errorf("%s must be a single path segment", label)
	}
	return value, nil
}

// validateAuditToken rejects an opaque TOKEN value (id/ref/timestamp/exit-status)
// that would corrupt the whitespace-delimited `key=value` audit-line format if
// interpolated: any whitespace (a newline forges a whole audit line, a space
// injects a fake key=value field) or control character is refused, since these
// tokens have no legitimate whitespace. PATH values (which may legitimately
// contain spaces) are not tokens; they are percent-encoded instead by the
// session-lifecycle audit writers added in a follow-up change.
func validateAuditToken(label, value string) error {
	for _, r := range value {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return fmt.Errorf("%s must not contain whitespace or control characters", label)
		}
	}
	return nil
}

func writeJSON(path string, value any) error {
	content, err := marshalManifestBytes(value)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}
