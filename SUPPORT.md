# Support

## Where to ask for help

- use GitHub Discussions for usage questions, setup help, and assurance-model
  questions
- use GitHub issues for confirmed bugs and concrete feature requests
- use [SECURITY.md](SECURITY.md) for sandbox escapes, secret exposure,
  provenance bypasses, or other security-sensitive reports

## Before opening a discussion or issue

- run `workcell --agent <provider> --doctor --workspace /path/to/repo`
- run `workcell --agent <provider> --inspect --workspace /path/to/repo`
- if auth is involved, run `workcell auth status --agent <provider>` and
  `workcell --agent <provider> --auth-status --workspace /path/to/repo`
- compare the reported `support_matrix_*` lines with
  [policy/host-support-matrix.tsv](policy/host-support-matrix.tsv), using
  [docs/diagnostics-and-support-matrix.md](docs/diagnostics-and-support-matrix.md)
  to interpret each field and [docs/support-tiers.md](docs/support-tiers.md)
  for the tier and status vocabulary
- capture the exact command, provider, mode, and host environment

## Generate a support bundle

`workcell support-bundle` collects a single, redacted JSON diagnostics document
covering the evidence needed to triage install, policy, target, provider, and
runtime failures. It runs entirely host-side and never starts the runtime.

```sh
workcell support-bundle --output ~/workcell-support-bundle.json
```

Without `--output` the bundle is written to stdout so it can be piped or
inspected directly.

### What it collects

- **install**: launcher path/presence, repo root, host OS/arch, and version
- **policy**: repo policy-file inventory (names only) and whether a user
  injection policy and hosted-controls policy are present
- **target**: state-root layout and per-profile Colima directory state
  (config/lima presence); live VM status is intentionally not collected, so run
  `workcell --agent <provider> --doctor` for live target state
- **providers**: per-provider adapter presence, support tier, and credential
  **key names** (never values)
- **sessions**: durable session-record summaries (status, target, workspace
  path) — never workspace or agent output
- **audit_pointers**: audit-log path, presence, size, and mtime — never log
  contents

Each section degrades gracefully: a missing source is recorded as a `gaps`
entry rather than failing the whole bundle.

### Redaction guarantees

The bundle is designed to be safe to attach to a public issue or discussion.
Every value is produced under these rules (also embedded in each bundle under
`redaction`):

- credential file **contents are never read** — only path, presence, and
  size/mtime metadata are recorded
- workspace and agent output are **never collected**; log bodies are referenced
  by pointer only
- token, key, password, secret, and credential material is masked by pattern
  (JWT, GitHub/OpenAI/Google/AWS/Slack tokens, PEM private keys, `Bearer`
  headers) and by secret-named `key=value` pairs
- the operator home-directory prefix is rewritten to `~` so the local username
  never leaks through paths
- only structured, enumerated diagnostic fields are emitted; there are no raw
  environment dumps or command-output blobs

The output shape is deterministic (stable field order, sorted lists), so two
bundles from the same host state differ only in the `generated_at` timestamp
and are easy to diff.

### Sharing it safely

- the bundle is redacted by construction, but skim it once before sharing
- attach it to the GitHub issue or discussion, or paste the relevant section
- for anything that looks like it exposed a secret, use
  [SECURITY.md](SECURITY.md) instead of a public issue

## Include this context

- Workcell version or commit SHA
- host OS version
- provider (`codex`, `claude`, `copilot`, or `gemini`)
- runtime mode (`strict`, `development`, `build`, or `breakglass`)
- whether the problem happens on the default safe path or only on a
  lower-assurance path

## Support window

- active development happens on `main`
- the latest tagged release is the primary install target
- security fixes land on `main`; there are no long-lived release branches
- CI and tagged-release install/uninstall verification currently run only on
  GitHub-hosted Apple Silicon `macos-26` and `macos-15`
- other macOS versions are outside the current install verification matrix

For major behavior changes, check [CHANGELOG.md](CHANGELOG.md) and
[ROADMAP.md](ROADMAP.md).
