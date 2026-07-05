# Fuzzing

Workcell fuzzes the hand-rolled parsers that sit on attacker- or
contributor-influenceable input. Each target asserts a single invariant: for any
input, the parser must not panic. An error return for malformed input is the
expected, correct outcome; only a crash is a finding.

## Where the targets live

Fuzz targets are ordinary Go `Fuzz*` functions in `_test.go` files, each in the
same package as the parser it exercises:

- `internal/metadatautil/fuzz_test.go`
  - `FuzzExtractWorkflowUses` — the workflow-YAML `uses:` extractor that feeds
    the default-deny GitHub Actions allowlist scan.
  - `FuzzParseToolPins` — the `[tool_pins]` TOML parser behind
    `policy/tool-pins.toml`.
  - `FuzzValidateControlPlaneManifest` — the control-plane manifest JSON
    validator.
- `internal/tomlsubset/fuzz_test.go`
  - `FuzzParse` — the TOML-subset parser that gates auth and injection policy.
- `internal/injection/fuzz_test.go`
  - `FuzzIsAllowedSystemSymlink` — the direct-mount source-chain oracle.
  - `FuzzParseSSHDirective` — the SSH config directive parser.

Seed corpora are drawn from real repository configs (actual workflow files, the
real `policy/tool-pins.toml`, and the committed control-plane manifest) plus
enumerated malformed shapes, so the checked-in corpus reflects the inputs these
parsers actually see.

### Rust exec-guard classifiers (cargo-fuzz)

The `workcell_exec_guard` LD_PRELOAD guard (`runtime/container/rust/`) is fuzzed
with [cargo-fuzz]/libFuzzer. Targets live in
`runtime/container/rust/fuzz/fuzz_targets/`, one per A3 parser surface:

- `path_classification` — `classify_protected_runtime_path`,
  `path_points_to_dynamic_loader`, and `classify_loader_target` (the path
  validation / dynamic-loader classification surface).
- `env_filtering` — `path_from_env_entries`, `env_has_unsafe_git_override`, and
  `resolve_command_via_path_value` (the environment-filtering surface).
- `git_config_parsing` — `git_config_spec_is_blocked`,
  `git_config_key_is_blocked`, and `git_config_spec_value_is_explicit_safe` (the
  git-config spec parsing surface).

These classifiers are private in the shipped `cdylib`. They are exposed to the
fuzz targets only through the `fuzz_api` module in `src/lib.rs`, which is gated
`#[cfg(fuzzing)]` — cargo-fuzz sets `--cfg fuzzing`, so the widened surface never
exists in the normal `cargo build`/`cargo test` or the released library. The
crate adds `rlib` to `crate-type` purely so cargo-fuzz can link the lib; the
`cdylib` artifact is unchanged. Seed corpora
(`runtime/container/rust/fuzz/corpus/<target>/`) are real loader paths, environ
entries, and git-config specs taken from the guard's own constants.

[cargo-fuzz]: https://github.com/rust-fuzz/cargo-fuzz

## Running a target locally

The seed corpus runs as a normal regression test on every PR:

```sh
go test ./internal/metadatautil/ ./internal/tomlsubset/ ./internal/injection/
```

To spend a bounded budget mutating the seeds for one target, name the target and
its package:

```sh
go test ./internal/metadatautil/ -run '^$' -fuzz='^FuzzParseToolPins$' -fuzztime=1m
```

`-fuzz` runs exactly one target in one package at a time; `-run '^$'` skips the
ordinary unit tests so only the fuzzer runs.

### Rust targets

The Rust targets need the nightly toolchain (libFuzzer's sanitizer/coverage
codegen) and `cargo-fuzz`:

```sh
rustup toolchain install nightly
cargo install cargo-fuzz --version 0.13.2 --locked
```

