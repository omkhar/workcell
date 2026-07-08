// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

// Package auditseal implements A5 "Signed Session Audit Records": it signs the
// head of a session's existing audit hash-chain host-side and verifies it back
// from the authoritative durable log.
//
// scripts/workcell already writes a tamper-evident chain where each record's
// record_digest = SHA256(prev_digest \x00 timestamp \x00 args...)
// (hoststate.AuditRecordDigest) links to the previous record's digest; the head
// of a session is the record_digest of the last record carrying session_id=<id>.
// SignSessionHead signs a domain-separated session-id+head message with a
// per-host ECDSA P-256 key (the curve cosign uses); VerifySessionSeal recomputes
// the chain and head and verifies the signature over the RECOMPUTED head (never
// a head carried in the seal), so any tamper fails closed.
//
// This is a boundary/host signature, not an agent signature: it detects tamper
// by any party lacking the per-host key, but does NOT defend a host-root
// attacker who can read signing.key and re-sign. See
// docs/signed-session-audit-records.md.
package auditseal

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/omkhar/workcell/internal/host/hoststate"
	"github.com/omkhar/workcell/internal/ocsf"
)

// Algorithm is the fixed signature algorithm identifier recorded in every seal.
const Algorithm = "ecdsa-p256-sha256"

// sealVersion is the on-disk seal schema version so a future format change can
// be gated by consumers.
const sealVersion = 1

// signingKeyBasename is the per-host private key file under the signing dir.
const signingKeyBasename = "signing.key"

// ErrUnsupportedAuditChain reports that a session's audit records carry no
// digest chain (no record_digest), so there is nothing to sign or verify — the
// preview-only apple-container target writes plain lifecycle lines. Callers
// treat it as "unsigned": the signing hook skips it and `session verify` fails
// closed with that reason, rather than emitting a seal that can never verify.
var ErrUnsupportedAuditChain = errors.New("auditseal: session audit records have no digest chain (provider audit chain unsupported)")

// Seal is the durable, host-owned signature over a session's audit-chain head.
// It is stored beside the session record as <record>.audit-sig.json. Only
// Version, SessionID, KeyID, Algorithm, and Signature are load-bearing for
// verification; HeadDigest and SignedAt are informational (verification always
// recomputes the head from the authoritative log).
type Seal struct {
	Version    int    `json:"version"`
	SessionID  string `json:"session_id"`
	HeadDigest string `json:"head_digest"`
	KeyID      string `json:"key_id"`
	Algorithm  string `json:"algorithm"`
	Signature  string `json:"signature"`
	SignedAt   string `json:"signed_at,omitempty"`
}

// signedMessage is the exact byte sequence signed and verified. It is domain
// separated and binds the session id to the recomputed head so a signature
// cannot be replayed onto a different session or a different chain state.
func signedMessage(sessionID, head string) []byte {
	return []byte(fmt.Sprintf("workcell-session-audit-seal\nv%d\n%s\n%s\n", sealVersion, sessionID, head))
}

// SignSessionHead recomputes the chain over auditLogPath, derives the session
// head, loads (or creates) the per-host signing key under signingDir, and
// returns a Seal signing that head. signedAt is recorded verbatim.
func SignSessionHead(signingDir, auditLogPath, targetProvider, sessionID, signedAt string) (Seal, error) {
	if strings.TrimSpace(sessionID) == "" {
		return Seal{}, errors.New("auditseal: session id is required")
	}
	head, err := recomputeSessionHead(auditLogPath, targetProvider, sessionID)
	if err != nil {
		return Seal{}, err
	}
	key, keyID, err := loadOrCreateSigningKey(signingDir)
	if err != nil {
		return Seal{}, err
	}
	digest := sha256.Sum256(signedMessage(sessionID, head))
	sig, err := ecdsa.SignASN1(rand.Reader, key, digest[:])
	if err != nil {
		return Seal{}, fmt.Errorf("auditseal: sign: %w", err)
	}
	return Seal{
		Version:    sealVersion,
		SessionID:  sessionID,
		HeadDigest: head,
		KeyID:      keyID,
		Algorithm:  Algorithm,
		Signature:  base64.StdEncoding.EncodeToString(sig),
		SignedAt:   signedAt,
	}, nil
}

// VerifySessionSeal recomputes the chain and head from the authoritative log
// and verifies seal.Signature over the RECOMPUTED head using the pinned public
// key named by seal.KeyID under signingDir. It is fail-closed: any parse error,
// chain break, head mismatch, unknown key, or signature mismatch is an error.
// On success it returns the authoritative recomputed head (never the head value
// carried in the seal, which is only informational) so callers report a value
// the signature actually covers.
func VerifySessionSeal(signingDir, auditLogPath, targetProvider, sessionID string, seal Seal) (string, error) {
	if seal.Version != sealVersion {
		return "", fmt.Errorf("auditseal: unsupported seal version %d", seal.Version)
	}
	if seal.Algorithm != Algorithm {
		return "", fmt.Errorf("auditseal: unsupported seal algorithm %q", seal.Algorithm)
	}
	if seal.SessionID != sessionID {
		return "", fmt.Errorf("auditseal: seal session id %q does not match %q", seal.SessionID, sessionID)
	}
	sig, err := base64.StdEncoding.DecodeString(seal.Signature)
	if err != nil {
		return "", fmt.Errorf("auditseal: decode signature: %w", err)
	}
	head, err := recomputeSessionHead(auditLogPath, targetProvider, sessionID)
	if err != nil {
		return "", err
	}
	pub, err := loadPublicKey(signingDir, seal.KeyID)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(signedMessage(sessionID, head))
	if !ecdsa.VerifyASN1(pub, digest[:], sig) {
		return "", errors.New("auditseal: signature does not verify against the pinned host key")
	}
	return head, nil
}

