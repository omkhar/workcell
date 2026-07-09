// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

// Package auditseal implements A5 "Signed Session Audit Records": it signs the
// head of scripts/workcell's existing audit hash-chain host-side and verifies the
// signature over the RECOMPUTED head from the authoritative durable log, so any
// tamper fails closed. This is a boundary/host signature (not agent-signed): it
// detects tamper by any party lacking the per-host key, but does NOT defend a
// host-root attacker who can read signing.key. See
// docs/signed-session-audit-records.md.
package auditseal

import (
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/omkhar/workcell/internal/host/hoststate"
	"github.com/omkhar/workcell/internal/host/keystore"
	"github.com/omkhar/workcell/internal/ocsf"
)

const Algorithm = "ecdsa-p256-sha256"

const sealVersion = 1

// ErrUnsupportedAuditChain reports that a session's audit records carry no digest
// chain (the preview-only apple-container target). Callers treat it as
// "unsigned": the hook skips it and `session verify` fails closed.
var ErrUnsupportedAuditChain = errors.New("auditseal: session audit records have no digest chain (provider audit chain unsupported)")

// Seal is the durable, host-owned signature over a session's audit-chain head,
// stored beside the session record. Only Version, SessionID, KeyID, Algorithm,
// and Signature are load-bearing; HeadDigest and SignedAt are informational
// (verification always recomputes the head from the authoritative log).
type Seal struct {
	Version    int    `json:"version"`
	SessionID  string `json:"session_id"`
	HeadDigest string `json:"head_digest"`
	KeyID      string `json:"key_id"`
	Algorithm  string `json:"algorithm"`
	Signature  string `json:"signature"`
	SignedAt   string `json:"signed_at,omitempty"`
}

// signedMessage is the domain-separated bytes signed/verified; it binds the session id to the recomputed head against replay.
func signedMessage(sessionID, head string) []byte {
	return []byte(fmt.Sprintf("workcell-session-audit-seal\nv%d\n%s\n%s\n", sealVersion, sessionID, head))
}

// SignSessionHead recomputes the chain and head, loads/creates the per-host key, and returns a Seal signing that head.
func SignSessionHead(signingDir, auditLogPath, targetProvider, sessionID, signedAt string) (Seal, error) {
	if strings.TrimSpace(sessionID) == "" {
		return Seal{}, errors.New("auditseal: session id is required")
	}
	head, err := recomputeSessionHead(auditLogPath, targetProvider, sessionID)
	if err != nil {
		return Seal{}, err
	}
	key, keyID, err := keystore.LoadOrCreateSigningKey(signingDir)
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

// VerifySessionSeal recomputes the chain/head from the authoritative log and
// verifies seal.Signature over the RECOMPUTED head using the pinned key named by
// seal.KeyID. Fail-closed on any parse error, chain break, head mismatch, unknown
// key, or signature mismatch; returns the recomputed head on success.
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
	pub, err := keystore.LoadPublicKey(signingDir, seal.KeyID)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(signedMessage(sessionID, head))
	if !ecdsa.VerifyASN1(pub, digest[:], sig) {
		return "", errors.New("auditseal: signature does not verify against the pinned host key")
	}
	return head, nil
}

type chainRecord struct {
	timestamp    string
	prevDigest   string
	recordDigest string
	args         []string
}

