# Implement-First Delivery Record

This page records the bridge from the session platform to runtime-target
Phases 1 through 12. Workcell completed these phases.

Use [Runtime Target Phase Record](runtime-target-phase-plan.md) for the phase
results. Use [Runtime Target Expansion](runtime-target-expansion-plan.md) for
the current target program.

Use
[`policy/host-support-matrix.tsv`](../policy/host-support-matrix.tsv) for
support decisions. This page does not authorize a support change.

## Phase 13 Active Slice

Phase 13 evaluates one Linux amd64 `local_compat` candidate. It does not change
operator support.

### Candidate

The candidate target is `local_compat/docker-desktop/compat`. Select one Linux
distribution, distribution version, and Docker Desktop version before
implementation. The current wildcard matrix row stays `unsupported` and
`blocked`.

### Promotion Gates

Before promotion, do all these actions:

- Replace the wildcard row with one exact host and target row.
- Block launch outside that row with clear diagnostics.
- Add a fail-closed gate for the selected Docker Desktop version.
- Add deterministic selection and negative tests.
- Complete live certification on the selected operator host.
- Add lifecycle, support-bundle, operator, and validation documents.

Use [Host Expansion Readiness](host-expansion-readiness.md) for the complete
promotion gate.

### Non-Goals

- Do not claim Linux operator support before promotion.
- Do not add a generic Linux `local_compat` row.
- Do not claim Linux `strict` support.
- Do not change the supported macOS rows.

Use [ROADMAP.md](../ROADMAP.md) for later planned work.
