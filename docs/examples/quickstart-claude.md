# Quickstart: Claude in Workcell

## Prerequisites

- Workcell installed with `./scripts/install.sh`
- a repo you want to mount as the workspace
- either reviewed Claude login state or a reviewed API key file

## 1. Create an injection policy

Persisted Claude login:

```toml
version = 1

[credentials]
claude_auth = "/Users/example/.claude.json"
```

API key helper path:

```toml
version = 1

[credentials]
claude_api_key = "/Users/example/.config/workcell/claude-api-key.txt"
```

## 2. Prepare the runtime image

```bash
workcell --prepare-only --agent claude --workspace /path/to/repo
```

## 3. Check the derived state

```bash
workcell --agent claude --inspect --workspace /path/to/repo
workcell --agent claude --auth-status --workspace /path/to/repo
```

## 4. Launch Claude

```bash
workcell --agent claude --workspace /path/to/repo
```

Prompt-mode autonomy:

```bash
workcell --agent claude --agent-autonomy prompt --workspace /path/to/repo
```

## 5. Optional reviewed MCP state

If you want Claude to use a reviewed MCP registry instead of the empty safe
baseline:

```toml
[credentials]
claude_mcp = "/Users/example/.config/workcell/claude-mcp.json"
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
