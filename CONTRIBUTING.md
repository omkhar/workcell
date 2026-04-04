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

## Prerequisites

Local development expects:

- `git`
- `go`
- `docker`
- `shellcheck`
- `shfmt`
- `cargo`, `rustfmt`, and `clippy`
- `actionlint`
- `zizmor`
- `jq`

On macOS with Homebrew:

```bash
brew install go shellcheck shfmt actionlint zizmor jq
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
2. Make the change.
3. Run the fast local gate:

   ```bash
   ./scripts/dev-quick-check.sh
   ```

4. Before opening a PR, run the full local gate:

   ```bash
   ./scripts/pre-merge.sh
   ```

5. Open a PR against `main`.

## Commit messages

Use [Conventional Commits](https://www.conventionalcommits.org/) format:

```
<type>: <description>

[optional body]
```

Common types: `feat`, `fix`, `docs`, `chore`, `test`, `refactor`.

Keep the subject line under 72 characters. If the change touches the
runtime boundary, trust model, or provider adapters, mention it in the
body.

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
