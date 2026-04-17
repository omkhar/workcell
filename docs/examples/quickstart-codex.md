# Quickstart: Codex in Workcell

This guide walks through the normal Codex path inside Workcell's bounded
runtime.

It assumes a supported Apple Silicon macOS host. GitHub-hosted CI and
tagged-release install verification currently cover `macos-26` and `macos-15`;
the strongest local boundary claim still depends on local Colima validation.

## Prerequisites

- macOS
- Apple Silicon host
- Colima
- Docker CLI
- Workcell installed with `./scripts/install.sh`

## 1. Create or update the injection policy

Workcell does not pass host provider homes through to the session. Use the
host-side auth helpers to create the policy and copy the reviewed auth file
into Workcell's managed credential store:

```bash
workcell auth init
workcell auth set \
  --agent codex \
  --credential codex_auth \
  --source /Users/example/.config/workcell/codex-auth.json
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
workcell --agent codex --doctor --workspace /path/to/repo
workcell --agent codex --inspect --workspace /path/to/repo
workcell auth status --agent codex
workcell --agent codex --auth-status --workspace /path/to/repo
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
- [Requirements validation](../requirements-validation.md)
- [Validation scenarios](../validation-scenarios.md)
