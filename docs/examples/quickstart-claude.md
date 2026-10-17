# Quickstart: Claude in Workcell

This guide assumes a supported Apple Silicon macOS host. GitHub-hosted CI and
tagged-release install verification currently cover `macos-26` and `macos-15`;
the strongest local boundary claim still depends on local Colima validation.

## Prerequisites

- Workcell installed with `./scripts/install.sh`
- a repo you want to mount as the workspace
- a reviewed Claude auth export, a reviewed Claude API key file, or a
  fail-closed macOS resolver scaffold config (manual support tier today)

## 1. Create or update the injection policy

Launch-ready paths today:

Reviewed exported Claude auth file:

```bash
workcell auth set \
  --agent claude \
  --credential claude_auth \
  --source /Users/example/.config/workcell/claude-auth.json
```

Reviewed API key path, served from the login Keychain item
`workcell-claude-api-key`:

```bash
workcell auth init
security add-generic-password -U -s workcell-claude-api-key -a "$USER" -T /usr/bin/security -w
workcell auth set \
  --agent claude \
  --credential claude_api_key \
  --resolver keychain-broker \
  --ack-host-resolver
```

A strict Colima launch uses the egress proxy by default. It refuses a Claude,
Gemini or Codex API key from a file
(`internal/injection/render_credentials.go`). The agent must hold only a
placeholder. Store the key in its macOS login Keychain item and use the
`keychain-broker` resolver, as
[the injection policy guide](../injection-policy.md) shows. A launch with
`--egress-proxy=off` still accepts a key from a file.

The `keychain-broker` resolver works only in a strict Colima session that uses
the egress proxy. A `--mode development` launch, or a launch with
`--egress-proxy=off`, has no broker placeholder and stops on it. For those
launches, set `claude_api_key` from a file with `--source`.

Optional fail-closed macOS resolver scaffold:

```bash
workcell auth set \
  --agent claude \
  --credential claude_auth \
  --resolver claude-macos-keychain \
  --ack-host-resolver
```

The resolver records the intended host auth source. Workcell stops preparation
and launch because no supported export path exists. Use this scaffold only to
record policy intent. Check its state with
`workcell auth status --agent claude`.

## 2. Optional explicit prepare

If you configured a launch-ready Claude auth input such as `claude_auth` or
`claude_api_key`, a normal strict launch prepares the reviewed runtime image
automatically when needed:

```bash
workcell --agent claude --workspace /path/to/repo
```

Use `--prepare-only` when you want to prewarm without launching:

```bash
workcell --prepare-only --agent claude --workspace /path/to/repo
```

## 3. Check the derived state

```bash
workcell --agent claude --doctor --workspace /path/to/repo
workcell --agent claude --inspect --workspace /path/to/repo
workcell auth status --agent claude
workcell --agent claude --auth-status --workspace /path/to/repo
```

`workcell auth status` reports the host policy view. `--auth-status` reports the
staged launch view after selector evaluation and resolver preprocessing.

If you configured only `claude_auth` via the fail-closed macOS resolver
scaffold, expect `credential_resolution_states=claude_auth:configured-only` and
`provider_auth_mode=none` until a supported export path exists. The bootstrap
summary will also report `provider_bootstrap_path=host-export-scaffold` and
`provider_bootstrap_support=manual`.

## 4. Launch Claude

```bash
workcell --agent claude --workspace /path/to/repo
```

Prompt-mode autonomy:

```bash
workcell --agent claude --agent-autonomy prompt --workspace /path/to/repo
```

Managed development lane. It needs a Claude key from a file or `claude_auth`,
not the `keychain-broker` resolver:

```bash
workcell --agent claude --mode development --workspace /path/to/repo -- bash -lc 'rg TODO'
```

## 5. Optional reviewed MCP state

If you want Claude to use a reviewed MCP registry instead of the empty safe
baseline:

```bash
workcell auth set \
  --agent claude \
  --credential claude_mcp \
  --source /Users/example/.config/workcell/claude-mcp.json
```

## 6. Publish the result on the host

```bash
./scripts/pre-merge.sh --profile pr-parity
./scripts/repo-publish-pr.sh --workspace /path/to/repo --branch feature/my-change \
  --title-file /tmp/pr-title.txt --body-file /tmp/pr-body.md \
  --commit-message-file /tmp/commit-message.txt
```

## Further reading

- [docs/examples/enterprise-claude-setup.md](enterprise-claude-setup.md)
- [docs/injection-policy.md](../injection-policy.md)
- [docs/provider-bootstrap-matrix.md](../provider-bootstrap-matrix.md)
- [docs/adapter-control-planes.md](../adapter-control-planes.md)
- [docs/requirements-validation.md](../requirements-validation.md)
- [docs/validation-scenarios.md](../validation-scenarios.md)
