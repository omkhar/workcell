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
4. **Halt the target's isolation boundary (defense in depth).** Stopping the
   session (steps 2-3) stops its container; the general principle for a fuller
   halt is then to bring down the target's isolation boundary. That command is
   **target-specific**, so first confirm which target the **suspect session**
   used. Read it from that session's record — `workcell session show --id
   SESSION_ID` (or `workcell session list --verbose`) reports the record's
   `target_kind` and `target_provider`. Do not use `--inspect` for this: it
   prints your *current* launch options, not the suspect session's target. At 1.0
   the operator-reachable targets on the supported macOS arm64 host are the two
   `launch=allowed` rows in
   [`policy/host-support-matrix.tsv`](../policy/host-support-matrix.tsv):
   - **`local_vm` / Colima (the default, strict target).** All sessions for a
     profile run inside one dedicated Colima VM. Take the profile from the session
     record (`workcell session show --id SESSION_ID`), then stop that VM through
     Colima directly. Pin the Colima home so you hit the same VM the launcher
     created — the launcher always invokes Colima with `COLIMA_HOME=~/.colima`
     (`scripts/workcell:620-632`, `run_host_colima`) — so run
     `COLIMA_HOME=~/.colima colima stop -p <profile>` to halt all execution on it.
     Note: stopping the VM removes the profile's Docker socket, so a later
     `workcell session delete` cannot clean container artifacts until the VM is
     running again (see step 7). Host-side collection in step 4 does not need the
     VM, so this halt is safe to do before collecting.
   - **`local_compat` / Docker Desktop (compat, lower assurance).** The isolation
     boundary is the Docker Desktop container/context, not a Workcell-dedicated
     VM, so there is no `colima stop` to run; `workcell session stop --id
     SESSION_ID --force` (kill/remove the container) is the boundary teardown.
   Do **not** delete the profile, VM, or container yet — teardown is step 7,
   after evidence is collected. The `remote_vm` preview targets (`aws-ec2-ssm`,
   `gcp-vm`) are `preview-only` with `launch=blocked` in the matrix, so no live
   operator session runs on them at 1.0 and they are out of scope here; if you
   ever tear down a preview broker, follow that target's own teardown, not
   `colima stop`.

## 3. Preserve evidence

Durable session state lives on the host under the Workcell-owned target-state
root, and the path is **target-aware**. Identify the target first — the same
`target_kind` and `target_provider` from the suspect session's record
(`workcell session show --id SESSION_ID`, or `workcell session list --verbose`)
you read in step 2 — then preserve that target's tree.

The general layout (`internal/host/hoststate/profilepaths.go`
`ProfileTargetStateDir` /`ProfileSessionsDirPath` / `ProfileAuditLogPath`) is
`<target-state-root>/<target_kind>/<target_provider>/<profile>/`, where the
target-state root is `${WORKCELL_STATE_ROOT}/targets` and `WORKCELL_STATE_ROOT`
defaults to `${XDG_STATE_HOME:-~/.local/state}/workcell` (`scripts/workcell:91-92`):

- session records:
  `.../<target_kind>/<target_provider>/<profile>/sessions/<session_id>.json`
- audit log (profile-wide, shared by all sessions in the profile):
  `.../<target_kind>/<target_provider>/<profile>/workcell.audit.log`

For the two operator-reachable targets at 1.0 this resolves to:

- **`local_vm` / Colima (default, strict):**
  `~/.local/state/workcell/targets/local_vm/colima/<profile>/{sessions/,workcell.audit.log}`
- **`local_compat` / Docker Desktop (compat):**
  `~/.local/state/workcell/targets/local_compat/docker-desktop/<profile>/{sessions/,workcell.audit.log}`

The Colima target additionally has a **legacy** tree that compatibility reads
still accept, and Docker Desktop does **not** (`LegacyProfileSessionsDirPath` /
`LegacyProfileAuditLogPath` are keyed on the Colima state root):

- legacy records: `~/.colima/<profile>/sessions/<session_id>.json`
- legacy profile-wide audit log (a profile-level file **outside** `sessions/`):
  `~/.colima/<profile>/workcell.audit.log`

`WORKCELL_STATE_ROOT` is operator-overridable, so if it is set in your
environment the `targets/...` tree lives under that root instead of
`~/.local/state/workcell`. The Colima legacy tree is **not** overridable: the
launcher unconditionally resets `COLIMA_STATE_ROOT` to `~/.colima`
(`scripts/workcell:90`, `COLIMA_STATE_ROOT="${REAL_HOME}/.colima"`), so the
legacy Colima path is always `~/.colima/<profile>/` regardless of any
`COLIMA_STATE_ROOT` you export.

- **Do not garbage-collect before you collect.** `workcell --gc` (alias
  `workcell gc`) deliberately deletes transient `session-audit.*` scratch, temp
  scratch, and runtime-image cache residue. Durable session records survive gc,
  but the transient audit scratch does not — running gc during an incident
  destroys evidence. Do not run it until collection is complete.
- **Do not `session delete`.** `workcell session delete` removes the durable
  record and stopped-session artifacts. Defer it to recovery (step 7).
- **Snapshot the state root.** Copy your target's profile directory
  (`.../<target_kind>/<target_provider>/<profile>/`) whole — session records plus
  the profile-wide `workcell.audit.log`. For the **Colima** target, also copy the
  legacy tree under `~/.colima/<profile>/` — both the legacy `sessions/` records
  and the legacy profile-level `~/.colima/<profile>/workcell.audit.log`, since a
  legacy record can point its `audit_log_path` at that profile-level file outside
  `sessions/` (the Docker Desktop target has no legacy tree). Copy all of them to
  read-only storage before further action, so the on-disk evidence is preserved
  even if later steps mutate host state.

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
  trail. Audit records can include raw `session send` message text, so treat this
  as sensitive (see step 6).
- `workcell session export --id SESSION_ID --output PATH` — the record plus all
  matching audit records as one JSON bundle (written `0600`). The bundled audit
  records carry the same raw message text (see step 6).
- `workcell session diff --id SESSION_ID --output PATH` — the workspace change
  set against the clean launch base: a `[status]` file list plus a `[diff]`
  section of raw file contents. The raw contents are sensitive (see step 6).
- `workcell session logs --id SESSION_ID --kind audit` — prints the **entire
  profile-wide** `workcell.audit.log` resolved from the session's record, not a
  per-session slice: it is shared by every session in the profile, so you see all
  sessions' events, which is useful for cross-session correlation around the
  suspect session. Use `session timeline` (above) when you want only the suspect
  session's entries. The `debug`, `file-trace`, and `transcript` kinds exist only
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

Include (redaction-safe by default):

- the redacted `workcell-support-bundle.json` from step 4;
- the durable session record(s) for the affected `SESSION_ID`(s) — the
  structured metadata from `workcell session show`;
- from `workcell session diff`, the **`[status]` summary only** — the list of
  changed and untracked files (`git status --short`) — plus, if useful, file
  counts and hashes. This is metadata about *which* files changed, not their
  contents;
- the observed signal, severity, provider, mode, host OS, and the exact commands
  run.

Review and redact before sharing (may contain secrets/proprietary content):

- The `[diff]` section of `workcell session diff` output. It is a full
  `git diff` of raw workspace file contents (`render_session_diff_bundle` in
  `scripts/workcell` runs `git diff --no-ext-diff --no-textconv` against the
  launch base), so a compromised session that wrote secrets or changed
  proprietary files puts that content directly into the diff.
- The **audit log and anything that reads it** — `workcell session timeline`,
  `workcell session logs --id SESSION_ID --kind audit`, and the audit records
  bundled by `workcell session export`. When a detached session was steered with
  `workcell session send`, the launcher writes the **raw message text** into the
  audit log as `argv=<message>` (`scripts/workcell:3739-3742`), so the timeline,
  the audit log, and the exported audit records can contain raw operator/agent
  message content — including secrets or sensitive data an operator typed.

Treat all of these like the `debug`/`file-trace`/`transcript` logs: raw content
that must be reviewed and redacted by the operator before it crosses the trust
boundary into a report.

Do **not** include:

- secrets, credential values, or `.env`-style material;
- raw workspace content, raw audit/timeline message text, or full agent
  transcripts unless specifically requested — the `session diff` `[diff]` body,
  the audit log / timeline / `session logs --kind audit` (raw `session send`
  messages), and the `debug`/`file-trace`/`transcript` logs are the ones most
  likely to carry it.

The support bundle is redacted by construction, but **skim it once before
sharing** ([`SUPPORT.md`](../SUPPORT.md#sharing-it-safely)). The session diff,
the audit log / timeline / exported audit records, and the lower-assurance logs
are **not** redacted — review them yourself first. If any artifact you were about
to attach looks like it exposed a secret, keep it out of the report body and
describe it to the maintainer over the private advisory instead.

[pvr]: https://github.com/omkhar/workcell/security/advisories/new

## 7. Recover

Only after evidence is collected and reported:

- **Rotate exposed credentials.** If any host secret could have been read,
  rotate it at the source, then re-stage it with `workcell auth unset` /
  `workcell auth set` and confirm with `workcell auth status --agent <provider>`.
- **Tear down the compromised session.** `workcell session delete --id
  SESSION_ID` removes the durable record and stopped-session artifacts (use
  `--dry-run` first to preview the cleanup). It has two preconditions: the
  session must be in a terminal status (exited, failed, or stopped — stop it
  first, per step 2), and cleaning the session **container** needs the profile's
  Docker socket available, i.e. the Colima VM running. If you halted the VM in
  step 2.4, normal `session delete` refuses container cleanup and tells you to
  retry with the socket available; in that state either restart the VM
  (`COLIMA_HOME=~/.colima colima start -p <profile>`) and delete, or pass
  `--record-only` to remove just the durable record (keeping container/log
  artifacts), or remove the preserved state-root files by hand after evidence
  collection.
- **Reset a suspect Colima profile.** `--repair-profile` is **not** a reset for a
  compromised VM: it only deletes an *unmanaged* profile (one that pre-exists
  without Workcell ownership metadata) as a launch-time conflict repair, and does
  nothing to a Workcell-managed profile. To fully reset a suspect profile, delete
  it through Colima's own CLI, pinning the same home the launcher uses so you hit
  the Workcell VM — `COLIMA_HOME=~/.colima colima delete -p <profile> --force`
  (`scripts/workcell:620-632`) — then a fresh `workcell` launch recreates a clean
  managed profile from the
  reviewed image. Because the strict runtime container is ephemeral, a clean
  relaunch otherwise starts from the reviewed image already.
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
