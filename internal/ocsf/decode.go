// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package ocsf

// AuditField is one ordered key/value pair decoded from an audit line. It is
// the exported view of the package-internal auditField so that the A5 audit
// hash-chain verifier (internal/host/auditseal) can reuse this package's
// hardened, dup-key-rejecting tokenizer instead of reimplementing the bash
// `printf %q` and percent-path decoders.
type AuditField struct {
	Key   string
	Value string
}

// DecodeAuditLine tokenizes a single durable audit line into ordered,
// un-escaped key/value fields, selecting the on-disk encoding from the
// session's target provider exactly as the OCSF export does
// (auditEncodingForProvider). It REJECTS a record that carries a duplicate key
// (fail-closed) so a tampered line such as `session_id=A session_id=B` is
// detected rather than silently collapsed. Order is preserved so a caller can
// reconstruct the exact digest input the writer hashed.
func DecodeAuditLine(line, targetProvider string) ([]AuditField, error) {
	fields, err := decodeAuditLine(line, auditEncodingForProvider(targetProvider))
	if err != nil {
		return nil, err
	}
	out := make([]AuditField, len(fields))
	for i, f := range fields {
		out[i] = AuditField{Key: f.key, Value: f.value}
	}
	return out, nil
}
