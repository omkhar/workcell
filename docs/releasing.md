# Releasing Workcell

This runbook defines the repeatable process for cutting a new Workcell release.

## Current assurance model

Workcell currently operates in single-maintainer release mode.

That means:

- one maintainer may open, merge, tag, approve the release environment, and
  verify publication
- asynchronous review from humans and configured async reviewers is still
  expected and must be swept before merge
- asynchronous review is advisory input, not equivalent to an independent
  human approval
- signed history, strict CI, reproducibility checks, provenance, SBOMs,
  attestations, immutable releases, and public review artifacts are the primary
  compensating controls

This is lower assurance than true separation of duties and should be described
honestly in docs, status reports, and release commentary.

## Principles

- Treat a release request as implicitly including peer review unless the
  maintainer explicitly narrows that scope.
- For release work, peer review means continuing through review findings,
  validation failures, documentation drift, and hosted workflow failures until
  no actionable findings remain or a concrete blocker is reported.
- Review and finish open pull requests before cutting a release.
- Address actionable PR comments and review feedback as part of release work.
- Use signed commits and signed tags.
- Before tagging a release, make sure shipped features are documented and do
  not remain on the roadmap. Remove roadmap items only after the code and
  focused validation confirm they are fully implemented.
- Before tagging a release, sweep outstanding security findings and verify each
  claimed fix with a one-off proof-of-closure command or repro, even when the
  repo already has permanent regression tests.
- Before tagging a release, verify that release-facing documentation examples
  are still covered by existing tests or scenario lanes.
- Before publishing or merging a release PR, verify that
  `policy/operator-contract.toml`, `policy/requirements.toml`, `workcell --help`,
  `man/workcell.1`, and any curated `README.md` workflow claims still agree.
- When a release changes a user-visible workflow, run the repo-local
  `workcell-contract-parity` skill sweep and treat any parity failure as a
  release blocker.
- Review any intentional upstream holdbacks or exceptions before refreshing
  pins, and document them in policy or release notes rather than carrying
  unexplained drift.
- Publish PRs from the host with `./scripts/workcell publish-pr`.
- For agentic release PR publication and follow-up, use the repo-local
  `workcell-pr-lifecycle` skill in addition to this release runbook.
- Wait for `main` to be green before pushing the release tag.
- Follow the tag-triggered `Release` workflow through completion.
- Approve the `release` environment only after release preflight and install
  verification are green.
- Verify that the repository-level immutable-release control is enabled before
  pushing a release tag.
- Publish release assets through a draft GitHub release first, then publish
  the final release record only after the asset set is complete. The release
  workflow handles that draft creation and final publication automatically; the
  operator verifies the finished published release state.
- Verify the published GitHub release, attached assets, and immutable-release
  state before concluding.
- Do not rewrite or delete a failed release tag. Recover by patching `main` and
  cutting the next patch release.
- In single-maintainer mode, leave an explicit public release-PR comment before
  merge that records the version, exact head SHA, timestamp, and the exact
  single-maintainer path used, including maintainer self-review for the
  `release` environment and any explicit branch-protection bypass actually used
  for the PR merge because no second approver was configured.
- If the repository-level immutable-release control was disabled, enable it
  before tagging. A release that was already published mutable cannot be made
  immutable in place; fix the control, patch `main`, and cut the next patch
  release instead.

## Inputs

Set these values before starting:

```sh
export REPO="omkhar/workcell"
export VERSION="vX.Y.Z"
export RELEASE_BRANCH="codex/release-${VERSION}"
export RELEASE_TITLE="Release ${VERSION}"
```

If a previously pushed release tag already failed, do not reuse it. Bump the
patch version instead.

## Release mode

Use one of these modes explicitly:

- `review-gated`: default for interactive release work with the maintainer in
  the thread. Stop before each irreversible release action, present a short
  review packet, and wait for maintainer feedback or approval.
- `autonomous`: only when the maintainer explicitly asks for an end-to-end
  release run without pauses between release gates.

If the operator has not clearly opted into `autonomous`, use `review-gated`.

## Maintainer checkpoints

In `review-gated` mode, stop before each of these actions:

1. publishing the release PR
2. marking the release PR ready or merging it
3. pushing the signed release tag
4. approving the `release` environment
5. declaring the release complete and cleaning up lingering release branches or
   temporary workspaces

Each checkpoint packet should stay short and include:

- the exact next action
- the exact branch, PR, tag, or commit SHA involved
- current CI and comment-sweep state
- documentation and roadmap status
- any open risks, tradeoffs, or deviations from the normal path

