# Getting Started

Use this guide to install Workcell and start an agent in the managed runtime.

It assumes an Apple Silicon macOS host.
Continuous CI and tagged-release install verification currently cover only
GitHub-hosted Apple Silicon `macos-26` and `macos-15`.

## 1. Install Workcell

### Option A: verified release install (recommended)

Always verify a release before you install it. `install-release.sh` is the
verified install path. It downloads the release bundle and its signed
`SHA256SUMS` file. It verifies the Cosign signature and digest before it runs
bundle code. If verification fails, it stops the install.

Install the verification tools on the host before you run the installer.
Install `cosign`, `gnupg`, and `git`:

```bash
brew install cosign git gnupg
```

The release does not include `install-release.sh` as a separate asset. Get it
from the signed release tag. Do not get it from the mutable default branch.
Use [release-posture.md](release-posture.md) to find the current release tag.
First, import and confirm the maintainer key fingerprint from
[SECURITY.md](../SECURITY.md#signing-key).

```bash
git clone --branch vX.Y.Z --depth 1 https://github.com/omkhar/workcell.git
cd workcell
git tag -v vX.Y.Z
./scripts/install-release.sh --version vX.Y.Z
```

`git tag -v` checks the installer against the maintainer signing key. Arguments
after `--` go to the bundle installer. For example, use
`-- --no-install-deps` for a launcher-only install.

For an **additional** GitHub attestation check, append `--attestation`. That
step runs `gh attestation verify`, which queries the GitHub API, so it also
needs `gh` **installed and authenticated** (`brew install gh && gh auth login`)
and network access:

```bash
./scripts/install-release.sh --version vX.Y.Z --attestation
```

For manual verification, download the bundle, `SHA256SUMS`, and
`SHA256SUMS.sigstore.json`. Verify them before extraction. The Cosign and digest
steps can run offline with the downloaded Sigstore bundle.

The GitHub attestation step requires network access by default. Omit it for an
offline install. You can also give it a local attestation with `--bundle`.

The regex below anchors and escapes the fixed identity text (`^…\.…$`). Thus,
only the release tag is variable. `verify-release-artifact.sh` uses the same
expression.

```bash
cosign verify-blob SHA256SUMS \
  --bundle SHA256SUMS.sigstore.json \
  --certificate-identity-regexp '^https://github\.com/omkhar/workcell/\.github/workflows/release\.yml@refs/tags/.+$' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
shasum -a 256 --ignore-missing -c SHA256SUMS
# needs network; pins the same anchored/escaped identity regex + OIDC issuer that
# install-release.sh --attestation uses (not --signer-workflow, which can over-match).
gh attestation verify workcell-vX.Y.Z.tar.gz --repo omkhar/workcell \
  --cert-identity-regex '^https://github\.com/omkhar/workcell/\.github/workflows/release\.yml@refs/tags/.+$' \
  --cert-oidc-issuer https://token.actions.githubusercontent.com
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

Each supported release includes a `workcell.rb` formula asset. The formula
installs the reviewed tree in the Homebrew `libexec` directory.

```bash
curl -LO https://github.com/omkhar/workcell/releases/download/vX.Y.Z/workcell.rb
curl -LO https://github.com/omkhar/workcell/releases/download/vX.Y.Z/SHA256SUMS
curl -LO https://github.com/omkhar/workcell/releases/download/vX.Y.Z/SHA256SUMS.sigstore.json
cosign verify-blob SHA256SUMS \
  --bundle SHA256SUMS.sigstore.json \
  --certificate-identity-regexp '^https://github\.com/omkhar/workcell/\.github/workflows/release\.yml@refs/tags/.+$' \
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
`copilot_github_token`. It removes the original token file and its staged copy
from direct mounts. For an authenticated start, Workcell uses a temporary token
handoff. The handoff is outside provider state. The entrypoint remains PID 1
and unlinks the mounted file. Workcell exports `COPILOT_GITHUB_TOKEN` only to
the managed Copilot child.

Google Antigravity CLI is not a supported agent yet. Do not configure
`--agent antigravity`, unimplemented credential keys, or host provider state
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

There is no Antigravity quickstart. Workcell must implement and certify support
before it adds a quickstart.

For team rollout patterns on today's local-first product, see
[Enterprise rollout today](enterprise-rollout.md).

## 6. Understand the contract

- [Security invariants](invariants.md)
- [Injection policy](injection-policy.md)
- [Validation scenarios](validation-scenarios.md)
- [Provenance and signing](provenance.md)

## 7. Clean up and uninstall

- Reclaim stale runtime/cache/temp state with `workcell --gc` (this does **not**
  remove managed state or Colima profiles). It reaps `session-audit` records
  older than 12 hours, so do **not** run it before preserving evidence for a
  suspected security incident — see the
  [incident-response runbook](incident-response.md).
- **Bundle or source install**: First run `./scripts/uninstall.sh --dry-run`.
  Then run `./scripts/uninstall.sh`. It removes Workcell links, state, profiles,
  and caches. It preserves `~/.config/workcell`, including the injection policy
  and managed credentials. It also keeps shared packages and unrelated profiles.
  Remove custom `WORKCELL_STATE_ROOT` or `XDG_STATE_HOME` content separately.
- **Homebrew formula install**: Run `brew uninstall workcell`. Then run
  `./scripts/uninstall.sh` from a release bundle or source checkout. The second
  command removes runtime state and Colima profiles.

These are covered as repeatable day-two operations in
[docs/install-lifecycle.md](install-lifecycle.md).
