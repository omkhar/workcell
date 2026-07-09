// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

// Package keystore manages the per-host ECDSA P-256 key used to sign A5 session
// audit seals: generating, loading, and hardening the private key and its
// public sidecar under an owner-secured signing directory. It is a distinct
// concern from the audit-chain recompute and sign/verify in package auditseal,
// which consumes LoadOrCreateSigningKey and LoadPublicKey.
//
// Every path is fail-closed and lstat-checked BEFORE any open: the signing dir,
// the private key, and the public sidecar must each be a non-symlink,
// owner-owned file of the expected kind (regular file or directory) with no
// group/world access it should not have — so a symlink, a FIFO/device/socket, a
// wrong-owner file, or a drifted mode is refused (or, for the public key,
// atomically repaired from the private key) without ever being opened.
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
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

const signingKeyBasename = "signing.key"

// fileKind selects what os.FileMode type requireOwnerSecure accepts.
type fileKind int

const (
	regularFile fileKind = iota
	directory
)

// LoadOrCreateSigningKey returns the per-host ECDSA P-256 signing key under dir,
// generating it on first use (dir 0700, key 0600, fail-closed if unsecurable;
// keyID is the hex SHA-256 prefix of the PKIX public key). The key is published
// atomically (temp file then os.Link); the create-race loser adopts the winner.
func LoadOrCreateSigningKey(dir string) (*ecdsa.PrivateKey, string, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, "", errors.New("keystore: signing directory is required")
	}
	if err := ensureSecureDir(dir); err != nil {
		return nil, "", err
	}
	keyPath := filepath.Join(dir, signingKeyBasename)

	if _, err := os.Lstat(keyPath); err == nil {
		return adoptSigningKey(dir)
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

	// Publish atomically. os.Link fails with ErrExist if a concurrent signer
	// already created signing.key; then we adopt that complete key.
	tmpPath, err := writeTempFile(dir, signingKeyBasename, pemBytes, 0o600)
	if err != nil {
		return nil, "", err
	}
	linkErr := os.Link(tmpPath, keyPath)
	_ = os.Remove(tmpPath)
	if linkErr != nil {
		if errors.Is(linkErr, os.ErrExist) {
			return adoptSigningKey(dir)
		}
		return nil, "", linkErr
	}

	keyID, err := publicKeyID(&key.PublicKey)
	if err != nil {
		return nil, "", err
	}
	if err := ensurePublicKey(dir, keyID, &key.PublicKey); err != nil {
		return nil, "", err
	}
	return key, keyID, nil
}

// adoptSigningKey validates and parses an existing private key, derives its key
// id, and ensures a matching secure public key file before returning.
func adoptSigningKey(dir string) (*ecdsa.PrivateKey, string, error) {
	keyPath := filepath.Join(dir, signingKeyBasename)
	// Check BEFORE reading: requireOwnerSecure lstat's the path, so a symlinked
	// (e.g. to a FIFO/device), non-regular, wrong-owner, or group/world-accessible
	// signing.key is refused without ever being opened.
	if err := requireOwnerSecure(keyPath, 0o077, regularFile); err != nil {
		return nil, "", err
	}
	data, err := os.ReadFile(keyPath)
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
	if err := ensurePublicKey(dir, keyID, &key.PublicKey); err != nil {
		return nil, "", err
	}
	return key, keyID, nil
}

// ensureSecureDir creates dir and enforces a real, owner-owned, 0700 directory.
func ensureSecureDir(dir string) error {
	// Lstat BEFORE any chmod: a pre-existing symlink must be refused, never
	// followed (chmod/stat would operate on its target). Create privately if absent.
	if info, err := os.Lstat(dir); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("keystore: signing directory %s is a symlink; refusing to trust the key store", dir)
		}
		if !info.IsDir() {
			return fmt.Errorf("keystore: signing directory %s is not a directory", dir)
		}
	} else if errors.Is(err, os.ErrNotExist) {
		if merr := os.MkdirAll(dir, 0o700); merr != nil {
			return merr
		}
	} else {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	// Final gate: a real, owner-owned, 0700 directory (catches a post-chmod swap).
	return requireOwnerSecure(dir, 0o077, directory)
}

