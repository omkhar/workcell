# Provenance, Signing, and SBOMs

Workcell release artifacts are published with verifiable metadata, not just as
opaque downloads. Signed release publication is gated by a release preflight
that reruns repo validation, container smoke checks, a reproducible
runtime-image check, and a reproducible source-bundle check on the tagged
commit.

## Release outputs

Tagged releases publish:

- a multi-architecture container image to GHCR
- a source bundle tarball
- a keyless Sigstore bundle for the source bundle
- a published image digest file
- a machine-readable build input manifest
- a keyless Sigstore bundle for the build input manifest
- a machine-readable control-plane manifest
- a keyless Sigstore bundle for the control-plane manifest
- a machine-readable builder environment manifest
- a keyless Sigstore bundle for the builder environment manifest
- `SHA256SUMS`
- a source SBOM in SPDX JSON
- a keyless Sigstore bundle for the source SBOM
- an image SBOM in SPDX JSON
- a keyless Sigstore bundle for the image SBOM
- keyless Sigstore signatures for the published image
- a keyless Sigstore bundle for `SHA256SUMS`, which covers the source bundle,
  published image digest, build input manifest, control-plane manifest, builder
  environment manifest, and both SBOM files
- GitHub attestations when `WORKCELL_ENABLE_GITHUB_ATTESTATIONS=true` is set on
  a repository and plan that support them

## Trusted builder posture

The release workflow uses:

- pinned GitHub Actions refs
- GitHub-hosted runners
- GitHub OIDC for short-lived signing identity
- Docker Buildx provenance and SBOM generation
- Sigstore keyless signing
- exact pinned Cosign releases in CI and release workflows, plus an exact
  pinned Syft release in the publishing workflow
- a digest-pinned base image
- a digest-pinned BuildKit daemon image for GitHub-hosted builder jobs
- fixed Debian snapshot archives
- a vendored, locked, offline Rust build for the shipped launcher and exec
  guard artifacts, rebuilt inside the runtime image with a fixed
  `SOURCE_DATE_EPOCH`
- a committed provider CLI lockfile with integrity-pinned transitive npm inputs
  installed with `npm ci --ignore-scripts` and validated in CI for source,
  version, and integrity coverage
- a deterministic build input manifest that records the pinned runtime base
  image, full runtime Dockerfile digest, Debian snapshot, Codex release assets,
  provider lockfile digests, the actual runtime build-context inputs consumed by
  the Dockerfile including `.dockerignore`, adapters, vendored Rust runtime
  sources, and runtime enforcement code, plus the rest of the tracked
  repository inputs that control release publication, and is itself directly
  signed and also covered by the signed checksum set
- a deterministic control-plane manifest that records the reviewed adapter
  baselines, runtime control-plane scripts that seed provider-facing homes,
  and the host-side launcher, Docker-context guard, direct-mount extractor,
  and injection-policy renderer that stage those homes, is copied into the
  runtime image, drives runtime hash checks for the adapter baselines and
  selected seeding inputs, is directly signed at release time, and is also
  bound into the signed checksum set
- host-side pin-hygiene checks that run before rebuilding the validator image
  in CI and release preflight, including exact package-set checks for both the
  runtime and validator Dockerfiles, pinned workflow-side BuildKit and signing
  tool releases, and rejection of ambient `gh release` publication paths
- release-preflight verification of the pinned Codex Linux assets against
  OpenAI's published Sigstore bundle
- scheduled freshness checks for pinned non-Dependabot inputs such as the
  Debian snapshot and pinned Codex release version
- a fixed source bundle mtime plus `gzip -n` for reproducible source archives
- fixed-order multi-architecture publication, where the final manifest list is
  assembled from independently published `linux/amd64` and `linux/arm64`
  manifests in a reviewed order rather than emitted by one opaque multi-platform
  push step

This gives consumers one always-on verification path and one optional one:

1. Sigstore and Cosign verification
2. GitHub attestation verification when repository settings enable it

The builder proves that GitHub Actions produced the published artifacts. The
release workflow additionally requires the tagged commit to be reachable from
`main`, but consumers should still verify branch protection and review policy in
the repository itself.

