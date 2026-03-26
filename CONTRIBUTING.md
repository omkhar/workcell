# Contributing to Workcell

## Prerequisites

The following tools must be installed before working on this repository:

| Tool | Used by |
|------|---------|
| `shellcheck` | `dev-quick-check.sh`, `pre-merge.sh` |
| `shfmt` | `dev-quick-check.sh` |
| `python3` | `dev-quick-check.sh` (compile + unittest) |
| `cargo` + `rustfmt` | `dev-quick-check.sh` (Rust launcher) |
| `docker` | `pre-merge.sh` (validator image, smoke, repro) |
| `git` | `pre-merge.sh` |
| `actionlint` | `scripts/check-workflows.sh` (called by `pre-merge.sh`) |
| `zizmor` | `scripts/check-workflows.sh` (called by `pre-merge.sh`) |
| `cosign` | Release signing and provenance verification |
| `jq` | Runtime container scripts |

On macOS, install Colima for the VM boundary:

```bash
brew install colima docker shellcheck shfmt python3 rustup actionlint zizmor jq
rustup-init  # then: rustup component add rustfmt
```

## Quick start for contributors

1. Fork the repository and create a feature branch from `main`.
2. Make your changes. Keep `runtime/`, `policy/`, `adapters/`, `verify/`, and `workflows/` in lockstep if a change touches shared contracts.
3. Run the fast local check before committing:

   ```bash
   ./scripts/dev-quick-check.sh
   ```

4. Before opening a PR, run the full pre-merge stack:

   ```bash
   ./scripts/pre-merge.sh
   ```

5. Open a PR against `main`. Never push directly to `main`.

## Test levels

### `./scripts/dev-quick-check.sh`

Fast, host-local validation. Run this after every non-trivial edit.

What it checks:

- `shellcheck` on all first-party shell scripts (entrypoints, container helpers, runtime scripts)
- `shfmt` format check on those same scripts (2-space indent, `bash` dialect, `case` indentation)
- Python syntax compilation (`py_compile`) and `unittest` discovery for `scripts/lib/` and `tests/python/`
- `cargo fmt --check` and `cargo test --locked --offline` for the Rust launcher under `runtime/container/rust/`

When to run: before every commit and as a git pre-commit hook if desired.

### `./scripts/pre-merge.sh`

Full local pre-merge stack. Requires Docker and a clean worktree by default.

What it checks, in order:

1. **Pinned-input policy** (`scripts/check-pinned-inputs.sh`) — verifies Debian snapshot pins, base-image digests, runtime and validator package sets, BuildKit/QEMU/Cosign/Syft workflow inputs, and provider lockfile graph integrity.
2. **Upstream Codex release verification** (`scripts/verify-upstream-codex-release.sh`) — re-verifies the pinned Codex release assets against OpenAI's published Sigstore bundle.
3. **Builds the validator image** from `tools/validator/Dockerfile`.
4. **Workflow lint** (`scripts/check-workflows.sh`) — `actionlint` and `zizmor` on all GitHub Actions workflows. Requires host `actionlint` and `zizmor`.
5. **Repository validation** (`scripts/validate-repo.sh`) — shell, JSON, TOML, YAML, and manpage checks run inside the validator container.
6. **Invariant checks** (`scripts/verify-invariants.sh`) — launcher and adapter policy regression tests.
7. **Container smoke** (`scripts/container-smoke.sh`) — direct container build and adapter smoke tests.
8. **Release bundle reproducibility** (`scripts/verify-release-bundle.sh`) — deterministic source bundle verification under a fixed `SOURCE_DATE_EPOCH`.
9. **Runtime reproducibility** (`scripts/verify-reproducible-build.sh`) — paired multi-platform OCI image verification.

Flags:

```bash
./scripts/pre-merge.sh --allow-dirty          # validate the live worktree without requiring a clean state
./scripts/pre-merge.sh --skip-repro           # skip the reproducible-build check (saves significant time)
./scripts/pre-merge.sh --skip-release-bundle  # skip the release bundle check
./scripts/pre-merge.sh --remote               # also run the remote linux/amd64 validate lane
./scripts/pre-merge.sh --rebuild-validator    # force-rebuild the local validator image
```

When to run: before opening a PR and whenever you change `runtime/`, `scripts/`, `tools/`, or `.github/workflows/`.

### Full CI (push and PR)

GitHub-hosted CI validates everything `pre-merge.sh` covers except the Colima VM boundary (which remains a local-macOS exercise). CI also:

- Runs on `linux/amd64` and `linux/arm64` via QEMU.
- Verifies release signing, SBOMs, and provenance attestations on tagged releases.
- Publishes the final multi-arch runtime image after tag validation.

See `docs/github-workflows.md` and `docs/provenance.md` for details.

## Branch and PR workflow

- **Always use feature branches.** Never push directly to `main` or rewrite history on `main`.
- Branch naming is not enforced, but use a descriptive prefix: `fix/`, `feat/`, `docs/`, `chore/`.
- PR descriptions should state what changed and why, note any invariant assumptions the change relies on, and call out any lower-assurance modes introduced or widened.
- If a security control depends on a specific runtime assumption, document that assumption in the same PR.
- Reference the relevant section of `docs/threat-model.md` or `docs/invariants.md` if the change touches the trust boundary.

