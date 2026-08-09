# Mutation Results for Security Findings, 2026-04-24

This test covered one sink mutation for each validated finding. It was not a
repository-wide mutation test.

| Mutant | Finding | Command | Result |
|---|---|---|---|
| Disable the YAML AST check for job permissions | F2 | `go test ./internal/metadatautil -run TestSafePullRequestTargetWorkflowRejectsInlineJobLevelPermissions -count=1` | Killed |
| Restore the legacy Codex resolver environment override | F3 | `go test ./internal/authresolve -run TestRunCodexHomeResolverIgnoresLegacyTestOverrideEnv -count=1` | Killed |
| Disable the path-separator check in `statePathSegment` | F4 | `go test ./internal/remotevm -run TestFakeTargetRejectsPathTraversal -count=1` | Killed |
| Restore the publication wrapper before hardening | F1 | `bash ./tests/scenarios/shared/test-publish-pr-dry-run.sh` | Killed |

The targeted mutation result was 4 of 4 killed mutants. Full repository
validation also ran the current Workcell mutation suite and passed.
