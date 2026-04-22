# Workcell System Design

## Purpose

This document describes the current Workcell architecture as implemented in the
repository today. Roadmap direction and future product strategy live in
[`ROADMAP.md`](../ROADMAP.md) and
[`docs/implement-first-delivery-plan.md`](implement-first-delivery-plan.md)
rather than in this design doc.

## Executive Summary

Workcell is a host-launched, policy-driven runtime for coding agents. Its
current architecture is:

1. a trusted host control plane in [`scripts/workcell`](../scripts/workcell)
   prepares and validates each session
2. a dedicated Colima VM provides the primary strict machine boundary on
   supported Apple Silicon macOS hosts, while an explicit Docker Desktop
   compatibility target reuses the host-owned control plane without claiming
   the same VM boundary
3. a hardened container inside the selected target runtime provides the agent
   execution environment
4. thin provider adapters seed provider-native homes and configuration without
   pretending provider config is the security boundary
5. host-side policy rendering, control-plane masking, and explicit injection
   determine what material enters the runtime
6. host-side durable session records, audit logs, and detached session control
   make the runtime inspectable without introducing a daemonized control plane

The main architectural point is unchanged across providers: Workcell does not
trust prompt files, provider settings, or repo-local control-plane files as the
primary boundary. The supported boundary is the host-controlled VM plus
container path, reinforced by explicit injection, managed provider homes,
control-plane masking, provider wrappers, and release-time provenance.

## Design Goals

The repository working agreement prioritizes:

1. developer experience
2. simplicity
3. security invariants
4. performance
5. idiomatic correctness

Within that order, the system is designed to:

- keep the trusted control plane on the host
- preserve a dedicated VM plus container boundary for supported adapters
- make lower-assurance paths explicit instead of silently widening trust
- keep provider-specific behavior visible instead of hiding it behind a fake
  universal abstraction
- prefer auditable configuration and invariant checks over hidden magic

## Non-Goals

Workcell is not trying to be:

- a generic shared-kernel container sandbox
- a provider-agnostic agent framework that erases real CLI differences
- a repo-local prompt/config convention presented as a security system
- a remote worker or cloud-agent platform today
- a centralized enterprise policy or analytics plane today

## System Overview

### 1. Host Control Plane

The main orchestrator is [`scripts/workcell`](../scripts/workcell). The host
control plane is responsible for:

- scrubbing the host environment before launch
- resolving trusted host binaries and helper scripts
- computing and validating the Colima profile for the workspace
- validating runtime assumptions such as `vz`, mount shape, and the bounded
  Docker path
- loading auth policy and injection policy
- resolving host-side credential sources before launch
- rendering the staged injection bundle and metadata
- shadowing repo-local control-plane paths on the safe path
- applying the VM egress allowlist
- preparing the runtime image when required
- launching the runtime container with controlled mounts, tmpfs state, and
  managed environment variables
- recording audit metadata, assurance state, and detached session records

Operationally, `scripts/workcell` is the real control plane. Trust-sensitive
decisions are made before the provider CLI starts.

### 2. Host-Side Structured Helpers

The shell launcher delegates structured policy and metadata work to small Go
packages under [`internal/`](../internal):

- [`internal/authpolicy`](../internal/authpolicy): host-side auth-policy state
  and `workcell auth ...`
- [`internal/authresolve`](../internal/authresolve): host-side credential source
  resolution and preprocessing
- [`internal/injection`](../internal/injection): staged injection bundle
  rendering and validation
- [`internal/hostutil`](../internal/hostutil): session records, timeline/export
  helpers, and host utility subcommands
- [`internal/metadatautil`](../internal/metadatautil): metadata and validation
  helpers used by repo checks and release posture
- [`internal/runtimeutil`](../internal/runtimeutil): runtime-related utility
  helpers

This split keeps orchestration visible in shell while moving structured data
validation and rendering into typed code.

### 3. Runtime Boundary and Profiles

The current local runtime model has two explicit target classes:

