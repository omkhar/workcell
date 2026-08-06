# 1.0 improvement-track delivery record

Status: historical record.

Workcell published `v1.0.2` on 2026-08-05.
This release completed the 1.0 program.
The `v1.0.0` and `v1.0.1` tags did not produce published releases.

This page records the result of the improvement plan that started in July 2026.
It is not the plan for new work.
See the [roadmap](../ROADMAP.md) for current work and sequencing.
See the [1.0 readiness review](1.0-readiness-review.md) for the final evidence.

## Status terms

- **Complete**: The reviewed 1.0 scope shipped.
- **Evaluated**: The evaluation finished, but the result did not create support.
- **Implemented and locally certified**: The feature shipped, and local live certification passed.
- **Complete for 1.0**: The G4 review accepted the shipped scope and named the remaining work.
- **Partial**: Part of the original scope shipped, and named work remains.
- **Post-1.0**: The maintainer explicitly deferred the item.
- **Later**: The item remains an idea and has no delivery commitment.

These terms do not create a support claim.
The [support tiers](support-tiers.md) define the supported targets and assurance classes.

The [roadmap milestone train](../ROADMAP.md#milestone-train) records the historical sequence.

## Track A: boundary and agent-threat controls

| ID | Result | Shipped record |
| --- | --- | --- |
| A1 | Complete | Strict Colima uses default-deny egress rules. The injection policy can add reviewed endpoints. Other targets have explicit assurance labels. See [outbound endpoints](outbound-endpoints.md) and [injection policy](injection-policy.md). |
| A2 | Complete | Workcell denies repository MCP files by default. The lower-assurance exception requires `--allow-repo-mcp` and a dated `--ack-repo-mcp` value. Workcell masks provider and Git control paths by default. See [adapter control planes](adapter-control-planes.md). |
| A3 | Complete | Rust and Go fuzz targets run in the fuzz workflow. See [fuzzing](fuzzing.md) and the `Fuzz` workflow. |
| A4 | Complete | Rust unsafe blocks have `SAFETY:` records. The repository includes an [unsafe-code audit checklist](unsafe-code-audit-checklist.md). |
| A5 | Complete | On chain-capable targets, verification covers records through the signed session head. Workcell seals this head with a per-host ECDSA P-256 key that has no external trust anchor. See [signed session audit records](signed-session-audit-records.md). |
| A6 | Complete | The repository versions the hardening profile and its deterministic checks. See [hardening profile](../policy/hardening-profile.toml) and [invariants](invariants.md). |
| A7 | Complete | The [OWASP agentic mapping](owasp-agentic-mapping.md) records containment, partial coverage, and gaps. |

## Track B: supply chain and release assurance

| ID | Result | Shipped record |
| --- | --- | --- |
| B1 | Complete | The [provenance record](provenance.md) maps the release path to SLSA v1.0 and records unmet levels. |
| B2 | Post-1.0 | Workcell has no second trusted maintainer. The [release process](releasing.md) records single-maintainer controls and does not claim separation of duties. |
| B3 | Complete | The mutation workflow runs weekly and with the `approved-heavy-ci` label. The versioned mutation policy enforces the score. See [mutation policy](../policy/mutation-score-policy.json). Mutation testing is not limited to release preflight. |
| B4 | Complete | Workcell keeps tool pins and the GitHub Actions allowlist in reviewed policy files. See [tool pins](../policy/tool-pins.toml) and [allowed actions](../policy/allowed-actions.toml). |
| B5 | Complete | The [retention policy](retention-policy.md) records workflow retention and post-expiry evidence sources. |
| B6 | Post-1.0 | Certification on hosted Apple Silicon did not ship. The [B6 decision record](b6-disposition-options-draft.md) records the funding and trust limits. Local certification supplies the 1.0 evidence. |
| B7 | Post-1.0 | The OpenSSF Best Practices badge and a funded third-party boundary audit did not ship. |
| B8 | Complete | Heavy pull-request builds require the `approved-heavy-ci` label. Reproducibility runs on each main push and during release preflight. Weekly reports show flake candidates and workflow durations. |
| B9 | Complete | The [CI threat model](ci-threat-model.md) records runner tiers, permissions, secrets, attestations, and incident actions. |

## Track C: runtime platform

| ID | Result | Shipped record |
| --- | --- | --- |
| C1 | Evaluated | The Apple container evaluation returned GO for technical feasibility. The preview writes audit lifecycle records without signatures. Workcell blocks operator launch. Colima remains the reviewed default. See [Apple container evaluation](apple-container-evaluation.md). |
| C2 | Post-1.0 | Workcell has benchmark evidence, but it has no certified claim for session start time. See [session startup benchmarks](session-startup-benchmarks.md). |
| C3 | Implemented and locally certified | Native parallel sessions use separate worktrees, branches, runtimes, and linked session records. Local certification passed on 2026-08-03. |
| C4 | Later | A nested container-tooling lane did not ship. Any future lane must keep the outer VM and container boundary. |
| C5 | Complete | The benchmark lane reports syscall-shim exec and spawn measurements and run-to-run stability. |

## Track D: code health

| ID | Result | Shipped record |
| --- | --- | --- |
| D1 | Complete | The repository agreement assigns Rust to the shim, Go to policy and orchestration, and shell to glue. |
| D2 | Complete | Shared shell helpers and the shellcheck lane shipped. |
| D3 | Complete | Go owns most of the static invariant scope. Bash keeps an accepted static residual, the dynamic tail, and smoke orchestration. The G4 review accepted this boundary. |
| D4 | Complete | Launcher modules and the [launcher contract](launcher-contract.md) shipped. |
| D5 | Partial | Workcell extracted the Rust git-policy module. More interception-library splits remain after 1.0. |
| D6 | Complete for 1.0 | Workcell split the large Go validators by major format. Small inline validators and a dispatcher refactor remain code-health work. |
| D7 | Partial | Property-based session tests and shell-protocol coverage shipped. More Go benchmarks and shell unit tests remain. |
| D8 | Complete | The [stability contract](stability-contract.md) records public, internal, and exit-code rules. |

## Track E: documentation and adoption

| ID | Result | Shipped record |
| --- | --- | --- |
| E1 | Complete | README entry points and the documentation map serve operators, evaluators, and contributors. |
| E2 | Complete | The [system design](workcell-system-design.md) contains maintained architecture diagrams. |
| E3 | Complete | The [support tiers](support-tiers.md) and [diagnostics guide](diagnostics-and-support-matrix.md) define the emitted support fields. |
| E4 | Complete | Documentation CI checks links, orphans, spelling, man pages, support-field parity, and public-contract drift. |
| E5 | Complete | The [injection policy](injection-policy.md) has an annotated schema and provider examples. |
| E6 | Post-1.0 | A rendered documentation site, external demos, a Homebrew tap, and an architecture article did not ship. |
| E7 | Complete | Contributor procedures and adapter-specific README files shipped. |

## Track F: enterprise and standards

| ID | Result | Shipped record |
| --- | --- | --- |
| F1 | Complete | `workcell session export` produces JSON bundles and OCSF 1.3.0 JSON Lines streams. |
| F2 | Later | SPIFFE-style session identity did not ship. |
| F3 | Complete | The [standards watchlist](standards-watchlist.md) records sources, owners, and review cadence. |

## Track G: 1.0 contract and operations

| ID | Result | Shipped record |
| --- | --- | --- |
| G1 | Complete | The repository versions the public contract inventory, v1 stability classes, deprecation policy, and drift checks. |
| G2 | Complete | `workcell support-bundle` ships with documented collection and redaction rules. This command is not future work. |
| G3 | Complete | The tagged-release installer verifies by default and has an acknowledged bypass. Direct `install.sh` runs from a source tree or downloaded bundle do not perform the tagged-release verification. Hosted checks prove installation and link removal, not complete uninstall behavior. See [install lifecycle](install-lifecycle.md). |
| G4 | Complete | The [1.0 readiness review](1.0-readiness-review.md) records fixed evidence identities, scope decisions, and no unresolved P0 or P1 findings. |

## Provider item: Google Antigravity

Google Antigravity support did not ship in the 1.0 program.
The v0.14 work delivered a fail-closed adapter scaffold.
It does not install, authenticate, or launch Antigravity.

### Current status

The [provider matrix](provider-matrix.md) lists Antigravity as unsupported.
The `Current support` table and supported-provider set exclude it.
The roadmap keeps it as post-1.0 provider work.
Do not use the old milestone assignment as a support claim.

## Recorded scope decisions

- On 2026-07-09, the maintainer deferred B2, B6, B7, and E6.
- On 2026-08-01, the G4 review deferred C2 and its certified start time target.
- On 2026-08-04, the G4 review completed with no unresolved P0 or P1 finding.
- On 2026-08-05, Workcell published `v1.0.2` and completed the 1.0 program.

## Evidence index

- The [1.0 readiness review](1.0-readiness-review.md) gives the final item status and exact evidence identities.
- The [roadmap program record](../ROADMAP.md#10-program-record) records the release result and deferred work.
- The [release process](releasing.md) records the single-maintainer controls.
- The [support tiers](support-tiers.md) record the current support boundary.

The repository does not claim independent approval or hosted real-boundary certification for 1.0.

## Current work

Do not add new planned work to this historical record.
Add current work to the [roadmap](../ROADMAP.md) or to a focused design record.
If a shipped fact changes, update this record and the roadmap in the same review unit.