## Security disclosures

Do not open a public issue for sandbox escapes, secret-exposure paths, credential leaks, signing bypasses, or boundary-preservation bugs.

See [SECURITY.md](SECURITY.md) for reporting instructions.

## Adding a new adapter

Adapters are thin mappings from the shared Workcell runtime into a provider's native control surface. Keep the adapter thin; do not treat provider config as the primary security boundary.

### Step-by-step

1. **Create the adapter directory** under `adapters/<provider>/`.

2. **Add a `README.md`** describing which native control files the adapter manages and its assurance tier. See `adapters/claude/README.md` and `adapters/codex/README.md` for the expected structure and tone.

3. **Add native config files** for the provider's home directory (settings JSON, TOML config, instruction docs, MCP template). Follow the existing pattern:
   - Claude: `managed-settings.json`, `mcp-template.json`, `CLAUDE.md`, `hooks/`
   - Codex: `managed_config.toml`, `requirements.toml`, `mcp/config.toml`
   - Gemini: `.gemini/settings.json`, `GEMINI.md`

4. **Add a `seed_<provider>_home()` function** in `runtime/container/home-control-plane.sh`. The function must:
   - Create the provider's home directory under `/state/agent-home/`.
   - Link or copy managed baseline files with `workcell_link_control_plane_path` (for immutable files) or a copy with appropriate `chmod`.
   - Call `workcell_render_provider_doc` to render the instruction doc with workspace import and injection policy layering.
   - Call `workcell_copy_manifest_credential_file` for any provider-specific credential keys.

5. **Register the provider in `provider-wrapper.sh`**:
   - Add an autonomy-flag mapping in the `AGENT_NAME:WORKCELL_AGENT_AUTONOMY` case.
   - Add a launch `exec` branch in the `AGENT_NAME` case at the bottom.
   - Add `reject_unsafe_<provider>_args` in `provider-policy.sh` if the provider accepts override flags that must be blocked.

6. **Add the provider to `docs/provider-matrix.md`** with its native control plane, boundary fit, and notes.

7. **Write invariant checks** in `verify/` for the new adapter. Ship invariant tests with the adapter, not as a follow-up.

8. **Update `docs/adapter-control-planes.md`** to include the new provider's control file matrix and flag mappings.

9. **Add MCP template** — ship an empty or locked-down MCP config. Do not seed any live registry-backed MCP defaults. See `adapters/codex/mcp/config.toml` and `adapters/claude/mcp-template.json`.

### Rules

- Never claim the adapter config is the primary security boundary.
- Keep CLI and GUI assurance tiers separate. Mark GUI paths as lower-assurance explicitly.
- Do not mount host credentials, home directories, or sockets through the adapter.
- Mask repo-local control files for the new provider (`.claude/`, `.codex/`, `.gemini/` equivalents) on the safe path.

## Common maintenance tasks

### Update a Debian snapshot pin

The Debian snapshot URL and date are pinned in the Dockerfile under `runtime/container/`. Update the snapshot date and the corresponding digest. Run `scripts/check-pinned-inputs.sh` to confirm the new pin passes policy checks, then run `./scripts/pre-merge.sh` to confirm the container still builds reproducibly.

### Update the Codex version pin

The pinned Codex release version, checksum, and Sigstore bundle reference live in the build input manifest and Dockerfile. After updating:

1. Run `scripts/verify-upstream-codex-release.sh` to re-verify the new release assets against the published Sigstore bundle.
2. Run `scripts/check-pinned-inputs.sh` to confirm lockfile graph integrity.
3. Run `./scripts/pre-merge.sh` to confirm the full stack passes.

### Add a Codex execpolicy rule

Codex execpolicy rules live in `adapters/codex/.codex/rules/default.rules` (the baseline readable by agents) and are reflected in `adapters/codex/requirements.toml` (the hard requirements file).

To add a rule:

1. Add a `[[rules.prefix_rules]]` entry to `adapters/codex/requirements.toml` with the `pattern`, `decision`, and `justification` fields.
2. Add the same or a corresponding entry to `adapters/codex/managed_config.toml` if it belongs in the admin-managed baseline.
3. Run `./scripts/dev-quick-check.sh` to confirm TOML syntax is valid.
4. Add or update the corresponding invariant test in `verify/`.

## Code style

### Shell (`shellcheck` + `shfmt`)

All first-party shell scripts must pass `shellcheck -x` and `shfmt -ln=bash -i 2 -ci -d`. The exact list of checked files is in `scripts/dev-quick-check.sh`. Scripts start with `#!/usr/bin/env -S BASH_ENV= ENV= bash` and `set -euo pipefail` to sanitize the loader environment before any logic runs.

### Python

Python files under `scripts/lib/` and `tests/python/` must compile with `python3 -m py_compile` and all `test_*.py` files must pass `python3 -m unittest discover`. No external linter is currently enforced beyond compilation and passing tests; keep style consistent with the existing files.

### Rust

The Rust launcher under `runtime/container/rust/` must pass `cargo fmt --all --check` and `cargo test --locked --offline`. Run both via `./scripts/dev-quick-check.sh`.
