# Security Finding Validation, 2026-04-24

Repository: `omkhar/workcell`

Validation branch: `codex/security-findings-remediation`

Baseline commit: `e0268f7711ad3cb61102eb313c2f5b3715188a39`

Scope: Four Codex scanner findings from the 2026-04-24 CSV export, validated
against the PR 157 head.

## Result

All four findings were genuine. The [signed remediation commit](https://github.com/omkhar/workcell/commit/860e21f6dae310aafb14956c33fdc462faa5ee72) later merged to `main`.

| ID | Component | Original severity | Result | Retriaged severity |
|---|---|---|---|---|
| F1 | `scripts/repo-publish-pr.sh` | High | Fixed | High |
| F2 | `internal/metadatautil/validate.go` | Medium | Fixed | Medium |
| F3 | `internal/authresolve`, `scripts/workcell` | Medium | Fixed | High |
| F4 | `internal/remotevm/fake_target.go` | Informational | Fixed | Low |

## F1: Publication Tool Resolution

The publication wrapper used Git and jq from a caller-controlled path before it
checked `pr-parity` evidence. This was a host code-execution and publication
integrity risk.

The fix gave the wrapper a sanitized trusted entry point and trusted absolute
Git and jq paths. The regression test puts fake Bash, dirname, Git, and jq tools
first in `PATH`. The wrapper creates the correct dry-run plan, and no fake tool
runs.

## F2: `pull_request_target` YAML Bypass

Line-pattern checks accepted inline YAML that hid job permissions, reusable
workflow calls, or external actions.

The fix parses the YAML AST. It rejects non-empty top-level permissions,
job-level permissions, reusable workflow calls, action steps, and checkout in
block or flow form. Unit tests cover each inline form.

## F3: Codex Resolver Test Variable

The launcher forwarded `WORKCELL_TEST_CODEX_AUTH_FILE`. The resolver could use
that value instead of the fixed `~/.codex/auth.json` path. A caller could direct
the resolver to another readable file.

The fix removed the resolver support for that variable. The launcher rejects
inherited harness-only resolver values before bundle preparation. Tests use a
synthetic home or an internal probe that stages data.

Regression tests prove that the resolver ignores the legacy value, the launcher
rejects it, and neither component forwards it.

## F4: Fake Remote Target Path Traversal

The fake remote target joined target and session identifiers to state paths
without a complete segment check. Public AWS target identifiers had a separate
check, but the reusable fake target did not.

The fix validates provider, target, materialization, and session identifiers as
single path segments before a state-root join. Unit tests reject traversal in
each identifier class.

## Validation

The remediation passed focused authentication, metadata, remote-target, and
test-kit Go tests. It also passed resolver, publication, manifest, operator
contract, requirements, dead-code, repository-hygiene, and full repository
validation.

See the [PoC matrix](security-findings-2026-04-24-poc-matrix.md) and
[mutation results](security-findings-2026-04-24-mutation-results.md) for the
replayable evidence.
