# Maintainers

## Current maintainers

| GitHub | Role | Areas |
|---|---|---|
| `@omkhar` | Lead maintainer | runtime boundary, adapters, release posture, policy, docs |

## Current release mode

- Workcell currently operates in single-maintainer release mode.
- `@omkhar` may merge release PRs, approve the `release` environment, and cut
  signed tags after required checks and comment sweeps succeed.
- Asynchronous review from humans and configured async reviewers is expected
  input but is not a substitute for an independent maintainer.
- Release status should describe this honestly as single-maintainer operation,
  not as multi-party approval.

## Review expectations

- boundary- or policy-affecting changes should not merge without maintainer
  review
- contributor-facing docs should stay aligned with shipped behavior
- release and provenance changes should include verification updates
- every PR should be checked for top-level comments, inline review comments,
  and unresolved review threads before merge
- actionable comments from human or asynchronous reviewers should be addressed
  or explicitly dispositioned before merge
- comments should be checked again after CI turns green and immediately before
  merge

## Growth plan

The project intends to add reviewers and maintainers over time, starting with:

- runtime and policy review capacity
- provider adapter review capacity
- docs and onboarding review capacity
- release and supply-chain review capacity
- at least one backup release approver who can independently review or approve
  security-sensitive release work when available

See [GOVERNANCE.md](GOVERNANCE.md) for the role model and
[ROADMAP.md](ROADMAP.md) for the near-term priorities.
