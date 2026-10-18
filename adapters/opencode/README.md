# OpenCode Adapter (uncertified)

This adapter runs the pinned OpenCode CLI (`opencode-ai` on npm) inside the
bounded runtime. No review certified its CLI, so its manifest tier is
`uncertified`. A launch needs `--allow-uncertified-agent`, and the session is
lower assurance. The VM and container runtime boundary is the primary control.

## Auth methods

Workcell stages no OpenCode credential. The manifest egress allowlist names
only `opencode.ai:443`, the OpenCode Zen service. OpenCode keeps a provider key
that you enter in the session in the session home.

## Managed control-plane files

The adapter ships no OpenCode configuration file. `provider-wrapper.sh` passes
`OPENCODE_CONFIG_CONTENT` with `autoupdate` set to `false`, `share` set to
`disabled`, and `snapshot` set to `false`. OpenCode applies it after the
configuration file in the session home. An OpenCode snapshot starts `git`
with its own `--git-dir`, `--work-tree`, and `-c` options. In a live session
that start ended with `EPERM`, so snapshots are
off, and OpenCode cannot undo file changes from a snapshot. This README is
the adapter baseline in the control-plane manifest.

## Adapter behavior

- `reject_manifest_args` in `runtime/container/provider-policy.sh` passes an
  option only when the manifest `[flags]` `allow` list has it.
  `[flags]` `other_commands` names every top-level command except `run`, and
  the policy refuses a word that names one of them
  (`internal/adapters/uncertified_test.go`). Other words pass, for example a
  prompt or a project path for the default terminal UI.
- The wrapper removes each `OPENCODE_*` variable from the environment. It sets
  `OPENCODE_DISABLE_PROJECT_CONFIG=1`, so OpenCode does not read
  `opencode.json` or `.opencode/` from the workspace. It also turns off the
  automatic update, the model list download, and the LSP server download.
- `yolo` autonomy adds `--auto`, which approves each permission that the
  configuration does not deny. `prompt` autonomy adds no option. The policy
  refuses `--auto` from the user (`internal/adapters/uncertified_test.go`).
- The manifest refuses the network listener options, `--attach`, and the hidden
  `--yolo`, `--dangerously-skip-permissions`, and `--share` options
  (`adapters/opencode/adapter.toml`, `internal/adapters/uncertified_test.go`).

## See also

- [`../README.md`](../README.md)
- [`../../docs/provider-matrix.md`](../../docs/provider-matrix.md)
