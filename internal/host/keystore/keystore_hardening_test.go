// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

// Exhaustive keystore hardening matrix (perms/symlink/FIFO/O_NOFOLLOW,
// open-then-fstat TOCTOU, *at-anchored writes, key-id binding, durability
// parent/ancestor fsync + racers, cross-platform stat, trailing slash). Uses the
// withFsyncPhaseFailure seam defined in keystore_test.go.

package keystore

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/x509"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestConcurrentKeyGenerationIsAtomic(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "signing")
	const n = 8
	ids := make([]string, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			_, id, err := LoadOrCreateSigningKey(dir)
			ids[i], errs[i] = id, err
		}(i)
	}
	wg.Wait()
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("goroutine %d: %v", i, errs[i])
		}
		if ids[i] != ids[0] {
			t.Fatalf("goroutines disagree on key id: %s != %s", ids[i], ids[0])
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, signingKeyBasename))
	if err != nil {
		t.Fatalf("read key: %v", err)
	}
	if _, err := parsePrivateKey(data); err != nil {
		t.Fatalf("published key must parse: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("leftover temp file %s", e.Name())
		}
	}
}

// TestLoadFailsOnFifoRegularKeyWithoutReading: a FIFO created directly as the key
// is refused as non-regular before any open (opening it would block forever).
func TestLoadFailsOnFifoRegularKeyWithoutReading(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "signing")
	if _, _, err := LoadOrCreateSigningKey(dir); err != nil {
		t.Fatalf("create: %v", err)
	}
	keyPath := filepath.Join(dir, signingKeyBasename)
	if err := os.Remove(keyPath); err != nil {
		t.Fatalf("remove key: %v", err)
	}
	if err := syscall.Mkfifo(keyPath, 0o600); err != nil {
		t.Skipf("mkfifo unsupported: %v", err)
	}
	if _, _, err := LoadOrCreateSigningKey(dir); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("a FIFO key must fail closed without opening, got %v", err)
	}
}

// TestLoadFailsOnSymlinkKeyToFifoWithoutReading: a key symlinked to a FIFO is
// refused by the lstat check WITHOUT being opened.
func TestLoadFailsOnSymlinkKeyToFifoWithoutReading(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "signing")
	if _, _, err := LoadOrCreateSigningKey(dir); err != nil {
		t.Fatalf("create: %v", err)
	}
	keyPath := filepath.Join(dir, signingKeyBasename)
	fifo := filepath.Join(t.TempDir(), "fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("mkfifo unsupported: %v", err)
	}
	if err := os.Remove(keyPath); err != nil {
		t.Fatalf("remove key: %v", err)
	}
	if err := os.Symlink(fifo, keyPath); err != nil {
		t.Fatalf("symlink key: %v", err)
	}
	if _, _, err := LoadOrCreateSigningKey(dir); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlink-to-FIFO key must fail closed without opening, got %v", err)
	}
}

// TestLoadFailsOnSymlinkSigningDir: a symlinked signing dir is refused by the
// O_NOFOLLOW|O_DIRECTORY open (before any fchmod), so the symlink TARGET's mode
// is never changed and no key is written through it. The open rejects a symlink
// as ELOOP or (with O_DIRECTORY) ENOTDIR depending on the platform; both are a
// fail-closed refusal.
func TestLoadFailsOnSymlinkSigningDir(t *testing.T) {
	tmp := t.TempDir()
	realDir := filepath.Join(tmp, "real")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	before, err := os.Stat(realDir)
	if err != nil {
		t.Fatalf("stat real: %v", err)
	}
	dir := filepath.Join(tmp, "signing")
	if err := os.Symlink(realDir, dir); err != nil {
		t.Fatalf("symlink dir: %v", err)
	}
	if _, _, err := LoadOrCreateSigningKey(dir); err == nil ||
		!(strings.Contains(err.Error(), "symlink") || strings.Contains(err.Error(), "not a directory")) {
		t.Fatalf("symlinked signing dir must fail closed, got %v", err)
	}
	// L135: the O_NOFOLLOW open must have refused the symlink WITHOUT chmoding the
	// target — its mode is unchanged.
	after, err := os.Stat(realDir)
	if err != nil {
		t.Fatalf("stat real after: %v", err)
	}
	if before.Mode().Perm() != after.Mode().Perm() {
		t.Fatalf("symlink target mode changed: %o -> %o (chmod followed the symlink)", before.Mode().Perm(), after.Mode().Perm())
	}
	if _, err := os.Stat(filepath.Join(realDir, signingKeyBasename)); !os.IsNotExist(err) {
		t.Fatalf("no key must be written through a symlinked dir (err=%v)", err)
	}
}

