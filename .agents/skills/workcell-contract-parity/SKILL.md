---
name: workcell-contract-parity
description: Keep the Workcell operator contract, docs, help text, and automated evidence in sync when user-visible workflows or compatibility aliases change. Use for CLI/help/man/README/requirements/scenario updates in the Workcell repository.
---

# Workcell Contract Parity

Use this skill only for the Workcell repository at
`/Users/omkharanarasaratnam/src/workcell`.

Trigger this skill when a change touches any of:

- public CLI workflows or flags
- compatibility aliases
- `scripts/workcell` help or remediation text
- `policy/operator-contract.toml`
- `policy/requirements.toml`
- `README.md`, `man/workcell.1`, or release-facing workflow docs
- scenario tests or validators that prove a user-visible workflow

## Required context

Read:

- `/Users/omkharanarasaratnam/src/workcell/AGENTS.md`
- `/Users/omkharanarasaratnam/src/workcell/policy/operator-contract.toml`
- `/Users/omkharanarasaratnam/src/workcell/policy/requirements.toml`
- `/Users/omkharanarasaratnam/src/workcell/docs/requirements-validation.md`

When the change is release-bound, also read:

- `/Users/omkharanarasaratnam/src/workcell/docs/releasing.md`

## Contract rules

- `policy/operator-contract.toml` is the workflow source of truth.
- Every public workflow must have:
  - a stable workflow id
  - a support tier
  - canonical syntax
  - real discoverability surfaces
  - repo-relative docs paths
  - repo-relative automated evidence paths
- Compatibility aliases must have explicit `alias_probes`, or they do not stay
  in the contract.
- Do not claim a discoverability surface that only falls back to generic help.
- `policy/requirements.toml` must reference the workflow ids for the supported
  requirement and must also cite the same docs/evidence paths.
- Design docs are explanatory. Do not treat them as the authoritative command
  inventory.

## Workflow

1. Enumerate the user-visible workflows affected by the change.
2. Decide whether each workflow is `supported`, `compatibility-only`, or
   `internal`.
3. Update `policy/operator-contract.toml` first:
   - canonical syntax
   - alias lifecycle and alias probes
   - discoverability
   - docs
   - evidence
   - remediation text when the workflow fails closed
4. Update `policy/requirements.toml` so the relevant requirement references the
   workflow ids and cites the same docs/evidence paths.
5. Update the real operator surfaces:
   - `scripts/workcell`
   - `README.md`
   - `man/workcell.1`
   - only the explanatory docs that should stay in sync
6. Update or add the smallest test set that proves the changed workflow.
7. Run the contract validators and the directly affected scenario/unit tests.

## Validation

Always run:

```sh
go run ./cmd/workcell-metadatautil validate-operator-contract . ./policy/operator-contract.toml ./policy/requirements.toml
bash ./scripts/verify-operator-contract.sh
```

Then run the focused evidence you changed, such as:

- `bash ./tests/scenarios/shared/test-session-commands.sh`
- `bash ./tests/scenarios/shared/test-assurance-dry-run.sh`
- `bash ./tests/scenarios/shared/test-auth-commands.sh`
- `bash ./tests/scenarios/shared/test-auth-status.sh`
- `bash ./tests/scenarios/shared/test-policy-commands.sh`
- `bash ./tests/scenarios/shared/test-publish-pr-dry-run.sh`
- `go test ./internal/metadatautil ./internal/testkit`

If the contract changes broadly or release-facing docs move, finish with:

```sh
bash ./scripts/validate-repo.sh
```

## Blocking rule

If a public workflow cannot point to current docs and automated evidence in the
same change, do not leave it as implicitly supported. Either add the missing
proof or demote the workflow or alias explicitly.
