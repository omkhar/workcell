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
- split git-admin directories outside the mounted workspace

## Invariant 2: Writes stay inside the intended workspace

The active provider must not be able to modify files outside the chosen task
workspace unless the operator enters an explicit `breakglass` path.

## Invariant 3: Repo policy must not silently widen trust

The repository must not auto-enable project MCP servers, arbitrary networked
tools, or higher-trust profiles just because the files exist.
It should also refuse broad non-git workspace mounts and unsafe provider
command overrides unless the operator opts into them explicitly.
On the safe path it should also mask repo-local provider control files and
mutable git hook/config paths so workspace content cannot silently retake the
control plane between runs. Home-scoped provider control files must be
re-seeded on provider launch, and nested coding-agent CLI launches must not be
an unmediated escape hatch. Public in-container launch surfaces must also
sanitize hostile loader environment variables before shell or provider wrapper
logic starts. This claim applies to the public launcher surface under
`/usr/local/bin`, not
to internal support scripts under `/usr/local/libexec/workcell/*.sh`. For
node-based adapters, Workcell blocks the shipped provider entrypoints and
repackaged workspace copies of the shipped provider package trees on the public
`node` surface, and the public `node` surface also blocks native addon loading
so workspace JS cannot retake native code execution. Workcell still does not
claim to classify arbitrarily rewritten workspace JavaScript as vendor code.

## Invariant 4: Network egress is explicit

Ambient outbound network access is not an acceptable default. Tier 1 must use a
named network mode:

- `strict`: allowlisted runtime endpoint access only
- `build`: expanded allowlist for package registries and build tooling
- `breakglass`: unrestricted outbound access, explicitly chosen and acknowledged

On the allowlist path, Workcell enforces reviewed destination-IP allowlists
derived from the configured hostnames at launch time. It programs both IPv4
and, when available in the VM, IPv6 rules; if IPv6 enforcement is unavailable,
Workcell disables IPv6 rather than leaving an unmanaged parallel egress path.
The current enforcement model is not a hostname-aware proxy. `strict` does not
perform cold image builds or rebuilds; runtime-image creation is an explicit
`build`-mode operation that may temporarily apply a separate pinned bootstrap
endpoint set before returning to the steady-state runtime allowlist.

## Invariant 5: Destructive command paths have defense in depth

Recursive deletion, raw block writes, privileged host mutation, and history
rewrites must not depend on prompt quality alone. They need policy controls plus
the outer runtime boundary.
Common git hook-bypass flags such as `--no-verify`, `git commit -n`, and inline
`core.hooksPath` overrides are part of the same class and are not allowed on
the Tier 1 path. The runtime should also prefer `noexec` scratch space by
default and reserve exec-capable temporary storage for the explicit per-session
state area. On `strict`, direct native ELF launches from mutable `/workspace`
and `/state` paths are blocked so helper binaries cannot bypass the managed
launcher, and mutable shebang scripts cannot jump straight to protected real
runtimes or loaders that target them.

## Invariant 6: Lower-assurance modes are explicit

If a workflow cannot preserve the Tier 1 guarantees, it must be labeled
lower-assurance rather than presented as equivalent.

## Invariant 7: Autonomous runs remain auditable

The secure path must preserve enough durable host-side information to
reconstruct:

- which workspace was used
- which runtime profile was active
- which network mode was applied
- which provider adapter was selected
- whether the run stayed on the managed Tier 1 path or used an explicitly
  lower-assurance mode

The reference launcher satisfies this by appending an operator-visible audit log
entry under the managed Colima profile directory on each real launch.

## Profile expectations

### `strict`

- dedicated Colima VM profile
- hardened inner container
- ephemeral in-container home
- no host credential or socket passthrough
- provider-native bounded or constrained mode only where the provider offers
  one, with the shared Workcell runtime remaining the primary boundary
- allowlisted steady-state egress only
- requires a prebuilt reviewed runtime image; `strict` does not rebuild or
  cold-bootstrap that image

### `build`

- same VM and container boundary as `strict`
- same secret and mount restrictions
- broader network allowlist for dependency and build traffic
- reviewed runtime-image creation and rebuilds happen here, with any temporary
  bootstrap allowlist logged separately and restored before the session starts
- still no host credential or control-plane passthrough

### `breakglass`

- still inside the dedicated VM plus container boundary
- unrestricted network, with any adapter-specific unsafe provider mode selected
  only through an explicitly lower-assurance direct provider path rather than
  auto-injected by the managed entrypoint
- explicitly selected, acknowledged, and visibly different from the safe path

## Non-goals

This repository does not claim:

- equivalent guarantees for host-native GUI, web, or cloud surfaces
- protection from a compromised host administrator
- perfect network isolation when the operator bypasses the wrapper
