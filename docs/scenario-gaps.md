# Scenario Test Coverage Gaps

This file tracks the main places where Workcell is documented or designed but
not yet covered as deeply as the core secretless validation path.

## Highest-value gaps

### Full macOS boundary proof

The strongest local claim depends on the actual Colima plus
Virtualization.Framework boundary. GitHub-hosted runners cannot prove that, so
the best evidence today is local or self-hosted macOS verification.

### End-to-end authenticated coverage for every provider

Provider-authenticated smoke (`scripts/provider-e2e.sh`) is available for
local use, but it requires a real macOS+Colima environment and live provider
credentials and is not run in CI. Not every documented auth path has full
automated end-to-end coverage across all providers.

### Lower-assurance transition coverage

Some downgrade paths are validated statically or by smoke tests but still need
more explicit end-to-end scenarios, especially:

- prompt autonomy audit fields
- package-mutation downgrade behavior
- `breakglass` posture and audit output

## Provider-specific gaps

### Codex

- more end-to-end coverage around rule mutability transitions
- more explicit live coverage of host-side publication handoff

### Claude

- deeper authenticated coverage for imported MCP state and prompt-autonomy
  downgrade labeling

### Gemini

- more end-to-end coverage for Gemini OAuth reuse
- more end-to-end coverage for Vertex plus `gcloud_adc`
- more coverage for `breakglass` folder-trust restoration

## Why these gaps remain acceptable for now

The core secretless path is covered by invariants, smoke tests, repo
validation, reproducibility checks, and tagged release preflight. The open
gaps are mostly at the edges: live-provider auth, local macOS proof, and
explicit lower-assurance transitions.
