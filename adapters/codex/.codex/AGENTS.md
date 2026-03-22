# Codex Policy

This repository defines the secure Codex runtime and policy slice for OpenAI's
coding agents.

## Priorities

1. Developer experience.
2. Simplicity.
3. Preserve the security invariants.
4. Performance.
5. Idiomatic correctness.

## Non-negotiables

- Tier 1 runs inside a dedicated Colima VM profile plus a hardened inner
  container. Do not treat the shared host as part of the trust boundary.
- Do not mount host home directories, keychains, browser profiles, SSH/GPG
  material, cloud credentials, or `docker.sock`.
- Network is off by default. Only the build profile may opt into allowlisted
  outbound access.
- External MCP is opt-in and allowlisted. Never enable project-shipped MCP
  servers automatically.
- Destructive shell commands and history-rewriting git commands stay blocked.

## Working rules

- Keep the policy files aligned. If you change one security boundary, update
  `config.toml`, `managed_config.toml`, `requirements.toml`, and
  `rules/default.rules` together.
- Prefer prompt over allow, and forbid over prompt for destructive actions.
- Use `codex execpolicy check` when changing rules so the strictest decision is
  visible before merging.
- Keep the configuration usable by default. One command should be enough to get
  a safe session, and the safe session should not require a long checklist.

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
