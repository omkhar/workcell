# Google Antigravity CLI Adapter

This adapter directory is a fail-closed scaffold. Workcell recognizes
`antigravity` as a provider name in `internal/providerid/providerid.go`.
Workcell does not install, prepare, authenticate, or start Antigravity. The
supported-provider registry does not include it. The function
`providerid.IsValid("antigravity")` returns false.

## Auth methods

None. A supported path must pin official install and auth provenance. It must
stage only reviewed Google auth material in session-local provider state.
Host Google account caches, browser profiles, keychains, host homes, and
provider caches are not acceptable implicit safe-path inputs
(`docs/injection-policy.md`, `docs/invariants.md` §1). No Antigravity credential
keys exist in current releases; they must not appear in operator policy until
the matching adapter, validation, and docs land.

## Managed control-plane files

None. Before support, the adapter must own session-local provider state under
`/state/agent-home`. It must map or block each Antigravity control surface. See
`docs/adapter-control-planes.md`.

## Adapter behavior

`workcell --agent antigravity` exits before runtime preparation. It gives a
"planned Workcell provider adapter, but it is not supported yet" diagnostic.
One review unit must add all these controls before support:

- Pin the official Antigravity CLI install path and provenance.
- Own Antigravity home and cache state under `/state/agent-home`.
- Stage Google auth without a host account, browser, keychain, home, or cache
  mount.
- Reject provider unsafe flags in `runtime/container/provider-policy.sh`.
- Add deterministic dry-run and scenario tests.
- Pass live provider certification before a Tier 1 claim.

## See also

- [../README.md](../README.md) — adapter index and common contract
- [../../docs/adapter-control-planes.md](../../docs/adapter-control-planes.md)
- [../../docs/invariants.md](../../docs/invariants.md)
- [../../docs/extending-adapters.md](../../docs/extending-adapters.md) — worked
  contributor examples (including adding a new adapter)
