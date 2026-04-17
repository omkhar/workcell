<!-- markdownlint-disable MD024 -->
# Changelog

This project follows a lightweight Keep a Changelog style.

Historical release details before this file existed remain available in GitHub
Releases.

## Unreleased

## v0.10.1 - 2026-04-17

### Changed

- enabled GitHub immutable releases at the repository level and cut the latest
  release under that hosted control so the current published release is
  immutable
- clarified the single-maintainer release runbook with literal public comment
  templates for release-PR provenance and release-environment self-review, plus
  the documented recovery path when an earlier published release was mutable

## v0.10.0 - 2026-04-17

### Added

- expanded host-side session control and observability on the shipped session
  plane, including detached `session start|attach|send|stop`, richer
  `session list` status/control rendering, and terminal-session cleanup through
  `session delete`
- explicit local-first rollout and provider-auth maturity docs for the current
  Apple Silicon macOS support boundary and single-maintainer release model

### Changed

- restore `workspace-write` as the managed Codex sandbox while keeping
  `danger-full-access` reserved for breakglass-style paths
- tighten the provider-bump policy so reviewed holdbacks can cap Claude to a
  known-good stable version while other upstream pins continue to refresh

### Fixed

- prevent terminal resize from aborting transcript-capture launches and from
  crashing attached interactive sessions through Docker's embedded `tini`
  forwarding path
- harden host and runtime boundaries against the reviewed security finding set,
  including control-plane manifest override, provider-directory overwrite,
  validation-snapshot and build-manifest symlink escapes, shared GitHub
  credential scoping, trusted-Docker home spoofing, and alignment of the
  tracked hosted-controls policy with the live repository ruleset
- pin Claude CLI to `2.1.104` while the known defect in `2.1.105+` remains out
  of policy

## v0.9.3 - 2026-04-14

### Changed

- release: refresh the reviewed upstream provider pins to Claude CLI `2.1.107`
  and Gemini CLI `0.37.2` so tagged release preflight matches the latest
  reviewed track after the `v0.9.2` release attempt
- release: advance the pinned Debian snapshot to `20260415T000000Z` across the
  runtime and validator images so release-time package resolution stays aligned
  with the current reviewed archive snapshot

## v0.9.2 - 2026-04-14

### Fixed

- scripts: stop `scripts/workcell` from executing repo-local cached `hostutil`
  and `runtimeutil` binaries so workspace content cannot replace trusted host
  helpers during host-side publication and inspection flows
- scripts: harden host-side session git inspection against repository-defined
  filters, worktree config, and include redirection so git status and diff
  reads cannot trigger workspace-controlled command execution
- ci: restore the required `Reproducible build` status context on pull requests
  and `main` merges after the v0.9.1 workflow simplification

## v0.9.1 - 2026-04-14

### Changed

- container: decouple adapter and script COPY layers from the Rust build step so
  non-Rust changes no longer invalidate the 90–180 s cargo compile cache
- container: copy provider npm artifacts directly between build stages instead of
  creating and decompressing a tar archive on every container start
- container: fix apt retry loop so `apt-get` cache is only cleared on failure,
  not before every attempt; remove the post-success `npm cache clean` that
  discarded reusable BuildKit layer cache
- container: scope the `find /` SOURCE\_DATE\_EPOCH normalisation pass to
  directories written during the build instead of walking the entire image
  filesystem
- container: add `.dockerignore` to exclude `.git`, logs, and node\_modules from
  the build context
- mutation tests: run all Go and Rust mutation cases in parallel, cutting
  total mutation test wall time proportionally to available CPU cores
- unit tests: add `t.Parallel()` to all top-level test functions in
  `authpolicy`, `hostutil`, `authresolve`, and `metadatautil` packages; fix
  `canonicalizeForTest` helper to use goroutine-safe `t.Setenv` instead of
  `os.Setenv`
- scenario tests: pre-build `workcell-hostutil` before running scenario tests so
  `scripts/workcell` skips repeated `go run` overhead; run all secretless
  scenarios in parallel
