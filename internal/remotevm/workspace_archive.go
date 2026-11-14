// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package remotevm

import (
	"archive/tar"
	"bufio"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/omkhar/workcell/internal/rootio"
)

// The wire format is one header line, the archive bytes, and one ack line:
//
//	host:  workcell-workspace-v1 <materialization-id> <sha256> <size>\n<tar>
//	guest: ok <sha256> <workspace>\n  or  error <quoted reason>\n
//
// The header carries the size, so the host never half-closes the stream. The
// brokered tunnel (SSM or IAP) authenticates both ends. The digest catches a
// truncated or corrupt stream before the guest unpacks a byte.
const archiveMagic = "workcell-workspace-v1"

// archiveEntryOverhead bounds the tar bytes per entry beyond file content: a
// header block, a PAX header for a long name and link target, and padding.
const archiveEntryOverhead = 16 << 10

// writeWorkspaceArchive writes a deterministic tar of the staged workspace at
// root in manifest order. Each file must still match its manifest digest.
func writeWorkspaceArchive(w io.Writer, root string, entries []WorkspaceEntry) error {
	source, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer source.Close()
	archive := tar.NewWriter(w)
	remaining := defaultWorkspaceLimits.maxBytes
	for _, entry := range entries {
		header := &tar.Header{Name: entry.Path, Mode: int64(entry.Mode.Perm()), ModTime: time.Unix(0, 0)}
		switch entry.Kind {
		case "dir":
			header.Typeflag = tar.TypeDir
		case "symlink":
			header.Typeflag = tar.TypeSymlink
			header.Linkname = entry.LinkTarget
		case "file":
			header.Typeflag = tar.TypeReg
		default:
			return fmt.Errorf("workspace entry %q has unsupported kind %q", entry.Path, entry.Kind)
		}
		if header.Typeflag != tar.TypeReg {
			if err := archive.WriteHeader(header); err != nil {
				return err
			}
			continue
		}
		size, err := writeArchiveFile(archive, header, source, entry.Path, entry.SHA256, min(defaultWorkspaceLimits.maxFileBytes, remaining))
		if err != nil {
			return err
		}
		remaining -= size
	}
	return archive.Close()
}

// writeArchiveFile writes the staged file rel under source. The root keeps the
// open inside the staged workspace, O_NONBLOCK keeps a swapped-in FIFO from
// blocking, and the size cap stops a replacement before its bytes are copied.
// The digest check catches any other change since the copy.
func writeArchiveFile(archive *tar.Writer, header *tar.Header, source *os.Root, rel, wantSHA256 string, allowed int64) (int64, error) {
	file, err := source.OpenFile(filepath.FromSlash(rel), os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return 0, err
	}
	if !info.Mode().IsRegular() {
		return 0, fmt.Errorf("staged workspace file %q is not a regular file", rel)
	}
	if info.Size() > allowed {
		return 0, fmt.Errorf("staged workspace file %q is larger than the workspace caps allow", rel)
	}
	header.Size = info.Size()
	if err := archive.WriteHeader(header); err != nil {
		return 0, err
	}
	hash := sha256.New()
	if _, err := io.CopyN(io.MultiWriter(archive, hash), file, header.Size); err != nil {
		return 0, err
	}
	if hex.EncodeToString(hash.Sum(nil)) != wantSHA256 {
		return 0, fmt.Errorf("staged workspace file %q differs from its manifest", header.Name)
	}
	return header.Size, nil
}

// sendWorkspaceArchive sends the framed archive and returns the guest
// workspace path from an ack that names the same digest.
func sendWorkspaceArchive(conn io.ReadWriter, materializationID string, archive io.Reader, size int64, digest string) (string, error) {
	// The header is space-separated and newline-terminated, so the receiver
	// can parse only an ID that is a path segment without those delimiters.
	if _, err := statePathSegment("materialization id", materializationID); err != nil || strings.ContainsAny(materializationID, " \r\n") {
		return "", fmt.Errorf("materialization id %q cannot be sent in a workspace archive header", materializationID)
	}
	if _, err := fmt.Fprintf(conn, "%s %s %s %d\n", archiveMagic, materializationID, digest, size); err != nil {
		return "", err
	}
	if _, err := io.CopyN(conn, archive, size); err != nil {
		return "", err
	}
	line, err := readArchiveLine(bufio.NewReaderSize(conn, 4096))
	if err != nil {
		return "", fmt.Errorf("read workspace ack: %w", err)
	}
	fields := strings.SplitN(line, " ", 3)
	if len(fields) == 3 && fields[0] == "ok" && fields[1] == digest && path.IsAbs(fields[2]) {
		return fields[2], nil
	}
	return "", fmt.Errorf("guest refused the workspace archive: %q", line)
}

