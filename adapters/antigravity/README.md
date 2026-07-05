# Google Antigravity CLI Adapter

This adapter directory is a fail-closed planning scaffold. Workcell recognizes
`antigravity` as a planned Google Antigravity CLI provider id
(`internal/providerid/providerid.go`), but it does not install, prepare,
authenticate, or launch Antigravity yet. It is not in the supported-provider
registry (`internal/adapters/data.go`) and `providerid.IsValid("antigravity")`
returns false.

## Auth methods

None today. A future path must first pin official install and auth provenance,
then stage only reviewed Google auth material into session-local provider state.
Host Google account caches, browser profiles, keychains, host homes, and
provider caches are not acceptable implicit safe-path inputs
(`docs/injection-policy.md`, `docs/invariants.md` §1). No Antigravity credential
keys exist in current releases; they must not appear in operator policy until
the matching adapter, validation, and docs land.

## Managed control-plane files

None today. Before support is claimed the adapter must own session-local
provider home/cache/settings state under `/state/agent-home` and explicitly map
or block Antigravity's subagent, plugin, MCP, sandbox, permission, hook, and
instruction surfaces (`docs/adapter-control-planes.md`).

## Adapter behavior

`workcell --agent antigravity` exits before runtime preparation with a
"planned Workcell provider adapter, but it is not supported yet" diagnostic and
a non-zero status (`scripts/workcell`). Support promotion requires the same
review unit to add:

- an official pinned Antigravity CLI install path with provenance evidence
- explicit Antigravity home and cache-state handling under `/state/agent-home`
- a staged Google auth handoff that does not mount host Google account state,
  browser profiles, keychains, host homes, or provider caches
- provider-native unsafe-flag rejection in the Workcell wrapper
  (`runtime/container/provider-policy.sh`)
- deterministic dry-run and scenario coverage
- a live provider certification run before any supported Tier 1 matrix claim

## See also

- [../README.md](../README.md) — adapter index and common contract
- [../../docs/adapter-control-planes.md](../../docs/adapter-control-planes.md)
- [../../docs/invariants.md](../../docs/invariants.md)
- [../../docs/extending-adapters.md](../../docs/extending-adapters.md) — worked
  contributor examples (including adding a new adapter)
