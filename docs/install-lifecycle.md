# Install lifecycle proof

Workcell's day-two operations — install, upgrade-in-place, rollback, uninstall,
and `workcell --gc` — each need repeatable evidence that they behave correctly
and leave no orphaned Workcell-owned state. This page enumerates that evidence
set and, for each item, states plainly whether it is **CI-automatable** (proven
by GitHub-hosted runners or `go test`, with no special hardware or secrets) or
**local-operator certification** (needs real Apple Silicon hardware or a
genuinely published, cosign-signed release, and is recorded as operator
evidence rather than faked in CI).

The split is deliberate and honest: hosted runners can prove the non-container
install-lifecycle mechanics — download, verify, extract, link, PATH, uninstall,
the `--gc` cleanup contract, upgrade/rollback repointing, and config
compatibility — but they cannot prove a live containerized launch (that needs
Apple Silicon with nested virtualization for Colima/Docker), a unit test cannot
exercise a real keyless Sigstore signature (that needs GitHub OIDC and a
published release), and a live host-wide `--gc` is not safe to run in an
automated lane (it reaps the real host's `/tmp` and home cache roots — see the
remainder note).

## Release matrix

- Non-container mechanics run on the standard `validate` lane
  (`ubuntu-latest`) via `go test ./...` and `scripts/run-scenario-tests.sh
  --repo-required`.
- The `install-verification` lane
  ([`.github/workflows/ci.yml`](../.github/workflows/ci.yml)) exercises the
  bundle installer and Homebrew formula on the GitHub-hosted macOS matrix
  (`macos-26`, `macos-15`), mirrored at release time in
  [`.github/workflows/release.yml`](../.github/workflows/release.yml).

## Day-two evidence set

| Operation | What is proven | Evidence | Class |
| --- | --- | --- | --- |
| Install (plain bundle) | `install.sh` links the launcher and man page, the launcher runs, and the Homebrew formula installs/uninstalls | `install-verification` lane | CI-automatable |
| Verified install (`install-release.sh`) | download → `cosign`-verify fail-closed → extract → handoff to the bundle installer; a bad signature or a digest mismatch aborts **before** any bundle code runs | [`internal/testkit/install_release_e2e_test.go`](../internal/testkit/install_release_e2e_test.go) (offline fixture, stubbed `curl`/`cosign`) | CI-automatable for the orchestration + fail-closed logic; verification against a real published, cosign-signed release is the release/local remainder |
| Verifier internals | `verify-release-artifact.sh` rejects a tampered artifact, a bad signature, absent material, and absent `cosign`; an acknowledged `--skip-verify` returns a distinct unverified code | [`internal/testkit/release_verify_test.go`](../internal/testkit/release_verify_test.go) | CI-automatable |
| Upgrade-in-place | re-running the installer from a newer tree repoints the single launcher entry with no duplicate and no orphaned old link, and the new launcher runs | [`tests/scenarios/shared/test-install-lifecycle.sh`](../tests/scenarios/shared/test-install-lifecycle.sh) | CI-automatable |
| Rollback | re-installing the prior tree repoints the launcher back, proving the operation is symmetric and pins no state forward | same scenario | CI-automatable |
| Uninstall | the launcher and man symlinks are removed (no orphaned Workcell-owned install links); `~/.config/workcell` is preserved | `install-verification` lane (end to end on a hosted runner) | CI-automatable |
| `workcell --gc` cleanup contract | the reap logic removes Workcell-owned, pattern-matching stale scratch in a **given** root and preserves unrelated files | [`tests/scenarios/shared/test-install-lifecycle.sh`](../tests/scenarios/shared/test-install-lifecycle.sh) exercises `cleanup_workcell_temp_root` at the function level against an **injected sandbox root**; [`internal/workcellhardening`](../internal/workcellhardening) covers the surfaces `--gc` must reap | CI-automatable |
| `workcell --gc` end to end | a full live `--gc` cleans stale runtime/cache/temp state without harming user data | recorded operator run | local-operator certification (see safety note below) |
| Config/schema compatibility | the current binary reads a persisted version-1 session record, and fails closed on an unrecognized future version | [`internal/host/sessions/sessions_test.go`](../internal/host/sessions/sessions_test.go) | CI-automatable |
| Live launch | a containerized agent session actually launches on the release matrix | recorded launch evidence | local-operator certification (Apple Silicon) |

## Config and schema compatibility

An in-place upgrade only crosses a compatibility boundary if the newer binary
must read an on-disk format an older install wrote in an older shape. Workcell's
two runtime-persisted versioned formats are:

- **Session records** (`~/.local/state/workcell/.../sessions/*.json`) —
  `version` field, currently `1`
  ([`internal/host/sessions/sessions.go`](../internal/host/sessions/sessions.go)).
- **Injection policy** (`~/.config/workcell/injection-policy.toml`) — `version`
  field, currently `1`.

