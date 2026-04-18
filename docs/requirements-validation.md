# Requirements Validation

Workcell now keeps:

- a normative operator workflow contract in
  [`policy/operator-contract.toml`](../policy/operator-contract.toml)
- a requirement-to-doc-and-evidence mapping in
  [`policy/requirements.toml`](../policy/requirements.toml)

## Purpose

The operator contract defines:

- which public workflows are supported
- their canonical syntax and compatibility aliases
- where those workflows must be discoverable
- which docs and automated evidence currently back each public workflow

The requirements matrix does three things:

- names the current functional and nonfunctional requirements that the repo is
  claiming as implemented
- links functional requirements to stable workflow ids from the operator
  contract
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

`./scripts/verify-operator-contract.sh` validates that:

- every public workflow in `policy/operator-contract.toml` is mapped to at
  least one requirement
- every requirement workflow reference resolves to a declared workflow id
- every workflow-cited doc and evidence path exists and is also cited by the
  referenced requirements
- required help, README, and manpage surfaces contain the contract-declared
  workflow syntax
- compatibility aliases still pass their contract-declared alias probes
- contract-declared remediation copy still exists in the launcher source

Both checks run through the normal validation entrypoints, including
`./scripts/dev-quick-check.sh` and `./scripts/validate-repo.sh`.

## Scope

The requirements matrix is intentionally not the command-inventory source of
truth. It is about traceability for the supported repo contract, not every
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

1. update `policy/operator-contract.toml` when the public workflow surface,
   canonical syntax, discoverability, compatibility status, docs, or evidence
   changes
2. update `policy/requirements.toml`
3. update or add the automated evidence
4. update the docs that explain the requirement
5. rerun both validators

If a requirement cannot point to real automated evidence, it is not ready to be
claimed as part of the canonical supported contract.

When adding a new Markdown file under `docs/examples/`, update
`policy/requirements.toml` in the same change so the release-facing example
docs remain machine-checked.
