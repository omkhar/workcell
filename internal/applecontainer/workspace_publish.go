// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package applecontainer

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/omkhar/workcell/internal/rootio"
	"golang.org/x/sys/unix"
)

const workspaceStageAttempts, workspaceStagingName = 16, ".materialization-staging"

var errWorkspaceMaterializationUnsupported = errors.New("secure workspace materialization is unsupported on this platform")

type workspaceMaterializeOps struct {
	workspace workspaceOps
	random    func([]byte) (int, error)
	write     func(int, []byte) (int, error)
	fsync     func(int) error
	publish   func(int, string, int, string) error
	manifest  int64
}

func systemWorkspaceMaterializeOps() workspaceMaterializeOps {
	return workspaceMaterializeOps{systemWorkspaceOps(), rand.Read, unix.Write, unix.Fsync, workspacePublish, workspaceManifestMaxBytes}
}

// publishWorkspaceMaterialization creates one materialization without replacement.
// StateRoot must be a pre-existing, trusted, caller-owned directory.
func publishWorkspaceMaterialization(stateRoot string, targetChain []string, finalName, workspaceName, manifestName, sourceRoot string, excluded []string, manifest WorkspaceManifest, ops workspaceMaterializeOps) (_ WorkspaceManifest, retErr error) {
	if !workspaceMaterializationSupported || !workspaceCopySupported {
		return WorkspaceManifest{}, errWorkspaceMaterializationUnsupported
	}
	if len(targetChain) == 0 || ops.manifest <= 0 {
		return WorkspaceManifest{}, fmt.Errorf("workspace materialization configuration is invalid")
	}
	for label, value := range map[string]string{"materialization id": finalName, "workspace directory": workspaceName, "manifest name": manifestName} {
		if err := validateWorkspaceLeaf(label, value); err != nil {
			return WorkspaceManifest{}, err
		}
	}
	for _, name := range targetChain {
		if err := validateWorkspaceLeaf("managed directory", name); err != nil {
			return WorkspaceManifest{}, err
		}
	}
	stateFD, stateSnapshot, err := openPinnedWorkspaceDirectory("state root", stateRoot, false, ops.workspace)
	if err != nil {
		return WorkspaceManifest{}, err
	}
	defer unix.Close(stateFD)
	if stateSnapshot.uid != uint32(os.Geteuid()) || stateSnapshot.mode&0o022 != 0 || stateSnapshot.mode&0o7000 != 0 {
		return WorkspaceManifest{}, fmt.Errorf("state root must be caller-owned and must not be group-writable or world-writable")
	}
	sourceFD, sourceSnapshot, err := openWorkspaceSourceRoot(sourceRoot, ops.workspace)
	if err != nil {
		return WorkspaceManifest{}, err
	}
	defer unix.Close(sourceFD)
	if err := rejectWorkspaceDescriptorOverlap(stateFD, stateSnapshot, sourceFD, sourceSnapshot, ops.workspace); err != nil {
		return WorkspaceManifest{}, err
	}
	targetFD, targetID, err := openManagedWorkspaceChain(stateFD, targetChain, true, ops.workspace)
	if err != nil {
		return WorkspaceManifest{}, err
	}
	defer unix.Close(targetFD)
	finalParentFD, finalParentID, err := openManagedWorkspaceDirectory(targetFD, "materializations", 0o700, false, true, ops.workspace)
	if err != nil {
		return WorkspaceManifest{}, err
	}
	defer unix.Close(finalParentFD)
	if err := requireWorkspaceNameAbsent(finalParentFD, finalName, ops.workspace); err != nil {
		return WorkspaceManifest{}, err
	}
	stagingParentFD, stagingParentID, err := openManagedWorkspaceDirectory(targetFD, workspaceStagingName, 0o700, true, true, ops.workspace)
	if err != nil {
		return WorkspaceManifest{}, err
	}
	defer unix.Close(stagingParentFD)
	stageName, stageFD, err := createPrivateWorkspaceStage(stagingParentFD, ops)
	if err != nil {
		if stageName != "" {
			return WorkspaceManifest{}, fmt.Errorf("materialization failed after private stage %q was created; no pathname cleanup was attempted: %w", stageName, err)
		}
		return WorkspaceManifest{}, err
	}
	published := false
	defer func() {
		if retErr != nil && !published {
			modeErr := ops.workspace.fchmod(stageFD, 0o700)
			bound, bindErr := workspaceNameBindsFD(stagingParentFD, stageName, stageFD, ops.workspace)
			if modeErr == nil && bindErr == nil && bound {
				retErr = fmt.Errorf("materialization failed; private stage %q was retained without pathname cleanup: %w", stageName, retErr)
			} else if modeErr == nil && bindErr == nil {
				retErr = fmt.Errorf("materialization publication outcome is ambiguous; the owned object was returned to mode 0700, but private stage %q no longer binds it; no pathname cleanup was attempted: %w", stageName, retErr)
			} else {
				retErr = fmt.Errorf("materialization failed after stage creation; no pathname cleanup was attempted and stage retention could not be confirmed: %w", errors.Join(retErr, modeErr, bindErr))
			}
		}
		_ = unix.Close(stageFD)
	}()
	copyFD, err := ops.workspace.openat(sourceFD, ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return WorkspaceManifest{}, err
	}
	var copyStat unix.Stat_t
	if err := ops.workspace.fstat(copyFD, &copyStat); err != nil || workspaceSnapshotFromUnix(copyStat) != sourceSnapshot {
		return WorkspaceManifest{}, closeFDError(copyFD, "source workspace changed before copying")
	}
	entries, err := copyWorkspaceTreeFromDescriptor(copyFD, sourceSnapshot, stageFD, workspaceName, excluded, defaultWorkspaceCopyLimits(), ops.workspace)
	if err != nil {
		return WorkspaceManifest{}, err
	}
	manifest.Entries = entries
	want, err := marshalManifestBytes(manifest)
	if err != nil {
		return WorkspaceManifest{}, err
	}
	if int64(len(want)) > ops.manifest {
		return WorkspaceManifest{}, fmt.Errorf("workspace manifest exceeds %d bytes", ops.manifest)
	}
	if err := writeWorkspaceManifestAt(stageFD, manifestName, want, ops); err != nil {
		return WorkspaceManifest{}, err
	}
	if err := validateWorkspaceManifestAt(stageFD, manifestName, want, ops); err != nil {
		return WorkspaceManifest{}, err
	}
	if err := requireWorkspaceStageEntries(stageFD, workspaceName, manifestName); err != nil {
		return WorkspaceManifest{}, err
	}
	if err := ops.workspace.fchmod(stageFD, 0o755); err != nil {
		return WorkspaceManifest{}, fmt.Errorf("set final materialization mode: %w", err)
	}
	if err := ops.fsync(stageFD); err != nil {
		return WorkspaceManifest{}, fmt.Errorf("sync private workspace stage: %w", err)
	}
	if err := ops.fsync(stagingParentFD); err != nil {
		return WorkspaceManifest{}, fmt.Errorf("sync staging parent: %w", err)
	}
	if err := ops.fsync(finalParentFD); err != nil {
		return WorkspaceManifest{}, fmt.Errorf("sync materializations parent: %w", err)
	}
	if err := verifyWorkspaceSource(sourceFD, sourceSnapshot, stateFD, stateSnapshot, ops.workspace); err != nil {
		return WorkspaceManifest{}, err
	}
	if err := verifyWorkspacePublishAnchors(stateRoot, stateSnapshot, stateFD, targetChain, targetID, targetFD, finalParentID, finalParentFD, stagingParentID, stagingParentFD, ops.workspace); err != nil {
		return WorkspaceManifest{}, err
	}
	if err := requireWorkspaceNameAbsent(finalParentFD, finalName, ops.workspace); err != nil {
		return WorkspaceManifest{}, err
	}
	stageSnapshot, err := verifyWorkspaceNameAt(stagingParentFD, stageName, stageFD, ops.workspace)
	if err != nil || stageSnapshot.mode&0o7777 != 0o755 {
		return WorkspaceManifest{}, fmt.Errorf("private workspace stage changed before publication")
	}
	publishErr := ops.publish(stagingParentFD, stageName, finalParentFD, finalName)
	stageBound, stageBindErr := workspaceNameBindsFD(stagingParentFD, stageName, stageFD, ops.workspace)
	finalBound, finalBindErr := workspaceNameBindsFD(finalParentFD, finalName, stageFD, ops.workspace)
	if finalBound {
		published = true
		if stageBound {
			return WorkspaceManifest{}, fmt.Errorf("materialization was published, but the private stage name still exists")
		}
	} else if publishErr == nil {
		return WorkspaceManifest{}, fmt.Errorf("publication returned success but final binding is invalid: %w", errors.Join(stageBindErr, finalBindErr))
	} else {
		return WorkspaceManifest{}, fmt.Errorf("publish materialization %q without replacement: %w", finalName, errors.Join(publishErr, stageBindErr, finalBindErr))
	}
	if err := ops.fsync(stagingParentFD); err != nil {
		return WorkspaceManifest{}, fmt.Errorf("materialization was published, but staging parent sync failed: %w", err)
	}
	if err := ops.fsync(finalParentFD); err != nil {
		return WorkspaceManifest{}, fmt.Errorf("materialization was published, but materializations parent sync failed: %w", err)
	}
	if err := verifyWorkspacePublishAnchors(stateRoot, stateSnapshot, stateFD, targetChain, targetID, targetFD, finalParentID, finalParentFD, stagingParentID, stagingParentFD, ops.workspace); err != nil {
		return WorkspaceManifest{}, fmt.Errorf("materialization was published, but its anchor changed: %w", err)
	}
	if bound, err := workspaceNameBindsFD(finalParentFD, finalName, stageFD, ops.workspace); err != nil || !bound {
		return WorkspaceManifest{}, fmt.Errorf("materialization was published, but final binding changed")
	}
	return manifest, nil
}

