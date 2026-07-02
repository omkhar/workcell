# GitHub Copilot CLI Adapter

This adapter owns the Workcell-managed GitHub Copilot CLI baseline.

The safe path uses a pinned Copilot CLI binary, a session-local `COPILOT_HOME`,
a session-local `COPILOT_CACHE_HOME`, and one explicit credential key:
`copilot_github_token`. For auth-required launches, Workcell converts that
staged token into a host-mounted token handoff outside mounted provider state;
the runtime entrypoint moves it into a transient handoff file, unlinks the
mounted file, and re-execs without the token in its environment, then the
provider wrapper unlinks that file and exports the value as
`COPILOT_GITHUB_TOKEN` only for the managed
Copilot child process. The original staged token file is removed from direct
runtime mounts; the temporary handoff mount is outside provider state and is not
copied into `COPILOT_HOME`.

The adapter intentionally does not use host GitHub CLI state, host Copilot
provider state (`~/.copilot`, `~/.config/github-copilot`,
`~/.cache/github-copilot`), host keychains, `GH_TOKEN`, or `GITHUB_TOKEN` as
readiness or auth inputs. Copilot custom instructions are disabled; plugin,
MCP, custom-agent, hook, skill, dynamic-retrieval, and remote-session expansion
overrides stay
blocked on the default path until each one has a separate Workcell review unit
and validation evidence.
