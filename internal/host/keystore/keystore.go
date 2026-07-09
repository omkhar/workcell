// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

// Package keystore manages the per-host ECDSA P-256 key used to sign A5 session
// audit seals: generating, loading, and hardening the private key and its
// public sidecar under an owner-secured signing directory. It is a distinct
// concern from the audit-chain recompute and sign/verify in package auditseal,
// which consumes LoadOrCreateSigningKey and LoadPublicKey.
//
// The ENTIRE operation is anchored to one validated directory fd. The signing
// dir is opened O_NOFOLLOW|O_DIRECTORY and fstat-validated (real dir, owner euid,
// 0700), then every key/pub read and write is performed with *at syscalls
// relative to that fd (openat/renameat/linkat/unlinkat). So a same-parent
// attacker who swaps the dir for a symlink after validation cannot redirect any
// read or write to an attacker-chosen directory: the object validated is the
// object used. Files opened for content are additionally O_NOFOLLOW (no symlink
// at the leaf) and fstat-checked (regular file, owner euid, mode) on the fd read.
package keystore

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

const signingKeyBasename = "signing.key"

// fsyncDir is the directory-durability fsync, indirected so tests can inject a
// writeback failure (ENOSPC/EIO) and confirm the publish fails closed.
var fsyncDir = unix.Fsync

// LoadOrCreateSigningKey returns the per-host ECDSA P-256 signing key under dir,
// generating it on first use (dir 0700, key 0600, fail-closed if unsecurable;
// keyID is the hex SHA-256 prefix of the PKIX public key). The key is published
// atomically (temp file linked into place relative to the validated dir fd); the
// create-race loser adopts the winner.
func LoadOrCreateSigningKey(dir string) (*ecdsa.PrivateKey, string, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, "", errors.New("keystore: signing directory is required")
	}
	dirFile, err := ensureSecureDir(dir)
	if err != nil {
		return nil, "", err
	}
	defer dirFile.Close()
	dirFd := int(dirFile.Fd())

	// Fast path: adopt an existing key. Fstatat (no-follow) relative to the fd.
	if _, err := fstatatNoFollow(dirFd, signingKeyBasename); err == nil {
		return adoptSigningKey(dirFd)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, "", err
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, "", fmt.Errorf("keystore: generate key: %w", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, "", fmt.Errorf("keystore: marshal key: %w", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})

	// Publish atomically, all relative to the validated dir fd. Linkat fails
	// EEXIST if a concurrent signer already created signing.key; then adopt it.
	tmpName, err := writeTempAt(dirFd, signingKeyBasename, pemBytes, 0o600)
	if err != nil {
		return nil, "", err
	}
	linkErr := unix.Linkat(dirFd, tmpName, dirFd, signingKeyBasename, 0)
	_ = unix.Unlinkat(dirFd, tmpName, 0)
	if linkErr != nil {
		if errors.Is(linkErr, os.ErrExist) {
			return adoptSigningKey(dirFd)
		}
		return nil, "", linkErr
	}
	// Fsync the directory to durably record the new signing.key entry; a
	// writeback failure here (ENOSPC/EIO) means the key may not survive a crash,
	// so fail rather than hand back a keyID that would end up in seals verifying
	// against a key that is not on disk.
	if err := fsyncDir(dirFd); err != nil {
		return nil, "", fmt.Errorf("keystore: fsync signing directory after publishing signing.key: %w", err)
	}

	keyID, err := publicKeyID(&key.PublicKey)
	if err != nil {
		return nil, "", err
	}
	if err := ensurePublicKey(dirFd, keyID, &key.PublicKey); err != nil {
		return nil, "", err
	}
	return key, keyID, nil
}

// adoptSigningKey reads+parses the existing private key relative to the validated
// dir fd, derives its key id, and ensures a matching secure public key file.
func adoptSigningKey(dirFd int) (*ecdsa.PrivateKey, string, error) {
	data, err := readAtSecure(dirFd, signingKeyBasename, 0o077)
	if err != nil {
		return nil, "", err
	}
	key, err := parsePrivateKey(data)
	if err != nil {
		return nil, "", err
	}
	keyID, err := publicKeyID(&key.PublicKey)
	if err != nil {
		return nil, "", err
	}
	if err := ensurePublicKey(dirFd, keyID, &key.PublicKey); err != nil {
		return nil, "", err
	}
	return key, keyID, nil
}

