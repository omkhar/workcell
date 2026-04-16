# Validation Scenarios

Workcell uses several validation layers. No single test proves the full
boundary or release story by itself.

Use this page with:

- [docs/use-case-matrix.md](use-case-matrix.md) for what is currently covered
- [docs/requirements-validation.md](requirements-validation.md) for the
  machine-checked requirement-to-evidence mapping
- [docs/scenario-gaps.md](scenario-gaps.md) for what is still missing

## Local secretless checks

These run without provider credentials:

- `./scripts/dev-quick-check.sh`
- `./scripts/build-and-test.sh` (host-native by default; `--docker` reruns repo validation inside the pinned CI validator container from a disposable snapshot)
- `./scripts/container-smoke.sh`
- `./scripts/verify-invariants.sh`
- `./scripts/verify-release-bundle.sh`
- `./scripts/verify-reproducible-build.sh`
- `./scripts/pre-merge.sh` (builds or reuses the same pinned validator container and can run the local stack from a disposable snapshot)

They cover repo shape, runtime contracts, smoke behavior, and reproducibility.
They also now cover canonical requirement traceability, host-side policy
inspection and explainability, and host-side detached session inventory,
control, logs/timeline, and clean-base diff/export behavior.

## Manual authenticated smoke

`./scripts/provider-e2e.sh` is the reviewed path for provider-authenticated
checks. It is deliberately separate from default CI so the default path stays
secretless.

Use it when you need to verify:

- real provider login reuse
- provider-specific auth selection
- injected MCP or project-registry behavior
- provider UX that only shows up with a live account

## GitHub CI vs local boundary proof

GitHub-hosted CI proves:

- repo validation and workflow hygiene
- smoke behavior in the reviewed runtime image
- reproducibility and release-preflight logic
- bundle install/uninstall and Homebrew install/uninstall on GitHub-hosted
  Apple Silicon `macos-26` and `macos-15`
- signing and attestation logic on tagged releases

GitHub-hosted CI does not prove the full macOS Colima boundary. That remains a
local exercise.

## Credential placement rule

Provider credentials belong in the injection policy or the reviewed manual
provider-e2e path. They do not belong in the workspace, repo config, or
ambient host passthrough.

## Out of scope

These are not treated as equivalent to the default path:

- host-native GUI execution
- arbitrary container commands outside the explicit managed `development` or `--allow-arbitrary-command` paths
- `breakglass`
- whole-home or socket passthrough
