# Roadmap

Workcell is still pre-1.0. The roadmap must expand adoption without weakening
the core promise: coding agents run behind explicit runtime boundaries,
support tiers, diagnostics, and evidence. Delivered features belong in the
changelog and user docs. This file records direction and sequencing, not a
support claim.

Authoritative support status remains in
[`policy/host-support-matrix.tsv`](policy/host-support-matrix.tsv). Today the
reviewed operator-host launch path is Apple Silicon macOS. Linux `amd64`
exists as a trusted validation-host lane, not an operator launch host. Linux
operator hosts, Windows hosts, Linux `arm64`, and Raspberry Pi remain
unsupported until their support-matrix rows, diagnostics, docs, rollback
guidance, and live certification evidence land together.

Authoritative provider support status remains in
[`docs/provider-matrix.md`](docs/provider-matrix.md). Codex, Claude Code,
GitHub Copilot CLI, and Gemini are the supported Tier 1 provider adapters
today. Google Antigravity CLI is a queued follow-on track and remains planned
and fail-closed until its Workcell adapter, auth path, docs, deterministic
evidence, and live certification land together.

The active delivery shape lives in
[`docs/implement-first-delivery-plan.md`](docs/implement-first-delivery-plan.md).
The longer-lived runtime-target and deployment-reach program lives in
[`docs/runtime-target-expansion-plan.md`](docs/runtime-target-expansion-plan.md).
The deterministic phase breakdown lives in
[`docs/runtime-target-phase-plan.md`](docs/runtime-target-phase-plan.md).
Cross-cutting engineering, security-depth, and adoption improvements from the
2026-07 repository review live in
[Engineering And Ecosystem Improvement Tracks](#engineering-and-ecosystem-improvement-tracks)
below. The release program that sequences those tracks, the remaining feature
work, and the 1.0 criteria lives in [Path To 1.0](#path-to-10) below.

## Product Direction

- Position Workcell as an auditable runtime boundary for coding agents, not as
  another agent framework.
- Preserve the current strict local VM plus hardened container path as the
  highest-assurance local mode.
- Expand reach through explicit target kinds and assurance classes:
  `local_vm`, `local_compat`, `remote_vm`, and `managed_workstation`.
- Treat `compat`, `preview`, `certification candidate`, `experimental`, and
  `unsupported` as materially different states.
- Make enterprise adoption boringly operational: evidence, identity, policy,
  audit, install, rollback, support bundles, and certification before broad
  launch claims.
- Keep open-source adoption grounded in quickstart reliability, public
  invariants, contributor ergonomics, and honest support labels.
- Promote provider expansion through the same Tier 1 adapter bar as the current
  Codex, Claude, Copilot, and Gemini adapters, rather than through
  provider-specific shortcuts.

## Current Support Boundary

- `macos/arm64/local_vm/colima/strict` is the reviewed strict operator-host
  path.
- `macos/arm64/local_compat/docker-desktop/compat` is supported as lower
  assurance than strict Colima.
- `remote_vm/aws-ec2-ssm/compat` and `remote_vm/gcp-vm/compat` remain
  preview-only broker-plan paths with live smokes isolated to certification
  lanes.
- `linux/amd64` is useful as a trusted validation host, but operator launch is
  blocked until promoted through the host-expansion gates below.
- `linux/arm64`, Raspberry Pi, and Windows are not support claims. They are
  planning targets with explicit readiness and certification gates.
- Phases 10 through 12 are now implemented as contract, evidence, and readiness
  gates. They do not add a managed-workstation backend, Linux support, Windows
  support, or any new launch target.
- GitHub Copilot CLI is a supported Tier 1 provider adapter through explicit
  `copilot_github_token` staging, a temporary host-mounted token handoff
  outside provider state with the staged token file removed from direct runtime
  mounts, `COPILOT_GITHUB_TOKEN` export only to the managed child, isolated
  `COPILOT_HOME` and `COPILOT_CACHE_HOME`, and no host `gh` auth, keychain, or
  host `~/.copilot` passthrough. Google
  Antigravity CLI is queued as the
  follow-on track; current releases do not support `--agent antigravity`, its
  credential keys, or a matching quickstart.
- Upstream retires Gemini CLI for the free, Pro, and Ultra personal-account
  login tiers on June 18, 2026 in favor of the closed-source Antigravity
  CLI; Gemini Code Assist Standard/Enterprise licenses and paid Gemini API
  keys keep access. Reviewed posture: the Gemini Tier 1 adapter stays
  supported for the auth inputs Google keeps serving (licensed Code Assist
  or a paid Gemini API key, with `gcloud_adc` as the supplemental Vertex
  input to those modes rather than a standalone path), and an Antigravity
  adapter is a committed follow-on provider-parity track behind the same
  Tier 1 evidence bar (see [docs/provider-matrix.md](docs/provider-matrix.md)).

## Path To 1.0

Workcell 1.0 is a contract-stability and assurance claim, not a
platform-reach claim. 1.0 means: the public operator contract is frozen under
semantic versioning, the reviewed macOS boundary is certified and audit-ready,
the supported provider set covers the current agent ecosystem, day-two
operations are proven, and the release path meets the supply-chain bar
enterprise buyers require. Platform expansion — Linux operator hosts, Windows,
managed workstations — continues after 1.0 through the phase gates below and
does not gate 1.0.

The competitive context shaping this bar: microVM-per-session runtimes with
warm starts and per-session egress allowlists are now the mainstream
comparison point; OS-level sandbox competitors have shipped bypass CVEs;
repo-defined MCP servers are a proven one-keypress RCE class across all four
currently supported provider CLIs; and enterprises evaluate agent runtimes
against OWASP agentic guidance, SIEM-ready audit export, and SLSA supply-chain
levels. Workcell's 1.0 bar leans into its differentiators — the strongest
local boundary, staged credentials, host-side signing, signed evidence, and
honest support labels — while closing the speed, parallelism, and egress-policy
gaps competitors treat as table stakes.

### 1.0 Release Criteria

1.0 ships when all of the following hold. Item identifiers refer to the
improvement tracks below; `G` items are the 1.0 contract-and-operations track.

1. Contract stability: CLI flags and stable output lines, exit codes, the
   injection-policy schema, and session-record and export formats are
   versioned, frozen, and covered by a published deprecation policy (G1); the
   manpage and CLI reference are complete for the frozen surface.
2. Boundary assurance: per-session egress policy (A1), repo-defined MCP and
   agent-config containment (A2), hardening-profile conformance (A6),
   expanded fuzzing (A3), documented unsafe-code invariants (A4), and signed
   session audit records (A5) are landed; a third-party boundary audit is
   scheduled or complete (B7).
3. Platform: `macos/arm64` strict (Colima) and compat (Docker Desktop) are
   certified on the release matrix; the Apple `container` backend decision
   (C1) is recorded either way; session start latency targets (C2) are met
   and published; native parallel sessions (C3) work on the strict path.
4. Providers: the Tier 1 set covers the current agent ecosystem — Codex,
   Claude Code, GitHub Copilot CLI, Gemini, and Google Antigravity CLI —
   each through the same Tier 1 evidence bar. If upstream instability blocks
   Antigravity live certification, 1.0 may ship with the fail-closed scaffold
   plus an explicit recorded scope decision instead of a support claim.
5. Operations: install, upgrade, uninstall, rollback, `--gc`, and support
   bundles (G2) are proven end to end on the release matrix (G3).
6. Release assurance: dual-control releases (B2), mutation-score gating
   (B3), SLSA L3 gaps closed or explicitly dispositioned (B1), OpenSSF Best
   Practices badge (B7), and a real-boundary certification lane (B6) are in
   place.
7. Adoption surface: tiered docs (E1), architecture diagrams (E2), a
   rendered docs site with demos and reproducible benchmarks (E6), and the
   support-tier and diagnostics guides (E3) are live.
8. Readiness review: a cross-lens 1.0 gate review (G4) — product,
   enterprise/security, adapter-maintainer, validation, docs/contract, and
   release lenses — records no unresolved P0/P1 findings, and every support
   matrix row matches shipped behavior.

### Milestone Train

Milestones are sequencing buckets on the existing release cadence; versions
are indicative and may split. Per-item steps, exit gates, and dependencies
live in
[`docs/improvement-tracks-implementation-plan.md`](docs/improvement-tracks-implementation-plan.md).

| Milestone | Theme | Contents |
|---|---|---|
| v0.12 | Containment and hygiene | A2, A7, B3, B4, B5, D1, D2, E3, E4 |
| v0.13 | Boundary depth and stability | A1, A3, A4, B1, B7 (badge), C5, D8, E1, E2, F3, G1 (inventory) |
| v0.14 | Platform, speed, and adoption | C1, C2, B8, B9, D3 (start), D4, E5, E6, E7, G2, Antigravity Tier 1 adapter track |
| v0.15 | Enterprise evidence and release assurance | A5, A6, B2, B6, C3, D5, D7, F1, G3 |
| v1.0-rc | Freeze and gate | G1 (freeze), G4, D3 (complete), D6 |
| post-1.0 | Reach expansion | Phases 13–19 remainder, C4, B7 (audit completion), F2 |

Phase 13 (Linux `amd64` `local_compat` certification candidate) may land
before or after 1.0 depending on certification evidence; it does not gate
1.0 and 1.0 creates no Linux claim.

## Next Provider And Target Phases

### Phase 10: Managed Workstation Contract

Define `managed_workstation` as a first-class target kind before any
provider-specific backend ships.

Status: implemented as a contract and discovery gate in
[`docs/managed-workstation-contract.md`](docs/managed-workstation-contract.md).
The first provider discovery lane is `gcp-cloud-workstations`; no provider
backend or support claim is shipped in this phase.

Exit gates:

- lifecycle, workspace materialization, identity, policy, audit, and recovery
  semantics are documented
- managed workstations are compared against `local_vm`, `local_compat`, and
  `remote_vm`
- deterministic fake-target or conformance expectations are recorded
- the first managed-workstation provider candidate is named
- `azure-vm` remains explicitly queued as the next raw `remote_vm` lane

### Phase 11: Enterprise Evidence Baseline

Produce the buyer-facing evidence packet needed for enterprise and regulated
adoption.

Status: implemented as an evidence map in
[`docs/enterprise-evidence-baseline.md`](docs/enterprise-evidence-baseline.md).
The map is an evaluation aid, not a SOC 2, ISO 27001, or similar certification
claim.

Exit gates:

- architecture and data-flow diagrams exist
- threat model, known gaps, support boundaries, and non-protections are public
- SBOM, provenance, reproducibility, release signing, and vulnerability
  handling are summarized
- audit-log schema and retention expectations are documented
- SOC 2 and ISO 27001 control mappings are drafted as evaluation aids, not
  compliance claims

### Phase 12: Host-Expansion Readiness Gate

Define how Linux and Windows can be promoted without creating premature
support claims.

Status: implemented as a readiness gate in
[`docs/host-expansion-readiness.md`](docs/host-expansion-readiness.md). The
current support matrix remains unchanged; Linux and Windows operator hosts stay
blocked until later promotion changes land with evidence.

Exit gates:

- Linux and Windows support tiers are defined separately for `strict`,
  `compat`, `preview`, `certification candidate`, `experimental`, and
  `unsupported`
- packaging, install, uninstall, upgrade, and rollback options are scoped for
  Linux `amd64`, Linux `arm64`, Raspberry Pi, Windows WSL2, and native Windows
- live certification host requirements are recorded
- CI-proven, locally mirrored, and certification-only evidence are separated
- support-matrix promotion criteria and fail-closed diagnostics are specified
- the gate creates no Linux or Windows operator-host support claim by itself

### Provider Parity Phase: GitHub Copilot CLI Tier 1 Adapter Parity

Status: documentation and contract surfaces now describe GitHub Copilot CLI as
a Tier 1 provider adapter. Live provider-authenticated certification of a
non-destructive `copilot -p` launch with staged credentials remains required
before signing or publishing changes that promote or materially alter the
Copilot support claim.

Exit gates:

- `workcell --agent copilot --workspace /path/to/repo` launches Copilot CLI
  fully inside the bounded Workcell runtime
- Copilot home, cache, settings, permissions, sessions, logs, plugins, hooks,
  MCP/LSP state, and instruction surfaces are session-local, explicitly
  staged, or blocked by the managed wrapper; host `~/.copilot`, host keychains,
  ambient `gh` auth, and whole-home passthrough remain outside the safe path
- the primary auth path is an explicit staged credential such as
  `copilot_github_token`, converted host-side into a temporary token handoff
  mount outside mounted provider state and exported to the managed Copilot
  child process as `COPILOT_GITHUB_TOKEN`
- Copilot auth fallback is fail-closed: `GH_TOKEN`, `GITHUB_TOKEN`,
  keychain/plaintext config fallback, and `gh auth token` fallback are scrubbed
  or rejected unless a separate reviewed lower-assurance path explicitly
  enables them
- `COPILOT_HOME` and `COPILOT_CACHE_HOME` are set to Workcell-owned paths, and
  BYOK provider env, remote control, plugins, MCP expansion, ACP, and custom
  instruction overrides are either blocked or explicitly reviewed before
  support
- Copilot telemetry, OpenTelemetry, and content-capture environment variables
  are scrubbed by default in `strict`; any enablement is lower-assurance,
  explicitly acknowledged, audited, and covered by deterministic tests
- `--agent-autonomy prompt` and `--agent-autonomy yolo` map to reviewed
  Copilot permission flags without letting user argv silently widen tool, path,
  URL, remote-session, or update behavior
- workspace control-plane masking accounts for Copilot-specific files such as
  `AGENTS.md`, `.github/copilot-instructions.md`,
  `.github/instructions/**`, and `.github/copilot/settings*.json`
- README, provider matrix, bootstrap matrix, injection policy, adapter control
  planes, quickstart, validation scenarios, requirements, operator contract,
  and release-facing docs land with the implementation
- release preflight, upstream provider-pin verification, provenance docs,
  manifests, and release validation include Copilot before any support
  promotion
- deterministic repo-required tests cover provider selection, auth policy,
  bootstrap summaries, control-plane seeding, unsafe-argument rejection, and
  scenario manifest parity, including fail-closed auth fallback and
  telemetry/content-capture behavior
- live provider certification proves a non-destructive `copilot -p` launch with
  staged credentials before any signed commit claims Copilot support
- product, enterprise/security, adapter-maintainer, validation, docs/contract,
  and release review lenses have no unresolved P0/P1 objections about support
  labels, auth, masking, or certification

### Phase 13: Linux `amd64` `local_compat` Certification Candidate

Move Linux earlier than the old late-roadmap position, but only as a narrow
candidate for lower-assurance operator-host support after the Copilot provider
parity phase records its adapter-support outcome.

Exit gates:

- one distro/runtime matrix is selected before broad Linux support is implied
- launch diagnostics fail closed outside the selected matrix
- live certification passes on real Linux operator hosts, not only hosted CI
- install, uninstall, upgrade, rollback, docs, and support bundles work on the
  selected host path
- host-support matrix rows, validation lanes, and operator docs land in the
  same change as any promotion
- no `strict` Linux claim exists until a dedicated VM plus container boundary
  with equivalent guarantees is proven

### Phase 14: Linux `arm64` And Raspberry Pi Readiness

Evaluate Linux `arm64` after Linux `amd64`, with Raspberry Pi treated as a
smaller experimental subset rather than an enterprise default.

Exit gates:

- Linux `arm64` package/runtime prerequisites are identified separately from
  Linux `amd64`
- Raspberry Pi profiles account for memory, disk I/O, SD-card reliability,
  cgroups, kernel variants, and coding-agent workload limits
- live certification hardware is named
- support labels distinguish Linux `arm64` certification candidate from
  Raspberry Pi experimental
- no Raspberry Pi support claim exists until docs, diagnostics, certification,
  rollback, and support-matrix rows land together

### Phase 15: Identity And Access Model

Define enterprise identity and attribution before central administration.

Exit gates:

- SSO/OIDC/SAML, SCIM/group mapping, machine identity, per-user launch
  identity, service-account boundaries, and breakglass identity are modeled
- local-first attribution works without requiring a hosted Workcell control
  plane
- audit events connect sessions, target, policy, identity, and assurance class
- identity docs do not treat provider config, prompt files, or workspace rules
  as the security boundary

### Phase 16: Signed Policy Bundle Distribution

Give teams a safe way to distribute reviewed Workcell policy without turning
workspace content into policy authority.

Exit gates:

- signed and versioned org policy bundle format exists
- policy precedence, expiry, rollback, drift detection, and local override
  rules are documented
- required operator acknowledgements are auditable
- bundles can be distributed through MDM, Git, or future admin tooling without
  implying centralized remote execution

### Phase 17: Fleet Inventory And Audit Export

Make host-local Workcell records consumable by enterprise inventory and SIEM
systems.

Exit gates:

- machine-readable session, target, policy, runtime, provider, assurance,
  downgrade, and support-status metadata can be exported
- redaction rules and privacy boundaries are documented
- JSONL/SIEM-friendly export formats are stable enough for pilot use
- support bundles include the evidence needed to diagnose install, policy,
  target, provider, and runtime failures

### Phase 18: Regulated-Team Proof Harness And Windows Investigation

Expand certification into a repeatable proof harness while investigating
Windows without claiming support.

Exit gates:

- deterministic negative tests cover forbidden host mounts, host sockets,
  credential stores, SSH/GPG agents, Docker sockets, and workspace-controlled
  policy takeover
- certification lanes cover local macOS strict, Linux `amd64` candidates,
  remote VM previews, and managed-workstation candidates where applicable
- Windows WSL2 and native Windows investigation records filesystem semantics,
  path translation, credential isolation, Docker Desktop/WSL integration,
  PowerShell packaging, endpoint controls, and live-certification blockers
- Windows remains unsupported until a separate support-matrix promotion lands
  with evidence

### Phase 19: Managed Workstation Provider Preview And Azure Return

Ship the first managed-workstation provider preview only after the contract and
evidence model exist, then return to Azure as the next raw `remote_vm` lane.

Exit gates:

- first provider-specific managed-workstation preview is labeled `compat` or
  lower unless stronger evidence proves otherwise
- deterministic conformance, docs, diagnostics, rollback, and live smoke land
  with the preview
- support load and pilot feedback are measured before beta promotion
- `remote_vm/azure-vm/compat` reuses the existing remote-VM contract and avoids
  inbound public SSH on the reviewed path
- AWS, GCP, and Azure expectations are compared across identity, private
  networking, tags, quotas, costs, logs, key management, and data residency

## Engineering And Ecosystem Improvement Tracks

Recorded 2026-07-03 from a full review of the repository (documentation,
Rust/Go/shell source, tests, CI, and release workflows) combined with external
research on the 2025–2026 agent-sandboxing ecosystem, competing runtimes,
disclosed agent-CLI vulnerabilities, and emerging enterprise standards. These
tracks are direction and sequencing, not support claims. Every item lands
under the same evidence bar as the phases above: docs, deterministic tests,
diagnostics, and (where applicable) live certification travel with the change.

Horizons: `now` (next one to three releases), `next` (after the current
provider and target phases stabilize), `later` (post-1.0 or gated on earlier
items). Sizes: S/M/L. Milestone assignment for every item lives in the
[Milestone Train](#milestone-train) under Path To 1.0, and the per-item
implementation plan — steps, exit gates, dependencies, and validation
expectations — lives in
[`docs/improvement-tracks-implementation-plan.md`](docs/improvement-tracks-implementation-plan.md).

### Track A: Boundary Depth And Agent-Threat Defenses

The external threat picture moved fast in 2025–2026: one-keypress RCE via
repo-defined MCP servers across all four supported provider CLIs (TrustFall),
prompt injection through PR titles and comments (CVE-2025-66032), recurring
npm worm campaigns (Shai-Hulud and successors), and sandbox-bypass CVEs in
OS-level sandboxes (Seatbelt/bubblewrap). Workcell's VM-plus-container
boundary and staged-credential model match the containment doctrine the
strongest vendors now publish; these items deepen that lead.

- **A1 (now, L): Per-session network egress policy.** Add a default-deny
  egress lane with explicit domain allowlists as first-class per-session
  policy, enforced outside the agent process (proxy or VM-level filtering),
  with the allowlist recorded in the session record. Egress allowlisting is
  the control every adjacent runtime now treats as core, and NSA MCP guidance
  recommends filtering outgoing proxies for external MCP connections. Today
  the strict lane has a reviewed network posture but no operator-visible
  per-session allowlist artifact.
- **A2 (now, M): Repo-defined MCP and agent-config containment.** Extend
  workspace control-plane masking into an explicit reviewed policy for
  repo-local MCP server definitions, tool configs, and instruction files:
  deny by default in `strict`, require explicit per-path acknowledgement to
  enable, and record the decision in the session record. This addresses the
  TrustFall class (agent CLIs auto-executing project-defined MCP servers) and
  MCP supply-chain vectors (postmark-mcp).
- **A3 (now, M): Fuzzing expansion and continuous fuzzing.** Only two Go fuzz
  targets exist (`internal/injection`, `internal/tomlsubset`) and the Rust
  syscall-interception library has none. Add fuzz targets for the Rust shim's
  path validation, environment filtering, and Git-config parsing; add Go fuzz
  targets for workflow-YAML, provider-manifest, and pinned-inputs parsing;
  then wire the corpus into continuous fuzzing (OSS-Fuzz or a scheduled CI
  lane).
- **A4 (now, S): Unsafe-code safety documentation.** The Rust runtime library
  (`runtime/container/rust/src/lib.rs`) carries roughly 60 `unsafe` blocks
  with minimal inline safety justification. Adopt the standard-library
  `SAFETY:` comment convention on every unsafe block and add a pre-audit
  checklist so a future third-party audit starts from documented invariants.
- **A5 (next, M): Signed, tamper-evident session audit records.** Workcell
  already keeps publication and signing on the host; extend the same trust
  model to session evidence: hash-chain session audit logs and sign the chain
  head with the existing Sigstore tooling so audit records are verifiable
  from outside the agent. This is emerging in the ecosystem as
  boundary-signed "action receipts" and directly strengthens the Phase 17
  export story.
- **A6 (next, M): Documented syscall and filesystem hardening profile.**
  Publish the runtime container's expected seccomp posture, capability set,
  read-only-rootfs expectations, and outbound-endpoint inventory as reviewed
  policy artifacts with a deterministic conformance check, rather than
  leaving them implicit in image build files.
- **A7 (now, S): OWASP Agentic Top 10 control mapping.** Publish a docs page
  mapping Workcell controls to the OWASP Top 10 for Agentic Applications 2026
  (ASI01–ASI10): staged credentials → ASI03, VM boundary → ASI05,
  control-plane masking → ASI04, session records → observability. This
  converts the security posture into the checklist vocabulary buyers and
  reviewers now use, and feeds the Phase 11 evidence packet.

### Track B: Supply Chain And Release Assurance

CI is already strong: all 72 action references SHA-pinned, workflows start
from `permissions: {}`, reproducible builds verified on amd64 and arm64,
keyless Sigstore signing, SBOMs, and GitHub attestations. These items close
the remaining gaps between that posture and the level enterprises will ask a
security-boundary product to prove.

- **B1 (now, M): SLSA v1.0 gap analysis.** Audit the release path against
  SLSA Build L3 and publish the gap matrix (hermeticity status, builder
  hardening, two-person review) in `docs/provenance.md`, including which gaps
  are structural to the single-maintainer model and what closes them.
- **B2 (next, M): Dual-control release approval.** `docs/releasing.md`
  honestly discloses single-maintainer mode as lower assurance. Before 1.0,
  add a second release approver, require two approvals on the release
  environment, and document the emergency bypass. This is the single largest
  step toward SLSA L3 and OpenSSF expectations.
- **B3 (now, M): Mutation testing gated in CI.** `scripts/run-mutation-tests.sh`
  exists but no workflow invokes it, so mutation coverage can silently
  regress. Add a scheduled or `approved-heavy-ci` mutation lane, publish the
  score, and block release when coverage degrades below the recorded
  baseline.
- **B4 (now, S): Centralized tool pins and action allowlist.** Tool pins
  (actionlint, zizmor, syft, cosign, buildx) are correct but scattered across
  workflow env blocks. Centralize them in a reviewed policy file with
  integrity hashes, add a permitted-actions allowlist check, and validate
  both in pre-commit and release preflight.
- **B5 (now, S): Audit-trail retention policy.** Artifact retention is 5–7
  days across workflows, which is thin for incident response. Document a
  retention policy, extend release-evidence artifacts to 30–90 days, and
  document how to query GitHub attestations and the Rekor log after artifact
  expiry.
- **B6 (next, L): Real-boundary certification lane in CI.** The macOS Colima
  boundary is still a local operator exercise because GitHub-hosted runners
  cannot prove it. Evaluate a self-hosted Apple Silicon runner (or a macOS CI
  service) as a scheduled certification lane that actually launches the
  strict Colima path, treating the runner itself as lower-trust
  infrastructure per a documented CI threat model.
- **B7 (now, S → later, L): OpenSSF badge, then third-party audit.** Register
  for the OpenSSF Best Practices badge and add it plus the Scorecard badge to
  the README (`scorecard.yml` already runs weekly). Post-1.0, pursue a funded
  third-party audit of the boundary; for a security-boundary product, "who
  audited the boundary" is the credibility question none of the local-tier
  competitors can answer either.
- **B8 (next, M): CI efficiency and reliability program.** Split expensive
  reproducibility checks into a nightly lane on `main` with release preflight
  consuming the result, add retry policy for transient artifact/network
  failures, track flaky tests explicitly, and add CI cost visibility so
  coverage-versus-cost tradeoffs are deliberate.
- **B9 (next, M): CI/CD threat model.** Publish `docs/ci-threat-model.md`
  covering secrets handling and rotation, runner trust tiers, attestation
  verification assumptions, and the signing-compromise incident-response
  plan. Complements Phase 11 and gates B6.

### Track C: Runtime Platform Evolution

- **C1 (next, L): Apple `container` backend evaluation (macOS 26+).** Apple's
  Containerization framework reached 1.0.0 in June 2026: one lightweight VM
  per container on Virtualization.framework with sub-second boot and a frozen
  1.0 API, Apple Silicon only, macOS 26 baseline. Evaluate it as an
  additional target (`local_vm/apple-container`) with per-session VM
  isolation — a stronger and lighter boundary than one shared Colima VM.
  Colima remains the reviewed default for macOS below 26; promotion follows
  the same support-matrix gates as every other target.
- **C2 (next, M): Session start latency program.** Startup latency is the
  most-benchmarked axis in the 2026 sandbox ecosystem (hosted competitors
  advertise sub-second starts; local competitors ship cached project
  images). Add prebaked per-project image caching and an optional kept-warm
  VM lane under existing cache-profile labeling, and publish reproducible
  startup benchmarks so the cost of the stronger boundary is measured, not
  assumed.
- **C3 (later, L): Native parallel sessions.** The 2026 unit of agent work is
  one agent per git worktree, branch, and isolated environment; orchestrator
  UIs currently supply their own weak isolation. Support N concurrent
  sessions per repo natively (worktree-aware workspace handling, per-session
  runtime, session-record linkage) so orchestrators can sit on top of
  Workcell as the strong substrate instead of competing with it.
- **C4 (later, L): Container tooling inside the boundary.** Agents routinely
  need to build and run containers for tests; one major competitor markets
  container-in-sandbox as its headline differentiator. Investigate a
  rootless or nested container lane inside the bounded runtime, labeled at
  the appropriate assurance tier, without weakening the outer boundary.
- **C5 (now, S): Syscall-shim performance baselines.** The LD_PRELOAD
  interception library has no published overhead numbers. Add
  microbenchmarks for the hooked exec/spawn paths and record baselines so
  future changes and competitive comparisons are empirical.

### Track D: Code Health And Consolidation

The Go tree is in good shape (28.8k source lines, 82% test-to-code ratio,
three direct dependencies). The concentration risks are a 2,288-line
monolithic Rust interception library, a 8,910-line launcher script, a
9,131-line `verify-invariants.sh`, and widespread helper duplication across
120 shell scripts (for example, 25 separate `cleanup()` definitions).

- **D1 (now, S): Language-boundary doctrine.** Document the intended
  separation: Rust only for syscall interception and exec guards, Go for all
  policy, state, and orchestration logic, shell only as thin glue. New logic
  defaults to Go; large shell growth requires justification. This makes the
  remaining items directional rather than ad hoc.
- **D2 (now, M): Shared shell library.** Extract the duplicated `cleanup`,
  `require_tool`, `die`, JSON, and resolver helpers into `scripts/lib/`
  shared sources, and add a shellcheck lane (including SC2154) over all 120
  scripts.
- **D3 (next, L): Migrate the largest orchestration scripts to Go.**
  `verify-invariants.sh` (9,131 lines, 57 functions) and
  `container-smoke.sh` (4,570 lines) are de facto integration-test
  orchestrators written in bash. Reimplement them incrementally as Go
  commands under the existing `cmd/` pattern for structured errors,
  parallelism, and unit-testability, keeping scenario parity via the
  existing manifests.
- **D4 (next, M): Modularize the launcher.** Split `scripts/workcell`
  (8,910 lines, 354 functions) into sourced modules (host detection,
  environment scrubbing, wrapper assembly, dispatch) and document the
  launcher contract (required tools, environment expectations, exit codes,
  test override flags).
- **D5 (next, L): Modularize the Rust interception library.** Split
  `lib.rs` into focused modules (syscall shim, git policy, runtime
  protection, path validation) so security policy is reviewable separately
  from FFI mechanics; pairs with A3 fuzzing and A4 safety docs.
- **D6 (later, M): Split oversized Go validators.** `pinnedinputs.go`
  (1,546 lines) validates many unrelated formats; split per-format
  (docker, node, rust, workflows, python) with focused tests. Same pattern
  for the largest dispatcher mains.
- **D7 (next, M): Deepen test kinds.** Add property-based tests for the
  session lifecycle state machine (attach/detach/timeout idempotency, signal
  races), Go benchmarks for validation and session hot paths, and a bats (or
  equivalent) unit lane for shared shell helpers. Complements the mutation
  lane in B3.
- **D8 (now, S): Stability contracts.** Document which internal Go APIs and
  which CLI flags/output lines are stable versus experimental ahead of 1.0,
  and unify the exit-code contract across the Rust launcher, Go binaries,
  and shell entrypoints.

### Track E: Documentation And Adoption

- **E1 (now, M): Tiered documentation entry points.** The README is 500+
  lines mixing a 5-minute path with deep reference material. Restructure
  around three labeled paths (open-source operator, enterprise evaluator,
  contributor), move the operator command reference and long tables into
  dedicated docs, and keep the README as orientation plus the 5-minute path.
- **E2 (now, M): Architecture diagrams.** `docs/workcell-system-design.md`
  is text-only. Add maintained Mermaid diagrams for the host/VM/container/
  provider boundary stack, the policy-to-injection trust flow, and
  control-plane seeding/masking. The Phase 11 evidence packet names these as
  expected artifacts.
- **E3 (now, S): Support-tier legend and diagnostics guide.** Publish one
  page defining `strict`, `compat`, `preview`, `certification candidate`,
  `experimental`, and `unsupported` with current examples, and one page
  explaining `--doctor`/`--inspect` output fields (`support_matrix_*`) with a
  triage decision tree.
- **E4 (now, S): Docs CI depth.** Add intra-repo link checking and orphan
  detection to `docs.yml`, add status labels (active/planning/historical) to
  planning docs, and add last-verified release markers to time-sensitive
  docs (threat model, invariants, provider matrix).
- **E5 (next, M): Injection-policy schema documentation.** Expand
  `docs/injection-policy.md` with an annotated schema (each key: type,
  required/optional, provider applicability) and complete per-provider
  example policies, including the multi-provider single-host pattern.
- **E6 (next, M): Adoption growth kit.** Publish a rendered docs site, add
  asciinema terminal demos to the README and quickstarts, ship a Homebrew
  tap alongside the formula asset, and write the "why a VM boundary"
  architecture post with reproducible benchmarks (C2/C5) so Workcell appears
  in the 2026 sandbox comparisons on its own evidence. Every winning
  comparable pairs an isolation-model manifesto with embeddable demos.
- **E7 (next, S): Contributor runbook depth.** Give adapter READMEs real
  content (auth methods, managed control-plane files, what the adapter
  does), and add worked contributor examples (add a credential type, extend
  an adapter) with the invariants/threat-model checklist each touches.

### Track F: Enterprise And Standards Alignment

- **F1 (next, M): OCSF-mapped audit export.** Emit session records in an
  OCSF-mapped JSONL form for SIEM interop; the OWASP Agent Observability
  Standard already defines agent-trace-to-OCSF mappings. This is the concrete
  format decision inside Phase 17 rather than a new phase.
- **F2 (later, M): Per-session identity groundwork.** IETF and NIST drafts
  converge on SPIFFE-style workload identity for agent authorization and
  audit. Stamp session records with a stable trust-domain session identifier
  now so Phase 15 identity work and the managed-workstation track inherit
  compatible records.
- **F3 (now, S): Standards watchlist.** Track the MCP 2026 spec line (OAuth
  alignment, registry, Server Cards), OWASP agentic guidance, and agent
  identity drafts in a short reviewed doc so adapter and policy decisions
  cite current standards instead of rediscovering them per PR.

### Track G: 1.0 Contract And Operations

These items exist specifically to make 1.0 a truthful claim: a frozen public
contract and proven day-two operations.

- **G1 (next, M): Public contract freeze and deprecation policy.** Inventory
  the public operator surface (CLI flags, stable output lines, exit codes,
  injection-policy schema, session-record and export formats), version each
  schema, publish the semantic-versioning and deprecation policy, and bring
  the manpage and CLI reference to completeness for the frozen surface. The
  inventory lands early; the freeze is the 1.0-rc act.
- **G2 (next, M): Support bundle command.** Ship a `workcell support-bundle`
  flow that collects the evidence needed to diagnose install, policy, target,
  provider, and runtime failures with documented redaction rules — the
  operator-facing half of the Phase 17 export story.
- **G3 (next, M): Install lifecycle proof.** Prove install, upgrade,
  uninstall, rollback, and `--gc` end to end on the release matrix as
  repeatable evidence, including upgrade-in-place across one minor version
  and config/schema compatibility reads.
- **G4 (later, S): 1.0 readiness gate review.** A recorded cross-lens review
  (product, enterprise/security, adapter-maintainer, validation,
  docs/contract, release) against the 1.0 release criteria, with every
  support-matrix row verified against shipped behavior and all scope
  decisions (for example an Antigravity certification deferral) recorded
  explicitly.

### Sequencing Summary

Sequencing for all tracks now lives in the
[Milestone Train](#milestone-train) under Path To 1.0. The v0.12 milestone
(A2, A7, B3, B4, B5, D1, D2, E3, E4) is the suggested first slice: high
impact, small-to-medium effort, no new support claims, and each item is
independently shippable.

## Adoption Workstreams

### Open Source

- publish a short supported quickstart for the strict macOS path
- publish a clearly labeled Docker Desktop compat quickstart
- keep the Copilot CLI quickstart tied to the explicit staged-token auth model
  and keep live certification tied to the staged-token provider-e2e gate
- explain why Docker, devcontainers, prompt rules, and provider config are not
  equivalent to Workcell's runtime boundary
- keep examples and provider adapters thin, versioned, and honest about
  support status
- maintain contributor paths for docs, examples, validation fixtures, and
  adapter metadata before inviting broad runtime refactors
- prefer opt-in diagnostics, issue-template evidence, and validation reports
  over invasive telemetry

### Enterprise

- provide an enterprise evaluation guide, control evidence packet, deployment
  decision tree, and pilot rollout plan
- keep Copilot enterprise rollout material tied to licensing/policy
  prerequisites, token ownership, audit expectations, and the ban on host
  `~/.copilot`, keychain, and ambient GitHub CLI passthrough
- keep customer-owned cloud resources, no broad IAM grants, no public ingress,
  and explicit rollback as non-negotiable cloud preview requirements
- support MDM-friendly install, upgrade, uninstall, rollback, and support
  bundle flows before broad enterprise rollout
- add centralized policy administration, inventory, and analytics only after
  local export and signed policy distribution are proven
- preserved-boundary IDE and GUI entrypoints must be clients of the session
  plane, not alternate execution paths

## Non-Goals

- weakening the dedicated VM plus container boundary for convenience
- pretending provider config, prompt files, or workspace rules are the primary
  security boundary
- claiming Linux, Windows, Linux `arm64`, or Raspberry Pi support before the
  support matrix, docs, diagnostics, and certification evidence exist
- claiming GitHub Copilot CLI live certification without the staged-token
  provider-e2e gate
- treating Copilot cloud agent, IDE extensions, or host-native Copilot CLI
  execution as equivalent to a Workcell Tier 1 provider adapter
- treating Linux or Windows `compat` support as strict parity
- treating Raspberry Pi as an enterprise host default
- automatic backend fallback
- a fake universal backend abstraction that hides real provider and runtime
  differences
- folding Kubernetes-backed execution into the near-term backend program
- treating managed workstations as interchangeable with raw remote VMs