func rejectWorkspaceDescriptorOverlap(stateFD int, state workspaceSnapshot, sourceFD int, source workspaceSnapshot, ops workspaceOps) error {
	stateID, sourceID := workspaceObjectID{state.dev, state.ino}, workspaceObjectID{source.dev, source.ino}
	stateWithinSource, err := workspaceDescriptorWithin(sourceID, stateFD, ops)
	if err != nil {
		return err
	}
	sourceWithinState, err := workspaceDescriptorWithin(stateID, sourceFD, ops)
	if err != nil {
		return err
	}
	if stateWithinSource || sourceWithinState {
		return fmt.Errorf("state root must not overlap the source workspace")
	}
	return nil
}

func workspaceDescriptorWithin(ancestor workspaceObjectID, childFD int, ops workspaceOps) (bool, error) {
	current, err := ops.openat(childFD, ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return false, err
	}
	defer func() { _ = unix.Close(current) }()
	for hop := 0; hop < workspaceMaxPathBytes; hop++ {
		var currentStat unix.Stat_t
		if err := ops.fstat(current, &currentStat); err != nil {
			return false, err
		}
		currentID := snapshotObjectID(workspaceSnapshotFromUnix(currentStat))
		if currentID == ancestor {
			return true, nil
		}
		parent, err := ops.openat(current, "..", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			return false, err
		}
		var parentStat unix.Stat_t
		if err := ops.fstat(parent, &parentStat); err != nil {
			unix.Close(parent)
			return false, err
		}
		parentID := snapshotObjectID(workspaceSnapshotFromUnix(parentStat))
		if parentID == currentID {
			unix.Close(parent)
			return false, nil
		}
		unix.Close(current)
		current = parent
	}
	return false, fmt.Errorf("filesystem ancestry exceeds %d components", workspaceMaxPathBytes)
}

