// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package applecontainer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/omkhar/workcell/internal/host/sessions"
)

// newSessionFixture builds a target with one materialization ("mid") and one
// bootstrap ("bid") under a fresh temp state root — the shared preamble of
// every hostile-fs test here. State root is recoverable via stateRootFor.
func newSessionFixture(t *testing.T) (AppleContainerTarget, MaterializeResult, BootstrapResult) {
	t.Helper()
	ctx := context.Background()
	target, err := NewAppleContainerTarget(Contract{})
	mustNil(t, err)
	state := t.TempDir()
	mat, err := target.MaterializeWorkspace(ctx, MaterializeRequest{StateRoot: state, TargetID: "tid", MaterializationID: "mid", SourceWorkspace: writeSampleWorkspace(t)})
	mustNil(t, err)
	boot, err := target.BootstrapTarget(ctx, BootstrapRequest{StateRoot: state, TargetID: "tid", BootstrapID: "bid", ImageRef: "img:1"})
	mustNil(t, err)
	return target, mat, boot
}

// startReq is the StartSessionRequest literal shared by these tests, parameterized
// only by the session id.
func startReq(sid string, mat MaterializeResult, boot BootstrapResult) StartSessionRequest {
	return StartSessionRequest{SessionID: sid, Agent: "codex", Mode: "strict", StartedAt: "2026", Materialization: mat, Bootstrap: boot}
}

// mustNotHang fails the test if f blocks past 5s (an unhardened open/read of a
// FIFO would hang); returns f's error for the caller's own assertions.
func mustNotHang(t *testing.T, hangMsg string, f func() error) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- f() }()
	select {
	case e := <-done:
		return e
	case <-time.After(5 * time.Second):
		t.Fatal(hangMsg)
		return nil
	}
}

// mkfifoOrSkip creates a FIFO at path or skips the test if the runner lacks
// Mkfifo support.
func mkfifoOrSkip(t *testing.T, path string) {
	t.Helper()
	if err := unix.Mkfifo(path, 0o600); err != nil {
		t.Skipf("Mkfifo unavailable on this runner: %v", err)
	}
}

// validRecordFields is an encode-passing SessionRecord field map: hostile-fs
// tests use it so a neutralized guard that followed the object would DECODE
// successfully, isolating the guard as the only thing that can fail the test.
func validRecordFields() map[string]string {
	return map[string]string{
		"session_id": "sid", "profile": "tid", "agent": "codex", "mode": "strict",
		"status": "running", "workspace": "/ws", "started_at": "2026",
	}
}

// TestStartSessionRefusesUnreadableLogBeforePublish: a pre-existing UNREADABLE audit log
// (a symlink the hardened reader rejects) is present-but-unusable, so fresh-start refuses
// at the pre-publish read and publishes NO record; once the log is genuinely absent, a
// fresh start proceeds.
func TestStartSessionRefusesUnreadableLogBeforePublish(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	target, mat, boot := newSessionFixture(t)
	req := startReq("sid", mat, boot)

	// A pre-existing UNREADABLE audit log (here a symlink the hardened reader rejects) is
	// present-but-unusable, not absent: fresh-start must REFUSE at the pre-publish read and
	// leave NO record behind — never publish a running record over a hostile/unusable log.
	mustNil(t, os.MkdirAll(filepath.Dir(boot.AuditLogPath), 0o755))
	mustNil(t, os.Symlink(filepath.Join(t.TempDir(), "evil.log"), boot.AuditLogPath))
	_, e := target.StartSession(ctx, req)
	if e == nil || !strings.Contains(e.Error(), "present but unreadable") {
		t.Fatalf("expected present-but-unreadable refusal for a symlinked audit log, got: %v", e)
	}
	recordPath := filepath.Join(boot.TargetRoot, "sessions", "sid.json")
	if _, serr := os.Stat(recordPath); !os.IsNotExist(serr) {
		t.Fatalf("a record was published over an unreadable audit log (fail-open): %v", serr)
	}

	// Remove the symlink so the log is now genuinely ABSENT: a fresh start then succeeds.
	mustNil(t, os.Remove(boot.AuditLogPath))
	if _, e := target.StartSession(ctx, req); e != nil {
		t.Fatalf("fresh start over a genuinely absent log rejected: %v", e)
	}
	data, _ := os.ReadFile(boot.AuditLogPath)
	for _, ev := range []string{"event=workspace_materialized", "event=bootstrap_ready", "event=session_started "} {
		if n := strings.Count(string(data), ev); n != 1 {
			t.Fatalf("recovery produced %q count=%d (want 1):\n%s", ev, n, data)
		}
	}
}

