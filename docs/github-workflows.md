# GitHub Workflow Design

Workcell keeps the workflow set narrow and reviewable. GitHub automation should
reinforce the runtime boundary and release posture, not replace them.

## Workflow inventory

| Workflow | Purpose |
|---|---|
| `ci.yml` | repository validation, smoke, reproducibility, pin verification, upstream release re-verification, and continuous package install/uninstall verification on pushes and PRs |
| `pr-base-policy.yml` | trusted base-branch guard that keeps `main` as the supported ready-PR base and leaves non-`main` PR bases as draft-only lower-assurance review units |
| `docs.yml` | fast spelling and manpage feedback for docs-only changes |
| `security.yml` | workflow lint, dependency review, and `zizmor` |
| `codeql.yml` | code scanning for shipped Rust, Go, and JavaScript surfaces |
| `scorecard.yml` | OpenSSF Scorecard analysis |
| `pin-hygiene.yml` | scheduled re-validation of pinned inputs plus upstream refresh drift across providers, Linux base images, toolchains, and release-build pins |
| `upstream-refresh.yml` | scheduled and manual signed refresh PR creation for reviewed upstream pins, followed by maintainer-approved validation from the draft PR |
| `hosted-controls.yml` | drift detection for GitHub-hosted controls that live outside git |
| `release.yml` | tagged release preflight, publication, signatures, SBOMs, manifests, and attestations |

## Release workflow posture

`release.yml` is intentionally strict:

- it verifies from GitHub-owned sources that the release install matrix still
  matches the newest two GitHub-hosted Apple Silicon macOS runner labels
- it publishes from the archived source bundle, not the live checkout
- it runs repo-mounted validator and release-helper lanes under an explicit
  caller UID/GID with isolated writable home, cache, and tmp roots rather than
  relying on ambient container-root defaults; passwd-less caller UIDs get a
  synthesized isolated home instead of collapsing to `/`
- it re-verifies upstream provider releases and every reviewed upstream pin from the archived source tree before packaging and signing
- it gates publication on release-bundle install/uninstall and Homebrew
  install/uninstall verification on GitHub-hosted Apple Silicon `macos-26`
  and `macos-15`
- it runs a release-scoped CodeQL matrix so Go uses `autobuild` while Rust and
  JavaScript keep buildless scanning
- it binds publish outputs to preflight results before signing
- it refuses to publish when provider pins, Linux base images, Linux toolchains,
  or release-build pins lag the latest tracked upstream versions
- it signs release assets with keyless Sigstore/Cosign
- it publishes GitHub attestations as an additional surface, not a
  replacement, but only when the reviewed hosted controls say the repository
  visibility and plan support them
- it uses pinned GitHub Actions and pinned Buildx, QEMU, Cosign, and Syft
  versions
- it runs with minimal workflow-level permissions and only elevates the publish
  job for package publication, OIDC, attestations, and release asset upload

That is also the current tested host-support limit for CI and tagged releases.
Other macOS versions are not install-gated today.

## Upstream refresh workflow

`upstream-refresh.yml` is the dedicated refresh lane for reviewed upstream pins:

- it runs on a weekday schedule and on manual dispatch
- it refreshes provider pins, the Linux runtime and validator base images,
  Debian snapshot, Go/Rust/Hadolint toolchains, and release-build helper pins
- it requires a maintainer-owned GitHub-verified signing key through
  `WORKCELL_UPSTREAM_REFRESH_GPG_PRIVATE_KEY` and
  `WORKCELL_UPSTREAM_REFRESH_GPG_KEY_ID`, plus matching
  `WORKCELL_UPSTREAM_REFRESH_GIT_NAME` and
  `WORKCELL_UPSTREAM_REFRESH_GIT_EMAIL` repository variables
- it opens a draft PR instead of mutating `main`
- it leaves expensive validation behind the same maintainer approval gate used
  for other public-repo PRs instead of auto-dispatching workflows during
  publication

`ci.yml` and `docs.yml` use the same explicit nonroot validator contract when
they bind-mount the repository: the workflow computes the caller UID/GID,
passes isolated writable roots, and creates those paths inside the validator
before repo validation or docs checks run. That contract still holds when the
caller UID lacks a passwd entry inside the image because the launcher
synthesizes an isolated writable home for those lanes.

## Hosted controls

Some release-relevant controls live outside git. Workcell still treats them as
reviewed inputs:

- branch rulesets for signed commits, anti-rewrite protection, PR gating, and
  required checks
- tag rulesets for `refs/tags/v*`
- the `release` environment, with reviewer protection when the GitHub plan
  supports it and an explicitly documented lower-assurance fallback when it
  does not
- GitHub Actions SHA pinning
- canonical repository variables such as
  `WORKCELL_ENABLE_GITHUB_ATTESTATIONS=true` and
  `WORKCELL_ENABLE_PRIVATE_GITHUB_ATTESTATIONS=false`

`scripts/verify-github-hosted-controls.sh` audits those settings against
`policy/github-hosted-controls.toml`.

## Public vs private behavior

The workflow set supports both public and private repos, but some features are
conditional:

- private repos may need `WORKCELL_HOSTED_CONTROLS_TOKEN` for hosted-control
  auditing
- private code scanning and some SARIF uploads are opt-in because GitHub plan
  support varies
- public repos can publish GitHub attestations when enabled
- private/internal repos only publish GitHub attestations when the reviewed
  `WORKCELL_ENABLE_PRIVATE_GITHUB_ATTESTATIONS` variable is explicitly set for
  a GitHub plan that supports them

## Deliberate omissions

Workcell does not use:

- general-purpose `pull_request_target` workflows; the only exception is
  `pr-base-policy.yml`, which reads pull-request metadata from trusted
  base-branch workflow code, keeps top-level `permissions: {}`, and must not
  check out repository contents or use external actions
- ambient PAT-style workflow credentials
- GitHub-hosted macOS claims for the full Colima boundary
- stale-issue or other bot churn unrelated to the runtime or release posture