- ci: enable GHA layer cache for the validator image build in `ci.yml` and
  `docs.yml` via `docker buildx build --cache-from/--cache-to type=gha`
- ci: remove the no-op `reproducible-build` aggregator job from `ci.yml`
- scripts: extract common sanitized-entrypoint preamble into
  `scripts/lib/trusted-entrypoint.sh` and update the three
  `verify-upstream-*-release.sh` scripts to source it
- runtime: consolidate duplicate `emit_session_assurance_notice()` into
  `runtime/container/assurance.sh`; remove from `entrypoint.sh`,
  `provider-wrapper.sh`, and `development-wrapper.sh`

## v0.9.0 - 2026-04-14

### Added

- host-side `workcell policy show|validate|diff` commands and `workcell why`
  so operators can inspect merged credential policy decisions without launching
  a session
- `workcell session diff` with recorded git launch metadata for reviewable
  session output against the session start point
- interactive launch spinner support with text heartbeat fallback plus a
  user-facing `--no-spinner` override

### Changed

- expanded the roadmap and delivery planning docs around `Implement first` and
  `Implement next` priorities for supervisor, policy, auth, and observability
- surfaced richer host-side auth readiness and selection reporting in
  `workcell auth status` and launch-time `--auth-status`
- synced the man page, policy docs, and scenario coverage with the current
  session, policy, and launch UX surfaces

### Fixed

- aligned `workcell why` with launch and status credential scoping so
  out-of-scope credentials are never reported as selected
- failed closed on `workcell session diff` when the source session launched
  from a dirty git workspace
- replaced session-record rewrites with atomic write-and-rename handling

## v0.8.1 - 2026-04-13

### Changed

- normalized Go package documentation, usage text, and validator error strings
  for current stable Go tooling
- removed dead helper paths from auth, injection, policy, and metadata code so
  static analysis stays actionable

### Fixed

- kept host-only interactive TTY session-list flows from emitting terminal reset
  escapes before the terminal state is actually dirtied
- added a TTY regression check for `workcell session list` and aligned scenario
  and auth-resolution tests with the normalized error surface

## v0.8.0 - 2026-04-13

### Added

- a named unprivileged `workcell` default user for the runtime, validator, and
  remote-validator images
- invariant coverage that keeps repo-mounted validator callers on explicit
  caller UID/GID mappings with isolated writable home, cache, and tmp roots

### Changed

- completed the unprivileged-user rollout across CI, release preflight,
  docs-only validation, local pre-merge, local `build-and-test.sh --docker`
  validation, reproducibility, and release-bundle verification paths
- made remote heavy validation explicitly run the helper container as the
  remote login UID/GID, with a mapped Docker socket group and a read-only
  mounted host-home snapshot for trusted Docker client state
- clarified the roadmap around explicit non-macOS deployment planning without
  claiming host-platform parity

### Fixed

- closed secret-file TOCTOU validation races by opening reviewed files before
  stat-based ownership and mode checks
- failed closed on persisted runtime assurance writes and replaced predictable
  temp-file patterns with `mktemp` plus atomic writes
- kept repo-mounted validator lanes nonroot for passwd-less caller UIDs by
  synthesizing an isolated writable home instead of falling back to `/`
- removed repeated protected-runtime signature stats from the exec guard fast
  path with cached lookup state

## v0.7.2 - 2026-04-11

### Changed

- hardened runtime validation and audit handling in the managed path and its
  release-facing checks

## v0.7.1 - 2026-04-10

### Added

- review-gated hosted-control policy for the public release environment
- fuzz coverage for the validation toolchain and pinned markdownlint installs

### Changed

- kept validator markdownlint available at runtime for repo validation
- made host-side session validation failures deterministic

### Fixed

- hardened the `development` wrapper and validation gates against trust-widening
  execution paths

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

<!-- markdownlint-enable MD024 -->