// TestCheckStatEnforcesOwner (L201): the owner check reads unix.Stat_t.Uid
// directly (no *unix.Stat_t type assertion on os.FileInfo.Sys(), which is
// *syscall.Stat_t on Linux and would silently skip the check), so it runs on
// every platform. A stat whose Uid != euid is rejected; the current euid passes.
func TestCheckStatEnforcesOwner(t *testing.T) {
	euid := uint32(os.Geteuid())

	var mine unix.Stat_t
	mine.Mode = unix.S_IFREG | 0o600
	mine.Uid = euid
	if err := checkStat("k", &mine, false, 0o077); err != nil {
		t.Fatalf("owner==euid must pass: %v", err)
	}

	var other unix.Stat_t
	other.Mode = unix.S_IFREG | 0o600
	other.Uid = euid + 1
	if err := checkStat("k", &other, false, 0o077); err == nil || !strings.Contains(err.Error(), "not owned by the current user") {
		t.Fatalf("wrong-owner stat must be rejected, got %v", err)
	}
}

// TestLoadFailsOnSymlinkSigningDirTrailingSlash (L181): a trailing separator must
// not let a final-component symlink slip past O_NOFOLLOW — the dir path is
// cleaned first.
func TestLoadFailsOnSymlinkSigningDirTrailingSlash(t *testing.T) {
	tmp := t.TempDir()
	realDir := filepath.Join(tmp, "real")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	before, err := os.Stat(realDir)
	if err != nil {
		t.Fatalf("stat real: %v", err)
	}
	link := filepath.Join(tmp, "signing")
	if err := os.Symlink(realDir, link); err != nil {
		t.Fatalf("symlink dir: %v", err)
	}
	if _, _, err := LoadOrCreateSigningKey(link + string(os.PathSeparator)); err == nil ||
		!(strings.Contains(err.Error(), "symlink") || strings.Contains(err.Error(), "not a directory")) {
		t.Fatalf("symlinked signing dir with trailing slash must fail closed, got %v", err)
	}
	after, err := os.Stat(realDir)
	if err != nil {
		t.Fatalf("stat real after: %v", err)
	}
	if before.Mode().Perm() != after.Mode().Perm() {
		t.Fatalf("symlink target mode changed: %o -> %o", before.Mode().Perm(), after.Mode().Perm())
	}
}

// TestPublishFailsOnDirFsyncError (L224): a directory-fsync writeback failure in
// the key or .pub publish must fail LoadOrCreateSigningKey closed.
func TestPublishFailsOnDirFsyncError(t *testing.T) {
	// Each phase runs as a subtest so its withFsyncPhaseFailure override is
	// restored by defer even if the assertion fails early (an escaped override
	// would corrupt fsyncDir for later tests). Sequential subtests also avoid the
	// override stacking that a shared-scope defer would introduce.
	t.Run("signing.key", func(t *testing.T) {
		defer withFsyncPhaseFailure("signing.key")()
		if _, _, err := LoadOrCreateSigningKey(filepath.Join(t.TempDir(), "signing")); err == nil ||
			!strings.Contains(err.Error(), "publishing signing.key") {
			t.Fatalf("private-key dir fsync failure must fail closed, got %v", err)
		}
	})

	t.Run("pub", func(t *testing.T) {
		defer withFsyncPhaseFailure("pub")()
		if _, _, err := LoadOrCreateSigningKey(filepath.Join(t.TempDir(), "signing")); err == nil ||
			!strings.Contains(err.Error(), ".pub") {
			t.Fatalf(".pub dir fsync failure must fail closed, got %v", err)
		}
	})
}

// TestExistingDirParentFsyncFailsClosed (L199): the EEXIST path (a dir that
// already exists — the concurrent first-use loser's path) must ALSO fsync the
// ancestor chain (whose first level is the parent) and fail closed if that fsync
// fails, so a loser never returns a keyID before the dir is durably linked.
func TestExistingDirParentFsyncFailsClosed(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "signing")
	if err := os.Mkdir(dir, 0o700); err != nil { // pre-create -> LoadOrCreate sees EEXIST
		t.Fatalf("pre-create: %v", err)
	}
	defer withFsyncPhaseFailure("ancestor")()
	if _, _, err := LoadOrCreateSigningKey(dir); err == nil || !strings.Contains(err.Error(), "fsync ancestor directory") {
		t.Fatalf("EEXIST-path ancestor fsync failure must fail closed, got %v", err)
	}
}

