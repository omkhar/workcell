# Security Findings Mutation Results - 2026-04-24

Scope: sink-level mutations for the four validated security findings. This is not repo-wide mutation coverage.

## Results

| Mutant | Finding | Command | Result |
| --- | --- | --- | --- |
| Disable YAML AST job-permissions rejection | F2 | `go test ./internal/metadatautil -run TestSafePullRequestTargetWorkflowRejectsInlineJobLevelPermissions -count=1` | killed |
| Reintroduce `WORKCELL_TEST_CODEX_AUTH_FILE` override in Codex resolver | F3 | `go test ./internal/authresolve -run TestRunCodexHomeResolverIgnoresLegacyTestOverrideEnv -count=1` | killed |
| Disable path-separator rejection in `statePathSegment` | F4 | `go test ./internal/remotevm -run TestFakeTargetRejectsPathTraversal -count=1` | killed |
| Restore pre-hardened `repo-publish-pr.sh` entrypoint | F1 | `bash ./tests/scenarios/shared/test-publish-pr-dry-run.sh` | killed |

Mutation score: 4 / 4 targeted mutants killed.

The full repository validation also ran the existing Workcell mutation suite through `bash ./scripts/validate-repo.sh`; it completed successfully as part of the final validation gate.
