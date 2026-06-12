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
[`docs/provider-matrix.md`](docs/provider-matrix.md). Codex, Claude Code, and
Gemini are the supported Tier 1 provider adapters today. GitHub Copilot CLI is
the next committed provider-parity track, but it is not a support claim
until its adapter, auth path, docs, deterministic evidence, and live
certification land together.

The active delivery shape lives in
[`docs/implement-first-delivery-plan.md`](docs/implement-first-delivery-plan.md).
The longer-lived runtime-target and deployment-reach program lives in
[`docs/runtime-target-expansion-plan.md`](docs/runtime-target-expansion-plan.md).
The deterministic phase breakdown lives in
[`docs/runtime-target-phase-plan.md`](docs/runtime-target-phase-plan.md).

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
  Codex, Claude, and Gemini adapters, rather than through provider-specific
  shortcuts.

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
- GitHub Copilot CLI is planned as the next Tier 1 provider adapter. Current
  releases do not support `--agent copilot`, Copilot credential keys, or a
  Copilot quickstart.
- Upstream retires Gemini CLI for free, Pro, and Ultra accounts on June 18,
  2026 in favor of the closed-source Antigravity CLI; Gemini Code Assist
  Standard and Enterprise licenses keep Gemini CLI access. Reviewed posture:
  the Gemini Tier 1 adapter is supported for those licensed auth inputs
  only after that date, and an Antigravity adapter is a committed follow-on
  provider-parity track behind the same Tier 1 evidence bar (see
  [docs/provider-matrix.md](docs/provider-matrix.md)).

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

Deliver GitHub Copilot CLI to the same end-state support bar as Codex, Claude
Code, and Gemini before the Linux operator-host expansion resumes. This is a
provider-adapter phase, not a host-support promotion.

Exit gates:

- `workcell --agent copilot --workspace /path/to/repo` launches Copilot CLI
  fully inside the bounded Workcell runtime
- Copilot home, cache, settings, permissions, sessions, logs, plugins, hooks,
  MCP/LSP state, and instruction imports are session-local or explicitly
  staged; host `~/.copilot`, host keychains, ambient `gh` auth, and whole-home
  passthrough remain outside the safe path
- the primary auth path is an explicit staged credential such as
  `copilot_github_token`, exported only to the managed Copilot child process as
  `COPILOT_GITHUB_TOKEN`
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

## Adoption Workstreams

### Open Source

- publish a short supported quickstart for the strict macOS path
- publish a clearly labeled Docker Desktop compat quickstart
- publish the Copilot CLI quickstart only with its adapter, auth, and
  certification evidence; until then, keep Copilot labeled as planned parity
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
- require Copilot enterprise rollout material to document licensing/policy
  prerequisites, token ownership, audit expectations, and the ban on host
  `~/.copilot`, keychain, and ambient GitHub CLI passthrough before support
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
- claiming GitHub Copilot CLI support before the Tier 1 adapter, auth model,
  unsafe-argument policy, docs, tests, and live certification evidence land
- treating Copilot cloud agent, IDE extensions, or host-native Copilot CLI
  execution as equivalent to a Workcell Tier 1 provider adapter
- treating Linux or Windows `compat` support as strict parity
- treating Raspberry Pi as an enterprise host default
- automatic backend fallback
- a fake universal backend abstraction that hides real provider and runtime
  differences
- folding Kubernetes-backed execution into the near-term backend program
- treating managed workstations as interchangeable with raw remote VMs
