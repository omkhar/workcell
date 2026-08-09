# Requirements and Validation

Workcell keeps two machine-readable contract files:

- [`policy/operator-contract.toml`](../policy/operator-contract.toml) defines
  supported public workflows.
- [`policy/requirements.toml`](../policy/requirements.toml) maps requirements to
  documents and automated evidence.

## Operator Contract

The operator contract defines:

- A stable identifier for each public workflow.
- Canonical syntax and compatibility aliases.
- Help, README, manpage, and document locations.
- Automated evidence for the workflow.
- Required remediation text and alias probes when applicable.

It is the command inventory source. The requirements matrix is not a command
inventory.

## Requirements Matrix

The requirements matrix:

- Lists current functional and nonfunctional requirements.
- Links functional requirements to operator workflow identifiers.
- Links each requirement to current evidence and documents.

Do not add planned behavior as an implemented requirement.

## Validators

`./scripts/verify-requirements-coverage.sh` checks these conditions:

- TOML syntax is valid.
- Functional and nonfunctional sections exist.
- Requirement identifiers and titles are unique.
- Each requirement has at least one automated evidence file.
- Each evidence and document path is repository-relative and exists.
- Release examples occur in a requirement document list. The list also
  includes `docs/provider-matrix.md`, `docs/validation-scenarios.md`, and
  `docs/enterprise-rollout.md` when those files exist.

`./scripts/verify-operator-contract.sh` checks these conditions:

- Each public workflow maps to at least one requirement.
- Each requirement workflow reference exists.
- Workflow evidence and documents also occur in the linked requirements.
- Each declared discovery surface contains the canonical syntax.
- Compatibility aliases pass their declared probes.
- Required remediation text remains in the launcher.

Both validators run in the quick and full repository validation paths.

## Maintenance Rule

When a supported requirement changes:

1. Update the operator contract if the public workflow, syntax, alias,
   discovery surface, document, or evidence changes.
2. Update the requirements matrix.
3. Add or update automated evidence.
4. Update the explanatory documents.
5. Run both validators.

If a requirement has no automated evidence, do not claim it as part of the
canonical supported contract.

When you add a Markdown file under `docs/examples/`, add it to an applicable
requirement in the same change.
