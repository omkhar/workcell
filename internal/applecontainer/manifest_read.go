// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package applecontainer

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

func rejectDuplicateJSONKeys(raw []byte) error {
	return scanJSONValue(json.NewDecoder(bytes.NewReader(raw)))
}

func scanJSONValue(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return nil
	}
	if delim == '{' {
		seen := make(map[string]struct{})
		for dec.More() {
			key, err := dec.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok {
				return fmt.Errorf("JSON object key is not a string")
			}
			if _, found := seen[name]; found {
				return fmt.Errorf("duplicate JSON key %q", name)
			}
			seen[name] = struct{}{}
			if err := scanJSONValue(dec); err != nil {
				return err
			}
		}
	} else {
		for dec.More() {
			if err := scanJSONValue(dec); err != nil {
				return err
			}
		}
	}
	_, err = dec.Token()
	return err
}

// readFileSafeBounded reads a stable, single-linked regular file through a
// pinned parent. It accepts files up to limit bytes and verifies the final name.
func readFileSafeBounded(trustedRoot, filePath, label string, limit int64) ([]byte, error) {
	return readFileSafeBoundedWithParent(trustedRoot, filePath, label, limit, openParentDirNoCreate, nil)
}

func readFileSafeBoundedWithParent(trustedRoot, filePath, label string, limit int64, openParent func(string, string) (int, error), validate func([]byte) error) ([]byte, error) {
	if limit < 0 {
		return nil, fmt.Errorf("%s has an invalid byte limit", label)
	}
	parentFD, err := openParent(trustedRoot, filepath.Dir(filePath))
	if err != nil {
		return nil, err
	}
	defer unix.Close(parentFD)
	name := filepath.Base(filePath)
	fd, err := unix.Openat(parentFD, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open %s %q: %w", label, filePath, err)
	}
	handle := os.NewFile(uintptr(fd), filePath)
	defer handle.Close()

	var before unix.Stat_t
	if err := unix.Fstat(fd, &before); err != nil {
		return nil, fmt.Errorf("stat %s %q: %w", label, filePath, err)
	}
	want := workspaceSnapshotFromUnix(before)
	if want.mode&uint32(unix.S_IFMT) != uint32(unix.S_IFREG) {
		return nil, fmt.Errorf("%s %q is not a regular file", label, filePath)
	}
	if want.nlink != 1 {
		return nil, fmt.Errorf("%s %q is multiply linked (%d links)", label, filePath, want.nlink)
	}
	if want.size < 0 || want.size > limit {
		return nil, fmt.Errorf("%s exceeds the byte limit of %d: %s", label, limit, name)
	}
	data, err := io.ReadAll(io.LimitReader(handle, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read %s %q: %w", label, filePath, err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s exceeds the byte limit of %d: %s", label, limit, name)
	}
	if int64(len(data)) != want.size {
		return nil, fmt.Errorf("%s %q changed while reading", label, filePath)
	}

	if _, err := handle.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("seek %s %q: %w", label, filePath, err)
	}
	digest := sha256.Sum256(data)
	check := sha256.New()
	read, err := io.Copy(check, io.LimitReader(handle, limit+1))
	if err != nil || read != int64(len(data)) || !bytes.Equal(check.Sum(nil), digest[:]) {
		return nil, fmt.Errorf("%s %q changed during verification", label, filePath)
	}
	// Keep both original descriptors open through decoding and caller validation.
	// Reopen the full path only after all semantic work completes.
	if validate != nil {
		if err := validate(data); err != nil {
			return nil, err
		}
	}
	reopenedParent, err := openParent(trustedRoot, filepath.Dir(filePath))
	if err != nil {
		return nil, fmt.Errorf("%s %q changed during verification", label, filePath)
	}
	defer unix.Close(reopenedParent)
	var opened, named, pinnedParent, reboundParent, reboundLeaf unix.Stat_t
	if err := unix.Fstat(fd, &opened); err != nil {
		return nil, fmt.Errorf("restat %s %q: %w", label, filePath, err)
	}
	if err := unix.Fstatat(parentFD, name, &named, unix.AT_SYMLINK_NOFOLLOW); err != nil ||
		unix.Fstat(parentFD, &pinnedParent) != nil || unix.Fstat(reopenedParent, &reboundParent) != nil ||
		unix.Fstatat(reopenedParent, name, &reboundLeaf, unix.AT_SYMLINK_NOFOLLOW) != nil ||
		workspaceSnapshotFromUnix(opened) != want || workspaceSnapshotFromUnix(named) != want ||
		workspaceSnapshotFromUnix(pinnedParent) != workspaceSnapshotFromUnix(reboundParent) ||
		workspaceSnapshotFromUnix(reboundLeaf) != want {
		return nil, fmt.Errorf("%s %q changed during verification", label, filePath)
	}
	return data, nil
}

func readPersistedManifest(stateRoot, manifestPath string, into any) error {
	return readPersistedManifestWithParent(stateRoot, manifestPath, into, openParentDirNoCreate, nil)
}