// TestSymlinkedAncestorIsFsynced (L230): the ancestor durability walk is
// fd-anchored (openat(fd, "..")), so the REAL directory behind a symlinked
// ancestor component IS fsynced — a path-string re-traversal would fail
// O_NOFOLLOW at the symlink and wrongly skip the real dir that gained a new child
// entry. Assert /real's inode is among the fsynced ancestors.
func TestSymlinkedAncestorIsFsynced(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatalf("mkdir real: %v", err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	var rst unix.Stat_t
	if err := unix.Stat(real, &rst); err != nil {
		t.Fatalf("stat real: %v", err)
	}
	orig := fsyncDir
	t.Cleanup(func() { fsyncDir = orig })
	synced := map[[2]uint64]bool{}
	fsyncDir = func(fd int, label string) error {
		if label == "ancestor" {
			var st unix.Stat_t
			if unix.Fstat(fd, &st) == nil {
				synced[[2]uint64{uint64(st.Dev), uint64(st.Ino)}] = true
			}
		}
		return orig(fd, label)
	}
	// dir is nested UNDER the symlink, with a freshly created real subtree.
	if _, _, err := LoadOrCreateSigningKey(filepath.Join(link, "a", "signing")); err != nil {
		t.Fatalf("create: %v", err)
	}
	if !synced[[2]uint64{uint64(rst.Dev), uint64(rst.Ino)}] {
		t.Fatal("the real directory behind the symlinked ancestor was not fsynced")
	}
}

// TestConcurrentFirstUseSucceeds (L199): two goroutines racing on the same fresh
// dir both succeed and agree on the key id/public key, with the concurrent
// first-use (EEXIST loser) path deterministically exercised.
//
// The firstUseBarrier seam is a 2-party rendezvous set only during this test:
// LoadOrCreateSigningKey calls it immediately before the atomic Linkat publish,
// AFTER the existing-key fast path. So a worker reaches the barrier only if it
// went down the create path; blocking until BOTH arrive guarantees both attempt
// the publish and exactly one observes EEXIST (the loser adopts). This removes
// the nondeterminism of a plain release barrier, where one worker could publish
// before the other left the fast-path check and the EEXIST path would never run.
func TestConcurrentFirstUseSucceeds(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "signing")

	var arrivals int32
	arrived := make(chan struct{}, 2)
	release := make(chan struct{})
	origBarrier := firstUseBarrier
	t.Cleanup(func() { firstUseBarrier = origBarrier })
	firstUseBarrier = func() {
		atomic.AddInt32(&arrivals, 1)
		arrived <- struct{}{}
		<-release // hold until both create-path racers have arrived
	}
	// Release both once BOTH create-path workers have arrived — but BOUND the
	// wait: a regression that serializes first-use (2nd worker takes the fast
	// path and never reaches the hook) or a worker erroring before the publish
	// point would otherwise block here forever, turning an actionable failure
	// into a package-level timeout. On the bound we still close release so any
	// arrived worker unblocks and no goroutine leaks; the arrivals!=2 assertion
	// below then reports the real regression with a clear message.
	rendezvous := make(chan struct{})
	go func() {
		defer close(rendezvous)
		defer close(release)
		select {
		case <-arrived:
		case <-time.After(5 * time.Second):
			return
		}
		select {
		case <-arrived:
		case <-time.After(5 * time.Second):
		}
	}()

	var wg sync.WaitGroup
	ids := make([]string, 2)
	pubs := make([][]byte, 2)
	errs := make([]error, 2)
	wg.Add(2)
	for i := 0; i < 2; i++ {
		go func(i int) {
			defer wg.Done()
			var priv *ecdsa.PrivateKey
			priv, ids[i], errs[i] = LoadOrCreateSigningKey(dir)
			if errs[i] == nil {
				pubs[i], errs[i] = x509.MarshalPKIXPublicKey(&priv.PublicKey)
			}
		}(i)
	}
	wg.Wait()
	<-rendezvous // ensure the release goroutine has exited (no leak)
	for i := 0; i < 2; i++ {
		if errs[i] != nil {
			t.Fatalf("goroutine %d: %v", i, errs[i])
		}
	}
	// Both racers reached the pre-publish barrier => both took the create path and
	// exactly one hit EEXIST (the first-use-loser adopt path ran).
	if got := atomic.LoadInt32(&arrivals); got != 2 {
		t.Fatalf("first-use race did not occur: only %d worker(s) reached the publish point (want 2)", got)
	}
	if ids[0] != ids[1] {
		t.Fatalf("racers disagree on key id: %s != %s", ids[0], ids[1])
	}
	if !bytes.Equal(pubs[0], pubs[1]) {
		t.Fatal("racers disagree on public key")
	}
}

