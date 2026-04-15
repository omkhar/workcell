# Roadmap

Workcell is still pre-1.0. The next roadmap slices should turn the shipped
host-side inspection and policy surfaces into a first-class session platform,
then expand deployment reach without weakening the current VM plus container
boundary. Delivered features belong in the changelog and user docs rather than
remaining on this roadmap.
The delivery shape for the active slice lives in
`docs/implement-first-delivery-plan.md`.

## Short term

- finish Phase 2 of the session supervisor:
  detached/background sessions, `session start`, `session attach`,
  `session send`, `session stop`, and default worktree-per-session flows
- add session observability on top of the current host-side inventory:
  live status, branch/worktree, assurance state, logs, transcript pointers,
  and command timeline views
- broaden host-owned auth flows where the current implementation is still thin:
  more resolver coverage plus explicit browser/bootstrap handoffs for provider
  onboarding
- expand end-to-end coverage for authenticated, lower-assurance, and
  session-supervisor transitions so new orchestration features ship with
  invariant checks
- define the first remote and non-macOS deployment targets explicitly:
  trusted `linux/amd64` validation hosts, operator-managed deployment targets,
  and the first cloud-spawn path, without claiming Tier 1 Linux or Windows host
  parity before the same guarantees exist

## Medium term

- add reviewed team workflow packs at the Workcell layer: versioned
  instruction bundles, commands, approved MCP packs, and task templates
- add a lightweight TUI or dashboard backed by the same host-controlled
  session plane rather than a separate execution path
- deliver the first cloud-spawned workspace path for the highest-value use
  cases:
  secure ephemeral repro and PR environments, standardized onboarding
  environments, and sandboxed agent workspaces in the operator's own account
- improve comparison material and use-case guidance for teams evaluating
  Workcell in cloud, hybrid, and regulated development workflows
- make release assets and operator verification flows easier to consume

## Long term

- expand cloud spawning across the major providers `AWS`, `Azure`, and `GCP`
  through thin provider adapters rather than provider-specific trust models
- add explicit Linux and Windows support only where the same boundary
  guarantees, validation coverage, and operator story can be stated honestly
- add enterprise policy administration, centralized session inventory, and
  usage and audit analytics
- add preserved-boundary GUI and IDE entrypoints backed by the same session
  plane

## Non-goals

- weakening the dedicated VM plus container boundary for convenience
- pretending provider config or prompt files are the primary security boundary
- claiming Linux or Windows parity before the same security guarantees exist
