# Use Case Coverage Matrix

This matrix maps providers, personas, and use cases to their current test
coverage status.

Test status values:

- `tested` — automated coverage exists (scenario test, unit test, or
  invariant check)
- `doc-only` — documented and manually verified, but no automated scenario
  test yet
- `aspirational` — identified as a target scenario, not yet tested or
  documented in detail

Some `tested` rows are static contract checks rather than end-to-end runtime
scenarios. The `Coverage Source` column shows where the current automated
coverage lives.

## Matrix

| Provider | Persona | Use Case | Test Status | Coverage Source |
|---|---|---|---|---|
| Codex | SWE | `--inspect` contract retains required keys | tested | `scripts/verify-invariants.sh` |
| Codex | SWE | audit record contract retains required fields | tested | `scripts/verify-invariants.sh` |
| Codex | SWE | launch with codex_auth injection policy credential | tested | `scripts/container-smoke.sh` |
| Codex | SWE | session-local Codex rules mutability does not affect adapter baseline | tested | `scripts/container-smoke.sh`, `scripts/verify-invariants.sh` |
| Codex | Developer | first-run --prepare flow seeds runtime image | doc-only | — |
| Codex | Developer | doctor reports missing workspace before suggesting --prepare | tested | `scripts/verify-invariants.sh` |
| Codex | Developer | --injection-policy default path auto-picked from ~/.config/workcell | doc-only | — |
| Codex | DevRel | publish-pr creates signed commit and draft PR on host | tested | `tests/scenarios/shared/test-publish-pr-dry-run.sh`, `scripts/verify-invariants.sh` |
| Codex | DevRel | auth-status prints primary auth mode from injected codex_auth | tested | `tests/scenarios/shared/test-auth-status.sh`, `scripts/verify-invariants.sh` |
| Codex | PM | yolo autonomy mode launches with --ask-for-approval never | tested | `scripts/container-smoke.sh` |
| Codex | PM | prompt autonomy mode marked lower-assurance in audit output | tested | `scripts/verify-invariants.sh` |
| Codex | NaiveCoder | nested coding-agent CLI launch blocked by provider-policy.sh | doc-only | — |
| Codex | NaiveCoder | git hook-bypass flags rejected on Tier 1 path | doc-only | — |
| Codex | NaiveCoder | direct push to main blocked | doc-only | — |
| Codex | NaiveCoder | host home not reachable from inside session | doc-only | — |
| Claude | SWE | guard-bash.sh blocks unsafe git commands | tested | `tests/scenarios/claude-swe/test-hook-parametric.sh` |
| Claude | SWE | `--inspect` contract retains required keys | tested | `scripts/verify-invariants.sh` |
| Claude | SWE | audit record contract retains required fields | tested | `scripts/verify-invariants.sh` |
| Claude | SWE | managed-settings.json is not replaceable by workspace files | doc-only | — |
| Claude | Developer | claude_api_key generates apiKeyHelper without second plaintext copy | tested | `scripts/container-smoke.sh` |
| Claude | Developer | claude_mcp injects reviewed MCP config replacing empty template | tested | `scripts/container-smoke.sh` |
| Claude | Developer | enableAllProjectMcpServers remains false after workspace launch | tested | `scripts/container-smoke.sh`, `scripts/verify-invariants.sh` |
| Claude | DevRel | publish-pr creates signed commit and draft PR on host | tested | `tests/scenarios/shared/test-publish-pr-dry-run.sh`, `scripts/verify-invariants.sh` |
| Claude | DevRel | org CLAUDE.md overlay appended via documents.claude in policy | doc-only | — |
| Claude | PM | yolo autonomy mode launches with --permission-mode bypassPermissions | tested | `scripts/container-smoke.sh` |
| Claude | PM | prompt autonomy mode marked lower-assurance in audit output | aspirational | — |
| Claude | NaiveCoder | nested claude subprocess with unsafe flags blocked by guard-bash.sh | tested | `tests/scenarios/claude-swe/test-hook-parametric.sh` |
| Claude | NaiveCoder | rm -rf blocked by guard-bash.sh | tested | `tests/scenarios/claude-swe/test-hook-parametric.sh` |
| Claude | NaiveCoder | shell expansion syntax blocked by guard-bash.sh | tested | `tests/scenarios/claude-swe/test-hook-parametric.sh` |
| Claude | NaiveCoder | workspace control file reads blocked by guard-bash.sh | tested | `tests/scenarios/claude-swe/test-hook-parametric.sh` |
| Gemini | SWE | auth modes correctly parsed from `gemini_env` injection | tested | `tests/python/test_injection_helpers.py` |
| Gemini | SWE | `--inspect` contract retains required keys | tested | `scripts/verify-invariants.sh` |
| Gemini | SWE | audit record contract retains required fields | tested | `scripts/verify-invariants.sh` |
| Gemini | SWE | trustedFolders.json seeded with /workspace before launch | tested | `scripts/container-smoke.sh`, `scripts/verify-invariants.sh` |
| Gemini | Developer | Vertex env derives regional aiplatform allowlist entry | doc-only | — |
| Gemini | Developer | gcloud_adc supplemental only, not standalone Gemini auth | tested | `tests/scenarios/shared/test-auth-status.sh`, `scripts/verify-invariants.sh` |
| Gemini | Developer | gemini_oauth credential copied to ~/.gemini/oauth_creds.json | doc-only | — |
| Gemini | DevRel | publish-pr creates signed commit and draft PR on host | tested | `tests/scenarios/shared/test-publish-pr-dry-run.sh`, `scripts/verify-invariants.sh` |
| Gemini | DevRel | gemini_projects credential seeded into ~/.gemini/projects.json | tested | `scripts/container-smoke.sh` |
| Gemini | PM | yolo autonomy mode launches with --approval-mode yolo | tested | `scripts/container-smoke.sh` |
| Gemini | PM | prompt autonomy mode marked lower-assurance in audit output | aspirational | — |
| Gemini | NaiveCoder | workspace .gemini/ masked on safe path | doc-only | — |
| Gemini | NaiveCoder | nested coding-agent CLI launch blocked | doc-only | — |
| Gemini | NaiveCoder | breakglass restores Gemini folder-trust prompt, omits trustedFolders seed | doc-only | — |
| Gemini | NaiveCoder | host home not reachable from inside session | doc-only | — |

## Notes on coverage sources

Scenario tests are declared in `tests/scenarios/manifest.json`, including the
lane and platform metadata that determines whether they run in the default
secretless path. Their scripts live under `tests/scenarios/`. Static
invariant checks live in `scripts/verify-invariants.sh`, container/runtime
coverage lives in `scripts/container-smoke.sh`, and unit tests live under
`tests/python/`.

For gaps and aspirational scenarios, see `docs/scenario-gaps.md`.
