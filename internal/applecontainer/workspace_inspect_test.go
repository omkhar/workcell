// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package applecontainer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestWorkspaceInspectionMatchesPublishedManifest(t *testing.T) {
	target, err := NewAppleContainerTarget(Contract{})
	mustNil(t, err)
	stateRoot, sourceRoot := t.TempDir(), writeSampleWorkspace(t)
	mustNil(t, os.Symlink("README.md", filepath.Join(sourceRoot, "readme-link")))
	materialized, err := target.MaterializeWorkspace(context.Background(), MaterializeRequest{
		StateRoot: stateRoot, TargetID: "tid", MaterializationID: "mid", SourceWorkspace: sourceRoot,
	})
	mustNil(t, err)

	sourceEntries, err := inspectSourceWorkspace(sourceRoot, target.Contract.WorkspaceMaterialization.ExcludedPaths)
	mustNil(t, err)
	finalEntries, err := inspectMaterializedWorkspace(stateRoot, materialized.MaterializedWorkspace)
	mustNil(t, err)
	if !slices.Equal(sourceEntries, materialized.Manifest.Entries) || !slices.Equal(finalEntries, materialized.Manifest.Entries) {
		t.Fatalf("inspection entries differ from the published manifest")
	}
}

func TestWorkspaceInspectionReadsPinnedSymlinkDuringABA(t *testing.T) {
	sourceRoot := t.TempDir()
	for _, name := range []string{"file", "other"} {
		mustNil(t, os.WriteFile(filepath.Join(sourceRoot, name), []byte(name), 0o600))
	}
	link := filepath.Join(sourceRoot, "link")
	mustNil(t, os.Symlink("file", link))
	ops := systemWorkspaceOps()
	originalReadlink := ops.readlink
	pinned := false
	observed := ""
	ops.readlink = func(fd int, name string, buffer []byte) (int, error) {
		pinned = name == ""
		saved := link + ".saved"
		if err := os.Rename(link, saved); err != nil {
			return 0, err
		}
		if err := os.Symlink("other", link); err != nil {
			_ = os.Rename(saved, link)
			return 0, err
		}
		count, readErr := originalReadlink(fd, name, buffer)
		if readErr == nil {
			observed = string(buffer[:count])
		}
		restoreErr := errors.Join(os.Remove(link), os.Rename(saved, link))
		return count, errors.Join(readErr, restoreErr)
	}
	_, err := inspectSourceWorkspaceWithOps(sourceRoot, nil, defaultWorkspaceCopyLimits(), ops)
	if (err != nil && !strings.Contains(err.Error(), "workspace symlink \"link\" changed during inspection")) || !pinned || observed != "file" {
		t.Fatalf("symlink ABA result: pinned=%t observed=%q error=%v", pinned, observed, err)
	}
}

func TestWorkspaceInspectionRejectsSpecialFileBeforeOpen(t *testing.T) {
	sourceRoot := t.TempDir()
	mustNil(t, unix.Mkfifo(filepath.Join(sourceRoot, "pipe"), 0o600))
	ops := systemWorkspaceOps()
	originalOpenat := ops.openat
	openedPipe := false
	ops.openat = func(fd int, name string, flags int, mode uint32) (int, error) {
		if name == "pipe" {
			openedPipe = true
		}
		return originalOpenat(fd, name, flags, mode)
	}
	if _, err := inspectSourceWorkspaceWithOps(sourceRoot, nil, defaultWorkspaceCopyLimits(), ops); err == nil || openedPipe {
		t.Fatalf("special file result: opened=%t error=%v", openedPipe, err)
	}
}

func TestConformanceWorkspaceInspectionDoesNotCreateMirror(t *testing.T) {
	ctx, target, contract, c, layout := newFixture(t)
	materialized, _ := materializeAndBootstrap(t, ctx, target, c)
	missingTempRoot := filepath.Join(t.TempDir(), "missing")
	t.Setenv("TMPDIR", missingTempRoot)
	if temp, err := os.MkdirTemp("", "must-fail."); err == nil {
		_ = os.RemoveAll(temp)
		t.Fatal("test temp root unexpectedly permits mirror creation")
	}
	if err := validateMaterialization(contract, layout, materialized, c); err != nil {
		t.Fatalf("read-only materialization validation failed: %v", err)
	}
	if _, err := os.Lstat(missingTempRoot); !os.IsNotExist(err) {
		t.Fatalf("materialization validation created temp state: %v", err)
	}
}

