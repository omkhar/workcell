# Outbound Endpoint Inventory

This page lists the fixed endpoint literals in Workcell launch plans. It is not
a list of every reachable host.

`policy/hardening-profile.toml` is the machine-checked endpoint artifact. The
repository check compares that file with endpoint source functions.

The check does not compare this Markdown page. Update this page when an endpoint
source changes.

## Enforcement scope

The launcher assembles `ALLOW_ENDPOINTS` from fixed helper sets, a versioned
runtime profile, credentials, and operator policy. It then removes each
`deny_endpoints` entry.

On Colima, Workcell resolves the result to IP addresses. It applies IPv4 and
IPv6 `DOCKER-USER` rules. A host on a shared IP address that the rules allow can
remain reachable.

The rules apply to one Colima profile. They are not session-specific. The last
launch replaces the rules for all active containers in that profile.

An unrestricted launch clears the Workcell rules for the profile. The clear
state remains until a later allowlist launch applies new rules.

Rule replacement is not atomic. The helper removes the old chains before it
resolves endpoint names. It installs the final drop rules after allow rules.

If name resolution or rule setup fails, active profile containers can lose the
Workcell default-deny rule. Stop the affected profile. Inspect its rules.

Policy changes do not stop an established connection. The rules accept
`ESTABLISHED,RELATED` traffic before they evaluate a new endpoint set.

Docker Desktop has no Workcell egress enforcement. Preview targets report
`egress_enforcement=none` and cannot launch.

## Fixed endpoint sets

### Provider endpoints

The selected agent adds one provider set.

| Provider | Endpoint literals |
|---|---|
| `codex` | `api.openai.com:443`, `auth.openai.com:443`, `chatgpt.com:443` |
| `claude` | `api.anthropic.com:443`, `claude.ai:443`, `console.anthropic.com:443` |
| `copilot` | `api.githubcopilot.com:443`, `api.individual.githubcopilot.com:443`, `api.github.com:443`, `github.com:443` |
| `gemini` | `generativelanguage.googleapis.com:443`, `ai.google.dev:443` |

### Remote broker endpoints

These sets appear in selected remote preview plans. The remote targets are
preview-only and launch-blocked.

| Target | Endpoint literals |
|---|---|
| `aws-ec2-ssm` | `ec2.amazonaws.com:443`, `ssm.amazonaws.com:443`, `ssmmessages.amazonaws.com:443`, `ec2messages.amazonaws.com:443` |
| `gcp-vm` | `compute.googleapis.com:443`, `iap.googleapis.com:443`, `oslogin.googleapis.com:443` |

### Credential endpoints

Workcell adds these sets when the selected credential is present.

| Credential | Endpoint literals |
|---|---|
| `github_hosts` or `github_config` | `github.com:443`, `api.github.com:443`, `objects.githubusercontent.com:443`, `raw.githubusercontent.com:443` |
| `gemini_oauth` or `gcloud_adc` | `accounts.google.com:443`, `oauth2.googleapis.com:443`, `sts.googleapis.com:443`, `aiplatform.googleapis.com:443` |

When Gemini has no selected auth mode, the auth-recovery set adds these entries:

- `accounts.google.com:443`
- `oauth2.googleapis.com:443`
- `sts.googleapis.com:443`

### Ephemeral runtime endpoints

An ephemeral local launch adds the pinned snapshot mirrors for Debian:

- `snapshot-cloudflare.debian.org:443`
- `snapshot.debian.org:443`

### Versioned development and build endpoints

The `development` and `build` profiles add this same fixed set:

- `github.com:443`
- `api.github.com:443`
- `objects.githubusercontent.com:443`
- `raw.githubusercontent.com:443`
- `registry.npmjs.org:443`
- `storage.googleapis.com:443`
- `pypi.org:443`
- `files.pythonhosted.org:443`
- `crates.io:443`
- `index.crates.io:443`
- `static.crates.io:443`
- `proxy.golang.org:443`
- `sum.golang.org:443`

The source files are `runtime/profiles/development.env` and
`runtime/profiles/build.env`.

### Build and prepare endpoints

The image build path uses a larger fixed set.

| Purpose | Endpoint literals |
|---|---|
| Docker registry | `auth.docker.io:443`, `docker.io:443`, `index.docker.io:443`, `registry-1.docker.io:443` |
| Docker content | `production.cloudflare.docker.com:443`, `production.cloudfront.docker.com:443`, `docker-images-prod.6aa30f8b08e16409b46e0173d6de2f56.r2.cloudflarestorage.com:443` |
| GitHub content | `github.com:443`, `objects.githubusercontent.com:443`, `release-assets.githubusercontent.com:443` |
| npm registry | `registry.npmjs.org:443` |
| Google storage | `storage.googleapis.com:443` |
| Debian snapshot | `snapshot-cloudflare.debian.org:443`, `snapshot.debian.org:443` |

## Dynamic additions

Versioned runtime profiles add `EXTRA_ENDPOINTS`. These entries are part of the
selected profile, not an operator environment override.

`gemini_env` credentials that select Vertex AI add
`aiplatform.googleapis.com:443`. Each valid location value also adds
`<location>-aiplatform.googleapis.com:443`. Workcell accepts
`GOOGLE_CLOUD_LOCATION`, `GOOGLE_CLOUD_REGION`, `CLOUD_ML_REGION`,
`VERTEX_LOCATION`, and `VERTEX_AI_LOCATION`. The fixed tables and
hardening-profile conformance check do not list these regional additions.

The injection policy can add `[network].allow_endpoints`. These operator
entries are not in the fixed tables above.

`[network].deny_endpoints` removes exact entries after all additions. On a
Colima allowlist session, an empty result stops the launch.

The tables do not list other host names that use an allowed IP address.

## Maintenance rule

The fixed source functions are in these files:

- `scripts/lib/launcher/egress-endpoints.sh`
- `scripts/workcell`

The credential-derived endpoint source is
`internal/injection/render_credentials.go`.

The `hardening-profile-conformance` check compares the fixed source functions
with `policy/hardening-profile.toml`. It checks both directions. It does not
check credential-derived endpoints or `EXTRA_ENDPOINTS` in the versioned
profile files.

Add each helper endpoint to its source function and the policy artifact. Update
each profile endpoint in the applicable profile files. Also update this page.

The repository does not have an automated drift check for this page.
