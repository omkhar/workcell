# GitHub Workflow Design

Workcell keeps the workflow set narrow and reviewable. GitHub automation should
reinforce the runtime boundary and release posture, not replace them.

Workcell also keeps a machine-checked lane inventory so local parity and
GitHub-only behavior do not drift silently:

- [`policy/workflow-lane-policy.json`](../policy/workflow-lane-policy.json)
  declares each expanded workflow lane's profiles, authority, and local mode
- [`policy/workflow-lanes.json`](../policy/workflow-lanes.json) is the
  generated manifest derived from the live workflow YAML plus that policy
- `./scripts/verify-workflow-lanes.sh` fails if the manifest drifts
- `./scripts/ci-plan.sh` explains which mirrored lanes apply locally for a
  given profile, event, labels, and changed files

That inventory underpins the local `./scripts/pre-merge.sh` profiles and the
repo-local `./scripts/repo-publish-pr.sh` publication gate.

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
| `upstream-refresh.yml` | scheduled and manual candidate generation for reviewed upstream pins, with later signed host-side PR publication |
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

`upstream-refresh.yml` is the dedicated candidate lane for reviewed upstream pins:

- it runs on a weekday schedule and on manual dispatch
- it refreshes provider pins, the Linux runtime and validator base images,
  Debian snapshot, Go/Rust/Hadolint toolchains, and release-build helper pins
- it binds to the empty `upstream-refresh` GitHub environment as an out-of-tree
  `main`-only gate with no hosted signing inputs
- it uploads an advisory candidate bundle containing `patch`, `diffstat`, and
  `metadata.json`
- it opens or updates one rolling tracking issue instead of pushing a branch or
  opening a PR from GitHub Actions
- the authoritative refresh PR is created later on the host through
  `./scripts/publish-upstream-refresh-pr.sh`, which recreates the refresh
  locally, requires exact candidate identity matches when a candidate run is
  cited, runs `pr-parity`, and then delegates to
  `./scripts/repo-publish-pr.sh`
- the candidate artifact and tracking issue are operator signals only; they are
  not integrity evidence and do not replace the later signed host-side review
  unit

`ci.yml` and `docs.yml` use the same explicit nonroot validator contract when
they bind-mount the repository: the workflow computes the caller UID/GID,
passes isolated writable roots, and creates those paths inside the validator
before repo validation or docs checks run. That contract still holds when the
caller UID lacks a passwd entry inside the image because the launcher
synthesizes an isolated writable home for those lanes.

The mirrored local workflow bodies live under `scripts/ci/`:

- `job-pr-shape.sh`
- `job-validate.sh`
- `job-docs.sh`
- `job-pin-hygiene.sh`
- `build-validator-image.sh`
- `run-validate-in-validator.sh`
- `run-docs-in-validator.sh`

GitHub workflow YAML stays responsible for event routing, permissions, runners,
and hosted-only concerns. The shared scripts keep the mirrorable job logic
identical between local parity runs and GitHub CI.

## Hosted controls

Some release-relevant controls live outside git. Workcell still treats them as
reviewed inputs:

- branch rulesets for signed commits, anti-rewrite protection, PR gating, and
  required checks
- tag rulesets for `refs/tags/v*`
- the `release` environment, with reviewer protection when the GitHub plan
  supports it and an explicitly documented lower-assurance fallback when it
  does not
- the `hosted-controls-audit` and `upstream-refresh` environments, including
  their exact secret and variable contents plus their disabled admin bypass
  posture
- `hosted-controls-audit` permits the `main` branch and protected `v*` release
  tags so scheduled/main audits and tag-triggered release preflight both use a
  dedicated environment gate for `WORKCELL_HOSTED_CONTROLS_TOKEN`
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
