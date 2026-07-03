# Improvement Tracks Implementation Plan

This document turns the
[Path To 1.0](../ROADMAP.md#path-to-10) program and its
[Engineering And Ecosystem Improvement Tracks](../ROADMAP.md#engineering-and-ecosystem-improvement-tracks)
into a concrete, milestone-based implementation plan. The tracks came from
the 2026-07 repository review (documentation, Rust/Go/shell source, tests,
CI, release workflows) plus external research on the 2025–2026
agent-sandboxing ecosystem. This plan records sequencing, per-item
implementation shape, exit gates, and validation expectations. It creates no
support claims; every support-visible change still lands through the
host-support matrix, provider matrix, and certification gates.

Track and item identifiers (`A1`, `B3`, `G1`, ...) refer to the roadmap
tracks and stay stable across both documents. Milestones match the roadmap's
Milestone Train; versions are indicative and may split.

## Principles

- tests first: every code-bearing item starts by writing the failing
  deterministic test or check that defines done
- one review unit per item or sub-item; no bundling unrelated tracks into one
  PR
- docs, contract surfaces, and validation evidence land in the same change as
  the behavior they describe
- refactors (Track D) must be behavior-preserving and proven by the existing
  test surface before and after
- security-depth items (Track A) fail closed by default; any relaxation is an
  explicit labeled lower-assurance path
- nothing in this plan weakens the VM-plus-container boundary, staged
  credential model, or host-side publication rules
- 1.0 is a truth claim: no milestone content ships a support label ahead of
  its evidence

## Competitive Drivers

The milestone ordering answers the current landscape directly:

- repo-defined MCP servers are a proven one-keypress RCE class across three
  of the four supported provider CLIs (Claude Code, Gemini CLI, Copilot CLI
  per the TrustFall disclosure) → A2 lands first (v0.12)
- per-session egress allowlists are table stakes in every adjacent runtime
  and in national-agency MCP guidance; strict Colima launches already enforce
  default-deny allowlisting → A1 (v0.13) documents, extends, and brings
  target parity to that shipped control instead of building a duplicate lane
- microVM-per-session backends with warm starts are the mainstream
  comparison point → C1/C2 anchor v0.14
- parallel worktree-per-agent sessions are the 2026 unit of agent work →
  C3 is in 1.0 scope (v0.15), not a post-1.0 idea
- upstream retires Gemini CLI personal-account tiers in June 2026 →
  the Antigravity Tier 1 adapter track runs inside the 1.0 window (v0.14)
- enterprises evaluate against OWASP agentic guidance, SIEM-ready export,
  and SLSA levels → A7, F1, B1/B2 sit on the critical path
- container-in-sandbox (C4) stays post-1.0: it is a differentiator response,
  not a 1.0 gate, and must not risk the outer boundary

## Delivery Model

| Milestone | Theme | Contents |
|---|---|---|
| v0.12 | Containment and hygiene | A2, A7, B3, B4, B5, D1, D2, E3, E4 |
| v0.13 | Boundary depth and stability | A1, A3, A4, B1, B7 (badge), C5, D8, E1, E2, F3, G1 (inventory) |
| v0.14 | Platform, speed, and adoption | C1, C2, B8, B9, D3 (start), D4, E5, E6, E7, G2, Antigravity Tier 1 adapter track |
| v0.15 | Enterprise evidence and release assurance | A5, A6, B2, B6, C3, D5, D7, F1, G3 |
| v1.0-rc | Freeze and gate | G1 (freeze), G4, D3 (complete), D6 |
| post-1.0 | Reach expansion | Phases 13–19 remainder, C4, B7 (audit completion), F2 |

Items inside a milestone are independently shippable and individually
reviewable. Later-milestone items may start early when nothing earlier gates
them.

## Milestone v0.12: Containment And Hygiene

### A2: Repo-Defined MCP And Agent-Config Containment

- Steps: inventory repo-local MCP/tool-config/instruction surfaces per
  provider (Codex, Claude, Copilot, Gemini) from the adapter control-plane
  docs; extend workspace control-plane masking policy to classify each
  surface as `deny`, `mask`, or `ack-required`; implement `strict`-mode
  deny-by-default for repo-defined MCP server definitions; add an explicit
  per-path acknowledgement input on the reviewed policy path; record the
  decision in the session record.
- Exit gates: deterministic tests prove repo-local MCP config cannot reach
  the provider process in `strict` without acknowledgement; masking tables in
  [`adapter-control-planes.md`](adapter-control-planes.md) updated; scenario
  manifest parity holds; injection-policy docs describe the new keys.
- Validation: unit tests per adapter surface, one scenario per provider,
  mutation coverage over the new policy branch points.
- Size: M. Dependencies: none.

### A7: OWASP Agentic Top 10 Control Mapping

- Steps: write `docs/owasp-agentic-mapping.md` mapping each Workcell control
  to ASI01–ASI10 (staged credentials → ASI03, control-plane masking → ASI04,
  VM boundary → ASI05, session records → observability), including explicit
  non-coverage rows; link from the threat model and the enterprise evidence
  baseline.
- Exit gates: every ASI row states covered/partial/not-covered with the
  enforcing mechanism or gap named; docs lint and link checks pass.
- Validation: docs lanes; review lens from the threat-model owner.
- Size: S. Dependencies: none.

### B3: Mutation Testing Gated In CI

- Steps: add a scheduled and `approved-heavy-ci` mutation lane invoking the
  existing `scripts/run-mutation-tests.sh` path (today mutation tests run
  only inside the release-preflight `validate-repo.sh` profile); record the
  current score as the baseline in a reviewed policy file; fail the lane when
  the score drops below baseline; surface the score in the job summary.
- Exit gates: lane green on `main`; baseline file reviewed; release preflight
  refuses when the recorded baseline regresses.
- Validation: deliberate mutant-survival dry run proves the gate trips.
- Size: M. Dependencies: none. Land before the Track D refactors so the
  refactors inherit a scored safety net.

### B4: Centralized Tool Pins And Action Allowlist

- Steps: create a reviewed policy file holding tool pins (actionlint, zizmor,
  syft, cosign, buildx, buildkit, QEMU) with integrity hashes; refactor
  workflows to consume the central values; add a permitted-actions allowlist
  and a checker that every workflow `uses:` entry matches it; wire both into
  pre-commit and release preflight.
- Exit gates: no workflow carries an inline unpinned or off-allowlist tool;
  checker fails on a seeded violation; pin-hygiene lane covers the new file.
- Validation: negative-test the checker; full CI pass.
- Size: S. Dependencies: none.

### B5: Audit-Trail Retention Policy

- Steps: write `docs/retention-policy.md` recording artifact retention per
  workflow with justification; extend release-evidence artifact retention to
  90 days; document how to query GitHub attestations and the Rekor
  transparency log after artifact expiry.
- Exit gates: retention values in workflows match the documented policy; a
  drift check compares them.
- Validation: docs lanes plus the drift check.
- Size: S. Dependencies: none.

### D1: Language-Boundary Doctrine

- Steps: add a language-boundary section to `AGENTS.md` (or a referenced
  standalone doc): Rust only for syscall interception and exec guards, Go for
  policy/state/orchestration logic, shell only as thin glue; new logic
  defaults to Go; shell growth beyond glue requires stated justification.
- Exit gates: doctrine reviewed and linked from contributor docs.
- Validation: docs lanes.
- Size: S. Dependencies: none. Gates D2–D6 direction.

### D2: Shared Shell Library And Shellcheck Lane

- Steps: extract duplicated `cleanup`, `require_tool`, `die`, JSON, and
  resolver helpers into `scripts/lib/` shared sources; convert call sites
  incrementally (one reviewable batch per PR); add a shellcheck lane at
  warning severity (including SC2154) over `scripts/`.
- Exit gates: duplicate helper definitions reduced to the shared sources;
  shellcheck lane green; scenario suite unchanged before/after each batch.
- Validation: existing scenario and smoke suites per batch; shellcheck in CI.
- Size: M. Dependencies: D1.

### E3: Support-Tier Legend And Diagnostics Guide

- Steps: write `docs/support-tiers.md` defining `strict`, `compat`,
  `preview`, `certification candidate`, `experimental`, and `unsupported`
  with current examples from the host-support matrix; write
  `docs/diagnostics-and-support-matrix.md` explaining `--doctor`/`--inspect`
  fields (`support_matrix_*`) with a triage decision tree; link both from
  README and SUPPORT.
- Exit gates: every tier word used in README/ROADMAP resolves to one
  definition; diagnostics fields in the guide match the emitting code.
- Validation: docs lanes; a deterministic check that documented
  `support_matrix_*` field names match the code-emitted set.
- Size: S. Dependencies: none.

### E4: Docs CI Depth

- Steps: add intra-repo markdown link checking and orphan detection to the
  docs workflow; add status labels (active/planning/historical) to planning
  docs; add last-verified release markers to the threat model, invariants,
  and provider matrix; document the local docs-validation command in
  CONTRIBUTING.
- Exit gates: link checker fails on a seeded broken link; every `docs/*.md`
  is either linked from an index/README or explicitly labeled.
- Validation: docs workflow green with the new checks.
- Size: S. Dependencies: none. Unblocks confidence for E1 restructuring.

## Milestone v0.13: Boundary Depth And Stability

### A1: Egress Policy Depth And Target Parity

Strict Colima launches already enforce default-deny, per-session egress
allowlisting (`runtime/profiles/strict.env` sets `NETWORK_POLICY=allowlist`;
the launcher computes per-session `ALLOW_ENDPOINTS`, applies them through the
VM-level egress helper, and prints and audits the endpoint set). A1 is the
delta on that shipped control, not a new lane.

- Steps: document the shipped mechanism as a reviewed policy artifact
  alongside A6; design review for the delta — an operator-facing
  injection-policy surface for reviewed per-session allowlist extensions and
  tightenings, domain/DNS-level and proxy-based filtering options for
  finer-grained control, and enforcement-parity labeling for targets where
  the allowlist helper does not apply; implement the reviewed delta with
  fail-closed diagnostics.
- Exit gates: the shipped mechanism is documented and covered by
  deterministic tests (blocked/allowed flows, audited endpoint recording);
  operator extensions flow through the reviewed policy path; targets without
  allowlist enforcement are explicitly labeled; docs (injection policy,
  invariants, threat model) updated; no weakening of the shipped default.
- Validation: unit plus scenario tests; live boundary exercise on the strict
  path for the delta before any support-visible claim.
- Size: M. Dependencies: design review; coordinates with A6 artifacts.

### A3: Fuzzing Expansion And Continuous Fuzzing

- Steps: add Rust fuzz targets (path validation, environment filtering,
  Git-config parsing) using `cargo-fuzz`; add Go fuzz targets
  (workflow-YAML, provider-manifest, pinned-inputs parsing); seed corpora
  from real repo configs; add a scheduled CI fuzz lane with time budget;
  evaluate OSS-Fuzz onboarding once targets are stable.
- Exit gates: each named parser has a fuzz target and checked-in corpus; the
  scheduled lane runs green; crash triage workflow documented.
- Validation: seeded-crash dry run proves the lane reports failures.
- Size: M. Dependencies: none (D5 later refactors keep targets intact).

### A4: Unsafe-Code Safety Documentation

- Steps: add `SAFETY:` comments to every `unsafe` block in
  `runtime/container/rust/src/lib.rs` and binaries, stating the invariant
  that makes the block sound; add a lint/check that rejects new undocumented
  unsafe blocks; write the pre-audit checklist.
- Exit gates: zero undocumented unsafe blocks; check enforced in CI.
- Validation: Rust build and test lanes; negative-test the check.
- Size: S. Dependencies: none. Pairs with D5.

### B1: SLSA v1.0 Gap Analysis

- Steps: audit the release path against SLSA Build L1–L3; publish the gap
  matrix in [`provenance.md`](provenance.md) (hermeticity, builder hardening,
  two-person review), marking which gaps are structural to single-maintainer
  mode and what closes each.
- Exit gates: every SLSA L3 requirement has a status and, where unmet, a
  named path or explicit non-goal.
- Validation: docs lanes; release-owner review lens.
- Size: M. Dependencies: none. Feeds B2 and Phase 11 evidence.

### B7 (part 1): OpenSSF Best Practices Badge

- Steps: register the project, complete the passing-level questionnaire,
  remediate any unmet criteria, add the badge plus the existing Scorecard
  badge to README.
- Exit gates: passing badge live; criteria answers recorded.
- Validation: badge status page.
- Size: S. Dependencies: none.

### C5: Syscall-Shim Performance Baselines

- Steps: add microbenchmarks for hooked exec/spawn/posix_spawn paths versus
  unhooked baseline; record results in a reviewed benchmarks doc; wire an
  optional benchmark lane.
- Exit gates: baseline numbers published with methodology; rerun instructions
  documented.
- Validation: benchmark lane produces stable numbers across two runs.
- Size: S. Dependencies: none. Feeds C2 and E6 benchmarking claims.

### D8: Stability Contracts

- Steps: document which internal Go APIs and CLI flags/output lines are
  stable versus experimental ahead of 1.0; unify and document the exit-code
  contract across the Rust launcher, Go binaries, and shell entrypoints.
- Exit gates: contract doc reviewed; any exit-code mismatches found during
  the audit are fixed or explicitly recorded.
- Validation: deterministic exit-code tests for documented paths.
- Size: S. Dependencies: none. Direct input to G1.

### E1: Tiered Documentation Entry Points

- Steps: restructure README around three labeled paths (open-source
  operator, enterprise evaluator, contributor); extract the operator command
  reference and long tables into dedicated docs; keep README as orientation
  plus the 5-minute path.
- Exit gates: README materially shorter; no content lost (moved, not
  deleted); link checks pass; quickstart path unchanged or improved.
- Validation: docs lanes with E4 checks.
- Size: M. Dependencies: E4 (link checking) strongly preferred first.

### E2: Architecture Diagrams

- Steps: add maintained Mermaid diagrams to
  [`workcell-system-design.md`](workcell-system-design.md): boundary stack
  (host/VM/container/provider), policy-to-injection trust flow, control-plane
  seeding/masking; reference from the enterprise evidence baseline.
- Exit gates: diagrams render on GitHub; sources live in the repo; evidence
  baseline links them.
- Validation: docs lanes; visual review.
- Size: M. Dependencies: none.

### F3: Standards Watchlist

- Steps: write a short reviewed doc tracking the MCP spec line, OWASP
  agentic guidance, and agent-identity drafts, with owner and review cadence.
- Exit gates: doc exists with current entries and next-review date.
- Validation: docs lanes.
- Size: S. Dependencies: none.

### G1 (part 1): Public Contract Inventory

- Steps: enumerate the public operator surface — CLI flags, stable output
  lines (`support_matrix_*`, `provider_bootstrap_*`, session key=value
  summaries), exit codes, injection-policy schema keys, session-record and
  export formats — into a versioned contract document; classify each entry
  stable/experimental/deprecated; reconcile the manpage and CLI reference
  against the inventory.
- Exit gates: inventory complete and reviewed; every documented surface has
  a deterministic test or check referencing it; manpage gaps filed or fixed.
- Validation: contract-to-code drift check in CI.
- Size: M. Dependencies: D8. The freeze itself is G1 part 2 at v1.0-rc.

## Milestone v0.14: Platform, Speed, And Adoption

### C1: Apple `container` Backend Evaluation (macOS 26+)

- Steps: spike behind the existing target-kind contract as
  `local_vm/apple-container`; map lifecycle, mounts, networking, and
  diagnostics onto the shared conformance harness; measure boundary
  properties versus Colima (per-session VM, boot latency); record the
  evaluation and a go/no-go promotion decision; ship nothing support-visible
  until the full support-matrix gates pass.
- Exit gates: written evaluation with conformance results; explicit
  fail-closed behavior on macOS below 26; promotion decision recorded in the
  roadmap.
- Validation: deterministic conformance harness; live local exercise.
- Size: L. Dependencies: none technically; C2 benefits from it. The 1.0 gate
  is the recorded decision, not a shipped backend.

### C2: Session Start Latency Program

- Steps: measure and publish the current cold/warm start breakdown; add
  prebaked per-project image caching under the existing cache-profile
  labeling; evaluate a kept-warm VM lane as an explicit labeled mode;
  publish reproducible startup benchmarks; set and record the 1.0 latency
  target.
- Exit gates: measured improvement recorded; no new unlabeled assurance
  downgrade; benchmark methodology published; 1.0 target met or re-scoped
  with rationale.
- Validation: benchmark lane; scenario coverage for cache-profile behavior.
- Size: M. Dependencies: C5 methodology; C1 informs the ceiling.

### B8: CI Efficiency And Reliability Program

- Steps: move expensive reproducibility checks to a nightly lane on `main`
  with release preflight consuming the recorded result; add retry policy for
  transient artifact/network steps; add flaky-test tracking (label plus
  weekly report); add CI cost visibility reporting.
- Exit gates: PR wall-clock reduced without losing release-time assurance;
  flake report exists; cost report exists.
- Validation: before/after CI timing evidence.
- Size: M. Dependencies: none.

### B9: CI/CD Threat Model

- Steps: write `docs/ci-threat-model.md`: secrets handling and rotation,
  runner trust tiers (GitHub-hosted versus self-hosted), attestation
  verification assumptions, signing-compromise incident response.
- Exit gates: doc reviewed; SECURITY.md links it.
- Validation: docs lanes; security review lens.
- Size: M. Dependencies: none. Gates B6 in v0.15.

### D3 (start): Migrate Largest Orchestration Scripts To Go

- Steps: reimplement `verify-invariants.sh` (9,131 lines) and
  `container-smoke.sh` (4,570 lines) incrementally as Go commands under
  `cmd/`, one check-group per PR, keeping scenario parity via existing
  manifests; retire shell paths only when their Go replacements have equal or
  better coverage.
- Exit gates: per batch — identical pass/fail behavior on the scenario
  corpus; final (v1.0-rc) — shell originals removed or reduced to thin
  shims.
- Validation: side-by-side runs during migration; mutation coverage on the
  Go replacements.
- Size: L. Dependencies: D1; D2 reduces churn first; B3 baseline in place.

### D4: Modularize The Launcher

- Steps: split `scripts/workcell` (8,910 lines) into sourced modules (host
  detection, environment scrubbing, wrapper assembly, dispatch); write
  `docs/launcher-contract.md` (required tools, environment expectations,
  exit codes, test override flags).
- Exit gates: behavior-identical on the scenario suite; contract doc matches
  implementation.
- Validation: scenario suite before/after; bats coverage for extracted
  modules (D7).
- Size: M. Dependencies: D1, D2.

### E5: Injection-Policy Schema Documentation

- Steps: expand [`injection-policy.md`](injection-policy.md) with an
  annotated schema (each key: type, required/optional, provider
  applicability) and complete per-provider example policies including the
  multi-provider single-host pattern; add a check that documented keys match
  the parser's accepted set.
- Exit gates: schema doc complete; drift check green.
- Validation: docs lanes plus the drift check.
- Size: M. Dependencies: A2 adds keys — document them together; feeds G1.

### E6: Adoption Growth Kit

- Steps: publish a rendered docs site from the existing markdown; record
  asciinema demos for the 5-minute path and one provider quickstart; ship a
  Homebrew tap alongside the formula asset; write the "why a VM boundary"
  architecture post backed by C5/C2 benchmark numbers.
- Exit gates: site live; demos embedded; tap installable; post published
  with reproducible benchmark methodology.
- Validation: install-from-tap verification lane; docs lanes.
- Size: M. Dependencies: C5 (numbers), E1 (structure) first.

### E7: Contributor Runbook Depth

- Steps: give adapter READMEs real content (auth methods, managed
  control-plane files, adapter behavior summary); add worked contributor
  examples (add a credential type, extend an adapter) with the
  invariants/threat-model checklist each touches.
- Exit gates: no stub adapter README remains; examples verified by following
  them.
- Validation: docs lanes; dry-run of one worked example.
- Size: S. Dependencies: none.

### G2: Support Bundle Command

- Steps: design the `workcell support-bundle` collection set (install state,
  policy view, target diagnostics, provider bootstrap summaries, session
  metadata, recent audit pointers) with documented redaction rules; implement
  host-side with deterministic output shape; document the operator flow in
  SUPPORT.md.
- Exit gates: bundle contains the evidence needed to diagnose install,
  policy, target, provider, and runtime failures; redaction tests prove no
  credential material or workspace content leaks; docs updated.
- Validation: unit tests over collection and redaction; golden-file bundle
  shape; one scenario per failure class.
- Size: M. Dependencies: none; coordinates with F1 field naming.

### Provider Parity: Google Antigravity CLI Tier 1 Adapter

Runs as the already-committed provider-parity track under its existing exit
gates (adapter, pinned install/auth provenance, explicit Google auth staging,
session-local provider home/cache, unsafe-argument policy, quickstart,
deterministic tests, live provider certification). This plan adds only
sequencing: the track runs inside the 1.0 window starting v0.14, and its
outcome — support claim or explicitly recorded deferral — is a G4 input. No
shortcut to the Tier 1 evidence bar.

## Milestone v0.15: Enterprise Evidence And Release Assurance

### A5: Signed Session Audit Records

- Steps: design the hash-chain format for session audit logs; sign chain
  heads host-side with existing Sigstore tooling; add verification tooling
  (`workcell session verify` shape decided at design review); document the
  trust model (boundary-signed, not agent-signed).
- Exit gates: tamper on any record breaks verification in tests; export
  format versioned; docs updated.
- Validation: unit tests over chain/verify; scenario for tamper detection.
- Size: M. Dependencies: F1 format decisions coordinated.

### A6: Documented Syscall And Filesystem Hardening Profile

- Steps: extract the runtime container's effective seccomp posture,
  capability set, and rootfs expectations into reviewed policy artifacts;
  publish the outbound-endpoint inventory; add a deterministic conformance
  check comparing artifacts to the built image.
- Exit gates: conformance check green in CI and release preflight; docs
  reference the artifacts.
- Validation: negative-test the conformance check.
- Size: M. Dependencies: A1 design informs the endpoint inventory.

### B2: Dual-Control Release Approval

- Steps: add a second release approver identity; require two approvals on
  the release environment; update `docs/releasing.md` including the
  emergency bypass and its audit trail.
- Exit gates: hosted controls enforce two approvals; releasing doc updated;
  hosted-controls audit lane verifies the setting.
- Validation: hosted-controls workflow assertions.
- Size: M. Dependencies: a second trusted maintainer; B1 recommended first.

### B6: Real-Boundary Certification Lane In CI

- Steps: with B9 landed, evaluate self-hosted Apple Silicon runner options
  versus macOS CI services; treat the runner as lower-trust per the threat
  model (no repo secrets beyond scoped runner registration); implement a
  scheduled lane that launches the strict Colima path and runs the
  certification smoke; document runner lifecycle.
- Exit gates: scheduled lane exercises a real strict launch; failure alerts
  actionable; threat model covers the runner.
- Validation: the lane itself plus induced-failure drill.
- Size: L. Dependencies: B9.

### C3: Native Parallel Sessions

- Steps: design review (may start during v0.14) for worktree-aware workspace
  handling (one agent per worktree/branch/isolated runtime); extend session
  records with linkage across parallel sessions; define per-session resource
  and identity boundaries; implement launch/inventory/diff flows for N
  concurrent sessions per repo.
- Exit gates: two concurrent sessions on one repo cannot interfere
  (deterministic tests); session tooling renders parallel topology; docs
  updated; works on the strict path.
- Validation: scenario coverage including conflict cases; live exercise.
- Size: L. Dependencies: C2 (latency makes parallel viable); C1 decision
  helps (per-session VMs).

### D5: Modularize The Rust Interception Library

- Steps: split `lib.rs` (2,288 lines) into focused modules (syscall shim,
  git policy, runtime protection, path validation) preserving the exported
  ABI; keep A3 fuzz targets and A4 safety docs green through the refactor.
- Exit gates: no behavior change on runtime test surface; module boundaries
  documented; unsafe-block documentation preserved.
- Validation: full runtime test suite plus fuzz corpus replay before/after.
- Size: L. Dependencies: A3, A4 first (safety net before refactor).

### D7: Deepen Test Kinds

- Steps: add property-based tests for the session lifecycle state machine
  (attach/detach/timeout idempotency, signal races); add Go benchmarks for
  validation and session hot paths; add a bats (or equivalent) unit lane for
  shared shell helpers from D2.
- Exit gates: new lanes green in CI; at least one previously untested
  interleaving covered.
- Validation: the lanes themselves; mutation score movement (B3).
- Size: M. Dependencies: D2 for the shell portion; extends to C3's parallel
  session states.

### F1: OCSF-Mapped Audit Export

- Steps: map session-record fields to OCSF classes per the OWASP Agent
  Observability Standard; add an OCSF JSONL export mode to
  `workcell session export`; document redaction rules and privacy
  boundaries; version the mapping.
- Exit gates: exported events validate against the OCSF schema; redaction
  tests pass; Phase 17 docs reference the format.
- Validation: schema-validation tests; golden-file exports.
- Size: M. Dependencies: A5 coordinated (signed records should export
  cleanly); shares redaction rules with G2.

### G3: Install Lifecycle Proof

- Steps: define the day-two evidence set — install, upgrade-in-place across
  one minor version, uninstall, rollback, `--gc` — on the release matrix;
  add config/schema compatibility reads where upgrades cross versioned
  formats; automate what GitHub-hosted macOS runners can prove and record
  the local-operator remainder as certification evidence.
- Exit gates: each lifecycle operation has repeatable evidence; upgrade and
  rollback leave no orphaned Workcell-owned state; docs match behavior.
- Validation: CI install-matrix lanes extended with upgrade/rollback;
  scenario coverage for state cleanup.
- Size: M. Dependencies: G1 inventory (formats must be versioned to prove
  compatibility).

## Milestone v1.0-rc: Freeze And Gate

### G1 (part 2): Contract Freeze And Deprecation Policy

- Steps: publish the semantic-versioning and deprecation policy; mark the
  inventoried surface frozen; convert the contract-to-code drift check into
  a release gate; declare experimental surfaces explicitly.
- Exit gates: every stable surface is versioned, tested, and documented in
  the manpage/CLI reference; deprecation policy published; drift gate
  enforced in release preflight.
- Validation: contract drift gate; release preflight dry run.
- Size: M. Dependencies: G1 part 1, D8, E5.

### G4: 1.0 Readiness Gate Review

- Steps: run the cross-lens review (product, enterprise/security,
  adapter-maintainer, validation, docs/contract, release) against the 1.0
  release criteria in the roadmap; verify every host-support and provider
  matrix row against shipped behavior; record all scope decisions (for
  example an Antigravity certification deferral) explicitly; file and burn
  down any P0/P1 findings.
- Exit gates: no unresolved P0/P1 findings; matrices verified; scope
  decisions recorded; criteria checklist complete with evidence links.
- Validation: the recorded review itself.
- Size: S (review) over the rc period. Dependencies: everything above.

### D3 (complete): Orchestration Migration Finished

- Exit gates: `verify-invariants` and `container-smoke` fully served by Go
  implementations with equal or better coverage; shell originals removed or
  reduced to thin shims.

### D6: Split Oversized Go Validators

- Steps: split `pinnedinputs.go` (1,546 lines) into per-format packages
  (docker, node, rust, workflows, python) with focused tests; apply the same
  pattern to the largest dispatcher mains.
- Exit gates: behavior-identical validation results on the existing corpus;
  per-package tests.
- Validation: existing pin-hygiene lanes before/after; mutation coverage.
- Size: M. Dependencies: none; scheduled late to avoid churn against B4.

## Post-1.0

### B7 (part 2): Third-Party Boundary Audit

- Steps: scope an external audit of the runtime boundary (Rust shim, VM and
  container configuration, credential staging); pursue funding (OSTIF or
  sponsor); remediate and publish results. Scheduling the audit is a 1.0
  criterion; completing it is post-1.0 work.
- Exit gates: audit report published; findings triaged to closure or
  explicit acceptance.
- Size: L. Dependencies: A3, A4, D5 (audit-ready code).

### C4: Container Tooling Inside The Boundary

- Steps: investigate rootless or nested container execution inside the
  bounded runtime; document the assurance impact; if viable, ship as an
  explicit labeled lane, never as a silent capability of `strict`.
- Exit gates: written investigation with a go/no-go; any shipped lane has
  its own tier label, diagnostics, and scenario evidence.
- Validation: boundary tests proving the outer VM/container posture is
  unchanged when the lane is off.
- Size: L. Dependencies: C1 evaluation informs the mechanism.

### F2: Per-Session Identity Groundwork

- Steps: define a stable trust-domain session identifier (SPIFFE-style URI
  shape) and stamp it into session records and exports; keep it local-first
  (no hosted control plane); document how Phase 15 identity work consumes
  it.
- Exit gates: identifier present and stable across session lifecycle;
  export formats (F1) carry it; docs updated.
- Validation: unit tests over identifier stability; export golden files.
- Size: M. Dependencies: F1.

### Roadmap Phases 13–19

Linux `amd64` promotion (Phase 13), Linux `arm64`/Raspberry Pi (Phase 14),
identity and access (Phase 15), signed policy bundles (Phase 16), fleet
inventory and audit export beyond F1 (Phase 17), the regulated-team proof
harness and Windows investigation (Phase 18), and the managed-workstation
preview plus Azure (Phase 19) continue under their existing exit gates.
Phase 13 may land before 1.0 if its certification evidence arrives, but it
does not gate 1.0.

## Dependency Summary

- D1 → D2 → D3/D4; A3 + A4 → D5; D2 → D7 (shell portion)
- B9 → B6; B1 → B2; B3 baseline before D3/D5 refactors land
- C5 → C2 → C3; C1 decision informs C2/C3/C4
- E4 → E1 → E6; A2 → E5; D8 → G1 (inventory) → G1 (freeze); E5 → G1 (freeze)
- G1 (inventory) → G3; A5 ↔ F1 coordinated; F1 → F2; G2 ↔ F1 redaction rules
- A3/A4/D5 → B7 (part 2); everything → G4

## Verification Model

- every code-bearing item lands tests-first with unit, integration, and —
  where the touched packages already carry it — mutation coverage
- refactor items (D2–D6) require identical behavior on the existing scenario
  corpus before and after, per batch
- security items (A1, A2, A5, A6) require fail-closed negative tests and
  threat-model/invariants updates in the same change
- CI items (B3, B4, B6, B8) require a seeded-failure drill proving the gate
  actually trips
- operations items (G2, G3) require redaction/negative tests and repeatable
  lifecycle evidence, not one-off manual runs
- support-visible changes (C1 promotion, C3, C4 lanes, Antigravity support)
  additionally require the standard support-matrix, diagnostics, docs,
  rollback, and live certification package before any claim
- the 1.0 claim itself requires the G4 gate review with recorded evidence

## Status Tracking

Track progress by item identifier in PR titles and the changelog. When an
item completes, record the delivered evidence next to its roadmap entry;
when an item is dropped or reshaped, update both this plan and the roadmap
section in the same change. Milestone reassignment is a normal, recorded
event; silently skipping a 1.0 criterion is not.
