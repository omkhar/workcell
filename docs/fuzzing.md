# Fuzzing

Workcell fuzzes parsers and classifiers that process repository or
attacker-controlled input. A target must not panic. A target must preserve its
harness invariant.

## Where the targets live

### Go targets

| Package | Target | Surface |
|---|---|---|
| `internal/metadatautil` | `FuzzExtractWorkflowUses` | GitHub Actions `uses` values |
| `internal/metadatautil` | `FuzzParseToolPins` | Tool-pin TOML |
| `internal/metadatautil` | `FuzzValidateControlPlaneManifest` | Control-plane manifest JSON |
| `internal/tomlsubset` | `FuzzParse` | Policy TOML subset |
| `internal/injection` | `FuzzIsAllowedSystemSymlink` | Direct-mount source chains |
| `internal/injection` | `FuzzParseSSHDirective` | SSH configuration directives |

The seed corpus includes repository configuration and invalid forms. The
`go test ./...` pull-request lane replays each saved seed.

### Rust exec-guard classifiers (cargo-fuzz)

The Rust targets use `cargo-fuzz` and libFuzzer.

| Target | Surface |
|---|---|
| `path_classification` | Protected runtime paths and dynamic loaders |
| `env_filtering` | Path lookup and unsafe Git environment values |
| `git_config_parsing` | Git configuration keys and values |

The targets are in `runtime/container/rust/fuzz/fuzz_targets/`. Their seed data
is in `runtime/container/rust/fuzz/corpus/`.

The release library does not export the private classifiers. Fuzz builds enable
`fuzz_api` through `cfg(fuzzing)`.

## Running a target locally

Run the Go seed tests:

```sh
go test ./internal/metadatautil/ ./internal/tomlsubset/ ./internal/injection/
```

Run one target for one minute:

```sh
go test ./internal/metadatautil/ -run '^$' \
  -fuzz='^FuzzParseToolPins$' -fuzztime=1m
```

### Rust targets

Install the pinned tools:

```sh
rustup toolchain install nightly-2026-07-02
cargo install cargo-fuzz --version 0.13.2 --locked
```

The release crate uses vendored sources. The fuzz crate also needs
`libfuzzer-sys` from crates.io.

For a local fuzz build, temporarily remove the `replace-with =
"vendored-sources"` line from `runtime/container/rust/.cargo/config.toml`.
Do not commit that change.

Then run these commands from `runtime/container/rust/`:

```sh
cargo +nightly-2026-07-02 fuzz build
cargo +nightly-2026-07-02 fuzz run path_classification -- \
  -max_total_time=25
```

## Scheduled lane

`.github/workflows/fuzz.yml` runs each target each week and on demand.

The Go job uses the pinned validator image. The Rust job installs the pinned
nightly toolchain and `cargo-fuzz` version.

The workflow is not a pull request gate. The `go test ./...` pull-request lane
replays the saved Go seed corpus. The scheduled or on-demand Rust job replays
the saved Rust corpus.

Workflow lane policy and retention policy record both jobs and their crash
artifacts.

## Crash triage

### Go

For a Go crash:

1. Download the `fuzz-reproducers` artifact.
2. Put the input in `PACKAGE/testdata/fuzz/TARGET/HASH`.
3. Reproduce it with `go test ./PACKAGE -run='^TARGET$/HASH$'`.
4. Fix the parser or classifier so the harness invariant holds.
5. Commit the input as a regression seed.
6. Run the target again.

### Rust

For a Rust crash:

1. Download the `rust-fuzz-reproducers` artifact.
2. Put the input in `fuzz/artifacts/TARGET/`.
3. Replay it with `cargo +nightly-2026-07-02 fuzz run TARGET PATH`.
4. Minimize it with `cargo +nightly-2026-07-02 fuzz tmin TARGET PATH`.
5. Fix the classifier or the harness.
6. Put the minimized input in `fuzz/corpus/TARGET/`.
7. Run the target again.
