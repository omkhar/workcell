# Install lifecycle evidence

Workcell checks install, update, rollback, uninstall, and cleanup operations.
This page separates automated evidence from live operator evidence.

Automated evidence runs without special hardware or repository secrets.
Live operator evidence needs a published release, Apple Silicon, or a local runtime.

## Test matrix

The standard validation lane runs on `ubuntu-latest`.
It checks Go tests and the required repository scenarios.

The CI workflow also runs install checks on these GitHub-hosted Apple Silicon runners:

- `macos-15`
- `macos-26`

The release workflow uses the same macOS runner matrix.
Each macOS job checks the bundle installer, the uninstaller, and the Homebrew formula.

## Evidence by operation

| Operation | Evidence | Limit |
| --- | --- | --- |
| Install from a local bundle | The macOS jobs check the launcher link, man-page link, Homebrew install, and Homebrew uninstall. | These jobs do not verify a downloaded release signature. |
| Install a verified release | [`install_release_e2e_test.go`](../internal/testkit/install_release_e2e_test.go) checks download, verification, extraction, and installer handoff. | The test uses local `curl` and `cosign` fixtures. |
| Verify a release asset | [`release_verify_test.go`](../internal/testkit/release_verify_test.go) checks missing tools, missing files, invalid signatures, and digest mismatches. | A live release check still needs published Sigstore data. |
| Update an install | [`test-install-lifecycle.sh`](../tests/scenarios/shared/test-install-lifecycle.sh) checks that a new install replaces the launcher link. | The test uses local source trees. |
| Roll back an install | The same scenario installs the earlier tree again and checks the launcher link. | The test uses local source trees. |
| Uninstall | The CI and release macOS matrices run the uninstaller without options. They confirm removal of the launcher and man-page links. | The jobs do not seed other Workcell cleanup targets or confirm their removal. These targets include state, profiles, caches, token handoff files, and cleanup scratch. |
| Check cleanup rules | The install scenario checks `cleanup_workcell_temp_root` with an isolated root. | This check does not run host-wide cleanup. |
| Run host-wide cleanup | A live `workcell --gc` run checks real Workcell state roots. | First, preserve all Workcell state and evidence that must remain. |
| Read stored data | Session tests read version 1 records and reject an unknown version. | Workcell has not shipped a data migration. |
| Start a runtime session | The strict and compatibility launch-smoke scenarios check real containers. | GitHub-hosted macOS runners do not provide the required nested virtualization. |

## Stored-format compatibility evidence

The install and update evidence on this page covers two versioned host formats:

- Session records use `version = 1`.
- The injection policy uses `version = 1`.

Only version 1 has shipped.
The current binary reads version 1 and rejects an unknown version.
No released Workcell version needs a format migration.

This list is not a complete inventory of versioned Workcell state.
For example, signed audit records use a separate version 1 seal sidecar.
Session verification checks that sidecar.
See [signed session audit records](signed-session-audit-records.md).

Installed updates do not read build manifests.
Validation reads those manifests during builds and repository checks.

## Cleanup safety

Preserve incident evidence before you run `workcell --gc` or the uninstaller.
Follow the [incident response runbook](incident-response.md) for a suspected boundary failure.

For a Homebrew formula install, remove the formula first:

```bash
brew uninstall workcell
```

Then use an extracted release bundle or a source checkout.
From its root, preview every runtime-state removal target:

```bash
./scripts/uninstall.sh --dry-run
```

Review each path before you run `./scripts/uninstall.sh` without `--dry-run`.
The uninstaller removes these Workcell-owned items:

- launcher and man-page links
- `~/.local/state/workcell`
- managed Colima profiles and related data
- Workcell caches and token handoff data
- Workcell temporary files

The uninstaller preserves `~/.config/workcell` and user-specified log files.
It also preserves shared host packages and unrelated Colima profiles.

`workcell --gc` removes stale cache, temporary, session-audit, and runtime state.
It does not provide a dry-run option.
It can remove Workcell state from another local run.
Do not run host-wide cleanup as a repository scenario.

An install, update, or rollback does not restore deleted state.

## Published v1.0.2 record

Release `v1.0.2` is the first published stable Workcell 1.0 release.
The `Release` workflow run `30974305124` completed successfully on 2026-08-05.

That run completed these install jobs:

- `Release install verification (macos-15)`
- `Release install verification (macos-26)`

The workflow uploaded 18 assets to the immutable release.
Each uploaded asset has a GitHub SHA-256 digest.
The release page lists 21 entries because GitHub adds two source-code archives and one release-attestation entry.

On 2026-08-05, the maintainer ran the shipped verified installer against `v1.0.2`.
The installer source matched the `v1.0.2` tag.
The run used an isolated home and `--no-install-deps`.

The live run proved these results:

- Cosign verified `SHA256SUMS` against the release workflow identity.
- The bundle digest matched the signed value `c2a34aa1cf2c2119633cd0f04fc0470520ac44d1e22e1d74f409ff696fc38d1f`.
- `gh attestation verify` accepted the source-bundle attestation.
- The installer created the launcher and man-page links.

The installed launcher reported `workcell v1.0.1`.
This output does not match the `v1.0.2` release tag.
The `v1.0.2` source bundle starts its changelog at `v1.0.1`.
The launcher reads its version from that first changelog entry.

Treat this result as a shipped version output defect.
Do not use `workcell --version` to prove the `v1.0.2` bundle tag.
Use the release tag, signed checksum, and verified bundle digest for that proof.

## Consumer-verification gap

The recommended release install verifies the bundle before extraction.
Verification is not mandatory for every install path.

An operator can run `install.sh` from a source tree or a manually downloaded bundle without signature verification.
Also, the release does not publish `install-release.sh` as a separate asset.
The operator must get that script from the repository.

See [CI/CD threat model](ci-threat-model.md) for the tracked risk.
