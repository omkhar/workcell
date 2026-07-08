# Enterprise Evidence Baseline

This page records the Phase 11 evidence baseline for enterprise evaluation. It
is an evidence map, not a compliance certification or an independent audit
report.

Workcell currently supports local-first rollout on reviewed Apple Silicon macOS
hosts. Evidence for broader host support, centralized policy, fleet inventory,
or managed-workstation execution must land in later phases before those become
support claims.

## Evidence Packet

| Area | Current source |
|---|---|
| Architecture and data flow | [workcell-system-design.md](workcell-system-design.md) (with boundary-stack, policy-to-injection, and control-plane masking diagrams), [invariants.md](invariants.md), [adapter-control-planes.md](adapter-control-planes.md) |
| Runtime and target boundaries | [remote-vm-contract.md](remote-vm-contract.md), [managed-workstation-contract.md](managed-workstation-contract.md), [host-expansion-readiness.md](host-expansion-readiness.md), [policy/host-support-matrix.tsv](../policy/host-support-matrix.tsv) |
| Threat model and non-protections | [threat-model.md](threat-model.md), [invariants.md](invariants.md), [enterprise-rollout.md](enterprise-rollout.md) |
| Validation evidence | [validation-scenarios.md](validation-scenarios.md), [requirements-validation.md](requirements-validation.md), [use-case-matrix.md](use-case-matrix.md) |
| Release provenance, SBOMs, and signing | [provenance.md](provenance.md), [github-workflows.md](github-workflows.md), [releasing.md](releasing.md) |
| Support boundary and rollout | [enterprise-rollout.md](enterprise-rollout.md), [provider-matrix.md](provider-matrix.md), [../SUPPORT.md](../SUPPORT.md), [../ROADMAP.md](../ROADMAP.md) |

## Audit Schema And Retention

Current audit and session evidence is host-local:

- profile audit logs live under Workcell-owned target state, for example
  `~/.local/state/workcell/targets/<target-kind>/<provider>/<profile>/workcell.audit.log`
- audit records are append-only text records with timestamps, event fields,
  assurance data, and chained record digests
- launched sessions write durable JSON records under the same target-state root
- `workcell session timeline` filters audit records for one session
- `workcell session export` bundles a session record with matching audit records
- durable session records intentionally survive `workcell --gc`
- `workcell session delete` removes a stopped session record and recorded
  session-owned artifacts, but it does not rewrite the shared profile audit log

Retention remains operator or organization owned. Workcell does not yet provide
a centralized retention policy or fleet inventory. SIEM ingestion is served by
the OCSF-mapped export below (an export format, not a hosted pipeline or
retention service).

### OCSF-Mapped Export (Phase 17)

`workcell session export --format ocsf` renders the session record and its audit
lifecycle events as [OCSF](https://schema.ocsf.io/) JSON Lines — one
**Application Lifecycle** event (`category_uid` 6, `class_uid` 6002) per line —
so a SIEM can ingest Workcell session evidence without a bespoke parser. The
default `--format json` bundle is unchanged.

- **Mapping.** One summary event is emitted for the session record (a finished
  session maps to `activity_id` 4 "Stop", a live one to 3 "Start"), followed by
  one event per audit record. Audit `event=session_started`/`launch` map to
  Start, `session_finished`/`exit` to Stop, and every other lifecycle event to
  99 "Other". The agent/mode become the OCSF `app` object and the sandbox target
  the `device` object — both sourced from the authoritative session record so
  every standalone event carries them, even though the launcher audit lines do
  not repeat the target on each line. The remaining session/audit fields are
  preserved verbatim under the OCSF `unmapped` object so no evidence is dropped.
- **Versioning.** Every event carries `metadata.version` (the OCSF schema
  version) and `metadata.mapping_version` (the Workcell mapping version) so
  consumers can pin to an exact mapping and handle future changes.
- **Redaction.** The OCSF output is redacted by the **same** rule-set as
  `workcell support-bundle` (see [../SUPPORT.md](../SUPPORT.md)): credential,
  token, key, and secret material is masked and the operator home prefix is
  rewritten to `~`, so no secret or local username leaks into exported events.
- **Integrity.** Audit records are decoded from their on-disk encoding before
  mapping, chosen per the session's writer: launcher backends
  (colima/docker-desktop/aws-ec2-ssm/gcp-vm) write bash `printf %q`, so
  space-delimited fields such as the endpoints allowlist are preserved intact
  rather than truncated; the Apple-container target percent-encodes its path
  fields, so a literal backslash in a workspace path survives instead of being
  mis-read as a shell escape. A record that carries a duplicate key — the shape a
  tampered line takes — fails the export closed and emits no events, so a forged
  record can never surface as an OCSF event. Only field names the audit writers
  actually emit become `unmapped` properties; any other key (a mutable audit line
  could carry a secret-shaped key) is bucketed under one fixed, redacted property
  rather than becoming a JSON property name, so a secret can never leak in a key.

## Known Gaps

These are intentionally not claimed today:

- independent SOC 2, ISO 27001, or similar certification
- centralized Workcell RBAC, SSO, SCIM, analytics, or inventory
- Linux, Windows, Linux `arm64`, or Raspberry Pi operator-host support
- managed-workstation provider support
- automatic backend fallback
- proof that release provenance alone proves the full local runtime boundary

## Control Mapping Aid

The mappings below are evaluation aids for buyers and reviewers. They are not
claims that Workcell is certified against those frameworks.

| Framework area | Workcell evidence to inspect |
|---|---|
| SOC 2 logical access and change management | host-side policy commands, signed PR publication, release provenance, hosted-control audits |
| SOC 2 system operations and monitoring | validation scenarios, session audit records, release workflow evidence |
| SOC 2 risk mitigation | threat model, invariants, support matrix, lower-assurance mode labeling |
| ISO 27001 access control | injection policy, provider bootstrap matrix, host-owned credential staging |
| ISO 27001 configuration and change management | operator contract, requirements traceability, signed commits, PR-parity validation |
| ISO 27001 logging and monitoring | session records, audit logs, hosted-control audits, release attestations |
| ISO 27001 secure development and supplier controls | pinned upstream verification, reproducible builds, SBOM publication, vulnerability reporting |
| OWASP Top 10 for Agentic Applications (2026) | [owasp-agentic-mapping.md](owasp-agentic-mapping.md) — a conservative posture map, not a conformance claim |

## Quality Gate

Enterprise evidence must stay reviewable and current:

- do not convert roadmap items into support claims
- keep evidence links repo-local and machine-checked when practical
- remove duplicated or vague assurance language during peer review
- update the evidence map in the same change as any support-tier, release,
  audit, or runtime-boundary change that affects it