func TestWorkspaceInspectionRejectsSourceRootRebinding(t *testing.T) {
	sourceRoot := t.TempDir()
	mustNil(t, os.WriteFile(filepath.Join(sourceRoot, "file"), []byte("content"), 0o600))
	movedRoot := sourceRoot + ".moved"
	ops := systemWorkspaceOps()
	originalStat := ops.stat
	rootStats := 0
	ops.stat = func(name string, stat *unix.Stat_t) error {
		rootStats++
		if rootStats == 2 {
			if err := errors.Join(os.Rename(sourceRoot, movedRoot), os.Mkdir(sourceRoot, 0o700)); err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(sourceRoot, "file"), []byte("content"), 0o600); err != nil {
				return err
			}
		}
		return originalStat(name, stat)
	}
	if _, err := inspectSourceWorkspaceWithOps(sourceRoot, nil, defaultWorkspaceCopyLimits(), ops); err == nil || rootStats != 2 {
		t.Fatalf("source rebinding result: rootStats=%d error=%v", rootStats, err)
	}
}

func TestWorkspaceInspectionRejectsFinalRootRebinding(t *testing.T) {
	parent := t.TempDir()
	workspace := filepath.Join(parent, "workspace")
	mustNil(t, os.Mkdir(workspace, 0o755))
	mustNil(t, os.WriteFile(filepath.Join(workspace, "file"), []byte("content"), 0o600))
	parentFD, err := unix.Open(parent, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	mustNil(t, err)
	defer unix.Close(parentFD)
	var initial unix.Stat_t
	mustNil(t, unix.Fstatat(parentFD, "workspace", &initial, unix.AT_SYMLINK_NOFOLLOW))
	expected := workspaceSnapshotFromUnix(initial)

	ops := systemWorkspaceOps()
	originalFstatat := ops.fstatat
	rootStats := 0
	ops.fstatat = func(fd int, name string, stat *unix.Stat_t, flags int) error {
		if fd == parentFD && name == "workspace" {
			rootStats++
			if rootStats == 3 {
				if err := errors.Join(os.Rename(workspace, workspace+".moved"), os.Mkdir(workspace, 0o755)); err != nil {
					return err
				}
				if err := os.WriteFile(filepath.Join(workspace, "file"), []byte("content"), 0o600); err != nil {
					return err
				}
			}
		}
		return originalFstatat(fd, name, stat, flags)
	}
	if _, err := inspectWorkspaceAt(parentFD, "workspace", expected, nil, defaultWorkspaceCopyLimits(), ops); err == nil || rootStats != 3 {
		t.Fatalf("final rebinding result: rootStats=%d error=%v", rootStats, err)
	}
}

func TestWorkspaceInspectionRejectsPreopenRootRebinding(t *testing.T) {
	parent := t.TempDir()
	workspace := filepath.Join(parent, "workspace")
	mustNil(t, os.Mkdir(workspace, 0o755))
	mustNil(t, os.WriteFile(filepath.Join(workspace, "file"), []byte("content"), 0o600))
	parentFD, err := unix.Open(parent, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	mustNil(t, err)
	defer unix.Close(parentFD)
	var initial unix.Stat_t
	mustNil(t, unix.Fstatat(parentFD, "workspace", &initial, unix.AT_SYMLINK_NOFOLLOW))
	expected := workspaceSnapshotFromUnix(initial)

	mustNil(t, os.Rename(workspace, workspace+".saved"))
	mustNil(t, os.Mkdir(workspace, 0o755))
	mustNil(t, os.WriteFile(filepath.Join(workspace, "file"), []byte("content"), 0o600))
	if _, err := inspectWorkspaceAt(parentFD, "workspace", expected, nil, defaultWorkspaceCopyLimits(), systemWorkspaceOps()); err == nil ||
		!strings.Contains(err.Error(), "changed before opening") {
		t.Fatalf("pre-open root rebinding error = %v", err)
	}
}

func TestWorkspaceInspectionRejectsUnsafeIdentityBeforeContentRead(t *testing.T) {
	sourceRoot := t.TempDir()
	unsafeName := "secret\nevent=forged"
	mustNil(t, os.WriteFile(filepath.Join(sourceRoot, unsafeName), []byte("secret"), 0o600))
	ops := systemWorkspaceOps()
	originalOpenat := ops.openat
	openedUnsafe := false
	ops.openat = func(fd int, name string, flags int, mode uint32) (int, error) {
		if name == unsafeName {
			openedUnsafe = true
		}
		return originalOpenat(fd, name, flags, mode)
	}
	_, err := inspectSourceWorkspaceWithOps(sourceRoot, nil, defaultWorkspaceCopyLimits(), ops)
	if err == nil || openedUnsafe || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "forged") {
		t.Fatalf("unsafe identity result: opened=%t error=%v", openedUnsafe, err)
	}
}
