// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package applecontainer

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"

	"github.com/omkhar/workcell/internal/pathutil"
	"golang.org/x/sys/unix"
)

// inspectSourceWorkspace reads a source tree without creating a mirror. It
// requires the source path to bind the same directory after inspection.
func inspectSourceWorkspace(sourceRoot string, excluded []string) ([]WorkspaceEntry, error) {
	return inspectSourceWorkspaceWithOps(sourceRoot, excluded, defaultWorkspaceCopyLimits(), systemWorkspaceOps())
}

func inspectSourceWorkspaceWithOps(sourceRoot string, excluded []string, limits workspaceCopyLimits, ops workspaceOps) ([]WorkspaceEntry, error) {
	if !workspaceCopySupported {
		return nil, errWorkspaceCopyUnsupported
	}
	for _, item := range excluded {
		if _, err := pathutil.CollisionKey(item); err != nil {
			return nil, fmt.Errorf("invalid excluded path: %w", err)
		}
	}
	rootFD, rootSnapshot, err := openWorkspaceSourceRoot(sourceRoot, ops)
	if err != nil {
		return nil, err
	}
	defer unix.Close(rootFD)
	entries, err := inspectWorkspaceDescriptor(rootFD, rootSnapshot, excluded, limits, ops)
	if err != nil {
		return nil, err
	}
	reopened, rebound, err := openWorkspaceSourceRoot(sourceRoot, ops)
	if err != nil {
		return nil, fmt.Errorf("source workspace changed after inspection: %w", err)
	}
	defer unix.Close(reopened)
	if rebound != rootSnapshot {
		return nil, fmt.Errorf("source workspace changed after inspection")
	}
	return entries, nil
}

// inspectMaterializedWorkspace reads a derived workspace through StateRoot.
// It rejects parent symlinks and requires the final name to retain its inode.
func inspectMaterializedWorkspace(stateRoot, workspaceRoot string) ([]WorkspaceEntry, error) {
	parentFD, err := openParentDirNoCreate(stateRoot, filepath.Dir(workspaceRoot))
	if err != nil {
		return nil, err
	}
	defer unix.Close(parentFD)
	name := filepath.Base(workspaceRoot)
	var initial unix.Stat_t
	if err := unix.Fstatat(parentFD, name, &initial, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return nil, err
	}
	expected := workspaceSnapshotFromUnix(initial)
	entries, err := inspectWorkspaceAt(parentFD, name, expected, nil, defaultWorkspaceCopyLimits(), systemWorkspaceOps())
	if err != nil {
		return nil, err
	}
	finalStat, err := statPathSafe(stateRoot, workspaceRoot)
	if err != nil || workspaceSnapshotFromUnix(finalStat) != expected {
		return nil, fmt.Errorf("materialized workspace path changed during inspection")
	}
	var pinned unix.Stat_t
	if err := unix.Fstatat(parentFD, name, &pinned, unix.AT_SYMLINK_NOFOLLOW); err != nil ||
		workspaceSnapshotFromUnix(pinned) != expected {
		return nil, fmt.Errorf("materialized workspace path changed during inspection")
	}
	return entries, nil
}

