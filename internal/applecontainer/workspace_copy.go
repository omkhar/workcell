// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package applecontainer

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/omkhar/workcell/internal/pathutil"
	"golang.org/x/sys/unix"
)

const (
	workspaceMaxFileBytes     int64 = 256 << 20
	workspaceMaxBytes         int64 = 2 << 30
	workspaceMaxEntries             = 100_000
	workspaceMaxPathBytes           = 4096
	workspaceMaxLinkBytes           = 4096
	workspaceMaxDepth               = 256
	workspaceMaxSymlinkHops         = 40
	workspaceManifestMaxBytes       = 64 << 20
)

var errWorkspaceCopyUnsupported = errors.New("workspace descriptor copy is unsupported on this platform")

type workspaceCopyLimits struct{ maxFileBytes, maxBytes, maxEntries int64 }

func defaultWorkspaceCopyLimits() workspaceCopyLimits {
	return workspaceCopyLimits{workspaceMaxFileBytes, workspaceMaxBytes, workspaceMaxEntries}
}

// workspaceSnapshot excludes access time because descriptor reads can change it.
type workspaceSnapshot struct {
	mode                      uint32
	size                      int64
	nlink                     uint64
	uid, gid                  uint32
	dev, ino                  uint64
	mtimeSec, mtimeNsec       int64
	changeTimeSec, changeNsec int64
}

type workspaceObjectID struct{ dev, ino uint64 }
type workspaceOps struct {
	openat    func(int, string, int, uint32) (int, error)
	fstat     func(int, *unix.Stat_t) error
	fstatat   func(int, string, *unix.Stat_t, int) error
	mkdirat   func(int, string, uint32) error
	fchmod    func(int, uint32) error
	readlink  func(int, string, []byte) (int, error)
	symlinkat func(string, int, string) error
}

func systemWorkspaceOps() workspaceOps {
	return workspaceOps{unix.Openat, unix.Fstat, unix.Fstatat, unix.Mkdirat, unix.Fchmod, workspaceReadlink, unix.Symlinkat}
}

type workspaceWalk struct {
	excluded                              []string
	limits                                workspaceCopyLimits
	entries                               []WorkspaceEntry
	fileSizes                             map[string]int64
	reserved                              map[string]WorkspaceEntry
	directories                           map[workspaceObjectID]string
	entryCount, totalBytes, manifestBytes int64
	ops                                   workspaceOps
}

// copyWorkspaceTreeDescriptor requires parentFD to stay open and caller-owned.
// It must name a private CLOEXEC directory that is physically disjoint from sourceRoot.
// Each entry uses its last descriptor-content or directory-name check as its source observation point.
// The function does not provide a filesystem-wide source snapshot.
// The function does not close parentFD.
func copyWorkspaceTreeDescriptor(sourceRoot string, parentFD int, name string, excluded []string) ([]WorkspaceEntry, error) {
	return copyWorkspaceTreeWithOps(sourceRoot, parentFD, name, excluded, defaultWorkspaceCopyLimits(), systemWorkspaceOps())
}

func abortWorkspaceCopy(fd int, err error) ([]WorkspaceEntry, error) { unix.Close(fd); return nil, err }
func closeFDError(fd int, f string, a ...any) error                  { unix.Close(fd); return fmt.Errorf(f, a...) }

