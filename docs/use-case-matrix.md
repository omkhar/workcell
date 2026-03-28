# Use Case Coverage Matrix

This matrix maps providers, personas, and use cases to their current test
coverage status.

Test status values:

- `tested` — a scenario test file exists and runs in CI or the local
  secretless lane
- `doc-only` — documented and manually verified, but no automated scenario
  test yet
- `aspirational` — identified as a target scenario, not yet tested or
  documented in detail

## Matrix

| Provider | Persona | Use Case | Test Status | Test File |
|---|---|---|---|---|
| Codex | SWE | inspect output schema contains required keys | tested | `tests/scenarios/cross-provider/test-inspect-parity.sh` |
| Codex | SWE | audit log entries contain required fields | tested | `tests/scenarios/cross-provider/test-audit-log-schema.sh` |
| Codex | SWE | launch with codex_auth injection policy credential | doc-only | — |
| Codex | SWE | session-local Codex rules mutability does not affect adapter baseline | doc-only | — |
| Codex | Developer | first-run --prepare flow seeds runtime image | doc-only | — |
| Codex | Developer | doctor reports missing workspace before suggesting --prepare | doc-only | — |
| Codex | Developer | --injection-policy default path auto-picked from ~/.config/workcell | doc-only | — |
| Codex | DevRel | publish-pr creates signed commit and draft PR on host | doc-only | — |
| Codex | DevRel | auth-status prints primary auth mode from injected codex_auth | doc-only | — |
| Codex | PM | yolo autonomy mode launches with --ask-for-approval never | doc-only | — |
| Codex | PM | prompt autonomy mode marked lower-assurance in audit output | doc-only | — |
| Codex | NaiveCoder | nested coding-agent CLI launch blocked by provider-policy.sh | doc-only | — |
| Codex | NaiveCoder | git hook-bypass flags rejected on Tier 1 path | doc-only | — |
| Codex | NaiveCoder | direct push to main blocked | doc-only | — |
| Codex | NaiveCoder | host home not reachable from inside session | doc-only | — |
| Claude | SWE | guard-bash.sh blocks unsafe git commands | tested | `tests/scenarios/claude-swe/test-hook-parametric.sh` |
| Claude | SWE | inspect output schema contains required keys | tested | `tests/scenarios/cross-provider/test-inspect-parity.sh` |
| Claude | SWE | audit log entries contain required fields | tested | `tests/scenarios/cross-provider/test-audit-log-schema.sh` |
| Claude | SWE | managed-settings.json is not replaceable by workspace files | doc-only | — |
| Claude | Developer | claude_api_key generates apiKeyHelper without second plaintext copy | doc-only | — |
| Claude | Developer | claude_mcp injects reviewed MCP config replacing empty template | doc-only | — |
| Claude | Developer | enableAllProjectMcpServers remains false after workspace launch | doc-only | — |
| Claude | DevRel | publish-pr creates signed commit and draft PR on host | doc-only | — |
| Claude | DevRel | org CLAUDE.md overlay appended via documents.claude in policy | doc-only | — |
| Claude | PM | yolo autonomy mode launches with --permission-mode bypassPermissions | doc-only | — |
| Claude | PM | prompt autonomy mode marked lower-assurance in audit output | aspirational | — |
| Claude | NaiveCoder | nested claude subprocess with unsafe flags blocked by guard-bash.sh | tested | `tests/scenarios/claude-swe/test-hook-parametric.sh` |
| Claude | NaiveCoder | rm -rf blocked by guard-bash.sh | tested | `tests/scenarios/claude-swe/test-hook-parametric.sh` |
| Claude | NaiveCoder | shell expansion syntax blocked by guard-bash.sh | tested | `tests/scenarios/claude-swe/test-hook-parametric.sh` |
| Claude | NaiveCoder | workspace control file reads blocked by guard-bash.sh | tested | `tests/scenarios/claude-swe/test-hook-parametric.sh` |
| Gemini | SWE | auth modes correctly parsed from gemini_env injection | tested | `tests/scenarios/gemini-auth-modes/test-auth-modes.py` |
| Gemini | SWE | inspect output schema contains required keys | tested | `tests/scenarios/cross-provider/test-inspect-parity.sh` |
| Gemini | SWE | audit log entries contain required fields | tested | `tests/scenarios/cross-provider/test-audit-log-schema.sh` |
| Gemini | SWE | trustedFolders.json seeded with /workspace before launch | doc-only | — |
| Gemini | Developer | Vertex env derives regional aiplatform allowlist entry | doc-only | — |
| Gemini | Developer | gcloud_adc supplemental only, not standalone Gemini auth | doc-only | — |
| Gemini | Developer | gemini_oauth credential copied to ~/.gemini/oauth_creds.json | doc-only | — |
| Gemini | DevRel | publish-pr creates signed commit and draft PR on host | doc-only | — |
| Gemini | DevRel | gemini_projects credential seeded into ~/.gemini/projects.json | doc-only | — |
| Gemini | PM | yolo autonomy mode launches with --approval-mode yolo | doc-only | — |
| Gemini | PM | prompt autonomy mode marked lower-assurance in audit output | aspirational | — |
| Gemini | NaiveCoder | workspace .gemini/ masked on safe path | doc-only | — |
| Gemini | NaiveCoder | nested coding-agent CLI launch blocked | doc-only | — |
| Gemini | NaiveCoder | breakglass restores Gemini folder-trust prompt, omits trustedFolders seed | doc-only | — |
| Gemini | NaiveCoder | host home not reachable from inside session | doc-only | — |

## Notes on test file paths

The scenario test files listed above are declared in
`tests/scenarios/manifest.json`. The test scripts themselves live under
`tests/scenarios/<scenario-id>/`. The manifest is the authoritative source of
truth for which scenarios are tracked.

For gaps and aspirational scenarios, see `docs/scenario-gaps.md`.
