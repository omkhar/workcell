# Threat Model

## Assets

- host credentials and long-lived tokens
- host filesystem outside the task workspace
- git history and remote branch integrity
- Workcell policy, runtime, and reviewer configuration
- autonomous-run audit metadata

## Trust boundaries

1. macOS host
2. dedicated Colima VM profile
3. hardened inner container
4. active provider process and provider-managed shell commands
5. mounted task workspace
6. explicitly enabled MCP servers and outbound network destinations

## Main attacker models

### Malicious repository content

The repository being analyzed may contain prompt injection, dangerous scripts,
malicious dependency metadata, and files intended to exfiltrate secrets or push
the agent into destructive actions.

### Malicious or compromised MCP server

An MCP server may try to widen trust, obtain credentials, or cause unexpected
mutations.

### Mis-scoped operator trust

An operator may accidentally run the tool outside the wrapper, use the wrong
profile, or mount host state that should remain outside the trust boundary.

## Primary abuse paths

1. Read host secrets through ambient mounts or inherited environment.
2. Escape the intended workspace by using broad writable roots or host
   passthrough.
3. Use ambient network access to exfiltrate code or pull malicious tooling.
4. Rewrite git history or mutate shared branches from an autonomous workflow.
5. Expand tool reach by enabling project MCP servers, dangerous profiles, or
   weaker host-native GUI paths.

## Controls

- dedicated Colima VM profile rather than the shared default profile
- non-privileged inner container with `no-new-privileges`
- explicit mount allowlist
- Workcell profile split: `strict`, `build`, `breakglass`
- disabled web search by default
- explicit MCP definitions and disabled-by-default posture
- command guardrails in `.rules`
- VM-level egress policy rather than extra container capabilities

## Residual risks

- If the operator bypasses the wrapper and runs Codex on the host, Tier 1 is
  lost.
- Bind mounts remain the main path from the VM into host-controlled data.
- Network allowlists based on resolved IPs are best-effort and must be refreshed
  when endpoints change.
- Lower-assurance app and IDE flows may remain necessary for some workflows.
