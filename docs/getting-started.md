# Getting Started

This guide is the shortest path from "I want to evaluate Workcell" to "I have
an agent running inside the managed boundary."

It assumes an Apple Silicon macOS host.
Continuous CI and tagged-release install verification currently cover only
GitHub-hosted Apple Silicon `macos-26` and `macos-15`.

## 1. Install Workcell

### Option A: verified release install (recommended)

Always verify a release before installing it. `install-release.sh` is the
one-command verified path: it downloads the tagged release bundle plus its
signed `SHA256SUMS`, verifies the cosign signature and digest **fail-closed
before any bundle code runs**, and only then extracts and installs.

`install-release.sh` is not published as a standalone release asset, so obtain
it from the repository over TLS (a trusted source for the installer) rather than
from the not-yet-verified bundle — clone the repo, then run it:

```bash
git clone https://github.com/omkhar/workcell.git
cd workcell
./scripts/install-release.sh --version vX.Y.Z --attestation
```

`--attestation` additionally requires `gh attestation verify` to pass. Arguments
after `--` are forwarded to the bundle installer (e.g.
`-- --no-install-deps` for a launcher-only install). A tampered or unsigned
bundle is refused before its (also-tampered) installer could run — this is why
verifying before extraction is sound.

**Manual equivalent** (from the release page directly, air-gapped, or to inspect
each step): download the bundle plus `SHA256SUMS` and `SHA256SUMS.sigstore.json`
from GitHub Releases and verify before unpacking:

```bash
cosign verify-blob SHA256SUMS \
  --bundle SHA256SUMS.sigstore.json \
  --certificate-identity-regexp 'https://github.com/omkhar/workcell/.github/workflows/release.yml@refs/tags/.+' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
shasum -a 256 --ignore-missing -c SHA256SUMS
tar -xzf workcell-vX.Y.Z.tar.gz
cd workcell-vX.Y.Z
./scripts/install.sh
```

See [docs/provenance.md](provenance.md) for the full verification contract and
[docs/install-lifecycle.md](install-lifecycle.md) for the day-two lifecycle.

On supported macOS hosts, the installer uses Homebrew to install only the
missing required packages (`colima`, `docker`, `gh`, `git`, `go`). Use
`./scripts/install.sh --no-install-deps` if you want a launcher-only install
plus a final warning summary instead.

### Option B: Homebrew formula asset from a tagged release

Each tagged release can publish a versioned `workcell.rb` formula asset that
installs the same reviewed tree into Homebrew-managed `libexec`:

```bash
curl -LO https://github.com/omkhar/workcell/releases/download/vX.Y.Z/workcell.rb
curl -LO https://github.com/omkhar/workcell/releases/download/vX.Y.Z/SHA256SUMS
curl -LO https://github.com/omkhar/workcell/releases/download/vX.Y.Z/SHA256SUMS.sigstore.json
cosign verify-blob SHA256SUMS \
  --bundle SHA256SUMS.sigstore.json \
  --certificate-identity-regexp 'https://github.com/omkhar/workcell/.github/workflows/release.yml@refs/tags/.+' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
shasum -a 256 --ignore-missing -c SHA256SUMS
brew install --formula ./workcell.rb
```

The formula pins the bundle digest, so `brew` re-verifies the downloaded tree
against the reviewed release at install time.

The formula declares `colima`, `docker`, `gh`, `git`, and `go` as explicit
dependencies.

### Option C: source checkout

For local development or repo review:

```bash
git clone https://github.com/omkhar/workcell.git
cd workcell
./scripts/install.sh
```

## 2. Create the host-side injection policy

Initialize the managed credential store once:

```bash
workcell auth init
```

Then configure the credential you need:

Codex:

```bash
workcell auth set \
  --agent codex \
  --credential codex_auth \
  --source /Users/example/.config/workcell/codex-auth.json
```

Claude API key:

```bash
workcell auth set \
  --agent claude \
  --credential claude_api_key \
  --source /Users/example/.config/workcell/claude-api-key.txt
```

Gemini env file:

```bash
workcell auth set \
  --agent gemini \
  --credential gemini_env \
  --source /Users/example/.config/workcell/gemini.env
```

GitHub Copilot CLI:

```bash
workcell auth set \
  --agent copilot \
  --credential copilot_github_token \
  --source /Users/example/.config/workcell/copilot-github-token.txt
```

Do not use host `gh` auth, `GH_TOKEN`, `GITHUB_TOKEN`, host keychains, or host
Copilot provider state (`~/.copilot`, `~/.config/github-copilot`,
`~/.cache/github-copilot`) as Copilot readiness sources. Workcell stages only
`copilot_github_token` and removes the token file plus staged direct-mount
copy from direct runtime mounts for Copilot sessions. For auth-required
provider launches, Workcell converts it to a temporary host-mounted token
handoff outside mounted provider state, moves it through a transient runtime
handoff file with the Workcell entrypoint as PID 1, unlinks the mounted handoff
file, and exports it as `COPILOT_GITHUB_TOKEN` only for the managed Copilot
child.

Google Antigravity CLI is not a supported agent yet. Do not configure
`--agent antigravity`, planned credential keys, or host provider-home state
until the matching Workcell adapter support phase lands with docs and
certification.

Check the host-side view at any time:

```bash
workcell auth status --agent codex
```

To roll back a credential entry from the host policy, use:

```bash
workcell auth unset --agent codex --credential codex_auth
```

That output, `workcell --auth-status`, and `workcell why` all include bootstrap
summary fields. Use
[docs/provider-bootstrap-matrix.md](provider-bootstrap-matrix.md) to interpret
whether the selected path is repo-required, certification-only, or manual.

## 3. Inspect before launch

These commands do not start the runtime. They show whether the host,
workspace, and injection policy are in the expected shape.

```bash
workcell --agent codex --doctor --workspace /path/to/repo
workcell --agent codex --inspect --workspace /path/to/repo
workcell --agent codex --auth-status --workspace /path/to/repo
```

## 4. Launch the agent

```bash
workcell --agent codex --workspace /path/to/repo
```

Useful variants:

```bash
workcell --agent codex --prepare-only --workspace /path/to/repo
workcell --agent codex --mode development --workspace /path/to/repo -- bash -lc 'git status'
workcell --agent codex --agent-autonomy prompt --workspace /path/to/repo
```

## 5. Read the provider-specific quickstart

- [Codex quickstart](examples/quickstart-codex.md)
- [Claude quickstart](examples/quickstart-claude.md)
- [Copilot quickstart](examples/quickstart-copilot.md)
- [Gemini quickstart](examples/quickstart-gemini.md)

There is no Antigravity quickstart in current releases. That planned provider
will get a quickstart only when Workcell support is implemented and certified.

For team rollout patterns on today's local-first product, see
[Enterprise rollout today](enterprise-rollout.md).

## 6. Understand the contract

- [Security invariants](invariants.md)
- [Injection policy](injection-policy.md)
- [Validation scenarios](validation-scenarios.md)
- [Provenance and signing](provenance.md)