func inspectWorkspaceAt(parentFD int, name string, expected workspaceSnapshot, excluded []string, limits workspaceCopyLimits, ops workspaceOps) ([]WorkspaceEntry, error) {
	if !workspaceCopySupported {
		return nil, errWorkspaceCopyUnsupported
	}
	if err := validateWorkspaceLeaf("workspace root", name); err != nil {
		return nil, err
	}
	var before unix.Stat_t
	if err := ops.fstatat(parentFD, name, &before, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return nil, err
	}
	if workspaceSnapshotFromUnix(before) != expected {
		return nil, fmt.Errorf("materialized workspace changed before opening")
	}
	if expected.mode&uint32(unix.S_IFMT) != uint32(unix.S_IFDIR) {
		return nil, fmt.Errorf("materialized workspace is not a directory")
	}
	rootFD, err := ops.openat(parentFD, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	defer unix.Close(rootFD)
	opened, err := verifyWorkspaceNameAt(parentFD, name, rootFD, ops)
	if err != nil || opened != expected {
		return nil, fmt.Errorf("materialized workspace changed while opening")
	}
	entries, err := inspectWorkspaceDescriptor(rootFD, opened, excluded, limits, ops)
	if err != nil {
		return nil, err
	}
	stable, err := verifyWorkspaceNameAt(parentFD, name, rootFD, ops)
	if err != nil || stable != opened {
		return nil, fmt.Errorf("materialized workspace binding changed during inspection")
	}
	return entries, nil
}

type workspaceInspector struct {
	walk *workspaceWalk
}

func inspectWorkspaceDescriptor(rootFD int, rootSnapshot workspaceSnapshot, excluded []string, limits workspaceCopyLimits, ops workspaceOps) ([]WorkspaceEntry, error) {
	walk := &workspaceWalk{
		excluded:    excluded,
		limits:      limits,
		entries:     make([]WorkspaceEntry, 0),
		reserved:    make(map[string]WorkspaceEntry),
		directories: make(map[workspaceObjectID]string),
		ops:         ops,
	}
	inspector := workspaceInspector{walk: walk}
	if err := inspector.directory(rootFD, "", 0, rootSnapshot); err != nil {
		return nil, err
	}
	if err := validateWorkspaceLinks(walk.entries); err != nil {
		return nil, err
	}
	return sortedWorkspaceEntries(walk.entries), nil
}

func (inspector workspaceInspector) directory(fd int, parentRel string, depth int, expected workspaceSnapshot) error {
	id := workspaceObjectID{expected.dev, expected.ino}
	if previous, found := inspector.walk.directories[id]; found {
		return fmt.Errorf("workspace directory identity repeats at %q and %q", previous, parentRel)
	}
	inspector.walk.directories[id] = parentRel
	initialNames, err := workspaceDirectoryNames(fd, workspaceMaxEntries+1)
	if err != nil {
		return err
	}
	for _, name := range initialNames {
		if err := validateWorkspaceLeaf("workspace entry", name); err != nil {
			return err
		}
		rel := path.Join(parentRel, name)
		if err := validateWorkspacePath(rel, depth+1); err != nil {
			return err
		}
		excluded, err := isExcludedPathSafe(rel, inspector.walk.excluded)
		if err != nil {
			return err
		}
		if inspector.walk.entryCount >= inspector.walk.limits.maxEntries {
			return fmt.Errorf("workspace has more than %d entries", inspector.walk.limits.maxEntries)
		}
		inspector.walk.entryCount++
		if excluded {
			continue
		}
		var stat unix.Stat_t
		if err := inspector.walk.ops.fstatat(fd, name, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			return fmt.Errorf("stat workspace entry %q: %w", rel, err)
		}
		before := workspaceSnapshotFromUnix(stat)
		if err := inspector.walk.reserve(rel); err != nil {
			return err
		}
		var entry WorkspaceEntry
		switch before.mode & uint32(unix.S_IFMT) {
		case uint32(unix.S_IFDIR):
			entry, err = inspector.inspectDirectory(fd, name, rel, depth+1, before)
		case uint32(unix.S_IFREG):
			entry, err = inspector.inspectFile(fd, name, rel, before)
		case uint32(unix.S_IFLNK):
			entry, err = inspector.inspectSymlink(fd, name, rel, before)
		default:
			err = fmt.Errorf("unsupported workspace entry kind for %q", rel)
		}
		if err != nil {
			return err
		}
		if err := inspector.walk.retain(entry); err != nil {
			return err
		}
	}
	var after unix.Stat_t
	if err := inspector.walk.ops.fstat(fd, &after); err != nil || workspaceSnapshotFromUnix(after) != expected {
		return fmt.Errorf("workspace directory changed during inspection")
	}
	stableNames, err := workspaceDirectoryNames(fd, len(initialNames)+1)
	if err != nil {
		return err
	}
	sort.Strings(initialNames)
	sort.Strings(stableNames)
	if !slices.Equal(initialNames, stableNames) {
		return fmt.Errorf("workspace directory entries changed during inspection")
	}
	return nil
}

func (inspector workspaceInspector) inspectDirectory(parentFD int, name, rel string, depth int, before workspaceSnapshot) (WorkspaceEntry, error) {
	fd, err := inspector.walk.ops.openat(parentFD, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return WorkspaceEntry{}, fmt.Errorf("open workspace directory %q: %w", rel, err)
	}
	defer unix.Close(fd)
	opened, err := verifyWorkspaceNameAt(parentFD, name, fd, inspector.walk.ops)
	if err != nil || opened != before {
		return WorkspaceEntry{}, fmt.Errorf("workspace directory %q changed while opening", rel)
	}
	if err := inspector.directory(fd, rel, depth, opened); err != nil {
		return WorkspaceEntry{}, err
	}
	stable, err := verifyWorkspaceNameAt(parentFD, name, fd, inspector.walk.ops)
	if err != nil || stable != opened {
		return WorkspaceEntry{}, fmt.Errorf("workspace directory %q changed during inspection", rel)
	}
	return WorkspaceEntry{Path: rel, Kind: "dir", Mode: entryMode(stable, fs.ModeDir)}, nil
}

func (inspector workspaceInspector) inspectFile(parentFD int, name, rel string, before workspaceSnapshot) (WorkspaceEntry, error) {
	fd, err := inspector.walk.ops.openat(parentFD, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return WorkspaceEntry{}, fmt.Errorf("open workspace file %q: %w", rel, err)
	}
	file := os.NewFile(uintptr(fd), rel)
	defer file.Close()
	var stat unix.Stat_t
	if err := inspector.walk.ops.fstat(fd, &stat); err != nil {
		return WorkspaceEntry{}, err
	}
	opened := workspaceSnapshotFromUnix(stat)
	if opened != before || opened.mode&uint32(unix.S_IFMT) != uint32(unix.S_IFREG) || opened.nlink != 1 {
		return WorkspaceEntry{}, fmt.Errorf("workspace file %q changed kind or is multiply linked", rel)
	}
	if opened.size < 0 || opened.size > inspector.walk.limits.maxFileBytes || opened.size > inspector.walk.limits.maxBytes-inspector.walk.totalBytes {
		return WorkspaceEntry{}, inspector.walk.sizeError(rel, opened.size)
	}
	hash := sha256.New()
	read, err := io.Copy(hash, io.LimitReader(file, opened.size+1))
	if err != nil || read != opened.size {
		return WorkspaceEntry{}, fmt.Errorf("workspace file %q changed while reading", rel)
	}
	digest := hash.Sum(nil)
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return WorkspaceEntry{}, err
	}
	stableHash := sha256.New()
	stableRead, err := io.Copy(stableHash, io.LimitReader(file, opened.size+1))
	if err != nil || stableRead != opened.size || !bytes.Equal(stableHash.Sum(nil), digest) {
		return WorkspaceEntry{}, fmt.Errorf("workspace file %q content changed during inspection", rel)
	}
	stable, err := verifyWorkspaceNameAt(parentFD, name, fd, inspector.walk.ops)
	if err != nil || stable != opened {
		return WorkspaceEntry{}, fmt.Errorf("workspace file %q changed during inspection", rel)
	}
	inspector.walk.totalBytes += opened.size
	return WorkspaceEntry{Path: rel, Kind: "file", Mode: entryMode(stable, 0), SHA256: hex.EncodeToString(digest)}, nil
}

