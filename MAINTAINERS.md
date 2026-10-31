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
comment, or an unresolved review thread.

Do not describe this process as independent or multi-party approval.

## Pull request review

Apply these rules to every pull request:

1. Keep the pull request open for review.
2. Check top-level comments, inline comments, formal reviews, and unresolved
   threads.
3. Check configured asynchronous reviewers.
4. Fix or disposition each actionable finding.
5. Run the required validation again.
6. If a fix changes the branch, push a signed commit.
7. Resolve each addressed review thread.
8. Repeat the comment sweep after each push.
9. Repeat the comment sweep after CI succeeds and before merge.

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
