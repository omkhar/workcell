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
- hosted-control auditing
- authoritative-source verification of the GitHub-hosted Apple Silicon macOS
  release install runner labels
- upstream pinned Codex, Claude, and Gemini release verification
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

The canonical upstream repo also publishes GitHub attestations when the
reviewed hosted controls allow it. That posture is tracked through two
repository variables:

- `WORKCELL_ENABLE_GITHUB_ATTESTATIONS=true` requests GitHub attestations
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

## Scope note

Release provenance proves how the published artifacts were built and signed. It
does not, by itself, prove the entire local macOS runtime boundary. That still
depends on Workcell's local runtime controls, local validation, and operator
discipline.

Continuous CI and tagged-release install verification are also intentionally
narrow: they currently prove package installability only on GitHub-hosted
Apple Silicon `macos-26` and `macos-15`.
