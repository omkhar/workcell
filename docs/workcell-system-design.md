# Workcell System Design and Feature Review

## Purpose

This document explains how Workcell works today based on the repository's
runtime code, policy code, adapters, and verification material. It also
compares the current design to modern coding-agent systems and identifies the
single next feature that should be added to Workcell first.

## Executive Summary

Workcell is a host-launched, policy-driven runtime for coding agents. Its core
design is:

1. A trusted host control plane prepares and validates the session.
2. A dedicated Colima VM provides the primary machine boundary on macOS.
3. A hardened container inside that VM provides the agent execution runtime.
4. Thin provider adapters seed provider-native homes and configuration without
   pretending the providers are interchangeable.
5. Multiple policy layers prevent the workspace from silently taking over the
   control plane.
6. Verification and provenance tooling try to make the boundary auditable and
   reproducible.

The most important architectural fact is that Workcell does not treat prompt
files, provider settings, or repo rules as the security boundary. The real
boundary is the host-controlled VM plus container path, reinforced by control
plane masking, provider wrappers, syscall-level exec interception, egress
controls, and explicit credential injection.

The biggest missing product capability is not stronger isolation. Workcell is
already strong there. The biggest missing capability is a first-class session
supervisor that can run, track, resume, and compare multiple isolated agent
sessions in parallel.

## Design Goals

The repository's own priorities are clear:

1. Developer experience
2. Simplicity
3. Security invariants
4. Performance
5. Idiomatic correctness

Within that frame, Workcell optimizes for these system properties:

- Keep the trusted control plane on the host.
- Preserve a dedicated VM plus container boundary for supported adapters.
- Make lower-assurance paths explicit instead of silently widening trust.
- Keep provider-specific behavior visible rather than hiding it behind a fake
  abstraction layer.
- Prefer auditable configuration and invariant checks over implicit behavior.

## Non-Goals

Workcell is not trying to be:

- A generic shared-kernel container sandbox.
- A provider-agnostic agent framework that erases real CLI differences.
- A repo-local prompt/config convention presented as a security system.
- A host-side PR publishing tool that pushes from inside the bounded session.

## System Overview

### 1. Host Control Plane

The main control plane is [`scripts/workcell`](../scripts/workcell). It is a
host-side launcher, validator, policy renderer, and audit collector.

Its core responsibilities are:

- Scrub the host environment before session launch.
- Resolve trusted host binaries and helper scripts.
- Compute a deterministic Colima profile per workspace.
- Validate the Colima runtime assumptions:
  - `vz` VM type
  - `virtiofs` mount type
  - Docker runtime
  - exactly one writable workspace mount
  - no SSH agent forwarding
- Resolve injection policy and credential sources.
- Render a staged injection bundle on the host.
- Shadow repo-local control-plane paths on the safe path.
- Apply the VM egress allowlist.
- Build or prepare the runtime image.
- Launch the container with controlled mounts, tmpfs mounts, and managed env.
- Record session metadata, assurance metadata, and optional debug logs.

Operationally, `scripts/workcell` is the system's real orchestrator today.
Almost every trust-sensitive decision is made before the provider CLI starts.

### 2. Host Policy Helpers

The host launcher delegates structured policy work to small Go packages in
`internal/` and `cmd/`.

Important components include:

- `internal/authpolicy`
  - manages local auth-policy state via `workcell auth`.
- `internal/authresolve`
  - resolves credential sources before launch.
- `internal/injection`
  - renders the staged injection bundle and manifest.
- `internal/assurance`
  - models session assurance metadata.
- `internal/endpointpolicy`
  - tracks extra endpoint handling.

This split is healthy. The shell launcher owns orchestration, while the Go code
owns structured data validation and rendering.

### 3. Runtime Boundary

The runtime boundary is two-tier on the safe path:

1. A dedicated Colima VM on the host.
2. A hardened agent container inside that VM.

The container launch path applies controls such as:

