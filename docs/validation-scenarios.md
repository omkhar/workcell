# Validation Scenarios

This page groups the validation paths used by the provider validation plan.
The split is intentional: secretless tests stay default, provider e2e stays
explicit, and breakglass remains outside the normal workflow.

## Local Secretless Tests

Use this lane when you want fast validation without provider credentials.
It should exercise repository shape, launcher behavior, invariant checks, and
container smoke that does not depend on provider auth.

Recommended entry points are the normal local validation commands already
documented in the README, such as `./scripts/dev-quick-check.sh` and the
broader pre-merge path when you need full local coverage.

Recommended commands:

- `./scripts/validate-repo.sh`
- `./scripts/verify-invariants.sh`
- `./scripts/container-smoke.sh`
- `./scripts/pre-merge.sh`

If you want the broader local validation stack to run against a disposable
snapshot instead of the live worktree, use the reviewed pre-merge flags:

- `./scripts/pre-merge.sh --local-snapshot worktree --local-include-untracked`
- `./scripts/pre-merge.sh --local-snapshot head`

The snapshot mode is intentionally scoped to `pre-merge.sh` today. Provider
authenticated smoke remains an explicit live-worktree lane.

## Local Provider Authenticated Smoke

Use this lane when you need to validate that Workcell stages real provider
credentials and completes a small authenticated provider probe inside the
Workcell boundary.
The credential source must be explicit and operator-owned.

Place provider credentials in the dedicated injection path, not in repo files,
shell history, host homes, or ambient environment variables. This lane proves
credential detection, control-plane seeding, strict launch, and a minimal
provider-authenticated round trip. It is still an owner-triggered smoke lane,
not a high-volume integration test.

The reviewed entry point is:

- `./scripts/provider-e2e.sh --agent codex --workspace "$PWD"`
- `./scripts/provider-e2e.sh --agent claude --workspace "$PWD"`
- `./scripts/provider-e2e.sh --agent gemini --workspace "$PWD"`

That helper first runs `workcell --auth-status`, then `--prepare-only`, then a
provider-specific non-interactive prompt that must emit the exact token
`WORKCELL_PROVIDER_E2E_OK`.

## GitHub CI Versus Manual Provider Authenticated Smoke

GitHub default CI should stay secretless and deterministic. It is the lane for
repo validation, workflow lint, pinned-input checks, and other checks that do
not require provider credentials.

The manual provider-e2e workflow is a separate `workflow_dispatch` lane. It
should stay narrow, require explicit operator selection of the provider, pull
any needed credentials from a dedicated secret-backed environment on the
self-hosted macOS path, stay limited to the default branch, and only execute
for the repository owner. A non-secret guard job should fail fast before the
self-hosted secret-bearing job starts when those preconditions are not met. It
should not run on pull requests.

## Credential Placement

Use the least-broad credential location that can support the test:

- Local runs: `~/.config/workcell/injection-policy.toml` or another
  operator-owned secret source that feeds that policy
- GitHub manual provider-e2e runs: environment-scoped secrets for
  `provider-e2e.yml` only, with environment protection rules enabled
- Default CI: no provider credentials

Never place provider secrets in committed files, default CI variables, host
home directories, git config, or socket passthrough paths.

Recommended local credential files:

- Codex: `credentials.codex_auth = "~/.codex/auth.json"`
- Claude: `credentials.claude_auth = "~/.claude/.credentials.json"` or
  `credentials.claude_api_key = "~/.config/workcell/claude-api-key.txt"`
- Gemini: `credentials.gemini_env = "~/.config/workcell/gemini.env"` or
  `credentials.gemini_oauth = "~/.config/workcell/gemini-oauth.json"`
- Gemini Vertex supplement: `credentials.gcloud_adc =
  "~/.config/gcloud/application_default_credentials.json"`
- Shared GitHub CLI auth when needed:
  `credentials.github_hosts` / `credentials.github_config`

Recommended GitHub environment secret names:

- `WORKCELL_E2E_CODEX_AUTH_JSON`
- `WORKCELL_E2E_CLAUDE_AUTH_JSON`
- `WORKCELL_E2E_CLAUDE_API_KEY`
- `WORKCELL_E2E_CLAUDE_MCP_JSON`
- `WORKCELL_E2E_GEMINI_ENV`
- `WORKCELL_E2E_GEMINI_OAUTH_JSON`
- `WORKCELL_E2E_GEMINI_PROJECTS_JSON`
- `WORKCELL_E2E_GCLOUD_ADC_JSON`

The `provider-e2e.sh` helper intentionally limits its generated env-backed
policy to the selected provider's credentials. If a future scenario needs
shared GitHub CLI auth, pass an explicit injection policy instead of expanding
the `WORKCELL_E2E_*` environment surface for this smoke lane.

## Out Of Scope And Breakglass

These scenarios are intentionally out of scope for the normal validation plan:

- host-native GUI or browser-only provider validation
- unrestricted network or provider overrides that would require
  `breakglass`
- broad arbitrary-command debugging paths
- any workflow that assumes host credential passthrough or ambient auth state
- PR-triggered self-hosted runs that could expose provider-backed secrets to
  untrusted code

If a check needs any of those conditions, label it as `breakglass` explicitly
and require the separate acknowledged path described elsewhere in the docs.