func openManagedWorkspaceChain(stateFD int, names []string, create bool, ops workspaceOps) (int, workspaceObjectID, error) {
	current, err := ops.openat(stateFD, ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, workspaceObjectID{}, err
	}
	var id workspaceObjectID
	for _, name := range names {
		next, nextID, err := openManagedWorkspaceDirectory(current, name, 0o700, false, create, ops)
		unix.Close(current)
		if err != nil {
			return -1, workspaceObjectID{}, err
		}
		current, id = next, nextID
	}
	return current, id, nil
}

func openManagedWorkspaceDirectory(parentFD int, name string, createMode uint32, requirePrivate, create bool, ops workspaceOps) (int, workspaceObjectID, error) {
	created := false
	if create {
		if err := ops.mkdirat(parentFD, name, createMode); err == nil {
			created = true
		} else if !errors.Is(err, unix.EEXIST) {
			return -1, workspaceObjectID{}, fmt.Errorf("create managed directory %q: %w", name, err)
		}
	}
	var before unix.Stat_t
	if err := ops.fstatat(parentFD, name, &before, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return -1, workspaceObjectID{}, fmt.Errorf("inspect managed directory %q: %w", name, err)
	}
	want := workspaceSnapshotFromUnix(before)
	if want.mode&uint32(unix.S_IFMT) != uint32(unix.S_IFDIR) || want.uid != uint32(os.Geteuid()) || want.mode&0o022 != 0 || want.mode&0o7000 != 0 {
		return -1, workspaceObjectID{}, fmt.Errorf("managed directory %q is not a secure caller-owned directory", name)
	}
	fd, err := ops.openat(parentFD, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, workspaceObjectID{}, fmt.Errorf("open managed directory %q: %w", name, err)
	}
	opened, err := verifyWorkspaceDirectoryNameAt(parentFD, name, fd, ops)
	if err != nil || snapshotObjectID(opened) != snapshotObjectID(want) {
		return -1, workspaceObjectID{}, closeFDError(fd, "managed directory %q changed while opening", name)
	}
	if created {
		if err := ops.fchmod(fd, createMode); err != nil {
			return -1, workspaceObjectID{}, closeFDError(fd, "set managed directory %q mode: %w", name, err)
		}
		opened, err = verifyWorkspaceDirectoryNameAt(parentFD, name, fd, ops)
		if err != nil || opened.mode&0o7777 != createMode {
			return -1, workspaceObjectID{}, closeFDError(fd, "managed directory %q changed after mode update", name)
		}
	}
	if requirePrivate && opened.mode&0o7777 != 0o700 {
		return -1, workspaceObjectID{}, closeFDError(fd, "managed directory %q must have mode 0700", name)
	}
	return fd, snapshotObjectID(opened), nil
}

