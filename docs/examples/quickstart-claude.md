# Quickstart: Anthropic Claude in Workcell

This guide walks through running Anthropic Claude Code inside the Workcell
bounded runtime. All commands are copy-pasteable.

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

Workcell does not forward host environment variables or the host home directory
into the session. The supported method is an explicit injection policy.

### Option A: API key injection

Create the API key file with owner-only permissions:

```bash
mkdir -p ~/.config/workcell
install -m 0600 /dev/null ~/.config/workcell/claude-api-key.txt
# Write your actual key into that file (not FIXTURE_FAKE_KEY_DO_NOT_USE)
```

Create `~/.config/workcell/injection-policy.toml`:

```toml
version = 1

[credentials]
claude_api_key = "/Users/example/.config/workcell/claude-api-key.txt"
```

The `claude_api_key` key mounts the file read-only and generates a
session-local `apiKeyHelper` script inside `~/.claude/workcell/` that reads
the mounted credential path directly. This means Claude can reuse the key
without creating a second plaintext copy and without mutating the reviewed
`~/.claude/settings.json` baseline.

### Option B: Persisted CLI auth

If you have already authenticated via `claude login` and want to reuse the
resulting session file:

```toml
version = 1

[credentials]
claude_auth = "/Users/example/.claude.json"
```

On the host, `claude_auth` can point at your reviewed Claude login artifact,
commonly `~/.claude.json`. Inside Workcell, current Claude releases persist
session state under `~/.claude/.claude.json` when `CLAUDE_CONFIG_DIR=~/.claude`.
If your host still writes `~/.claude/.credentials.json` or
`~/.config/claude-code/auth.json`, you can point `claude_auth` at that legacy
path instead. Workcell seeds the current session path plus the legacy mirrors
for compatibility.

Only one of `claude_api_key` or `claude_auth` is needed for most workflows.

> **Note:** Never store credentials in environment variables, shell history,
> or committed files. `FIXTURE_FAKE_KEY_DO_NOT_USE` is used as a placeholder
> in examples and must never appear in real config.

## 2. Prepare the runtime image

The `strict` profile requires a prebuilt runtime image. Run `--prepare` on
first launch or after a Workcell update:

```bash
workcell --prepare --agent claude --workspace /path/to/repo
```

## 3. Basic launch

```bash
workcell --agent claude --workspace /path/to/repo
```

Workcell starts the dedicated Colima VM profile and launches Claude Code
inside the hardened inner container. The workspace mounts at `/workspace`.

The default autonomy mode is `yolo` (`--permission-mode bypassPermissions`).
Claude proceeds without per-action approval prompts.

## 4. Autonomy mode: prompt

> **Note (lower-assurance):** Enabling prompt autonomy keeps Claude's ordinary
> per-action approval flow but marks the session as
> `autonomy_assurance=lower-assurance-prompt-autonomy`. Provider-native
> approval state or session-local policy amendments can change during the live
> session, so Workcell surfaces this posture explicitly in the launch audit
> output. Use it only when you need interactive oversight.

```bash
workcell --agent claude --agent-autonomy prompt --workspace /path/to/repo
```

## 5. managed-settings.json: the immutable baseline

Claude's session home is seeded from `adapters/claude/managed-settings.json`
on every provider launch. The file contains:

- a deny list covering `rm -rf`, `sudo`, force pushes, and reads of host
  credential paths such as `~/.ssh/`, `~/.aws/`, `~/.kube/`, and
  `~/.config/gh/`
- `enableAllProjectMcpServers: false` to block automatic project MCP server
  activation
- the `PreToolUse` Bash hook registration pointing to `guard-bash.sh`

This file is symlinked from the immutable adapter baseline, not copied into a
location the workspace can replace. Workspace-visible `CLAUDE.md` and
`.claude/` paths are masked on the safe path; the agent cannot silently
override `managed-settings.json` by writing control files into the workspace.

## 6. guard-bash.sh: what it blocks and why

The `guard-bash.sh` hook runs as a `PreToolUse` filter on every Bash tool
invocation. It blocks:

- `git` calls that bypass hooks (`--no-verify`, `commit -n`, inline
  `-c core.hookspath=...`, `--git-dir`, `--work-tree` overrides)
- Nested `claude` subprocess calls with unsafe flags such as
  `--dangerously-skip-permissions`, `--mcp-config`, `--settings`, or
  `--system-prompt`
- Shell expansion syntax: command substitution `$(...)`, backticks,
  parameter expansion `${...}`, process substitution `<(...)` / `>(...)`
- `eval` and shell variable expansion (`$VAR`, `${VAR}`)
- Nested coding-agent CLI launches (`codex`, `claude`, `gemini`)
- `source` and `.` (shell script sourcing)
- Nested shell interpreter calls (`bash script.sh`) — `bash -c '...'` is
  allowed
- `rm -rf` and `rm -fr`
- Direct pushes to `main` or `master`
- Reads or writes to workspace control files and directories (`AGENTS.md`,
  `CLAUDE.md`, `GEMINI.md`, `.mcp.json`, `.claude/`, `.codex/`, `.gemini/`)
- Reads or writes to Workcell home control-plane paths

The hook is defense in depth. The primary boundary is always the external
runtime (dedicated Colima VM plus hardened inner container), not the hook.
The hook does not cover non-Bash tools (`Read`, `Edit`, `Write`, etc.); those
are constrained by the `permissions.deny` list in `managed-settings.json`.

## 7. MCP setup

MCP servers are disabled by default. The safe path symlinks an empty
`~/.mcp.json` from `adapters/claude/mcp-template.json`.

To inject a reviewed MCP configuration:

```toml
version = 1

[credentials]
claude_mcp = "/Users/example/.config/workcell/claude-mcp.json"
```

The file at that path replaces the empty template for the session. It must be
operator-reviewed and owned by the launching user with mode `0600`.

> **Note (lower-assurance):** MCP servers with network access or
> package-fetching behavior require a broader runtime mode such as `build`.
> Enabling such servers inside `strict` is possible but counts as a
> lower-assurance configuration. Do not commit live MCP server entries to the
> adapter baseline.

## 8. Publish a PR after work is done

Final branch publication is a host-side action.

Prepare the PR metadata (the agent or operator fills these in):

```bash
echo "Add feature X" > /tmp/pr-title.txt
cat > /tmp/pr-body.md <<'EOF'
## Summary

- Implements feature X

## Test plan

- [ ] Tests pass
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
using the operator's host identity, pushes it, and opens a draft PR.

## Further reading

- `docs/injection-policy.md` — full injection policy reference
- `docs/invariants.md` — the seven security invariants
- `adapters/claude/README.md` — Claude adapter overview
- `docs/adapter-control-planes.md` — full control file matrix and hook
  coverage
