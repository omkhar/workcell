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

Explicit operator-owned injection is a separate path, not a default exception.
When an operator chooses to inject docs, config, or secrets, Workcell must
copy or mount them into per-session runtime state and keep them out of the
immutable adapter baseline, workspace mount, durable host logs, and ambient
environment. Provider credentials injected through the dedicated
`[credentials]` path must not be re-staged into a second plaintext host-side
bundle before launch; they are validated on the host, mounted read-only for the
current session, and then copied into the ephemeral agent home.
Secret-bearing sources used by the injection policy must be explicit files or
directories owned by the invoking UID, must not be symlinks, and must not be
group- or world-readable by default. Credential entries may also be scoped to
specific providers or runtime modes so a safe launch does not receive broader
credential material than it needs.

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
control plane between runs. Repo-local `AGENTS.md`, `CLAUDE.md`, and
`GEMINI.md` may be imported into provider-native home docs from hidden
read-only copies, but the workspace-visible files remain masked. Home-scoped
provider control files must be re-seeded on provider launch, and nested
coding-agent CLI launches must not be an unmediated escape hatch. Public
in-container launch surfaces must also
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
derived from the configured hostnames at launch time. It programs an IPv4
allowlist and a deny-by-default IPv6 chain for every allowlist launch, adding
explicit IPv6 allowlist entries when the resolved destination set is dual-stack.
If `ip6tables` enforcement is unavailable in the VM, Workcell fails closed
rather than silently downgrading to IPv4-only enforcement.
The current enforcement model is not a hostname-aware proxy. `strict` does not
perform cold image builds or rebuilds; runtime-image creation is an explicit
`build`-mode operation that may temporarily apply a separate pinned bootstrap
endpoint set before returning to the steady-state runtime allowlist. When the
operator explicitly opts into `ephemeral` container mutability, the steady-state
allowlist also includes the pinned Debian snapshot endpoints used by
`apt` and `apt-get` so transient build tooling can be installed without enabling
arbitrary distro mirrors. A successful package mutation is an explicit
lower-assurance event for the live session because maintainer scripts run as
root inside the mutable container. Workcell must warn when that happens and
must not imply that the rest of the session preserves the same in-container
control-plane integrity as a readonly launch.

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
That includes provider prompt-autonomy mode and any mutable session that has
successfully performed package-manager mutations as root. It also includes any
session that explicitly opts into writable Codex execpolicy rules.

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
under the managed Colima profile directory on each real launch and exit,
including package-mutation downgrade events inferred from the session-assurance
marker that the launcher copies out of the runtime after exit. Injected secret
values themselves are not part of that durable record. By default, this durable
audit is metadata-only. Full host-persisted stdout/stderr debug capture and
full interactive transcript capture are explicit opt-in observability paths and
are classified as lower-assurance because they can persist provider content
outside the ephemeral runtime boundary. The transcript path retains terminal I/O
plus session timestamps and the final exit code, but not the raw host launch
command.

## Profile expectations

### `strict`

- dedicated Colima VM profile
- hardened inner container
- ephemeral in-container home
- no host credential or socket passthrough
- provider-native bounded or constrained mode only where the provider offers
  one, with the shared Workcell runtime remaining the primary boundary
- allowlisted steady-state egress only
- pinned Debian snapshot egress for `apt` and `apt-get` when explicit
  `ephemeral` container mutability is active
- requires a prebuilt prepared runtime image; `strict` does not rebuild or
  cold-bootstrap that image
- requires active Docker seccomp support in the managed runtime before launch
- may mount an opt-in host-persisted non-secret cache plane for package
  indexes and compiler caches when `cache_profile=standard`; the default cache
  namespace is scoped to the current workspace, and this is still a
  lower-assurance path and is not part of the clean `strict` session boundary

Assurance mapping within `strict`:

- `ephemeral`: default developer lane, `container_assurance=managed-mutable`
- `ephemeral` adds only the handoff capabilities required to move from root to
  the mapped runtime user inside the container: `SETUID`, `SETGID`
- `readonly`: strongest managed lane for `strict`,
  `container_assurance=managed-readonly`
- default managed autonomy: `autonomy_assurance=managed-yolo`
- prompt autonomy: separate lower-assurance flag,
  `autonomy_assurance=lower-assurance-prompt-autonomy`
- default configured Codex rules posture:
  `codex_rules_assurance_configured=managed-immutable-rules`
- default effective Codex rules posture:
  `codex_rules_assurance_effective_initial=managed-immutable-rules`
- session Codex rules mutability: explicit lower-assurance flag,
  `codex_rules_assurance_configured=lower-assurance-session-rules`
- prompt autonomy can also force the initial effective Codex rules posture to
  `codex_rules_assurance_effective_initial=lower-assurance-session-rules`
- package mutation can also force this session-local copy for the remainder of
  an already-downgraded container
- successful package mutation: runtime downgrade to
  `lower-assurance-package-mutation` until exit

### `build`

- same VM and container boundary as `strict`
- same secret and mount restrictions
- broader network allowlist for dependency and build traffic
- prepared runtime-image creation and rebuilds happen here, with any temporary
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
