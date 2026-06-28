# Getting Started

This guide is the shortest path from "I want to evaluate Workcell" to "I have
an agent running inside the managed boundary."

It assumes an Apple Silicon macOS host.
Continuous CI and tagged-release install verification currently cover only
GitHub-hosted Apple Silicon `macos-26` and `macos-15`.

## 1. Install Workcell

### Option A: verified release bundle

Download a tagged release bundle plus `SHA256SUMS` and
`SHA256SUMS.sigstore.json` from GitHub Releases, verify the bundle, then
unpack it and run the installer:

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

See [docs/provenance.md](provenance.md) for the full verification contract.

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

GitHub Copilot CLI and Google Antigravity CLI are not supported agents yet. Do
not configure `--agent copilot`, `--agent antigravity`, planned credential keys,
or host provider-home state until the matching adapter support phase lands with
docs and certification.

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
- [Gemini quickstart](examples/quickstart-gemini.md)

There is no Copilot or Antigravity quickstart in current releases. Each planned
provider will get a quickstart only when support is implemented and certified.

For team rollout patterns on today's local-first product, see
[Enterprise rollout today](enterprise-rollout.md).

## 6. Understand the contract

- [Security invariants](invariants.md)
- [Injection policy](injection-policy.md)
- [Validation scenarios](validation-scenarios.md)
- [Provenance and signing](provenance.md)
