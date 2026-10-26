---
name: workcell-pr-lifecycle
description: Publish, review, follow, and merge Workcell pull requests through the approved host workflow. Use for signed commits, PR publication, checks, review comments, ready state, merge, or post-merge follow-up.
---

# Workcell Pull Request Lifecycle

Use this skill only in the Workcell repository. Confirm that these files exist:

- `AGENTS.md`
- `scripts/workcell`
- `policy/reviewer-identities.toml`

## Read First

Read:

- `AGENTS.md`
- `.agents/skills/commit/SKILL.md`
- `policy/reviewer-identities.toml`

For a public workflow or contract change, also use
`workcell-contract-parity`. For release work, read `docs/releasing.md`.

## Publication Rules

- Sign each commit.
- Use a feature branch.
- Do not force-push a branch after pull request publication.
- Use a new signed follow-up commit for each correction.
- If history replacement is necessary, stop and request explicit user
  authorization.
- After authorization, create a new branch with a signed replacement commit.
- Publish the replacement as a new draft pull request.
- Link the old and replacement pull requests in both directions.
- Close the old pull request after the replacement exists and the links exist.
- Use `main` as the pull request base by default.
- Keep a non-`main` pull request draft. Do not merge it.
- Open each pull request as a draft.
- Keep each review unit small and single-purpose.
- Publish from the host with `./scripts/repo-publish-pr.sh`.
- Before each publication, run `pr-parity` for the exact head and base.
- Do not use `--allow-parity-override` for normal publication.
- Treat missing or mismatched parity evidence as a blocker.
- Use an override only when a repository runbook documents it.
- Require explicit user authorization and acknowledgement for the override.
- After you create a signed merge commit to resolve a base conflict, rerun
  `pr-parity` for that exact head before publication.
- Use the lower-level publication helper only for an exception that `AGENTS.md`
  or a repository runbook permits.
- If the commit changes a supported end-to-end workflow or backend, complete
  live certification. Apply the same gate to a support-tier claim or
  certification-only validation path. Complete certification before you sign
  the commit.
- Clean Workcell-owned residue when validation creates it.

The no-force-push rule is an operator procedure. It does not configure or
enforce GitHub branch protection.

The upstream-refresh workflow makes a provider candidate and a toolchain
candidate. It publishes each one as a PR with a GitHub App token, except a
candidate that changes `.github/workflows/`. See `docs/github-workflows.md`. An
operator publishes such a candidate on the host with
`./scripts/publish-upstream-refresh-pr.sh --candidate <kind>`. That helper
recreates the change locally and calls `./scripts/repo-publish-pr.sh`.

## Hosted Mutation Order

Serialize each push, base edit, title or body edit, and draft or ready
transition.

Treat the approved publisher's initial branch push and draft PR creation as one
publication operation. After the pull request exists:

1. Wait for its opening `Allowed PR base` run.
2. Require that run to succeed before any later mutation.
3. Confirm that `headRefOid` equals the published commit.

After each later mutation:

1. Wait for its own `Allowed PR base` run.
2. Confirm that the run matches the current pull request state.
3. Require success before the next mutation.
4. After a push, confirm that `headRefOid` equals the pushed commit.

If base policy fails because of the current state, make only the corrective
draft or base change. Then wait for the replacement policy run.

## Required Review Surfaces

For each pull request, inspect:

- Top-level comments.
- Inline review comments.
- Unresolved review threads.
- Reviews and requested changes.
- Configured asynchronous reviewers in
  `policy/reviewer-identities.toml`.

Fix or explicitly disposition each actionable finding. An asynchronous review
is advisory. It is not an independent approval.

## Parallel Work

When one operator manages a PR queue, finish the current PR before you publish
the next PR. This reduces stale-base and review churn.

Keep only one remote mutation active for a PR. Use one coordinator for
publication, pushes, review triggers, state transitions, and merge.

Other agents can review, validate, and poll in parallel. They must not mutate
the same PR concurrently.

Use this compact JSON envelope for agent handoffs:

```json
{"u":"","op":"","state":"","repo":"","pr":0,"base":"","head":"","evidence":[],"findings":[],"tests":[],"blocker":"","next":""}
```

Do not include credentials, tokens, or raw comment bodies. Reconstruct remote
state before a mutation. Record reviewed heads, checks, reactions, replies, and
thread states with stable identifiers.

### Observation Identity and Polling

Record observation owner, tool, session handle, and OS PID as separate fields.
Let the originating agent poll its agent-scoped session handle.
Let peers inspect an OS process only after they resolve its PID and provenance.

Treat an unknown cross-agent handle or quiet output as non-terminal evidence.
Do not restart a workload only because observation timed out.
Inspect the target filesystem capacity when disk state affects the observation.

## Check and Merge Workflow

1. Confirm that the worktree contains only intended changes.
2. Run live certification when the publication rules require it.
3. Create signed commits with the `commit` skill.
4. Run focused local validation.
5. Run `/usr/bin/env -u GIT_PAGER ./scripts/validate-repo.sh` when the change
   affects repository-wide behavior, contracts, policies, or validation.
6. Use focused validators for narrow documentation or configuration changes.
7. Run Workcell-owned cleanup when validation creates residue.
8. Run `./scripts/pre-merge.sh --profile pr-parity`.
9. Publish a draft pull request with `./scripts/repo-publish-pr.sh`.
10. Follow all repository-owned checks.
11. Inspect the required review surfaces.
12. Fix each check or review failure.
13. Repeat each gate that the failure affects.
14. Mark the pull request ready only when required checks succeed and the
   review surfaces have no actionable finding.
15. Wait for the ready-state base-policy run, then recheck the required review
    surfaces.
16. Immediately before merge, recheck the required review surfaces.
17. Merge the pull request.
18. Follow all workflows for the merged `main` commit.
19. For release, cleanup, or a task that merges all pull requests, run the
   repository readiness check.

Use required-check polling for the merge gate:

```sh
gh pr checks PR_NUMBER --repo OWNER/REPO --required --watch
```

Use the full check list as an advisory sweep. Skipped optional jobs must not hide
a required failure.

For release, cleanup, or a task that merges all pull requests, run after merge:

```sh
./scripts/check-repo-readiness.sh --repo OWNER/REPO --base main
```

Use `--watch` while workflows for the merge commit are active.

## Single-Maintainer Admin Merge

An admin merge can bypass only a missing independent approval. It cannot bypass:

- A failed or pending required check.
- The commit-signature gate.
- Base policy.
- An actionable comment.
- An unresolved review thread.

## Failure Rule

Do not accept a failed repository-owned check or workflow.

For a failure:

1. Inspect the check and its logs.
2. Fix the cause.
3. Run the smallest local proof.
4. Recheck the affected review and check gates.
5. Run `./scripts/pre-merge.sh --profile pr-parity`.
6. Push a signed follow-up commit from the host.
7. Confirm the new head and base-policy run.
8. Repeat review and checks.

For a merge task, follow the merged `main` workflows to success. Historical
scheduled failures for another SHA are triage inputs, not blockers for the
current SHA.

## Blocking Rule

Do not report completion while any of these conditions is true:

- The published pull request omits an intended change.
- A repository-owned check reports a failure or lacks review.
- A review surface has an actionable finding.
- A merged-`main` workflow for the current merge reports a failure.
- For release, cleanup, or a task that merges all pull requests, the readiness
  check reports a blocker.

If repeated friction exposes a durable process gap, update the repo-local
instruction that owns the process. Use a separate review unit if the update
does not match the active pull request purpose.
