# Quickstart: GitHub Copilot CLI

This quickstart covers the Workcell Tier 1 Copilot CLI path. It uses one
explicit credential key, `copilot_github_token`. Workcell stages that token
through reviewed host-side inputs and removes the token file plus staged
direct-mount copy from direct runtime mounts for Copilot sessions. For
auth-required provider launches, Workcell converts the token to a temporary
host-mounted token handoff outside mounted provider state, moves it through a
transient runtime handoff file with the Workcell entrypoint as PID 1, unlinks
the mounted handoff file, and exports it as
`COPILOT_GITHUB_TOKEN` only for the managed Copilot child process.

Do not rely on host `gh` auth, host keychains, `GH_TOKEN`, `GITHUB_TOKEN`,
host Copilot provider state (`~/.copilot`, `~/.config/github-copilot`,
`~/.cache/github-copilot`), or whole-home passthrough for Copilot auth.

## 1. Prepare a token file

Create a host-owned file that contains only the reviewed Copilot token value:

```bash
install -m 0700 -d /Users/example/.config/workcell
install -m 0600 /dev/null /Users/example/.config/workcell/copilot-github-token.txt
```

Write the token value into that file using your normal secret-handling process.

## 2. Stage the credential

```bash
workcell auth set \
  --agent copilot \
  --credential copilot_github_token \
  --source /Users/example/.config/workcell/copilot-github-token.txt
```

Check the host-side policy view:

```bash
workcell auth status --agent copilot
workcell why --agent copilot --mode strict --credential copilot_github_token
```

The selected bootstrap path should report `direct-staged`. Shared GitHub CLI
state is intentionally not a Copilot auth input.

## 3. Inspect the launch view

```bash
workcell --agent copilot --auth-status --workspace /path/to/repo
workcell --agent copilot --inspect --workspace /path/to/repo
```

The managed runtime sets `COPILOT_HOME` and `COPILOT_CACHE_HOME` to
Workcell-owned session-local paths. It does not mount host Copilot provider
state (`~/.copilot`, `~/.config/github-copilot`,
`~/.cache/github-copilot`), host keychains, or host GitHub CLI auth, and it
does not copy the token into `COPILOT_HOME`. Workcell also disables Copilot
custom instructions on the managed path.

## 4. Launch

```bash
workcell --agent copilot --workspace /path/to/repo
```

For a lower-assurance development command lane:

```bash
workcell --agent copilot --mode development --workspace /path/to/repo -- bash -lc 'git status'
```

Maintainers must run live provider-authenticated certification of a
non-destructive `copilot -p` launch with staged credentials before signing
changes that promote or materially alter the Copilot support claim.
