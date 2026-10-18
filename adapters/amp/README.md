# Amp Adapter (uncertified)

This adapter runs the pinned Amp CLI (`@ampcode/cli` on npm) inside the
bounded runtime. No review certified its CLI, so its manifest tier is
`uncertified`. A launch needs `--allow-uncertified-agent`, and the session is
lower assurance. The VM and container runtime boundary is the primary control.

## Auth methods

Workcell stages no Amp credential. Amp needs an Amp account. When the session
home has no Amp key, Amp starts its device login: it prints an
`ampcode.com` URL and a code. Open the URL on the host, and confirm the code.
Amp then saves the account in the session home. The manifest egress
allowlist names only `ampcode.com:443`. The Amp service serves the login, the
threads, and the model calls.

## Managed control-plane files

The adapter ships no Amp settings file. This README is the adapter baseline in
the control-plane manifest.

## Adapter behavior

- `reject_manifest_args` in `runtime/container/provider-policy.sh` passes an
  option only when the manifest `[flags]` `allow` list has it.
  `[flags]` `other_commands` names every top-level command and alias except
  `login`, `logout`, and `version`. The policy refuses a word that names one
  of them (`internal/adapters/uncertified_test.go`). Other words pass, for
  example the prompt after `-x`.
- `other_commands` also names the hidden commands that the help does not
  list. A probe ran `amp <word> --help` for each word in the pinned binary to
  find them. A hidden command that the probe did not find passes the policy.
- The wrapper removes each `AMP_*` variable from the environment. It sets
  `AMP_SKIP_UPDATE_CHECK=1`, so Amp does not check for an update. It sets
  `AMP_REMOTE_CONTROL_TERMINAL=0`, so `ampcode.com` gets no terminal access to
  the session.
- `yolo` autonomy adds `--dangerously-allow-all`, which runs each tool without
  a prompt. `prompt` autonomy adds no option. The policy refuses
  `--dangerously-allow-all` from the user (`internal/adapters/uncertified_test.go`).
- The manifest refuses the remote executor, runner, orb, sharing, and
  terminal access options, `--settings-file`, `--mcp-config`, and
  `--log-file` (`adapters/amp/adapter.toml`).
- Amp reads repository input: the workspace `.amp/settings.json`, `AGENTS.md`,
  the `.agents/skills/` directory, and the `.amp/plugins/` directory. Workcell
  does not turn off these inputs.

## See also

- [`../README.md`](../README.md)
- [`../../docs/provider-matrix.md`](../../docs/provider-matrix.md)
