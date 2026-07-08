// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package applecontainer

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/omkhar/workcell/internal/host/sessions"
)

// Bounded non-blocking retry for the shared audit-log append lock: ~50 attempts × 20ms ≈
// a 1s ceiling. High enough that many genuine concurrent appenders (microsecond critical
// sections) all win, low enough that an adversarial/stuck advisory-lock holder makes the
// append fail closed instead of hanging.
const (
	auditLockAttempts = 50
	auditLockBackoff  = 20 * time.Millisecond
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

// DecodeAuditPathValue is the exported canonical decoder for audit path values
// this package writes with encodeAuditPathValue. Cross-package readers (the OCSF
// export in internal/ocsf) reuse it to recover the exact original path from a
// percent-encoded AppleContainer audit field, so a literal backslash in a legal
// POSIX path survives instead of being mis-read as a shell escape.
func DecodeAuditPathValue(value string) string {
	return decodeAuditPathValue(value)
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

// openAuditParent opens the target-managed parent directory for a WRITE, creating
// any missing component (Mkdirat) — see openParentDir. Used by the write paths
// (appendAuditLine / the record writers) where creating an absent dir is correct.
func openAuditParent(trustedRoot, parent string) (int, error) {
	return openParentDir(trustedRoot, parent, true)
}

// openParentDirNoCreate opens the parent directory for a READ/stat: it never
// creates a missing component, so a check cannot side-effect the filesystem — a
// missing component returns its error. Used by the hardened readers and statPathSafe.
func openParentDirNoCreate(trustedRoot, parent string) (int, error) {
	return openParentDir(trustedRoot, parent, false)
}

// openParentDir walks down from trustedRoot atomically. The trusted root is opened
// by path (so it may traverse legitimate system symlinks such as macOS
// /var → /private/var that live ABOVE the trust boundary); then every
// target-managed component below it is opened with O_NOFOLLOW relative to the
// previous directory fd, so a directory swapped for a symlink is refused with no
// path re-resolution. This closes the TOCTOU an Lstat-then-open-by-path check
// leaves open. When create is true a missing component is created with Mkdirat and
// re-opened (write paths); when false a missing component's error is returned
// (read/stat paths must not create). The returned fd is the caller's to close.
func openParentDir(trustedRoot, parent string, create bool) (int, error) {
	trustedRoot = filepath.Clean(trustedRoot)
	parent = filepath.Clean(parent)
	rel, err := filepath.Rel(trustedRoot, parent)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return -1, fmt.Errorf("path %q is not under trusted root %q", parent, trustedRoot)
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
		if err == unix.ENOENT && create {
			if mkErr := unix.Mkdirat(dirfd, comp, 0o755); mkErr != nil && mkErr != unix.EEXIST {
				unix.Close(dirfd)
				return -1, fmt.Errorf("create dir %q: %w", comp, mkErr)
			}
			next, err = unix.Openat(dirfd, comp, openFlags, 0)
		}
		unix.Close(dirfd)
		if err != nil {
			return -1, fmt.Errorf("open dir %q under %q: %w", comp, trustedRoot, err)
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
// validateAuditLogFd rejects anything but a single-linked regular file: a hard link
// passes S_IFREG but would write audit evidence into an attacker-chosen inode, and a log
// this helper created and owns has exactly one link.
func validateAuditLogFd(fd int, path string) error {
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return fmt.Errorf("stat audit log %q: %w", path, err)
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG {
		return fmt.Errorf("audit log %q is not a regular file", path)
	}
	if st.Nlink != 1 {
		return fmt.Errorf("audit log %q is multiply linked (%d links)", path, st.Nlink)
	}
	return nil
}

func appendAuditLine(trustedRoot, path, line string) error {
	parentFD, err := openAuditParent(trustedRoot, filepath.Dir(path))
	if err != nil {
		return err
	}
	defer unix.Close(parentFD)
	base := filepath.Base(path)
	// Open O_WRONLY first: an EXISTING log with owner-write but NO owner-read (mode 0o200 —
	// e.g. a read-masking umask + a crash after O_CREAT but before the Fchmod below ran) can
	// still be opened this way, whereas O_RDWR would EACCES on the absent read bit BEFORE we
	// could heal it — a recovery availability regression. Validate (regular, single-linked)
	// then Fchmod owner read+write, so the O_RDWR re-open below and future readers succeed.
	wfd, err := unix.Openat(parentFD, base, unix.O_APPEND|unix.O_CREAT|unix.O_WRONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return fmt.Errorf("open audit log %q: %w", path, err)
	}
	if err := validateAuditLogFd(wfd, path); err != nil {
		_ = unix.Close(wfd)
		return err
	}
	if err := unix.Fchmod(wfd, 0o600); err != nil {
		_ = unix.Close(wfd)
		return fmt.Errorf("chmod audit log %q: %w", path, err)
	}
	_ = unix.Close(wfd)
	// Re-open O_RDWR (owner read now granted) for the flock + tail-Pread + append. Same
	// openat-from-validated-parent + O_NOFOLLOW hardening, and re-validate regular +
	// single-linked on the new fd.
	leaf, err := unix.Openat(parentFD, base, unix.O_APPEND|unix.O_CREAT|unix.O_RDWR|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return fmt.Errorf("reopen audit log %q: %w", path, err)
	}
	handle := os.NewFile(uintptr(leaf), path)
	defer handle.Close()
	if err := validateAuditLogFd(leaf, path); err != nil {
		return err
	}
	// Serialize the tail-check + append against other appenders to this SAME inode with an
	// advisory exclusive lock on the log fd: the target audit log is shared across sessions
	// (the per-session flock does not cover it), so without this a concurrent appender
	// could write a newline-less fragment (or prepend a duplicate heal) BETWEEN our
	// tail-Pread (which saw a clean '\n') and our O_APPEND write, gluing our line onto that
	// fragment or injecting a stray blank line. LOCK_EX is advisory and per-inode across
	// processes; a crashed writer's lock is auto-released by the OS on fd close/process
	// death, so a dead torn-writer never wedges future appenders (the heal covers its
	// stranded fragment).
	//
	// NON-BLOCKING with a bounded retry, never a blocking LOCK_EX: the log is shared, so a
	// blocking acquire lets any same-UID process hold the advisory lock indefinitely and
	// hang every appender (DoS). The real critical section (Fstat + Pread + one WriteString)
	// is microseconds, so this retry virtually always wins against genuine concurrent
	// appenders; an adversarial/stuck holder instead makes the append fail closed (the
	// caller can retry) rather than hang forever.
	locked := false
	for attempt := 0; attempt < auditLockAttempts; attempt++ {
		err := unix.Flock(leaf, unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			locked = true
			break
		}
		if err != unix.EWOULDBLOCK && err != unix.EAGAIN {
			return fmt.Errorf("lock audit log %q: %w", path, err)
		}
		time.Sleep(auditLockBackoff)
	}
	if !locked {
		return fmt.Errorf("could not acquire audit log lock %q: contended", path)
	}
	// Heal a torn append boundary: if the tail byte is not a newline (a prior short/ENOSPC
	// append, or a crashed process, left a newline-less fragment at EOF), prepend a
	// separator so this line lands on its OWN line rather than glued onto the fragment — a
	// merged line would never byte-match an expected line yet inherit its trailing field,
	// which conflictingCompleteLine would misread as a conflict, permanently dead-ending
	// the append-only log. The fragment is left as an inert line (no session_id token →
	// filterAuditSessionLines ignores it). Size is re-read UNDER the lock: a concurrent
	// appender may have grown the file since the validation Fstat above.
	prefix := ""
	var end unix.Stat_t
	if err := unix.Fstat(leaf, &end); err != nil {
		return fmt.Errorf("stat audit log %q: %w", path, err)
	}
	if end.Size > 0 {
		var last [1]byte
		if _, err := unix.Pread(leaf, last[:], end.Size-1); err != nil {
			return fmt.Errorf("read audit log tail %q: %w", path, err)
		}
		if last[0] != '\n' {
			prefix = "\n"
		}
	}
	if _, err := io.WriteString(handle, prefix+line+"\n"); err != nil {
		return err // the deferred Close releases the advisory lock
	}
	_ = unix.Flock(leaf, unix.LOCK_UN) // release promptly; deferred Close is a backstop
	return nil
}

// readFileSafe reads path through the openat traversal from trustedRoot (each
// parent O_NOFOLLOW), opening the leaf O_RDONLY|O_NOFOLLOW|O_NONBLOCK and Fstat-
// rejecting anything but a single-linked regular file: a plain os.ReadFile of a
// FIFO-swapped path would BLOCK, while symlink/hard-link/special are refused.
// label names the artifact in errors. Shared by the audit-log and record readers.
func readFileSafe(trustedRoot, path, label string) ([]byte, error) {
	parentFD, err := openParentDirNoCreate(trustedRoot, filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	defer unix.Close(parentFD)
	leaf, err := unix.Openat(parentFD, filepath.Base(path), unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open %s %q: %w", label, path, err)
	}
	handle := os.NewFile(uintptr(leaf), path)
	defer handle.Close()
	var st unix.Stat_t
	if err := unix.Fstat(leaf, &st); err != nil {
		return nil, fmt.Errorf("stat %s %q: %w", label, path, err)
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG {
		return nil, fmt.Errorf("%s %q is not a regular file", label, path)
	}
	if st.Nlink != 1 {
		return nil, fmt.Errorf("%s %q is multiply linked (%d links)", label, path, st.Nlink)
	}
	return io.ReadAll(handle)
}

// readAuditLog reads the audit log through readFileSafe.
// readAuditLog reads the audit log through the hardened reader. If the read fails ONLY
// because the log is write-only (EACCES — a crash after O_CREAT under a read-masking umask
// left the read bit off before appendAuditLine's Fchmod), it heals the mode to 0o600 once
// and re-reads, so recovery reads are not stranded. Every other error (ErrNotExist and the
// no-follow/regular/Nlink refusals) passes through, and the heal never creates state.
func readAuditLog(trustedRoot, path string) ([]byte, error) {
	data, err := readFileSafe(trustedRoot, path, "audit log")
	if err == nil || !errors.Is(err, os.ErrPermission) {
		return data, err
	}
	if herr := healAuditLogModeForRead(trustedRoot, path); herr != nil {
		return nil, herr
	}
	return readFileSafe(trustedRoot, path, "audit log")
}

// healAuditLogModeForRead re-establishes owner read on an EXISTING write-only audit log
// (mode 0o200 — a crash after O_CREAT under a read-masking umask, before appendAuditLine's
// Fchmod). Opens it O_WRONLY (which a 0o200 log still permits) via the NO-CREATE parent
// opener + O_NOFOLLOW — a read-path heal must never create a missing parent or log — then
// re-validates single-linked regular and Fchmods 0o600 so the re-read succeeds. A missing
// component errors rather than being created.
func healAuditLogModeForRead(trustedRoot, path string) error {
	parentFD, err := openParentDirNoCreate(trustedRoot, filepath.Dir(path))
	if err != nil {
		return err
	}
	defer unix.Close(parentFD)
	fd, err := unix.Openat(parentFD, filepath.Base(path), unix.O_WRONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("open audit log %q for mode heal: %w", path, err)
	}
	defer unix.Close(fd)
	if err := validateAuditLogFd(fd, path); err != nil {
		return err
	}
	if err := unix.Fchmod(fd, 0o600); err != nil {
		return fmt.Errorf("chmod audit log %q: %w", path, err)
	}
	return nil
}

// readSessionRecordSafe reads and decodes the session record through readFileSafe
// (openat O_NOFOLLOW, Fstat regular/Nlink==1), so a FIFO record cannot HANG the
// caller and a symlinked/hard-linked record is refused instead of followed —
// mirroring readAuditLog. Decoding reuses sessions' shared parse/validation.
func readSessionRecordSafe(trustedRoot, recordPath string) (sessions.SessionRecord, error) {
	data, err := readFileSafe(trustedRoot, recordPath, "session record")
	if err != nil {
		return sessions.SessionRecord{}, err
	}
	return sessions.DecodeSessionRecord(data, recordPath)
}

// statPathSafe stats path through the openat traversal from trustedRoot (each
// parent component O_NOFOLLOW), then Fstatat-s the leaf with AT_SYMLINK_NOFOLLOW,
// so a parent swapped for a symlink after an earlier check cannot redirect the
// stat and a symlinked leaf reports as a symlink (not its target). Returns the
// leaf's stat for a type/existence check without a fresh path-based Lstat.
func statPathSafe(trustedRoot, path string) (unix.Stat_t, error) {
	var st unix.Stat_t
	parentFD, err := openParentDirNoCreate(trustedRoot, filepath.Dir(path))
	if err != nil {
		return st, err
	}
	defer unix.Close(parentFD)
	if err := unix.Fstatat(parentFD, filepath.Base(path), &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return st, fmt.Errorf("stat %q: %w", path, err)
	}
	return st, nil
}

// filterAuditSessionLines returns the non-empty lines whose session_id equals
// sessionID (mirrors sessions.SessionAuditRecords over readAuditLog's bytes).
func filterAuditSessionLines(data []byte, sessionID string) []string {
	var out []string
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		for _, tok := range strings.Fields(line) {
			if k, v, ok := strings.Cut(tok, "="); ok && k == "session_id" && v == sessionID {
				out = append(out, line)
				break
			}
		}
	}
	return out
}

// recordCreateFailpoint, when set (tests only), forces the staged write to fail so
// the unlink-on-failure cleanup can be exercised; nil in production.
var recordCreateFailpoint error

// removeRecordAtomic unlinks the record relative to the openat-verified parent fd,
// so a rollback that removes a just-created record cannot be redirected through a
// parent swapped for a symlink between the create and the remove.
func removeRecordAtomic(stateRoot, recordPath string) error {
	parentFD, err := openParentDirNoCreate(stateRoot, filepath.Dir(recordPath))
	if err != nil {
		return err
	}
	defer unix.Close(parentFD)
	if err := unix.Unlinkat(parentFD, filepath.Base(recordPath), 0); err != nil {
		return fmt.Errorf("remove session record %q: %w", recordPath, err)
	}
	return nil
}

// stageRecordTemp writes data to a freshly-named temp in the verified parent dir
// (O_CREAT|O_EXCL|O_NOFOLLOW), fsyncs it, and returns its name for a subsequent
// rename. The name suffix is random (crypto/rand) so a crash-left or attacker
// pre-created predictable temp cannot block the write; an O_EXCL collision retries
// with a fresh name. The temp is unlinked on any write/sync/close failure.
func stageRecordTemp(parentFD int, base string, data []byte) (string, error) {
	for attempt := 0; attempt < 8; attempt++ {
		var rnd [12]byte
		if _, err := rand.Read(rnd[:]); err != nil {
			return "", fmt.Errorf("random temp name: %w", err)
		}
		tmp := base + ".tmp-" + hex.EncodeToString(rnd[:])
		tfd, err := unix.Openat(parentFD, tmp, unix.O_CREAT|unix.O_EXCL|unix.O_WRONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
		if err == unix.EEXIST {
			continue // name already taken → try another random name
		}
		if err != nil {
			return "", fmt.Errorf("create temp session record %q: %w", tmp, err)
		}
		tf := os.NewFile(uintptr(tfd), tmp)
		// Openat's mode is umask-masked; Fchmod the fd to force 0o600 regardless of
		// the process umask, so the published record can't end up unreadable.
		if err := unix.Fchmod(tfd, 0o600); err != nil {
			tf.Close()
			_ = unix.Unlinkat(parentFD, tmp, 0)
			return "", fmt.Errorf("chmod temp session record %q: %w", tmp, err)
		}
		writeErr := recordCreateFailpoint
		if writeErr == nil {
			_, writeErr = tf.Write(data)
		}
		if writeErr == nil {
			writeErr = tf.Sync() // flush before the rename (as writeFileAtomically does)
		}
		if writeErr != nil {
			tf.Close()
			_ = unix.Unlinkat(parentFD, tmp, 0)
			return "", writeErr
		}
		if err := tf.Close(); err != nil {
			_ = unix.Unlinkat(parentFD, tmp, 0)
			return "", err
		}
		return tmp, nil
	}
	return "", fmt.Errorf("could not stage a unique temp for %q", base)
}

// stagedRecordWrite publishes data at recordPath by staging a complete, fsynced
// temp and Renameat-ing the final name onto it, so the final `.json` only ever
// appears atomically with full content (no empty/partial window a concurrent
// ListSessionRecords could observe) AND is always Nlink==1 — rename moves the
// inode with no second link, so it stays compatible with the readers' hard-link
// (Nlink==1) defense.
//
// This is atomic-PUBLISH, NOT atomically create-once: Renameat replaces an
// existing final name. Primitive-level atomic create-once on Darwin would require
// linkat, which transiently exposes a SECOND hard link (temp+final) that the
// Nlink==1 read guard would reject — the two are mutually exclusive on Darwin
// (no RENAME_NOREPLACE). So create-once is the CALLER's responsibility via
// serialization: the applecontainer session lifecycle holds a per-session flock
// and performs an under-lock record-exists check before creating. The per-session
// lock is the correct layer for that guarantee.
//
// mustExist=true (rewrite) additionally requires an existing single-linked regular
// file (refuse symlink/FIFO/hard-link). mustExist=false (create) keeps a cheap
// pre-check for a clear "already exists" error, but that check races the rename —
// the caller's lock is the real create-once guard. The temp is removed on failure.
func stagedRecordWrite(parentFD int, recordPath string, data []byte, mustExist bool) error {
	base := filepath.Base(recordPath)
	vfd, err := unix.Openat(parentFD, base, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if mustExist {
		if err != nil {
			return fmt.Errorf("open session record %q: %w", recordPath, err)
		}
		var st unix.Stat_t
		statErr := unix.Fstat(vfd, &st)
		unix.Close(vfd)
		if statErr != nil {
			return fmt.Errorf("stat session record %q: %w", recordPath, statErr)
		}
		if st.Mode&unix.S_IFMT != unix.S_IFREG {
			return fmt.Errorf("session record %q is not a regular file", recordPath)
		}
		if st.Nlink != 1 {
			return fmt.Errorf("session record %q is multiply linked (%d links)", recordPath, st.Nlink)
		}
	} else if err == nil {
		unix.Close(vfd)
		return fmt.Errorf("session record %q already exists", recordPath)
	} else if err != unix.ENOENT {
		return fmt.Errorf("check session record %q: %w", recordPath, err)
	}
	tmp, err := stageRecordTemp(parentFD, base, data)
	if err != nil {
		return err
	}
	if err := unix.Renameat(parentFD, tmp, parentFD, base); err != nil {
		_ = unix.Unlinkat(parentFD, tmp, 0)
		return fmt.Errorf("rename temp session record over %q: %w", recordPath, err)
	}
	return nil
}

// writeRecordBytesAtomic publishes data at the session record through the hardened
// openat path via stage-and-rename (final always Nlink==1, complete content). It
// is atomic-PUBLISH, not atomically create-once — a caller needing create-once
// (create=true) must serialize creates for the record (see stagedRecordWrite).
func writeRecordBytesAtomic(stateRoot, recordPath string, data []byte, create bool) error {
	parentFD, err := openAuditParent(stateRoot, filepath.Dir(recordPath))
	if err != nil {
		return err
	}
	defer unix.Close(parentFD)
	return stagedRecordWrite(parentFD, recordPath, data, !create)
}

// writeSessionRecordAtomic serializes the record via sessions.EncodeSessionRecordFrom
// — reusing its merge and \r\n/required-field validation — then writes the
// validated bytes through the hardened openat path. For a rewrite it reads the
// existing record through readFileSafe (openat/O_NOFOLLOW/O_NONBLOCK/Fstat) rather
// than letting the encode do an unhardened os.ReadFile, so NO read on the write
// path can hang on a FIFO or follow a symlinked record. A create has no existing
// record to read.
func writeSessionRecordAtomic(stateRoot, recordPath string, updates map[string]string, create bool) error {
	var existing []byte
	if !create {
		data, err := readFileSafe(stateRoot, recordPath, "session record")
		if err != nil {
			return err
		}
		existing = data
	}
	data, err := sessions.EncodeSessionRecordFrom(existing, updates)
	if err != nil {
		return err
	}
	return writeRecordBytesAtomic(stateRoot, recordPath, data, create)
}