// receiveWorkspace reads one framed archive, verifies its digest before it
// unpacks anything under root, and acks the result. It fails closed: an error
// removes the partial materialization and acks the reason.
func receiveWorkspace(conn io.ReadWriter, root string, limits workspaceLimits) error {
	workspace, digest, err := receiveWorkspaceArchive(bufio.NewReaderSize(conn, 4096), root, limits)
	if err != nil {
		_, ackErr := fmt.Fprintf(conn, "error %q\n", err.Error())
		return errors.Join(err, ackErr)
	}
	_, err = fmt.Fprintf(conn, "ok %s %s\n", digest, workspace)
	return err
}

func receiveWorkspaceArchive(reader *bufio.Reader, root string, limits workspaceLimits) (string, string, error) {
	line, err := readArchiveLine(reader)
	if err != nil {
		return "", "", fmt.Errorf("read workspace archive header: %w", err)
	}
	fields := strings.Split(line, " ")
	if len(fields) != 4 || fields[0] != archiveMagic {
		return "", "", fmt.Errorf("workspace archive header is malformed")
	}
	materializationID, err := statePathSegment("materialization id", fields[1])
	if err != nil {
		return "", "", err
	}
	digest := fields[2]
	if decoded, err := hex.DecodeString(digest); err != nil || len(decoded) != sha256.Size || hex.EncodeToString(decoded) != digest {
		return "", "", fmt.Errorf("workspace archive digest is malformed")
	}
	size, err := strconv.ParseInt(fields[3], 10, 64)
	if maxSize := limits.maxBytes + int64(limits.maxEntries)*archiveEntryOverhead; err != nil || size < 0 || size > maxSize {
		return "", "", fmt.Errorf("workspace archive size must be 0 to %d bytes", maxSize)
	}
	receiver, err := openReceiverRoot(root)
	if err != nil {
		return "", "", err
	}
	defer receiver.Close()
	spool, spoolName, err := createSpool(receiver)
	if err != nil {
		return "", "", err
	}
	defer receiver.Remove(spoolName)
	defer spool.Close()
	hash := sha256.New()
	if read, err := io.Copy(io.MultiWriter(spool, hash), io.LimitReader(reader, size)); err != nil || read != size {
		return "", "", fmt.Errorf("workspace archive is truncated: read %d of %d bytes", read, size)
	}
	if hex.EncodeToString(hash.Sum(nil)) != digest {
		return "", "", fmt.Errorf("workspace archive digest mismatch")
	}
	if _, err := spool.Seek(0, io.SeekStart); err != nil {
		return "", "", err
	}
	// The materialization root is owner-only, so no other guest account can
	// reach the workspace while it unpacks or after it is acked.
	if err := receiver.Mkdir("materializations", 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return "", "", err
	}
	materializations, err := openSubdirectoryRoot(receiver, "materializations")
	if err != nil {
		return "", "", err
	}
	defer materializations.Close()
	// One receive at a time changes the materializations, so a late failure
	// of one receive cannot remove the copy that another receive of the same
	// ID acked. Closing the directory ends the lock.
	lock, err := materializations.Open(".")
	if err != nil {
		return "", "", err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return "", "", err
	}
	if err := removeMaterialization(materializations, materializationID); err != nil {
		return "", "", err
	}
	if err := materializations.Mkdir(materializationID, 0o700); err != nil {
		return "", "", err
	}
	err = unpackMaterialization(receiver, materializations, materializationID, spool, limits)
	if err != nil {
		return "", "", errors.Join(err, removeMaterialization(materializations, materializationID))
	}
	return filepath.Join(root, "materializations", materializationID, MaterializedWorktree), digest, nil
}

// removeMaterialization removes the materialization id. A directory that the
// archive made read-only blocks the removal of its entries on a non-root
// guest, so a walk that follows no symlink first gives each directory of the
// tree owner rwx.
func removeMaterialization(materializations *os.Root, id string) error {
	if info, err := materializations.Lstat(id); err == nil && info.IsDir() {
		_ = fs.WalkDir(materializations.FS(), id, func(name string, entry fs.DirEntry, err error) error {
			if err == nil && entry.IsDir() {
				_ = materializations.Chmod(name, 0o700)
			}
			return nil
		})
	}
	return materializations.RemoveAll(id)
}

