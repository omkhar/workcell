# Standards Watchlist

Workcell tracks a small set of external standards that shape how coding agents
are configured, secured, and identified. This is a living reference: it records
each standard's current status and what to watch, so the project's controls and
docs stay aligned as the standards move.

- **Owner:** the maintainer (see [MAINTAINERS.md](../MAINTAINERS.md)).
- **Review cadence:** quarterly, or sooner when a tracked standard publishes a
  new version.
- **Last reviewed:** 2026-07-04.
- **Next review due:** 2026-10-04.

## Watchlist

| Standard | Body | Status at last review | Canonical source | Why it matters to Workcell | What to watch |
|---|---|---|---|---|---|
| Model Context Protocol (MCP) | Agentic AI Foundation (Linux Foundation); originated at Anthropic | Date-revisioned spec (e.g. `2025-11-25`); donated to the Linux Foundation's Agentic AI Foundation in December 2025 | <https://modelcontextprotocol.io/specification> | Workcell injects MCP server/config surfaces for providers; auth and transport changes affect what the runtime must contain and validate | new spec revisions; OAuth/OIDC auth alignment; transport changes; the SEP (Specification Enhancement Proposal) process and any working-group split |
| OWASP Top 10 for Agentic Applications | OWASP GenAI Security Initiative | 2026 edition (ASI01–ASI10), released December 2025 | <https://genai.owasp.org/resource/owasp-top-10-for-agentic-applications-for-2026/> | Workcell's control coverage is mapped against it in [owasp-agentic-mapping.md](owasp-agentic-mapping.md); a new edition would change that mapping | the next edition and its cadence; changes to ASI03 (agent identity); divergence from the separate LLM Top 10 |
| Agent identity and authentication | IETF WIMSE working group, plus individual drafts | Active work in the IETF **WIMSE** WG (Workload Identity in Multi-System Environments), which covers workload/agent identity and carries agent-specific drafts (e.g. WIMSE applicability to AI agents, cross-organizational delegation); complemented by individual drafts building on OAuth 2.0, SPIFFE, and WIMSE. A dedicated agent-identity protocol is still emerging. | <https://datatracker.ietf.org/wg/wimse/> | Bears on how Workcell would attest and authenticate an agent's actions beyond human OAuth; informs ASI03 posture | WIMSE WG documents (esp. the AI-agent applicability and delegation drafts) advancing toward RFC; adoption of OAuth extensions for agent-to-service auth |

## Notes

- The OWASP entry is the authoritative input to Workcell's agentic-security
  control mapping; keep it and [owasp-agentic-mapping.md](owasp-agentic-mapping.md)
  in step when the edition changes.
- The agent-identity line is deliberately conservative: the active venue is the
  IETF WIMSE working group, but a dedicated agent-identity protocol is still
  emerging (multiple drafts building on OAuth 2.0, SPIFFE, and WIMSE), so
  Workcell tracks WIMSE without committing to any one draft.
- Version numbers and dates above reflect the last-reviewed date; confirm the
  canonical sources at each review rather than trusting this snapshot.
