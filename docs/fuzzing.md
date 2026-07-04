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

## Scheduled lane

`.github/workflows/fuzz.yml` runs weekly and on demand. It gives each target a
few minutes of fuzzing, exercising every target above. The lane runs the targets
inside the validator image (via `scripts/ci/job-fuzz.sh` →
`scripts/ci/run-fuzz-in-validator.sh`) so it uses the reviewed, pinned Go
toolchain rather than the runner's ambient Go. The lane is not on the PR path —
the seed corpus already gates PRs through the normal `go test` lanes — so it
stays a scheduled, heavy sweep. It is registered in
`policy/workflow-lane-policy.json` and reflected in `policy/workflow-lanes.json`.

## Crash triage

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
