// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package rootio

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// errStagedSiblingLeft marks a publication that succeeded while the staged
// sibling could not be removed. The caller must not unlink again: the name is
// already published, and the removal is the step that just failed.
var errStagedSiblingLeft = errors.New("the staged sibling was left in place")

// RequireSingleLinkedRegular rejects a leaf that is not a single-linked regular
// file.
//
// A symlink, a FIFO, a socket and a device node all pass a bare existence
// check, and a hard link to a file outside the trusted root passes a file-type
// check as well: the link count is the only thing that separates the intended
// inode from an attacker's copy of it. The caller supplies a stat taken
// without following symlinks, which is the only stat this rule is sound over.
func RequireSingleLinkedRegular(info *unix.Stat_t, label, name string) error {
	if info == nil {
		return fmt.Errorf("%s %q has no stat to check", label, name)
	}
	if info.Mode&unix.S_IFMT != unix.S_IFREG {
		return fmt.Errorf("%s %q is not a regular file (mode %#o); a symlink, FIFO, socket or device is rejected",
			label, name, info.Mode&unix.S_IFMT)
	}
	if info.Nlink != 1 {
		return fmt.Errorf("%s %q is multiply linked (%d links); a hard link to a file outside the trusted root is rejected",
			label, name, info.Nlink)
	}
	return nil
}

// MkdirAllSyncedAt creates relative under parent and makes every new directory
// entry durable.
//
// It differs from os.MkdirAll in two ways the review corpus demands. Each
// component is opened relative to the previous descriptor with O_NOFOLLOW, so
// a symlink swapped into the middle of the path is refused rather than
// followed. And the directory that gains each entry is fsynced, not only the
// parent of the last one, so a crash cannot lose an ancestor that the call
// reported as created.
//
// The fsync runs whether this call created the component or found it present.
// A racing creator can return from its own mkdir before its fsync completes,
// or fail that fsync, so the loser of the race cannot treat an existing
// directory as already durable.
func MkdirAllSyncedAt(parent *os.File, relative string, mode os.FileMode) error {
	components, err := relativeComponents(relative)
	if err != nil {
		return err
	}
	// F_DUPFD_CLOEXEC rather than dup: dup clears FD_CLOEXEC, so a child
	// started while this descriptor is open would inherit a handle on a
	// trusted directory.
	current, err := unix.FcntlInt(parent.Fd(), unix.F_DUPFD_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(current) }()

	for _, component := range components {
		if err := unix.Mkdirat(current, component, uint32(mode.Perm())); err != nil && !errors.Is(err, unix.EEXIST) {
			return fmt.Errorf("create %s under %s: %w", component, parent.Name(), err)
		}
		next, err := unix.Openat(current, component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			return fmt.Errorf("open %s under %s: %w", component, parent.Name(), err)
		}
		if err := unix.Fsync(current); err != nil {
			_ = unix.Close(next)
			return fmt.Errorf("sync the directory that gained %s: %w", component, err)
		}
		_ = unix.Close(current)
		current = next
	}
	return nil
}

// StageAndPublishAt writes data to name under parent, replacing any file
// already there.
//
// StageAndCreateAt publishes the same way but refuses to replace an existing
// name.
func StageAndPublishAt(parent *os.File, name string, data []byte, mode os.FileMode, tempPrefix string) error {
	return stageAndPublish(parent, name, data, mode, tempPrefix, false)
}

// StageAndCreateAt writes data to name under parent and fails when name
// already exists.
//
// The kernel refuses the existing name in the same step that publishes, so two
// callers that both find the name absent cannot both report success. A plain
// renameat would replace a name that appeared after the absence check.
func StageAndCreateAt(parent *os.File, name string, data []byte, mode os.FileMode, tempPrefix string) error {
	return stageAndPublish(parent, name, data, mode, tempPrefix, true)
}

// stageAndPublish writes a uniquely named sibling with O_EXCL, sets its mode,
// syncs its contents, publishes it under name, and syncs the parent directory.
//
// Every step answers a defect the reviewer found. The temporary name is random
// so a crash-left file or one an attacker pre-created cannot block the write,
// and an O_EXCL collision retries with a fresh name. The mode is set through
// the descriptor so a restrictive umask cannot publish an unreadable file. The
// contents are synced before publication and the parent is synced after it, so
// a crash cannot leave a name that points at unwritten bytes.
func stageAndPublish(parent *os.File, name string, data []byte, mode os.FileMode, tempPrefix string, createOnce bool) error {
	if err := validateLeafName(name); err != nil {
		return err
	}
	if tempPrefix == "" {
		tempPrefix = ".workcell-tmp-"
	}
	if strings.Contains(tempPrefix, string(filepath.Separator)) {
		return fmt.Errorf("temporary-file prefix must not contain a path separator: %s", tempPrefix)
	}
	parentFD := int(parent.Fd())
	for attempt := 0; attempt < 32; attempt++ {
		suffix, err := randomSuffix()
		if err != nil {
			return err
		}
		temporary := tempPrefix + suffix + ".tmp"
		fd, err := unix.Openat(parentFD, temporary,
			unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, uint32(mode.Perm()))
		if errors.Is(err, unix.EEXIST) {
			continue
		}
		if err != nil {
			return err
		}
		if err := writeStagedFile(parentFD, fd, temporary, filepath.Join(parent.Name(), temporary), data, mode); err != nil {
			return err
		}
		if err := publishStagedFile(parentFD, temporary, name, createOnce); err != nil {
			// The staged sibling is removed only when nothing was published;
			// linkStagedFile reports its own removal failure after the name
			// exists, and unlinking again there would be a second attempt at
			// the step that just failed.
			if !errors.Is(err, errStagedSiblingLeft) {
				_ = unix.Unlinkat(parentFD, temporary, 0)
			}
			return err
		}
		// The published entry is only durable once the directory that gained
		// it is synced. A crash before this returns can otherwise lose the
		// name while the caller has already been told the write succeeded.
		if err := unix.Fsync(parentFD); err != nil {
			return fmt.Errorf("sync the directory that gained %s: %w", name, err)
		}
		return nil
	}
	return fmt.Errorf("unable to allocate temporary file under %s", parent.Name())
}

