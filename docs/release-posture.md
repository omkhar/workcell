# Release Posture

Tagged releases are rebuilt and verified before publication. The release path:

- reruns validation, smoke, and reproducibility checks
- reruns repo-mounted validator and release-helper paths under an explicit
  caller UID/GID with isolated writable home, cache, and tmp roots instead of
  relying on ambient container-root defaults, including passwd-less caller UIDs
- verifies from GitHub-owned sources that the release install matrix still
  targets the newest two GitHub-hosted Apple Silicon macOS runner labels
- refuses to publish if any reviewed provider, Linux base image, Linux
  toolchain, or release-build pin is behind the latest tracked upstream
- verifies pinned Codex, Claude, Copilot, and Gemini releases against upstream
  metadata as part of the reviewed provider set; Antigravity gets the same
  gate before any future support claim
- publishes from the archived source bundle rather than the live checkout
- gates publication on bundle and Homebrew install verification on
  GitHub-hosted Apple Silicon `macos-26` and `macos-15`
- signs the image, source bundle, Homebrew formula asset, published image
  digest file, checksums, build-input manifest, control-plane manifest,
  builder-environment manifest, and both SBOMs with keyless Sigstore/Cosign
- publishes GitHub-native attestations when the reviewed hosted controls say
  the repository visibility and GitHub plan support them for every published
  primary release artifact, as an additional verification surface rather than a
  replacement for Sigstore

That install matrix is the current release-gated support window. Other macOS
versions may work, but they are not currently proven by tagged-release CI.

Forks can keep the GitHub attestation gates off. The upstream repo treats
those settings as hosted control-plane state and audits them accordingly.

See [provenance.md](provenance.md) and
[github-workflows.md](github-workflows.md).
