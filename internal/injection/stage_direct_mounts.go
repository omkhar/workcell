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
	"golang.org/x/sys/unix"
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
	// Reject symlinked sources up front: a symlink anywhere in the
	// path under the user's host-inputs root that points at /etc/passwd
	// would dereference through every subsequent stat/open and leak
	// the target's content into the staged bundle. copyDirContents
	// already skips symlinks encountered inside a directory source;
	// this closes the matching escape for the top-level source path
	// itself.
	//
	// Intermediate-component-symlink defense (Sec-r2-1, PR-FIX-10):
	// the prior FIX-8 implementation only Lstat'd the leaf component,
	// so an attacker who planted a symlink at any *parent* component
	// (e.g., `bundle/userdir/parent-link -> /etc`) could route the
	// staging copy through the swapped-out directory and harvest
	// arbitrary host content. We now walk every component from the
	// filesystem root down to the leaf and Lstat each step.
	//
	// Allowing macOS system symlinks: `/var -> private/var`,
	// `/etc -> private/etc`, `/tmp -> private/tmp` are *legitimate*
	// system-level symlinks that any `t.TempDir()` traversal crosses
	// on macOS. We allow only those exact platform links. Every
	// operator-controlled symlink in the source chain is rejected,
	// including relative non-escaping targets like `link -> child`.
	//
	// `os.Root` (Go 1.24+) was evaluated as an alternative but it
	// follows relative symlinks transparently as long as the resolved
	// path stays within the root, which means a relative-target attacker
	// link (`parent-link -> ../etc`) is not flagged. Component-walk
	// with target-shape inspection catches both absolute and
	// relative-escape attacks while preserving the macOS system path.
	offender, inspectErr := findUnsafeSymlinkInPathChain(cleanedSource)
	if inspectErr != nil {
		return fmt.Errorf("Direct input source could not be inspected: %s: %v", cleanedSource, inspectErr)
	}
	if offender != "" {
		return fmt.Errorf("Direct input source must not be a symbolic link: %s", cleanedSource)
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
	source, mode, kind, err := openDirectMountSource(hostSource)
	if err != nil {
		return err
	}
	defer source.Close()

	switch kind {
	case directMountSourceDir:
		if err := os.MkdirAll(stagedSource, 0o755); err != nil {
			return err
		}
		return copyDirContents(source, filepath.Clean(hostSource), stagedSource)
	case directMountSourceRegular:
		if err := os.MkdirAll(filepath.Dir(stagedSource), 0o755); err != nil {
			return err
		}
		return copyOpenFileWithMode(source, stagedSource, mode)
	default:
		return fmt.Errorf("Direct input source is missing, not absolute, or not a regular file/directory: %s", hostSource)
	}
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
// Source traversal is anchored to opened directory descriptors. Each child is
// opened with openat(O_NOFOLLOW), so a parent path swapped after validation
// cannot redirect staging to a different host tree.
func copyDirContents(src *os.File, srcDisplay, dst string) error {
	entries, err := src.ReadDir(-1)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		mode := entry.Type()
		displayPath := filepath.Join(srcDisplay, entry.Name())
		if mode&fs.ModeSymlink != 0 {
			log.Printf("workcell injection: skipping symlink under host-input source: %s", displayPath)
			continue
		}
		childMode, kind, err := classifyDirectMountChild(src, entry.Name())
		if err != nil {
			return err
		}
		if kind == directMountSourceOther {
			continue
		}
		target := filepath.Join(dst, entry.Name())
		child, childMode, kind, err := openDirectMountChild(src, entry.Name(), displayPath)
		if err != nil {
			if errors.Is(err, unix.ELOOP) || isSkippableDirectMountOpenError(err) {
				log.Printf("workcell injection: skipping non-regular entry under host-input source: %s", displayPath)
				continue
			}
			return err
		}
		switch kind {
		case directMountSourceDir:
			if err := os.MkdirAll(target, childMode); err != nil {
				child.Close()
				return err
			}
			if err := copyDirContents(child, displayPath, target); err != nil {
				child.Close()
				return err
			}
			if err := child.Close(); err != nil {
				return err
			}
		case directMountSourceRegular:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				child.Close()
				return err
			}
			if err := copyOpenFileWithMode(child, target, childMode); err != nil {
				child.Close()
				return err
			}
			if err := child.Close(); err != nil {
				return err
			}
		default:
			if err := child.Close(); err != nil {
				return err
			}
		}
	}
	return nil
}

