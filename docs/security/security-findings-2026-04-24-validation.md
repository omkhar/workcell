# Security Findings Validation - 2026-04-24

Repository: `omkhar/workcell`
Validation branch: `codex/security-findings-remediation`
Baseline commit: `e0268f7711ad3cb61102eb313c2f5b3715188a39`
Patch state: main-based signed PR patch.
Scope: four Codex scanner findings from the 2026-04-24 CSV export, validated against the PR #157 head.

## Summary

All four reported findings were genuine in the validated branch. The fixes are prepared as a separate security follow-up after PR #157 and should not be merged until #157 is on `main`.

| ID | Component | Original severity | Result | Retriaged severity |
| --- | --- | --- | --- | --- |
| F1 | `scripts/repo-publish-pr.sh` | high | fixed | high |
| F2 | `internal/metadatautil/validate.go` | medium | fixed | medium |
| F3 | `internal/authresolve`, `scripts/workcell` | medium | fixed | high |
| F4 | `internal/remotevm/fake_target.go` | informational | fixed | low |

## Findings

### F1: Publication wrapper trusted tool resolution

Original claim: the repo-local publication wrapper ran `git` and `jq` through caller-controlled `PATH` before enforcing PR-parity evidence.

Verification result: genuine. The wrapper is host-side publication control-plane code, so hostile interpreter/tool resolution before parity enforcement is a host code execution and publication-integrity risk.

Fix: `scripts/repo-publish-pr.sh` now enters through the sanitized trusted entrypoint and resolves trusted absolute `git` and `jq` paths before parity checks.

Evidence: `tests/scenarios/shared/test-publish-pr-dry-run.sh` includes a poisoned `PATH` with fake `bash`, `dirname`, `git`, and `jq`; the wrapper still publishes a dry-run plan and none of the fake tools execute.

### F2: `pull_request_target` YAML parser bypass

Original claim: line-oriented regex checks accepted inline YAML mappings that could hide job permissions, reusable workflows, or external action use.

Verification result: genuine. Flow-style YAML bypassed the line regex shape while preserving the same workflow semantics.

Fix: `isSafePullRequestTargetWorkflow` now parses the workflow YAML AST and rejects non-empty top-level permissions, job-level permissions, reusable workflow `uses`, step `uses`, and checkout regardless of block or flow style.

Evidence: `internal/metadatautil/workflow_validation_test.go` covers inline job permissions, inline job `uses`, inline step `uses`, and inline checkout.

### F3: Codex resolver harness env disclosure

Original claim: `WORKCELL_TEST_CODEX_AUTH_FILE` could redirect the Codex host-auth resolver to an arbitrary operator-readable file.

Verification result: genuine. The launcher forwarded the test env var into resolver execution, and the resolver honored it as a replacement for the fixed `~/.codex/auth.json` contract.

Fix: the resolver no longer recognizes `WORKCELL_TEST_CODEX_AUTH_FILE`; `scripts/workcell` rejects harness-only resolver env vars before bundle preparation and no longer forwards caller-controlled test resolver env. Tests use synthetic `HOME` fixtures or self-staging probe internals instead.

Evidence: `internal/authresolve` includes a regression test proving the legacy env var is ignored. `internal/testkit` statically checks that the launcher rejects harness-only resolver env before bundle preparation and does not forward caller-controlled values.

### F4: Fake remote VM path traversal

Original claim: fake remote VM target IDs were joined directly into filesystem paths used by materialization removal and session record writes.

Verification result: genuine, currently low severity. Public AWS CLI target IDs are separately constrained, but the internal fake target and conformance harness must be safe before reuse by later remote VM provider code.

Fix: `FakeTarget` validates target provider, target ID, materialization ID, and session ID as single path segments before any state-root path join.

Evidence: `internal/remotevm/fake_target_test.go` rejects traversal in target, provider, materialization, and session identifiers.

## Review Lenses

Applied review lenses: security exploitability, SWE fixability/regression risk, and engineering-management sequencing/scope. The review outcome is that all four findings are genuine and should be remediated. This patch is based on the merged Phase 7 mainline after PR #157. If reviewers want smaller review units, split by commit into publication/workflow hardening, credential resolver boundary, and remote VM confinement.

## Validation

Focused validation:

```sh
go test ./internal/authresolve ./internal/authpolicy ./internal/metadatautil ./internal/remotevm ./internal/testkit
bash ./tests/scenarios/shared/test-codex-resolver-launcher.sh
bash ./tests/scenarios/shared/test-publish-pr-dry-run.sh
bash ./scripts/verify-control-plane-manifest.sh
```

Contract and repo validation:

```sh
bash ./scripts/verify-operator-contract.sh
bash ./scripts/verify-requirements-coverage.sh
bash ./scripts/check-dead-code.sh
bash ./scripts/check-public-repo-hygiene.sh
bash ./scripts/validate-repo.sh
./scripts/workcell --gc
```

Result: all commands passed on the patched worktree.