func createPrivateWorkspaceStage(parentFD int, ops workspaceMaterializeOps) (string, int, error) {
	for attempt := 0; attempt < workspaceStageAttempts; attempt++ {
		var token [16]byte
		n, err := ops.random(token[:])
		if err != nil || n != len(token) {
			return "", -1, errors.Join(err, io.ErrUnexpectedEOF)
		}
		name := ".workcell-stage-" + hex.EncodeToString(token[:])
		if err := ops.workspace.mkdirat(parentFD, name, 0o700); errors.Is(err, unix.EEXIST) {
			continue
		} else if err != nil {
			return "", -1, fmt.Errorf("create private workspace stage: %w", err)
		}
		fd, err := openCreatedWorkspaceDirectory(parentFD, name, ops.workspace)
		if err != nil {
			return name, -1, err
		}
		if err := ops.workspace.fchmod(fd, 0o700); err != nil {
			return name, -1, closeFDError(fd, "set private workspace stage mode: %w", err)
		}
		snapshot, err := verifyWorkspaceNameAt(parentFD, name, fd, ops.workspace)
		if err != nil || snapshot.mode&0o7777 != 0o700 {
			return name, -1, closeFDError(fd, "private workspace stage changed after mode update")
		}
		return name, fd, nil
	}
	return "", -1, fmt.Errorf("could not allocate a private workspace stage")
}

func marshalManifestBytes(value any) ([]byte, error) {
	switch value.(type) {
	case WorkspaceManifest, *WorkspaceManifest:
		return rootio.MarshalCompactJSON(value, "workspace manifest", workspaceManifestMaxBytes)
	}
	content, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(content, '\n'), nil
}

func writeWorkspaceManifestAt(stageFD int, name string, content []byte, ops workspaceMaterializeOps) error {
	fd, err := ops.workspace.openat(stageFD, name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return fmt.Errorf("create workspace manifest: %w", err)
	}
	defer unix.Close(fd)
	if err := ops.workspace.fchmod(fd, 0o600); err != nil {
		return err
	}
	if n, err := ops.write(fd, content); err != nil {
		return err
	} else if n != len(content) {
		return io.ErrShortWrite
	}
	if err := ops.fsync(fd); err != nil {
		return err
	}
	snapshot, err := verifyWorkspaceNameAt(stageFD, name, fd, ops.workspace)
	if err != nil || snapshot.mode&uint32(unix.S_IFMT) != uint32(unix.S_IFREG) || snapshot.mode&0o7777 != 0o600 || snapshot.uid != uint32(os.Geteuid()) || snapshot.nlink != 1 || snapshot.size != int64(len(content)) {
		return fmt.Errorf("workspace manifest changed while writing")
	}
	return nil
}

func validateWorkspaceManifestAt(stageFD int, name string, want []byte, ops workspaceMaterializeOps) error {
	fd, err := ops.workspace.openat(stageFD, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("open workspace manifest: %w", err)
	}
	handle := os.NewFile(uintptr(fd), name)
	defer handle.Close()
	var before unix.Stat_t
	if err := ops.workspace.fstat(fd, &before); err != nil {
		return err
	}
	snapshot := workspaceSnapshotFromUnix(before)
	if snapshot.mode&uint32(unix.S_IFMT) != uint32(unix.S_IFREG) || snapshot.mode&0o7777 != 0o600 || snapshot.uid != uint32(os.Geteuid()) || snapshot.nlink != 1 || snapshot.size < 0 || snapshot.size > ops.manifest {
		return fmt.Errorf("workspace manifest is not a bounded private regular file")
	}
	content, err := io.ReadAll(io.LimitReader(handle, ops.manifest+1))
	if err != nil {
		return fmt.Errorf("read bounded workspace manifest: %w", err)
	}
	if int64(len(content)) > ops.manifest {
		return fmt.Errorf("workspace manifest exceeds %d bytes", ops.manifest)
	}
	stable, err := verifyWorkspaceNameAt(stageFD, name, fd, ops.workspace)
	if err != nil || stable != snapshot || !bytes.Equal(content, want) {
		return fmt.Errorf("workspace manifest changed during validation")
	}
	return nil
}

