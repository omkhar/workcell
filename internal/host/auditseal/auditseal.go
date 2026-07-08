// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

// Package auditseal implements A5 "Signed Session Audit Records": it signs the
// head of a session's existing audit hash-chain host-side and verifies it back
// from the authoritative durable log.
//
// # Hash chain (formalized, not reinvented)
//
// scripts/workcell's append_audit_record_to_path already writes a tamper-evident
// chain: every record line carries prev_digest and record_digest, where
//
//	record_digest = SHA256( prev_digest \x00 timestamp \x00 arg0 \x00 arg1 ... )
//
// (hoststate.AuditRecordDigest) and prev_digest is the previous record's
// record_digest. The HEAD of a session is the record_digest of the LAST log
// record that carries session_id=<id>.
//
// # Seal (this package)
//
// SignSessionHead recomputes the chain over the AUTHORITATIVE profile audit log,
// derives the session HEAD, and signs a domain-separated message binding the
// session id to that head with a per-host ECDSA P-256 key (the curve cosign uses
// by default). VerifySessionSeal recomputes the chain and HEAD the same way and
// verifies the signature over the RECOMPUTED head — it never trusts a head value
// carried in the seal file — so any tamper (a flipped byte, a reordered, dropped,
// or duplicate-key record) changes a recomputed digest, breaks chain linkage, or
// changes the head and fails verification closed.
//
// # Trust model
//
// This is a boundary/host signature, not an agent signature: the operator host
// signs the chain head after the runtime boundary finalizes the session record.
// It detects tampering by any party that lacks the per-host private key. It does
// NOT defend against a host-root attacker who can read signing.key and re-sign.
// See docs/signed-session-audit-records.md.
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
// the hash chain from the first record up to and including that head.
//
// Scoping (do not fail on unrelated later records): membership is found with a
// tolerant scan, and the chain is verified only over the records up to and
// including this session's head. A torn, legacy, or duplicate-key line written
// by a LATER, unrelated session — anything after this session's head — never
// affects this session's verdict. Records before the head (including other
// sessions' interleaved records) ARE verified, because the chain digest links
// globally and this session's head integrity depends on them.
//
// Every record in the verified range is decoded strictly (ocsf
// DecodeAuditLineStrict): a duplicate key or a bare non-key=value token fails
// closed, so an appended forged token cannot leave the recomputed digest
// unchanged.
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

	// Pass 1: tolerant membership scan to locate this session's head index.
	lastMatch := -1
	for i, line := range lines {
		if lineHasSessionID(line, sessionID) {
			lastMatch = i
		}
	}
	if lastMatch < 0 {
		return "", fmt.Errorf("auditseal: no audit records for session %s", sessionID)
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

// lineHasSessionID reports whether a raw audit line carries session_id=<id>. It
// is deliberately tolerant (whitespace split, no error path) so that a torn or
// duplicate-key line belonging to some OTHER session cannot make this session's
// verification fail during the membership scan; strict decoding is applied only
// to the records that are actually chain-verified. A session id never contains
// whitespace, so its token survives the split intact regardless of how a
// neighbouring escaped value tokenizes. Mirrors sessions.auditLineHasSessionID.
func lineHasSessionID(line, sessionID string) bool {
	for _, field := range strings.Fields(line) {
		key, value, ok := strings.Cut(field, "=")
		if ok && key == "session_id" && value == sessionID {
			return true
		}
	}
	return false
}

// loadOrCreateSigningKey returns the per-host ECDSA P-256 signing key under dir,
// generating it on first use. The directory is created and hardened to 0700 and
// the private key is written 0600; if the directory cannot be secured the call
// fails closed rather than sign with an insecurely stored key. The returned
// keyID is the hex SHA-256 prefix of the PKIX-encoded public key.
//
// The private key is published atomically (write a temp file, then link it into
// place) so a concurrent reader never observes a partial PEM: on a fresh host
// two sessions may sign at once, and the loser of the create race adopts the
// winner's complete key rather than reading an empty file.
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