// ensurePublicKey makes sure <keyID>.pub exists, holds exactly the derived key,
// and is stored securely; an absent, corrupt, mismatched, or insecure file is
// rewritten atomically from the private key.
func ensurePublicKey(dir, keyID string, pub *ecdsa.PublicKey) error {
	path := publicKeyPath(dir, keyID)
	// Check BEFORE reading: only open the .pub if it is stored securely (regular,
	// owner-owned, not a symlink, not group/world-writable), so a symlink-to-FIFO
	// or device .pub is never opened. If it is secure with correct content, reuse
	// it; otherwise (insecure, absent, corrupt, or mismatched) fall through to an
	// atomic rewrite from the private key — never reading a suspect sidecar.
	if requireOwnerSecure(path, 0o022, regularFile) == nil {
		if data, err := os.ReadFile(path); err == nil {
			if existing, perr := parsePublicKeyPEM(data); perr == nil && pub.Equal(existing) {
				return nil
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return fmt.Errorf("keystore: marshal public key: %w", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
	tmpPath, err := writeTempFile(dir, keyID+".pub", pemBytes, 0o644)
	if err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

// writeTempFile writes data to a fresh fsynced temp file in dir for atomic publish via os.Link/os.Rename.
func writeTempFile(dir, prefix string, data []byte, perm os.FileMode) (string, error) {
	f, err := os.CreateTemp(dir, prefix+".*.tmp")
	if err != nil {
		return "", err
	}
	tmpPath := f.Name()
	if err := f.Chmod(perm); err != nil {
		_ = f.Close()
		_ = os.Remove(tmpPath)
		return "", err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmpPath)
		return "", err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmpPath)
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", err
	}
	return tmpPath, nil
}

// LoadPublicKey returns the pinned public key <keyID>.pub under dir, after
// rechecking (lstat, before opening) that the store is owner-secured: verify may
// run without the private key, so a drifted group/world-writable store would let
// a local user plant a <keyID>.pub and forge a passing seal.
func LoadPublicKey(dir, keyID string) (*ecdsa.PublicKey, error) {
	if err := validateKeyID(keyID); err != nil {
		return nil, err
	}
	if err := requireOwnerSecure(dir, 0o077, directory); err != nil {
		return nil, err
	}
	if err := requireOwnerSecure(publicKeyPath(dir, keyID), 0o022, regularFile); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("keystore: no pinned public key %s (session was not signed by this host)", keyID)
		}
		return nil, err
	}
	data, err := os.ReadFile(publicKeyPath(dir, keyID))
	if err != nil {
		return nil, err
	}
	pub, err := parsePublicKeyPEM(data)
	if err != nil {
		return nil, fmt.Errorf("keystore: public key %s: %w", keyID, err)
	}
	return pub, nil
}

// requireOwnerSecure fails closed unless path is owner-owned, not a symlink, of
// the expected kind (a REGULAR file, or a directory), and its perm bits set none
// of maxOther. The kind check (via lstat) refuses a FIFO/device/socket created
// directly in the owner-only dir — which passes the owner/mode checks but would
// block or error on open — before anything opens it.
func requireOwnerSecure(path string, maxOther os.FileMode, kind fileKind) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("keystore: %s is a symlink; refusing to trust the key store", path)
	}
	switch kind {
	case directory:
		if !info.IsDir() {
			return fmt.Errorf("keystore: %s is not a directory; refusing to trust the key store", path)
		}
	default:
		if info.Mode()&os.ModeType != 0 {
			return fmt.Errorf("keystore: %s is not a regular file; refusing to trust the key store", path)
		}
	}
	if info.Mode().Perm()&maxOther != 0 {
		return fmt.Errorf("keystore: %s has insecure permissions %#o; refusing to trust the key store", path, info.Mode().Perm())
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok && st.Uid != uint32(os.Getuid()) {
		return fmt.Errorf("keystore: %s is not owned by the current user; refusing to trust the key store", path)
	}
	return nil
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

func publicKeyPath(dir, keyID string) string {
	return filepath.Join(dir, keyID+".pub")
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
