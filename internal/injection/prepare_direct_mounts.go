// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package injection

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/omkhar/workcell/internal/host/hoststate"
	"github.com/omkhar/workcell/internal/runtimeutil"
)

// hostInputMountPrefix is the only mount root that may be staged for the
// container via direct mounts.  Matches the bash guard "/opt/workcell/host-inputs/*".
const hostInputMountPrefix = "/opt/workcell/host-inputs/"

// StageDirectMounts replicates the legacy prepare_injection_direct_mounts helper
// in Go.  It reads the mount spec produced by extract_direct_mounts, validates
// each entry, stages the host source into ${bundleRoot}/direct-mounts/${hash},
// and returns the docker "-v src:dst:ro" arguments interleaved (as bash did).
//
// The function returns an empty slice when the mount-spec file is missing
// (bash returned 0 in that case).  It returns a usage-style error when the
// bundle root is empty.
func StageDirectMounts(bundleRoot, mountSpecPath string) ([]string, error) {
	if _, err := os.Stat(mountSpecPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	if bundleRoot == "" {
		return nil, errors.New("Direct input staging requires an injection bundle root.")
	}

	stagedRoot := filepath.Join(bundleRoot, "direct-mounts")
	if err := os.MkdirAll(stagedRoot, 0o755); err != nil {
		return nil, err
	}

	mounts, err := runtimeutil.ListDirectMounts(mountSpecPath)
	if err != nil {
		return nil, err
	}

	args := make([]string, 0, len(mounts)*2)
	for _, mount := range mounts {
		if mount.Source == "" {
			continue
		}
		if err := validateDirectMount(mount.Source, mount.MountPath); err != nil {
			return nil, err
		}
		entryHash := hoststate.DirectMountCacheKey(mount.Source, mount.MountPath)
		stagedSource := filepath.Join(stagedRoot, entryHash)
		if err := os.RemoveAll(stagedSource); err != nil {
			return nil, err
		}
		if err := stageDirectMountEntry(mount.Source, stagedSource); err != nil {
			return nil, err
		}
		// bash: chmod -R go-rwx — best effort; ignore errors as bash did.
		_ = chmodRecursiveGoNoAccess(stagedSource)
		args = append(args, "-v", fmt.Sprintf("%s:%s:ro", stagedSource, mount.MountPath))
	}
	return args, nil
}

// validateDirectMount mirrors the bash entry validation: source must be an
// absolute path that points at a regular file or directory, and the mount
// path must be inside /opt/workcell/host-inputs/.
//
// Both inputs are normalised via filepath.Clean before the prefix check so
// a `..` segment cannot escape the managed host-input root.  Concretely,
// `strings.HasPrefix("/opt/workcell/host-inputs/../etc/foo", "/opt/workcell/host-inputs/")`
// returns true on raw strings — filepath.Clean collapses the `..` first so
// the resulting `/opt/etc/foo` fails the prefix test, matching the bash
// invariant that the container only sees content explicitly staged under
// the host-inputs root.
//
// The prefix is re-checked against `cleaned + "/"` to handle the edge case
// where the cleaned mount path is exactly the host-input root (no trailing
// slash); HasPrefix on the bare path would let an attacker mount the root
// itself, which has no defensible meaning for a per-entry direct mount.
func validateDirectMount(hostSource, mountPath string) error {
	if !filepath.IsAbs(hostSource) {
		return fmt.Errorf("Direct input source is missing, not absolute, or not a regular file/directory: %s", hostSource)
	}
	cleanedSource := filepath.Clean(hostSource)
	if !filepath.IsAbs(cleanedSource) {
		return fmt.Errorf("Direct input source is missing, not absolute, or not a regular file/directory: %s", hostSource)
	}
	info, err := os.Stat(cleanedSource)
	if err != nil || !(info.Mode().IsRegular() || info.IsDir()) {
		return fmt.Errorf("Direct input source is missing, not absolute, or not a regular file/directory: %s", hostSource)
	}
	// filepath.Clean strips trailing slashes, so the cleaned form of a
	// legal entry is `/opt/workcell/host-inputs/<leaf>`.  Checking the
	// cleaned path directly against the prefix (which still has a
	// trailing slash) rejects both `/opt/workcell/host-inputs/../etc/foo`
	// (cleans to `/opt/etc/foo`) and the bare root
	// `/opt/workcell/host-inputs` (no trailing component) — both of
	// which the old raw-string HasPrefix accepted.
	cleanedMount := filepath.Clean(mountPath)
	if !strings.HasPrefix(cleanedMount, hostInputMountPrefix) {
		return fmt.Errorf("Direct input mount path is outside the managed host-input root: %s", mountPath)
	}
	return nil
}

// stageDirectMountEntry copies a host source into stagedSource, replicating
// "cp -R ${src}/." for directories and "cp -f ${src}" for files.
func stageDirectMountEntry(hostSource, stagedSource string) error {
	info, err := os.Stat(hostSource)
	if err != nil {
		return err
	}
	if info.IsDir() {
		if err := os.MkdirAll(stagedSource, 0o755); err != nil {
			return err
		}
		return copyDirContents(hostSource, stagedSource)
	}
	if err := os.MkdirAll(filepath.Dir(stagedSource), 0o755); err != nil {
		return err
	}
	return copyFileWithMode(hostSource, stagedSource, info.Mode().Perm())
}

// copyDirContents mirrors "cp -R src/. dst" with one cautious-staging
// divergence: symlinks under src are skipped with a log warning rather
// than being dereferenced.  The legacy bash helper relied on `cp -R`
// which would have followed the link target, but a symlink inside a
// host-input source can escape the staging root entirely
// (e.g. `~/.aws/credentials -> /etc/passwd`) and surface arbitrary
// host files inside the container.  Skipping matches the cautious-
// staging discipline applied elsewhere in injection: validate strictly
// and refuse anything that cannot be vouched for.  The warning gives
// the operator enough signal to notice that an expected file did not
// land in the container.
//
// We use entry.Type() / entry.Info() from WalkDir's DirEntry to avoid a
// second Stat per file (entry.Type() returns the lstat-derived mode
// bits, and Info() is the cached fs.FileInfo for the entry).
func copyDirContents(src, dst string) error {
	return filepath.WalkDir(src, func(current string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, current)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		mode := entry.Type()
		if mode&fs.ModeSymlink != 0 {
			log.Printf("workcell injection: skipping symlink under host-input source: %s", current)
			return nil
		}
		target := filepath.Join(dst, rel)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}
		if info.Mode().IsRegular() {
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			return copyFileWithMode(current, target, info.Mode().Perm())
		}
		// Non-regular files (devices, sockets, FIFOs) are skipped — bash
		// cp would refuse most of these too, and they have no defensible
		// meaning inside a container input mount.
		return nil
	})
}

func copyFileWithMode(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chmod(dst, mode)
}

// chmodRecursiveGoNoAccess strips group/other rwx bits from every entry under
// root, mirroring "chmod -R go-rwx".  Errors are returned to the caller; the
// bash original masked them with "|| true", which the caller can mimic.
//
// Uses entry.Info() from the DirEntry to avoid a second Stat per file.
func chmodRecursiveGoNoAccess(root string) error {
	return filepath.WalkDir(root, func(current string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return infoErr
		}
		return os.Chmod(current, info.Mode().Perm()&^0o077)
	})
}
