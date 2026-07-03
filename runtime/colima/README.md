# Colima Design

Workcell uses a dedicated Colima VM profile because that is the strongest
practical local boundary available in the current macOS stack.

## Boundary rules

- use a dedicated profile per workspace by default
- do not rely on Colima's shared `default` profile for Tier 1
- mount only the selected workspace as writable host state, except for
  explicit narrow per-session handoff directories such as the Copilot token
  handoff mount
- do not mount host homes, sockets, or broad source trees
- enforce network posture at the VM layer

## Operational stance

The host launcher derives a profile name from the workspace path unless the
operator overrides it. The managed path validates the resulting Lima config and
expects exactly one writable host mount for durable host state: the selected
workspace. Auth handoffs that must cross the VM boundary, such as the Copilot
token handoff, use a two-level shape: Workcell mounts a guarded parent staging
root into Colima, then mounts only the per-session token handoff subdirectory
into the container. These handoff directories stay outside provider state and
must not become host homes, sockets, keychains, or broad credential stores.

## What stays out

- host home directories
- host auth and agent sockets
- keychain and browser-profile passthrough
- host Docker control sockets
- unrelated source trees
