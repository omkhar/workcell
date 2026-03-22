# GitHub Workflow Design

Workcell keeps its GitHub automation intentionally small. The workflow surface
should reinforce the runtime boundary and release posture, not bury the project
under platform automation.

## Current workflow set

- `ci.yml`: repo validation and direct container smoke on every pull request and
  push to `main`
- `security.yml`: GitHub Actions linting and `zizmor`
- `scorecard.yml`: weekly and default-branch OpenSSF Scorecard analysis
- `release.yml`: multi-arch image publish, SBOM generation, signing, checksums,
  and attestations on tagged releases
- `.github/dependabot.yml`: grouped weekly updates for GitHub Actions and the
  runtime base image, with cooldowns

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
`WORKCELL_ENABLE_PRIVATE_SCORECARD_SARIF=true`. GitHub Advanced Security is not
assumed by default.

## Dependency update scope

Dependabot currently covers:

- GitHub Actions
- the runtime Dockerfile base image

It does not yet update the pinned vendor CLI versions in
`runtime/container/Dockerfile`. Those remain an explicit review point until they
move behind a manifest that Dependabot can safely manage.

## Deliberate omissions

These are omitted on purpose:

- A docs-only workflow. Markdown and manpage checks live in `ci.yml` because a
  separate docs lane would be duplication.
- A GitHub-hosted macOS boundary workflow. Hosted runners cannot prove the full
  Colima plus Virtualization.Framework boundary, so Workcell does not pretend
  they can.
- `pull_request_target`. This repo treats untrusted pull request code as
  untrusted.
- CodeQL. Today the repository is mostly shell, config, and documentation; the
  higher-signal controls are shell linting, workflow linting, `zizmor`,
  Scorecard, and the runtime smoke checks.
- Bot churn such as stale-issue automation. It does not improve the boundary or
  developer workflow.

## Review standard

Every workflow should pass the same review gate:

1. Does it improve contributor experience or just add ceremony?
2. Does it validate a real Workcell invariant?
3. Is the same check already covered elsewhere?
4. Are action permissions and refs minimal?
5. Does it avoid implying assurance we do not actually have?
