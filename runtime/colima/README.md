# Colima Design

Workcell uses a dedicated Colima profile for each workspace by default. The VM
is the primary boundary for the strict macOS target.

## Boundary rules

- Do not use the shared Colima `default` profile for the strict target.
- Mount only the selected workspace as writable durable host data.
- Use only narrow, per-session credential handoff directories.
- Do not mount a host home, socket, keychain, or broad source tree.
- Apply the Workcell network policy at the VM layer.

## Profile and mounts

The launcher derives the profile name from the workspace path. The operator can
select another profile.

The managed Lima configuration has one writable workspace mount: the selected
workspace. A reviewed credential path can add a narrow handoff mount.

The Copilot token path uses two mount levels. Workcell first mounts a guarded
parent staging directory into the VM. It then mounts only the session token
directory into the container.

The handoff stays outside provider state. It must not become a host home,
socket, keychain, or general credential store.

## Network limit

All containers in one Colima profile share the Workcell egress rules. The last
launch replaces those rules. Do not run concurrent sessions with different
complete endpoint sets in one profile.
