# Changelog

This project follows a lightweight Keep a Changelog style.

Historical release details before this file existed remain available in GitHub
Releases.

## Unreleased

## v0.7.0 - 2026-04-09

### Added

- project governance, maintainer, conduct, support, roadmap, and changelog docs
- a contributor bootstrap script
- Homebrew formula generation for versioned release assets
- continuous macOS install and uninstall verification on GitHub-hosted
  `macos-26` and `macos-15` Apple Silicon runners
- an automated upstream refresh workflow for provider pins, Linux base images,
  toolchains, and release-build inputs
- direct signing and attestation coverage for release formulas, manifests,
  checksums, SBOMs, and image digest metadata

### Changed

- standardized onboarding around `workcell auth init|set|status`
- simplified the README and getting-started flow around user outcomes and first-run success
- normalized CLI examples around the flag-based public form
- limited supported macOS hosts to Apple Silicon and documented the tested host
  matrix explicitly
- made the manual installer dependency-aware with an opt-out flag that skips
  package installs and reports missing requirements at the end
- replaced Ruby-based Colima profile validation with a repo-owned Go helper
- wired CI and release workflows to verify bundle and Homebrew install and
  uninstall paths before publication
- refreshed provider pins and release toolchain inputs to the current upstream
  stable set for this release

### Fixed

- removed stale Python bootstrap and parity paths that no longer backed any
  supported runtime or validation flow
- eliminated the runtime build dependency on live `rustup-init` downloads by
  copying a pinned Rust toolchain image into the container build
