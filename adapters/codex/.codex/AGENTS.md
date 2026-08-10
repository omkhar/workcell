# Codex Policy

This repository defines the Codex adapter for the generic Workcell runtime and
policy boundary.

## Priorities

1. Developer experience.
2. Simplicity.
3. Preserve the security invariants.
4. Performance.
5. Idiomatic correctness.

## Non-negotiables

- The strict Tier 1 path uses a dedicated Colima profile and a hardened
  container. Docker Desktop is a lower-assurance compatibility target.
- Do not mount a host home, keychain, browser profile, credential store, or
  `docker.sock`. Use only a narrow staged input that policy approves.
- Keep network policy explicit. The managed Colima allowlist applies to the
  complete profile. Docker Desktop has no Workcell egress enforcement.
- The managed baseline ships no MCP servers. Workcell denies repository MCP by
  default. In non-`breakglass` modes, the lower-assurance exception requires
  `--allow-repo-mcp` and `--ack-repo-mcp=YYYY-MM-DD` with today's UTC date.
- `breakglass` unmasks repository MCP surfaces after
  `--ack-breakglass=YYYY-MM-DD` with today's UTC date. Workcell does not inspect
  MCP server behavior.
- Rules forbid the listed forms of recursive forced removal. Rules also forbid
  `git reset --hard`. For force-push, rules forbid these patterns:
  `git push <force-flag> ...` and
  `git push origin <listed-branch> <force-flag>`. The other push forms can
  require confirmation or match no rule. Do not claim that the rules block all
  history changes.

## Working rules

- Keep the policy files aligned. If you change one security boundary, update
  `config.toml`, `managed_config.toml`, `requirements.toml`, and
  `rules/default.rules` together.
- Prefer prompt over allow, and forbid over prompt for destructive actions.
- Use `codex execpolicy check` when changing rules so the strictest decision is
  visible before merging.
- Keep the configuration usable by default. One command must start the safe
  session without a long checklist.

## Review order

When evaluating a change, check:

1. Does it preserve the runtime boundary?
2. Does it preserve the command and MCP restrictions?
3. Does it reduce operator friction or make setup harder?
4. Does it add unnecessary configuration surface?

## Agent authoring

- Keep agent personas short and task-specific.
- Describe what the agent should preserve, not generic roleplay.
- Include the files and control points the agent should inspect.
- Do not add instructions that depend on Claude-specific behavior.
