# Quickstart: Claude in Workcell

This guide assumes a supported Apple Silicon macOS host. GitHub-hosted CI and
tagged-release install verification currently cover `macos-26` and `macos-15`;
the strongest local boundary claim still depends on local Colima validation.

## Prerequisites

- Workcell installed with `./scripts/install.sh`
- a repo you want to mount as the workspace
- a reviewed Claude auth export, a reviewed Claude API key file, or an
  experimental macOS resolver config

## 1. Create or update the injection policy

Launch-ready paths today:

Reviewed exported Claude auth file:

```bash
workcell auth set \
  --agent claude \
  --credential claude_auth \
  --source /Users/example/.config/workcell/claude-auth.json
```

Reviewed API key helper path:

```bash
workcell auth init
workcell auth set \
  --agent claude \
  --credential claude_api_key \
  --source /Users/example/.config/workcell/claude-api-key.txt
```

Optional fail-closed macOS resolver scaffold:

```bash
workcell auth set \
  --agent claude \
  --credential claude_auth \
  --resolver claude-macos-keychain \
  --ack-host-resolver
```

That resolver records the intended host-side auth source in policy, but the
current Workcell implementation still aborts `--prepare-only` and launch flows
unless a supported export path exists. Use it only to record intent for a
future supported export and verify the configured-only state with
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

If you configured only `claude_auth` via the experimental macOS resolver, expect
`credential_resolution_states=claude_auth:configured-only` and
`provider_auth_mode=none` until a supported export path exists.

## 4. Launch Claude

```bash
workcell --agent claude --workspace /path/to/repo
```

Prompt-mode autonomy:

```bash
workcell --agent claude --agent-autonomy prompt --workspace /path/to/repo
```

Managed development lane:

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
workcell publish-pr --workspace /path/to/repo --branch feature/my-change \
  --title-file /tmp/pr-title.txt --body-file /tmp/pr-body.md \
  --commit-message-file /tmp/commit-message.txt
```

## Further reading

- [docs/injection-policy.md](../injection-policy.md)
- [docs/adapter-control-planes.md](../adapter-control-planes.md)
- [docs/validation-scenarios.md](../validation-scenarios.md)
