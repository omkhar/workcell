# Scenario test coverage gaps

This page lists shipped interfaces that have less test coverage than the core secretless path.

## Highest-priority gaps

### Full macOS boundary proof

The macOS Colima runtime boundary uses Colima and Virtualization.framework.
GitHub-hosted runners cannot prove this boundary.
The local certification lane supplies the current evidence:

```bash
./scripts/run-scenario-tests.sh --secretless-only --certification-only
```

### Authenticated tests for each provider

`scripts/provider-e2e.sh` runs authenticated provider tests.
It requires an Apple Silicon macOS host, Colima, and live provider credentials.
CI does not run this path.
Some documented authentication paths do not have complete live tests.

### Lower-assurance transitions

No end-to-end scenario does these checks:

- Start a prompt-autonomy session and verify its final audit fields.
- Change packages in a mutable container and verify the final assurance record.
- Start a `breakglass` session and verify its posture and audit output.

### Changed-file planner

The changed-file planner has these open hardening tasks:

- Harden JSON temporary state.
- Move the later `go run` cache path into the controlled run root.

## Provider gaps

### Codex

- No end-to-end scenario changes rule mutability and verifies the final audit record.
- No live scenario completes host publication from a managed session.

### Claude

- No authenticated scenario imports MCP state and verifies runtime state.
- No end-to-end scenario changes prompt autonomy and verifies its audit label.

### Gemini

- No end-to-end scenario reuses cached Gemini OAuth and completes a provider request.
- No end-to-end scenario combines Vertex credentials with `gcloud_adc` and completes a provider request.
- No end-to-end scenario verifies folder-trust restoration after `breakglass`.

### Copilot and Antigravity

- Copilot live staged-token certification remains outside repo-required CI.
- Antigravity has no supported adapter, authentication input, bootstrap path,
  control plane, scenario evidence, or live certification.
- Complete Antigravity live certification before any support claim.

## Current evidence boundary

Repository invariants, smoke tests, deterministic scenarios, reproducibility checks,
and release preflight cover the core path.
The remaining gaps apply to live provider authentication, local macOS proof,
lower-assurance changes, and planner hardening.
Copilot has deterministic adapter evidence and a live certification gate.
Antigravity remains unsupported.
