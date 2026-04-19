# Roadmap

Workcell is still pre-1.0. The next roadmap slices should turn the shipped
host-side inspection and policy surfaces into a first-class session platform,
then expand deployment reach without weakening the current VM plus container
boundary. Deployment reach should expand through explicit runtime-target
classes and support tiers rather than a flat notion of interchangeable
backends. Delivered features belong in the changelog and user docs rather than
remaining on this roadmap.
The delivery shape for the active slice lives in
`docs/implement-first-delivery-plan.md`. The longer-lived runtime-target and
deployment-reach program lives in `docs/runtime-target-expansion-plan.md`.
The deterministic phase breakdown lives in
`docs/runtime-target-phase-plan.md`.

## Short term

- broaden host-owned auth flows where the current implementation is still thin:
  more resolver coverage plus explicit browser/bootstrap handoffs for provider
  onboarding
- expand end-to-end coverage for authenticated, lower-assurance, and
  session-supervisor transitions so new orchestration features ship with
  invariant checks
- add a narrow trusted `linux/amd64` validation-host lane plus target-aware
  diagnostics before broad non-macOS or cloud support claims
- define the support matrix, remote-VM contract prerequisites, and evidence
  gates that later `compat` and `remote_vm` targets must satisfy, without
  implying backend shipment or Tier 1 Linux or Windows host parity before the
  same guarantees exist

## Medium term

### Session plane

- add pause, resume, checkpoint, and fork flows on the same host-controlled
  session plane
- add reviewed team workflow packs at the Workcell layer: versioned
  instruction bundles, commands, approved MCP packs, and task templates
- add a lightweight TUI or dashboard backed by the same host-controlled
  session plane rather than a separate execution path

### Deployment reach

- ship the first cross-platform compatibility backend with explicit
  lower-assurance labeling and backend-aware diagnostics
- deliver the first remote VM backend for the highest-value use cases:
  secure ephemeral repro and PR environments, standardized onboarding
  environments, and sandboxed agent workspaces in the operator's own account
- reuse the same remote VM contract for the second cloud provider with limited
  provider-specific delta
- keep managed workstations as a separate discovery track rather than treating
  them as the same class as raw remote VMs
- improve comparison material and use-case guidance for teams evaluating
  Workcell in cloud, hybrid, and regulated development workflows
- make release assets and operator verification flows easier to consume

## Long term

- expand remote VM support across the major providers through thin provider
  adapters on the same host-owned control-plane model:
  `AWS` first, `GCP` second, and `Azure` demand-gated third
- add explicit Linux and Windows support only where the same boundary
  guarantees, validation coverage, and operator story can be stated honestly;
  until then, keep those paths labeled `compat`
- evaluate managed workstation targets as a separate product mode with their
  own lifecycle and trust model rather than as peers to local VM or remote VM
  targets
- add enterprise policy administration, centralized session inventory, and
  usage and audit analytics
- add preserved-boundary GUI and IDE entrypoints backed by the same session
  plane

## Non-goals

- weakening the dedicated VM plus container boundary for convenience
- pretending provider config or prompt files are the primary security boundary
- claiming Linux or Windows parity before the same security guarantees exist
- automatic backend fallback
- a fake universal backend abstraction that hides real provider and runtime
  differences
- treating `compat` backends as equivalent to the current strict Colima path
- folding Kubernetes-backed execution into the same near-term backend program
- treating managed workstations as interchangeable with raw remote VMs