Release-path pull requests should stay human-reviewable. Split opportunistic
cleanup, unrelated fixes, or separate reviewer-sized concerns into other PRs
before publishing the release path.

If the maintainer gives feedback, incorporate it and refresh the packet before
continuing.

## PR comment sweep

Every PR involved in the release path must go through a comment sweep.

Configured async reviewer identities live in
`policy/reviewer-identities.toml`.

A PR is not ready to merge until all of the following are true:

- top-level PR comments have been reviewed
- inline review comments and review threads have been reviewed
- unresolved review threads are resolved or explicitly closed with rationale
- actionable comments from human reviewers and configured async reviewers are
  fixed or answered
- the comment sweep has been repeated after CI turned green
- the comment sweep has been repeated immediately before merge

If no async reviewer identities are configured, still sweep all PR comments,
review comments, and unresolved review threads before merge.

The required sweep points are:

1. after the PR is published
2. after required CI turns green
3. immediately before merge

Useful commands:

```sh
gh pr view <pr-number> --repo "${REPO}" --comments
gh pr checks <pr-number> --repo "${REPO}"
```

Use the GitHub API or GraphQL as needed to inspect unresolved review threads.

## Documentation review gate

Every release branch must also go through an explicit documentation review.

A release is not ready to merge unless all of the following are true:

- `CHANGELOG.md` accurately summarizes the release contents and date
- `README.md` and `docs/getting-started.md` still describe the current support
  boundary, install path, and tested release-install matrix honestly
- provider and rollout docs that affect the release, such as
  `docs/injection-policy.md`, `docs/provider-matrix.md`, and relevant
  quickstarts or setup guides, match the current implementation and auth
  maturity
- `policy/operator-contract.toml` still points each public workflow at current
  docs and automated evidence, and compatibility aliases still have working
  alias probes
- `ROADMAP.md` and nearby planning or design docs do not describe shipped work
  as future work and do not remove partially shipped work from the roadmap
- release-sensitive runbooks such as `docs/releasing.md`,
  `docs/provenance.md`, and `docs/github-workflows.md` still describe the
  current release process accurately
- release-facing documentation claims are backed by code, CI, or focused manual
  validation rather than assumption

The required documentation review points are:

1. while preparing the release branch
2. again after the release PR checks turn green
3. immediately before merge if the release diff changed after the second review

## 1. Start from a clean `main` worktree

Use a dedicated release worktree or an otherwise clean checkout rooted at
`main`.

```sh
git fetch origin --tags
git checkout main
git pull --ff-only origin main
```

Confirm the working tree is clean before making release changes.

## 2. Review open pull requests first

List open PRs:

```sh
gh pr list --repo "${REPO}" --state open
```

For each open PR that is part of the release path:

1. inspect the PR, changed files, checks, reviews, and comments
2. perform the PR comment sweep
3. address actionable review feedback and comment threads
4. re-run or fix CI until the PR is green
5. perform the PR comment sweep again after CI is green
6. merge only after the final pre-merge comment sweep succeeds

Useful commands:

```sh
gh pr view <pr-number> --repo "${REPO}" --comments --web
gh pr checks <pr-number> --repo "${REPO}" --watch
gh pr diff <pr-number> --repo "${REPO}"
```

## 3. Confirm the next version

Check the latest existing tags before choosing the next release:

```sh
git tag --sort=-v:refname | head
```

Rules:

- normal case: cut the next patch release
- recovery case: if a release tag already exists but its release workflow
  failed, do not reuse that tag; patch `main` and cut the next patch release

## 4. Prepare the release branch

Create the release branch from up-to-date `main`:

```sh
git checkout -b "${RELEASE_BRANCH}"
```

Update the changelog for the new version and date.

Before opening the release PR, sweep the roadmap and nearby planning docs:

- remove features from `ROADMAP.md` only when the current code and tests prove
  they are shipped
- keep any partially implemented work on the roadmap
- update user or design docs when shipped features would otherwise still appear
  as future work

Before opening the release PR, complete the release-readiness sweeps that tend
to rot between releases:

- review any open external or offline security finding queue for the repo
- run a one-off proof-of-closure for each security finding being marked fixed
- verify release-facing documentation examples still map to requirements,
  tests, or scenario coverage
- review `policy/provider-bumps.toml` and any temporary holdbacks before
  refreshing upstream pins

Useful commands:

```sh
./scripts/verify-requirements-coverage.sh
./scripts/run-scenario-tests.sh --repo-required
./scripts/verify-scenario-coverage.sh
```

When a release touches the local runtime boundary or launch path, also run the
local certification lane on a machine that has the live runtime prerequisites:

