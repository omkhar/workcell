# Operator Boundary-Incident Response Runbook

This runbook is for an **operator** who suspects a **runtime boundary breach**
on a Workcell host: an agent escaping the session boundary, host credential
exposure, or workspace/control-plane tampering. It is the operational companion
to the [runtime-boundary threat model](threat-model.md); the
[CI/CD threat model](ci-threat-model.md) covers the separate
signing-compromise case (build and release pipeline), not a live session.

This runbook **stitches together shipped tooling** — it introduces no new
commands. Every command below exists today unless it is explicitly marked as
roadmapped. Work top to bottom: **detect, contain, preserve, collect, verify,
report, recover.** When time is short, the order that matters most is
**contain, then preserve before collect** — never clean or garbage-collect
before evidence is off the box.

## 0. Scope

Use this runbook for a suspected violation of a runtime trust boundary defined
in the [threat model](threat-model.md#trust-boundaries), for example:

- reads of host secrets outside the documented boundary (credential exposure)
- writes outside the intended workspace without explicit `breakglass`, or
  unmanaged host socket/credential passthrough (agent escape)
- repo-local control-plane files replacing reviewed provider baselines, or
  silent trust widening through repo content (workspace/control-plane tamper)

These mirror the **in-scope** reports in [`SECURITY.md`](../SECURITY.md#in-scope).
For anything in the build/release pipeline, use the
[signing-compromise runbook](ci-threat-model.md#signing-compromise-incident-response)
instead.

## 1. Detect and triage

Signals that a session may have crossed a boundary:

- **Assurance downgrade.** Each session record carries an initial, current, and
  final assurance state (see the
  [session supervisor design](workcell-session-supervisor-design.md#data-model)).
  A session that *ended lower-assurance* (for example a warning that
  package-manager mutations ran as root inside the container) is a triage signal,
  not proof, but it warrants review.
- **Unexpected workspace changes.** `workcell session diff --id SESSION_ID`
  renders the current workspace against the clean git base recorded at launch. It
  is read-only and fails closed if it cannot trust the comparison. Unexpected
  edits — especially to control-plane files (agent config, MCP config, git
  config) — are a tamper signal.
- **Boundary/egress anomalies.** Unexpected outbound behavior on the managed
  Colima path, or host secrets/sockets that should never have been reachable
  (see the [threat model controls](threat-model.md#controls)).
- **Live host state.** `workcell --agent <provider> --doctor` and
  `workcell --agent <provider> --inspect` report resolved profile, target, and
  launch/assurance state; use
  [diagnostics-and-support-matrix.md](diagnostics-and-support-matrix.md) to read
  the fields.

**Severity.** Treat host **secret exposure** and confirmed **sandbox escape**
(a write outside the workspace without `breakglass`, or host socket/credential
passthrough) as **critical** — these are the cases
[`SECURITY.md`](../SECURITY.md#response) prioritizes. Treat workspace/control-plane
tamper without confirmed host reach as **high**. If in doubt, escalate at the
higher severity and let the assessment downgrade it.

## 2. Contain

Stop the affected session(s) and halt further agent execution before you
investigate.

1. **Inventory sessions.** `workcell session list` (add `--verbose` for target
   and workspace transport, or `--json` to script it) shows every recorded
   session with its live status, profile, and id.
2. **Stop a detached session.** `workcell session stop --id SESSION_ID` performs
   a graceful stop; add `--force` to force-remove (kill) the container instead.
   This works only for **detached** sessions started with
   `workcell session start`.
3. **Stop a foreground session.** An interactive `workcell` launch has no
   `session stop` handle. Terminate the launcher process; the runtime container
   is ephemeral by default (`--container-mutability ephemeral`) and does not
   persist after the launch exits.
4. **Halt everything on a profile (defense in depth).** All sessions for a
   profile run inside one dedicated Colima VM. Identify the profile from
   `workcell session list` or `--inspect`, then stop that VM through Colima
   directly (`colima stop -p <profile>`) to halt all execution on it. Do **not**
   delete the profile or VM yet — teardown is step 7, after evidence is
   collected.

## 3. Preserve evidence

Durable session state lives on the host under the Workcell-owned target-state
root (defaults from the
[session supervisor design](workcell-session-supervisor-design.md#data-model)):

- session records:
  `~/.local/state/workcell/targets/local_vm/colima/<profile>/sessions/<session_id>.json`
- audit log:
  `~/.local/state/workcell/targets/local_vm/colima/<profile>/workcell.audit.log`
- legacy records (compatibility reads):
  `~/.colima/<profile>/sessions/<session_id>.json`

The state-root location follows `WORKCELL_STATE_ROOT` (default
`${XDG_STATE_HOME:-~/.local/state}/workcell`) and `COLIMA_STATE_ROOT` (default
`~/.colima`) if those are set in the operator's environment.

- **Do not garbage-collect before you collect.** `workcell --gc` (alias
  `workcell gc`) deliberately deletes transient `session-audit.*` scratch, temp
  scratch, and runtime-image cache residue. Durable session records survive gc,
  but the transient audit scratch does not — running gc during an incident
  destroys evidence. Do not run it until collection is complete.
- **Do not `session delete`.** `workcell session delete` removes the durable
  record and stopped-session artifacts. Defer it to recovery (step 7).
- **Snapshot the state root.** Copy the profile's target-state directory
  (records + `workcell.audit.log`) and any legacy `~/.colima/<profile>/sessions/`
  records to read-only storage before further action, so the on-disk evidence is
  preserved even if later steps mutate host state.

Note: workflow-artifact retention in
[retention-policy.md](retention-policy.md) governs **CI/release** artifacts, not
these host-side session state roots; host evidence is preserved by the operator,
not by a retention timer.

## 4. Collect

Capture a redacted, shareable diagnostics snapshot with the support bundle
(roadmap item G2, shipped):

```sh
workcell support-bundle --output ~/workcell-support-bundle.json
```

It runs entirely host-side and never starts the runtime. It collects install
state, the policy-file inventory, target/state-root layout, per-provider adapter
presence and credential **key names** (never values), durable session-record
summaries, and **audit pointers** (audit-log path, presence, size, and mtime —
never log contents). See [`SUPPORT.md`](../SUPPORT.md#what-it-collects) for the
full field list.

**Redaction guarantees** (also embedded in each bundle under `redaction`, and
documented in [`SUPPORT.md`](../SUPPORT.md#redaction-guarantees)): credential
file contents are never read; workspace and agent output are never collected;
token/key/password/secret/credential material is masked by pattern and by
secret-named `key=value` pairs; and the operator home-directory prefix is
rewritten to `~`. The output shape is deterministic, so two bundles diff cleanly.

Per-session evidence, for the specific `SESSION_ID`(s) from step 2:

- `workcell session show --id SESSION_ID [--text]` — the full durable record.
- `workcell session timeline --id SESSION_ID` — the session-specific audit
  trail.
- `workcell session export --id SESSION_ID --output PATH` — the record plus all
  matching audit records as one JSON bundle (written `0600`).
- `workcell session diff --id SESSION_ID --output PATH` — the workspace change
  set against the clean launch base.
- `workcell session logs --id SESSION_ID --kind audit` — the retained audit log
  for the session. The `debug`, `file-trace`, and `transcript` kinds exist only
  when those lower-assurance host logs were explicitly enabled at launch, and may
  contain workspace or agent output — treat them as sensitive (see step 6).

## 5. Verify evidence integrity

**What exists today.** Session audit records are host-owned records in the
target audit log; the launcher, not the agent, writes them (the host owns the
trusted control plane — see the
[session supervisor design](workcell-session-supervisor-design.md#why-this-shape)).
`workcell session export` bundles the records that match a session id, and the
support bundle records the audit log's size and mtime under `audit_pointers`, so
you can detect truncation or replacement of the durable log against a preserved
snapshot (step 3). Cross-check the exported records, the timeline, and the
preserved `workcell.audit.log` for consistency.

**What does not exist yet.** There is **no cryptographic hash-chain
verification** of session audit records today. Signed, tamper-evident audit
records with external verification tooling are **roadmapped as A5**
(["Signed Session Audit Records", milestone v0.15](../ROADMAP.md#track-a-boundary-depth-and-agent-threat-defenses)) —
a `workcell session verify`-style command is planned there but is **not
shipped**. Until A5 lands, integrity is host-preservation plus consistency
cross-checks, not signature verification. Do not represent current audit records
as cryptographically tamper-proof.

## 6. Report

Escalate through the private channel in [`SECURITY.md`](../SECURITY.md#reporting):
open a [GitHub Private Vulnerability Report][pvr]. **Do not** disclose a
suspected boundary breach in a public issue. Expect acknowledgment within
**5 business days** and an initial assessment within **10 business days**;
sandbox escapes and secret exposure are prioritized
([`SECURITY.md`](../SECURITY.md#response)).

Include:

- the redacted `workcell-support-bundle.json` from step 4;
- the exported session record(s), timeline, and diff for the affected
  `SESSION_ID`(s);
- the observed signal, severity, provider, mode, host OS, and the exact commands
  run.

Do **not** include:

- secrets, credential values, or `.env`-style material;
- raw workspace content or full agent transcripts unless specifically requested
  — the `debug`/`file-trace`/`transcript` logs are the ones most likely to carry
  it.

The support bundle is redacted by construction, but **skim it once before
sharing** ([`SUPPORT.md`](../SUPPORT.md#sharing-it-safely)). If any artifact you
were about to attach looks like it exposed a secret, keep it out of the report
body and describe it to the maintainer over the private advisory instead.

[pvr]: https://github.com/omkhar/workcell/security/advisories/new

## 7. Recover

Only after evidence is collected and reported:

- **Rotate exposed credentials.** If any host secret could have been read,
  rotate it at the source, then re-stage it with `workcell auth unset` /
  `workcell auth set` and confirm with `workcell auth status --agent <provider>`.
- **Tear down the compromised session.** With the session stopped and evidence
  preserved, `workcell session delete --id SESSION_ID` removes the record and
  stopped-session artifacts (use `--dry-run` first to preview the cleanup).
- **Reset the runtime.** Because the strict runtime container is ephemeral, a
  clean relaunch starts from the reviewed image. If the Colima profile is
  suspect, `workcell --repair-profile ...` deletes a conflicting profile before
  the next launch.
- **Close the loop.** If the incident revealed a genuine boundary weakness (not
  just an expected lower-assurance mode), it belongs in the private advisory so a
  fix and regression test can follow — see the coordinated-disclosure model in
  [`SECURITY.md`](../SECURITY.md#disclosure).

## References

- [Runtime-boundary threat model](threat-model.md) — assets, trust boundaries,
  and controls this runbook responds to
- [`SECURITY.md`](../SECURITY.md) — reporting channel, SLAs, and scope
- [`SUPPORT.md`](../SUPPORT.md) — the `workcell support-bundle` field list and
  redaction guarantees
- [Session supervisor design](workcell-session-supervisor-design.md) — durable
  session records, state-root paths, and the host-owned audit model
- [Support tiers and status vocabulary](support-tiers.md) and
  [diagnostics and the support matrix](diagnostics-and-support-matrix.md)
- [CI/CD threat model](ci-threat-model.md) — the separate signing-compromise
  incident runbook
- [`ROADMAP.md`](../ROADMAP.md) — A5 signed session audit records (roadmapped)
