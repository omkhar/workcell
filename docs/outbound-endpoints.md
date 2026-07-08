# Outbound-endpoint inventory

This is the reviewed inventory of every outbound `host:port` a Workcell runtime
session may reach (roadmap item A6). It is the human-readable companion to the
machine-checked `policy/hardening-profile.toml`: the endpoint literals below are
declared in that artifact and cross-checked against their source files by the
`hardening-profile-conformance` invariant (run from
`scripts/verify-invariants.sh`), so removing or altering an endpoint in the
source drifts the inventory and fails CI.

## How egress is assembled

`scripts/lib/launcher/egress-endpoints.sh` computes the per-session allowlist
(`ALLOW_ENDPOINTS`) as the deduped union of the categories below, minus any
operator `[network].deny_endpoints`. On the `colima` target with
`NETWORK_POLICY=allowlist`, that allowlist is enforced fail-closed by the
DOCKER-USER iptables/ip6tables rules
(`scripts/colima-egress-allowlist.sh`); every other target relies on its own
network controls and reports `egress_enforcement=none` on the launch summary.
Deny rules only ever tighten the list, and a deny that empties it aborts the
launch (`fail_empty_egress_after_deny`) rather than proceeding with no egress.

## Provider endpoints

Selected by `--agent`; one provider's set is active per session.

| Provider | Endpoints |
| --- | --- |
| `codex` | `api.openai.com:443`, `auth.openai.com:443`, `chatgpt.com:443` |
| `claude` | `api.anthropic.com:443`, `claude.ai:443`, `console.anthropic.com:443` |
| `copilot` | `api.githubcopilot.com:443`, `api.individual.githubcopilot.com:443`, `api.github.com:443`, `github.com:443` |
| `gemini` | `generativelanguage.googleapis.com:443`, `ai.google.dev:443` |

## Target-broker endpoints

Added only when launching against a remote runtime target.

| Target | Endpoints |
| --- | --- |
| `aws-ec2-ssm` | `ec2.amazonaws.com:443`, `ssm.amazonaws.com:443`, `ssmmessages.amazonaws.com:443`, `ec2messages.amazonaws.com:443` |
| `gcp-vm` | `compute.googleapis.com:443`, `iap.googleapis.com:443`, `oslogin.googleapis.com:443` |

## Credential endpoints

Added only when the matching injected credential is present.

| Credential | Endpoints |
| --- | --- |
| `github_hosts` / `github_config` | `github.com:443`, `api.github.com:443`, `objects.githubusercontent.com:443`, `raw.githubusercontent.com:443` |
| `gemini_oauth` / `gcloud_adc` (gemini) | `accounts.google.com:443`, `oauth2.googleapis.com:443`, `sts.googleapis.com:443`, `aiplatform.googleapis.com:443` |

## Provider-auth recovery endpoints

Emitted by `provider_auth_recovery_extra_endpoints()` in `scripts/workcell` for
Gemini when no provider auth mode is selected (reuses the Google OAuth/STS
hosts):

| Purpose | Endpoints |
| --- | --- |
| Gemini auth recovery | `accounts.google.com:443`, `oauth2.googleapis.com:443`, `sts.googleapis.com:443` |

## Ephemeral-launch endpoints

Appended by `ephemeral_extra_endpoints()` in `scripts/workcell` on non-remote
`CONTAINER_MUTABILITY=ephemeral` launches (the in-container apt path needs the
Debian snapshot mirrors). A distinct runtime code path from the bootstrap set,
though the hosts overlap:

| Purpose | Endpoints |
| --- | --- |
| Debian snapshot (ephemeral apt) | `snapshot-cloudflare.debian.org:443`, `snapshot.debian.org:443` |

## Bootstrap (rebuild/prepare) endpoints

The runtime image build/rebuild path (`--prepare` / `--rebuild`) reaches a wider
set than a normal session. `bootstrap_endpoints()` in `scripts/workcell` is the
complete source of truth:

| Purpose | Endpoints |
| --- | --- |
| Docker registry / auth | `auth.docker.io:443`, `docker.io:443`, `index.docker.io:443`, `registry-1.docker.io:443` |
| Docker CDN / blob storage | `production.cloudflare.docker.com:443`, `production.cloudfront.docker.com:443`, `docker-images-prod.6aa30f8b08e16409b46e0173d6de2f56.r2.cloudflarestorage.com:443` |
| GitHub sources / release assets | `github.com:443`, `objects.githubusercontent.com:443`, `release-assets.githubusercontent.com:443` |
| npm registry | `registry.npmjs.org:443` |
| Google storage | `storage.googleapis.com:443` |
| Debian snapshot (pinned APT) | `snapshot-cloudflare.debian.org:443`, `snapshot.debian.org:443` |

## Keeping this inventory current

This inventory is the union of two sources, both drift-checked against
`policy/hardening-profile.toml` by the `hardening-profile-conformance`
invariant:

- Runtime session egress — the `*_endpoints()` helpers in
  `scripts/lib/launcher/egress-endpoints.sh` (provider, target broker,
  credential) plus `provider_auth_recovery_extra_endpoints` and
  `ephemeral_extra_endpoints` in `scripts/workcell`.
- Rebuild/prepare egress — `bootstrap_endpoints()` in `scripts/workcell`.

Each endpoint section is bound to its `*_endpoints()` source function and the
conformance check enforces an EXACT, bidirectional match: it fails both when a
declared endpoint is missing from the function AND when the function emits an
endpoint the artifact never declared. So a new `host:port` in any of these
helpers fails CI until it is added to the matching table above and to
`policy/hardening-profile.toml` — the inventory cannot silently fall behind.

## Operator extension points

`INJECTION_EXTRA_ENDPOINTS` (injection policy) and `EXTRA_ENDPOINTS` (operator
override) may add further `host:port` entries at launch; these are session-scoped
and are not part of the reviewed inventory above.
