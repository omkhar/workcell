// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package applecontainer

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// encodeAuditPathValue makes a filesystem path safe to interpolate into a
// whitespace-delimited audit line without rejecting legitimate spaces. It ranges
// over BYTES, not runes, so a non-UTF-8 path byte is preserved exactly instead of
// collapsing to U+FFFD: any '%', any byte <= 0x20 (space and all control/
// whitespace), and any byte >= 0x7f (DEL and every high/non-UTF-8 byte) is
// percent-encoded; other printable ASCII is emitted literally. A space cannot
// inject a stray token and a newline cannot forge a line, while
// decodeAuditPathValue recovers the exact original byte string for ANY input.
func encodeAuditPathValue(value string) string {
	var b strings.Builder
	for i := 0; i < len(value); i++ {
		c := value[i]
		if c == '%' || c <= 0x20 || c >= 0x7f {
			fmt.Fprintf(&b, "%%%02X", c)
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

func decodeAuditPathValue(value string) string {
	var b strings.Builder
	for i := 0; i < len(value); {
		if value[i] == '%' && i+3 <= len(value) {
			if n, err := strconv.ParseUint(value[i+1:i+3], 16, 8); err == nil {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		b.WriteByte(value[i])
		i++
	}
	return b.String()
}

// auditHasEvents reports whether every named event appears as the exact parsed
// event= field of some record (lines already filtered to one session by
// SessionAuditRecords), so a value like a path ending in "event=session_started"
// cannot satisfy it — only a genuine event line counts.
func auditHasEvents(records []string, events ...string) bool {
	seen := make(map[string]bool)
	for _, r := range records {
		seen[auditLineEvent(r)] = true
	}
	for _, e := range events {
		if !seen[e] {
			return false
		}
	}
	return true
}

// auditLineEvent returns the exact value of a line's event= field ("" if absent).
func auditLineEvent(line string) string {
	for _, tok := range strings.Fields(line) {
		if k, v, ok := strings.Cut(tok, "="); ok && k == "event" {
			return v
		}
	}
	return ""
}

// stateRootFor returns the state root that owns targetRoot, which the target
// constructs as <stateRoot>/targets/<kind>/<provider>/<targetID> (four levels
// below the state root).
func stateRootFor(targetRoot string) string {
	return filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(targetRoot))))
}

// rejectSymlinkChain refuses a symlink on any directory component of dir that
// lies below trustedRoot (dir must be trustedRoot or under it). Each Lstat
// follows trusted ancestors ABOVE trustedRoot (e.g. macOS /var → /private/var)
// and inspects only the component itself; the walk stops at trustedRoot, so a
// legit system symlink above the trusted root is not flagged while a
// target-managed directory swapped for a symlink is rejected. Used to guard both
// the session-record and audit-log parent chains from a single place.
func rejectSymlinkChain(trustedRoot, dir string) error {
	trustedRoot = filepath.Clean(trustedRoot)
	cur := filepath.Clean(dir)
	rel, err := filepath.Rel(trustedRoot, cur)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%q is not under trusted root %q", dir, trustedRoot)
	}
	for cur != trustedRoot {
		if info, err := os.Lstat(cur); err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("state directory %q is a symlink", cur)
			}
		} else if !os.IsNotExist(err) {
			return err
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}
	return nil
}

// openAuditParent opens the audit log's parent directory by walking down from
// trustedRoot atomically. The trusted root is opened by path (so it may traverse
// legitimate system symlinks such as macOS /var → /private/var that live ABOVE
// the trust boundary); then every target-managed component below it is opened
// with O_NOFOLLOW relative to the previous directory fd, so a directory swapped
// for a symlink is refused with no path re-resolution. This closes the TOCTOU an
// Lstat-then-open-by-path check leaves open (a parent could be swapped between
// the Lstat and the OpenFile). A missing component is created with Mkdirat and
// re-opened; in the real flow every directory already exists. The returned fd is
// the caller's to close.
func openAuditParent(trustedRoot, parent string) (int, error) {
	trustedRoot = filepath.Clean(trustedRoot)
	parent = filepath.Clean(parent)
	rel, err := filepath.Rel(trustedRoot, parent)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return -1, fmt.Errorf("audit log parent %q is not under trusted root %q", parent, trustedRoot)
	}
	dirfd, err := unix.Open(trustedRoot, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, fmt.Errorf("open trusted root %q: %w", trustedRoot, err)
	}
	if rel == "." {
		return dirfd, nil
	}
	openFlags := unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC
	for _, comp := range strings.Split(rel, string(filepath.Separator)) {
		next, err := unix.Openat(dirfd, comp, openFlags, 0)
		if err == unix.ENOENT {
			if mkErr := unix.Mkdirat(dirfd, comp, 0o755); mkErr != nil && mkErr != unix.EEXIST {
				unix.Close(dirfd)
				return -1, fmt.Errorf("create audit dir %q: %w", comp, mkErr)
			}
			next, err = unix.Openat(dirfd, comp, openFlags, 0)
		}
		unix.Close(dirfd)
		if err != nil {
			return -1, fmt.Errorf("open audit dir %q under %q: %w", comp, trustedRoot, err)
		}
		dirfd = next
	}
	return dirfd, nil
}

// appendAuditLine appends line to the audit log at path, refusing to write
// through a symlink anywhere in the target-managed portion of the path. It walks
// from trustedRoot to the log's parent with openAuditParent (every component
// O_NOFOLLOW), then opens the leaf O_NOFOLLOW relative to that verified parent
// fd, so no directory or the log file itself can be swapped for a symlink between
// check and use — the whole traversal is atomic. O_NONBLOCK plus an Fstat on the
// returned fd reject a pre-created FIFO/device/socket: opening a FIFO O_WRONLY
// would otherwise block waiting for a reader, or divert evidence into a pipe.
// The Fstat inspects the ACTUAL opened object, so there is no path re-lookup to
// race. O_NONBLOCK is a no-op for appends to the regular file, so it is left set.
func appendAuditLine(trustedRoot, path, line string) error {
	parentFD, err := openAuditParent(trustedRoot, filepath.Dir(path))
	if err != nil {
		return err
	}
	defer unix.Close(parentFD)
	leaf, err := unix.Openat(parentFD, filepath.Base(path), unix.O_APPEND|unix.O_CREAT|unix.O_WRONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return fmt.Errorf("open audit log %q: %w", path, err)
	}
	handle := os.NewFile(uintptr(leaf), path)
	defer handle.Close()
	var st unix.Stat_t
	if err := unix.Fstat(leaf, &st); err != nil {
		return fmt.Errorf("stat audit log %q: %w", path, err)
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG {
		return fmt.Errorf("audit log %q is not a regular file", path)
	}
	// Reject a hard link: a multiply-linked regular file passes the S_IFREG check
	// but would write audit evidence into an attacker-chosen inode. A log this
	// helper created and owns has exactly one link.
	if st.Nlink != 1 {
		return fmt.Errorf("audit log %q is multiply linked (%d links)", path, st.Nlink)
	}
	if _, err := io.WriteString(handle, line+"\n"); err != nil {
		return err
	}
	return nil
}
