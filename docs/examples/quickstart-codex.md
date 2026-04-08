# Quickstart: Codex in Workcell

This guide walks through the normal Codex path inside Workcell's bounded
runtime.

## Prerequisites

- macOS
- Colima
- Docker CLI
- Workcell installed with `./scripts/install.sh`

## 1. Create a reviewed auth file

Workcell does not pass host environment variables or provider homes through to
the session. Use an injection policy instead.

```bash
mkdir -p ~/.config/workcell
install -m 0600 /dev/null ~/.config/workcell/codex-auth.json
```

Create `~/.config/workcell/injection-policy.toml`:

```toml
version = 1

[credentials]
codex_auth = "/Users/example/.config/workcell/codex-auth.json"
```

## 2. Optional explicit prepare

A normal strict launch prepares the reviewed runtime image automatically when
needed:

```bash
workcell --agent codex --workspace /path/to/repo
```

To prewarm without launching:

```bash
workcell --prepare-only --agent codex --workspace /path/to/repo
```

## 3. Check the derived state

```bash
workcell doctor --agent codex --workspace /path/to/repo
workcell inspect --agent codex --workspace /path/to/repo
```

## 4. Launch Codex

```bash
workcell --agent codex --workspace /path/to/repo
```

Important defaults:

- `strict` is the default mode
- `yolo` is the default autonomy posture
- the injection policy at `~/.config/workcell/injection-policy.toml` is used
  automatically if present

## 5. Optional lower-assurance paths

Prompt mode:

```bash
workcell --agent codex --agent-autonomy prompt --workspace /path/to/repo
```

Session-local writable rules:

```bash
workcell --agent codex --codex-rules-mutability session --workspace /path/to/repo
```

Build lane:

```bash
workcell --agent codex --mode build --workspace /path/to/repo
```

Managed development lane:

```bash
workcell --agent codex --mode development --workspace /path/to/repo -- bash -lc 'git status'
```

Breakglass:

```bash
workcell --agent codex --mode breakglass --ack-breakglass --workspace /path/to/repo
```

## 6. Publish on the host

Prepare the PR metadata, then publish from the host:

```bash
workcell publish-pr \
  --workspace /path/to/repo \
  --branch feature/name \
  --title-file /tmp/pr-title.txt \
  --body-file /tmp/pr-body.md \
  --commit-message-file /tmp/commit-message.txt
```

## Further reading

- [Injection policy](../injection-policy.md)
- [Codex adapter](../../adapters/codex/README.md)
- [Adapter control planes](../adapter-control-planes.md)