// chainRecord is one parsed audit line reduced to the fields the chain digest is
// computed over, plus the stored digests.
type chainRecord struct {
	timestamp    string
	prevDigest   string
	recordDigest string
	args         []string
}

// recomputeSessionHead reads the authoritative audit log and returns the digest
// of this session's head — the last record bearing sessionID — after verifying
// the chain from the first record up to and including that head.
//
// Scoping: the chain is verified only over records up to this session's head, so
// a torn/legacy/duplicate-key line from a LATER unrelated session never affects
// this session; records before the head (including interleaved other-session
// records) ARE verified since the digest links globally. Every verified record
// is decoded strictly (DecodeAuditLineStrict): a duplicate key or bare token
// fails closed, so an appended forged token cannot leave the digest unchanged.
func recomputeSessionHead(auditLogPath, targetProvider, sessionID string) (string, error) {
	data, err := os.ReadFile(auditLogPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("auditseal: no audit log for session %s", sessionID)
		}
		return "", err
	}

	raw := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	lines := make([]string, 0, len(raw))
	for _, line := range raw {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}

	// Pass 1: locate this session's head via lineHasSessionID, which matches the
	// DECODED session_id field (see its doc for why a raw scan is unsafe).
	lastMatch := -1
	for i, line := range lines {
		if lineHasSessionID(line, targetProvider, sessionID) {
			lastMatch = i
		}
	}
	if lastMatch < 0 {
		return "", fmt.Errorf("auditseal: no audit records for session %s", sessionID)
	}

	// No-chain provider guard: if this session's head record has no
	// record_digest, the provider produces no digest chain (apple-container);
	// report it as unsupported rather than a confusing "chain broken" error. A
	// decode error here is genuine tamper and is surfaced as such.
	headFields, err := ocsf.DecodeAuditLineStrict(lines[lastMatch], targetProvider)
	if err != nil {
		return "", fmt.Errorf("auditseal: %w", err)
	}
	if !auditFieldsHaveKey(headFields, "record_digest") {
		return "", ErrUnsupportedAuditChain
	}

	// Pass 2: strict chain verification over records 0..lastMatch only.
	expectedPrev := ""
	head := ""
	for i := 0; i <= lastMatch; i++ {
		fields, err := ocsf.DecodeAuditLineStrict(lines[i], targetProvider)
		if err != nil {
			return "", fmt.Errorf("auditseal: %w", err)
		}
		rec := chainRecord{}
		for _, f := range fields {
			switch f.Key {
			case "timestamp":
				rec.timestamp = f.Value
			case "prev_digest":
				rec.prevDigest = f.Value
			case "record_digest":
				rec.recordDigest = f.Value
			default:
				rec.args = append(rec.args, f.Key+"="+f.Value)
			}
		}
		if rec.recordDigest == "" {
			return "", fmt.Errorf("auditseal: audit chain broken at record %d: missing record_digest", i)
		}
		want := hoststate.AuditRecordDigest(rec.prevDigest, rec.timestamp, rec.args)
		if want != rec.recordDigest {
			return "", fmt.Errorf("auditseal: audit chain broken at record %d: recomputed digest does not match stored record_digest", i)
		}
		if rec.prevDigest != expectedPrev {
			return "", fmt.Errorf("auditseal: audit chain broken at record %d: prev_digest does not link to the previous record", i)
		}
		expectedPrev = rec.recordDigest
		head = rec.recordDigest
	}
	return head, nil
}

// lineHasSessionID reports whether an audit line's DECODED session_id field
// equals sessionID. Decoding with the strict tokenizer means free-form content —
// notably a `session send` argv whose bash-%q `\ ` spaces would split into a
// fake session_id token under a raw scan — stays a single argv value and cannot
// forge membership. A line that fails to decode (torn/duplicate-key, perhaps
// another session's) is a non-member and never fails this session's scan.
func lineHasSessionID(line, targetProvider, sessionID string) bool {
	fields, err := ocsf.DecodeAuditLineStrict(line, targetProvider)
	if err != nil {
		return false
	}
	for _, f := range fields {
		if f.Key == "session_id" {
			return f.Value == sessionID
		}
	}
	return false
}

