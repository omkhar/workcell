# GitHub Workflow Design

Workcell keeps its GitHub automation intentionally small. The workflow surface
should reinforce the runtime boundary and release posture, not bury the project
under platform automation.

## Current workflow set

- `ci.yml`: repo validation and direct container smoke on every pull request and
  push to `main`
- `ci.yml`: also verifies each supported pinned runtime platform
  reproducibly under the same `SOURCE_DATE_EPOCH` in parallel, while keeping a
  single required `Reproducible build` status for branch protection
- `ci.yml`: caches only exact pinned QEMU and Buildx helper artifacts; it does
  not add layer caching to the reviewed runtime build path
- `ci.yml`: also verifies that the release source bundle is reproducible with a
  fixed `SOURCE_DATE_EPOCH`
- `ci.yml`: also enforces the reviewed pin policy for non-ecosystem inputs such
  as the Debian snapshot, immutable base-image digests, and exact runtime and
  validator package sets
- `ci.yml`: also verifies the pinned upstream Codex Linux assets against
  OpenAI's published Sigstore bundle
- `ci.yml`: also verifies the determinism of the release build-input manifest
- `ci.yml`: first runs host-side pin-hygiene checks for the runtime and
  validator Dockerfiles, then runs repo validation inside a pinned validator
  image so release gating does not trust mutable runner apt packages
- `docs.yml`: fast spelling and manpage feedback for documentation-only changes,
  including runtime documentation under `runtime/**`
- `security.yml`: GitHub Actions linting, dependency review on pull requests,
  and `zizmor`
- `codeql.yml`: code scanning for the shipped Rust and JavaScript surfaces on
  public repositories by default, or on private repositories when
  `WORKCELL_ENABLE_PRIVATE_CODE_SCANNING=true`
- `scorecard.yml`: weekly and default-branch OpenSSF Scorecard analysis
- `pin-hygiene.yml`: weekly re-validation of pinned non-ecosystem inputs and
  re-verification of the pinned upstream Codex release
- `macos-boundary.yml`: optional self-hosted macOS launch verification for the
  real Colima plus Virtualization.Framework path when
  `WORKCELL_ENABLE_SELF_HOSTED_MACOS=true`; it first seeds the reviewed runtime
  image in `build` mode and then verifies the managed `strict` launch path; it
  runs only on `main` pushes and manual dispatches so untrusted pull request
  code never lands on a self-hosted runner
- `release.yml`: multi-arch image publish, a directly signed build-input
  manifest, a signed builder-environment manifest, direct signing for the
  source bundle and both published SBOMs, SBOM generation, checksums,
  attestations on tagged releases, deterministic fixed-order multi-arch
  manifest assembly, preflight-to-publish digest binding for the source bundle
  and both per-platform image manifests, byte-for-byte preflight binding for
  the build-input manifest, a conditional release-preflight CodeQL rerun when
  repository settings enable private code scanning or the repository is public,
  a protected `release` environment with a human approval gate before publish
  when the repository visibility and billing plan support required reviewers,
  an explicit release-asset allowlist, exact pinned BuildKit, Cosign, and Syft
  tooling, archived-source-tree image publication instead of live-worktree
  publication, and pinned GitHub Release publication instead of an ambient `gh`
  binary
- `hosted-controls.yml`: live GitHub control-plane drift detection on `main`
  pushes, schedule, and manual dispatch, using the same hosted-controls policy
  that release preflight enforces before publish; on private repositories it
  uses an optional dedicated `WORKCELL_HOSTED_CONTROLS_TOKEN` because the
  default workflow token cannot read rulesets/collaborator/environment
  metadata for this audit
- `.github/dependabot.yml`: grouped weekly updates for GitHub Actions and the
  runtime base image, validator base image, pinned provider CLI dependency
  graph, and shipped Rust runtime crate, with cooldowns

## Source influence

The original `trailofbits/claude-code-config` repository did not ship GitHub
workflow files. The linked Trail of Bits repos that informed the posture did:

- `trailofbits/skills`: `lint` and `validate`
- `trailofbits/skills-curated`: `lint`, `validate`, and a focused security scan
- `trailofbits/dropkit`: one CI workflow

Workcell follows that smaller pattern instead of adding a wide spray of generic
automation.

## Repository-hosted controls

Workcell keeps several repository settings outside git, but they are still part
of the reviewed control plane:

- active rulesets on the default branch for signed commits and anti-rewrite
  integrity
- an active required-status-checks ruleset on the default branch for the core
  CI and GitHub workflow security checks
- an active review ruleset on the default branch that requires pull requests;
  in the current single-owner-private mode it requires zero approvals and no
  code-owner/thread gate, while multi-maintainer mode raises that back to
  human-reviewed approval plus review resolution
- an active ruleset on `refs/tags/v*` that blocks tag creation, updates, and
  deletion except for explicit repository-role bypass actors
- GitHub Actions SHA pinning required at the repository level
- a protected `release` environment whose expected protection mode is declared
  in [`policy/github-hosted-controls.toml`](../policy/github-hosted-controls.toml)