// TestAncestorChainFsyncFailsClosed (L160/L210): when dir is nested below missing
// parents, every ancestor is fsynced up the chain — an ancestor fsync failure
// fails closed, and a clean nested create succeeds. (The walk runs regardless of
// which process created the tree, so the race loser fsyncs it too.)
func TestAncestorChainFsyncFailsClosed(t *testing.T) {
	base := t.TempDir()
	// Run the injected-failure phase in its own subtest so the fsyncDir override
	// is restored by defer even if the assertion t.Fatals early — otherwise the
	// package-global fault (EIO) would leak into later tests and cascade-fail.
	t.Run("ancestor-fsync-fails-closed", func(t *testing.T) {
		defer withFsyncPhaseFailure("ancestor")()
		if _, _, err := LoadOrCreateSigningKey(filepath.Join(base, "a", "b", "c", "signing")); err == nil ||
			!strings.Contains(err.Error(), "fsync ancestor directory") {
			t.Fatalf("an ancestor fsync failure must fail closed, got %v", err)
		}
	})
	// A clean nested create fsyncs every ancestor and succeeds.
	if _, _, err := LoadOrCreateSigningKey(filepath.Join(base, "x", "y", "signing")); err != nil {
		t.Fatalf("clean nested create must succeed: %v", err)
	}
}

func TestReuseFsyncsAfterFailedPublish(t *testing.T) {
	orig := fsyncDir
	t.Cleanup(func() { fsyncDir = orig })
	dir := filepath.Join(t.TempDir(), "signing")

	// First call: fail the .pub publish fsync -> call errors, but signing.key and
	// <keyID>.pub are both on disk.
	fsyncDir = func(fd int, label string) error {
		if label == "pub" {
			return unix.EIO
		}
		return orig(fd, label)
	}
	if _, _, err := LoadOrCreateSigningKey(dir); err == nil {
		t.Fatal("first call must fail on the .pub dir fsync")
	}

	// Second call: dir already exists; adopts the key and reuses the .pub — the
	// reuse path must fsync the dir (with the "pub-reuse" phase) and succeed.
	var reuseSynced bool
	fsyncDir = func(fd int, label string) error {
		if label == "pub-reuse" {
			reuseSynced = true
		}
		return orig(fd, label)
	}
	if _, _, err := LoadOrCreateSigningKey(dir); err != nil {
		t.Fatalf("second call must succeed: %v", err)
	}
	if !reuseSynced {
		t.Fatal("the .pub reuse fast-path must fsync the directory")
	}
}

// TestWritesAnchoredToValidatedDirFd (L221): after ensureSecureDir validates and
// returns the dir fd, a same-parent attacker who swaps the dir PATH for a symlink
// to another directory must not redirect writes. The publish uses *at relative to
// the retained fd, so the temp+link land in the ORIGINAL validated directory (via
// the fd), never the attacker's target.
func TestWritesAnchoredToValidatedDirFd(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "signing")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatalf("mkdir signing: %v", err)
	}
	attacker := filepath.Join(base, "attacker")
	if err := os.Mkdir(attacker, 0o700); err != nil {
		t.Fatalf("mkdir attacker: %v", err)
	}

	f, err := ensureSecureDir(dir)
	if err != nil {
		t.Fatalf("ensureSecureDir: %v", err)
	}
	defer f.Close()
	dirFd := int(f.Fd())

	// Swap the validated dir's NAME: move the real dir aside (the fd still refers
	// to that inode) and point the original path at the attacker's dir.
	original := filepath.Join(base, "real")
	if err := os.Rename(dir, original); err != nil {
		t.Fatalf("move real dir: %v", err)
	}
	if err := os.Symlink(attacker, dir); err != nil {
		t.Fatalf("symlink swap: %v", err)
	}

	// Publish relative to the retained fd.
	tmpName, err := writeTempAt(dirFd, signingKeyBasename, []byte("payload"), 0o600)
	if err != nil {
		t.Fatalf("writeTempAt: %v", err)
	}
	if err := unix.Linkat(dirFd, tmpName, dirFd, signingKeyBasename, 0); err != nil {
		t.Fatalf("linkat: %v", err)
	}
	_ = unix.Unlinkat(dirFd, tmpName, 0)

	// The key landed in the ORIGINAL validated directory (via the fd)...
	if _, err := os.Stat(filepath.Join(original, signingKeyBasename)); err != nil {
		t.Fatalf("key must be written into the original validated dir: %v", err)
	}
	// ...and NOTHING leaked into the attacker's swapped-in target.
	entries, err := os.ReadDir(attacker)
	if err != nil {
		t.Fatalf("read attacker dir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("writes leaked into the attacker's swapped dir: %v", entries)
	}
}