- `--init`
- `--security-opt no-new-privileges:true`
- `--cap-drop ALL`
- `--pids-limit 1024`
- tmpfs mounts for `/tmp`, `/run`, and `/state`
- a tightly constrained environment
- a single writable workspace mount

Modes in `runtime/profiles/*.env` tune network posture and resources:

- `strict`
  - deny-by-default style safe path with allowlisted egress.
- `development`
  - broader developer convenience, still managed.
- `build`
  - broader build-oriented access.
- `breakglass`
  - explicit higher-trust mode with widened behavior.

### 4. Workspace and Control-Plane Isolation

One of Workcell's strongest and most unusual controls is workspace
control-plane masking.

On the safe path, `scripts/workcell` prevents mutable workspace content from
becoming the active provider control plane. It masks or shadows paths such as:

- `AGENTS.md`
- `CLAUDE.md`
- `GEMINI.md`
- `.mcp.json`
- `.codex/`
- `.claude/`
- `.gemini/`
- editor control directories
- mutable git hook/config paths

For tracked files, the masking path materializes content from the git index
rather than the live working tree. That matters because it narrows the gap
between "what the developer reviewed" and "what the runtime consumes."

The workspace can still provide reviewed instructions, but only through an
import path controlled by Workcell rather than by direct trust of live repo
state.

### 5. Injection and Credential Flow

Workcell's injection model is the supported path for credentials, SSH material,
and reviewed documentation fragments.

The flow is:

1. The host loads the injection policy.
2. Credential sources are resolved on the host.
3. A staged injection bundle is rendered with a manifest.
4. Files are mounted into controlled paths in the container.
5. Session home seeding copies or links only approved targets.

Important properties:

- Secrets are staged and classified explicitly.
- Supported target paths are tightly enumerated.
- Reserved home paths cannot be overwritten arbitrarily.
- SSH material is validated and constrained.
- Resolver-backed credentials are a host preprocessing step, not a live
  pass-through to host agents or host keychains.
- Resolver coverage is intentionally narrow today. In practice, direct staged
  injection is the main path, with only limited host-side resolver support.

This is consistent with the repository's stated security model: credentials may
be made usable to the runtime, but host credential systems are not the runtime
boundary and must not be mounted through casually.

### 6. Container Entry and Provider Launch

Inside the container, the launch chain is deliberately layered:

1. `runtime/container/entrypoint.sh`
2. `runtime/container/runtime-user.sh`
3. `runtime/container/home-control-plane.sh`
4. `runtime/container/provider-wrapper.sh`
5. provider binary

This separation matters.

`entrypoint.sh` is PID 1. It validates the managed environment, handles user
mapping for mutable sessions, seeds provider home, validates launch targets, and
routes non-provider commands through the development wrapper when that lower
assurance path is allowed.

`provider-wrapper.sh` then:

- sanitizes environment variables
- reloads runtime state
- reseeds the home if needed
- forces managed autonomy flags
- rejects dangerous provider-native overrides before execution

`provider-policy.sh` adds another gate by rejecting trust-widening provider
flags such as:

- alternative config/profile overrides
- local autonomy overrides
- unsafe plugin or MCP config overrides
- additional directory or remote overrides

Only the top-level `breakglass` path can enable true breakglass behavior.
Nested invocations cannot silently upgrade themselves.

### 7. Session Home as a Managed Control Plane

`runtime/container/home-control-plane.sh` is the system's in-container control
plane builder.

It:

- verifies control-plane artifact hashes
- rebuilds the effective provider home under `/state/agent-home`
- layers baseline docs, imported workspace docs, and injected docs
- renders provider-specific config files
- seeds auth material, MCP config, rules, and SSH material
- optionally captures file-trace metadata

The layered documentation model is especially important:

1. adapter baseline doc
2. imported workspace `AGENTS.md`
3. imported provider doc such as `CLAUDE.md` or `GEMINI.md`
4. injected common doc
5. injected provider-specific doc

This allows Workcell to preserve provider-native instruction formats without
allowing the workspace to become the sole trust root.