// TestFinishSessionRollsBackOnAuditFailure: a symlinked audit log is rejected at
// the hardened read before finalization, so a retry with the real log finalizes.
func TestFinishSessionRollsBackOnAuditFailure(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	final := DefaultContract().Session.FinalStatus
	target, mat, boot := newSessionFixture(t)
	started, err := target.StartSession(ctx, startReq("sid", mat, boot))
	mustNil(t, err)
	finishReq := FinishSessionRequest{Started: started, FinishedAt: "2027", ExitStatus: "0"}

	// Replace the log with a symlink to a sidecar copy holding the start triplet:
	// the hardened reader (readAuditLog, O_NOFOLLOW) rejects the symlinked log
	// DETERMINISTICALLY (even as root, unlike a chmod) at the start-events check,
	// before the status is set final — so the session stays non-final and a retry
	// with the real log finalizes.
	content, err := os.ReadFile(started.AuditLogPath)
	mustNil(t, err)
	sidecar := started.AuditLogPath + ".real"
	mustNil(t, os.WriteFile(sidecar, content, 0o600))
	mustNil(t, os.Remove(started.AuditLogPath))
	mustNil(t, os.Symlink(sidecar, started.AuditLogPath))
	if _, e := target.FinishSession(ctx, finishReq); e == nil {
		t.Fatalf("finish accepted a symlinked audit log")
	}
	rec, err := sessions.ReadSessionRecord(started.RecordPath)
	mustNil(t, err)
	if rec.Status == final {
		t.Fatalf("record left final after audit failure (retry cannot finalize)")
	}

	// Restore the real log and retry: it must finalize.
	mustNil(t, os.Remove(started.AuditLogPath))
	mustNil(t, os.Rename(sidecar, started.AuditLogPath))
	if _, e := target.FinishSession(ctx, finishReq); e != nil {
		t.Fatalf("retry finalize rejected: %v", e)
	}
	rec2, err := sessions.ReadSessionRecord(started.RecordPath)
	mustNil(t, err)
	if rec2.Status != final {
		t.Fatalf("record not final after retry finalize: %q", rec2.Status)
	}
}

// TestStartSessionRejectsSymlinkedMaterializationDir: a symlinked materialization
// <id> dir redirecting the manifest read/workspace Lstat to an attacker tree with
// a copied manifest is rejected by the parent-chain check before the read.
func TestStartSessionRejectsSymlinkedMaterializationDir(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	target, mat, boot := newSessionFixture(t)
	// Move the real materialization tree aside (intact) and symlink <id> → it: the
	// pinned path and byte-compare still match, so only the guard rejects it.
	matDir := filepath.Join(mat.TargetRoot, "materializations", "mid")
	aside := filepath.Join(t.TempDir(), "attacker-mat")
	mustNil(t, os.Rename(matDir, aside))
	mustNil(t, os.Symlink(aside, matDir))
	if _, e := target.StartSession(ctx, startReq("sid", mat, boot)); e == nil {
		t.Fatalf("StartSession followed a symlinked materialization directory")
	} else if !strings.Contains(e.Error(), "symlink") {
		t.Fatalf("expected symlink rejection, got: %v", e)
	}
}