// TestLoadRepairsDriftedDirModeViaFchmod: a real, owner-owned signing dir whose
// mode drifted group/world-accessible is repaired (fchmod on the pinned fd) and
// signing proceeds.
func TestLoadRepairsDriftedDirModeViaFchmod(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "signing")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Chmod AFTER Mkdir: os.Mkdir(0o755) under a restrictive umask (e.g. 077)
	// yields 0700, so the test would never create the group/world-accessible dir
	// it means to repair. Chmod ignores umask; assert the drift precondition holds
	// before exercising the repair.
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	if info, err := os.Stat(dir); err != nil {
		t.Fatalf("stat precondition: %v", err)
	} else if info.Mode().Perm()&0o077 == 0 {
		t.Fatalf("precondition: dir must have group/world bits set, got %o", info.Mode().Perm())
	}
	if _, _, err := LoadOrCreateSigningKey(dir); err != nil {
		t.Fatalf("load: %v", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("drifted dir mode must be repaired to owner-only, got %o", info.Mode().Perm())
	}
}

// tamperPubThenReload creates a key, applies mutate to its .pub, reloads (which
// repairs the .pub), and asserts LoadPublicKey returns the correct key from a
// secure regular file.
func tamperPubThenReload(t *testing.T, mutate func(t *testing.T, pubPath string)) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "signing")
	key, keyID, err := LoadOrCreateSigningKey(dir)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	pubPath := filepath.Join(dir, keyID+".pub")
	mutate(t, pubPath)
	if _, _, err := LoadOrCreateSigningKey(dir); err != nil {
		t.Fatalf("reload/repair: %v", err)
	}
	info, err := os.Lstat(pubPath)
	if err != nil {
		t.Fatalf(".pub sidecar missing after repair: %v", err)
	}
	if info.Mode()&os.ModeType != 0 || info.Mode().Perm()&0o022 != 0 {
		t.Fatalf(".pub must be a secure regular file after repair (mode %v)", info.Mode())
	}
	loaded, err := LoadPublicKey(dir, keyID)
	if err != nil {
		t.Fatalf("load pub: %v", err)
	}
	if !key.PublicKey.Equal(loaded) {
		t.Fatal("repaired .pub must match the private key")
	}
}

func TestLoadRepairsDriftedModePublicKey(t *testing.T) {
	tamperPubThenReload(t, func(t *testing.T, pubPath string) {
		if err := os.Chmod(pubPath, 0o666); err != nil {
			t.Fatalf("chmod pub: %v", err)
		}
	})
}

func TestLoadRepairsSymlinkPublicKey(t *testing.T) {
	tamperPubThenReload(t, func(t *testing.T, pubPath string) {
		moved := pubPath + ".real"
		if err := os.Rename(pubPath, moved); err != nil {
			t.Fatalf("move pub: %v", err)
		}
		if err := os.Symlink(moved, pubPath); err != nil {
			t.Fatalf("symlink pub: %v", err)
		}
	})
}

func TestLoadRepairsFifoSymlinkPublicKeyWithoutReading(t *testing.T) {
	tamperPubThenReload(t, func(t *testing.T, pubPath string) {
		fifo := filepath.Join(t.TempDir(), "fifo")
		if err := syscall.Mkfifo(fifo, 0o600); err != nil {
			t.Skipf("mkfifo unsupported: %v", err)
		}
		if err := os.Remove(pubPath); err != nil {
			t.Fatalf("remove pub: %v", err)
		}
		if err := os.Symlink(fifo, pubPath); err != nil {
			t.Fatalf("symlink pub: %v", err)
		}
	})
}

func TestLoadRepairsCorruptPublicKey(t *testing.T) {
	tamperPubThenReload(t, func(t *testing.T, pubPath string) {
		if err := os.WriteFile(pubPath, []byte("-----BEGIN PUBLIC KEY-----\ntruncat"), 0o644); err != nil {
			t.Fatalf("corrupt pub: %v", err)
		}
	})
}

