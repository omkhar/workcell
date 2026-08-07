# GitHub Copilot CLI Delivery Record

This page records the shipped GitHub Copilot CLI adapter. It does not define
Linux host support.

## Shipped Controls

Workcell supports GitHub Copilot CLI as a Tier 1 provider adapter. The adapter
runs inside the bounded runtime.

Workcell applies these controls:

- Workcell accepts a directly staged `copilot_github_token` credential.
- The host-owned credential path stages the token.
- A temporary handoff mount carries the token outside provider state.
- The Workcell entrypoint removes the mounted token file before launch.
- The wrapper exports `COPILOT_GITHUB_TOKEN` only to the managed child.
- Workcell uses session-local `COPILOT_HOME` and `COPILOT_CACHE_HOME`.
- Workcell does not mount host GitHub CLI authentication or Copilot state.
- Workcell disables custom instructions on the managed path.
- Workcell blocks options that can bypass Workcell policy.

The adapter does not use host keychains, `GH_TOKEN`, or `GITHUB_TOKEN`. It does
not use host `~/.copilot`, `~/.config/github-copilot`, or
`~/.cache/github-copilot` state.

## Certification Gate

Before you sign a support-claim change, complete non-destructive provider-e2e
certification through Workcell. Use the staged `copilot_github_token` path.
Repository tests do not replace this certification.

## Operator Sources

Use [Provider Matrix](provider-matrix.md) for the current support boundary. Use
[Quickstart: GitHub Copilot CLI](examples/quickstart-copilot.md) for operator
instructions.

## Linux Host Work

Workcell completed the Tier 1 Copilot provider adapter. Phase 13 is the next
runtime-target candidate. It will evaluate one exact Linux amd64
`local_compat` combination.

Use [Host Expansion Readiness](host-expansion-readiness.md) for the promotion
gate. Use [Runtime Target Phase Record](runtime-target-phase-plan.md) for the
phase status.
