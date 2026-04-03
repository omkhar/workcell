# Validation Scenarios

Workcell uses several validation layers. No single test proves the full
boundary or release story by itself.

Use this page with:

- [docs/use-case-matrix.md](use-case-matrix.md) for what is currently covered
- [docs/scenario-gaps.md](scenario-gaps.md) for what is still missing

## Local secretless checks

These run without provider credentials:

- `./scripts/dev-quick-check.sh`
- `./scripts/build-and-test.sh` (runs in the same container as CI)
- `./scripts/container-smoke.sh`
- `./scripts/verify-invariants.sh`
- `./scripts/verify-release-bundle.sh`
- `./scripts/verify-reproducible-build.sh`
- `./scripts/pre-merge.sh`

They cover repo shape, runtime contracts, smoke behavior, and reproducibility.

## Manual authenticated smoke

`./scripts/provider-e2e.sh` is the reviewed path for provider-authenticated
checks. It is deliberately separate from default CI so the default path stays
secretless.

Use it when you need to verify:

- real provider login reuse
- provider-specific auth selection
- injected MCP or project-registry behavior
- provider UX that only shows up with a live account

## Remote heavy validation

`./scripts/dev-remote-validate.sh` stages the selected snapshot to a trusted
remote `linux/amd64` host and runs validation there. This is useful when local
heavy checks are too slow, but it is still a lower-assurance trusted-builder
path because the helper container talks to the remote host's Docker daemon.

## GitHub CI vs local boundary proof

GitHub-hosted CI proves:

- repo validation and workflow hygiene
- smoke behavior in the reviewed runtime image
- reproducibility and release-preflight logic
- signing and attestation logic on tagged releases

GitHub-hosted CI does not prove the full macOS Colima boundary. That remains a
local or self-hosted exercise.

## Credential placement rule

Provider credentials belong in the injection policy or the reviewed manual
provider-e2e path. They do not belong in the workspace, repo config, or
ambient host passthrough.

## Out of scope

These are not treated as equivalent to the default path:

- host-native GUI execution
- arbitrary container commands through the managed runtime image
- `breakglass`
- whole-home or socket passthrough