### 8. Runtime Mutability and Assurance Downgrades

Workcell distinguishes between the safe immutable path and explicit lower
assurance behavior.

Examples:

- Mutable sessions map the container user to the host UID/GID.
- Package mutation is mediated by `apt-wrapper.sh` and `apt-helper.sh`.
- APT mutations are allowed only in mutable sessions and downgrade assurance to
  `lower-assurance-package-mutation`.
- The downgrade is persisted and auditable.

This is a pragmatic design choice. Workcell does not pretend that package
installation inside a mutable developer session has the same assurance profile
as a stricter replayable path.

### 9. Exec Guard and Wrapper Defense in Depth

The deepest technical control in the repository is the Rust exec guard in
`runtime/container/rust/src/lib.rs`, built as `libworkcell_exec_guard.so` and
loaded with `LD_PRELOAD`.

It intercepts multiple execution paths including:

- `execve`
- `execv`
- `execvp`
- `execvpe`
- `execveat`
- `fexecve`
- `posix_spawn`
- `posix_spawnp`
- raw Linux syscall paths for `execve` and `execveat`

Its policy role is to stop bypasses beneath the shell-wrapper layer, including:

- direct execution of protected runtimes outside approved wrappers
- shebang scripts from mutable paths that target protected runtimes
- native ELF execution from mutable paths in strict mode
- git hook and config bypass patterns

Workcell reinforces this with command wrappers:

- `runtime/container/bin/git`
  - blocks dangerous git env/config and hook bypasses.
- `runtime/container/bin/node`
  - strips risky Node execution flags and blocks direct protected-runtime
    execution paths.
- `runtime/container/public-node-guard.mjs`
  - prevents execution of copied protected provider packages through Node.

This layered model is stronger than depending on shell aliases, repo policy
files, or CLI settings alone.

### 10. Provider Adapters

The adapters are intentionally thin and provider-specific.

#### Codex

The Codex adapter seeds managed config and rules, disables provider-native web
search by default, pins provider-native sandboxing off in favor of the outer
Workcell boundary, and injects managed requirements and MCP config.

#### Claude Code

The Claude adapter seeds reviewed settings, baseline instructions, auth mirrors,
MCP config, and a `PreToolUse` hook that blocks trust-widening shell patterns.

#### Gemini CLI

The Gemini adapter seeds reviewed settings, disables IDE-style trust defaults in
the managed path, enforces tool sandbox expectations, validates auth-related
files, and manages trust state for `/workspace`.

The important architectural point is that Workcell keeps one shared boundary and
many thin adapters, which is consistent with the repository's own guidance.

### 11. Verification and Release

The repo includes a serious verification and provenance story.

Key pieces:

- `verify/invariants/`
  - invariant-oriented checks and expected controls.
- `docs/invariants.md`
  - design-level invariant contract.
- `docs/threat-model.md`
  - threat assumptions and abuse paths.
- `docs/provenance.md`
  - release and attestation model.
- `docs/validation-scenarios.md`
  - scenario coverage and known limits.

Release publication is intentionally host-side. `workcell publish-pr` stages,
signs, pushes, and opens the PR from outside the Tier 1 session. That preserves
the design claim that GitHub publication is a host action, not an in-container
trust escalation.

## End-to-End Execution Flow

### Safe Launch Flow

The normal strict-path launch is:

1. Operator runs `workcell --agent <provider> --workspace <repo>`.
2. Host launcher scrubs env and validates workspace eligibility.
3. Host launcher validates or creates the Colima profile.
4. Host launcher validates runtime assumptions and mount shape.
5. Injection policy is resolved and rendered into a staged bundle.
6. Workspace control-plane files are shadowed and imported.
7. VM-level egress allowlist is applied.
8. Runtime image is prepared if required.
9. Container starts with hardened flags and managed env.
10. Container PID 1 seeds runtime state and effective provider home.
11. Provider wrapper enforces managed autonomy and provider policy.
12. Agent starts in the bounded runtime.

