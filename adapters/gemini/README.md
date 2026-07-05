# Gemini Adapter

The Gemini adapter seeds Gemini CLI's session-local home from reviewed baselines
and explicit injection inputs. Workcell's external VM-plus-container boundary is
primary; Gemini's own sandbox is optional and not the Tier 1 boundary here.

## Auth methods

- `gemini_env` credential key — API key, GCA, or Vertex configuration seeded to
  `~/.gemini/.env` (`internal/adapters/data.go`).
- `gemini_oauth` credential key — cached Gemini OAuth state seeded to
  `~/.gemini/oauth_creds.json`.
- `gemini_projects` credential key — persisted project registry seeded to
  `~/.gemini/projects.json`; a supplemental input, not a standalone auth mode.
- `gcloud_adc` credential key — Vertex application-default credentials seeded to
  `~/.config/gcloud/application_default_credentials.json`; supplemental to
  `gemini_env` Vertex config, not a standalone auth mode.
- Shared GitHub CLI (`github_hosts`, `github_config`) and SSH inputs apply
  (`sharedCredentialsEnabled: true` in `internal/adapters/data.go`).
- Google OAuth/ADC needs extra egress: `accounts.google.com:443`,
  `oauth2.googleapis.com:443`, `sts.googleapis.com:443`
  (`GeminiGoogleAuthEndpoints` in `internal/adapters/data.go`).

See [../../docs/injection-policy.md](../../docs/injection-policy.md) and the
Gemini retirement caveat in
[../../docs/provider-matrix.md](../../docs/provider-matrix.md).

## Managed control-plane files

Repo baselines under `adapters/gemini/`:

- `.gemini/settings.json`: managed settings seeded to `~/.gemini/settings.json`.
- `GEMINI.md`: managed baseline rendered into `~/.gemini/GEMINI.md`.

Additional in-container session targets: `~/.gemini/.env`,
`~/.gemini/oauth_creds.json`, `~/.gemini/projects.json`,
`~/.gemini/trustedFolders.json`, and the gcloud ADC path
(`ReservedTargets` in `internal/adapters/data.go`).

## Adapter behavior

- Each launch rebuilds the provider home from the baseline plus explicit
  injection inputs; repo-local `.gemini/` content is masked on the safe path
  (`runtime/container/home-control-plane.sh`, `docs/invariants.md` §3).
- Workcell seeds `~/.gemini/trustedFolders.json` for `/workspace` so masked
  ephemeral sessions do not force a restart-based trust prompt; `breakglass`
  restores Gemini's own folder-trust flow
  (`docs/adapter-control-planes.md#gemini-folder-trust`).
- The wrapper sanitizes Gemini's own-sandbox env
  (`GEMINI_SANDBOX*`) before launch (`sanitize_gemini_sandbox_env` in
  `runtime/container/provider-wrapper.sh`).
- Autonomy is set host-side via `workcell --agent-autonomy` (mapped to
  `--approval-mode`); provider-native overrides are not honored.
- Unsafe-argument policy (`reject_unsafe_gemini_args` in
  `runtime/container/provider-policy.sh`): the wrapper blocks
  `--*dangerously*`, `--*bypass*permission*`, `--sandbox`, `--add-dir`,
  `-y`/`--yolo`, and in-session `--approval-mode` overrides. `breakglass`
  exempts these.

## See also

- [../README.md](../README.md) — adapter index and common contract
- [../../docs/adapter-control-planes.md](../../docs/adapter-control-planes.md)
- [../../docs/invariants.md](../../docs/invariants.md)
- [../../docs/extending-adapters.md](../../docs/extending-adapters.md) — worked
  contributor examples
