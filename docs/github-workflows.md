# GitHub Workflow Design

Workcell keeps the workflow set narrow and reviewable. GitHub automation should
reinforce the runtime boundary and release posture, not replace them.

## Workflow inventory

| Workflow | Purpose |
|---|---|
| `ci.yml` | repository validation, smoke, reproducibility, pin verification, and upstream release re-verification on pushes and PRs |
| `docs.yml` | fast spelling and manpage feedback for docs-only changes |
| `security.yml` | workflow lint, dependency review, and `zizmor` |
| `codeql.yml` | code scanning for shipped Rust, Python, and JavaScript surfaces |
| `scorecard.yml` | OpenSSF Scorecard analysis |
| `pin-hygiene.yml` | scheduled re-validation of pinned inputs, upstream release pins, and the 72-hour stable provider bump window |
| `hosted-controls.yml` | drift detection for GitHub-hosted controls that live outside git |
| `release.yml` | tagged release preflight, publication, signatures, SBOMs, manifests, and attestations |

## Release workflow posture

`release.yml` is intentionally strict:

- it publishes from the archived source bundle, not the live checkout
- it binds publish outputs to preflight results before signing
- it refuses to publish when provider pins lag the newest eligible stable releases in `policy/provider-bumps.toml`
- it signs release assets with keyless Sigstore/Cosign
- it publishes GitHub attestations as an additional surface, not a
  replacement, but only when the reviewed hosted controls say the repository
  visibility and plan support them
- it uses pinned GitHub Actions and pinned Buildx, QEMU, Cosign, and Syft
  versions
- it runs with minimal workflow-level permissions and only elevates the publish
  job for package publication, OIDC, attestations, and release asset upload

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

- `pull_request_target`
- ambient PAT-style workflow credentials
- GitHub-hosted macOS claims for the full Colima boundary
- stale-issue or other bot churn unrelated to the runtime or release posture