func copyWorkspaceTreeWithOps(sourceRoot string, parentFD int, name string, excluded []string, limits workspaceCopyLimits, ops workspaceOps) ([]WorkspaceEntry, error) {
	if !workspaceCopySupported {
		return nil, errWorkspaceCopyUnsupported
	}
	if err := validateWorkspaceLeaf("workspace destination", name); err != nil {
		return nil, err
	}
	sourceFD, sourceSnapshot, err := openWorkspaceSourceRoot(sourceRoot, ops)
	if err != nil {
		return nil, err
	}
	if err := requireWorkspaceNameAbsent(parentFD, name, ops); err != nil {
		return abortWorkspaceCopy(sourceFD, err)
	}
	destFD, err := createWorkspaceDirectory(parentFD, name, ops)
	if err != nil {
		return abortWorkspaceCopy(sourceFD, err)
	}
	walk := &workspaceWalk{excluded: excluded, limits: limits, fileSizes: make(map[string]int64), reserved: make(map[string]WorkspaceEntry), directories: make(map[workspaceObjectID]string), ops: ops}
	err = walk.directory(sourceFD, destFD, "", 0, sourceSnapshot)
	if err == nil {
		err = validateWorkspaceLinks(walk.entries)
	}
	if err == nil {
		err = ops.fchmod(destFD, 0o755)
	}
	if err == nil {
		err = walk.validateDestination(destFD)
	}
	if err == nil {
		_, err = verifyWorkspaceNameAt(parentFD, name, destFD, ops)
	}
	err = errors.Join(err, unix.Close(destFD))
	if err != nil {
		return nil, err
	}
	sort.Slice(walk.entries, func(i, j int) bool { return walk.entries[i].Path < walk.entries[j].Path })
	return walk.entries, nil
}

func (walk *workspaceWalk) directory(sourceFD, destFD int, parentRel string, depth int, expected workspaceSnapshot) (retErr error) {
	source := os.NewFile(uintptr(sourceFD), parentRel)
	defer func() { retErr = errors.Join(retErr, source.Close()) }()
	initialNames := []string{}
	id := workspaceObjectID{expected.dev, expected.ino}
	if previous, found := walk.directories[id]; found {
		return fmt.Errorf("workspace directory identity repeats at %q and %q", previous, parentRel)
	}
	walk.directories[id] = parentRel
	for {
		names, readErr := source.Readdirnames(128)
		if readErr != nil && readErr != io.EOF {
			return readErr
		}
		initialNames = append(initialNames, names...)
		for _, name := range names {
			if err := validateWorkspaceLeaf("workspace entry", name); err != nil {
				return err
			}
			rel := path.Join(parentRel, name)
			if err := validateWorkspacePath(rel, depth+1); err != nil {
				return err
			}
			excluded, err := isExcludedPathSafe(rel, walk.excluded)
			if err != nil {
				return err
			}
			if walk.entryCount >= walk.limits.maxEntries {
				return fmt.Errorf("workspace has more than %d entries", walk.limits.maxEntries)
			}
			walk.entryCount++
			if excluded {
				continue
			}
			var before unix.Stat_t
			if err := walk.ops.fstatat(sourceFD, name, &before, unix.AT_SYMLINK_NOFOLLOW); err != nil {
				return fmt.Errorf("stat workspace entry %q: %w", rel, err)
			}
			if err := walk.reserve(rel); err != nil {
				return err
			}
			snapshot := workspaceSnapshotFromUnix(before)
			switch snapshot.mode & uint32(unix.S_IFMT) {
			case uint32(unix.S_IFDIR):
				err = walk.copyDirectory(sourceFD, destFD, name, rel, depth+1, snapshot)
			case uint32(unix.S_IFREG):
				err = walk.copyFile(sourceFD, destFD, name, rel, snapshot)
			case uint32(unix.S_IFLNK):
				err = walk.copySymlink(sourceFD, destFD, name, rel, snapshot)
			default:
				err = fmt.Errorf("unsupported workspace entry kind for %q", rel)
			}
			if err != nil {
				return err
			}
		}
		if readErr == io.EOF {
			break
		}
	}
	var after unix.Stat_t
	if err := walk.ops.fstat(sourceFD, &after); err != nil || workspaceSnapshotFromUnix(after) != expected {
		return fmt.Errorf("workspace directory changed while copying")
	}
	stableNames, err := workspaceDirectoryNames(sourceFD, len(initialNames)+1)
	sort.Strings(initialNames)
	sort.Strings(stableNames)
	if err != nil || strings.Join(stableNames, "\x00") != strings.Join(initialNames, "\x00") {
		return fmt.Errorf("workspace directory entries changed while copying")
	}
	return nil
}