func TestLoadPublicKeyFailsWhenMissing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "signing")
	if _, _, err := LoadOrCreateSigningKey(dir); err != nil {
		t.Fatalf("create: %v", err)
	}
	// A well-formed but absent key id (32 hex chars) fails closed.
	if _, err := LoadPublicKey(dir, "00000000000000000000000000000000"); err == nil || !strings.Contains(err.Error(), "no pinned public key") {
		t.Fatalf("missing pinned public key must fail closed, got %v", err)
	}
}

// TestLoadPublicKeyRejectsMismatchedContent: a valid-perms <keyID>.pub whose
// CONTENT is a different key must fail closed — the filename's key id is an
// integrity check on the contents, so a stale/planted/incorrectly-repaired
// sidecar cannot make signatures verify against the wrong key.
func TestLoadPublicKeyRejectsMismatchedContent(t *testing.T) {
	dirA := filepath.Join(t.TempDir(), "a")
	_, idA, err := LoadOrCreateSigningKey(dirA)
	if err != nil {
		t.Fatalf("key A: %v", err)
	}
	// A different key's public half.
	dirB := filepath.Join(t.TempDir(), "b")
	_, idB, err := LoadOrCreateSigningKey(dirB)
	if err != nil {
		t.Fatalf("key B: %v", err)
	}
	pubB, err := os.ReadFile(filepath.Join(dirB, idB+".pub"))
	if err != nil {
		t.Fatalf("read pub B: %v", err)
	}
	// Plant B's public key under A's key-id filename with valid perms.
	if err := os.WriteFile(filepath.Join(dirA, idA+".pub"), pubB, 0o644); err != nil {
		t.Fatalf("plant pub: %v", err)
	}
	if _, err := LoadPublicKey(dirA, idA); err == nil || !strings.Contains(err.Error(), "does not bind to its key id") {
		t.Fatalf("mismatched .pub content must fail closed, got %v", err)
	}
	// The genuine .pub for B still loads under B's id.
	if _, err := LoadPublicKey(dirB, idB); err != nil {
		t.Fatalf("genuine .pub must load: %v", err)
	}
}

// TestLoadPublicKeyRejectsSymlink: the authoritative check is on the opened fd —
// a symlink at the .pub path is refused by the O_NOFOLLOW open (a post-lstat
// name swap to a symlink would be caught here, not merely at a prior lstat).
func TestLoadPublicKeyRejectsSymlink(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "signing")
	_, keyID, err := LoadOrCreateSigningKey(dir)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	pubPath := filepath.Join(dir, keyID+".pub")
	moved := pubPath + ".real"
	if err := os.Rename(pubPath, moved); err != nil {
		t.Fatalf("move pub: %v", err)
	}
	if err := os.Symlink(moved, pubPath); err != nil {
		t.Fatalf("symlink pub: %v", err)
	}
	if _, err := LoadPublicKey(dir, keyID); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlink .pub must be refused by O_NOFOLLOW, got %v", err)
	}
}

// TestLoadPublicKeyRejectsFifoWithoutBlocking: a FIFO at the .pub path opens
// (O_NONBLOCK) but is rejected by the fstat as non-regular, without blocking.
func TestLoadPublicKeyRejectsFifoWithoutBlocking(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "signing")
	_, keyID, err := LoadOrCreateSigningKey(dir)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	pubPath := filepath.Join(dir, keyID+".pub")
	if err := os.Remove(pubPath); err != nil {
		t.Fatalf("remove pub: %v", err)
	}
	if err := syscall.Mkfifo(pubPath, 0o600); err != nil {
		t.Skipf("mkfifo unsupported: %v", err)
	}
	if _, err := LoadPublicKey(dir, keyID); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("FIFO .pub must be rejected by the fstat, got %v", err)
	}
}

func TestLoadPublicKeyFailsOnInsecurePerms(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "signing")
	_, keyID, err := LoadOrCreateSigningKey(dir)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := os.Chmod(filepath.Join(dir, keyID+".pub"), 0o666); err != nil {
		t.Fatalf("chmod pub: %v", err)
	}
	if _, err := LoadPublicKey(dir, keyID); err == nil || !strings.Contains(err.Error(), "insecure permissions") {
		t.Fatalf("group/world-writable .pub must fail closed at load, got %v", err)
	}
}