- explicit CODEOWNERS entries for high-risk paths such as
  `.github/workflows/`, `scripts/`, and the runtime boundary

The repository includes [`scripts/verify-github-hosted-controls.sh`](../scripts/verify-github-hosted-controls.sh)
to audit those hosted settings with the GitHub API.
[`hosted-controls.yml`](../.github/workflows/hosted-controls.yml) runs that
audit continuously against the live repository, and tagged releases rerun it in
release preflight before publish and refuse publication if the hosted controls
drift.

For private repositories, GitHub's default workflow token does not have enough
access to read the rulesets, direct collaborators, and protected environment
metadata that the hosted-controls audit requires. Workcell therefore uses a
dedicated `WORKCELL_HOSTED_CONTROLS_TOKEN` secret for workflow-based audits.
The continuous `hosted-controls.yml` workflow skips cleanly when that secret is
absent so `main` does not stay red on a known GitHub token limitation, but the
tagged `release.yml` preflight fails closed unless the secret is configured.
The workflow enforces that split explicitly with
`WORKCELL_HOSTED_CONTROLS_REQUIRED="0"` in
[`hosted-controls.yml`](../.github/workflows/hosted-controls.yml) and
`WORKCELL_HOSTED_CONTROLS_REQUIRED="1"` in
[`release.yml`](../.github/workflows/release.yml).
That token should be a fine-grained token or GitHub App token scoped only to
this repository and only to the repository-administration metadata needed for
the audit. Workflow jobs that can read that token run inside a dedicated
`hosted-controls-audit` environment so the secret is not exposed to unrelated
jobs.

The current repository policy uses
`branch_review.mode = "single-owner-private-pr"` and
`release_environment.mode = "single-owner-private"` because this is a private
single-owner repository. The audit enforces that as a private user-owned
repository whose only direct collaborator is the owner. In that mode, the
audited expectation is:

- `main` still requires pull requests and green required checks, but does not
  require impossible third-party approvals, code-owner review, or review-thread
  resolution
- the named `release` environment exists, but it is not treated as a
  multi-party human approval gate

If the repository later moves to a multi-maintainer or public operating model,
flip those policies back to `review-gated` and Workcell will require at least
one human reviewer and no administrator bypass before protected-branch merges
or tagged publication.

For private repositories, `codeql.yml` is opt-in through
`WORKCELL_ENABLE_PRIVATE_CODE_SCANNING=true` because GitHub code scanning is
not universally available on private repositories. The Scorecard run still
executes and uploads its artifact, but SARIF upload is opt-in through
`WORKCELL_ENABLE_PRIVATE_SCORECARD_SARIF=true`. The same pattern applies to
`zizmor` through `WORKCELL_ENABLE_PRIVATE_ZIZMOR_SARIF=true`, and to dependency
review through `WORKCELL_ENABLE_PRIVATE_DEPENDENCY_REVIEW=true`. Public
repositories upload both SARIF result sets into the GitHub Security tab by
default and run dependency review on pull requests without extra flags.

Every workflow sets `permissions: read-all` at the workflow level and then
elevates only the specific jobs that need write access, such as CodeQL SARIF
upload, Scorecard SARIF upload, or tagged release publication. Workcell also
forbids `pull_request_target` and disallows long-lived PAT-style workflow
credentials in the reviewed workflow set. Release publication uses OIDC-backed
ephemeral credentials for signing and attestations instead of long-lived cloud
keys.

## Dependency update scope

Dependabot currently covers:

- GitHub Actions
- the runtime Dockerfile base image
- the validator Dockerfile base image
- the pinned provider CLI lockfile in `runtime/container/providers`
- the vendored Rust runtime crate in `runtime/container/rust`

Non-ecosystem pinned inputs such as the Codex release bundle and Debian
snapshot are checked by CI, release preflight, and the scheduled
`pin-hygiene.yml` workflow rather than Dependabot.

Dependency diffs on pull requests are additionally screened with
`actions/dependency-review-action` using
[`../.github/dependency-review-config.yml`](../.github/dependency-review-config.yml).

## Deliberate omissions

These are omitted on purpose:

- A GitHub-hosted macOS boundary workflow. Hosted runners cannot prove the full
  Colima plus Virtualization.Framework boundary, so Workcell does not pretend
  they can.
- Mandatory self-hosted macOS gating for every repo. Workcell ships an opt-in
  `macos-boundary.yml` workflow for repos that actually have the runner and
  host prerequisites, but it does not make unverifiable promises for repos that
  do not.
- `pull_request_target`. This repo treats untrusted pull request code as
  untrusted.
- Bot churn such as stale-issue automation. It does not improve the boundary or
  developer workflow.

The small exception is `docs.yml`, which exists only to give faster spelling
and manpage feedback on documentation-only changes. It does not replace the
full validation and release gates in `ci.yml`.

## Review standard

Every workflow should pass the same review gate:

1. Does it improve contributor experience or just add ceremony?
2. Does it validate a real Workcell invariant?
3. Is the same check already covered elsewhere?
4. Are action permissions and refs minimal?
5. Does it avoid implying assurance we do not actually have?