// TestFinishSessionRejectsFIFOAuditLog: an audit log replaced with a FIFO must be
// rejected promptly by the start-events READ (a plain os.ReadFile blocks forever)
// leaving the session non-final for a retry.
func TestFinishSessionRejectsFIFOAuditLog(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	final := DefaultContract().Session.FinalStatus
	target, mat, boot := newSessionFixture(t)
	started, err := target.StartSession(ctx, startReq("sid", mat, boot))
	mustNil(t, err)
	mustNil(t, os.Remove(started.AuditLogPath))
	mkfifoOrSkip(t, started.AuditLogPath)
	finishReq := FinishSessionRequest{Started: started, FinishedAt: "2027", ExitStatus: "0"}
	e := mustNotHang(t, "FinishSession hung reading a FIFO audit log", func() error {
		_, err := target.FinishSession(ctx, finishReq)
		return err
	})
	if e == nil {
		t.Fatalf("FinishSession accepted a FIFO audit log")
	}
	if !strings.Contains(e.Error(), "not a regular file") {
		t.Fatalf("expected not-a-regular-file rejection, got: %v", e)
	}
	if rec, err := sessions.ReadSessionRecord(started.RecordPath); err != nil {
		t.Fatal(err)
	} else if rec.Status == final {
		t.Fatalf("session finalized despite a FIFO audit log")
	}
}

// TestStartSessionRejectsSymlinkedSessionsDir: a $TargetRoot/sessions directory
// swapped for a symlink is rejected so the record write cannot be redirected.
func TestStartSessionRejectsSymlinkedSessionsDir(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	target, mat, boot := newSessionFixture(t)

	sessionsDir := filepath.Join(boot.TargetRoot, "sessions")
	mustNil(t, os.MkdirAll(filepath.Dir(sessionsDir), 0o755))
	mustNil(t, os.Symlink(t.TempDir(), sessionsDir))
	if _, e := target.StartSession(ctx, startReq("sid", mat, boot)); e == nil {
		t.Fatalf("start session accepted a symlinked sessions directory")
	}
}

// TestSessionRejectsSymlinkedTargetRootParent: a target-root parent swapped for a
// symlink is rejected by both StartSession and FinishSession.
func TestSessionRejectsSymlinkedTargetRootParent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	target, mat, boot := newSessionFixture(t)
	started, err := target.StartSession(ctx, startReq("sid", mat, boot))
	mustNil(t, err)

	// Swap the provider dir (a parent of the target root) for a symlink.
	providerDir := filepath.Dir(boot.TargetRoot) // <root>/targets/local_vm/apple-container
	aside := providerDir + ".real"
	mustNil(t, os.Rename(providerDir, aside))
	mustNil(t, os.Symlink(aside, providerDir))
	t.Cleanup(func() { _ = os.Remove(providerDir); _ = os.Rename(aside, providerDir) })

	if _, e := target.FinishSession(ctx, FinishSessionRequest{Started: started, FinishedAt: "2027", ExitStatus: "0"}); e == nil {
		t.Fatalf("finish accepted a symlinked target-root parent")
	}
	if _, e := target.StartSession(ctx, startReq("sid2", mat, boot)); e == nil {
		t.Fatalf("start accepted a symlinked target-root parent")
	}
}

// TestStartSessionRejectsSymlinkedWorkspace: a materialized workspace swapped for
// a symlink after MaterializeWorkspace returned is rejected (Lstat, not Stat).
func TestStartSessionRejectsSymlinkedWorkspace(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	target, mat, boot := newSessionFixture(t)

	evil := t.TempDir()
	mustNil(t, os.WriteFile(filepath.Join(evil, "secret"), []byte("x\n"), 0o644))
	mustNil(t, os.RemoveAll(mat.MaterializedWorkspace))
	mustNil(t, os.Symlink(evil, mat.MaterializedWorkspace))
	if _, e := target.StartSession(ctx, startReq("sid", mat, boot)); e == nil {
		t.Fatalf("start session accepted a symlinked materialized workspace")
	}
}

