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
| Agent identity and authentication | IETF (individual drafts; no adopted working group yet) | Multiple active individual drafts building on OAuth 2.0, SPIFFE, and WIMSE rather than a new protocol; no formal IETF working group as of this review | <https://datatracker.ietf.org/> | Bears on how Workcell would attest and authenticate an agent's actions beyond human OAuth; informs ASI03 posture | formation of an IETF working group; convergence of the competing drafts; adoption of OAuth extensions or WIMSE for agent-to-service auth |

## Notes

- The OWASP entry is the authoritative input to Workcell's agentic-security
  control mapping; keep it and [owasp-agentic-mapping.md](owasp-agentic-mapping.md)
  in step when the edition changes.
- The agent-identity line is deliberately conservative: the space is still
  pre-standardization (competing IETF drafts, no adopted working group), so
  Workcell tracks it without committing to any one draft.
- Version numbers and dates above reflect the last-reviewed date; confirm the
  canonical sources at each review rather than trusting this snapshot.
