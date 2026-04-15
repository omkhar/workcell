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

## Current operating mode

Workcell currently operates as a single-maintainer project.

That means:

- routine merges and releases may be performed by one maintainer
- asynchronous review from humans and configured async reviewers is expected
  and should be swept before merge
- asynchronous review is advisory input, not equivalent to an independent
  human approval
- the project relies on signed history, strict CI, reproducibility checks,
  provenance, SBOMs, attestations, hosted controls, and public review artifacts
  as compensating controls

This is lower assurance than true separation of duties and should be described
honestly.

## Decision process

Most changes should land through the normal pull request flow.

Changes that affect the runtime boundary, trust model, lower-assurance modes,
release provenance, or hosted controls should include:

- the invariant or threat-model section the change depends on
- the validation or evidence added with the change
- a clear note about any new or widened lower-assurance behavior

Every mergeable PR should also receive a comment sweep before merge:

- top-level comments reviewed
- inline comments reviewed
- unresolved threads resolved or explicitly dispositioned
- asynchronous reviewer comments checked again after CI turns green
- one final comment sweep completed immediately before merge

When a change is large or controversial, open an issue first and describe the
problem, alternatives, and invariants that must remain intact.

## Stability and releases

Workcell is currently pre-1.0. Breaking changes may happen, but they should be
called out in [CHANGELOG.md](CHANGELOG.md), reflected in the docs, and kept
deliberate rather than incidental.

Releases normally land through a short-lived release PR, followed by green
post-merge `main` CI, a signed tag, the tagged `Release` workflow, and final
verification of the published GitHub release and assets.

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
