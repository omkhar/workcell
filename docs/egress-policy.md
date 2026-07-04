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
3. The rules ACCEPT established/related traffic and each allowed `host:port`
   (resolved to IPv4 and IPv6), then end with an explicit `DROP`.
4. Enforcement is fail-closed and dual-stack: if `ip6tables` is unavailable the
   helper aborts rather than leave IPv6 egress unfiltered.

Only the colima target applies these rules; the dispatch never changes based on
policy content.

## Endpoint sources

`ALLOW_ENDPOINTS` is assembled from these reviewed sources:

- provider endpoints for the selected agent
- target/broker endpoints for the runtime backend
- credential-derived endpoints (e.g. the Google auth endpoints a Gemini OAuth/ADC credential requires)
- provider auth-recovery endpoints
- injection-policy `[network].allow_endpoints` (operator extension)
- `EXTRA_ENDPOINTS` from the profile
- `snapshot.debian.org:443` for ephemeral-container package refresh

The combined list is de-duplicated, then every endpoint in
`[network].deny_endpoints` is subtracted (deny wins).

## Operator `[network]` surface

Operators extend or tighten the allowlist only through the reviewed
injection-policy `[network]` table, never by disabling the default:

```toml
[network]
allow_endpoints = ["registry.internal.example:443"]  # add to the allowlist
deny_endpoints  = ["chatgpt.com:443"]                 # remove from the allowlist
```

- `allow_endpoints` are unioned into the computed allowlist (they extend, never
  replace the reviewed provider/credential endpoints).
- `deny_endpoints` are subtracted after every allow source is combined. Deny
  wins — denying an endpoint a provider needs removes it (an intentional
  tightening, not a bug).
- Each endpoint must be `host:port` or `[ipv6]:port` (port 1-65535, host
  `^[A-Za-z0-9.-]+$`, no leading dot or `..`, IP-shaped hosts must be real IPs),
  validated with the same grammar `scripts/colima-egress-allowlist.sh` applies.
- Fail-closed: a malformed endpoint, empty string, unknown `[network]` key, or
  non-array value aborts with an error naming the offending value.

### No-weakening invariant

The `[network]` surface can only contribute endpoint lists — `allow_endpoints`
adds, `deny_endpoints` removes. It cannot set `NETWORK_POLICY`, disable the
allowlist, or switch to an unrestricted posture: no policy key or code path lets
`[network]` change the enforcement mode, so an injection policy never weakens the
shipped default-deny allowlist.

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
`NETWORK_POLICY == allowlist`; every other combination prints `none`. The label
sits next to the `network_policy=... endpoints=...` summary line so operators see
at a glance whether the session is enforced or relying on the target's controls.

## Related docs

- [Injection policy](injection-policy.md) documents the `[network]` key
  alongside the other reviewed injection surfaces.
- [Security invariants](invariants.md) records the network-posture invariant.
- [Threat model](threat-model.md) covers egress control in the abuse-path model.