// beforeReceiverOpen runs between the check of a receiver path and its open.
// Only tests replace it, to swap the path in that gap.
var beforeReceiverOpen = func(string) {}

// openReceiverRoot opens root as an os.Root that is the directory a no-follow
// walk from / reaches. A symlink at root or at any ancestor is refused, and a
// path swapped between the walk and the open fails the identity check. Every
// later receiver write and removal is relative to the returned root.
func openReceiverRoot(root string) (*os.Root, error) {
	parent, cleaned, err := rootio.OpenParentDirectoryNoFollow(root)
	if err != nil {
		return nil, fmt.Errorf("open workspace receiver root without following symlinks: %w", err)
	}
	defer parent.Close()
	pinned, err := rootio.OpenDirectoryAtNoFollow(parent, filepath.Base(cleaned))
	if err != nil {
		return nil, fmt.Errorf("open workspace receiver root without following symlinks: %w", err)
	}
	defer pinned.Close()
	beforeReceiverOpen(cleaned)
	opened, err := os.OpenRoot(cleaned)
	if err != nil {
		return nil, err
	}
	if err := requireSameDirectory(pinned, opened, cleaned); err != nil {
		return nil, errors.Join(err, opened.Close())
	}
	return opened, nil
}

// openSubdirectoryRoot opens name under parent as an os.Root only when name is
// a real directory. os.Root confines a symlink to parent but still follows
// one, so the Lstat refuses a symlink and the identity check refuses a swap
// between the Lstat and the open.
func openSubdirectoryRoot(parent *os.Root, name string) (*os.Root, error) {
	info, err := parent.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("workspace receiver path %q is not a directory", filepath.Join(parent.Name(), name))
	}
	beforeReceiverOpen(filepath.Join(parent.Name(), name))
	opened, err := parent.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	openedInfo, err := opened.Stat(".")
	if err == nil && !os.SameFile(info, openedInfo) {
		err = fmt.Errorf("workspace receiver path %q changed while it was opened", filepath.Join(parent.Name(), name))
	}
	if err != nil {
		return nil, errors.Join(err, opened.Close())
	}
	return opened, nil
}

func requireSameDirectory(pinned *os.File, opened *os.Root, name string) error {
	pinnedInfo, err := pinned.Stat()
	if err != nil {
		return err
	}
	openedInfo, err := opened.Stat(".")
	if err != nil {
		return err
	}
	if !os.SameFile(pinnedInfo, openedInfo) {
		return fmt.Errorf("workspace receiver root %q changed while it was opened", name)
	}
	return nil
}

// createSpool creates an owner-only spool file directly under the receiver
// root with O_EXCL, so it never opens a file that is already there.
func createSpool(receiver *os.Root) (*os.File, string, error) {
	for range 32 {
		var suffix [8]byte
		if _, err := rand.Read(suffix[:]); err != nil {
			return nil, "", err
		}
		name := ".incoming-" + hex.EncodeToString(suffix[:])
		file, err := receiver.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			return file, name, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, "", err
		}
	}
	return nil, "", fmt.Errorf("cannot create a workspace archive spool under %q", receiver.Name())
}

// unpackMaterialization unpacks the spool into the worktree of the new
// materialization directory id and syncs what it created.
func unpackMaterialization(receiver, materializations *os.Root, id string, spool io.Reader, limits workspaceLimits) error {
	materialization, err := openSubdirectoryRoot(materializations, id)
	if err != nil {
		return err
	}
	defer materialization.Close()
	if err := materialization.Mkdir(MaterializedWorktree, 0o755); err != nil {
		return err
	}
	workspace, err := openSubdirectoryRoot(materialization, MaterializedWorktree)
	if err != nil {
		return err
	}
	defer workspace.Close()
	if err := unpackWorkspaceArchive(spool, workspace, limits); err != nil {
		return err
	}
	return syncMaterialization(receiver, materializations, materialization, workspace)
}

// syncMaterialization fsyncs every directory of the unpacked workspace, the
// materialization directory, the materializations directory and the receiver
// root, so the workspace that the receiver acks survives a guest power loss.
// unpackArchiveFile already synced the files.
func syncMaterialization(receiver, materializations, materialization, workspace *os.Root) error {
	err := fs.WalkDir(workspace.FS(), ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || !entry.IsDir() {
			return err
		}
		return syncRootDirectory(workspace, name)
	})
	if err != nil {
		return err
	}
	for _, directory := range []*os.Root{materialization, materializations, receiver} {
		if err := syncRootDirectory(directory, "."); err != nil {
			return err
		}
	}
	return nil
}

