// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package applecontainer

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/omkhar/workcell/internal/host/sessions"
)

// ioValidUpdates returns a complete, valid non-terminal session-record update map
// (all required fields set) so the record writer can be exercised without the
// session-lifecycle code that lives in the branch above.
func ioValidUpdates() map[string]string {
	c := DefaultContract()
	return map[string]string{
		"session_id":             "sid",
		"profile":                "tid",
		"target_kind":            c.TargetKind,
		"target_provider":        c.TargetProvider,
		"target_id":              "tid",
		"target_assurance_class": c.TargetAssuranceClass,
		"runtime_api":            c.RuntimeAPI,
		"workspace_transport":    c.WorkspaceTransport,
		"agent":                  "codex",
		"mode":                   "strict",
		"status":                 "running",
		"workspace":              "/ws",
		"started_at":             "2026",
	}
}

// ioRecordSetup builds a canonical <stateRoot>/targets/.../sessions/ layout and
// returns the state root and the session record path (not yet created).
func ioRecordSetup(t *testing.T) (stateRoot, recordPath string) {
	t.Helper()
	stateRoot = t.TempDir()
	targetRoot := filepath.Join(stateRoot, "targets", "local_vm", "apple-container", "tid")
	recordPath = filepath.Join(targetRoot, "sessions", "sid.json")
	mustNil(t, os.MkdirAll(filepath.Dir(recordPath), 0o755))
	return stateRoot, recordPath
}

// TestIOWriteAndReadSessionRecord: the atomic create writer produces a record the
// hardened reader decodes back with its fields intact.
func TestIOWriteAndReadSessionRecord(t *testing.T) {
	t.Parallel()
	stateRoot, recordPath := ioRecordSetup(t)
	mustNil(t, writeSessionRecordAtomic(stateRoot, recordPath, ioValidUpdates(), true))
	rec, err := readSessionRecordSafe(stateRoot, recordPath)
	mustNil(t, err)
	if rec.SessionID != "sid" || rec.Status != "running" || rec.TargetID != "tid" {
		t.Fatalf("record round-trip mismatch: %+v", rec)
	}
}

// TestIOCreateOnce: a second create on an existing record fails (Linkat EEXIST).
func TestIOCreateOnce(t *testing.T) {
	t.Parallel()
	stateRoot, recordPath := ioRecordSetup(t)
	mustNil(t, writeSessionRecordAtomic(stateRoot, recordPath, ioValidUpdates(), true))
	if err := writeSessionRecordAtomic(stateRoot, recordPath, ioValidUpdates(), true); err == nil {
		t.Fatalf("create=true overwrote an existing record")
	}
}

// TestIOCreateOnceSerialized: the primitive is atomic-publish, NOT create-once on
// its own — create-once is the caller's responsibility via serialization. Under a
// mutex (simulating the session flock) concurrent creates for the same record
// yield exactly one success, and the published record parses fully.
func TestIOCreateOnceSerialized(t *testing.T) {
	t.Parallel()
	stateRoot, recordPath := ioRecordSetup(t)
	const n = 8
	var wg sync.WaitGroup
	var lock sync.Mutex // stands in for the per-session flock
	var count sync.Mutex
	ok := 0
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			lock.Lock()
			err := writeSessionRecordAtomic(stateRoot, recordPath, ioValidUpdates(), true)
			lock.Unlock()
			if err == nil {
				count.Lock()
				ok++
				count.Unlock()
			}
		}()
	}
	close(start)
	wg.Wait()
	if ok != 1 {
		t.Fatalf("serialized creates: %d succeeded, want exactly 1", ok)
	}
	if _, err := readSessionRecordSafe(stateRoot, recordPath); err != nil {
		t.Fatalf("record does not parse after serialized creates: %v", err)
	}
}

// TestIOCreateFinalIsSingleLinked: stage-and-rename keeps the published record
// Nlink==1 (rename moves the inode, no second link), so the readers' hard-link
// defense accepts it. A linkat-without-unlink create would leave Nlink==2 and be
// rejected by the reader.
func TestIOCreateFinalIsSingleLinked(t *testing.T) {
	t.Parallel()
	stateRoot, recordPath := ioRecordSetup(t)
	mustNil(t, writeSessionRecordAtomic(stateRoot, recordPath, ioValidUpdates(), true))
	var st unix.Stat_t
	mustNil(t, unix.Stat(recordPath, &st))
	if st.Nlink != 1 {
		t.Fatalf("published record has Nlink=%d, want 1 (readers reject Nlink!=1)", st.Nlink)
	}
	if _, err := readSessionRecordSafe(stateRoot, recordPath); err != nil {
		t.Fatalf("hardened reader rejected the published record: %v", err)
	}
}

