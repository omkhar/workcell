# CI/CD Threat Model

This document is the threat model for Workcell's GitHub Actions CI/CD pipeline:
what the pipeline protects, who could attack it, which controls exist today, and
where the honest gaps are. It complements the runtime-boundary
[threat model](threat-model.md) (which covers the local Colima/container
sandbox) and the [provenance and signing](provenance.md) contract (which covers
what a consumer can verify). It does not restate those; it focuses on the build,
release, and secret-handling surface of the workflows themselves.

Every claim below is grounded in the workflows under `.github/workflows/`, the
reviewed hosted-control policy in
[`policy/github-hosted-controls.toml`](../policy/github-hosted-controls.toml),
and the pin policies in
[`policy/tool-pins.toml`](../policy/tool-pins.toml) and
[`policy/allowed-actions.toml`](../policy/allowed-actions.toml). Where a control
is absent, this document says so rather than implying protection that does not
exist.

## Scope and assets

The CI/CD pipeline protects:

- **Released-artifact integrity and provenance** — the runtime image published
  to GHCR, the source bundle, the Homebrew formula, `SHA256SUMS`, the
  deterministic build-input / control-plane / builder-environment manifests, and
  both SPDX SBOMs, plus their Sigstore bundles and GitHub attestations.
- **The chain of trust that lets a consumer verify those artifacts** — the
  keyless Sigstore identity bound to the release workflow, the GitHub attestation
  signer identity, and the maintainer's git commit/tag signing key.
- **The repository supply chain** — the pinned action set, the pinned toolchain
  (cosign, syft, buildx, buildkit, QEMU, actionlint, zizmor), and the reviewed
  hosted controls (branch/tag rulesets, required checks, environments) that keep
  a malicious change from silently reaching a release.

Out of scope for this document (covered elsewhere or by design):

- the local macOS runtime boundary and the container sandbox — see the runtime
  [threat model](threat-model.md) and [injection policy](injection-policy.md);
- the correctness of the shipped product itself (memory safety, exec-guard
  soundness) — see the [unsafe-code audit checklist](unsafe-code-audit-checklist.md);
- social-engineering and physical-access attacks on the maintainer — out of
  scope per [`SECURITY.md`](../SECURITY.md);