// recomputeSessionHead returns this session's head digest (its last record) after
// verifying the chain up to that head. Only records up to the head are verified
// (a LATER unrelated session's line never affects it); earlier interleaved
// records ARE verified, each decoded strictly.
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

	// Pass 1: locate the head. A line claims this session iff it has a session_id
	// TOKEN == sessionID (argv-safe); a claiming line that fails strict decode is
	// same-session tamper (fail closed), a non-claiming line is skipped (L209/L280).
	lastMatch := -1
	for i, line := range lines {
		// Corruption gate: a malformed field can make a record untokenizable before
		// its session_id (the writer emits `event=` first). The genuine writer never
		// emits one, so fail closed rather than drop an unreadable same-session
		// tamper as a non-member (a legitimate other-session torn line tokenizes).
		if !ocsf.AuditLineTokenizable(line, targetProvider) {
			return "", fmt.Errorf("auditseal: audit log has an untokenizable record at line %d (corruption or tamper)", i+1)
		}
		if !ocsf.AuditLineClaimsSession(line, targetProvider, sessionID) {
			continue
		}
		if _, err := ocsf.DecodeAuditLineStrict(line, targetProvider); err != nil {
			return "", fmt.Errorf("auditseal: audit record for session %s is malformed: %w", sessionID, err)
		}
		lastMatch = i
	}
	if lastMatch < 0 {
		return "", fmt.Errorf("auditseal: no audit records for session %s", sessionID)
	}

	// No-chain provider guard: report ErrUnsupportedAuditChain ONLY when NONE of
	// the session's chain-region records (lines[0..lastMatch]) carries a
	// record_digest — the genuine no-chain provider (apple-container) emits a
	// digest on NO record. Checking only the HEAD record would let an attacker on
	// a chained provider (colima/docker) strip the head's record_digest, or append
	// a no-digest record claiming the session AFTER a valid chain (making it the
	// new head), and have real tamper misreported as "unsupported/unsigned"
	// (HasSignableChain maps ErrUnsupportedAuditChain->false). If ANY record in the
	// region has a digest the chain exists: fall through to pass 2, which anchors
	// at the first digest-bearing line and fails "missing record_digest" on any
	// post-root record (including the head) that lacks one. A decode error here is
	// genuine tamper and is surfaced as such.
	anyDigest := false
	for i := 0; i <= lastMatch; i++ {
		fields, err := ocsf.DecodeAuditLineStrict(lines[i], targetProvider)
		if err != nil {
			return "", fmt.Errorf("auditseal: %w", err)
		}
		if auditFieldsHaveKey(fields, "record_digest") {
			anyDigest = true
			break
		}
	}
	if !anyDigest {
		return "", ErrUnsupportedAuditChain
	}

	// Pass 2: strict chain verification over records 0..lastMatch. On an upgraded
	// profile the audit log can have legacy entries written BEFORE record_digest
	// existed; the append path seeds prev_digest from the last existing digest (or
	// "" if none), so there is exactly ONE chain root = the first physical line
	// that carries a record_digest field. Skip the CONTIGUOUS LEADING run of lines
	// with no record_digest field (the legacy prefix), then start the strict chain
	// at that root with expectedPrev="". After the root, a line missing
	// record_digest is stripped-digest tamper — only the leading prefix is skippable.
	expectedPrev := ""
	head := ""
	started := false
	for i := 0; i <= lastMatch; i++ {
		fields, err := ocsf.DecodeAuditLineStrict(lines[i], targetProvider)
		if err != nil {
			return "", fmt.Errorf("auditseal: %w", err)
		}
		rec := chainRecord{}
		hasDigest := false
		for _, f := range fields {
			switch f.Key {
			case "timestamp":
				rec.timestamp = f.Value
			case "prev_digest":
				rec.prevDigest = f.Value
			case "record_digest":
				rec.recordDigest = f.Value
				hasDigest = true
			default:
				rec.args = append(rec.args, f.Key+"="+f.Value)
			}
		}
		if !started {
			if !hasDigest {
				continue // contiguous legacy prefix before the chain root
			}
			started = true // first line carrying a record_digest is the chain root
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

func auditFieldsHaveKey(fields []ocsf.AuditField, key string) bool {
	for _, f := range fields {
		if f.Key == key {
			return true
		}
	}
	return false
}

// HasSignableChain reports whether a session's audit records form a signable
// digest chain; false only for a no-chain provider (ErrUnsupportedAuditChain,
// e.g. apple-container). Callers use it only for a clear message; the verdict is
// the signature.
func HasSignableChain(auditLogPath, targetProvider, sessionID string) bool {
	_, err := recomputeSessionHead(auditLogPath, targetProvider, sessionID)
	return !errors.Is(err, ErrUnsupportedAuditChain)
}
