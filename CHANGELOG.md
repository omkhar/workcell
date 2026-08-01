<!-- markdownlint-disable MD024 -->
# Changelog

This project follows a lightweight Keep a Changelog style.

Historical release details before this file existed remain available in GitHub
Releases.

## Unreleased

## v1.0.0-rc.2 - 2026-07-12

Supersedes v1.0.0-rc.1, which was tagged but never published: its release
workflow failed in the new arm64 Copilot runtime-image probe, whose build
requested a manifest-list output (`BUILDKIT_MULTI_PLATFORM=1`) that the
`--load` docker exporter rejects on the buildx `v0.35.0` pin, and release
preflight then failed closed on that check. This release fixes the probe and
carries the full v1.0.0-rc.1 content below.

### Fixed

- build the release-time arm64 Copilot help-probe image as a plain
  single-platform image so it can be loaded and probed; the probe never
  compared digests against the reproducible-build manifest, so manifest-list
  packaging was never needed there.

## v1.0.0-rc.1 - 2026-07-09

This entry reconstructs the v0.12 through v0.15 milestone train plus the 1.0
release-candidate gate work in one pass: those milestones shipped only as
main progression, with no intermediate tags, so no fake per-version headings
are recorded below. Grouped by user- and operator-facing theme, not
chronology.

### Added

- add `workcell session verify --id`, which recomputes the session's audit
  hash-chain from a per-host signing key, rejects duplicate-key and reordered
  or dropped entries, and fails closed (exit `0`/`1`/`2`); session-stop now
  signs the finalized audit chain automatically, and the trust model
  (host-signed, not host-root re-signed) is documented.
- add `workcell session export --format ocsf`, an OCSF-mapped (Application
  Lifecycle, class `6002`) export of session and audit-record events as JSON
  Lines, with per-writer audit-line decoding, duplicate-key rejection, a
  known-field allowlist for audit keys, and hard redaction of free-form
  operator messages.
- deny repo-defined MCP server configuration (`.mcp.json`,
  `.github/mcp.json`) by default in `strict` mode; a committed config is
  neutralized before it reaches the provider unless the operator opts in
  with a dated acknowledgement token.
- publish `policy/hardening-profile.toml`, a reviewed artifact recording the
  container hardening posture (capability drops, read-only rootfs, tmpfs
  mount flags, egress inventory) with a conformance check that fails CI on
  drift from the shipped launcher.
- add a verified consumer install path: `install-release.sh` now verifies
  the cosign signature and digest of a downloaded release bundle before
  extraction, refusing a tampered or unsigned bundle by default; an explicit
  offline bypass requires two separate flags and prints a loud warning.
- add `workcell support-bundle`, which collects operator-facing diagnostics
  with documented redaction rules; add an operator incident-response
  runbook for suspected boundary breaches (agent escape, credential
  exposure, workspace tamper) that walks detect, contain, preserve,
  collect, and report using existing tooling.
- add `workcell session list --parallel`, which groups concurrent sessions
  by their shared source repository and renders each sibling's isolated
  worktree, branch, and container, plus a scenario proof that same-repo
  parallel sessions get distinct clone paths, branches, and containers with
  no visible cross-writes.
- evaluate an Apple `container` backend (`local_vm/apple-container`,
  macOS 26+): go/no-go recorded as GO on the evaluation only; the backend
  ships preview-only and support-invisible, Colima remains the default and
  the only supported backend below macOS 26, and promotion to a supported
  backend is deferred post-1.0 pending a funded real-boundary certification
  lane.
- add a session-start latency benchmarking harness (cold/warm/cache-hit,
  median/p90/stddev) with a reproducible dry-run mode; live measurements are
  deferred to local-operator certification and are not fabricated.
- add a public contract inventory and drift check, a support-tier legend,
  docs link/orphan CI checks, an OWASP Agentic Top-10 control mapping, a
  SLSA v1.0 Build-track gap analysis, a CI/CD threat model, a standards
  watchlist, and a unified exit-code contract across the CLI surface.
- add a scheduled mutation-score CI lane with a published baseline and
  regression gate, a reviewed GitHub Actions allowlist, and centralized CI
  tool-pin policy.
