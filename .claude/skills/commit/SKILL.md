---
name: commit
description: Apply Risk-Aware Commit Notation in Workcell. Use when an agent groups changes, selects a risk prefix, writes a message, or creates a signed commit.
---

# Risk-Aware Commit Notation

Use this skill for each Workcell commit.

## Priorities

Use the repository priorities in this order:

1. Developer experience.
2. Simplicity.
3. Security invariants.
4. Performance.
5. Idiomatic correctness.

Do not reduce a runtime boundary or an explicit security guarantee.

## Subject Format

Use this format:

```text
<risk><intent> <description> (risk reason; case reason)
```

### Risk

| Symbol | Level | Claim |
|---|---|---|
| `.` | Safe | Structural proof and focused validation cover each applicable invariant |
| `^` | Validated | Validation covers the intended change and known invariants |
| `!` | Risky | Validation covers only the intended change |
| `@` | Broken | Evidence does not prove the intended change |

Select a risk level only when the available evidence supports its claim.

### Intent

| Letter | Intent | Meaning |
|---|---|---|
| `F` or `f` | Feature | Change one behavior. |
| `B` or `b` | Bug fix | Correct a defect and preserve other behavior. |
| `R` or `r` | Refactor | Change structure without a behavior change. |
| `D` or `d` | Documentation | Change information without an execution change. |

Use uppercase for a primary or user-visible change. Use lowercase for a
secondary change.

A feature or bug fix that changes more than eight code lines has the highest
risk by default. Tests are part of this count. Use `.r` only when structural
proof and focused tests prove the refactor.

Use `!` when evidence proves only the intended change. Use `@` when evidence
does not prove the intended change.

## Commit Scope

- Use one intent in each commit.
- Keep the commit and pull request reviewable.
- Split unrelated behavior, cleanup, and follow-up work.
- Remove dead code that the change exposes, or give a reason to keep it.
- Remove machine-specific data and repository debris.
- Keep contract, help, document, policy, and test changes with each
  user-visible workflow that they describe.
- Run Workcell-owned garbage collection when validation creates Workcell-owned
  residue.

## Required Gates

- Sign each commit with the verified maintainer identity.
- Use a feature branch. Do not push directly to `main`.
- Do not rewrite history without explicit user authority.
- If the commit changes a supported workflow or backend, complete live
  certification. Apply the same gate to a support-tier claim or certification
  path. Complete certification before you sign the commit.
- If a hook does not run, run its equivalent checks. Record why the hook did
  not run.
- Use the `workcell-pr-lifecycle` skill for publication, review, or merge work.
- Publish a `main`-based pull request with
  `./scripts/repo-publish-pr.sh` on the host.
- Keep a non-`main` pull request draft. Do not merge it.

## Review Loop

Apply the peer-review rules in `AGENTS.md`.

After each finding:

1. Fix or explicitly disposition it.
2. Run validation that proves the correction.
3. Review the new state as required by `AGENTS.md`.
4. Continue until no actionable finding remains or a concrete blocker exists.

For a pull request, complete the exact-head Codex bot loop after every push.
Check all comments, inline reviews, unresolved threads, and configured
asynchronous reviewers.

Do not accept a failed repository check. For a merge task, follow workflows for
the merged `main` commit until they finish successfully.

When repeated friction exposes a durable instruction gap, update the skill,
runbook, or `AGENTS.md` that owns the process.

## Examples

- `^F Add --branch-exclude flag (tests pass; user-visible CLI flag)`
- `^f Add branch filter helper (tests pass; secondary function)`
- `^B Fix cache key collision (regression test passes; user-visible defect)`
- `^D Update target support record (docs checks pass; user-visible support status)`
- `.r Rename parser helper (rename proof and focused tests pass; internal refactor)`
