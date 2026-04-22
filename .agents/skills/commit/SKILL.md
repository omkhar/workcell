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
- Treat every user request as implicitly including peer review unless the user
  explicitly narrows that scope.
- When peer review finds an actionable issue, keep iterating through fixes,
  validation, and another review pass until no actionable findings remain or a
  concrete blocker is reported.
- Treat that review loop as unbounded. If the peer or follow-up review pass
  finds new issues after a fix, keep iterating with that peer until all
  findings are resolved, explicitly dispositioned, or blocked by a concrete
  external constraint.

## Commit Grouping

- One intention per commit.
- Minimize risk per commit.
- Prefer small safe commits over fewer risky ones.
- Keep the eventual PR human-reviewable. Do not bundle unrelated fixes,
  opportunistic cleanup, and behavior changes into one remote review unit.
  Split broad work before pushing.
- Sign every commit.
- Before signing a commit that introduces or materially changes a supported
  end-to-end workflow, backend, support-tier claim, or certification-only
  validation path, run the relevant live certification successfully. Do not
  sign a support-claim commit while planning to gather certification later.
- Use feature branches. Do not push directly to `main` or rewrite history.
- Treat final GitHub publication as a host-side action.
- Default remote review units to `main`-based pull requests. Keep non-`main`
  base PRs draft-only and non-mergeable, and do not treat them as carrying the
  same repo-owned PR validation guarantees as `main`-based review units.
- For publish, PR follow-up, or merge work in this repository, use the
  repo-local `workcell-pr-lifecycle` skill. Treat generic GitHub publication
  skills as fallback only when the repo-local lifecycle instructions do not
  cover the need.
- Do not accept failing repo-owned tests, checks, or workflows as "good
  enough." If a lane fails because of the change or a hosted-control drift
  uncovered during the task, keep working until it is fixed or the guarantee is
  explicitly changed in the same review unit.
- If the task includes merging, do not stop at PR-green. Follow the merged
  `main` workflows and fix any repo-owned failures they surface before calling
  the work complete.
- If the task includes publication, review comments, or follow-up CI, do not
  stop at the first green run. Re-check review surfaces and continue until no
  actionable findings remain.
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