func copyFileWithMode(src, dst string, _ os.FileMode) error {
	in, sourceMode, kind, err := openDirectMountSource(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if kind != directMountSourceRegular {
		return fmt.Errorf("Direct input source is missing, not absolute, or not a regular file/directory: %s", src)
	}
	return copyOpenFileWithMode(in, dst, sourceMode)
}

func copyOpenFileWithMode(in *os.File, dst string, mode os.FileMode) error {
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

type directMountSourceKind int

const (
	directMountSourceOther directMountSourceKind = iota
	directMountSourceRegular
	directMountSourceDir
)

func openDirectMountSource(src string) (*os.File, os.FileMode, directMountSourceKind, error) {
	cleanedSource := canonicalizeAllowedSystemPath(filepath.Clean(src))
	if !filepath.IsAbs(cleanedSource) {
		return nil, 0, directMountSourceOther, fmt.Errorf("Direct input source is missing, not absolute, or not a regular file/directory: %s", src)
	}
	fd, err := unix.Open(string(filepath.Separator), unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY, 0)
	if err != nil {
		return nil, 0, directMountSourceOther, err
	}
	if cleanedSource != string(filepath.Separator) {
		components := strings.Split(strings.TrimPrefix(cleanedSource, string(filepath.Separator)), string(filepath.Separator))
		for index, component := range components {
			nextFD, err := unix.Openat(fd, component, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
			_ = unix.Close(fd)
			if err != nil {
				if errors.Is(err, unix.ELOOP) {
					return nil, 0, directMountSourceOther, fmt.Errorf("Direct input source must not be a symbolic link: %s", cleanedSource)
				}
				return nil, 0, directMountSourceOther, err
			}
			fd = nextFD
			if index < len(components)-1 {
				kind, _, err := classifyDirectMountFD(fd)
				if err != nil {
					_ = unix.Close(fd)
					return nil, 0, directMountSourceOther, err
				}
				if kind != directMountSourceDir {
					_ = unix.Close(fd)
					return nil, 0, directMountSourceOther, fmt.Errorf("Direct input source is missing, not absolute, or not a regular file/directory: %s", src)
				}
			}
		}
	}
	file := os.NewFile(uintptr(fd), cleanedSource)
	if file == nil {
		_ = unix.Close(fd)
		return nil, 0, directMountSourceOther, fmt.Errorf("Direct input source is missing, not absolute, or not a regular file/directory: %s", src)
	}
	kind, mode, err := classifyDirectMountFD(fd)
	if err != nil {
		file.Close()
		return nil, 0, directMountSourceOther, err
	}
	if kind == directMountSourceOther {
		file.Close()
		return nil, 0, directMountSourceOther, fmt.Errorf("Direct input source is missing, not absolute, or not a regular file/directory: %s", src)
	}
	return file, mode, kind, nil
}

func openDirectMountChild(parent *os.File, name, displayPath string) (*os.File, os.FileMode, directMountSourceKind, error) {
	fd, err := unix.Openat(int(parent.Fd()), name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, 0, directMountSourceOther, err
	}
	file := os.NewFile(uintptr(fd), displayPath)
	if file == nil {
		_ = unix.Close(fd)
		return nil, 0, directMountSourceOther, fmt.Errorf("Direct input source is missing, not absolute, or not a regular file/directory: %s", displayPath)
	}
	kind, mode, err := classifyDirectMountFD(fd)
	if err != nil {
		file.Close()
		return nil, 0, directMountSourceOther, err
	}
	return file, mode, kind, nil
}

func classifyDirectMountChild(parent *os.File, name string) (os.FileMode, directMountSourceKind, error) {
	var stat unix.Stat_t
	if err := unix.Fstatat(int(parent.Fd()), name, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return 0, directMountSourceOther, err
	}
	return classifyDirectMountStat(stat)
}

func classifyDirectMountFD(fd int) (directMountSourceKind, os.FileMode, error) {
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return directMountSourceOther, 0, err
	}
	mode, kind, err := classifyDirectMountStat(stat)
	return kind, mode, err
}

func classifyDirectMountStat(stat unix.Stat_t) (os.FileMode, directMountSourceKind, error) {
	modeBits := uint32(stat.Mode)
	mode := os.FileMode(modeBits & 0o777)
	switch modeBits & uint32(unix.S_IFMT) {
	case uint32(unix.S_IFREG):
		return mode, directMountSourceRegular, nil
	case uint32(unix.S_IFDIR):
		return mode, directMountSourceDir, nil
	default:
		return mode, directMountSourceOther, nil
	}
}

func isSkippableDirectMountOpenError(err error) bool {
	return errors.Is(err, unix.ENXIO) || errors.Is(err, unix.ENODEV)
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