// writeStagedFile fills, chmods, syncs and closes the staged descriptor,
// unlinking it on any failure so a partial file is never left behind.
func writeStagedFile(parentFD, fd int, temporary, path string, data []byte, mode os.FileMode) error {
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = unix.Close(fd)
		_ = unix.Unlinkat(parentFD, temporary, 0)
		return fmt.Errorf("create temporary file %s", temporary)
	}
	fail := func(err error) error {
		_ = file.Close()
		_ = unix.Unlinkat(parentFD, temporary, 0)
		return err
	}
	if _, err := io.Copy(file, bytes.NewReader(data)); err != nil {
		return fail(err)
	}
	// O_CREAT applies the umask, so a restrictive one would publish a file the
	// owner cannot read. Set the mode through the descriptor instead.
	if err := unix.Fchmod(fd, uint32(mode.Perm())); err != nil {
		return fail(err)
	}
	if err := file.Sync(); err != nil {
		return fail(err)
	}
	if err := file.Close(); err != nil {
		_ = unix.Unlinkat(parentFD, temporary, 0)
		return err
	}
	return nil
}

// publishStagedFile moves the staged name onto its final name.
//
// The create-once form asks the kernel to refuse an existing name in the same
// step that publishes, so no window exists in which both names are links to
// one inode. Darwin spells that renameatx_np with RENAME_EXCL and Linux spells
// it renameat2 with RENAME_NOREPLACE.
func publishStagedFile(parentFD int, temporary, name string, createOnce bool) error {
	if !createOnce {
		return unix.Renameat(parentFD, temporary, parentFD, name)
	}
	switch err := renameNoReplaceAt(parentFD, temporary, name); {
	case err == nil:
		return nil
	case errors.Is(err, unix.EEXIST):
		return fmt.Errorf("%s already exists", name)
	case errors.Is(err, unix.ENOTSUP), errors.Is(err, unix.ENOSYS), errors.Is(err, unix.EINVAL):
		return linkStagedFile(parentFD, temporary, name)
	default:
		return err
	}
}

// linkStagedFile is the create-once publication for a filesystem whose kernel
// refuses the one-step form. link refuses an existing name, so create-once
// still holds.
//
// ponytail: link leaves the staged name as a second link to the inode until
// the unlink returns, and a concurrent RequireSingleLinkedRegular during that
// window refuses a file that is in fact intact. The one-step form above has no
// such window, so this path runs only where the kernel or the filesystem
// rejects it, such as an older Darwin volume. Delete this fallback when the
// supported filesystems all carry the one-step form.
func linkStagedFile(parentFD int, temporary, name string) error {
	if err := unix.Linkat(parentFD, temporary, parentFD, name, 0); err != nil {
		if errors.Is(err, unix.EEXIST) {
			return fmt.Errorf("%s already exists", name)
		}
		return err
	}
	// The published name exists from here on, so a failure to remove the
	// staged sibling is reported without unpublishing it, and the caller still
	// syncs the parent.
	if err := unix.Unlinkat(parentFD, temporary, 0); err != nil {
		return fmt.Errorf("%w: remove the staged sibling of %s: %w", errStagedSiblingLeft, name, err)
	}
	return nil
}

// relativeComponents splits a relative path into the names to create, and
// refuses any spelling that would leave the opened parent.
func relativeComponents(relative string) ([]string, error) {
	if relative == "" || filepath.IsAbs(relative) {
		return nil, fmt.Errorf("path must be relative to the opened parent: %q", relative)
	}
	var components []string
	for _, component := range strings.Split(filepath.Clean(relative), string(filepath.Separator)) {
		if component == "" || component == "." {
			continue
		}
		if component == ".." {
			return nil, fmt.Errorf("path must stay within the opened parent: %q", relative)
		}
		components = append(components, component)
	}
	if len(components) == 0 {
		return nil, fmt.Errorf("path names no directory to create: %q", relative)
	}
	return components, nil
}
