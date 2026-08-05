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
- use ASD-STE100 Simplified Technical English Issue 9 for public prose

See [docs/documentation-language.md](docs/documentation-language.md) for the
project language rules.

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
- Node.js and npm (the bootstrap helper enforces the repository-locked
  markdownlint runtime requirement)
- `cargo`, `rustfmt`, and `clippy`

On macOS with Homebrew:

```bash
brew install go node shellcheck shfmt yamllint codespell actionlint zizmor jq
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

5. Create a signed commit. Use Risk-Aware Commit Notation.
6. Before publication, run the full local gate:

   ```bash
   ./scripts/pre-merge.sh --profile pr-parity
   ```

7. Publish a draft PR against `main` with the repository wrapper:

   ```bash
   ./scripts/repo-publish-pr.sh \
     --branch feature-name \
     --title "PR title" \
     --body "Explain the change and its evidence." \
     --commit-message "^D Describe the change (validation passes; user-visible documentation)"
   ```

8. Post the standalone PR comment `@codex review`.
9. Fix or disposition each finding.
10. Resolve each review thread.
11. Repeat the Codex review after each push.
12. Require a clean Codex marker for the current head.
13. Mark the PR ready only after all required checks and reviews pass.
14. Check comments and review threads again immediately before merge.

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

Use GitHub Discussions for usage questions, open-ended design exploration, and
operator workflow conversations. Use GitHub issues for confirmed bugs and
concrete feature requests.

## Commit messages

Use Risk-Aware Commit Notation:

```text
<risk><intention> <description> (risk reason; case reason)
```

Risk symbols are `.`, `^`, `!`, and `@`. They mean safe, validated, risky,
and broken.

Intention letters are `F`, `B`, `R`, and `D`. They mean feature, bug fix,
refactor, and documentation. Use an uppercase letter for a user-visible change.

Example:

```text
^D Update release status (validation passes; user-visible documentation)
```

See [the commit skill](.agents/skills/commit/SKILL.md) for the complete rules.

## Validation levels

### Fast local gate

`./scripts/dev-quick-check.sh` is the normal edit loop. It covers:

- shell lint and format checks (`shellcheck`, `shfmt`)
- Dockerfile lint via `hadolint`
- Go formatting (`gofmt -l`), `go vet ./...`, and `go test ./...`
- Rust fmt, clippy, and tests inside `runtime/container/rust/`
- Dead-code check (`scripts/check-dead-code.sh`)
- Public repo hygiene check (`scripts/check-public-repo-hygiene.sh`)
- Requirements coverage and operator-contract verification

For fuller repo validation without the entire pre-merge stack, use:

```bash
./scripts/build-and-test.sh
./scripts/build-and-test.sh --docker
```

The default path is host-native. `--docker` reruns repo validation inside the
validator container from a disposable snapshot of the current worktree.

### Full local gate

`./scripts/pre-merge.sh` is the normal local parity entrypoint. It supports
three explicit profiles:

- `repo-core`: repo-required deterministic checks
- `pr-parity` (default): the mirrored local subset of required `main`-based PR
  workflows plus parity evidence generation for publication
- `release-preflight`: `pr-parity` plus the extra mirrored release-facing
  hygiene lanes

The default `pr-parity` profile covers the shared mirrored workflow bodies:

- workflow lint and workflow-lane manifest verification
- PR shape
- validator-backed shared validate job
- docs parity
- container smoke
- runtime-image reproducibility on locally supported platforms

`release-preflight` adds the mirrored pin-hygiene lane. `repo-core` keeps the
smaller deterministic subset for repo-owned contract work.

Helpful flags:

```bash
./scripts/pre-merge.sh --allow-dirty
./scripts/pre-merge.sh --profile repo-core
./scripts/pre-merge.sh --profile release-preflight
./scripts/pre-merge.sh --profile repo-core --skip-repro
./scripts/pre-merge.sh --profile release-preflight --skip-release-bundle
./scripts/pre-merge.sh --rebuild-validator
```

`--skip-repro` is diagnostic-only for the non-publication profiles;
publication-grade `pr-parity` rejects it so emitted evidence cannot certify a
selected but unexecuted reproducibility lane.

For `main`-based PRs, `./scripts/repo-publish-pr.sh` consumes the fresh
`pr-parity` evidence emitted by `./scripts/pre-merge.sh` and refuses to publish
if that evidence does not match the tree being sent for review.

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
porting checklist and
[docs/extending-adapters.md](docs/extending-adapters.md) for worked examples
(adding a credential type, extending an adapter) annotated with the invariants
each step touches.

## Package naming

For new internal Go packages, name by role rather than suffix:
`canonicalpath`, `secretfile`, `transcript`, `injection`. Avoid the
`*util`, `*helper`, `*manager`, `*handler` suffixes that the
[Go style guide](https://go.dev/wiki/CodeReviewComments#package-names)
flags as opaque — they hide what the package is for and become magnets
for unrelated helpers over time.

The repo carries some pre-existing `*util` packages (`pathutil`,
`metadatautil`, `runtimeutil`, `colimautil`) and corresponding binary
names (`cmd/workcell-hostutil`, `cmd/workcell-runtimeutil`,
`cmd/workcell-citools`, `cmd/workcell-colimautil`). These are not
retargeted for rename in place — the binary names appear in
`policy/host-support-matrix.tsv`, audit logs, install paths, and PR
review surfaces. The rule applies to new packages only.

## Project docs

- [GOVERNANCE.md](GOVERNANCE.md)
- [MAINTAINERS.md](MAINTAINERS.md)
- [ROADMAP.md](ROADMAP.md)
- [SUPPORT.md](SUPPORT.md)
- [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md)
- [CITATION.cff](CITATION.cff)
