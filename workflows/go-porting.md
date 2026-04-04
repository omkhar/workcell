# Go Porting Workflow

This workflow defines the compatibility-first migration path for replacing
repo-owned Python with Go on the experimental branch.

## Scope

Tranche 1 covers the bounded helper surface under `scripts/lib/` plus the
repo-owned Python tests and mutation harness that define current behavior.

Tranche 2 covers the remaining launcher- and invariant-side host utilities that
still need Go-backed replacements. Track those separately so the shared helper
migration does not stall on unrelated one-off host-side probes.

## Dependencies

Prefer the Go standard library by default. Treat every non-stdlib dependency
as a policy exception that needs an explicit written rationale covering:

- why the gap is not practical to solve with stdlib and small local code
- why the dependency improves safety or compatibility more than it increases
  supply-chain surface
- how widely the dependency would spread through the repo

Until a dependency clears that bar, do not add it.

## Team

Use parallel workstreams with explicit ownership:

1. Contract and fixtures
   Capture CLI contracts, file formats, permissions, and error-path behavior.
2. Low-coupling tools
   Port `scenario_manifest`, `extract_direct_mounts`, and `pty_transcript`
   first to prove the Go CLI and parity harness with minimal blast radius.
3. Shared policy core
   Port duplicated parsing, include-merging, path validation, and TOML rendering
   logic into one Go library before touching the highest-coupling helpers.
4. Injection pipeline
   Port `render_injection_bundle` and `resolve_credential_sources` against the
   shared library rather than independently.
5. Host auth management
   Port `manage_injection_policy` only after the shared policy primitives are
   stable, because it depends on the same selector and validation semantics.
6. Verification and benchmarks
   Keep parity gates green, then compare Python and Go implementations for
   runtime, filesystem behavior, and operator-facing output.

## Order Of Operations

1. Freeze the Python baseline with unit, mutation, and coverage checks.
2. Add Go parity tests that compare future Go helpers against the current
   Python outputs on the same fixtures.
3. Port the low-coupling helpers first to validate the Go module layout and
   the parity harness on small surfaces.
4. Port the shared policy and injection cluster as a unit to avoid duplicated
   parsing drift.
5. Replace call sites only after the Go entrypoint has parity coverage and the
   current Python tests pass against the new implementation.
6. Remove Python from validation scripts last, after the helper cutover is
   complete and remaining inline snippets are either ported or explicitly
   deferred.

## Compatibility Gates

Every helper cutover should preserve:

- CLI flags and exit status semantics
- JSON and TOML output shape
- deterministic ordering where current tests rely on it
- file modes for secrets and staged outputs
- path validation and symlink rejection behavior
- mutation-test resistance for security-sensitive branches

## Local Validation

Use the bounded baseline script before and after each porting step:

```bash
./scripts/go-port-validate.sh
```

For non-functional comparison, benchmark only fixture-driven helpers with stable
inputs and compare:

- wall-clock runtime
- peak memory where practical
- bytes written and file mode outcomes

Do not switch shell entrypoints to Go until the compatibility harness proves the
new helper is a drop-in replacement.
