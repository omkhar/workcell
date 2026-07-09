// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package keystore

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
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

// withFsyncPhaseFailure makes fsyncDir fail with EIO at the given phase label
// ("parent", "ancestor", "signing.key", "pub", "pub-reuse"); other phases pass
// through. It returns a restore func so a test can inject, assert, restore, and
// inject again — robust to how many ancestor dirs the temp path has.
func withFsyncPhaseFailure(phase string) (restore func()) {
	orig := fsyncDir
	fsyncDir = func(fd int, label string) error {
		if label == phase {
			return unix.EIO
		}
		return orig(fd, label)
	}
	return func() { fsyncDir = orig }
}

// TestPublishFailsOnParentFsyncError (L144): the ancestor-chain fsync (whose
// first level is dir's immediate parent) must fail the call closed on a
// writeback failure, so no keyID is returned before dir's entry is durable.
func TestPublishFailsOnParentFsyncError(t *testing.T) {
	defer withFsyncPhaseFailure("ancestor")()
	if _, _, err := LoadOrCreateSigningKey(filepath.Join(t.TempDir(), "signing")); err == nil ||
		!strings.Contains(err.Error(), "fsync ancestor directory") {
		t.Fatalf("ancestor (parent) fsync failure on dir creation must fail closed, got %v", err)
	}
}

// TestRejectsNonP256Key (L558): a valid ECDSA signing.key on a different curve
// (P-384) must not be adopted — the contract and generated keys are P-256. A
// P-256 store still works.
func TestRejectsNonP256Key(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "signing")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Plant a P-384 private key as signing.key.
	k384, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatalf("gen p384: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(k384)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	if err := os.WriteFile(filepath.Join(dir, signingKeyBasename), pemBytes, 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	if _, _, err := LoadOrCreateSigningKey(dir); err == nil || !strings.Contains(err.Error(), "not a P-256 key") {
		t.Fatalf("a P-384 signing.key must fail closed, got %v", err)
	}

	// A P-256 store works.
	dir2 := filepath.Join(t.TempDir(), "signing")
	if _, keyID, err := LoadOrCreateSigningKey(dir2); err != nil {
		t.Fatalf("P-256 store must work: %v", err)
	} else if _, err := LoadPublicKey(dir2, keyID); err != nil {
		t.Fatalf("LoadPublicKey P-256 must work: %v", err)
	}
}

// TestBlankDirGuardParity (L285): both entry points reject a blank/whitespace dir
// (fail closed) rather than cleaning it to "." (the current working directory).
func TestBlankDirGuardParity(t *testing.T) {
	for _, d := range []string{"", "   "} {
		if _, _, err := LoadOrCreateSigningKey(d); err == nil || !strings.Contains(err.Error(), "signing directory is required") {
			t.Fatalf("LoadOrCreateSigningKey(%q) must fail closed, got %v", d, err)
		}
		if _, err := LoadPublicKey(d, "00000000000000000000000000000000"); err == nil || !strings.Contains(err.Error(), "signing directory is required") {
			t.Fatalf("LoadPublicKey(%q) must fail closed, got %v", d, err)
		}
	}
	// A non-blank store still works end to end.
	dir := filepath.Join(t.TempDir(), "signing")
	_, keyID, err := LoadOrCreateSigningKey(dir)
	if err != nil {
		t.Fatalf("non-blank dir must work: %v", err)
	}
	if _, err := LoadPublicKey(dir, keyID); err != nil {
		t.Fatalf("LoadPublicKey on a real store must work: %v", err)
	}
}