// TestStartSessionRejectsSymlinkedBootstrapDir: a symlinked bootstrap/<id> dir
// redirecting the bootstrap manifest read to an attacker tree is rejected.
func TestStartSessionRejectsSymlinkedBootstrapDir(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	target, mat, boot := newSessionFixture(t)
	// Move the real bootstrap tree aside (manifest intact) and symlink <id> → it: the
	// pinned path and byte-compare still match, so only the parent-chain guard rejects.
	bootDir := filepath.Join(boot.TargetRoot, "bootstrap", "bid")
	aside := filepath.Join(t.TempDir(), "attacker-boot")
	mustNil(t, os.Rename(bootDir, aside))
	mustNil(t, os.Symlink(aside, bootDir))
	if _, e := target.StartSession(ctx, startReq("sid", mat, boot)); e == nil {
		t.Fatalf("StartSession followed a symlinked bootstrap directory")
	} else if !strings.Contains(e.Error(), "symlink") {
		t.Fatalf("expected symlink rejection, got: %v", e)
	}
}

// startForRecordTest runs a full StartSession and returns the trusted state root
// and record path for exercising the atomic record writer directly (the record
// READ path is out of scope for the write-TOCTOU fix, so these unit-test the
// write guards without routing through FinishSession's earlier record read).
func startForRecordTest(t *testing.T) (stateRoot, recordPath string) {
	t.Helper()
	ctx := context.Background()
	target, mat, boot := newSessionFixture(t)
	_, err := target.StartSession(ctx, startReq("sid", mat, boot))
	mustNil(t, err)
	return stateRootFor(boot.TargetRoot), filepath.Join(boot.TargetRoot, "sessions", "sid.json")
}

// writeValidRecordFile writes an encode-passing SessionRecord to path. Hostile-fs tests
// put THIS (not an invalid partial map) behind a symlink/hardlink so a neutralized guard
// that followed the object would DECODE successfully and the write/read would SUCCEED —
// making the guard the ONLY thing that can make the test pass, not an incidental decode
// failure.
func writeValidRecordFile(t *testing.T, path string) {
	t.Helper()
	b, err := sessions.EncodeSessionRecordFrom(nil, validRecordFields())
	mustNil(t, err)
	mustNil(t, os.WriteFile(path, b, 0o600))
}

// TestWriteSessionRecordAtomicRejectsSymlinkedParent: the openat write refuses a
// symlinked sessions parent (no path re-resolution), closing the record-write
// TOCTOU independent of StartSession's fast pre-check.
// symlinkSessionsDirAside renames the sessions dir aside (keeping the real valid record) and
// replaces it with a symlink to the aside copy, then returns the aside path.
func symlinkSessionsDirAside(t *testing.T, sessionsDir string) string {
	t.Helper()
	aside := sessionsDir + ".real"
	mustNil(t, os.Rename(sessionsDir, aside))
	mustNil(t, os.Symlink(aside, sessionsDir))
	return aside
}

// validRecordMap is an encode-passing record map so create-path encode cannot be the reason
// a hostile-fs create fails — only the guard can.
func validRecordMap(sessionID string) map[string]string {
	return map[string]string{
		"session_id": sessionID, "profile": "tid", "agent": "codex", "mode": "strict",
		"status": "running", "workspace": "/ws", "started_at": "2026",
	}
}

// TestWriteSessionRecordAtomicRejectsSymlinkedParent: the WRITE path must reject a symlinked
// record parent. Uses create=true — the create path does NOT pre-read the existing record
// (no readFileSafe), so writeRecordBytesAtomic → openAuditParent is what walks the parent
// O_NOFOLLOW and rejects it (create=false would be rejected by the READ-existing parent walk
// FIRST, never exercising the writer's parent guard — the guard-isolation flaw). A FRESH
// (non-existent) record name behind the symlink so that, without the guard, the create would
// WRITE THROUGH the symlinked parent and SUCCEED — making the write-path parent guard the
// only possible rejecter. Rejection surfaces as ELOOP on Darwin, ENOTDIR on Linux.
func TestWriteSessionRecordAtomicRejectsSymlinkedParent(t *testing.T) {
	t.Parallel()
	stateRoot, recordPath := startForRecordTest(t)
	sessionsDir := filepath.Dir(recordPath)
	symlinkSessionsDirAside(t, sessionsDir)
	fresh := filepath.Join(sessionsDir, "new.json") // does not exist behind the symlink
	err := writeSessionRecordAtomic(stateRoot, fresh, validRecordMap("new"), true)
	if !errors.Is(err, unix.ELOOP) && !errors.Is(err, unix.ENOTDIR) {
		t.Fatalf("atomic create did not reject a symlinked record parent via O_NOFOLLOW: %v", err)
	}
}