// TestIORewriteReplacesInode: a rewrite stages a temp and renames it over the
// record, so the inode changes (atomic replacement, no truncate-in-place).
func TestIORewriteReplacesInode(t *testing.T) {
	t.Parallel()
	stateRoot, recordPath := ioRecordSetup(t)
	mustNil(t, writeSessionRecordAtomic(stateRoot, recordPath, ioValidUpdates(), true))
	var before unix.Stat_t
	mustNil(t, unix.Stat(recordPath, &before))
	mustNil(t, writeSessionRecordAtomic(stateRoot, recordPath, map[string]string{"observed_at": "2099"}, false))
	var after unix.Stat_t
	mustNil(t, unix.Stat(recordPath, &after))
	if before.Ino == after.Ino {
		t.Fatalf("rewrite kept the same inode %d (not stage-and-rename)", before.Ino)
	}
}

// TestIOReadFileSafeRejectsSymlink: readFileSafe refuses a symlinked leaf.
func TestIOReadFileSafeRejectsSymlink(t *testing.T) {
	t.Parallel()
	stateRoot, recordPath := ioRecordSetup(t)
	evil := filepath.Join(t.TempDir(), "evil")
	mustNil(t, os.WriteFile(evil, []byte("x"), 0o600))
	mustNil(t, os.Symlink(evil, recordPath))
	if _, err := readFileSafe(stateRoot, recordPath, "session record"); err == nil {
		t.Fatalf("readFileSafe followed a symlink")
	}
}

// TestIOStatPathSafeRejectsSymlinkedLeaf: statPathSafe reports a symlinked leaf as
// a symlink (not its target), so a caller requiring a directory refuses it.
func TestIOStatPathSafeRejectsSymlinkedLeaf(t *testing.T) {
	t.Parallel()
	stateRoot := t.TempDir()
	dir := filepath.Join(stateRoot, "targets", "local_vm", "apple-container", "tid")
	mustNil(t, os.MkdirAll(dir, 0o755))
	realDir := filepath.Join(t.TempDir(), "real")
	mustNil(t, os.MkdirAll(realDir, 0o755))
	link := filepath.Join(dir, "ws")
	mustNil(t, os.Symlink(realDir, link))
	st, err := statPathSafe(stateRoot, link)
	mustNil(t, err)
	if st.Mode&unix.S_IFMT == unix.S_IFDIR {
		t.Fatalf("statPathSafe followed a symlinked leaf to a directory")
	}
	if st.Mode&unix.S_IFMT != unix.S_IFLNK {
		t.Fatalf("expected symlink mode, got %o", st.Mode&unix.S_IFMT)
	}
}

// TestIORejectSymlinkChain: a symlinked directory in the chain is rejected; a
// clean chain passes.
func TestIORejectSymlinkChain(t *testing.T) {
	t.Parallel()
	stateRoot := t.TempDir()
	dir := filepath.Join(stateRoot, "targets", "local_vm", "apple-container", "tid", "sessions")
	mustNil(t, os.MkdirAll(dir, 0o755))
	if err := rejectSymlinkChain(stateRoot, dir); err != nil {
		t.Fatalf("clean chain rejected: %v", err)
	}
	provider := filepath.Join(stateRoot, "targets", "local_vm", "apple-container")
	aside := provider + ".real"
	mustNil(t, os.Rename(provider, aside))
	mustNil(t, os.Symlink(aside, provider))
	if err := rejectSymlinkChain(stateRoot, dir); err == nil {
		t.Fatalf("symlinked chain component accepted")
	}
}

// TestIOStateRootFor: stateRootFor strips the four target-managed segments.
func TestIOStateRootFor(t *testing.T) {
	t.Parallel()
	root := "/s"
	tr := filepath.Join(root, "targets", "local_vm", "apple-container", "tid")
	if got := stateRootFor(tr); got != root {
		t.Fatalf("stateRootFor(%q) = %q, want %q", tr, got, root)
	}
}

// TestIOIsTerminalSessionStatus: the exported terminal predicate covers the full
// terminal set and nothing else.
func TestIOIsTerminalSessionStatus(t *testing.T) {
	t.Parallel()
	for _, s := range []string{"exited", "failed", "aborted"} {
		if !sessions.IsTerminalSessionStatus(s) {
			t.Fatalf("%q should be terminal", s)
		}
	}
	for _, s := range []string{"running", "starting", "stopping", ""} {
		if sessions.IsTerminalSessionStatus(s) {
			t.Fatalf("%q should not be terminal", s)
		}
	}
}

