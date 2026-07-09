// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package ocsf

import (
	"fmt"
	"strings"

	"github.com/omkhar/workcell/internal/applecontainer"
)

// AuditField is one ordered key/value pair decoded from an audit line. It is
// the exported view of the package-internal auditField so that the A5 audit
// hash-chain verifier (internal/host/auditseal) can reuse this package's
// hardened, dup-key-rejecting tokenizer instead of reimplementing the bash
// `printf %q` and percent-path decoders.
type AuditField struct {
	Key   string
	Value string
}

// DecodeAuditLineStrict tokenizes a single durable audit line into ordered,
// un-escaped key/value fields, selecting the on-disk encoding from the session's
// target provider exactly as the OCSF export does (auditEncodingForProvider),
// and rejecting a record that carries a duplicate key (fail-closed) so a
// tampered line such as `session_id=A session_id=B` is detected. It adds one
// extra fail-closed rule for tamper detection over the OCSF export's tolerant
// reader: it REJECTS a record that carries a bare (non key=value) token rather
// than silently dropping it. The export intentionally tolerates torn crash lines
// by skipping bare tokens (decodeAuditLine), but a tamper-evidence verifier must
// not — a writer never emits a bare token, so an appended one such as
// `... FORGED` is tampering. Because the record digest is computed only over the
// key=value args, a dropped bare token would otherwise leave the recomputed
// digest unchanged and let the forged line verify. Order is preserved so a
// caller can reconstruct the exact digest input the writer hashed.
func DecodeAuditLineStrict(line, targetProvider string) ([]AuditField, error) {
	enc := auditEncodingForProvider(targetProvider)
	var tokens []string
	if enc == encodingPercentPath {
		tokens = strings.Fields(line)
	} else {
		var err error
		tokens, err = splitQuotedTokens(line)
		if err != nil {
			return nil, err
		}
	}
	fields := make([]AuditField, 0, len(tokens))
	seen := make(map[string]struct{}, len(tokens))
	for _, tok := range tokens {
		key, value, ok := strings.Cut(tok, "=")
		if !ok {
			return nil, fmt.Errorf("audit record has bare token %q (no key=value)", tok)
		}
		if _, dup := seen[key]; dup {
			return nil, fmt.Errorf("audit record has duplicate key %q", key)
		}
		seen[key] = struct{}{}
		if enc == encodingPercentPath {
			if _, isPath := percentEncodedAuditFields[key]; isPath {
				value = applecontainer.DecodeAuditPathValue(value)
			}
		}
		fields = append(fields, AuditField{Key: key, Value: value})
	}
	return fields, nil
}

// AuditLineClaimsSession reports whether an audit line carries a session_id
// TOKEN equal to sessionID, using the provider's proper tokenizer so a bash-%q
// argv value cannot forge a claim (its `\ ` spaces stay inside one argv token,
// never a session_id token). It tolerates bare/duplicate tokens — a structural
// claim test, paired by callers with DecodeAuditLineStrict to decide
// member-vs-tamper for a claiming line.
//
// A tokenizer error (a malformed quoted field) does NOT erase a matching
// session_id FIELD token seen BEFORE it: splitQuotedTokens returns the fields it
// completed before failing, so a corrupted record for the target session still
// claims (and is then passed to strict decode to fail closed) rather than being
// misclassified as an unrelated non-member. A match found only AFTER the error
// point cannot exist (there are no completed tokens past it), and an
// argv-embedded session_id remains a single argv token, never a claim.
func AuditLineClaimsSession(line, targetProvider, sessionID string) bool {
	var tokens []string
	if auditEncodingForProvider(targetProvider) == encodingPercentPath {
		tokens = strings.Fields(line)
	} else {
		// Ignore the error: use whatever fields were completed before it.
		tokens, _ = splitQuotedTokens(line)
	}
	for _, tok := range tokens {
		if k, v, ok := strings.Cut(tok, "="); ok && k == "session_id" && v == sessionID {
			return true
		}
	}
	return false
}