func (inspector workspaceInspector) inspectSymlink(parentFD int, name, rel string, before workspaceSnapshot) (WorkspaceEntry, error) {
	fd, err := inspector.walk.ops.openat(parentFD, name, workspaceSymlinkOpenFlags, 0)
	if err != nil {
		return WorkspaceEntry{}, fmt.Errorf("pin workspace symlink %q: %w", rel, err)
	}
	defer unix.Close(fd)
	opened, err := verifyWorkspaceNameAt(parentFD, name, fd, inspector.walk.ops)
	if err != nil || opened != before || opened.nlink != 1 {
		return WorkspaceEntry{}, fmt.Errorf("workspace symlink %q changed or is multiply linked", rel)
	}
	target, err := readWorkspaceLink(fd, "", inspector.walk.ops)
	if err != nil {
		return WorkspaceEntry{}, err
	}
	if target == "" || path.IsAbs(target) {
		return WorkspaceEntry{}, fmt.Errorf("workspace symlink %q has an invalid target", rel)
	}
	if _, err := pathutil.CollisionKey(target); err != nil {
		return WorkspaceEntry{}, fmt.Errorf("workspace symlink %q has an invalid target: %w", rel, err)
	}
	stable, err := verifyWorkspaceNameAt(parentFD, name, fd, inspector.walk.ops)
	if err != nil || stable != opened {
		return WorkspaceEntry{}, fmt.Errorf("workspace symlink %q changed during inspection", rel)
	}
	return WorkspaceEntry{Path: rel, Kind: "symlink", Mode: entryMode(stable, fs.ModeSymlink), LinkTarget: target}, nil
}
