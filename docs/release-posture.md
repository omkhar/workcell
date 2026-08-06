# Release posture

The latest release is [`v1.0.2`](https://github.com/omkhar/workcell/releases/tag/v1.0.2).
It is the first published release in the 1.0 series.
GitHub reports this release as immutable.
GitHub also provides a SHA-256 digest for each release asset.

The release workflow rebuilds and verifies each tagged release before publication.
It does these checks:

- It runs repository validation, container smoke tests, and reproducibility tests.
- It runs repo-mounted validators and release helpers with an explicit caller user ID and group ID.
- It gives those processes separate writable home, cache, and temporary directories.
- It supports a caller user ID that has no password-file entry.
- It verifies the two current GitHub-hosted Apple Silicon macOS runner labels.
- It blocks publication when a reviewed provider, Linux image, toolchain, or release-build pin is out of date.
- It verifies the pinned Codex, Claude, Copilot, and Gemini releases against upstream metadata.
- It keeps Antigravity unsupported until that provider passes the same gate.

The preflight job records the expected digest for its source archive.
The release job creates and extracts an independent archive from the checked-out release tag.
It compares that archive digest with the expected digest.
It creates source-dependent manifests and the amd64 image from the extracted tree.
It creates the Homebrew formula from the verified archive digest.

The native arm64 image job builds from the checked-out release tag.
The workflow compares the published platform digests with the preflight data.

The hosted install jobs prove these properties on Apple Silicon `macos-26` and `macos-15`:

- bundle installation
- launcher-link removal
- man-page-link removal
- Homebrew installation
- formula removal

The hosted jobs do not prove complete bundle uninstall behavior.
The release gate tests installation on these two macOS versions only.
Other macOS versions can work, but tagged-release CI does not prove them.

The `release` environment gates image pushes and sealed-asset construction.
The `hosted-controls-audit` environment gates release preflight and final GitHub release publication.

The workflow uses Cosign to create keyless Sigstore signatures.
It signs the image, source archive, Homebrew formula, image-digest file, checksums, manifests, and software bills of materials.
It also creates GitHub attestations when the reviewed hosted controls permit them.
GitHub attestations are an additional verification surface.
They do not replace Sigstore signatures.

Forks can keep the GitHub attestation gates off.
The upstream repository audits those gates as hosted control-plane state.

See [provenance.md](provenance.md) and [github-workflows.md](github-workflows.md).
