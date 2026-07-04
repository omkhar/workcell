# Provenance, Signing, and SBOMs

Workcell releases publish verifiable artifacts, not just opaque downloads.
The canonical release posture uses two verification surfaces:

1. always-on keyless Sigstore/Cosign signing
2. GitHub-native attestations as an additional publication surface

Sigstore is the portable baseline. GitHub attestations are additive.

## What tagged releases publish

Tagged releases publish:

- a multi-architecture runtime image to GHCR
- a source bundle tarball
- a versioned Homebrew formula asset (`workcell.rb`)
- `SHA256SUMS`
- a published image digest file
- a deterministic build-input manifest
- a deterministic control-plane manifest
- a deterministic builder-environment manifest
- source and image SBOMs in SPDX JSON
- a Sigstore bundle for the Homebrew formula asset
- Sigstore bundles for the source bundle, published image digest file,
  checksums, build-input manifest, control-plane manifest,
  builder-environment manifest, and both SBOMs
- keyless Sigstore signatures for the published image
- GitHub attestations for the published image, image SBOM, source bundle,
  source SBOM, Homebrew formula, published image digest file, checksums,
  build-input manifest, control-plane manifest, and builder-environment
  manifest when the reviewed hosted controls say the repository visibility and
  GitHub plan support that publication path

## What the release workflow proves

Before publish, release preflight reruns:

- repository validation
- container smoke
- release-bundle reproducibility
- runtime-image reproducibility
- explicit nonroot validator and release-helper execution when the archived or
  live repository is bind-mounted into verification containers
- hosted-control auditing
- authoritative-source verification of the GitHub-hosted Apple Silicon macOS
  release install runner labels
- upstream pinned Codex, Claude, Copilot, and Gemini release verification
- fail-closed Google Antigravity provider promotion checks before any future
  support claim
- reviewed upstream pin verification across providers, Linux base images,
  Linux toolchains, and release-build helper pins
- release-bundle install/uninstall and Homebrew install/uninstall verification
  on GitHub-hosted Apple Silicon `macos-26` and `macos-15`

The publish job then rebuilds from the archived source bundle, not the live
checkout, and re-verifies upstream provider releases plus the reviewed upstream
pin set from that archived source tree. It binds the published per-platform
image digests, source bundle, build-input manifest, and control-plane manifest
back to preflight results before signing and publication.

## Sigstore path

The always-on path is:

- GitHub OIDC identity
- keyless Cosign signing
- Rekor-backed Sigstore bundles published with the release assets

This path is the recommended verifier for consumers who want a portable,
GitHub-independent check.

## GitHub attestation path

The canonical upstream repo publishes GitHub attestations fail-closed: every
release attests its artifacts unless the opt-out is set. That posture is
tracked through two repository variables:

- `WORKCELL_RELEASE_NO_ATTEST=true` is the explicit opt-out; when unset,
  releases attest (the earlier `WORKCELL_ENABLE_GITHUB_ATTESTATIONS` opt-in
  was removed because a missing toggle silently produced unattested releases)
- `WORKCELL_ENABLE_PRIVATE_GITHUB_ATTESTATIONS=true` is only allowed when the
  repository is private/internal and the GitHub plan actually supports private
  artifact attestations

This does not replace Sigstore. It adds:

- GitHub-native attestation UX and policy integration
- subject-linked attestations for the image and selected release artifacts

Because GitHub attestations can add extra OCI attestation manifests next to the
published image, Workcell validates multi-arch platform ordering separately
from those attestation entries.

## Verifying the image

Verify the image with Cosign:

```bash
cosign verify ghcr.io/omkhar/workcell@sha256:DIGEST \
  --certificate-identity-regexp 'https://github.com/omkhar/workcell/.github/workflows/release.yml@refs/tags/.+' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

If the canonical repo published GitHub attestations for that release, verify
the same image with GitHub attestation tooling:

```bash
gh attestation verify oci://ghcr.io/omkhar/workcell@sha256:DIGEST \
  --repo omkhar/workcell
```

## Verifying release assets

1. Download `SHA256SUMS`, `SHA256SUMS.sigstore.json`, and the assets you want
   to verify, including `workcell.rb` if you plan to install through Homebrew.
2. Verify the signed checksum file with Cosign.
3. Verify the asset digests against `SHA256SUMS`.
4. Optionally verify the directly signed Homebrew formula, JSON manifests, or
   SBOMs with their own Sigstore bundles.

Example:

```bash
cosign verify-blob SHA256SUMS \
  --bundle SHA256SUMS.sigstore.json \
  --certificate-identity-regexp 'https://github.com/omkhar/workcell/.github/workflows/release.yml@refs/tags/.+' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com