// TestReadSessionRecordSafeRejectsSymlinkedParent: the hardened READ walks the parent
// O_NOFOLLOW and rejects a symlinked parent — isolated from the write path. The real valid
// record sits behind the symlink, so without O_NOFOLLOW the read would follow and DECODE it
// successfully; only the parent guard can make this fail.
func TestReadSessionRecordSafeRejectsSymlinkedParent(t *testing.T) {
	t.Parallel()
	stateRoot, recordPath := startForRecordTest(t)
	symlinkSessionsDirAside(t, filepath.Dir(recordPath))
	_, e := readSessionRecordSafe(stateRoot, recordPath)
	if !errors.Is(e, unix.ELOOP) && !errors.Is(e, unix.ENOTDIR) {
		t.Fatalf("hardened read did not reject a symlinked record parent via O_NOFOLLOW: %v", e)
	}
}

// TestWriteRecordAtomicRejectsSymlinkedLeaf: a record swapped for a symlink is
// refused on rewrite (O_NOFOLLOW), not followed to the target.
func TestWriteRecordAtomicRejectsSymlinkedLeaf(t *testing.T) {
	t.Parallel()
	stateRoot, recordPath := startForRecordTest(t)
	evil := filepath.Join(t.TempDir(), "evil.json")
	writeValidRecordFile(t, evil) // valid: only the symlink guard, not a decode/parse, can reject
	evilBefore, err := os.ReadFile(evil)
	mustNil(t, err)
	mustNil(t, os.Remove(recordPath))
	mustNil(t, os.Symlink(evil, recordPath))
	// The write's O_NOFOLLOW vfd rejects the symlinked leaf with ELOOP; without it the rename
	// would replace the symlink and succeed.
	werr := writeRecordBytesAtomic(stateRoot, recordPath, []byte("x"), false)
	if !errors.Is(werr, unix.ELOOP) {
		t.Fatalf("atomic rewrite did not reject a symlinked record file via O_NOFOLLOW: %v", werr)
	}
	if data, _ := os.ReadFile(evil); string(data) != string(evilBefore) {
		t.Fatalf("wrote through the symlink: %q", data)
	}
}

// TestWriteRecordAtomicRejectsFIFOLeaf: a record swapped for a FIFO is rejected
// promptly (O_NONBLOCK + Fstat), not blocked on.
func TestWriteRecordAtomicRejectsFIFOLeaf(t *testing.T) {
	t.Parallel()
	stateRoot, recordPath := startForRecordTest(t)
	mustNil(t, os.Remove(recordPath))
	mkfifoOrSkip(t, recordPath)
	if e := mustNotHang(t, "atomic rewrite blocked on a FIFO record", func() error {
		return writeRecordBytesAtomic(stateRoot, recordPath, []byte("x"), false)
	}); e == nil {
		t.Fatalf("atomic rewrite accepted a FIFO record")
	}
}

// TestWriteRecordAtomicRejectsHardlinkedLeaf: a hard-linked record (Nlink>1) is
// rejected so a rewrite cannot land in an attacker-linked inode.
func TestWriteRecordAtomicRejectsHardlinkedLeaf(t *testing.T) {
	t.Parallel()
	stateRoot, recordPath := startForRecordTest(t)
	// recordPath is the REAL valid record (Nlink now 2 via the hard link), so only the
	// Nlink==1 guard — not a decode — can reject the rewrite.
	mustNil(t, os.Link(recordPath, filepath.Join(t.TempDir(), "hard.json")))
	err := writeRecordBytesAtomic(stateRoot, recordPath, []byte("x"), false)
	if err == nil || !strings.Contains(err.Error(), "multiply linked") {
		t.Fatalf("atomic rewrite did not reject a hard-linked record via the Nlink guard: %v", err)
	}
}

