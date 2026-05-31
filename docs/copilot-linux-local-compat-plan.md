# Copilot And Linux Local Compat Plan

Status: planning only. This page does not promote GitHub Copilot CLI,
Linux hosts, or Linux `local_compat` to supported operator-launch status.

As of 2026-05-31, GitHub Copilot CLI is generally available, but Workcell has
not yet implemented the adapter evidence required to support `--agent copilot`.
The Linux `amd64` `local_compat` path also remains blocked until a selected
distro/runtime matrix has live host evidence and fail-closed diagnostics.

## Current External Facts

- GitHub announced Copilot CLI general availability on 2026-02-25 and describes
  it as a terminal coding agent that can plan, edit files, run commands, and
  iterate with user-controlled approval modes:
  <https://github.blog/changelog/2026-02-25-github-copilot-cli-is-now-generally-available/>
- GitHub documents Copilot CLI installation through npm, Homebrew, WinGet, or
  the install script. The npm path requires Node.js 22 or later:
  <https://docs.github.com/en/copilot/how-tos/copilot-cli/set-up-copilot-cli/install-copilot-cli>
- GitHub documents `COPILOT_GITHUB_TOKEN` as an environment-token auth path and
  says supported token types include fine-grained PATs with the Copilot Requests
  permission, Copilot CLI OAuth tokens, and GitHub CLI OAuth tokens. Classic
  PATs are not supported:
  <https://docs.github.com/en/copilot/reference/copilot-cli-reference/cli-command-reference>
- GitHub documents repository-wide Copilot instructions in
  `.github/copilot-instructions.md`, path-specific
  `.github/instructions/**/*.instructions.md`, and `AGENTS.md`:
  <https://docs.github.com/en/copilot/how-tos/copilot-cli/use-copilot-cli/overview>
- Debian lists `amd64` and `arm64` as official released ports, and Debian 13
  release notes list `amd64`, `arm64`, `armel`, `armhf`, `ppc64el`, `riscv64`,
  and `s390x` as officially supported architectures:
  <https://www.debian.org/ports/> and
  <https://ddp-team.pages.debian.net/release-notes/en/html/whats-new.html>
- Ubuntu Server documentation lists `amd64`, `arm64`, `ppc64el`, and `s390x`
  as supported 64-bit architectures:
  <https://ubuntu.com/server/docs/tutorial/basic-installation/>

Popularity signals are use-case dependent. Server-oriented sources favor
Debian-family systems, while desktop and gaming signals vary and may favor
Arch-derived or Fedora-derived systems in some cohorts. Workcell should treat
those signals as prioritization inputs only, not proof of supportability.

## Proposed PR Sequence

1. Copilot discovery and fail-closed scaffold.
   Record current Copilot CLI product behavior, unsupported diagnostics, unsafe
   flag inventory, and control-plane risks without claiming support.

2. Copilot auth and bootstrap.
   Add the `copilot_github_token` credential key, auth-status explainability,
   direct staged-token materialization, and tests proving that host
   `~/.copilot`, `GH_TOKEN`, `GITHUB_TOKEN`, host keychains, and ambient
   GitHub CLI auth do not become safe-path inputs.

3. Copilot managed home and control plane.
   Add session-local `COPILOT_HOME` and `COPILOT_CACHE_HOME`, managed
   `~/.copilot` state, reviewed instruction import rules, repo-local Copilot
   control-plane masking, MCP/LSP/plugin/hook defaults, telemetry/content
   capture posture, and deterministic tests.

4. Copilot launch and unsafe argument policy.
   Map Workcell autonomy modes to Copilot CLI options, block permissive
   Copilot flags that would bypass Workcell policy, add scenario coverage, and
   keep the live provider check certification-only.

5. Copilot live certification and support claim.
   Before signing the support-claim commit, run a non-destructive live
   `copilot -p` certification with a staged `COPILOT_GITHUB_TOKEN` inside the
   managed runtime. Only then update the supported provider matrix, quickstart,
   operator contract, requirements, and release-facing evidence.

6. Linux `amd64` `local_compat` research.
   Select the first candidate matrix narrowly. The default candidate should be
   Debian-family `amd64` first, with Ubuntu LTS and Debian stable as the
   evidence anchors. Fedora and Arch remain follow-up candidates unless the
   selected runtime evidence proves their host behavior separately.

7. Linux `amd64` `local_compat` candidate.
   Add exact host-support matrix rows, unsupported-combination diagnostics,
   install/rollback guidance, deterministic tests, and live host certification
   evidence. Stop at certification candidate unless the evidence supports a
   stronger support tier.

8. Vulnerability-validation loop.
   After the support work above has landed or is concretely blocked, run the
   vulnerability-validation workflow on latest `main` in isolated local
   validation. Each true issue becomes its own empirical proof, fix, regression
   test, review, PR, merge, and merged-main follow-up cycle.

9. Optimization loop.
   After correctness and security gates are stable, optimize Workcell and
   Workcell workflows with baseline measurements, targeted changes,
   post-change measurements, and no weakened invariants. Keep performance PRs
   separate from support-claim or security-fix PRs unless a measured bottleneck
   blocks the supported workflow.

## Support Matrix Direction

Do not add a generic Linux launch row. Use exact rows by host OS, host
architecture, target kind, provider, assurance class, and reason.

Initial candidate rows should preserve this shape:

| Host family | Host arch | Target kind | Provider | Assurance | Status before certification |
|---|---|---|---|---|---|
| Debian stable | `amd64` | `local_compat` | Docker Desktop or selected compatible runtime | `compat` | blocked or certification candidate |
| Ubuntu LTS | `amd64` | `local_compat` | Docker Desktop or selected compatible runtime | `compat` | blocked or certification candidate |
| Fedora Workstation/Server | `amd64` | `local_compat` | selected compatible runtime | `compat` | unsupported until separately reviewed |
| Arch Linux | `amd64` | `local_compat` | selected compatible runtime | `compat` | unsupported until separately reviewed |

The machine-readable matrix currently lacks distro/version columns. If Linux
candidate rows need distro scoping, add that schema deliberately with parser
tests and docs rather than encoding distro names only in free-form reasons.

## Pre-Signing Gates

For any commit that promotes Copilot, Linux `local_compat`, a support tier, a
backend, or a certification-only path:

- run the relevant live certification before signing
- record host OS, distro, distro version, architecture, runtime version,
  kernel, cgroup mode, Docker security features, Workcell commit SHA, dirty
  state, command, timestamp, and cleanup status
- rerun contract parity validators and the focused scenario tests
- keep unsupported combinations fail-closed with explicit remediation text

## Review Discipline

Every PR stays single-purpose and draft-first. Before merge, sweep top-level PR
comments, inline comments, unresolved review threads, configured async
reviewers, and repo-owned CI. After merge, follow the merged `main` workflows
and run repo readiness checks when the task scope includes merge follow-up.