**No versioned on-disk format crosses a boundary yet:** only version 1 has ever
shipped, so an in-place upgrade to a newer binary reads the same version-1
records it writes — there is no older-shape-to-newer-binary migration to
perform today. Both formats validate strictly against their known version and
reject anything else, so the boundary is *fail-closed*, not silently
misinterpreted. Two tests pin this state as a contract:

- The current binary reads a persisted version-1 session record correctly (the
  forward-read baseline for upgrade).
- The current binary rejects a version-2 record with an explicit
  `unsupported session record version` error.

Together these ensure any future format bump must be a **deliberate, migrated,
and tested** step — a silent break or an accidental version bump fails CI. The
build-time-only formats (control-plane manifest, workflow-lane and provider-bump
manifests) are validated at build/CI time and are not read across a runtime
upgrade, so they are out of scope for day-two compatibility.

## Local-operator / published-release remainder

These are intentionally **not** faked in CI and are certified by the operator
with recorded evidence:

- **End-to-end `install-release.sh` against a genuinely published, cosign-signed
  release** over the network (Sigstore/Fulcio/Rekor). The fixture test proves
  the orchestration and fail-closed decisions offline; the real keyless
  signature check against a real release tag is certified at release time.
- **A live containerized launch** on Apple Silicon macOS (hosted runners lack
  the nested virtualization Colima/Docker needs).
- **A full live `workcell --gc`** across real host cache layouts. A live `--gc`
  cannot be safely automated in the repo-required scenario: it reaps the
  hard-coded `/tmp` scratch root and the passwd-derived real-home cache roots,
  and neither can be redirected to a sandbox — `resolve_workcell_real_home`
  prefers the passwd identity over `$HOME` (and rejects a mismatched
  `WORKCELL_DOCKER_REAL_HOME` override), and the `/tmp` root has no override. So
  a live `--gc` in CI could delete a developer's or runner's real Workcell
  temp/cache state. The scenario therefore proves the reap **contract** at the
  function level against an injected sandbox root, and the full live run is
  operator-certified. (`scripts/uninstall.sh` reaps `/tmp` the same way, which
  is why its end-to-end coverage lives in the hosted `install-verification`
  lane, not the repo-required scenario.)
- **A real cross-minor-version data migration**, which only becomes testable
  once a second on-disk format version ships.

### Certification record

Recorded local-operator certification on the maintainer host
(macOS 26.5.2, `arm64`; Colima Docker 29.2.1; Docker Desktop 29.5.3):

- **Verified install against the published, cosign-signed `v1.0.0-rc.2`
  release** (2026-07-13): `install-release.sh --version v1.0.0-rc.2
  --attestation` run end-to-end into an isolated `HOME` completed exit `0` — a
  single command that downloaded the real release bundle, keyless-verified it
  against the release signing identity (`Verified OK`, bundle
  `sha256:d1cc3bba197ab09b195ad6a98b674d79299d6c9eca2a151798127a9f0a83dba9`),
  passed the GitHub attestation gate that `--attestation` requires
  (`GitHub attestation verified`), then extracted and installed a `workcell`
  that reports `workcell v1.0.0-rc.2`. A **negative control** — the same bundle
  with appended bytes, checked via `verify-release-artifact.sh` — was rejected
  fail-closed with `digest mismatch` (the cosign signature on `SHA256SUMS`
  still verified; the tampered tarball digest did not match the signed value),
  confirming a tampered bundle cannot reach the installer.
- **Live `workcell --gc`** from the verified `v1.0.0-rc.2` bundle completed
  exit `0`, reporting the stale injection/session-audit/runtime-cache/
  build-cache/temp state it cleaned.
- **Live containerized launch on Apple Silicon** is certified by the two
  boundary launch-smoke scenarios run on this host in the same session:
  `tests/scenarios/shared/test-agent-launch-smoke.sh`
  (`macos/arm64/local_vm/colima/strict`) and
  `tests/scenarios/shared/test-docker-desktop-launch-smoke.sh`
  (`macos/arm64/local_compat/docker-desktop/compat`), both passing against the
  real runtime image on this hardware.

## Relationship to forced consumer verification (CI threat model gap 1)

[ci-threat-model.md](ci-threat-model.md) tracks, as known gap 1, that verified
install — now the *documented default* — is not yet *forced* across all
documented install paths. The end-to-end fixture proof above closes the
CI-automatable half of that gap's "exercise `install-release.sh` end to end"
requirement — a tampered or unsigned bundle cannot reach the bundle installer —
and the "against a real published release" half is now done (the Certification
record above: `install-release.sh --attestation` against the published
`v1.0.0-rc.2`). Fully closing the gap now requires only making verification the
*forced* default across all flows and shipping a standalone verified installer
bootstrap (so consumers need not clone the repo to obtain a trusted
`install-release.sh`).