The reproducibility check compares two multi-platform OCI layout exports that
cover every supported runtime platform, built with the same
`SOURCE_DATE_EPOCH` and without attached attestations. This is intentional:
BuildKit attestations are separately signed and verified release metadata,
while the reproducibility gate measures the image contents themselves. The
check compares the stable OCI subject digest from those exports and still
records reviewed per-platform manifest and config digests, and release
publication requires the pushed per-platform digests to match the preflight
manifest before the final multi-architecture manifest is published. The release
preflight also verifies deterministic source bundle generation, records the
expected tarball digest for the tag-specific prefix and ref, and requires the
published source bundle to match that preflight digest.
The publish job then rebuilds the signed build-input manifest and the published
runtime image from the extracted signed source bundle tree, not from the live
checkout, so the signed tarball is the authoritative source for the shipped
runtime contents. Release preflight also generates the exact expected
build-input manifest and publish requires the manifest regenerated from the
archived source tree to match it byte-for-byte before signing. Local
verification also requires release-critical manifest inputs to be tracked in
git before the repo validation or release preflight passes, so newly added
workflow, script, docs, policy, or runtime files cannot silently fall outside
the signed input set.

Those checks prove that Workcell can rebuild the OCI runtime image and the
published source bundle deterministically when the same pinned upstream
artifacts are still available. They do not make the build hermetic or
independent of upstream artifact availability, and they do not claim
byte-for-byte reproducibility for generated SBOM or attestation metadata. The
published SBOMs are directly signed and are also bound into the always-on
signed checksum manifest. The runtime also removes general-purpose Perl
entrypoints from the final image,
mounts `/tmp` as `noexec`, and uses `/state/tmp` as the exec-capable temporary
area so the shipped runtime shape matches the invariants the verification suite
expects.

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

1. Download `SHA256SUMS`, `SHA256SUMS.sigstore.json`, and the release assets you
   want to verify.
2. Optionally verify the directly signed source bundle and SBOM bundles with
   Cosign.
3. Verify the checksum file bundle with Cosign.
4. Verify the tarball, image digest file, build input manifest, builder
  environment manifest, control-plane manifest, and SBOM digests against
  `SHA256SUMS`.

The control-plane manifest publishes two reviewed domains in one deterministic
artifact:

- `runtime_artifacts`: reviewed files baked into the runtime image; current
  runtime hash checks cover the adapter baselines and selected seeding inputs
- `host_artifacts`: reviewed host-side launcher, Docker-context guard,
  direct-mount extractor, and injection-renderer inputs that are signed and
  published for release provenance, but not verified inside the runtime

```bash
cosign verify-blob SHA256SUMS \
  --bundle SHA256SUMS.sigstore.json \
  --certificate-identity-regexp 'https://github.com/OWNER/workcell/.github/workflows/release.yml@refs/tags/.+' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com

cosign verify-blob workcell-build-inputs.json \
  --bundle workcell-build-inputs.sigstore.json \
  --certificate-identity-regexp 'https://github.com/OWNER/workcell/.github/workflows/release.yml@refs/tags/.+' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com

cosign verify-blob workcell-control-plane.json \
  --bundle workcell-control-plane.sigstore.json \
  --certificate-identity-regexp 'https://github.com/OWNER/workcell/.github/workflows/release.yml@refs/tags/.+' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com

cosign verify-blob workcell-VERSION.tar.gz \
  --bundle workcell-VERSION.tar.gz.sigstore.json \
  --certificate-identity-regexp 'https://github.com/OWNER/workcell/.github/workflows/release.yml@refs/tags/.+' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com

cosign verify-blob workcell-source.spdx.json \
  --bundle workcell-source.spdx.sigstore.json \
  --certificate-identity-regexp 'https://github.com/OWNER/workcell/.github/workflows/release.yml@refs/tags/.+' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com

cosign verify-blob workcell-image.spdx.json \
  --bundle workcell-image.spdx.sigstore.json \
  --certificate-identity-regexp 'https://github.com/OWNER/workcell/.github/workflows/release.yml@refs/tags/.+' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com

cosign verify-blob workcell-builder-environment.json \
  --bundle workcell-builder-environment.sigstore.json \
  --certificate-identity-regexp 'https://github.com/OWNER/workcell/.github/workflows/release.yml@refs/tags/.+' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

If GitHub attestations are available:

```bash
gh attestation verify workcell-VERSION.tar.gz --repo OWNER/workcell
```

## Scope note

These release materials prove how the published artifacts were built. They do
not, by themselves, prove the full local macOS runtime boundary. That assurance
still depends on Workcell's local runtime verification and operator discipline.
