# Getting Started

This guide is the shortest path from "I want to evaluate Workcell" to "I have
an agent running inside the managed boundary."

It assumes an Apple Silicon macOS host.
Continuous CI and tagged-release install verification currently cover only
GitHub-hosted Apple Silicon `macos-26` and `macos-15`.

## 1. Install Workcell

### Option A: verified release bundle

Download a tagged release bundle from GitHub Releases, unpack it, and run the
installer:

```bash
tar -xzf workcell-vX.Y.Z.tar.gz
cd workcell-vX.Y.Z
./scripts/install.sh
```

On supported macOS hosts, the installer uses Homebrew to install only the
missing required packages (`colima`, `docker`, `gh`, `git`, `go`). Use
`./scripts/install.sh --no-install-deps` if you want a launcher-only install
plus a final warning summary instead.

### Option B: Homebrew formula asset from a tagged release

Each tagged release can publish a versioned `workcell.rb` formula asset that
installs the same reviewed tree into Homebrew-managed `libexec`:

```bash
curl -LO https://github.com/omkhar/workcell/releases/download/vX.Y.Z/workcell.rb
brew install --formula ./workcell.rb
```

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

Check the host-side view at any time:

```bash
workcell auth status --agent codex
```

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

## 6. Understand the contract

- [Security invariants](invariants.md)
- [Injection policy](injection-policy.md)
- [Validation scenarios](validation-scenarios.md)
- [Provenance and signing](provenance.md)