// TestIOStatPathSafeRejectsSymlinkedParent: statPathSafe refuses a leaf whose
// PARENT directory is a symlink (openat O_NOFOLLOW per component), where a
// path-based os.Lstat would FOLLOW the parent and stat the target — this is the
// workspace-check hardening (Fix 1).
func TestIOStatPathSafeRejectsSymlinkedParent(t *testing.T) {
	t.Parallel()
	stateRoot := t.TempDir()
	matDir := filepath.Join(stateRoot, "targets", "local_vm", "apple-container", "tid", "materializations")
	mustNil(t, os.MkdirAll(matDir, 0o755))
	real := filepath.Join(t.TempDir(), "attacker")
	mustNil(t, os.MkdirAll(filepath.Join(real, "workspace"), 0o755))
	idLink := filepath.Join(matDir, "mid")
	mustNil(t, os.Symlink(real, idLink))
	// Precondition: a path-based Lstat WOULD follow the symlinked parent to the dir.
	if info, err := os.Lstat(filepath.Join(idLink, "workspace")); err != nil || !info.IsDir() {
		t.Fatalf("precondition: os.Lstat should follow the symlinked parent (err=%v)", err)
	}
	if _, err := statPathSafe(stateRoot, filepath.Join(idLink, "workspace")); err == nil {
		t.Fatalf("statPathSafe followed a symlinked parent directory")
	}
}

// TestIOStatPathSafeMissingParentCreatesNothing: a read/stat must not side-effect
// the filesystem — statPathSafe on a path with an absent parent errors and does
// NOT create the parent directory.
func TestIOStatPathSafeMissingParentCreatesNothing(t *testing.T) {
	t.Parallel()
	stateRoot := t.TempDir()
	tidDir := filepath.Join(stateRoot, "targets", "local_vm", "apple-container", "tid")
	mustNil(t, os.MkdirAll(tidDir, 0o755))
	missingParent := filepath.Join(tidDir, "materializations", "mid")
	if _, err := statPathSafe(stateRoot, filepath.Join(missingParent, "workspace")); err == nil {
		t.Fatalf("statPathSafe on a missing parent did not error")
	}
	if _, err := os.Stat(missingParent); !os.IsNotExist(err) {
		t.Fatalf("statPathSafe created the missing parent (a stat must not): %v", err)
	}
}

// TestIOCreateLeavesNoTemp: the create publishes via stage-and-rename and leaves
// no staged temp behind; the final record is complete and parses.
func TestIOCreateLeavesNoTemp(t *testing.T) {
	t.Parallel()
	stateRoot, recordPath := ioRecordSetup(t)
	mustNil(t, writeSessionRecordAtomic(stateRoot, recordPath, ioValidUpdates(), true))
	entries, err := os.ReadDir(filepath.Dir(recordPath))
	mustNil(t, err)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Fatalf("create left a staged temp file: %s", e.Name())
		}
	}
	if _, err := readSessionRecordSafe(stateRoot, recordPath); err != nil {
		t.Fatalf("published record does not parse: %v", err)
	}
}

// TestIOStagePredictableTempNameDoesNotBlock: a squatter at the OLD predictable
// temp name (<base>.tmp-<pid>-<counter>) no longer blocks the write, because the
// staged temp name is random (Fix 2). Neutralizing to a predictable name makes the
// O_EXCL create collide → this fails.
func TestIOStagePredictableTempNameDoesNotBlock(t *testing.T) {
	t.Parallel()
	stateRoot, recordPath := ioRecordSetup(t)
	predictable := recordPath + ".tmp-" + strconv.Itoa(os.Getpid()) + "-1"
	mustNil(t, os.WriteFile(predictable, []byte("squatter"), 0o600))
	mustNil(t, writeSessionRecordAtomic(stateRoot, recordPath, ioValidUpdates(), true))
	rec, err := readSessionRecordSafe(stateRoot, recordPath)
	mustNil(t, err)
	if rec.SessionID != "sid" {
		t.Fatalf("record round-trip mismatch after a temp-name squat")
	}
}

// TestIOStagedRecordModeUmaskIndependent: the published record is 0o600 regardless
// of a restrictive umask (Fchmod forces the mode on the fd). NOT parallel — umask
// is process-global; it is set after the dir setup and restored via defer, and Go
// pauses t.Parallel() tests while a non-parallel test runs, so siblings are unaffected.
func TestIOStagedRecordModeUmaskIndependent(t *testing.T) {
	stateRoot, recordPath := ioRecordSetup(t) // dirs created under the normal umask
	old := syscall.Umask(0o777)               // would mask a plain create to mode 0
	defer syscall.Umask(old)
	mustNil(t, writeSessionRecordAtomic(stateRoot, recordPath, ioValidUpdates(), true))
	info, err := os.Stat(recordPath)
	mustNil(t, err)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("record mode = %o under a restrictive umask, want 0o600 (umask-masked?)", info.Mode().Perm())
	}
}
