# Scenario Test Coverage Gaps

This document lists known gaps in scenario test coverage. Each gap is marked
with the reason it is not yet tested and the path to add a test when the
constraint is lifted.

Scenarios marked `aspirational` in `docs/use-case-matrix.md` correspond to
entries here.

---

## Codex gaps

### codex-auth injection end-to-end

**Description:** Verify that a `codex_auth` injection policy entry correctly
places `auth.json` at `~/.codex/auth.json` inside the session and that Codex
CLI can authenticate against the OpenAI API.

**Why not tested yet:** Requires live Codex/OpenAI credentials. The secretless
CI lane cannot execute this without real auth material.

**Path to add:** Add a scenario under `tests/scenarios/codex-auth/` that
checks the injected file path inside the container using the provider-e2e
smoke harness (`scripts/provider-e2e.sh`). Gate it behind
`requires_credentials: true` in the manifest and wire it to the
`provider-e2e.yml` workflow dispatch lane.

### codex-rules-mutability session vs readonly

**Description:** Verify that `--codex-rules-mutability session` seeds a
writable rules copy and that the adapter baseline under `adapters/` is
unchanged after the session.

**Why not tested yet:** Requires a running container session. The current
secretless lane validates the static adapter files but does not run a full
session lifecycle.

**Path to add:** Add a scenario under `tests/scenarios/codex-rules/` that
mounts a test workspace, launches a minimal Codex session in readonly mode,
attempts an execpolicy amendment, and verifies the adapter baseline is
unmodified. Can run secretless if Codex is invoked with a fixture command that
does not require network auth.

---

## Claude gaps

### claude prompt autonomy audit field

**Description:** Verify that launching Claude with `--agent-autonomy prompt`
produces an audit log entry with
`autonomy_assurance=lower-assurance-prompt-autonomy`.

**Why not tested yet:** Requires a running container session that reaches the
audit log write path. The hook-parametric test covers the hook layer but not
the audit log schema for autonomy mode.

**Path to add:** Add a scenario under `tests/scenarios/claude-audit/` that
checks the audit log entry fields after a minimal prompt-autonomy session.
Can share the audit-log-schema test infrastructure already declared in the
manifest.

### claude MCP injection replaces empty template

**Description:** Verify that `credentials.claude_mcp` replaces the symlinked
empty `mcp-template.json` with the operator-provided file and that
`~/.mcp.json` inside the session reflects the injected content.

**Why not tested yet:** Requires a fixture MCP config and a running session
that can inspect the seeded home. The injection helper tests cover the
rendering step but not the final in-container file.

**Path to add:** Add a scenario under `tests/scenarios/claude-mcp/` using a
fixture `.mcp.json` with no live server entries. Run secretless.

---

## Gemini gaps

### gemini OAuth interactive login

**Description:** Verify that Gemini CLI completes the OAuth browser login flow
inside a managed Workcell session and that the resulting `oauth_creds.json` is
session-local only.

**Why not tested yet:** Requires a live browser interaction on the host.
There is no headless path for this flow. It cannot run in CI without a
self-hosted macOS runner with an active desktop session.

**Path to add:** Document as a manual validation step in
`docs/validation-scenarios.md`. A fully automated test is only feasible if
Gemini CLI exposes a headless OAuth code-exchange path.

### gemini GCA (Google Cloud Application Default Credentials)

**Description:** Verify that `GOOGLE_GENAI_USE_GCA=true` in `gemini_env`
correctly activates GCA mode and that the session can reach Gemini through
ADC without a separate API key.

**Why not tested yet:** Requires a live GCP project with ADC provisioned on
the self-hosted macOS runner. This is an infrastructure dependency that the
default CI lane cannot satisfy.

**Path to add:** Wire to the `provider-e2e.yml` workflow dispatch lane on the
self-hosted macOS path with `WORKCELL_E2E_GCLOUD_ADC_JSON` as the environment
secret. Add a scenario under `tests/scenarios/gemini-gca/` gated behind
`requires_credentials: true`.

### gemini Vertex regional allowlist expansion

**Description:** Verify that a `gemini_env` file containing
`GOOGLE_CLOUD_LOCATION=us-central1` causes Workcell to add
`us-central1-aiplatform.googleapis.com:443` to the active egress allowlist
before launch.

**Why not tested yet:** The injection helper unit tests cover env-file parsing.
The allowlist expansion step happens in the host launcher and requires a
running Colima VM to observe the resulting iptables rules. This is a macOS
local or self-hosted test.

**Path to add:** Add an invariant check to `scripts/verify-invariants.sh` that
parses the launcher output for a Vertex-env fixture and confirms the derived
allowlist entry. No live network required; the check is on the derived
hostname, not the live endpoint.

---

## Cross-provider gaps

### Full VM boundary test

**Description:** Verify that the Colima VM plus container boundary prevents the
agent from reading host home directories, keychains, and credential paths.

**Why not tested yet:** Requires Colima and the Virtualization.Framework on a
macOS host. This test cannot run on GitHub-hosted ubuntu runners.

**Path to add:** The `macos-boundary.yml` workflow (`workflows/macos-boundary.yml`)
is the designated lane for this test on a self-hosted macOS runner. Extend it
with an explicit host-secret-isolation scenario once a self-hosted macOS runner
is provisioned with `WORKCELL_ENABLE_SELF_HOSTED_MACOS=true`.

### breakglass mode audit and posture

**Description:** Verify that a `breakglass` session produces an audit log entry
recording the explicit acknowledgement and that the session posture fields
reflect the lower-assurance downgrade.

**Why not tested yet:** `breakglass` requires `--ack-breakglass` and a running
Colima VM. It is intentionally local macOS only; running it in default CI would
require the full VM boundary.

**Path to add:** Add a manual scenario under `tests/scenarios/breakglass/`
with a fixture `--ack-breakglass` launch that checks the audit log fields.
Gate it behind a local-only flag so it does not run in the default CI lane.
Document it in `docs/validation-scenarios.md` under "Out Of Scope And
Breakglass".

### Live provider end-to-end for all three providers

**Description:** Full round-trip test: inject real credentials, prepare image,
launch provider inside Workcell, run a minimal authenticated probe, verify
`WORKCELL_PROVIDER_E2E_OK` in output.

**Why not tested yet:** Requires live provider credentials. The
`provider-e2e.yml` workflow is `workflow_dispatch` only and requires a
self-hosted macOS runner with environment-scoped secrets.

**Path to add:** All three providers are already scaffolded in
`scripts/provider-e2e.sh`. Wire each to a scenario under
`tests/scenarios/<provider>-e2e/` gated behind `requires_credentials: true`
and `workflow_dispatch` in `provider-e2e.yml`. This is the lane described in
`docs/validation-scenarios.md`.
