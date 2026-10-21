// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package remotevm

import (
	"archive/tar"
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
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
	archive := tar.NewWriter(w)
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
		if err := writeArchiveFile(archive, header, filepath.Join(root, filepath.FromSlash(entry.Path)), entry.SHA256); err != nil {
			return err
		}
	}
	return archive.Close()
}

func writeArchiveFile(archive *tar.Writer, header *tar.Header, path, wantSHA256 string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	// The digest check below catches a staged file that changed since the
	// copy, whatever replaced it.
	info, err := file.Stat()
	if err != nil {
		return err
	}
	header.Size = info.Size()
	if err := archive.WriteHeader(header); err != nil {
		return err
	}
	hash := sha256.New()
	if _, err := io.CopyN(io.MultiWriter(archive, hash), file, header.Size); err != nil {
		return err
	}
	if hex.EncodeToString(hash.Sum(nil)) != wantSHA256 {
		return fmt.Errorf("staged workspace file %q differs from its manifest", header.Name)
	}
	return nil
}

// sendWorkspaceArchive sends the framed archive and returns the guest
// workspace path from an ack that names the same digest.
func sendWorkspaceArchive(conn io.ReadWriter, materializationID string, archive io.Reader, size int64, digest string) (string, error) {
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
	spool, err := os.CreateTemp(root, ".incoming-")
	if err != nil {
		return "", "", err
	}
	defer os.Remove(spool.Name())
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
	materializationRoot := filepath.Join(root, "materializations", materializationID)
	workspace := filepath.Join(materializationRoot, MaterializedWorktree)
	if err := os.RemoveAll(materializationRoot); err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		return "", "", err
	}
	if err := unpackWorkspaceArchive(spool, workspace, limits); err != nil {
		return "", "", errors.Join(err, os.RemoveAll(materializationRoot))
	}
	return workspace, digest, nil
}

// unpackWorkspaceArchive applies the host copy rules again on the guest: safe
// names, no excluded paths, no collisions, a parent directory from the same
// archive (so no write passes through a symlink), only dirs, files and
// contained symlinks, and the size and entry caps.
func unpackWorkspaceArchive(archive io.Reader, workspace string, limits workspaceLimits) error {
	reader := tar.NewReader(archive)
	directories := map[string]bool{".": true}
	destinations := newWorkspaceDestinationState()
	excluded := DefaultContract().WorkspaceMaterialization.ExcludedPaths
	var links []*tar.Header
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
		dest := filepath.Join(workspace, filepath.FromSlash(rel))
		mode := header.FileInfo().Mode().Perm()
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.Mkdir(dest, mode); err != nil {
				return err
			}
			directories[rel] = true
		case tar.TypeReg:
			if header.Size > min(limits.maxFileBytes, limits.maxBytes-totalBytes) {
				return workspaceSizeError(rel, header.Size, limits)
			}
			totalBytes += header.Size
			if err := unpackArchiveFile(reader, dest, mode); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if err := os.Symlink(header.Linkname, dest); err != nil {
				return err
			}
			links = append(links, header)
		default:
			return fmt.Errorf("unsupported workspace entry kind for %q", rel)
		}
	}
	// Check links after the unpack, when every in-workspace target exists.
	resolvedWorkspace, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		return err
	}
	for _, link := range links {
		if err := checkWorkspaceSymlink(resolvedWorkspace, filepath.Join(workspace, filepath.FromSlash(link.Name)), link.Name, link.Linkname); err != nil {
			return err
		}
	}
	return nil
}

func unpackArchiveFile(reader io.Reader, dest string, mode os.FileMode) error {
	file, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	_, err = io.Copy(file, reader)
	return errors.Join(err, file.Close())
}

func readArchiveLine(reader *bufio.Reader) (string, error) {
	line, err := reader.ReadSlice('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(string(line), "\n"), nil
}