func syncRootDirectory(root *os.Root, name string) error {
	directory, err := root.Open(name)
	if err != nil {
		return err
	}
	return errors.Join(directory.Sync(), directory.Close())
}

// unpackWorkspaceArchive applies the host copy rules again on the guest: safe
// names, no excluded paths, no collisions, a parent directory from the same
// archive (so no write passes through a symlink), only dirs, files and
// contained symlinks, and the size and entry caps.
func unpackWorkspaceArchive(archive io.Reader, workspace *os.Root, limits workspaceLimits) error {
	reader := tar.NewReader(archive)
	directories := map[string]bool{".": true}
	destinations := newWorkspaceDestinationState()
	excluded := DefaultContract().WorkspaceMaterialization.ExcludedPaths
	var links, dirs []*tar.Header
	entryCount, totalBytes := 0, int64(0)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		rel := header.Name
		if entryCount >= limits.maxEntries {
			return fmt.Errorf("workspace has more than %d entries", limits.maxEntries)
		}
		entryCount++
		if !filepath.IsLocal(rel) || path.Clean(rel) != rel {
			return fmt.Errorf("workspace archive entry name is unsafe: %q", rel)
		}
		if excludedPath, err := isExcludedPath(rel, excluded); err != nil || excludedPath {
			return fmt.Errorf("workspace archive entry is excluded or invalid: %q", rel)
		}
		if err := destinations.reserve(rel); err != nil {
			return err
		}
		if !directories[path.Dir(rel)] {
			return fmt.Errorf("workspace archive entry %q has no parent directory in the archive", rel)
		}
		dest := filepath.FromSlash(rel)
		mode := header.FileInfo().Mode().Perm()
		switch header.Typeflag {
		case tar.TypeDir:
			// Owner-only until the unpack ends, so a read-only source
			// directory cannot block its own entries on a non-root guest.
			if err := workspace.Mkdir(dest, 0o700); err != nil {
				return err
			}
			directories[rel] = true
			dirs = append(dirs, header)
		case tar.TypeReg:
			if header.Size > min(limits.maxFileBytes, limits.maxBytes-totalBytes) {
				return workspaceSizeError(rel, header.Size, limits)
			}
			totalBytes += header.Size
			if err := unpackArchiveFile(reader, workspace, dest, mode); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if err := workspace.Symlink(header.Linkname, dest); err != nil {
				return err
			}
			links = append(links, header)
		default:
			return fmt.Errorf("unsupported workspace entry kind for %q", rel)
		}
	}
	// Check links after the unpack, when every in-workspace target exists.
	for _, link := range links {
		if err := checkReceivedSymlink(workspace, link.Name, link.Linkname); err != nil {
			return err
		}
	}
	// The umask must not change the modes that the manifest records. A
	// child comes after its parent in the archive, so the reverse order
	// sets each directory mode after its children.
	for _, dir := range slices.Backward(dirs) {
		if err := workspace.Chmod(filepath.FromSlash(dir.Name), dir.FileInfo().Mode().Perm()); err != nil {
			return err
		}
	}
	return nil
}

// checkReceivedSymlink applies the host symlink rule to an unpacked link. The
// workspace root resolves the link and refuses a target outside it, which
// also catches a chain that the lexical check cannot see. Dangling links fail.
func checkReceivedSymlink(workspace *os.Root, rel, target string) error {
	if err := checkWorkspaceSymlinkTarget(rel, target); err != nil {
		return err
	}
	if _, err := workspace.Stat(filepath.FromSlash(rel)); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("workspace symlink %q does not resolve inside the workspace: %q", rel, err.Error())
		}
		return fmt.Errorf("workspace symlink %q escapes the workspace or does not resolve: %q", rel, err.Error())
	}
	return nil
}

func unpackArchiveFile(reader io.Reader, workspace *os.Root, dest string, mode os.FileMode) error {
	file, err := workspace.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	_, err = io.Copy(file, reader)
	if err == nil {
		err = file.Chmod(mode)
	}
	if err == nil {
		err = file.Sync()
	}
	return errors.Join(err, file.Close())
}

func readArchiveLine(reader *bufio.Reader) (string, error) {
	line, err := reader.ReadSlice('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(string(line), "\n"), nil
}