1. `local_vm/colima/strict`: dedicated Colima VM on the host plus a hardened
   agent container inside that VM
2. `local_compat/docker-desktop/compat`: host-owned compatibility target that
   reuses the same hardened container and control plane without claiming the
   same VM boundary

There is no silent fallback between these targets. Backend selection is an
explicit operator choice, and unsupported or unhealthy targets fail closed.

The container launch path applies controls such as:

- `--init`
- `--security-opt no-new-privileges:true`
- `--cap-drop ALL`
- `--pids-limit 1024`
- tmpfs mounts for `/tmp`, `/run`, and `/state`
- a tightly managed environment
- a selected workspace mount at `/workspace`

Runtime profiles in [`runtime/profiles/`](../runtime/profiles) separate
posture and expectations:

- `strict`: default managed provider lane
- `development`: managed interactive lane with broader command flexibility
- `build`: broader preparation and dependency-refresh lane
- `breakglass`: explicit higher-trust lane requiring acknowledgement

The system relies on profile-specific guarantees, not on provider prompt text
that describes those guarantees after the fact.

### 4. Workspace and Control-Plane Isolation

On the safe path, Workcell prevents mutable workspace content from silently
becoming the live provider control plane.

Repo-local control-plane paths such as:

- `AGENTS.md`
- `CLAUDE.md`
- `GEMINI.md`
- `.mcp.json`
- `.codex/`
- `.claude/`
- `.gemini/`
- mutable git hook and config paths

are masked or shadowed by Workcell and then imported into the managed provider
home as reviewed inputs.

For tracked files, the shadow path can materialize content from the git index
instead of live mutable workspace state. That narrows the gap between what the
operator reviewed and what the runtime consumes.

### 5. Injection and Credential Flow

Workcell's supported path for credentials, shared GitHub state, SSH material,
and reviewed documentation fragments is explicit injection policy.

The flow is:

1. load the injection policy on the host
2. resolve host-side credential sources and selector scope
3. render a staged injection bundle and manifest
4. mount the staged bundle into controlled runtime paths
5. reseed the managed provider home from approved sources

Important properties of the current implementation:

- secrets are staged and classified explicitly
- supported target paths are tightly enumerated
- reserved provider-home paths cannot be overwritten arbitrarily
- shared GitHub and SSH inputs are opt-in and policy-scoped
- resolver-backed credentials are a host preprocessing step, not live host
  socket or keychain passthrough
- direct staged credentials are the primary supported auth path today
- built-in resolver coverage remains narrow and explicit:
  `codex-home-auth-file` is the reviewed Codex host-auth reuse path, while
  `claude-macos-keychain` remains a fail-closed scaffold rather than a launch
  path

### 6. Container Entry and Provider Launch Chain

Inside the container, the launch chain is deliberately layered:

1. [`runtime/container/entrypoint.sh`](../runtime/container/entrypoint.sh)
2. [`runtime/container/runtime-user.sh`](../runtime/container/runtime-user.sh)
3. [`runtime/container/home-control-plane.sh`](../runtime/container/home-control-plane.sh)
4. [`runtime/container/provider-wrapper.sh`](../runtime/container/provider-wrapper.sh)
5. provider binary

This chain exists to keep the provider home, runtime state, and policy checks
under Workcell control.

`entrypoint.sh` validates the managed environment, maps the runtime user for
mutable sessions, seeds runtime state, and routes lower-assurance development
commands only through the managed wrapper path.

`home-control-plane.sh` rebuilds the effective provider home under
`/state/agent-home`, verifies control-plane artifacts, layers baseline and
imported docs, and seeds auth, MCP, rules, and SSH material.

`provider-wrapper.sh` and
[`runtime/container/provider-policy.sh`](../runtime/container/provider-policy.sh)
then sanitize the environment and reject trust-widening provider flags or
config overrides before launching the provider.

### 7. Runtime Mutability and Assurance

Workcell distinguishes between the default managed path and explicit
lower-assurance behavior.

Examples already implemented in the runtime:

