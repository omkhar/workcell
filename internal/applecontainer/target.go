// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package applecontainer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
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
	// Writing generated state into (or as, or around) the tree being copied is a
	// degenerate configuration; reject it before creating any state.
	if overlap, err := stateRootOverlapsSource(req.StateRoot, req.SourceWorkspace); err != nil {
		return MaterializeResult{}, err
	} else if overlap {
		return MaterializeResult{}, fmt.Errorf("state root %q must not overlap source workspace %q", req.StateRoot, req.SourceWorkspace)
	}
	root := targetRoot(req.StateRoot, t.Contract.TargetKind, targetProvider, targetID)
	materializationRoot := filepath.Join(root, "materializations", materializationID)
	workspaceRoot := filepath.Join(materializationRoot, t.Contract.WorkspaceMaterialization.WorkspaceDir)
	manifestPath := filepath.Join(materializationRoot, t.Contract.WorkspaceMaterialization.ManifestName)
	if err := os.RemoveAll(materializationRoot); err != nil {
		return MaterializeResult{}, err
	}
	if err := os.MkdirAll(workspaceRoot, 0o755); err != nil {
		return MaterializeResult{}, err
	}
	entries, err := copyWorkspaceTree(req.SourceWorkspace, workspaceRoot, t.Contract.WorkspaceMaterialization.ExcludedPaths)
	if err != nil {
		return MaterializeResult{}, err
	}
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
		Entries:               entries,
	}
	if err := writeJSON(manifestPath, manifest); err != nil {
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

// stateRootOverlapsSource reports whether the state root equals, is inside, or is
// a parent of the source workspace, comparing absolute symlink-resolved paths.
func stateRootOverlapsSource(stateRoot, source string) (bool, error) {
	s, err := resolveExistingPrefix(source)
	if err != nil {
		return false, err
	}
	r, err := resolveExistingPrefix(stateRoot)
	if err != nil {
		return false, err
	}
	return strings.EqualFold(s, r) || pathWithin(s, r) || pathWithin(r, s), nil
}

// pathWithin reports whether child is inside parent (component-aware via
// filepath.Rel, so /foobar is not inside /foo). Comparison is case-insensitive
// because on a case-insensitive volume (APFS default) /Foo and /foo are the same
// path; this is also the safe direction for the isolation/overlap boundary.
func pathWithin(parent, child string) bool {
	rel, err := filepath.Rel(strings.ToLower(parent), strings.ToLower(child))
	if err != nil || rel == "." {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// resolveExistingPrefix makes path absolute and resolves symlinks on its longest
// existing prefix, appending any not-yet-created tail lexically.
func resolveExistingPrefix(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	current, tail := filepath.Clean(abs), ""
	for {
		if resolved, err := filepath.EvalSymlinks(current); err == nil {
			return filepath.Join(resolved, tail), nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return abs, nil
		}
		current, tail = parent, filepath.Join(filepath.Base(current), tail)
	}
}

// copyWorkspaceTree copies sourceRoot into destRoot, excluding the configured
// paths, and returns a sorted manifest of the copied entries. Symlinks that are
// absolute or escape the workspace (lexically or after kernel resolution) fail
// closed so materialization cannot smuggle non-workspace content into the VM.
func copyWorkspaceTree(sourceRoot, destRoot string, excluded []string) ([]WorkspaceEntry, error) {
	entries := make([]WorkspaceEntry, 0)
	// Dir chmods deferred to after the walk (deepest-first) so a read-only source dir stays writable while children copy.
	var dirDests []string
	var dirPerms []fs.FileMode
	// Resolve the root first so a symlinked source-workspace root is walked as its real dir (WalkDir does not follow it).
	resolvedRoot, err := filepath.EvalSymlinks(sourceRoot)
	if err != nil {
		return nil, err
	}
	// A non-directory source workspace (file, or symlink to one) is invalid: WalkDir would visit only the root.
	if rootInfo, err := os.Stat(resolvedRoot); err != nil {
		return nil, err
	} else if !rootInfo.IsDir() {
		return nil, fmt.Errorf("source workspace is not a directory: %s", sourceRoot)
	}
	// Absolute root for symlink-containment checks: a relative sourceRoot (e.g. ".")
	// makes EvalSymlinks return relative paths, so compare resolved absolutes.
	rootAbs, err := filepath.Abs(resolvedRoot)
	if err != nil {
		return nil, err
	}
	err = filepath.WalkDir(resolvedRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == resolvedRoot {
			return nil
		}
		rel, err := filepath.Rel(resolvedRoot, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if isExcludedPath(rel, excluded) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		destPath := filepath.Join(destRoot, filepath.FromSlash(rel))
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			if filepath.IsAbs(target) {
				return fmt.Errorf("workspace symlink %s targets an absolute path: %s", rel, target)
			}
			resolved := filepath.ToSlash(filepath.Clean(filepath.Join(filepath.Dir(filepath.FromSlash(rel)), target)))
			if resolved == ".." || strings.HasPrefix(resolved, "../") {
				return fmt.Errorf("workspace symlink %s escapes the workspace: %s", rel, target)
			}
			physical, err := filepath.EvalSymlinks(path)
			if err != nil {
				return fmt.Errorf("workspace symlink %s does not resolve inside the workspace: %v", rel, err)
			}
			physicalAbs, err := filepath.Abs(physical)
			if err != nil {
				return err
			}
			if !strings.EqualFold(physicalAbs, rootAbs) && !pathWithin(rootAbs, physicalAbs) {
				return fmt.Errorf("workspace symlink %s escapes the workspace: %s", rel, target)
			}
			if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
				return err
			}
			if err := os.Symlink(target, destPath); err != nil {
				return err
			}
			entries = append(entries, WorkspaceEntry{Path: rel, Kind: "symlink", Mode: info.Mode(), LinkTarget: target})
		case info.IsDir():
			if err := os.MkdirAll(destPath, 0o700); err != nil {
				return err
			}
			dirDests = append(dirDests, destPath)
			dirPerms = append(dirPerms, info.Mode().Perm())
			entries = append(entries, WorkspaceEntry{Path: rel, Kind: "dir", Mode: info.Mode()})
		case info.Mode().IsRegular():
			if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
				return err
			}
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if err := os.WriteFile(destPath, content, info.Mode().Perm()); err != nil {
				return err
			}
			if err := os.Chmod(destPath, info.Mode().Perm()); err != nil {
				return err
			}
			sum := sha256.Sum256(content)
			entries = append(entries, WorkspaceEntry{Path: rel, Kind: "file", Mode: info.Mode(), SHA256: hex.EncodeToString(sum[:])})
		default:
			return fmt.Errorf("unsupported workspace entry kind for %s", rel)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	// Restore directory modes deepest-first so a read-only parent can't block a child.
	for i := len(dirDests) - 1; i >= 0; i-- {
		if err := os.Chmod(dirDests[i], dirPerms[i]); err != nil {
			return nil, err
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Path < entries[j].Path
	})
	return entries, nil
}

// isExcludedPath matches exclusions case-insensitively (component-boundary), so
// on a case-insensitive volume (APFS default) `.GIT`/`.Git` are excluded just
// like `.git` — git control state must never leak into the isolated workspace.
func isExcludedPath(rel string, excluded []string) bool {
	lower := strings.ToLower(rel)
	for _, item := range excluded {
		item = strings.ToLower(item)
		if lower == item || strings.HasPrefix(lower, item+"/") {
			return true
		}
	}
	return false
}

func writeJSON(path string, value any) error {
	content, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	content = append(content, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}
