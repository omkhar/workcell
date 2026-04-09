# Governance

Workcell is a maintainer-led open source project with an explicit bias toward
auditable security boundaries, small reviewable changes, and clear operator
contracts.

## Goals

- keep the runtime boundary and trust model explicit
- make the secure path easier than the ad hoc path
- keep provider adapters thin and native
- grow contributor capacity without diluting the project's invariants

## Roles

### Maintainer

Maintainers set roadmap direction, review high-risk changes, cut releases, and
decide when project contracts change.

### Reviewer

Reviewers provide trusted review on a subsystem or workflow area. Reviewers do
not need blanket authority across the whole repository.

### Contributor

Contributors improve code, docs, tests, workflows, and design material through
pull requests and issues.

## Decision process

Most changes should land through the normal pull request flow.

Changes that affect the runtime boundary, trust model, lower-assurance modes,
release provenance, or hosted controls should include:

- the invariant or threat-model section the change depends on
- the validation or evidence added with the change
- a clear note about any new or widened lower-assurance behavior

When a change is large or controversial, open an issue first and describe the
problem, alternatives, and invariants that must remain intact.

## Stability and releases

Workcell is currently pre-1.0. Breaking changes may happen, but they should be
called out in [CHANGELOG.md](CHANGELOG.md), reflected in the docs, and kept
deliberate rather than incidental.

## Becoming a reviewer or maintainer

New reviewers and maintainers are added by existing maintainers based on:

- sustained high-signal contributions
- sound judgment on security and operator-facing tradeoffs
- timely and constructive review behavior
- willingness to document decisions, not just make them

The project should prefer multiple trusted reviewers over concentrated
ownership wherever practical.

## Conflict resolution

If maintainers disagree, the immediate rule is to preserve the narrower trust
boundary and the clearer operator contract until the disagreement is resolved.
