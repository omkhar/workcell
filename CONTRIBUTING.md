# Contributing to Workcell

Workcell changes should preserve the runtime boundary first and developer
ergonomics second.

## Ground rules

- keep `runtime/`, `policy/`, `adapters/`, `verify/`, and `workflows/` in
  sync when a change touches shared contracts
- do not widen trust silently
- document lower-assurance paths instead of implying parity
- sign every commit
- use feature branches and pull requests; do not push directly to `main`

## First-time setup

Use the bootstrap helper:

```bash
./scripts/bootstrap-dev.sh
```

That script installs the common local toolchain, configures `.githooks` as the
repo hook path, and leaves you ready to run the local gates.

## Prerequisites

Local development expects:

- `git`
- `go`
- `docker`
- `shellcheck`
- `shfmt`
- `yamllint`
- `codespell`
- `actionlint`
- `zizmor`
- `jq`
- `cargo`, `rustfmt`, and `clippy`

On macOS with Homebrew:

```bash
brew install go shellcheck shfmt yamllint codespell actionlint zizmor jq
brew install --cask docker
rustup-init  # installs cargo, rustfmt, clippy
```

For the real VM boundary path:

```bash
brew install colima
```

## Commit signing

Every commit on `main` must be signed. Set up GPG or SSH signing before your
first contribution:

```bash
git config --global commit.gpgsign true
git config --global user.signingkey <your-key>
```

See [GitHub's docs on signing commits][sign-docs] for setup details.

[sign-docs]: https://docs.github.com/en/authentication/managing-commit-signature-verification

## Recommended workflow

1. Create a feature branch from `main`.
2. Bootstrap once if you have not already:

   ```bash
   ./scripts/bootstrap-dev.sh
   ```

3. Make the change.
4. Run the fast local gate:

   ```bash
   ./scripts/dev-quick-check.sh
   ```

5. Before opening a PR, run the full local gate:

   ```bash
   ./scripts/pre-merge.sh
   ```

6. Open a PR against `main`.

The pre-commit hook blocks unrelated commits when stable provider pin bumps are
pending and points you at `./scripts/publish-provider-bump-pr.sh`.

## Good first contributions

Useful starter changes tend to be:

- quickstart, README, or manpage consistency fixes
- validation coverage for already-documented behavior
- scenario-gap closure that does not change the trust model
- adapter documentation and control-plane clarity improvements

If a change touches the boundary or policy model, read
[docs/invariants.md](docs/invariants.md) and [docs/threat-model.md](docs/threat-model.md)
first.

## Commit messages

Use [Conventional Commits](https://www.conventionalcommits.org/) format:

```
<type>: <description>

[optional body]
```

Common types: `feat`, `fix`, `docs`, `chore`, `test`, `refactor`.

Keep the subject line under 72 characters. If the change touches the runtime
boundary, trust model, or provider adapters, mention it in the body.

## Validation levels

### Fast local gate

`./scripts/dev-quick-check.sh` is the normal edit loop. It covers:

- shell lint and format checks
- Rust fmt, clippy, and tests

For fuller repo validation without the entire pre-merge stack, use:

```bash
./scripts/build-and-test.sh
./scripts/build-and-test.sh --docker
```

The default path is host-native. `--docker` reruns repo validation inside the
validator container from a disposable snapshot of the current worktree.

### Full local gate

`./scripts/pre-merge.sh` is the normal pre-PR gate. It covers:

- pinned-input checks
- upstream release verification for pinned provider artifacts
- validator-image rebuild
- workflow lint
- repo validation
- invariant checks
- container smoke
- source-bundle reproducibility
- runtime-image reproducibility

Helpful flags:

```bash
./scripts/pre-merge.sh --allow-dirty
./scripts/pre-merge.sh --skip-repro
./scripts/pre-merge.sh --skip-release-bundle
./scripts/pre-merge.sh --remote
./scripts/pre-merge.sh --rebuild-validator
```

## Pull requests

A good PR should:

- explain what changed and why
- call out any runtime or trust assumptions the change depends on
- note any lower-assurance modes introduced or widened
- update docs in the same change when behavior changes
- update [CHANGELOG.md](CHANGELOG.md) for user-visible changes

If you touch the boundary or policy model, link the relevant invariant or
threat-model section in the PR description.

## Security-sensitive issues

Do not open a public issue for:

- sandbox escapes
- secret exposure
- signing or provenance bypasses
- unexpected trust widening

Use the process in [SECURITY.md](SECURITY.md).

## Adding or changing adapters

Adapters should stay thin. A new or changed adapter should:

1. map into the provider's native control plane
2. avoid treating provider config as the primary boundary
3. ship invariant checks with the adapter change
4. update the provider matrix and adapter-control-plane docs

See [workflows/adapter-porting.md](workflows/adapter-porting.md) for the
porting checklist.

## Project docs

- [GOVERNANCE.md](GOVERNANCE.md)
- [MAINTAINERS.md](MAINTAINERS.md)
- [ROADMAP.md](ROADMAP.md)
- [SUPPORT.md](SUPPORT.md)
- [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md)
