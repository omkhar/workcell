# Google Antigravity CLI Adapter

This adapter directory is a fail-closed planning scaffold. Workcell recognizes
`antigravity` as a planned Google Antigravity CLI provider id, but it does not
install, prepare, authenticate, or launch Antigravity yet.

Support promotion requires the same review unit to add:

- an official pinned Antigravity CLI install path with provenance evidence
- explicit Antigravity home and cache-state handling under `/state/agent-home`
- a staged Google auth handoff that does not mount host Google account state,
  browser profiles, keychains, host homes, or provider caches
- provider-native unsafe-flag rejection in the Workcell wrapper
- deterministic dry-run and scenario coverage
- a live provider certification run before any supported Tier 1 matrix claim

Until those gates land, `workcell --agent antigravity` exits before runtime
preparation with an unsupported-provider diagnostic.
