# Provenance, Signing, and SBOMs

Workcell release artifacts are built to be verifiable, not just downloadable.
Signed release publication is gated by a release preflight that reruns repo
validation and container smoke checks on the tagged commit.

## Release outputs

Tagged releases publish:

- a multi-architecture container image to GHCR
- a source bundle tarball
- `SHA256SUMS`
- a source SBOM in SPDX JSON
- an image SBOM in SPDX JSON
- keyless Sigstore signatures for the published image
- a keyless signature plus certificate for `SHA256SUMS`
- GitHub attestations when `WORKCELL_ENABLE_GITHUB_ATTESTATIONS=true` is set on
  a repository and plan that support them

## Trusted builder posture

The release workflow uses:

- pinned GitHub Actions refs
- GitHub-hosted runners
- GitHub OIDC for short-lived signing identity
- Docker Buildx provenance and SBOM generation
- Sigstore keyless signing

This gives consumers two verification paths:

1. Sigstore and Cosign verification
2. GitHub attestation verification

The builder proves that GitHub Actions produced the published artifacts. The
release workflow additionally requires the tagged commit to be reachable from
`main`, but consumers should still verify branch protection and review policy in
the repository itself.

## Verifying a release image

```bash
cosign verify ghcr.io/OWNER/workcell@sha256:DIGEST \
  --certificate-identity-regexp 'https://github.com/OWNER/workcell/.github/workflows/release.yml@refs/tags/.+' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

If GitHub attestations are available for the repository:

```bash
gh attestation verify oci://ghcr.io/OWNER/workcell@sha256:DIGEST \
  --repo OWNER/workcell
```

## Verifying the source bundle

1. Download `SHA256SUMS`, `SHA256SUMS.sig`, and `SHA256SUMS.pem`.
2. Verify the checksum file signature with Cosign.
3. Verify the tarball digest against `SHA256SUMS`.

If GitHub attestations are available:

```bash
gh attestation verify workcell-VERSION.tar.gz --repo OWNER/workcell
```

## Scope note

These release materials prove how the published artifacts were built. They do
not, by themselves, prove the full local macOS runtime boundary. That assurance
still depends on Workcell's local runtime verification and operator discipline.
