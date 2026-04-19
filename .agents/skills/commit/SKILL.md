---
name: commit
description: Risk-Aware Commit Notation. Use when creating git commits to determine the correct prefix, risk level, and commit grouping.
---

# Risk-Aware Commit Notation

All commits in this repo use Risk-Aware Commit Notation. The first characters of
the subject encode risk level and intention.

## Standing priorities

Always prefer, in order:

1. Simplicity
2. Correctness
3. Linting and clean validation
4. Appropriate test coverage
5. Security
6. Performance
7. Current idiomatic correctness

These priorities apply only inside the repo invariants. Do not trade away the
runtime boundary, explicit security guarantees, or host-side publication rules
for convenience.

## Format

`<risk><intention> <description>`

## Risk Levels

| Symbol | Level | Guarantees |
|--------|-------|------------|
| `.` | Safe | Intended change + known & unknown invariants |
| `^` | Validated | Intended change + known invariants |
| `!` | Risky | Intended change only |
| `@` | Broken | None |

## Intentions

| Letter | Type | Meaning |
|--------|------|---------|
| **F/f** | Feature | Modify one behavioral aspect without affecting others |
| **B/b** | Bugfix | Repair undesirable behavior, preserve everything else |
| **R/r** | Refactoring | Restructure code without changing runtime behavior |
| **D/d** | Documentation | Update info that doesn't impact code execution |

## Rules

- Uppercase = primary/user-visible changes. Lowercase = supporting changes.
- Features/bugfixes exceeding 8 lines of code (including tests) default to highest risk level.
- Safe refactoring (`.r`) requires provable refactoring via automated tools or test-supported procedural refactoring.
- Choose the risk level honestly. If you haven't verified invariants, use `!` or `@`.

## Commit Grouping

- One intention per commit.
- Minimize risk per commit.
- Prefer small safe commits over fewer risky ones.
- Keep the eventual PR human-reviewable. Do not bundle unrelated fixes,
  opportunistic cleanup, and behavior changes into one remote review unit.
  Split broad work before pushing.
- Sign every commit.
- Use feature branches. Do not push directly to `main` or rewrite history.
- Treat final GitHub publication as a host-side action.
- Do not accept failing repo-owned tests, checks, or workflows as "good
  enough." If a lane fails because of the change or a hosted-control drift
  uncovered during the task, keep working until it is fixed or the guarantee is
  explicitly changed in the same review unit.
- If the task includes merging, do not stop at PR-green. Follow the merged
  `main` workflows and fix any repo-owned failures they surface before calling
  the work complete.
- When a commit changes a user-visible Workcell workflow, support tier, help
  surface, repo-local operator docs, contract entry, or validation evidence,
  land the matching contract/help/doc/test updates in the same change stream
  unless the commit message explains the staged exception and why it is safe.
- Remove dead code when it is discovered as part of the change, or explicitly
  justify why it must remain.
- Remove machine-specific details from public repo surfaces and clean repo
  detritus before finalizing the change.
- If commit hooks are bypassed, rerun the equivalent validations manually and
  record the reason in the working notes or final report.

## Notation Justification in Commit Messages

After drafting each commit, add a terse parenthetical after the description explaining both the risk level and the uppercase/lowercase choice. Keep it to one clause each, separated by a semicolon.

Format: `(risk reason; case reason)`

Examples:

- `^F Add --branch-exclude flag (tests pass; user-visible CLI flag)`
- `^f Add FilterByBranchExclusion utility (existing tests green; supporting function, not user-facing)`
- `^B Fix cache key collision (regression test added; user-visible bug)`
- `^b Update call sites for new signature (compiles and tests green; supporting change for ^F above)`
- `!F Add experimental diff parser (no tests yet; user-visible feature)`
- `.r Extract helper, no behavior change (automated rename; internal restructure)`
