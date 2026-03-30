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
- `SHA256SUMS`
- a published image digest file
- a deterministic build-input manifest
- a deterministic control-plane manifest
- a deterministic builder-environment manifest
- source and image SBOMs in SPDX JSON
- Sigstore bundles for the source bundle, checksums, build-input manifest,
  control-plane manifest, builder-environment manifest, and both SBOMs
- keyless Sigstore signatures for the published image
- GitHub attestations for the published image, image SBOM, source bundle, and
  source SBOM in the canonical upstream repository

## What the release workflow proves

Before publish, release preflight reruns:

- repository validation
- container smoke
- release-bundle reproducibility
- runtime-image reproducibility
- hosted-control auditing
- upstream pinned Codex and Claude release verification

The publish job then rebuilds from the archived source bundle, not the live
checkout. It binds the published per-platform image digests, source bundle,
build-input manifest, and control-plane manifest back to preflight results
before signing and publication.

## Sigstore path

The always-on path is:

- GitHub OIDC identity
- keyless Cosign signing
- Rekor-backed Sigstore bundles published with the release assets

This path is the recommended verifier for consumers who want a portable,
GitHub-independent check.

## GitHub attestation path

The canonical upstream repo also publishes GitHub attestations. That posture is
treated as part of the reviewed hosted control plane through the
`WORKCELL_ENABLE_GITHUB_ATTESTATIONS=true` repository variable.

This does not replace Sigstore. It adds:

- GitHub-native attestation UX and policy integration
- subject-linked attestations for the image and selected release artifacts

Because GitHub attestations can add extra OCI attestation manifests next to the
published image, Workcell validates multi-arch platform ordering separately
from those attestation entries.

## Verifying the image

Verify the image with Cosign:

```bash
cosign verify ghcr.io/OWNER/workcell@sha256:DIGEST \
  --certificate-identity-regexp 'https://github.com/OWNER/workcell/.github/workflows/release.yml@refs/tags/.+' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

Verify the same image with GitHub attestation tooling:

```bash
gh attestation verify oci://ghcr.io/OWNER/workcell@sha256:DIGEST \
  --repo OWNER/workcell
```

## Verifying release assets

1. Download `SHA256SUMS`, `SHA256SUMS.sigstore.json`, and the assets you want
   to verify.
2. Verify the signed checksum file with Cosign.
3. Verify the asset digests against `SHA256SUMS`.
4. Optionally verify the directly signed JSON manifests or SBOMs with their own
   Sigstore bundles.

Example:

```bash
cosign verify-blob SHA256SUMS \
  --bundle SHA256SUMS.sigstore.json \
  --certificate-identity-regexp 'https://github.com/OWNER/workcell/.github/workflows/release.yml@refs/tags/.+' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com

sha256sum -c SHA256SUMS
```

If you want GitHub's attestation view for the source bundle:

```bash
gh attestation verify workcell-VERSION.tar.gz --repo OWNER/workcell
```

## Scope note

Release provenance proves how the published artifacts were built and signed. It
does not, by itself, prove the entire local macOS runtime boundary. That still
depends on Workcell's local runtime controls, local validation, and operator
discipline.