- `development` is a managed lane, but not equivalent to `strict`
- mutable package installation is mediated and recorded as a downgrade
- prompt autonomy, transcript capture, and other widened paths are explicit
- `breakglass` is separate and acknowledged rather than silently inherited

Assurance is therefore a runtime property that Workcell records and surfaces,
not a marketing label applied equally to every mode.

### 8. Exec Guard and Wrapper Defense in Depth

The deepest technical enforcement layer is the Rust exec guard under
[`runtime/container/rust/`](../runtime/container/rust), loaded with
`LD_PRELOAD`.

Its role is to prevent bypasses beneath shell wrappers, including:

- direct protected-runtime execution outside approved wrappers
- shebang execution from mutable paths targeting protected runtimes
- native execution paths that bypass policy on the strict path
- git hook and config bypass patterns

Workcell reinforces this with command wrappers such as:

- [`runtime/container/bin/git`](../runtime/container/bin/git)
- [`runtime/container/bin/node`](../runtime/container/bin/node)
- [`runtime/container/public-node-guard.mjs`](../runtime/container/public-node-guard.mjs)

The result is defense in depth below prompt files, shell aliases, and
provider-native settings.

### 9. Thin Provider Adapters

Provider integration remains intentionally thin and explicit:

- Codex: managed config, requirements, rules, and MCP material under the
  Workcell boundary
- Claude Code: reviewed settings, baseline instructions, auth mirrors, MCP
  config, and shell-hook policy support
- Gemini CLI: managed settings, trust-state seeding, and explicit auth material
  such as `.env`, OAuth, project, and optional Vertex supplement files

Workcell keeps one shared boundary and many thin adapters. It does not hide the
real provider differences behind a fake universal control plane.

### 10. Runtime Target Taxonomy And Remote VM Contract

The current live local targets are the strict `local_vm/colima` boundary and
the explicit lower-assurance `local_compat/docker-desktop` path. Phase 5 adds
a canonical preview-only `remote_vm` contract in
[`policy/remote-vm-contract.json`](../policy/remote-vm-contract.json) plus a
shared fake target and conformance harness in
[`internal/remotevm`](../internal/remotevm).

That contract fixes the control-plane meanings that later remote providers
must reuse:

- explicit host-owned remote workspace materialization rather than an implicit
  live host mount
- brokered access and bootstrap metadata rather than ambient host socket or
  credential passthrough
- `target kind`, `assurance class`, `runtime API`, and `workspace transport`
  as separate recorded concepts in session and audit state
- a shared fake target plus deterministic conformance harness that later cloud
  adapters must pass unchanged instead of redefining contract suites per
  provider

This does not mean a cloud backend ships today. It means the provider-neutral
contract is now fixed in-repo before later `remote_vm` adapters try to consume
it.

### 11. Host-Side Detached Session Plane

Workcell now includes a host-owned detached session plane. The current CLI
surface includes:

- `workcell session start`
- `workcell session attach`
- `workcell session send`
- `workcell session stop`
- `workcell session list`
- `workcell session show`
- `workcell session delete`
- `workcell session logs`
- `workcell session timeline`
- `workcell session diff`
- `workcell session export`

The supported operator inventory is maintained in
[`policy/operator-contract.toml`](../policy/operator-contract.toml). This
system-design doc explains shape and rationale; it should not be treated as the
authoritative CLI inventory.

Detached session records are written under the Workcell-owned target-state tree
as
`~/.local/state/workcell/targets/<target-kind>/<target-provider>/<profile>/sessions/<session-id>.json`.
For the default strict path that means
`~/.local/state/workcell/targets/local_vm/colima/<profile>/sessions/<session-id>.json`.
For the Docker Desktop compatibility path that means
`~/.local/state/workcell/targets/local_compat/docker-desktop/<profile>/sessions/<session-id>.json`.
Compatibility reads still accept older legacy records under
`~/.colima/<profile>/sessions/<session-id>.json`. The current session record
schema in [`internal/hostutil/sessions.go`](../internal/hostutil/sessions.go)
includes:

- session identity, profile, target kind, target provider, target id,
  target assurance class, runtime API, agent, mode, and UI
- workspace path, workspace origin, workspace root, and worktree path
- workspace transport as a distinct recorded concept alongside workspace paths
- git branch, head, and clean-base metadata
- container name, monitor pid, status, and live status
- audit, debug, file-trace, and transcript log pointers
- start, observe, and finish timestamps
- initial, current, and final assurance
- workspace control-plane state

Detached sessions default to `--session-workspace isolated`, which clones a
clean per-session git worktree on the host for supported source workspaces.

The current session plane is intentionally file-backed and host-owned. It does
not require a separate daemon, local socket trust, or centralized service.

### 12. Verification and Release Posture

The repository pairs runtime controls with verification and provenance
material:

- [`verify/invariants/`](../verify/invariants) and
  [`docs/invariants.md`](invariants.md) define the safe-path contract
- [`docs/threat-model.md`](threat-model.md) documents trust boundaries and
  abuse paths
- [`docs/validation-scenarios.md`](validation-scenarios.md) tracks scenario
  coverage and remaining gaps
- [`docs/provenance.md`](provenance.md) and [`docs/releasing.md`](releasing.md)
  describe release-time signing, attestation, and publication expectations

Final GitHub publication is intentionally host-side. In this repository the
supported `main`-based PR path goes through `./scripts/repo-publish-pr.sh`,
which delegates to `workcell publish-pr` after fresh local parity evidence
exists. That keeps branch push, commit signing, and PR creation outside the
bounded Tier 1 runtime.

## End-to-End Flow

### Safe Launch Flow

The normal strict-path launch is:

1. the operator runs `workcell --agent <provider> --workspace <repo>`
2. the host launcher scrubs the environment and validates workspace and profile
   assumptions
3. auth and injection policy are resolved on the host
4. repo-local control-plane files are shadowed or imported through Workcell
5. the runtime image and VM egress posture are prepared
6. the container starts with hardened flags and managed environment variables
7. the managed provider home is rebuilt under `/state/agent-home`
8. the provider wrapper validates flags and starts the provider inside the
   bounded runtime

### Detached Session Flow

The current detached flow adds host-owned lifecycle state:

1. `workcell session start` allocates a durable session id
2. detached sessions default to an isolated per-session workspace when the
   source workspace supports it
3. the launcher writes a durable session record and audit state on the host
4. the runtime executes in the same bounded environment as foreground launches
5. the operator can later inspect, attach, steer, stop, diff, or export the
   session from the host

### Publish Flow

The supported publication path remains:

1. run the provider inside Workcell
2. review the resulting changes
3. run local `pr-parity`, then publish from the host with
   `./scripts/repo-publish-pr.sh`

This keeps commit signing and repository publication outside the in-container
runtime boundary.

## Current Limits

The current architecture is intentionally narrower than the longer-term
roadmap:

- Apple Silicon macOS hosts only
- no Workcell-managed cloud or remote worker plane today
- no centralized enterprise policy, session administration, or analytics plane
- no queue, pause/resume, checkpoint, or fork model yet on the session plane
- GUI and IDE surfaces are lower assurance unless they act only as clients to
  the same bounded runtime
- built-in auth resolver coverage remains narrow, with direct staged inputs as
  the primary supported auth path plus Codex host-auth reuse and the
  fail-closed Claude macOS scaffold
- the strongest macOS boundary claim still depends on local validation rather
  than GitHub-hosted CI alone

## Light Comparison

Compared with cloud-first or IDE-first agent systems, Workcell currently
prioritizes:

- explicit local runtime boundaries over remote execution
- host-owned publication and signing over in-session repository publication
- reviewed provider-home seeding over trusting repo-local mutable config as the
  live control plane
- explicit lower-assurance labeling over flattening all modes into one trust
  story

Roadmap direction such as broader session orchestration, deployment reach, and
team-level administration lives in [`ROADMAP.md`](../ROADMAP.md), not in this
architecture description.