- add Go fuzz targets across parser and injection-boundary code plus Rust
  `cargo-fuzz` targets for the exec-guard classifiers, wired into a
  scheduled fuzz lane; add exec-guard syscall-shim performance baselines.
- add a fail-closed GitHub Copilot Tier 1 adapter and a fail-closed
  Antigravity adapter scaffold.
- add `workcell --version`, which prints the first released version heading
  from `CHANGELOG.md` using the same rule as the support bundle (so the two
  version sources cannot disagree); it reports semver pre-release suffixes
  such as `v1.0.0-rc.1` rather than overstating a final release (hyphenated
  pre-release identifiers included), works toolchain-free as the bare first
  argument on hosts without a Go toolchain, is honored after other global
  options on provisioned hosts and through the debug-wrapper install, and
  prints `unknown` rather than failing when no heading is present.

### Changed

- migrate the `verify-invariants.sh` static invariants (thousands of checks
  across host, runtime, provider, and policy boundaries) from bash into a
  typed Go validator suite (`internal/workcellhardening` and related
  packages); the residual left in bash is provably non-static (real-repo
  state, not fixed patterns) and is recorded as an intentional scope
  boundary, not an oversight.
- modularize the 8,900-line launcher script: extract host detection, trusted
  host-command execution, Go/Colima host-utility wrappers, and
  egress-endpoint assembly into `scripts/lib/launcher/`, and document the
  full contract (required tools, environment expectations, exit codes, test
  override flags) in `docs/launcher-contract.md`.
- split the oversized pinned-input validator (`internal/metadatautil`) into
  per-ecosystem files for GitHub Actions workflows, Node/npm, and Docker;
  the remaining Rust and Python validators stay inline as an accepted,
  non-oversized remainder.
- extract the git-policy module from the Rust interception library's
  monolithic `lib.rs` into its own file, with the shim's exported symbol set
  and behavior byte-identical; further module splits are follow-up work.
- add property-based (randomized-lifecycle) tests for the session lifecycle
  covering encode/decode round-tripping, terminal-status monotonicity, and
  log-injection defenses, using the standard library's `testing/quick`
  rather than a new dependency.
- restructure the README into tiered documentation entry points, add
  maintained architecture diagrams, document the injection-policy schema
  with per-provider examples, and deepen the contributor runbook and
  adapter READMEs.
- split regex-kind and fixed-string-kind check patterns onto separate
  fields in `internal/workcellhardening` so literal patterns (such as a
  pinned hostname) can never be misidentified as an unanchored regular
  expression; closes all 10 open CodeQL hostname-regex alerts with no
  suppressions and no behavior change.
- amend the 1.0 release criteria: defer dual-control release approval (B2,
  no second trusted maintainer), the funded third-party boundary audit and
  OpenSSF Best Practices badge (B7), the automated real-boundary
  certification lane (B6, no runner funding), and the adoption growth kit
  and rendered docs site (E6) to post-1.0. 1.0 instead relies on documented
  single-maintainer release controls and local-operator certification of
  both the strict Colima and compat Docker Desktop boundaries on the
  maintainer host; each deferral is recorded with its tradeoff.
- refresh pinned upstream inputs (toolchains, base images, provider pins) to
  the newest reviewed versions; at rc.1 cut the reviewed pins are Codex CLI
  `0.144.1`, Claude CLI `2.1.207`, Copilot CLI `1.0.70`, Gemini CLI `0.50.0`,
  Go `1.26.5`, and Rust `1.97.0`, with sigstore-verified provider tarball
  digests updated in lockstep.

### Fixed

- harden security pins and direct mount staging, keep Colima staging cache
  mounts correct, and keep pin hygiene green when the upstream Rust Docker
  tag lags.
- heal an audit-log append boundary that could produce a torn write, and
  recover a write-only audit log on the read path.
- pin the scheduled Rust fuzz lane to a dated nightly toolchain instead of
  the moving `nightly` alias, so an upstream nightly release can no longer
  silently change fuzz coverage or break the lane.
