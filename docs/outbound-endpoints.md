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

Gemini provider-auth recovery (`provider_auth_recovery_extra_endpoints`, when no
provider auth mode is selected) reuses `accounts.google.com:443`,
`oauth2.googleapis.com:443`, and `sts.googleapis.com:443` from the same set.

## Bootstrap endpoints

Added for the Debian snapshot image-build path.

| Purpose | Endpoints |
| --- | --- |
| Debian snapshot (pinned APT) | `snapshot-cloudflare.debian.org:443`, `snapshot.debian.org:443` |

## Operator extension points

`INJECTION_EXTRA_ENDPOINTS` (injection policy) and `EXTRA_ENDPOINTS` (operator
override) may add further `host:port` entries at launch; these are session-scoped
and are not part of the reviewed inventory above.