// TestWriteSessionRecordAtomicCreateOnce: create=true on an existing record fails at the
// O_EXCL create step, matching StartSession's exactly-once creation. Uses a VALID record so
// EncodeSessionRecordFrom passes and the create path actually reaches the O_EXCL check
// (a partial/invalid map would be rejected at encode first — passing for the wrong reason).
func TestWriteSessionRecordAtomicCreateOnce(t *testing.T) {
	t.Parallel()
	stateRoot, recordPath := startForRecordTest(t)
	valid := validRecordFields()
	// Sanity: the SAME valid map creates cleanly when the target does NOT exist, so a
	// failure below can only come from the create-on-existing (O_EXCL) check, not encode.
	fresh := filepath.Join(filepath.Dir(recordPath), "fresh.json")
	mustNil(t, writeSessionRecordAtomic(stateRoot, fresh, valid, true))
	if err := writeSessionRecordAtomic(stateRoot, recordPath, valid, true); err == nil {
		t.Fatalf("create=true overwrote an existing record")
	}
}

// TestReadSessionRecordSafeRejectsFIFO: a record swapped for a FIFO is rejected
// promptly by the hardened reader (O_NONBLOCK), not blocked on like os.ReadFile.
func TestReadSessionRecordSafeRejectsFIFO(t *testing.T) {
	t.Parallel()
	stateRoot, recordPath := startForRecordTest(t)
	mustNil(t, os.Remove(recordPath))
	mkfifoOrSkip(t, recordPath)
	if e := mustNotHang(t, "hardened read blocked on a FIFO record", func() error {
		_, err := readSessionRecordSafe(stateRoot, recordPath)
		return err
	}); e == nil {
		t.Fatalf("hardened read accepted a FIFO record")
	}
}

// TestReadSessionRecordSafeRejectsSymlink: a symlinked record is refused
// (O_NOFOLLOW), not followed to the target.
func TestReadSessionRecordSafeRejectsSymlink(t *testing.T) {
	t.Parallel()
	stateRoot, recordPath := startForRecordTest(t)
	evil := filepath.Join(t.TempDir(), "evil.json")
	writeValidRecordFile(t, evil) // VALID: if O_NOFOLLOW were removed, the follow would decode fine
	mustNil(t, os.Remove(recordPath))
	mustNil(t, os.Symlink(evil, recordPath))
	// Must be rejected by the O_NOFOLLOW open (ELOOP), NOT by a decode failure — with a
	// valid target, only the symlink guard can make this fail.
	_, e := readSessionRecordSafe(stateRoot, recordPath)
	if !errors.Is(e, unix.ELOOP) {
		t.Fatalf("hardened read did not reject a symlinked record via O_NOFOLLOW: %v", e)
	}
}

// TestReadSessionRecordSafeRejectsHardlink: a hard-linked record (Nlink>1) is
// refused so a read cannot trust a multiply-linked inode.
func TestReadSessionRecordSafeRejectsHardlink(t *testing.T) {
	t.Parallel()
	stateRoot, recordPath := startForRecordTest(t)
	// recordPath is the REAL valid record (Nlink now 2), so only the Nlink==1 guard — not a
	// decode failure — can reject the read.
	mustNil(t, os.Link(recordPath, filepath.Join(t.TempDir(), "hard.json")))
	_, e := readSessionRecordSafe(stateRoot, recordPath)
	if e == nil || !strings.Contains(e.Error(), "multiply linked") {
		t.Fatalf("hardened read did not reject a hard-linked record via the Nlink guard: %v", e)
	}
}

// TestFinishSessionRejectsFIFORecord: a record swapped for a FIFO makes
// FinishSession's pre-finalize read reject promptly (no hang), not finalize.
func TestFinishSessionRejectsFIFORecord(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	target, mat, boot := newSessionFixture(t)
	started, err := target.StartSession(ctx, startReq("sid", mat, boot))
	mustNil(t, err)
	mustNil(t, os.Remove(started.RecordPath))
	mkfifoOrSkip(t, started.RecordPath)
	if e := mustNotHang(t, "FinishSession hung reading a FIFO record", func() error {
		_, err := target.FinishSession(ctx, FinishSessionRequest{Started: started, FinishedAt: "2027", ExitStatus: "0"})
		return err
	}); e == nil {
		t.Fatalf("FinishSession accepted a FIFO record")
	}
}

