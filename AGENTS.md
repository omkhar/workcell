# Repository Working Agreement

This repository exists to provide a bounded local runtime with explicit
isolation and policy controls for coding agents by combining the Workcell
runtime boundary with provider-specific adapters.

## Priorities

1. Developer experience
2. Simplicity
3. Security invariants
4. Performance
5. Idiomatic correctness

These priorities apply only within the defined invariant set. Do not trade away
the runtime boundary or explicit security guarantees in the name of convenience.

## Peer review default

- Treat every user request as implicitly asking for peer review unless the user
  explicitly narrows or waives that expectation.
- Peer review means continuing through review, fixes, revalidation, and another
  review pass until no actionable findings remain or a concrete blocker is
  reported.
- Treat peer review as an open-ended loop, not a single extra pass. If a peer
  or review surface returns new findings after a fix, keep iterating with that
  peer or surface until every finding is addressed, explicitly dispositioned,
  or blocked by a concrete external constraint.
- Apply that default across repo-local skills, documentation work, CI follow-up,
  publication, merge, and release actions. Do not stop at "implemented" if
  review, checks, or hosted workflows still expose actionable problems.

## Mandatory rules

- Sign every commit. Do not create or rewrite commits in this repository
  without a verified signature from the maintainer identity.
- Treat final GitHub publication as a host-side action. Prepare branch,
  signed commit message, and PR metadata inside Workcell, then use
  `workcell publish-pr` on the host rather than publishing directly from the
  Tier 1 in-container session.
- Do not treat provider config, prompt files, or rules as the sole security
  boundary.
- Preserve the dedicated VM plus container boundary as the Tier 1 design for
  all supported CLI adapters.
- Prefer explicit, auditable configuration over hidden magic.
- Mark lower-assurance modes clearly instead of overstating guarantees.
- Keep host mounts minimal. Never mount `$HOME`, host keychains, or host
  credential stores.
- Never pass through host sockets or auth state including `docker.sock`,
  `ssh-agent`, GPG agent sockets, launchd sockets, host `~/.codex`, or git
  credential-helper state.
- Keep `breakglass` paths explicit, narrow, and separately documented.
- Require explicit operator acknowledgement for `breakglass` or equivalent
  higher-trust paths.
- Treat non-git workspaces and arbitrary container commands as opt-in debugging
  paths, not the default developer flow.
- Mask repo-local provider control files and mutable git hook/config paths on
  the safe path so workspace content cannot silently take over the control
  plane.
- Ship invariant checks with new controls whenever practical.

## Pull request workflow

- For publish, PR follow-up, or merge requests in this repository, use the
  repo-local `workcell-pr-lifecycle` skill. Repo-local publication rules
  override generic GitHub publication skills.
- For `main`-based PR publication in this repository, use the host-side
  repo-local wrapper `./scripts/repo-publish-pr.sh`. It requires fresh local
  `pr-parity` evidence before delegating to the lower-level
  `./scripts/workcell publish-pr` helper.
- `main` is the only supported PR base by default. Non-`main` base PRs are
  lower-assurance exceptions: keep them draft, do not merge them, and do not
  expect the normal `main`-based repo-owned validation or merge gating for
  that branch shape.
- Every PR should remain open for comments and review before merge.
- Every PR must be checked for:
  - top-level PR comments
  - inline review comments
  - unresolved review threads
  - asynchronous review comments from configured async reviewers listed in
    `policy/reviewer-identities.toml`
- Actionable comments must be addressed or explicitly dispositioned before
  merge.
- Re-check comments and review threads after CI turns green and immediately
  before merge.
- Treat newly surfaced review findings the same way as pre-merge findings:
  address them, rerun the relevant validation, and re-review until the PR has
  no actionable findings left.
- Do not treat failing tests, checks, or repo-owned workflows as acceptable.
  If a repo-owned validation lane fails, keep working until it is fixed or the
  guarantee is explicitly removed or demoted in the same change.
- When the task includes merging a PR, follow the merged `main` workflows to a
  finished state and treat any repo-owned failure as actionable work, not as an
  acceptable post-merge residue.
- Async reviewer feedback is advisory input, not a substitute for an
  independent human approval.

## Release workflow

- For release requests, follow `docs/releasing.md`.
- Workcell currently operates in single-maintainer release mode. Do not claim
  independent approval or separation of duties unless it actually happened.
- Review open pull requests, review feedback, and PR comments before cutting a
  release, and address actionable feedback as part of the release workflow.
- Use host-side `./scripts/repo-publish-pr.sh` for release PR publication
  after fresh local `pr-parity` evidence exists.
- Wait for the merged `main` commit to finish required GitHub Actions workflows
  successfully before pushing the signed release tag.
- After pushing a release tag, follow the `Release` workflow to completion and
  verify the GitHub release exists with uploaded assets.
- In the current single-maintainer operating mode, approving the `release`
  environment is part of finishing the release when the release workflow is
  otherwise green.
- If a release tag already exists and its release workflow failed, do not
  rewrite or delete the tag. Patch `main` and cut the next patch release
  instead.
- Prefer immutable GitHub releases and treat mutable release state as a hosted
  control gap to close.

## Change discipline

- Root files define shared contracts; keep them concise.
- `runtime/`, `policy/`, `adapters/`, `verify/`, and `workflows/` should evolve
  in lockstep.
- If a security control depends on a specific runtime assumption, document that
  assumption in the same change.
- Keep one shared boundary and many thin adapters. Do not hide provider
  differences behind a fake universal abstraction.
- Prefer small scripts and plain configuration over framework-heavy machinery.
