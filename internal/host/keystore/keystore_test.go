// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package keystore

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

func TestSigningKeyIsStableAndRotates(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "signing")
	_, id1, err := LoadOrCreateSigningKey(dir)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	_, id2, err := LoadOrCreateSigningKey(dir)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if id1 != id2 {
		t.Fatalf("key id must be stable across loads: %s != %s", id1, id2)
	}
	// Rotation: remove the private key; a new key with a new id is generated and
	// the old public key is retained so historical seals still verify.
	if err := os.Remove(filepath.Join(dir, signingKeyBasename)); err != nil {
		t.Fatalf("remove key: %v", err)
	}
	_, id3, err := LoadOrCreateSigningKey(dir)
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if id3 == id1 {
		t.Fatal("rotated key must have a new id")
	}
	if _, err := os.Stat(filepath.Join(dir, id1+".pub")); err != nil {
		t.Fatalf("old public key must be retained after rotation: %v", err)
	}
}

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

// TestLoadFailsOnInsecureExistingKey: a drifted-perms signing.key is refused, never repaired.
func TestLoadFailsOnInsecureExistingKey(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "signing")
	if _, _, err := LoadOrCreateSigningKey(dir); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := os.Chmod(filepath.Join(dir, signingKeyBasename), 0o644); err != nil {
		t.Fatalf("chmod key: %v", err)
	}
	if _, _, err := LoadOrCreateSigningKey(dir); err == nil || !strings.Contains(err.Error(), "insecure permissions") {
		t.Fatalf("insecure existing key must fail closed, got %v", err)
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
	if err != nil || info.Mode()&os.ModeType != 0 || info.Mode().Perm()&0o022 != 0 {
		t.Fatalf(".pub must be a secure regular file after repair (mode %v, err %v)", info.Mode(), err)
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
