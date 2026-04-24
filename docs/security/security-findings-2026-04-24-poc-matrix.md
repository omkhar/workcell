# Security Findings PoC Matrix - 2026-04-24

| PoC or regression | Type | Findings | Positive control | Negative control |
| --- | --- | --- | --- | --- |
| `test-publish-pr-dry-run.sh` poisoned PATH wrapper check | Runtime regression | F1 | Fake `bash`, `dirname`, `git`, and `jq` are first in `PATH`. | Wrapper still produces the expected dry-run plan and the poison marker is not created. |
| `TestSafePullRequestTargetWorkflowRejectsInlineJobLevelPermissions` | Unit regression | F2 | Inline `permissions: { contents: write }` under a job. | Existing trusted metadata-only workflow remains accepted. |
| `TestSafePullRequestTargetWorkflowRejectsInlineReusableWorkflowCalls` | Unit regression | F2 | Inline job-level `uses`. | Existing block-style rejection tests and accepted workflow still pass. |
| `TestSafePullRequestTargetWorkflowRejectsInlineStepUses` | Unit regression | F2 | Inline step-level external action. | Existing allowed `run`-only workflow still passes. |
| `TestSafePullRequestTargetWorkflowRejectsInlineCheckout` | Unit regression | F2 | Inline `actions/checkout` step. | Existing checkout rejection remains specific. |
| `TestRunCodexHomeResolverIgnoresLegacyTestOverrideEnv` | Unit regression | F3 | Legacy `WORKCELL_TEST_CODEX_AUTH_FILE` points at a readable secret. | Empty synthetic `HOME` produces `configured-only` placeholder, not attacker file materialization. |
| `TestWorkcellRejectsHarnessOnlyCredentialResolverEnvBeforeBundlePreparation` | Static boundary regression | F3 | Caller-controlled harness-only resolver env appears in launcher source. | Internal self-staging probe may still synthesize resolver inputs without forwarding caller env. |
| `test-codex-resolver-launcher.sh` self-staging probe | Runtime regression | F3 | Launcher proves Codex resolver staging with an internal synthetic `HOME`. | No external `WORKCELL_TEST_CODEX_AUTH_FILE` is required or forwarded. |
| `TestFakeTargetRejectsPathTraversalIdentifiers` | Unit regression | F4 | Target and materialization IDs contain `../`. | Valid fake target materialization tests still pass. |
| `TestFakeTargetRejectsPathTraversalProvider` | Unit regression | F4 | Contract provider contains `../`. | Provider-specific AWS state-root test still passes. |
| `TestFakeTargetRejectsPathTraversalSessionID` | Unit regression | F4 | Session ID contains `../`. | Valid materialize/bootstrap setup still succeeds before rejected session start. |
