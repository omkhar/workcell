# Install

Workcell ships with a supported installer plus a tagged Homebrew formula asset.
For the shortest end-to-end path, see the
[5-minute path](../README.md#5-minute-path) in the README.

## Install options

### Verified release install (recommended)

`scripts/install-release.sh` is the fail-closed install path. It downloads a
tagged release bundle plus its signed `SHA256SUMS`, verifies the signature and
the bundle digest **before** any bundle code runs, and only then extracts the
bundle and hands off to its `scripts/install.sh`. Verifying before extraction is
what makes it sound: a tampered bundle is rejected before its (also-tampered)
installer could run.

```bash
# From a source checkout (or after placing install-release.sh and
# verify-release-artifact.sh together in a scripts/ directory):
./scripts/install-release.sh --version vX.Y.Z
```

Verification requires [`cosign`](https://github.com/sigstore/cosign) on `PATH`
(`brew install cosign`). It is keyless: the script checks that the release's
`SHA256SUMS` was signed by the Workcell release workflow's OIDC identity
(`.../.github/workflows/release.yml@refs/tags/…`, issuer
`https://token.actions.githubusercontent.com`) through Sigstore/Fulcio with a
Rekor transparency entry, then binds the downloaded bundle to its entry in that
verified `SHA256SUMS`. This automates the manual `cosign verify-blob` and
`sha256sum -c` steps in
[provenance.md](provenance.md#verifying-release-assets).

It **fails closed**: a missing `cosign`, absent verification material, a
signature that does not verify against the pinned identity, or a digest mismatch
each refuse the install with a non-zero exit and a clear error. Pass
`--attestation` to additionally require `gh attestation verify` against the
release workflow, and `--repo OWNER/REPO` to install from a fork or mirror.

Because this runs before the download is trusted, the installer pins its
interpreter to the absolute `/bin/bash` (a system bash at `/bin/bash` is
required — present on macOS and mainstream Linux) so a fake `bash` earlier in
`PATH` cannot hijack it, and it resolves the tools it calls (`cosign`, `gh`,
`curl`, `tar`, `sha256sum`) from a fixed trusted `PATH` (the system directories
plus `/usr/local/bin` and `/opt/homebrew/bin`) rather than your ambient `PATH`,
so a user-writable directory early in `PATH` cannot shadow them with a fake.
Invoke it as `./scripts/install-release.sh` (which uses that trusted
interpreter) — do not run it through an untrusted `bash …`. Install
`cosign`/`gh` in one of those standard locations (`brew install cosign gh` does
this). If yours live elsewhere, set `WORKCELL_INSTALL_TRUSTED_PATH` to a trusted
`PATH` that includes them.

#### Offline / air-gapped installs

If you cannot reach Sigstore/Rekor (an air-gapped host) you may bypass
verification, but only with an explicit acknowledgement:

```bash
./scripts/install-release.sh --version vX.Y.Z \
  --skip-verify --i-understand-unverified-install
```

This prints a loud warning and installs **without** checking provenance. Use it
only when you have already verified the bundle out of band (for example, you ran
`cosign verify-blob` / `sha256sum -c` on a connected host and transferred the
checked bundle). Without the acknowledgement flag, `--skip-verify` refuses to
run — the default is always verify-and-fail-closed.

### Tagged release bundle

If you prefer to verify manually, download a tagged release bundle, verify it
following [provenance.md](provenance.md#verifying-release-assets), unpack it, and
run the supported installer:

```bash
tar -xzf workcell-vX.Y.Z.tar.gz
cd workcell-vX.Y.Z
./scripts/install.sh
```

On Apple Silicon macOS, `./scripts/install.sh` installs only the missing
required Homebrew formulas (`colima`, `docker`, `gh`, `git`, `go`) before it
links the launcher. Use `./scripts/install.sh --no-install-deps` to leave the
system unchanged and get a warning summary of anything still missing.

### Tagged Homebrew formula asset

Tagged releases can publish a versioned `workcell.rb` asset. Download it from
the release page and install it locally with Homebrew:

```bash
curl -LO https://github.com/omkhar/workcell/releases/download/vX.Y.Z/workcell.rb
brew install --formula ./workcell.rb
```

The formula declares the same required host dependencies: `colima`, `docker`,
`gh`, `git`, and `go`. The published `workcell.rb` pins the bundle's `sha256`,
so Homebrew fails the install if the downloaded bundle's digest does not match —
a checksum-level integrity check. For the full signed-provenance guarantee
(keyless cosign signature over `SHA256SUMS`), verify `workcell.rb` and the
bundle following [provenance.md](provenance.md#verifying-release-assets) before
`brew install`, or use the verified release install above.

### Source checkout

For contributors and local repo review:

```bash
git clone https://github.com/omkhar/workcell.git
cd workcell
./scripts/install.sh
```

`./scripts/install.sh` is the supported installer entrypoint. The
`scripts/install-workcell.sh` helper remains an internal implementation detail.

## Requirements

- **macOS** (Apple Silicon only). Workcell manages a dedicated
  [Colima](https://github.com/abiosoft/colima) VM profile using Apple's
  Virtualization.Framework. Linux and Windows host platforms are not currently
  supported.
- **Homebrew** available on the host if you want the installer to auto-install
  missing required packages.
- Required host packages: `colima`, `docker`, `gh`, `git`, and `go`.
  `./scripts/install.sh` installs only the missing ones on supported macOS
  hosts by default, or you can install them yourself with
  `brew install colima docker gh git go`.

## Lifecycle and upgrades

Upgrade-in-place (re-run the installer from a newer bundle), rollback,
uninstall, and `workcell --gc` are covered as repeatable day-two evidence in
[install-lifecycle.md](install-lifecycle.md), which also records the on-disk
format-compatibility posture and which lifecycle checks are proven in CI versus
certified by a local operator.
