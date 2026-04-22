# Docker Desktop Compatibility Backend

Workcell now ships an explicit lower-assurance local compatibility target:

- `target_kind=local_compat`
- `target_provider=docker-desktop`
- `target_id=desktop-linux`
- `target_assurance_class=compat`

This target is intentionally not described as equivalent to the strict Colima
path. It reuses the same host-owned control plane, runtime image, injection
policy, audit/session state, and fail-closed diagnostics, but it does not
claim the dedicated VM boundary that the reviewed `local_vm/colima/strict`
path provides.

## Current reviewed host matrix

The canonical support matrix lives in
[`policy/host-support-matrix.tsv`](../policy/host-support-matrix.tsv).

Current reviewed rows for `docker-desktop`:

- `macos/arm64 local_compat/docker-desktop/compat`: `supported`, launch
  `allowed`, evidence `certification-only`
- Linux and Windows rows remain blocked until later review and certification

When the selected host or target combination is not in the reviewed matrix,
Workcell fails closed and prints the support-boundary reason instead of
silently falling back to another backend.

## Enable

Use the explicit target flag for either launch or image preparation:

```bash
workcell --target docker-desktop --agent codex --workspace /path/to/repo
workcell --target docker-desktop --prepare-only --agent codex --workspace /path/to/repo
```

Workcell requires a healthy Docker Desktop context named `desktop-linux` for
real launches or prepares on this target.

## Local certification smoke

The reviewed local end-to-end certification lane for this backend is:

```bash
./scripts/run-scenario-tests.sh --secretless-only --certification-only
```

On a healthy macOS Docker Desktop host, that tier includes
`shared/docker-desktop-launch-smoke`, which performs:

- `workcell --target docker-desktop --prepare-only --agent codex`
- real managed launch/version probes for `codex`, `claude`, and `gemini`

This certification-only smoke is intentionally separate from
repo-required validation so default repo validation stays deterministic on
hosts that do not have a live Docker Desktop runtime available.

## Inspect and diagnose

Use `--doctor` or `--inspect` to confirm the selected backend and state roots:

```bash
workcell --target docker-desktop --agent codex --doctor --workspace /path/to/repo
workcell --target docker-desktop --agent codex --inspect --workspace /path/to/repo
```

These commands emit stable key=value fields including:

- `target_kind=local_compat`
- `target_provider=docker-desktop`
- `target_id=desktop-linux`
- `target_assurance_class=compat`
- `target_state_dir=~/.local/state/workcell/targets/local_compat/docker-desktop/<profile>`

If Docker Desktop is installed but the `desktop-linux` context is missing or
unhealthy, `--doctor` reports `doctor_missing_host_tools=docker-desktop-context`
and recommends `install-host-tools`. Real launch and prepare paths fail closed
with an explicit Docker Desktop diagnostic instead of falling back to Colima.

## Disable and rollback

Rollback is explicit:

- omit `--target docker-desktop` to return to the default strict Colima path
- or pass `--target colima` explicitly

If you need to clear retained compat state before retrying:

```bash
rm -rf ~/.local/state/workcell/targets/local_compat/docker-desktop/<profile>
```

This clears Workcell-owned target state only. It does not mutate Docker
Desktop itself.

## Operator contract

The supported operator-facing contract for this backend is tracked in:

- [`policy/operator-contract.toml`](../policy/operator-contract.toml)
- [`policy/requirements.toml`](../policy/requirements.toml)
- [docs/validation-scenarios.md](validation-scenarios.md)

Those files, the support matrix, and the launcher diagnostics should move in
lockstep whenever this compatibility target changes.