func auditFieldsHaveKey(fields []ocsf.AuditField, key string) bool {
	for _, f := range fields {
		if f.Key == key {
			return true
		}
	}
	return false
}

// HasSignableChain reports whether a session's audit records form a signable
// digest chain; it returns false only for a no-chain provider
// (ErrUnsupportedAuditChain, e.g. apple-container). Callers use it only to
// choose a clear human message; the security verdict is the signature.
func HasSignableChain(auditLogPath, targetProvider, sessionID string) bool {
	_, err := recomputeSessionHead(auditLogPath, targetProvider, sessionID)
	return !errors.Is(err, ErrUnsupportedAuditChain)
}

// loadOrCreateSigningKey returns the per-host ECDSA P-256 signing key under dir,
// generating it on first use. The dir is hardened to 0700 and the key written
// 0600; if the dir cannot be secured the call fails closed. keyID is the hex
// SHA-256 prefix of the PKIX public key. The key is published atomically (temp
// file then os.Link) so a concurrent reader never sees a partial PEM; the create
// race loser adopts the winner's complete key.
func loadOrCreateSigningKey(dir string) (*ecdsa.PrivateKey, string, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, "", errors.New("auditseal: signing directory is required")
	}
	if err := ensureSecureDir(dir); err != nil {
		return nil, "", err
	}
	keyPath := filepath.Join(dir, signingKeyBasename)

	if data, err := os.ReadFile(keyPath); err == nil {
		return adoptSigningKey(dir, data)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, "", err
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, "", fmt.Errorf("auditseal: generate key: %w", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, "", fmt.Errorf("auditseal: marshal key: %w", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})

	// Publish the complete key atomically. os.Link fails with ErrExist if a
	// concurrent signer already created signing.key, in which case we adopt that
	// (complete) key instead of racing to overwrite it.
	tmpPath, err := writeTempFile(dir, signingKeyBasename, pemBytes, 0o600)
	if err != nil {
		return nil, "", err
	}
	linkErr := os.Link(tmpPath, keyPath)
	_ = os.Remove(tmpPath)
	if linkErr != nil {
		if errors.Is(linkErr, os.ErrExist) {
			data, rerr := os.ReadFile(keyPath)
			if rerr != nil {
				return nil, "", rerr
			}
			return adoptSigningKey(dir, data)
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

// adoptSigningKey parses an existing private key PEM, derives its key id, and
// makes sure the matching public key file exists and is valid before returning.
func adoptSigningKey(dir string, data []byte) (*ecdsa.PrivateKey, string, error) {
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

// ensureSecureDir creates dir if needed and enforces 0700 ownership-only perms,
// failing closed if the mode cannot be tightened.
func ensureSecureDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	info, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("auditseal: signing directory %s is not private (mode %o)", dir, info.Mode().Perm())
	}
	return nil
}

// ensurePublicKey makes sure <keyID>.pub exists AND holds exactly the public
// key derived from the private key. A file that merely exists is not trusted: a
// prior run that crashed mid-write could leave a truncated or wrong .pub, which
// would make verification fail against seals this host itself produced. If the
// file is absent, unparsable, or does not match, it is (re)written atomically
// from the private key's public half.
func ensurePublicKey(dir, keyID string, pub *ecdsa.PublicKey) error {
	path := publicKeyPath(dir, keyID)
	if data, err := os.ReadFile(path); err == nil {
		if existing, perr := parsePublicKeyPEM(data); perr == nil && pub.Equal(existing) {
			return nil
		}
		// Present but corrupt or mismatched: repair it below.
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return fmt.Errorf("auditseal: marshal public key: %w", err)
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

// writeTempFile writes data to a fresh temp file in dir with the given mode,
// fsyncs it, and returns its path. Callers publish it into place with os.Link or
// os.Rename so a concurrent reader of the final path never sees partial bytes.
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

func loadPublicKey(dir, keyID string) (*ecdsa.PublicKey, error) {
	if err := validateKeyID(keyID); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(publicKeyPath(dir, keyID))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("auditseal: no pinned public key %s (session was not signed by this host)", keyID)
		}
		return nil, err
	}
	pub, err := parsePublicKeyPEM(data)
	if err != nil {
		return nil, fmt.Errorf("auditseal: public key %s: %w", keyID, err)
	}
	return pub, nil
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

// validateKeyID guards the on-disk key file lookup against path traversal: a key
// id is always a lowercase hex fingerprint.
func validateKeyID(keyID string) error {
	if keyID == "" {
		return errors.New("auditseal: empty key id")
	}
	for _, r := range keyID {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return fmt.Errorf("auditseal: invalid key id %q", keyID)
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
		return "", fmt.Errorf("auditseal: marshal public key: %w", err)
	}
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:16]), nil
}

func parsePrivateKey(data []byte) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("auditseal: signing key is not valid PEM")
	}
	keyAny, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("auditseal: parse signing key: %w", err)
	}
	key, ok := keyAny.(*ecdsa.PrivateKey)
	if !ok {
		return nil, errors.New("auditseal: signing key is not an ECDSA key")
	}
	return key, nil
}
