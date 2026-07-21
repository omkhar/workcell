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
  both SPDX SBOMs, plus the Sigstore bundles and GitHub attestations that cover
  the release (the SBOMs are attached to the image/source attestation subjects
  rather than attested as standalone files — see [Attestation](#what-is-produced)).
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
- `internal/metadatautil/pinnedinputs.go` pins specific runner label sets — the
  CI reproducible-build matrix, the macOS install/release matrices, and the
  presence of an arm64 release runner — so those cannot silently swap to
  `self-hosted` without a reviewed diff to the pin set. It is **not** a global
  guard over every `runs-on:` value: a job outside those checked sets (e.g. the
  `release` publish job) could change its runner without tripping a pinned-input
  diff, and relies on review instead (see the known gap on the self-hosted ban).

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
`id-token: write`, `attestations: write`, `artifact-metadata: write`,
`contents: write`, and `packages: write` at once (OIDC signing, attestation and
artifact-metadata publication, GitHub release creation, and GHCR push). That job — and the native arm64 image build/push job — run only inside
the `release` deployment environment, which requires human approval and
disallows admin bypass (see `[release_environment]` in the hosted-control
policy). A handful of scanning jobs hold `security-events: write` for SARIF
upload, and `scorecard.yml` holds `id-token: write` for Scorecard provenance;
these are the expected minimum for those tools.

One non-release workflow also holds a write scope:
[`upstream-refresh.yml`](../.github/workflows/upstream-refresh.yml) grants its
scheduled/manual job `issues: write` to update a single rolling tracking issue
with the latest upstream-candidate status (it keeps `contents: read`). When it
detects changes it also uploads an internal `upstream-refresh-candidate`
workflow artifact (the candidate patch, diffstat, and metadata) for maintainer
review. It cannot publish **release** or signed artifacts, sign, or push code —
its mutations are the tracking issue and that review-only workflow artifact — but
auditors inventorying non-read `GITHUB_TOKEN` scopes and artifact channels should
count it alongside the release path.

### Untrusted-fork boundary

The pull-request lanes (`ci.yml`, `security.yml`, `codeql.yml`, `mutation.yml`,
`docs.yml`) run on the plain `pull_request` trigger. That means fork PR code
executes with a **read-only** `GITHUB_TOKEN` and no access to environment-scoped
secrets, so a malicious fork PR cannot exfiltrate the one stored secret or push
anything. The heavy lanes (CodeQL, mutation, install verification, and the
`reproducible-build-platform` 2×~45-minute rebuilds) are gated behind an
`approved-heavy-ci` label so an unreviewed fork PR cannot spend that compute
without maintainer opt-in. Reproducibility assurance is not lost: the gated
rebuild still runs on every post-merge `main` push and is re-verified natively in
`release.yml` preflight, and the required aggregate `Reproducible build` check
(`if: always()`) passes green-on-skip for unlabeled PRs while still failing on any
real build failure.

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
| `WORKCELL_HOSTED_CONTROLS_TOKEN` | [`hosted-controls.yml`](../.github/workflows/hosted-controls.yml), [`release.yml`](../.github/workflows/release.yml) preflight | **Environment-scoped** (`hosted-controls-audit`), `allow_admin_bypass = false`, `deployment_branches = ["main"]`, `deployment_tags = ["v*"]` | Provisioned as a read-only admin-metadata token so `scripts/run-hosted-controls-audit.sh` can read rulesets, environments, and variables that the default `GITHUB_TOKEN` cannot | **Intended** read-only. The repo checks the secret's *name* and *environment*, not the granted token scopes — GitHub does not expose a consuming repo's ability to introspect a PAT/App token's scopes — so read-only is a **provisioning discipline, not a repo-enforced guarantee**. Provision it with least privilege accordingly |

Everything else is ephemeral and auto-minted:

- **`GITHUB_TOKEN` / `github.token`** — used for `gh api` calls and GHCR login
  via the pinned `docker/login-action`. Auto-issued per job, expires when the
  job ends. When handed to a script it is either consumed directly from the
  environment by `gh api` (e.g. `check-release-tag-signature.sh`) or, where a
  script isolates it, written to a `mktemp` file under `umask 077` with
  `GITHUB_TOKEN`/`GH_TOKEN` unset and the path passed via
  `WORKCELL_GITHUB_API_TOKEN_FILE`, `rm -f`'d on an `EXIT` trap. Both are
  acceptable for an auto-expiring job token; no workflow echoes a token. (The
  file pattern matters more for the long-lived stored token below.)
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

1. Mint a replacement **fine-grained PAT** with the same read-only
   administration/metadata scopes and an expiry ≤ 90 days. Do **not** store a
   GitHub App *installation* token here — those expire after ~1 hour and the
   scheduled/preflight jobs would fail on a stale secret; if a GitHub App is
   preferred, store the App's credentials and mint the installation token at
   workflow runtime instead.
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
- **GitHub-native attestations** (`actions/attest`) for the image digest, the
  source bundle, the Homebrew formula, the image-digest file, the three
  deterministic manifests, and `SHA256SUMS`. Fail-closed: absent an explicit
  opt-out (`WORKCELL_RELEASE_NO_ATTEST`, pinned to `false` in
  [`policy/github-hosted-controls.toml`](../policy/github-hosted-controls.toml)),
  every release attests.
- **SBOM attestations** (`actions/attest` with `sbom-path`) that attach each
  SPDX-JSON SBOM to the **image-digest and source-bundle subjects** — the SBOM *describes* those
  subjects; the `.spdx.json` files are not themselves attestation subjects.
  Verify them via the image/source subject (`gh attestation verify <image>`), not
  by pointing `gh attestation verify` at a `.spdx.json` file. The SBOM files are
  additionally Cosign-signed as blobs (see the signatures above).
- **BuildKit SLSA provenance** (`mode=max`) plus **SPDX-JSON SBOMs** (syft) for
  source and image.

### Verification assumptions — and the gap

Verification is **partly asymmetric**: the pipeline *produces* signatures,
attestations, and SBOMs comprehensively. On the consumer side
`scripts/install-release.sh` now verifies the release fail-closed before
installing — **by default** the keyless cosign signature over `SHA256SUMS` plus
binding the downloaded bundle's digest to its entry in that verified
`SHA256SUMS`. GitHub attestation verification is **opt-in** via `--attestation`;
the published SBOMs are for downstream SCA, not an install-time gate, so the
installer does **not** verify them (see
[known gap 1](#known-gaps-and-future-work)). Meanwhile **CI still does not verify
Workcell's own release outputs**. The release workflow verifies inputs and
reproducibility (tag signature via `check-release-tag-signature.sh`,
byte-for-byte bundle reproducibility via `verify-release-bundle.sh`) but does
not re-run `cosign verify` or `gh attestation verify` on the artifacts it just
signed. (Publish-range commit signatures are checked separately by
`check-publish-commit-signatures.sh` on the host-side publish-PR path, not by
the release workflow.) The plain installers (`install.sh` →
`install-workcell.sh`) install from a local checkout and do **not** download or
verify a signed release artifact — that is what `install-release.sh` adds.

On the consumer side, `scripts/install-release.sh` automates the
trust-establishing step: it verifies the signed `SHA256SUMS` and the bundle
digest fail-closed (via `scripts/verify-release-artifact.sh`) before installing,
so on that path the guarantee no longer depends on a user remembering the manual
`cosign verify` / `gh attestation verify` commands in
[provenance.md](provenance.md). As of the 1.0 docs this verified path is the
**documented default for installing a tagged release** — the README Install
section and getting-started recommend `install-release.sh` (obtained via a repo
clone over TLS, since the installer is not itself a standalone release asset) or,
from the release page, the manual `cosign verify` flow. Verification is not yet
*forced*, however: the README's 5-minute quickstart still runs the plain
`./scripts/install.sh` from an assumed local checkout, a user can otherwise
choose an unverified install (`tar … && ./scripts/install.sh` or a hand-fetched
bundle), and no standalone verified installer is
published as a release asset — so the residual is a defaulting/bootstrap gap,
not an absent capability (threat 8,
[known gap 1](#known-gaps-and-future-work)). The verification identity that a consumer (or the installer) pins is the
release workflow at a tag ref (`.../release.yml@refs/tags/.+`) with issuer
`https://token.actions.githubusercontent.com`. What remains asymmetric is CI:
the release workflow verifies inputs and reproducibility but still does not
re-run `cosign verify` / `gh attestation verify` on the artifacts it just
signed.

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
| 2 | **Compromised/poisoned build dependency** (apt, npm, provider tarball) enters the image | TLS-bootstrap `.deb`s and provider tarballs are `sha256`-verified against repo-hardcoded digests; apt uses a pinned Debian snapshot with signed repo metadata; Rust stage is hermetic (vendored, `--offline`) | Medium: build is **not hermetic** — `npm ci` and apt package contents are not repo-digest-pinned; a compromised upstream within the pinned snapshot window is possible. The pin updater resolves bootstrap paths and digests from `Packages.gz` over authenticated HTTPS and byte-verifies each `.deb`, but does not independently verify the index's Debian OpenPGP signature; the reviewed, signed repository update is the durable trust record. |
| 3 | **Secret exfiltration via a malicious fork PR** | PR lanes run on `pull_request` with a read-only token and no environment secrets; the sole stored secret is environment-scoped with no admin bypass and `main`/`v*`-only deployment; the one `pull_request_target` job does no checkout and holds no secret | Low |
| 4 | **Compromised runner** steals credentials or tampers with the build | 100% GitHub-hosted ephemeral single-use runners; no self-hosted tenancy; `persist-credentials: false`; least-privilege per-job tokens | Medium: no `harden-runner`/egress restriction on CI runners, so a compromised step has unrestricted network egress; build+sign share a runner (the L3 gap) |
| 5 | **Signing-key compromise** | **No long-lived cosign key exists** (keyless OIDC/Fulcio); the git tag/commit key lives off-CI on the maintainer host; releases are immutable (`immutable_github_releases = true`) | Low for cosign; see [incident response](#signing-compromise-incident-response) for the OIDC-identity and GPG-key cases |
| 6 | **Provenance / attestation forgery** | Attestations bound to the release workflow identity via OIDC; consumers pin `--certificate-identity` / `--signer-workflow`; Rekor transparency; fail-closed attestation pinned by policy | Medium: **Build L2, not L3** — a compromised build step in the same job could get a genuine attestation over a forged digest; and no consumer is *forced* to verify (threat 8) |
| 7 | **Cache poisoning** from a fork PR into trusted builds | PR-keyed buildx cache scope distinct from `validator-main`; `mode=max` writes gated to `push` events | Low |
| 8 | **Unverified consumer install** — user runs an artifact that was never verified | A fail-closed verified path is the **documented default**: `scripts/install-release.sh` downloads, verifies via `verify-release-artifact.sh` (`cosign verify-blob` of the signed `SHA256SUMS` against the pinned release-workflow identity, plus optional `gh attestation verify`), and only then installs; the README and getting-started lead with it (or the manual `cosign verify` flow from the release page); verification commands also documented in [provenance.md](provenance.md); immutable releases; `SHA256SUMS` signed | Medium: verified install is the documented default and fail-closed, **but not *forced*** — a user can still choose an unverified install (`tar … && ./scripts/install.sh`, the plain `install.sh`, or any hand-fetched bundle), and the installer is obtained via a repo clone rather than a standalone signed release asset. Reduced from High, not eliminated, until verification is forced across all paths and a verified installer bootstrap ships (see [known gap 1](#known-gaps-and-future-work)) |
| 9 | **Malicious change reaches a release** without review | Signed-commits + anti-rewrite branch ruleset; tag ruleset on `refs/tags/v*`; required status checks; `pr-base-policy.yml` forces `main` base; publish gated on tag-on-green-main and the `release` environment approval; the amd64 image is rebuilt from the archived source bundle (`context: dist/release-source`) | Medium: **single-maintainer** — the tag signer and the release approver are the same identity; no two-person review (a SLSA Source-track control, out of scope for v1.0 Build). The **arm64** image is built from the checked-out release tag (`context: .`), not the repackaged source archive, so only amd64 gets the archive-rebuild property; both platforms still derive from the same signed, immutable tag |
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
   config. Do **not** move, delete, or re-sign an existing release tag — that
   violates the immutable-release rule in [`releasing.md`](releasing.md) ("Never
   move or rewrite an existing release tag") and destroys the audit trail.
   Instead, if a published release is implicated, cut a **superseding release**:
   a new signed commit on `main` and a new signed tag under the new key.
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

1. **Forced consumer verification — PARTIALLY addressed (verified install is now
   the documented default; not yet *forced*, and the installer bootstraps via a
   repo clone).** A fail-closed verified install path now
   exists: `scripts/install-release.sh` downloads, verifies, and only then
   installs a tagged release via `scripts/verify-release-artifact.sh`, which by
   default `cosign verify-blob`s the signed `SHA256SUMS` against the pinned
   release workflow identity (`.../release.yml@refs/tags/.+`, issuer
   `https://token.actions.githubusercontent.com`) and binds the bundle digest to
   that verified list, optionally requiring `gh attestation verify`
   (`--attestation`). A missing `cosign`, absent material, a bad signature, or a
   digest mismatch each refuse the install; the only bypass is an explicit,
   acknowledged `--skip-verify` for documented air-gapped installs (see
   [install.md](install.md#verified-release-install-recommended)). The published
   SBOMs are for downstream SCA and are deliberately **not** an install-time gate
   (the installer does not verify them). The README and getting-started now lead
   with this verified path (`install-release.sh`, obtained via a repo clone over
   TLS, or the manual `cosign verify` flow from the release page). **This makes
   verified install the documented default, but does not yet fully close the
   gap:** verification is not *forced* — a user can still choose an unverified
   install (`tar … && ./scripts/install.sh`, the plain `install.sh`, or a
   hand-fetched bundle) — and the installer is bootstrapped from a repo clone
   rather than a standalone signed release asset.
   Homebrew installs verify at checksum level via the pinned `sha256` in
   `workcell.rb`. **G3 progress:** `install-release.sh` is now exercised end to
   end in CI against a locally built fixture release with stubbed `curl`/`cosign`
   (`internal/testkit/install_release_e2e_test.go`), proving the full
   download → verify → extract → handoff chain is fail-closed — a bad signature
   or a digest mismatch aborts before the bundle installer runs. Part (b) below
   is now done: the verified path has been exercised end-to-end against the
   genuinely published, cosign-signed `v1.0.0-rc.2` release (`install-release.sh
   --attestation` exit 0 with cosign + digest + attestation verified, plus a
   tamper negative control — see
   [install-lifecycle.md](install-lifecycle.md) §"Certification record"), so the
   real keyless Sigstore signature path is proven, not just the offline fixture.
   **Fully closing gap 1** now requires only (a) making verification *forced*
   across all documented install flows and shipping a standalone verified
   installer bootstrap (so consumers need not clone the repo to get a trusted
   `install-release.sh`); tracked under **G3**, install-lifecycle proof; see
   [install-lifecycle.md](install-lifecycle.md).
2. **SLSA Build L3 not met (threat 6).** Build and provenance generation share a
   job/runner, so provenance is forgeable by a compromised build step. Closing
   it requires moving the build into an isolated trusted reusable workflow (or
   `slsa-github-generator`). Self-documented in [provenance.md](provenance.md).
3. **Build is not hermetic (threat 2).** Live `apt`/`npm ci`/provider fetches
   during the image build are not fully repo-digest-pinned. Relatedly, only the
   amd64 image is rebuilt from the archived source bundle; the arm64 image builds
   from the checked-out release tag (`context: .`), so the archive-rebuild
   provenance property is amd64-only (both platforms still derive from the same
   signed tag).
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