func (walk *workspaceWalk) copyDirectory(sourceParent, destParent int, name, rel string, depth int, before workspaceSnapshot) (retErr error) {
	child, err := walk.ops.openat(sourceParent, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("open workspace directory %q: %w", rel, err)
	}
	var opened unix.Stat_t
	if err := walk.ops.fstat(child, &opened); err != nil || workspaceSnapshotFromUnix(opened) != before {
		return closeFDError(child, "workspace directory %q changed before open", rel)
	}
	destChild, err := createWorkspaceDirectory(destParent, name, walk.ops)
	if err != nil {
		return closeFDError(child, "create workspace directory %q: %w", rel, err)
	}
	defer func() { retErr = errors.Join(retErr, unix.Close(destChild)) }()
	if err := walk.directory(child, destChild, rel, depth, before); err != nil {
		return err
	}
	mode := entryMode(before, fs.ModeDir)
	if err := walk.ops.fchmod(destChild, uint32(mode.Perm())); err != nil {
		return err
	}
	if _, err := verifyWorkspaceNameAt(destParent, name, destChild, walk.ops); err != nil {
		return err
	}
	return walk.retain(WorkspaceEntry{Path: rel, Kind: "dir", Mode: mode})
}

func (walk *workspaceWalk) copyFile(sourceParent, destParent int, name, rel string, before workspaceSnapshot) (retErr error) {
	fd, err := walk.ops.openat(sourceParent, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("open workspace file %q: %w", rel, err)
	}
	source := os.NewFile(uintptr(fd), rel)
	defer func() { retErr = errors.Join(retErr, source.Close()) }()
	var opened unix.Stat_t
	if err := walk.ops.fstat(fd, &opened); err != nil {
		return err
	}
	openedSnapshot := workspaceSnapshotFromUnix(opened)
	if openedSnapshot != before || openedSnapshot.mode&uint32(unix.S_IFMT) != uint32(unix.S_IFREG) || openedSnapshot.nlink != 1 {
		return fmt.Errorf("workspace file %q changed kind or is multiply linked", rel)
	}
	if openedSnapshot.size < 0 || openedSnapshot.size > walk.limits.maxFileBytes || openedSnapshot.size > walk.limits.maxBytes-walk.totalBytes {
		return walk.sizeError(rel, openedSnapshot.size)
	}
	destFD, err := walk.ops.openat(destParent, name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return fmt.Errorf("create workspace file %q: %w", rel, err)
	}
	dest := os.NewFile(uintptr(destFD), rel)
	defer func() { retErr = errors.Join(retErr, dest.Close()) }()
	allowed := min(walk.limits.maxFileBytes, walk.limits.maxBytes-walk.totalBytes)
	hash := sha256.New()
	written, err := io.CopyBuffer(io.MultiWriter(dest, hash), io.LimitReader(source, allowed+1), make([]byte, 32*1024))
	if err != nil {
		return err
	}
	if written > allowed {
		return walk.sizeError(rel, written)
	}
	var after unix.Stat_t
	if err := walk.ops.fstat(fd, &after); err != nil || workspaceSnapshotFromUnix(after) != before {
		return fmt.Errorf("workspace file %q changed while copying", rel)
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	if _, err := source.Seek(0, io.SeekStart); err != nil {
		return err
	}
	stableHash := sha256.New()
	if read, err := io.Copy(stableHash, io.LimitReader(source, written+1)); err != nil || read != written || hex.EncodeToString(stableHash.Sum(nil)) != digest {
		return fmt.Errorf("workspace file %q content changed while copying", rel)
	}
	walk.totalBytes += written
	mode := entryMode(openedSnapshot, 0)
	if err := walk.ops.fchmod(destFD, uint32(mode.Perm())); err != nil {
		return err
	}
	copiedSnapshot, err := verifyWorkspaceNameAt(destParent, name, destFD, walk.ops)
	if err != nil {
		return err
	}
	if copiedSnapshot.mode&uint32(unix.S_IFMT) != uint32(unix.S_IFREG) || copiedSnapshot.nlink != 1 {
		return fmt.Errorf("workspace file %q is not an isolated regular file", rel)
	}
	mode = entryMode(copiedSnapshot, 0)
	walk.fileSizes[rel] = written
	return walk.retain(WorkspaceEntry{Path: rel, Kind: "file", Mode: mode, SHA256: digest})
}

func (walk *workspaceWalk) copySymlink(sourceParent, destParent int, name, rel string, before workspaceSnapshot) error {
	sourceLinkFD, err := walk.ops.openat(sourceParent, name, workspaceSymlinkOpenFlags, 0)
	if err != nil {
		return fmt.Errorf("pin workspace symlink %q: %w", rel, err)
	}
	defer unix.Close(sourceLinkFD)
	if opened, err := verifyWorkspaceNameAt(sourceParent, name, sourceLinkFD, walk.ops); err != nil || opened != before {
		return fmt.Errorf("workspace symlink %q changed before read", rel)
	}
	target, err := readWorkspaceLink(sourceParent, name, walk.ops)
	if err != nil {
		return fmt.Errorf("read workspace symlink %q: %w", rel, err)
	}
	if target == "" {
		return fmt.Errorf("workspace symlink %q has an empty target", rel)
	}
	if _, err := pathutil.CollisionKey(target); err != nil {
		return fmt.Errorf("invalid workspace symlink target for %q: %w", rel, err)
	}
	if path.IsAbs(target) {
		return fmt.Errorf("workspace symlink %q targets an absolute path", rel)
	}
	var after unix.Stat_t
	if err := walk.ops.fstatat(sourceParent, name, &after, unix.AT_SYMLINK_NOFOLLOW); err != nil || workspaceSnapshotFromUnix(after) != before {
		return fmt.Errorf("workspace symlink %q changed while copying", rel)
	}
	if err := walk.ops.symlinkat(target, destParent, name); err != nil {
		return fmt.Errorf("create workspace symlink %q: %w", rel, err)
	}
	copiedFD, err := walk.ops.openat(destParent, name, workspaceSymlinkOpenFlags, 0)
	if err != nil {
		return err
	}
	defer unix.Close(copiedFD)
	copiedSnapshot, err := verifyWorkspaceNameAt(destParent, name, copiedFD, walk.ops)
	if err != nil {
		return err
	}
	var stable unix.Stat_t
	if copiedSnapshot.mode&uint32(unix.S_IFMT) != uint32(unix.S_IFLNK) {
		return fmt.Errorf("workspace symlink %q changed after creation", rel)
	}
	copiedTarget, err := readWorkspaceLink(destParent, name, walk.ops)
	if err != nil {
		return err
	}
	if err := walk.ops.fstatat(destParent, name, &stable, unix.AT_SYMLINK_NOFOLLOW); err != nil || copiedTarget != target || workspaceSnapshotFromUnix(stable) != copiedSnapshot {
		return fmt.Errorf("workspace symlink %q changed after creation", rel)
	}
	mode := entryMode(copiedSnapshot, fs.ModeSymlink)
	return walk.retain(WorkspaceEntry{Path: rel, Kind: "symlink", Mode: mode, LinkTarget: target})
}

func (walk *workspaceWalk) sizeError(rel string, size int64) error {
	if size > walk.limits.maxFileBytes {
		return fmt.Errorf("workspace file %q exceeds the per-file limit of %d bytes", rel, walk.limits.maxFileBytes)
	}
	return fmt.Errorf("workspace exceeds the aggregate file-byte limit of %d bytes", walk.limits.maxBytes)
}

func (walk *workspaceWalk) reserve(rel string) error {
	key, err := pathutil.CollisionKey(rel)
	if err != nil {
		return fmt.Errorf("invalid workspace destination path: %w", err)
	}
	if previous, found := walk.reserved[key]; found {
		return fmt.Errorf("workspace destination path collision between %q and %q", previous.Path, rel)
	}
	cost := int64(6*len(rel) + 256)
	if cost > workspaceManifestMaxBytes-walk.manifestBytes {
		return fmt.Errorf("workspace manifest metadata exceeds %d bytes", workspaceManifestMaxBytes)
	}
	walk.manifestBytes += cost
	walk.reserved[key] = WorkspaceEntry{Path: rel}
	return nil
}

func (walk *workspaceWalk) retain(entry WorkspaceEntry) error {
	cost := int64(6 * len(entry.LinkTarget))
	if cost > workspaceManifestMaxBytes-walk.manifestBytes {
		return fmt.Errorf("workspace manifest metadata exceeds %d bytes", workspaceManifestMaxBytes)
	}
	walk.manifestBytes += cost
	key, _ := pathutil.CollisionKey(entry.Path)
	walk.reserved[key] = entry
	walk.entries = append(walk.entries, entry)
	return nil
}

func workspaceDirectoryNames(fd, limit int) (names []string, retErr error) {
	duplicate, err := unix.Openat(fd, ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	directory := os.NewFile(uintptr(duplicate), "workspace destination")
	defer func() { retErr = errors.Join(retErr, directory.Close()) }()
	flags, err := unix.FcntlInt(uintptr(duplicate), unix.F_GETFD, 0)
	if err != nil || flags&unix.FD_CLOEXEC == 0 {
		return nil, fmt.Errorf("workspace directory descriptor is not close-on-exec")
	}
	names, err = directory.Readdirnames(limit)
	if err == io.EOF {
		err = nil
	}
	return names, err
}

func (walk *workspaceWalk) validateDestination(rootFD int) error {
	var root unix.Stat_t
	if err := walk.ops.fstat(rootFD, &root); err != nil {
		return err
	}
	if snapshot := workspaceSnapshotFromUnix(root); snapshot.mode&uint32(unix.S_IFMT) != uint32(unix.S_IFDIR) || snapshot.mode&0o7777 != 0o755 {
		return fmt.Errorf("workspace destination root differs from its required mode")
	}
	if err := walk.validateDestinationDirectory(rootFD, "", 0, workspaceSnapshotFromUnix(root), walk.reserved); err != nil {
		return err
	}
	if len(walk.reserved) != 0 {
		return fmt.Errorf("workspace destination is missing manifest entries")
	}
	return nil
}

func (walk *workspaceWalk) validateDestinationDirectory(fd int, parent string, depth int, before workspaceSnapshot, expected map[string]WorkspaceEntry) error {
	names, err := workspaceDirectoryNames(fd, workspaceMaxEntries+1)
	if err != nil {
		return err
	}
	for _, name := range names {
		if err := validateWorkspaceLeaf("workspace destination entry", name); err != nil {
			return err
		}
		rel := path.Join(parent, name)
		if err := validateWorkspacePath(rel, depth+1); err != nil {
			return err
		}
		key, err := pathutil.CollisionKey(rel)
		if err != nil {
			return err
		}
		entry, found := expected[key]
		if !found || entry.Path != rel {
			return fmt.Errorf("workspace destination has an unmanifested entry")
		}
		var stat unix.Stat_t
		if err := walk.ops.fstatat(fd, name, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			return err
		}
		snapshot := workspaceSnapshotFromUnix(stat)
		switch entry.Kind {
		case "dir":
			if snapshot.mode&uint32(unix.S_IFMT) != uint32(unix.S_IFDIR) || snapshot.mode&0o7777 != uint32(entry.Mode.Perm()) {
				return fmt.Errorf("workspace destination directory differs from its manifest")
			}
			child, err := walk.ops.openat(fd, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
			if err != nil {
				return err
			}
			opened, openErr := verifyWorkspaceNameAt(fd, name, child, walk.ops)
			if openErr == nil && opened == snapshot {
				openErr = walk.validateDestinationDirectory(child, rel, depth+1, snapshot, expected)
			}
			stable, stableErr := verifyWorkspaceNameAt(fd, name, child, walk.ops)
			err = errors.Join(openErr, stableErr, unix.Close(child))
			if err != nil || stable != snapshot {
				return fmt.Errorf("workspace destination directory changed during validation")
			}
		case "file":
			err = walk.validateDestinationFile(fd, name, entry, snapshot)
		case "symlink":
			err = walk.validateDestinationSymlink(fd, name, entry, snapshot)
		default:
			err = fmt.Errorf("workspace destination manifest has an invalid kind")
		}
		if err != nil {
			return err
		}
		delete(expected, key)
	}
	var after unix.Stat_t
	if err := walk.ops.fstat(fd, &after); err != nil || workspaceSnapshotFromUnix(after) != before {
		return fmt.Errorf("workspace destination directory changed during validation")
	}
	return nil
}

func (walk *workspaceWalk) validateDestinationFile(parentFD int, name string, entry WorkspaceEntry, before workspaceSnapshot) (retErr error) {
	size, found := walk.fileSizes[entry.Path]
	if !found || before.mode&uint32(unix.S_IFMT) != uint32(unix.S_IFREG) || before.nlink != 1 || before.size != size || before.mode&0o7777 != uint32(entry.Mode.Perm()) {
		return fmt.Errorf("workspace destination file differs from its manifest")
	}
	fd, err := walk.ops.openat(parentFD, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(fd), entry.Path)
	defer func() { retErr = errors.Join(retErr, file.Close()) }()
	var opened unix.Stat_t
	if err := walk.ops.fstat(fd, &opened); err != nil || workspaceSnapshotFromUnix(opened) != before {
		return fmt.Errorf("workspace destination file changed before validation")
	}
	hash := sha256.New()
	read, err := io.Copy(hash, io.LimitReader(file, size+1))
	if err != nil || read != size || hex.EncodeToString(hash.Sum(nil)) != entry.SHA256 {
		return fmt.Errorf("workspace destination file content differs from its manifest")
	}
	stable, err := verifyWorkspaceNameAt(parentFD, name, fd, walk.ops)
	if err != nil || stable != before {
		return fmt.Errorf("workspace destination file changed during validation")
	}
	return nil
}

func (walk *workspaceWalk) validateDestinationSymlink(parentFD int, name string, entry WorkspaceEntry, before workspaceSnapshot) error {
	if before.mode&uint32(unix.S_IFMT) != uint32(unix.S_IFLNK) || before.nlink != 1 || before.mode&0o7777 != uint32(entry.Mode.Perm()) {
		return fmt.Errorf("workspace destination symlink differs from its manifest")
	}
	fd, err := walk.ops.openat(parentFD, name, workspaceSymlinkOpenFlags, 0)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	if opened, err := verifyWorkspaceNameAt(parentFD, name, fd, walk.ops); err != nil || opened != before {
		return fmt.Errorf("workspace destination symlink changed before validation")
	}
	target, err := readWorkspaceLink(parentFD, name, walk.ops)
	if err != nil {
		return err
	}
	var after unix.Stat_t
	if err := walk.ops.fstatat(parentFD, name, &after, unix.AT_SYMLINK_NOFOLLOW); err != nil || workspaceSnapshotFromUnix(after) != before || target != entry.LinkTarget {
		return fmt.Errorf("workspace destination symlink changed during validation")
	}
	return nil
}

func validateWorkspaceLinks(entries []WorkspaceEntry) error {
	index := make(map[string]WorkspaceEntry, len(entries))
	for _, entry := range entries {
		key, err := pathutil.CollisionKey(entry.Path)
		if err != nil {
			return err
		}
		index[key] = entry
	}
	for _, entry := range entries {
		if entry.Kind == "symlink" {
			if err := resolveWorkspaceLink(entry, index); err != nil {
				return err
			}
		}
	}
	return nil
}

func resolveWorkspaceLink(link WorkspaceEntry, index map[string]WorkspaceEntry) error {
	resolved := []string(nil)
	if parent := path.Dir(link.Path); parent != "." {
		resolved = strings.Split(parent, "/")
	}
	pending := strings.Split(link.LinkTarget, "/")
	seen := make(map[string]struct{})
	for hops := 1; len(pending) > 0; {
		component := pending[0]
		pending = pending[1:]
		switch component {
		case "", ".":
			continue
		case "..":
			if len(resolved) == 0 {
				return fmt.Errorf("workspace symlink %q escapes the workspace", link.Path)
			}
			resolved = resolved[:len(resolved)-1]
			continue
		}
		resolved = append(resolved, component)
		current := strings.Join(resolved, "/")
		key, err := pathutil.CollisionKey(current)
		if err != nil {
			return err
		}
		entry, found := index[key]
		if !found || entry.Path != current {
			return fmt.Errorf("workspace symlink %q is dangling", link.Path)
		}
		if entry.Kind != "symlink" {
			if len(pending) > 0 && entry.Kind != "dir" {
				return fmt.Errorf("workspace symlink %q traverses non-directory %q", link.Path, entry.Path)
			}
			continue
		}
		if hops == workspaceMaxSymlinkHops {
			return fmt.Errorf("workspace symlink %q exceeds %d link hops", link.Path, workspaceMaxSymlinkHops)
		}
		state := key + "\x00" + strings.Join(pending, "/")
		if _, found := seen[state]; found {
			return fmt.Errorf("workspace symlink %q forms a cycle", link.Path)
		}
		seen[state] = struct{}{}
		resolved = resolved[:len(resolved)-1]
		pending = append(strings.Split(entry.LinkTarget, "/"), pending...)
		hops++
	}
	return nil
}

func entryMode(s workspaceSnapshot, k fs.FileMode) fs.FileMode { return fs.FileMode(s.mode&0o777) | k }

func validateWorkspaceLeaf(label, value string) error {
	if value == "" || value == "." || value == ".." || filepath.Base(value) != value || strings.ContainsRune(value, 0) {
		return fmt.Errorf("%s must be a safe path segment", label)
	}
	if _, err := pathutil.CollisionKey(value); err != nil {
		return fmt.Errorf("invalid %s: %w", label, err)
	}
	return nil
}

func validateWorkspacePath(rel string, components int) error {
	if len(rel) > workspaceMaxPathBytes {
		return fmt.Errorf("workspace path exceeds %d bytes", workspaceMaxPathBytes)
	}
	if components > workspaceMaxDepth {
		return fmt.Errorf("workspace exceeds the maximum depth of %d", workspaceMaxDepth)
	}
	return nil
}

func isExcludedPathSafe(rel string, excluded []string) (bool, error) {
	if _, err := pathutil.CollisionKey(rel); err != nil {
		return false, err
	}
	for _, item := range excluded {
		inside, err := pathutil.WithinOrEqual(item, rel, true)
		if err != nil {
			return false, err
		}
		if inside {
			return true, nil
		}
	}
	return false, nil
}

func readWorkspaceLink(parentFD int, name string, ops workspaceOps) (string, error) {
	buffer := make([]byte, workspaceMaxLinkBytes+1)
	n, err := ops.readlink(parentFD, name, buffer)
	if err != nil {
		return "", err
	}
	if n > workspaceMaxLinkBytes {
		return "", fmt.Errorf("link target exceeds %d bytes", workspaceMaxLinkBytes)
	}
	return string(buffer[:n]), nil
}

func openWorkspaceSourceRoot(sourceRoot string, ops workspaceOps) (int, workspaceSnapshot, error) {
	if _, err := pathutil.CollisionKey(sourceRoot); err != nil {
		return -1, workspaceSnapshot{}, err
	}
	resolved, err := filepath.EvalSymlinks(sourceRoot)
	if err != nil {
		return -1, workspaceSnapshot{}, err
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return -1, workspaceSnapshot{}, err
	}
	var before unix.Stat_t
	if err := unix.Stat(resolved, &before); err != nil {
		return -1, workspaceSnapshot{}, err
	}
	beforeSnapshot := workspaceSnapshotFromUnix(before)
	if beforeSnapshot.mode&uint32(unix.S_IFMT) != uint32(unix.S_IFDIR) {
		return -1, workspaceSnapshot{}, fmt.Errorf("source workspace is not a directory: %s", sourceRoot)
	}
	fd, err := openAbsoluteWorkspaceDir(resolved, ops)
	if err != nil {
		return -1, workspaceSnapshot{}, fmt.Errorf("open source workspace %q: %w", sourceRoot, err)
	}
	var opened unix.Stat_t
	if err := ops.fstat(fd, &opened); err != nil || workspaceSnapshotFromUnix(opened) != beforeSnapshot {
		return -1, workspaceSnapshot{}, closeFDError(fd, "source workspace changed while opening")
	}
	return fd, beforeSnapshot, nil
}

func openAbsoluteWorkspaceDir(directory string, ops workspaceOps) (int, error) {
	clean := filepath.Clean(directory)
	if !filepath.IsAbs(clean) {
		return -1, fmt.Errorf("directory is not absolute: %s", directory)
	}
	fd, err := unix.Open(string(filepath.Separator), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, err
	}
	if clean == string(filepath.Separator) {
		return fd, nil
	}
	for _, component := range strings.Split(strings.TrimPrefix(clean, string(filepath.Separator)), string(filepath.Separator)) {
		next, err := ops.openat(fd, component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		unix.Close(fd)
		if err != nil {
			return -1, err
		}
		fd = next
	}
	return fd, nil
}

func requireWorkspaceNameAbsent(parentFD int, name string, ops workspaceOps) error {
	var stat unix.Stat_t
	err := ops.fstatat(parentFD, name, &stat, unix.AT_SYMLINK_NOFOLLOW)
	if err == nil {
		return fmt.Errorf("workspace destination %q already exists", name)
	}
	if !errors.Is(err, unix.ENOENT) {
		return fmt.Errorf("inspect workspace destination %q: %w", name, err)
	}
	return nil
}

func verifyWorkspaceNameAt(parentFD int, name string, fd int, ops workspaceOps) (workspaceSnapshot, error) {
	var opened, named unix.Stat_t
	if err := ops.fstat(fd, &opened); err != nil {
		return workspaceSnapshot{}, err
	}
	snapshot := workspaceSnapshotFromUnix(opened)
	if err := ops.fstatat(parentFD, name, &named, unix.AT_SYMLINK_NOFOLLOW); err != nil || workspaceSnapshotFromUnix(named) != snapshot {
		return workspaceSnapshot{}, fmt.Errorf("workspace destination %q changed", name)
	}
	return snapshot, nil
}

func createWorkspaceDirectory(parentFD int, name string, ops workspaceOps) (int, error) {
	if err := ops.mkdirat(parentFD, name, 0o700); err != nil {
		return -1, err
	}
	var before unix.Stat_t
	if err := ops.fstatat(parentFD, name, &before, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return -1, err
	}
	expected := workspaceSnapshotFromUnix(before)
	fd, err := ops.openat(parentFD, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, err
	}
	opened, err := verifyWorkspaceNameAt(parentFD, name, fd, ops)
	if err != nil || opened != expected || expected.mode&uint32(unix.S_IFMT) != uint32(unix.S_IFDIR) {
		return -1, closeFDError(fd, "workspace destination directory changed while opening")
	}
	names, err := workspaceDirectoryNames(fd, 1)
	if err != nil || len(names) != 0 {
		return -1, closeFDError(fd, "workspace destination directory was not empty after creation")
	}
	return fd, nil
}