// ensureSecureDir creates dir if absent and returns an OPEN, validated directory
// fd (real dir, owner euid, 0700). Callers keep it open and perform every
// key/pub read/write via *at relative to it, so the operation is anchored to the
// directory this validated — never a path that can be swapped. The caller closes
// the returned fd.
func ensureSecureDir(dir string) (*os.File, error) {
	// Strip a trailing separator BEFORE the O_NOFOLLOW open: with a trailing slash
	// (a configured `.../signing/`) the kernel resolves a final-component symlink
	// before O_NOFOLLOW takes effect, so a symlinked dir would be accepted. Clean
	// leaves the real dir name as the final component.
	dir = filepath.Clean(dir)
	// Create atomically if absent. os.Mkdir is atomic; a symlink (or anything
	// else) already at this name makes it fail EEXIST, which we treat as "exists,
	// verify below" — never adopt a name we did not create without an fd check.
	if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		if parent := filepath.Dir(dir); parent != dir {
			if merr := os.MkdirAll(parent, 0o700); merr != nil {
				return nil, merr
			}
			if merr := os.Mkdir(dir, 0o700); merr != nil && !errors.Is(merr, os.ErrExist) {
				return nil, merr
			}
		} else {
			return nil, err
		}
	}
	f, err := openSecureDir(dir)
	if err != nil {
		return nil, err
	}
	st, err := fstatFd(int(f.Fd()))
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if uint32(st.Mode)&0o077 != 0 {
		// Repair drifted perms via fchmod ON THE FD (the opened dir, not a name).
		if cerr := f.Chmod(0o700); cerr != nil {
			_ = f.Close()
			return nil, cerr
		}
		if st, err = fstatFd(int(f.Fd())); err != nil {
			_ = f.Close()
			return nil, err
		}
	}
	if err := checkStat(dir, &st, true, 0o077); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

// openSecureDir opens dir as a directory fd, failing on a symlink (O_NOFOLLOW)
// or a non-directory (O_DIRECTORY) at the final component.
func openSecureDir(dir string) (*os.File, error) {
	f, err := os.OpenFile(dir, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		if errors.Is(err, unix.ELOOP) {
			return nil, fmt.Errorf("keystore: %s is a symlink; refusing to trust the key store", dir)
		}
		if errors.Is(err, unix.ENOTDIR) {
			return nil, fmt.Errorf("keystore: %s is not a directory; refusing to trust the key store", dir)
		}
		return nil, err
	}
	return f, nil
}

// fstatFd fstats an open fd, always via unix.Fstat so it returns a *unix.Stat_t
// on both darwin and linux. os.File.Stat().Sys() is *syscall.Stat_t on Linux (not
// *unix.Stat_t), so a type assertion to *unix.Stat_t silently fails there and any
// owner check keyed on it never runs — hence the direct fstat.
func fstatFd(fd int) (unix.Stat_t, error) {
	var st unix.Stat_t
	err := unix.Fstat(fd, &st)
	return st, err
}

// checkStat enforces, on a fstat result, that the object is the expected kind (a
// regular file or a directory), owned by the current euid, and its perm bits set
// none of maxOther. Reads mode/uid straight from unix.Stat_t so the owner check
// runs on every platform. st.Mode is uint16 on darwin / uint32 on linux; widen
// before masking.
func checkStat(name string, st *unix.Stat_t, wantDir bool, maxOther os.FileMode) error {
	mode := uint32(st.Mode)
	if wantDir {
		if mode&uint32(unix.S_IFMT) != uint32(unix.S_IFDIR) {
			return fmt.Errorf("keystore: %s is not a directory; refusing to trust the key store", name)
		}
	} else if mode&uint32(unix.S_IFMT) != uint32(unix.S_IFREG) {
		return fmt.Errorf("keystore: %s is not a regular file; refusing to trust the key store", name)
	}
	if mode&uint32(maxOther) != 0 {
		return fmt.Errorf("keystore: %s has insecure permissions %#o; refusing to trust the key store", name, mode&0o777)
	}
	if st.Uid != uint32(os.Geteuid()) {
		return fmt.Errorf("keystore: %s is not owned by the current user; refusing to trust the key store", name)
	}
	return nil
}

