# Repository Working Agreement

This repository exists to provide a bounded local runtime with explicit
isolation and policy controls for coding agents by combining the Workcell
runtime boundary with provider-specific adapters.

## Priorities

1. Developer experience
2. Simplicity
3. Security invariants
4. Performance
5. Idiomatic correctness

These priorities apply only within the defined invariant set. Do not trade away
the runtime boundary or explicit security guarantees in the name of convenience.

## Mandatory rules

- Sign every commit. Do not create or rewrite commits in this repository
  without a verified signature from the maintainer identity.
- Treat final GitHub publication as a host-side action. Prepare branch,
  signed commit message, and PR metadata inside Workcell, then use
  `workcell publish-pr` on the host rather than publishing directly from the
  Tier 1 in-container session.
- Do not treat provider config, prompt files, or rules as the sole security
  boundary.
- Preserve the dedicated VM plus container boundary as the Tier 1 design for
  all supported CLI adapters.
- Prefer explicit, auditable configuration over hidden magic.
- Mark lower-assurance modes clearly instead of overstating guarantees.
- Keep host mounts minimal. Never mount `$HOME`, host keychains, or host
  credential stores.
- Never pass through host sockets or auth state including `docker.sock`,
  `ssh-agent`, GPG agent sockets, launchd sockets, host `~/.codex`, or git
  credential-helper state.
- Keep `breakglass` paths explicit, narrow, and separately documented.
- Require explicit operator acknowledgement for `breakglass` or equivalent
  higher-trust paths.
- Treat non-git workspaces and arbitrary container commands as opt-in debugging
  paths, not the default developer flow.
- Mask repo-local provider control files and mutable git hook/config paths on
  the safe path so workspace content cannot silently take over the control
  plane.
- Ship invariant checks with new controls whenever practical.

## Change discipline

- Root files define shared contracts; keep them concise.
- `runtime/`, `policy/`, `adapters/`, `verify/`, and `workflows/` should evolve
  in lockstep.
- If a security control depends on a specific runtime assumption, document that
  assumption in the same change.
- Keep one shared boundary and many thin adapters. Do not hide provider
  differences behind a fake universal abstraction.
- Prefer small scripts and plain configuration over framework-heavy machinery.
