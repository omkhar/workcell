---
name: workcell-pr-lifecycle
description: Publish, follow, and merge Workcell pull requests through the repo-approved host-side workflow. Use when the user asks to commit, raise a PR, follow checks, mark ready, address review feedback, or merge in the Workcell repository.
---

# Workcell PR Lifecycle

Use this skill only in the Workcell repository root, identified by:

- `AGENTS.md`
- `scripts/workcell`
- `policy/reviewer-identities.toml`

Use it when a task includes any part of the Workcell pull request lifecycle:

- preparing signed commits for publication
- opening a PR
- following PR checks
- fixing CI or review feedback
- marking a PR ready
- merging a PR

## Standing priorities

Always prefer, in order:

1. Simplicity
2. Correctness
3. Linting and clean validation
4. Appropriate test coverage
5. Security
6. Performance
7. Current idiomatic correctness

These priorities apply only inside the repo invariants. Do not trade away the
runtime boundary, explicit security guarantees, or host-side publication rules
for convenience.

Treat every user request as implicitly including peer review unless the user
explicitly narrows that scope. For PR work, peer review means continuing
through review, fixes, validation, another review pass, and hosted workflow
follow-through until no actionable findings remain or a concrete blocker is
reported.
Treat that as a continuing loop with the same peers and review surfaces. If a
comment sweep, thread sweep, CI rerun, or follow-up review produces new
findings after a fix, keep iterating until every finding is resolved,
explicitly dispositioned, or blocked by a concrete external constraint.
Treat repeated PR friction, publication fallbacks, or hosted-check surprises as
signals that the repo-local lifecycle instructions should improve. When a
durable process lesson appears, capture it in a versioned repo-local update
instead of leaving it as an unwritten workaround.

## Read first

- `AGENTS.md`
- `.agents/skills/commit/SKILL.md`
- `policy/reviewer-identities.toml`

If the task changes user-visible Workcell workflows, docs, or evidence, also
use the repo-local `workcell-contract-parity` skill.

If the task is release-bound, also read:

- `docs/releasing.md`

## Invariants

- Final GitHub publication is a host-side action. Use the repo-local
  `./scripts/repo-publish-pr.sh` wrapper for `main`-based PRs so fresh local
  parity evidence is enforced before it delegates to
  `./scripts/workcell publish-pr`; do not normalize direct publication from
  the Tier 1 session.
- Upstream refresh candidate issues and artifacts from
  `.github/workflows/upstream-refresh.yml` are advisory only. Authoritative
  upstream refresh publication goes through the repo-local
  `./scripts/publish-upstream-refresh-pr.sh` helper, which recreates the
  refresh locally and then calls `./scripts/repo-publish-pr.sh`.
- `main` is the only supported PR base by default. If a lower-assurance
  non-`main` base path exists, keep that PR draft-only, treat it as
  non-mergeable, and do not claim the normal `main`-based repo-owned
  validation or merge gating exists for that branch shape.
- Keep PRs reviewer-sized and single-purpose. Split broad work before
  publication.
- Sign every commit and use feature branches.
- If the change introduces or materially changes a supported end-to-end
  workflow, backend, support-tier claim, or certification-only validation
  path, ensure the relevant live certification passed before the signed commit
  was created. Do not use PR publication as a substitute for required
  end-to-end certification.
- Open the PR as a draft first. Mark it ready only after the review and check
  gates below are satisfied.
- Do not stop at PR creation. Follow repo-owned checks until they are green,
  fix failures, and rerun the relevant local validation before pushing more
  commits.
- Sweep top-level comments, inline comments, unresolved review threads, and
  configured async reviewers in `policy/reviewer-identities.toml`.
- Mark the PR ready only after repo-owned checks are green and the review
  surfaces have no actionable findings.
- After marking ready, re-check checks and review surfaces again. Some repos
  gate differently once a PR leaves draft.
- If the task includes merging, re-check review surfaces immediately before
  merge, then follow merged `main` workflows until all repo-owned lanes are
  green.
- Do not accept failing repo-owned tests, checks, or workflows as acceptable
  residue. Fix them or explicitly change the claimed guarantee in the same
  review unit.

## Workflow

1. Confirm the branch is reviewable and the local worktree only contains the
   intended scope.
2. Run any required live end-to-end certification before signing commits for
   support-claim or backend changes.
3. Create signed commits using the repo-local `commit` skill.
4. Run the focused local validation for the change before publication.
5. If validation, CI follow-up, or failed live runs created Workcell-owned
   residue, run `./scripts/workcell --gc` or the narrow cleanup path before
   publication. Do not leave temp roots, validator images, or over-budget
   runtime-cache debris for later turns.
6. Publish `main`-based PRs with host-side `./scripts/repo-publish-pr.sh`
   using a draft PR by default. Only fall back to the lower-level
   `./scripts/workcell publish-pr` path for explicit lower-assurance
   non-`main` exceptions or other repo-approved special cases.
7. Follow repo-owned checks to completion.
8. If a repo-owned check fails:
   - inspect the failing GitHub Actions logs or PR checks
   - fix the underlying issue locally
   - rerun the smallest local validation that proves the fix
   - push the signed follow-up commit host-side to the existing branch
   - continue following checks until green
9. Sweep top-level comments, inline comments, unresolved threads, and async
   reviewer feedback.
10. When checks are green and no actionable findings remain, mark the PR ready
   unless the user explicitly asked to keep it draft. Do not mark non-`main`
   base PRs ready; they stay lower-assurance draft-only review units.
11. After marking ready, re-check checks and review surfaces again.
12. If merge is part of the task, repeat the review sweep immediately before
    merge, merge, then follow merged `main` workflows until repo-owned lanes
    are green.
13. If the task exposed a reusable PR-lifecycle or hosted-validation lesson,
    update the relevant repo-local instructions in the same change stream or a
    separate follow-on PR.

## Validation

Always run the smallest local validation that proves the actual change. When
the work updates repo-wide instructions or multiple docs/skills, finish with:

```sh
bash ./scripts/validate-repo.sh
```

Use the GitHub plugin helpers when they fit the task:

- `github:gh-fix-ci` for failing Actions lanes
- `github:gh-address-comments` for actionable review feedback

## Blocking rule

Do not call PR work complete while any of these remain true:

- the branch has unpublished intended changes
- repo-owned checks are still red or unreviewed
- review surfaces still contain actionable findings
- merged `main` workflows still show repo-owned failures for a merge task