// ensurePublicKey makes sure <keyID>.pub (relative to dirFd) exists, holds
// exactly the derived key, and is stored securely; an absent, corrupt,
// mismatched, or insecure file is rewritten atomically from the private key —
// all anchored to the validated dir fd.
func ensurePublicKey(dirFd int, keyID string, pub *ecdsa.PublicKey) error {
	name := keyID + ".pub"
	// Only a securely-stored (regular, owner-owned, non-symlink, not group/world-
	// writable) .pub with correct content is reused; anything else falls through
	// to an atomic rewrite, so a suspect sidecar is never trusted.
	if data, err := readAtSecure(dirFd, name, 0o022); err == nil {
		if existing, perr := parsePublicKeyPEM(data); perr == nil && pub.Equal(existing) {
			// Durability of the reuse path: a prior publish may have renamed this
			// .pub into place but then failed its directory fsync (returning an
			// error). This next successful call would otherwise reuse it and return
			// success without any fsync, so the sidecar could stay non-durable. Fsync
			// the directory now (and propagate a failure) so reuse also guarantees
			// durability.
			if ferr := fsyncDir(dirFd); ferr != nil {
				return fmt.Errorf("keystore: fsync signing directory for existing %s: %w", name, ferr)
			}
			return nil
		}
	}
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return fmt.Errorf("keystore: marshal public key: %w", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
	tmpName, err := writeTempAt(dirFd, name, pemBytes, 0o644)
	if err != nil {
		return err
	}
	if err := unix.Renameat(dirFd, tmpName, dirFd, name); err != nil {
		_ = unix.Unlinkat(dirFd, tmpName, 0)
		return err
	}
	// Fsync the directory so the new .pub entry is durable; propagate a writeback
	// failure so a keyID is never returned for a sidecar that may be lost on a
	// crash (verification of those sessions would then fail closed).
	if err := fsyncDir(dirFd); err != nil {
		return fmt.Errorf("keystore: fsync signing directory after publishing %s: %w", name, err)
	}
	return nil
}

// LoadPublicKey returns the pinned public key <keyID>.pub under dir. It opens and
// fstat-validates the directory fd, then reads the .pub relative to that fd, so
// verification is anchored to the validated directory and cannot be redirected
// by a post-validation swap of the dir or the .pub name.
func LoadPublicKey(dir, keyID string) (*ecdsa.PublicKey, error) {
	if err := validateKeyID(keyID); err != nil {
		return nil, err
	}
	dirFile, err := openValidatedDir(dir, 0o077)
	if err != nil {
		return nil, err
	}
	defer dirFile.Close()

	data, err := readAtSecure(int(dirFile.Fd()), keyID+".pub", 0o022)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("keystore: no pinned public key %s (session was not signed by this host)", keyID)
		}
		return nil, err
	}
	pub, err := parsePublicKeyPEM(data)
	if err != nil {
		return nil, fmt.Errorf("keystore: public key %s: %w", keyID, err)
	}
	// Bind the sidecar to the key id it is filed under: recompute the fingerprint
	// of the loaded key and require it equals keyID, so a stale/corrupt/planted
	// .pub under a valid key-id filename cannot make signatures verify against the
	// WRONG key.
	gotID, err := publicKeyID(pub)
	if err != nil {
		return nil, err
	}
	if gotID != keyID {
		return nil, fmt.Errorf("keystore: public key %s does not bind to its key id (content fingerprints as %s); refusing to trust the key store", keyID, gotID)
	}
	return pub, nil
}

