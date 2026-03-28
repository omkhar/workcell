# Enterprise Claude Setup with Workcell

This guide covers deploying Claude Code inside Workcell for organizations
where multiple engineers share a common policy structure.

## How the injection policy works at org scale

Each engineer owns their own injection policy file on their host machine.
Workcell reads it at launch time, validates the declared sources, and stages
them into the ephemeral per-session container home. The policy file never
leaves the host; secrets are mounted read-only from their original paths and
then copied into the session.

For org-wide deployment, the pattern is:

1. Maintain a canonical policy template in a shared (non-secret) location such
   as a company config repo or a managed MDM-deployed file.
2. Each engineer copies or symlinks (at the file level, not for secret sources)
   the shared non-secret fragments and supplies their own credential paths.
3. Secrets remain on each engineer's machine at operator-owned paths and are
   never shared.

## 1. Common CLAUDE.md overlay via injection policy

The `documents.claude` key appends a provider-specific instruction fragment
to the rendered `~/.claude/CLAUDE.md` inside every session. This is the
supported way to distribute org-wide Claude instructions without replacing
the reviewed adapter baseline.

Example policy entry:

```toml
version = 1

providers = ["claude"]

[documents]
claude = "/Users/example/.config/workcell/org-claude-overlay.md"
```

The `providers = ["claude"]` scoping ensures this fragment is only applied
to Claude sessions, not Codex or Gemini sessions launched from the same
policy file.

Rendering order inside `~/.claude/CLAUDE.md`:

1. Immutable baseline from `adapters/claude/CLAUDE.md`
2. Workspace `AGENTS.md` (imported from a read-only masked snapshot, if present)
3. Workspace `CLAUDE.md` (imported from a read-only masked snapshot, if present)
4. `documents.common` (if configured)
5. `documents.claude` (the org overlay, if configured)

The org overlay appends at the end. It does not replace or override the
reviewed adapter baseline.

## 2. managed-settings.json as the immutable baseline

`adapters/claude/managed-settings.json` is baked into the container image and
symlinked to `~/.claude/settings.json` inside each session. It contains the
deny-list permissions and the `PreToolUse` Bash hook registration.

This file is not replaceable by workspace content or injection policy entries.
Writing to `.claude/settings.json` or `CLAUDE.md` through workspace files has
no effect because those workspace paths are masked on the safe path and the
session home is re-seeded from the immutable adapter baseline on each provider
launch.

If your org needs to adjust the deny list or hooks, that requires a reviewed
change to the adapter baseline and a new container image build, not a
per-engineer policy override.

## 3. apiKeyHelper pattern

When `credentials.claude_api_key` is configured, Workcell generates a
session-local `apiKeyHelper` script at `~/.claude/workcell/api-key-helper.sh`.
This script reads the mounted credential path directly rather than copying
the key to a second location.

Claude Code reads the `apiKeyHelper` field from `~/.claude/settings.json` and
calls the helper at auth time. Because the helper reads the mounted file, the
key is never duplicated into a second plaintext session-local copy.

Policy entry:

```toml
version = 1

[credentials]
claude_api_key = "/Users/example/.config/workcell/claude-api-key.txt"
```

The source file must be mode `0600`, owned by the launching user, and must not
be a symlink.

## 4. Org-wide policy example

The following example shows a policy structure suitable for a team where
engineers share common documents and each has their own API key.

`~/.config/workcell/injection-policy.toml` (per-engineer, references shared
and personal paths):

```toml
version = 1

# Include shared non-secret fragments managed by the org
includes = ["/opt/corp/workcell/shared-docs.toml"]

[credentials]
# Personal API key — owned by each engineer, never shared
claude_api_key = "/Users/example/.config/workcell/claude-api-key.txt"

# Shared GitHub CLI auth scoped to Claude sessions
[credentials.github_hosts]
source = "/Users/example/.config/gh/hosts.yml"
providers = ["claude"]
modes = ["strict", "build"]
```

`/opt/corp/workcell/shared-docs.toml` (org-managed, non-secret):

```toml
version = 1

providers = ["claude"]

[documents]
common = "/opt/corp/workcell/org-agent-instructions.md"
claude = "/opt/corp/workcell/org-claude-overlay.md"
```

The `includes` field loads `shared-docs.toml` relative to the entrypoint
policy. Include files must stay within the entrypoint policy tree.
Duplicate `documents.*` entries across includes are rejected so one fragment
cannot silently override another.

## 5. Scoping credentials to specific providers and modes

Use `providers` and `modes` fields to limit which sessions receive which
credentials:

```toml
version = 1

[credentials.github_hosts]
source = "/Users/example/.config/gh/hosts.yml"
providers = ["claude"]
modes = ["strict", "build"]
```

This ensures that GitHub CLI credentials are only injected into Claude sessions
running in `strict` or `build` mode, not into Codex or Gemini sessions or
`breakglass` sessions.

## 6. Launch

Each engineer launches with their personal policy:

```bash
workcell --agent claude --workspace /path/to/repo
```

If `~/.config/workcell/injection-policy.toml` exists, Workcell uses it
automatically. To use an explicit path:

```bash
workcell --agent claude --workspace /path/to/repo \
  --injection-policy ~/.config/workcell/injection-policy.toml
```

## Further reading

- `docs/injection-policy.md` — full injection policy reference
- `docs/examples/quickstart-claude.md` — Claude quickstart
- `docs/adapter-control-planes.md` — how settings.json and hooks are seeded
- `docs/invariants.md` — the seven security invariants
