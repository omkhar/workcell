# Enterprise Evidence Baseline

This page records the Phase 11 evidence map. It is not a certification or an
independent audit report.

Workcell supports operator launch only on the allowed macOS arm64 matrix rows.
Broader host support, centralized policy, fleet inventory, and a managed
workstation need new implementation and evidence.

## Evidence Map

| Area | Current source |
|---|---|
| Architecture and data flow | [System Design](workcell-system-design.md), [Invariants](invariants.md), [Adapter Control Planes](adapter-control-planes.md) |
| Runtime and target boundaries | [Remote VM Contract](remote-vm-contract.md), [Managed Workstation Contract](managed-workstation-contract.md), [Host Expansion Readiness](host-expansion-readiness.md), [host-support matrix](../policy/host-support-matrix.tsv) |
| Threats and non-protections | [Threat Model](threat-model.md), [Invariants](invariants.md), [Enterprise Rollout](enterprise-rollout.md) |
| Validation | [Validation Scenarios](validation-scenarios.md), [Requirements and Validation](requirements-validation.md), [Use-Case Matrix](use-case-matrix.md) |
| Release provenance | [Provenance](provenance.md), [GitHub Workflows](github-workflows.md), [Release Process](releasing.md) |
| Support and rollout | [Enterprise Rollout](enterprise-rollout.md), [Provider Matrix](provider-matrix.md), [Support](../SUPPORT.md), [Roadmap](../ROADMAP.md) |

## Audit and Session Evidence

Audit and session evidence is host-local. New state uses the Workcell-owned
target-state tree. The exact root depends on the target kind, provider, and
profile.

A launched session can create these records:

- An append-only profile audit log with event, time, and assurance fields.
- A chained digest for each supported audit record.
- A durable JSON session record.
- A best-effort host signature for the final session-chain head.
- Pointers to the debug log, transcript log, file-trace log, session audit
  directory, and shared audit log when the session record has these paths.

`workcell session verify` recomputes the authoritative chain and checks the seal
and public key. Use [Signed Session Audit Records](signed-session-audit-records.md)
for its guarantees and legacy limits.

`workcell session timeline` selects audit records for one session.
`workcell session export` creates a session bundle. `workcell --gc` preserves
durable session records. `workcell session delete` removes a stopped record. A
full delete also removes the recorded stopped container, debug log, file-trace
log, transcript log, session audit directory, and audit seal when they exist.

It does not remove the recorded isolated clone. It does not rewrite the shared
profile audit log.

The operator or organization owns retention. Workcell has no central retention
service or fleet inventory.

## OCSF Export

`workcell session export --format ocsf` writes OCSF 1.3.0 JSON Lines. Each line
is an Application Lifecycle event with `category_uid` 6 and `class_uid` 6002.
The default JSON bundle is unchanged.

The export has these properties:

- It writes one summary event and one event for each audit record that matches
  the session.
- It records OCSF schema version `1.3.0` and Workcell mapping version `2`.
- It applies the shared support-bundle redactor to each dynamic session or
  audit string value.
- It replaces free-form session messages and unexpected audit fields with a
  fixed redaction value.
- It stops on a duplicate audit identity key.
- It drops an audit record when the decoded session identifier does not match
  the exported session.
- It maps a torn crash fragment to an unknown result, not a success result.

The shared redactor masks credentials and tokens. It replaces the operator home
prefix with `~`. It can retain any non-home path that does not match another
redaction rule.

## Claims That Workcell Does Not Make

Workcell does not claim:

- Independent SOC 2, ISO 27001, or equivalent certification.
- Central Workcell RBAC, SSO, SCIM, analytics, retention, or inventory.
- Linux or Windows operator-host support.
- Managed-workstation provider support.
- Automatic target fallback.
- That release provenance proves the complete local runtime boundary.

## Control Mapping Aid

These mappings help an evaluator find evidence. They are not conformance claims.

| Framework area | Evidence to inspect |
|---|---|
| Access and change management | Host policy commands, signed publication, provenance, and hosted-control audits |
| Operations and monitoring | Validation scenarios, session audit records, and release workflow evidence |
| Risk treatment | Threat model, invariants, support matrix, and lower-assurance labels |
| Configuration management | Operator contract, requirements traceability, signed commits, and `pr-parity` evidence |
| Logging | Session records, audit logs, hosted-control audits, and release attestations |
| Secure development and suppliers | Pinned upstream checks, reproducible builds, SBOMs, and vulnerability reporting |
| OWASP agentic applications | [OWASP Agentic Mapping](owasp-agentic-mapping.md), which is a conservative posture map |

Update this map in the same change as a support, release, audit, or
runtime-boundary claim that changes the evidence map.