### Development Flow

`development` mode is still managed, but it allows non-provider commands through
`development-wrapper.sh`. This is a deliberate convenience path, not the same
assurance level as strict provider-only execution.

### Breakglass Flow

`breakglass` exists, but the system tries hard to make it explicit:

- it must be requested
- its semantics are separate
- nested invocations cannot silently self-upgrade into it

This is the correct pattern for enterprise trust boundaries. Higher-trust paths
exist, but they are not smuggled in as normal behavior.

### Publish Flow

The PR publication path intentionally exits the bounded runtime:

1. Work is completed inside Workcell.
2. Operator runs `workcell publish-pr` on the host.
3. The host creates or switches branch, stages changes, commits with signing,
   pushes, and opens a draft PR.

This keeps the signing identity and final repository publication outside the
agent session boundary.

## What Workcell Already Does Well

Workcell is stronger than many current agent runtimes in several areas:

- It has a real, explicit runtime boundary instead of relying on prompt files or
  repo-local config alone.
- It treats workspace trust and host trust as different problems.
- It masks repo-local control-plane paths to reduce prompt/config injection from
  mutable workspace state.
- It uses syscall-level exec interception rather than trusting shell wrappers
  alone.
- It has explicit lower-assurance states instead of silently widening trust.
- It keeps provider differences explicit with thin adapters.
- It has a credible audit and provenance story.
- It now has a first durable host-side session inventory slice for completed or
  aborted launches.

These are foundational strengths. They should be preserved.

## Current Functional Gaps

Workcell's current gaps are mostly product and workflow gaps, not core boundary
gaps.

### Gaps in the Current Product Surface

- No first-class multi-agent orchestration.
- No background session supervisor or queue.
- No built-in pause, resume, takeover, or follow-up model for live sessions.
- No first-class worktree-per-task or branch-per-session workflow.
- No checkpoint, fork, or snapshot model for active sessions.
- GUI mode is intentionally not implemented yet as a preserved-boundary path.
- No preserved-boundary GUI or IDE session surface.
- No org-level policy administration plane or session analytics plane.
- Limited host credential resolver coverage.
- No reusable team workflow layer at the Workcell level comparable to agent
  packs, commands, or enterprise agent profiles.

## Comparison With Modern Coding-Agent Systems

### Trail of Bits `claude-code-config`

