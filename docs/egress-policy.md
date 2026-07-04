# Egress Policy

Workcell ships a default-deny, per-session network allowlist for the managed
local path. This document is the reviewed policy artifact for that control: how
it is computed and enforced end to end, which endpoint sources feed it, the
operator `[network]` extension and tightening surface, and which targets
actually enforce it.

## Shipped mechanism

`strict` mode sets `NETWORK_POLICY=allowlist` (`runtime/profiles/strict.env`).
On the `colima` target the launcher enforces that allowlist as a fail-closed,
dual-stack, default-deny firewall inside the runtime VM:

1. The host launcher (`scripts/workcell`) computes `ALLOW_ENDPOINTS` — the union
   of every reviewed endpoint source (below), de-duplicated, then tightened by
   any operator deny list.
2. The launcher hands that endpoint list to
   `scripts/colima-egress-allowlist.sh`, which programs `iptables` and
   `ip6tables` rules in the `DOCKER-USER` chain of the Colima VM.
3. The rules ACCEPT established/related return traffic and each allowed
   `host:port` (resolved to IPv4 and IPv6 addresses), then end with an explicit
   `DROP`. Anything not on the allowlist is denied.
4. Enforcement is fail-closed and dual-stack: if `ip6tables` is unavailable the
   helper aborts rather than leaving IPv6 egress unfiltered, so a session never
   silently loses IPv6 containment.

The allowlist matches on `host:port`. Only the colima target applies these
rules; the launcher does not change the enforcement dispatch based on policy
content.

## Endpoint sources

`ALLOW_ENDPOINTS` is assembled from these reviewed sources:

- provider endpoints for the selected agent
- target/broker endpoints for the runtime backend
- credential-derived endpoints (for example the Google auth endpoints a Gemini
  OAuth/ADC credential requires)
- provider auth-recovery endpoints
- injection-policy `[network].allow_endpoints` (operator extension)
- `EXTRA_ENDPOINTS` from the profile
- `snapshot.debian.org:443` for ephemeral-container package refresh

The combined list is de-duplicated, then every endpoint in
`[network].deny_endpoints` is subtracted (deny wins).

## Operator `[network]` surface

Operators extend or tighten the per-session allowlist only through the reviewed
injection-policy path, never by disabling the default. The injection policy
accepts a top-level `[network]` table:

```toml
[network]
allow_endpoints = ["registry.internal.example:443"]  # add to the allowlist
deny_endpoints  = ["chatgpt.com:443"]                 # remove from the allowlist
```

- `allow_endpoints` are unioned into the computed allowlist. They extend it;
  they never replace the reviewed provider/credential endpoints.
- `deny_endpoints` are subtracted from the computed allowlist after every allow
  source is combined. Deny wins: if an operator denies an endpoint a provider
  would otherwise need, it is removed. That is an intentional operator
  tightening, not a bug.
- Each endpoint must be `host:port` or `[ipv6]:port`, with a port in 1-65535 and
  a host matching `^[A-Za-z0-9.-]+$` (no leading dot, no `..`). This grammar is
  validated in the injection path with the same rules
  `scripts/colima-egress-allowlist.sh` applies, so an endpoint the policy
  accepts is one the enforcement helper accepts.
- The surface is fail-closed: a malformed endpoint, an empty string, an unknown
  key under `[network]`, or a non-array value aborts the launch with an error
  that names the offending value.

### No-weakening invariant

The `[network]` surface can only contribute endpoint lists. It cannot set
`NETWORK_POLICY`, switch to an unrestricted posture, or disable the allowlist.
`allow_endpoints` only ever adds endpoints and `deny_endpoints` only ever
removes them; there is no policy key or code path through which `[network]`
changes the enforcement mode. The shipped default-deny allowlist is never
weakened by an injection policy.

## Enforcement parity

Per-session allowlist enforcement is a property of the `colima` target only.
Other targets rely on their own network controls and do not receive the
`DOCKER-USER` allowlist. The launch summary makes this explicit with an
`egress_enforcement=` line:

| Target | `egress_enforcement` | Per-session allowlist enforced |
|---|---|---|
| `colima` (allowlist) | `allowlist` | yes — `iptables`/`ip6tables` default-deny in `DOCKER-USER` |
| `colima` (unrestricted, e.g. breakglass) | `none` | no — allowlist not applied |
| `docker-desktop` | `none` | no — relies on Docker Desktop / host network controls |
| `aws-ec2-ssm` (preview) | `none` | no — relies on the VM's own security groups / network controls |
| `gcp-vm` (preview) | `none` | no — relies on the VM's own firewall / network controls |

`egress_enforcement=allowlist` prints only when `TARGET_BACKEND == colima` and
`NETWORK_POLICY == allowlist`; every other combination prints
`egress_enforcement=none`. The label sits next to the existing
`network_policy=... endpoints=...` summary line so operators can see at a glance
whether the session is actually enforced or is relying on the target's own
controls.

## Related docs

- [Injection policy](injection-policy.md) documents the `[network]` key
  alongside the other reviewed injection surfaces.
- [Security invariants](invariants.md) records the network-posture invariant.
- [Threat model](threat-model.md) covers egress control in the abuse-path model.
