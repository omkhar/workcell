# Operator Boundary-Incident Response Runbook

Use this runbook when you suspect a Workcell runtime boundary breach.

First, contain the session. Then preserve evidence before you collect or inspect
it.

Do not run `workcell gc`. Do not run `workcell session delete` before you
preserve and report the evidence.

For a build or release incident, use the
[CI/CD threat model](ci-threat-model.md#signing-compromise-incident-response).

## 1. Scope and severity

Use this runbook for these events:

- A session reads a host secret outside the documented boundary.
- A session writes outside the workspace without explicit `breakglass`.
- A session receives an unmanaged host socket or credential store.
- Workspace content changes provider controls or mutable Git controls.
- A session has unexpected outbound access.

These events match the runtime scope in [`SECURITY.md`](../SECURITY.md#in-scope).

Treat host-secret exposure or a confirmed sandbox escape as critical. Treat
workspace control-plane changes as high severity.

## 2. Immediate safety rules

- Do not delete a session, profile, VM, container, or state file.
- Do not run `workcell gc` before evidence collection is complete.
- Do not publish raw audit data, workspace data, or credentials.
- Use the session record to identify the target. Do not use `--inspect` for this
  purpose.
- Stop active execution immediately when continued execution creates more risk.

An immediate VM stop can destroy volatile network evidence. Stop immediately
when active execution increases host exposure.

## 3. Containment

1. Run `workcell session list --verbose`.
2. Run `workcell session show --id SESSION_ID`.
3. Record the profile, container name, target kind, and target provider.
4. Create a new owner-only evidence directory at an absolute path.
5. Capture network evidence if this action does not delay containment.
6. Stop the affected session.
7. Wait for a terminal session status.
8. Capture the final Colima firewall rules.
9. Stop the target boundary.

Set the evidence path before any capture command. Use operator-controlled
storage outside the affected profile.

```sh
EVIDENCE_DIRECTORY=/absolute/operator-controlled/path
umask 077
mkdir -m 700 "$EVIDENCE_DIRECTORY"
```

`mkdir` must create the directory. Stop if the path exists.

Use this command for a normal detached-session stop:

```sh
workcell session stop --id SESSION_ID
```

Use `--force` if a graceful stop increases host exposure or can change more
evidence:

```sh
workcell session stop --id SESSION_ID --force
```

After you stop a detached session, poll this command:

```sh
workcell session show --id SESSION_ID
```

Wait until the status is `stopped`, `exited`, `failed`, or `aborted`.

Do not stop the target boundary while the status is `stopping`. The session
monitor can still write the durable record and audit data.

Stop the boundary if the monitor stalls or continued execution increases host
exposure. Record the last status. The durable evidence can be incomplete.

An interactive foreground launch has no session stop handle. Terminate its
launcher process.

### Colima network evidence

The `local_vm` target uses one Colima VM for each Workcell profile. All
containers in that profile share the egress rules.

Capture the container state only if this action does not delay containment or
increase exposure:

```sh
DOCKER_HOST="unix://$HOME/.colima/PROFILE/docker.sock" \
  docker inspect CONTAINER_NAME > "$EVIDENCE_DIRECTORY/container-inspect.json"
```

Capture the IPv4 and IPv6 firewall state before the VM stop:

```sh
COLIMA_HOME="$HOME/.colima" colima ssh --profile PROFILE -- \
  sudo iptables-save > "$EVIDENCE_DIRECTORY/colima-iptables-before.txt"
COLIMA_HOME="$HOME/.colima" colima ssh --profile PROFILE -- \
  sudo ip6tables-save > "$EVIDENCE_DIRECTORY/colima-ip6tables-before.txt"
```

After the session reaches a terminal status, capture both rule sets again:

```sh
COLIMA_HOME="$HOME/.colima" colima ssh --profile PROFILE -- \
  sudo iptables-save > "$EVIDENCE_DIRECTORY/colima-iptables-after.txt"
COLIMA_HOME="$HOME/.colima" colima ssh --profile PROFILE -- \
  sudo ip6tables-save > "$EVIDENCE_DIRECTORY/colima-ip6tables-after.txt"
```

The monitor can remove the stopped container. Thus, the pre-stop inspection
can be the only container-network record.

Then stop the Colima boundary:

```sh
COLIMA_HOME="$HOME/.colima" colima stop --profile PROFILE
```

### Docker Desktop containment

The `local_compat` target uses Docker Desktop. It does not have a dedicated
Workcell VM or a Workcell egress allowlist.

Force-stop the suspect session if a graceful stop increases exposure. Stop
Docker Desktop only if a force-stop fails or other Docker workloads are
suspect.

## 4. Evidence preservation

The durable profile state has this layout:

```text
${WORKCELL_STATE_ROOT}/targets/<target-kind>/<provider>/<profile>/
```

The default root is `~/.local/state/workcell`. The profile directory contains
session records and the shared `workcell.audit.log`.

Use these paths for the two supported launch targets:

| Target | Profile state path |
|---|---|
| `local_vm` / `colima` | `${WORKCELL_STATE_ROOT}/targets/local_vm/colima/PROFILE/` |
| `local_compat` / `docker-desktop` | `${WORKCELL_STATE_ROOT}/targets/local_compat/docker-desktop/PROFILE/` |

The Colima target can also read legacy state from `~/.colima/PROFILE/`. Preserve
that complete directory when it exists.

Use the owner-only evidence directory from section 3. It must be outside the
affected profile. Copy the complete profile directory into it.

For example:

```sh
cp -pR PROFILE_STATE_DIRECTORY "$EVIDENCE_DIRECTORY/"
```

Replace each uppercase placeholder before you run the commands.

Resolve the state root. Then preserve the public key that the seal names.

```sh
WORKCELL_INCIDENT_STATE_ROOT="${WORKCELL_STATE_ROOT:-${XDG_STATE_HOME:-$HOME/.local/state}/workcell}"
cp -p "$WORKCELL_INCIDENT_STATE_ROOT/signing/KEY_ID.pub" "$EVIDENCE_DIRECTORY/"
```

Record a fingerprint in the evidence directory:

```sh
shasum -a 256 "$EVIDENCE_DIRECTORY/KEY_ID.pub" \
  > "$EVIDENCE_DIRECTORY/KEY_ID.pub.sha256"
```

CAUTION: Do not copy the private key into the evidence directory, support
bundle, or report.

After you preserve essential evidence, use a separate trusted system to revoke
each exposed credential at its source. Record the revocation time. Do not stage
a replacement on the affected operator host.

## 5. Evidence collection

Create a redacted host diagnostics bundle:

```sh
workcell support-bundle --output ~/workcell-support-bundle.json
```

See [`SUPPORT.md`](../SUPPORT.md) for the field list and redaction rules.

Collect the applicable session artifacts:

| Artifact | Command | Content | Sensitivity |
|---|---|---|---|
| Durable record | `workcell session show --id SESSION_ID` | Session metadata and final state | Review before you share. |
| Session timeline | `workcell session timeline --id SESSION_ID` | Audit events for one session | Can contain raw `session send` text. |
| Session export | `workcell session export --id SESSION_ID --output PATH` | Record and audit events for the session | Can contain raw message text. |
| Workspace status and diff | `workcell session diff --id SESSION_ID --output PATH` | File status and raw changed content | Can contain secrets and private source. |
| Profile audit log | `workcell session logs --id SESSION_ID --kind audit` | Events for all sessions in the profile | Can contain raw message text from other sessions. |
| Optional runtime logs | `workcell session logs --id SESSION_ID --kind KIND` | Debug, file-trace, or transcript data | Can contain workspace and agent output. |

## 6. Integrity verification

Run the verification command against the durable profile log:

```sh
workcell session verify --id SESSION_ID
```

Workcell attempts to sign the audit head. If this operation fails, the terminal
session has no seal.

Require `session_verify=verified` before you rely on signed integrity. The
command fails closed when the chain, seal, or public key is invalid or missing.

A verified result detects offline changes at or before that session head. A
later valid record for another session does not change the earlier head.

The result does not protect against a host-root attacker who can use the
private key. Use the preserved files and fingerprints as additional evidence.

Apple container records have no digest chain. Verification reports that these
records have no signable chain.

See [Signed Session Audit Records](signed-session-audit-records.md) for the
format, coverage, and trust limits.

## 7. Private report

Use a [GitHub Private Vulnerability Report][pvr]. Do not use a public issue for
a suspected boundary breach.

Include these items when they are safe to share:

- The redacted support bundle.
- The durable session record.
- The `[status]` section from the session diff.
- Hashes and file counts for preserved evidence.
- The signal, severity, provider, mode, host OS, and commands used.
- The `session verify` result.

Review and redact these items before you share them:

- The `[diff]` section from `workcell session diff`.
- The audit log, timeline, and session export.
- Debug, file-trace, and transcript logs.
- Container inspection and network state.

Do not include credential values, `.env` data, raw workspace files, or the host
private signing key.

The support bundle applies fixed redaction rules. Review it once before you
share it. The other incident artifacts do not receive the same guarantee.

[pvr]: https://github.com/omkhar/workcell/security/advisories/new

## 8. Recovery

Start recovery only after you complete evidence collection and the private
report.

Decide whether the operator host and Docker Desktop remain trusted. Rebuild an
untrusted system before service recovery. Then use `workcell auth unset` and
`workcell auth set`. Confirm the replacements with `workcell auth status`.

### Delete the session record

CAUTION: `workcell session delete` removes the durable session record. Confirm
that you completed evidence collection and the private report.

Run a dry run first:

```sh
workcell session delete --dry-run --id SESSION_ID
```

Review the result. Then run the command without `--dry-run`.

The session must have a terminal status. Container cleanup also requires the
target Docker transport.

For Colima, restart the preserved profile when cleanup needs its socket:

```sh
COLIMA_HOME="$HOME/.colima" colima start --profile PROFILE
```

For Docker Desktop, start Docker Desktop and use its configured context.

Do not use `--record-only` when you must remove residual container artifacts.
Do not manually remove state files.

### Reset a Colima profile

CAUTION: This command deletes the complete Colima profile. Confirm the
profile name and preserved evidence first.

```sh
COLIMA_HOME="$HOME/.colima" colima delete --profile PROFILE --force
```

`--repair-profile` does not reset a managed profile. It only repairs an
unmanaged profile conflict during launch.

Report each confirmed boundary weakness through the private advisory. Add a
fix and a regression check before you close the incident.