// TestRewriteRecordAtomicReplacesInode: a rewrite stages a temp and renames it
// over the record, so the record's inode CHANGES (atomic replacement) rather than
// being truncated in place — the property that avoids a truncate-then-lose window.
// The full atomic-rewrite postcondition is asserted on one fixture: inode changed
// + content persisted + no staged temp left lingering in the sessions dir.
func TestRewriteRecordAtomicReplacesInode(t *testing.T) {
	t.Parallel()
	stateRoot, recordPath := startForRecordTest(t)
	var before unix.Stat_t
	mustNil(t, unix.Stat(recordPath, &before))
	mustNil(t, writeSessionRecordAtomic(stateRoot, recordPath, map[string]string{"observed_at": "2099"}, false))
	var after unix.Stat_t
	mustNil(t, unix.Stat(recordPath, &after))
	if before.Ino == after.Ino {
		t.Fatalf("rewrite kept the same inode %d (truncated in place, not stage-and-rename)", before.Ino)
	}
	rec, err := sessions.ReadSessionRecord(recordPath)
	mustNil(t, err)
	if rec.ObservedAt != "2099" {
		t.Fatalf("rewrite did not persist: observed_at=%q", rec.ObservedAt)
	}
	entries, err := os.ReadDir(filepath.Dir(recordPath))
	mustNil(t, err)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Fatalf("rewrite left a staged temp file: %s", e.Name())
		}
	}
}

// TestWriteSessionRecordAtomicRewriteReadFIFO: the rewrite path reads the existing
// record to merge; a FIFO-swapped existing record must be rejected promptly by
// that read (hardened), not block the write. Neutralizing it (os.ReadFile inside
// the encode) makes this hang.
func TestWriteSessionRecordAtomicRewriteReadFIFO(t *testing.T) {
	t.Parallel()
	stateRoot, recordPath := startForRecordTest(t)
	mustNil(t, os.Remove(recordPath))
	mkfifoOrSkip(t, recordPath)
	if e := mustNotHang(t, "rewrite blocked reading a FIFO existing record", func() error {
		return writeSessionRecordAtomic(stateRoot, recordPath, map[string]string{"observed_at": "2099"}, false)
	}); e == nil {
		t.Fatalf("rewrite accepted a FIFO existing record")
	}
}

// TestWriteSessionRecordAtomicRewriteReadSymlink: the rewrite's read-existing
// refuses a symlinked existing record (O_NOFOLLOW), not following it.
func TestWriteSessionRecordAtomicRewriteReadSymlink(t *testing.T) {
	t.Parallel()
	stateRoot, recordPath := startForRecordTest(t)
	evil := filepath.Join(t.TempDir(), "evil.json")
	writeValidRecordFile(t, evil) // VALID: a followed read+merge would encode fine, so only the guard can fail
	evilBefore, err := os.ReadFile(evil)
	mustNil(t, err)
	mustNil(t, os.Remove(recordPath))
	mustNil(t, os.Symlink(evil, recordPath))
	// The rewrite's read-existing must be rejected by O_NOFOLLOW (ELOOP), not a decode
	// failure, and must not touch the symlink target.
	werr := writeSessionRecordAtomic(stateRoot, recordPath, map[string]string{"observed_at": "2099"}, false)
	if !errors.Is(werr, unix.ELOOP) {
		t.Fatalf("rewrite did not reject a symlinked existing record via O_NOFOLLOW: %v", werr)
	}
	if data, _ := os.ReadFile(evil); string(data) != string(evilBefore) {
		t.Fatalf("wrote through the symlinked existing record: %q", data)
	}
}

