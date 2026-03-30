# Colima Design

Workcell uses a dedicated Colima VM profile because that is the strongest
practical local boundary available in the current macOS stack.

## Boundary rules

- use a dedicated profile per workspace by default
- do not rely on Colima's shared `default` profile for Tier 1
- mount only the selected workspace as writable host state
- do not mount host homes, sockets, or broad source trees
- enforce network posture at the VM layer

## Operational stance

The host launcher derives a profile name from the workspace path unless the
operator overrides it. The managed path validates the resulting Lima config and
expects exactly one writable host mount: the selected workspace.

## What stays out

- host home directories
- host auth and agent sockets
- keychain and browser-profile passthrough
- host Docker control sockets
- unrelated source trees
