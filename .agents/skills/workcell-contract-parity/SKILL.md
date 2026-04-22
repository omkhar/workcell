---
name: workcell-contract-parity
description: Keep the Workcell operator contract, docs, help text, and automated evidence in sync when user-visible workflows or compatibility aliases change. Use for CLI/help/man/README/requirements/scenario updates in the Workcell repository.
---

# Workcell Contract Parity

Use this skill only in the Workcell repository root, identified by:

- `AGENTS.md`
- `policy/operator-contract.toml`
- `policy/requirements.toml`

Use it when a change touches:

- public CLI workflows, flags, aliases, help, or remediation text
- `policy/operator-contract.toml` or `policy/requirements.toml`
- repo-local operator docs such as `README.md`, `man/workcell.1`, `docs/*.md`
- tests or validators that prove a user-visible workflow

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

Docs review is repo-local. Keep Workcell docs, help text, manpages, policies,
and validators aligned inside this repository. Do not treat external docs as
the source of truth for repo behavior.

Treat every user request as implicitly including peer review unless the user
explicitly narrows that scope. For parity work, peer review means continuing
through findings, fixes, validation, and another review pass until no
actionable findings remain or a concrete blocker is reported.
Treat that as an open-ended peer loop. If a peer, validator, or review surface
finds new parity issues after a fix, continue re-reviewing with that peer or
surface until every finding is resolved, explicitly dispositioned, or blocked
by a concrete external constraint.
Treat repeated docs confusion, support-boundary ambiguity, or validator churn
as a signal to improve the repo-local contract surfaces themselves. When a
durable parity lesson appears, version it in the relevant skill, contract, or
docs rather than keeping it only in working memory.

## Read first

- `AGENTS.md`
- `policy/operator-contract.toml`
- `policy/requirements.toml`
- `docs/requirements-validation.md`

If the change is release-bound, also read:

- `docs/releasing.md`

## Invariants

- `policy/operator-contract.toml` is the workflow source of truth.
- `policy/requirements.toml` must reference the same workflow ids and cite the
  same docs and evidence.
- Every supported public workflow must have canonical syntax, support tier,
  discoverability, docs, and automated evidence.
- If a supported workflow depends on certification-only evidence, that live
  certification must pass before you sign the commit that claims or changes
  support for the workflow.
- Compatibility aliases must have explicit `alias_probes`.
- Design docs are explanatory, not the authoritative command inventory.
- The runtime boundary is the primary control. Do not treat hooks, prompt text,
  or docs as the security boundary.
- Do not widen host mounts, host sockets, credential access, or `breakglass`
  behavior implicitly. Any higher-trust path must stay explicit and documented.
- Schema validation must fail closed on wrong types.
- Standard parity validation must check the repo script, not an ambient
  `WORKCELL_HELP_BIN` override.
- Final GitHub publication is host-side and branch-based. Do not normalize
  direct pushes to `main`.
- In this repository, the parity-enforcing `main`-based publication path is
  `./scripts/repo-publish-pr.sh`, which delegates to
  `./scripts/workcell publish-pr` only after fresh local `pr-parity`
  evidence exists.
- Anything pushed for review must stay human-reviewable. Keep PRs single-purpose
  and bounded enough that a reviewer can reason about the change without
  juggling multiple independent decisions. Split broad work into sequenced PRs
  before host-side publication.
- For publish, PR follow-up, or merge work in this repository, use the
  repo-local `workcell-pr-lifecycle` skill so host-side publication, check
  follow-through, and review sweeps stay versioned in-repo.
- If `scripts/workcell` changes, regenerate and revalidate
  `runtime/container/control-plane-manifest.json`.
- Dead code is a simplicity bug. Remove dead code when found, and keep the
  repo-level dead-code check green.
- Public-repo hygiene is part of correctness. Remove machine-specific details
  from public repo surfaces and clean repo detritus before finishing a change.
- Re-check PR comments and review threads after CI turns green and immediately
  before merge.
- Treat newly surfaced review findings, docs drift, or parity regressions as
  part of the same task. Fix them, rerun validation, and re-review until the
  change is clean.
- Do not leave repo-owned tests, checks, or workflows red. If a validation lane
  fails during the task, fix it or explicitly remove/demote the claimed
  guarantee in the same change.
- If the task includes merging, continue through the merged `main` workflow set
  and treat any repo-owned failure as part of the same change until resolved.

## Workflow

1. Enumerate the affected user-visible workflows.
2. Update `policy/operator-contract.toml` first.
3. Update `policy/requirements.toml` to match the contract.
4. Update repo-local operator surfaces:
   - `scripts/workcell`
   - `README.md`
   - `man/workcell.1`
   - only the explanatory docs that should stay aligned
5. Add the smallest tests that prove the changed behavior.
6. Check PR shape before publication. If the branch mixes unrelated behavior,
   large opportunistic refactors, or more than one reviewer-sized concern,
   split it before pushing for review.
7. Run the contract validators and the focused tests.
8. If publication or merge is part of the task, follow the repo-owned hosted
   workflows through completion and fix any failures they surface before
   finishing.
9. Re-review the affected contract surfaces after fixes land and before calling
   the task done. Do not stop while actionable findings remain.
10. If the task exposed a reusable parity or operator-guidance gap, update the
    relevant repo-local instruction surface in the same change stream or in a
    separate follow-on review unit.

## Validation

Always run:

```sh
bash ./scripts/verify-operator-contract.sh
bash ./scripts/verify-requirements-coverage.sh
bash ./scripts/check-dead-code.sh
bash ./scripts/check-public-repo-hygiene.sh
```

Run the focused evidence you changed. Common paths:

- `go test ./internal/metadatautil ./internal/testkit`
- `bash ./tests/scenarios/shared/test-session-commands.sh`
- `bash ./tests/scenarios/shared/test-assurance-dry-run.sh`
- `bash ./tests/scenarios/shared/test-auth-commands.sh`
- `bash ./tests/scenarios/shared/test-auth-status.sh`
- `bash ./tests/scenarios/shared/test-policy-commands.sh`
- `bash ./tests/scenarios/shared/test-publish-pr-dry-run.sh`

If `scripts/workcell` changed, also run:

```sh
./scripts/generate-control-plane-manifest.sh ./runtime/container/control-plane-manifest.json
```

If contract or release-facing docs changed broadly, finish with:

```sh
bash ./scripts/validate-repo.sh
```

## Blocking rule

If a public workflow cannot point to current repo-local docs and automated
evidence in the same change, do not leave it implicitly supported. Add the
proof or demote it explicitly.