```sh
./scripts/run-scenario-tests.sh --secretless-only --certification-only
```

Perform the release documentation review on the exact branch diff:

```sh
git diff --stat main...HEAD -- CHANGELOG.md README.md ROADMAP.md docs
git diff main...HEAD -- CHANGELOG.md README.md ROADMAP.md docs
```

At minimum, review:

- the changelog entry for the release version and date
- `README.md`, `docs/getting-started.md`, and changed quickstarts or setup docs
- `ROADMAP.md` plus any changed planning or system-design docs
- changed rollout, auth-maturity, provenance, workflow, or release-runbook docs
- `policy/operator-contract.toml` and `policy/requirements.toml`
- any doc statement about supported hosts, CI coverage, session surfaces, auth
  maturity, or release posture that could overstate what the current code and
  validation actually prove
Before opening the release PR, verify release inputs that commonly drift:

```sh
./scripts/update-upstream-pins.sh --check
```

If the check fails because reviewed upstream pins or Debian snapshots drifted,
refresh them:

```sh
./scripts/update-upstream-pins.sh --apply
```

Then update the changelog entry to describe the refresh and continue with the
new patch version if needed.

Run the focused validation needed to justify roadmap and documentation updates
before committing:

```sh
./scripts/verify-operator-contract.sh
./tests/scenarios/shared/test-auth-commands.sh
./tests/scenarios/shared/test-auth-status.sh
./tests/scenarios/shared/test-session-commands.sh
```

Then run basic validation before committing:

```sh
git diff --check
```

Before committing, run a short live sanity sweep for interactive flows that CI
does not exercise well enough:

- from a clean throwaway checkout or other clean scratch workspace, launch an
  attached interactive session and resize the terminal window
- from a clean throwaway checkout or other clean scratch workspace, start,
  attach to, send to, stop, and delete a detached session
- confirm session cleanup removes the expected runtime artifacts only

Create a signed release commit:

```sh
git add -A
git commit -S -m "release: ${VERSION}"
```

## 5. Publish the release PR from the host

Prepare and publish the release PR with the host-side helper:

```sh
./scripts/workcell publish-pr
```

In `review-gated` mode, stop with the first checkpoint packet before running
the publish step.

As soon as the PR exists, perform the first PR comment sweep.

Before merge in the current single-maintainer path, leave a public PR comment
using the exact reviewed release head SHA. A literal template is below so the
public artifact stays consistent from release to release:

```text
Single-maintainer release approval for vX.Y.Z on <timestamp>.
Reviewed head SHA: <release-pr-head-sha>
Path used: signed commits, required CI, comment sweeps after publish/green/pre-merge,
and single-maintainer merge because no second approver was configured.
PR merge path: <normal merge | explicit admin or branch-protection bypass, with reason>
If the release workflow later waits on the release environment, I will record
that self-review publicly with the workflow run URL and timestamp.
```

## 6. Follow release PR checks and comments until green

Stay on the release PR until all required checks succeed and all actionable
comments are addressed.

Useful commands:

```sh
gh pr checks <pr-number> --repo "${REPO}" --watch
gh pr view <pr-number> --repo "${REPO}" --comments
```

If CI fails:

1. inspect the failing workflow or job logs
2. fix the issue on the same release branch
3. commit with a signed follow-up commit
4. push and re-run checks
5. repeat until green

When required checks turn green, perform the second PR comment sweep.

Then repeat the documentation review on the exact release PR diff before
deciding the branch is ready to merge.

In `review-gated` mode, stop with the second checkpoint packet before marking
the release PR ready or merging it.

Do not merge while required checks are failing or while comment sweeps are
incomplete.

## 7. Merge the release PR to `main`

In the current single-maintainer operating model, merge after all of the
following are true:

- required checks are green
- the PR comment sweep has been completed after CI turned green
- the final pre-merge comment sweep has been completed
- the release documentation review has been completed after CI turned green
- release-facing docs accurately describe the exact merge diff
- changelog and release notes are correct
- the public single-maintainer release comment records the version, exact head
  SHA, timestamp, and the exact single-maintainer path used, including any
  release-environment self-review and any explicit PR-merge bypass when
  applicable

After merge, record the resulting `main` commit SHA. This is the commit that
will be tagged.

## 8. Wait for post-merge `main` CI

Do not tag immediately after merging. First wait for the workflows on the
merged `main` commit to finish green.

Example:

```sh
gh run list --repo "${REPO}" --commit <main-commit-sha>
```

Proceed only when every required workflow on the merge commit has completed
successfully.

