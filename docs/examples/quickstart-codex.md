# Quickstart: OpenAI Codex in Workcell

This guide walks through running OpenAI Codex inside the Workcell bounded
runtime. All commands are copy-pasteable. Use fake credentials in examples;
never substitute real keys.

## Prerequisites

- macOS (the host launcher is macOS only)
- [Colima](https://github.com/abiosoft/colima) installed (`brew install colima`)
- Docker CLI installed (`brew install docker`)
- Workcell installed (`./scripts/install.sh` from the repo root)

Verify the install:

```bash
workcell --version
```

## 1. Credential setup

Workcell does not forward host environment variables or host home directories
into the session. Credentials reach the agent only through an explicit
injection policy.

Create the API key file with owner-only permissions:

```bash
mkdir -p ~/.config/workcell
# Write your actual key here; the file must be mode 0600
install -m 0600 /dev/null ~/.config/workcell/codex-auth.json
# Populate the file with your reviewed Codex auth.json content
```

Create `~/.config/workcell/injection-policy.toml`:

```toml
version = 1

[credentials]
codex_auth = "/Users/example/.config/workcell/codex-auth.json"
```

Replace `/Users/example` with your actual home path. The `codex_auth` key
mounts the file read-only into the session and copies it to
`~/.codex/auth.json` inside the container.

> **Note:** Never store credentials in environment variables, shell history,
> or committed files. `FIXTURE_FAKE_KEY_DO_NOT_USE` is used as a placeholder
> in examples throughout this document and must never appear in real config.

## 2. Prepare the runtime image

The `strict` profile (the default) requires a prebuilt runtime image. Run
`--prepare` on first launch or after a Workcell update:

```bash
workcell --prepare --agent codex --workspace /path/to/repo
```

To preload the image without starting a session:

```bash
workcell --prepare-only --agent codex --workspace /path/to/repo
```

## 3. Check preconditions

Before launching a session, verify host, workspace, and profile posture:

```bash
workcell doctor --agent codex --workspace /path/to/repo
```

`doctor` reports the first blocking issue and prints the next safe command.
Run it whenever a launch fails unexpectedly.

## 4. Basic launch

```bash
workcell --agent codex --workspace /path/to/repo
```

Workcell starts the dedicated Colima VM profile, builds or reuses the
prepared runtime image, and launches Codex inside the hardened inner
container. The workspace is mounted at `/workspace` inside the container.

The default autonomy mode is `yolo` (`--ask-for-approval never`). Codex
proceeds without per-action approval prompts.

With an explicit injection policy:

```bash
workcell --agent codex --workspace /path/to/repo \
  --injection-policy ~/.config/workcell/injection-policy.toml
```

If `~/.config/workcell/injection-policy.toml` exists, Workcell uses it
automatically without `--injection-policy`.

## 5. Inspect derived session state

To see the full derived configuration without launching a session:

```bash
workcell --agent codex --inspect --workspace /path/to/repo
```

This prints the resolved profile, network mode, injection inputs, and
assurance fields. Use it to confirm credentials will be injected correctly
before a real launch.

## 6. Publish a PR after work is done

Final branch publication is a host-side action. The agent prepares a branch
and commit message inside the session; the operator runs `publish-pr` on the
host to sign the commit, push, and open a draft PR.

Prepare the PR metadata files (the agent or operator fills these in):

```bash
echo "Add feature X" > /tmp/pr-title.txt
cat > /tmp/pr-body.md <<'EOF'
## Summary

- Implements feature X
- Adds tests

## Test plan

- [ ] Unit tests pass
EOF
```

Then publish from the host:

```bash
workcell publish-pr \
  --branch feature-branch \
  --title-file /tmp/pr-title.txt \
  --body-file /tmp/pr-body.md \
  --workspace /path/to/repo
```

`publish-pr` creates or switches to the named branch, makes a signed commit
using the operator's host identity, pushes it, and opens a draft PR. It does
not run inside the container.

## 7. Lower-assurance options

> **Note (lower-assurance):** The options below reduce session integrity
> guarantees. They are explicitly surfaced in the launch audit output and
> should only be used when you understand the tradeoff.

**Prompt autonomy** keeps Codex's ordinary per-action approval flow but marks
the session as `autonomy_assurance=lower-assurance-prompt-autonomy`:

```bash
workcell --agent codex --agent-autonomy prompt --workspace /path/to/repo
```

**Session-writable Codex rules** allow execpolicy amendments to persist
across nested Codex launches until container exit:

```bash
workcell --agent codex --codex-rules-mutability session --workspace /path/to/repo
```

**Build mode** opens a broader egress allowlist for package registries and
dependency fetching. Use this for image preparation and build work:

```bash
workcell --agent codex --mode build --workspace /path/to/repo
```

**Breakglass** requires an explicit acknowledgement flag and gives
unrestricted network access. The managed in-container entrypoint still does
not auto-inject unsafe provider flags:

```bash
workcell --agent codex --mode breakglass --ack-breakglass --workspace /path/to/repo
```

## Further reading

- `docs/injection-policy.md` — full injection policy reference
- `docs/invariants.md` — the seven security invariants
- `adapters/codex/README.md` — Codex adapter control-plane details
- `docs/adapter-control-planes.md` — full control file matrix