func requireWorkspaceStageEntries(stageFD int, workspaceName, manifestName string) error {
	names, err := workspaceDirectoryNames(stageFD, 3)
	if err != nil {
		return err
	}
	if workspaceName == manifestName || len(names) != 2 || names[0] != workspaceName && names[1] != workspaceName || names[0] != manifestName && names[1] != manifestName {
		return fmt.Errorf("private workspace stage has unexpected entries")
	}
	return nil
}

func workspaceNameBindsFD(parentFD int, name string, fd int, ops workspaceOps) (bool, error) {
	var opened, named unix.Stat_t
	if err := ops.fstat(fd, &opened); err != nil {
		return false, err
	}
	err := ops.fstatat(parentFD, name, &named, unix.AT_SYMLINK_NOFOLLOW)
	if errors.Is(err, unix.ENOENT) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return workspaceSnapshotFromUnix(opened) == workspaceSnapshotFromUnix(named), nil
}

func verifyWorkspaceDirectoryNameAt(parentFD int, name string, fd int, ops workspaceOps) (workspaceSnapshot, error) {
	var opened, named unix.Stat_t
	if err := ops.fstat(fd, &opened); err != nil {
		return workspaceSnapshot{}, err
	}
	snapshot := workspaceSnapshotFromUnix(opened)
	if err := ops.fstatat(parentFD, name, &named, unix.AT_SYMLINK_NOFOLLOW); err != nil || snapshotObjectID(workspaceSnapshotFromUnix(named)) != snapshotObjectID(snapshot) {
		return workspaceSnapshot{}, fmt.Errorf("managed directory %q changed", name)
	}
	return snapshot, nil
}

func verifyWorkspaceSource(sourceFD int, source workspaceSnapshot, stateFD int, state workspaceSnapshot, ops workspaceOps) error {
	var current unix.Stat_t
	if err := ops.fstat(sourceFD, &current); err != nil || workspaceSnapshotFromUnix(current) != source {
		return fmt.Errorf("source workspace changed before publication")
	}
	return rejectWorkspaceDescriptorOverlap(stateFD, state, sourceFD, source, ops)
}

func verifyWorkspacePublishAnchors(stateRoot string, state workspaceSnapshot, stateFD int, chain []string, targetID workspaceObjectID, targetFD int, finalParentID workspaceObjectID, finalParentFD int, stagingParentID workspaceObjectID, stagingParentFD int, ops workspaceOps) error {
	var opened, visible unix.Stat_t
	openedErr, visibleErr := ops.fstat(stateFD, &opened), ops.stat(stateRoot, &visible)
	openedState, visibleState, stateID := workspaceSnapshotFromUnix(opened), workspaceSnapshotFromUnix(visible), snapshotObjectID(state)
	if openedErr != nil || visibleErr != nil || snapshotObjectID(openedState) != stateID || snapshotObjectID(visibleState) != stateID || openedState.uid != uint32(os.Geteuid()) || visibleState.uid != uint32(os.Geteuid()) || openedState.mode&0o7022 != 0 || visibleState.mode&0o7022 != 0 {
		return fmt.Errorf("state root identity or security policy changed")
	}
	openedTarget, openedTargetID, err := openManagedWorkspaceChain(stateFD, chain, false, ops)
	if err != nil {
		return err
	}
	defer unix.Close(openedTarget)
	if openedTargetID != targetID {
		return fmt.Errorf("target directory identity changed")
	}
	for _, check := range []struct {
		parent int
		name   string
		fd     int
		id     workspaceObjectID
	}{{targetFD, "materializations", finalParentFD, finalParentID}, {targetFD, workspaceStagingName, stagingParentFD, stagingParentID}} {
		if opened, err := verifyWorkspaceDirectoryNameAt(check.parent, check.name, check.fd, ops); err != nil || snapshotObjectID(opened) != check.id {
			return fmt.Errorf("managed directory %q changed", check.name)
		}
	}
	return nil
}

func snapshotObjectID(s workspaceSnapshot) workspaceObjectID { return workspaceObjectID{s.dev, s.ino} }
