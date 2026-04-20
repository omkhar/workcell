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
  [docs/host-support-matrix.md](docs/host-support-matrix.md)
- capture the exact command, provider, mode, and host environment

## Include this context

- Workcell version or commit SHA
- host OS version
- provider (`codex`, `claude`, or `gemini`)
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
