# Install

Workcell includes a supported installer and a Homebrew formula asset. Use
[release-posture.md](release-posture.md) to find the current release tag.
For the shortest end-to-end path, see the
[5-minute path](../README.md#5-minute-path) in the README.

## Install options

### Verified release install (recommended)

`scripts/install-release.sh` is the fail-closed install path. It downloads the
release bundle and its signed `SHA256SUMS` file. It verifies the signature and
bundle digest before it runs bundle code. It then extracts the bundle and runs
`scripts/install.sh`.

Install Git, GnuPG, and [`cosign`](https://github.com/sigstore/cosign) before
you verify the tag. On macOS, use `brew install cosign git gnupg`.

Import the maintainer signing key. Confirm its fingerprint against
[`SECURITY.md`](../SECURITY.md#signing-key). Then use the signed release tag:

```bash
git clone --branch vX.Y.Z --depth 1 https://github.com/omkhar/workcell.git
cd workcell
git tag -v vX.Y.Z
./scripts/install-release.sh --version vX.Y.Z
```

The `git tag -v` command verifies the pre-verification installer before you run
it. Do not use the installer from an arbitrary source checkout.

The script checks the release-workflow OIDC identity and issuer. It checks the
Sigstore bundle and Rekor entry. Then it checks the bundle digest in the
verified `SHA256SUMS` file. These checks automate the manual steps in
[provenance.md](provenance.md#verify-release-assets).

The script fails closed. It stops for a missing tool or verification file. It
also stops for an invalid signature or digest. Use `--attestation` to require a
GitHub attestation. Use `--repo OWNER/REPO` for a fork or mirror.

The installer uses `/bin/bash` before it trusts the download. It finds its tools
only in a fixed trusted `PATH`. Thus, an earlier user-writable directory cannot
provide a false tool.

Run `./scripts/install-release.sh` directly. Do not run it with an untrusted
`bash`. Install `cosign` and `gh` in a standard location. If necessary, set
`WORKCELL_INSTALL_TRUSTED_PATH` to a trusted path that contains them.

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

For manual verification, download the selected release bundle. Use the steps in
[provenance.md](provenance.md#verify-release-assets). Then unpack the bundle.
Run the supported installer:

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

Each supported release includes a versioned `workcell.rb` asset. Download it
from the release page. Then install it with Homebrew:

```bash
curl -LO https://github.com/omkhar/workcell/releases/download/vX.Y.Z/workcell.rb
brew install --formula ./workcell.rb
```

The formula declares the same required host dependencies: `colima`, `docker`,
`gh`, `git`, and `go`. The published `workcell.rb` pins the bundle's `sha256`,
so Homebrew fails the install if the downloaded bundle's digest does not match —
a checksum-level integrity check. For the full signed-provenance guarantee
(keyless cosign signature over `SHA256SUMS`), verify `workcell.rb` and the
bundle following [provenance.md](provenance.md#verify-release-assets) before
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
