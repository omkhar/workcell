# Provenance, signatures, and SBOMs

Workcell publishes signed release data for each successful tagged release.
The release uses two verification systems:

1. Keyless Sigstore and Cosign signatures
2. GitHub artifact attestations

Sigstore is the required release signature system.
GitHub attestations add a second verification surface.

## What tagged releases publish

The release publishes a multi-platform runtime image to GHCR.
It also publishes 18 downloadable assets.

Nine assets contain release data:

- the source bundle
- the Homebrew formula
- the image digest
- the build-input manifest
- the control-plane manifest
- the builder-environment manifest
- the source SBOM
- the image SBOM
- `SHA256SUMS`

The other nine assets contain one Sigstore bundle for each release-data asset.
The release also signs the runtime image in the container registry.

By default, the release creates GitHub build-provenance attestations for these subjects:

- the runtime image
- the source bundle
- the Homebrew formula
- the image digest
- the three JSON manifests
- `SHA256SUMS`

The release also attaches an SBOM predicate to the image and source-bundle subjects.
The SBOM files are not attestation subjects.
Cosign signs each SBOM file as a release asset.

## Published v1.0.2 evidence

[`v1.0.2`](https://github.com/omkhar/workcell/releases/tag/v1.0.2) is the first published stable Workcell 1.0 release.
GitHub reports this release as final and immutable.
GitHub also reports a SHA-256 digest for each of its 18 assets.

The [`Release` workflow](https://github.com/omkhar/workcell/actions/runs/30974305124) completed successfully on 2026-08-05.
Both Apple Silicon install-verification jobs passed.
The final publisher uploaded the exact 18-asset publication set.

The 2026-08-05 live install record verified the `v1.0.2` source bundle.
Cosign verified `SHA256SUMS` against the release workflow identity.
GitHub accepted its source-bundle attestation.
The signed source-bundle digest is:

```text
c2a34aa1cf2c2119633cd0f04fc0470520ac44d1e22e1d74f409ff696fc38d1f
```

## What the release workflow proves

Release preflight checks these items before publication:

- repository validation
- container smoke tests
- source-bundle reproducibility
- runtime-image reproducibility
- nonroot validator and release-helper execution
- hosted repository controls
- Apple Silicon runner metadata
- pinned provider releases
- pinned base images and toolchains
- bundle install and uninstall on `macos-15` and `macos-26`
- Homebrew install and uninstall on the same runners

The amd64 image job rebuilds from the archived source bundle.
A separate native arm64 job builds from the checked-out signed tag.
The amd64 job checks the archived provider pins again.
The workflow binds both platform digests and the image manifests to the preflight results.
It then signs and stages the release asset set.

The final job checks hosted controls again.
It removes the administration token before publication.
It publishes only the sealed workflow artifact.

## Sigstore path

The Sigstore path uses these parts:

- the GitHub OIDC identity
- short-lived Fulcio certificates
- keyless Cosign signatures
- Rekor transparency data in Sigstore bundles

This check does not need the GitHub attestation service.
It still trusts the named GitHub release workflow identity.

## GitHub attestation path

The canonical public repository creates GitHub attestations.
Its hosted-control policy requires both repository variables below to be `false`:

- `WORKCELL_RELEASE_NO_ATTEST`
- `WORKCELL_ENABLE_PRIVATE_GITHUB_ATTESTATIONS`

Do not set either variable to `true` in the canonical repository.
The hosted-control check fails if a variable has a different value.
Thus, a variable change alone cannot create an upstream release without attestations.

Do not use the old `WORKCELL_ENABLE_GITHUB_ATTESTATIONS` variable.
The release workflow no longer uses that opt-in variable.

A fork cannot enable an exception with a variable or policy-file change alone.
It must first change and review both the hosted-control policy and its validator.
This is a code-and-policy change, not an operator exception.
A fork release without GitHub attestations has lower assurance.
It must still create all Sigstore signatures.

GitHub attestations do not replace Sigstore signatures.
They add GitHub policy data and subject lookup.

## Verify the image

Select the exact release tag.
Download these three files from that release:

- `workcell-image.digest`
- `SHA256SUMS`
- `SHA256SUMS.sigstore.json`

The `v1.0.2` GHCR package denied anonymous access during the 2026-08-05 check.
Authenticate to GHCR with `read:packages` access before you verify that image.
See [GitHub container registry authentication](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry#authenticating-to-the-container-registry).

Run the complete procedure in one Bash subshell.
The subshell does not replace the current Docker configuration or exit trap.
When the prompt appears, enter a token that has package access.

```bash
(
  set -euo pipefail

  workcell_docker_config="$(mktemp -d)"
  export DOCKER_CONFIG="${workcell_docker_config}"
  trap 'rm -rf -- "${workcell_docker_config}"' EXIT

  read -r -p 'GitHub user: ' workcell_github_user
  read -r -s -p 'GHCR token: ' workcell_ghcr_token
  printf '\n'
  printf '%s' "${workcell_ghcr_token}" | docker login ghcr.io \
    --username "${workcell_github_user}" --password-stdin
  unset workcell_github_user

  tag=v1.0.2
  identity="https://github.com/omkhar/workcell/.github/workflows/release.yml@refs/tags/${tag}"

  cosign verify-blob SHA256SUMS \
    --bundle SHA256SUMS.sigstore.json \
    --certificate-identity "${identity}" \
    --certificate-oidc-issuer https://token.actions.githubusercontent.com

  expected="$(awk '$2 == "workcell-image.digest" {print $1}' SHA256SUMS)"
  actual="$(shasum -a 256 workcell-image.digest | awk '{print $1}')"
  test -n "${expected}" && test "${actual}" = "${expected}"

  image_ref="$(cat workcell-image.digest)"
  cosign verify "${image_ref}" \
    --certificate-identity "${identity}" \
    --certificate-oidc-issuer https://token.actions.githubusercontent.com

  GH_TOKEN="${workcell_ghcr_token}" gh attestation verify "oci://${image_ref}" \
    --repo omkhar/workcell \
    --cert-identity "${identity}" \
    --source-ref "refs/tags/${tag}" \
    --cert-oidc-issuer https://token.actions.githubusercontent.com

  unset workcell_ghcr_token
  docker logout ghcr.io
)
```

`docker login` stores the credential only in the temporary configuration.
The exit trap deletes that configuration after success or failure.
For a fork release without GitHub attestations, omit the `gh attestation verify` command.

## Verify release assets

Select the exact release tag and asset.
Download the asset and these two files from that release:

- `SHA256SUMS`
- `SHA256SUMS.sigstore.json`

Verify the checksum signature first:

```bash
tag=v1.0.2
asset=workcell-v1.0.2.tar.gz
identity="https://github.com/omkhar/workcell/.github/workflows/release.yml@refs/tags/${tag}"

cosign verify-blob SHA256SUMS \
  --bundle SHA256SUMS.sigstore.json \
  --certificate-identity "${identity}" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

On Linux, check the downloaded asset digest with `sha256sum`:

```bash
expected="$(awk -v name="${asset}" '$2 == name {print $1}' SHA256SUMS)"
actual="$(sha256sum "${asset}" | awk '{print $1}')"
test -n "${expected}" && test "${actual}" = "${expected}"
```

On macOS, check one asset with `shasum`:

```bash
expected="$(awk -v name="${asset}" '$2 == name {print $1}' SHA256SUMS)"
actual="$(shasum -a 256 "${asset}" | awk '{print $1}')"
test -n "${expected}" && test "${actual}" = "${expected}"
```

The command fails if the asset has no signed checksum entry.
It also fails if the computed digest does not match.

Use this optional GitHub check for a downloaded source bundle:

```bash
gh attestation verify "${asset}" \
  --repo omkhar/workcell \
  --cert-identity "${identity}" \
  --source-ref "refs/tags/${tag}" \
  --cert-oidc-issuer https://token.actions.githubusercontent.com
```

The verified installer runs the Cosign and digest checks before extraction.
Add `--attestation` to require the GitHub check.

The shipped installer pins the repository workflow but accepts any release tag identity.
Use the manual procedure above when you require exact-tag certificate binding.

## SLSA v1.0 Build-track gap analysis

[SLSA v1.0](https://slsa.dev/spec/v1.0/levels) defines Build levels L1 through L3.
It does not define a Source track.

For releases with GitHub attestations, Workcell claims Build L2 for the eight build-provenance subjects.
Workcell does not claim a Build level for other release files or for releases that disable GitHub attestations.
Workcell does not claim Build L3.

Reproducibility and hermeticity do not set a SLSA v1.0 Build level.
They are separate build properties.

### Build L1 — provenance exists

| Requirement | Status | Workcell evidence |
| --- | --- | --- |
| Provenance describes the build | Met | BuildKit creates image provenance. GitHub creates build-provenance predicates for eight subjects. |
| The build process is consistent | Met | One tag workflow uses pinned actions, images, toolchains, and provider inputs. |
| Consumers can get provenance | Met | GHCR stores image attestations. GitHub stores artifact attestations for release files. |

### Build L2 — hosted platform, authentic provenance

| Requirement | Status | Workcell evidence |
| --- | --- | --- |
| A hosted platform runs the build | Met | [GitHub-hosted runners](https://docs.github.com/en/actions/reference/runners/github-hosted-runners) run all release build and publish jobs. |
| A signature ties provenance to that platform | Met | GitHub OIDC and Fulcio bind signatures to the release workflow. |
| Downstream checks validate provenance authenticity | Met | [GitHub artifact attestations](https://docs.github.com/en/enterprise-cloud@latest/actions/concepts/security/artifact-attestations) and documented commands pin the workflow identity. |

Build L2 is the highest level that Workcell claims for those eight subjects.

### Build L3 — hardened builds

SLSA v1.0 adds two build-platform controls at L3.

| Requirement | Status | Workcell evidence or gap |
| --- | --- | --- |
| One run cannot influence another run | Not established | GitHub documents a new virtual machine for each standard hosted job. This fact does not prove every SLSA isolation condition. |
| Build steps cannot access provenance signature material | Not met | Build and attestation steps share one job and its OIDC authority. |

These gaps prevent a Build L3 claim.
Move provenance authority outside user-defined build steps to close this gap.
Use a trusted builder that enforces this separation.

### Hermeticity

The image build is reproducible, pinned, and network-dependent.
It is not hermetic.

The build downloads these inputs:

- Debian packages from a pinned snapshot
- Node packages through `npm ci`
- provider archives

Repository checks validate provider archives with fixed digests.
They also validate the OpenSSL and CA certificate bootstrap packages.
APT validates Debian repository metadata.
Only the vendored Rust compile stage runs offline.

### Source-integrity note (two-person review, outside SLSA v1.0)

[SLSA v1.2 Source L4](https://slsa.dev/spec/v1.2/source-requirements#level-4-two-party-review) requires two-party review.
Workcell operates in single-maintainer mode and does not claim Source L4.

Workcell uses signed commits, signed tags, required checks, and public PR records.
These controls do not replace two-party review.

## Scope note

Release provenance describes published artifacts and their release workflow.
It does not prove the local macOS runtime boundary.

Local runtime assurance depends on the Workcell VM, container controls, validation, and operator actions.
The hosted install matrix covers only Apple Silicon `macos-15` and `macos-26`.