func readPersistedManifestWithParent(stateRoot, manifestPath string, into any, openParent func(string, string) (int, error), validate func([]byte) error) error {
	_, err := readFileSafeBoundedWithParent(stateRoot, manifestPath, "manifest", workspaceManifestMaxBytes, openParent, func(data []byte) error {
		if err := rejectDuplicateJSONKeys(data); err != nil {
			return fmt.Errorf("manifest at %q: %w", manifestPath, err)
		}
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.DisallowUnknownFields()
		if err := dec.Decode(into); err != nil {
			return fmt.Errorf("manifest at %q: %w", manifestPath, err)
		}
		if _, err := dec.Token(); err != io.EOF {
			return fmt.Errorf("manifest at %q: trailing data after manifest", manifestPath)
		}
		if manifest, ok := into.(*WorkspaceManifest); ok {
			if err := validateWorkspaceManifestStructure(*manifest); err != nil {
				return fmt.Errorf("manifest at %q: %w", manifestPath, err)
			}
		}
		want, err := marshalManifestBytes(into)
		if err != nil {
			return fmt.Errorf("manifest at %q: %w", manifestPath, err)
		}
		if !bytes.Equal(data, want) {
			return fmt.Errorf("manifest at %q is not canonical", manifestPath)
		}
		if validate != nil {
			return validate(data)
		}
		return nil
	})
	return err
}

func validateWorkspaceManifestStructure(manifest WorkspaceManifest) error {
	for label, value := range map[string]string{
		"source_workspace":       manifest.SourceWorkspace,
		"materialized_workspace": manifest.MaterializedWorkspace,
	} {
		if _, err := pathutil.CollisionKey(value); err != nil {
			return fmt.Errorf("invalid %s: %w", label, err)
		}
	}
	if manifest.ExcludedPaths == nil {
		return fmt.Errorf("excluded_paths must not be null")
	}
	for _, excluded := range manifest.ExcludedPaths {
		if _, err := pathutil.CollisionKey(excluded); err != nil {
			return fmt.Errorf("invalid excluded path: %w", err)
		}
	}
	if manifest.Entries == nil {
		return fmt.Errorf("entries must not be null")
	}
	if len(manifest.Entries) > workspaceMaxEntries {
		return fmt.Errorf("entries exceeds %d items", workspaceMaxEntries)
	}
	seen := make(map[string]string, len(manifest.Entries))
	for index, entry := range manifest.Entries {
		if entry.Path == "" || path.IsAbs(entry.Path) || path.Clean(entry.Path) != entry.Path {
			return fmt.Errorf("entry %d has an invalid path", index)
		}
		components := strings.Split(entry.Path, "/")
		if err := validateWorkspacePath(entry.Path, len(components)); err != nil {
			return err
		}
		for _, component := range components {
			if err := validateWorkspaceLeaf("workspace manifest entry", component); err != nil {
				return err
			}
		}
		key, err := pathutil.CollisionKey(entry.Path)
		if err != nil {
			return err
		}
		if previous, found := seen[key]; found {
			return fmt.Errorf("workspace manifest entry collision between %q and %q", previous, entry.Path)
		}
		seen[key] = entry.Path
		if index > 0 && manifest.Entries[index-1].Path >= entry.Path {
			return fmt.Errorf("workspace manifest entries are not in canonical order")
		}
		if err := validateWorkspaceManifestEntry(entry); err != nil {
			return fmt.Errorf("entry %q: %w", entry.Path, err)
		}
	}
	if err := validateWorkspaceLinks(manifest.Entries); err != nil {
		return err
	}
	return nil
}

func validateWorkspaceManifestEntry(entry WorkspaceEntry) error {
	allowedMode := fs.ModePerm
	switch entry.Kind {
	case "dir":
		allowedMode |= fs.ModeDir
		if entry.Mode.Type() != fs.ModeDir || entry.SHA256 != "" || entry.LinkTarget != "" {
			return fmt.Errorf("directory metadata is invalid")
		}
	case "file":
		if entry.Mode.Type() != 0 || entry.LinkTarget != "" || len(entry.SHA256) != sha256.Size*2 || entry.SHA256 != strings.ToLower(entry.SHA256) {
			return fmt.Errorf("file metadata is invalid")
		}
		if _, err := hex.DecodeString(entry.SHA256); err != nil {
			return fmt.Errorf("file digest is invalid")
		}
	case "symlink":
		allowedMode |= fs.ModeSymlink
		if entry.Mode.Type() != fs.ModeSymlink || entry.SHA256 != "" || entry.LinkTarget == "" || path.IsAbs(entry.LinkTarget) {
			return fmt.Errorf("symlink metadata is invalid")
		}
		if len(entry.LinkTarget) > workspaceMaxLinkBytes {
			return fmt.Errorf("symlink target exceeds %d bytes", workspaceMaxLinkBytes)
		}
		if _, err := pathutil.CollisionKey(entry.LinkTarget); err != nil {
			return fmt.Errorf("symlink target is invalid: %w", err)
		}
	default:
		return fmt.Errorf("kind is invalid")
	}
	if entry.Mode&^allowedMode != 0 {
		return fmt.Errorf("mode has unsupported bits")
	}
	return nil
}

func sortedWorkspaceEntries(entries []WorkspaceEntry) []WorkspaceEntry {
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return entries
}