- make the upstream-pins refresh helpers replace files atomically while
  preserving their modes: rewriting a pinned script no longer strips its
  executable bit (which shipped spurious mode-only diffs and broke the
  refresh-PR parity run), and an interrupted write can no longer leave a
  truncated target behind; also serialize the `--version` testkit fixtures
  to remove an ETXTBSY flake.

### Documentation

- document the day-two install lifecycle (install, upgrade-in-place,
  rollback, `--gc`, uninstall) with an explicit CI-automatable versus
  local-operator-certification split, backed by an offline end-to-end test
  of `install-release.sh` against a fixture release.
- document the OCSF-mapped session export in the enterprise evidence
  baseline, the Rust/Go/shell language-boundary doctrine, and the
  syscall/filesystem hardening profile referenced above.

### Security

- close a boundary/exfiltration gap where a committed `.mcp.json` could
  reach the agent provider even in `strict` mode.
- add signed, tamper-evident session audit records: a per-host P-256 signing
  key is generated and hardened under an owner-secured, symlink-resistant
  directory, and audit-chain verification detects offline tampering,
  reordering, drops, and duplicate-key injection; it does not protect
  against host-root re-signing, which is documented as an explicit
  boundary of the trust model.
- close the installer-side supply-chain gap: an unsigned or tampered
  release bundle is refused before its own installer ever runs.
- harden the Codex adapter to a deny-by-default posture: managed sessions
  now pass Codex subcommands through an explicit allowlist (with `debug`
  denied wholesale because its second-level subcommands are not read-only),
  guard every sandbox/profile/approval/cd value-flag in all clap-accepted
  spellings (space-separated, `--flag=value`, glued short `-sVALUE`, and
  short-with-equals `-s=VALUE`), and enforce a version-stamped subcommand
  fixture so a Codex upstream bump cannot silently introduce an
  unclassified subcommand.
- deny the new Claude prompt-override and plugin surfaces that ship with
  the 2.1.207 pin: `--append-subagent-system-prompt`, the file-based
  `--system-prompt-file`/`--append-system-prompt-file` variants,
  `--plugin-url` session plugin fetches, `--agents` inline agent
  definitions, and the kebab-case `--allowed-tools` alias of the denied
  `--allowedTools` pre-approval flag are all blocked in managed sessions,
  each proven by a container-smoke denial assertion against the pinned
  binary; the Copilot 1.0.70 session-local `--sandbox`/`--no-sandbox`
  toggles are likewise denied so sandbox posture can only come from host
  policy.

## v0.11.2 - 2026-06-15

Supersedes v0.11.1, which was tagged but never published: its release-preflight
mutation gate failed because the mutation-test harness anchors still pointed at
pre-split source locations. This patch repoints them and carries the full
v0.11.0/v0.11.1 content below.

### Fixed

- repoint the `internal/mutation` harness source anchors to the current
  injection/metadatautil layout: `validateAllowedKeys`, `targetIsReserved`,
  and the secret-permission check moved into `render_credentials.go`,
  `render_documents_copies.go`, and `render_validation.go` during the injection
  package split, and the operator-contract evidence check was hoisted — so the
  release-preflight mutation gate matches the shipped source again.

## v0.11.1 - 2026-06-15

Supersedes v0.11.0, which was tagged but never published: its release-preflight
aborted on the pin-freshness gate because the latest reviewed track had moved on.
This patch refreshes those pins and re-cuts the release; it carries the full
v0.11.0 content below plus the refresh.

### Changed

- bump Claude Code to `2.1.177` (from the v0.11.0 `2.1.175` pin) and refresh the
  rust runtime toolchain image to the current reviewed `rust:1.96.0-slim-trixie`
  digest, so the release matches the latest reviewed upstream track.

## v0.11.0 - 2026-06-15

### Added

- upgrade the Codex CLI to `0.139.0`, switch to the musl static artifact, and
  migrate managed Codex configuration to the profile-v2 layered model (a base
  `config.toml` plus per-profile `strict`/`development`/`build`/`breakglass`
  `<name>.config.toml` files applied with `--profile`), with the managed GUI
  app-server sandbox mode injected by the provider wrapper.
- bump Claude Code to `2.1.175` and Gemini CLI to `0.46.0`.
- migrate the runtime and validator base images to Node 24 LTS.
- add a fail-closed Copilot adapter scaffold and distro-scoped host
  support-matrix fields.
