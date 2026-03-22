# Security Invariants

These invariants are the contract for this repository. Everything else,
including the priority order and provider adapter behavior, is subordinate to
them.

## Invariant 1: Host secrets stay outside the default trust boundary

Tier 1 must not expose host secrets to the active provider by default. That
includes:

- host home directories
- host provider state directories
- keychains and browser profiles
- SSH, GPG, and similar agent sockets
- cloud credentials and git credential helper state
- container or VM control sockets such as `docker.sock`

## Invariant 2: Writes stay inside the intended workspace

The active provider must not be able to modify files outside the chosen task
workspace unless the operator enters an explicit `breakglass` path.

## Invariant 3: Repo policy must not silently widen trust

The repository must not auto-enable project MCP servers, arbitrary networked
tools, or higher-trust profiles just because the files exist.

## Invariant 4: Network egress is explicit

Ambient outbound network access is not an acceptable default. Tier 1 must use a
named network mode:

- `strict`: allowlisted service access only
- `build`: expanded allowlist for package registries and build tooling
- `breakglass`: unrestricted outbound access, explicitly chosen

## Invariant 5: Destructive command paths have defense in depth

Recursive deletion, raw block writes, privileged host mutation, and history
rewrites must not depend on prompt quality alone. They need policy controls plus
the outer runtime boundary.
Common git hook-bypass flags such as `--no-verify`, `git commit -n`, and inline
`core.hooksPath` overrides are part of the same class and are not allowed on
the Tier 1 path.

## Invariant 6: Lower-assurance modes are explicit

If a workflow cannot preserve the Tier 1 guarantees, it must be labeled
lower-assurance rather than presented as equivalent.

## Invariant 7: Autonomous runs remain auditable

The secure path must preserve enough information to reconstruct:

- which workspace was used
- which runtime profile was active
- which network mode was applied
- which provider adapter was selected
- which provider-native mode or profile was selected

## Profile expectations

### `strict`

- dedicated Colima VM profile
- hardened inner container
- ephemeral in-container home
- no host credential or socket passthrough
- provider-native bounded or constrained mode where the provider offers one
- allowlisted egress only

### `build`

- same VM and container boundary as `strict`
- same secret and mount restrictions
- broader network allowlist for dependency and build traffic
- still no host credential or control-plane passthrough

### `breakglass`

- still inside the dedicated VM plus container boundary
- unrestricted network and the provider's highest-trust execution mode
- explicitly selected and visibly different from the safe path

## Non-goals

This repository does not claim:

- equivalent guarantees for host-native GUI, web, or cloud surfaces
- protection from a compromised host administrator
- perfect network isolation when the operator bypasses the wrapper