// TestStartSessionRejectsSymlinkedManifestFile: a manifest FILE (materialization
// or bootstrap) swapped for a symlink is refused by verifyPersistedManifest's
// hardened read, not followed to an attacker-planted manifest.
func TestStartSessionRejectsSymlinkedManifestFile(t *testing.T) {
	t.Parallel()
	for _, which := range []string{"materialization", "bootstrap"} {
		t.Run(which, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			target, mat, boot := newSessionFixture(t)
			mpath := mat.ManifestPath
			if which == "bootstrap" {
				mpath = boot.ManifestPath
			}
			// Copy the real manifest aside and replace the file with a symlink to it:
			// the byte-compare still matches through the symlink, so only the hardened
			// read (O_NOFOLLOW) rejects it.
			real, err := os.ReadFile(mpath)
			mustNil(t, err)
			aside := filepath.Join(t.TempDir(), "aside.json")
			mustNil(t, os.WriteFile(aside, real, 0o600))
			mustNil(t, os.Remove(mpath))
			mustNil(t, os.Symlink(aside, mpath))
			if _, e := target.StartSession(ctx, startReq("sid", mat, boot)); e == nil {
				t.Fatalf("StartSession followed a symlinked %s manifest file", which)
			}
		})
	}
}

// TestCreateRecordAtomicUnlinksOnWriteFailure: a create whose write fails must not
// leave a half-written record behind (unlink-on-failure).
func TestCreateRecordAtomicUnlinksOnWriteFailure(t *testing.T) {
	// Not parallel: mutates the package-level failpoint.
	stateRoot := t.TempDir()
	targetRoot := filepath.Join(stateRoot, "targets", "local_vm", "apple-container", "tid")
	recordPath := filepath.Join(targetRoot, "sessions", "sid.json")
	mustNil(t, os.MkdirAll(filepath.Dir(recordPath), 0o755))
	recordCreateFailpoint = errors.New("forced write failure")
	t.Cleanup(func() { recordCreateFailpoint = nil })
	if err := writeRecordBytesAtomic(stateRoot, recordPath, []byte("x"), true); err == nil {
		t.Fatalf("create with forced write failure did not error")
	}
	if _, err := os.Lstat(recordPath); !os.IsNotExist(err) {
		t.Fatalf("create left a leftover record after write failure: %v", err)
	}
	// The failed create must also unlink its staged temp (sid.json.tmp-<rand>).
	// This test created the sessions dir empty and the failed create is its only
	// writer, so the directory must contain NO entries at all afterwards. Removing
	// stageRecordTemp's unlink-on-failure (the Unlinkat in the writeErr branch)
	// leaves the temp behind and fails this.
	entries, err := os.ReadDir(filepath.Dir(recordPath))
	mustNil(t, err)
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("create failure left staged files in sessions dir: %v", names)
	}
}

// TestRemoveRecordAtomicRejectsSymlinkedParent: the atomic rollback remove refuses
// a symlinked record parent (openat), so it cannot unlink through a swapped dir.
func TestRemoveRecordAtomicRejectsSymlinkedParent(t *testing.T) {
	t.Parallel()
	stateRoot, recordPath := startForRecordTest(t)
	sessionsDir := filepath.Dir(recordPath)
	aside := sessionsDir + ".real"
	mustNil(t, os.Rename(sessionsDir, aside))
	mustNil(t, os.Symlink(aside, sessionsDir))
	if err := removeRecordAtomic(stateRoot, recordPath); err == nil {
		t.Fatalf("atomic remove followed a symlinked record parent")
	}
	if _, err := os.Lstat(filepath.Join(aside, "sid.json")); err != nil {
		t.Fatalf("real record removed through the symlink: %v", err)
	}
}

// TestLockSessionRejectsSymlinkedLockFile: a symlinked <id>.lock is refused
// (O_NOFOLLOW), so the lock cannot be redirected through a planted symlink.
func TestLockSessionRejectsSymlinkedLockFile(t *testing.T) {
	t.Parallel()
	stateRoot, recordPath := startForRecordTest(t)
	lockPath := strings.TrimSuffix(recordPath, ".json") + ".lock"
	_ = os.Remove(lockPath) // StartSession created it; replace with a symlink
	mustNil(t, os.Symlink(filepath.Join(t.TempDir(), "evil.lock"), lockPath))
	if _, err := lockSession(stateRoot, recordPath); err == nil {
		t.Fatalf("lockSession followed a symlinked lock file")
	}
}