- build the native arm64 release image on a native runner and split the
  release preflight into parallel amd64-reproducibility and container-smoke
  jobs.

### Changed

- thin the public shell entrypoints and internal surfaces through a large set
  of behavior-preserving refactors across the Codex provider wrapper, the
  provider-policy argument validators, and the `metadatautil`, `authpolicy`,
  `authresolve`, `injection`, `sessionctl`, `publishpr`, host `launcher`, and
  `transcript` packages.

### Fixed

- close the glued `-c` Codex hook-bypass gap and align the nested-CLI rules.
- fail the release gate closed on unregistered or stale required `main` checks.
- enforce BuildKit/buildx pin parity across `docs.yml` and the validator-image
  fallback.
- harden host and runtime boundary checks, including physical remote-VM
  symlink resolution and `0600` rewritten-manifest permissions.

### Documentation

- document the exec guard's process-linkage model and noexec hardening, record
  the reviewed auth-input-scoped Gemini posture (Code Assist Standard/Enterprise
  licenses and paid API keys; upstream retires only the personal-account OAuth
  login), make the quickstart verify release assets, and correct CI trigger
  wording.

## v0.10.7 - 2026-05-29

### Added

- add a repository readiness gate for maintainer-facing workflow hardening.

### Changed

- remove dead bash and Go helpers and dedup runtime wrappers, execveat loader
  classification, registry CA blocks, and `publishpr`/`hostutil`/`providerid`
  helpers across many internal surfaces.
- extract the shared `PolicySource` type into `internal/injectionpolicy`.

### Fixed

- harden runtime image fetch retries and build recovery, keep GitHub API auth
  host-bound, preserve refresh publication validation, keep Debian refreshes
  bootstrap-buildable, resolve Colima egress hosts inside the VM, fail closed
  on snapshot TLS bootstrap errors, allow Docker CloudFront blobs in bootstrap
  policy, and fetch actionlint checksums through the asset API.

## v0.10.6 - 2026-05-18

### Added

- record Phase 10 through Phase 12 execution with a managed-workstation
  contract, enterprise evidence baseline, host-expansion readiness gate, and
  requirements traceability without adding new support claims.
- add explicit forbidden-host-path policy data, control-plane lockstep
  validation, release publication gates, pinned-input checks, and fuzz coverage
  for parser and injection security boundaries.
- add internal Go packages for session control, auth/policy commands,
  injection preparation, publication helpers, host launch state, workflow
  validation, and release metadata so public shell entrypoints stay thinner.

### Changed

- add a repo-local quality loop for roadmap and contract work so changes keep
  code, docs, validation, and support claims tight before review completion.
- refresh release-time upstream pins for Gemini CLI `0.42.0`, Buildx
  `v0.34.0`, zizmor `1.25.2`, zizmor-action `v0.5.6`, the
  `20260518T000000Z` Debian snapshot, and current reviewed runtime and
  validator base-image digests.
- collapse internal helper binaries behind fewer reviewed host utilities,
  including the `workcell-citools` metadata/workflow validation surface and
  renamed tree-compare tooling.
- tighten roadmap, rollout, release, provenance, and workflow documentation
  around the current single-maintainer release model and host-support boundary.

### Fixed

- preserve host-side publication signing environment, signed-range checks, and
  release preflight behavior across the repo-owned `publish-pr` path.
- reject colliding credential tables, intermediate-symlink traversal, malformed
  policy TOML, and other reviewed host/runtime boundary regressions with
  focused tests.
- keep session and launcher helper exit contracts stable after the Go helper
  migrations, including Bash 3-compatible invariant parsing and Colima helper
  trailer recovery.
- keep Docker Desktop `compat` launch validation target-aware so the lower
  assurance path requires seccomp without inheriting the strict Colima
  AppArmor/SELinux daemon check.
- harden hosted workflow validation, provider-bump status checks, release
  attestation handling, and CI timeouts so release and review lanes fail closed.

## v0.10.5 - 2026-04-25

### Fixed

