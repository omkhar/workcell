# Docker Desktop Compatibility Backend

Workcell ships an explicit lower-assurance local compatibility target:
`local_compat/docker-desktop/compat` with `target_id=desktop-linux`.
It reuses the same host-owned control plane, runtime image, injection policy,
and session or audit state as the default launcher path, but it does not claim
the dedicated VM boundary of `local_vm/colima/strict`.

## Current reviewed host matrix

The canonical support matrix lives in
[`policy/host-support-matrix.tsv`](../policy/host-support-matrix.tsv).

Current reviewed row:
- `macos/arm64 local_compat/docker-desktop/compat`: `supported`, launch
  `allowed`, evidence `certification-only`

Linux and Windows remain blocked until later review and certification.
Unsupported or unhealthy selections fail closed; Workcell never falls back
silently to another backend.

## Launch, prepare, and inspect

Use the explicit target flag:

```bash
workcell --target docker-desktop --agent codex --workspace /path/to/repo
workcell --target docker-desktop --prepare-only --agent codex --workspace /path/to/repo
workcell --target docker-desktop --agent codex --doctor --workspace /path/to/repo
workcell --target docker-desktop --agent codex --inspect --workspace /path/to/repo
```

Workcell requires a healthy Docker Desktop context named `desktop-linux` for
real launches or prepares on this target.

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

## Certification and rollback

The reviewed local end-to-end certification lane for this backend is:

```bash
./scripts/run-scenario-tests.sh --secretless-only --certification-only
```

On a healthy macOS Docker Desktop host, that tier includes
`shared/docker-desktop-launch-smoke` with prepare-only plus real version probes
for `codex`, `claude`, and `gemini`. Repo-required validation stays
deterministic and does not require a live Docker Desktop runtime.

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