The exec-guard crate pins crates.io to a vendored directory for its reproducible
shipping build (`runtime/container/rust/.cargo/config.toml`), and the
non-shipping fuzz crate's extra dependency (`libfuzzer-sys`) is not vendored. A
local fuzz build therefore needs crates.io access for that one dependency, so
`cargo +nightly fuzz build` will fail dependency resolution against the vendored
config unless you first override it. Apply the same override the scheduled lane
uses (see the `Rust fuzz` job in [`.github/workflows/fuzz.yml`](../.github/workflows/fuzz.yml)):
in `runtime/container/rust/.cargo/config.toml`, temporarily **remove** (or comment
out) the `replace-with = "vendored-sources"` line so `crates.io` resolves from its
built-in default registry. Do not add a second source pointed at the crates.io
index — Cargo rejects that as a duplicate of the built-in `crates-io` source. Do
this locally only and **do not commit it** — the committed vendored config is what
release builds use.

Then, from `runtime/container/rust/`, build all targets or run one on its seed
corpus for a bounded budget:

```sh
cargo +nightly fuzz build
cargo +nightly fuzz run path_classification -- -max_total_time=25
```

## Scheduled lane

`.github/workflows/fuzz.yml` runs weekly and on demand. It gives each target a
few minutes of fuzzing, exercising every target above. The `Go fuzz` job runs
the Go targets inside the validator image (via `scripts/ci/job-fuzz.sh` →
`scripts/ci/run-fuzz-in-validator.sh`) so it uses the reviewed, pinned Go
toolchain rather than the runner's ambient Go. The `Rust fuzz` job installs the
nightly toolchain plus `cargo-fuzz` and runs each Rust target for a bounded
`-max_total_time` budget. Neither job is on the PR path — the Go seed corpus
already gates PRs through the normal `go test` lanes, and the Rust seed corpora
are checked in — so the lane stays a scheduled, heavy sweep. Both jobs are
registered in `policy/workflow-lane-policy.json` and reflected in
`policy/workflow-lanes.json`; their crash-artifact retention is in
`policy/retention-policy.json`.

## Crash triage

### Go

When the fuzzer finds a crash it writes a reproducer file at
`testdata/fuzz/<Target>/<hash>` next to the target's package and fails the run.
On the scheduled lane the failing job uploads those files as the
`fuzz-reproducers` artifact before the runner workspace is discarded, so the
exact input survives the run.
To triage:

1. Retrieve the reproducer — from the failing run's `fuzz-reproducers` artifact
   for a scheduled-lane crash, or from your working tree for a local one — and
   commit it. It becomes a permanent regression seed and, once fixed, guards
   against the crash returning.
2. Reproduce it directly: `go test ./<package>/ -run='^<Target>$/<hash>$'`.
3. Fix the parser so the input returns an error instead of panicking.
4. Re-run the target with `-fuzz` to confirm the crash is gone and no new one
   appears.

### Rust

When a Rust target crashes, libFuzzer prints the panic and writes the minimized
failing input under `runtime/container/rust/fuzz/artifacts/<target>/`. The
scheduled `Rust fuzz` job uploads that directory as the `rust-fuzz-reproducers`
artifact on failure, so the exact input survives the run.
To triage, from `runtime/container/rust/`:

1. Retrieve the reproducer — from the failing run's `rust-fuzz-reproducers`
   artifact for a scheduled-lane crash, or from `fuzz/artifacts/<target>/` for a
   local one.
2. Reproduce it directly by replaying the single input:
   `cargo +nightly fuzz run <target> fuzz/artifacts/<target>/<crash-file>`.
3. Optionally minimize it further: `cargo +nightly fuzz tmin <target>
   fuzz/artifacts/<target>/<crash-file>`.
4. Decide whether the crash is a real classifier bug (fix `src/lib.rs` so the
   input is handled instead of panicking) or an over-aggressive target (fix the
   harness). Add the minimized input to `fuzz/corpus/<target>/` as a permanent
   regression seed.
5. Re-run the target (`cargo +nightly fuzz run <target> -- -max_total_time=60`)
   to confirm the crash is gone and no new one appears.
