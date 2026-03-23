# GitHub Workflow Design

Workcell keeps its GitHub automation intentionally small. The workflow surface
should reinforce the runtime boundary and release posture, not bury the project
under platform automation.

## Current workflow set

- `ci.yml`: repo validation and direct container smoke on every pull request and
  push to `main`
- `ci.yml`: also verifies that two OCI exports of each supported pinned runtime
  platform are byte-for-byte identical when built with the same
  `SOURCE_DATE_EPOCH`
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
- `codeql.yml`: code scanning for the shipped Rust and JavaScript surfaces
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
  the build-input manifest, a release-preflight CodeQL rerun, an
  explicit release-asset allowlist, exact pinned BuildKit, Cosign, and Syft
  tooling, archived-source-tree image publication instead of live-worktree
  publication, and pinned GitHub Release publication instead of an ambient `gh`
  binary
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

For private repositories, the Scorecard run still executes and uploads its
artifact, but SARIF upload is opt-in through
`WORKCELL_ENABLE_PRIVATE_SCORECARD_SARIF=true`. The same pattern applies to
`zizmor` through `WORKCELL_ENABLE_PRIVATE_ZIZMOR_SARIF=true`. Public
repositories upload both SARIF result sets into the GitHub Security tab by
default.

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