sha256sum -c SHA256SUMS
```

If you want GitHub's attestation view for the source bundle and the canonical
repo published GitHub attestations for that release:

```bash
gh attestation verify workcell-VERSION.tar.gz --repo omkhar/workcell
```

## Host-side PR publication

Release and publication changes enter review through host-side PR publication,
not from inside the Tier 1 runtime. The supported `main`-based path uses
`./scripts/repo-publish-pr.sh`, which verifies fresh local PR-parity evidence
before delegating to `workcell publish-pr`.

`workcell publish-pr` signs any new commit it creates and verifies every
commit in the branch range being published before push or PR creation. The
signature check uses host Git signing trust and ignores workspace-local signer
configuration, stale tracking refs, and replacement refs as trust shortcuts.

## SLSA v1.0 Build-track gap analysis

SLSA v1.0 splits its requirements into tracks. This section assesses the release
build against the **Build track** (levels L1-L3). Source-integrity controls such
as two-person review live in the separate **Source track** and are covered as a
non-goal below.

Reproducibility and hermeticity are *not* Build L1-L3 requirements in v1.0; they
are additive properties. Workcell's reproducibility work is credit beyond the
required levels and does not by itself raise the Build-track level.

**Current posture: Build L2 (met). Build L3 is partial — Workcell does not claim
L3.**

### Build L1 — provenance exists

| Requirement | Status | Mechanism |
|---|---|---|
| Provenance describes the platform, process, and top-level inputs | Met | BuildKit SLSA provenance (`provenance: mode=max`) on both image builds; bare `actions/attest` SLSA build-provenance predicates for the image and every release blob; deterministic `workcell-build-inputs.json`, `workcell-control-plane.json`, and `workcell-builder-environment.json` manifests |
| Consistent build process | Met | one tag-triggered workflow builds a SHA-pinned Dockerfile with digest-pinned base images and toolchains |
| Provenance distributed to consumers | Met | Sigstore bundles and SLSA attestations are published as release assets and pushed to the registry; verification is documented above |

### Build L2 — hosted platform, authentic provenance

| Requirement | Status | Mechanism |
|---|---|---|
| Build runs on hosted, dedicated infrastructure | Met | all build and publish jobs run on GitHub-hosted runners; the release path uses no self-hosted runners |
| Provenance tied to the platform by signature | Met | keyless cosign signing and GitHub attestations, both via GitHub OIDC and Fulcio short-lived certificates (no long-lived signing key) |
| Downstream verification validates authenticity | Met | documented verifier commands pin the certificate identity to the release workflow at a tag ref and the GitHub OIDC issuer |

Build L2 is the highest level Workcell claims today.

### Build L3 — hardened builds, non-forgeable provenance

| Requirement | Status | Path to close / non-goal |
|---|---|---|
| Isolated, ephemeral environment; runs cannot influence one another | Met | GitHub-hosted runners are single-use ephemeral VMs; the reproducibility harness additionally rebuilds with no cache and byte-compares independent builds |
| Signing material inaccessible to build steps | Partial | there is no long-lived signing key to exfiltrate (ephemeral Fulcio certs), but the image build and the signing/attestation steps run in the same job on the same runner. Close by moving provenance generation into an isolated reusable workflow that receives only artifact digests |
| Provenance strongly resistant to tenant forgery | Partial | the provenance predicate is generated by `actions/attest` in the same job that ran the build. GitHub documents this exact configuration as Build L2; L3 requires isolating provenance generation via a reusable workflow. Close by the same reusable-workflow (or `slsa-framework/slsa-github-generator`) change |

The L2-to-L3 gap is a single, closeable change: generate provenance in an
isolated reusable workflow (or the SLSA GitHub generator) that consumes only the
build job's output digests, with no user-defined build steps present. Until that
lands, Workcell claims Build L2.

### Hermeticity

The image build is **pinned, integrity-checked, reproducible, and
network-dependent — not hermetic**. It performs live network fetches (Debian
snapshot apt, `npm ci`, and provider-binary downloads), each pinned by digest or
snapshot date and verified against a hardcoded checksum. Only the Rust compile
stage is hermetic (vendored crates, `cargo build --locked --offline`).
Hermeticity is not a Build L1-L3 requirement; closing it (vendoring the remaining
inputs and building with the network disabled) is optional hardening.

### Source-track note (two-person review)

Two-person review is a Source-track control and is a structural non-goal in
single-maintainer mode. The release path nonetheless raises source-integrity
assurance through signed annotated release tags (verified via GitHub's API and
locally), a tag-ancestry check requiring the tag commit to be on `main`, a
"main checks green" gate before publish, signed publish commits verified across
the branch range, and a gated `release` deployment environment. These do not
satisfy, and are not claimed to satisfy, Source-track two-person review.

### Caveats

- Claim **Build L2**, not L3: the runner is isolated, but provenance generation
  is not isolated from the build job.
- Reproducible and hermetic are distinct properties, and neither is a Build-track
  level; the reproducibility story does not discharge the L3 isolation gap.
- The default posture is fail-closed attestation. A release built with
  `WORKCELL_RELEASE_NO_ATTEST=true` ships without GitHub SLSA attestations
  (cosign bundles remain), which drops the provenance guarantee for that release.

## Scope note

Release provenance proves how the published artifacts were built and signed. It
does not, by itself, prove the entire local macOS runtime boundary. That still
depends on Workcell's local runtime controls, local validation, and operator
discipline.

Continuous CI and tagged-release install verification are also intentionally
narrow: they currently prove package installability only on GitHub-hosted
Apple Silicon `macos-26` and `macos-15`.
