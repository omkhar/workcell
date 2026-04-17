# Requirements Validation

Workcell now keeps a canonical requirement-to-evidence mapping in
[`policy/requirements.toml`](../policy/requirements.toml).

## Purpose

The requirements matrix does two things:

- names the current functional and nonfunctional requirements that the repo is
  claiming as implemented
- points each requirement at concrete automated evidence and supporting
  documentation

This is meant to reduce drift between:

- what the docs say Workcell does
- what the tests and validation scripts actually prove
- what maintainers treat as the supported contract

## Validation Rule

`./scripts/verify-requirements-coverage.sh` validates that:

- the matrix is syntactically valid
- functional and nonfunctional requirement sections are both present
- requirement ids and titles are unique
- every requirement cites at least one automated evidence file
- every evidence and documentation path is repo-relative and exists in the repo
- every release-facing Markdown example and the current release-evaluation docs
  (`docs/provider-matrix.md`, `docs/validation-scenarios.md`, and
  `docs/enterprise-rollout.md` when present) appear in at least one requirement
  `docs` array

This check runs through the normal validation entrypoints, including
`./scripts/dev-quick-check.sh` and `./scripts/validate-repo.sh`.

## Scope

The matrix is intentionally about the supported repo contract, not every
possible implementation detail.

It should cover:

- the main managed launch path
- host-side operator commands
- supported install and uninstall surfaces
- auth and injection behavior
- assurance and audit behavior
- release and reproducibility guarantees
- newly added product capabilities such as session inventory

It should not be used to smuggle in speculative roadmap items that are not yet
implemented.

## Maintenance Rules

When a supported requirement changes:

1. update `policy/requirements.toml`
2. update or add the automated evidence
3. update the docs that explain the requirement
4. rerun the requirement validator

If a requirement cannot point to real automated evidence, it is not ready to be
claimed as part of the canonical supported contract.

When adding a new Markdown file under `docs/examples/`, update
`policy/requirements.toml` in the same change so the release-facing example
docs remain machine-checked.
