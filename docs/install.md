# Install

Workcell ships with a supported installer plus a tagged Homebrew formula asset.
For the shortest end-to-end path, see the
[5-minute path](../README.md#5-minute-path) in the README.

## Install options

### Tagged release bundle

Download a tagged release bundle, unpack it, and run the supported installer:

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
`gh`, `git`, and `go`.

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
