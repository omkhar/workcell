// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package authpolicy

import (
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/omkhar/workcell/internal/rootio"
	"github.com/omkhar/workcell/internal/secretfile"
)

// canonicalDestinationPath returns the absolute managed-root path for
// the given credential, or an error if the credential is one of the
// host-resolved kinds (e.g. claude-macos-keychain) that workcell does
// not stage on disk.
func canonicalDestinationPath(managedRoot string, credential string) (string, error) {
	parts, ok := canonicalCredentialDestinations[credential]
	if !ok {
		return "", die(fmt.Sprintf("workcell auth set does not manage %s automatically", credential))
	}
	return filepath.Join(managedRoot, parts[0], parts[1]), nil
}

// canonicalDestinationPathPart returns the managed-root-relative path
// portion (provider-dir/leaf) for credential.  Unlike its sibling above
// this never errors — used in diagnostics where a missing mapping
// should produce an empty string rather than a hard failure.
func canonicalDestinationPathPart(credential string) string {
	parts := canonicalCredentialDestinations[credential]
	return filepath.Join(parts[0], parts[1])
}

func ensureDirectory(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	return os.Chmod(path, 0o700)
}

func validateManagedPath(managedRoot string, path string, label string) error {
	if err := requireNoSymlinkInPathChain(path, label); err != nil {
		return err
	}
	return requirePathWithin(managedRoot, path, label)
}

func writeManagedRootMarker(managedRoot string) error {
	if err := ensureDirectory(managedRoot); err != nil {
		return err
	}
	managedRootFS, err := os.OpenRoot(managedRoot)
	if err != nil {
		return err
	}
	defer managedRootFS.Close()
	return rootio.WriteFileAtomic(managedRootFS, managedRootMarker, []byte("managed_by=workcell\n"), 0o600, "."+managedRootMarker+"-")
}

func isWorkcellManagedRoot(path string) bool {
	_, err := os.Stat(filepath.Join(path, managedRootMarker))
	return err == nil
}

// cleanupStagedFile removes a staged temp file rooted at `path`.  It
// is a no-op if path is empty or if the file is already gone — both
// are normal in the happy path where commandSet completed without
// rolling back a staging operation.
func cleanupStagedFile(root *os.Root, path string) error {
	if path == "" {
		return nil
	}
	if err := root.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// stageExistingFile moves the file at `path` to an unguessable
// sibling temp name within the same managed root and returns the temp
// path.  Callers use the returned path with restoreStagedFile to
// undo the move on failure, or with cleanupStagedFile to discard it.
func stageExistingFile(root *os.Root, path string) (string, error) {
	if path == "" {
		return "", nil
	}
	cleanPath, err := normalizeRootRelativePath(path)
	if err != nil {
		return "", err
	}
	if _, err := root.Stat(cleanPath); err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	tempPath, err := reserveRootTempPath(root, filepath.Dir(cleanPath), ".workcell-stage-")
	if err != nil {
		return "", err
	}
	if err := root.Rename(cleanPath, tempPath); err != nil {
		return "", err
	}
	return tempPath, nil
}

func restoreStagedFile(root *os.Root, stagedPath string, destination string) error {
	if stagedPath == "" || destination == "" {
		return nil
	}
	cleanDestination, err := normalizeRootRelativePath(destination)
	if err != nil {
		return err
	}
	if err := root.Rename(stagedPath, cleanDestination); err != nil {
		return err
	}
	return nil
}

func openManagedRoot(managedRoot string) (*os.Root, error) {
	if err := ensureDirectory(managedRoot); err != nil {
		return nil, err
	}
	return os.OpenRoot(managedRoot)
}

func managedRelativePath(managedRoot string, path string, label string) (string, error) {
	if path == "" {
		return "", nil
	}
	return rootio.RelativePathWithin(managedRoot, path, label)
}

func writeSourceFile(root *os.Root, source string, destination string) error {
	in, err := secretfile.Open(source, "credential source", os.Getuid())
	if err != nil {
		return err
	}
	defer in.Close()
	return rootio.WriteFileAtomicFromReader(root, destination, in, 0o600, ".workcell-auth-")
}

func normalizeRootRelativePath(path string) (string, error) {
	if filepath.IsAbs(path) {
		return "", fmt.Errorf("path must be relative to managed root: %s", path)
	}
	cleanPath := filepath.Clean(path)
	if cleanPath == "." || cleanPath == string(filepath.Separator) {
		return "", fmt.Errorf("path must name a file within the managed root: %s", path)
	}
	return cleanPath, nil
}

// reserveRootTempPath returns an unguessable path under root that is
// guaranteed not to exist at return time (the caller will create it).
// The temp file is exclusively created and then immediately removed
// to confirm the name is reserveable; callers must race-safely re-open
// it for write.  Bounded to 32 attempts because the suffix is 8 random
// bytes and a collision means /dev/urandom is misbehaving.
func reserveRootTempPath(root *os.Root, parent string, prefix string) (string, error) {
	parent = filepath.Clean(parent)
	if parent == "." {
		parent = ""
	}
	for attempt := 0; attempt < 32; attempt++ {
		suffix, err := randomTempSuffix()
		if err != nil {
			return "", err
		}
		name := prefix + suffix + ".tmp"
		tempPath := name
		if parent != "" {
			tempPath = filepath.Join(parent, name)
		}
		tempFile, err := root.OpenFile(tempPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			if closeErr := tempFile.Close(); closeErr != nil {
				_ = root.Remove(tempPath)
				return "", closeErr
			}
			if removeErr := root.Remove(tempPath); removeErr != nil {
				return "", removeErr
			}
			return tempPath, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return "", err
		}
	}
	return "", fmt.Errorf("unable to allocate temporary staging path under %s", root.Name())
}

func randomTempSuffix() (string, error) {
	var data [8]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", data[:]), nil
}