- allow protected `v*` release tags to enter the dedicated
  `hosted-controls-audit` environment so release preflight can run
  hosted-controls verification without bypassing the environment secret gate.

## v0.10.4 - 2026-04-25

### Added

- add the canonical remote-VM contract harness and planning docs for target
  backend expansion
- add Docker Desktop compatibility-target support with live certification
  evidence and enforce end-to-end certification before signing future
  support-claim commits
- add preview AWS EC2 SSM and GCP IAP remote-VM target backends, including
  support-matrix coverage, certification-only live smoke lanes, and operator
  setup guidance
- add the preview-only `remote_vm/gcp-vm/compat` backend with deterministic
  IAP broker-plan diagnostics, shared remote-VM conformance reuse, canonical
  support-matrix coverage, rollout docs, and a certification-only live smoke
  lane
- add local and hosted PR-parity gates, workflow lane manifests, lane-planning
  tooling, and publication wrappers so review branches exercise the same
  evidence expected by GitHub CI

### Changed

- refresh release-time upstream pins for Codex CLI `0.125.0`, Syft `v1.43.0`,
  the `20260425T000000Z` Debian snapshot, and current reviewed runtime and
  validator base-image digests
- rewrite the adoption roadmap around enterprise and open-source platform
  phases, prioritizing managed workstation discovery before the Azure raw-VM
  lane and moving cross-platform host support earlier in the plan
- clarify repo-local peer-review, PR lifecycle, release, and commit skills so
  review loops continue through fixes, validation, and re-review until no
  actionable findings remain
- replace direct upstream-refresh PR publication with candidate-only hosted
  output and a repo-local recreation path guarded by hosted-controls parity
- record the Phase 9 later-expansion decision to fund managed workstation
  contract and discovery next, while deferring `azure-vm` to the following raw
  `remote_vm` provider lane

### Fixed

- align release-facing docs, operator-contract evidence, and the pre-commit
  hook so local contract checks no longer diverge from `validate-repo`
- fix the repo-local PR publication wrapper on macOS Bash when no passthrough
  arguments are needed, preserving the parity-enforced release publication path
- allow the host-side publication helper to publish an existing clean signed
  branch so the release runbook's commit-before-publication flow works as
  documented
- bound Workcell cleanup and garbage-collection behavior so local validation,
  certification, and failed test runs remove owned residue instead of leaving
  stale temp roots, runtime cache, or build cache state
- canonicalize GCP dry-run host-tool detection and restore isolated Docker
  Desktop smoke workspaces used by certification validation
- remediate the reviewed security finding set across host/runtime boundaries
  and preserve one-off proof-backed closure evidence in the release scope

## v0.10.3 - 2026-04-17

### Changed

- add an explicit reviewed Claude provider-bump exception path so a known-good
  stable upstream fix can bypass the normal cool-off window without fighting
  release preflight
- bump the pinned Claude CLI from `2.1.104` to `2.1.108` because `2.1.108`
  fixes the defect that kept the repo on the earlier holdback
- default the release runbook to maintainer review-gated checkpoints before
  publish, merge, tag, release-environment approval, and final closeout while
  keeping the single-maintainer provenance path explicit

### Fixed

- stop scrubbed host-side Go helper launches from reusing a stale partial
  module cache under `/tmp`, which could make `workcell --agent claude` fail
  before launch with `no required module provides package golang.org/x/sys/unix`
- keep reviewed Claude `approved_version` exceptions from forcing a future
  downgrade once the pinned runtime has already moved past the exception
- add real macOS integration coverage that launches Workcell-managed Codex,
  Claude, and Gemini version probes and exercises the host detached-session
  control plane against live and terminal workloads, including
  `list|show|logs|timeline|export|attach|send|stop|delete`, so launcher and
  control-plane regressions fail earlier in release validation

## v0.10.2 - 2026-04-17

### Fixed

- publish GitHub release assets through a draft-first release flow so
  repository-level immutable releases can still receive the full artifact,
  signature, SBOM, and attestation set before the final release record is
  published

## v0.10.1 - 2026-04-17

### Changed

- enabled GitHub immutable releases at the repository level so `v0.10.1`
  publishes under that hosted control and becomes the current immutable release
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