Refresh the local repository so the merged `main` commit is present locally
before tagging:

```sh
git fetch origin main
git checkout main
git pull --ff-only origin main
```

Before pushing the tag, verify the repository-level immutable-release control:

```sh
gh api repos/"${REPO}"/immutable-releases
```

If it reports `"enabled": false`, enable it before tagging:

```sh
gh api -X PUT repos/"${REPO}"/immutable-releases
```

## 9. Create and push the signed tag

In `review-gated` mode, stop with the third checkpoint packet before pushing
the signed tag.

Create a signed tag on the merged `main` commit:

```sh
git tag -s "${VERSION}" -m "${VERSION}" <main-commit-sha>
git tag -v "${VERSION}"
git push origin "refs/tags/${VERSION}"
```

Never move or rewrite an existing release tag.

## 10. Follow the tag-triggered `Release` workflow

Watch the `Release` workflow for the tagged commit until it completes:

```sh
gh run list --repo "${REPO}" --workflow Release --limit 10
gh run watch <release-run-id> --repo "${REPO}"
```

If the workflow enters a waiting state for the `release` environment:

1. verify that preflight and install verification jobs are green
2. approve the environment in the standard single-maintainer path
3. continue watching until publication finishes

In `review-gated` mode, stop with the fourth checkpoint packet before approving
the environment.

In immutable-release mode, the release publisher must create or reuse a draft
release, upload the full artifact set into that draft, and only then publish
the final release record. If publication instead tries to upload assets into an
already-published immutable release, treat that as a release-process bug,
patch `main`, and cut the next patch release rather than rewriting the failed
tag.

After approving the environment in single-maintainer mode, leave a public PR
follow-up comment so the self-review is visible in the same release thread. Use
this template:

```text
Single-maintainer release-environment self-review recorded for vX.Y.Z.
Workflow run: <actions-run-url>
Approved at: <timestamp>
Reason: release preflight and install verification were green, and no second
release approver was configured for this repository.
```

If the release workflow fails:

1. inspect the failing job and step
2. determine whether the issue can be fixed on `main`
3. patch `main` through a normal PR
4. cut the next patch release
5. do not rewrite or delete the failed tag

## 11. Verify the GitHub release

Confirm the GitHub release exists and assets are uploaded:

```sh
gh release view "${VERSION}" --repo "${REPO}" \
  --json name,tagName,isDraft,isPrerelease,isImmutable,url,assets
```

At minimum, verify:

- the release record exists
- the release is not a draft
- the release is not a prerelease unless intentionally marked that way
- the release is immutable
- expected assets are present, including the release tarball, Homebrew formula,
  checksums, signed metadata, manifests, and SBOMs

If `isImmutable` is false, treat that as a hosted-control regression to fix.

## 12. Final closeout

In `review-gated` mode, stop with the fifth checkpoint packet before declaring
the release complete.

At the end of the release, confirm all of the following:

- no open PRs remain that should have been part of the release
- actionable PR comments were addressed or dispositioned
- external or offline security findings for the release scope were reviewed,
  and any claimed closures were validated with one-off PoCs
- release-facing docs and examples match the shipped behavior
- `main` is green
- the signed release tag exists on GitHub
- the `Release` workflow completed successfully
- the GitHub release exists with uploaded assets
- the GitHub release is immutable
- stale remote release-path branches were reviewed for overlap and either merged
  intentionally or deleted as superseded work
- temporary release worktrees or clones were removed if the requested outcome
  was clean local state

## Recovery notes

### Failed release tag already exists

If a release tag was already pushed and its release workflow failed:

- do not delete the tag
- do not force-move the tag
- patch `main`
- cut the next patch release instead

### Upstream drift during release

A common release-preflight failure is drift in reviewed upstream pins or the
Debian snapshot. Check with:

```sh
./scripts/update-upstream-pins.sh --check
```

If drift is reported, apply the refresh:

```sh
./scripts/update-upstream-pins.sh --apply
```

Then update the changelog, merge the fix to `main`, and cut the next patch
release rather than reusing the failed tag.

### Published release is mutable

If the release workflow succeeded but `gh release view` reports
`"isImmutable": false`:

- do not rewrite, delete, or recreate the published release
- verify the repository-level immutable-release control with
  `gh api repos/"${REPO}"/immutable-releases`
- enable it with `gh api -X PUT repos/"${REPO}"/immutable-releases` if needed
- patch `main` so the release publisher uses a draft-first upload flow before
  the immutable release record is published
- patch `main` so the docs and changelog describe the gap honestly
- cut the next patch release under the enabled immutable-release control
