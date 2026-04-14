# Roadmap

Workcell is still pre-1.0. The near-term roadmap is about making the current
security model easier to adopt, easier to verify, and easier to contribute to.
The delivery shape for the active slice lives in
`docs/implement-first-delivery-plan.md`.

## Implement first

- ship Phase 2 of the Workcell session supervisor:
  detached/background sessions, `session start`, `session attach`,
  `session send`, `session stop`, `session diff`, and default
  worktree-per-session flows
- add `workcell why` and richer policy inspection so operators can see why
  a path, credential, endpoint, or control-plane input is allowed or blocked
- improve host-owned auth flows: better resolver coverage, clearer
  `auth-status` diagnostics, and explicit login/setup handoffs where provider
  onboarding needs browser or credential bootstrap help

## Implement next

- add reviewed team workflow packs at the Workcell layer: versioned
  instruction bundles, commands, approved MCP packs, and task templates
- add session observability on top of the supervisor:
  live status, branch/worktree, assurance state, logs, transcript pointers,
  and command timeline views
- add a lightweight TUI or dashboard backed by the same host-controlled
  session plane rather than a separate execution path

## Next 90 days

### Distribution and onboarding

- keep the secure install path simple from a release, not just from a source checkout
- standardize provider onboarding around `workcell auth init|set|status`
- keep the CLI and docs examples consistent across README, manpage, and quickstarts

### Boundary verification

- keep the local macOS Colima boundary proof clearly documented as a local
  operator responsibility
- expand end-to-end coverage for authenticated and lower-assurance transitions

### Community and contributor experience

- document governance, support, conduct, and maintainer expectations
- lower local setup friction with a single contributor bootstrap command
- make project direction visible through a changelog and roadmap

### Ecosystem fit

- keep provider quickstarts current as upstream CLIs evolve
- improve comparison material and use-case guidance for teams evaluating Workcell
- make release assets easier for operators to consume and verify
- define the first non-macOS deployment roadmap explicitly: trusted
  `linux/amd64` remote validation hosts, self-hosted release and reproducibility
  builders, and operator-managed deployment targets, without claiming Tier 1
  Linux or Windows host parity before the same boundary guarantees exist

## Non-goals

- weakening the dedicated VM plus container boundary for convenience
- pretending provider config or prompt files are the primary security boundary
- claiming Linux or Windows parity before the same security guarantees exist
