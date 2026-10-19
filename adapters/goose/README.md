# goose Adapter (uncertified)

This adapter runs the pinned goose CLI (the `aaif-goose/goose` GitHub release
archive) inside the bounded runtime. No review certified its CLI, so its
manifest tier is `uncertified`. A launch needs `--allow-uncertified-agent`,
and the session is lower assurance. The VM and container runtime boundary is
the primary control.

## Auth methods

Workcell stages no goose credential. goose talks to the model provider that
you select. The manifest egress allowlist names only `api.anthropic.com:443`,
so the Anthropic provider is the one provider that the session can reach. Run
`goose configure` in the session, select Anthropic, and enter an Anthropic API
key. The wrapper turns off the keyring, so goose saves the key in
`~/.config/goose/secrets.yaml` in the session home.

## Managed control-plane files

The adapter ships no goose configuration file. This README is the adapter
baseline in the control-plane manifest.

## Adapter behavior

- The build downloads the static musl archive of the target architecture from
  the GitHub release and checks its sha256 against the Dockerfile pin.
  `scripts/verify-upstream-goose-release.sh` checks the pin against the
  release digests.
- `reject_manifest_args` in `runtime/container/provider-policy.sh` passes an
  option only when the manifest `[flags]` `allow` list has it.
  `[flags]` `other_commands` names every top-level command and alias except
  `configure`, `help`, `info`, `run`, and `session` (alias `s`), and the
  hidden `mcp-probe` and `validate-extensions` commands. The policy refuses a
  word that names one of them (`internal/adapters/uncertified_test.go`).
  Other words pass, for example the prompt after `run -t`.
- `other_commands` names the hidden commands that a probe of each candidate
  word in the pinned binary found (`goose <word> --help`). A hidden command
  that the probe did not find passes the policy.
- The wrapper removes each `GOOSE_*` variable from the environment. It sets
  `GOOSE_TELEMETRY_OFF=1`, so goose sends no usage data, and
  `GOOSE_DISABLE_KEYRING=1`.
- goose runs each tool without a prompt in its default `auto` mode. `yolo`
  autonomy sets `GOOSE_MODE=auto`. `prompt` autonomy sets
  `GOOSE_MODE=approve`, so goose asks before each tool call.
- The manifest refuses the extension options (`--with-extension`,
  `--with-builtin`, `--with-streamable-http-extension`), `--container`, and
  the recipe options (`adapters/goose/adapter.toml`,
  `internal/adapters/uncertified_test.go`).
- goose reads repository input: `.goosehints` and `AGENTS.md` files, the
  `.goose/` directory and project plugins in `.agents/plugins/`. On the safe
  path, the workspace control-plane mask covers `.goose/`, `.agents/plugins/`,
  `.goosehints` at any depth and the top-level `AGENTS.md`
  (`docs/invariants.md`).

## See also

- [`../README.md`](../README.md)
- [`../../docs/provider-matrix.md`](../../docs/provider-matrix.md)