Reference: [GitHub repository](https://github.com/trailofbits/claude-code-config)

What it emphasizes:

- reusable slash commands
- plugin-distributed skills, agents, and commands
- hook-driven workflow controls
- a statusline with live session context and cost data
- optional local-model support through LM Studio

What Workcell has instead:

- stronger runtime isolation
- stronger separation between workspace trust and control-plane trust
- stronger enforcement beneath the CLI layer

What Workcell is missing relative to that workflow model:

- a first-class reusable workflow/command library at the Workcell layer
- an agent-pack or subagent-pack concept that is independent of one provider
- lightweight UX features such as live status and session ergonomics

### Penligent's Sandbox Architecture

Reference: [Sandboxes for Coding Agents](https://www.penligent.ai/hackinglabs/he/sandboxes-for-coding-agents/)

The article argues that production systems need separate control, execution,
policy, artifact, verification, and lifecycle planes, and that state management
is as important as isolation.

Workcell already covers:

- control plane
- execution plane
- policy plane
- verification plane
- part of the artifact plane

Workcell is weaker on:

- lifecycle plane as a first-class product surface
- pause/resume/snapshot/fork semantics
- user-facing artifact browsing and replay workflows
- warm-pool style responsiveness and session management

### Dagger `container-use`

Reference: [GitHub repository](https://github.com/dagger/container-use)

What it emphasizes:

- multiple agents in parallel
- a fresh isolated environment per agent
- per-agent git branch isolation
- real-time command visibility
- direct intervention into a live agent terminal
- MCP-compatible integration across multiple agent products

Workcell is stronger on boundary rigor, but weaker on orchestration:

- Workcell does not yet have per-task isolated session management as a product.
- Workcell does not yet expose a direct session-takeover workflow.
- Workcell does not yet make live command history a first-class user interface.

### Cursor Cloud/Background Agents

References:

- [Background agents docs](https://docs.cursor.com/en/background-agents)
- [Cloud agents announcement](https://cursor.com/blog/cloud-agents)
- [Cloud agents with computer use](https://cursor.com/blog/agent-computer-use)
- [Self-hosted cloud agents](https://cursor.com/blog/self-hosted-cloud-agents/)
- [Cloud agents changelog](https://cursor.com/changelog/02-24-26/)

What Cursor emphasizes:

- many asynchronous agents in parallel
- isolated remote machines
- follow-up prompts while the agent is still running
- takeover into the remote environment
- multi-surface access from desktop, web, mobile, Slack, and GitHub
- self-hosted worker fleets for enterprise

Workcell is missing most of this operating model:

- no background session supervisor
- no background queue
- no pause/resume/follow-up UX
- no remote worker or fleet model
- no handoff between CLI and GUI surfaces

### GitHub Copilot Cloud Agent and Custom Agents

References:

- [Managing cloud agents](https://docs.github.com/en/enterprise-cloud@latest/copilot/how-tos/use-copilot-agents/manage-agents)
- [Creating custom agents](https://docs.github.com/en/enterprise-cloud@latest/copilot/how-tos/use-copilot-agents/cloud-agent/create-custom-agents)
- [About agent management](https://docs.github.com/en/copilot/concepts/agents/cloud-agent/agent-management)
- [Managing Copilot cloud agent in your enterprise](https://docs.github.com/en/copilot/how-tos/administer-copilot/manage-for-enterprise/manage-agents/manage-copilot-cloud-agent)

What GitHub emphasizes:

- cloud sessions that create PRs directly
- steering input during a running session
- resume in VS Code or CLI
- repository, organization, and enterprise-level custom agents
- centralized enterprise AI controls
- enterprise session inventory and audit views

Workcell is missing:

- org and enterprise-level agent/profile distribution
- session list and centralized activity views
- resume/follow-up workflows
- a durable admin plane for policy and usage management

### Aider and Similar Terminal-First Agents

Reference: [Aider watch mode](https://aider.chat/docs/usage/watch.html)

Terminal-first agents like Aider emphasize small-loop developer productivity:

- quick iteration from the terminal
- watch mode and automation hooks
- low-friction editing workflows

Workcell is currently more rigorous than it is fast-moving. It could benefit
from more loop-shortening features without giving up the boundary model.

## Finalized Next Feature: Workcell Supervisor

The next feature that should be present in Workcell is a first-class **Workcell
Supervisor**.

This should be a host-side session orchestration layer that manages multiple
bounded Workcell sessions as durable objects rather than treating every launch as
an isolated foreground shell command.

Workcell now has the phase 1 foundation for that direction: durable session
records and host-side session inventory/export commands. The remaining gap is
true orchestration rather than inventory alone.

### Why This Is the Right Next Feature

It is the highest-leverage addition because it:

- closes the largest gap with Cursor, Copilot, and Container Use
- compounds Workcell's existing boundary strengths instead of weakening them
- improves developer throughput more than another round of low-level hardening
- creates the foundation for enterprise control-plane features later

### What the Supervisor Should Do

The minimum viable Supervisor should provide:

1. Session objects
   - each launch becomes a named session with durable metadata
2. Per-session worktree or branch isolation
   - default to a new git worktree per task on the safe path
3. Parallel execution
   - run multiple sessions concurrently without workspace conflicts
4. Follow-up prompts
   - send steering input to a running or paused session
5. Pause and resume
   - preserve long-running work without keeping a foreground terminal attached
6. Takeover
   - attach directly to a live session shell or provider TTY
7. Session inventory
   - list status, branch/worktree, mode, assurance state, runtime age, and logs
8. Artifact capture
   - diff, transcript, command log, assurance downgrade events, and outputs
9. Policy inheritance
   - all existing Workcell controls still apply to every session

### Recommended CLI Shape

Possible commands:

- `workcell session start`
- `workcell session list`
- `workcell session follow`
- `workcell session send`
- `workcell session attach`
- `workcell session pause`
- `workcell session resume`
- `workcell session stop`
- `workcell session diff`
- `workcell session export`

### Recommended Internal Model

The Supervisor should introduce a stable session record containing:

- session id
- workspace root
- worktree path
- branch name
- provider and mode
- assurance state
- active runtime identifiers
- timestamps
- artifact paths
- operator identity

The host should own this metadata. The runtime should remain disposable.

### Phase 2 Extensions

Once the Supervisor exists, Workcell can add higher-level features safely:

- lightweight TUI or web session dashboard
- GitHub and Slack entrypoints
- resumable checkpoints and forks
- warm pools or prepared base runtimes
- reusable team profiles, commands, and agent packs
- central policy distribution and enterprise analytics

## Features Workcell Should Add After the Supervisor

In priority order:

1. Team workflow packs
   - versioned commands, subagents, instruction bundles, and approved MCP packs
     at the Workcell layer
2. Session observability
   - live status, command timeline, cost/context counters, and artifact browser
3. Checkpoint and fork support
   - especially for long-running validation or UI tasks
4. Enterprise policy plane
   - centralized policy distribution, session inventory, and usage analytics
5. Preserved-boundary GUI surfaces
   - IDE and web entrypoints backed by the same host-controlled supervisor
6. Broader auth resolver support
   - enterprise secret managers and brokered credential flows

## Enterprise Productivity Recommendations

If the goal is to improve enterprise developer productivity without trading away
the core Workcell boundary, the program should be:

### 1. Turn Safe Sessions Into a Queueable Platform

Today, Workcell is primarily a secure launcher. Enterprises need it to become a
secure session platform. The Supervisor is the bridge from "secure shell entry"
to "secure developer throughput."

### 2. Default to Isolated Worktrees Per Task

This removes a common source of friction and makes multi-session execution
practical. It also improves auditability because each task has a clearer branch
and diff boundary.

### 3. Make Session State Visible

Enterprise teams need to know:

- what is running
- who started it
- what it changed
- what network and credential posture it had
- whether assurance was downgraded

Workcell already captures much of the raw data needed for this. It needs a user
surface, not a new security primitive.

### 4. Add Reusable Team-Level Workflow Assets

Popular agent systems have learned that productivity comes from repeatable
workflows, not only from model quality. Workcell should support:

- review agents
- refactor agents
- release agents
- documentation agents
- approved MCP bundles
- approved command packs

These should be distributed as reviewed, auditable Workcell assets rather than
as ad hoc repo-local prompt files.

### 5. Add Resume and Steering as First-Class Actions

Enterprise developers often want to:

- start work asynchronously
- redirect the agent mid-task
- inspect the environment
- take over locally

Modern systems treat this as normal. Workcell currently treats sessions as
foreground launches. That is too limiting for large teams.

### 6. Keep the Boundary Model Intact

The main trap to avoid is copying the UX of popular agent systems by weakening
the security model. Workcell should not give up:

- control-plane masking
- explicit assurance states
- host-side publication
- deny-by-default trust assumptions
- thin provider adapters with managed policy

The right move is to add orchestration and visibility on top of the current
boundary, not to replace the boundary with convenience features.

## Bottom Line

Workcell already has the shape of a strong enterprise-safe coding-agent runtime.
Its main weakness is not architecture drift at the isolation layer. Its weakness
is that the product surface stops too early.

The system needs to evolve from:

- "securely launch one bounded agent session"

to:

- "securely operate, steer, compare, and audit many bounded agent sessions"

That is the missing capability that best aligns with both modern coding-agent
workflows and Workcell's existing strengths.
