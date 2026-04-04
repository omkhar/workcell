---
name: commit
description: Risk-Aware Commit Notation. Use when creating git commits to determine the correct prefix, risk level, and commit grouping.
---

# Risk-Aware Commit Notation

All commits in this repo must use Risk-Aware Commit Notation (based on [Arlo's Commit Notation](https://github.com/RefactoringCombos/ArlosCommitNotation)), a risk-based notation where the first characters of every commit message encode risk level and intention.

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

- **One intention per commit.** Do not mix refactoring, features, bugfixes, or documentation in a single commit. If a task requires both refactoring and a feature change, split them into separate commits (refactoring first, then the feature).
- **Minimize risk per commit.** Break work into the smallest commits that each achieve the lowest possible risk level. For example, extract a safe refactoring (`.r`) as its own commit before making a risky feature change (`!F`), rather than bundling both into one `!F` commit.
- **Prefer many small safe commits over fewer risky ones.** A sequence like `.r` then `.r` then `^F` is better than a single `!F` that includes the refactoring.

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

## Examples

- `.r Style: reformat line wrapping in scm_policy_checker.py`
- `!F Add new endpoint for policy evaluation`
- `^B Fix cache invalidation on prompt change (regression test added)`
- `.d Update README with setup instructions`
