# Policy Core

`policy/` holds the shared contract layer for Workcell.

It exists to define what every adapter and workflow must preserve:

- the runtime boundary is primary
- host secrets and control sockets stay out by default
- network modes are explicit
- `breakglass` is narrow and visibly lower assurance
- hosted controls outside git still require explicit policy

Provider-native config does not live here. That belongs in `adapters/`.

`hardening-profile.toml` captures the runtime's reviewed container-hardening
posture (dropped capabilities, `no-new-privileges`, read-only rootfs, hardened
tmpfs mounts, PID limit, mapped non-root user) and the outbound-endpoint
inventory. The `hardening-profile-conformance` invariant
(`scripts/verify-invariants.sh`) fails closed when the launcher drifts from it.
See [docs/outbound-endpoints.md](../docs/outbound-endpoints.md).
