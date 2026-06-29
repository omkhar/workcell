# GitHub Copilot CLI Adapter

This adapter directory is a fail-closed planning scaffold. Workcell recognizes
`copilot` as the planned GitHub Copilot CLI provider id. The runtime image pins
and installs the Copilot CLI for provenance and release verification, but
Workcell does not prepare, authenticate, or launch Copilot yet.

Support promotion requires a later review unit to add:

- explicit `COPILOT_HOME` and cache-state handling under `/state/agent-home`
- a staged `COPILOT_GITHUB_TOKEN` or equivalent reviewed auth handoff that does
  not mount host GitHub CLI state, host homes, or provider caches
- provider-native unsafe-flag rejection in the Workcell wrapper
- deterministic dry-run and scenario coverage
- a live provider certification run before any supported Tier 1 matrix claim

Until those gates land, `workcell --agent copilot` exits before runtime
preparation with an unsupported-provider diagnostic.
