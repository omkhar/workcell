# Signed Session Audit Records

Workcell session audit records form a tamper-evident hash chain whose head is
signed host-side. `workcell session verify --id SESSION_ID` recomputes the chain
from the authoritative durable log and verifies that signature, so a reviewer
outside the agent can detect tampering. This document specifies the record
format, the signing and verification model, and — explicitly — what the model
does and does not protect.

## Hash-chain format

Every audit record is appended to a per-profile log under Workcell-owned target
state, for example:

```text
~/.local/state/workcell/targets/<target-kind>/<provider>/<profile>/workcell.audit.log
```

`scripts/workcell`'s `append_audit_record_to_path` writes each record as
whitespace-delimited `key=value` tokens:

```text
timestamp=<ts> <event fields...> [prev_digest=<hex>] record_digest=<hex>
```

- `timestamp` is a UTC RFC 3339 second-resolution stamp.
- The event fields are the record payload (for example
  `event=exit session_id=<id> exit_status=0 ...`). Every session record carries
  `session_id=<id>`.
- `record_digest` chains the record to its predecessor:

  ```text
  record_digest = SHA-256( prev_digest \x00 timestamp \x00 arg0 \x00 arg1 ... )
  ```

  computed by `hoststate.AuditRecordDigest`, where the args are the event-field
  tokens in write order and `prev_digest` is the previous record's
  `record_digest` (empty for the first record in the log).
- `prev_digest` is omitted on the first record and present on every subsequent
  record.

Because each digest folds in the previous digest, altering, reordering, or
dropping any record changes every downstream digest — a standard tamper-evident
chain. The **head** of a session is the `record_digest` of the last log record
carrying that session's `session_id`.

## Seal (host-side signature)

When the runtime boundary finalizes a session (`finalize_session_audit`, after
the terminal audit record is appended and the durable record is written),
Workcell signs the session head:

- **Key.** A per-host ECDSA P-256 key (the curve cosign uses by default) is
  generated on first use under `~/.local/state/workcell/signing/` — directory
  mode `0700`, private key `signing.key` mode `0600`. If that directory cannot
  be created and confined to owner-only access, signing fails closed rather than
  sign with an insecurely stored key.
- **Signed message.** A domain-separated message binding the session id to the
  recomputed head is signed, so a signature cannot be replayed onto a different
  session or a different chain state.
- **Seal file.** The signature is stored beside the durable session record as
  `<session-id>.audit-sig` (host-owned, mode `0600`). It records the seal
  version, session id, head digest, signing key id, algorithm, and the
  base64 signature. Only the version, session id, key id, algorithm, and
  signature are load-bearing; the head digest is informational because
  verification always recomputes the head from the authoritative log.
- **Public key.** The public half is written to
  `~/.local/state/workcell/signing/<key-id>.pub` (PKIX PEM), where `<key-id>` is
  a SHA-256 fingerprint prefix of the public key. `session verify` pins the
  signature to the key id named in the seal.

This is a boundary/host signature, **not** an agent signature: the operator host
signs on the trusted side of the runtime boundary, so the agent inside the
sandbox never holds the signing key and cannot forge a seal.

## Verification

`workcell session verify --id SESSION_ID` is read-only and fail-closed:

1. Locate the durable session record and its authoritative audit log.
2. Read the seal beside the record. A session with no seal fails closed.
3. Recompute the hash chain over the authoritative log from the first record up
   to and including the session head, rejecting any record that carries a
   duplicate key (the same fail-closed rule the OCSF export applies).
4. Verify the seal signature over the **recomputed** head using the pinned
   public key named by the seal.

Exit codes: `0` verified, `1` verification failed (tamper, or a missing or
invalid seal), `2` usage error. Output is passed through the shared support
bundle redactor (the [G2](../SUPPORT.md) rule-set), so no secret leaks.

### Key rotation

To rotate, an operator removes (or moves aside) `signing.key`; the next signing
run generates a fresh key with a new key id. Old public keys are retained under
the signing directory, so seals produced by an earlier key still verify against
their recorded key id.

## Trust model

**Detected.** Any modification to the persisted audit records at or before a
session's signed head:

- a flipped byte in any record field or digest;
- a reordered or dropped record (chain linkage breaks or the head changes);
- a forged duplicate key on a record;
- an exported or copied log presented in place of the authoritative one (the
  copy is not the persisted record the seal was computed over);
- any session presented without a valid host seal.

**Not defended.** This is boundary integrity, not host compromise resistance:

- A host-root attacker who can read `signing.key` can re-sign a tampered chain.
  The seal proves the records were signed by a holder of the per-host key, not
  that the host itself was never compromised.
- The signing key protects records against parties **without** that key
  (the sandboxed agent, a lower-privilege process, offline tampering of the log
  or an exported copy) — not against the key holder.
- Verification confirms integrity and host-side provenance; it is not an
  identity attestation of a remote signer (contrast the release-artifact keyless
  Sigstore flow in [provenance.md](provenance.md), whose trust anchor is a CI
  workflow identity that does not exist at session runtime).

## Coordination with OCSF export

Signing adds a sidecar file and does not modify the audit log or the durable
session record, so `workcell session export --format ocsf` is unchanged: signed
sessions export exactly as before, and the export's own duplicate-key and
cross-session fail-closed rules continue to apply.
