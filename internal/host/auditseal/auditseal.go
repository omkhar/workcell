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

	"github.com/omkhar/workcell/internal/host/auditlog"
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
	if err := checkSealHeader(sessionID, seal); err != nil {
		return "", err
	}
	head, err := recomputeSessionHead(auditLogPath, targetProvider, sessionID)
	if err != nil {
		return "", err
	}
	if err := verifySealSignature(signingDir, sessionID, head, seal); err != nil {
		return "", err
	}
	return head, nil
}

func checkSealHeader(sessionID string, seal Seal) error {
	if seal.Version != sealVersion {
		return fmt.Errorf("auditseal: unsupported seal version %d", seal.Version)
	}
	if seal.Algorithm != Algorithm {
		return fmt.Errorf("auditseal: unsupported seal algorithm %q", seal.Algorithm)
	}
	if seal.SessionID != sessionID {
		return fmt.Errorf("auditseal: seal session id %q does not match %q", seal.SessionID, sessionID)
	}
	return nil
}

// verifySealSignature checks seal.Signature over head with the pinned key that
// seal.KeyID names.
func verifySealSignature(signingDir, sessionID, head string, seal Seal) error {
	if err := checkSealHeader(sessionID, seal); err != nil {
		return err
	}
	sig, err := base64.StdEncoding.DecodeString(seal.Signature)
	if err != nil {
		return fmt.Errorf("auditseal: decode signature: %w", err)
	}
	pub, err := keystore.LoadPublicKey(signingDir, seal.KeyID)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(signedMessage(sessionID, head))
	if !ecdsa.VerifyASN1(pub, digest[:], sig) {
		return errors.New("auditseal: signature does not verify against the pinned host key")
	}
	return nil
}

type chainRecord struct {
	timestamp    string
	prevDigest   string
	recordDigest string
	args         []string
}

// sessionMatch is one record that claims the session, with the chain head and
// chain verdict at that record.
type sessionMatch struct {
	head   string
	err    error
	fields []ocsf.AuditField
}

// recomputeSessionHead returns this session's head digest (its last record) after
// verifying the chain up to that head. Only records up to the head are verified
// (a LATER unrelated session's line never affects it); earlier interleaved
// records ARE verified, each decoded strictly.
func recomputeSessionHead(auditLogPath, targetProvider, sessionID string) (string, error) {
	matches, err := walkSessionChain(auditLogPath, targetProvider, sessionID)
	if err != nil {
		return "", err
	}
	last := matches[len(matches)-1]
	return last.head, last.err
}

// SealedSessionRecords verifies seal over the head it names and returns the
// session's records up to and including that head, in log order. Records that
// the session appended after the seal are not returned, so a running session
// whose chain grew after the last seal still yields its signed prefix.
// Fail-closed on a bad signature, an unknown key, a broken chain before the
// sealed head, or a sealed head that no session record carries.
func SealedSessionRecords(signingDir, auditLogPath, targetProvider, sessionID string, seal Seal) ([][]ocsf.AuditField, error) {
	if err := verifySealSignature(signingDir, sessionID, seal.HeadDigest, seal); err != nil {
		return nil, err
	}
	matches, err := walkSessionChain(auditLogPath, targetProvider, sessionID)
	if err != nil {
		return nil, err
	}
	for i := len(matches) - 1; i >= 0; i-- {
		if matches[i].err != nil || matches[i].head != seal.HeadDigest {
			continue
		}
		records := make([][]ocsf.AuditField, 0, i+1)
		for _, match := range matches[:i+1] {
			records = append(records, match.fields)
		}
		return records, nil
	}
	return nil, fmt.Errorf("auditseal: no verified record of session %s carries the sealed head", sessionID)
}

func walkSessionChain(auditLogPath, targetProvider, sessionID string) ([]sessionMatch, error) {
	// Verify the chain in one bounded stream. Reopening the path for separate
	// passes could combine records from different file states during an append.
	// Keep the verdict at each matching record, so unrelated later records retain
	// the existing behavior and do not affect that session's head.
	//
	// On an upgraded
	// profile the audit log can have legacy entries written BEFORE record_digest
	// existed; the append path seeds prev_digest from the last existing digest (or
	// "" if none), so there is exactly ONE chain root = the first physical line
	// that carries a record_digest field. Skip the CONTIGUOUS LEADING run of lines
	// with no record_digest field (the legacy prefix), then start the strict chain
	// at that root with expectedPrev="". After the root, a line missing
	// record_digest is stripped-digest tamper — only the leading prefix is skippable.
	expectedPrev := ""
	chainHead := ""
	started := false
	var matches []sessionMatch
	var chainErr error
	logicalLine := 0
	err := auditlog.ForEachLine(auditLogPath, func(rawLine string, _ int) error {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			return nil
		}
		i := logicalLine
		logicalLine++
		// A malformed field can make a record untokenizable before its session ID.
		// The writer never emits such a record, so reject it as corruption.
		if !ocsf.AuditLineTokenizable(line, targetProvider) {
			return fmt.Errorf("auditseal: audit log has an untokenizable record at line %d (corruption or tamper)", i+1)
		}
		claimsSession := ocsf.AuditLineClaimsSession(line, targetProvider, sessionID)
		fields, err := ocsf.DecodeAuditLineStrict(line, targetProvider)
		if err != nil {
			if claimsSession {
				return fmt.Errorf("auditseal: audit record for session %s is malformed: %w", sessionID, err)
			}
			if chainErr == nil {
				chainErr = fmt.Errorf("auditseal: %w", err)
			}
			return nil
		}
		if chainErr == nil {
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
			if !started && hasDigest {
				started = true
			}
			if started {
				switch {
				case rec.recordDigest == "":
					chainErr = fmt.Errorf("auditseal: audit chain broken at record %d: missing record_digest", i)
				case hoststate.AuditRecordDigest(rec.prevDigest, rec.timestamp, rec.args) != rec.recordDigest:
					chainErr = fmt.Errorf("auditseal: audit chain broken at record %d: recomputed digest does not match stored record_digest", i)
				case rec.prevDigest != expectedPrev:
					chainErr = fmt.Errorf("auditseal: audit chain broken at record %d: prev_digest does not link to the previous record", i)
				default:
					expectedPrev = rec.recordDigest
					chainHead = rec.recordDigest
				}
			}
		}
		if claimsSession {
			match := sessionMatch{head: chainHead, err: chainErr, fields: fields}
			if match.err == nil && !started {
				match.err = ErrUnsupportedAuditChain
			}
			matches = append(matches, match)
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("auditseal: no audit log for session %s", sessionID)
		}
		return nil, err
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("auditseal: no audit records for session %s", sessionID)
	}
	return matches, nil
}

// HasSignableChain reports whether a session's audit records form a signable
// digest chain; false only for a no-chain provider (ErrUnsupportedAuditChain,
// e.g. apple-container). Callers use it only for a clear message; the verdict is
// the signature.
func HasSignableChain(auditLogPath, targetProvider, sessionID string) bool {
	_, err := recomputeSessionHead(auditLogPath, targetProvider, sessionID)
	return !errors.Is(err, ErrUnsupportedAuditChain)
}
