# Python To Go Migration Workflow

Use this workflow when porting repo-owned Python helpers to Go.

## Goal

Replace Python helper implementations without weakening the Workcell runtime
boundary, broadening parser behavior, or creating compatibility drift at the
shell/runtime boundary.

## Design order

Optimize in this order:

1. Compatibility with current Python behavior
2. Simplicity
3. Security invariant preservation
4. Performance
5. Idiomatic correctness

That order applies only after the current boundary and assurance model stay
intact.

## Dependencies

Default to the Go standard library. Any non-stdlib dependency needs an explicit
written rationale that explains why it is unavoidable, why a small local
implementation is worse, and why the added supply-chain surface is justified.

## Migration sequence

### 1. Freeze the legacy baseline first

Before porting anything, capture a passing baseline for:

- `./scripts/verify-coverage.sh`
- `./scripts/run-mutation-tests.sh`
- `./scripts/run-scenario-tests.sh --secretless-only`
- `./scripts/verify-scenario-coverage.sh`
- `./scripts/verify-control-plane-parity.sh`

### 2. Keep the shell contract stable

Do not churn shell call sites more than once. Prefer stable Go binaries under
`cmd/` plus extensionless wrappers when cutover begins. Keep Python as the
oracle until parity is proven.

### 3. Port leaf tools before the policy cluster

Port in this order:

1. `scenario_manifest`
2. `extract_direct_mounts`
3. `pty_transcript`
4. shared policy library replacing `policy_bundle.py`
5. `resolve_credential_sources`
6. `manage_injection_policy`
7. `render_injection_bundle`

The policy and rendering cluster should move only after the shared parser and
validation behavior exist in one Go library.

### 4. Add parity gates before cutover

For each helper, run Python and Go against the same fixtures and compare:

- exit code
- stdout/stderr
- rewritten JSON or TOML
- output file layout
- file contents
- file modes

Normalize only fields that are inherently unstable, such as PTY transcript
timestamps.

### 5. Cut over call sites in batches

After parity is green for a helper, switch all direct consumers of that helper
in one change. Keep control-plane manifests, invariant checks, and validation
scripts in sync with the new entrypoints.

### 6. Replace validation plumbing after helper parity

Only after the helper ports are stable should the repo remove:

- Python mutation coverage for helper paths
- residual helper-specific host-language invocations in shell validation

### 7. Treat inline shell Python as a second migration wave

Porting the helper entrypoints does not remove the remaining host-side utility
logic embedded in shell scripts. Handle that second wave by domain rather than
file-by-file.

## Local validation matrix

- Functional: differential Python-vs-Go fixture tests plus existing unit,
  invariant, scenario, and smoke coverage.
- Non-functional: startup time, max RSS, binary size, and output tree size on
  fixed local fixtures.
- Avoid hard performance thresholds for Docker, Colima, networked auth, or
  TTY-scheduling-heavy paths.

## Review questions

Before merging any migration slice, ask:

1. Did this preserve current Python behavior at the CLI and filesystem level?
2. Did this keep the runtime boundary and control-plane masking rules intact?
3. Did this reduce future cutover work, or only move code around?
4. Are validation and invariant checks still exercising the migrated path?
5. Is any remaining Python dependency explicit and temporary?
