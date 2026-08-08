# Signed Session Audit Records

For launched Colima and Docker Desktop sessions, Workcell writes chained audit
records to a host-owned profile log. Each later chained record contains the
prior digest.

At finalization, Workcell attempts to sign the final session digest.
`workcell session verify` checks the digest chain and the host signature.

This control detects offline record changes. It does not protect against a
host-root attacker who has the signing key.

## Security purpose

The operator host owns the audit log and the signing key. The agent does not
receive the signing key. Thus, an agent cannot create a valid host seal.

The seal gives boundary integrity. It does not attest the identity of a remote
signer. For release identity, see [Provenance](provenance.md).

## Record format

Workcell appends records to this target-aware path:

```text
${WORKCELL_STATE_ROOT}/targets/<target-kind>/<provider>/<profile>/workcell.audit.log
```

The default `WORKCELL_STATE_ROOT` is `~/.local/state/workcell`.

Each line contains space-separated `key=value` fields:

```text
timestamp=<time> <event-fields> [prev_digest=<hex>] record_digest=<hex>
```

| Field | Writer | Required state | Meaning |
|---|---|---|---|
| `timestamp` | Host launcher | All records | UTC time in RFC 3339 format |
| Event fields | Host launcher | All records | Event data in write order |
| `session_id` | Host launcher | Session records | Session that owns the event |
| `prev_digest` | Host launcher | Chained records after the chain root | Digest of the prior chained record |
| `record_digest` | Host launcher | Chained records | Digest of this record and the prior digest |

Workcell calculates the record digest as follows:

```text
SHA-256(prev_digest \x00 timestamp \x00 event_field_0 \x00 event_field_1 ...)
```

The first record with a digest has no `prev_digest`. A changed, missing, or
reordered chained record breaks the chain.

A profile log can start with unchained legacy records. Digest-chain verification
excludes this initial legacy prefix. Those records are outside signed integrity.

The session head is the last `record_digest` for that `session_id`.

## Append lock

Workcell locks the profile audit log before it reads the tail and appends a
record. The lock covers both operations.

The lock uses the repository `acquire-profile-lock` operation. It does not
require `flock` on macOS.

The appender uses approximately 30 seconds of sleep backoff. Helper execution
can make the total wait longer. If the lock remains unavailable, the append
operation fails.

## Host seal

Workcell attempts to sign the session head. If this operation fails, Workcell
finalizes the session without a seal.

Workcell uses these seal items:

| Item | Location | Content |
|---|---|---|
| Private key | `${WORKCELL_STATE_ROOT}/signing/signing.key` | ECDSA P-256 private key |
| Public key | `${WORKCELL_STATE_ROOT}/signing/<key-id>.pub` | PKIX PEM public key |
| Seal | Beside the session record as `<session-id>.audit-sig` | Session ID, key ID, algorithm, signature, and recorded head |

Workcell creates the signing directory with mode `0700`. It creates the private
key and seal with mode `0600`.

The key ID is a prefix of the SHA-256 public-key fingerprint. The signed message
binds the session ID to the calculated head. Therefore, a seal cannot apply to
another session or chain state.

The head value in the seal is information only. Verification calculates the
head from the authoritative audit log.

If key storage is not owner-only, Workcell does not sign. If the lock or signer
fails, Workcell reports a warning and lets finalization continue.

CAUTION: Preserve the old public key before you replace `signing.key`. Old seals
require their recorded public keys.

Do not put `signing.key` in a support bundle or incident report.

## Verification procedure

Run this read-only command:

```sh
workcell session verify --id SESSION_ID
```

The command does these steps:

1. It finds the durable session record and its authoritative profile log.
2. It reads the seal beside the record.
3. It recalculates the chain from the first record with a digest through the session head.
4. It rejects each strictly checked record that contains a duplicate key.
5. It loads the public key that the seal identifies.
6. It checks the signature against the calculated head.

The command returns these exit codes:

| Exit code | Result |
|---|---|
| `0` | Verification succeeded. |
| `1` | Verification failed. The chain, seal, or key is missing or invalid. |
| `2` | The command usage is invalid. |

Every log line must tokenize. Each line for the selected session must also pass
strict field validation. The last such line defines the session head.

Workcell strictly validates all records through that head. It verifies the
digest chain from the first record with a digest through the head. A later
tokenizable record for another session stays outside strict validation.

Require `session_verify=verified` before you rely on signed integrity. A
terminal session without a seal does not meet this requirement.

## Limits and provider coverage

Verification detects these conditions:

- A chained record changed from the first record with a digest through the signed head.
- A chained record through the signed head is missing or in a different position.
- A strictly checked record contains a duplicate key.
- A different log replaces the authoritative log and changes signed content.
- The seal or named public key is missing or invalid.

Verification does not protect the initial unchained legacy prefix. It also does
not detect a rewrite by a host-root attacker who can use the private key.

The seal shows that the key holder signed the chain. It does not show whether an
attacker compromised the operator host.

Launched sessions have signed-chain support on these providers:

- `colima`
- `docker-desktop`

The `aws-ec2-ssm` and `gcp-vm` targets are preview-only and launch-blocked.
Workcell does not provide remote-session signatures because it cannot launch
these targets.

The preview-only `apple-container` target writes unchained lifecycle records.
It has no operator launch path. Workcell cannot seal those records, and
`session verify` fails with a `no signable digest chain` reason.

`workcell session export --format ocsf` does not include the seal. It rejects
duplicate keys and excludes records for another session.
