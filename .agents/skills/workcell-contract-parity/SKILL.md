---
name: workcell-contract-parity
description: Keep Workcell contracts, help, documents, and evidence consistent with shipped behavior. Use for public workflows, aliases, help, requirements, or scenarios.
---

# Workcell Contract Parity

Use this skill only in the Workcell repository. Confirm that these files exist:

- `AGENTS.md`
- `policy/operator-contract.toml`
- `policy/requirements.toml`

## Read First

Read:

- `AGENTS.md`
- `policy/operator-contract.toml`
- `policy/requirements.toml`
- `docs/requirements-validation.md`

For release work, also read `docs/releasing.md`.

## Sources of Truth

- `policy/operator-contract.toml` is the normative public workflow contract.
- `policy/requirements.toml` maps workflows to documents and evidence.
- Design documents explain the system. They are not the command inventory.
- Repository source implements shipped behavior.
- Tests provide evidence for that behavior.
- External documents do not control repository behavior.

## Parity Rules

- Give each supported workflow canonical syntax, a support tier, discovery
  locations, documents, and automated evidence.
- Require each supported workflow to map to a valid requirement.
- Require each referenced workflow ID to resolve to the operator contract.
- Give each compatibility alias an explicit `alias_probes` entry.
- Make schema validation stop on an incorrect type.
- Check the repository launcher in parity validation. Do not use an ambient
  `WORKCELL_HELP_BIN` value.
- Treat the runtime boundary as the primary control.
- Do not use hooks, prompts, or documents as a security boundary.
- Never add a host socket, a host credential-store mount, an authentication-state
  mount, or a path that `AGENTS.md` forbids.
- Keep each `breakglass` or equivalent higher-trust path explicit and narrow.
- Document each higher-trust path separately.
- Require explicit operator acknowledgement for each higher-trust path.
- If a commit changes a supported end-to-end workflow or backend, complete live
  certification. Apply the same gate to a support-tier claim or
  certification-only validation path. Complete certification before you sign
  the commit.
- If `scripts/workcell` changes, regenerate the control-plane manifest.
- Validate the regenerated control-plane manifest.
- Keep garbage collection and cache-retention behavior in the operator
  contract.
- Keep uninstall and validator behavior in the requirements, help, documents,
  and evidence that own these operations.
- Remove dead code and public repository debris.
- Keep each pull request small and single-purpose.

## Workflow

1. List each affected public workflow.
2. Update `policy/operator-contract.toml`.
3. Update `policy/requirements.toml`.
4. Update the launcher, README, manpage, and each document that the workflow
   change affects.
5. Add the smallest automated evidence that proves the changed behavior.
6. Run the contract validators and focused tests.
7. Re-review all changed contract surfaces.
8. Continue the peer loop until no actionable finding remains.
9. For publication or merge, use the `workcell-pr-lifecycle` skill.
10. Record a parity lesson in a repo-local instruction when the lesson can
    prevent the same problem.

Do not add a planned item as a supported workflow. If current evidence cannot
support a workflow, add the evidence or demote the claim in the same change.

## Documentation Preflight

Before publication:

1. Scan inbound links when a heading changes or disappears.
2. Compare support terms with `policy/host-support-matrix.tsv`.
3. Inventory all endpoint sources when a network document changes.
4. Include credential, policy, profile, bootstrap, and recovery endpoint sources.
5. Give each historical measurement a date and a stable evidence link.
6. Compare each changed claim with current source and tests.

## Validation

Always run:

```sh
bash ./scripts/verify-operator-contract.sh
bash ./scripts/verify-requirements-coverage.sh
bash ./scripts/check-dead-code.sh
bash ./scripts/check-public-repo-hygiene.sh
```

Run focused evidence for the changed workflow. Common commands are:

```sh
go test ./internal/metadatautil ./internal/testkit
bash ./tests/scenarios/shared/test-session-commands.sh
bash ./tests/scenarios/shared/test-assurance-dry-run.sh
bash ./tests/scenarios/shared/test-auth-commands.sh
bash ./tests/scenarios/shared/test-auth-status.sh
bash ./tests/scenarios/shared/test-policy-commands.sh
bash ./tests/scenarios/shared/test-publish-pr-dry-run.sh
```

If `scripts/workcell` changes, also run:

```sh
./scripts/generate-control-plane-manifest.sh   ./runtime/container/control-plane-manifest.json
./scripts/verify-control-plane-manifest.sh
```

For broad contract or release-document changes, finish with:

```sh
/usr/bin/env -u GIT_PAGER ./scripts/validate-repo.sh
```

Do not finish while a changed workflow lacks current documents or automated
evidence.