// openValidatedDir opens dir O_NOFOLLOW|O_DIRECTORY and fstat-validates it (real
// dir, owner euid, perms), returning the open fd for *at operations.
func openValidatedDir(dir string, maxOther os.FileMode) (*os.File, error) {
	// Strip a trailing separator so O_NOFOLLOW applies to the real final component.
	dir = filepath.Clean(dir)
	f, err := openSecureDir(dir)
	if err != nil {
		return nil, err
	}
	st, err := fstatFd(int(f.Fd()))
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if err := checkStat(dir, &st, true, maxOther); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

// fstatatNoFollow reports the mode of name relative to dirFd without following a
// final-component symlink; a missing name is os.ErrNotExist.
func fstatatNoFollow(dirFd int, name string) (unix.Stat_t, error) {
	var st unix.Stat_t
	if err := unix.Fstatat(dirFd, name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return st, err
	}
	return st, nil
}

// openReadAtSecure opens name relative to dirFd for reading and validates the
// OPEN FD (regular file, owner euid, no perm bit in maxOther). O_NOFOLLOW refuses
// a symlink at the leaf; O_NONBLOCK means a FIFO opens immediately and is then
// rejected by the fstat. The object validated is the object read.
func openReadAtSecure(dirFd int, name string, maxOther os.FileMode) (*os.File, error) {
	fd, err := unix.Openat(dirFd, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		if errors.Is(err, unix.ELOOP) {
			return nil, fmt.Errorf("keystore: %s is a symlink; refusing to trust the key store", name)
		}
		return nil, err
	}
	f := os.NewFile(uintptr(fd), name)
	st, err := fstatFd(int(f.Fd()))
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if err := checkStat(name, &st, false, maxOther); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

func readAtSecure(dirFd int, name string, maxOther os.FileMode) ([]byte, error) {
	f, err := openReadAtSecure(dirFd, name, maxOther)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

// writeTempAt creates a fresh temp file relative to dirFd (O_CREAT|O_EXCL|
// O_NOFOLLOW), writes+fsyncs it, and returns its name for a relative
// linkat/renameat. It cleans up the temp on any error. The name is unpredictable
// (random suffix) so it cannot be pre-created by an attacker.
func writeTempAt(dirFd int, prefix string, data []byte, perm os.FileMode) (string, error) {
	var rnd [8]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return "", err
	}
	tmpName := prefix + "." + hex.EncodeToString(rnd[:]) + ".tmp"
	fd, err := unix.Openat(dirFd, tmpName, unix.O_CREAT|unix.O_EXCL|unix.O_WRONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, uint32(perm))
	if err != nil {
		return "", err
	}
	f := os.NewFile(uintptr(fd), tmpName)
	fail := func(e error) (string, error) {
		_ = f.Close()
		_ = unix.Unlinkat(dirFd, tmpName, 0)
		return "", e
	}
	// O_CREAT honours umask, so fchmod to the exact perm on the fd.
	if err := f.Chmod(perm); err != nil {
		return fail(err)
	}
	if _, err := f.Write(data); err != nil {
		return fail(err)
	}
	if err := f.Sync(); err != nil {
		return fail(err)
	}
	if err := f.Close(); err != nil {
		_ = unix.Unlinkat(dirFd, tmpName, 0)
		return "", err
	}
	return tmpName, nil
}

func parsePublicKeyPEM(data []byte) (*ecdsa.PublicKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("not valid PEM")
	}
	pubAny, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	pub, ok := pubAny.(*ecdsa.PublicKey)
	if !ok {
		return nil, errors.New("not an ECDSA key")
	}
	return pub, nil
}

// validateKeyID guards the key-file lookup against path traversal (a key id is lowercase hex).
func validateKeyID(keyID string) error {
	if keyID == "" {
		return errors.New("keystore: empty key id")
	}
	for _, r := range keyID {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return fmt.Errorf("keystore: invalid key id %q", keyID)
		}
	}
	return nil
}

func publicKeyID(pub *ecdsa.PublicKey) (string, error) {
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return "", fmt.Errorf("keystore: marshal public key: %w", err)
	}
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:16]), nil
}

func parsePrivateKey(data []byte) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("keystore: signing key is not valid PEM")
	}
	keyAny, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("keystore: parse signing key: %w", err)
	}
	key, ok := keyAny.(*ecdsa.PrivateKey)
	if !ok {
		return nil, errors.New("keystore: signing key is not an ECDSA key")
	}
	return key, nil
}
