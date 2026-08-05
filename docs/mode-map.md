# Mode Map

Workcell uses these terms:

- `Tier 1`: A provider CLI runs fully inside the bounded Workcell runtime.
- `strict`: This term identifies the default managed Tier 1 mode.

The `--mode` option selects one of four modes.

| Mode | Use | Main properties |
|---|---|---|
| `strict` | Default provider work | Workcell uses the VM and container boundary, reviewed network controls, repository control-plane masks, the provider entrypoint, and default `--agent-autonomy yolo`. |
| `development` | Managed interactive development | Workcell uses the same boundary and masks as `strict`. This mode permits managed non-provider commands and more dependency endpoints. It has lower assurance than `strict`. |
| `build` | Image preparation and dependency updates | This mode permits the broader network access that the build path needs. |
| `breakglass` | Higher-trust debugging | This mode requires `--ack-breakglass=YYYY-MM-DD` with the current UTC date. It has lower assurance than the managed modes. |

## Container mutability

The `--container-mutability` option is independent of `--mode`.

- `ephemeral` is the default. It permits package-manager changes in the
  disposable container and labels the session `managed-mutable`.
- `readonly` blocks package-manager writes. Use `--mode strict
  --container-mutability readonly` for the strongest managed posture.

## Other defaults

- You must select an agent with `--agent`.
- `--agent-autonomy yolo` is the default. `--agent-autonomy prompt` is an
  explicit lower-assurance choice.
- `--cache-profile off` is the default.
- `--cache-profile standard` keeps workspace-scoped package and compiler
  caches. It does not keep secret provider state. It is an explicit
  lower-assurance choice.
- A strict launch prepares the reviewed runtime image when the image is missing
  or stale.
- An interactive launch shows a spinner and elapsed time. Use `--no-spinner`
  for plain status updates.
- Use `--prepare` or `--prepare-only` when you must prepare the image as a
  separate step.

See [invariants.md](invariants.md) for the controls that apply to each mode.
See [safe-path-expectations.md](safe-path-expectations.md) for operator
examples.
