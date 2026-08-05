# Roadmap

Workcell 1.0 freezes the reviewed local boundary and operator contract. The
roadmap must expand adoption without weakening
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
below. The [1.0 Program Record](#10-program-record) contains the historical
release criteria and deferred work.

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

## 1.0 Program Record

Workcell published `v1.0.2` on 2026-08-05. This release completed the 1.0
program. The `v1.0.0` and `v1.0.1` tags did not produce published releases.

The 1.0 program froze the public operator contract. It certified the reviewed
macOS boundary and completed the reviewed release process. It did not add
Linux, Windows, or managed workstation support.

### 1.0 Release Criteria

The final review confirmed these results:

1. Workcell froze the v1 command and data contracts.
2. Workcell shipped the A1 through A6 boundary controls.
3. Workcell certified strict Colima and Docker Desktop compatibility on macOS.
4. Workcell supported Codex, Claude Code, Copilot CLI, and Gemini.
5. Workcell proved install, upgrade-in-place, rollback, uninstall,
   `workcell --gc`, and support bundle operations.
6. Workcell used mutation tests, signed history, provenance, and immutable
   release controls.
7. Workcell shipped the in-repository user and assurance documents.
8. The G4 review had no open P0 or P1 findings.

The final G4 record is in
[`docs/1.0-readiness-review.md`](docs/1.0-readiness-review.md). It contains the
exact evidence and each deferred item.

The maintainer deferred these items to work after 1.0:

- a second release approver
- an automated Apple Silicon boundary lane; see the
  [B6 decision record](docs/b6-disposition-options-draft.md)
- a third-party boundary audit and an OpenSSF badge
- a rendered document site and external demos
- a certified session start time claim
- Google Antigravity CLI support

### Milestone Train

These historical milestones led to 1.0. The detailed record is in
[`docs/improvement-tracks-implementation-plan.md`](docs/improvement-tracks-implementation-plan.md).

| Milestone | Theme | Contents |
|---|---|---|
| v0.12 | Containment and hygiene | A2, A7, B3, B4, B5, D1, D2, E3, E4 |
| v0.13 | Boundary depth and stability | A1, A3, A4, B1, C5, D8, E1, E2, F3, G1 (inventory) |
| v0.14 | Platform, speed, and adoption | C1, B8, B9, D3 (start), D4, E5, E7, G2, Antigravity Tier 1 adapter track |
| v0.15 | Enterprise evidence and release assurance | A5, A6, C3, D5, D7, F1, G3 |
| v1.0 | Freeze and gate | G1 (freeze), G4, D3 (complete), D6 |
| post-1.0 | Reach expansion | Phases 13–19, C2, C4, B2, B6, B7, E6, F2 |

Phase 13 is the next host support candidate. It does not create a Linux
support claim.

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

Status: complete. Workcell supports GitHub Copilot CLI as a Tier 1 provider.
The release process requires live provider certification before a material
support claim change.

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
[Milestone Train](#milestone-train) under the 1.0 Program Record, and the per-item
implementation plan — steps, exit gates, dependencies, and validation
expectations — lives in
[`docs/improvement-tracks-implementation-plan.md`](docs/improvement-tracks-implementation-plan.md).

### Track A: Boundary Depth And Agent-Threat Defenses

The external threat picture moved fast in 2025–2026: one-keypress RCE via
repo-defined MCP servers across three of the four supported provider CLIs —
Claude Code, Gemini CLI, and Copilot CLI (the TrustFall disclosure; Codex CLI
was not in the disclosed set), prompt injection through PR titles and
comments against agent PR-review integrations, recurring
npm worm campaigns (Shai-Hulud and successors), and sandbox-bypass CVEs in
OS-level sandboxes (Seatbelt/bubblewrap). Workcell's VM-plus-container
boundary and staged-credential model match the containment doctrine the
strongest vendors now publish; these items deepen that lead.

- **A1 (complete, M):** egress policy depth and target parity — document, extend,
  and parity-label the shipped strict default-deny allowlist rather than
  build a duplicate lane
- **A2 (complete, M):** repo-defined MCP and agent-config containment,
  deny-by-default in `strict` with acknowledged, audited exceptions
- **A3 (complete, M):** fuzzing expansion across the Rust shim and Go parsers,
  wired into continuous fuzzing
- **A4 (complete, S):** `SAFETY:` documentation for every unsafe Rust block plus a
  pre-audit checklist
- **A5 (complete, M):** signed, tamper-evident session audit records verifiable
  from outside the agent
- **A6 (complete, M):** documented syscall/filesystem hardening profile with a
  deterministic conformance check
- **A7 (complete, S):** OWASP Agentic Top 10 control mapping feeding the Phase 11
  evidence packet

### Track B: Supply Chain And Release Assurance

CI is already strong: all 72 action references SHA-pinned, workflows start
from `permissions: {}`, reproducible builds verified on amd64 and arm64,
keyless Sigstore signing, SBOMs, and GitHub attestations. These items close
the remaining gaps between that posture and the level enterprises will ask a
security-boundary product to prove.

- **B1 (complete, M):** SLSA v1.0 gap analysis published in the provenance docs
- **B2 (post-1.0, M):** dual-control release approval — deferred by the
  2026-07-09 criterion-6 amendment because no second trusted maintainer exists;
  1.0 uses the documented single-maintainer controls in `docs/releasing.md`
- **B3 (complete, M):** scheduled mutation-score lane with published score and
  baseline regression gate, beyond today's release-preflight-only run
- **B4 (complete, S):** centralized tool pins and a permitted-GitHub-actions
  allowlist check
- **B5 (complete, S):** audit-trail retention policy with extended
  release-evidence retention and post-expiry attestation/Rekor guidance
- **B6 (post-1.0, L):** real-boundary certification lane on Apple Silicon runner
  infrastructure, gated on the CI threat model — deferred to post-1.0 by the
  2026-07-09 criterion-6 amendment (no runner funding); 1.0 uses local-operator
  certification of both boundaries instead
- **B7 (post-1.0, S + L):** OpenSSF Best Practices badge and funded
  third-party boundary audit — both deferred to post-1.0 by the 2026-07-09
  criterion-2/6 amendment
- **B8 (complete, M):** CI efficiency and reliability program — nightly
  reproducibility split, retries, flake tracking, cost visibility
- **B9 (complete, M):** CI/CD threat model covering secrets, runner trust tiers,
  attestation assumptions, and signing-compromise response

### Track C: Runtime Platform Evolution

- **C1 (evaluated, L):** Apple `container` backend evaluation (macOS 26+) as
  `local_vm/apple-container` — **recorded go/no-go: GO on the evaluation
  (per-session VM, sub-second boot, confirmed on macOS 26.5.1);
  `preview-only`/`blocked` and support-invisible; Colima stays the reviewed
  default (and the only option below macOS 26); promotion to a supported
  backend deferred post-1.0 pending the B6 certification lane.** See
  [docs/apple-container-evaluation.md](docs/apple-container-evaluation.md)
- **C2 (post-1.0, M):** session start latency program with cached images, an
  optional kept-warm lane, and a certification design that does not broaden the
  launcher with destructive controls; generic output remains benchmark-only
- **C3 (implemented; locally certified 2026-08-03, L):** native parallel
  sessions — one agent per worktree, branch, and isolated runtime, with
  session-record linkage. The exact-tree pre-merge certification is bound to
  signed activation commit `a26b750f`, which is reachable from reviewed `main`
- **C4 (later, L):** container tooling inside the boundary as an explicit
  labeled lane that never weakens the outer boundary
- **C5 (complete, S):** syscall-shim performance baselines for the hooked
  exec/spawn paths

### Track D: Code Health And Consolidation

The Go tree is in good shape (28.8k source lines, 82% test-to-code ratio,
three direct dependencies). The concentration risks are a 2,288-line
monolithic Rust interception library, a 8,910-line launcher script, a
9,131-line `verify-invariants.sh`, and widespread helper duplication across
120 shell scripts (for example, 25 separate `cleanup()` definitions).

- **D1 (complete, S):** language-boundary doctrine — Rust for the shim, Go for
  logic, shell as thin glue
- **D2 (complete, M):** shared shell helper library plus a shellcheck lane over
  all scripts
- **D3 (complete, L):** migrate the static `verify-invariants` scope to Go;
  retain the documented static residual, dynamic tail, and container-smoke
  orchestration in bash by the recorded G4 narrowing
- **D4 (complete, M):** modularize the launcher and document its contract
- **D5 (partial; post-1.0 remainder, L):** the git-policy module is extracted;
  carry the remaining Rust interception-library module splits post-1.0
- **D6 (complete for 1.0, M):** split the oversized Go validators by major
  format; retain the small inline validators and dispatcher-main refactor as
  post-1.0 code-health work by the recorded G4 scope decision
- **D7 (partial; post-1.0 remainder, M):** property-based session-lifecycle
  tests and shell-protocol coverage shipped; carry Go benchmarks and the broader
  shell unit-test lane post-1.0
- **D8 (complete, S):** stability contracts for internal APIs, CLI surfaces, and
  a unified exit-code contract

### Track E: Documentation And Adoption

- **E1 (complete, M):** tiered documentation entry points and a slimmer README
- **E2 (complete, M):** maintained architecture diagrams in the system design doc
- **E3 (complete, S):** support-tier legend and a `--doctor`/`--inspect`
  diagnostics interpretation guide
- **E4 (complete, S):** docs CI depth — link checking, orphan detection, status
  labels, freshness markers
- **E5 (complete, M):** injection-policy annotated schema with complete
  per-provider examples
- **E6 (post-1.0, M):** adoption growth kit — docs site, terminal demos,
  Homebrew tap, isolation-model architecture post backed by benchmarks;
  deferred by the 2026-07-09 criterion-7 amendment
- **E7 (complete, S):** contributor runbook depth and substantive adapter READMEs

### Track F: Enterprise And Standards Alignment

- **F1 (complete, M):** OCSF-mapped audit export — the concrete Phase 17 format
  decision
- **F2 (later, M):** SPIFFE-style per-session identity groundwork feeding
  Phase 15
- **F3 (complete, S):** standards watchlist (MCP spec line, OWASP agentic
  guidance, agent-identity drafts) with a review cadence

### Track G: 1.0 Contract And Operations

These items exist specifically to make 1.0 a truthful claim: a frozen public
contract and proven day-two operations.

- **G1 (complete, M):** the public contract inventory, v1 stability
  classification, deprecation policy, release-preflight drift gate, and exact
  G4 evidence identities are versioned
- **G2 (complete, M):** `workcell support-bundle` ships with documented
  redaction rules
- **G3 (complete, M):** install, upgrade, uninstall, rollback, and `--gc` have
  repeatable CI evidence plus published-release local certification
- **G4 (complete, S):** the cross-lens 1.0 readiness gate records fixed
  statuses, exact candidate/evidence identities, every explicit scope decision,
  and no unresolved P0/P1 findings

### Sequencing Summary

The [Milestone Train](#milestone-train) records the historical sequence to
1.0. The provider and target phases define the sequence after 1.0.

## Adoption Workstreams

### Open Source

- maintain the supported quickstart for the strict macOS path
- maintain the clear Docker Desktop compatibility instructions
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

- maintain the enterprise guide, evidence baseline, deployment choices, and
  pilot plan
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
