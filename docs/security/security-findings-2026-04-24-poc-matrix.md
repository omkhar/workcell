# PoC Matrix for Security Findings, 2026-04-24

| Test | Type | Finding | Positive control | Negative control |
|---|---|---|---|---|
| `test-publish-pr-dry-run.sh` poisoned-path check | Runtime | F1 | Fake Bash, dirname, Git, and jq tools occur first in `PATH`. | The dry-run plan is correct, and no poison marker exists. |
| `TestSafePullRequestTargetWorkflowRejectsInlineJobLevelPermissions` | Unit | F2 | A job has inline write permissions. | Trusted metadata-only workflow remains valid. |
| `TestSafePullRequestTargetWorkflowRejectsInlineReusableWorkflowCalls` | Unit | F2 | A job has inline reusable-workflow `uses`. | Block-form checks and the valid workflow pass. |
| `TestSafePullRequestTargetWorkflowRejectsInlineStepUses` | Unit | F2 | A step has inline external-action `uses`. | A run-only workflow passes. |
| `TestSafePullRequestTargetWorkflowRejectsInlineCheckout` | Unit | F2 | A step has inline checkout. | The specific checkout rejection and valid workflow pass. |
| `TestRunCodexHomeResolverIgnoresLegacyTestOverrideEnv` | Unit | F3 | The legacy variable points to a readable secret. | An empty synthetic home produces a placeholder, not the attacker file. |
| `TestWorkcellRejectsHarnessOnlyCredentialResolverEnvBeforeBundlePreparation` | Static | F3 | A caller-controlled harness value occurs in launcher source. | An internal probe can create its own synthetic input. |
| `test-codex-resolver-launcher.sh` | Runtime | F3 | The launcher stages Codex auth from an internal synthetic home. | It does not need or forward the legacy variable. |
| `TestFakeTargetRejectsPathTraversalIdentifiers` | Unit | F4 | Target and materialization identifiers contain `../`. | Valid materialization tests pass. |
| `TestFakeTargetRejectsPathTraversalProvider` | Unit | F4 | The provider contains `../`. | Valid AWS state-root tests pass. |
| `TestFakeTargetRejectsPathTraversalSessionID` | Unit | F4 | The session identifier contains `../`. | Valid setup completes before the invalid session start stops. |
