# Maintainers

## Maintainer

| GitHub account | Role | Areas |
|---|---|---|
| `@omkhar` | Lead maintainer | Runtime, adapters, policy, releases, and documentation |

## Single-maintainer operation

Workcell uses a single-maintainer release process. `@omkhar` can merge a pull
request, approve the `release` environment, and create a signed tag.

The maintainer must first complete all required checks and comment sweeps.
Administrative merge authority can satisfy only the independent-approval
requirement. It cannot override a commit-signature gate or the base-branch
policy. It cannot override a failed repository check, an unresolved actionable
comment, or a missing clean Codex review of the current head commit. It cannot
override an unresolved review thread.

Do not describe this process as independent or multi-party approval.

## Pull request review

Apply these rules to every pull request:

1. Keep the pull request open for review.
2. After every push, post a standalone issue comment with only
   `@codex review`.
3. Inspect issue comments, inline comments, formal reviews, and the trigger
   reaction.
4. React to each Codex finding.
5. Fix or disposition each actionable finding.
6. Run the required validation again.
7. If a fix changes the branch, push a signed commit.
8. After each new push, request another review.
9. Resolve each addressed Codex thread.
10. Require a fresh clean result for the current head commit.
11. Continue until Codex reports no actionable finding for the current head.
12. Check top-level comments, inline comments, formal reviews, and unresolved
    threads.
13. Check configured asynchronous reviewers.
14. Repeat the comment sweep after CI succeeds and before merge.

An asynchronous review is advisory input. It is not an independent maintainer
approval.

## Future growth

The project can add maintainers for these areas:

- Runtime and policy.
- Provider adapters.
- Documentation and support for new contributors.
- Release and supply chain.

See [Governance](GOVERNANCE.md) for the role model. See the
[Roadmap](ROADMAP.md) for planned work.