- compromise of GitHub itself (the platform, Actions control plane, GHCR, or
  Sigstore's public-good instances) as a trusted root. The pipeline treats these
  as trusted infrastructure; a full compromise of GitHub's OIDC issuer or
  Sigstore's Fulcio/Rekor is a residual risk, not a modeled mitigation.

## Trust boundaries and runner tiers

### Runner tiers

**Every workflow job runs on a GitHub-hosted runner.** The only labels in use
are `ubuntu-latest`, `ubuntu-24.04-arm`, `macos-26`, and `macos-15`. There are
**no self-hosted runners** anywhere in the tree, so there is no
persistent-runner tenancy boundary to defend: each job runs on a single-use
ephemeral VM that GitHub tears down afterward. The release path in particular
uses no self-hosted runners, which is what lets the SLSA Build-L2 claim in
[provenance.md](provenance.md) hold.

This is a current-state property enforced indirectly rather than by a single
"reject self-hosted" gate:

- `scripts/verify-github-macos-release-test-runners.sh` confirms the macOS
  labels match GitHub's authoritative `runner-images` list, so the release
  matrix cannot silently target a stale or fake label;
- the exact `runs-on:` labels for `ci.yml` and `release.yml` are pinned in
  `internal/metadatautil/pinnedinputs.go`, so a PR cannot swap a hosted label
  for a `self-hosted` one without a reviewed diff to that pin set.

There is no policy rule that outright rejects the string `self-hosted`; the
guarantee rests on label pinning plus review. This is called out again under
[Known gaps](#known-gaps-and-future-work).

### Privilege boundary within the workflows

All 13 workflows share a hardened baseline:

- **`permissions: {}` at the top level** — every workflow starts deny-all and
  grants the narrowest per-job scope it needs. This is codified as
  `default_workflow_token_permissions = "read"` in
  [`policy/github-hosted-controls.toml`](../policy/github-hosted-controls.toml).
- **`persist-credentials: false` on every `actions/checkout`** — no workflow
  leaves the job's git credential on the runner filesystem.
- **Every action is pinned to a full 40-character commit SHA** and its
  `owner/repo` must appear in the reviewed allowlist
  [`policy/allowed-actions.toml`](../policy/allowed-actions.toml). SHA pinning
  fixes the code that runs; the allowlist restricts which publishers may run at
  all. Both are enforced by `scripts/check-pinned-inputs.sh`. Tools fetched by
  URL instead of as actions (actionlint, zizmor) are downloaded and then
  `sha256sum -c`-verified against the digests in
  [`policy/tool-pins.toml`](../policy/tool-pins.toml).

Only the release path escalates. The highest-privilege job is the `release`
(publish) job in [`release.yml`](../.github/workflows/release.yml), which holds
`id-token: write`, `attestations: write`, `contents: write`, and
`packages: write` at once (OIDC signing, GitHub release creation, and GHCR
push). That job — and the native arm64 image build/push job — run only inside
the `release` deployment environment, which requires human approval and
disallows admin bypass (see `[release_environment]` in the hosted-control
policy). A handful of scanning jobs hold `security-events: write` for SARIF
upload, and `scorecard.yml` holds `id-token: write` for Scorecard provenance;
these are the expected minimum for those tools.

### Untrusted-fork boundary

The pull-request lanes (`ci.yml`, `security.yml`, `codeql.yml`, `mutation.yml`,
`docs.yml`) run on the plain `pull_request` trigger. That means fork PR code
executes with a **read-only** `GITHUB_TOKEN` and no access to environment-scoped
secrets, so a malicious fork PR cannot exfiltrate the one stored secret or push
anything. Heavy lanes (CodeQL, mutation, install verification) are additionally
gated behind an `approved-heavy-ci` label so a fork cannot spend expensive
compute or reach conditional code paths without maintainer opt-in.

**The only `pull_request_target` workflow is
[`pr-base-policy.yml`](../.github/workflows/pr-base-policy.yml)**, and it is the
hardened form of that pattern: it keeps `permissions: {}`, performs **no
checkout**, uses **no external actions**, and reads only
`github.event.pull_request.base.ref` / `.draft` from the trusted event context
to enforce that ready PRs target `main`. Because it never checks out or executes
untrusted PR code and holds no token or secret, it carries no exfiltration risk.
It uses `pull_request_target` deliberately: a plain `pull_request` guard could be
rewritten by the same-repo PR it is meant to police.

### Cache-poisoning boundary

The validator-image buildx caches in `ci.yml`, `docs.yml`, and `mutation.yml`
use a PR-keyed cache scope (`validator-pr-<number>`) distinct from the trusted
`validator-main` scope, and write in `mode=max` only on `push` events. A fork PR
therefore cannot poison the cache that trusted main/release builds consume.

## Secrets handling and rotation

### Inventory

The pipeline is **almost entirely secret-free**. There is exactly **one
long-lived stored secret** in the whole workflow set:

| Secret | Used in | Scope | Purpose | Sensitivity |
|---|---|---|---|---|
| `WORKCELL_HOSTED_CONTROLS_TOKEN` | [`hosted-controls.yml`](../.github/workflows/hosted-controls.yml), [`release.yml`](../.github/workflows/release.yml) preflight | **Environment-scoped** (`hosted-controls-audit`), `allow_admin_bypass = false`, `deployment_branches = ["main"]`, `deployment_tags = ["v*"]` | Read-only admin-metadata token so `scripts/run-hosted-controls-audit.sh` can read rulesets, environments, and variables that the default `GITHUB_TOKEN` cannot | Read-only; **cannot sign, publish, or push** |

Everything else is ephemeral and auto-minted:

- **`GITHUB_TOKEN` / `github.token`** — used for `gh api` calls and GHCR login
  via the pinned `docker/login-action`. Auto-issued per job, expires when the
  job ends. Where it is handed to a script, the workflows use a hardened pattern:
  write the token to a `mktemp` file under `umask 077`, `unset GITHUB_TOKEN
  GH_TOKEN`, pass the path via `WORKCELL_GITHUB_API_TOKEN_FILE`, and `rm -f` it
  on an `EXIT` trap. No workflow echoes a token.
- **OIDC `id-token`** — minted for keyless signing/attestation in `release.yml`
  and for Scorecard provenance. Short-lived; never stored.

**Signing uses no stored key material at all** — see
[Attestation and signing](#attestation-and-signing). The maintainer's git
commit/tag signing key (GPG) is the one other long-lived signing credential in
the system, but it lives on the maintainer's host, not in Actions secrets; CI
only *verifies* signatures with it, never signs.

### Exposure paths

- The stored token flows into `scripts/run-hosted-controls-audit.sh` as an env
  var. It is never echoed by the workflow, but any `set -x` or accidental print
  inside that script would surface it — that script is the audit point for this
  secret.
- Because the token is environment-scoped and the environments disallow admin
  bypass and restrict deployment branches/tags, a PR from a fork or a push to a
  non-`main` branch cannot obtain it.

### Rotation

Because the only long-lived secret is a **read-only** metadata token and
signing is keyless, the rotation surface is small — but a rotation *procedure*
was previously undocumented. The cadence and steps are:

**`WORKCELL_HOSTED_CONTROLS_TOKEN` — rotate every 90 days and immediately on any
suspected exposure:**

1. Mint a replacement fine-grained token (or GitHub App installation token)
   with the same read-only administration/metadata scopes and an expiry ≤ 90
   days.
2. Update the secret in the `hosted-controls-audit` environment
   (Settings → Environments), not at repo scope.
3. Trigger `hosted-controls.yml` via `workflow_dispatch` and confirm the audit
   passes with the new token.
4. Revoke the old token (Developer settings → the token → Delete).
5. Record the rotation date.

**Git commit/tag signing key (GPG, maintainer host):** rotate on the key's own
expiry or on suspected host compromise. Because this key underwrites the
signed-commits ruleset and release-tag verification, treat its compromise as a
signing-compromise incident (see below), not a routine rotation.

**`GITHUB_TOKEN` and OIDC tokens** need no rotation — they are ephemeral by
construction.

## Attestation and signing

The full producer/verifier contract lives in [provenance.md](provenance.md);
this section states only the trust assumptions and gaps relevant to a threat
model.

### What is produced

- **Keyless Sigstore/Cosign signatures** for the image and ~9 release blobs.
  Signing uses GitHub OIDC → Fulcio (short-lived certificate) → Rekor
  transparency log, with `COSIGN_YES: "true"` and **no `--key` flag**. There is
  **no long-lived cosign private key** to steal.
- **GitHub-native attestations** (`actions/attest`) for the image digest, both
  SBOMs, the source bundle, the Homebrew formula, the image-digest file, the
  three deterministic manifests, and `SHA256SUMS`. Fail-closed: absent an
  explicit opt-out (`WORKCELL_RELEASE_NO_ATTEST`, pinned to `false` in
  [`policy/github-hosted-controls.toml`](../policy/github-hosted-controls.toml)),
  every release attests.
- **BuildKit SLSA provenance** (`mode=max`) plus **SPDX-JSON SBOMs** (syft) for
  source and image.

### Verification assumptions — and the gap

Verification is **asymmetric**: the pipeline *produces* signatures,
attestations, and SBOMs comprehensively, but **nothing in CI or in the
installer verifies Workcell's own release outputs**. The release workflow
verifies inputs and reproducibility (tag signature via
`check-release-tag-signature.sh`, byte-for-byte bundle reproducibility via
`verify-release-bundle.sh`, publish-range commit signatures via
`check-publish-commit-signatures.sh`) but does not re-run `cosign verify` or
`gh attestation verify` on the artifacts it just signed. The installers
(`install.sh` → `install-workcell.sh`) install from the local checkout and do
**not** download or verify a signed release artifact.

Consequently, the trust-establishing verification step is **entirely
consumer-driven and documentation-only**: a user who follows the `cosign
verify` / `gh attestation verify` commands in [provenance.md](provenance.md)
gets the guarantee; a user who does not gets none. The verification identity
that a consumer must pin is the release workflow at a tag ref
(`.../release.yml@refs/tags/.+`) with issuer
`https://token.actions.githubusercontent.com`.

SLSA posture is **Build L2, not L3** (self-documented in
[provenance.md](provenance.md)): the image build and the provenance/signing
steps run in the *same* job on the *same* runner, so a compromised build step
could hand a forged digest to the attestation step. The build is also **pinned
and reproducible but not hermetic** (live `apt`/`npm ci`/provider fetches).

## Threats and mitigations

Residual risk is rated after the existing mitigation.

| # | Threat / vector | Existing mitigation (source) | Residual risk |
|---|---|---|---|
| 1 | **Poisoned third-party action or transitive action** runs in a privileged job | Every `uses:` pinned to a full commit SHA and restricted to [`allowed-actions.toml`](../policy/allowed-actions.toml); enforced by `check-pinned-inputs.sh`; `zizmor` audits workflows in `security.yml` | Low: a pinned SHA can still be a compromised-at-tag commit; the allowlist bounds blast radius but does not detect a backdoored pinned commit |
| 2 | **Compromised/poisoned build dependency** (apt, npm, provider tarball) enters the image | TLS-bootstrap `.deb`s and provider tarballs are `sha256`-verified against repo-hardcoded digests; apt uses a pinned Debian snapshot with signed repo metadata; Rust stage is hermetic (vendored, `--offline`) | Medium: build is **not hermetic** — `npm ci` and apt package contents are not repo-digest-pinned; a compromised upstream within the pinned snapshot window is possible |
| 3 | **Secret exfiltration via a malicious fork PR** | PR lanes run on `pull_request` with a read-only token and no environment secrets; the sole stored secret is environment-scoped with no admin bypass and `main`/`v*`-only deployment; the one `pull_request_target` job does no checkout and holds no secret | Low |
| 4 | **Compromised runner** steals credentials or tampers with the build | 100% GitHub-hosted ephemeral single-use runners; no self-hosted tenancy; `persist-credentials: false`; least-privilege per-job tokens | Medium: no `harden-runner`/egress restriction on CI runners, so a compromised step has unrestricted network egress; build+sign share a runner (the L3 gap) |
| 5 | **Signing-key compromise** | **No long-lived cosign key exists** (keyless OIDC/Fulcio); the git tag/commit key lives off-CI on the maintainer host; releases are immutable (`immutable_github_releases = true`) | Low for cosign; see [incident response](#signing-compromise-incident-response) for the OIDC-identity and GPG-key cases |
| 6 | **Provenance / attestation forgery** | Attestations bound to the release workflow identity via OIDC; consumers pin `--certificate-identity` / `--signer-workflow`; Rekor transparency; fail-closed attestation pinned by policy | Medium: **Build L2, not L3** — a compromised build step in the same job could get a genuine attestation over a forged digest; and no consumer is *forced* to verify (threat 8) |
| 7 | **Cache poisoning** from a fork PR into trusted builds | PR-keyed buildx cache scope distinct from `validator-main`; `mode=max` writes gated to `push` events | Low |
| 8 | **Unverified consumer install** — user runs an artifact that was never verified | Verification commands documented in [provenance.md](provenance.md); immutable releases; `SHA256SUMS` signed | High (by design today): installers do not verify; verification is manual/opt-in — the single most valuable gap to close (tracked toward B6) |
| 9 | **Malicious change reaches a release** without review | Signed-commits + anti-rewrite branch ruleset; tag ruleset on `refs/tags/v*`; required status checks; `pr-base-policy.yml` forces `main` base; publish gated on tag-on-green-main and the `release` environment approval; publish rebuilds from the archived source bundle | Medium: **single-maintainer** — the tag signer and the release approver are the same identity; no two-person review (a SLSA Source-track control, out of scope for v1.0 Build) |
| 10 | **Oversized/obfuscated PR** hides a malicious diff | `scripts/check-pr-shape.sh` caps PRs at ≤25 files, ≤1200 changed lines, ≤8 areas, 0 binaries (reviewed override raises limits for certified-adapter PRs only) | Low |
| 11 | **Stored token leaks via script logging** | Token is environment-scoped read-only metadata; passed by env, never echoed by the workflow | Low: depends on `run-hosted-controls-audit.sh` never running `set -x` over the token |

## Signing-compromise incident response

Because signing is keyless, "signing compromise" has three distinct shapes.
Handle each explicitly.

### A. Compromise of the release workflow identity (an attacker gets a genuine Sigstore/attestation signature)

This is the case where an attacker manages to run code in the `release` job (or
otherwise triggers a tag build) and obtains a legitimate OIDC-backed signature
over a malicious artifact. There is no key to revoke; the response is to
invalidate the release and re-establish a clean root.

1. **Detect.** Watch for unexpected tags/releases, a GHCR digest that does not
   match preflight, or a Rekor entry for the release identity that no
   maintainer initiated. The Rekor transparency log makes every keyless
   signature publicly auditable — use it.
2. **Contain.** Immediately disable the `release` environment approval (or
   remove its reviewers) and pause tag creation. Rotate any GitHub credentials
   (PAT/App) that could have triggered the workflow, including
   `WORKCELL_HOSTED_CONTROLS_TOKEN`.
3. **Invalidate.** Delete or yank the bad GHCR image tag and the GitHub release.
   Because releases are immutable, publish a superseding release rather than
   editing in place. Publish an advisory listing the compromised digest(s) so
   consumers who pinned by digest can reject them.
4. **Re-sign clean.** Rebuild from a known-good tagged commit on `main`, re-run
   the full preflight, and publish a new signed/attested release with a new
   version.
5. **Communicate.** Open a GitHub Security Advisory (see
   [`SECURITY.md`](../SECURITY.md)) naming the bad digests, the window, and the
   safe replacement version and its verification identity.

### B. Compromise of the git commit/tag signing key (GPG)

This key underwrites the signed-commits ruleset and release-tag verification. If
the maintainer host or key is compromised:

1. **Revoke** the GPG key (publish a revocation certificate to the keyservers)
   and remove it from the GitHub account's verified signing keys.
2. **Generate** a new signing key, add it to GitHub, and update local signing
   config. Re-sign the current `HEAD`/release tag if it was signed with the
   revoked key.
3. **Audit** recent commits/tags for signatures made after the suspected
   compromise; treat any that verify only under the revoked key as suspect and
   re-review those changes.
4. **Rotate** any secrets that lived on the same host (the
   `WORKCELL_HOSTED_CONTROLS_TOKEN` if it was ever handled there).
5. **Communicate** as in case A if any published release tag is implicated.

### C. Compromise of the transparency/trust root (Sigstore or GitHub OIDC)

A compromise of Fulcio/Rekor or GitHub's OIDC issuer is a trusted-infrastructure
failure outside Workcell's control. The response is to follow Sigstore's /
GitHub's published incident guidance, treat all releases signed in the affected
window as suspect, and re-sign from clean infrastructure once the root is
restored. This is a residual risk, not a mitigated one.

### Post-incident (all cases)

- Write a short post-mortem and add it under [`docs/security/`](../SECURITY.md).
- If the vector was a workflow weakness, fix the workflow and add a `zizmor` /
  `check-pinned-inputs` guard so it cannot recur.
- Re-run `scripts/verify-github-hosted-controls.sh` to confirm rulesets and
  environments are intact.

## Known gaps and future work

These are real weaknesses in the current CI/CD posture, stated so they can be
tracked or accepted deliberately:

1. **No forced consumer verification (threat 8).** Signatures, attestations, and
   SBOMs are produced but no automated consumer — including the installers —
   verifies them; verification is manual and documentation-only. Closing this
   (installer-side `cosign`/`gh attestation verify`) is the highest-value item
   and is a dependency direction for **B6**.
2. **SLSA Build L3 not met (threat 6).** Build and provenance generation share a
   job/runner, so provenance is forgeable by a compromised build step. Closing
   it requires moving the build into an isolated trusted reusable workflow (or
   `slsa-github-generator`). Self-documented in [provenance.md](provenance.md).
3. **Build is not hermetic (threat 2).** Live `apt`/`npm ci`/provider fetches
   during the image build are not fully repo-digest-pinned.
4. **No CI-runner egress hardening (threat 4).** No `step-security/harden-runner`
   or equivalent restricts network egress on GitHub-hosted runners, so a
   compromised step has open egress. (The product's own runtime sandbox has a
   default-deny egress allowlist — that is a *product* control, not a CI one.)
5. **No hard self-hosted-runner ban.** The all-GitHub-hosted guarantee rests on
   exact label pinning plus review, not a rule that rejects a `self-hosted`
   label outright. Future self-hosted Apple Silicon runner work would need an
   explicit trust-tier policy first.
6. **Single-maintainer review (threat 9).** No two-person review; the tag signer
   and release approver are one identity. This is a SLSA Source-track control,
   out of scope for the v1.0 Build-track claim, and a deliberate accepted risk
   in single-maintainer mode.
7. **Attestation opt-out exists.** `WORKCELL_RELEASE_NO_ATTEST=true` would ship a
   release without GitHub SLSA attestations (cosign bundles remain). Mitigated
   by pinning the variable to `false` in the hosted-control policy, but the code
   path exists.

## References

- Runtime-boundary [threat model](threat-model.md) and
  [OWASP agentic mapping](owasp-agentic-mapping.md)
- [Provenance, signing, and SBOMs](provenance.md) — the full producer/verifier
  contract and SLSA gap analysis
- [Release posture](release-posture.md) and the [release runbook](releasing.md)
- [GitHub workflow design](github-workflows.md) — the workflow inventory and
  hosted-control catalog
- [Artifact retention policy](retention-policy.md)
- [`policy/github-hosted-controls.toml`](../policy/github-hosted-controls.toml),
  [`policy/tool-pins.toml`](../policy/tool-pins.toml),
  [`policy/allowed-actions.toml`](../policy/allowed-actions.toml)
- [`SECURITY.md`](../SECURITY.md) — reporting and disclosure
